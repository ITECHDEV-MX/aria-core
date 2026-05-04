# Exploration — Skills governance foundation (F1.a)

**Date**: 2026-05-04
**Driver**: JC asked to enforce skill format and replicate control
across the team. Sub-agente research showed the ecosystem in 2026
converges on (1) frontmatter `name`+`description` with regex naming,
(2) CI gate as a published GitHub Action, (3) body content rules
beyond YAML, (4) SHA-256 content hash + lockfile + bundle semver,
(5) cross-agent compatibility via agentskills.io v2026-03 spec.

## Findings

### What ARIA already has
- `skills/<name>/SKILL.md` × 21 with frontmatter (`name`, `description`,
  `license`, `metadata`).
- `AGENTS.md` at repo root mapping skills to triggers.
- MCP tools `aria_get_skills` (with Wilson lower-bound ranker) +
  `aria_record_skill_feedback`.
- `openspec/changes/<slug>/` SDD-flow doc set already in use.

### What's missing
- No JSON Schema (file or embedded). Validation is by convention.
- No CLI to validate.
- No CI gate to block bad PRs.
- No PR template forcing the dossier JC asked for.
- No formal `version`/`owner` on skills — provenance is git blame.
- No support for multi-agent chain skills (frontmatter doesn't model
  `agent`/`inputs`/`outputs`).

### Authoritative external references
- `agentskills.io/specification` v2026-03 — community spec donated by
  Anthropic.
- `skills-ref` CLI — official validator of the agentskills.io spec.
- Gentleman-Programming/Gentleman-Skills `validate-pr.yml` — the
  most complete example of body-content enforcement seen in the
  ecosystem (frontmatter + body sections + ≥3 code blocks + ≥500
  chars + security boundary).

## Risks

- **Existing 21 skills don't yet have `version` and `owner`.** The
  validator will fail every one until they're updated. Mitigation:
  ship the validator in **soft mode** (warnings only) by default;
  bump to strict in F1.b once skills are migrated.
- **Maintainer registry is not in F1.a.** Without the DB-backed
  registry, "skill maintainer" is enforced by GitHub PR review
  convention only. Acceptable for v0.5.0; tightened in F1.b.
- **Drift detection (lockfile)** is deferred to F1.b. Cloud and
  client may serve different skills until then.

## Constraints

- Schema must extend the agentskills.io v2026-03 fields without
  breaking compatibility — additive only.
- `version` is required; supersedes the implicit "git HEAD is the
  version" model.
- Local CLI runs in soft mode by default (advisory). Strict mode is
  reserved for CI and (in F1.b) the cloud server.
- Body rules use `## When to Use` and `## Verification` headings,
  matching the existing skills convention.
