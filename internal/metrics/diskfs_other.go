//go:build !linux

package metrics

import "fmt"

func statfs(mount string) (total, avail uint64, err error) {
	return 0, 0, fmt.Errorf("statfs поддерживается только на Linux")
}
