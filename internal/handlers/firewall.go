package handlers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/sysutil"
)

const fwMaxShow = 20

type fwRule struct {
	num  int
	text string
}

func handleFW(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {

	if !env.OS.IsDebianLike() {
		return Unsupported(env, cq, "Firewall (UFW)")
	}
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		return fwMenu(env, cq)

	case "install":
		return fwInstall(env, cq)

	case "list":
		out, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "status", "numbered")
		if err != nil {
			Fail(env, cq, "получить список правил", err, "fw:menu")
			return nil
		}
		text := "<b>🔥 Правила UFW</b>\n<pre>" + Esc(Trunc(strings.TrimSpace(out), 3500)) + "</pre>"
		return Edit(env, cq, text, [][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})

	case "add":

		env.Pending.Set(cq.From.Id, "fw:add")
		return Edit(env, cq,
			"➕ Пришлите порт и протокол сообщением в формате <code>порт/протокол</code>.\n\n"+
				"Примеры: <code>8080/tcp</code>, <code>53/udp</code>, <code>9000</code> (по умолчанию tcp).",
			[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})

	case "dellist":
		return fwDelList(env, cq)

	case "ask":
		if len(parts) < 3 {
			break
		}
		switch parts[2] {
		case "del":
			if len(parts) < 4 {
				break
			}
			num := parts[3]

			warn := ""
			if out, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "status", "numbered"); err == nil {
				sshPort := sysutil.SSHPort()
				for _, r := range fwParseRules(out) {
					if fmt.Sprint(r.num) == num && fwRuleHasPort(r.text, sshPort) {
						warn = fmt.Sprintf("\n\n‼️ <b>Внимание:</b> это правило про SSH-порт <code>%d</code> — "+
							"его удаление может отрезать доступ к серверу!", sshPort)
						break
					}
				}
			}
			return Edit(env, cq,
				fmt.Sprintf("⚠️ Удалить правило <b>№%s</b>?%s", Esc(num), warn),
				ConfirmKB("fw:do:del:"+num, "fw:dellist"))
		case "on":
			return Edit(env, cq,
				fmt.Sprintf("⚠️ Включить UFW?\n\nУбедитесь, что SSH-порт <code>%d</code> разрешён правилами, "+
					"иначе можно потерять доступ к серверу.", sysutil.SSHPort()),
				ConfirmKB("fw:do:on", "fw:menu"))
		case "off":
			return Edit(env, cq,
				"⚠️ Выключить UFW?\n\nФайрвол перестанет фильтровать трафик — сервер останется без защиты.",
				ConfirmKB("fw:do:off", "fw:menu"))
		}

	case "do":
		if len(parts) < 3 {
			break
		}
		switch parts[2] {
		case "del":
			if len(parts) < 4 {
				break
			}
			num := parts[3]
			env.Audit.Log(cq.From.Id, "ufw: удаление правила №"+num)
			if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "--force", "delete", num); err != nil {
				Fail(env, cq, "удалить правило №"+num, err, "fw:dellist")
				return nil
			}
			return fwDelList(env, cq)
		case "on":
			env.Audit.Log(cq.From.Id, "ufw: включение")
			if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "--force", "enable"); err != nil {
				Fail(env, cq, "включить UFW", err, "fw:menu")
				return nil
			}
			return fwMenu(env, cq)
		case "off":
			env.Audit.Log(cq.From.Id, "ufw: выключение")
			if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "disable"); err != nil {
				Fail(env, cq, "выключить UFW", err, "fw:menu")
				return nil
			}
			return fwMenu(env, cq)
		}
	}
	return fwMenu(env, cq)
}

func handleFWText(env *Env, msg *gotgbot.Message, parts []string) error {
	if len(parts) < 2 || parts[1] != "add" {
		return nil
	}
	port, proto, ok := parsePortProto(msg.GetText())
	if !ok {
		_, err := SendHTML(env, msg.Chat.Id,
			"⚠️ Неверный формат. Пришлите <code>порт</code> или <code>порт/протокол</code> "+
				"(tcp|udp|any), порт 1–65535.\nПример: <code>8080/tcp</code>",
			[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})
		return err
	}
	rule := fmt.Sprintf("%d/%s", port, proto)
	env.Audit.Log(msg.From.Id, "ufw: allow "+rule)
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "allow", rule); err != nil {
		env.Log.Printf("FAIL ufw allow %s: %v", rule, err)
		_, err2 := SendHTML(env, msg.Chat.Id,
			fmt.Sprintf("⚠️ Не удалось добавить правило <code>%s</code>:\n<code>%s</code>", Esc(rule), Esc(failShort(err))),
			[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})
		return err2
	}
	_, err := SendHTML(env, msg.Chat.Id,
		fmt.Sprintf("✅ Правило добавлено: <code>%s</code> разрешён.", Esc(rule)),
		[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})
	return err
}

func fwMenu(env *Env, cq *gotgbot.CallbackQuery) error {
	if !sysutil.Exists("ufw") {
		return Edit(env, cq,
			"<b>🔥 Firewall (UFW)</b>\n\nUFW не установлен на этом сервере.",
			[][]gotgbot.InlineKeyboardButton{
				Row(Btn("📦 Установить и настроить", "fw:install")),
				BackRow("menu:main"),
			})
	}

	state := "unknown"
	if out, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "status"); err == nil {
		if _, v, ok := strings.Cut(strings.SplitN(out, "\n", 2)[0], ":"); ok {
			state = strings.TrimSpace(v)
		}
	} else {
		env.Log.Printf("ufw status: %v", err)
	}

	toggle := Btn("🟢 Включить", "fw:ask:on")
	if state == "active" {
		toggle = Btn("🔴 Выключить", "fw:ask:off")
	}
	text := fmt.Sprintf("<b>🔥 Firewall (UFW)</b>\nСтатус: <code>%s</code>", Esc(state))
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("📋 Правила", "fw:list")),
		Row(Btn("➕ Добавить правило", "fw:add"), Btn("➖ Удалить правило", "fw:dellist")),
		Row(toggle),
		BackRow("menu:main"),
	}
	return Edit(env, cq, text, kb)
}

func fwInstall(env *Env, cq *gotgbot.CallbackQuery) error {
	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Выполняю, это может занять минуту..."})
	_ = Edit(env, cq, "⏳ Устанавливаю и настраиваю UFW…", [][]gotgbot.InlineKeyboardButton{})

	if _, err := sysutil.PMInstall(env.RootCtx, env.OS.PM, "ufw"); err != nil {
		Fail(env, cq, "установить пакет ufw", err, "fw:menu")
		return nil
	}
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "default", "deny", "incoming"); err != nil {
		Fail(env, cq, "задать политику «deny incoming»", err, "fw:menu")
		return nil
	}
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "default", "allow", "outgoing"); err != nil {
		Fail(env, cq, "задать политику «allow outgoing»", err, "fw:menu")
		return nil
	}

	sshPort := sysutil.SSHPort()
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "allow", fmt.Sprintf("%d/tcp", sshPort)); err != nil {
		Fail(env, cq, fmt.Sprintf("разрешить SSH-порт %d/tcp", sshPort), err, "fw:menu")
		return nil
	}
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "ufw", "--force", "enable"); err != nil {
		Fail(env, cq, "включить UFW", err, "fw:menu")
		return nil
	}
	env.Audit.Log(cq.From.Id, fmt.Sprintf("ufw: установка и включение (ssh %d/tcp открыт)", sshPort))
	return Edit(env, cq,
		fmt.Sprintf("✅ <b>UFW установлен и включён.</b>\n\n"+
			"• Входящие по умолчанию запрещены, исходящие разрешены\n"+
			"• Открыт SSH-порт <code>%d/tcp</code>", sshPort),
		[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})
}

func fwDelList(env *Env, cq *gotgbot.CallbackQuery) error {
	out, err := sysutil.Run(env.RootCtx, 10*time.Second, "ufw", "status", "numbered")
	if err != nil {
		Fail(env, cq, "получить список правил", err, "fw:menu")
		return nil
	}
	rules := fwParseRules(out)
	if len(rules) == 0 {
		return Edit(env, cq, "➖ Правил для удаления нет.",
			[][]gotgbot.InlineKeyboardButton{BackRow("fw:menu")})
	}
	var b strings.Builder
	b.WriteString("<b>➖ Удалить правило</b>\nВыберите правило:")
	kb := [][]gotgbot.InlineKeyboardButton{}
	for i, r := range rules {
		if i >= fwMaxShow {
			fmt.Fprintf(&b, "\n… показаны первые %d правил из %d", fwMaxShow, len(rules))
			break
		}
		kb = append(kb, Row(Btn(fmt.Sprintf("➖ %d: %s", r.num, Trunc(r.text, 30)),
			fmt.Sprintf("fw:ask:del:%d", r.num))))
	}
	kb = append(kb, BackRow("fw:menu"))
	return Edit(env, cq, b.String(), kb)
}

func fwParseRules(out string) []fwRule {
	var rules []fwRule
	for _, line := range strings.Split(out, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "[") {
			continue
		}
		end := strings.Index(trim, "]")
		if end < 0 {
			continue
		}
		num, err := strconv.Atoi(strings.TrimSpace(trim[1:end]))
		if err != nil {
			continue
		}

		desc := strings.Join(strings.Fields(trim[end+1:]), " ")
		rules = append(rules, fwRule{num: num, text: desc})
	}
	return rules
}

func fwRuleHasPort(text string, port int) bool {
	p := strconv.Itoa(port)
	for _, f := range strings.Fields(text) {
		base := f
		if i := strings.Index(base, "/"); i >= 0 {
			base = base[:i]
		}
		for _, part := range strings.Split(base, ":") {
			if part == p {
				return true
			}
		}
	}
	return false
}

func parsePortProto(s string) (port int, proto string, ok bool) {
	portPart, proto, _ := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "/")
	if proto == "" {
		proto = "tcp"
	}
	if proto != "tcp" && proto != "udp" && proto != "any" {
		return 0, "", false
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", false
	}
	return port, proto, true
}
