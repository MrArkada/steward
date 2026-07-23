package storage

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type GeoEntry struct {
	Country string    `json:"country"`
	City    string    `json:"city"`
	At      time.Time `json:"at"`
}

type RebootFlag struct {
	At     time.Time `json:"at"`
	ChatID int64     `json:"chat_id"`
}

type State struct {
	PortSnapshot    map[string]string   `json:"port_snapshot,omitempty"`
	PendingReboot   *RebootFlag         `json:"pending_reboot,omitempty"`
	GeoCache        map[string]GeoEntry `json:"geo_cache,omitempty"`
	Watchdog        map[string]bool     `json:"watchdog,omitempty"`
	WatchdogAuto    bool                `json:"watchdog_auto_restart"`
	AutoClean       bool                `json:"auto_clean"`
	AutoSetupDone   bool                `json:"auto_setup_done"`
	AutoSetupV2     bool                `json:"auto_setup_v2"`
	LastPowerAction time.Time           `json:"last_power_action"`
	LastDigest      string              `json:"last_digest,omitempty"`
	LastAutoClean   string              `json:"last_auto_clean,omitempty"`
	AuthOffset      int64               `json:"auth_offset"`
	BannedToday     int                 `json:"banned_today"`
	BannedDate      string              `json:"banned_date,omitempty"`
}

type Storage struct {
	mu   sync.RWMutex
	path string
	st   State
}

func Load(path string) (*Storage, error) {
	s := &Storage{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(data) > 0 {

		_ = json.Unmarshal(data, &s.st)
	}
	return s, nil
}

func (s *Storage) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *Storage) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(&s.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {

		_ = os.Remove(s.path)
		return os.Rename(tmp, s.path)
	}
	return nil
}

func (s *Storage) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	return s.saveLocked()
}

func (s *Storage) Ports() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.st.PortSnapshot))
	for k, v := range s.st.PortSnapshot {
		out[k] = v
	}
	return out
}

func (s *Storage) HasPorts() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.st.PortSnapshot) > 0
}

func (s *Storage) PendingReboot() *RebootFlag {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.st.PendingReboot == nil {
		return nil
	}
	cp := *s.st.PendingReboot
	return &cp
}

func (s *Storage) GeoGet(ip string) (GeoEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.st.GeoCache[ip]
	return e, ok
}

func (s *Storage) WatchdogList() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.st.Watchdog))
	for k, v := range s.st.Watchdog {
		out[k] = v
	}
	return out
}

func (s *Storage) GetBool(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch name {
	case "watchdog_auto":
		return s.st.WatchdogAuto
	case "auto_clean":
		return s.st.AutoClean
	case "auto_setup_v2":
		return s.st.AutoSetupV2
	}
	return false
}

func (s *Storage) LastPower() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.LastPowerAction
}

func (s *Storage) GetString(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch name {
	case "last_digest":
		return s.st.LastDigest
	case "last_auto_clean":
		return s.st.LastAutoClean
	case "banned_date":
		return s.st.BannedDate
	}
	return ""
}

func (s *Storage) GetInt(name string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch name {
	case "auth_offset":
		return s.st.AuthOffset
	case "banned_today":
		return int64(s.st.BannedToday)
	}
	return 0
}
