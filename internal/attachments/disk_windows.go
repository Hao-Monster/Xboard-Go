//go:build windows

package attachments

import (
	"golang.org/x/sys/windows"
)

func filesystemFreeBytes(path string) *uint64 {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(value, &free, nil, nil); err != nil {
		return nil
	}
	return &free
}
