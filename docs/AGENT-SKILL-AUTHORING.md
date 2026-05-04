[← Back to README](../README.md)

# Authoring agent-skills (multi-agent chain)

ARIA Core distinguishes two kinds of skill:

| Kind | `agent` | Use case |
|---|---|---|
| Inline rule set | `false` (default) | The calling agent reads the SKILL.md and applies the rules in-context. E.g. `commit-hygiene`, `docs-alignment`. |
| **Agent skill** | `true` | The skill is invoked as a *sub-agent* in a chain. It reads inputs from prior artifacts, writes one new artifact, and is composed with other agent-skills via the `/Historias/<slug>/` protocol. |

This doc covers **agent skills**.

## Anatomy of an agent-skill SKILL.md

```yaml
---
name: story-writer
description: …
version: 1.0.0
owner: you@itechpymes.com.mx

agent: true
position_in_chain: 3
inputs:
  required_artifacts: [0-office-hours.md, 1-ceo-plan.md, 2-eng-plan.md]
outputs:
  artifact: 3-story.md
  aria_save_type: story
  aria_save_scope: project

triggers:
  - context: pipeline=story-creation
---
```

The frontmatter contract is enforced by `aria-core skills validate`
(see schema at `skills/schema/aria-skill-v1.schema.json`).

### Hard rules (validator errors)

- `agent: true` ⇒ `position_in_chain` MUST be present (integer ≥ 0).
- `outputs.artifact` MUST match `^\d+-[a-z0-9-]+\.md$`. The numeric
  prefix MUST equal `position_in_chain`.

### Soft rules (validator warnings, may harden later)

- Body should have `## When to Use` and `## Verification` sections.
- Body trimmed length ≥ 200 chars (target 500+).

## Runtime contract

When the orchestrator invokes an agent-skill, it:

1. Reads the SKILL.md to discover `inputs.required_artifacts` and
   `outputs.artifact`.
2. Loads the prior artifacts from `/Historias/<slug>/` via
   `aria_artifact_get`.
3. Spawns a sub-agent with the SKILL.md body as the system prompt
   plus the prior artifacts as context.
4. The sub-agent produces the body of the next artifact and saves it
   via `aria_artifact_save`.
5. Continues to the next position in the chain.

## Position numbering

Positions are **0-indexed and contiguous**. A chain with positions
`[0, 1, 2]` is valid; `[0, 2, 5]` is broken. The orchestrator MUST
fail loudly on gaps.

## Branching chains (out of scope)

The chain protocol is linear in F4. Branching ("on verdict X go to
position 2, else terminate") is supported by the calling agent's
control flow — `plan-ceo-review` does this when verdict ≠ `NOW`. The
artifact protocol itself does not encode branches.

## Persistence

Each artifact is persisted in two places:

| Location | Source of truth | Purpose |
|---|---|---|
| `/Historias/<slug>/<file>.md` | Yes | Versioned with git, audit log, code review surface |
| `aria_pages` (cloud) | No | Search index, dashboard rendering |

Sync is file → page. The `aria_page_template` field in `outputs`
hints which template to use when mirroring (see F2.1 follow-up).

## Verification at PR time

Every PR that adds or modifies an agent-skill goes through:

1. `aria-core skills validate ./skills --strict` (CI)
2. `aria-core skills check-drift ./skills` (CI)
3. CODEOWNERS review by a skill maintainer
4. Dossier section presence check (advisory)

See `.github/PULL_REQUEST_TEMPLATE/skill.md` for the dossier the
maintainer expects.

## Example: writing a new agent-skill

1. Create the directory: `skills/<slug>/`.
2. Write `SKILL.md` with `agent: true` frontmatter + body.
3. Run `aria-core skills validate ./skills/<slug>/SKILL.md` locally.
4. Re-lock: `aria-core skills lock`. Commit `MANIFEST.yaml`.
5. Open a PR using the skill template. Fill the dossier.
6. CI must be green and a maintainer must approve.

## See also

- [Skill schema](../skills/schema/aria-skill-v1.schema.json)
- [Skills catalog](../skills/catalog.md)
- [/Historias protocol](../openspec/changes/2026-05-historias-f2/spec.md)
- [Skills governance F1.a](../openspec/changes/2026-05-skills-governance-f1a/spec.md)
- [Skills governance F1.b](../openspec/changes/2026-05-skills-governance-f1b/spec.md)
