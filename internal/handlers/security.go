package handlers

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

const (
	sshdConfPath = "/etc/ssh/sshd_config"
	sshdBakPath  = "/etc/ssh/sshd_config.serverbot.bak"
	autoUpgPath  = "/etc/apt/apt.conf.d/20auto-upgrades"
)

var failedIPRe = regexp.MustCompile(`from (\d+\.\d+\.\d+\.\d+)`)

func secMenuKB() [][]gotgbot.InlineKeyboardButton {
	return [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔐 SSH Hardening", "sec:ssh")),
		Row(Btn("📜 Последние входы", "sec:last"), Btn("🚨 Неудачные попытки", "sec:failed")),
		Row(Btn("🌐 Открытые порты", "sec:ports"), Btn("🔄 Автообновления безопасности", "sec:uau")),
		Row(Btn("📋 Установить rsyslog", "sec:ask:rsys")),
		BackRow("menu:main"),
	}
}

func handleSec(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		return Edit(env, cq, "<b>🔐 Безопасность</b>\n\nВыберите раздел:", secMenuKB())

	case "ssh":
		return secSSHView(env, cq, "")

	case "port":

		env.Pending.Set(cq.From.Id, "sec:port")
		return Edit(env, cq,
			"🔌 <b>Смена SSH-порта</b>\n\n"+
				"Пришлите новый порт сообщением (число от 1024 до 65535).\n\n"+
				"⚠️ Перед сменой порт будет открыт в ufw, текущая сессия не разорвётся.",
			[][]gotgbot.InlineKeyboardButton{BackRow("sec:ssh")})

	case "last":
		out, err := sysutil.Run(env.RootCtx, 10*time.Second, "last", "-n", "15")
		if err != nil {
			Fail(env, cq, "получить список входов", err, "sec:menu")
			return nil
		}
		return Edit(env, cq,
			"📜 <b>Последние входы</b>\n<pre>"+Esc(Trunc(strings.TrimSpace(out), 3500))+"</pre>",
			[][]gotgbot.InlineKeyboardButton{BackRow("sec:menu")})

	case "failed":

		_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Выполняю..."})
		return secFailedView(env, cq)

	case "ports":
		return secPortsView(env, cq, "")

	case "portsnap":

		ports, err := sysutil.ListeningPorts(env.RootCtx)
		if err != nil {
			Fail(env, cq, "получить список портов", err, "sec:ports")
			return nil
		}
		snap := make(map[string]string, len(ports))
		for _, p := range ports {
			snap[p.Key()] = p.Process
		}
		if err := env.Store.Update(func(st *storage.State) { st.PortSnapshot = snap }); err != nil {
			Fail(env, cq, "сохранить снимок портов", err, "sec:ports")
			return nil
		}
		return secPortsView(env, cq, fmt.Sprintf("✅ Снимок сохранён: %d портов.\n\n", len(snap)))

	case "uau":
		return secUAUView(env, cq, "")

	case "ask":
		return secAsk(env, cq, parts)

	case "do":
		return secDo(env, cq, parts)
	}
	return Edit(env, cq, "<b>🔐 Безопасность</b>\n\nВыберите раздел:", secMenuKB())
}

func handleSecText(env *Env, msg *gotgbot.Message, parts []string) error {
	if len(parts) < 2 || parts[1] != "port" {
		return nil
	}
	port, err := strconv.Atoi(strings.TrimSpace(msg.GetText()))
	if err != nil || port < 1024 || port > 65535 {
		_, err := SendHTML(env, msg.Chat.Id,
			"⚠️ Порт должен быть числом от 1024 до 65535.\nНажмите «🔌 SSH-порт» ещё раз и пришлите корректное значение.",
			[][]gotgbot.InlineKeyboardButton{BackRow("sec:ssh")})
		return err
	}
	_, err = SendHTML(env, msg.Chat.Id,
		fmt.Sprintf("⚠️ <b>Сменить SSH-порт на <code>%d</code>?</b>\n\n"+
			"Перед сменой порт будет открыт в ufw (если файрвол активен), конфиг проверен (<code>sshd -t</code>) и sshd перезагружен.\n"+
			"Текущая сессия не разорвётся, но новые подключения будут на порту <code>%d</code>.", port, port),
		ConfirmKB(fmt.Sprintf("sec:do:port:%d", port), "sec:ssh"))
	return err
}

func secSSHView(env *Env, cq *gotgbot.CallbackQuery, note string) error {
	passAuth, rootLogin, port := sshdState()
	text := note + "<b>🔐 SSH Hardening</b>\n\n" +
		fmt.Sprintf("PasswordAuthentication: <code>%s</code>\n", Esc(passAuth)) +
		fmt.Sprintf("PermitRootLogin: <code>%s</code>\n", Esc(rootLogin)) +
		fmt.Sprintf("Port: <code>%s</code>\n\n", Esc(port)) +
		"Рекомендуется: вход по ключу (без пароля), запрет входа root по паролю, нестандартный порт."
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔑 PasswordAuthentication: "+passAuth, "sec:ask:passno")),
		Row(Btn("👤 PermitRootLogin → no", "sec:ask:root:no"),
			Btn("👤 → prohibit-password", "sec:ask:root:prohibit-password")),
		Row(Btn("🔌 SSH-порт: "+port, "sec:port")),
		BackRow("sec:menu"),
	}
	return Edit(env, cq, text, kb)
}

func sshdState() (passAuth, rootLogin, port string) {
	passAuth, rootLogin, port = "yes", "prohibit-password", "22"
	data, err := os.ReadFile(sshdConfPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, " ")
		if !ok {
			k, v, ok = strings.Cut(line, "\t")
		}
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "passwordauthentication":
			passAuth = v
		case "permitrootlogin":
			rootLogin = v
		case "port":
			port = v
		}
	}
	return
}

func sshdSetDirective(env *Env, key, value string) error {
	data, err := os.ReadFile(sshdConfPath)
	if err != nil {
		return fmt.Errorf("прочитать %s: %w", sshdConfPath, err)
	}

	if _, err := os.Stat(sshdBakPath); os.IsNotExist(err) {
		if err := os.WriteFile(sshdBakPath, data, 0o600); err != nil {
			return fmt.Errorf("создать бэкап: %w", err)
		}
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		k, _, ok := strings.Cut(trimmed, " ")
		if !ok {
			k, _, ok = strings.Cut(trimmed, "\t")
		}
		if !ok || !strings.EqualFold(strings.TrimSpace(k), key) {
			continue
		}
		lines[i] = key + " " + value
		replaced = true
		break
	}
	if !replaced {
		lines = append(lines, key+" "+value)
	}
	if err := os.WriteFile(sshdConfPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("записать %s: %w", sshdConfPath, err)
	}

	if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "sshd", "-t"); err != nil {
		if bak, rerr := os.ReadFile(sshdBakPath); rerr == nil {
			_ = os.WriteFile(sshdConfPath, bak, 0o644)
		}
		return fmt.Errorf("sshd -t: %w (конфиг восстановлен из бэкапа)", err)
	}

	if _, err := sysutil.ServiceAction(env.RootCtx, "reload", "sshd"); err != nil {
		if _, err2 := sysutil.ServiceAction(env.RootCtx, "reload", "ssh"); err2 != nil {
			return fmt.Errorf("reload sshd/ssh: %w", err2)
		}
	}
	return nil
}

func secAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) >= 3 {
		switch parts[2] {
		case "passno":
			return Edit(env, cq,
				"⚠️ <b>Отключить вход по паролю?</b>\n\n"+
					"Будет установлено <code>PasswordAuthentication no</code>.\n"+
					"Убедитесь, что вход по SSH-ключу уже работает — иначе доступ к серверу будет потерян!\n\n"+
					"Конфиг проверяется (<code>sshd -t</code>) и при ошибке откатывается автоматически.",
				ConfirmKB("sec:do:passno", "sec:ssh"))

		case "root":
			if len(parts) >= 4 && (parts[3] == "no" || parts[3] == "prohibit-password") {
				return Edit(env, cq,
					fmt.Sprintf("⚠️ <b>Установить <code>PermitRootLogin %s</code>?</b>\n\n"+
						"Конфиг будет проверен (<code>sshd -t</code>) и при ошибке откачен из бэкапа.", Esc(parts[3])),
					ConfirmKB("sec:do:root:"+parts[3], "sec:ssh"))
			}

		case "uau":
			if len(parts) >= 4 {
				switch parts[3] {
				case "on":
					return Edit(env, cq,
						"⚠️ <b>Включить автообновления безопасности?</b>\n\n"+
							"Будет установлен пакет <code>unattended-upgrades</code> и включена автоматическая установка обновлений безопасности.",
						ConfirmKB("sec:do:uau:on", "sec:uau"))
				case "off":
					return Edit(env, cq,
						"⚠️ <b>Выключить автообновления безопасности?</b>\n\n"+
							"Автоматическая установка обновлений будет отключена (пакет <code>unattended-upgrades</code> останется установленным).",
						ConfirmKB("sec:do:uau:off", "sec:uau"))
				}
			}

		case "rsys":
			return Edit(env, cq,
				"⚠️ <b>Установить rsyslog?</b>\n\n"+
					"Появятся классические логи (<code>/var/log/auth.log</code>, <code>/var/log/syslog</code>) — "+
					"без них на этом сервере fail2ban со стандартным конфигом не стартует, а статистика входов читается только из journald.\n\n"+
					"rsyslog — фоновый демон, потребляет ~10–20 МБ RAM. "+
					"Без него бот и fail2ban уже работают через journald (backend=systemd) — ставьте, только если нужны текстовые логи.",
				ConfirmKB("sec:do:rsys", "sec:menu"))
		}
	}
	return Edit(env, cq, "<b>🔐 Безопасность</b>\n\nВыберите раздел:", secMenuKB())
}

func secDo(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) < 3 {
		return Edit(env, cq, "<b>🔐 Безопасность</b>\n\nВыберите раздел:", secMenuKB())
	}

	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Выполняю..."})
	switch parts[2] {
	case "passno":
		env.Audit.Log(cq.From.Id, "sshd PasswordAuthentication no")
		if err := sshdSetDirective(env, "PasswordAuthentication", "no"); err != nil {
			Fail(env, cq, "изменить PasswordAuthentication", err, "sec:ssh")
			return nil
		}
		return secSSHView(env, cq, "✅ Установлено <code>PasswordAuthentication no</code>.\n\n")

	case "root":
		if len(parts) >= 4 && (parts[3] == "no" || parts[3] == "prohibit-password") {
			env.Audit.Log(cq.From.Id, "sshd PermitRootLogin "+parts[3])
			if err := sshdSetDirective(env, "PermitRootLogin", parts[3]); err != nil {
				Fail(env, cq, "изменить PermitRootLogin", err, "sec:ssh")
				return nil
			}
			return secSSHView(env, cq,
				fmt.Sprintf("✅ Установлено <code>PermitRootLogin %s</code>.\n\n", Esc(parts[3])))
		}

	case "port":
		if len(parts) >= 4 {
			port, err := strconv.Atoi(parts[3])
			if err != nil || port < 1024 || port > 65535 {
				return Edit(env, cq, "⚠️ Некорректный порт.",
					[][]gotgbot.InlineKeyboardButton{BackRow("sec:ssh")})
			}
			env.Audit.Log(cq.From.Id, "sshd Port "+parts[3])

			if sysutil.Exists("ufw") {
				out, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "status")
				if err == nil && strings.Contains(out, "Status: active") {
					if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "allow", parts[3]+"/tcp"); err != nil {
						Fail(env, cq, "открыть порт в ufw", err, "sec:ssh")
						return nil
					}
				}
			}
			if err := sshdSetDirective(env, "Port", parts[3]); err != nil {
				Fail(env, cq, "сменить SSH-порт", err, "sec:ssh")
				return nil
			}
			return secSSHView(env, cq, fmt.Sprintf(
				"✅ SSH-порт изменён на <code>%s</code>.\n"+
					"⚠️ Текущая сессия не разорвётся, но новые подключения — только на новом порту. "+
					"Проверьте доступ, не закрывая эту сессию!\n\n", Esc(parts[3])))
		}

	case "uau":
		if !env.OS.IsDebianLike() {
			return Unsupported(env, cq, "Автообновления безопасности")
		}
		if len(parts) >= 4 {
			switch parts[3] {
			case "on":
				env.Audit.Log(cq.From.Id, "enable unattended-upgrades")
				if _, err := sysutil.PMInstall(env.RootCtx, env.OS.PM, "unattended-upgrades"); err != nil {
					Fail(env, cq, "установить unattended-upgrades", err, "sec:uau")
					return nil
				}
				if err := writeAutoUpgrades(true); err != nil {
					Fail(env, cq, "записать 20auto-upgrades", err, "sec:uau")
					return nil
				}
				return secUAUView(env, cq, "✅ Автообновления безопасности включены.\n\n")
			case "off":
				env.Audit.Log(cq.From.Id, "disable unattended-upgrades")
				if err := writeAutoUpgrades(false); err != nil {
					Fail(env, cq, "записать 20auto-upgrades", err, "sec:uau")
					return nil
				}
				return secUAUView(env, cq, "✅ Автообновления безопасности выключены.\n\n")
			}
		}
	case "rsys":
		if !env.OS.IsDebianLike() {
			return Unsupported(env, cq, "Установка rsyslog")
		}
		env.Audit.Log(cq.From.Id, "install rsyslog")
		if _, err := sysutil.PMInstall(env.RootCtx, env.OS.PM, "rsyslog"); err != nil {
			Fail(env, cq, "установить rsyslog", err, "sec:menu")
			return nil
		}
		if _, err := sysutil.Systemctl(env.RootCtx, "enable", "--now", "rsyslog"); err != nil {
			Fail(env, cq, "запустить rsyslog", err, "sec:menu")
			return nil
		}

		authOK := false
		for i := 0; i < 10; i++ {
			if _, err := os.Stat("/var/log/auth.log"); err == nil {
				authOK = true
				break
			}
			select {
			case <-env.RootCtx.Done():
			case <-time.After(500 * time.Millisecond):
			}
		}
		var b strings.Builder
		b.WriteString("✅ <b>rsyslog установлен и запущен</b>\n")
		if authOK {
			b.WriteString("\nФайл <code>/var/log/auth.log</code> создан — теперь работают:\n• статистика входов из файла (а не только journald)\n• fail2ban со стандартным конфигом Debian")
		} else {
			b.WriteString("\n<code>/var/log/auth.log</code> появится после первого события аутентификации.")
		}
		if sysutil.Exists("fail2ban-client") &&
			sysutil.ServiceState(env.RootCtx, "fail2ban") != "active" {
			b.WriteString("\n\n⚠️ fail2ban сейчас не запущен — перезапустите его:")
			return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{
				Row(Btn("🔄 Перезапустить fail2ban", "f2b:ask:restart")),
				BackRow("sec:menu"),
			})
		}
		return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{BackRow("sec:menu")})
	}
	return Edit(env, cq, "<b>🔐 Безопасность</b>\n\nВыберите раздел:", secMenuKB())
}

func secFailedView(env *Env, cq *gotgbot.CallbackQuery) error {
	kb := [][]gotgbot.InlineKeyboardButton{BackRow("sec:menu")}

	data, err := os.ReadFile("/var/log/auth.log")
	source := "/var/log/auth.log"
	if err != nil {
		data, err = os.ReadFile("/var/log/secure")
		source = "/var/log/secure"
	}
	if err != nil {

		jout, jerr := sysutil.Run(env.RootCtx, 30*time.Second,
			"journalctl", "_COMM=sshd", "-o", "cat", "--no-pager", "-q", "--since", "-7d", "-n", "50000")
		if jerr != nil {
			return Edit(env, cq,
				"🚨 <b>Неудачные попытки входа</b>\n\nЛог не найден (<code>/var/log/auth.log</code>, <code>/var/log/secure</code>), journald тоже недоступен.", kb)
		}
		data = []byte(jout)
		source = "journald (за 7 суток)"
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "Failed password") {
			continue
		}
		if m := failedIPRe.FindStringSubmatch(line); m != nil {
			counts[m[1]]++
		}
	}
	if len(counts) == 0 {
		return Edit(env, cq,
			fmt.Sprintf("🚨 <b>Неудачные попытки входа</b> <i>(%s)</i>\n\nПопыток не найдено — отлично! 👍", Esc(source)), kb)
	}
	type kv struct {
		ip string
		n  int
	}
	list := make([]kv, 0, len(counts))
	for ip, n := range counts {
		list = append(list, kv{ip, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	if len(list) > 10 {
		list = list[:10]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🚨 <b>Топ-10 неудачных попыток входа</b> <i>(%s)</i>\n\n", Esc(source))
	for _, e := range list {
		fmt.Fprintf(&b, "<code>%s</code> — %d попыток", Esc(e.ip), e.n)
		if geo := env.Geo.Lookup(env.RootCtx, e.ip); geo != "" {
			b.WriteString(" — " + Esc(geo))
		}
		b.WriteString("\n")
	}
	return Edit(env, cq, b.String(), kb)
}

func secPortsView(env *Env, cq *gotgbot.CallbackQuery, note string) error {
	ports, err := sysutil.ListeningPorts(env.RootCtx)
	if err != nil {
		Fail(env, cq, "получить список портов", err, "sec:menu")
		return nil
	}
	var b strings.Builder
	b.WriteString(note)
	b.WriteString("🌐 <b>Открытые порты</b>\n\n")
	if len(ports) == 0 {
		b.WriteString("Слушающих портов не найдено.\n")
	}
	for _, p := range ports {
		proc := p.Process
		if proc == "" {
			proc = "—"
		}
		fmt.Fprintf(&b, "<code>%s</code> — %s — %s\n", Esc(p.Key()), Esc(p.Addr), Esc(proc))
	}
	if env.Store.HasPorts() {
		fmt.Fprintf(&b, "\n📸 Снимок сохранён: %d портов.", len(env.Store.Ports()))
	}
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔄 Обновить", "sec:ports"), Btn("📸 Запомнить снимок", "sec:portsnap")),
		BackRow("sec:menu"),
	}
	return Edit(env, cq, b.String(), kb)
}

func secUAUView(env *Env, cq *gotgbot.CallbackQuery, note string) error {
	if !env.OS.IsDebianLike() {
		return Unsupported(env, cq, "Автообновления безопасности")
	}
	installed := sysutil.Exists("unattended-upgrades")
	enabled := false
	if data, err := os.ReadFile(autoUpgPath); err == nil {
		enabled = strings.Contains(string(data), `Unattended-Upgrade "1"`)
	}
	yesno := func(b bool) string {
		if b {
			return "✅ да"
		}
		return "❌ нет"
	}
	text := note + "<b>🔄 Автообновления безопасности</b>\n\n" +
		fmt.Sprintf("Пакет unattended-upgrades: %s\n", yesno(installed)) +
		fmt.Sprintf("Автообновления включены: %s", yesno(enabled))
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("✅ Включить", "sec:ask:uau:on"), Btn("❌ Выключить", "sec:ask:uau:off")),
		BackRow("sec:menu"),
	}
	return Edit(env, cq, text, kb)
}

func writeAutoUpgrades(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	content := "APT::Periodic::Update-Package-Lists \"" + v + "\";\n" +
		"APT::Periodic::Unattended-Upgrade \"" + v + "\";\n"
	return os.WriteFile(autoUpgPath, []byte(content), 0o644)
}
