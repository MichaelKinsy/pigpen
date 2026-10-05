# Milestones: Simplify goal drafting while hardening runtime execution and audit

Free-form implementation log. Record meaningful phase changes, successful milestones, failed attempts, setbacks, fixes, validation notes, and decisions. Use third-level headings with timestamps down to seconds, for example `### 2026-05-13 14:16:36 - Short milestone title`. No strict schema is required.

### 2026-05-13 00:00:00 - Spec established

Created the initial PRODUCT.md and TECH.md for simplifying `/goal-set` and `/goal-sisyphus` drafting while preserving strict active-goal execution and independent completion audit behavior. Research found that drafting validators were already softened in `extensions/goal-draft.ts`, but `extensions/goal.ts` still carried heavier session state through `draftingFor`, `draftId`, `questionsAsked`, `draftingNudgesByDraftId`, hidden prompt reinjection, and drafting-specific turn hooks.

### 2026-05-13 16:54:11 - Milestone

Implemented the lightweight goal confirmation refactor. `extensions/goal.ts` now uses a thin `confirmationIntent` instead of `draftId`/`questionsAsked` drafting state, starts `/goal-set` and `/goal-sisyphus` through a normal confirmation prompt, removes drafting nudges and prompt reinjection, and keeps strict execution/audit gates intact. `extensions/goal-draft.ts` now validates against confirmation intent, ignores deprecated `draftId` for compatibility, and emits shorter lightweight confirmation guidance. Updated README, architecture/design docs, PRODUCT/TECH decisions, and goal-draft tests. Validation passed: `npm run check`, `npm test`, `npm pack --dry-run`, and `git diff --check`.

### 2026-05-13 17:41:36 - Milestone

Updated the command model after user direction: `/goals` and `/sisyphus` now start discussion/research/grilling-based confirmation flows, while `/goals-set` and `/sisyphus-set` directly create and start goals from the supplied objective. Removed registration of the redundant `/goal-set`, `/goal-sisyphus`, and `/goal-replace` creation aliases; refreshed prompt/validator/docs wording for the new command surface. Validation passed with `npm run check` and `npm test` (75 tests).

### 2026-05-14 09:50:00 - Live auditor progress widget

Exposed auditor progress via the above-editor widget during goal completion audit. Changes:

- `extensions/goal-auditor.ts`: Added `AuditorProgress` interface and `AuditorProgressCallback` type. `runGoalCompletionAuditor()` now accepts an optional `onProgress` callback. The session subscribe handler listens for `tool_execution_start`, `tool_execution_end`, and `message_update` events in addition to the existing `message_end` handler, and calls `onProgress` with current tool, tool args, and streaming text output.

- `extensions/widgets/goal-widget.ts`: Added `renderAuditorWidgetLines()` function that renders an animated auditor widget showing spinner, current tool with args, elapsed duration, and recent output lines. Extended `GoalWidgetComponent` to accept a `getAuditorProgress` getter and pass `auditorProgress` through `renderGoalWidgetLines` options.

- `extensions/goal.ts`: Added `auditProgress` state variable and `auditAnimationTimer` for 80ms spinner animation. The `update_goal` handler sets up auditor progress before launching `runGoalCompletionAuditor`, wires the `onProgress` callback to update the widget in real time, shows the final output lines after the audit completes, and clears the progress display after the verdict.

- `tests/goal-widget.test.ts`: Added 4 new tests covering auditor progress display with current tool, done phase, empty output handling, and progress overriding normal goal display.

Validation passed: `npm run check`, `npm test` (79 tests).

### 2026-05-14 10:20:00 - Audit lifecycle improvements

Added auditor disable/skip/cancel support and lag fix. Changes:

- `extensions/goal-auditor.ts`: Added `disabled` boolean to `GoalAuditorConfig`. Updated `parseGoalAuditorConfig` and `saveGoalAuditorFileConfig` to read/write the field. Only persisted in file config (not env).

- `extensions/goal-ledger.ts`: Added `audit_skipped` event type to `GoalLedgerEvent` union with `reason: "disabled" | "user_aborted"`. Added validation, sanitization, and reconstruction handling.

- `extensions/goal.ts`: 
  - Added `disabled` toggle to `/goal-settings` auditor UI
  - Added `confirmBypassAuditor` optional param to `update_goal` tool
  - When auditor disabled: handler asks agent to confirm via `goal_question` on first call, archives immediately on confirmed second call with `audit_skipped` ledger event
  - Added `auditAbortController` (dedicated `AbortController`) for independent audit cancellation
  - Added `abortAudit()` function that aborts the controller, clears progress, sends `GOAL_AUDIT_ENTRY` with "skipped" phase, appends `audit_skipped` ledger event, and notifies user
  - Extended Escape handler: when audit is running, Escape aborts audit instead of pausing goal
  - Lag fix: `pi.sendMessage` for "started" phase now awaited with `{ triggerTurn: true }`
  - Widget progress set before `runGoalCompletionAuditor()` call (verified widget is first visible signal)
  - Added `"skipped"` phase to `GoalAuditEventDetails` and `renderGoalAuditEvent`

- `extensions/widgets/goal-widget.ts`: Added "Esc to skip — abort the audit" hint line at the bottom of active auditor widget

- `tests/goal-auditor.test.ts`: Added 2 new tests for `disabled` field parsing, save/load round-trip, and env-file config merging. Added `loadGoalAuditorConfig` import.

Validation passed: `npm run check`, `npm test` (86 tests).

### 2026-05-14 10:45:00 - Added unit test coverage for audit lifecycle features

Added the missing tests for success criterion 7:

- `tests/goal-ledger.test.ts`: Added 3 tests — `audit_skipped` with disabled reason and full metadata, `audit_skipped` with user_aborted reason and minimal metadata, and `reconstructGoalLedger` handling of `audit_skipped` without changing goal status or setting auditor result.
- `tests/goal-widget.test.ts`: Added 2 tests — `Esc to skip` hint visible when audit active, hint omitted when audit complete.
- Also fixed a prior constraint violation: `audit_skipped` ledger event type now includes `provider?`, `model?`, `thinkingLevel?` fields matching `audit_started`, and both `appendGoalEvent` calls (disabled bypass and `abortAudit`) populate them from the loaded auditor config.

### 2026-05-14 12:14:00 - Fix audit-cancellation loop (Esc-skip completes goal)

When the user pressed Escape during a running completion audit, `abortAudit()` aborted the controller and appended an `audit_skipped` ledger event, but the `update_goal` tool's abort handler returned "Audit was aborted by user. Goal remains open." without setting `turnStoppedFor` or `terminate: true`. The agent interpreted "goal remains open" as a signal to retry completion, creating an infinite loop: agent calls `update_goal` → user Escapes → agent retries.

Fix: changed the abort handler to treat a user-aborted audit as a bypass signal (mirroring the disabled-auditor bypass pattern). When `auditor.error === "Auditor aborted."`, the handler now:
- Calls `accountProgress(ctx)`, `stopActiveGoal("complete", "agent", ctx)` — same steps as disabled bypass
- Sets `turnStoppedFor = completedGoal.id` to block subsequent in-turn tool calls
- Cleans up pool/focus state, appends `goal_completed` ledger event
- Returns `buildCompletionReport(...)` with `terminate: true`

Updated `abortAudit()` notification text from "Audit aborted by user. Goal remains open." to "Audit skipped by user." to avoid contradicting the pending completion.

Validation: `npm run check` clean, `npm test` 89/89 pass.
