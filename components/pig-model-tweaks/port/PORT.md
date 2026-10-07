# Port record: pig-model-tweaks

Original: **`@liyu1981/pi-tweaks` 0.1.1** by Yu Li (MIT), commit `65e6ccfbb77956aa15ce34e7ca9735e1d476c292`,
kept unmodified in [`oracle/`](oracle). Built against **PiG 0.4.1**'s public Go SDK
(`github.com/MichaelKinsy/PiG/extensions/sdk`). Credits: [`../CREDITS.md`](../CREDITS.md).

## Status

The three model extensions are ported; this is not a whole-repository port.

| Ported | Not ported |
|---|---|
| `pt-remember-model` → `pmt-remember-model` | `pt-subagent` (upstream's fourth extension, added after the pinned `v0.1.1` release): spawns isolated child `pi` processes — a separate concern and a separate extension if ever |
| `pt-openrouter-lock-provider` → `pmt-openrouter-lock-provider` | |
| `pt-model-guard` → `pmt-model-guard-pref` | |

## Mapping (original file → Go)

| Original | Go |
|---|---|
| `extensions/pt-remember-model.ts` | `remember_model.go` |
| `extensions/pt-openrouter-lock-provider.ts` | `openrouter_lock.go`, `openrouter.go` |
| `extensions/pt-model-guard.ts` | `model_guard.go`, `approval.go`, `keyseq.go`, `picker.go` |
| `src/store.ts` | `store.go` |
| `src/pi-settings.ts` | `pigsettings.go` |
| `src/agent-dir.ts` | resolved through the SDK (`configHome()`), not vendored as code |

## Deliberate differences

- **Names**: commands take the `pmt-` prefix where the original used `pt-`, and the settings file
  is `pmt-settings.json` where the original wrote `pi-tweaks-settings.json`, so the port's identity
  is distinct from the original it runs beside.
- **No model pickers.** The original opened a filterable multi-select over the model registry for
  `pmt-model-guard-pref` and a model list for `pmt-openrouter-lock-provider`. PiG's Go SDK cannot
  enumerate the model registry at the PiG revision this repository's CI pins, so both commands take
  explicit arguments (`add <provider/model>`, `lock <model> <provider>`) and fall back to asking.
  The pickers can come back unchanged when `ModelRegistry.GetAvailable()` is available to the port.
- **One Go package instead of a shared module.** The three features are one factory, so they share
  a `store` value rather than the original's filesystem lock between separate extensions.
- **Model identity is read from the host event and `getModelInfo`**, since PiG exposes models as
  untyped maps where pi had typed objects.
- **The guard never blocks in its input handler.** PiG dispatches input handlers on the goroutine
  that owns the terminal, so a dialog opened and awaited inside the handler would wait on itself and
  freeze the TUI. The `ask` worker runs after the handler returns, and an accepted prompt is
  re-sent through `SendUserMessage`. The original asked from inside the handler, which pi's
  input pipeline allows. The same rule governs the selection question below.
- **Selecting a non-preferred model asks; the original only warns.** The original announces a
  non-preferred `model_select` with a toast and lets the switch stand. Here the switch is confirmed
  after the handler returns; a decline changes back to the event's `previousModel` (or the startup
  model), because PiG applies the model before it emits `model_select` and discards the handler's
  result, so there is no refuse-in-place. An approval is session-only and is deliberately **not**
  remembered: remember-model skips every selection the guard would question, approved or not.
- **`pt-subagent` is out of scope**, as in the table above.

## Results (this revision)

- `pig package validate ./components/pig-model-tweaks` and
  `pig install ./components/pig-model-tweaks/extensions/pig-model-tweaks --validate-only --json`:
  valid, the factory builds, starts and registers.
- **Unit tests** for the selection-confirmation state (`model_select_test.go`: what the guard
  questions, single-use revert and dialog markers, session-scoped approvals, the decline fallback)
  pass under `npm run test:go`.
- Behaviour verified by hand against the documented command walkthrough in
  [`../README.md`](../README.md): the settings file round-trips, the guard refuses and `ask`
  re-sends, and an OpenRouter lock strips its `:provider` suffix at request time.
- **No equivalence-harness scenarios yet.** This port was not run through `pigeq` with recorded
  golden traces from the TypeScript original under Pi, so unlike `dirty-repo-guard` it claims
  behavioural fidelity from the mapping above and manual verification, not from recorded traces.
  Recording the scenarios is the remaining work before this port can be called complete in the
  porter's sense.

## Re-verified

- Target: PiG 0.4.1 (the version `npm run validate` pins in `scripts/pig-requirement.json`),
  go1.27.x, `npm run check` (quality gates, npm contract, go-only, ports list) passes.
