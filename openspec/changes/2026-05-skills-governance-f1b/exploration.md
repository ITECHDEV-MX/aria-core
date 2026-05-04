# Exploration — Skills governance F1.b (lockfile + drift + CODEOWNERS)

**Date**: 2026-05-04
**Driver**: F1.a shipped schema + validator + CI gate. JC asked to
continue all the way to F5. F1.b is the final governance piece before
moving on to /Historias artifacts (F2).

## Decision

Originally F1.b was scoped to "maintainer registry in Postgres". After
revisiting the sub-agente research:
- GitHub CODEOWNERS already enforces per-path review-required, free.
- The Postgres approach buys per-domain expertise routing, audit log,
  expertise discovery — none of which are urgent for a 5-person team.
- The ecosystem patterns (Skill Provenance, APM, sigstore-a2a) all use
  **lockfile + content hash** as the replicable-control primitive.

**Refocused F1.b to ship lockfile + drift + CODEOWNERS** — the
"replicable team control" JC wanted, without standing up new DB
tables. Postgres registry deferred to F1.c if/when it's needed.

## Findings

- ARIA Core has no lockfile concept yet. Reproducing "this is the
  canonical skill set as of v0.5.0" requires a git-tag+walk approach.
- GitHub CODEOWNERS is the standard primitive. Already part of the
  GitHub PR review flow ARIA uses.
- The validator embeds the schema; the lockfile must embed the hashes.

## Constraints

- Lockfile is a YAML file at `skills/MANIFEST.yaml`. Sorted by skill
  name for deterministic diffs.
- `aria-core skills check-drift` exits 1 on drift; CI calls it.
- No remote state — purely repo-local. Cloud enforcement remains F2+.
