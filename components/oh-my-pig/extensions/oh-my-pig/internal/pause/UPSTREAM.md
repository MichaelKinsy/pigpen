# Upstream reference: `internal/pause`

Port of omp's process-wide pause gate (`AgentPauseGate`) and `/pause` slash command (https://github.com/can1357/oh-my-pi, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/agent/src/pause.ts:25-107` (`AgentPauseGate`, `agentPauseGate` -> `Gate`, `DefaultGate`)
- `packages/coding-agent/src/slash-commands/builtin-control.ts:24-40,112-120` (`/pause` slash command, `pauseWorkStatus` -> `WorkStatus` / `WorkSummary`)
- `packages/tui/src/overlays/pause-screen.ts:28-31,412-440` (`PauseWorkStatus`, interactive resume lifecycle & `Resumed after <dur> — agents are running again.`)

## Architecture & hooks

- **Process-wide `Gate` (`pause.go`)**:
  - `DefaultGate` mirrors `agentPauseGate`: `Pause() bool`, `Resume() (time.Duration, bool)`, `IsPaused() bool`, `PausedSince() (time.Time, bool)`, `Status() GateStatus`, `WaitUntilResumed(ctx context.Context) error`, `OnChange(fn func(paused bool)) func()`.
  - `WaitUntilResumed` loops while paused and selects on the active wait channel vs `ctx.Done()`, so cancelling one waiter releases only that waiter while keeping the gate engaged for others, and a pause re-engaged right after resume re-parks waking waiters.
  - `OnChange` callbacks run outside the mutex with panic recovery so UI subscribers cannot deadlock or break the gate.
- **`Register(e *sdk.Extension)`**:
  - Registers `/pause [resume|status]` (and `/pause off` alias for resume). With no args: toggles paused/resumed in headless mode, and in interactive TUI (`ctx.HasUI()`) opens a blocking `ctx.Select` Resume dialog that always releases the gate in `defer` on select or cancel.
  - Hooks `sdk.EventToolCall`: single mutex fast-path check (`!gate.IsPaused()`) when unpaused; parks tool calls on `gate.WaitUntilResumed` when paused, returning `{block: true, reason: "Tool call aborted while paused."}` if the call's context cancels while parked.
  - Hooks `lifecycle.OnEnd("pause", ...)` and `OnSessionShutdown` so ending the owning interactive session or last tracked session never leaks an engaged process-wide pause.
- **Integrator hooks (`wiring.go`)**:
  - `pause.WorkStatus func(ctx sdk.Context) pause.WorkSummary` — optional callback returning outstanding unsettled turns, running jobs, and active agents (from `internal/jobs` / `internal/agents`).
  - Subagents, advisors, and eval loops can call `pause.DefaultGate.WaitUntilResumed(ctx)` (or `pause.WaitUntilResumed(ctx)`) at turn/tool boundaries.

## Tests

- **8 Go test functions** in `pause_test.go` (run with `-race`):
  - `pause-gate.test.ts` (3 ported cases): pause/resume idempotency & metadata, concurrent waiters with single-waiter cancellation leaving gate engaged, `OnChange` transitions outside mutex with panic isolation.
  - Port-specific tests: concurrent `-race` stress under interleaved `Pause`/`Resume`/`WaitUntilResumed`, `/pause` headless toggle/status/resume/off with `WorkStatus` hook, `/pause` interactive `ctx.Select` blocking hold & auto-resume on dismiss, `EventToolCall` fast-path allow / parking / sibling cancel / resume wake-up, and `OnSessionShutdown` releasing leaked pause.

## Not ported

| Feature | Upstream file | Why not ported |
|---|---|---|
| Fullscreen ANSI/native `PauseScreenComponent` live-clock alternate-screen overlay | `packages/tui/src/overlays/pause-screen.ts:55-410` | PiG subprocess extensions render interactive dialogs via `ctx.Select` rather than owning the host's fullscreen alternate screen; interactive `/pause` uses a blocking `ctx.Select` Resume prompt and always resumes on dismiss/Esc. |
| Provider-call boundary parking inside host agent loop | `packages/agent/src/agent-loop.ts` | PiG host owns the top-level provider loop; `internal/pause` parks tool boundaries via `EventToolCall` and exposes `WaitUntilResumed(ctx)` for extension-owned loops (subagents, advisors, eval). |
