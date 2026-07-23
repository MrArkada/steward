package handlers

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	tghandlers "github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"serverbot/internal/sysutil"
)

const debounceWindow = 1500 * time.Millisecond

type router struct {
	env *Env
	mu  sync.Mutex

	presses map[string]time.Time
}

func Register(dp *ext.Dispatcher, env *Env) {
	r := &router{env: env, presses: make(map[string]time.Time)}
	dp.AddHandler(tghandlers.NewCallback(func(cq *gotgbot.CallbackQuery) bool { return true }, r.onCallback))
	dp.AddHandler(tghandlers.NewMessage(func(m *gotgbot.Message) bool { return true }, r.onMessage))
}

var callbackHandlers = map[string]func(env *Env, cq *gotgbot.CallbackQuery, parts []string) error{
	"menu": handleMenu,
	"st":   handleStatus,
	"f2b":  handleF2B,
	"fw":   handleFW,
	"svc":  handleSvc,
	"disk": handleDisk,
	"opt":  handleOpt,
	"sec":  handleSec,
	"upd":  handleUpd,
	"acc":  handleAcc,
	"pwr":  handlePwr,
	"set":  handleSet,
}

var textHandlers = map[string]func(env *Env, msg *gotgbot.Message, parts []string) error{
	"f2b": handleF2BText,
	"fw":  handleFWText,
	"sec": handleSecText,
	"acc": handleAccText,
	"set": handleSetText,
}

func (r *router) onCallback(b *gotgbot.Bot, ctx *ext.Context) (err error) {
	env := r.env
	cq := ctx.CallbackQuery
	if cq == nil {
		return nil
	}

	if !env.Sec.Allowed(cq.From.Id) {
		return nil
	}

	defer func() {
		if rec := recover(); rec != nil {
			env.Log.Printf("PANIC callback %q: %v", cq.Data, rec)
			err = nil
		}
	}()

	if r.debounced(cq.From.Id, cq.Data) {
		_, _ = cq.Answer(b, nil)
		return nil
	}

	parts := strings.Split(cq.Data, ":")

	if parts[0] == "alw" && len(parts) == 3 && parts[1] == "restart" {
		unit := parts[2]
		env.Audit.Log(cq.From.Id, "watchdog restart "+unit)
		_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "🔄 Перезапускаю " + unit + "..."})
		if _, err := sysutil.ServiceAction(env.RootCtx, "restart", unit); err != nil {
			env.Log.Printf("watchdog restart %s: %v", unit, err)
			_, _ = cq.Answer(b, &gotgbot.AnswerCallbackQueryOpts{Text: "⚠️ Не удалось перезапустить " + unit, ShowAlert: true})
		}
		return nil
	}

	if h, ok := callbackHandlers[parts[0]]; ok {
		if err := h(env, cq, parts); err != nil {
			env.Log.Printf("обработчик %q: %v", cq.Data, err)
		}
	}

	_, _ = cq.Answer(b, nil)
	return nil
}

func (r *router) onMessage(b *gotgbot.Bot, ctx *ext.Context) (err error) {
	env := r.env
	msg := ctx.EffectiveMessage
	if msg == nil || msg.From == nil {
		return nil
	}
	userID := msg.From.Id
	if !env.Sec.Allowed(userID) {
		return nil
	}
	defer func() {
		if rec := recover(); rec != nil {
			env.Log.Printf("PANIC message: %v", rec)
			err = nil
		}
	}()

	if kind, ok := env.Pending.Take(userID); ok {
		parts := strings.Split(kind, ":")
		if h, ok := textHandlers[parts[0]]; ok {
			if err := h(env, msg, parts); err != nil {
				env.Log.Printf("text-обработчик %q: %v", kind, err)
			}
			return nil
		}
	}

	text := strings.TrimSpace(msg.GetText())
	switch {
	case strings.HasPrefix(text, "/start"), strings.HasPrefix(text, "/menu"):
		return sendMainMenu(env, msg.Chat.Id)
	default:
		_, err := env.API.SendMessage(msg.Chat.Id, "Используйте /menu для открытия панели управления.", nil)
		return err
	}
}

func (r *router) debounced(userID int64, data string) bool {
	key := strconv.FormatInt(userID, 10) + "|" + data
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.presses) > 512 {
		cutoff := time.Now().Add(-time.Minute)
		for k, t := range r.presses {
			if t.Before(cutoff) {
				delete(r.presses, k)
			}
		}
	}
	if t, ok := r.presses[key]; ok && time.Since(t) < debounceWindow {
		return true
	}
	r.presses[key] = time.Now()
	return false
}
