[← Back to README](../README.md)

# Contributing skills to ARIA Core

ARIA Core ships skills as first-class versioned artifacts. Every dev on
the iTechDev team can author or update one; this doc is the workflow.

For the deeper schema/runtime details of *agent-skills* (sub-agent chains),
see [AGENT-SKILL-AUTHORING.md](AGENT-SKILL-AUTHORING.md). This guide covers
the contributor experience from "I need a new rule" to "merged to main".

- [Inline vs agent-skill](#inline-vs-agent-skill)
- [Workflow at a glance](#workflow-at-a-glance)
- [Step-by-step](#step-by-step)
- [Naming + slug rules](#naming--slug-rules)
- [Local validation](#local-validation)
- [PR review](#pr-review)
- [Versioning + breaking changes](#versioning--breaking-changes)
- [Common mistakes](#common-mistakes)

---

## Inline vs agent-skill

| Type | `agent: true` in frontmatter? | Use case |
|---|---|---|
| **Inline rule set** | No (default) | The calling agent reads SKILL.md and applies the rules. E.g. `commit-hygiene`, `docs-alignment`, `aria-mcp-protocol`. |
| **Agent-skill** | Yes | The skill is invoked as a *sub-agent* in a `/Historias/<slug>` chain. Has `position_in_chain`, `inputs.required_artifacts`, `outputs.artifact`. E.g. `office-hours`, `plan-ceo-review`. |

Pick inline unless you specifically need composability with other agent
skills via /Historias. The chain protocol is overkill for one-off rule
sets.

## Workflow at a glance

```
1. Decide: inline or agent-skill (default: inline)
2. Create skills/<slug>/SKILL.md with frontmatter + body
3. Run aria-core skills validate ./skills/<slug>/SKILL.md
4. Run aria-core skills lock           # updates MANIFEST.yaml hash
5. Commit both: git add skills/<slug>/SKILL.md skills/MANIFEST.yaml
6. Open PR using the skill template; fill the dossier
7. CI runs Skills Validate + Snapshot Build
8. CODEOWNERS auto-requests review from a skill maintainer
9. Approval + merge → catalog rebuild on main
```

## Step-by-step

### 1. Pick a slug

The slug is the directory name and must match `^[a-z0-9-]+$`. Conventions:

- `<topic>-<verb>` for action skills: `pr-review-deep`, `commit-hygiene`
- `<topic>-<noun>` for reference skills: `architecture-guardrails`
- Match an existing series: marketing skills all use the imported names
  from `coreyhaines31/marketingskills` (e.g. `email-sequence`).

### 2. Write SKILL.md

Frontmatter is mandatory and validated. Minimum shape:

```yaml
---
name: my-skill
description: One-line summary; min 10 chars, max 280.
version: 1.0.0
owner: tu@itechdev.com.mx
---

## When to Use
…

## Verification
…
```

For agent-skills, see [AGENT-SKILL-AUTHORING.md](AGENT-SKILL-AUTHORING.md)
for the additional `agent: true`, `position_in_chain`, `inputs`, `outputs`
fields.

Body conventions (validator emits soft warnings if missing):

- `## When to Use` — describe the trigger context. Be specific.
- `## Verification` — how the agent (or human) confirms the skill was applied.
- Body trimmed length ≥ 200 chars (target 500+).

### 3. Validate locally

```bash
# Single skill
aria-core skills validate ./skills/my-skill/SKILL.md --strict

# All skills in repo
aria-core skills validate ./skills --strict
```

`--strict` makes warnings fatal. CI runs without it but errors still block.

### 4. Lock + check drift

```bash
aria-core skills lock              # writes/updates skills/MANIFEST.yaml
aria-core skills check-drift       # confirms no unlocked changes
```

`MANIFEST.yaml` records sha256 of every SKILL.md so the lockfile catches
unauthorized edits. CI gate `Skills Validate` runs `check-drift` and
fails if you forgot the lock step.

### 5. Generate the version index

```bash
aria-core skills versions          # rewrites skills/VERSIONS.md
```

`VERSIONS.md` is a human-readable table of all skills + versions + owners.
Always commit it together with `MANIFEST.yaml`.

### 6. Open a PR

Use the skill PR template at `.github/PULL_REQUEST_TEMPLATE/skill.md`.
Fill the dossier:

- **Why this skill exists** — the gap it closes.
- **Target callers** — which agent profiles will use it.
- **Risk + mitigation** — anything that could surprise readers.
- **Verification plan** — how you'll confirm the skill works in practice.

### 7. CI + review

Two required status checks:

- **Skills Validate** — runs `aria-core skills validate --strict` + `check-drift`.
- **Snapshot Build** — confirms the binary still compiles.

CODEOWNERS auto-requests review from a maintainer registered in
`aria_skill_maintainers`. Branch protection on `main` enforces the gate.

To register a maintainer (admin only):

```bash
ARIA_CORE_DATABASE_URL=postgres://… aria-core admin skill-maintainer-add \
  --email maintainer@itechdev.com.mx \
  --github jdoe \
  --domains marketing,kb
```

`--domains` is informational; it doesn't gate which skills the maintainer
can review.

---

## Naming + slug rules

| Rule | Why |
|---|---|
| Slug is `^[a-z0-9-]+$` | Matches the schema; predictable on disk |
| `name:` frontmatter equals slug | The validator enforces this |
| Slug is **immutable** once merged | Rename = remove + add (breaking change) |
| Avoid abbreviations unless universal | `pr-review` ✓, `prv-rvw` ✗ |

## Local validation

The full local check before pushing:

```bash
aria-core skills validate ./skills --strict
aria-core skills check-drift
aria-core skills lock              # if validate or drift complained
go test ./internal/skills/         # the schema unit tests
```

If `validate --strict` is green and `check-drift` reports no diff, CI
will pass.

## PR review

What a maintainer looks for, in order:

1. **Schema/lint** — already enforced by CI; visual confirmation only.
2. **Body coherence** — does the skill say what it does, in usable
   language? Read it as if you were the calling agent.
3. **Overlap with existing skills** — search `skills/` for adjacent
   names. If there's overlap, decide: extend the existing skill or split
   into two?
4. **Versioning** — for edits, did the author bump `version:`? See
   [Versioning](#versioning--breaking-changes).
5. **Dossier** — the PR template's risk + verification sections aren't
   bureaucracy. They're how you'll defend the skill to future maintainers.

Approval requires explicit click — no auto-merge.

## Versioning + breaking changes

`version:` follows semver:

- **Patch** (`1.0.0` → `1.0.1`) — typo, clarification, body tightening.
- **Minor** (`1.0.0` → `1.1.0`) — new section, expanded scope, new optional
  field in agent-skill frontmatter.
- **Major** (`1.0.0` → `2.0.0`) — breaking: removed sections, changed
  `position_in_chain`, renamed slug (delete + add), changed
  `outputs.artifact`.

Major bumps require explicit notice in the PR description so consumers
(other skills referencing this one in `inputs.required_artifacts`) update
in lockstep.

## Common mistakes

| Symptom | Cause | Fix |
|---|---|---|
| `validate` says "schema mismatch" | Frontmatter has a stray field or wrong type | Read the error path, compare to schema |
| `check-drift: skill X has hash mismatch` | Edited SKILL.md but didn't re-lock | `aria-core skills lock`, commit MANIFEST.yaml |
| CI Skills Validate fails on green local run | Stale lockfile from earlier branch | Pull main, re-lock, push |
| Maintainer can't be assigned via CODEOWNERS | Maintainer not yet in `aria_skill_maintainers` table | Admin runs `skill-maintainer-add` first |
| `position_in_chain` complaint on agent-skill | Position number doesn't match outputs.artifact prefix (e.g. position=2 but artifact=`3-foo.md`) | Align them; the validator anchors on the integer prefix |

## See also

- [AGENT-SKILL-AUTHORING.md](AGENT-SKILL-AUTHORING.md) — agent-skill protocol
- [Skill schema](../skills/schema/aria-skill-v1.schema.json)
- [Skills catalog](../skills/catalog.md)
- [PR template](../.github/PULL_REQUEST_TEMPLATE/skill.md)
- [ARCHITECTURE-CLOUD.md](ARCHITECTURE-CLOUD.md) — where skills fit in the system
