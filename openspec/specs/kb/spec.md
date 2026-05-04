# ARIA Core — Knowledge Base Subsystem Spec

**Path**: `internal/kb/`

## Purpose

Project graph. Each project links to its repo, key files, decisions,
and the team members who own it. Drives the dashboard's
`/projects/<slug>` view and feeds the cotizador pipeline.

## Surface

- `aria_kb_status` — overall sync state
- `aria_kb_sync_project` — refresh a project from its repo
- `aria_kb_sync_quote` — link a project to a quote artifact

## Invariants

1. **Repo URL is identity**. Two projects with the same git remote are
   the same project (after slug normalization).
2. **Sync is incremental**. Diffs only, never full repo re-ingest.
3. **No code execution from KB sync**. We read, never run, repo content.
