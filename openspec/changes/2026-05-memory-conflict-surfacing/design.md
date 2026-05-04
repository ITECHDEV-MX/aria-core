# Design — Memory conflict surfacing

## New table

```sql
CREATE TABLE IF NOT EXISTS aria_memory_conflicts (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_a_id      INTEGER NOT NULL,
    observation_b_id      INTEGER NOT NULL,
    project               TEXT,
    topic_key             TEXT,
    detection_signal      TEXT NOT NULL,   -- 'topic_key_hash_diverge' | 'fts_title_overlap' | ...
    detection_score       REAL,            -- 0.0-1.0 confidence of the DETECTION (not the verdict)
    status                TEXT NOT NULL DEFAULT 'pending',  -- pending|resolved|dismissed
    verdict               TEXT,            -- supersedes|equivalent|conflicting|dismissed (set on resolve)
    verdict_reason        TEXT,
    verdict_confidence    REAL,
    verdict_model         TEXT,            -- e.g. 'claude-opus-4-7'
    verdict_session_id    TEXT,
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at           TEXT,
    FOREIGN KEY (observation_a_id) REFERENCES observations(id),
    FOREIGN KEY (observation_b_id) REFERENCES observations(id)
);
CREATE INDEX IF NOT EXISTS idx_conflicts_status   ON aria_memory_conflicts(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_conflicts_obs_a    ON aria_memory_conflicts(observation_a_id);
CREATE INDEX IF NOT EXISTS idx_conflicts_obs_b    ON aria_memory_conflicts(observation_b_id);
CREATE INDEX IF NOT EXISTS idx_conflicts_project  ON aria_memory_conflicts(project, status);
```

The migration sits in `internal/store/store.go` as
`migrateConflictsTable()`, called from `migrate()` after the existing
`migrateFTSTopicKey` and `migrateSyncChunksTable`.

## Detection function

```go
package conflicts

type Candidate struct {
    ConflictID       int64    // 0 if not yet inserted
    ObservationAID   int64    // the just-saved observation
    ObservationBID   int64    // the candidate predecessor
    Project          string
    TopicKey         string
    DetectionSignal  string
    DetectionScore   float64
    PriorTitle       string
    PriorContentSnippet string
}

// DetectCandidates runs after a save commit. It returns at most
// `limit` candidates ordered by score descending.
//
// The function is read-only — it does NOT insert into
// aria_memory_conflicts. The caller (cmd/aria-core/aria_mem_adapter)
// decides which to persist (typically all returned candidates).
//
// Detection signals tried in order:
//   1. topic_key_hash_diverge: same (project, topic_key) + different
//      normalized_hash than the previous most-recent observation.
//      Score = 1.0 (strongest signal).
//   2. fts_title_overlap: FTS5 MATCH on title within (project, scope)
//      with score > 0.5 (using bm25). Excludes any rows already
//      caught by signal 1.
//
// Both signals time-bounded: `WHERE created_at > observed.created_at - 365 days`
// to keep the search bounded.
func DetectCandidates(db *sql.DB, observation Observation, limit int) ([]Candidate, error)
```

## Save flow change

`cmd/aria-core/aria_mem_adapter.go` (the local-store mem_save handler):

1. INSERT observation as today.
2. Call `conflicts.DetectCandidates(db, saved, 3)`.
3. For each candidate, INSERT into `aria_memory_conflicts` with
   `status='pending'`. Capture the `id`s.
4. Augment the response payload:
   ```json
   {
     "saved": { ... },
     "conflicts": [
       {
         "conflict_id": 42,
         "candidate_observation_id": 100,
         "signal": "topic_key_hash_diverge",
         "score": 1.0,
         "prior_title": "...",
         "prior_snippet": "..."
       }
     ]
   }
   ```
5. The agent reads the `conflicts` array and decides whether to call
   `aria_judge` directly (high confidence) or ask the user.

The cloud `aria_save` (in `internal/mcp/aria_cloud.go`) hits the
HTTP API. Cloud-side detection is a follow-up; for now, the cloud
handler returns an empty `conflicts` array.

## MCP tools

### `aria_judge`

```go
mcp.NewTool("aria_judge",
    mcp.WithDescription("Record a verdict on a pending memory conflict surfaced by aria_save."),
    mcp.WithString("conflict_id", mcp.Required(), mcp.Description("Conflict row ID returned by aria_save")),
    mcp.WithString("verdict", mcp.Required(), mcp.Description("supersedes | equivalent | conflicting | dismissed")),
    mcp.WithString("reason", mcp.Description("Free-text rationale for the verdict")),
    mcp.WithString("confidence", mcp.Description("0.0-1.0 confidence")),
    mcp.WithString("model", mcp.Description("e.g. claude-opus-4-7")),
    mcp.WithString("session_id", mcp.Description("Active session ID for audit")),
)
```

### `aria_compare`

```go
mcp.NewTool("aria_compare",
    mcp.WithDescription("Persist a semantic verdict comparing two observations. Use when the agent identifies a relationship not surfaced by detection."),
    mcp.WithString("observation_a_id", mcp.Required()),
    mcp.WithString("observation_b_id", mcp.Required()),
    mcp.WithString("verdict", mcp.Required(), mcp.Description("supersedes | equivalent | conflicting | dismissed | related")),
    mcp.WithString("reason"),
    mcp.WithString("confidence"),
    mcp.WithString("model"),
)
```

`aria_compare` inserts a new row in `aria_memory_conflicts` with
`status='resolved'` and the verdict already filled — the detection
signal is `manual_compare`.

## Test strategy

Hermetic tests using `t.TempDir()` + a fresh store:
- `TestDetect_NoCandidates_FreshStore`
- `TestDetect_TopicKeyHashDiverge`
- `TestDetect_FTSTitleOverlap`
- `TestDetect_LimitRespected`
- `TestRecordVerdict_StatusUpdates`
- `TestRecordVerdict_AuditFieldsPopulated`
