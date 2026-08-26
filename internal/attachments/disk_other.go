//go:build !linux && !windows

package attachments

func filesystemFreeBytes(string) *uint64 { return nil }
