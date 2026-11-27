---
name: pigpen-omh-ulw-interview
description: "[omh] Vague, underspecified request: one-question-at-a-time clarification before planning or handoff, bounded to 6 rounds across outcome, constraints, and success criteria."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, clarification]
    category: clarification
    phase: discovery
    role: planner
    quality_tier: clarity-gated
    related_skills: [pigpen-omh-ulw-context, pigpen-omh-ulw-plan]
---

# OMH Deep Interview

Hermes-native `ulw-interview` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-interview` prevents guessing through ambiguous product, workflow, or implementation intent. It converts uncertainty into a clarified brief with goals, non-goals, constraints, and acceptance criteria before planning (`pigpen-omh-ulw-plan`) or handoff (`omh_maestro`).

## Do Not Use When

- The request already has concrete scope, acceptance criteria, and verification commands.
- The missing information is discoverable from the repository via `read_file` or `terminal` without asking the user.
- The user asked for immediate read-only analysis and the ambiguity does not change the answer.
- The ambiguity is specifically repository terminology alignment; use `pigpen-omh-ulw-context`.

## Interview Round Protocol

This interview is bounded to **at most 6 rounds, one decision-changing question per round**.

Preceding every question, emit this status header on its own line, then a blank line, then the question:

```text
Round {n}/6 · Clarity: {percent}% ({resolved}/3) · Targeting: {dimension}
```

- **Three fixed clarity dimensions**:
  1. **outcome** — what is true when this is done
  2. **constraints and non-goals** — what bounds the work
  3. **success criteria** — how anyone would verify it
- `{resolved}` counts how many dimensions you can restate in one sentence without a qualifier (`0`, `1`, `2`, or `3` → `0%`, `33%`, `67%`, `100%`).
- `{dimension}` names the unresolved dimension that most changes the plan, not the easiest one.
- Ask like a senior colleague: one concise sentence, no preamble, no numbered sub-questions. Do not fold counters or dimension jargon into the question sentence itself.
- **Candidate options**: ship 2 to 4 concrete candidate answers with your recommended option first, plus a final free-input option (`N) Something else — type your answer`). Accept a number, an option's text, or arbitrary free text without re-asking.
- **Mid-interview check**: before Round 4, emit a brief non-round check (no header) asking whether to keep going or plan now.
- **Stop rules (first match wins)**:
  1. All three dimensions resolved → emit the clarified brief and proceed to planning.
  2. User asks to stop or proceed → emit the brief immediately, recording unresolved dimensions as explicit assumptions.
  3. Round 6 answer recorded → do not emit Round 7; name what stayed unresolved as assumptions and emit the brief.

## Safety & Evidence Boundaries

- Gather discoverable repository facts (`read_file`) before asking the user.
- A "yes" to an idea approves only the idea; planning, handoff, or code changes require their own gates.
- Do not prepare an implementation handoff (`omh_maestro`) or dispatch `delegate_task` until blocking decisions are resolved or recorded as explicit assumptions.

## Completion Checklist

- Clarified brief names goals, non-goals, constraints, acceptance criteria, and one next path (`pigpen-omh-ulw-plan`, `pigpen-omh-ulw-work`, or `pigpen-omh-dag-ulw`).
- Remaining ambiguity is listed only where it changes the plan, risk, or stop condition.
- No implementation edits or handoffs were started before clarification closed.
