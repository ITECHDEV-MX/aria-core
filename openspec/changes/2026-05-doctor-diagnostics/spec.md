# Spec — aria_doctor

## CLI surface

### `aria-core doctor`

```
Usage:
  aria-core doctor [flags]

Flags:
  --json    Emit JSON report (default: human-readable text)

Exit codes:
  0  All checks OK or only informational items
  1  At least one warning
  2  At least one error
```

## MCP tool surface

### `aria_doctor`

- Description: "Read-only operational diagnostics. Returns a Report
  with the status of 8 health checks: config, db, schema, tables,
  FTS5, disk, activity, session."
- No required arguments
- Optional argument `format`: `"json"` (default) or `"text"`
- Profile: `agent` and `admin`

Response (JSON mode):
```json
{
  "timestamp": "2026-05-04T12:34:56Z",
  "version": "v0.2.0",
  "status": "ok",
  "checks": [
    {"name": "config_dir", "status": "ok", "detail": "...", "duration_ms": 2},
    ...
  ]
}
```

## Behavior contract

1. Doctor MUST be read-only. It MAY create and immediately delete a
   single tempfile under `cfg.DataDir` to verify writability — that
   tempfile MUST be deleted before the function returns.

2. Doctor MUST complete in < 500ms on a healthy store with up to
   10,000 observations.

3. Each check MUST have a fixed name (snake_case) that does not change
   across versions.

4. The `OverallStatus` is the maximum of all child statuses, with
   precedence `error > warn > ok`. An "info" item never affects
   overall status.

5. Errors during a check MUST be reported as Status=`error` with the
   error message in `detail`. The doctor MUST NOT panic.

6. The text format MUST include a "Took: <ms>" line.

7. JSON format MUST be valid JSON parseable by `json.Decode` (no
   trailing comma, no comments).
