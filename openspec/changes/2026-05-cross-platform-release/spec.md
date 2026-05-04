# Spec — Cross-platform release

## CLI surface (added)

- `aria-core setup cursor` — install Cursor MCP config
- `aria-core setup vscode` (aliases: `vs-code`, `code`) — install VS Code MCP config

Both are idempotent: re-running replaces the `aria-core` entry without
touching unrelated server entries.

## Files added

- `.github/workflows/release.yml` — tag-driven goreleaser
- `.github/workflows/snapshot.yml` — PR-driven snapshot, uploads artifacts
- `Makefile` targets: `build`, `build-snapshot`, `release-snapshot`, `release-check`, `release`, `run-mcp`, `run-serve`

## Files modified

- `.goreleaser.yaml` — refined ldflags, archive includes, conditional brew, release notes
- `.gitignore` — `/dist/` and `/bin/`
- `internal/setup/setup.go` — `installCursor()`, `installVSCode()`, plus updates to `SupportedAgents()` and `Install()` switch
- `docs/INSTALLATION.md` — Quick Start, corrected brew tap, agent paths table

## Behavior contract

- Running `aria-core setup` (interactive) lists 6 agents (was 4).
- Running `aria-core setup cursor` exits 0, writes `~/.cursor/mcp.json`,
  prints `✓ Installed cursor plugin (1 files)`.
- Running `aria-core setup vscode` exits 0 even when `code` CLI is
  absent (uses settings.json fallback).
- Running `aria-core setup unknown` exits 1 with message
  `unknown agent: "unknown" (supported: opencode, claude-code, gemini-cli, codex, cursor, vscode)`.

## Out of contract

- The fallback path for VS Code does not preserve JSON5 comments. Users
  with JSON5-style settings.json see a warning in the Instructions
  field. (Documented, not fixed in this change.)
