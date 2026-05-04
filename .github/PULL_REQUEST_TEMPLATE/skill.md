<!--
This template is for PRs that add or modify a SKILL.md file.

It exists because ARIA Core skills are governed: a skill maintainer
must be able to evaluate the change with full context. A PR without
this dossier filled in will fail review.

Pick this template by adding ?template=skill.md to your PR creation URL,
or copy it into your PR body manually.
-->

## Skill being added/modified

Name: `…`
Owner: `…@itechpymes.com.mx`
Version: `0.0.0`

## Trigger conditions

When should the agent activate this skill? Be specific: keywords,
context, tool calls, hostname patterns. Avoid "use whenever it seems
relevant".

## Rules imposed

The numbered list of rules the skill enforces. One sentence each.

## Verification

How will we know an agent is following this skill? Concrete checks
(file existed, schema passed, tag was applied, …) — not feelings.

## Rationale

What problem does this skill solve? Cite evidence (incident, recurring
bug, customer feedback, audit finding). If the problem is hypothetical,
say so explicitly.

## Alternatives rejected

What other patterns did you consider? Why is this skill the right one?

## Risks of adopting

What could go wrong? False positives, friction, agent confusion, time
cost. Be honest.

## Risks of NOT adopting

What's the cost of leaving this gap open? Tie back to the rationale.

## Success metrics

How will we measure that the skill is working a quarter from now?
(`aria_record_skill_feedback` data, drop in incident X, etc.)

## Maintainer review

- [ ] Schema validation passes (`aria-core skills validate --strict`)
- [ ] Body length ≥ 500 chars
- [ ] When to Use + Verification sections present
- [ ] Approved by skill maintainer: @…

---

🤖 Generated with [Claude Code](https://claude.com/claude-code)
