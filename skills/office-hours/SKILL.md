---
name: office-hours
description: >-
  Reframe a client request before any planning happens. Asks the user
  forcing questions, identifies the real product hiding inside the ask,
  and outputs a structured rewrite that the rest of the chain consumes.
  First step in story-creation pipelines.
version: 1.0.0
owner: contacto@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [story-creation, requirements, cotizador]
last_reviewed: 2026-05-04

agent: true
position_in_chain: 0
inputs:
  required_artifacts: []
outputs:
  artifact: 0-office-hours.md
  aria_page_template: historia-step
  aria_save_type: meeting
  aria_save_scope: project

triggers:
  - keywords: [office hours, reframe, rfp, briefing, redactar historia]
  - context: pipeline=story-creation
---

## When to Use

Use this skill as the **first** step of any story-creation chain
under `/Historias/`. Trigger conditions:

- The user asked to "redactar una historia" / "draft a story" /
  "feature spec" without first stating WHO/WHY/WHAT explicitly.
- An RFP / client brief landed and we are about to price it.
- Internal "we should do X" requests that haven't been validated.

Skip this skill only if the input already arrives in
**Connextra+Gherkin** form — that's the contract for `story-writer`.

## Rules

1. **Iron Law: no fixes without investigation.** Before reframing,
   restate the original request verbatim at the top of the artifact
   (under heading `## Original request`) so the chain has the
   audit trail.
2. Identify the **real product hiding inside the request**. The user
   often asks for solution X but wants outcome Y. Name Y explicitly.
3. Ask **at least three forcing questions** about Who / Why / What
   success looks like. Do not assume answers. If the user is not
   present, list questions as `## Forcing questions (unanswered)`.
4. Output is structured Markdown with these sections in order:
   `## Original request`, `## Reframed objective`, `## Stakeholders`,
   `## Forcing questions`, `## What success looks like`,
   `## Constraints we already know`.
5. Word count target: 300–800. Below is too thin; above is leaking
   into the next agent's job.
6. Hand off to `plan-ceo-review` (position 1) by writing the artifact
   to `/Historias/<slug>/0-office-hours.md` and calling
   `aria_artifact_save`.

## Verification

- [ ] Artifact saved at `/Historias/<slug>/0-office-hours.md`
- [ ] Sections in canonical order
- [ ] Word count in [300, 800]
- [ ] At least 3 forcing questions present (answered or not)
- [ ] Original request quoted verbatim
- [ ] No solutions proposed (that's `autoplan`'s job)
