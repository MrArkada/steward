//go:build linux

package metrics

import "syscall"

func statfs(mount string) (total, avail uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		return 0, 0, err
	}
	total = st.Blocks * uint64(st.Bsize)
	avail = st.Bavail * uint64(st.Bsize)
	return total, avail, nil
}
