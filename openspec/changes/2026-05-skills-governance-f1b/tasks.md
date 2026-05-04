# Tasks — Skills governance F1.b

## Lockfile + drift
- [ ] internal/skills/lock.go (BuildLockfile, WriteLockfile, ReadLockfile, CheckDrift, types).
- [ ] internal/skills/lock_test.go (7 hermetic tests).

## CLI
- [ ] cmd/aria-core/skills.go: cmdSkillsLock, cmdSkillsCheckDrift.

## CODEOWNERS
- [ ] .github/CODEOWNERS with skill-path entries.

## CI gate
- [ ] .github/workflows/skills-validate.yml: add check-drift step.

## Initial lockfile
- [ ] aria-core skills lock ./skills committed in this PR.

## Release
- [ ] Commit `feat(skills): lockfile + drift + CODEOWNERS (F1.b)`.
- [ ] PR + CI green.
- [ ] Merge.
- [ ] Tag v0.5.1.
