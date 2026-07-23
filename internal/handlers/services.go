package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

var svcActions = map[string]string{
	"start":   "запустить",
	"stop":    "остановить",
	"restart": "перезапустить",
	"enable":  "включить автозапуск",
	"disable": "отключить автозапуск",
}

func handleSvc(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if !env.OS.HasSystemd {
		return Unsupported(env, cq, "Управление сервисами")
	}
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		text, kb := svcMenu(env)
		return Edit(env, cq, text, kb)

	case "s":

		if len(parts) > 2 {
			return svcCard(env, cq, parts[2])
		}

	case "ask":

		if len(parts) > 3 {
			action, unit := parts[2], parts[3]
			if text := svcAskText(action, unit); text != "" {
				return Edit(env, cq, text,
					ConfirmKB("svc:do:"+action+":"+unit, "svc:s:"+unit))
			}
		}

	case "do":

		if len(parts) > 3 {
			action, unit := parts[2], parts[3]
			desc, ok := svcActions[action]
			if !ok {
				break
			}
			env.Audit.Log(cq.From.Id, "svc "+action+" "+unit)
			if _, err := sysutil.ServiceAction(env.RootCtx, action, unit); err != nil {
				Fail(env, cq, desc+" сервис "+unit, err, "svc:s:"+unit)
				return nil
			}
			return svcCard(env, cq, unit)
		}

	case "logs":

		if len(parts) > 2 {
			return svcLogs(env, cq, parts[2])
		}

	case "wd":

		if len(parts) > 2 {
			unit := parts[2]
			on := !env.Store.WatchdogList()[unit]
			err := env.Store.Update(func(st *storage.State) {
				if st.Watchdog == nil {
					st.Watchdog = make(map[string]bool)
				}
				st.Watchdog[unit] = on
			})
			if err != nil {
				Fail(env, cq, "сохранить настройки watchdog", err, "svc:s:"+unit)
				return nil
			}
			return svcCard(env, cq, unit)
		}
	}

	text, kb := svcMenu(env)
	return Edit(env, cq, text, kb)
}

func svcMenu(env *Env) (string, [][]gotgbot.InlineKeyboardButton) {
	ctx := env.RootCtx
	var kb [][]gotgbot.InlineKeyboardButton
	var row []gotgbot.InlineKeyboardButton
	found := 0
	for _, unit := range sysutil.KnownServices() {
		if !sysutil.UnitExists(ctx, unit) {
			continue
		}
		found++
		row = append(row, Btn(svcStateEmoji(svcDisplayState(ctx, unit))+" "+unit, "svc:s:"+unit))
		if len(row) == 2 {
			kb = append(kb, Row(row...))
			row = nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, Row(row...))
	}
	text := "<b>⚙️ Сервисы</b>"
	if found == 0 {
		text += "\n\nИзвестные сервисы не обнаружены."
	} else {
		text += "\nВыберите сервис для управления:"
	}
	kb = append(kb, BackRow("menu:main"))
	return text, kb
}

func svcCard(env *Env, cq *gotgbot.CallbackQuery, unit string) error {
	ctx := env.RootCtx
	state := svcDisplayState(ctx, unit)

	enabled := "unknown"
	out, _ := sysutil.Systemctl(ctx, "is-enabled", unit)
	if line, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); line != "" {
		enabled = line
	}
	wd := env.Store.WatchdogList()[unit]
	wdText, wdBtn := "не следит", "👁 Следить"
	if wd {

		wdText, wdBtn = "следит", "❌ Не следить"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>⚙️ Сервис <code>%s</code></b>\n", Esc(unit))
	fmt.Fprintf(&b, "Состояние: %s <code>%s</code>\n", svcStateEmoji(state), Esc(state))
	fmt.Fprintf(&b, "Автозагрузка: <code>%s</code>\n", Esc(enabled))
	fmt.Fprintf(&b, "Watchdog: %s", wdText)
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("▶️ Start", "svc:do:start:"+unit), Btn("⏹ Stop", "svc:ask:stop:"+unit)),
		Row(Btn("🔄 Restart", "svc:ask:restart:"+unit)),
		Row(Btn("✅ Enable", "svc:do:enable:"+unit), Btn("🚫 Disable", "svc:ask:disable:"+unit)),
		Row(Btn("📄 Логи", "svc:logs:"+unit), Btn(wdBtn, "svc:wd:"+unit)),
		BackRow("svc:menu"),
	}
	return Edit(env, cq, b.String(), kb)
}

func svcAskText(action, unit string) string {
	switch action {
	case "stop":
		return fmt.Sprintf("⚠️ Остановить сервис <code>%s</code>?\n\nЕго работа будет прервана.", Esc(unit))
	case "restart":
		return fmt.Sprintf("⚠️ Перезапустить сервис <code>%s</code>?\n\nВозможен кратковременный простой.", Esc(unit))
	case "disable":
		return fmt.Sprintf("⚠️ Отключить автозапуск <code>%s</code>?\n\nСервис не стартует автоматически после перезагрузки.", Esc(unit))
	}
	return ""
}

func svcLogs(env *Env, cq *gotgbot.CallbackQuery, unit string) error {
	out, err := sysutil.JournalLogs(env.RootCtx, unit, 50)
	if err != nil {
		Fail(env, cq, "получить логи "+unit, err, "svc:s:"+unit)
		return nil
	}
	back := [][]gotgbot.InlineKeyboardButton{BackRow("svc:s:" + unit)}
	if len(out) <= 3500 {
		return Edit(env, cq,
			fmt.Sprintf("<b>📄 Логи <code>%s</code></b> (последние 50 строк)\n<pre>%s</pre>",
				Esc(unit), Esc(out)),
			back)
	}
	if cq.Message == nil {
		return nil
	}
	if err := SendDoc(env, cq.Message.GetChat().Id, unit+".log", []byte(out), "Логи "+unit); err != nil {
		Fail(env, cq, "отправить логи файлом", err, "svc:s:"+unit)
		return nil
	}
	return Edit(env, cq, "📄 Логи отправлены файлом.", back)
}

func svcDisplayState(ctx context.Context, unit string) string {
	if unit == "ufw" {
		out, err := sysutil.Run(ctx, 10*time.Second, "ufw", "status")
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				if rest, ok := strings.CutPrefix(line, "Status:"); ok {
					if strings.TrimSpace(rest) == "active" {
						return "active"
					}
					return "inactive"
				}
			}
		}
	}
	return sysutil.ServiceState(ctx, unit)
}

func svcStateEmoji(state string) string {
	switch state {
	case "active":
		return "🟢"
	case "failed":
		return "🔴"
	case "inactive":
		return "⚪"
	default:
		return "🟡"
	}
}
