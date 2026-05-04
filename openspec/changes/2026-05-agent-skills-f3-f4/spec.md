# Spec — F3 + F4

## Each F4 skill MUST

- Pass `aria-core skills validate --strict` with zero errors.
- Have `agent: true`, `position_in_chain` matching the file numeric
  prefix, and `outputs.artifact` matching that prefix.
- Declare `inputs.required_artifacts` truthfully (orchestrator relies
  on this for ordering).
- Body contains `## When to Use`, `## Rules`, `## Verification`.

## F3 tests MUST

- Validate the agent-skill happy path.
- Reject `outputs.artifact` patterns that don't match the regex.
- Accept `position_in_chain: 0`.
- Iterate the four shipped skills and assert each passes strict.
