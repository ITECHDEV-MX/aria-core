# Design — /Historias artifact protocol

## Filesystem layout

```
<repo>/Historias/                  (default; HISTORIAS_ROOT env override)
└── <slug>/                         (one per historia)
    ├── 0-office-hours.md
    ├── 1-ceo-plan.md
    ├── 2-eng-plan.md
    ├── 3-story.md
    └── MANIFEST.yaml
```

## MANIFEST.yaml

```yaml
# /Historias chain manifest. Updated by aria_artifact_save.
slug: 2026-05-mantenimiento-industrial-cotizador
created_at: 2026-05-04T08:00:00Z
created_by: contacto@itechpymes.com.mx
status: in_progress
chain:
  - position: 0
    artifact: 0-office-hours.md
    skill: office-hours@1.0.0
    agent_model: claude-opus-4-7
    content_sha256: abc123...
    inputs: []
    duration_ms: 8200
    created_at: 2026-05-04T08:01:00Z
  - position: 1
    artifact: 1-ceo-plan.md
    skill: plan-ceo-review@1.0.0
    ...
final_artifact: 3-story.md
```

`status` ∈ {`in_progress`, `completed`, `abandoned`}.

## MCP surface

| Tool | Read/Write | Purpose |
|---|---|---|
| `aria_artifact_save` | W | Write artifact + upsert manifest entry |
| `aria_artifact_get` | R | Read entry + body |
| `aria_artifact_list` | R | List slugs OR chain entries |
| `aria_artifact_complete` | W | Mark chain done with final_artifact |

## Test strategy

Hermetic with `t.TempDir()`. 12 tests cover:

- Happy save (returns entry with hash).
- Chain order preserved.
- Invalid slug rejected.
- Bad filename rejected.
- Position mismatch rejected.
- First-writer-wins.
- Overwrite=true succeeds.
- Inputs must exist.
- ListSlugs returns sorted set.
- CompleteChain mutates status.
- GetArtifact missing returns ErrArtifactMissing.
- Manifest persisted to disk.
