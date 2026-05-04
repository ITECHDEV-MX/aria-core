# Proposal — /Historias artifact protocol (F2)

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/historias-f2`)

## Problem

After F1.a/F1.b shipped governance, ARIA still has no place to put the
artifacts produced by chained agents. The story-creation pipeline JC
designed (office-hours → ceo-plan → eng-plan → story) needs a
file-backed home with a typed chain manifest.

## Goal

Ship the file protocol + MCP tools so any orchestrator can run a
chained agent flow and end up with a clean `/Historias/<slug>/`
directory + a parseable `MANIFEST.yaml`.

## Scope

In:
- `internal/historias/` Go package (types, SaveArtifact, GetArtifact,
  ListChain, ListSlugs, CompleteChain).
- `internal/mcp/aria_historias.go` registering 4 MCP tools.
- 12 hermetic tests covering happy/sad paths.
- MCP test count assertions updated 19 → 23.
- openspec change folder.

Out (deferred):
- `aria_pages` mirror (F2.1).
- Dashboard `/dashboard/historias` view (F5).
- CLI subcommand `aria-core historias list` (added in F2.1 if asked).
- Concurrent write locking.

## Success criteria

- 12 hermetic tests pass.
- Tool count goes 19 → 23.
- Build clean across the repo.
- Snapshot CI green.
