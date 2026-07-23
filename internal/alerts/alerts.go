package alerts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/config"
	"serverbot/internal/detect"
	"serverbot/internal/geoip"
	"serverbot/internal/metrics"
	"serverbot/internal/security"
	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

var authFiles = []string{"/var/log/auth.log", "/var/log/secure"}

var authRe = regexp.MustCompile(`Accepted (?:password|publickey|keyboard-interactive/\S+) for (\S+) from (\S+)`)

const maxAuthChunk = 256 << 10

const editWindow = 60 * time.Second

type Deps struct {
	API   *gotgbot.Bot
	Cfg   *config.Config
	CfgMu *sync.RWMutex
	Store *storage.Storage
	Met   *metrics.Monitor
	OS    *detect.Info
	Sec   *security.Guard
	Geo   *geoip.Cache
	Log   *log.Logger
}

type rec struct {
	msgID    int64
	baseText string
	kb       [][]gotgbot.InlineKeyboardButton
	lastSent time.Time
	count    int
}

type Alerter struct {
	deps Deps

	mu   sync.Mutex
	recs map[string]*rec

	incMu    sync.Mutex
	incDate  string
	incCount int

	journalCursor string
	journalFailed bool
}

func New(d Deps) *Alerter {
	return &Alerter{deps: d, recs: make(map[string]*rec)}
}

func (a *Alerter) Run(ctx context.Context) {
	a.safeGo(ctx, "thresholdsLoop", a.thresholdsLoop)
	a.safeGo(ctx, "watchdogLoop", a.watchdogLoop)
	a.safeGo(ctx, "authLogLoop", a.authLogLoop)
	a.safeGo(ctx, "dailyLoop", a.dailyLoop)
}

func (a *Alerter) safeGo(ctx context.Context, name string, fn func(context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.deps.Log.Printf("PANIC %s: %v", name, r)
			}
		}()
		fn(ctx)
	}()
}

func (a *Alerter) thresholdsLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	cpuStrikes := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		snap, err := a.deps.Met.Snapshot()
		if err != nil {
			a.deps.Log.Printf("thresholdsLoop: снапшот метрик: %v", err)
			continue
		}
		a.deps.CfgMu.RLock()
		th := a.deps.Cfg.Thresholds
		a.deps.CfgMu.RUnlock()

		if snap.CPUPercent > th.CPUPercent {
			cpuStrikes++
		} else {
			cpuStrikes = 0
		}
		if cpuStrikes > 0 &&
			time.Duration(cpuStrikes)*30*time.Second >= time.Duration(th.CPUDurationMin)*time.Minute {
			durMin := float64(cpuStrikes) * 30 / 60
			text := fmt.Sprintf("🔥 <b>Высокая загрузка CPU</b>\n\nТекущее: %.1f%%\nПорог: %.0f%%\nДлительность: %.1f мин",
				snap.CPUPercent, th.CPUPercent, durMin)
			a.send(ctx, "cpu", text, kbRow("📋 Топ процессов", "st:procs:cpu"), false)
		}

		if snap.MemPercent > th.RAMPercent {
			text := fmt.Sprintf("🧠 <b>Высокое использование RAM</b>\n\nТекущее: %.1f%% (порог %.0f%%)\nЗанято: %s из %s",
				snap.MemPercent, th.RAMPercent, fmtGB(snap.MemUsed), fmtGB(snap.MemTotal))
			a.send(ctx, "ram", text, kbRow("📋 Топ процессов", "st:procs:ram"), false)
		}

		for _, d := range snap.Disks {
			if d.Percent <= th.DiskPercent {
				continue
			}
			text := fmt.Sprintf("💾 <b>Диск %s заполнен</b>\n\nЗанято: %.1f%% (порог %.0f%%)\nИспользовано: %s из %s",
				esc(d.Mount), d.Percent, th.DiskPercent, fmtGB(d.Used), fmtGB(d.Total))
			a.send(ctx, "disk:"+d.Mount, text, kbRow("💾 Диск", "disk:menu"), false)
		}

		if snap.Load1 > float64(snap.CPUCount) {
			text := fmt.Sprintf("⚠️ <b>Высокий Load Average</b>\n\nLoad1: %.2f при %d ядрах",
				snap.Load1, snap.CPUCount)
			a.send(ctx, "load", text, kbRow("📋 Топ процессов", "st:procs:cpu"), false)
		}
	}
}

func (a *Alerter) watchdogLoop(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.checkServices(ctx)
		a.checkPorts(ctx)
	}
}

func (a *Alerter) checkServices(ctx context.Context) {
	for unit, on := range a.deps.Store.WatchdogList() {
		if !on {
			continue
		}
		state := sysutil.ServiceState(ctx, unit)
		if state == "active" {
			continue
		}
		autoNote := ""
		if a.deps.Store.GetBool("watchdog_auto") {
			if _, err := sysutil.ServiceAction(ctx, "restart", unit); err != nil {
				a.deps.Log.Printf("watchdog: авторестарт %s не удался: %v", unit, err)
				autoNote = "\nАвторестарт: ❌ " + esc(errShort(err))
			} else {
				a.deps.Log.Printf("watchdog: %s перезапущен автоматически", unit)
				autoNote = "\nАвторестарт: ✅ успешно"
			}
		}
		text := fmt.Sprintf("⚠️ <b>Сервис %s не активен</b>\n\nСостояние: %s%s",
			esc(unit), esc(state), autoNote)
		a.send(ctx, "wd:"+unit, text, kbRow("🔄 Перезапустить", "alw:restart:"+unit), false)
	}
}

func (a *Alerter) checkPorts(ctx context.Context) {
	if !a.deps.Store.HasPorts() {
		return
	}
	ports, err := sysutil.ListeningPorts(ctx)
	if err != nil {
		a.deps.Log.Printf("watchdog: список портов: %v", err)
		return
	}
	snap := a.deps.Store.Ports()
	seen := make(map[string]bool, len(ports))
	for _, p := range ports {
		key := p.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := snap[key]; ok {
			continue
		}
		proc := p.Process
		if proc == "" {
			proc = "неизвестен"
		}
		text := fmt.Sprintf("⚠️ <b>Открыт новый порт: %d</b> (процесс: %s)", p.Port, esc(proc))
		a.send(ctx, "port:"+key, text, nil, false)
	}
}

func (a *Alerter) authLogLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.readAuthLog(ctx)
	}
}

func (a *Alerter) readAuthLog(ctx context.Context) {
	path := ""
	for _, p := range authFiles {
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if path == "" {

		a.readAuthJournal(ctx)
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	size := fi.Size()
	offset := a.deps.Store.GetInt("auth_offset")
	if size < offset {
		offset = 0
	}
	if size-offset > maxAuthChunk {
		offset = size - maxAuthChunk
	}
	if size == offset {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		a.deps.Log.Printf("authLog: открытие %s: %v", path, err)
		return
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		a.deps.Log.Printf("authLog: seek %s: %v", path, err)
		return
	}
	buf := make([]byte, size-offset)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		a.deps.Log.Printf("authLog: чтение %s: %v", path, err)
		return
	}
	buf = buf[:n]
	newOffset := offset + int64(n)

	if n > 0 && buf[n-1] != '\n' {
		if idx := bytes.LastIndexByte(buf, '\n'); idx >= 0 {
			buf = buf[:idx+1]
			newOffset = offset + int64(idx+1)
		} else {
			buf = nil
			newOffset = offset
		}
	}

	for _, line := range strings.Split(string(buf), "\n") {
		a.processAuthLine(ctx, line)
	}

	if newOffset != offset {
		if err := a.deps.Store.Update(func(st *storage.State) { st.AuthOffset = newOffset }); err != nil {
			a.deps.Log.Printf("authLog: сохранение offset: %v", err)
		}
	}
}

func (a *Alerter) processAuthLine(ctx context.Context, line string) {
	m := authRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	user, ip := m[1], m[2]
	geo := a.deps.Geo.Lookup(ctx, ip)
	geoPart := ""
	if geo != "" {
		geoPart = " (" + esc(geo) + ")"
	}
	text := fmt.Sprintf("🔓 <b>Вход: %s@%s</b>%s, %s",
		esc(user), esc(ip), geoPart, time.Now().Format("02.01.2006 15:04:05"))
	a.send(ctx, "auth:"+user+"@"+ip, text, nil, true)
}

func (a *Alerter) readAuthJournal(ctx context.Context) {
	if a.journalFailed {
		return
	}
	if a.journalCursor == "" {
		if !a.journalInit(ctx) {
			a.journalFailed = true
		}
		return
	}
	out, err := sysutil.Run(ctx, 10*time.Second, "journalctl", "_COMM=sshd",
		"--after-cursor", a.journalCursor, "--show-cursor", "-o", "cat", "--no-pager", "-q")
	if err != nil {

		a.journalCursor = ""
		if !a.journalInit(ctx) {
			a.journalFailed = true
		}
		return
	}
	if cur := parseJournalCursor(out); cur != "" {
		a.journalCursor = cur
	}
	for _, line := range strings.Split(out, "\n") {
		a.processAuthLine(ctx, line)
	}
}

func (a *Alerter) journalInit(ctx context.Context) bool {
	if !sysutil.Exists("journalctl") {
		return false
	}
	out, err := sysutil.Run(ctx, 10*time.Second, "journalctl", "_COMM=sshd",
		"-n", "0", "--show-cursor", "-o", "cat", "--no-pager", "-q")
	if err != nil {
		return false
	}
	a.journalCursor = parseJournalCursor(out)
	if a.journalCursor != "" {
		a.deps.Log.Printf("authLog: auth.log не найден, слежу за SSH-входами через journald")
	}
	return a.journalCursor != ""
}

func parseJournalCursor(out string) string {
	const marker = "-- cursor: "
	idx := strings.LastIndex(out, marker)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(out[idx+len(marker):])
}

func (a *Alerter) dailyLoop(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		today := now.Format("2006-01-02")
		a.maybeDigest(ctx, now, today)
		a.maybeAutoClean(ctx, today)
	}
}

func (a *Alerter) maybeDigest(ctx context.Context, now time.Time, today string) {
	a.deps.CfgMu.RLock()
	dg := a.deps.Cfg.Digest
	a.deps.CfgMu.RUnlock()
	if !dg.Enabled || now.Format("15:04") != dg.Time || a.deps.Store.GetString("last_digest") == today {
		return
	}

	snap, err := a.deps.Met.Snapshot()
	if err != nil {
		a.deps.Log.Printf("дайджест: снапшот метрик: %v", err)
		return
	}

	diskLine := "н/д"
	for _, d := range snap.Disks {
		if d.Mount == "/" {
			diskLine = fmt.Sprintf("свободно %s из %s (%.0f%% занято)", fmtGB(d.Total-d.Used), fmtGB(d.Total), d.Percent)
			break
		}
	}

	banned := "fail2ban недоступен"
	if sysutil.Exists("fail2ban-client") {
		out, err := sysutil.Run(ctx, 10*time.Second, "fail2ban-client", "status", "sshd")
		if err != nil {
			a.deps.Log.Printf("дайджест: fail2ban-client: %v", err)
			banned = "fail2ban недоступен"
		} else {
			banned = "н/д"
			for _, line := range strings.Split(out, "\n") {
				if !strings.Contains(line, "Currently banned") {
					continue
				}
				if idx := strings.LastIndex(line, ":"); idx >= 0 {
					banned = strings.TrimSpace(line[idx+1:])
				}
				break
			}
		}
	}

	text := fmt.Sprintf("📊 <b>Ежедневная сводка</b>\n\n🕐 Аптайм: %s\n📈 Load average: %.2f / %.2f / %.2f\n💾 Диск /: %s\n🚫 Забанено IP сейчас: %s\n🚨 Инцидентов за сутки: %d",
		formatUptime(snap.Uptime), snap.Load1, snap.Load5, snap.Load15, esc(diskLine), esc(banned), a.incidents())
	a.send(ctx, "digest", text, nil, false)

	if err := a.deps.Store.Update(func(st *storage.State) { st.LastDigest = today }); err != nil {
		a.deps.Log.Printf("дайджест: сохранение last_digest: %v", err)
	}
}

func (a *Alerter) maybeAutoClean(ctx context.Context, today string) {
	if !a.deps.Store.GetBool("auto_clean") {
		return
	}
	last := a.deps.Store.GetString("last_auto_clean")
	if last != "" {
		if t, err := time.Parse("2006-01-02", last); err == nil && time.Since(t) < 7*24*time.Hour {
			return
		}
	}

	type step struct {
		name string
		ok   bool
	}
	var steps []step
	run := func(name string, fn func() error) {
		err := fn()
		if err != nil {
			a.deps.Log.Printf("автоочистка: %s: %v", name, err)
		}
		steps = append(steps, step{name, err == nil})
	}

	run("journalctl --vacuum-size=100M", func() error {
		_, err := sysutil.Run(ctx, 2*time.Minute, "journalctl", "--vacuum-size=100M")
		return err
	})
	if a.deps.OS.IsDebianLike() {
		run("apt-get clean + autoremove", func() error {
			_, err := sysutil.AptClean(ctx)
			return err
		})
	}
	run("очистка /tmp (старше 7 дней)", func() error {
		_, err := sysutil.Run(ctx, 2*time.Minute, "sh", "-c", "find /tmp -type f -mtime +7 -delete")
		return err
	})

	var sb strings.Builder
	sb.WriteString("🧹 <b>Автоочистка выполнена</b>")
	for _, s := range steps {
		mark := "✅"
		if !s.ok {
			mark = "❌"
		}
		sb.WriteString(fmt.Sprintf("\n%s %s", mark, esc(s.name)))
	}
	a.send(ctx, "autoclean", sb.String(), nil, false)

	if err := a.deps.Store.Update(func(st *storage.State) { st.LastAutoClean = today }); err != nil {
		a.deps.Log.Printf("автоочистка: сохранение last_auto_clean: %v", err)
	}
}

func (a *Alerter) send(ctx context.Context, kind string, text string, kb [][]gotgbot.InlineKeyboardButton, critical bool) {
	a.deps.CfgMu.RLock()
	quiet := a.deps.Cfg.InQuietHours(time.Now())
	a.deps.CfgMu.RUnlock()
	if quiet && !critical {
		a.deps.Log.Printf("алерт %q пропущен (тихие часы)", kind)
		return
	}
	a.bumpIncidents()

	now := time.Now()
	for _, chatID := range a.deps.Sec.List() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		key := fmt.Sprintf("%s|%d", kind, chatID)

		a.mu.Lock()
		r, ok := a.recs[key]
		fresh := ok && now.Sub(r.lastSent) < editWindow
		if fresh {
			r.count++
			r.lastSent = now
		}
		a.mu.Unlock()

		if fresh {
			editText := fmt.Sprintf("%s\n\n🔁 Повторов: ×%d (последний: %s)",
				r.baseText, r.count, now.Format("15:04:05"))
			_, _, err := a.deps.API.EditMessageText(editText, &gotgbot.EditMessageTextOpts{
				ChatId:      chatID,
				MessageId:   r.msgID,
				ParseMode:   "HTML",
				ReplyMarkup: gotgbot.InlineKeyboardMarkup{InlineKeyboard: r.kb},
			})
			if err != nil && !strings.Contains(err.Error(), "message is not modified") {
				a.deps.Log.Printf("алерт %q: редактирование в чат %d: %v", kind, chatID, err)
			}
			continue
		}

		msg, err := a.deps.API.SendMessage(chatID, text, &gotgbot.SendMessageOpts{
			ParseMode:   "HTML",
			ReplyMarkup: gotgbot.InlineKeyboardMarkup{InlineKeyboard: kb},
		})
		if err != nil {
			a.deps.Log.Printf("алерт %q: отправка в чат %d: %v", kind, chatID, err)
			continue
		}
		a.mu.Lock()
		a.recs[key] = &rec{msgID: msg.MessageId, baseText: text, kb: kb, lastSent: now}
		a.mu.Unlock()
	}
}

func (a *Alerter) bumpIncidents() {
	a.incMu.Lock()
	defer a.incMu.Unlock()
	today := time.Now().Format("2006-01-02")
	if a.incDate != today {
		a.incDate = today
		a.incCount = 0
	}
	a.incCount++
}

func (a *Alerter) incidents() int {
	a.incMu.Lock()
	defer a.incMu.Unlock()
	if a.incDate != time.Now().Format("2006-01-02") {
		return 0
	}
	return a.incCount
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func kbRow(text, data string) [][]gotgbot.InlineKeyboardButton {
	return [][]gotgbot.InlineKeyboardButton{{{Text: text, CallbackData: data}}}
}

func fmtGB(b uint64) string {
	return fmt.Sprintf("%.1f ГБ", float64(b)/float64(uint64(1)<<30))
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d дн %d ч %d мин", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%d ч %d мин", hours, mins)
	default:
		return fmt.Sprintf("%d мин", mins)
	}
}

func errShort(err error) string {
	var ce *sysutil.CmdError
	if errors.As(err, &ce) {
		return ce.Short()
	}
	return err.Error()
}
