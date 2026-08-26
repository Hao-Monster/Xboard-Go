//go:build linux

package attachments

import "golang.org/x/sys/unix"

func filesystemFreeBytes(path string) *uint64 {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return nil
	}
	value := status.Bavail * uint64(status.Bsize)
	return &value
}
