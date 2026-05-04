# Exploration — /Historias artifact protocol (F2)

**Date**: 2026-05-04
**Driver**: gstack patterns + JC's choice of multi-agent chain with
dual persistence (file + aria_pages mirror).

## Findings

### What we need
- Filesystem-backed artifact chain under `/Historias/<slug>/`.
- Per-slug `MANIFEST.yaml` audit log.
- MCP tools so the orchestrator can save/get/list artifacts.
- Optional: mirror to `aria_pages` for cloud index.

### Decisions

- **Slug as primary key**: regex `^[a-z0-9]+(-[a-z0-9]+)*$`. No spaces,
  no uppercase, no special chars. Same convention as our openspec
  changes folder.
- **Filename pattern `<position>-<short>.md`**: matches the schema
  field `outputs.artifact` from F1.a / agent-skills.
- **First-writer-wins by default**, `overwrite=true` to replace —
  protects against retries clobbering deliberate work.
- **MANIFEST.yaml is the single source of truth** for chain state. Files
  alone are insufficient because they don't record skill/agent/inputs.
- **`aria_pages` sync deferred**: keep F2 file-only for testability;
  add the sync hook in F2.1 once we have a UI to display them.

## Risks

- **Concurrent writes**: two agents save to the same position
  simultaneously and the second clobbers MANIFEST. Mitigated by
  upsert semantics + sort by position. Real concurrency control
  (locks) is overkill until we have actual contention.
- **No max chain length**: a runaway agent could create unbounded
  artifacts. Out of scope for F2 — orchestrator's responsibility.

## Constraints

- Pure file-based, no DB migration needed.
- No external dependencies beyond gopkg.in/yaml.v3 (already in go.mod).
- MCP tools follow existing naming convention (`aria_*`).
