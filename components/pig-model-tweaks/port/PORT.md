# Port record: pig-model-tweaks

Original: **`@liyu1981/pi-tweaks` 0.1.1** by Yu Li (MIT), commit `65e6ccfbb77956aa15ce34e7ca9735e1d476c292`,
kept unmodified in [`oracle/`](oracle). Built against **PiG 0.4.1**'s public Go SDK
(`github.com/MichaelKinsy/PiG/extensions/sdk`). Credits: [`../CREDITS.md`](../CREDITS.md).

Kind 1: a Pi TypeScript extension, proven against the original under Pi 1.3.0.1402973a
(`pigeq record --ts port/oracle`). The three extensions load as one directory (`--ts port/oracle`).

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
| `extensions/pt-openrouter-lock-provider.ts` | `openrouter_lock.go`, `openrouter.go`, `guard_picker.go` (showLockPicker's component) |
| `extensions/pt-model-guard.ts` | `model_guard.go`, `guard_picker.go` (showModelPicker), `catalog.go`, `approval.go`, `keyseq.go` |
| `src/store.ts` | `store.go` (including migrateLegacy) |
| `src/pi-settings.ts` | `pigsettings.go` |
| `src/agent-dir.ts` | resolved through the SDK (`configHome()`), not vendored as code |

## Deliberate differences

- **Names**: commands take the `pmt-` prefix where the original used `pt-`, and the settings file
  is `pmt-settings.json` where the original wrote `pi-tweaks-settings.json`, so the port's identity
  is distinct from the original it runs beside. `eqcopy.sh` (below) swaps them back for the equivalence run.
- **No theme colors in the pickers.** A Go extension has no handle on the host theme
  (`ctx.ui.theme` is not in the SDK), so the multi-select and the lock picker draw the original's
  rows, order, keys and texts without accent, success, warning and dim styling.
- **This port's own forms**: `ask`, `noask`, `add <provider/model>`, `remove <provider/model>`
  on the guard, `clear <model>` and `lock <model> <provider>` on the lock, provider-only allow-list
  entries, the startup-model rule, per-session approvals. They never change what the original's
  forms do. Owner decision pending (status file, #6).
- **The guard never blocks in its input handler.** PiG dispatches input handlers on the goroutine that
  owns the terminal, so a dialog opened and awaited inside the handler would wait on itself and
  freeze the TUI. The `ask` worker runs after the handler returns, and an accepted prompt is
  re-sent through `SendUserMessage`. The original asked from inside the handler, which pi's
  input pipeline allows. SDK item filed (status file, #1).
- **Selecting a non-preferred model asks; the original only warns.** The original announces a
  non-preferred `model_select` with a toast and lets the switch stand. Here the switch is confirmed
  after the handler returns; a decline changes back to the event's `previousModel` (or the startup
  model), because PiG applies the model before it emits `model_select` and discards the handler's
  result, so there is no refuse-in-place. An approval is session-only and is deliberately **not**
  remembered: remember-model skips every selection the guard would question, approved or not.
  Owner decision pending (#2).
- **`settings.json` gets the base model id**, not the original's `base:provider` variant, and the
  "defaultModel ->" line of the lock command names the id written. Owner decision pending (#8).
- **One Go package instead of a shared module.** The three features are one factory, so they share
  a `store` value rather than the original's filesystem lock between separate extensions, and one
  `session_start` handler runs their session-start work in the original's load order.
- **Model identity is read from the host event and `getModelInfo`**, since PiG exposes models as
  untyped maps where pi had typed objects.
- **`pt-subagent` is not provided** (no command or tool is registered), as in the table above.

## Equivalence runs

`port/oracle` is run unmodified under Pi (`pigeq record --ts port/oracle`). One scenario drives
both lanes, so the Go lane must answer to the original's command and settings-file names:
`port/eqcopy.sh <dir>/pig-model-tweaks` copies the extension and rewrites exactly four constants
(`rememberModelName`, `modelGuardName`, `lockProviderName`, `settingsFileName`); the copy is what
`pigeq check`, `run` and `mutate` take as `--go` (the directory must be named `pig-model-tweaks`,
PiG checks the identity). Nothing else differs between the copy and the shipped extension.

Scenarios (`port/scenarios`, goldens in `port/golden`, 18):

| Contract | Scenarios |
|---|---|
| remember-model commands, texts, case/whitespace | `remember-commands` |
| model_select remembers, writes settings.json, toasts one tick later; no settings.json, no toast; off | `remember-select-toast`, `remember-select-no-settings-file` |
| startup restore with an empty branch; disabled | `remember-startup-restore`, `remember-disabled-no-restore` |
| new session restore, deferred toast ordering | `remember-new-session-b4` (known PiG bug B4, see findings) |
| guard list, on, off, toggle, session_start hint, no hint when off or seeded | `guard-commands-empty`, `guard-commands-seeded`, `guard-disabled-no-hint` |
| add, remove and bare multi-select flows in RPC (no models; cancelled; first time; seeded) | `guard-pickers-no-models`, `guard-pickers-cancelled`, `guard-pickers-cancelled-seeded` |
| lock list, on, off, clear, provider on a non-OpenRouter model, usage, multi-word slug | `lock-commands`, `lock-list-empty` |
| lock routing: the suffix is stripped and provider.order set, full-id lock, disabled, unmatched catalog suffix (the request reaches a fake OpenRouter server) | `lock-routing-strips-and-orders`, `lock-routing-full-id-lock`, `lock-routing-disabled`, `lock-routing-unmatched-suffix` |

Not in a scenario, proven by layer-1 tests only (`parity_test.go`, `legacy_test.go`,
`lock_provider_test.go`, `home_test.go`, `model_select_test.go`): the component frames and keys
(`ui.custom` is a no-op in RPC, so the trace never shows a picker), the confirmed picker flows
(add, remove, bare, lock picker with its provider input), `new` restore, the single-argument
lock (the original writes `base:provider` into settings.json and says so, this port writes the
base id: owner decision #8), `settings.json` persistence, the legacy-file migration, the host's
agent directory, and everything TUI-only: the input handler's confirm (#1) and the
model_select behaviour (#2).

## Results (this revision)

- `pigeq gaps --ts port/oracle`: 6 PARTIAL stand-ins (`ctx.model`, `getAvailable`, `ui.custom`), 0 blocking.
- `pigeq record`: 18 scenarios from Pi 1.3.0.1402973a; 17 pass the host check (the original under
  PiG's Node host equals Pi), `remember-new-session-b4` fails it (finding 1).
- `pigeq check` (Go lane against the goldens, with the eqcopy names): 18 of 18 identical.
- `pigeq mutate --unit` on the eqcopy: 76 mutations in `port/mutations.json`, 0 survivors (73 killed
  by layer-1 tests, 3 by scenarios: `lock-clear-wrong-provider-allowed`, `lock-nonrouter-text`,
  `lock-usage-text`). One mutation (writing the base id instead of the persisted one into the
  toast) is equivalent while the port writes the base id and was removed.
- `go vet` for linux, windows and darwin, and `go test -race -count=5`: pass.
- Not proven by a scenario or a mutation: the pickers' titles reach the host only through
  `ui.custom`, whose component content a fake host cannot read; `guardTitle` and
  `lockPickerItems` are tested as functions.

## Findings (host differences, none normalized)

1. **PiG bug B4.** The original defers its remembered-model toast with `setTimeout(0)`. After
   `new_session`, Pi sends the RPC response first; PiG's Node extension host sends the toast first
   (the timer fires while the handler's result crosses the host boundary). `remember-new-session-b4`
   keeps the step and is expected to fail the pig-ts host check until B4 is fixed. The Go port sends the
   toast 30 ms after the handler returns, after the response, as Pi does (`set_model` passes).
2. **Scripted-model address in `set_model` responses.** `data.baseUrl` carries the scripted LLM
   server's port, which differs per lane. The scenarios avoid `set_model` onto `eq-llm` until pigeq
   normalizes that address (harness branch `pigeq-normalize`); they select `openai` models instead.
3. **System prompt identity.** The provider request body carries "inside pi" under Pi and "inside pig"
   under PiG (documented identity difference). The routing scenarios pass `--system-prompt eq --no-tools`.

## Fixed against the original

0.1.2:

- `before_provider_request` acts only when the active model's provider is `openrouter`, as the
  original does; it used to strip suffixes and add `provider.order` for any provider.
- `/pmt-openrouter-lock-provider <provider>` locks the current OpenRouter model and bare `clear`
  clears it; setting or clearing a lock writes the model into `settings.json` as the default.
- `pmt-remember-model` restores the remembered model on `startup` when the session branch has no
  message, as well as on `new`.

0.2.0 (this revision):

- The model-guard command's texts and pickers (`add`, `remove`, bare: the multi-select with the
  original's ordering, search, keys and scrolling; Cancelled, Added, Removed, first-time and
  empty-selection behaviour), the session_start hint, and `on`, `off`, `toggle`, `list` texts.
- remember-model's `on`, `off`, `clear` and status texts, and the toast sent after the handler returns.
- The lock command's `on`, `off` and `list` texts, the whole argument as the provider slug (the
  original accepts "a b"), and the picker: available OpenRouter models plus every locked id, with
  "provider: x" and "no provider lock" descriptions, a search line, a provider prompt whose
  placeholder is the current lock or "deepseek", and a silent return on cancel. Non-TUI usage is unchanged.
- store: the agent directory is the host's (`sdk.AgentDir`, Pi's `getAgentDir()`) when it gives one;
  the per-feature files of earlier versions (`last-model.json`, `openrouter-provider-prefs.json`,
  `model-preferences.json`) seed the settings file as in `store.ts`.

## Still open

Tracked in `handoffs/pigpen-parity/status/pig-model-tweaks.md`: the input-handler confirm (needs an
SDK capability), the model_select confirm-and-revert (owner decision), allow-list extensions (owner
decision), `settings.json` writing the base id (owner decision), names (owner decision), and
`pt-subagent` (owner decision).
