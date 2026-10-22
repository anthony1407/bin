//go:build windows

package admin

import "golang.org/x/sys/windows"

// diskSpace returns the size and free space of the volume holding dir.
func diskSpace(dir string) (total, free uint64, err error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var avail, size, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(path, &avail, &size, &totalFree); err != nil {
		return 0, 0, err
	}
	return size, avail, nil
}
