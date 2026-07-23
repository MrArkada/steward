package bot

import (
	"log"
	"runtime/debug"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"serverbot/internal/handlers"
)

func New(api *gotgbot.Bot, env *handlers.Env, logger *log.Logger) *ext.Updater {
	dp := ext.NewDispatcher(&ext.DispatcherOpts{
		MaxRoutines: 4,
		Error: func(b *gotgbot.Bot, ctx *ext.Context, err error) ext.DispatcherAction {
			logger.Printf("ошибка обработчика: %v", err)
			return ext.DispatcherActionContinueGroups
		},
		Panic: func(b *gotgbot.Bot, ctx *ext.Context, r any) {
			logger.Printf("PANIC в обработчике: %v\n%s", r, debug.Stack())
		},
		UnhandledErrFunc: func(err error) {
			logger.Printf("dispatcher: %v", err)
		},
	})
	handlers.Register(dp, env)
	return ext.NewUpdater(dp, &ext.UpdaterOpts{
		UnhandledErrFunc: func(err error) {
			logger.Printf("updater: %v", err)
		},
	})
}
