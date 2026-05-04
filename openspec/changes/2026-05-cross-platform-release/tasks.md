# Tasks — Cross-platform release

## A.1 — Release infrastructure ✅
- [x] Install goreleaser on VPS
- [x] Validate `.goreleaser.yaml` (existed since initial commit)
- [x] Snapshot build all 6 targets
- [x] Add `.github/workflows/release.yml` (tag-driven)
- [x] Add `.github/workflows/snapshot.yml` (PR-driven)
- [x] Refine `.goreleaser.yaml`: ldflags, archives, brews skip_upload auto, release notes
- [x] Update `Makefile` with build/release targets
- [x] Add `dist/` and `bin/` to `.gitignore`

## A.2 — Local client mode ✅ (already implemented)
- [x] Verify `modernc.org/sqlite` cross-platform (pure Go)
- [x] Verify `os.UserHomeDir()` resolution on each OS
- [x] Verify `ARIA_CORE_DATA_DIR` env override
- [x] Verify `aria-core mcp --profile=aria` for cloud REST mode

## A.3 — Setup agents Cursor + VS Code ✅
- [x] `cursorMCPConfigPath()` cross-platform
- [x] `installCursor()` with idempotent JSON merge
- [x] `vsCodeUserSettingsPath()` with `runtime.GOOS` branching
- [x] `installVSCode()` with `code --add-mcp` preferred + settings.json fallback
- [x] Update `SupportedAgents()` and `Install()` switch
- [x] Verify build clean (`go vet`)

## A.4 — Documentation ✅
- [x] Fix brew tap reference (was leftover from engram)
- [x] Add Quick Start section to INSTALLATION.md
- [x] Update agent paths table with Cursor + VS Code
- [x] Mark Antigravity / Windsurf as manual config

## Pending (human action)

- [ ] Create repo `ITECHDEV-MX/homebrew-tap` (empty with README)
- [ ] Add `HOMEBREW_TAP_TOKEN` secret in `aria-core` repo settings
- [ ] Merge `feat/cross-platform-release` to main
- [ ] `git tag v0.1.0 && git push --tags`
- [ ] Verify GitHub Release published with all 6 binaries
- [ ] Test `go install github.com/ITECHDEV-MX/aria-core/cmd/aria-core@v0.1.0` on a Windows machine
