# Design — F3 + F4

## F4 chain shape

```
0-office-hours.md   → reframe + forcing questions (skill: office-hours)
1-ceo-plan.md       → strategic verdict NOW/LATER/NEVER (skill: plan-ceo-review)
2-eng-plan.md       → engineering decomposition (skill: autoplan)
3-story.md          → Connextra + Gherkin formal output (skill: story-writer)
```

Branching: chain ends early if `plan-ceo-review` returns LATER/NEVER.
Linear otherwise.

## F3 validator additions

- `TestValidateFile_AgentSkillHappy` — schema-valid agent-skill passes.
- `TestValidateFile_AgentArtifactPattern` — bad filename rejected.
- `TestValidateFile_AgentZeroPositionAccepted` — position 0 is valid.
- `TestValidateExistingAgentSkills` — t.Run subtest per shipped skill.

## docs/AGENT-SKILL-AUTHORING.md

Authoring guide covering:
- Frontmatter contract (hard vs soft rules).
- Runtime contract (orchestrator behaviour).
- Position numbering rules.
- Persistence (file + aria_pages, F2.1).
- PR + verification flow.
- Example walkthrough.
