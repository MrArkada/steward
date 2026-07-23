package metrics

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DiskUsage struct {
	Device  string
	Mount   string
	FSType  string
	Total   uint64
	Used    uint64
	Percent float64
}

type Snapshot struct {
	CPUPercent   float64
	MemTotal     uint64
	MemUsed      uint64
	MemAvailable uint64
	MemPercent   float64
	SwapTotal    uint64
	SwapUsed     uint64
	Load1        float64
	Load5        float64
	Load15       float64
	Uptime       time.Duration
	Disks        []DiskUsage
	NetRX        uint64
	NetTX        uint64
	SessionRX    uint64
	SessionTX    uint64
	Processes    int
	CPUCount     int
	Hostname     string
	CollectedAt  time.Time
}

type cpuSample struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (s cpuSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuSample) idleAll() uint64 { return s.idle + s.iowait }

type Monitor struct {
	ttl      time.Duration
	mu       sync.Mutex
	cache    *Snapshot
	cacheAt  time.Time
	prevCPU  cpuSample
	hasPrev  bool
	baseRX   uint64
	baseTX   uint64
	baseSet  bool
	hostname string
}

func (m *Monitor) Hostname() string { return m.hostname }

func New(ttl time.Duration) *Monitor {
	if ttl <= 0 {
		ttl = 4 * time.Second
	}
	h, _ := os.Hostname()
	return &Monitor{ttl: ttl, hostname: h}
}

func (m *Monitor) Snapshot() (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache != nil && time.Since(m.cacheAt) < m.ttl {
		return m.cache, nil
	}
	snap, err := m.collectLocked()
	if err != nil {

		if m.cache != nil {
			return m.cache, nil
		}
		return nil, err
	}
	m.cache = snap
	m.cacheAt = time.Now()
	return snap, nil
}

func (m *Monitor) collectLocked() (*Snapshot, error) {
	snap := &Snapshot{
		CPUCount:    runtime.NumCPU(),
		Hostname:    m.hostname,
		CollectedAt: time.Now(),
	}

	cur, err := readCPUSample()
	if err != nil {
		return nil, fmt.Errorf("/proc/stat: %w", err)
	}
	if !m.hasPrev {
		m.prevCPU = cur
		m.hasPrev = true
		time.Sleep(120 * time.Millisecond)
		cur, err = readCPUSample()
		if err != nil {
			return nil, fmt.Errorf("/proc/stat: %w", err)
		}
	}
	totalDelta := float64(cur.total() - m.prevCPU.total())
	idleDelta := float64(cur.idleAll() - m.prevCPU.idleAll())
	if totalDelta > 0 {
		snap.CPUPercent = (1 - idleDelta/totalDelta) * 100
		if snap.CPUPercent < 0 {
			snap.CPUPercent = 0
		}
	}
	m.prevCPU = cur

	mem, err := readMeminfo()
	if err != nil {
		return nil, fmt.Errorf("/proc/meminfo: %w", err)
	}
	snap.MemTotal = mem.total
	snap.MemAvailable = mem.available
	snap.MemUsed = mem.total - mem.available
	if mem.total > 0 {
		snap.MemPercent = float64(snap.MemUsed) / float64(mem.total) * 100
	}
	snap.SwapTotal = mem.swapTotal
	snap.SwapUsed = mem.swapTotal - mem.swapFree

	if l1, l5, l15, err := readLoadavg(); err == nil {
		snap.Load1, snap.Load5, snap.Load15 = l1, l5, l15
	}
	if up, err := readUptime(); err == nil {
		snap.Uptime = up
	}

	snap.Disks = readDisks()

	rx, tx, err := readNetDev()
	if err == nil {
		if !m.baseSet {
			m.baseRX, m.baseTX, m.baseSet = rx, tx, true
		}
		snap.NetRX, snap.NetTX = rx, tx
		snap.SessionRX = rx - m.baseRX
		snap.SessionTX = tx - m.baseTX
	}

	snap.Processes = countProcesses()

	return snap, nil
}

func readCPUSample() (cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer f.Close()
	return parseCPUSample(f)
}

func parseCPUSample(r io.Reader) (cpuSample, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			return cpuSample{}, fmt.Errorf("некорректная строка cpu: %q", line)
		}
		vals := make([]uint64, 8)
		for i := 0; i < 8; i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuSample{}, err
			}
			vals[i] = v
		}
		return cpuSample{vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], vals[7]}, nil
	}
	return cpuSample{}, fmt.Errorf("строка cpu не найдена")
}

type memInfo struct {
	total, available, swapTotal, swapFree uint64
}

func readMeminfo() (memInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memInfo{}, err
	}
	defer f.Close()
	return parseMeminfo(f)
}

func parseMeminfo(r io.Reader) (memInfo, error) {
	var m memInfo
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		v *= 1024
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			m.total = v
		case "MemAvailable":
			m.available = v
		case "SwapTotal":
			m.swapTotal = v
		case "SwapFree":
			m.swapFree = v
		}
	}
	if m.total == 0 {
		return m, fmt.Errorf("MemTotal не найден")
	}

	if m.available == 0 {
		m.available = m.total / 4
	}
	return m, nil
}

func readLoadavg() (l1, l5, l15 float64, err error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	return parseLoadavg(string(data))
}

func parseLoadavg(s string) (l1, l5, l15 float64, err error) {
	fields := strings.Fields(s)
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("некорректный loadavg: %q", s)
	}
	l1, err1 := strconv.ParseFloat(fields[0], 64)
	l5, err2 := strconv.ParseFloat(fields[1], 64)
	l15, err3 := strconv.ParseFloat(fields[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("некорректный loadavg: %q", s)
	}
	return l1, l5, l15, nil
}

func readUptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseUptime(string(data))
}

func parseUptime(s string) (time.Duration, error) {
	fields := strings.Fields(s)
	if len(fields) < 1 {
		return 0, fmt.Errorf("некорректный uptime: %q", s)
	}
	sec, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(sec * float64(time.Second)), nil
}

func readNetDev() (rx, tx uint64, err error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	return parseNetDev(f)
}

func parseNetDev(r io.Reader) (rx, tx uint64, err error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		iface, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(iface) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(fields[0], 10, 64)
		t, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 == nil && err2 == nil {
			rx += r
			tx += t
		}
	}
	return rx, tx, nil
}

type mountEntry struct {
	device, mount, fstype string
}

var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "pstore": true, "securityfs": true,
	"debugfs": true, "tracefs": true, "configfs": true, "fusectl": true,
	"mqueue": true, "hugetlbfs": true, "rpc_pipefs": true, "binfmt_misc": true,
	"autofs": true, "efivarfs": true, "bpf": true, "nsfs": true,
	"ramfs": true, "selinuxfs": true, "smackfs": true, "tmpfs": true,
	"overlay": true, "squashfs": true, "iso9660": true, "udf": true,
}

func readMounts() []mountEntry {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseMounts(f)
}

func parseMounts(r io.Reader) []mountEntry {
	var out []mountEntry
	seen := make(map[string]bool)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		if pseudoFS[fields[2]] {
			continue
		}
		if seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		out = append(out, mountEntry{device: fields[0], mount: unescapeMount(fields[1]), fstype: fields[2]})
	}
	return out
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func readDisks() []DiskUsage {
	var out []DiskUsage
	for _, me := range readMounts() {
		total, avail, err := statfs(me.mount)
		if err != nil || total == 0 {
			continue
		}
		used := total - avail
		out = append(out, DiskUsage{
			Device:  me.device,
			Mount:   me.mount,
			FSType:  me.fstype,
			Total:   total,
			Used:    used,
			Percent: float64(used) / float64(total) * 100,
		})
	}
	return out
}

func countProcesses() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && isNumeric(e.Name()) {
			n++
		}
	}
	return n
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
