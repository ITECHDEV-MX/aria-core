# Exploration — Cross-platform release infrastructure

**Date**: 2026-05-03
**Driver**: JC (iTech)
**Inspiration**: [Gentleman-Programming/engram](https://github.com/Gentleman-Programming/engram), the project ARIA Core started from.

## Findings

1. **`.goreleaser.yaml` exists since the initial commit (Apr 26)** but was
   missing the GitHub Actions workflow to drive it on tag push.
2. **`goreleaser` not installed on the VPS** — added via `go install`.
3. **Snapshot build worked first try**: 6 binaries (linux/darwin/windows × amd64/arm64),
   ~10MB each, 47s clean, 12s cached. CGO_ENABLED=0 confirmed pure Go.
4. **`internal/setup/` already had 4 agents wired** (opencode, claude-code,
   gemini-cli, codex). Cursor and VS Code were missing.
5. **Local SQLite mode already worked** via `modernc.org/sqlite` (pure Go).
   `~/.aria-core/aria-core.db` resolved cross-platform via `os.UserHomeDir()`.
6. **15 MCP tools registered** across 3 profiles (agent/admin/all). Naming
   `aria_*` (not `mem_*` like engram).
7. **Pre-existing test failures** in `TestInstallOpenCodeBakesENGRAMBIN/*`.
   Verified unrelated to release infra.

## Risks

- **Brew tap publish requires `HOMEBREW_TAP_TOKEN`** secret + a separate
  `ITECHDEV-MX/homebrew-tap` repo. Until that's wired, releases publish
  binaries to GitHub Releases but skip the brew formula. Documented in
  the workflow's `skip_upload: auto`.
- **Antivirus false positives** on unsigned Windows binaries are expected
  (engram has the same problem). Mitigated by recommending `go install`.

## Constraints

- ARIA Core is internal iTech tooling, not public SaaS — no need for
  signed installers, brew cask, MSI builders.
- Postgres remains the cloud source of truth; SQLite is personal mirror.
