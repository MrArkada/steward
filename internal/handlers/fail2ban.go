package handlers

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/sysutil"
)

const f2bJailLocal = "/etc/fail2ban/jail.local"

const f2bMaxShow = 20

type bannedIP struct {
	jail string
	ip   string
}

func handleF2B(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		return f2bMenu(env, cq)

	case "install":
		return f2bInstall(env, cq)

	case "status":
		return f2bStatus(env, cq)

	case "banned":
		return f2bBanned(env, cq)

	case "unbanlist":
		return f2bUnbanList(env, cq)

	case "ban":

		env.Pending.Set(cq.From.Id, "f2b:ban")
		return Edit(env, cq,
			"➕ Пришлите IP-адрес сообщением — забаню его в jail <code>sshd</code>.\n\nПример: <code>203.0.113.10</code>",
			[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})

	case "wl":

		env.Pending.Set(cq.From.Id, "f2b:wlmanual")
		return Edit(env, cq,
			"⭕ Пришлите IP-адрес сообщением — добавлю его в <code>ignoreip</code> (whitelist) Fail2ban.",
			[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})

	case "ask":
		if len(parts) < 3 {
			break
		}
		switch parts[2] {
		case "unban":
			if len(parts) < 4 {
				break
			}
			ip := parts[3]
			return Edit(env, cq,
				fmt.Sprintf("⚠️ Разбанить <code>%s</code> во всех jail'ах?", Esc(ip)),
				ConfirmKB("f2b:do:unban:"+ip, "f2b:unbanlist"))
		case "restart":
			return Edit(env, cq,
				"⚠️ Перезапустить службу <b>fail2ban</b>?\nАктивные баны сохранятся.",
				ConfirmKB("f2b:do:restart", "f2b:menu"))
		}

	case "do":
		if len(parts) < 3 {
			break
		}
		switch parts[2] {
		case "unban":
			if len(parts) < 4 {
				break
			}
			ip := parts[3]
			env.Audit.Log(cq.From.Id, "fail2ban: разбан "+ip)
			if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "unban", ip); err != nil {
				Fail(env, cq, "разбанить "+ip, err, "f2b:unbanlist")
				return nil
			}
			return Edit(env, cq,
				fmt.Sprintf("✅ IP <code>%s</code> разбанен.", Esc(ip)),
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:banned")})
		case "restart":
			env.Audit.Log(cq.From.Id, "fail2ban: перезапуск службы")
			if _, err := sysutil.ServiceAction(env.RootCtx, "restart", "fail2ban"); err != nil {
				Fail(env, cq, "перезапустить fail2ban", err, "f2b:menu")
				return nil
			}

			state := f2bWaitActive(env.RootCtx, 8*time.Second)
			if state != "active" {
				return f2bDown(env, cq, state)
			}
			return f2bMenu(env, cq)
		}
	}
	return f2bMenu(env, cq)
}

func handleF2BText(env *Env, msg *gotgbot.Message, parts []string) error {
	if len(parts) < 2 {
		return nil
	}
	text := strings.TrimSpace(msg.GetText())
	switch parts[1] {
	case "ban":
		ip := net.ParseIP(text)
		if ip == nil {
			_, err := SendHTML(env, msg.Chat.Id,
				"⚠️ Это не похоже на IP-адрес: <code>"+Esc(Trunc(text, 64))+"</code>\nОткройте раздел Fail2ban и попробуйте ещё раз.",
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
			return err
		}
		ipStr := ip.String()
		env.Audit.Log(msg.From.Id, "fail2ban: ручной бан "+ipStr)
		if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "set", "sshd", "banip", ipStr); err != nil {
			env.Log.Printf("FAIL забанить %s: %v", ipStr, err)
			_, err2 := SendHTML(env, msg.Chat.Id,
				fmt.Sprintf("⚠️ Не удалось забанить <code>%s</code>:\n<code>%s</code>", Esc(ipStr), Esc(failShort(err))),
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
			return err2
		}
		_, err := SendHTML(env, msg.Chat.Id,
			fmt.Sprintf("✅ IP <code>%s</code> забанен в jail <code>sshd</code>.", Esc(ipStr)),
			[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
		return err

	case "wlmanual":
		ip := net.ParseIP(text)
		if ip == nil {
			_, err := SendHTML(env, msg.Chat.Id,
				"⚠️ Это не похоже на IP-адрес: <code>"+Esc(Trunc(text, 64))+"</code>\nОткройте раздел Fail2ban и попробуйте ещё раз.",
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
			return err
		}
		ipStr := ip.String()
		env.Audit.Log(msg.From.Id, "fail2ban: whitelist "+ipStr)
		if err := f2bAddIgnoreIP(ipStr); err != nil {
			env.Log.Printf("FAIL whitelist %s: %v", ipStr, err)
			_, err2 := SendHTML(env, msg.Chat.Id,
				"⚠️ Не удалось обновить <code>"+f2bJailLocal+"</code>:\n<code>"+Esc(Trunc(err.Error(), 200))+"</code>",
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
			return err2
		}
		if _, err := sysutil.ServiceAction(env.RootCtx, "restart", "fail2ban"); err != nil {
			env.Log.Printf("FAIL перезапуск fail2ban: %v", err)
			_, err2 := SendHTML(env, msg.Chat.Id,
				fmt.Sprintf("⚠️ IP <code>%s</code> добавлен в <code>ignoreip</code>, но перезапустить fail2ban не удалось:\n<code>%s</code>\nПерезапустите службу вручную из меню.",
					Esc(ipStr), Esc(failShort(err))),
				[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
			return err2
		}
		_, err := SendHTML(env, msg.Chat.Id,
			fmt.Sprintf("✅ IP <code>%s</code> добавлен в <code>ignoreip</code>, fail2ban перезапущен.", Esc(ipStr)),
			[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
		return err
	}
	return nil
}

func f2bMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	if !sysutil.Exists("fail2ban-client") {
		return Edit(env, cq,
			"<b>🛡 Fail2ban</b>\n\nFail2ban не установлен на этом сервере.",
			[][]gotgbot.InlineKeyboardButton{
				Row(Btn("📦 Установить и настроить", "f2b:install")),
				BackRow("menu:main"),
			})
	}
	state := sysutil.ServiceState(env.RootCtx, "fail2ban")
	jailsStr := "—"
	if state == "active" {
		if jails, err := f2bJails(env); err == nil && len(jails) > 0 {
			jailsStr = strings.Join(jails, ", ")
		}
	}
	text := fmt.Sprintf("<b>🛡 Fail2ban</b>\nСлужба: <code>%s</code>\nJail'ы: <code>%s</code>",
		Esc(state), Esc(jailsStr))
	if state != "active" {
		text += "\n\n⚠️ Служба не активна — баны и статистика недоступны, пока она не запущена."
	}
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("📋 Статус", "f2b:status"), Btn("🚫 Забаненные IP", "f2b:banned")),
		Row(Btn("🔓 Разбанить IP", "f2b:unbanlist"), Btn("➕ Забанить вручную", "f2b:ban")),
		Row(Btn("⭕ В whitelist (ignoreip)", "f2b:wl")),
		Row(Btn("🔄 Перезапустить", "f2b:ask:restart")),
		BackRow("menu:main"),
	}
	return Edit(env, cq, text, kb)
}

func f2bInstall(env *Env, cq *gotgbot.CallbackQuery) error {
	if !env.OS.HasPM() {
		return Unsupported(env, cq, "Fail2ban (автоустановка)")
	}
	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Выполняю, это может занять минуту..."})
	_ = Edit(env, cq, "⏳ Устанавливаю и настраиваю Fail2ban…", [][]gotgbot.InlineKeyboardButton{})

	if _, err := sysutil.PMInstall(env.RootCtx, env.OS.PM, "fail2ban"); err != nil {
		Fail(env, cq, "установить пакет fail2ban", err, "f2b:menu")
		return nil
	}
	if err := f2bWriteJailLocal(); err != nil {
		Fail(env, cq, "записать "+f2bJailLocal, err, "f2b:menu")
		return nil
	}
	if _, err := sysutil.Systemctl(env.RootCtx, "enable", "--now", "fail2ban"); err != nil {
		Fail(env, cq, "включить и запустить fail2ban", err, "f2b:menu")
		return nil
	}
	env.Audit.Log(cq.From.Id, "fail2ban: установка и настройка")

	state := f2bWaitActive(env.RootCtx, 8*time.Second)
	if state != "active" {
		return Edit(env, cq,
			fmt.Sprintf("⚠️ <b>Fail2ban установлен, но служба не запустилась</b> (состояние: <code>%s</code>).\n\n"+
				"Посмотрите причину: <code>journalctl -u fail2ban -n 50</code> — или попробуйте перезапустить.", Esc(state)),
			[][]gotgbot.InlineKeyboardButton{
				Row(Btn("🔄 Перезапустить fail2ban", "f2b:ask:restart")),
				BackRow("f2b:menu"),
			})
	}
	return Edit(env, cq,
		"✅ <b>Fail2ban установлен и запущен.</b>\n\n"+
			"Создан <code>"+f2bJailLocal+"</code>:\n"+
			"• bantime 1h, findtime 10m, maxretry 5\n"+
			"• включён jail <code>sshd</code>\n\n"+
			"Прежний конфиг, если был, сохранён в <code>jail.local.bak</code>.",
		[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
}

func f2bStatus(env *Env, cq *gotgbot.CallbackQuery) error {

	if state := sysutil.ServiceState(env.RootCtx, "fail2ban"); state != "active" {
		return f2bDown(env, cq, state)
	}
	out, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "status")
	if err != nil {

		if ce, ok := sysErr(err); ok && strings.Contains(ce.Output, "socket") {
			return f2bDown(env, cq, "socket недоступен")
		}
		Fail(env, cq, "получить статус fail2ban", err, "f2b:menu")
		return nil
	}
	var b strings.Builder
	b.WriteString("<b>📋 Статус Fail2ban</b>\n<pre>")
	b.WriteString(Esc(strings.TrimSpace(out)))
	for _, jail := range parseJailList(out) {
		jout, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "status", jail)
		if err != nil {
			env.Log.Printf("f2b status %s: %v", jail, err)
			continue
		}
		cur, total := f2bJailSummary(jout)
		fmt.Fprintf(&b, "\n%s: сейчас забанено %s, всего %s", Esc(jail), Esc(cur), Esc(total))
	}
	b.WriteString("</pre>")
	return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
}

func f2bBanned(env *Env, cq *gotgbot.CallbackQuery) error {
	if state := sysutil.ServiceState(env.RootCtx, "fail2ban"); state != "active" {
		return f2bDown(env, cq, state)
	}
	list := f2bAllBanned(env)
	var b strings.Builder
	b.WriteString("<b>🚫 Забаненные IP</b>\n")
	if len(list) == 0 {
		b.WriteString("Сейчас никто не забанен.")
	} else {
		for i, item := range list {
			if i >= f2bMaxShow {
				fmt.Fprintf(&b, "… и ещё %d\n", len(list)-f2bMaxShow)
				break
			}
			geo := env.Geo.Lookup(env.RootCtx, item.ip)
			if geo != "" {
				fmt.Fprintf(&b, "<code>%s</code> (%s) — %s\n", Esc(item.ip), Esc(geo), Esc(item.jail))
			} else {
				fmt.Fprintf(&b, "<code>%s</code> — %s\n", Esc(item.ip), Esc(item.jail))
			}
		}
	}
	return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
}

func f2bUnbanList(env *Env, cq *gotgbot.CallbackQuery) error {
	if state := sysutil.ServiceState(env.RootCtx, "fail2ban"); state != "active" {
		return f2bDown(env, cq, state)
	}
	list := f2bAllBanned(env)
	if len(list) == 0 {
		return Edit(env, cq, "🔓 Сейчас никто не забанен.",
			[][]gotgbot.InlineKeyboardButton{BackRow("f2b:menu")})
	}
	var b strings.Builder
	b.WriteString("<b>🔓 Разбанить IP</b>\nВыберите адрес:")
	kb := [][]gotgbot.InlineKeyboardButton{}
	for i, item := range list {
		if i >= f2bMaxShow {
			fmt.Fprintf(&b, "\n… и ещё %d (показаны первые %d)", len(list)-f2bMaxShow, f2bMaxShow)
			break
		}
		kb = append(kb, Row(Btn("🔓 "+item.ip, "f2b:ask:unban:"+item.ip)))
	}
	kb = append(kb, BackRow("f2b:banned"))
	return Edit(env, cq, b.String(), kb)
}

func f2bJails(env *Env) ([]string, error) {
	out, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "status")
	if err != nil {
		return nil, err
	}
	return parseJailList(out), nil
}

func parseJailList(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, "Jail list:")
		if idx < 0 {
			continue
		}
		var jails []string
		for _, j := range strings.Split(line[idx+len("Jail list:"):], ",") {
			if j = strings.TrimSpace(j); j != "" {
				jails = append(jails, j)
			}
		}
		return jails
	}
	return nil
}

func parseBannedIPs(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, "Banned IP list:")
		if idx < 0 {
			continue
		}
		return strings.Fields(line[idx+len("Banned IP list:"):])
	}
	return nil
}

func f2bJailSummary(out string) (cur, total string) {
	cur, total = "?", "?"
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, "Currently banned:"); idx >= 0 {
			cur = strings.TrimSpace(line[idx+len("Currently banned:"):])
		}
		if idx := strings.Index(line, "Total banned:"); idx >= 0 {
			total = strings.TrimSpace(line[idx+len("Total banned:"):])
		}
	}
	return cur, total
}

func f2bAllBanned(env *Env) []bannedIP {
	jails, err := f2bJails(env)
	if err != nil {
		return nil
	}
	var out []bannedIP
	for _, jail := range jails {
		jout, err := sysutil.Run(env.RootCtx, 10*time.Second, "fail2ban-client", "status", jail)
		if err != nil {
			env.Log.Printf("f2b status %s: %v", jail, err)
			continue
		}
		for _, ip := range parseBannedIPs(jout) {
			out = append(out, bannedIP{jail: jail, ip: ip})
		}
	}
	return out
}

func f2bWriteJailLocal() error {
	return sysutil.WriteFail2banJail()
}

func f2bAddIgnoreIP(ip string) error {
	data, err := os.ReadFile(f2bJailLocal)
	if err != nil {
		return fmt.Errorf("не удалось прочитать %s: %w", f2bJailLocal, err)
	}
	lines := strings.Split(string(data), "\n")
	inDefault := false
	done := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			inDefault = trim == "[DEFAULT]"
			continue
		}
		if inDefault && !done && strings.HasPrefix(trim, "ignoreip") {

			if !strings.Contains(line, ip) {
				lines[i] = line + " " + ip
			}
			done = true
		}
	}
	if !done {

		inserted := false
		for i, line := range lines {
			if strings.TrimSpace(line) == "[DEFAULT]" {
				rest := append([]string{"ignoreip = 127.0.0.1/8 ::1 " + ip}, lines[i+1:]...)
				lines = append(lines[:i+1], rest...)
				inserted = true
				break
			}
		}
		if !inserted {

			lines = append([]string{"[DEFAULT]", "ignoreip = 127.0.0.1/8 ::1 " + ip, ""}, lines...)
		}
	}
	return os.WriteFile(f2bJailLocal, []byte(strings.Join(lines, "\n")), 0o644)
}

func f2bDown(env *Env, cq *gotgbot.CallbackQuery, state string) error {
	return Edit(env, cq,
		fmt.Sprintf("⚠️ <b>Служба fail2ban не запущена</b> (состояние: <code>%s</code>).\n\n"+
			"Команды fail2ban-client работают только при активной службе. "+
			"Если после перезапуска снова упала — причина в логе: <code>journalctl -u fail2ban -n 50</code>.", Esc(state)),
		[][]gotgbot.InlineKeyboardButton{
			Row(Btn("🔄 Перезапустить fail2ban", "f2b:ask:restart")),
			BackRow("f2b:menu"),
		})
}

func f2bWaitActive(ctx context.Context, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		state := sysutil.ServiceState(ctx, "fail2ban")
		if state == "active" || time.Now().After(deadline) {
			return state
		}
		select {
		case <-ctx.Done():
			return state
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func failShort(err error) string {
	if ce, ok := sysErr(err); ok {
		return ce.Short()
	}
	return "внутренняя ошибка"
}
