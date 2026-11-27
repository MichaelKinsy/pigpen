# Upstream Provenance — `internal/prediction`

Ported from **omp v19.1.1** (`github.com/can1357/oh-my-pi`, MIT).

## Ported Source Files

| Upstream file | Lines | Go target | Notes |
| --- | --- | --- | --- |
| `packages/coding-agent/src/prompts/system/composer-prediction-user.md` | 1–14 | `prompts/composer-prediction-user.md` | Embedded verbatim via `//go:embed`. |
| `packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts` | 18–40, 86–129 | `parse.go` | `SkipReply`, `MaxPredictionLength`, `MaxPredictionChoices`, `PredictionMaxTokens`, `RenderPrompt`, `ParseComposerPrediction`, `ParseComposerPredictions`, `ParseReply`, `sanitizeText`, quote/list-marker/preamble stripping, Unicode case+punctuation deduplication. |
| `packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts` | 138–301 | `controller.go`, `complete.go` | Asynchronous per-session `Controller` with generation counter, per-session `widgetMu` publish/clear serialization, 15s deadline, interactive TUI & non-subagent gating, empty-draft check, conversation anchor staleness check, and prompt-cache-friendly side turn (`systemPrompt` + active tool schemas + compaction-aware LLM messages from `SessionManager().BuildSessionContext()` preserving real `toolCall`/`toolResult` blocks + `<composer-prediction>`, `maxTokens: 1024`, `toolChoice: "none"` when tools are non-empty). |
| `packages/coding-agent/src/modes/controllers/event-controller.ts` | 552, 1158, 1325, 2560–2566, 2619, 2665 | `register.go`, `controller.go` | `agent_end` triggers prediction only on clean settled assistant turns (`settledDone`); `agent_start`, `before_agent_start`, `input`, `session_before_switch`, `session_before_compact`, `session_compact`, `session_before_tree`, `session_tree`, `session_shutdown`, and `lifecycle.OnEnd("prediction", ...)` cancel/retire predictions immediately. |
| `packages/coding-agent/src/modes/settings.ts` | 1171–1198 | `controller.go`, `internal/settings/schema.json` | `composer.predictions` (default `true`) and `composer.predictionAlternatives` (default `false`). |
| `packages/utils/src/sanitize-text.ts` | 25–40 | `parse.go` (`sanitizeText`) | Strips invalid UTF-8, ANSI escape sequences, and C0/C1 control characters (keeping `\t`, `\n`). |

## Extension ↔ Frontend Bridge

- `widget.go`: When `surface.Native()` is true (Tern native surface), publishes a hidden structured `kit.View` dock widget under key `"omp.composer.prediction"` (`&kit.VStack{ID: "omp.composer.prediction"}` containing `&kit.Text{ID: "omp.composer.prediction.choice#<i>", Text: choice}` for each alternative `i = 0..3`, most likely first). `piglets/pig-tern/frontend` intercepts this widget in `ompDockWidgetParts` without rendering visible dock text and feeds `Field.Ghost`.
- On plain-terminal (`!surface.Native()`) surfaces, `publishWidget` is a no-op so plain-terminal scrollback and ANSI proofs are untouched. `clearWidget` clears unconditionally (whenever `ctx.HasUI()`) so a Tern→terminal fallback never leaves a stale widget behind.

## Ported Tests

| Upstream test | Go test |
| --- | --- |
| `packages/coding-agent/test/composer-prediction-controller.test.ts` (`parseComposerPrediction`, `parseComposerPredictions`, controller gating/cancellation/staleness/deadline) | `prediction_test.go` |

## Deliberate Omissions & Host-SDK Notes

| Upstream feature | Reason |
| --- | --- |
| OpenTelemetry metrics (`composer-prediction-telemetry.ts`, `MeterProvider`, `shown()`/`accepted()` histograms) | No OTLP metrics pipeline in `oh-my-pig` (zero external deps, stdlib only). |
| `model-usage-journal` sidecar (`#recordUsage` / `appendModelUsage` with `purpose: "composer-prediction"`) | PiG's extension SDK `ctx.Complete` does not expose `sessionManager.appendModelUsage`. |
| Wire-level abort propagation into `ctx.Complete` | PiG's `sdk.Context.Complete(model, req, auth)` does not accept a `context.Context` / `AbortSignal` across the subprocess wire; cancellation and the 15s deadline abort waiting locally and discard any late response immediately while the host call drains. |
| `ephemeralMaxTokensPreservesRequest()` per-model budget-thinking probe | PiG's `ctx.Complete` takes a uniform request map (`maxTokens: 1024`, plus `toolChoice: "none"` whenever active tools are present). |
