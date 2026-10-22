//go:build !windows

package sitebackup

import "syscall"

func FreeBytes(path string) (int64, error) {
	var info syscall.Statfs_t
	if err := syscall.Statfs(path, &info); err != nil {
		return 0, err
	}
	return int64(info.Bavail) * int64(info.Bsize), nil
}
