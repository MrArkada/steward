package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestParseCPUSample(t *testing.T) {
	data := `cpu  100 0 50 800 10 0 5 0 0 0
cpu0 50 0 25 400 5 0 2 0 0 0
intr 12345
`
	s, err := parseCPUSample(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if s.total() != 965 {
		t.Errorf("total = %d, ожидалось 965", s.total())
	}
	if s.idleAll() != 810 {
		t.Errorf("idleAll = %d, ожидалось 810", s.idleAll())
	}

	if _, err := parseCPUSample(strings.NewReader("intr 1\n")); err == nil {
		t.Error("ожидалась ошибка на пустом вводе")
	}
}

func TestParseMeminfo(t *testing.T) {
	data := `MemTotal:        2048000 kB
MemFree:          512000 kB
MemAvailable:    1024000 kB
Buffers:          100000 kB
Cached:           300000 kB
SwapTotal:        512000 kB
SwapFree:         256000 kB
`
	m, err := parseMeminfo(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if m.total != 2048000*1024 {
		t.Errorf("total = %d, ожидалось %d", m.total, 2048000*1024)
	}
	if m.available != 1024000*1024 {
		t.Errorf("available = %d", m.available)
	}
	if m.swapTotal != 512000*1024 || m.swapFree != 256000*1024 {
		t.Errorf("swap = %d/%d", m.swapTotal, m.swapFree)
	}

	if _, err := parseMeminfo(strings.NewReader("MemFree: 1 kB\n")); err == nil {
		t.Error("ожидалась ошибка без MemTotal")
	}
}

func TestParseLoadavg(t *testing.T) {
	l1, l5, l15, err := parseLoadavg("0.12 1.50 2.75 1/234 5678\n")
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if l1 != 0.12 || l5 != 1.50 || l15 != 2.75 {
		t.Errorf("load = %v %v %v", l1, l5, l15)
	}
	if _, _, _, err := parseLoadavg("bad\n"); err == nil {
		t.Error("ожидалась ошибка")
	}
}

func TestParseUptime(t *testing.T) {
	d, err := parseUptime("90125.44 180000.00\n")
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	want := time.Duration(90125.44 * float64(time.Second))
	if d != want {
		t.Errorf("uptime = %v, ожидалось %v", d, want)
	}
}

func TestParseNetDev(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 5000000  5000    0    0    0     0          0         0  2000000  3000    0    0    0     0       0          0
  wlan0: 3000      30    0    0    0     0          0         0     4000      40    0    0    0     0       0          0
`
	rx, tx, err := parseNetDev(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}

	if rx != 5000000+3000 {
		t.Errorf("rx = %d, ожидалось %d", rx, 5000000+3000)
	}
	if tx != 2000000+4000 {
		t.Errorf("tx = %d, ожидалось %d", tx, 2000000+4000)
	}
}

func TestParseMounts(t *testing.T) {
	data := `proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0
/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /tmp tmpfs rw,nosuid 0 0
/dev/sdb1 /home xfs rw,relatime 0 0
overlay /var/lib/docker/overlay2/x overlay rw 0 0
/dev/sdc1 /mnt/backup\040disk ext4 rw 0 0
`
	mounts := parseMounts(strings.NewReader(data))
	if len(mounts) != 3 {
		t.Fatalf("нашлось %d маунтов, ожидалось 3: %+v", len(mounts), mounts)
	}
	if mounts[0].mount != "/" || mounts[0].fstype != "ext4" {
		t.Errorf("неверный первый маунт: %+v", mounts[0])
	}
	if mounts[2].mount != "/mnt/backup disk" {
		t.Errorf("escape не сработал: %q", mounts[2].mount)
	}
}

func TestParseProcStat(t *testing.T) {

	fields := make([]string, 22)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[11] = "100"
	fields[12] = "50"
	fields[21] = "10"
	s, err := parseProcStat("123 (my proc) " + strings.Join(fields, " "))
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if s.name != "my proc" {
		t.Errorf("name = %q", s.name)
	}
	if s.sum != 150 {
		t.Errorf("sum = %d, ожидалось 150", s.sum)
	}
	if s.rss != 10*4096 && s.rss == 0 {
		t.Errorf("rss = %d", s.rss)
	}

	if _, err := parseProcStat("broken"); err == nil {
		t.Error("ожидалась ошибка")
	}
}

func TestBarSemantics(t *testing.T) {

	prev := cpuSample{user: 100, idle: 800, iowait: 10}
	cur := cpuSample{user: 150, idle: 850, iowait: 10}
	totalDelta := float64(cur.total() - prev.total())
	idleDelta := float64(cur.idleAll() - prev.idleAll())
	pct := (1 - idleDelta/totalDelta) * 100
	if pct != 50 {
		t.Errorf("cpu%% = %v, ожидалось 50", pct)
	}
}
