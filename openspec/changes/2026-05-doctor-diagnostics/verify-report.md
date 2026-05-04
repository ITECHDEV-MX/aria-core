# Verify Report — aria_doctor

**Date**: 2026-05-04
**Branch**: `feat/doctor-diagnostics`

## Tests run

```
$ go test ./internal/doctor/... -count=1 -v
=== RUN   TestAggregateStatus
    --- PASS: TestAggregateStatus/all_ok
    --- PASS: TestAggregateStatus/info_doesn't_escalate
    --- PASS: TestAggregateStatus/warn_beats_ok
    --- PASS: TestAggregateStatus/error_beats_warn
    --- PASS: TestAggregateStatus/empty
--- PASS: TestAggregateStatus
--- PASS: TestRunCheckRecoversFromPanic
--- PASS: TestReportSortedByName
--- PASS: TestReportJSONShape
--- PASS: TestHumanSize
--- PASS: TestHumanizeAge
PASS
ok  	github.com/ITECHDEV-MX/aria-core/internal/doctor	0.003s
```

```
$ go build ./...
(no output — builds clean across all packages)
```

## Smoke tests on real local store

### CLI text mode

```
$ aria-core doctor

ARIA Core Doctor — diagnostic report

  Status: ✓ ok

  ✓ config_dir       /root/.aria-core (writable)
  ✓ core_tables      3/3 expected tables present (20 total in DB)
  ✓ db_file          aria-core.db (188.0 KB)
  ✓ disk_space       606.2 GB free
  ✓ fts5_index       observations_fts healthy (0 docs)
  ⓘ recent_activity  no observations yet (fresh store)
  ✓ schema_version   schema v0
  ⓘ session          not configured (run 'aria-core login' for cloud)

  Issues: 0 critical, 0 warnings, 2 informational
  Took: 1ms
```

### CLI JSON mode

Returns parseable JSON with timestamp, version, overall status, and 8
checks. Verified with `jq .status` returning `"ok"` and `jq '.checks | length'` returning `8`.

### Latency

Total wall time: ~1ms on a fresh store. Well under the 500ms budget.

## Behavior contract verification

- [x] Doctor never writes (creates + deletes one tempfile under DataDir for writability probe — verified by inspecting the file count before/after).
- [x] Each check has a fixed snake_case name.
- [x] OverallStatus is `max(rank)` over all checks; `info` does not escalate.
- [x] Errors during checks reported as Status=error with detail; no panics escape (tested via TestRunCheckRecoversFromPanic).
- [x] Text format includes "Took: <ms>" line.
- [x] JSON format is valid (verified via `json.Decode` round-trip in TestReportJSONShape).

## Outcome

All A/B-grade success criteria met. B.1 ready to merge.

Pending after merge:
- Tag v0.2.0
- Verify release.yml green
- Manual smoke tests on Mac/Win (user responsibility)
