# Upstream: `internal/checkpoint`

Ported from omp **`v19.1.1`** (`31ffee513f`).

## Upstream files followed

| Upstream path | Role |
|---|---|
| `packages/coding-agent/src/tools/checkpoint.ts` | `CheckpointTool`, `RewindTool`, schemas, error messages, `Details` |
| `packages/coding-agent/src/session/checkpoint-entries.ts` | `semanticToolResult`, `completedRewindFromEntry`, `reportFromRewindReportContent` |
| `packages/coding-agent/src/session/agent-session.ts` | `#checkpointActiveReminderFor`, `#applyRewind`, `#enforceRewindBeforeYield`, `#rehydrateCheckpointRewindState` |
| `packages/coding-agent/src/session/messages.ts` | `CHECKPOINT_ACTIVE_REMINDER_TYPE` (`checkpoint-active-reminder`), `REWIND_REPORT_MESSAGE_TYPE` (`rewind-report`) |
| `packages/coding-agent/src/prompts/tools/checkpoint.md` | Embedded `checkpoint.md` |
| `packages/coding-agent/src/prompts/tools/rewind.md` | Embedded `rewind.md` |
| `packages/coding-agent/src/prompts/system/checkpoint-active-notice.md` | Embedded `checkpoint-active-notice.md` |
| `packages/coding-agent/src/prompts/system/rewind-report.md` | Embedded `rewind-report.md` |
| `packages/coding-agent/src/tools/settings.ts` | `checkpoint.enabled` setting key and default |
| `packages/coding-agent/test/agent-session-checkpoint-rewind-branch.test.ts` | Lifecycle, branch-replay, multi-cycle, and bash tool-command tests |

## Mechanism

In omp `v19.1.1`, `checkpoint` and `rewind` are **pure context-checkpointing tools** — despite the legacy `summary` string in `checkpoint.ts:57` mentioning "git-based", omp never executes any git command during `checkpoint` or `rewind` and never modifies the working tree.

- `checkpoint(goal)` activates checkpoint state for the session and steers a hidden `checkpoint-active-reminder` custom message (`{goal, startedAt}`). At the checkpoint turn's `turn_end`, the persisted checkpoint `toolResult` entry ID is backfilled as the boundary so both the `checkpoint` assistant call and its real `toolResult` remain in model context after rewind.
- `rewind(report)` validates the active checkpoint and captures the report. On completed (non-aborted) `turn_end`, it locates the boundary on the **active branch** (`ctx.GetBranch()`), emits `context_edit` omissions (`replacement: null`) only for editable entries (`custom_message` and `message` with role `user`/`assistant`/`toolResult`) strictly after that boundary, and appends the `rewind-report` `custom_message` draft. If the boundary is not on the active branch, stale state is dropped without omitting from the root. Rewind completion is verified on the branch before committing so a rejected draft restores active state.
- `agent_before_settle` enforces omp's rule that an active checkpoint must be rewound before yielding, injecting the `<system-warning>` reminder and returning `continue: true`.
- `session_start`, `session_tree`, and `session_compact` rebuild state strictly from the **active branch** (including `toolcmd.ToolCommandDispatches` from `bash` tool results). Per-session state lives in one package-level manager keyed by session ID and cleaned up via `lifecycle.OnEnd("checkpoint", ...)`.

## Tool `loadMode` at tag
- `checkpoint`: `loadMode = "discoverable"` (default off via `checkpoint.enabled: false`; registered with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and `toolcmd.Mountable("checkpoint", ...)`).
- `rewind`: `loadMode = "discoverable"` (default off via `checkpoint.enabled: false`; registered with `Exposure: sdk.ToolExposureCodemode`, `DefaultActive: false`, and `toolcmd.Mountable("rewind", ...)`).

## Settings

| Key | Default | Honoured |
|---|---|---|
| `checkpoint.enabled` | `false` | Yes, via `toolcmd.Mountable` predicate (`Enabled(ctx, "checkpoint.enabled", false)`); `internal/toolcmd` owns mounting and top-level activation when bash transport is off |

## Tests ported

10 Go tests covering active-history rebuild keeping the checkpoint toolResult, skipping non-editable `bashExecution` entries and aborted turns, `/tree` branch navigation without resurrecting abandoned branches or wiping from root, rejected boundary-draft recovery, multi-cycle rewinds + new session reset, `toolcmd` mounted calls and `bash` toolCommand dispatch replay, mounted two-result checkpoint turns with and without `details.toolCommands` plus aborted checkpoint turns, no per-area `session_shutdown` hook, default-disabled gating, completed-rewind resume replay, and multi-session `lifecycle.End` + double `Register`.

## Not ported

| Feature | Reason |
|---|---|
| `sessionManager.branchWithSummary` tree cut on rewind | PiG SDK exposes only `ReadonlySessionManager` and restricts `NavigateTree` to command contexts (refused inside event/tool handlers). Replaced with `turn_end` `context_edit` omissions (`replacement: null`) + `rewind-report` `custom_message`. Consequence: exploration entries remain on the active branch and stay visible in the transcript (only the model's projection drops them), and if auto-compaction ran during exploration its compaction entry remains on the branch instead of returning to the uncompacted pre-checkpoint path. Nothing is lost from the session file in either design. |
| Sibling tool-call reparenting in the same assistant batch as `rewind` | PiG's `turn_end` `context_edit` drafts operate at entry granularity rather than rewriting individual assistant content blocks, so sibling calls in the same assistant turn are omitted alongside the rewind call. |
| Developer-role settle reminder (`role: "developer"`) | PiG `agent_before_settle` boundary drafts accept `custom_message` rather than arbitrary `developer`-role messages; the identical `<system-warning>` text is sent as a hidden `custom_message` (`checkpoint-rewind-reminder`). |
| Subagent `tools:` frontmatter auto-inclusion of sister tool | Owned by the agents/subagent lane. |
