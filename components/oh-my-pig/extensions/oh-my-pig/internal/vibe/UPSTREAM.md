# Upstream: `internal/vibe`

Follows omp release tag **`v19.1.1`** (`31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/vibe/state.ts` (`ModeState`)
- `packages/coding-agent/src/vibe/lifecycle.ts` (`LifecycleCustomType`, `LifecycleVersion`, `LifecycleEvent`, `ParseLifecycleEvent`, `PersistedVibeChildIDs`)
- `packages/coding-agent/src/vibe/runtime.ts` (`CLIAgent`, `SessionRegistry`, `OwnerScope`, `Spawn`, `Send`, `Wait`, `Kill`, `KillAll`, `SuspendScope`, `Rehydrate`, `Screens`, `RenderTurnResult`, `AggregateWorkerTokensPerSecond`)
- `packages/coding-agent/src/tools/vibe.ts` (`ToolNames`, `vibe_spawn`, `vibe_send`, `vibe_wait`, `vibe_kill`, `vibe_list`, `wallOf`, approval tiers)
- `packages/tui/src/tools/vibe.ts` (`ToolDetails`, `ScreenSnapshot`, `SpawnOutcome`, `SendOutcome`, `KillOutcome`, `WaitOutcome`, `CLI`, `SessionState`, `Op`)
- `packages/coding-agent/src/utils/token-rate.ts` (`CalculateTokensPerSecond`)
- `packages/tui/src/tools/task.ts:2324-2331` (`OneLineLabel`)
- `packages/coding-agent/src/slash-commands/builtin-modes.ts:316-332` (`/vibe [prompt]` command)
- `packages/coding-agent/src/modes/interactive-mode.ts:5806-5810, 5883-5906, 6115-6134, 6155-6208, 7391-7604` (Vibe mode toggle, toolset swap/restore, branch reconciliation, worker teardown)
- `packages/coding-agent/src/session/agent-session.ts:8618-8653, 8966-8978, 10074-10076` (hidden `vibe-mode-context` injection and exit stripping)
- Embedded prompt templates (`internal/vibe/prompts/*.md`): copied verbatim from `packages/coding-agent/src/prompts/system/vibe-mode-active.md` and `packages/coding-agent/src/prompts/tools/vibe-{spawn,send,wait,kill,list,turn-result}.md`.

## Default-session isolation

- Zero Vibe tools are registered at extension startup (`Register` registers only the `/vibe` command and session/lifecycle hooks).
- When `/vibe` is inactive, `before_agent_start` returns `(nil, nil)` and no Vibe tools exist in the session registry, so default sessions advertise ZERO new tools and change ZERO default prompt lines.
- Entering `/vibe` registers the 5 tools dynamically via `ctx.RegisterTool` (wrapped with `schemaorder.Apply` + `intent.ToolForContext` and `DefaultActive: false`) and narrows active tools to `read`, optional `todo`, and the 5 Vibe tools. Exiting `/vibe` terminates all live workers and restores the exact pre-vibe active toolset (including an empty set).

## Not Ported

| Feature | Upstream source | Why not ported / how it differs |
|---|---|---|
| Cold revival of parked child sessions across restarts / TTL park | `packages/coding-agent/src/vibe/runtime.ts:750-780`, `packages/coding-agent/src/task/executor.ts:4114-4180`, `packages/coding-agent/src/task/persisted-revive.ts` | Port-wide cold revival (`task/persisted-revive.ts`) is not implemented in `oh-my-pig` (see `internal/agents/UPSTREAM.md`, which depends on omp's custom `session_init` / `taskPromptCacheKey` header contract; PiG SDK v0.8.0 does expose `SessionManagerOpen`/`SessionManager`, but the port does not implement persisted child revival). Vibe spawns workers with `AgentIdleTTLMs: 0` so live workers are not parked during an active session, `Rehydrate` marks sessionless workers `dead` (transcript readable at `history://<id>`), and `Send` / `finishTurn` fail fast and tombstone any worker whose live session is gone rather than leaving an idle zombie. |
| Atomic multi-entry `appendEntriesAtomically` + `recoverPersistenceFromCurrentState` | `packages/coding-agent/src/vibe/runtime.ts:436-487, 1130-1240` | PiG SDK exposes `ctx.AppendEntry(customType, data)` only (no atomic batch write or indeterminate-persistence recovery API). `KillAll` persists each worker's `mode-exit` tombstone entry before tearing down workers and before `exitVibeMode` persists `mode_change: none`; any write error aborts exit and surfaces to the caller. |
| Tool unregistration from host registry on Vibe exit | `packages/coding-agent/src/session/session-tools.ts:1122-1156` | PiG SDK v0.8.0 has `RegisterTool` and `SetActiveTools` but no `UnregisterTool` API. Exiting `/vibe` immediately deactivates all 5 Vibe tools via `SetActiveTools(previousTools)` and every Vibe tool's `Execute` handler refuses calls whenever Vibe mode is inactive. |
| Agent-authored steering attribution flag on wire `Steer` | `packages/coding-agent/src/vibe/runtime.ts:901` (`live.steer(message, undefined, { attribution: "agent" })`) | PiG SDK's `AgentSession.Steer(text string)` takes a single string with no `attribution` option. `SteerFunc` passes `attribution = "agent"` through the Go contract (verified by unit tests), while the default PiG SDK adapter forwards `message` to `ref.Session.Steer(message)`. |
| `AgentSession` boundary guards rejecting host `/new`, `/fork`, `/move` while Vibe is active | `packages/coding-agent/src/session/agent-session.ts:8648-8653, 12918, 13080, 13217` | PiG's host owns `/new` and `/fork` directly and does not provide a cancellation hook for arbitrary session transitions from extensions (`session_shutdown` cleans up Vibe workers via `lifecycle.End`; extension-owned `/clear`, `/plan`, `/goal`, and `/guided-goal` refuse while Vibe is active via `modes.VibeActive`). |
| Parent `prompt_cache_key` lineage sharing (`<parent>:task`, `<parent>:sonic`) & OAuth account pools (`task.agentAccountPools`) | `packages/coding-agent/src/vibe/runtime.ts:1265-1325` | PiG v0.8.0's `CreateAgentSessionOptions` does not expose `promptCacheKey` or `oauthAccountPools`; `ModelOverride` and `ModelRole` (`task` / `smol`) are resolved and forwarded through `SpawnRequest`. |
| TUI mini-composer & TV-wall custom ANSI/Tern renderers & status-line tok/s wiring | `packages/tui/src/tools/vibe.ts:110-674`, `packages/coding-agent/src/modes/interactive-mode.ts:2564-2571` | `ToolDetails` carries the exact upstream `ScreenSnapshot` / `HiddenKilled` / `WaitDetails` JSON schema and `AggregateWorkerTokensPerSecond` is exported, but custom TUI/Tern component renderers in `internal/ansi` are out of scope for this package. |
| Concurrent `/vibe <skill>` FIFO serialization with interactive editor input waiter | `packages/coding-agent/src/modes/interactive-mode.ts:7409-7506` | PiG slash-command handlers execute sequentially through `ctx.SendUserMessage`; PiG does not expose an interactive editor waiter callback or internal skill-dispatch queue. |
| Loop reset-mode guard against active Vibe mode | `packages/coding-agent/src/modes/interactive-mode.ts:3667-3701` | `internal/modes` does not implement `loop.mode: "reset"` (`NewSession` is command-only in PiG). |
| Persisted-agents `history://` scan integration for `PersistedVibeChildIDs` | `packages/coding-agent/src/registry/persisted-agents.ts:12,427,678` | `PersistedVibeChildIDs` is exported from `internal/vibe` for the integrator to wire into `internal/agents`'s persisted-roster scan if desired. |
