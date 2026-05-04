# Verify Report — Memory conflict surfacing (B.2)

**Date**: 2026-05-04
**Branch**: `feat/memory-conflict-surfacing`

## Tests

```
$ go test ./internal/conflicts/... -count=1 -v
=== RUN   TestVerdictIsValid                  PASS
=== RUN   TestDetect_NoCandidates_FreshStore  PASS
=== RUN   TestDetect_TopicKeyHashDiverge      PASS
=== RUN   TestDetect_TopicKeySameHash_NoFlag  PASS
=== RUN   TestRecordVerdict_HappyPath          PASS
=== RUN   TestRecordVerdict_AlreadyResolved    PASS
=== RUN   TestRecordCompare_Manual             PASS
ok      github.com/ITECHDEV-MX/aria-core/internal/conflicts     0.013s
```

`go test ./internal/mcp/...` passes for all server-registration and
annotation tests. `TestHandleSaveSimilarProjectWarning` was already
failing on `main` (typo-project detection unrelated to this change).

## Behavior

### MCP tools registered

The MCP server now registers 19 tools (was 17). The two new tools are:
- `aria_judge` — record a verdict on a pending conflict
- `aria_compare` — manual semantic verdict between any two observations

### Schema

`aria_memory_conflicts` table created idempotently in
`Store.migrate()` via `migrateConflictsTable()`. Indexes on `status`,
`observation_a_id`, `observation_b_id`, `(project, status)`.

### Detector

`conflicts.DetectCandidates(ctx, db, observation, limit)`:
- Signal 1 (topic_key_hash_diverge): same `(project, topic_key)` +
  different `normalized_hash` than the prior most-recent observation.
  Score 1.0.
- Signal 2 (fts_title_overlap): FTS5 MATCH on the title within
  `(project, scope)`, excluding observation itself and signal-1 hits.
- Hard 100ms context budget.
- Read-only — never inserts.

## Out of scope (this commit)

The detector is wired and tested but **not yet called from
`aria_save`**. That integration is the B.2.1 follow-up. As of this
release, agents who want to use conflict surfacing must:
1. Detect candidates by querying `observations` themselves OR
2. Wait for B.2.1 to integrate detection into the save path.

The two MCP tools (`aria_judge`, `aria_compare`) work standalone — an
agent can call `aria_compare` proactively at any time to record a
relationship it discovered through reasoning.

## Outcome

✓ Schema persisted, tested, deployable.
✓ Two MCP tools registered, tested for argument validation and audit
  field population.
✓ Detector library tested with hermetic SQLite fixtures.
✓ Build clean across all packages.

Pending:
- B.2.1: integrate detection into `aria_save` flow
- B.2.2: cloud-side mirror in Postgres
- B.2.3: optional CLI subcommand `aria-core conflicts list/show`
- B.2.4: dashboard view at `/dashboard/conflicts`
