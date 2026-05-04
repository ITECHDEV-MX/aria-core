package doctor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// expectedTables lists the table names that MUST exist in a healthy
// store. Names not in this list are tolerated (forward-compatibility).
//
// This list is intentionally conservative — kept short so new
// migrations don't constantly need updates here. Add only when a
// table is critical to MCP behavior.
var expectedTables = []string{
	"observations",
	"sessions",
	"user_prompts",
}

// checkDBFile verifies the SQLite file exists with a sane size and
// header.
func checkDBFile(cfg store.Config) (Status, string) {
	path := filepath.Join(cfg.DataDir, "aria-core.db")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StatusError, fmt.Sprintf("aria-core.db missing at %s", path)
		}
		return StatusError, fmt.Sprintf("stat aria-core.db: %v", err)
	}
	if info.Size() < 4096 {
		return StatusWarn, fmt.Sprintf("aria-core.db unusually small (%d bytes)", info.Size())
	}

	// Verify SQLite header (16 bytes "SQLite format 3\0").
	f, err := os.Open(path)
	if err != nil {
		return StatusError, fmt.Sprintf("open aria-core.db: %v", err)
	}
	defer f.Close()
	header := make([]byte, 16)
	if _, err := f.Read(header); err != nil {
		return StatusError, fmt.Sprintf("read aria-core.db header: %v", err)
	}
	if !strings.HasPrefix(string(header), "SQLite format 3") {
		return StatusError, "aria-core.db has invalid SQLite header"
	}

	return StatusOK, fmt.Sprintf("aria-core.db (%s)", humanSize(info.Size()))
}

// checkSchemaVersion reads PRAGMA user_version and compares to the
// migrate() expected value. We don't know the expected number from
// outside the store package, so we just report the observed version.
//
// A concrete "behind" check could be added later by exposing
// store.LatestSchemaVersion() — out of scope for B.1.
func checkSchemaVersion(db *sql.DB) (Status, string) {
	if db == nil {
		return StatusError, "no DB handle"
	}
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return StatusError, fmt.Sprintf("read user_version: %v", err)
	}
	return StatusOK, fmt.Sprintf("schema v%d", v)
}

// checkCoreTables asserts every expected table exists.
func checkCoreTables(db *sql.DB) (Status, string) {
	if db == nil {
		return StatusError, "no DB handle"
	}

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return StatusError, fmt.Sprintf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return StatusError, fmt.Sprintf("scan table name: %v", err)
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return StatusError, fmt.Sprintf("iterate tables: %v", err)
	}

	var missing []string
	for _, want := range expectedTables {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return StatusError, fmt.Sprintf("missing tables: %s", strings.Join(missing, ", "))
	}
	return StatusOK, fmt.Sprintf("%d/%d expected tables present (%d total in DB)", len(expectedTables), len(expectedTables), len(have))
}

// humanSize formats bytes as KB/MB/GB.
func humanSize(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// _ silence unused import for sql when no direct reference here.
var _ = sql.ErrNoRows
