# PRODUCT — Drafting Prompt Normalization

## Problem

When a user invokes `/goals <topic>` and enters the drafting flow, they may need to iterate several times: the agent proposes a goal draft, the user clicks "Continue Chatting", the agent refines and re-proposes. After a few such cycles, the LLM begins generating meta-cognitive commentary like "we seem to be going in circles" or "you seem frustrated."

This is false — the user is simply iterating on the goal contract, which is normal expected behavior during goal drafting.

## Root Cause

The "going in circles / frustrated" behavior is **not** explicitly coded anywhere in pi-goal. It is an emergent LLM behavior. The model detects a pattern:

```
propose_goal_draft → "Continue Chatting" → proposal rejected → ask what changed → refine → propose_goal_draft → "Continue Chatting" → ...
```

LLMs are trained to detect negative user sentiment and apologize/de-escalate. Repeated "rejection" signals trigger this learned behavior.

Two concrete factors amplify this:

1. **The drafting prompt does not normalize iteration.** `goalDraftingPrompt()` tells the agent to "keep grilling assumptions" and "ask one focused question" but never says "multiple cycles of proposal and refinement are normal." Without that framing, the LLM falls back to its default pattern of inferring frustration from repeated corrective feedback.

2. **The post-Continue-Chatting feedback frames the user's action negatively.** The tool result text currently says:

   > "User clicked 'Continue Chatting'. The goal was NOT created. Ask the user what they want to change..."

   The word "NOT" in all-caps, combined with the structure of reporting a "failure" to create, subtly frames the user's choice as a rejection. This primes the agent to treat subsequent iterations as a cycle of failure rather than a normal refinement process.

## Approach

Two targeted changes, purely at the prompt/framing level:

### Change 1: Normalize iteration in the drafting prompt

Add language to `goalDraftingPrompt()` that:

- Explicitly states that multiple proposal-refinement cycles are normal and expected.
- Tells the agent: "Do not interpret repeated 'Continue Chatting' choices as frustration. It simply means the user wants to refine the goal contract."
- Gives structured "what to do next" guidance for the turn after Continue Chatting: ask what to change, propose an updated draft, do not re-propose the same content.

This goes into the `commonProtocol` section so it applies to both `/goals` and `/sisyphus` modes.

### Change 2: Reframe the post-Continue-Chatting tool result

Change the tool result text in `goal.ts` from negative-framing language (all-caps "NOT") to neutral language that:

- Still makes it unambiguous the goal was NOT created (the agent must not proceed with execution).
- Removes the "rejection" framing.
- Uses matter-of-fact language: e.g., "Goal refinement requested" or "Goal draft deferred" rather than "The goal was NOT created."

### Change 3: Benchmark experiment

Create an experiment case that simulates 3+ iterations of the propose → Continue Chatting → refine cycle and scans the captured conversation for frustrated/cycling language patterns. This allows quantifying the defect before the fix and verifying its elimination after.

## Success Measurement

| Metric | Before | After |
|--------|--------|-------|
| Frustrated/cycling language in conversation after 3 iterations | Present | Absent |
| Unit test verifies normalization text in prompt | N/A | Present |
| Unit test verifies neutral framing in tool result | N/A | Present |

## Design Decisions

1. **No runtime state changes.** We considered adding an iteration counter to `confirmationIntent` that the drafting prompt could reference (e.g., "you've proposed 3 drafts so far"). This would have been more robust (the agent would know exactly how many iterations occurred) but adds unnecessary complexity. Pure prompt engineering is sufficient — the explicit instruction not to interpret iteration as frustration addresses the root cause directly.

2. **No UI changes.** The "Confirm / Continue Chatting" dialog labels stay the same. Only the tool result text that the agent reads changes.

3. **Benchmark exists alongside unit tests.** Unit tests verify prompt content deterministically. The benchmark experiment gives empirical before/after evidence with a real model, which is valuable for a behavioral fix like this.
