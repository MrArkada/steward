package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/metrics"
	"serverbot/internal/sysutil"
)

func statusText(env *Env) string {
	snap, err := env.Met.Snapshot()
	if err != nil {
		return fmt.Sprintf("⚠️ Метрики недоступны на этой ОС:\n<code>%s</code>", Esc(err.Error()))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<b>📊 Статус сервера</b> · <code>%s</code>\n", Esc(snap.Hostname))
	fmt.Fprintf(&b, "<b>CPU:</b> %.0f%% [%s] (%d ядер)\n", snap.CPUPercent, Bar(snap.CPUPercent, 10), snap.CPUCount)
	fmt.Fprintf(&b, "<b>RAM:</b> %s / %s (%.0f%%)\n     [%s]\n",
		FmtBytes(snap.MemUsed), FmtBytes(snap.MemTotal), snap.MemPercent, Bar(snap.MemPercent, 14))
	if snap.SwapTotal > 0 {
		swapPct := float64(snap.SwapUsed) / float64(snap.SwapTotal) * 100
		fmt.Fprintf(&b, "<b>Swap:</b> %s / %s (%.0f%%)\n", FmtBytes(snap.SwapUsed), FmtBytes(snap.SwapTotal), swapPct)
	}
	fmt.Fprintf(&b, "<b>Load:</b> %.2f %.2f %.2f\n", snap.Load1, snap.Load5, snap.Load15)
	fmt.Fprintf(&b, "<b>Uptime:</b> %s\n", FmtDur(snap.Uptime))
	fmt.Fprintf(&b, "<b>Сеть:</b> ↓%s ↑%s (за сессию: ↓%s ↑%s)\n",
		FmtBytes(snap.NetRX), FmtBytes(snap.NetTX), FmtBytes(snap.SessionRX), FmtBytes(snap.SessionTX))
	fmt.Fprintf(&b, "<b>Процессы:</b> %d\n", snap.Processes)
	if len(snap.Disks) > 0 {
		b.WriteString("<b>Диски:</b>\n")
		for _, d := range snap.Disks {
			fmt.Fprintf(&b, "<code>%-14s</code> %3.0f%% [%s] %s/%s\n",
				Esc(d.Mount), d.Percent, Bar(d.Percent, 8), FmtBytes(d.Used), FmtBytes(d.Total))
		}
	}
	return b.String()
}

func statusKB(live bool) [][]gotgbot.InlineKeyboardButton {
	if live {
		return [][]gotgbot.InlineKeyboardButton{
			Row(Btn("⏹ Стоп", "st:livestop")),
		}
	}
	return [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🔄 Обновить", "st:refresh"), Btn("🔴 Live-режим", "st:live")),
		Row(Btn("📋 Процессы (CPU)", "st:procs:cpu"), Btn("📋 Процессы (RAM)", "st:procs:ram")),
		BackRow("menu:main"),
	}
}

func handleStatus(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu", "refresh":
		return Edit(env, cq, statusText(env), statusKB(false))

	case "live":

		key := LiveKey(cq)
		if key == "" {
			return nil
		}
		api := env.API
		chatID := cq.Message.GetChat().Id
		msgID := cq.Message.GetMessageId()
		env.Live.Start(key, func(ctx context.Context) {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_, _, err := api.EditMessageText(statusText(env), &gotgbot.EditMessageTextOpts{
						ChatId:      chatID,
						MessageId:   msgID,
						ParseMode:   "HTML",
						ReplyMarkup: gotgbot.InlineKeyboardMarkup{InlineKeyboard: statusKB(true)},
					})
					if err != nil && !strings.Contains(err.Error(), "message is not modified") {
						return
					}
				}
			}
		})
		return Edit(env, cq, statusText(env), statusKB(true))

	case "livestop":
		env.Live.Stop(LiveKey(cq))
		return Edit(env, cq, statusText(env), statusKB(false))

	case "procs":
		by := "cpu"
		if len(parts) > 2 {
			by = parts[2]
		}
		return procsView(env, cq, by, 15)

	case "ask":

		if len(parts) > 3 && parts[2] == "kill" {
			pid := parts[3]
			return Edit(env, cq,
				fmt.Sprintf("⚠️ Убить процесс <code>%s</code> (PID %s)?", Esc(procName(env, pid)), Esc(pid)),
				ConfirmKB("st:do:kill:"+pid, "st:procs:cpu"))
		}

	case "do":
		if len(parts) > 3 && parts[2] == "kill" {
			pid := parts[3]
			env.Audit.Log(cq.From.Id, "kill pid "+pid)
			_, err := sysutil.Run(env.RootCtx, 5*time.Second, "kill", "-9", pid)
			if err != nil {
				Fail(env, cq, "убить процесс "+pid, err, "st:procs:cpu")
				return nil
			}
			return procsView(env, cq, "cpu", 15)
		}
	}
	return Edit(env, cq, statusText(env), statusKB(false))
}

func procsView(env *Env, cq *gotgbot.CallbackQuery, by string, n int) error {
	var (
		procs []metrics.ProcInfo
		err   error
		title string
	)
	if by == "ram" {
		procs, err = env.Met.TopMem(n)
		title = "📋 Топ-" + fmt.Sprint(n) + " процессов по RAM"
	} else {
		procs, err = env.Met.TopCPU(n)
		title = "📋 Топ-" + fmt.Sprint(n) + " процессов по CPU"
	}
	if err != nil {
		Fail(env, cq, "получить список процессов", err, "st:menu")
		return nil
	}
	var b strings.Builder
	b.WriteString("<b>" + title + "</b>\n")
	for _, p := range procs {
		fmt.Fprintf(&b, "<code>%-6d %-20s</code> CPU %5.1f%% · RAM %4.1f%% (%s)\n",
			p.PID, Esc(Trunc(p.Name, 20)), p.CPUPercent, p.MemPercent, FmtBytes(p.RSS))
	}
	kb := [][]gotgbot.InlineKeyboardButton{}

	for i, p := range procs {
		kb = append(kb, Row(Btn(fmt.Sprintf("☠️ %d: %s", i+1, Trunc(p.Name, 24)),
			fmt.Sprintf("st:ask:kill:%d", p.PID))))
	}
	kb = append(kb,
		Row(Btn("🔄 Обновить", "st:procs:"+by)),
		BackRow("st:menu"),
	)
	return Edit(env, cq, b.String(), kb)
}

func procName(env *Env, pid string) string {
	procs, err := env.Met.TopMem(100)
	if err != nil {
		return pid
	}
	for _, p := range procs {
		if fmt.Sprint(p.PID) == pid {
			return p.Name
		}
	}
	return pid
}
