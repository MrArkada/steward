package handlers

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"serverbot/internal/storage"
	"serverbot/internal/sysutil"
)

type sysctlPreset struct {
	Title string
	Desc  string
	Lines []string
}

var sysctlPresets = map[string]sysctlPreset{
	"web": {
		Title: "🌐 Веб-сервер",
		Desc: "Тюнинг под высокую нагрузку HTTP/прокси:\n" +
			"• <code>net.core.somaxconn=65535</code> — очередь входящих соединений\n" +
			"• <code>net.ipv4.tcp_max_syn_backlog=8192</code> — очередь полуоткрытых SYN\n" +
			"• <code>fs.file-max=2097152</code> — лимит открытых файлов\n" +
			"• <code>net.ipv4.ip_local_port_range=10240 65535</code> — больше портов для исходящих\n" +
			"• <code>vm.swappiness=10</code> — реже выгружать память в swap",
		Lines: []string{
			"net.core.somaxconn=65535",
			"net.ipv4.tcp_max_syn_backlog=8192",
			"fs.file-max=2097152",
			"net.ipv4.ip_local_port_range=10240 65535",
			"vm.swappiness=10",
		},
	},
	"db": {
		Title: "🗄 База данных",
		Desc: "Тюнинг под СУБД (PostgreSQL/MySQL):\n" +
			"• <code>vm.swappiness=1</code> — почти не использовать swap\n" +
			"• <code>vm.dirty_ratio=15</code> — порог «грязных» страниц в памяти\n" +
			"• <code>vm.dirty_background_ratio=5</code> — фоновая запись на диск\n" +
			"• <code>fs.file-max=2097152</code> — лимит открытых файлов\n" +
			"• <code>net.core.somaxconn=1024</code> — очередь входящих соединений",
		Lines: []string{
			"vm.swappiness=1",
			"vm.dirty_ratio=15",
			"vm.dirty_background_ratio=5",
			"fs.file-max=2097152",
			"net.core.somaxconn=1024",
		},
	},
	"vpn": {
		Title: "🛡 VPN",
		Desc: "Тюнинг для VPN-шлюза (WireGuard/OpenVPN):\n" +
			"• <code>net.ipv4.ip_forward=1</code> — маршрутизация IPv4\n" +
			"• <code>net.ipv6.conf.all.forwarding=1</code> — маршрутизация IPv6\n" +
			"• <code>net.core.default_qdisc=fq</code> — планировщик пакетов\n" +
			"• <code>net.ipv4.tcp_congestion_control=bbr</code> — алгоритм BBR",
		Lines: []string{
			"net.ipv4.ip_forward=1",
			"net.ipv6.conf.all.forwarding=1",
			"net.core.default_qdisc=fq",
			"net.ipv4.tcp_congestion_control=bbr",
		},
	},
}

var presetOrder = []string{"web", "db", "vpn"}

func optMenu(env *Env) (string, [][]gotgbot.InlineKeyboardButton) {
	cc := "неизвестно"
	if data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control"); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			cc = s
		}
	}
	auto := "выкл"
	if env.Store.GetBool("auto_clean") {
		auto = "вкл"
	}
	text := fmt.Sprintf("<b>🚀 Оптимизация</b>\n\nCongestion control: <code>%s</code>\nАвтоочистка по расписанию: <b>%s</b>",
		Esc(cc), auto)
	kb := [][]gotgbot.InlineKeyboardButton{
		Row(Btn("🚀 Включить BBR", "opt:ask:bbr")),
		Row(Btn("📦 Пресеты sysctl", "opt:presets")),
		Row(Btn("💾 Создать swap-файл", "opt:swap")),
		Row(Btn("🗞 Ограничить journald (100M)", "opt:ask:journald")),
		Row(Btn("🧹 Автоочистка по расписанию", "opt:autoclean")),
		BackRow("menu:main"),
	}
	return text, kb
}

func handleOpt(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	act := "menu"
	if len(parts) > 1 {
		act = parts[1]
	}
	switch act {
	case "menu":
		text, kb := optMenu(env)
		return Edit(env, cq, text, kb)

	case "presets":

		var b strings.Builder
		b.WriteString("<b>📦 Пресеты sysctl</b>\n\nГотовые наборы параметров ядра под типовую нагрузку.\n")
		for _, name := range presetOrder {
			p := sysctlPresets[name]
			fmt.Fprintf(&b, "\n<b>%s</b>\n%s\n", p.Title, p.Desc)
		}
		kb := [][]gotgbot.InlineKeyboardButton{}
		for _, name := range presetOrder {
			kb = append(kb, Row(Btn(sysctlPresets[name].Title, "opt:ask:preset:"+name)))
		}
		kb = append(kb, BackRow("opt:menu"))
		return Edit(env, cq, b.String(), kb)

	case "swap":

		var b strings.Builder
		b.WriteString("<b>💾 Swap</b>\n\n")
		if out, err := sysutil.Run(env.RootCtx, 10*time.Second, "swapon", "--show", "--noheadings"); err == nil && strings.TrimSpace(out) != "" {
			fmt.Fprintf(&b, "Активен:\n<pre>%s</pre>\n", Esc(strings.TrimSpace(out)))
		} else {
			b.WriteString("Сейчас swap не активен.\n\n")
		}
		if _, err := os.Stat("/swapfile"); err == nil {
			b.WriteString("Файл <code>/swapfile</code> существует.")
		} else {
			b.WriteString("Выберите размер файла подкачки <code>/swapfile</code>:")
		}
		kb := [][]gotgbot.InlineKeyboardButton{
			Row(Btn("1 ГБ", "opt:ask:swap:1"), Btn("2 ГБ", "opt:ask:swap:2"), Btn("4 ГБ", "opt:ask:swap:4")),
			Row(Btn("❌ Отключить и удалить swap", "opt:ask:swapoff")),
			BackRow("opt:menu"),
		}
		return Edit(env, cq, b.String(), kb)

	case "autoclean":

		if err := env.Store.Update(func(st *storage.State) { st.AutoClean = !st.AutoClean }); err != nil {
			Fail(env, cq, "переключить автоочистку", err, "opt:menu")
			return nil
		}
		text, kb := optMenu(env)
		return Edit(env, cq, text, kb)

	case "ask":
		return optAsk(env, cq, parts)

	case "do":
		return optDo(env, cq, parts)
	}
	text, kb := optMenu(env)
	return Edit(env, cq, text, kb)
}

func optAsk(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) >= 3 {
		switch parts[2] {
		case "bbr":
			return Edit(env, cq,
				"<b>🚀 Включить BBR?</b>\n\n"+
					"Будет записан файл <code>/etc/sysctl.d/99-serverbot-bbr.conf</code>:\n"+
					"<code>net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr</code>\n\n"+
					"Затем выполнится <code>sysctl --system</code>. BBR ускоряет TCP на медленных и потерянных каналах.",
				ConfirmKB("opt:do:bbr", "opt:menu"))

		case "preset":
			if len(parts) >= 4 {
				if p, ok := sysctlPresets[parts[3]]; ok {
					return Edit(env, cq,
						fmt.Sprintf("<b>📦 Применить пресет %s?</b>\n\n%s\n\n"+
							"Будет записан файл <code>/etc/sysctl.d/99-serverbot-%s.conf</code> и выполнено <code>sysctl --system</code>.",
							p.Title, p.Desc, Esc(parts[3])),
						ConfirmKB("opt:do:preset:"+parts[3], "opt:presets"))
				}
			}

		case "swap":
			if len(parts) >= 4 && validSwapSize(parts[3]) {
				return Edit(env, cq,
					fmt.Sprintf("<b>💾 Создать swap-файл %s ГБ?</b>\n\n"+
						"Будет создан <code>/swapfile</code> (fallocate), отформатирован (mkswap) и включён (swapon).\n"+
						"В <code>/etc/fstab</code> добавится строка для автоподключения при загрузке.", Esc(parts[3])),
					ConfirmKB("opt:do:swap:"+parts[3], "opt:swap"))
			}

		case "swapoff":
			return Edit(env, cq,
				"<b>❌ Отключить и удалить swap?</b>\n\n"+
					"Будет выполнено: <code>swapoff</code>, удалена строка из <code>/etc/fstab</code> "+
					"и удалён файл <code>/swapfile</code>.\n\n"+
					"⚠️ При нехватке RAM без swap возможны падения процессов (OOM).",
				ConfirmKB("opt:do:swapoff", "opt:swap"))

		case "journald":
			return Edit(env, cq,
				"<b>🗞 Ограничить журнал systemd?</b>\n\n"+
					"Будет создан <code>/etc/systemd/journald.conf.d/99-serverbot.conf</code> с лимитом "+
					"<code>SystemMaxUse=100M</code>, затем перезапущен <code>systemd-journald</code>.",
				ConfirmKB("opt:do:journald", "opt:menu"))
		}
	}
	text, kb := optMenu(env)
	return Edit(env, cq, text, kb)
}

func optDo(env *Env, cq *gotgbot.CallbackQuery, parts []string) error {
	if len(parts) < 3 {
		text, kb := optMenu(env)
		return Edit(env, cq, text, kb)
	}

	_, _ = cq.Answer(env.API, &gotgbot.AnswerCallbackQueryOpts{Text: "⏳ Выполняю..."})
	switch parts[2] {
	case "bbr":
		env.Audit.Log(cq.From.Id, "enable BBR")
		conf := "net.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n"
		if err := os.WriteFile("/etc/sysctl.d/99-serverbot-bbr.conf", []byte(conf), 0o644); err != nil {
			Fail(env, cq, "записать конфиг BBR", err, "opt:menu")
			return nil
		}
		if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "sysctl", "--system"); err != nil {
			Fail(env, cq, "применить sysctl --system", err, "opt:menu")
			return nil
		}

		cur := ""
		if data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control"); err == nil {
			cur = strings.TrimSpace(string(data))
		}
		var b strings.Builder
		b.WriteString("<b>🚀 BBR</b>\n\n" +
			"✅ Конфиг записан: <code>/etc/sysctl.d/99-serverbot-bbr.conf</code>\n" +
			"✅ Параметры применены (<code>sysctl --system</code>)\n")
		if cur == "bbr" {
			b.WriteString("Текущий congestion control: <code>bbr</code> — всё работает.")
		} else {
			fmt.Fprintf(&b, "⚠️ Текущий congestion control: <code>%s</code> — BBR включится после перезагрузки (модуль ядра может быть недоступен).", Esc(cur))
		}
		return Edit(env, cq, b.String(), [][]gotgbot.InlineKeyboardButton{BackRow("opt:menu")})

	case "preset":
		if len(parts) >= 4 {
			if p, ok := sysctlPresets[parts[3]]; ok {
				env.Audit.Log(cq.From.Id, "apply sysctl preset "+parts[3])
				conf := strings.Join(p.Lines, "\n") + "\n"
				path := "/etc/sysctl.d/99-serverbot-" + parts[3] + ".conf"
				if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
					Fail(env, cq, "записать пресет "+parts[3], err, "opt:presets")
					return nil
				}
				if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "sysctl", "--system"); err != nil {
					Fail(env, cq, "применить sysctl --system", err, "opt:presets")
					return nil
				}
				return Edit(env, cq,
					fmt.Sprintf("<b>📦 Пресет %s применён</b>\n\n"+
						"✅ Конфиг записан: <code>%s</code>\n"+
						"✅ Параметры применены (<code>sysctl --system</code>)\n\n<code>%s</code>",
						p.Title, Esc(path), Esc(conf)),
					[][]gotgbot.InlineKeyboardButton{BackRow("opt:presets")})
			}
		}

	case "swap":
		if len(parts) >= 4 && validSwapSize(parts[3]) {
			return optDoSwap(env, cq, parts[3])
		}

	case "swapoff":
		return optDoSwapOff(env, cq)

	case "journald":
		if !env.OS.HasSystemd {
			return Unsupported(env, cq, "Ограничение journald")
		}
		env.Audit.Log(cq.From.Id, "limit journald to 100M")
		if err := os.MkdirAll("/etc/systemd/journald.conf.d", 0o755); err != nil {
			Fail(env, cq, "создать каталог journald.conf.d", err, "opt:menu")
			return nil
		}
		conf := "[Journal]\nSystemMaxUse=100M\n"
		if err := os.WriteFile("/etc/systemd/journald.conf.d/99-serverbot.conf", []byte(conf), 0o644); err != nil {
			Fail(env, cq, "записать конфиг journald", err, "opt:menu")
			return nil
		}
		if _, err := sysutil.ServiceAction(env.RootCtx, "restart", "systemd-journald"); err != nil {
			Fail(env, cq, "перезапустить systemd-journald", err, "opt:menu")
			return nil
		}
		return Edit(env, cq,
			"<b>🗞 Журнал systemd ограничен</b>\n\n"+
				"✅ Установлен лимит <code>SystemMaxUse=100M</code>\n"+
				"✅ <code>systemd-journald</code> перезапущен",
			[][]gotgbot.InlineKeyboardButton{BackRow("opt:menu")})
	}
	text, kb := optMenu(env)
	return Edit(env, cq, text, kb)
}

func optDoSwap(env *Env, cq *gotgbot.CallbackQuery, size string) error {

	if _, err := os.Stat("/swapfile"); err == nil {
		return Edit(env, cq,
			"ℹ️ <b>Swap уже существует</b>\n\nФайл <code>/swapfile</code> найден — пересоздавать не буду.",
			[][]gotgbot.InlineKeyboardButton{BackRow("opt:menu")})
	}
	env.Audit.Log(cq.From.Id, "create swapfile "+size+"G")
	if _, err := sysutil.Run(env.RootCtx, 60*time.Second, "fallocate", "-l", size+"G", "/swapfile"); err != nil {
		Fail(env, cq, "создать файл подкачки (fallocate)", err, "opt:menu")
		return nil
	}
	if err := os.Chmod("/swapfile", 0o600); err != nil {
		Fail(env, cq, "выставить права 600 на /swapfile", err, "opt:menu")
		return nil
	}
	if _, err := sysutil.Run(env.RootCtx, 60*time.Second, "mkswap", "/swapfile"); err != nil {
		Fail(env, cq, "отформатировать swap (mkswap)", err, "opt:menu")
		return nil
	}
	if _, err := sysutil.Run(env.RootCtx, 15*time.Second, "swapon", "/swapfile"); err != nil {
		Fail(env, cq, "включить swap (swapon)", err, "opt:menu")
		return nil
	}

	fstab, err := os.ReadFile("/etc/fstab")
	if err == nil && !strings.Contains(string(fstab), "/swapfile") {
		f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			Fail(env, cq, "добавить swap в /etc/fstab", err, "opt:menu")
			return nil
		}
		_, err = f.WriteString("/swapfile none swap sw 0 0\n")
		_ = f.Close()
		if err != nil {
			Fail(env, cq, "добавить swap в /etc/fstab", err, "opt:menu")
			return nil
		}
	}
	out, _ := sysutil.Run(env.RootCtx, 10*time.Second, "swapon", "--show")
	return Edit(env, cq,
		fmt.Sprintf("<b>💾 Swap-файл %s ГБ создан и включён</b>\n\n<pre>%s</pre>",
			Esc(size), Esc(Trunc(strings.TrimSpace(out), 1000))),
		[][]gotgbot.InlineKeyboardButton{BackRow("opt:menu")})
}

func optDoSwapOff(env *Env, cq *gotgbot.CallbackQuery) error {
	env.Audit.Log(cq.From.Id, "disable and remove swap")

	_, statErr := os.Stat("/swapfile")
	active, _ := sysutil.Run(env.RootCtx, 10*time.Second, "swapon", "--show", "--noheadings")
	if statErr != nil && strings.TrimSpace(active) == "" {
		return Edit(env, cq,
			"ℹ️ <b>Swap не настроен</b> — ни активного swap, ни <code>/swapfile</code> не найдено.",
			[][]gotgbot.InlineKeyboardButton{BackRow("opt:swap")})
	}

	if _, err := sysutil.Run(env.RootCtx, 30*time.Second, "swapoff", "/swapfile"); err != nil {
		if _, err2 := sysutil.Run(env.RootCtx, 30*time.Second, "swapoff", "-a"); err2 != nil {
			Fail(env, cq, "отключить swap (swapoff)", err2, "opt:swap")
			return nil
		}
	}

	fstabNote := ""
	if data, err := os.ReadFile("/etc/fstab"); err == nil {
		lines := strings.Split(string(data), "\n")
		kept := lines[:0]
		removed := false
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == "/swapfile" {
				removed = true
				continue
			}
			kept = append(kept, line)
		}
		if removed {
			if err := os.WriteFile("/etc/fstab", []byte(strings.Join(kept, "\n")), 0o644); err != nil {
				Fail(env, cq, "убрать swap из /etc/fstab", err, "opt:swap")
				return nil
			}
			fstabNote = "\n✅ Строка удалена из <code>/etc/fstab</code>"
		}
	}

	fileNote := ""
	if statErr == nil {
		if err := os.Remove("/swapfile"); err != nil {
			Fail(env, cq, "удалить /swapfile", err, "opt:swap")
			return nil
		}
		fileNote = "\n✅ Файл <code>/swapfile</code> удалён"
	}

	return Edit(env, cq,
		"<b>❌ Swap отключён</b>\n\n✅ <code>swapoff</code> выполнен"+fstabNote+fileNote,
		[][]gotgbot.InlineKeyboardButton{BackRow("opt:swap")})
}

func validSwapSize(s string) bool {
	return s == "1" || s == "2" || s == "4"
}
