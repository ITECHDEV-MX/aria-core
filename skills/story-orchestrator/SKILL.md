---
name: story-orchestrator
description: >-
  Coordinator agent-skill that drives the multi-agent story-creation
  chain (office-hours → plan-ceo-review → autoplan → story-writer).
  Loads each child skill, supplies the previous artifact(s) as input,
  invokes the sub-agent, and short-circuits when plan-ceo-review
  returns LATER or NEVER. Lives at position-less (orchestrator role,
  not part of the chain itself).
version: 1.0.0
owner: contacto@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [story-creation, orchestration, gstack]
last_reviewed: 2026-05-04

agent: false  # The orchestrator is invoked by the calling agent
              # (Claude Code, Cursor, etc.), not as a chain step.

triggers:
  - keywords: [redactar historia, story creation, full pipeline, multi-agent chain]
  - context: pipeline=story-creation
---

## When to Use

Use this skill when the user asks to draft a user story / feature
spec / RFP response and the project standard is to run the full
gstack-style chain rather than free-write. The orchestrator owns the
coordination — the chain participants (office-hours, plan-ceo-review,
autoplan, story-writer) own the content.

Skip this skill when:

- The user explicitly says "skip the chain, just write the story".
- Output is for an internal one-off and dossier rigor is overkill.
- Verdict from plan-ceo-review is already known to be NEVER (no story
  to produce).

## Rules

1. **Pick a slug** before invoking any sub-agent. The slug must match
   `^[a-z0-9]+(-[a-z0-9]+)*$` (validated by `aria_artifact_save`).
   Convention: `<YYYY-MM>-<3-6 words about the work>`. Example:
   `2026-05-mantenimiento-industrial-cotizador`.
2. **Run sub-agents in declared order** (positions 0, 1, 2, 3). Each
   sub-agent SKILL.md declares its `inputs.required_artifacts` — the
   orchestrator MUST provide those by reading prior artifacts via
   `aria_artifact_get` and including them as context in the sub-agent
   prompt.
3. **Short-circuit on verdict**:
   - After position 1 (`plan-ceo-review`), parse the artifact's
     `## Verdict` section.
   - If verdict is `LATER` or `NEVER`, call
     `aria_artifact_complete(slug, final_artifact="1-ceo-plan.md")`
     and stop. Do not invoke positions 2 or 3.
   - If verdict is `NOW`, continue.
4. **One sub-agent per position**. Do not parallelize. Each artifact
   is the input to the next; concurrency only causes drift.
5. **Persist after each step** via `aria_artifact_save` BEFORE
   spawning the next sub-agent. The next agent reads from disk, not
   from in-memory state.
6. **Final actions** (after position 3):
   - `aria_artifact_complete(slug, final_artifact="3-story.md")`.
   - `aria_save(type=story, scope=project, title=story_title,
     content=story_connextra_header)`.
7. **On any sub-agent failure**, save the partial artifacts and call
   `aria_artifact_save` with `status=abandoned` via a follow-up
   `aria_artifact_complete` (the API supports it). Do not silently
   discard partial work.

## Verification

- [ ] Slug picked + matches regex
- [ ] Each sub-agent invocation supplied required_artifacts as input
- [ ] On verdict NOW: positions 0,1,2,3 all completed
- [ ] On verdict LATER/NEVER: chain stopped at position 1, completed
- [ ] After position 3: aria_artifact_complete + aria_save called
- [ ] On failure: chain marked abandoned (not silently lost)

## Implementation note for the calling agent

This skill is **not** a sub-agent itself (`agent: false`). It is a
prompt/protocol the calling agent (Claude Code, Cursor, etc.)
follows in-context to drive the chain. The calling agent reads each
of the four chain skills, spawns the appropriate sub-agent or runs
in-context with that skill's body as the system prompt, and persists
artifacts via the `aria_artifact_*` MCP tools.

A future ARIA Core release MAY ship a pure-server orchestrator, but
F5 deliberately keeps orchestration on the client side to avoid
coupling the server to a specific LLM lifecycle.
