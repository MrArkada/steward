package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const validYAML = `
token: "123456:ABC-DEF"
allowed_users: [111, 222]
thresholds:
  cpu_percent: 90
  cpu_duration_min: 5
  ram_percent: 85
  disk_percent: 90
quiet_hours:
  enabled: true
  start: "23:30"
  end: "07:15"
digest:
  enabled: true
  time: "09:00"
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if cfg.Token != "123456:ABC-DEF" || len(cfg.AllowedUsers) != 2 {
		t.Errorf("неверный конфиг: %+v", cfg)
	}
	if cfg.Paths.StateFile == "" || cfg.Paths.LogFile == "" {
		t.Error("дефолты путей не подставлены")
	}
}

func TestLoadInvalid(t *testing.T) {
	cases := map[string]string{
		"нет токена":      "allowed_users: [1]\n",
		"кривой токен":    "token: abc\nallowed_users: [1]\n",
		"пустой список":   "token: 1:a\nallowed_users: []\n",
		"плохой cpu":      "token: 1:a\nallowed_users: [1]\nthresholds: {cpu_percent: 150}\n",
		"плохое время":    "token: 1:a\nallowed_users: [1]\nquiet_hours: {start: \"25:00\"}\n",
		"плохой дайджест": "token: 1:a\nallowed_users: [1]\ndigest: {time: \"9 утра\"}\n",
	}
	for name, yaml := range cases {
		if _, err := Load(writeTemp(t, yaml)); err == nil {
			t.Errorf("%s: ожидалась ошибка валидации", name)
		}
	}
}

func TestSave(t *testing.T) {
	p := writeTemp(t, validYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Thresholds.CPUPercent = 77
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Thresholds.CPUPercent != 77 {
		t.Errorf("cpu_percent = %v после перезагрузки", cfg2.Thresholds.CPUPercent)
	}
}

func TestParseHHMM(t *testing.T) {
	h, m, err := ParseHHMM("07:05")
	if err != nil || h != 7 || m != 5 {
		t.Errorf("ParseHHMM = %d:%d, %v", h, m, err)
	}
	for _, bad := range []string{"", "7", "7:60", "24:00", "ab:cd", "7:5:1"} {
		if _, _, err := ParseHHMM(bad); err == nil {
			t.Errorf("%q: ожидалась ошибка", bad)
		}
	}
}

func TestInQuietHours(t *testing.T) {
	cfg := &Config{QuietHours: QuietHours{Enabled: true, Start: "23:00", End: "07:00"}}
	at := func(hh, mm int) time.Time {
		return time.Date(2024, 1, 1, hh, mm, 0, 0, time.Local)
	}
	if !cfg.InQuietHours(at(23, 30)) || !cfg.InQuietHours(at(3, 0)) {
		t.Error("ночные часы должны быть тихими")
	}
	if cfg.InQuietHours(at(12, 0)) || cfg.InQuietHours(at(22, 59)) {
		t.Error("дневные часы не должны быть тихими")
	}
	cfg.QuietHours.Enabled = false
	if cfg.InQuietHours(at(3, 0)) {
		t.Error("выключенные тихие часы не должны срабатывать")
	}
}
