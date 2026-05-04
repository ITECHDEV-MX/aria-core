# Tasks — aria_doctor

## Implementation
- [ ] `internal/doctor/doctor.go` — entry point, Report, Check, Status types
- [ ] `internal/doctor/checks_config.go` — config_dir check
- [ ] `internal/doctor/checks_db.go` — db_file, schema_version, core_tables
- [ ] `internal/doctor/checks_fts.go` — fts5_index
- [ ] `internal/doctor/checks_disk.go` — disk_space (uses syscall.Statfs)
- [ ] `internal/doctor/checks_activity.go` — recent_activity, session

## CLI integration
- [ ] `cmd/aria-core/doctor.go` — new file with `cmdDoctor(cfg)` function
- [ ] `cmd/aria-core/main.go` — add `case "doctor"` in switch
- [ ] Update `Commands:` listing in `--help` output

## MCP integration
- [ ] `internal/mcp/aria_doctor.go` — register `aria_doctor` MCP tool
- [ ] `internal/mcp/mcp.go` — call register from `registerTools()`
- [ ] Add to `agent` and `admin` profiles in shouldRegister allowlist

## Tests
- [ ] `internal/doctor/doctor_test.go` — happy path + each check failure
- [ ] At minimum: TestDiagnose_AllPass, TestDiagnose_DBMissing, TestDiagnose_FTSCorrupt

## Documentation
- [ ] Update `docs/INSTALLATION.md` Quick Start with `aria-core doctor` line
- [ ] Add to `docs/AGENT-SETUP.md` MCP tools table (when it exists, optional)

## Release
- [ ] Commit with `feat(doctor): aria_doctor read-only diagnostics`
- [ ] PR + snapshot CI green
- [ ] Merge to main
- [ ] Tag v0.2.0
- [ ] Verify release.yml green, binaries published
