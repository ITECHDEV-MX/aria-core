[← Back to README](../README.md)

# Installation

- [Quick Start](#quick-start)
- [Homebrew (macOS / Linux)](#homebrew-macos--linux)
- [Windows](#windows)
- [Install from source (macOS / Linux)](#install-from-source-macos--linux)
- [Download binary (all platforms)](#download-binary-all-platforms)
- [Requirements](#requirements)
- [Environment Variables](#environment-variables)
- [Windows Config Paths](#windows-config-paths)

---

## Quick Start

```bash
# Pre-requisite: this repo is private. Configure git auth once:
gh auth login                  # or: git config --global url.https://YOUR-PAT@github.com/.insteadOf https://github.com/
export GOPRIVATE=github.com/ITECHDEV-MX

# Install via go (any platform with Go ≥ 1.25)
go install github.com/ITECHDEV-MX/aria-core/cmd/aria-core@latest

# Or download a prebuilt binary from GitHub Releases (auth required while repo is private):
# https://github.com/ITECHDEV-MX/aria-core/releases/latest

# Wire up your agent (interactive menu)
aria-core setup
# Or directly:
aria-core setup cursor
aria-core setup vscode
aria-core setup claude-code

# Start MCP server (stdio — what the agent talks to)
aria-core mcp --tools=agent

# Or use the local CLI directly
aria-core save "decision: use Postgres" "Picked Postgres over MongoDB because..."
aria-core search "postgres"
aria-core tui                  # interactive terminal UI
aria-core serve                # HTTP API on port 7437
```

That's the whole loop. Below is the full reference.

---

## Homebrew (macOS / Linux)

```bash
brew install ITECHDEV-MX/homebrew-tap/aria-core
```

Upgrade to latest:

```bash
brew update && brew upgrade aria-core
```

> **Migrating from Cask?** If you installed aria-core before v1.0.1, it was distributed as a Cask. Uninstall first, then reinstall:
> ```bash
> brew uninstall --cask aria-core 2>/dev/null; brew install ITECHDEV-MX/homebrew-tap/aria-core
> ```

---

## Windows

**Option A: Install via `go install` (recommended for technical users)**

If you have Go installed, this is the cleanest and most trustworthy path — the binary is compiled on your machine from source, so no antivirus will flag it:

```powershell
go install github.com/ITECHDEV-MX/aria-core/cmd/aria-core@latest
# Binary goes to %GOPATH%\bin\aria-core.exe (typically %USERPROFILE%\go\bin\)
```

Ensure `%GOPATH%\bin` (or `%USERPROFILE%\go\bin`) is on your `PATH`.

**Option B: Build from source**

```powershell
git clone https://github.com/ITECHDEV-MX/aria-core.git
cd aria-core
go install ./cmd/aria-core
# Binary goes to %GOPATH%\bin\aria-core.exe (typically %USERPROFILE%\go\bin\)

# Optional: build with version stamp (otherwise `aria-core version` shows "dev")
$v = git describe --tags --always
go build -ldflags="-X main.version=local-$v" -o aria-core.exe ./cmd/aria-core
```

**Option C: Download the prebuilt binary**

1. Go to [GitHub Releases](https://github.com/ITECHDEV-MX/aria-core/releases)
2. Download `aria-core_<version>_windows_amd64.zip` (or `arm64` for ARM devices)
3. Extract `aria-core.exe` to a folder in your `PATH` (e.g. `C:\Users\<you>\bin\`)

```powershell
# Example: extract and add to PATH (PowerShell)
Expand-Archive aria-core_*_windows_amd64.zip -DestinationPath "$env:USERPROFILE\bin"
# Add to PATH permanently (run once):
[Environment]::SetEnvironmentVariable("Path", "$env:USERPROFILE\bin;" + [Environment]::GetEnvironmentVariable("Path", "User"), "User")
```

> **Antivirus false positives on prebuilt binaries**
>
> Windows Defender and other antivirus tools (ESET, Brave's built-in scanner) have flagged some
> aria-core prebuilt releases as malware (`Trojan:Script/Wacatac.H!ml` or similar). This is a
> **heuristic false positive**. The binary is built reproducibly from the public source code
> via GoReleaser and contains no malicious code.
>
> **Why does this happen?** Prebuilt binaries from small open-source projects are unsigned (code
> signing certificates cost hundreds of dollars per year). Many AV engines automatically flag
> unsigned executables from unknown publishers, especially recently compiled Go binaries. The
> same alert has been observed on Claude Code's own MSIX installer, which confirms this is an
> AV heuristic issue, not a code problem.
>
> **Maintainer stance:** We will not pay for a code signing certificate at this time. This is a
> distribution trust problem, not a security problem. The source code is fully auditable.
>
> **Recommended workaround:** Technical Windows users should prefer **Option A (`go install`)** or
> **Option B (build from source)**. Binaries you compile locally will not trigger AV alerts because
> they originate from your own machine.

> **Other Windows notes:**
> - Data is stored in `%USERPROFILE%\.aria-core\aria-core.db`
> - Override with `ARIA_CORE_DATA_DIR` environment variable
> - All core features work natively: CLI, MCP server, TUI, HTTP API, Git Sync
> - No WSL required for the core binary — it's a native Windows executable

---

## Install from source (macOS / Linux)

```bash
git clone https://github.com/ITECHDEV-MX/aria-core.git
cd aria-core
go install ./cmd/aria-core

# Optional: build with version stamp (otherwise `aria-core version` shows "dev")
go build -ldflags="-X main.version=local-$(git describe --tags --always)" -o aria-core ./cmd/aria-core
```

---

## Download binary (all platforms)

Grab the latest release for your platform from [GitHub Releases](https://github.com/ITECHDEV-MX/aria-core/releases).

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `aria-core_<version>_darwin_arm64.tar.gz` |
| macOS (Intel) | `aria-core_<version>_darwin_amd64.tar.gz` |
| Linux (x86_64) | `aria-core_<version>_linux_amd64.tar.gz` |
| Linux (ARM64) | `aria-core_<version>_linux_arm64.tar.gz` |
| Windows (x86_64) | `aria-core_<version>_windows_amd64.zip` |
| Windows (ARM64) | `aria-core_<version>_windows_arm64.zip` |

---

## Requirements

- **Go 1.25+** to build from source (not needed if installing via Homebrew or downloading a binary)
- That's it. No runtime dependencies.

The binary includes SQLite (via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — pure Go, no CGO). Works natively on **macOS**, **Linux**, and **Windows** (x86_64 and ARM64).

---

## Environment Variables

| Variable | Description | Default |
|---|---|---|
| `ARIA_CORE_DATA_DIR` | Data directory | `~/.aria-core` (Windows: `%USERPROFILE%\.aria-core`) |
| `ARIA_CORE_PORT` | HTTP server port | `7437` |

---

## Windows Config Paths

When using `aria-core setup`, config files are written to platform-appropriate locations:

| Agent | macOS / Linux | Windows |
|-------|---------------|---------|
| OpenCode | `~/.config/opencode/` | `%APPDATA%\opencode\` |
| Gemini CLI | `~/.gemini/` | `%APPDATA%\gemini\` |
| Codex | `~/.codex/` | `%APPDATA%\codex\` |
| Claude Code | `~/.claude/settings.json` (allowlist) + marketplace plugin | `%USERPROFILE%\.claude\settings.json` + marketplace plugin |
| Cursor | `~/.cursor/mcp.json` | `%USERPROFILE%\.cursor\mcp.json` |
| VS Code | `code --add-mcp` CLI preferred; fallback `~/Library/Application Support/Code/User/settings.json` (macOS) / `~/.config/Code/User/settings.json` (Linux) | `code --add-mcp` CLI preferred; fallback `%APPDATA%\Code\User\settings.json` |
| Antigravity | Manual JSON config | Manual JSON config |
| Windsurf | Manual JSON config | Manual JSON config |
| Data directory | `~/.aria-core/` | `%USERPROFILE%\.aria-core\` |
