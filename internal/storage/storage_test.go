package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissing(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("отсутствующий файл — не ошибка: %v", err)
	}
	if s.HasPorts() || s.PendingReboot() != nil {
		t.Error("состояние должно быть пустым")
	}
}

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(st *State) {
		st.PortSnapshot = map[string]string{"tcp/22": "sshd", "tcp/80": "nginx"}
		st.PendingReboot = &RebootFlag{At: time.Now().Round(0), ChatID: 42}
		st.Watchdog = map[string]bool{"nginx": true}
		st.WatchdogAuto = true
		st.AuthOffset = 12345
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	s2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	ports := s2.Ports()
	if len(ports) != 2 || ports["tcp/80"] != "nginx" {
		t.Errorf("ports = %+v", ports)
	}
	rf := s2.PendingReboot()
	if rf == nil || rf.ChatID != 42 {
		t.Errorf("pending reboot = %+v", rf)
	}
	if !s2.WatchdogList()["nginx"] || !s2.GetBool("watchdog_auto") {
		t.Error("watchdog не сохранился")
	}
	if s2.GetInt("auth_offset") != 12345 {
		t.Errorf("auth_offset = %d", s2.GetInt("auth_offset"))
	}
}

func TestCorruptedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(p, []byte("{битый json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(p)
	if err != nil {
		t.Fatalf("битый файл не должен ронять загрузку: %v", err)
	}
	if s.HasPorts() {
		t.Error("ожидалось пустое состояние")
	}
}

func TestGeoCache(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "state.json"))
	if _, ok := s.GeoGet("1.2.3.4"); ok {
		t.Error("кэш должен быть пуст")
	}
	_ = s.Update(func(st *State) {
		st.GeoCache = map[string]GeoEntry{"1.2.3.4": {Country: "Германия", City: "Берлин", At: time.Now()}}
	})
	e, ok := s.GeoGet("1.2.3.4")
	if !ok || e.Country != "Германия" {
		t.Errorf("geo = %+v", e)
	}
}
