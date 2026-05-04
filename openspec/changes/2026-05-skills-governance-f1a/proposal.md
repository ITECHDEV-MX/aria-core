# Proposal — Skills governance F1.a

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/skills-governance-f1a`)

## Problem

The team uses ARIA skills today, but enforcement is by trust. There
is no schema, no validator, no CI gate, no required `owner`/`version`
on skill files, and PRs that touch `skills/` aren't held to a uniform
review bar. As the catalog grows past 21 skills the drift will
compound silently.

## Goal

Ship the foundation so every PR that touches `skills/**` is held to
a canonical, machine-checked schema, and PRs that add new skills
must include a complete dossier the maintainer can evaluate.

## Scope

In:
- JSON Schema 2020-12 at `skills/schema/aria-skill-v1.schema.json`
  derived from agentskills.io v2026-03 + ARIA-specific dossier
  fields (`version` and `owner` required; multi-agent chain fields
  optional).
- Go validator package `internal/skills/` with `ValidateDir` /
  `ValidateFile`. Embeds the schema via `//go:embed` so it travels
  with the binary.
- CLI subcommand `aria-core skills validate [path]` with `--strict`
  and `--json` flags.
- GitHub Action `.github/workflows/skills-validate.yml` running on
  PRs that touch `skills/**`. Strict by default in CI.
- PR template at `.github/PULL_REQUEST_TEMPLATE/skill.md` covering
  the full dossier JC asked for (Trigger / Rules / Verification /
  Rationale / Alternatives / Risks of adopting / Risks of NOT
  adopting / Success metrics / Maintainer review).

Out (deferred to F1.b):
- Postgres-backed `aria_skill_maintainers` registry.
- `aria-core skills lock` lockfile generator.
- `aria-core skills check-drift` command.
- Cloud server-side enforcement (`aria_get_skills` filters out
  invalid skills).

## Alternatives considered

- **Use `skills-ref` CLI directly in CI** — rejected. We already
  ship a Go binary; depending on a third-party Node CLI for a
  blocking gate adds a moving piece. Our validator is < 200 lines
  of Go, embedded, and lockstep with the binary version.
- **Defer the validator entirely and just add the PR template** —
  rejected. Without a machine check, the dossier becomes a copy-paste
  ritual. The validator is the teeth.
- **Make `version` and `owner` optional in F1.a** — rejected. The
  whole point of F1 is enforcement; making provenance fields optional
  defeats it. Migration cost is one-time.

## Success criteria

- [x] `aria-core skills validate ./skills --strict` exits 1 on any
  current skill that lacks `version` or `owner`.
- [x] CI gate runs on PRs and posts validation findings.
- [x] PR template renders the dossier sections in the PR body.
- [x] Tests cover happy path, missing-field, name/dir mismatch, bad
  semver, bad email, body-too-short, agent-without-position.
- [x] Schema is published at the canonical URL via `main` branch and
  versioned with the binary.
