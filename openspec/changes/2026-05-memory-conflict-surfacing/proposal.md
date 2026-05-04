# Proposal — Memory conflict surfacing

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/memory-conflict-surfacing`)

## Problem

Agent memory accumulates contradictions over time. Two devs working on
the same project end up with two `topic_key`s for the same subject.
A decision made 3 months ago gets quietly contradicted by a new save.
Engram identified and solved this in v1.14.0; ARIA needs the same
guardrail.

## Goal

When `aria_save` persists an observation that may contradict a prior
one, return the candidate matches alongside the success ack. The agent
then either auto-resolves (if confidence is high) or asks the user.
Resolutions are recorded in an audit-friendly conflicts table.

## Scope

In:
- New table `aria_memory_conflicts` with idempotent migration.
- `internal/conflicts/` package: `Conflict`, `DetectCandidates`,
  `RecordVerdict`.
- `aria_save` flow change: after insert, run detector; if candidates
  found, insert pending conflict rows and return their IDs in the save
  response (additive field, no breaking change).
- New MCP tools:
  - `aria_judge(conflict_id, verdict, reason?, confidence?, model?)`
    — record verdict; valid verdicts: `supersedes`, `equivalent`,
    `conflicting`, `dismissed`.
  - `aria_compare(observation_a_id, observation_b_id, verdict, reason?, confidence?)`
    — record a semantic verdict between two arbitrary observations
    (does not require a pre-existing conflict row).
- Tests for detection (no false positive on unrelated saves, true
  positive on contradictory saves with same topic_key and changed
  hash).

Out (deferred):
- Cloud-side conflict mirror (Postgres) — follow-up.
- CLI `aria-core conflicts` subcommand — follow-up.
- Embedding-based similarity — out of scope.
- Auto-merge or auto-supersede mutations on observations — only the
  conflict row is mutated; observations remain immutable in this
  change.
- Repair / pruning of stale conflicts — follow-up.

## Alternatives considered

- **Run detection async after save returns.** Rejected — the agent
  needs the candidates synchronously to decide whether to ask the user
  before continuing. Async loses the in-line teachable moment.
- **Store verdicts directly on observations (e.g., `superseded_by`
  column).** Rejected for now — it conflates the audit log with the
  observation lifecycle. Add later if a clear use case emerges.
- **Embedding similarity at save time.** Rejected — too much infra
  for v1. FTS5 + topic_key gives 80% of the value at 5% of the cost.

## Success criteria

- [x] Detection completes in < 50ms on a store of 10k observations.
- [x] No false positive when saving with a fresh `topic_key` and no
      content overlap.
- [x] True positive when saving a new observation with the same
      `topic_key` and a divergent `normalized_hash`.
- [x] `aria_judge` round-trips: agent calls with verdict → conflict
      row updated, audit fields populated.
- [x] All tests pass.
