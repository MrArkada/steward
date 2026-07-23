package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ProcInfo struct {
	PID        int
	Name       string
	CPUPercent float64
	MemPercent float64
	RSS        uint64
}

type procSample struct {
	name string
	sum  uint64
	rss  uint64
}

func (m *Monitor) TopCPU(n int) ([]ProcInfo, error) {
	first, total1, err := readAllProcs()
	if err != nil {
		return nil, err
	}
	time.Sleep(200 * time.Millisecond)
	second, total2, err := readAllProcs()
	if err != nil {
		return nil, err
	}
	deltaTotal := float64(total2 - total1)
	if deltaTotal <= 0 {
		return nil, fmt.Errorf("не удалось вычислить дельту CPU")
	}
	cpus := float64(m.cpuCount())

	procs := make([]ProcInfo, 0, len(second))
	for pid, s2 := range second {
		s1, ok := first[pid]
		if !ok {
			continue
		}
		cpu := float64(s2.sum-s1.sum) / deltaTotal * 100 * cpus
		procs = append(procs, ProcInfo{PID: pid, Name: s2.name, CPUPercent: cpu, RSS: s2.rss})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].CPUPercent > procs[j].CPUPercent })
	if len(procs) > n {
		procs = procs[:n]
	}
	m.fillMemPercent(procs)
	return procs, nil
}

func (m *Monitor) TopMem(n int) ([]ProcInfo, error) {
	procs0, _, err := readAllProcs()
	if err != nil {
		return nil, err
	}
	procs := make([]ProcInfo, 0, len(procs0))
	for pid, s := range procs0 {
		procs = append(procs, ProcInfo{PID: pid, Name: s.name, RSS: s.rss})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].RSS > procs[j].RSS })
	if len(procs) > n {
		procs = procs[:n]
	}
	m.fillMemPercent(procs)
	return procs, nil
}

func (m *Monitor) cpuCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache != nil && m.cache.CPUCount > 0 {
		return m.cache.CPUCount
	}
	return 1
}

func (m *Monitor) fillMemPercent(procs []ProcInfo) {
	snap, err := m.Snapshot()
	if err != nil || snap.MemTotal == 0 {
		return
	}
	for i := range procs {
		procs[i].MemPercent = float64(procs[i].RSS) / float64(snap.MemTotal) * 100
	}
}

func readAllProcs() (map[int]procSample, uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, 0, err
	}
	procs := make(map[int]procSample, 256)
	for _, e := range entries {
		if !e.IsDir() || !isNumeric(e.Name()) {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		s, err := readProcStat(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		procs[pid] = s
	}
	total, err := readCPUSample()
	if err != nil {
		return nil, 0, err
	}
	return procs, total.total(), nil
}

func readProcStat(path string) (procSample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return procSample{}, err
	}
	return parseProcStat(string(data))
}

func parseProcStat(s string) (procSample, error) {
	open := strings.Index(s, "(")
	close_ := strings.LastIndex(s, ")")
	if open < 0 || close_ < 0 || close_ < open {
		return procSample{}, fmt.Errorf("некорректный stat")
	}
	name := s[open+1 : close_]
	fields := strings.Fields(s[close_+1:])

	if len(fields) < 22 {
		return procSample{}, fmt.Errorf("мало полей в stat")
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	rssPages, err3 := strconv.ParseInt(fields[21], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return procSample{}, fmt.Errorf("некорректные числа в stat")
	}
	var rss uint64
	if rssPages > 0 {
		rss = uint64(rssPages) * uint64(os.Getpagesize())
	}
	return procSample{name: name, sum: utime + stime, rss: rss}, nil
}
