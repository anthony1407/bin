//go:build !windows

package admin

import "syscall"

// diskSpace returns the size and free space of the file system holding dir.
func diskSpace(dir string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
}
