# Proposal — F3 + F4

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/agent-skills-f3-f4`)

## Problem

The schema for agent-skills exists but no concrete agent-skill ships
yet. The story-creation chain JC chose (office-hours / plan-ceo-review
/ autoplan / story-writer) is the first deliverable.

Validator coverage of agent fields was minimal. F3 adds 4 more tests
including a compile-time assert that the F4 skills validate strict.

## Goal

Ship 4 production-ready agent-skill SKILL.md files + extended
validator tests + an authoring guide.

## Scope

In:
- 4 agent-skills under `skills/<name>/SKILL.md`.
- 4 extra tests in internal/skills/validator_test.go.
- docs/AGENT-SKILL-AUTHORING.md.
- Lockfile updated.
- openspec change folder.

Out:
- Orchestrator implementation (caller's job — Claude Code, etc.).
- Dashboard view (F5).
- aria_pages mirror (F2.1).

## Success criteria

- All 4 skills pass `aria-core skills validate --strict`.
- All 4 skills pass `TestValidateExistingAgentSkills` t.Run.
- Lockfile committed and `check-drift` clean.
- Docs explain authoring + the runtime contract.
