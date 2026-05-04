---
name: plan-ceo-review
description: >-
  Strategic alignment pass over a reframed request. Cross-references
  the reframe against company strategy, current quarter priorities, and
  rejected alternatives. Decides whether the work fits NOW, LATER, or
  NEVER. Position 1 in story-creation pipelines.
version: 1.0.0
owner: contacto@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [story-creation, strategy, prioritization]
last_reviewed: 2026-05-04

agent: true
position_in_chain: 1
inputs:
  required_artifacts: [0-office-hours.md]
outputs:
  artifact: 1-ceo-plan.md
  aria_page_template: historia-step
  aria_save_type: decision
  aria_save_scope: project

triggers:
  - keywords: [strategic review, prioritization, ceo plan]
  - context: pipeline=story-creation
---

## When to Use

Always invoked at position 1 of a story-creation chain, immediately
after `office-hours`. Reads the reframed request and runs strategic
alignment.

Skip only when the request is explicitly compliance-driven (e.g. a
regulatory deadline) — those bypass strategic review.

## Rules

1. **Read `0-office-hours.md` first.** Do not start without it.
2. Cross-reference against the **current quarter's priorities** as
   recorded in `aria_pages` under `team/quarterly-priorities` (call
   `aria_page_search` if needed). If priorities are missing, flag
   that in the artifact and ask the user.
3. Pick **one of three verdicts**: `NOW`, `LATER`, `NEVER`.
   - `NOW`: aligned with current quarter, goes into the active queue.
   - `LATER`: aligned but not this quarter — record on backlog with
     the conditions that would promote it.
   - `NEVER`: misaligned. Document why and what alternative the user
     should pursue instead.
4. List **rejected alternatives** with a one-sentence reason each.
   Saying "we considered X but no" is necessary signal.
5. Sections in order: `## Verdict`, `## Strategic fit`,
   `## Rejected alternatives`, `## Risks if we proceed`,
   `## Conditions to promote (LATER)` or
   `## Replacement recommendation (NEVER)`.
6. Hand off only when verdict is `NOW`. For `LATER`/`NEVER`, save the
   artifact and call `aria_artifact_complete` to short-circuit the
   chain.

## Verification

- [ ] Reads `0-office-hours.md` (declared in `inputs`)
- [ ] Verdict is one of {NOW, LATER, NEVER}
- [ ] At least 2 rejected alternatives listed
- [ ] On NOW: chain continues to `autoplan`
- [ ] On LATER/NEVER: chain is closed via `aria_artifact_complete`
- [ ] Artifact at `/Historias/<slug>/1-ceo-plan.md`
