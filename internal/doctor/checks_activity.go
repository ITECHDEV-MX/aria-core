package doctor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// checkRecentActivity inspects the most recent observation timestamp.
// Returns:
//   - INFO if the observations table is empty (fresh install, fine).
//   - WARN if the most recent observation is > 30 days old (stale config?).
//   - OK otherwise, with a "last save Xh ago" detail.
func checkRecentActivity(db *sql.DB) (Status, string) {
	if db == nil {
		return StatusError, "no DB handle"
	}

	var ts sql.NullString
	err := db.QueryRow("SELECT MAX(created_at) FROM observations").Scan(&ts)
	if err != nil {
		// Table missing was caught in core_tables; return ok-ish here.
		return StatusInfo, fmt.Sprintf("activity check skipped: %v", err)
	}
	if !ts.Valid || ts.String == "" {
		return StatusInfo, "no observations yet (fresh store)"
	}

	t, err := parseSQLTime(ts.String)
	if err != nil {
		return StatusWarn, fmt.Sprintf("could not parse last save timestamp %q: %v", ts.String, err)
	}

	age := time.Since(t)
	human := humanizeAge(age)
	if age > 30*24*time.Hour {
		return StatusWarn, fmt.Sprintf("last save %s ago (>30 days, stale?)", human)
	}
	return StatusOK, fmt.Sprintf("last save %s ago", human)
}

// checkSession looks for session.json under DataDir. If absent, INFO
// (cloud not configured). If present, parse JWT exp and report.
func checkSession(cfg store.Config) (Status, string) {
	path := filepath.Join(cfg.DataDir, "session.json")
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StatusInfo, "not configured (run 'aria-core login' for cloud)"
		}
		return StatusWarn, fmt.Sprintf("stat session.json: %v", err)
	}
	if info.Size() == 0 {
		return StatusWarn, "session.json is empty"
	}

	expiresAt, err := readSessionExpiry(path)
	if err != nil {
		return StatusWarn, fmt.Sprintf("parse session.json: %v", err)
	}
	if time.Now().After(expiresAt) {
		return StatusWarn, fmt.Sprintf("session expired %s ago — re-run 'aria-core login'", humanizeAge(time.Since(expiresAt)))
	}
	return StatusOK, fmt.Sprintf("valid for another %s", humanizeAge(time.Until(expiresAt)))
}

// parseSQLTime accepts either RFC3339 or SQLite's CURRENT_TIMESTAMP form.
func parseSQLTime(s string) (time.Time, error) {
	formats := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05.000"}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("no matching time layout for %q", s)
}

func humanizeAge(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
