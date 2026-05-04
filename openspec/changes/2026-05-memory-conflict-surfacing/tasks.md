# Tasks — Memory conflict surfacing

## Migration
- [ ] `internal/store/store.go` — add `migrateConflictsTable()` called from `migrate()`.

## Package
- [ ] `internal/conflicts/conflicts.go` — types: `Candidate`, `Verdict`, status constants.
- [ ] `internal/conflicts/detector.go` — `DetectCandidates(db, obs, limit)`.
- [ ] `internal/conflicts/store.go` — `InsertPending`, `RecordVerdict`, `RecordCompare`.
- [ ] `internal/conflicts/conflicts_test.go` — 6+ test functions covering happy path, limit, audit fields, no-false-positive.

## Save flow
- [ ] `cmd/aria-core/aria_mem_adapter.go` — call detector after insert; persist pending conflicts; return them in the response.

## MCP tools
- [ ] `internal/mcp/aria_conflicts.go` — register `aria_judge` and `aria_compare`.
- [ ] `internal/mcp/mcp.go` — add `aria_judge` and `aria_compare` to allowlist defaults (agent profile).

## Tests
- [ ] Detection unit tests pass.
- [ ] `go test ./...` clean (allowing pre-existing `TestInstallOpenCodeBakesENGRAMBIN` failures).

## Docs
- [ ] `openspec/specs/mcp/spec.md` — bump tool count, mention conflict surface.

## Release
- [ ] Commit `feat(conflicts): memory conflict surfacing (B.2)`.
- [ ] PR + snapshot CI green.
- [ ] Merge.
- [ ] Tag v0.3.0.
- [ ] Verify release.yml green.
