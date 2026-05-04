//go:build !windows
// +build !windows

package doctor

import (
	"fmt"
	"syscall"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// checkDiskSpace reports free bytes under DataDir using statfs.
// Thresholds: < 50 MB error, < 500 MB warn, ≥ 500 MB ok.
func checkDiskSpace(cfg store.Config) (Status, string) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(cfg.DataDir, &stat); err != nil {
		return StatusWarn, fmt.Sprintf("statfs failed: %v", err)
	}
	free := stat.Bavail * uint64(stat.Bsize)

	const (
		errThreshold  = 50 * 1024 * 1024
		warnThreshold = 500 * 1024 * 1024
	)

	switch {
	case free < errThreshold:
		return StatusError, fmt.Sprintf("only %s free (< 50 MB)", humanSize(int64(free)))
	case free < warnThreshold:
		return StatusWarn, fmt.Sprintf("%s free (< 500 MB)", humanSize(int64(free)))
	default:
		return StatusOK, fmt.Sprintf("%s free", humanSize(int64(free)))
	}
}
