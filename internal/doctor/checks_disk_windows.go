//go:build windows
// +build windows

package doctor

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// checkDiskSpace on Windows uses GetDiskFreeSpaceExW.
func checkDiskSpace(cfg store.Config) (Status, string) {
	kernel32, err := syscall.LoadLibrary("kernel32.dll")
	if err != nil {
		return StatusWarn, fmt.Sprintf("kernel32 load failed: %v", err)
	}
	defer syscall.FreeLibrary(kernel32)

	getDiskFreeSpaceEx, err := syscall.GetProcAddress(kernel32, "GetDiskFreeSpaceExW")
	if err != nil {
		return StatusWarn, fmt.Sprintf("GetDiskFreeSpaceExW lookup failed: %v", err)
	}

	pathPtr, err := syscall.UTF16PtrFromString(cfg.DataDir)
	if err != nil {
		return StatusWarn, fmt.Sprintf("path encode failed: %v", err)
	}

	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	r1, _, callErr := syscall.SyscallN(getDiskFreeSpaceEx,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)))
	if r1 == 0 {
		return StatusWarn, fmt.Sprintf("GetDiskFreeSpaceExW failed: %v", callErr)
	}

	const (
		errThreshold  = 50 * 1024 * 1024
		warnThreshold = 500 * 1024 * 1024
	)

	switch {
	case freeBytesAvailable < errThreshold:
		return StatusError, fmt.Sprintf("only %s free (< 50 MB)", humanSize(int64(freeBytesAvailable)))
	case freeBytesAvailable < warnThreshold:
		return StatusWarn, fmt.Sprintf("%s free (< 500 MB)", humanSize(int64(freeBytesAvailable)))
	default:
		return StatusOK, fmt.Sprintf("%s free", humanSize(int64(freeBytesAvailable)))
	}
}
