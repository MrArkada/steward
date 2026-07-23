package handlers

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

const powerCooldown = 10 * time.Minute

func handlePwr(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "later":
		return pwrLater(env, cq)
	case "ask":
		return pwrAsk(env, cq, parts)
	case "do":
		return pwrDo(env, cq, parts)
	}
	return pwrMenu(env, cq)
}

func pwrCooldownLeft(env *Env) time.Duration {
	last := env.Store.LastPower()
	if last.IsZero() {
		return 0
	}
	return powerCooldown - time.Since(last)
}

func pwrChatID(cq *gotgbot.CallbackQuery) int64 {
	if cq.Message == nil {
		return 0
	}
	return cq.Message.GetChat().Id
}

func pwrMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	if !env.OS.HasSystemd {
		return Unsupported(env, cq, "Управление питанием")
	}
	var b strings.Builder
	b.WriteString("<b>⚡ Питание сервера</b>\n")
	if snap, err := env.Met.Snapshot(); err == nil {
		fmt.Fprintf(&b, "<b>Uptime:</b> %s\n", FmtDur(snap.Uptime))
	}
	fmt.Fprintf(&b, "<b>Активных SSH-сессий:</b> %d\n", sysutil.SSHSessionCount(env.RootCtx))

	if left := pwrCooldownLeft(env); left > 0 {
		fmt.Fprintf(&b, "\n⏳ Кулдаун: повторное действие возможно через %s", FmtDur(left))
	}
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔄 Перезагрузить", "pwr:ask:reboot"), Btn("⏻ Выключить", "pwr:ask:shutdown")),
		Row(Btn("⏰ Отложенная перезагрузка", "pwr:later")),
		BackRow("menu:main"),
	}
	return Edit(env, cq, b.String(), kb)
}

func pwrLater(env *Env, cq *gotgbot.CallbackQuery) error {
	text := "<b>⏰ Отложенная перезагрузка</b>\n" +
		"Сервер перезагрузится в выбранное время.\n" +
		"Запланированную перезагрузку можно отменить кнопкой ниже."
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("через 5 мин", "pwr:ask:later:5"), Btn("через 30 мин", "pwr:do:later:30")),
		Row(Btn("сегодня в 04:00", "pwr:do:later:0400")),
		Row(Btn("❌ Отменить отложенную", "pwr:do:cancel")),
		BackRow("pwr:menu"),
	}
	return Edit(env, cq, text, kb)
}

func pwrAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) < 3 {
		return pwrMenu(env, cq)
	}
	sess := sysutil.SSHSessionCount(env.RootCtx)
	switch parts[2] {
	case "reboot":
		return Edit(env, cq,
			fmt.Sprintf("⚠️ <b>Перезагрузить сервер?</b>\nАктивных SSH-сессий: %d", sess),
			ConfirmKB("pwr:do:reboot", "pwr:menu"))
	case "shutdown":
		return Edit(env, cq,
			fmt.Sprintf("⚠️ <b>Выключить сервер?</b>\nАктивных SSH-сессий: %d", sess),
			ConfirmKB("pwr:do:shutdown", "pwr:menu"))
	case "later":

		if len(parts) > 3 && parts[3] == "5" {
			return Edit(env, cq,
				fmt.Sprintf("⚠️ <b>Перезагрузить сервер через 5 минут?</b>\nАктивных SSH-сессий: %d", sess),
				ConfirmKB("pwr:do:later:5", "pwr:later"))
		}
		return pwrLater(env, cq)
	}
	return pwrMenu(env, cq)
}

func pwrDo(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) < 3 {
		return pwrMenu(env, cq)
	}
	switch parts[2] {
	case "reboot", "shutdown":
		return pwrDoNow(env, cq, parts[2] == "reboot")
	case "later":
		if len(parts) < 4 {
			return pwrLater(env, cq)
		}
		return pwrDoLater(env, cq, parts[3])
	case "cancel":
		return pwrDoCancel(env, cq)
	}
	return pwrMenu(env, cq)
}

func pwrDoNow(env *Env, cq *gotgbot.CallbackQuery, reboot bool) error {

	if left := pwrCooldownLeft(env); left > 0 {
		return Edit(env, cq,
			fmt.Sprintf("⏳ Подождите ещё %d мин. (защита от случайных повторов)",
				int(math.Ceil(left.Minutes()))),
			[][]gotgbot.InlineKeyboardButton{BackRow("pwr:menu")})
	}
	now := time.Now()
	chatID := pwrChatID(cq)
	if err := env.Store.Update(func(st *storage.State) {
		st.LastPowerAction = now
		if reboot {

			st.PendingReboot = &storage.RebootFlag{At: now, ChatID: chatID}
		}
	}); err != nil {
		env.Log.Printf("PWR save state: %v", err)
	}
	if reboot {
		env.Audit.Log(cq.From.Id, "перезагрузка сервера")

		_ = Edit(env, cq, "🔄 Сервер перезагружается. После подъёма пришлю отчёт.",
			[][]gotgbot.InlineKeyboardButton{})
		if _, err := sysutil.Run(env.RootCtx, 5*time.Second, "systemctl", "reboot"); err != nil {
			env.Log.Printf("PWR reboot: %v", err)
		}
		return nil
	}
	env.Audit.Log(cq.From.Id, "выключение сервера")
	_ = Edit(env, cq, "⏻ Сервер выключается.", [][]gotgbot.InlineKeyboardButton{})
	if _, err := sysutil.Run(env.RootCtx, 5*time.Second, "systemctl", "poweroff"); err != nil {
		env.Log.Printf("PWR poweroff: %v", err)
	}
	return nil
}

func pwrDoLater(env *Env, cq *gotgbot.CallbackQuery, arg string) error {
	var shutdownArg, when string
	switch arg {
	case "5":
		shutdownArg, when = "+5", "через 5 минут"
	case "30":
		shutdownArg, when = "+30", "через 30 минут"
	case "0400":
		shutdownArg, when = "04:00", "сегодня в 04:00"
	default:
		return pwrLater(env, cq)
	}
	if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "shutdown", "-r", shutdownArg); err != nil {
		Fail(env, cq, "запланировать перезагрузку", err, "pwr:later")
		return nil
	}
	env.Audit.Log(cq.From.Id, "отложенная перезагрузка: "+when)
	now := time.Now()
	chatID := pwrChatID(cq)
	if err := env.Store.Update(func(st *storage.State) {
		st.LastPowerAction = now
		st.PendingReboot = &storage.RebootFlag{At: now, ChatID: chatID}
	}); err != nil {
		env.Log.Printf("PWR save state: %v", err)
	}
	return Edit(env, cq,
		fmt.Sprintf("⏰ Сервер перезагрузится %s.", Esc(when)),
		[][]gotgbot.InlineKeyboardButton{
			Row(Btn("❌ Отменить", "pwr:do:cancel")),
			BackRow("pwr:menu"),
		})
}

func pwrDoCancel(env *Env, cq *gotgbot.CallbackQuery) error {
	if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "shutdown", "-c"); err != nil {
		Fail(env, cq, "отменить отложенную перезагрузку", err, "pwr:later")
		return nil
	}
	env.Audit.Log(cq.From.Id, "отмена отложенной перезагрузки")

	if err := env.Store.Update(func(st *storage.State) { st.PendingReboot = nil }); err != nil {
		env.Log.Printf("PWR save state: %v", err)
	}
	return Edit(env, cq, "✅ Отложенная перезагрузка отменена.",
		[][]gotgbot.InlineKeyboardButton{BackRow("pwr:menu")})
}
