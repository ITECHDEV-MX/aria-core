# Spec — Skills governance F1.a

## Frontmatter contract

Every `SKILL.md` MUST have a YAML frontmatter block delimited by
`---` lines, with at least the following keys:

- `name` (string, regex `^[a-z0-9]+(-[a-z0-9]+)*$`, ≤ 64, **must
  match parent directory name**).
- `description` (string, 10–1024 chars, non-empty).
- `version` (semver string).
- `owner` (RFC 5322 email address).

A skill MAY have:

- `license`, `compatibility`, `metadata`, `allowed-tools`
  (agentskills.io v2026-03 standard).
- `expertise_domains[]` of lowercase-hyphenated tags.
- `last_reviewed` ISO date `YYYY-MM-DD`.
- `supersedes[]` array of older skill names.
- Multi-agent chain block (`agent`, `position_in_chain`, `inputs`,
  `outputs`).
- `triggers[]` ranker hints.

If `agent: true` is present, `position_in_chain` (integer ≥ 0) is
REQUIRED. If `outputs.artifact` is present, it MUST match
`^\d+-[a-z0-9-]+\.md$`.

## Body conventions (F1.a — warn only)

Every `SKILL.md` SHOULD have:

- Trimmed body length ≥ 200 chars (aim ≥ 500).
- `## When to Use` heading.
- `## Verification` heading.

Promoted to error in F1.b once existing skills migrate.

## CLI exit codes

- `0`: validation completed (no errors, OR errors but `--strict` not
  set).
- `1`: errors detected AND `--strict` passed.
- `2`: I/O failure (unreadable directory, etc.).

## CI behavior

The workflow MUST fail the PR check when `aria-core skills validate
./skills --strict` exits non-zero. Dossier-section presence is
advisory (warning annotation) in F1.a.

## Out of contract (F1.a)

- Maintainer DB lookup (deferred to F1.b).
- Lockfile generation / drift detection (deferred to F1.b).
- Cloud server enforcement at `aria_get_skills` (deferred to F1.b).
- Body content rules as hard errors (deferred to F1.b).
