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
| `pt-remember-model` → `pmt-remember-model` | `pt-subagent` (upstream's fourth extension, added after the pinned `v0.1.1` release): out of scope until the original's pin moves past `v0.1.1`; it spawns isolated child `pi` processes, a separate concern |
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

## Additions (kept by owner decision, 2026-10-20)

These are additive features of this component, not refusals and not gaps. Every form of the
original still does what the original does, with the original's texts (scenarios).

- **The guard asks and can name a model**: `ask`, `noask`, `add <provider/model>`,
  `remove <provider/model>`; provider-only allow-list entries; the startup-model rule; per-session
  approvals. (Status #6.)
- **Selecting a non-preferred model asks and can revert.** In the TUI the original only toasts and lets
  the switch stand; here the switch is confirmed after the handler returns, a decline changes back to
  the event's `previousModel` (or the startup model), because PiG applies the model before it emits
  `model_select` and discards the handler's result. An approval is session-only and is **not**
  remembered: remember-model skips every selection the guard would question. Outside the TUI the
  original's toast texts are sent (`guard-model-select`). (#2.)
- **The lock command can name a model**: `clear <model>` and `lock <model> <provider>`.
- **Names**: commands take the `pmt-` prefix where the original used `pt-`, and the settings file
  is `pmt-settings.json` where the original wrote `pi-tweaks-settings.json`, so the port's identity
  is distinct from the original it can run beside. `eqcopy.sh` (below) swaps them back for the
  equivalence run. There is no filesystem lock between the features. (#15.)

## Differences that are not additions

- **No theme colors in the pickers.** A Go extension has no handle on the host theme
  (`ctx.ui.theme` is not in the SDK), so the multi-select and the lock picker draw the original's
  rows, order, keys and texts without accent, success, warning and dim styling. SDK item filed.
- **The prompt guard does not confirm inside its input handler.** PiG dispatches input handlers on the
  goroutine that owns the terminal, so a dialog opened and awaited inside the handler would wait on
  itself and freeze the TUI. The default refuses with a toast; the `ask` worker runs after the handler
  returns and an accepted prompt is re-sent through `SendUserMessage`. The original asked from inside the
  handler. SDK item filed (status #1).
- **One Go package instead of a shared module.** The three features are one factory, so they share
  a `store` value, and one `session_start` handler runs their session-start work in the original's
  load order.
- **Model identity is read from the host event and `getModelInfo`**, since PiG exposes models as
  untyped maps where pi had typed objects.
- **`pt-subagent` is not provided** (no command or tool is registered): upstream added it after the
  pinned release, so it stays out of scope until the original's pin moves.

## Equivalence runs

`port/oracle` is run unmodified under Pi (`pigeq record --ts port/oracle`). One scenario drives
both lanes, so the Go lane must answer to the original's command and settings-file names:
`port/eqcopy.sh <dir>/pig-model-tweaks` copies the extension and rewrites exactly four constants
(`rememberModelName`, `modelGuardName`, `lockProviderName`, `settingsFileName`); the copy is what
`pigeq check`, `run` and `mutate` take as `--go` (the directory must be named `pig-model-tweaks`,
PiG checks the identity). Nothing else differs between the copy and the shipped extension.

Scenarios (`port/scenarios`, goldens in `port/golden`, 25):

| Contract | Scenarios |
|---|---|
| remember-model commands, texts, case/whitespace | `remember-commands` |
| model_select remembers (openai and, via `set_model`, the scripted `eq-llm` model), writes settings.json, toasts one tick later; no settings.json, no toast; off | `remember-select-toast`, `remember-select-no-settings-file` |
| startup restore with an empty branch; disabled | `remember-startup-restore`, `remember-disabled-no-restore` |
| a locked OpenRouter model is restored as its catalog variant, remembered with `base:provider` in the toast and in settings.json; lock off writes the base id (the file is read back through a bash call, so key order and bytes are compared) | `remember-startup-restore-locked`, `remember-startup-restore-lock-off` |
| the guard's `model_select` announcements outside the TUI (in preferred, NOT in preferred) | `guard-model-select` |
| new session restore, deferred toast ordering | `remember-new-session-b4` (known PiG bug B4, see findings) |
| guard list, on, off, toggle, session_start hint, no hint when off or seeded | `guard-commands-empty`, `guard-commands-seeded`, `guard-disabled-no-hint` |
| add, remove and bare multi-select flows in RPC (no models; cancelled; first time; seeded) | `guard-pickers-no-models`, `guard-pickers-cancelled`, `guard-pickers-cancelled-seeded` |
| lock list, on, off, clear, provider on a non-OpenRouter model, usage, multi-word slug | `lock-commands`, `lock-list-empty` |
| single-argument lock says `defaultModel -> base:provider` and writes it into settings.json, multi-word slug, bare `clear` writes the base id, lock off | `lock-single-argument`, `lock-multi-word-argument`, `lock-clear-writes-base-id`, `lock-single-argument-off` |
| lock routing: the suffix is stripped and provider.order set, full-id lock, disabled, unmatched catalog suffix (the request reaches a fake OpenRouter server) | `lock-routing-strips-and-orders`, `lock-routing-full-id-lock`, `lock-routing-disabled`, `lock-routing-unmatched-suffix` |

Not in a scenario, proven by layer-1 tests only (`parity_test.go`, `legacy_test.go`,
`lock_provider_test.go`, `home_test.go`, `model_select_test.go`, `settings_order_test.go`): the
component frames and keys (`ui.custom` is a no-op in RPC, so the trace never shows a picker), the
confirmed picker flows (add, remove, bare, lock picker with its provider input), `new` restore,
`remember-model` on a `model_select` for an OpenRouter model (the scenarios cannot `set_model` onto
one: see finding 2), the legacy-file migration, the host's agent directory, and everything TUI-only:
the input handler's confirm (#1) and the model_select ask-and-revert (#2).

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

1. **PiG bug B4 (a race).** The original defers its remembered-model toast with `setTimeout(0)`.
   Pi sends the RPC response first; PiG's Node extension host sometimes sends the toast first (the
   timer fires while the handler's result crosses the host boundary). It showed up after `new_session`
   (`remember-new-session-b4`, which passed the host check in the last recording) and, in this revision's
   runs, in `set_model` steps of `remember-startup-restore` and `remember-select-toast` on some runs,
   not on others. A recording whose host check fails on that ordering is repeated until the pig-ts
   lane agrees with Pi; the goldens come from Pi, so a passing run proves nothing about the race. The
   Go port sends the toast 30 ms after the handler returns, after the response, as Pi does.
2. **PI_OFFLINE=1 changes PiG's OpenRouter catalog.** The harness starts every host with
   `PI_OFFLINE=1`. Under it Pi's `set_model` response for `google/gemma-4-26b-a4b-it` carries cost
   `{input 0.09, output 0.3, cacheRead 0.05}`, PiG's carries `{0.0675, 0.225, 0.0375}` (a different
   catalog snapshot: other OpenRouter rows differ too); without `PI_OFFLINE` both give Pi's values.
   Probed with a bare `--mode rpc` host and one env variable at a time. Not normalized: the scenarios
   start on the OpenRouter model (`--provider openrouter --model ...`) and only `set_model` onto the
   scripted `eq-llm` model, whose response the harness normalizes. Reported as a PiG core bug.
3. **System prompt identity.** The provider request body carries "inside pi" under Pi and "inside pig"
   under PiG (documented identity difference). The routing scenarios pass `--system-prompt eq --no-tools`.
4. **Pi oracle version string.** `pigeq env` of the `pigeq-normalize` branch wants a Pi that prints
   `1.3.0` and whose source checkout is at the pinned commit (`--pi-source pi-src`, 1402973a); the oracle
   wrapper used here prints `1.3.0` for `--version` and runs the same build. The goldens' host label is `pi 1.3.0`.

## Fixed against the original

0.1.2:

- `before_provider_request` acts only when the active model's provider is `openrouter`, as the
  original does; it used to strip suffixes and add `provider.order` for any provider.
- `/pmt-openrouter-lock-provider <provider>` locks the current OpenRouter model and bare `clear`
  clears it; setting or clearing a lock writes the model into `settings.json` as the default.
- `pmt-remember-model` restores the remembered model on `startup` when the session branch has no
  message, as well as on `new`.

0.2.0:

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

0.2.1 (this revision):

- `settings.json` gets `base:provider` for a locked OpenRouter model (remember-model and every lock
  change), as the original's `persistDefaultModel` writes it, and the single-argument lock says
  `defaultModel -> base:provider` exactly as the original does. With the lock feature off, or for another
  provider, the base id is written.
- `settings.json` is rewritten the way the original's `JSON.stringify(settings, null, 2)` does: the user's
  key order is kept (the earlier code sorted every key), a new key goes last, `<` is not escaped.
- The guard's `model_select` announcements outside the TUI use the original's texts
  (`✅ p/id — in preferred models`, `⚠️  p/id — NOT in preferred models`).

## Still open

Tracked in `handoffs/pigpen-parity/status/pig-model-tweaks.md`: the input-handler confirm (#1, needs an
SDK capability). The model_select ask-and-revert (#2), the allow-list extensions (#6), the names (#15)
and the lock's extra forms are kept additions (owner decision, see above). `pt-subagent` (#14) is out of
scope until the original's pin moves.
