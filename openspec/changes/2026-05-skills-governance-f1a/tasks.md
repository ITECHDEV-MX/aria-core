# Tasks — Skills governance F1.a

## Schema
- [ ] `skills/schema/aria-skill-v1.schema.json` (JSON Schema 2020-12).

## Validator
- [ ] `internal/skills/skills.go` (Schema, Severity, Finding, Result types).
- [ ] `internal/skills/validator.go` (ValidateDir, ValidateFile, frontmatter + body checks).
- [ ] `internal/skills/validator_test.go` (8 tests covering happy + adversarial).

## CLI
- [ ] `cmd/aria-core/skills.go` (cmdSkills dispatch + cmdSkillsValidate).
- [ ] `cmd/aria-core/main.go` add `case "skills"`.

## CI
- [ ] `.github/workflows/skills-validate.yml`.
- [ ] PR template `.github/PULL_REQUEST_TEMPLATE/skill.md`.

## Self-validation (existing 21 skills will fail until migrated)
- [ ] Decide F1.a migration strategy: keep CI in `--strict` but skip
      paths or relax to soft for `skills/**` until F1.b ships migration.

## Release
- [ ] Commit `feat(skills): governance foundation — schema + validator + CI gate (F1.a)`.
- [ ] PR + snapshot CI green.
- [ ] Merge to main.
- [ ] Tag v0.5.0.
