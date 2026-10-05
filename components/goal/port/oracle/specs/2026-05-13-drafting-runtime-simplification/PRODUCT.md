# Product Spec: Simplify goal drafting while hardening runtime execution and audit

## Summary

`pi-goal` should clearly separate goal-intent discussion from direct goal creation. `/goals` and `/sisyphus` start a normal agentic conversation that can research, clarify, grill, and converge on a confirmed goal contract. `/goals-set` and `/sisyphus-set` skip drafting and immediately create an active goal from the supplied text. Confirmed goals continue to have durable focus, lifecycle guards, auto-continue recovery, and independent completion auditing.

This change benefits users who start goals interactively. The current drafting flow can fail when tool visibility, hidden prompts, draft ids, or question nudges drift; the desired behavior is that goal confirmation remains robust even if the model asks plain questions, the user answers in later turns, or tools are resynced.

## Behavior

1. `/goals <topic>` starts a lightweight goal-intent conversation.
   - The user sees the agent clarify, research, grill assumptions, or propose a goal draft.
   - The agent may ask focused questions when the topic is vague or underspecified.
   - If limited read-only reconnaissance would improve the goal contract, the agent may perform it before proposing.
   - If the topic is already concrete, the agent may proceed directly to a draft proposal.
   - The runtime should not require a question counter, hidden draft prompt identity, or exact drafting-only tool surface to make progress.

2. `/sisyphus <topic>` follows the same intent-discussion model with stricter grilling and ordered-work wording.
   - The proposal preserves the user's ordered style and blocker rule.
   - Sisyphus remains a prompt/criteria variant, not a separate step-counter lifecycle.
   - The agent should grill the ordered plan enough to catch missing done criteria, blockers, and ambiguous step boundaries before proposing.
   - The agent should not add unrequested preflight, reconnaissance, or verification steps to the user's ordered plan.

3. `/goals-set <objective>` and `/sisyphus-set <objective>` directly create and focus a new active goal.
   - They do not start a drafting conversation or require `propose_goal_draft`.
   - The supplied text becomes the goal objective after trimming.
   - `/sisyphus-set` marks the goal as Sisyphus and requires the objective itself to carry any ordered plan the user wants enforced.
   - Empty objectives are rejected with a user-facing warning.

4. `propose_goal_draft` is a stable commit affordance rather than a fragile dynamically exposed tool.
   - The tool remains registered and discoverable enough that the model can use it after question turns, compaction, or active-tool resync.
   - Calls with an empty objective are rejected.
   - Calls that would silently change `/goals` into Sisyphus, or `/sisyphus` into a regular goal, are rejected.
   - Direct `create_goal` remains rejected; user confirmation through the draft dialog is still the creation path.

5. User confirmation remains explicit.
   - A proposed draft shows a plain-text confirmation report with original topic, proposed goal, mode, and auto-continue choice.
   - Confirm creates and focuses a new active goal without clearing other open goals.
   - Continue Chatting keeps the conversation in clarification mode and does not create a goal.
   - `/goal-clear` and `/goal-abort` can cancel an in-progress confirmation conversation.

6. The confirmed execution state remains strict and durable.
   - Active, paused, and complete states continue to be persisted under `.pi/goals/`.
   - Focus remains session-owned and is reconciled from disk/ledger as today.
   - Auto-continue applies only to the focused active goal.
   - Stale continuation prompts are neutralized instead of acting on the wrong goal.
   - Empty/non-progress turns do not trigger an infinite continuation loop.
   - Pause, abort, completion, and tweak stop the current turn from doing further mutating work.

7. Completion remains strict and independently audited.
   - `update_goal(status="complete")` remains the only completion/archival path for an active or paused goal.
   - The independent auditor must approve before the goal is archived as complete.
   - Audit start and audit result stay visible as transcript stages.
   - Auditor rejection leaves the goal open and records enough context for the next run to address the rejection.

8. Existing open goals and archives remain compatible.
   - Removing or simplifying drafting runtime state must not require migration of `.pi/goals/active_goal_*.md`, archived goal files, or `goal_events.jsonl`.
   - Existing active/paused goals continue to resume normally.
   - Existing documentation and tests should no longer describe drafting as a heavyweight runtime phase.

## Goals / Non-goals

- Goal: Make goal-intent discussion resilient and conversational instead of dependent on a fragile hidden drafting state machine.
- Goal: Provide direct set commands for users who already know the exact objective and want execution to start immediately.
- Goal: Keep the long-running execution loop, persistence, focus reconciliation, pause/abort semantics, post-stop guard, compaction recovery, and independent audit strict.
- Goal: Preserve explicit user confirmation for discussion-based goal creation.
- Goal: Reduce code and prompt surface around drafting-specific nudges, question counters, draft identity gates, dynamic tool visibility, and redundant creation aliases.
- Non-goal: Reintroduce token budgets, budget-limited lifecycle states, or hard auto-continue turn caps.
- Non-goal: Remove independent completion auditing or make executor self-certification sufficient.
- Non-goal: Change the on-disk goal record format unless required for execution-state compatibility.
- Non-goal: Build a full general-purpose workflow engine for goal creation.

## Decisions

- `propose_goal_draft` still requires a recent `/goals` or `/sisyphus` confirmation intent. This keeps discussion-based goal creation user-command-owned while removing hidden draft ids and question counters.
- Continue Chatting keeps the thin session-local confirmation intent so the next proposal can still use the same original topic and mode.
- `/goals-set` and `/sisyphus-set` are direct creation paths and intentionally bypass confirmation because the user selected an explicit set command.
- `/goal-tweak` remains a separate follow-up path for this refactor; it can be simplified later if the lightweight goal confirmation model proves stable.
