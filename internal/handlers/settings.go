package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func handleSet(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "thr":
		return setThrMenu(env, cq)
	case "thrset":
		return setThrAsk(env, cq, parts)
	case "quiet":
		return setQuiet(env, cq, parts)
	case "digest":
		return setDigest(env, cq)
	case "wl":
		return setWLMenu(env, cq)
	case "wladd":
		return setWLAdd(env, cq)
	case "ask":
		return setAsk(env, cq, parts)
	case "do":
		return setDo(env, cq, parts)
	}
	return setMenu(env, cq)
}

func handleSetText(env *Env, msg *gotgbot.Message, parts []string) error {
	if len(parts) < 2 || msg.From == nil {
		return nil
	}
	text := strings.TrimSpace(msg.GetText())
	switch parts[1] {
	case "thr":
		if len(parts) < 3 {
			return nil
		}
		return setThrText(env, msg, parts[2], text)
	case "wladd":
		return setWLAddText(env, msg, text)
	}
	return nil
}

func onOff(v bool) string {
	if v {
		return "🟢 вкл"
	}
	return "🔴 выкл"
}

func cfgSave(env *Env, fn func()) error {
	env.CfgMu.Lock()
	defer env.CfgMu.Unlock()
	fn()
	return env.SaveConfig()
}

func reportSaveErr(env *Env, cq *gotgbot.CallbackQuery, err error, backTo string) {
	env.Log.Printf("FAIL сохранить конфиг: %v", err)
	_ = Edit(env, cq,
		fmt.Sprintf("⚠️ Не удалось сохранить настройки:\n<code>%s</code>", Esc(err.Error())),
		[][]gotgbot.InlineKeyboardButton{BackRow(backTo)})
}

func denyNotSuper(env *Env, cq *gotgbot.CallbackQuery) bool {
	if env.Sec.IsSuper(cq.From.Id) {
		return false
	}
	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "Только для суперадмина", ShowAlert: true})
	return true
}

func setMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	env.CfgMu.RLock()
	thr := env.Cfg.Thresholds
	q := env.Cfg.QuietHours
	d := env.Cfg.Digest
	env.CfgMu.RUnlock()
	var b strings.Builder
	b.WriteString("<b>⚙️ Настройки</b>\n")
	fmt.Fprintf(&b, "<b>Пороги алертов:</b> CPU %v%% · RAM %v%% · Диск %v%%\n",
		thr.CPUPercent, thr.RAMPercent, thr.DiskPercent)
	fmt.Fprintf(&b, "<b>Тихие часы:</b> %s · %s–%s\n", onOff(q.Enabled), Esc(q.Start), Esc(q.End))
	fmt.Fprintf(&b, "<b>Дайджест:</b> %s · %s", onOff(d.Enabled), Esc(d.Time))
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🎯 Пороги алертов", "set:thr")),
		Row(Btn("🌙 Тихие часы", "set:quiet")),
		Row(Btn("📅 Дайджест", "set:digest")),
		Row(Btn("👥 Whitelist", "set:wl")),
		BackRow("menu:main"),
	}
	return Edit(env, cq, b.String(), kb)
}

func setThrMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	env.CfgMu.RLock()
	thr := env.Cfg.Thresholds
	env.CfgMu.RUnlock()
	text := fmt.Sprintf("<b>🎯 Пороги алертов</b>\nТекущие: CPU %v%% · RAM %v%% · Диск %v%%\n\nВыберите порог, затем пришлите новое значение.",
		thr.CPUPercent, thr.RAMPercent, thr.DiskPercent)
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("CPU %", "set:thrset:cpu"), Btn("RAM %", "set:thrset:ram"), Btn("Диск %", "set:thrset:disk")),
		BackRow("set:menu"),
	}
	return Edit(env, cq, text, kb)
}

func setThrAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) < 3 {
		return setThrMenu(env, cq)
	}
	var name string
	switch parts[2] {
	case "cpu":
		name = "CPU"
	case "ram":
		name = "RAM"
	case "disk":
		name = "диска"
	default:
		return setThrMenu(env, cq)
	}
	env.Pending.Set(cq.From.Id, "set:thr:"+parts[2])
	return Edit(env, cq,
		fmt.Sprintf("✏️ Пришлите новый порог для %s — число от 1 до 100 (можно дробное).", name),
		[][]gotgbot.InlineKeyboardButton{BackRow("set:thr")})
}

func setThrText(env *Env, msg *gotgbot.Message, field, text string) error {
	var name string
	var apply func(v float64)
	switch field {
	case "cpu":
		name = "CPU"
		apply = func(v float64) { env.Cfg.Thresholds.CPUPercent = v }
	case "ram":
		name = "RAM"
		apply = func(v float64) { env.Cfg.Thresholds.RAMPercent = v }
	case "disk":
		name = "диска"
		apply = func(v float64) { env.Cfg.Thresholds.DiskPercent = v }
	default:
		return nil
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(text, ",", "."), 64)
	if err != nil || v < 1 || v > 100 {

		env.Pending.Set(msg.From.Id, "set:thr:"+field)
		_, err = SendHTML(env, msg.Chat.Id,
			"⚠️ Нужно число от 1 до 100 (можно дробное). Попробуйте ещё раз.",
			[][]gotgbot.InlineKeyboardButton{BackRow("set:menu")})
		return err
	}
	if err := cfgSave(env, func() { apply(v) }); err != nil {
		env.Log.Printf("FAIL сохранить конфиг: %v", err)
		_, err2 := SendHTML(env, msg.Chat.Id,
			fmt.Sprintf("⚠️ Не удалось сохранить настройки:\n<code>%s</code>", Esc(err.Error())),
			[][]gotgbot.InlineKeyboardButton{BackRow("set:menu")})
		return err2
	}
	_, err = SendHTML(env, msg.Chat.Id,
		fmt.Sprintf("✅ Порог %s установлен: %v%%", name, v),
		[][]gotgbot.InlineKeyboardButton{BackRow("set:menu")})
	return err
}

func setQuiet(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) > 2 {
		switch parts[2] {
		case "on", "off":
			on := parts[2] == "on"
			if err := cfgSave(env, func() { env.Cfg.QuietHours.Enabled = on }); err != nil {
				reportSaveErr(env, cq, err, "set:quiet")
				return nil
			}
		case "p":

			if len(parts) > 3 {
				start, end, ok := parseQuietPreset(parts[3])
				if !ok {
					return setQuietMenu(env, cq)
				}
				if err := cfgSave(env, func() {
					env.Cfg.QuietHours.Start = start
					env.Cfg.QuietHours.End = end
					env.Cfg.QuietHours.Enabled = true
				}); err != nil {
					reportSaveErr(env, cq, err, "set:quiet")
					return nil
				}
			}
		}
	}
	return setQuietMenu(env, cq)
}

func setQuietMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	env.CfgMu.RLock()
	q := env.Cfg.QuietHours
	env.CfgMu.RUnlock()
	text := fmt.Sprintf("<b>🌙 Тихие часы</b>\nСтатус: %s\nИнтервал: %s–%s\n\nВ тихие часы приходят только критические алерты.",
		onOff(q.Enabled), Esc(q.Start), Esc(q.End))
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🟢 Вкл", "set:quiet:on"), Btn("🔴 Выкл", "set:quiet:off")),
		Row(Btn("00:00–08:00", "set:quiet:p:0000-0800")),
		Row(Btn("22:00–07:00", "set:quiet:p:2200-0700")),
		Row(Btn("23:00–06:00", "set:quiet:p:2300-0600")),
		BackRow("set:menu"),
	}
	return Edit(env, cq, text, kb)
}

func parseQuietPreset(s string) (start, end string, ok bool) {
	hhmm := func(v string) (string, bool) {
		if len(v) != 4 {
			return "", false
		}
		n, err := strconv.Atoi(v)
		if err != nil || n/100 > 23 || n%100 > 59 {
			return "", false
		}
		return fmt.Sprintf("%02d:%02d", n/100, n%100), true
	}
	a, b, found := strings.Cut(s, "-")
	if !found {
		return "", "", false
	}
	start, ok1 := hhmm(a)
	end, ok2 := hhmm(b)
	if !ok1 || !ok2 {
		return "", "", false
	}
	return start, end, true
}

func setDigest(env *Env, cq *gotgbot.CallbackQuery) error {
	if err := cfgSave(env, func() { env.Cfg.Digest.Enabled = !env.Cfg.Digest.Enabled }); err != nil {
		reportSaveErr(env, cq, err, "set:menu")
		return nil
	}
	return setMenu(env, cq)
}

func setWLMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	if denyNotSuper(env, cq) {
		return nil
	}
	ids := env.Sec.List()
	var b strings.Builder
	b.WriteString("<b>👥 Whitelist</b>\nРазрешённые Telegram ID:\n")
	for i, id := range ids {
		if i == 0 {
			fmt.Fprintf(&b, "· <code>%d</code> 👑\n", id)
		} else {
			fmt.Fprintf(&b, "· <code>%d</code>\n", id)
		}
	}
	kb := [][]gotgbot.InlineKeyboardButton{Row(Btn("➕ Добавить ID", "set:wladd"))}

	for i, id := range ids {
		if i == 0 {
			continue
		}
		kb = append(kb, Row(Btn(fmt.Sprintf("❌ %d", id), fmt.Sprintf("set:ask:wldel:%d", id))))
	}
	kb = append(kb, BackRow("set:menu"))
	return Edit(env, cq, b.String(), kb)
}

func setWLAdd(env *Env, cq *gotgbot.CallbackQuery) error {
	if denyNotSuper(env, cq) {
		return nil
	}
	env.Pending.Set(cq.From.Id, "set:wladd")
	return Edit(env, cq,
		"✏️ Пришлите Telegram ID пользователя, которого нужно добавить в whitelist (положительное число).",
		[][]gotgbot.InlineKeyboardButton{BackRow("set:wl")})
}

func setAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if denyNotSuper(env, cq) {
		return nil
	}
	if len(parts) > 3 && parts[2] == "wldel" {
		id := parts[3]
		return Edit(env, cq,
			fmt.Sprintf("⚠️ Удалить ID <code>%s</code> из whitelist?\nПользователь сразу потеряет доступ к боту.", Esc(id)),
			ConfirmKB("set:do:wldel:"+id, "set:wl"))
	}
	return setMenu(env, cq)
}

func setDo(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if denyNotSuper(env, cq) {
		return nil
	}
	if len(parts) > 3 && parts[2] == "wldel" {
		id, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return setWLMenu(env, cq)
		}
		env.Audit.Log(cq.From.Id, "whitelist: удаление ID "+parts[3])
		list, ok := env.Sec.Remove(id)
		if !ok {

			return setWLMenu(env, cq)
		}
		if err := cfgSave(env, func() { env.Cfg.AllowedUsers = list }); err != nil {
			reportSaveErr(env, cq, err, "set:wl")
			return nil
		}
		return setWLMenu(env, cq)
	}
	return setMenu(env, cq)
}

func setWLAddText(env *Env, msg *gotgbot.Message, text string) error {
	reply := func(t string) error {
		_, err := SendHTML(env, msg.Chat.Id, t,
			[][]gotgbot.InlineKeyboardButton{BackRow("set:menu")})
		return err
	}
	if !env.Sec.IsSuper(msg.From.Id) {
		return reply("⛔ Только для суперадмина.")
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id <= 0 {

		env.Pending.Set(msg.From.Id, "set:wladd")
		return reply("⚠️ Нужен положительный числовой Telegram ID. Попробуйте ещё раз.")
	}
	list, added := env.Sec.Add(id)
	if !added {
		return reply(fmt.Sprintf("ℹ️ ID <code>%d</code> уже есть в whitelist.", id))
	}
	env.Audit.Log(msg.From.Id, fmt.Sprintf("whitelist: добавлен ID %d", id))
	if err := cfgSave(env, func() { env.Cfg.AllowedUsers = list }); err != nil {
		env.Log.Printf("FAIL сохранить конфиг: %v", err)
		return reply(fmt.Sprintf("⚠️ Не удалось сохранить настройки:\n<code>%s</code>", Esc(err.Error())))
	}
	return reply(fmt.Sprintf("✅ ID <code>%d</code> добавлен в whitelist.", id))
}
