package handlers

import (
	"fmt"
	"os"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/sysutil"
)

func handleUpd(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if !env.OS.HasPM() {
		return Unsupported(env, cq, "Обновления пакетов")
	}
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "check":
		return updCheck(env, cq)
	case "ask":

		if len(parts) > 2 && parts[2] == "upgrade" {
			return Edit(env, cq,
				"⚠️ <b>Обновить все пакеты системы?</b>\n\nОперация может занять несколько минут и затронуть работающие сервисы.",
				ConfirmKB("upd:do:upgrade", "upd:menu"))
		}
	case "do":
		if len(parts) > 2 && parts[2] == "upgrade" {
			return updDoUpgrade(env, cq)
		}
	}

	return Edit(env, cq, updMenuText(env), updKB())
}

func updMenuText(env *Env) string {
	return fmt.Sprintf("<b>🔄 Обновления пакетов</b>\nПакетный менеджер: <code>%s</code>", Esc(env.OS.PM.String()))
}

func updKB() [][]gotgbot.InlineKeyboardButton {
	return [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔍 Проверить обновления", "upd:check")),
		Row(Btn("⬆️ Обновить всё", "upd:ask:upgrade")),
		BackRow("menu:main"),
	}
}

func updCheck(env *Env, cq *gotgbot.CallbackQuery) error {
	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Проверяю..."})
	_ = Edit(env, cq, "⏳ Проверяю обновления...", [][]gotgbot.InlineKeyboardButton{})
	if _, err := sysutil.PMRefresh(env.RootCtx, env.OS.PM); err != nil {
		Fail(env, cq, "обновить индекс пакетов", err, "upd:menu")
		return nil
	}
	out, err := sysutil.PMUpgradable(env.RootCtx, env.OS.PM)
	if err != nil {
		Fail(env, cq, "получить список обновлений", err, "upd:menu")
		return nil
	}
	list, n := formatUpgradable(out)
	if n == 0 {
		return Edit(env, cq, "✅ Обновлений нет — система в актуальном состоянии.", updKB())
	}
	if len([]rune(list)) <= 3500 {
		return Edit(env, cq,
			fmt.Sprintf("<b>🔄 Доступно обновлений: %d</b>\n<pre>%s</pre>", n, Esc(list)),
			updKB())
	}

	if cq.Message == nil {
		return nil
	}
	header := fmt.Sprintf("Доступно обновлений: %d\nОбновить все: меню бота → Обновления → «Обновить всё»\n\n", n)
	if err := SendDoc(env, cq.Message.GetChat().Id, "updates.txt", []byte(header+list),
		fmt.Sprintf("Доступно обновлений: %d", n)); err != nil {
		Fail(env, cq, "отправить список обновлений файлом", err, "upd:menu")
		return nil
	}
	return Edit(env, cq,
		fmt.Sprintf("📄 Доступно обновлений: <b>%d</b> — полный список отправлен файлом.\n<pre>%s</pre>",
			n, Esc(firstLines(list, 10))),
		updKB())
}

func updDoUpgrade(env *Env, cq *gotgbot.CallbackQuery) error {
	env.Audit.Log(cq.From.Id, "полное обновление системы")
	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Обновление запущено"})
	_ = Edit(env, cq, "⏳ Обновляю систему, это может занять несколько минут...", [][]gotgbot.InlineKeyboardButton{})
	out, err := sysutil.PMUpgradeAll(env.RootCtx, env.OS.PM)
	if err != nil {
		Fail(env, cq, "обновить систему", err, "upd:menu")
		return nil
	}
	report := tailRunes(strings.TrimSpace(out), 1500)
	if report == "" {
		report = "(вывод пуст)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>✅ Обновление завершено</b>\n<pre>%s</pre>", Esc(report))
	kb := [][]gotgbot.InlineKeyboardButton{Row(Btn("🔍 Проверить обновления", "upd:check"))}

	if _, statErr := os.Stat("/var/run/reboot-required"); statErr == nil {
		b.WriteString("\n⚠️ <b>Требуется перезагрузка сервера</b>")
		kb = append(kb, Row(Btn("⚡ Перезагрузить", "pwr:ask:reboot")))
	}
	kb = append(kb, BackRow("upd:menu"))
	return Edit(env, cq, b.String(), kb)
}

func formatUpgradable(out string) (string, int) {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			strings.HasPrefix(line, "Listing") || strings.HasPrefix(line, "Список") ||

			strings.HasPrefix(line, "WARNING") || strings.HasPrefix(line, "ПРЕДУПРЕЖДЕНИЕ") {
			continue
		}
		lines = append(lines, prettyUpgradeLine(line))
	}
	return strings.Join(lines, "\n"), len(lines)
}

func prettyUpgradeLine(line string) string {
	const marker = "[upgradable from: "
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "• " + line
	}
	old := strings.TrimSuffix(strings.TrimSpace(line[idx+len(marker):]), "]")
	fields := strings.Fields(line[:idx])
	if len(fields) < 2 {
		return "• " + line
	}
	name := fields[0]
	if slash := strings.Index(name, "/"); slash > 0 {
		name = name[:slash]
	}
	return fmt.Sprintf("• %s: %s → %s", name, old, fields[1])
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… и ещё %d", len(lines)-n)
}

func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…\n" + string(r[len(r)-n:])
}
