# Proposal — aria_doctor (read-only operational diagnostics)

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/doctor-diagnostics`)

## Problem

When `aria-core` misbehaves on a dev's machine, debugging is manual:
"check that the DB file exists", "verify migrations ran", "is the FTS5
index there", etc. There's no canonical tool to give the agent (or the
human) a snapshot of the system's health.

Engram solved this with `mem_doctor` in v1.15.0. We port the
pattern to ARIA Core so that:
- A dev can run `aria-core doctor` to get a one-screen status report.
- An agent can call `aria_doctor` MCP tool to self-diagnose before
  retrying or asking for help.
- CI / monitoring can parse `aria-core doctor --json`.

## Goal

A read-only, fast (< 500ms) health check for the local ARIA store with
8 specific checks. Output as text (default) or JSON (`--json`).

## Scope

In:
- `internal/doctor/` package with `Diagnose(s *store.Store) Report`
- `Report` struct: timestamp, status (healthy/warn/error), checks []Check
- `Check`: name, status (ok/warn/error), detail, duration_ms
- CLI: `aria-core doctor [--json]`
- MCP tool: `aria_doctor` (registered in agent + admin profiles)
- 8 local-mode checks: config, db file, schema version, core tables,
  FTS5 index, disk space, recent activity, session
- Tests for each check (hermetic, no real Postgres)

Out:
- `repair` subcommand (deferred to B.3 if ever needed)
- Cloud-mode HTTP reachability checks (one-line follow-up after B.1
  lands)
- Vault key validation (server-side concern, not dev client)
- Slow/expensive checks (full table scans)

## Alternatives considered

- **No tool, just docs** — rejected, opacity is what we're solving.
- **Combine into existing `aria-core stats`** — rejected, stats is
  about content (memory count by type), doctor is about system health.
  Different concerns.
- **Auto-run doctor on every MCP server start** — rejected, too eager;
  agent should call it explicitly when needed.

## Success criteria

- [ ] `aria-core doctor` returns in < 500ms on a fresh DataDir
- [ ] Each of the 8 checks has at least one passing test and one
      failing test (where failure is forced)
- [ ] `aria_doctor` MCP tool returns same data as `--json` CLI mode
- [ ] No flaky tests
- [ ] Doctor never writes to disk (verified by integration test that
      runs on read-only DataDir)
