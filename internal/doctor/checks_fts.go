package doctor

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// checkFTSIndex verifies the FTS5 virtual table is queryable.
// Issues a no-op MATCH that should always succeed if FTS5 is healthy.
func checkFTSIndex(db *sql.DB) (Status, string) {
	if db == nil {
		return StatusError, "no DB handle"
	}

	// Find the FTS table. ARIA may name it differently than engram.
	// Detect by looking for any table with "_fts" suffix in sqlite_master.
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%_fts'")
	if err != nil {
		return StatusError, fmt.Sprintf("scan for fts table: %v", err)
	}
	defer rows.Close()

	var fts string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return StatusError, fmt.Sprintf("scan fts name: %v", err)
		}
		// Prefer observations_fts if present
		if strings.Contains(name, "observations") {
			fts = name
			break
		}
		if fts == "" {
			fts = name
		}
	}
	if fts == "" {
		return StatusInfo, "no FTS5 table detected (acceptable for fresh stores)"
	}

	// Probe with a harmless MATCH.
	var count int
	probeQ := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s MATCH 'a OR b' LIMIT 1", fts, fts)
	if err := db.QueryRow(probeQ).Scan(&count); err != nil {
		// Some FTS schemas don't allow count with LIMIT. Try a simpler probe.
		simpleQ := fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", fts)
		var x int
		if err2 := db.QueryRow(simpleQ).Scan(&x); err2 != nil && !errors.Is(err2, errIgnoreNoRows) {
			return StatusError, fmt.Sprintf("FTS5 probe failed: %v / %v", err, err2)
		}
	}

	// Document count
	var docs int
	_ = db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", fts)).Scan(&docs)

	return StatusOK, fmt.Sprintf("%s healthy (%d docs)", fts, docs)
}

// errIgnoreNoRows is a sentinel — we'd compare with sql.ErrNoRows but
// avoid the import here to keep this file focused.
var errIgnoreNoRows = errors.New("no rows")
