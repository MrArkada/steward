package handlers

import (
	"context"
	"fmt"
	"log"
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

type Env struct {
	API        *gotgbot.Bot
	Cfg        *config.Config
	CfgMu      *sync.RWMutex
	SaveConfig func() error
	Store      *storage.Storage
	Met        *metrics.Monitor
	OS         *detect.Info
	Sec        *security.Guard
	Audit      *security.AuditLogger
	Geo        *geoip.Cache
	Log        *log.Logger
	Pending    *PendingStore
	Live       *LiveManager
	RootCtx    context.Context
	StartTime  time.Time
}

func Btn(text, data string) gotgbot.InlineKeyboardButton {
	return gotgbot.InlineKeyboardButton{Text: text, CallbackData: data}
}

func Row(btns ...gotgbot.InlineKeyboardButton) []gotgbot.InlineKeyboardButton { return btns }

func BackRow(to string) []gotgbot.InlineKeyboardButton { return Row(Btn("⬅️ Назад", to)) }

func ConfirmKB(doData, backData string) [][]gotgbot.InlineKeyboardButton {
	return [][]gotgbot.InlineKeyboardButton{
		Row(Btn("✅ Подтвердить", doData), Btn("❌ Отмена", backData)),
	}
}

func Esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

var byteUnits = []string{"К", "М", "Г", "Т", "П"}

func FmtBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dБ", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < len(byteUnits)-1; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%s", float64(n)/float64(div), byteUnits[exp])
}

func FmtDur(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "менее 1м"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dд %dч %dм", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dч %dм", hours, mins)
	default:
		return fmt.Sprintf("%dм", mins)
	}
}

func Trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func notModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

func Edit(env *Env, cq *gotgbot.CallbackQuery, text string, kb [][]gotgbot.InlineKeyboardButton) error {
	if cq.Message == nil {
		return nil
	}
	_, _, err := env.API.EditMessageText(text, &gotgbot.EditMessageTextOpts{
		ChatId:      cq.Message.GetChat().Id,
		MessageId:   cq.Message.GetMessageId(),
		ParseMode:   "HTML",
		ReplyMarkup: gotgbot.InlineKeyboardMarkup{InlineKeyboard: kb},
	})
	if notModified(err) {
		return nil
	}
	return err
}

func SendHTML(env *Env, chatID int64, text string, kb [][]gotgbot.InlineKeyboardButton) (*gotgbot.Message, error) {
	return env.API.SendMessage(chatID, text, &gotgbot.SendMessageOpts{
		ParseMode:   "HTML",
		ReplyMarkup: &gotgbot.InlineKeyboardMarkup{InlineKeyboard: kb},
	})
}

func SendDoc(env *Env, chatID int64, name string, data []byte, caption string) error {
	_, err := env.API.SendDocument(chatID, gotgbot.InputFileByReader(name, strings.NewReader(string(data))),
		&gotgbot.SendDocumentOpts{Caption: caption})
	return err
}

func Unsupported(env *Env, cq *gotgbot.CallbackQuery, feature string) error {
	return Edit(env, cq,
		fmt.Sprintf("❌ %s\n\nНе поддерживается на этой ОС (%s).", Esc(feature), Esc(env.OS.Name)),
		[][]gotgbot.InlineKeyboardButton{BackRow("menu:main")})
}

func Fail(env *Env, cq *gotgbot.CallbackQuery, what string, err error, backTo string) {
	env.Log.Printf("FAIL %s: %v", what, err)
	short := "внутренняя ошибка"
	if ce, ok := sysErr(err); ok {
		short = ce.Short()
	}
	_ = Edit(env, cq, fmt.Sprintf("⚠️ Не удалось %s:\n<code>%s</code>", what, Esc(short)),
		[][]gotgbot.InlineKeyboardButton{BackRow(backTo)})
}

func sysErr(err error) (*sysutil.CmdError, bool) {
	for err != nil {
		if ce, ok := err.(*sysutil.CmdError); ok {
			return ce, true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return nil, false
}

type PendingAction struct {
	Kind      string
	CreatedAt time.Time
}

type PendingStore struct {
	mu sync.Mutex
	m  map[int64]*PendingAction
}

func NewPendingStore() *PendingStore {
	return &PendingStore{m: make(map[int64]*PendingAction)}
}

func (p *PendingStore) Set(userID int64, kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gc()
	p.m[userID] = &PendingAction{Kind: kind, CreatedAt: time.Now()}
}

func (p *PendingStore) Take(userID int64) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.m[userID]
	if !ok {
		return "", false
	}
	delete(p.m, userID)
	if time.Since(a.CreatedAt) > 10*time.Minute {
		return "", false
	}
	return a.Kind, true
}

func (p *PendingStore) gc() {
	for id, a := range p.m {
		if time.Since(a.CreatedAt) > 10*time.Minute {
			delete(p.m, id)
		}
	}
}

type LiveManager struct {
	mu  sync.Mutex
	m   map[string]context.CancelFunc
	log *log.Logger
}

func NewLiveManager(logger *log.Logger) *LiveManager {
	return &LiveManager{m: make(map[string]context.CancelFunc), log: logger}
}

func (l *LiveManager) Start(key string, fn func(ctx context.Context)) {
	l.mu.Lock()
	if cancel, ok := l.m[key]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.m[key] = cancel
	l.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				l.log.Printf("PANIC live %s: %v", key, r)
			}
			l.mu.Lock()
			delete(l.m, key)
			l.mu.Unlock()
			cancel()
		}()
		fn(ctx)
	}()
}

func (l *LiveManager) Stop(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cancel, ok := l.m[key]
	if ok {
		cancel()
		delete(l.m, key)
	}
	return ok
}

func (l *LiveManager) StopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, cancel := range l.m {
		cancel()
		delete(l.m, k)
	}
}

func LiveKey(cq *gotgbot.CallbackQuery) string {
	if cq.Message == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", cq.Message.GetChat().Id, cq.Message.GetMessageId())
}
