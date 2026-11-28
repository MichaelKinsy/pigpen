# Upstream Reference: `internal/think`

Port of omp's hidden `think` private-scratchpad tool (`https://github.com/can1357/oh-my-pi`, MIT, release tag `v19.1.1`, commit `31ffee513f`).

## Upstream files followed (`v19.1.1`)

- `packages/coding-agent/src/tools/think.ts:39-72` → `think.go` (`ThinkTool`, schema `{ thoughts: string }` with `additionalProperties: false`, description `"private scratchpad; not shown to user"`, `intent = "omit"` so intentionally not wrapped with `intent.Tool`, returns text `"------"` and `Details: {"recorded": true}`).
- `packages/coding-agent/src/tools/index.ts:779-781,826` → `IsEnabled` and `toolcmd.RegisterAutoActivate("think", IsEnabled)` with `DefaultActive: false`.

## Settings

| Key | Default | Honoured | Notes |
|---|---|---|---|
| `externalThinking` | `false` | Yes (via `Setting`) | Gates auto-activation of the `think` tool; default-off keeps default sessions unchanged. |

## Partial / Host-Dependent

| Feature | Upstream file | Why partial / host-dependent |
|---|---|---|
| `supportsExternalThinking` provider-transport native reasoning suppression | `packages/coding-agent/src/tools/think.ts:6-37` | `SupportsModel` filters known incompatible models (`codex`, OpenAI `o1`/`o3`/`o4`) when `ctx.GetModelInfo()` is populated, but provider wire-level `api` transport filtering and native reasoning suppression (`forceReasoningOff`) are owned by PiG's host model transport, not extension tool registration. |
| First-turn forced `think` `tool_choice` | `packages/coding-agent/src/session/agent-session.ts` | PiG host does not expose per-turn automatic `think` forced `tool_choice` wiring without colliding with user `/force` overrides; remains host-dependent. |
