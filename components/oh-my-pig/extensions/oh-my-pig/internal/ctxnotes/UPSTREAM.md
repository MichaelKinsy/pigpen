# Upstream Reference: `internal/ctxnotes`

Port of omp's opt-in experimental context management tools (`context_notes`, `new_context`), session-entry persistence (`experimental_context_notes`), and `<experimental-context-notes>` system prompt injection (`https://github.com/can1357/oh-my-pi`, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/tools/context-notes.ts:1-184` → `ctxnotes.go` (`ContextNotesSchema`, `NewContextSchema`, `ExecuteContextNotes`, `ExecuteNewContext`, `Register`, `DefaultActive: false` + `toolcmd.RegisterAutoActivate` + `intent.Tool` + `intent.RegisterSync` + `schemaorder.Apply`).
- `packages/coding-agent/src/session/context-notes.ts:1-59` → `ctxnotes.go` (`EntryType = "experimental_context_notes"`, `MaxContextNotesBytes = 16384`, `ParseContextNotesFromEntries` scanning backwards until `reset_boundary` while ignoring malformed entries, `BuildPromptInstructions`).
- `packages/coding-agent/src/session/context-settings.ts:118-128` → `SettingExperimentalContextManagement = "compaction.experimentalContextManagement"` (default `false`).
- `packages/coding-agent/src/prompts/tools/{context-notes,context-notes-text,new-context}.md` + `packages/coding-agent/src/prompts/system/context-notes.md` → `prompts/*.md` (embedded verbatim).

## PiG SDK Adaptations

| Area | Upstream implementation | Port adaptation |
|---|---|---|
| Context-window rollover (`new_context`) | `AgentSession` consumes the `new_context` turn-local signal and appends a native `reset_boundary` session entry so the next turn starts from a fresh context window while preserving raw history for `history://current/full`. | PiG v0.8.0 Go SDK exposes `ctx.AppendEntry` and `ctx.Compact` rather than a native `reset_boundary` tree-reset primitive. `ExecuteNewContext` records a per-session rollover flag and returns `{"requested": true}` / `"New context window requested."`. On `EventAgentEnd`, `consumeRolloverIfPending` clears the flag, appends a custom `reset_boundary` marker entry, re-appends the current `experimental_context_notes` entry (if non-empty) so subsequent turns retain the notebook across the boundary, and triggers `ctx.Compact(...)` with the notebook passed as `customInstructions`. |
| Session ownership / branch-change guard | `ToolSession.sessionManager.getSessionId() === ownerId` and leaf-ID comparison across `ensureOnDisk()`/`flush()`. | `ctx.AppendEntry` is synchronous over the host socket; per-session cache is keyed by `ctx.GetSessionID()` and released via `lifecycle.OnEnd("ctxnotes", cleanupSession)`. |

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `compaction.experimentalContextManagement` | `false` | Yes | When `false` (default), both tools stay inactive (`DefaultActive: false`), `BuildPromptInstructions` returns `""`, and direct execution returns `"Experimental context management is disabled."`. |
