# Design — Skills governance F1.a

## Schema layout

Authoritative file: `skills/schema/aria-skill-v1.schema.json`.

Fields by source:

| Source | Fields |
|---|---|
| agentskills.io v2026-03 (core) | `name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools` |
| ARIA governance (required) | `version` (semver), `owner` (email) |
| ARIA governance (optional) | `expertise_domains[]`, `last_reviewed`, `supersedes[]` |
| ARIA multi-agent chain (optional) | `agent`, `position_in_chain`, `inputs.required_artifacts`, `outputs.{artifact,aria_page_template,aria_save_type,aria_save_scope}` |
| ARIA ranker hints (optional) | `triggers[].{keywords[],context,tool_called}` |

Schema is embedded via `//go:embed` so the binary IS the canonical
copy. External tools that want IDE auto-completion can fetch:

```
https://raw.githubusercontent.com/ITECHDEV-MX/aria-core/main/skills/schema/aria-skill-v1.schema.json
```

## Validator behavior

```go
ValidateDir(dir)  → walks dir, finds <subdir>/SKILL.md, validates each
ValidateFile(path, expectedDirName) → frontmatter + body checks
```

Findings carry a Severity (`error`, `warn`, `info`). The exit code of
the CLI depends on `--strict`:

- Default: print findings, exit 0 even on errors (soft).
- `--strict`: exit 1 on any error finding.

Body checks (warn for now, error in F1.b):
- Body trimmed length ≥ 200 chars (target ≥ 500).
- `## When to Use` heading present.
- `## Verification` heading present.

## CLI surface

```
aria-core skills <subcommand>

Subcommands:
  validate [path]   Validate SKILL.md files. Default: ./skills.
                    Flags: --strict, --json.
```

Future subcommands documented in proposal but unimplemented in F1.a:
`maintainer`, `lock`, `check-drift`.

## CI gate

`.github/workflows/skills-validate.yml` triggers on PRs that touch
`skills/**`. It runs:

1. Build aria-core binary.
2. `aria-core skills validate ./skills --strict` (hard).
3. Upload JSON report as artifact.
4. Check PR body for dossier section markers (advisory warn only).

## PR template

`.github/PULL_REQUEST_TEMPLATE/skill.md` enforces the dossier JC
defined: Trigger / Rules / Verification / Rationale / Alternatives /
Risks adopting / Risks NOT adopting / Success metrics / Maintainer
review checklist.

GitHub renders this template only when the contributor selects it
(?template=skill.md or via the dropdown). The CI step "Check PR has
skill dossier" looks for the section headings as a soft signal — it
warns rather than fails so non-skill PRs that happen to touch
`internal/skills/` aren't blocked.

## Test strategy

Hermetic. `t.TempDir()` + write fixture files. Cover:

- Happy path (valid skill).
- Missing required field per field (`name`, `description`, `version`, `owner`).
- `name` vs parent dir mismatch.
- Bad semver / bad email.
- `agent: true` without `position_in_chain`.
- Body too short (warn).
- Multi-skill aggregation (`ValidateDir` returns sum across skills).
- Embedded schema is intact (sanity).

Eight tests in F1.a; expanded as F1.b adds maintainer enforcement.
