package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Thresholds struct {
	CPUPercent     float64 `yaml:"cpu_percent"`
	CPUDurationMin int     `yaml:"cpu_duration_min"`
	RAMPercent     float64 `yaml:"ram_percent"`
	DiskPercent    float64 `yaml:"disk_percent"`
}

type QuietHours struct {
	Enabled bool   `yaml:"enabled"`
	Start   string `yaml:"start"`
	End     string `yaml:"end"`
}

type Digest struct {
	Enabled bool   `yaml:"enabled"`
	Time    string `yaml:"time"`
}

type Paths struct {
	StateFile string `yaml:"state_file"`
	LogFile   string `yaml:"log_file"`
	AuditFile string `yaml:"audit_file"`
}

type Config struct {
	Token        string     `yaml:"token"`
	AllowedUsers []int64    `yaml:"allowed_users"`
	Thresholds   Thresholds `yaml:"thresholds"`
	QuietHours   QuietHours `yaml:"quiet_hours"`
	Digest       Digest     `yaml:"digest"`
	Paths        Paths      `yaml:"paths"`
	GeoIPEnabled bool       `yaml:"geoip_enabled"`

	path string
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать %s: %w", path, err)
	}
	cfg := &Config{path: path}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("ошибка синтаксиса %s: %w", path, err)
	}
	cfg.defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) defaults() {
	if c.Thresholds.CPUPercent == 0 {
		c.Thresholds.CPUPercent = 90
	}
	if c.Thresholds.CPUDurationMin == 0 {
		c.Thresholds.CPUDurationMin = 5
	}
	if c.Thresholds.RAMPercent == 0 {
		c.Thresholds.RAMPercent = 85
	}
	if c.Thresholds.DiskPercent == 0 {
		c.Thresholds.DiskPercent = 90
	}
	if c.QuietHours.Start == "" {
		c.QuietHours.Start = "00:00"
	}
	if c.QuietHours.End == "" {
		c.QuietHours.End = "08:00"
	}
	if c.Digest.Time == "" {
		c.Digest.Time = "09:00"
	}
	if c.Paths.StateFile == "" {
		c.Paths.StateFile = "state.json"
	}
	if c.Paths.LogFile == "" {
		c.Paths.LogFile = "bot.log"
	}
	if c.Paths.AuditFile == "" {
		c.Paths.AuditFile = "audit.log"
	}
}

func (c *Config) Validate() error {
	if c.Token == "" {
		return fmt.Errorf("не указан токен в config.yaml (параметр token)")
	}
	if !strings.Contains(c.Token, ":") {
		return fmt.Errorf("токен выглядит некорректно (ожидается формат 123456:ABC-DEF...)")
	}
	if len(c.AllowedUsers) == 0 {
		return fmt.Errorf("не указан ни один разрешённый Telegram ID (параметр allowed_users)")
	}
	for _, id := range c.AllowedUsers {
		if id <= 0 {
			return fmt.Errorf("некорректный Telegram ID в allowed_users: %d", id)
		}
	}
	if c.Thresholds.CPUPercent < 1 || c.Thresholds.CPUPercent > 100 {
		return fmt.Errorf("thresholds.cpu_percent должен быть в диапазоне 1..100")
	}
	if c.Thresholds.RAMPercent < 1 || c.Thresholds.RAMPercent > 100 {
		return fmt.Errorf("thresholds.ram_percent должен быть в диапазоне 1..100")
	}
	if c.Thresholds.DiskPercent < 1 || c.Thresholds.DiskPercent > 100 {
		return fmt.Errorf("thresholds.disk_percent должен быть в диапазоне 1..100")
	}
	if c.Thresholds.CPUDurationMin < 1 || c.Thresholds.CPUDurationMin > 60 {
		return fmt.Errorf("thresholds.cpu_duration_min должен быть в диапазоне 1..60")
	}
	if _, _, err := ParseHHMM(c.QuietHours.Start); err != nil {
		return fmt.Errorf("quiet_hours.start: %w", err)
	}
	if _, _, err := ParseHHMM(c.QuietHours.End); err != nil {
		return fmt.Errorf("quiet_hours.end: %w", err)
	}
	if _, _, err := ParseHHMM(c.Digest.Time); err != nil {
		return fmt.Errorf("digest.time: %w", err)
	}
	return nil
}

func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("путь к конфигу неизвестен")
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return renameFile(tmp, c.path)
}

func renameFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		_ = os.Remove(dst)
		return os.Rename(src, dst)
	}
	return nil
}

func (c *Config) AbsStatePath() string {
	abs, err := filepath.Abs(c.Paths.StateFile)
	if err != nil {
		return c.Paths.StateFile
	}
	return abs
}

func ParseHHMM(s string) (hour, min int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, 0, fmt.Errorf("ожидается формат HH:MM, получено %q", s)
	}
	hour, err = strconv.Atoi(h)
	if err != nil {
		return 0, 0, fmt.Errorf("некорректный час в %q", s)
	}
	min, err = strconv.Atoi(m)
	if err != nil {
		return 0, 0, fmt.Errorf("некорректные минуты в %q", s)
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("время вне диапазона в %q", s)
	}
	return hour, min, nil
}

func (c *Config) InQuietHours(now time.Time) bool {
	if !c.QuietHours.Enabled {
		return false
	}
	sh, sm, err1 := ParseHHMM(c.QuietHours.Start)
	eh, em, err2 := ParseHHMM(c.QuietHours.End)
	if err1 != nil || err2 != nil {
		return false
	}
	start := sh*60 + sm
	end := eh*60 + em
	cur := now.Hour()*60 + now.Minute()
	if start == end {
		return false
	}
	if start < end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end
}
