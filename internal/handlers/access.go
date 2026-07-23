package handlers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/security"
	"serverbot/internal/sysutil"
)

var userNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func validUserName(s string) bool { return userNameRe.MatchString(s) }

var validKeyTypes = map[string]bool{
	"ssh-rsa":             true,
	"ssh-ed25519":         true,
	"ssh-dss":             true,
	"ecdsa-sha2-nistp256": true,
	"ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true,
}

type sysUser struct {
	Name  string
	Home  string
	Shell string
}

type sshKey struct {
	Type    string
	Tail    string
	Comment string
}

func handleAcc(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	arg := func(i int) string {
		if len(parts) > i {
			return parts[i]
		}
		return ""
	}
	switch act {
	case "passlist":
		return passlistView(env, cq)
	case "keylist":
		return keylistView(env, cq)
	case "keys":

		return keysView(env, cq, arg(2))
	case "keyadd":

		user := arg(2)
		if !validUserName(user) {
			return Edit(env, cq, "⚠️ Недопустимое имя пользователя.",
				[][]gotgbot.InlineKeyboardButton{BackRow("acc:keylist")})
		}
		env.Pending.Set(cq.From.Id, "acc:key:"+user)
		return Edit(env, cq,
			fmt.Sprintf("➕ Пришлите публичный SSH-ключ для <code>%s</code> одним сообщением.\nФормат: <code>ssh-ed25519 AAAA... комментарий</code>", Esc(user)),
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:keys:" + user)})
	case "ownpass":

		user := arg(2)
		if !validUserName(user) {
			return Edit(env, cq, "⚠️ Недопустимое имя пользователя.",
				[][]gotgbot.InlineKeyboardButton{BackRow("acc:passlist")})
		}
		env.Pending.Set(cq.From.Id, "acc:ownpass:"+user)
		return Edit(env, cq,
			fmt.Sprintf("✏️ Пришлите новый пароль для <code>%s</code> одним сообщением.\n\n"+
				"• минимум 8 символов\n"+
				"• ваше сообщение с паролем будет удалено сразу после применения\n"+
				"• пароль нигде не логируется", Esc(user)),
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:passlist")})
	case "users":
		return usersView(env, cq)
	case "useradd":
		env.Pending.Set(cq.From.Id, "acc:useradd")
		return Edit(env, cq,
			"➕ Пришлите имя нового пользователя одним сообщением.\nСтрочные латинские буквы, цифры, <code>_</code> и <code>-</code> (макс. 32 символа).",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:users")})
	case "ask":
		return handleAccAsk(env, cq, parts)
	case "do":
		return handleAccDo(env, cq, parts)
	}

	return Edit(env, cq, accMenuText(), accMenuKB())
}

func handleAccText(env *Env, msg *gotgbot.Message, parts []string) error {
	if len(parts) < 2 {
		return nil
	}
	switch parts[1] {
	case "key":
		if len(parts) < 3 {
			return nil
		}
		return accTextKey(env, msg, parts[2])
	case "useradd":
		return accTextUserAdd(env, msg)
	case "ownpass":
		if len(parts) < 3 {
			return nil
		}
		return accTextOwnPass(env, msg, parts[2])
	}
	return nil
}

func handleAccAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	arg := func(i int) string {
		if len(parts) > i {
			return parts[i]
		}
		return ""
	}
	switch arg(2) {
	case "pass":
		user := arg(3)
		return Edit(env, cq,
			fmt.Sprintf("⚠️ Сменить пароль пользователя <code>%s</code>?\nНовый пароль будет сгенерирован автоматически и отправлен отдельным сообщением.", Esc(user)),
			ConfirmKB("acc:do:pass:"+user, "acc:passlist"))
	case "keydel":
		user, n := arg(3), arg(4)
		return Edit(env, cq,
			fmt.Sprintf("⚠️ Удалить ключ №%s из authorized_keys пользователя <code>%s</code>?", Esc(n), Esc(user)),
			ConfirmKB(fmt.Sprintf("acc:do:keydel:%s:%s", user, n), "acc:keys:"+user))
	case "sudo":
		user := arg(3)
		text := fmt.Sprintf("⚠️ Выдать права sudo пользователю <code>%s</code>?", Esc(user))
		if hasSudo(userGroups(env, user)) {
			text = fmt.Sprintf("⚠️ Отозвать права sudo у пользователя <code>%s</code>?", Esc(user))
		}
		return Edit(env, cq, text, ConfirmKB("acc:do:sudo:"+user, "acc:users"))
	case "userdel":
		user := arg(3)
		return Edit(env, cq,
			fmt.Sprintf("⚠️ Удалить пользователя <code>%s</code> вместе с домашним каталогом?\nДействие необратимо.", Esc(user)),
			ConfirmKB("acc:do:userdel:"+user, "acc:users"))
	}
	return Edit(env, cq, accMenuText(), accMenuKB())
}

func handleAccDo(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	arg := func(i int) string {
		if len(parts) > i {
			return parts[i]
		}
		return ""
	}
	switch arg(2) {
	case "pass":
		return accDoPass(env, cq, arg(3))
	case "keydel":
		return accDoKeyDel(env, cq, arg(3), arg(4))
	case "sudo":
		return accDoSudo(env, cq, arg(3))
	case "userdel":
		return accDoUserDel(env, cq, arg(3))
	}
	return Edit(env, cq, accMenuText(), accMenuKB())
}

func accDoPass(env *Env, cq *gotgbot.CallbackQuery, user string) error {
	if !validUserName(user) {
		return Edit(env, cq, "⚠️ Недопустимое имя пользователя.",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:passlist")})
	}
	pw, err := security.GenPassword(20)
	if err != nil {
		Fail(env, cq, "сгенерировать пароль", err, "acc:passlist")
		return nil
	}
	if _, err := sysutil.RunStdin(env.RootCtx, 15*time.Second, user+":"+pw, "chpasswd"); err != nil {
		Fail(env, cq, "сменить пароль "+user, err, "acc:passlist")
		return nil
	}

	env.Audit.Log(cq.From.Id, "смена пароля "+user)
	_ = Edit(env, cq,
		fmt.Sprintf("✅ Пароль для <code>%s</code> отправлен отдельным сообщением.", Esc(user)),
		[][]gotgbot.InlineKeyboardButton{BackRow("acc:passlist")})
	if cq.Message != nil {
		sendPassword(env, cq.Message.GetChat().Id, user, pw)
	}
	return nil
}

func accDoKeyDel(env *Env, cq *gotgbot.CallbackQuery, user, ns string) error {
	n, err := strconv.Atoi(ns)
	if !validUserName(user) || err != nil || n < 1 {
		return Edit(env, cq, "⚠️ Некорректные параметры.",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:keylist")})
	}
	env.Audit.Log(cq.From.Id, fmt.Sprintf("удаление SSH-ключа #%d у %s", n, user))
	if err := deleteKeyLine(user, n); err != nil {
		Fail(env, cq, fmt.Sprintf("удалить ключ №%d", n), err, "acc:keys:"+user)
		return nil
	}
	return keysView(env, cq, user)
}

func accDoSudo(env *Env, cq *gotgbot.CallbackQuery, user string) error {
	if !validUserName(user) {
		return Edit(env, cq, "⚠️ Недопустимое имя пользователя.",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:users")})
	}
	groups := userGroups(env, user)
	inSudo, inWheel := containsStr(groups, "sudo"), containsStr(groups, "wheel")
	if inSudo || inWheel {

		target := "wheel"
		if inSudo {
			target = "sudo"
		}
		env.Audit.Log(cq.From.Id, "отзыв sudo у "+user)
		if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "gpasswd", "-d", user, target); err != nil {
			Fail(env, cq, "отозвать sudo у "+user, err, "acc:users")
			return nil
		}
		return usersView(env, cq)
	}
	target := sudoGroupName(env)
	env.Audit.Log(cq.From.Id, "выдача sudo "+user)
	if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "gpasswd", "-a", user, target); err != nil {
		Fail(env, cq, "выдать sudo "+user, err, "acc:users")
		return nil
	}
	return usersView(env, cq)
}

func accDoUserDel(env *Env, cq *gotgbot.CallbackQuery, user string) error {

	if user == "root" || !validUserName(user) {
		return Edit(env, cq, "⚠️ Этого пользователя удалять нельзя.",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:users")})
	}
	env.Audit.Log(cq.From.Id, "удаление пользователя "+user)
	if _, err := sysutil.Run(env.RootCtx, 30*time.Second, "userdel", "-r", user); err != nil {
		Fail(env, cq, "удалить пользователя "+user, err, "acc:users")
		return nil
	}
	return usersView(env, cq)
}

func accMenuText() string {
	return "<b>🔑 Доступ к серверу</b>\nПароли, SSH-ключи и пользователи."
}

func accMenuKB() [][]gotgbot.InlineKeyboardButton {
	return [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔑 Сменить пароль", "acc:passlist")),
		Row(Btn("🗝 SSH-ключи", "acc:keylist"), Btn("👥 Пользователи", "acc:users")),
		BackRow("menu:main"),
	}
}

func passlistView(env *Env, cq *gotgbot.CallbackQuery) error {
	kb := [][]gotgbot.InlineKeyboardButton{}
	for _, u := range listSysUsers() {
		kb = append(kb, Row(
			Btn("🔑 "+u.Name, "acc:ask:pass:"+u.Name),
			Btn("✏️ "+u.Name, "acc:ownpass:"+u.Name),
		))
	}
	kb = append(kb, BackRow("acc:menu"))
	return Edit(env, cq, "<b>🔑 Сменить пароль</b>\nВыберите пользователя:\n🔑 — случайный пароль, ✏️ — задать свой", kb)
}

func keylistView(env *Env, cq *gotgbot.CallbackQuery) error {
	kb := [][]gotgbot.InlineKeyboardButton{}
	for _, u := range listSysUsers() {
		kb = append(kb, Row(Btn("🗝 "+u.Name, "acc:keys:"+u.Name)))
	}
	kb = append(kb, BackRow("acc:menu"))
	return Edit(env, cq, "<b>🗝 SSH-ключи</b>\nВыберите пользователя:", kb)
}

func keysView(env *Env, cq *gotgbot.CallbackQuery, user string) error {
	if !validUserName(user) {
		return Edit(env, cq, "⚠️ Недопустимое имя пользователя.",
			[][]gotgbot.InlineKeyboardButton{BackRow("acc:keylist")})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>🗝 SSH-ключи: <code>%s</code></b>\n", Esc(user))
	keys, err := readKeys(user)
	switch {
	case err != nil && !os.IsNotExist(err):
		Fail(env, cq, "прочитать authorized_keys", err, "acc:keylist")
		return nil
	case err != nil || len(keys) == 0:
		b.WriteString("Ключей нет.\n")
	default:
		for i, k := range keys {
			fmt.Fprintf(&b, "%d. <code>%s</code> …<code>%s</code>", i+1, Esc(k.Type), Esc(k.Tail))
			if k.Comment != "" {
				fmt.Fprintf(&b, " — %s", Esc(Trunc(k.Comment, 30)))
			}
			b.WriteString("\n")
		}
	}
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("➕ Добавить ключ", "acc:keyadd:"+user)),
	}
	for i := range keys {
		kb = append(kb, Row(Btn(fmt.Sprintf("❌ %d", i+1),
			fmt.Sprintf("acc:ask:keydel:%s:%d", user, i+1))))
	}
	kb = append(kb, BackRow("acc:keylist"))
	return Edit(env, cq, b.String(), kb)
}

func usersView(env *Env, cq *gotgbot.CallbackQuery) error {
	var b strings.Builder
	b.WriteString("<b>👥 Пользователи с доступом к шеллу</b>\n")
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("➕ Добавить пользователя", "acc:useradd")),
	}
	for _, u := range listSysUsers() {
		sudoMark := ""
		if hasSudo(userGroups(env, u.Name)) {
			sudoMark = " — <b>sudo</b>"
		}
		fmt.Fprintf(&b, "• <code>%s</code>%s\n", Esc(u.Name), sudoMark)
		sudoBtn := Btn("🛡 sudo "+u.Name, "acc:ask:sudo:"+u.Name)
		if u.Name == "root" {

			kb = append(kb, Row(sudoBtn))
		} else {
			kb = append(kb, Row(sudoBtn, Btn("❌ "+u.Name, "acc:ask:userdel:"+u.Name)))
		}
	}
	kb = append(kb, BackRow("acc:menu"))
	return Edit(env, cq, b.String(), kb)
}

func accTextKey(env *Env, msg *gotgbot.Message, user string) error {
	chatID := msg.Chat.Id
	back := [][]gotgbot.InlineKeyboardButton{BackRow("acc:menu")}
	if !validUserName(user) {
		_, err := SendHTML(env, chatID, "⚠️ Недопустимое имя пользователя.", back)
		return err
	}
	fields := strings.Fields(strings.TrimSpace(msg.GetText()))
	if len(fields) < 2 || !validKeyTypes[fields[0]] {
		_, err := SendHTML(env, chatID,
			"⚠️ Неверный формат ключа. Пришлите публичный ключ вида:\n<code>ssh-ed25519 AAAA... комментарий</code>", back)
		return err
	}

	keyLine := strings.Join(fields, " ")
	home := userHome(user)
	if home == "" {
		_, err := SendHTML(env, chatID, "⚠️ Домашний каталог пользователя не найден.", back)
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "authorized_keys")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		env.Log.Printf("FAIL создать %s: %v", sshDir, err)
		_, serr := SendHTML(env, chatID, "⚠️ Не удалось создать каталог .ssh.", back)
		return serr
	}
	f, err := os.OpenFile(keyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		env.Log.Printf("FAIL открыть %s: %v", keyFile, err)
		_, serr := SendHTML(env, chatID, "⚠️ Не удалось открыть authorized_keys.", back)
		return serr
	}
	_, werr := f.WriteString(keyLine + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		env.Log.Printf("FAIL записать %s: %v", keyFile, werr)
		_, serr := SendHTML(env, chatID, "⚠️ Не удалось записать ключ.", back)
		return serr
	}

	_ = os.Chmod(sshDir, 0o700)
	_ = os.Chmod(keyFile, 0o600)
	if _, err := sysutil.Run(env.RootCtx, 10*time.Second, "chown", "-R", user+":"+user, sshDir); err != nil {
		env.Log.Printf("FAIL chown %s: %v", sshDir, err)
	}
	env.Audit.Log(fromID(msg), "добавление SSH-ключа для "+user)
	_, err = SendHTML(env, chatID, fmt.Sprintf("✅ Ключ добавлен пользователю <code>%s</code>.", Esc(user)), back)
	return err
}

func accTextUserAdd(env *Env, msg *gotgbot.Message) error {
	chatID := msg.Chat.Id
	back := [][]gotgbot.InlineKeyboardButton{BackRow("acc:menu")}
	name := strings.TrimSpace(msg.GetText())
	if !validUserName(name) {
		_, err := SendHTML(env, chatID,
			"⚠️ Недопустимое имя. Допустимы строчные латинские буквы, цифры, <code>_</code> и <code>-</code>; первый символ — буква или <code>_</code> (макс. 32 символа).", back)
		return err
	}
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "useradd", "-m", "-s", "/bin/bash", name); err != nil {
		env.Log.Printf("FAIL создать пользователя %s: %v", name, err)
		short := "внутренняя ошибка"
		if ce, ok := sysErr(err); ok {
			short = ce.Short()
		}
		_, serr := SendHTML(env, chatID, fmt.Sprintf("⚠️ Не удалось создать пользователя:\n<code>%s</code>", Esc(short)), back)
		return serr
	}
	pw, err := security.GenPassword(20)
	if err != nil {
		env.Log.Printf("FAIL генерация пароля: %v", err)
		_, serr := SendHTML(env, chatID,
			fmt.Sprintf("✅ Пользователь <code>%s</code> создан, но пароль сгенерировать не удалось — задайте его через «🔑 Сменить пароль».", Esc(name)), back)
		return serr
	}
	if _, err := sysutil.RunStdin(env.RootCtx, 15*time.Second, name+":"+pw, "chpasswd"); err != nil {
		env.Log.Printf("FAIL установить пароль %s: %v", name, err)
		_, serr := SendHTML(env, chatID,
			fmt.Sprintf("✅ Пользователь <code>%s</code> создан, но установить пароль не удалось — задайте его через «🔑 Сменить пароль».", Esc(name)), back)
		return serr
	}

	env.Audit.Log(fromID(msg), "создание пользователя "+name)
	if _, err := SendHTML(env, chatID,
		fmt.Sprintf("✅ Пользователь <code>%s</code> создан. Пароль отправлен отдельным сообщением.", Esc(name)), back); err != nil {
		return err
	}
	sendPassword(env, chatID, name, pw)
	return nil
}

func listSysUsers() []sysUser {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return []sysUser{{Name: "root", Home: "/root", Shell: "/bin/bash"}}
	}
	var users []sysUser
	hasRoot := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		u := sysUser{Name: f[0], Home: f[5], Shell: f[6]}
		if u.Name == "root" {
			hasRoot = true
			users = append(users, u)
			continue
		}
		if shellOK(u.Shell) {
			users = append(users, u)
		}
	}
	if !hasRoot {
		users = append([]sysUser{{Name: "root", Home: "/root", Shell: "/bin/bash"}}, users...)
	}
	return users
}

func shellOK(shell string) bool {
	return strings.HasSuffix(shell, "bash") || strings.HasSuffix(shell, "sh") || strings.HasSuffix(shell, "zsh")
}

func userHome(name string) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 6 && f[0] == name {
			return f[5]
		}
	}
	return ""
}

func isKeyLine(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && !strings.HasPrefix(line, "#")
}

func readKeys(user string) ([]sshKey, error) {
	home := userHome(user)
	if home == "" {
		return nil, errors.New("домашний каталог не найден")
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		return nil, err
	}
	var keys []sshKey
	for _, line := range strings.Split(string(data), "\n") {
		if !isKeyLine(line) {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		k := sshKey{Type: f[0], Tail: f[1]}
		if len(k.Tail) > 12 {
			k.Tail = k.Tail[len(k.Tail)-12:]
		}
		if len(f) >= 3 {
			k.Comment = strings.Join(f[2:], " ")
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func deleteKeyLine(user string, n int) error {
	home := userHome(user)
	if home == "" {
		return errors.New("домашний каталог не найден")
	}
	path := filepath.Join(home, ".ssh", "authorized_keys")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	idx := 0
	for _, line := range lines {
		if isKeyLine(line) {
			idx++
			if idx == n {
				continue
			}
		}
		out = append(out, line)
	}
	if idx < n {
		return fmt.Errorf("ключ №%d не найден", n)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600)
}

func userGroups(env *Env, user string) []string {
	out, err := sysutil.Run(env.RootCtx, 5*time.Second, "id", "-nG", user)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

func hasSudo(groups []string) bool {
	return containsStr(groups, "sudo") || containsStr(groups, "wheel")
}

func sudoGroupName(env *Env) string {
	if _, err := sysutil.Run(env.RootCtx, 5*time.Second, "getent", "group", "sudo"); err == nil {
		return "sudo"
	}
	return "wheel"
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sendPassword(env *Env, chatID int64, user, pw string) {
	m, err := SendHTML(env, chatID,
		fmt.Sprintf("🔑 Новый пароль для <code>%s</code>:\n\n<code>%s</code>\n\n⚠️ Сообщение удалится через 60 секунд.", Esc(user), Esc(pw)),
		[][]gotgbot.InlineKeyboardButton{})
	if err != nil {
		env.Log.Printf("FAIL отправить пароль: %v", err)
		return
	}
	deleteAfter(env, chatID, m.MessageId, 60*time.Second)
}

func deleteAfter(env *Env, chatID int64, msgID int64, d time.Duration) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				env.Log.Printf("PANIC deleteAfter: %v", r)
			}
		}()
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-env.RootCtx.Done():
			return
		case <-t.C:
			_, _ = env.API.DeleteMessage(chatID, msgID, nil)
		}
	}()
}

func fromID(msg *gotgbot.Message) int64 {
	if msg.From != nil {
		return msg.From.Id
	}
	return 0
}

func accTextOwnPass(env *Env, msg *gotgbot.Message, user string) error {
	chatID := msg.Chat.Id
	pw := strings.TrimRight(msg.GetText(), "\r\n")

	_, _ = env.API.DeleteMessage(chatID, msg.MessageId, nil)

	back := [][]gotgbot.InlineKeyboardButton{BackRow("acc:passlist")}
	switch {
	case !validUserName(user):
		_, err := SendHTML(env, chatID, "⚠️ Недопустимое имя пользователя.", back)
		return err
	case strings.ContainsAny(pw, "\r\n"):
		_, err := SendHTML(env, chatID,
			"⚠️ Пароль не должен содержать перенос строки — не применён. Попробуйте ещё раз через меню.", back)
		return err
	case len(pw) < 8:
		_, err := SendHTML(env, chatID,
			"⚠️ Пароль короче 8 символов — не применён. Попробуйте ещё раз через меню.", back)
		return err
	}

	env.Audit.Log(fromID(msg), "смена пароля (свой) "+user)
	if _, err := sysutil.RunStdin(env.RootCtx, 15*time.Second, user+":"+pw, "chpasswd"); err != nil {
		env.Log.Printf("FAIL сменить пароль %s: %v", user, err)
		_, err2 := SendHTML(env, chatID,
			fmt.Sprintf("⚠️ Не удалось сменить пароль для <code>%s</code>. Подробности — в bot.log.", Esc(user)),
			back)
		return err2
	}
	_, err := SendHTML(env, chatID,
		fmt.Sprintf("✅ Пароль для <code>%s</code> изменён.\nСообщение с паролем удалено, в логи он не записывался.", Esc(user)),
		back)
	return err
}
