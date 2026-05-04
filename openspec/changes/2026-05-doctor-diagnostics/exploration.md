# Exploration — aria_doctor (read-only operational diagnostics)

**Date**: 2026-05-04
**Trigger**: JC requested porting engram v1.15.0 features. `mem_doctor`
was identified in [the engram release plan](../2026-05-cross-platform-release/exploration.md)
as ⭐⭐ valuable for ARIA. Lower risk than memory-conflict-surfacing.

## Findings

### Engram's `mem_doctor` (v1.15.0)

- Read-only by default (`mem_doctor` MCP tool, `engram doctor` CLI)
- Returns text or JSON
- Checks: DB integrity, FTS5 index, sessions table, project locking,
  config validity
- Has a separate `engram doctor repair --plan/--dry-run/--apply` (not
  in MCP, only CLI) — out of scope for B.1.

### ARIA Core current state

- `internal/store/cloud_upgrade.go` already has `RepairCloudUpgrade()`
  and `DiagnoseCloudUpgradeLegacyMutations()` — **the Diagnose pattern
  is precedented** in the codebase.
- No top-level `internal/doctor/` package yet.
- No `aria-core doctor` CLI command.
- No `aria_doctor` MCP tool.
- `internal/store/store.go` exposes `migrate()` (private) and various
  query hooks. Schema version visible via `PRAGMA user_version`.

### What ARIA-specific diagnostics should check

Local mode (default `aria-core mcp`):
1. Config validity — DataDir resolvable, env var sanity
2. DB file — SQLite at `<DataDir>/aria-core.db`, exists, writable
3. Schema version — current `user_version` matches migrate() expected
4. Core tables present — observations, sessions, skills, prompts, etc.
5. FTS5 index — queryable, document count
6. Disk space — DataDir > 100 MB free
7. Recent activity — last `aria_save` timestamp
8. Session — `session.json` if exists, JWT not expired

Cloud mode adds:
9. Cloud reachable — HTTP ping to `aria-core cloud` URL from session
10. Vault key — `/etc/systemd/.../override.conf` readable to the
    server process (only relevant on the cloud server, not the dev
    client — defer)

For B.1 scope: focus on local-mode checks 1–8. Cloud reachability is
a one-line addition once we wire it; vault key is server-side only.

## Risks

- **False alarms** — disk space < 100 MB on a Mac with 50 MB free is
  a real warning; on a CI box with full disk it's spurious. Use a
  conservative threshold (50 MB) and report actual numbers, not just
  pass/fail.
- **Slow checks** — FTS5 query on huge stores. Use `LIMIT 1` form to
  keep doctor fast (< 200ms target).
- **Doctor should never write**. Even repair planning is read-only;
  only `--apply` mutates. For B.1, no repair at all.

## Constraints

- Compatible with both SQLite local and (eventually) Postgres cloud
  store — but B.1 ships local only.
- Must not require auth for local mode (works without `aria-core login`).
- JSON output must be machine-parseable for CI / monitoring.
