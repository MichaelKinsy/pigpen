# internal/providerwire — Upstream Provenance & Wire-Hook Notes

## Upstream Sources (`omp v19.1.1`)

- **Model roles & resolution** (`roles.go`):
  - `packages/coding-agent/src/config/model-roles.ts:18-155` — `@role` / `pi/role` / `*` alias prefixes, `MODEL_ROLES` metadata, chat vs kind roles.
  - `packages/tui/src/overlays/model-browser.ts:61-77` — canonical `ModelRole` IDs (`default`, `smol`, `slow`, `vision`, `plan`, `commit`, `tiny`, `memory`, `task`, `advisor`, `widget`, `image`, `web`, `speech`, `dictation`, `judge`).
  - `packages/coding-agent/src/config/model-resolver.ts:411-413,1210-1442` — `revisionSpellingKey`, `ROLE_PRIORITY_ALIAS`, `ROLE_CONFIGURED_FALLBACK`, cycle-safe nested `@role` expansion, `:thinking` suffix splitting.
  - `packages/catalog/src/compat/rules/runtime/behavior.kdl:431-549` — built-in `role-defaults` priority chains (`smol`, `slow`, `image`, `web`, `speech`, `dictation`, `judge`).

- **Service tiers & extended context** (`wire.go`):
  - `packages/coding-agent/src/slash-commands/builtin-modes.ts:76-167,508-645` — `/fast [on|ultra|off|status]`, `/slow [on|off|status]`, `/extended-context [on|off|status]`.
  - `packages/ai/src/providers/anthropic-headers.ts:26-53,82,109` — `buildBetaHeader`, `mergeAnthropicBetaHeader`, `fastModeBeta` (`fast-mode-2026-02-01`), `context-1m-2025-08-07`.
  - `packages/ai/src/providers/anthropic.ts:840-849,2157,5683-5685` — Anthropic fast mode (`speed: "fast"` + `fast-mode-2026-02-01` beta header).
  - `packages/ai/src/providers/openai-shared.ts:391-398`, `openai-completions.ts:2298`, `openai-responses.ts:152`, `openai-codex-responses.ts:1413`, `azure-openai-responses.ts:67` — OpenAI `service_tier` (`"priority"`, `"ultrafast"`, `"flex"`).
  - `packages/ai/src/providers/google-shared.ts:1053-1060,1384` — Google Gemini `serviceTier` (`"priority"`, `"flex"`).

- **Forced `tool_choice` wire shapes** (`wire.go`):
  - `packages/ai/src/utils/tool-choice.ts:47-101` — OpenAI Completions (`{"type":"function","function":{"name":...}}`), OpenAI Responses (`{"type":"function","name":...}`), Anthropic (`{"type":"tool","name":...}`).
  - `packages/ai/src/providers/google-shared.ts:1076-1079` — Google Gemini (`toolConfig.functionCallingConfig: {"mode":"ANY","allowedFunctionNames":[...]}`).

## PiG v0.8.0 Wire-Hook Contract Notes

- `sdk.EventBeforeProviderRequest` (`extensions/sdk/provider_hook.go:16-35`):
  - `data["payload"]` is a `map[string]any`. Mutating in place and returning `(nil, nil)` causes the SDK to send the modified payload only if `reflect.DeepEqual(object, sent())` is false; otherwise it replies `nil` so the host keeps its original payload byte-for-byte.
  - When neither service tier overrides/settings nor a forced `tool_choice` are active, `onBeforeProviderRequest` returns `(nil, nil)` immediately before touching `data["payload"]`.
- `sdk.EventBeforeProviderHeaders` (`extensions/sdk/provider_hook.go:17-22`):
  - `data["headers"]` is a `map[string]any`. Mutating in place modifies outgoing headers; return value is ignored.
  - When neither Anthropic fast mode nor `extendedContext` is enabled, `onBeforeProviderHeaders` returns `(nil, nil)` immediately without touching `data["headers"]`.
- Forced `tool_choice` is turn-scoped and automatically cleared on `EventTurnEnd`, `EventAgentEnd`, `EventSessionShutdown`, and `lifecycle.OnEnd("providerwire", ...)`. It is skipped if `payload["tools"]` is absent or does not declare the requested tool name.
