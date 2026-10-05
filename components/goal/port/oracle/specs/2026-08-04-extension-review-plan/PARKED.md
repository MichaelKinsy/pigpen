# Parked feature candidates (pi-goal-x review)

Parked 2026-08-04 by user steering: the review's new-feature section was
narrowed from 10 candidates to 1–3 features focused on making the task system
better, with no additional slash commands. The full original candidate set is
preserved here verbatim for future consideration; none of these items were
implemented.

## Part 3 — New features (3–10, balanced across agent / human / TUI)

**F1. Multi-goal dashboard overlay (`/goal-dashboard`).** Description: a TUI overlay listing
every open goal with status, usage, budget (when set), task progress, and the
next pending task, with enter-to-focus and a refresh key. Extends the
task-list-overlay pattern; headless equivalent: `/goal-list` already exists, so
the dashboard is TUI-only.
Surfaces: **TUI** (+ human). Rationale: the extension already supports many open
goals but only shows one at a time; the data (pool + usage + ledger) is all in
memory. User value: a single glance at everything running, without
`/goal-list` + `/goal-status` round trips.

**F2. Archive browser + reopen (`/goal-archive`, `/goal-reopen <id>`).**
Completed and cleared goals go to `.pi/goals/archived/` and the ledger but have
no user surface. Description: `/goal-archive` lists archived goals (from the
archived dir + ledger reconstruction) with completion dates and audit verdicts;
`/goal-reopen <id>` creates a fresh active goal carrying the objective, mode,
task structure (progress reset to pending), and verification contract.
Surfaces: **TUI** (+ human); agent-facing via an optional `get_goal` note when
an archived goal matches the current topic. Rationale: archival is currently a
dead end; reopening is a natural continuation pattern for recurring objectives.
User value: goals become part of a durable lifecycle instead of ending at
"complete".

**F3. Stall detector + wake prompt.** Description: a background (event-driven,
no polling) check: if an active auto-continue goal has had no
continuation/tool activity for N minutes (configurable; default off), emit one
notification and a
`[GOAL STALLED]` steering note into the next prompt asking the agent to report
progress or the user to pause/resume. Surfaces: **agent** (prompt + event) and
**TUI** (notify + widget badge). Rationale: an agent that silently stops
looping (provider hiccup, deadlocked turn) leaves an "active" goal that isn't
running; today nothing distinguishes that from healthy silence. User value:
stale "running" goals get noticed instead of lingering.

**F4. Quiet hours / pause schedule for auto-continue.** Description: settings
gain
`quietHours` (start/end, timezone-optional): during the window,
`queueContinuation` defers instead of firing (checkpoints queue but do not
send). Surfaces: **agent** (continuation policy) and **TUI** (settings rows +
widget shows "quiet until HH:MM"). Rationale: overnight auto-continue on
personal machines is the main reason users disable autoContinue entirely.
User value: keep long-running goals without burning tokens at 3am; the policy
lives in one place (GoalRuntime) and is independently testable.

**F5. Goal export/import (portable contracts).** Description: `/goal-export <id>`
writes a
portable markdown contract (objective, mode, verification contract, task
structure, blockCompletion, budget) to a chosen path; `/goal-import <path>`
validates and drafts it via the existing proposal dialog for confirmation.
Surfaces: **TUI** (+ human); agent can use it to lift a goal contract between
projects. Rationale: objectives are valuable artifacts; the file format
(JSON meta + `# Goal Prompt` body) is already stable and the drafting
confirmation path already exists. User value: move a goal between projects or
machines, or share a well-formed objective contract.

**F6. Token-budget alerts at thresholds.** Description: when accounted usage
crosses 50/75/90%
of a goal's token budget, emit a `goal_budget_warning` ledger event, a
notification, and a widget progress hint; the final `budget_limited` transition
already exists. Surfaces: **TUI** (widget + notify) and **agent** (event +
prompt line). Rationale: the budget currently transitions only at 100% with no
warning gradient. User value: users can decide to raise or trim scope before
the hard stop, not after.

**F7. Per-goal autoContinue presets.** Description: settings gain a preset map
(e.g.
`autopilot` = autoContinue on + aggressive idle retry, `watchful` = on with
stall detector + quiet hours, `manual` = off) applied at creation and
switchable via `/goal-settings`. Surfaces: **agent** (default applied at
`create_goal`/draft confirmation) and **TUI** (settings + status line).
Rationale: autoContinue is currently a single boolean with no vocabulary for
"how aggressively this goal should drive itself". User value: one decision at
creation sets a sane operating mode; presets make the settings menu less
abstract.

**F8. Audit report retention and diffing.** Description: store the last audit
report on the
goal record (`lastAuditReport` with verdict + date) and expose prior-vs-current
rejection reasons in the completion flow's rejection text (what changed since
the last rejection). Surfaces: **agent** (rejection text + `get_goal`) and
**TUI** (widget line). Rationale: the ledger retains reports but the surfaces
re-derive "latest rejection" ad hoc; the completion flow already injects
rejection context. User value: agents can see *why it was rejected before* and
target their rework instead of re-attempting blindly.

**F9. Sisyphus ordered-step progress widget.** Description: for sisyphus goals,
the widget
shows the ordered steps with the current step highlighted and a "Step N/M"
badge, derived from the objective's step markers and the latest task/event
state. Surfaces: **TUI** (widget) and **agent** (`get_goal` line, per E7).
Rationale: sisyphus is the extension's most sequential mode and currently has
the weakest progress visualization. User value: clear "where am I in the
sequence" at a glance, for both the human and the agent.

**F10. Headless-friendly pause banner.** Description: in `hasUI:false` sessions,
a pause via
`update_goal(paused)` or `/goal-pause` prints a one-shot banner line to the
session log (reason + suggested action) so the next human opening the log sees
why work stopped. Surfaces: **human** (log reader). Rationale: headless runs
have no widget or notify; the pause reason currently exists only in the goal
file. User value: auditability of why a background run stopped, without opening
`.pi/goals` files.

---

### Selection rationale (balance check)

Agent-facing value: F3 (stall), F4 (quiet hours), F6 (budget alerts), F7
(presets), F8 (audit retention). Human-facing: F2 (archive/reopen), F5
(export/import), F10 (pause banner). TUI: F1 (dashboard), F2, F4, F5, F6, F9
(widget). Every feature reuses existing infrastructure (pool, ledger, widget
registry, settings schema, proposal dialog, GoalRuntime scheduling) rather than
adding a new subsystem; each is independently shippable and testable against
the existing harness.



---

## F7 (raised then re-parked) — Headless pause banner

Raised into the plan as F7 on 2026-08-04, then parked again on the same day by
user direction: "we don't use headless". The expanded description is preserved
here for reference; it was never implemented.

**F7. Headless pause banner (priority 7).** Description: in headless sessions
(`hasUI:false` — CI runs, background pi processes, no TUI), a goal pause via
`update_goal({status:"paused"})` (with reason and optional suggested action)
or `/goal-pause` prints a **one-shot banner** to the session log at the moment
of the pause transition, so the next human who opens that log sees why the
run stopped. Example:

```
[PI GOAL PAUSED abc123] Work stopped: awaiting your decision on the feature set.
Suggested next: /goal-tweak, then /goal-resume. (Auto-continue is off.)
```

Mechanics: fires exactly once per pause transition — hooked to the existing
`goal_paused` ledger-event path so it is idempotent and never re-printed by
later checkpoints; it carries the full untruncated pause reason + suggested
action; TUI sessions are unaffected (they already have the widget/dialog).
Surfaces: **human** (log reader). Rationale: in headless runs there is no
widget and no notification surface — today the pause reason lives only in the
goal record and the agent-facing paused prompt (the "Agent pause reason:"
line), which a human skimming the log never sees; the ledger event exists but
is not readable at a glance. User value: anyone auditing why a background run
stopped gets the answer in the first lines of the log, without opening
`.pi/goals` files or re-parsing ledger JSONL.


---

## Parked optimisation candidates (non-clock-time)

Moved out of PLAN.md Part 1 on 2026-08-04 when the optimisation plan was
refocused on user-felt clock time, then **restored to PLAN.md Part 1B on the
same day** at the user's direction ("we want things clean and fast"). Kept
here only as a historical record; the authoritative copy is PLAN.md.

**P1-4. Single task-counting implementation.** `buildTaskSummary` (policy),
`countAuditorTasks` (auditor), `countAllTasks`/`countAllWithStatus` (widget +
overlay), and `countSubtree` (prompts) each re-implement subtree counting with
different "done" semantics. Description: one shared counter module with an
explicit `doneIncludesSkipped` flag, used by all four call sites. Rationale:
removes ~5 copies of the same walker and the semantic drift that produced
inconsistent "skipped counts as done" behavior between surfaces. User value:
consistent task numbers across widget, prompt, status, and auditor output.


**P1-5. Deduplicate contract extraction and confirmation-task rendering.**
`extractVerificationContract` exists in both `goal-contract.ts` and
`goal-draft.ts`; `renderConfirmationTasks` exists in both `goal-task-confirmation.ts`
and `goal-draft.ts`; the bordered dialog scaffold (`line()`, truncation,
header/footer) is copy-pasted across `goal-escape-dialog.ts`,
`goal-task-confirmation.ts`, and `widgets/task-list-overlay.ts`. Description:
collapse to one module each. Rationale: identical logic diverging in three
places is a correctness hazard (escape dialog vs confirmation dialog widths
already differ slightly). User value: fewer subtle rendering inconsistencies;
smaller surface to maintain.


**P1-9. Decompose `goal-state.ts`.** The 870-line core mixes state, UI, and
lifecycle. Description: extract the widget/status glue (`updateUI`,
`clearGoalWidget`, `goalForDisplay`) and the focus-setter trio into focused
helpers on the same core, shrinking the interface. Rationale: the 50-member
interface is the biggest maintainability cost in the extension. User value:
indirect (fewer regressions, faster iteration) — no user-visible behavior
change.


**P1-10. Prune debug-only surface from the shipped bundle.** The debug
keybindings/helpers in `goal-widget.ts` and the debug panel in
`widgets/goal-widget.ts` ship to every install. Description: gate them behind
an env flag (e.g. `PI_GOAL_DEBUG`) so the default bundle excludes dead code
(`openDebugProposal` is already effectively inert). Rationale: reduces shipped
code and removes module-level mutable debug state from production.
User value: smaller surface; fewer accidental trigger paths.

---


