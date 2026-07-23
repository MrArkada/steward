package detect

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type PkgManager int

const (
	PMUnknown PkgManager = iota
	PMApt
	PMDnf
	PMYum
	PMPacman
	PMApk
)

func (p PkgManager) String() string {
	switch p {
	case PMApt:
		return "apt"
	case PMDnf:
		return "dnf"
	case PMYum:
		return "yum"
	case PMPacman:
		return "pacman"
	case PMApk:
		return "apk"
	default:
		return "unknown"
	}
}

type Info struct {
	ID         string
	IDLike     string
	Name       string
	PM         PkgManager
	HasSystemd bool
	Arch       string
	IsLinux    bool
}

func Detect() *Info {
	info := &Info{Arch: runtime.GOARCH, IsLinux: runtime.GOOS == "linux"}

	if f, err := os.Open("/etc/os-release"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"`)
			switch k {
			case "ID":
				info.ID = v
			case "ID_LIKE":
				info.IDLike = v
			case "PRETTY_NAME":
				info.Name = v
			}
		}
	}
	if info.Name == "" {
		info.Name = info.ID
	}
	if info.Name == "" {
		info.Name = runtime.GOOS
	}

	info.PM = detectPM(info)
	info.HasSystemd = detectSystemd()
	return info
}

func detectPM(i *Info) PkgManager {
	candidates := []struct {
		bin string
		pm  PkgManager
	}{
		{"apt-get", PMApt},
		{"dnf", PMDnf},
		{"yum", PMYum},
		{"pacman", PMPacman},
		{"apk", PMApk},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.bin); err == nil {
			return c.pm
		}
	}
	id := i.ID + " " + i.IDLike
	switch {
	case strings.Contains(id, "debian"), strings.Contains(id, "ubuntu"):
		return PMApt
	case strings.Contains(id, "rhel"), strings.Contains(id, "fedora"), strings.Contains(id, "centos"):
		return PMDnf
	case strings.Contains(id, "arch"):
		return PMPacman
	case strings.Contains(id, "alpine"):
		return PMApk
	}
	return PMUnknown
}

func detectSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return true
	}

	return true
}

func (i *Info) HasPM() bool { return i.PM != PMUnknown }

func (i *Info) IsDebianLike() bool { return i.PM == PMApt }
