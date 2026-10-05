# Technical plan: Tweak status persistence + proposal presentation fixes

## 1. Audit — completion-status persistence across /goal-tweak

Walked the full path draft → confirm → §7.5 merge → apply → disk → reload.
Each finding is verified against the current working tree.

| # | Vector | Location | Verdict |
|---|--------|----------|---------|
| V1 | Merge by id preserves `status`/`evidence`/`completedAt`/`skippedAt`/`skipReason`; structural fields (title, verificationContract, lightweightSubtasks, parentage) authoritative from incoming; subtrees recurse | `extensions/goal-task-tools.ts:151` `mergeTasksWithExisting` | ✅ Correct (unit-tested: goal-task-tools.test.ts:71, goal-task-lifecycle.test.ts:327) |
| V2 | Tweak apply: `mergedTaskList = proposed && goal.taskList ? {...proposed, tasks: merge(...)} : proposed`; no-proposal ⇒ `goal.taskList` retained; `currentTaskId` kept only while pending | `extensions/goal-drafting.ts:395-414` | ✅ Correct |
| V3 | Merge base is the disk-fresh goal (`refreshFromDisk: true` + `reconcile: false`); `mergeFocusedGoalWithDisk` makes disk authoritative for taskList → statuses written by `complete_task` are the base | `extensions/goal-service.ts:416`, `extensions/goal-pool.ts` | ✅ Correct |
| V4 | Apply pipeline: mutate on clone, revision increment, optimistic per-goal lock, atomic write; taskList carried through spread | `extensions/goal-service.ts:333` | ✅ Correct |
| V5 | Serialization: full record (incl. taskList with all progress fields) in the JSON meta block; markdown `## Tasks` is a summary (no timestamps) | `extensions/storage/goal-files.ts:323` `serializeGoalFile` | ✅ Correct (meta is authoritative) |
| V6 | Parse/normalize: `normalizeTaskItem` reads every progress field; `currentTaskId` accepted only when referencing a pending task (cleared otherwise) | `extensions/storage/goal-files.ts:415`, `extensions/goal-record.ts:159,206,275` | ✅ Correct (clear-on-non-pending is by design) |
| V7 | No-task-list tweak retains the current list unchanged | `extensions/goal-drafting.ts:412-414` | ✅ Correct (existing test: "a tweak without explicit tasks previews the retained current list exactly once") |

### Identified gaps

- **G1 (user-reported, severity high)** — Auditor status line invisible in the
  proposal confirmation dialog on bounded terminals. `findProposalPresentationSegments`
  matches the tasks header but its task scan (`/^\s*\[[ x~]\]/`) fails on
  ANSI-styled lines (every rendered line carries `theme.fg(...)` escapes), so
  the "tasks section" collapses to its header; the auditor line is rendered
  between context and options, outside the protected head/tasks/tail regions,
  so the bounded fit drops it. Reproduced: rows=24, baseFrame=19 → 10-line
  dialog shows tasks header + options + footer hint ("• a toggle auditor") but
  no task lines and no auditor status line. Regression from `7bc07ee`
  (pi 0.84 fullscreen fallback bound `rows - 4`; previously unbounded ⇒ line
  visible) + `b9cb6a6` (fit keeps head/tasks/tail only). Mock-theme tests could
  not catch it: `createMockTheme` emits no ANSI.
- **G2 (user-reported, severity high)** — Phantom task set in scrollback. The
  `propose_goal_draft` renderCall derives tasks from the objective whenever no
  explicit `tasks` arg is present, without knowing the draft mode; for a tweak
  the apply path retains the current list, so the scrollback shows a
  derived-from-objective list that is never persisted while the dialog shows
  "Current task list (retained unchanged)". Two task sets for one proposal.
- **G3 (minor)** — Derivation-source mismatch: renderCall/proposalText derive
  from the raw objective; the apply path derives from `extracted.objective`
  (Verification contract line stripped) → shown derived set can diverge from
  the persisted set in edge cases.
- **G4 (hygiene, same root cause as G1)** — Bounded dialog renders an empty
  `┌─ TASKS ─┐` box for tweaks (task lines dropped by the ANSI-unaware scan).

### By design (not bugs)

- Non-matching ids in a tweak start pending — id stability is the merge
  contract; renaming/re-id'ing a task resets its progress.
- `currentTaskId` clears when its task completes, is skipped, or is removed.
- Structural fields (title, verification contract, parentage, blockCompletion,
  proposedAt) are authoritative from the incoming proposal; omission clears.
- Objective / verification contract / skipAuditor are intentionally replaced by
  the confirmed tweak; goal status resumes from paused/blocked on confirm.

## 2. Fixes

### F1 (G1/G4) — ANSI-aware proposal segment scan + auditor-line protection

- `findProposalPresentationSegments` (goal-questionnaire.ts): strip ANSI
  (`\x1b\[[0-9;]*m`) before the task-line scan so styled `[ ]`/`[x]`/`[~]`
  lines extend `tasksEnd`; consume the box-drawn `└──┘` bottom border after the
  last task line so the `┌─ TASKS ─┐` box renders complete.
- Protect the auditor line: scan backward from the options start for the
  "press 'a' to toggle" line and extend `tailStart` back to it, so
  `fitProposalPresentation` keeps head + tasks + auditor line + options/footer/
  border within the bound. Churn-guard invariant (frame ≤ terminal height)
  preserved.

### F2 (G2/G3) — Single task set in the scrollback presentation

- renderCall becomes draft-mode-aware via the `core` closure (`activeDraft(core)`):
  - tweak + no explicit tasks → show the current goal's list as "Current task
    list (retained unchanged):" (mirrors `proposalText`), never derived tasks.
  - new draft + no explicit tasks → derive from `extractVerificationContract(
    objective).objective` (same input as the apply path) so shown == persisted.
  - explicit tasks → "Tasks proposed for confirmation:" (unchanged).
- `proposalText` derives from the extracted objective for the same reason.

## 3. Tests

- Unit: `findProposalPresentationSegments` with ANSI-styled lines (plain and
  box-drawn TASKS sections); `fitProposalPresentation` keeps auditor line +
  task lines within the bound; render-level regression with an ANSI-emitting
  theme proves the bounded proposal dialog shows task lines and the auditor
  status line; `a` toggle feedback test.
- Flow: renderCall for a tweak without explicit tasks shows the retained list
  and no derived block; renderCall for a new draft derives from the extracted
  objective; e2e tweak round-trip preserves completed task status/evidence/
  timestamps through disk reload; no-task-list tweak retains list unchanged;
  subtask status survives; currentTaskId per contract.
