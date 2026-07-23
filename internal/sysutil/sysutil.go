package sysutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"serverbot/internal/detect"
)

type CmdError struct {
	Cmd     string
	Err     error
	Output  string
	Timeout bool
}

func (e *CmdError) Error() string {
	if e.Timeout {
		return fmt.Sprintf("команда %q превысила таймаут", e.Cmd)
	}
	return fmt.Sprintf("команда %q завершилась с ошибкой: %v", e.Cmd, e.Err)
}

func (e *CmdError) Unwrap() error { return e.Err }

func (e *CmdError) Short() string {
	if e.Timeout {
		return "превышено время ожидания"
	}
	var errLine, last string
	for _, line := range strings.Split(e.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		last = line
		low := strings.ToLower(line)
		if errLine == "" && (strings.HasPrefix(line, "E:") ||
			strings.Contains(low, "error") || strings.Contains(low, "failed") ||
			strings.Contains(low, "ошибка")) {
			errLine = line
		}
	}

	if e.Err != nil && strings.Contains(e.Err.Error(), "killed") {
		return "процесс убит (signal: killed) — вероятно, не хватило памяти; создайте swap: 🚀 Оптимизация → 💾 Swap"
	}
	pick := errLine
	if pick == "" {
		pick = last
	}
	if pick == "" {
		if e.Err != nil {
			return e.Err.Error()
		}
		return "команда завершилась с ошибкой"
	}
	if len(pick) > 140 {
		pick = pick[:140] + "…"
	}
	return pick
}

func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return RunEnv(ctx, timeout, nil, name, args...)
}

func RunEnv(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), &CmdError{Cmd: name + " " + strings.Join(args, " "), Err: cctx.Err(), Output: string(out), Timeout: true}
	}
	if err != nil {
		return string(out), &CmdError{Cmd: name + " " + strings.Join(args, " "), Err: err, Output: string(out)}
	}
	return string(out), nil
}

func RunStdin(ctx context.Context, timeout time.Duration, input string, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return string(out), &CmdError{Cmd: name, Err: cctx.Err(), Output: string(out), Timeout: true}
	}
	if err != nil {
		return string(out), &CmdError{Cmd: name, Err: err, Output: string(out)}
	}
	return string(out), nil
}

func Exists(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

func Systemctl(ctx context.Context, args ...string) (string, error) {
	return Run(ctx, 30*time.Second, "systemctl", args...)
}

func ServiceState(ctx context.Context, unit string) string {
	out, err := Run(ctx, 10*time.Second, "systemctl", "is-active", unit)
	s := strings.TrimSpace(out)
	if s == "" && err != nil {
		return "unknown"
	}
	return s
}

func UnitExists(ctx context.Context, unit string) bool {
	_, err := Run(ctx, 10*time.Second, "systemctl", "cat", unit)
	return err == nil
}

func ServiceAction(ctx context.Context, action, unit string) (string, error) {
	return Systemctl(ctx, action, unit)
}

func KnownServices() []string {
	return []string{
		"sshd", "ssh", "nginx", "apache2", "docker", "containerd",
		"postgresql", "mysql", "mariadb", "redis", "redis-server",
		"cron", "crond", "fail2ban", "ufw", "named", "memcached",
	}
}

var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive"}

var aptLockOpts = []string{"-o", "DPkg::Lock::Timeout=60"}

var hasSystemdRun = sync.OnceValue(func() bool { return Exists("systemd-run") })

func pmRun(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	if hasSystemdRun() {
		scoped := append([]string{"--scope", "-q", name}, args...)
		out, err := RunEnv(ctx, timeout, env, "systemd-run", scoped...)
		if err == nil || !strings.Contains(out, "transient") {
			return out, err
		}
	}
	return RunEnv(ctx, timeout, env, name, args...)
}

func aptRun(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	full := make([]string, 0, len(aptLockOpts)+len(args))
	full = append(full, aptLockOpts...)
	full = append(full, args...)

	out, err := pmRun(ctx, timeout, aptEnv, "apt-get", full...)
	if err == nil || !strings.Contains(out, "dpkg was interrupted") {
		return out, err
	}
	if _, repErr := pmRun(ctx, 10*time.Minute, aptEnv, "dpkg", "--configure", "-a"); repErr != nil {
		return out, fmt.Errorf("dpkg был прерван; авто-ремонт (dpkg --configure -a) не удался: %v", repErr)
	}
	return pmRun(ctx, timeout, aptEnv, "apt-get", full...)
}

func AptClean(ctx context.Context) (string, error) {
	if _, err := aptRun(ctx, 2*time.Minute, "clean"); err != nil {
		return "", err
	}
	return aptRun(ctx, 5*time.Minute, "autoremove", "-y")
}

func PMInstall(ctx context.Context, pm detect.PkgManager, pkgs ...string) (string, error) {
	switch pm {
	case detect.PMApt:
		args := append([]string{"install", "-y"}, pkgs...)
		return aptRun(ctx, 5*time.Minute, args...)
	case detect.PMDnf:
		args := append([]string{"install", "-y"}, pkgs...)
		return pmRun(ctx, 5*time.Minute, nil, "dnf", args...)
	case detect.PMYum:
		args := append([]string{"install", "-y"}, pkgs...)
		return pmRun(ctx, 5*time.Minute, nil, "yum", args...)
	case detect.PMPacman:
		args := append([]string{"-S", "--noconfirm"}, pkgs...)
		return pmRun(ctx, 5*time.Minute, nil, "pacman", args...)
	case detect.PMApk:
		args := append([]string{"add"}, pkgs...)
		return pmRun(ctx, 5*time.Minute, nil, "apk", args...)
	}
	return "", errors.New("пакетный менеджер не определён")
}

func PMRefresh(ctx context.Context, pm detect.PkgManager) (string, error) {
	switch pm {
	case detect.PMApt:
		return aptRun(ctx, 3*time.Minute, "update")
	case detect.PMDnf:
		return pmRun(ctx, 3*time.Minute, nil, "dnf", "makecache")
	case detect.PMYum:
		return pmRun(ctx, 3*time.Minute, nil, "yum", "makecache")
	case detect.PMPacman:
		return pmRun(ctx, 3*time.Minute, nil, "pacman", "-Sy", "--noconfirm")
	case detect.PMApk:
		return pmRun(ctx, 3*time.Minute, nil, "apk", "update")
	}
	return "", errors.New("пакетный менеджер не определён")
}

func PMUpgradable(ctx context.Context, pm detect.PkgManager) (string, error) {
	switch pm {
	case detect.PMApt:

		out, _ := Run(ctx, 60*time.Second, "apt", "list", "--upgradable")
		return out, nil
	case detect.PMDnf:
		out, _ := Run(ctx, 60*time.Second, "dnf", "check-update")
		return out, nil
	case detect.PMYum:
		out, _ := Run(ctx, 60*time.Second, "yum", "check-update")
		return out, nil
	case detect.PMPacman:
		out, _ := Run(ctx, 60*time.Second, "pacman", "-Qu")
		return out, nil
	case detect.PMApk:
		out, _ := Run(ctx, 60*time.Second, "apk", "version", "-l", "<")
		return out, nil
	}
	return "", errors.New("пакетный менеджер не определён")
}

func PMUpgradeAll(ctx context.Context, pm detect.PkgManager) (string, error) {
	switch pm {
	case detect.PMApt:
		return aptRun(ctx, 20*time.Minute, "upgrade", "-y")
	case detect.PMDnf:
		return pmRun(ctx, 20*time.Minute, nil, "dnf", "upgrade", "-y")
	case detect.PMYum:
		return pmRun(ctx, 20*time.Minute, nil, "yum", "update", "-y")
	case detect.PMPacman:
		return pmRun(ctx, 20*time.Minute, nil, "pacman", "-Su", "--noconfirm")
	case detect.PMApk:
		return pmRun(ctx, 20*time.Minute, nil, "apk", "upgrade")
	}
	return "", errors.New("пакетный менеджер не определён")
}

func SSHPort() int {
	port := 22
	read := func(path string) {
		data, err := os.ReadFile(path)
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
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "port") {
				continue
			}
			if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 && p < 65536 {
				port = p
			}
		}
	}
	read("/etc/ssh/sshd_config")

	matches, _ := filepath.Glob("/etc/ssh/sshd_config.d/*.conf")
	for _, m := range matches {
		read(m)
	}
	return port
}

func SSHSessionCount(ctx context.Context) int {
	out, err := Run(ctx, 5*time.Second, "who")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

type Port struct {
	Proto   string
	Addr    string
	Port    int
	Process string
}

func (p Port) Key() string { return fmt.Sprintf("%s/%d", p.Proto, p.Port) }

func ListeningPorts(ctx context.Context) ([]Port, error) {
	out, err := Run(ctx, 10*time.Second, "ss", "-tulpn")
	if err != nil {
		return nil, err
	}
	var ports []Port
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)

		if len(fields) < 5 || fields[0] == "Netid" {
			continue
		}
		proto := fields[0]
		if proto != "tcp" && proto != "udp" {
			continue
		}
		local := fields[4]
		idx := strings.LastIndex(local, ":")
		if idx < 0 {
			continue
		}
		pnum, err := strconv.Atoi(local[idx+1:])
		if err != nil {
			continue
		}
		p := Port{Proto: proto, Addr: local[:idx], Port: pnum}
		if len(fields) >= 7 {
			p.Process = parseSSProcess(fields[6])
		}
		ports = append(ports, p)
	}
	return ports, nil
}

func parseSSProcess(s string) string {
	start := strings.Index(s, "((\"")
	if start < 0 {
		return ""
	}
	s = s[start+3:]
	if end := strings.Index(s, "\""); end > 0 {
		return s[:end]
	}
	return ""
}

const fail2banJail = "[DEFAULT]\nbantime = 1h\nfindtime = 10m\nmaxretry = 5\n\n[sshd]\nenabled = true\nbackend = systemd\n"

func WriteFail2banJail() error {
	path := "/etc/fail2ban/jail.local"
	if data, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", data, 0o644)
	}
	return os.WriteFile(path, []byte(fail2banJail), 0o644)
}

func SetupFail2ban(ctx context.Context, pm detect.PkgManager) error {
	if !Exists("fail2ban-client") {
		if _, err := PMInstall(ctx, pm, "fail2ban"); err != nil {
			return fmt.Errorf("установка пакета: %w", err)
		}
	}
	if err := WriteFail2banJail(); err != nil {
		return fmt.Errorf("запись jail.local: %w", err)
	}
	if _, err := Systemctl(ctx, "enable", "--now", "fail2ban"); err != nil {
		return fmt.Errorf("запуск службы: %w", err)
	}
	return nil
}

func SetupUFW(ctx context.Context, pm detect.PkgManager) error {
	if !Exists("ufw") {
		if _, err := PMInstall(ctx, pm, "ufw"); err != nil {
			return fmt.Errorf("установка пакета: %w", err)
		}
	}
	if _, err := Run(ctx, 30*time.Second, "ufw", "default", "deny", "incoming"); err != nil {
		return fmt.Errorf("default deny incoming: %w", err)
	}
	if _, err := Run(ctx, 30*time.Second, "ufw", "default", "allow", "outgoing"); err != nil {
		return fmt.Errorf("default allow outgoing: %w", err)
	}
	port := strconv.Itoa(SSHPort())
	if _, err := Run(ctx, 30*time.Second, "ufw", "allow", port+"/tcp"); err != nil {
		return fmt.Errorf("открытие SSH-порта %s: %w", port, err)
	}
	if _, err := Run(ctx, 30*time.Second, "ufw", "--force", "enable"); err != nil {
		return fmt.Errorf("ufw enable: %w", err)
	}
	return nil
}

func JournalLogs(ctx context.Context, unit string, n int) (string, error) {
	return Run(ctx, 15*time.Second, "journalctl", "-u", unit, "-n", strconv.Itoa(n), "--no-pager")
}
