# Exploration — F3 + F4 (agent-skill validation + first 4 chain skills)

**Date**: 2026-05-04

## Findings

F1.a already added the agent-skill schema fields (agent,
position_in_chain, inputs, outputs). Validator covered the basic case
(agent=true requires position_in_chain). F3 strengthens that with:

- Validation of `outputs.artifact` matching `^\d+-[a-z0-9-]+\.md$`.
- Position 0 is acceptable.
- A "compile" test that loads each shipped agent-skill and ensures it
  passes strict.

F4 ships the first chain (story-creation): office-hours,
plan-ceo-review, autoplan, story-writer. These are the gstack
takeaways adapted for ARIA's domain.

## Constraints

- Each skill MUST pass `aria-core skills validate --strict`.
- Each skill body has the dossier sections expected by the PR
  template, even though they're not yet validated as hard rules.
- Skill names match parent directories (F1.a-style).
- Lockfile updated.
