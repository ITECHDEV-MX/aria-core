# Design — Cross-platform release

## Build pipeline

```
git tag v0.1.0 → push → GitHub Actions release.yml
  ↓
  Setup Go 1.25 → go install templ → templ generate
  ↓
  goreleaser release --clean
    ↓
    For each (linux,darwin,windows) × (amd64,arm64):
      go build -ldflags="-s -w -X main.version=$tag -X main.commit=$sha -X main.date=$date"
    ↓
    Archive: tar.gz (unix) / zip (windows)
    ↓
    Checksums: SHA-256 → checksums.txt
    ↓
    Optional: brew formula → ITECHDEV-MX/homebrew-tap (if HOMEBREW_TAP_TOKEN set)
    ↓
    Publish to GitHub Releases (draft: false, prerelease: auto)
```

## Setup flow per agent

| Agent | Path | Mechanism |
|-------|------|-----------|
| Cursor | `~/.cursor/mcp.json` | JSON merge into `mcpServers.aria-core` |
| VS Code | `code --add-mcp <json>` (preferred) → `settings.json` `mcp.servers.aria-core` (fallback) | CLI invocation or JSON merge |
| Claude Code | Marketplace + `~/.claude/settings.json` allowlist | `claude plugin install` + JSON edit |
| OpenCode | `~/.config/opencode/plugins/` | TS plugin copy + ARIA_CORE_BIN patch |
| Gemini CLI | `~/.gemini/settings.json` (Linux/Mac) / `%APPDATA%/gemini/settings.json` (Win) | JSON merge |
| Codex | `~/.codex/config.toml` (Linux/Mac) / `%APPDATA%/codex/config.toml` (Win) | TOML upsert |

## Cross-platform path resolution

`os.UserHomeDir()` returns the right thing on each OS:
- Linux: `$HOME` or `/etc/passwd` lookup
- macOS: `$HOME` (set by login)
- Windows: `%USERPROFILE%`

For platform-specific paths (`%APPDATA%`, `~/Library/Application Support`),
we branch on `runtime.GOOS`.

## Test strategy

- Snapshot build runs in `snapshot.yml` on every PR — fails the PR if any
  target stops compiling.
- Local validation: `make release-snapshot`.
- E2E install validation is manual (run binary on a fresh Mac/Win VM
  before declaring v0.1 stable).
