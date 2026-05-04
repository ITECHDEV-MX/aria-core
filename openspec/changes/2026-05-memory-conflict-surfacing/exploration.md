# Exploration — Memory conflict surfacing

**Date**: 2026-05-04
**Trigger**: Port engram v1.14.0 feature into ARIA Core, as recommended in the
release-notes review. Identified as ⭐⭐⭐ ROI for ARIA: agent memory will
contradict itself over time across sessions and devs.

## Findings

### Engram pattern (v1.14.0)

When a save's content overlaps with prior memories on similar topic_key /
title / FTS hits, engram surfaces the candidates back to the caller with a
`judgment_id`. The agent then either resolves directly (high-confidence
auto-merge) or asks the human in chat. Resolution is persisted with audit
trail (who, when, model, confidence, reason).

Engram tools added:
- `mem_judge` — record a verdict on a pending judgment.
- `mem_compare` — persist an agent-judged semantic verdict between two
  observations.
- `engram conflicts list/show/scan/stats/replay` (CLI).

### ARIA Core current state

- `observations` table already has `topic_key`, `normalized_hash`,
  `revision_count`, `duplicate_count`, plus FTS5 with topic_key indexed.
  Most of the substrate is here.
- `aria_save` UPSERTs by `(project, topic_key)` already — but it does
  not surface contradictions across distinct topic_keys nor across
  devs/sessions sharing the cloud store.
- No `aria_judge` / `aria_compare` tools today.
- No conflicts table.
- Migration pattern is **idempotent migration functions**
  (`CREATE TABLE IF NOT EXISTS` + `PRAGMA table_info` checks),
  not numbered migrations.

### What ARIA-shaped detection should look like

Cheap signals first (no embeddings yet):
1. **Same `topic_key` + same `(project, scope)`**: high-confidence duplicate
   candidate — already handled by UPSERT path. Only flag if the new content
   diverges from the prior `normalized_hash`.
2. **Different `topic_key` but FTS5 overlap on title** within the same
   `(project, scope)`: medium-confidence contradiction candidate. Surface
   the top-N matches with overlap > threshold.
3. **Same title prefix or shared key facts** across distinct topic_keys:
   weaker signal, last priority.

Surface the **top 1-3 candidates** to the agent on save, not all matches.

## Risks

- **False alarms drown the agent.** Threshold tuning matters. Start
  conservative — only flag when normalized_hash diverges or FTS5 score
  is high. Better to under-surface than over-surface in v1.
- **Detection on hot path.** `aria_save` becomes 2x slower if detection
  runs synchronously and unbounded. Cap at < 50ms with hard query
  timeouts and `LIMIT 10`.
- **Storage growth.** `aria_memory_conflicts` could grow large if every
  save triggers an entry. Index aggressively and prune resolved items
  > 90 days as a follow-up (out of scope for this change).

## Constraints

- B.2 ships SQLite local first. Postgres cloud-side mirror lives as a
  follow-up (B.2.1) once we observe the local pattern.
- No embedding-based similarity in B.2 — too much infra for first cut.
  FTS5 + topic_key is the MVP.
- No CLI subcommand `aria-core conflicts`. MCP tools are sufficient
  for the agent loop. CLI follows in a later iteration if humans need
  to inspect conflict history outside the dashboard.
