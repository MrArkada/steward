package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"

	"serverbot/internal/alerts"
	"serverbot/internal/bot"
	"serverbot/internal/config"
	"serverbot/internal/detect"
	"serverbot/internal/geoip"
	"serverbot/internal/handlers"
	"serverbot/internal/logging"
	"serverbot/internal/metrics"
	"serverbot/internal/security"
	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "путь к файлу конфигурации")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка конфигурации: %v\n", err)
		os.Exit(1)
	}

	logger, rot, err := logging.New(cfg.Paths.LogFile, 10<<20, 3)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось открыть лог-файл: %v\n", err)
		os.Exit(1)
	}
	defer rot.Close()

	audit, err := security.NewAuditLogger(cfg.Paths.AuditFile)
	if err != nil {
		logger.Printf("аудит-лог недоступен: %v", err)
	} else {
		defer audit.Close()
	}

	osInfo := detect.Detect()
	logger.Printf("ОС: %s (PM: %s, systemd: %v, arch: %s)", osInfo.Name, osInfo.PM, osInfo.HasSystemd, osInfo.Arch)

	store, err := storage.Load(cfg.Paths.StateFile)
	if err != nil {
		logger.Printf("state-файл не загружен, начинаем с чистого: %v", err)
		store, _ = storage.Load("")
	}

	met := metrics.New(4 * time.Second)
	guard := security.NewGuard(cfg.AllowedUsers)
	geo := geoip.New(store, cfg.GeoIPEnabled)

	api, err := gotgbot.NewBot(cfg.Token, nil)
	if err != nil {
		logger.Printf("FATAL: не удалось создать бота: %v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfgMu := &sync.RWMutex{}
	env := &handlers.Env{
		API:        api,
		Cfg:        cfg,
		CfgMu:      cfgMu,
		SaveConfig: cfg.Save,
		Store:      store,
		Met:        met,
		OS:         osInfo,
		Sec:        guard,
		Audit:      audit,
		Geo:        geo,
		Log:        logger,
		Pending:    handlers.NewPendingStore(),
		Live:       handlers.NewLiveManager(logger),
		RootCtx:    ctx,
		StartTime:  time.Now(),
	}

	updater := bot.New(api, env, logger)

	alerter := alerts.New(alerts.Deps{
		API:   api,
		Cfg:   cfg,
		CfgMu: cfgMu,
		Store: store,
		Met:   met,
		OS:    osInfo,
		Sec:   guard,
		Geo:   geo,
		Log:   logger,
	})
	alerter.Run(ctx)

	reportPendingReboot(ctx, env)

	autoSetup(ctx, env)

	logger.Println("запуск long polling")
	err = updater.StartPolling(api, &ext.PollingOpts{
		DropPendingUpdates:    true,
		EnableWebhookDeletion: true,
		GetUpdatesOpts: &gotgbot.GetUpdatesOpts{
			Timeout:     30,
			RequestOpts: &gotgbot.RequestOpts{Timeout: 35 * time.Second},
		},
	})
	if err != nil {
		logger.Printf("FATAL: polling не запустился: %v", err)
		os.Exit(1)
	}

	go func() {
		<-ctx.Done()
		logger.Println("получен сигнал, останавливаюсь...")
		if err := updater.Stop(); err != nil {
			logger.Printf("остановка updater: %v", err)
		}
	}()

	updater.Idle()

	env.Live.StopAll()
	if err := store.Save(); err != nil {
		logger.Printf("не удалось сохранить state: %v", err)
	}
	logger.Println("остановлено")
}

func autoSetup(ctx context.Context, env *handlers.Env) {
	if env.Store.GetBool("auto_setup_v2") {
		return
	}
	if !env.OS.IsDebianLike() {

		_ = env.Store.Update(func(st *storage.State) { st.AutoSetupV2 = true })
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				env.Log.Printf("PANIC autoSetup: %v", r)
			}
		}()

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		var report strings.Builder
		report.WriteString("🤵 Steward: первичная настройка защиты сервера\n")

		f2bOK, ufwOK := true, true
		if err := sysutil.SetupFail2ban(ctx, env.OS.PM); err != nil {
			f2bOK = false
			env.Log.Printf("auto-setup fail2ban: %v", err)
			report.WriteString("\n❌ Fail2ban: " + firstLine(err.Error()))
		} else {
			report.WriteString("\n✅ Fail2ban: установлен и запущен (jail sshd, backend=systemd)")
		}

		if err := sysutil.SetupUFW(ctx, env.OS.PM); err != nil {
			ufwOK = false
			env.Log.Printf("auto-setup ufw: %v", err)
			report.WriteString("\n❌ Firewall (UFW): " + firstLine(err.Error()))
		} else {
			report.WriteString(fmt.Sprintf("\n✅ Firewall (UFW): deny incoming, allow outgoing, SSH-порт %d открыт", sysutil.SSHPort()))
		}

		if f2bOK && ufwOK {
			_ = env.Store.Update(func(st *storage.State) { st.AutoSetupV2 = true })
			env.Audit.Log(0, "auto-setup fail2ban+ufw")
			report.WriteString("\n\nУправление: /menu")
		} else {

			report.WriteString("\n\n⚠️ Часть шагов не удалась — повторю при следующем запуске. " +
				"При ошибках «signal: killed» не хватает памяти: создайте swap (🚀 Оптимизация → 💾 Swap).")
		}
		for _, uid := range env.Sec.List() {
			if _, err := env.API.SendMessage(uid, report.String(), nil); err != nil {
				env.Log.Printf("auto-setup: отправка отчёта %d: %v", uid, err)
			}
		}
	}()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func reportPendingReboot(ctx context.Context, env *handlers.Env) {
	rf := env.Store.PendingReboot()
	if rf == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				env.Log.Printf("PANIC reportPendingReboot: %v", r)
			}
		}()

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		downtime := time.Since(rf.At).Round(time.Second)
		text := fmt.Sprintf("✅ <b>Сервер поднялся за %s</b>\n\nСервисы:", downtime)
		for _, unit := range sysutil.KnownServices() {
			if !sysutil.UnitExists(ctx, unit) {
				continue
			}
			state := sysutil.ServiceState(ctx, unit)
			mark := "✅"
			if state != "active" {
				mark = "❌"
			}
			text += fmt.Sprintf("\n%s %s — %s", mark, unit, state)
		}
		if _, err := env.API.SendMessage(rf.ChatID, text, &gotgbot.SendMessageOpts{ParseMode: "HTML"}); err != nil {
			env.Log.Printf("отчёт о перезагрузке: %v", err)
		}

		_ = env.Store.Update(func(st *storage.State) { st.PendingReboot = nil })
	}()
}
