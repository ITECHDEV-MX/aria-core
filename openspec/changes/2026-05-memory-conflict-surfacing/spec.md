# Spec — Memory conflict surfacing

## Behavior contract

### `aria_save` (local handler) — additive change

After successful insert, the response gains an optional `conflicts`
field:

```json
{
  "saved": { "id": 1234, ... },
  "conflicts": [
    {
      "conflict_id": 42,
      "candidate_observation_id": 100,
      "signal": "topic_key_hash_diverge",
      "score": 1.0,
      "prior_title": "Use Clean Architecture",
      "prior_snippet": "Decided to standardize on Clean Architecture..."
    }
  ]
}
```

If no candidates are detected, the field is either absent or an empty
array. Existing callers that ignore unknown fields keep working.

### `aria_judge`

Required args: `conflict_id`, `verdict`. Optional: `reason`,
`confidence`, `model`, `session_id`.

Behavior:
- Looks up the conflict by id. Errors with `not_found` if missing.
- Errors with `already_resolved` if status != 'pending'.
- Updates: `status='resolved'`, `verdict`, `verdict_reason`,
  `verdict_confidence`, `verdict_model`, `verdict_session_id`,
  `resolved_at = now`.
- Returns the updated conflict row.

### `aria_compare`

Required: `observation_a_id`, `observation_b_id`, `verdict`.
Optional: `reason`, `confidence`, `model`.

Behavior:
- Inserts a new conflict row with `detection_signal='manual_compare'`,
  `detection_score=null`, `status='resolved'`, the supplied verdict,
  audit fields, `resolved_at = now`.
- Returns the new conflict row.

## Detection guarantees

- Detection runs synchronously inside `aria_save` after commit.
- Detection MUST complete in under 50ms for a store with up to 10k
  observations. Hard cap: `LIMIT 10` candidates per signal, query
  timeout 100ms.
- Detection NEVER fails the save. If the detector errors, the save
  still succeeds and the response simply omits the `conflicts` field.
- Detection MUST NOT mutate any observations.

## Out of contract

- Cross-tenant conflict detection (current cut: same project + scope).
- Visual conflict review UI in dashboard (follow-up).
- Auto-resolution of dismissed conflicts (manual only).
