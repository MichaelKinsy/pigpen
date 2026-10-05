# TECH — Bounded continuation checkpoints and existing-session recovery

## v2 checkpoint data model

`extensions/goal-record.ts`:

```ts
export interface GoalCheckpointDetailsV2 {
  version: 2;
  kind: "checkpoint";
  goalId: string;
  status: "active";
  revision: number;
  checkpointSeq: number;
  timestamp: number;
}

export type GoalEventEntryDetails =
  | GoalCheckpointDetailsV2
  | LegacyGoalEventDetails   // pre-v2 shape, read-only
  | GoalStaleDetails;        // historical "stale" rewrites
```

v2 details never contain objective text, task text, verification contracts,
budget strings, or policy text. `GoalEventDetails` remains exported as the
legacy normalized shape used by the renderer.

## Checkpoint trigger prompt

`extensions/prompts/goal-prompts.ts`:

- `CHECKPOINT_TRIGGER_MAX_CHARS = 160`.
- `checkpointTriggerPrompt(goalId)` renders a self-closing marker:
  `<pi_goal_continuation goal_id="..." kind="checkpoint" v="2"/>` with
  XML-attribute escaping and an assertion on the length bound.
- `continuationPrompt()` becomes a deprecated compatibility wrapper that
  returns `checkpointTriggerPrompt(goal.id)` for one minor release; the full
  continuation builder is removed.
- `extractGoalIdFromInjectedMessage` keeps parsing both v2 self-closing
  markers and all legacy forms (`<pi_goal_continuation ...>`, `[GOAL CHECKPOINT
  goalId=...]`, `[GOAL CONTINUATION ...]`, `[GOAL STALE ...]`).

## Runtime scheduling change

`extensions/goal-runtime.ts` `sendQueuedContinuation`:

- Keeps scheduling, idle polling (50 ms retry), dedup, and actionable checks.
- Removes `loadGoalSettings()` and full prompt construction from the
  scheduling path entirely.
- Increments a per-runtime `checkpointSeq` counter and sends
  `checkpointTriggerPrompt(goal.id)` with v2 details via the existing
  `sendFollowUp` hook (`deliverAs: "followUp"`, `triggerTurn: true`
  unchanged — Pi needs the delivered message to trigger the turn).

## Provider-context compaction

`extensions/goal-events.ts` exports a pure helper:

```ts
compactGoalCheckpointContext(messages, currentGoal): AgentMessage[]
```

- Finds the last message carrying any goal-event id
  (`goalEventMessageId`). If none, returns the input array unchanged
  (reference equality → handler returns `undefined`).
- Drops every earlier goal-event message from provider context entirely;
  their authoritative state is reconstructed by `before_agent_start` from
  goal storage, so each historical checkpoint was redundant payload.
- Rewrites the single remaining (latest) checkpoint to the tiny v2 trigger
  content with `display: false` and details classifying it `checkpoint`
  (matches focused active goal) or `stale`. Keeping one user-role turn-start
  marker avoids provider edge cases where the request would otherwise end on
  an assistant/tool result.
- Audit events (`pi-goal-audit-event`), user/assistant/tool messages are
  untouched.

The `context` handler returns `{ messages }` only when the helper allocated a
new array. Stale-checkpoint safety is unchanged: `before_agent_start` still
parses the raw delivered prompt via `extractGoalIdFromInjectedMessage` and
aborts turns for non-actionable checkpoints; the `tool_call` guard still blocks
work tools for stale checkpoints.

## Single authoritative full prompt

`before_agent_start` stays the only place the complete goal block is injected
(system prompt). A composed-request test asserts the serialized request
contains the objective exactly once, the verification contract exactly once,
exactly one full `[PI GOAL ACTIVE` block, and at most one checkpoint marker.

## Session health diagnostics

`extensions/goal-session-health.ts`:

```ts
inspectCheckpointHealth(entries): {
  total, legacyFull, v2Minimal, totalContentChars,
  recoverableChars, largestCheckpointChars
}
```

Counts only `pi-goal-event` custom messages; classifies v2 by bounded
self-closing marker shape + `details.version === 2`; everything larger is
legacy/recoverable. Surfaced read-only in `/goal-recovery` and
`/goal-status health`.

## Offline recovery CLI

`scripts/recover-session-checkpoints.mjs`, published as bin
`pi-goal-x-recover`.

Contract:

    pi-goal-x-recover --session <path> [--dry-run] [--apply --confirm-pi-closed]

Transformation:

- Refuses symlinks; requires a regular file.
- Per line: blank lines pass through; malformed JSON passes through untouched
  (never discard unrelated data); non-checkpoint entries pass through
  byte-identical; legacy checkpoint entries are rewritten to
  `checkpointTriggerPrompt(goalId)` content with v2 details
  (objective stripped).
- Graph invariants asserted before writing: identical entry count, every
  entry `id` unchanged, every `parentId` unchanged, header line unchanged,
  all parent ids resolvable, every non-goal line byte-identical.
- Apply path: timestamped `.backup-<iso>` copy (mode preserved) → same-dir
  temp file (`wx`, fsync, close) → atomic rename → directory fsync (best
  effort) → post-write validation by re-reading. Temp file removed on failure.
- Idempotent: a second run reports zero changes.

Acceptance criteria (tested):

- dry-run writes nothing; apply creates backup before replacement;
- ids/parent links/header preserved; recovery idempotent; malformed lines
  preserved; symlink refused; `--apply` without `--confirm-pi-closed` fails;
  POSIX-style paths accepted.

## Complexity targets

For N unchanged continuation turns:

    Before: persisted checkpoint = O(N × fullPrompt); provider history O(N);
            compaction input O(N × fullPrompt)
    After:  persisted checkpoint = O(N × ≤160 B); provider-visible history ≤1;
            future compaction input O(N × ≤160 B)

## Benchmarks

`experiments/bench/b10-checkpoint-growth.mjs` (rows `B10.checkpoint.*`)
gates: v2 marker ≤160 chars; zero objective occurrences in persisted
checkpoints; ≤1 provider-visible checkpoint at 1 000 turns; persisted
checkpoint chars at 1 000 turns ≤160 000; recovery of an 850-entry legacy
session reduces checkpoint bytes to ≤5 % of original; exactly one full goal
block in the composed request. Wired into `bench:gate:naf`.

## Rollback

Do not restore full checkpoint persistence under any circumstances. If a
downstream incompatibility appears, revert only context-filtering or UI
changes and keep the v2 markers plus the recovery CLI.
