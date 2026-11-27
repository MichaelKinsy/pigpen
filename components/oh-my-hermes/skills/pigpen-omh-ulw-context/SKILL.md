---
name: pigpen-omh-ulw-context
description: "[omh] Repository vocabulary unclear or inconsistent: project terminology alignment workflow to look up, capture, correct, and align the words a repository uses before planning or handoff."
license: MIT
author: "oh-my-hermes maintainers"
metadata:
  hermes:
    tags: [workflow, oh-my-hermes, clarification]
    category: clarification
    phase: terminology-alignment
    role: planner
    quality_tier: clarity-gated
    related_skills: [pigpen-omh-ulw-interview, pigpen-omh-ulw-plan]
---

# OMH Context (Terminology Alignment)

Hermes-native `ulw-context` workflow skill adapted for `pig-hermes` + `oh-my-hermes-pig`.

## Why This Exists

`ulw-context` reduces repository terminology drift without creating a second machine store or a vocabulary router: inspect repository evidence via `read_file`, answer settled lookups directly, facilitate dependency-aware terminology alignment, and project approved results into planning or coding handoffs.

## Do Not Use When

- A safe one-term definition or source lookup can be answered directly; answer in read-only lookup mode without entering a full context interview.
- The request is broad ambiguity with no project-language conflict; use `pigpen-omh-ulw-interview`.
- The terminology is already agreed and the request is to produce an implementation plan; use `pigpen-omh-ulw-plan`.
- The user wants to capture general retained memory rather than repository terminology; use `omh_remember`.

## Workflow Protocol

1. **Classify the turn** as safe read-only lookup, reviewed capture, terminology correction, unresolved decision frontier, or confirmed planning/handoff transition.
2. **Lookup first**: inspect repository files (`read_file`) and any root `PROJECT_TERMS.md` or reviewed terminology notes before asking the user for discoverable information. Name source and freshness status. File presence is not proof that a model used the content.
3. **Sparse terminology**: keep entries concise — canonical identity, short definition, expression guidance, distinct-from boundary, and optional display label.
4. **Dependency-ready decision frontier**: before interviewing, confirm frontier entry. Present every currently dependency-ready unresolved decision in one numbered batch per round with stable `D1`, `D2`, ... identifiers, attaching one concise recommendation and tradeoff to each decision while leaving the choice with the user. Defer dependent decisions until prerequisites resolve.
5. **Bounded rounds**: cap the frontier at 6 rounds, with a non-round consent check before Round 4. Lookup, research, entry consent, summary confirmation, and next-path consent do not consume rounds. Never emit Round 7.
6. **Stop conditions**: stop on the first match — every reachable decision is resolved, deferred, or blocked; the user asks to stop or proceed; or the answer to Round 6 is recorded. Omitted decisions stay open and recommendations require explicit acceptance.
7. **Separate confirmation**: read back the confirmed shared understanding first. Only after explicit confirmation prepare a `pigpen-omh-ulw-plan` or `omh_maestro` coding-owner handoff; never auto-execute or auto-write `PROJECT_TERMS.md`.

## Safety & Evidence Boundaries

- Treat `PROJECT_TERMS.md` as optional human prose with zero direct routing, approval, or dispatch authority.
- Never write, synchronize, approve, or commit terminology files automatically; propose edits with explicit confirmation (`patch`).
- Keep candidate staging, clarification, handoff preparation (`prepared_not_observed`), execution, review, CI, and merge as separate evidence states (`omh_completion`).

## Completion Checklist

- Source status and freshness are named without claiming unobserved model use.
- Safe lookups were answered directly; unresolved decisions were asked only after frontier entry confirmation.
- Every decision frontier was dependency-ready, recommendation-backed, and bounded within 6 rounds.
- Any follow-on plan or `omh_maestro` handoff remains `prepared_not_observed` until explicitly authorized and observed.
