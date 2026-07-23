package handlers

import (
	"fmt"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

func mainMenu(env *Env) (string, [][]gotgbot.InlineKeyboardButton) {

	text := fmt.Sprintf("<b>🤵 Steward</b> · Управление виртуалкой\n%s · <code>%s</code>",
		Esc(env.OS.Name), Esc(env.Met.Hostname()))
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("📊 Статус сервера", "st:menu")),
		Row(Btn("🛡 Fail2ban", "f2b:menu"), Btn("🔥 Firewall", "fw:menu")),
		Row(Btn("⚙️ Сервисы", "svc:menu"), Btn("💾 Диск", "disk:menu")),
		Row(Btn("🚀 Оптимизация", "opt:menu"), Btn("🔐 Безопасность", "sec:menu")),
		Row(Btn("🔄 Обновления", "upd:menu"), Btn("🔑 Доступ", "acc:menu")),
		Row(Btn("⚡ Питание", "pwr:menu"), Btn("⚙️ Настройки", "set:menu")),
	}
	return text, kb
}

func handleMenu(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	text, kb := mainMenu(env)
	return Edit(env, cq, text, kb)
}

func sendMainMenu(env *Env, chatID int64) error {
	text, kb := mainMenu(env)
	_, err := SendHTML(env, chatID, text, kb)
	return err
}
