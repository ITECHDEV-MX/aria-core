# Proposal — Cross-platform release for aria-core

**Owner**: contacto@itechpymes.com.mx
**Status**: Applied (branch `feat/cross-platform-release`, PR pending merge)

## Problem

ARIA Core lives on the VPS. iTech devs are 90% Windows, ~10% Mac. To
adopt ARIA they have to SSH to the server. That's a non-starter for
daily use.

## Goal

Make `aria-core` installable in 30 seconds on Linux, Mac, and Windows
(amd64 + arm64), with one command to wire it to the dev's agent
(Cursor, VS Code, Claude Code, etc.).

## Scope

In:
- GoReleaser pipeline producing 6 cross-platform binaries on every tag
- GitHub Actions workflow `release.yml` (tag-driven) + `snapshot.yml` (PR-driven)
- `aria-core setup cursor` and `aria-core setup vscode` (first-class)
- INSTALLATION.md aligned with reality (correct brew tap, Quick Start, table of agent paths)

Out:
- Code-signing certificates (rejected — antivirus false positives are
  acceptable for internal tooling)
- MSI / Cask installers (rejected — out of scope for v0.1)
- Auto-update mechanism (deferred)

## Alternatives considered

- **Manual release process** — rejected, doesn't scale and breaks under
  human error.
- **Docker-only distribution** — rejected, devs need the binary on the
  host to wire MCP into Cursor/VS Code which run on the host.
- **`go install` only, no GitHub Releases** — rejected, devs without Go
  can't install.

## Success criteria

- [x] `goreleaser release --snapshot --clean --skip=publish` produces 6 binaries
- [x] `aria-core setup cursor` writes valid `~/.cursor/mcp.json` and is idempotent
- [x] `aria-core setup vscode` falls back gracefully when `code` CLI absent
- [x] INSTALLATION.md Quick Start works copy-paste on Linux/Mac/Windows
- [ ] First tag (`v0.1.0`) cut and binaries available at GitHub Releases (pending human)
