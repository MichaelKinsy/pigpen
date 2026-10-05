# Plan: To-Do List System for pi-goal

## Context

Goals in pi-goal currently have no structured sub-task tracking — the only decomposition is Sisyphus mode's text-formatted numbered plan, which is invisible to the runtime. For multi-step objectives, this means the agent and user have no shared machine-readable view of progress. A task list fills this gap: it's optional, surfaced in prompts and the widget, and auditable at completion.

The design follows pi-goal's core philosophy: user confirms proposals, schema gates enforce rules, disk-backed state survives context churn, and the auditor uses tasks as *evidence* — not as the arbiter of completion.

---

## Data Model — `extensions/goal-record.ts`

Add three new types and extend `GoalRecord`:

```typescript
export type TaskStatus = "pending" | "complete" | "skipped";

export interface GoalTask {
  id: string;           // short stable slug e.g. "task-1"
  title: string;
  status: TaskStatus;
  completedAt?: string; // ISO timestamp
  skippedAt?: string;
  evidence?: string;    // agent-provided proof note (≤200 chars)
  skipReason?: string;
}

export interface GoalTaskList {
  tasks: GoalTask[];
  blockCompletion: boolean; // default false; if true, warns when pending tasks remain
  proposedAt: string;
}

// In GoalRecord, add:
taskList?: GoalTaskList;
```

Update `normalizeGoalRecord` to deserialize `taskList` via a new `normalizeTaskList(value: unknown): GoalTaskList | undefined`.

Update `cloneGoal` to deep-clone the tasks array:
```typescript
export function cloneGoal(goal: GoalRecord): GoalRecord {
  return {
    ...goal,
    usage: { ...goal.usage },
    taskList: goal.taskList
      ? { ...goal.taskList, tasks: goal.taskList.tasks.map(t => ({ ...t })) }
      : undefined,
  };
}
```

---

## Tool Names — `extensions/goal-tool-names.ts`

```typescript
export const PROPOSE_TASK_LIST_TOOL_NAME = "propose_task_list";
export const COMPLETE_TASK_TOOL_NAME = "complete_task";
export const SKIP_TASK_TOOL_NAME = "skip_task";
```

Extend `ACTIVE_GOAL_TOOL_NAMES`:
```typescript
export const ACTIVE_GOAL_TOOL_NAMES = [
  "get_goal", "complete_goal", "pause_goal", ABORT_GOAL_TOOL_NAME,
  PROPOSE_TWEAK_TOOL_NAME,
  PROPOSE_TASK_LIST_TOOL_NAME, COMPLETE_TASK_TOOL_NAME, SKIP_TASK_TOOL_NAME,
] as const;
```

Extend `PAUSED_GOAL_TOOL_NAMES` (proposal only, no completion/skip since goal isn't running):
```typescript
export const PAUSED_GOAL_TOOL_NAMES = [
  "get_goal", "complete_goal", ABORT_GOAL_TOOL_NAME,
  PROPOSE_TWEAK_TOOL_NAME, PROPOSE_TASK_LIST_TOOL_NAME,
] as const;
```

Add `COMPLETE_TASK_TOOL_NAME` and `SKIP_TASK_TOOL_NAME` to `GOAL_PROGRESS_TOOL_NAMES` (they count as real work and sustain autoContinue turns). Keep `PROPOSE_TASK_LIST_TOOL_NAME` out of PROGRESS — it's a turn-stopping proposal, not a work action.

Add all three to `GOAL_WORK_TOOL_NAMES`.

---

## Policy Helpers — `extensions/goal-policy.ts`

New pure functions (no side effects, easily tested):

```typescript
export function buildTaskSummary(taskList: GoalTaskList): string
// "3/5 tasks complete (1 skipped)" or "No tasks" if empty

export function taskCompletionBlockWarning(taskList: GoalTaskList): string | null
// Returns warning string when blockCompletion=true and pending tasks exist; else null

export function validateTaskCompletion(args: { goal: GoalPolicyRecordLike | null; taskId: string }): PolicyValidation
// Rejects: no goal, unknown taskId, task already complete/skipped

export function validateTaskSkip(args: { goal: GoalPolicyRecordLike | null; taskId: string; reason: string }): PolicyValidation
// Rejects: no goal, unknown taskId, task already complete/skipped, empty reason

export function validateTaskListProposal(args: { goal: GoalPolicyRecordLike | null; tasks: { id: string; title: string }[] }): PolicyValidation
// Rejects: no goal, duplicate ids, >50 tasks, empty titles
```

Add `taskList?: GoalTaskList` to `GoalPolicyRecordLike`.

Extend `buildCompletionReport` to accept an optional `taskSummary?: string | null` and include it between the audit section and the footer line.

---

## Ledger Events — `extensions/goal-ledger.ts`

Add three new event types to `GoalLedgerEvent`:

```typescript
| { type: "task_list_set"; goalId: string; taskCount: number; blockCompletion: boolean; at: string }
| { type: "task_complete"; goalId: string; taskId: string; evidence?: string; at: string }
| { type: "task_skipped"; goalId: string; taskId: string; reason: string; at: string }
```

Update `isValidLedgerEvent` and `sanitizeEvent` for all three. Include task events in `buildGoalCompactSummary`.

---

## Serialization — `extensions/storage/goal-files.ts`

`serializeGoalFile` already JSON-serializes the entire `GoalRecord` (including `taskList`) in the file header — so roundtrip is automatic. Add a human-readable `## Tasks` section to the markdown body when `taskList` is present:

```
## Tasks

<!-- blockCompletion: false -->
- [x] task-1: Write validation layer — evidence: all 47 schema tests pass
- [ ] task-2: Add migration path
- [~] task-3: Update README — skipped: superseded by inline docs
```

Uses `[x]` complete, `[ ]` pending, `[~]` skipped. The JSON header is authoritative; this section is read-only for humans.

No changes needed to `parseGoalFile` — `normalizeGoalRecord` already handles the new `taskList` field from JSON.

---

## Three New Tools — `extensions/goal.ts`

### `propose_task_list`
Mirrors `propose_goal_tweak` pattern: shows confirmation dialog, stops the turn.

- Parameters: `tasks: Array<{id: string, title: string}>`, `blockCompletion?: boolean`, `changeSummary?: string`
- Validates via `validateTaskListProposal`
- Existing tasks with matching IDs preserve their `status`/`evidence`/timestamps; new IDs start as `pending`; removed IDs are gone
- Shows full proposed task list in the confirmation dialog
- On confirm: writes to disk, appends `task_list_set` ledger event, sets `turnStoppedFor`

### `complete_task`
Does NOT stop the turn. Agent marks a task done and continues.

- Parameters: `taskId: string`, `evidence?: string`
- Validates via `validateTaskCompletion`
- Sets `status="complete"`, `completedAt=nowIso()`, `evidence` (trimmed, ≤200 chars)
- Writes to disk, appends `task_complete` ledger event
- Returns task summary string (e.g. "task-2 complete. 3/5 tasks done.")

### `skip_task`
Does NOT stop the turn.

- Parameters: `taskId: string`, `reason: string`
- Validates via `validateTaskSkip`
- Sets `status="skipped"`, `skippedAt=nowIso()`, `skipReason`
- Writes to disk, appends `task_skipped` ledger event
- Returns task summary string

### Extend `complete_goal`
Before proceeding, call `taskCompletionBlockWarning(goal.taskList)`. If a warning is returned, surface it to the agent as a soft guard (not an error). Pass `buildTaskSummary(goal.taskList)` to `buildCompletionReport`.

### Extend `detailedSummary(goal)` 
Used by `get_goal` and auditor: include pending task count and next pending task ID/title when `taskList` is present.

---

## Prompt Injection — `extensions/prompts/goal-prompts.ts`

Add `taskListBlock(goal: GoalRecord): string` helper. When `goal.taskList` is present:

- Renders a `[TASK LIST — N/M tasks complete]` block
- Shows each task with `[x]`/`[ ]`/`[~]` marker, id, title, and evidence/skipReason
- When `blockCompletion=true` and pending tasks exist, adds: `TASK GATE: do not call complete_goal while tasks remain in [ ] pending state`
- Hints the next pending task's ID for quick action

Inject `taskListBlock(goal)` into both `goalPrompt(goal)` and `continuationPrompt(goal)` after the objective block.

---

## Widget — `extensions/widgets/goal-widget.ts`

Extend `GoalWidgetRecord`:
```typescript
taskList?: { tasks: Array<{ status: TaskStatus }>; blockCompletion: boolean } | null;
```

In `headingMeta`, add a `"3/5 tasks"` bit when `taskList` is present:
```
● Goal running · auto · 14m · 24K · 3/5 tasks
```

In the widget body (between objective and file path), add a compact task line:
- If there are pending tasks: `◻ task-3: Add migration path (next)`
- If all tasks done/skipped: `✓ All tasks complete`
- If no taskList: nothing added

Use existing `branchLine` helper. Show only the single next pending task to keep the widget compact.

---

## Auditor Integration — `extensions/goal-auditor.ts`

Update `buildGoalAuditorPrompt` to accept `taskList?: GoalTaskList | null` and include the task summary in `<goal_details>` when present. The auditor sees pending/completed/skipped tasks as part of its semantic verification evidence.

---

## When To Use Tasks (Agent Guidance)

Add a note to `goalDraftingPrompt` and `activeGoalPrompt`:
> After a goal is confirmed, you may call `propose_task_list` on the first continuation turn if the objective naturally decomposes into trackable milestones. Do not add a task list for simple, single-step goals.

---

## Migration

No version bump needed. `taskList` is optional — existing goals without it are unaffected. `normalizeGoalRecord` returns `taskList: undefined` when absent. All prompt/widget code guards on presence.

---

## Files to Modify

1. `extensions/goal-record.ts` — add types, update `GoalRecord`, `normalizeGoalRecord`, `cloneGoal`
2. `extensions/goal-tool-names.ts` — add constants, update allowlists
3. `extensions/goal-policy.ts` — add validation fns, `buildTaskSummary`, `taskCompletionBlockWarning`
4. `extensions/goal-ledger.ts` — add three event types, update validator/sanitizer
5. `extensions/storage/goal-files.ts` — extend `serializeGoalFile` with `## Tasks` section
6. `extensions/prompts/goal-prompts.ts` — add `taskListBlock`, inject into prompts
7. `extensions/widgets/goal-widget.ts` — extend `GoalWidgetRecord`, update `headingMeta` and body
8. `extensions/goal-auditor.ts` — pass task state to auditor prompt
9. `extensions/goal.ts` — register three new tools, extend `complete_goal` and `detailedSummary`

---

## Tests to Add

**`tests/goal-record.test.ts`**: `normalizeGoalRecord` with taskList present/absent/malformed; `cloneGoal` deep-clones tasks array.

**`tests/goal-files.test.ts`**: `serializeGoalFile` includes/omits `## Tasks`; `parseGoalFile` round-trips `taskList`.

**New `tests/goal-tasks.test.ts`**: All policy helpers (`buildTaskSummary`, `taskCompletionBlockWarning`, all `validate*` fns) with edge cases.

**`tests/goal-prompts.test.ts`**: `taskListBlock` renders correctly; TASK GATE appears when blockCompletion+pending; absent when no taskList.

**`tests/goal-widget.test.ts`**: Task count in heading; next pending task in body; "all complete" line.

**`tests/goal-ledger.test.ts`**: Three new event types roundtrip correctly.

---

## Verification

1. `npm test` — all existing tests pass; new tests for task logic pass
2. Create a goal, then call `propose_task_list` on the first turn — confirm dialog shows task list, goal file includes `## Tasks` section
3. Call `complete_task` with evidence — turn does not stop, task shows `[x]` in file and prompt
4. Call `skip_task` — turn does not stop, task shows `[~]`
5. With `blockCompletion: true` and pending tasks, call `complete_goal` — warning surfaces to agent
6. On completion, auditor prompt includes task summary; completion report shows "Task summary: 5/5 tasks complete"
7. Widget heading shows `3/5 tasks` bit; body shows next pending task
8. Goals without a task list work exactly as before
