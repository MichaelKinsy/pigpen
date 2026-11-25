# Upstream reference: `internal/jobs`

Per-session registry of background work (`api.go`) plus omp v19.1.1 completion delivery, `notify` batch formatting, `launch-completion` service exit notices, harness-tag escaping, 12k char follow-up preview cap, retry-with-backoff delivery, eviction tracking, per-session `lifecycle.OnEnd("jobs", ...)` cleanup, and `wait` helpers (`delivery.go`).

Upstream: https://github.com/can1357/oh-my-pi (MIT, tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/async/job-manager.ts`
- `packages/coding-agent/src/async/job-control.ts`
- `packages/coding-agent/src/session/async-job-delivery.ts`
- `packages/coding-agent/src/session/async-notify.ts`
- `packages/coding-agent/src/session/launch-completion.ts`
- `packages/coding-agent/src/session/harness-tags.ts`
- `packages/coding-agent/src/session/agent-session.ts` (`#formatAsyncResultForFollowUp`, 12k char cap)
- `packages/coding-agent/src/prompts/tools/async-result.md`
- `packages/coding-agent/src/prompts/tools/async-notify.md`
- `packages/coding-agent/src/prompts/session/launch-completion.md`
- `packages/tui/src/render/render-utils.ts` (`formatDuration`)
- `packages/coding-agent/src/job-widgets/{runtime,format,spec,registry}.ts` (deterministic `state: "fallback"` `JobWidgetView` in `widget.go`)

## Delivery mechanism

`EnsureDeliveryListener` installs a single process-wide `OnFinish` listener and `lifecycle.OnEnd("jobs", ...)` handler (safe across repeated `Register` calls). All per-session delivery, binding, claim, watch, consumed, and eviction state lives in ONE package-level table (`deliverySessions`, keyed by `sessionID`). Settled non-service, non-cancelled jobs are delivered into their session using the long-lived `session_start` `sdk.Context` (`BindSession` / `BindSessionIfMissing`, never clobbered by a cancellable tool-call Context) with exponential retry backoff (`500ms` → `30s`, woken immediately on session rebind/unbind) via `sdk.Context.SendMessage("async-result", content, true, sdk.SendMessageOptions{TriggerTurn: &true, DeliverAs: "followUp", Details: ...})`. Results over `12,000` characters are capped to a `4,000`-character preview plus `Full output: local://<N>.log`.

Single-delivery mutual exclusion across all job kinds (`bash`, `agent`, `eval`) is enforced by `Claim(ctx, sessionID, jobID) (release func(), ok bool)`, `Unclaim(sessionID, jobID)`, `Watch(ctx, sessionID, ids) (unwatch func())` (matching omp's `watchJobs`/`unwatchJobs`, `async/job-manager.ts:863-893`), and terminal `MarkConsumed(sessionID, jobID)`: both `Claim` and `Watch` accept a `context.Context` whose cancellation automatically releases ownership; both explicit `release()` and context cancellation re-dispatch any settled, unconsumed, unwatched job through the listener's normal delivery path so an abandoned or failing caller cannot strand a result until session end; `release()` is a no-op after `MarkConsumed`. Successful delivery calls `MarkConsumed`, firing `OnConsumed` listeners so `JobManager` shortens retention to `consumedEviction` (30s). `lifecycle.OnEnd("jobs", ...)` clears the session, unbinds its context, and deletes its `deliverySessions` entry after a 5s grace period.

## Tests ported

- 13 test functions in `delivery_test.go` (race-clean under `-race`) covering `async-result` single/batch formatting and harness-tag escaping, `async-notify` batch merging/capping/escaping and `DeliverAs: "steer"` mid-run delivery, `OnFinish` delivery listener + `WaitAny`, transient-error retry backoff, 12k char preview cap, `lifecycle.End` per-session scoping, evicted-job descriptions, `OnConsumed` notification + post-end per-session sweep, 200-run `-race` `Watch`/`Claim` exactly-once delivery during `wait`, `Claim`/release/`Unclaim` failed-send retry recovery, `Watch`/`Claim` context-cancellation auto-release, and 100-run explicit `Claim` `release()` re-delivery.

## Settings

None directly read; retention/cap policies are enforced by producers (`internal/shell`, `internal/agents`).

## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Step-boundary `aside` yield-queue batching for completions (folding multiple simultaneous completions into one message mid-turn) | `session/async-job-delivery.ts`, `session/agent-session.ts` | PiG SDK `SendMessageOptions.DeliverAs` supports `"steer"`, `"followUp"`, and `"nextTurn"` (no `"aside"` step-boundary queue receipt API); `DeliverNotify` (`async-notify`) uses `DeliverAs: "steer"` so mid-run notify matches arrive before the next LLM request while the job is still running, while settled job completions (`async-result`) and unexpected service exits (`launch-completion`) deliver as `"followUp"` with `TriggerTurn: true` (`FormatAsyncResultBatch` / `DeliverResults` still support multi-job batches when callers pass multiple entries). |
| Structured subagent schema validation blocks in `async-result.md` | `session/async-job-delivery.ts:75-94` | `ResultEntry` supports agent/history URLs and `/tan` request blocks; structured schema output blocks require the agents area's schema result type to be passed through `jobs.Job`. |
| Multimodal image blocks attached to `async-result` | `session/async-job-delivery.ts:191-195` | `sdk.Context.SendMessage` accepts string content and `internal/shell` does not extract inline terminal graphics. |
