package sitebackup

import (
	"syscall"
	"unsafe"
)

func FreeBytes(path string) (int64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	fn := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	ok, _, e := fn.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), 0, 0)
	if ok == 0 {
		return 0, e
	}
	return int64(free), nil
}
