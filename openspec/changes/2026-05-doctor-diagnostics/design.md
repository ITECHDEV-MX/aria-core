# Design — aria_doctor

## Package layout

```
internal/doctor/
  doctor.go          ← Diagnose() entry point, Report struct, Check struct
  checks_config.go   ← config_dir, env_vars
  checks_db.go       ← db_file, schema_version, core_tables
  checks_fts.go      ← fts5_index
  checks_disk.go     ← disk_space
  checks_activity.go ← recent_activity, session
  doctor_test.go     ← hermetic tests for each check
```

## Entry point

```go
package doctor

type Status string
const (
    StatusOK    Status = "ok"
    StatusWarn  Status = "warn"
    StatusError Status = "error"
)

type Check struct {
    Name       string        `json:"name"`
    Status     Status        `json:"status"`
    Detail     string        `json:"detail"`
    Duration   time.Duration `json:"duration_ms_swagger:string,format:duration"`
    DurationMs int64         `json:"duration_ms"`
}

type Report struct {
    Timestamp     time.Time `json:"timestamp"`
    OverallStatus Status    `json:"status"` // worst of any check
    Checks        []Check   `json:"checks"`
    Version       string    `json:"version"`
}

// Diagnose runs all checks against the given store + config.
// Read-only: never writes. Fast: target < 500ms total.
func Diagnose(s *store.Store, cfg store.Config) Report
```

## Per-check design

### config_dir
- Verify `cfg.DataDir` is absolute (already enforced at store init,
  but doctor sanity-checks).
- Verify it exists and is writable (write a tempfile, delete it).
- WARN if env vars are set but invalid (e.g. `ARIA_CORE_DATA_DIR=relative`).

### db_file
- Stat `<DataDir>/aria-core.db`. Exists? Size? Mode?
- Verify SQLite header (`SQLite format 3`).

### schema_version
- `PRAGMA user_version` — should match the latest migration.
- WARN if behind, ERROR if ahead (downgrade detected).

### core_tables
- Query `sqlite_master` for table names. Verify presence of:
  observations, sessions, skills, prompts, projects, conflicts (when
  B.2 ships).
- ERROR if any missing.

### fts5_index
- Query `aria_observations_fts MATCH 'a OR b'` LIMIT 1.
- Catch errors. ERROR if FTS5 broken.

### disk_space
- `syscall.Statfs(cfg.DataDir)` for free bytes.
- Threshold: ERROR < 50 MB, WARN < 500 MB, OK ≥ 500 MB.

### recent_activity
- `SELECT MAX(created_at) FROM observations`.
- INFO (not WARN) if no observations yet.
- WARN if last save > 30 days ago (might indicate stale config).

### session
- Read `<DataDir>/session.json`. If present:
  - Parse JWT exp claim.
  - WARN if expired.
  - OK if valid.
- INFO if absent (cloud not configured).

## CLI shape

```
$ aria-core doctor
ARIA Core Doctor — diagnostic report

  Status: ✓ healthy

  ✓ config_dir       /root/.aria-core (writable, 4.2 GB free)
  ✓ db_file          aria-core.db (12 MB, schema v23)
  ✓ schema_version   v23 (current)
  ✓ core_tables      24/24 expected tables present
  ✓ fts5_index       healthy (250 documents)
  ✓ disk_space       4.2 GB free (OK > 500 MB)
  ✓ recent_activity  last save 2h ago
  ⓘ session          not configured (run 'aria-core login' for cloud)

  Issues: 0 critical, 0 warnings, 1 informational
  Took: 87ms
```

```
$ aria-core doctor --json
{"timestamp":"2026-05-04T12:34:56Z","version":"v0.2.0","status":"ok",
 "checks":[{"name":"config_dir","status":"ok","detail":"...","duration_ms":2}, ...]}
```

## MCP tool shape

`aria_doctor` (agent + admin profiles):
- No required args
- Optional `format`: "text" | "json" (default "json" for agent
  consumption)
- Returns the JSON `Report`

## Test strategy

Each check has:
- A "happy path" test using `t.TempDir()` + a fresh store
- A "failure" test that forces the check to fail (delete the DB file,
  corrupt the schema, fill the disk via a quota mock, etc.)

Top-level `TestDiagnose` runs all checks and asserts `OverallStatus`
matches the worst child status.
