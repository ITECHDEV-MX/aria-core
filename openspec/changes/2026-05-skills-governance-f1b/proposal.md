# Proposal — Skills governance F1.b

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/skills-governance-f1b`)

## Problem

After F1.a, every PR that touches `skills/**` is validated against the
schema. But there's no way to say "this is the canonical state of the
catalog as of v0.5.0" — there's no lockfile and no per-path review
enforcement. Devs can introduce a new skill that passes validation
yet drifts from the team-approved catalog.

## Goal

Ship the lockfile + drift detection so the canonical catalog is
machine-checkable, plus CODEOWNERS so PR review is enforced at the
GitHub level.

## Scope

In:
- `internal/skills` extended with `BuildLockfile`, `WriteLockfile`,
  `ReadLockfile`, `CheckDrift`, `DriftReport`.
- `skills/MANIFEST.yaml` written by `aria-core skills lock` —
  schema_version, generated_at, sorted skills with name/version/owner/
  content_sha256/path.
- CLI subcommands `aria-core skills lock` and
  `aria-core skills check-drift [--json]`.
- CI workflow extended to run `check-drift` after `validate`.
- `.github/CODEOWNERS` entries for `/skills/`, `/internal/skills/`,
  PR template, and workflow file.
- 7 hermetic tests covering lockfile build, write/read roundtrip,
  ErrLockfileMissing sentinel, drift kinds (added, removed, hash,
  version), header presence.

Out (deferred):
- Postgres `aria_skill_maintainers` registry (F1.c if ever needed).
- Cloud-side enforcement at `aria_get_skills`.
- Body content rules → error severity.

## Success criteria

- `aria-core skills lock` writes a deterministic lockfile.
- `aria-core skills check-drift` exits 1 on any added/removed/changed
  skill.
- CI fails when the lockfile is stale.
- CODEOWNERS forces PR review on `skills/` paths.
