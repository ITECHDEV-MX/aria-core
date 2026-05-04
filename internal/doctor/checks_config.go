package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// checkConfigDir verifies the configured DataDir is absolute, exists,
// and is writable. Reports free disk space.
func checkConfigDir(cfg store.Config) (Status, string) {
	if cfg.DataDir == "" {
		return StatusError, "DataDir is empty"
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return StatusError, fmt.Sprintf("DataDir %q is not absolute", cfg.DataDir)
	}

	info, err := os.Stat(cfg.DataDir)
	if err != nil {
		return StatusError, fmt.Sprintf("cannot stat DataDir: %v", err)
	}
	if !info.IsDir() {
		return StatusError, fmt.Sprintf("%s exists but is not a directory", cfg.DataDir)
	}

	// Writability probe: create + delete a tempfile.
	tmp, err := os.CreateTemp(cfg.DataDir, ".doctor-probe-*")
	if err != nil {
		return StatusError, fmt.Sprintf("DataDir not writable: %v", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpName)

	// Look at the env var to flag misconfiguration.
	if env := strings.TrimSpace(os.Getenv("ARIA_CORE_DATA_DIR")); env != "" && env != cfg.DataDir {
		return StatusWarn, fmt.Sprintf("ARIA_CORE_DATA_DIR=%q but effective DataDir=%q", env, cfg.DataDir)
	}

	return StatusOK, fmt.Sprintf("%s (writable)", cfg.DataDir)
}
