---
name: autoplan
description: >-
  Engineering plan from the reframed + strategically-aligned request.
  Decomposes into tasks, identifies skills/recipes that apply, lists
  affected packages, estimates effort. Position 2 in story-creation
  pipelines.
version: 1.0.0
owner: contacto@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [story-creation, engineering, planning]
last_reviewed: 2026-05-04

agent: true
position_in_chain: 2
inputs:
  required_artifacts: [0-office-hours.md, 1-ceo-plan.md]
outputs:
  artifact: 2-eng-plan.md
  aria_page_template: historia-step
  aria_save_type: architecture
  aria_save_scope: project

triggers:
  - keywords: [engineering plan, autoplan, decomposition]
  - context: pipeline=story-creation
---

## When to Use

Position 2 of a story-creation chain after `plan-ceo-review` returned
verdict `NOW`. Skip when the verdict was `LATER` or `NEVER` — that
chain ends at position 1.

## Rules

1. **Read both prior artifacts** before producing the plan. The
   reframe (`0-`) gives the WHAT; the CEO plan (`1-`) gives priority
   and constraints.
2. **Decompose the work into 3-7 concrete tasks**. Each task has:
   one-sentence description, affected packages/files, estimated
   effort (S/M/L), risk level.
3. **Map skills + recipes that apply** to each task. Use
   `aria_get_skills` to discover. Do not invent skill names.
4. **Identify migrations** if any DB schema changes are implied.
   Flag them prominently — they cost real time.
5. **Identify required tests** by category (unit, integration,
   E2E). Avoid "add tests" as a single line item — be specific.
6. Sections: `## Task breakdown`, `## Skills + recipes that apply`,
   `## Migrations`, `## Tests required`, `## Risks`,
   `## Definition of Done`.
7. Word count target: 500–1500.

## Verification

- [ ] Both prior artifacts referenced explicitly
- [ ] Task breakdown has 3-7 items
- [ ] Each task lists effort + risk
- [ ] At least one skill/recipe mapped per non-trivial task
- [ ] Migrations explicitly addressed (even if "none")
- [ ] DoD checklist is testable
- [ ] Artifact at `/Historias/<slug>/2-eng-plan.md`
