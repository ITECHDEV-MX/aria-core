# Verify Report — Cross-platform release

**Date**: 2026-05-03
**Branch**: `feat/cross-platform-release`
**Commits**: `5d12b54`, `631bc98`, `0e0f38b`

## What was tested

### Snapshot build
```
$ goreleaser release --snapshot --clean --skip=publish
...
• release succeeded after 12s (cached) / 47s (cold)
```

Output: 6 binaries + 6 archives + checksums.txt + brew formula
generated locally (publish skipped per `skip_upload: auto`).

Sizes:
- linux_amd64: 11M
- linux_arm64: 9.4M
- darwin_amd64: 11M
- darwin_arm64: 9.8M
- windows_amd64: 11M
- windows_arm64: 9.4M

### Linux binary smoke test
```
$ /tmp/aria-core-test --help
aria-core v0.0.0-SNAPSHOT-5a68a61 — Persistent memory for AI coding agents

Commands:
  serve [port]       Start HTTP API server (default: 7437)
  mcp [--tools=PROFILE] [--project=NAME]
  tui                Launch interactive terminal UI
  search <query>     ...
  save <title> <msg> ...
```

15 MCP tools registered. `agent` profile = 11, `admin` = 4, `all` = 15.

### Cursor installer
```
$ /tmp/aria-core-test setup cursor
✓ Installed cursor plugin (1 files)
  → /root/.cursor

$ cat /root/.cursor/mcp.json
{
  "mcpServers": {
    "aria-core": {
      "args": ["mcp", "--tools=agent"],
      "command": "/tmp/aria-core-test"
    }
  }
}

$ /tmp/aria-core-test setup cursor   # idempotency
✓ Installed cursor plugin (1 files)
  → /root/.cursor

$ diff <(cat /root/.cursor/mcp.json) <(cat /root/.cursor/mcp.json.before)
# (empty — no drift)
```

### VS Code installer (fallback path)
No `code` in PATH on VPS. Fallback exercised:
```
$ /tmp/aria-core-test setup vscode
✓ Installed vscode plugin (1 files)
  → /root/.config/Code/User

$ cat /root/.config/Code/User/settings.json
{
  "mcp": {
    "servers": {
      "aria-core": { "args": ["mcp", "--tools=agent"], "command": "/tmp/aria-core-test" }
    }
  }
}
```

## Test results

```
$ go test ./internal/setup/...
... (failures pre-existing in TestInstallOpenCodeBakesENGRAMBIN — verified
     unrelated to this change via git stash + run)
```

## Outcome

All A.1–A.4 success criteria met. Pending: human merge + tag for
first real release with brew formula publishing.
