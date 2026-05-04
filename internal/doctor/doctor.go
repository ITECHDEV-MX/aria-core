// Package doctor provides read-only operational diagnostics for an
// aria-core local store. It is the ARIA equivalent of engram's
// `mem_doctor` (engram v1.15.0). It never writes.
package doctor

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// Status is the result of a single Check.
type Status string

const (
	StatusOK    Status = "ok"
	StatusInfo  Status = "info"
	StatusWarn  Status = "warn"
	StatusError Status = "error"
)

// statusRank gives precedence for computing OverallStatus.
// Higher rank wins. Info does NOT escalate.
var statusRank = map[Status]int{
	StatusOK:    0,
	StatusInfo:  0,
	StatusWarn:  1,
	StatusError: 2,
}

// Check is a single diagnostic result.
type Check struct {
	Name       string `json:"name"`
	Status     Status `json:"status"`
	Detail     string `json:"detail"`
	DurationMs int64  `json:"duration_ms"`
}

// Report is the aggregated output of Diagnose.
type Report struct {
	Timestamp     time.Time `json:"timestamp"`
	OverallStatus Status    `json:"status"`
	Checks        []Check   `json:"checks"`
	Version       string    `json:"version"`
}

// Diagnose runs all read-only checks and returns a Report.
// The store and config are passed explicitly so the same function
// works in CLI mode and MCP mode without any global state.
//
// Contract: Diagnose never writes (it MAY create and delete one
// tempfile to verify writability). Target latency: < 500ms.
func Diagnose(db *sql.DB, cfg store.Config, version string) Report {
	checks := []Check{
		runCheck("config_dir", func() (Status, string) { return checkConfigDir(cfg) }),
		runCheck("db_file", func() (Status, string) { return checkDBFile(cfg) }),
		runCheck("schema_version", func() (Status, string) { return checkSchemaVersion(db) }),
		runCheck("core_tables", func() (Status, string) { return checkCoreTables(db) }),
		runCheck("fts5_index", func() (Status, string) { return checkFTSIndex(db) }),
		runCheck("disk_space", func() (Status, string) { return checkDiskSpace(cfg) }),
		runCheck("recent_activity", func() (Status, string) { return checkRecentActivity(db) }),
		runCheck("session", func() (Status, string) { return checkSession(cfg) }),
	}

	return Report{
		Timestamp:     time.Now().UTC(),
		OverallStatus: aggregateStatus(checks),
		Checks:        checks,
		Version:       version,
	}
}

// runCheck wraps a check function with timing and a recovery so a
// panicking check can never crash the doctor.
func runCheck(name string, fn func() (Status, string)) (out Check) {
	start := time.Now()
	out.Name = name
	defer func() {
		out.DurationMs = time.Since(start).Milliseconds()
		if r := recover(); r != nil {
			out.Status = StatusError
			out.Detail = fmt.Sprintf("check panicked: %v", r)
		}
	}()

	out.Status, out.Detail = fn()
	return out
}

// aggregateStatus picks the worst (by rank) status from the checks.
// Info is treated as OK for aggregation.
func aggregateStatus(checks []Check) Status {
	worst := StatusOK
	for _, c := range checks {
		if statusRank[c.Status] > statusRank[worst] {
			worst = c.Status
		}
	}
	return worst
}

// SortedByName returns a copy of checks sorted alphabetically.
// Useful for deterministic JSON output and display.
func (r Report) SortedByName() Report {
	out := Report{
		Timestamp:     r.Timestamp,
		OverallStatus: r.OverallStatus,
		Version:       r.Version,
		Checks:        make([]Check, len(r.Checks)),
	}
	copy(out.Checks, r.Checks)
	sort.Slice(out.Checks, func(i, j int) bool {
		return out.Checks[i].Name < out.Checks[j].Name
	})
	return out
}
