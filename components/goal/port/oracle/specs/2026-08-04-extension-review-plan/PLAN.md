# Extension Review Plan — pi-goal-x (plan-only)

Date: 2026-08-04 · Scope: the full extension, every part, no exceptions ·
Deliverable: this plan (optimisation + feature enhancements + new
features + benchmarking), prioritized, each item with description + rationale + user value.
No effort/risk ratings by design. No implementation in this plan. New-feature
section scope steered by the user on 2026-08-04: task-focused features plus
the most useful UI feature changes (6 features total), with no new slash
commands (the earlier 10-candidate set is parked in `PARKED.md`).

---

## Part 0 — Audit coverage map (every module, no exceptions)

The extension ships as one installer (`extensions/goal.ts`, 33 lines) plus
~9.5k lines across 29 modules. Behavior is pinned by 24 test files under
`tests/` (unit + handler-level integration), the experiment matrix under
`experiments/cases/` (B1–B2, C1–C26), the scroll-churn harness under
`experiments/scroll-repro/`, and four docs (`docs/agent-flow-design.md`,
`docs/agentic-runtime-prd.md`, `docs/architecture.md`,
`docs/goal-ts-refactor-test-strategy.md`).

| Module | Responsibility | Observations (hotspots / plan-relevant) |
|---|---|---|
| `goal.ts` | Thin installer: message renderers, `createGoalCore`, registers commands/tools/events | Correctly minimal; no logic. |
| `goal-state.ts` (870) | `GoalCore`: all session state, tool-profile install, UI/widget glue, accounting glue, focus ops, archive/pause/continuation, `loadState`, `replaceGoal` | **Largest maintainability hotspot.** ~50-member interface; `state.goal` getter/setter with side effects; three similar focus-setters (`setGoal`, `updateFocusedGoal`, `setFocusedGoalId`); UI factory duplicated in `updateUI` (unfocused vs focused branches). |
| `goal-service.ts` (536) | `GoalService`: sole mutation boundary — reconcile, lock + revision check, write→ledger→memory commit, task transactions, `persist` with additive usage merge | Ledger-failure `onDiagnostic` boilerplate duplicated at **5 sites** (apply, updateTaskAttempt, persist, create, appendEvents) with identical message construction. |
| `goal-runtime.ts` (205) | Continuation scheduling (timer + idle gating), stale-checkpoint state, turn-stop guard, one-time steering reminders | Clean, encapsulated, independently testable. Scheduling retry loop uses 50ms unref'd timers — fine. |
| `goal-accounting.ts` (92) | Idempotent token/time charge, budget helpers | Tokens charged at **turn/tool-end granularity**, not per-tool-call — budgets can overshoot slightly between charges. `liveSeconds` clones goal per render. |
| `goal-policy.ts` (324) | Status/transition gates, task validation (depth, dupes, acyclic), task-tree walkers, report builders | `buildTaskSummary` counts **skipped as done**; `goal-auditor.ts`'s `countAuditorTasks` counts them separately; widgets/prompts each have their own counters — **4+ task-counting implementations** with subtle semantics drift. |
| `goal-record.ts` (309) | Types, normalization (status-authoritative reads), id/path helpers, budget validation | `safeIdPart` slices ids to 80 chars; `newGoalId` uses `Math.random` (non-crypto). Normalization is defensive and consistent. |
| `storage/goal-files.ts` (292) | Path safety (symlink/traversal guards), atomic writes, parse/serialize, `readActiveGoalPool`, objective-from-body merge | `readActiveGoalPool` does a **full sync directory scan + parse of every active goal file on every reconcile** (reconcile runs per tool call / turn event). |
| `storage/goal-lock.ts` (118) | Per-goal exclusive lock, stale recovery (TTL + pid liveness) | `Atomics.wait` **synchronous sleeps** on the main thread (default 100×25ms ≈ 2.5s worst case; persist uses 10×25ms). |
| `goal-ledger.ts` (367) | JSONL append (temp-write→read→append), full-file read+validate+sanitize, ledger reconstruction, latest-event queries | Every `readGoalLedger` **parses the entire JSONL** — called in `before_agent_start` (up to twice per turn), compaction, prompt building. Grows unbounded per session. Append = 2 file ops per event; events appended one-by-one in loops. |
| `goal-pool.ts` (101) | Pool construction, open-goal ordering, focus resolution, selector labels, usage merge | Small and clean. `otherOpenGoalCount` re-sorts the pool each call. |
| `goal-compaction.ts` (136) | Compaction summaries from goals + ledger | Depends on full ledger parse (see ledger). |
| `goal-events.ts` (408) | All lifecycle handlers: context rewrite, turn lifecycle, staleness gates, pause/error guards, session start/tree/compact, shutdown | `before_agent_start` can call `readGoalLedger` **twice** (paused + active paths) and `reconcileFocusedGoalFromDisk` multiple times. The `context` handler rewrites every queued goal event each context call. |
| `goal-commands.ts` (554) | 14-command palette: draft entry, direct-create, focus/unfocus, list/status, settings menu, tweak, clear, pause/resume | `handleSettingsMenu` inner block has **broken indentation** (readability); settings menu loop is the deepest nesting in the codebase. |
| `goal-core-tools.ts` (287) | `get_goal` / `create_goal` / `update_goal` (blocked, agent-pause, completion dispatch) | Prompt guidance embedded in tool definitions (good). `create_goal` always `terminate: true`. |
| `goal-task-tools.ts` (510) | `set_goal_tasks` / `update_goal_task`: flat→tree conversion, id-stable merge, task transactions | Flat-input validation is thorough (dupes, parents, cycles, depth, lightweight placement). `mergeTasksWithExisting` intentionally clears omitted structural fields. |
| `goal-task-confirmation.ts` (172) | Neutral task-list confirmation dialog (overlay) | Duplicates the bordered-line/truncation helpers also found in `goal-escape-dialog.ts` and `widgets/task-list-overlay.ts` (3 copy-pasted dialog scaffolds). Exports `renderConfirmationTasks`, which **`goal-draft.ts` re-implements identically**. |
| `goal-completion.ts` (398) | `update_goal(complete)` flow: gates, auditor dispatch, disabled/skip branches, escape dialog, single completion transaction, deferred archival | Long single function with interleaved ledger appends; four separate `appendEvents` try/catch blocks with near-identical swallow semantics. |
| `goal-auditor.ts` (455) | Auditor prompt, `resolveAuditorModel` (provider-only refusal), session creation with shared model runtime, progress tool, decision parse | `makeAuditorResourceLoader` returns an **empty resource loader** — auditor sessions get no project skills/extensions (deliberate isolation; also a capability ceiling). Audit animation timer pings the widget every 80ms. |
| `auditor-selector.ts` (72) | Settings picker choices: default/current ✓, authenticated models, manual entry, thinking levels | Clean, testable. |
| `goal-drafting.ts` (340) | Durable draft sessions (custom entries), rehydration, drafting tool registrations, proposal→confirm/cancel/continue, tweak apply | `activeDrafts` is a module-level `WeakMap`. Tweak path reuses the goal-draft pipeline cleanly. |
| `goal-draft.ts` (263) | Draft confirmation text builders, verification-contract extraction, drafting prompt | **Duplicates `extractVerificationContract` from `goal-contract.ts`** and `renderConfirmationTasks` from `goal-task-confirmation.ts`. |
| `goal-questionnaire.ts` (555) | Shared question UI (multi-tab), proposal dialog, spinner/hardware-cursor handling, terminal-height churn guard | Most complex UI file (~200 lines of custom ANSI render logic); the `render` closure is hard to test directly (covered via widget-level tests). |
| `goal-widget.ts` (341) | `syncTerminalInputPause`: Escape/audit-abort/task-overlay keybindings + **debug-mode helpers** | Debug helpers (`createDebugGoal`, `injectDebugTasks`, `startMockAudit`, `openDebugProposal`) ship in the production bundle; module-level mutable counters/timers; `openDebugProposal` is effectively dead (only notifies). |
| `widgets/goal-widget.ts` (378) | Above-editor goal widget + auditor progress widget + debug panel | Spinner frames derived from wall clock (`Date.now()/80`); render safety-net truncation. Debug panel gated by debugMode. |
| `widgets/task-list-overlay.ts` (389) | Ctrl+Shift+T scrollable multi-goal task overlay | Self-contained; duplicates dialog scaffold helpers. |
| `widgets/goal-escape-dialog.ts` (150) | Escape-during-audit choice dialog | Third copy of the bordered-dialog scaffold. |
| `widgets/goal-notifications.ts` (9) | Running-goal notification builder | Minimal. |
| `goal-format.ts` (237) | Entry/render helpers, event heading renderers, error/abort/tool-use message classifiers, token extraction | Message classifiers + token extraction are the model-coupling seam (all pi-agnostic shape checks — good). |
| `goal-core.ts` (78) | Display helpers: truncation, status labels, footer | `truncateText` hardcodes `max=120`; `footerStatus` truncates objective to 60 cols. |
| `goal-settings.ts` (187) | Settings file parse/load/save, env overrides, key rejection | `loadGoalSettings` does a **sync `fs.readFileSync` on every call** — invoked in `before_agent_start`, `queueContinuation`, tool gates, widget render (`getSettings`), drafting. No caching. |
| `goal-contract.ts` (58) | Verification-contract extraction, prompt-safe objectives, sisyphus sufficiency | Duplicated extraction exists in `goal-draft.ts`. |
| `goal-tool-names.ts` (70) | Tool-name constants, profiles, progress-tool sets | Single source of truth for tool sets — good. |
| `goal-tools.ts` (16) | Registration composition | Trivial. |
| `prompts/goal-prompts.ts` (228) | Active/continuation/paused/budget/stale/unfocused prompts, bounded fragments | `taskListBlock` renders the **entire task tree** (up to 50 tasks + subtrees) into every continuation prompt; fragment capped at 10k chars total so big trees crowd out the objective. |

---

## Part 1 — Optimisation plan (prioritized by felt clock time)

Framing: per-turn wall clock is dominated by model inference, but the
extension controls the *overhead around it* — synchronous I/O, context size,
and stalls. Part 1A lists the clock-time items the user actually feels,
ordered by impact, each stating its order-of-magnitude effect where it
applies (slow storage / long sessions / contention). Part 1B restores the
maintainability items at the user's direction — "we want things clean and
fast": they do not move clock time but keep the codebase clean while 1A makes
it fast. Every P1 item ships with a before/after benchmark (Part 4); nothing
lands unmeasured.

### 1A — User-felt clock time (priority order)

**P1-1. Cache-first read layer for settings, goal pool, and the focused goal.** Description:
one shared read layer with mtime-keyed caching covering the settings file,
the per-goal files, and the pool listing, so the per-turn pipeline never
re-reads or re-parses what it already holds; the focused goal is parsed once
per turn (reconcile currently parses it again right after the pool scan).
Rationale: `before_agent_start`, per-tool-call reconcile, `queueContinuation`,
and the widget's render-time `getSettings` together do roughly 5–10 sync
reads per turn; on local SSD that is ~5–20ms, but on network home dirs (NFS,
iCloud-synced homes, CI sandboxes) it is 0.5–2s of blocking I/O — and every
sync read also stalls the TUI render loop. User value: per-turn extension
overhead drops by an order of magnitude on exactly the storage where it is
noticeable, and the TUI stops stuttering on shared/remote homes.

**P1-2. Incremental ledger access (byte-offset tail cache) + bounded retention.** Description:
keep the parsed ledger tail in memory keyed by file size/mtime, resume from
the last byte offset on append, and cap the in-session window (older events
summarized into counts); `before_agent_start`'s double read becomes one O(1)
tail read. Rationale: every `readGoalLedger` parses the entire JSONL, and
sessions grow without bound — the only per-turn cost in the extension that
gets worse as the session ages, O(file) today. User value: hour-long sessions
with thousands of events stay as fast as fresh ones (order-of-magnitude on
long runs), and bounded memory removes parse/GC spikes.

**P1-3. Mutations batched into one transaction per turn.** Description:
task/status/usage mutations accumulate in memory during a turn and flush once
— one lock acquire, one goal-file write, one ledger batch — at turn end or
before the next continuation, instead of lock+write+append per tool call.
Rationale: a task-heavy turn currently performs N lock acquisitions, N
full-file writes, and N ledger appends, each re-entering the read pipeline
(P1-1); the lock window also widens the contention that can stall other pi
processes. User value: 5–10x fewer I/O ops and lock holds on real turns —
felt on slow storage, and a proportionally smaller contention window
everywhere.

**P1-4. Prompt/context memoization + task-tree trimming.** Description: render
the goal prompt block (status, task list, queued events, policy) into cached
fragments invalidated only when goal state actually changes, and trim
`taskListBlock` to pending-first with completed/skipped collapsed to counts.
Rationale: the context handler rebuilds the goal block on every context call,
and every continuation turn pays prefill on the full block; a 50-task tree
can be 2–4k tokens, most of it already-completed work. User value: a 5–10x
smaller goal block means proportionally cheaper and faster prefill on every
turn — on long auto-continue runs this is the largest compounding clock-time
and token saving available at the prompt level.

**P1-5. Remove the main-thread lock stall (async or strictly bounded acquire).** Description:
replace `Atomics.wait` polling with a promise-based acquire on the async tool
paths, and tighten the persist bound to a few tens of ms. Rationale: the
default acquire sleeps the main thread up to ~2.5s (100×25ms) under
cross-process contention — a frozen TUI during a tool call. User value:
contention no longer freezes the interface; worst-case wait collapses from
seconds to tens of ms (order-of-magnitude on the stall itself).

**P1-6. Auditor starts warm (reuse goal-relevant context).** Description:
seed the auditor session with the parent's already-rendered goal context —
objective, task states, verification contract, ledger tail, and the
tool-evidence trail from the current turn — instead of a cold session that
re-reads everything from scratch. Rationale: the completion audit is the most
expensive single operation in the extension (a full agent run, minutes); the
cold session pays full prefill and re-derives facts the parent already holds.
User value: verdicts arrive sooner on every completion (prefill seconds to
minutes depending on model), and audits see the evidence actually gathered
rather than a re-derivation.

**P1-7. Parallel + cached startup rehydration.** Description: `loadState`
currently reads the pool and goal/draft files sequentially; parallelize the
reads and reuse the P1-1 cache. Rationale: session start pays N sequential
sync reads before the first goal prompt renders. User value: with many open
goals, pi gets to the first goal interaction 5–10x faster.

**P1-8. Batch ledger appends (array-write, one durability op).** Description:
`appendGoalEvent` takes an event array and writes one line block with the
existing temp+rename durability, used by the completion/focus flows that
already emit 2–4 events in sequence. Rationale: halves to quarters per-event
I/O. User value: compounds with P1-2/P1-3 on the same write path.

**P1-9. Coalesced widget updates (one render per turn).** Description:
debounce `updateUI` so a tool-heavy turn renders once at turn end rather than
per event; spinner cadence and dialogs unchanged. Rationale: render is cheap,
but per-event rebuild plus the render-time settings read (see P1-1) add up in
turns with many tool calls. User value: steadier TUI during long turns and
fewer sync reads on the render path.

### 1B — Maintainability & cleanliness (priority order)

**P1-10.Single task-counting implementation.** `buildTaskSummary` (policy),
`countAuditorTasks` (auditor), `countAllTasks`/`countAllWithStatus` (widget +
overlay), and `countSubtree` (prompts) each re-implement subtree counting with
different "done" semantics. Description: one shared counter module with an
explicit `doneIncludesSkipped` flag, used by all four call sites. Rationale:
removes ~5 copies of the same walker and the semantic drift that produced
inconsistent "skipped counts as done" behavior between surfaces. User value:
consistent task numbers across widget, prompt, status, and auditor output.

**P1-11.Deduplicate contract extraction and confirmation-task rendering.**
`extractVerificationContract` exists in both `goal-contract.ts` and
`goal-draft.ts`; `renderConfirmationTasks` exists in both `goal-task-confirmation.ts`
and `goal-draft.ts`; the bordered dialog scaffold (`line()`, truncation,
header/footer) is copy-pasted across `goal-escape-dialog.ts`,
`goal-task-confirmation.ts`, and `widgets/task-list-overlay.ts`. Description:
collapse to one module each. Rationale: identical logic diverging in three
places is a correctness hazard (escape dialog vs confirmation dialog widths
already differ slightly). User value: fewer subtle rendering inconsistencies;
smaller surface to maintain.

**P1-12.Decompose `goal-state.ts`.** The 870-line core mixes state, UI, and
lifecycle. Description: extract the widget/status glue (`updateUI`,
`clearGoalWidget`, `goalForDisplay`) and the focus-setter trio into focused
helpers on the same core, shrinking the interface. Rationale: the 50-member
interface is the biggest maintainability cost in the extension. User value:
indirect (fewer regressions, faster iteration) — no user-visible behavior
change.

**P1-13.Prune debug-only surface from the shipped bundle.** The debug
keybindings/helpers in `goal-widget.ts` and the debug panel in
`widgets/goal-widget.ts` ship to every install. Description: gate them behind
an env flag (e.g. `PI_GOAL_DEBUG`) so the default bundle excludes dead code
(`openDebugProposal` is already effectively inert). Rationale: reduces shipped
code and removes module-level mutable debug state from production.
User value: smaller surface; fewer accidental trigger paths.

---

## Part 2 — Feature-enhancement plan (prioritized)

**E1. Per-goal event/audit history in status.** `get_goal` and `/goal-status`
show current state but not the goal's history. Description: surface the last
audit verdict + reason and recent lifecycle events (from the ledger, capped)
in `get_goal` and `/goal-status`, and in the paused prompt where a rejection
is already injected. Rationale: the ledger already records everything; the
surfaces just don't read it. User value: users and agents see why a goal was
paused/rejected without digging into `.pi/goals/goal_events.jsonl`.

**E2. Effective-settings visibility.** Env overrides can silently change
behavior (`PI_GOAL_DISABLE_TASKS`, `PI_GOAL_SETTINGS_FILE`, budgets).
Description: `/goal-status` gains a "Settings" block showing effective values
with provenance (env vs file vs default), and `/goal-settings` marks rows
overridden by env as read-only with a hint. Rationale: settings are declarative
but opaque about their source. User value: fewer "why is my auditor different"
surprises.

**E3. Auditor session reuse of project skills.** `makeAuditorResourceLoader`
returns an empty loader, so the auditor can only use its six tools.
Description: optionally load the project's own skills/extensions into the
auditor session (off by default for isolation, on via a setting) so audits can
follow project-specific verification conventions. Rationale: audit quality
currently depends only on generic read/bash evidence. User value: stronger,
project-aware audits for teams that codify checks as skills.

**E4. Budget awareness in the widget and completion gate.** Description: show a
live budget progress line in the widget (`used/total`, remaining) and make the
budget-limited completion path mention the remaining-vs-overshoot fact in the
wrap-up steering. Rationale: budgets are set on creation but nearly invisible
afterwards. User value: users see cost pressure before the limit hits; agents
get a concrete number to steer by.

**E5. Drafting answer echo in the created-goal report.** Description: the
post-confirmation report already shows the objective; append a compact
Q&A summary from the questionnaire (question → answer) when drafting used
`goal_questionnaire`. Rationale: the answers shaped the goal but vanish after
confirmation. User value: users can verify their input survived the
clarification loop.

**E6. Sisyphus step progress in `get_goal`.** Description: for sisyphus goals,
derive the current step from the objective's ordered list and the latest
events/tasks, and include "At step N of M" in `get_goal` output and the widget
subtitle. Rationale: ordered execution is the point of sisyphus mode; the
surfaces don't say where the goal is. User value: better progress awareness for
long sequential goals.

**E7. Pause/abort reason preview in headings.** The `update_goal` heading
renders the status word only (deliberate 383ae52 surface). Description: keep
headings byte-identical, but add an expandable tool-result detail line that
carries the pause reason + suggested action so the collapsed heading stays
clean while the full reason is one keystroke away. Rationale: resolves the
old "truncated pause reason" complaint without touching the heading surface.
User value: full pause context visible in the transcript.

---

## Part 3 — New features (user-steered: 1–3, task-focused, no new slash commands)

Scope note (user steering, 2026-08-04): the earlier 10-candidate feature set
is parked in `PARKED.md`. The features below make the task system better and
raise the most useful UI changes from the parked set; they deliberately add
**no new slash commands** — each reuses existing surfaces: the goal-running
widget, the task-list overlay (Ctrl+Shift+T), `get_goal`, the
proposal/confirmation dialogs, notifications, and the `goal-service` mutation
boundary with its existing lock + revision + ledger guarantees. F1–F3 are the
task-focused core; F4–F6 are the raised UI feature changes. (The parked
multi-goal dashboard remains parked because it requires a new slash command.)

**F1. Task detail in the goal-running widget (priority 1).** Description: the
goal-running widget at the bottom of the TUI currently shows a single
pending-task line; give it a compact task-progress block instead — done/total
counts, the next pending tasks with depth-aware indentation and their
verification-contract snippets, and evidence lines for the most recent
completions (actor + short evidence). Large trees collapse to counts with a
"more in overlay" hint; `get_goal` mirrors the same block for headless/agent
use. This absorbs the former E2 widget-depth enhancement into one coherent
widget story.
Surfaces: **TUI** (goal-running widget) and **agent** (`get_goal`). Rationale:
the audit found the widget is the always-visible surface but shows only the
first pending task, while the task records already carry contracts and
evidence that no surface renders — the detail exists but never reaches the
bottom of the screen. User value: the human sees at a glance where the goal's
tasks stand, what is next, and why recent tasks are done — without opening the
overlay or issuing any command.

**F2. Objective→task bootstrap at creation (priority 2).** Description: when a
goal is created (draft confirmation or `create_goal`) with no task list and the
objective contains numbered steps, checklist markers, or a "Verification
contract:" line, auto-derive a proposed task tree and show it in the existing
confirmation dialog so the human can edit/confirm it before the goal exists;
the agent path stays tool-driven (`set_goal_tasks`) with the proposal surfaced
as guidance. Surfaces: **human** (confirmation dialog), **agent** (creation
flow), **TUI** (dialog). Rationale: the audit found tasks are an afterthought —
goals are commonly created with an empty task list, and the objective's own
structure (sisyphus steps, success criteria) already contains a task skeleton
that is currently thrown away at confirmation. User value: goals start with a
trackable, human-approved plan instead of a blank checklist the agent must
remember to propose later.

**F3. Interactive task toggling in the task overlay (priority 3).** Description:
the Ctrl+Shift+T overlay becomes actionable: Enter toggles a pending task to
complete and back, with the same gates as `update_goal_task` (children must be
complete first, parent-complete blocks, lightweight-subtask rules); completing
a task with a verification contract opens the existing evidence-confirmation
dialog; all mutations flow through `goal-service` so locking, revision checks,
and ledger appends are unchanged. Surfaces: **TUI** (human). Rationale: the
audit found the overlay is read-only while the agent has full mutation tools —
a human maintaining tasks in the TUI must leave the overlay and use
`/goal-tweak`; the mutation machinery and dialogs already exist, so this only
wires the existing surface to the existing boundary. User value: humans can
keep the task tree current where they already look at it, with identical
safety gates to the agent path.

**F4. Sisyphus ordered-step progress in the widget (priority 4).** Description:
for sisyphus goals, the goal-running widget shows the ordered steps with the
current step highlighted and a "Step N/M" badge, derived from the objective's
step markers and the latest task/event state; `get_goal` carries the same
"At step N of M" line (per enhancement E6).
Surfaces: **TUI** (goal-running widget) and **agent** (`get_goal`). Rationale:
sisyphus is the extension's most sequential mode and currently has the weakest
progress visualization. User value: clear "where am I in the sequence" at a
glance, for both the human and the agent.

**F5. Stall detector + wake prompt (priority 5).** Description: an event-driven
check (no polling): if an active auto-continue goal has had no
continuation/tool activity for N minutes (configurable; default off), emit one
notification and a `[GOAL STALLED]` steering note into the next prompt asking
the agent to report progress or the user to pause/resume; the widget shows a
badge while stalled.
Surfaces: **TUI** (notify + widget badge) and **agent** (prompt + event).
Rationale: an agent that silently stops looping (provider hiccup, deadlocked
turn) leaves an "active" goal that isn't running; nothing today distinguishes
that from healthy silence. User value: stale "running" goals get noticed
instead of lingering.

**F6. Token-budget alerts at thresholds (priority 6).** Description: when
accounted usage crosses 50/75/90% of a goal's token budget, emit a
`goal_budget_warning` ledger event, a notification, and a widget progress
hint; the final `budget_limited` transition already exists.
Surfaces: **TUI** (widget + notify) and **agent** (event + prompt line).
Rationale: the budget currently transitions only at 100% with no warning
gradient. User value: users can decide to raise or trim scope before the hard
stop, not after.

Prioritisation note: F1 first because it surfaces data already recorded (no
new mutation surface), F2 next because it reuses the confirmation dialog, F3
last of the task core because it adds the most UI machinery and requires gate
parity with `update_goal_task`. The raised UI features F4–F6 are ordered by
visibility value: sisyphus progress (F4) extends the just-approved widget
story, stall detection (F5) catches silent stalls, budget alerts (F6) surface
cost pressure before the limit.

## Part 4 — Benchmarking plan (before/after, every optimisation measured)

Framing: each P1 item ships with a before/after measurement that verifies its
claimed magnitude — no optimisation lands unmeasured. Coverage extends to
*every* extension feature (the Part 0 module map doubles as the benchmark
coverage map), not just the P1 hot paths. Agent time is excluded by
construction: no live model calls and no agent-session spawns in any
benchmark (B8 enforces this). Baselines are captured on the same machine and
storage before changes, and both local-SSD and slow-storage numbers are
reported wherever the magnitude claim depends on storage (the pattern follows
the existing `experiments/scroll-repro` before/after harness). Items are
prioritized by what they prove first; B1–B5 prove the optimisation items,
B7–B9 prove everything else and turn baselines into budgets for the next run.

**B1. I/O hot-path micro-bench harness with slow-storage emulation.** Description:
`experiments/bench/` micro-benchmarks over synthetic goal fixtures for the
paths touched by P1-1/P1-2/P1-3/P1-5/P1-8 — settings load, pool scan, ledger
parse vs tail resume, lock acquire, append — re-run under latency injection
(a delayed-read wrapper simulating NFS/iCloud round trips of ~1–100ms).
Rationale: the order-of-magnitude claims are storage-dependent; only a
deterministic harness can verify them without waiting for real slow home
directories. User value: every claimed 10x on the I/O items becomes a measured
number on both fast and slow storage.

**B2. Real-session per-turn I/O accounting.** Description: instrument a real
session (one focused goal, normal tool flow) to count sync reads/writes and
milliseconds per turn before/after P1-1/P1-3/P1-9 — the top-line "extension
overhead per turn" number the user actually feels. Rationale: micro-benchmarks
prove the parts; this proves the whole: 5–10 reads/turn → 1, N mutations/turn
→ 1, N renders/turn → 1. User value: the plan's headline metric is tracked
across the optimisation work instead of being assumed.

**B3. Long-session ledger simulation.** Description: generate synthetic ledgers
at 1k/5k/10k events and time full-parse vs tail-resume per call; assert cost
is flat (O(1)) and memory bounded after P1-2. Rationale: P1-2's claim is that
per-turn cost stops growing with session age — only a scaled simulation proves
it. User value: a guarantee that long-running goals never degrade turn by
turn.

**B4. Prompt/context size and prefill measurement.** Description: measure the
goal-block token count before/after P1-4 on a representative 50-task tree,
then measure one real continuation turn's prefill latency delta. Rationale:
P1-4 claims a 5–10x smaller block with proportional prefill savings — the
first half is mechanical, the second needs one instrumented turn. User value:
the compounding per-turn saving becomes a verified number (tokens and wall
clock per turn).

**B5. Startup, contention, and auditor timing.** Description: time session start
with 1/10/50 open goals (P1-7), lock-acquire worst case with two processes
contending (P1-5), and auditor time-to-verdict warm vs cold on a real
completion fixture (P1-6). Rationale: these three claims sit outside the
micro-bench substrate and need dedicated harnesses (two-process spawn, real
completion fixture). User value: startup, contention stalls, and audit
latency each get a before/after number instead of an estimate.

**B6. Regression gate.** Description: one bounded script (target <2 min) that
replays B1–B9 baselines and fails when a shipped optimisation regresses below
its recorded magnitude; run on demand and in CI. Rationale: without a gate,
optimisations silently decay in later refactors — the "fast" half of "clean
and fast" needs a watchdog. User value: the plan's gains are locked in;
regressions surface the turn they land, not months later.

**B7. Feature-wide wall-clock matrix (all extension features, agent-free).** Description:
every extension feature gets a deterministic benchmark case, not just the P1
I/O paths: tool handlers (goal create/focus/status/list, task tools,
`update_goal` complete/blocked/paused transitions), dialog renders
(questionnaire, accept-goal confirmation, task-list confirmation, escape
dialog, settings palette, task-list overlay), widget render + spinner cadence,
prompt/context block construction, ledger/pool/settings/lock/persist ops,
lifecycle events (activation `loadState`, focus switch, compact rebuild,
drafts rehydrate), continuation-gating and policy decisions,
accounting/usage-merge, stale-checkpoint detection, and notifications. Each
case names the Part 0 module(s) it exercises and runs over fixture sizes
(1/10/50 goals, 1k/5k/10k events, 10/50 tasks). Rationale: P1 targets the hot
paths, but the next run needs every feature's measured cost to find the next
order-of-magnitude — wall clock hides in unmeasured features. User value: a
per-feature wall-clock baseline for every module, so the next run's
optimisation priorities come from measurement rather than guesses.

**B8. Agent-exclusion harness (no live agents in benchmarking).** Description:
the harness never calls a live model and never spawns an agent session: the
auditor is stubbed (completion-flow cases measure extension-side dispatch,
state changes, and ledger writes, ending at the pre-audit gate); drafting and
continuation are invoked with scripted fake turns; every other case is pure
function calls over fixtures. A "no model, no network" assertion guards each
run (spawn/session calls are forbidden and outbound requests counted as
zero). Rationale: benchmark numbers must be deterministic, fast, and
CI-runnable — live agent calls make runs minutes long, non-deterministic, and
flaky, and agent time is out of scope by definition: we are benchmarking the
extension, not the model. User value: bounded, repeatable benchmarks that
isolate extension wall clock from model inference so the two are never
conflated.

**B9. Baseline table + per-feature wall-clock budgets.** Description: the first
run emits a committed baseline table — per operation, p50/p95/max ms, op
counts, fixture size, storage class — from which per-operation-class budgets
are set (e.g., per-turn extension overhead, dialog open-to-render, widget
render, task-update round-trip, overlay open/to-toggle) as targets for the
next run; the table is re-emitted after changes and diffed. Rationale: "get
wall clock down on the next run" needs a target: budgets turn the baseline
from a report into a spec — any feature over its budget becomes a candidate,
and regressions trip B6. User value: the next implementation run starts with
concrete per-feature reduction targets and a measuring stick to prove each
one.
