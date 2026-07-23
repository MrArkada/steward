package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/sysutil"
)

var cleanNames = map[string]string{
	"apt":     "apt clean + autoremove",
	"journal": "vacuum журналов (100M)",
	"logs":    "старые логи /var/log",
	"tmp":     "/tmp старше 7 дней",
	"docker":  "docker prune",
	"all":     "полная очистка",
}

func handleDisk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		return Edit(env, cq, diskMenuText(env), diskKB(env))

	case "top":

		_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Сканирую..."})
		_ = Edit(env, cq, "⏳ Сканирую диск...", nil)
		out, err := sysutil.Run(env.RootCtx, 60*time.Second, "sh", "-c",
			"du -x -d1 -h / 2>/dev/null | sort -hr | head -n 11")
		if err != nil {
			Fail(env, cq, "просканировать диск", err, "disk:menu")
			return nil
		}
		return Edit(env, cq,
			fmt.Sprintf("<b>📊 Топ-10 жирных директорий</b>\n<pre>%s</pre>", Esc(strings.TrimSpace(out))),
			[][]gotgbot.InlineKeyboardButton{BackRow("disk:menu")})

	case "ask":

		if len(parts) > 3 && parts[2] == "clean" {
			what := parts[3]
			if !cleanAvailable(env, what) {
				return Unsupported(env, cq, "Очистка: "+cleanNames[what])
			}
			if text := cleanAskText(what); text != "" {
				return Edit(env, cq, text, ConfirmKB("disk:do:clean:"+what, "disk:menu"))
			}
		}

	case "do":

		if len(parts) > 3 && parts[2] == "clean" {
			return diskDoClean(env, cq, parts[3])
		}
	}
	return Edit(env, cq, diskMenuText(env), diskKB(env))
}

func diskMenuText(env *Env) string {
	snap, err := env.Met.Snapshot()
	if err != nil {
		return fmt.Sprintf("⚠️ Метрики недоступны на этой ОС:\n<code>%s</code>", Esc(err.Error()))
	}
	var b strings.Builder
	b.WriteString("<b>💾 Диск</b>\n")
	if len(snap.Disks) == 0 {
		b.WriteString("Разделы не обнаружены.\n")
	}
	for _, d := range snap.Disks {
		fmt.Fprintf(&b, "<code>%-14s</code> %3.0f%% [%s] %s/%s\n",
			Esc(d.Mount), d.Percent, Bar(d.Percent, 10), FmtBytes(d.Used), FmtBytes(d.Total))
	}
	return b.String()
}

func diskKB(env *Env) [][]gotgbot.InlineKeyboardButton {
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("📊 Топ-10 жирных директорий", "disk:top")),
	}
	if env.OS.IsDebianLike() {
		kb = append(kb, Row(Btn("🧹 apt clean + autoremove", "disk:ask:clean:apt")))
	}
	kb = append(kb,
		Row(Btn("🗞 Vacuum журналов (100M)", "disk:ask:clean:journal")),
		Row(Btn("📄 Старые логи /var/log", "disk:ask:clean:logs")),
		Row(Btn("📁 /tmp старше 7 дней", "disk:ask:clean:tmp")),
	)
	if sysutil.Exists("docker") {
		kb = append(kb, Row(Btn("🐳 Docker prune", "disk:ask:clean:docker")))
	}
	kb = append(kb,
		Row(Btn("🧹 Очистить всё", "disk:ask:clean:all")),
		BackRow("menu:main"),
	)
	return kb
}

func cleanAskText(what string) string {
	switch what {
	case "apt":
		return "⚠️ Выполнить <code>apt-get clean</code> и <code>apt-get autoremove -y</code>?\n\nБудут удалены кэш пакетов и неиспользуемые зависимости."
	case "journal":
		return "⚠️ Сжать журналы systemd до 100 МБ?\n\nСтарые записи журнала будут удалены."
	case "logs":
		return "⚠️ Удалить сжатые и ротированные логи (*.gz, *.1) старше 14 дней в /var/log?"
	case "tmp":
		return "⚠️ Удалить файлы в /tmp старше 7 дней?"
	case "docker":
		return "⚠️ Выполнить <code>docker system prune -f</code>?\n\nБудут удалены остановленные контейнеры, неиспользуемые сети и dangling-образы."
	case "all":
		return "⚠️ Выполнить ВСЕ операции очистки по очереди?\n\nКэш пакетов, журналы, старые логи, /tmp и docker prune (что доступно)."
	}
	return ""
}

func cleanAvailable(env *Env, what string) bool {
	switch what {
	case "apt":
		return env.OS.IsDebianLike()
	case "docker":
		return sysutil.Exists("docker")
	}
	_, ok := cleanNames[what]
	return ok
}

func diskDoClean(env *Env, cq *gotgbot.CallbackQuery, what string) error {
	name, ok := cleanNames[what]
	if !ok {
		return Edit(env, cq, diskMenuText(env), diskKB(env))
	}
	if !cleanAvailable(env, what) {
		return Unsupported(env, cq, "Очистка: "+name)
	}
	env.Audit.Log(cq.From.Id, "очистка диска: "+name)
	_ = Edit(env, cq, "⏳ Выполняю очистку: "+Esc(name)+"…", nil)

	before := rootUsed(env)
	var report string
	if what == "all" {

		report = cleanAll(env)
	} else {
		out, err := cleanOne(env, what)
		if err != nil {
			Fail(env, cq, "очистку («"+name+"»)", err, "disk:menu")
			return nil
		}
		report = strings.TrimSpace(out)
	}

	after := rootUsed(env)
	freed := uint64(0)
	if after < before {
		freed = before - after
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>🧹 Очистка: %s</b>\n", Esc(name))
	if report != "" {
		fmt.Fprintf(&b, "\n<pre>%s</pre>\n", Esc(Trunc(report, 3000)))
	}
	fmt.Fprintf(&b, "\n✅ Освобождено: <b>%s</b>", FmtBytes(freed))
	return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{BackRow("disk:menu")})
}

func cleanAll(env *Env) string {
	var b strings.Builder
	for _, what := range []string{"apt", "journal", "logs", "tmp", "docker"} {
		if !cleanAvailable(env, what) {
			continue
		}
		out, err := cleanOne(env, what)
		if err != nil {
			short := "ошибка"
			if ce, ok := sysErr(err); ok {
				short = ce.Short()
			}
			fmt.Fprintf(&b, "❌ %s: %s\n", cleanNames[what], short)
			continue
		}
		fmt.Fprintf(&b, "✅ %s", cleanNames[what])
		if line := strings.TrimSpace(out); line != "" {
			fmt.Fprintf(&b, ": %s", Trunc(line, 200))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func cleanOne(env *Env, what string) (string, error) {
	ctx := env.RootCtx
	switch what {
	case "apt":
		return sysutil.AptClean(ctx)
	case "journal":
		return sysutil.Run(ctx, 2*time.Minute, "journalctl", "--vacuum-size=100M")
	case "logs":
		out, err := sysutil.Run(ctx, 2*time.Minute, "sh", "-c",
			`find /var/log -type f \( -name "*.gz" -o -name "*.1" \) -mtime +14 -print -delete | wc -l`)
		if err != nil {
			return "", err
		}
		return "Удалено файлов: " + strings.TrimSpace(out), nil
	case "tmp":
		if _, err := sysutil.Run(ctx, 2*time.Minute, "find", "/tmp", "-type", "f", "-mtime", "+7", "-delete"); err != nil {
			return "", err
		}
		return "Удалены файлы старше 7 дней", nil
	case "docker":
		return sysutil.Run(ctx, 5*time.Minute, "docker", "system", "prune", "-f")
	}
	return "", fmt.Errorf("неизвестный тип очистки: %s", what)
}

func rootUsed(env *Env) uint64 {
	snap, err := env.Met.Snapshot()
	if err != nil {
		return 0
	}
	for _, d := range snap.Disks {
		if d.Mount == "/" {
			return d.Used
		}
	}
	return 0
}
