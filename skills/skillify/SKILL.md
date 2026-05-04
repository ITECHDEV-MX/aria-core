---
name: skillify
description: >-
  Capture a workflow you just ran ad-hoc and turn it into a draft
  SKILL.md file under skills/_drafts/. Lowers the barrier from "I
  did this once" to "we have a skill for this" so repeated patterns
  graduate into the catalog instead of dying as tribal knowledge.
  Inspired by oh-my-claudecode/skillify.
version: 1.0.0
owner: contacto@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [meta, capture, governance]
last_reviewed: 2026-05-04

agent: false  # Inline rule set — the calling agent reads this and
              # produces the draft in-context.

triggers:
  - keywords: [skillify, capture skill, promote workflow, save as skill]
  - context: post-task=true
---

## When to Use

Use this skill at the end of a session where you just ran a
non-trivial repeatable workflow that deserves to be a real skill,
but you do not want to derail the current task to write the full
SKILL.md.

Specifically:

- You debugged an unusual class of bug and the diagnostic recipe was
  not obvious.
- You implemented a feature pattern that will recur (e.g. "every
  cloud-server endpoint needs auth + tenant scope check + audit
  insert").
- A dev correction landed in your context that should be a rule going
  forward (memory-protocol territory, but more granular).

Skip this skill when:

- The workflow only applies once.
- The pattern is already covered by an existing skill (search first
  via `aria_get_skills`).
- The "skill" is really a configuration value or a prompt fragment
  (those go to `aria_save` or `aria_pages`, not skills).

## Rules

1. **Search first.** Call `aria_get_skills` with keywords describing
   the workflow. If a hit returns at score > 0.7, **update** that
   skill instead of creating a new one. Skill proliferation is a
   tax on every future agent invocation.
2. **Quality gate (oh-my-claudecode test):** A workflow is
   skill-worthy ONLY if it satisfies all three:
   - **Specific**: someone could not Google an equally-good answer
     in 5 minutes.
   - **Repeatable**: it will recur ≥ 3 times in the next quarter.
   - **Earned**: the rules came from real debugging or post-mortem,
     not theory.
   If any one fails, save it as an `aria_save` observation instead.
3. **Output to `skills/_drafts/<slug>/SKILL.md`** — never to the
   canonical `skills/<slug>/`. Drafts must be reviewed by a skill
   maintainer before promotion.
4. **Use the dossier template** from `.github/PULL_REQUEST_TEMPLATE/skill.md`
   verbatim in the draft body. Maintainer review fills missing
   sections; better empty than absent.
5. **Frontmatter required for the draft**:
   - `name` matching the directory.
   - `description` (10-1024 chars, includes trigger keywords as
     literal phrases — see marketingskills convention).
   - `version: 0.1.0` (drafts start sub-1).
   - `owner: <your-email>` (you sponsor the skill until handoff).
   - `last_reviewed: <today>`.
6. **Open a PR with the skill PR template** so a maintainer can
   review using the dossier checklist. CI will run validation.
7. **Do not promote drafts yourself** — even your own. Promotion
   means moving from `skills/_drafts/<slug>/` to `skills/<slug>/`,
   which requires another maintainer's review per CODEOWNERS.

## Verification

- [ ] `aria_get_skills` was called before drafting (anti-duplication)
- [ ] Quality gate (specific + repeatable + earned) explicitly
      addressed in the rationale
- [ ] Draft lives at `skills/_drafts/<slug>/SKILL.md`, never directly
      under `skills/<slug>/`
- [ ] Frontmatter has all required fields, version starts at `0.1.0`
- [ ] Body uses dossier template sections from PR template
- [ ] PR opened using `?template=skill.md`
- [ ] `aria-core skills validate ./skills/_drafts/<slug>/SKILL.md`
      passes
