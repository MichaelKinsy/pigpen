# pig-model-tweaks

Three everyday fixes for working with a mixed model set, as a PiG extension Package. All
state lives in one file; every command uses the `pmt-` prefix.

1. **PiG forgets your model.** Every new session starts on whatever is in
   `settings.json`, so the model gets re-picked by hand. → `pmt-remember-model`
   saves the selection and restores it next session.
2. **OpenRouter routes to a random upstream provider.** One model id can be served
   by backends with different speed, quality and price. → `pmt-openrouter-lock-provider`
   pins a model to the provider you choose.
3. **A prompt goes to the wrong model.** A stray `Ctrl+P` or `/model` can send work
   to a costly or weak model. → `pmt-model-guard-pref` refuses the prompt, naming
   the model and the command that allows it. `ask` turns that refusal into a
   question, and a model *selected* outside the list is confirmed before it is
   used (and is never remembered).

It registers commands and event handlers only: no tools, no providers, no
renderers.

## Provenance and licensing

Ported from the three model extensions of
[`github.com/liyu1981/pi-tweaks`](https://github.com/liyu1981/pi-tweaks) (MIT,
© Yu Li, pinned at commit `65e6ccfbb77956aa15ce34e7ca9735e1d476c292`, the
`v0.1.1` release), which were written for [pi](https://pi.dev). The unmodified
original is kept under [`port/oracle/`](port/oracle); see [`CREDITS.md`](CREDITS.md)
and [`provenance.json`](provenance.json). The port keeps the behaviour and
rewrites the code against PiG's Go extension SDK, since PiG does not
load pi's TypeScript extensions. Two names change to keep the port's identity
distinct from the original: commands take `pmt-` where the original used `pt-`,
and the settings file is `pmt-settings.json` where the original wrote
`pi-tweaks-settings.json`.

The original's fourth extension, `pt-subagent` (added upstream after the pinned
`v0.1.1` release), is **not** here. It spawns
isolated child `pi` processes and folds their transcripts back into the session,
which is a different concern from model selection; it would be a separate
extension rather than a fourth feature of this one.

## Commands

| Command | Description |
| --- | --- |
| `/pmt-remember-model [status\|on\|off\|clear]` | Remember and restore the last selected model. |
| `/pmt-openrouter-lock-provider [list\|on\|off\|clear <model>\|lock <model> <provider>]` | Manage OpenRouter provider locks. With no arguments, asks for a model and then a provider. |
| `/pmt-model-guard-pref [list\|on\|off\|toggle\|ask\|noask\|add <provider/model>\|remove <provider/model>]` | Manage the allowed-model list, and whether a prompt outside it is refused or asked about. |

`status` is the default for `pmt-remember-model`: no argument reports whether it is
on and what was remembered.

Every feature defaults to **on** and reports through a toast, so nothing here
prints into the conversation. Turning `pmt-openrouter-lock-provider` off also stops
`pmt-remember-model` from writing the `:provider` suffix, and no routing is
applied.

## Settings

One file, under PiG's agent directory:

```
~/.pig/agent/pmt-settings.json
```

It follows `PIG_HOME`, so an isolated `PIG_HOME` gets its own file and PiG never
reads a stale model from a developer's normal session.

```jsonc
{
  "version": 1,

  // /pmt-remember-model
  "rememberModel": {
    "enabled": true,
    "last": { "provider": "openrouter", "modelId": "deepseek/deepseek-v4.1-flash" }
  },

  // /pmt-model-guard-pref
  "modelGuard": {
    "enabled": true,
    "confirm": false,        // true asks instead of refusing; see "model-guard"
    "allowedModels": [
      { "provider": "opencode-go", "model": "space-bunny-free" },
      { "provider": "openrouter", "model": "" }   // provider-only: trust the backend
    ]
  },

  // /pmt-openrouter-lock-provider (base model id -> upstream provider slug)
  "openrouterModelProviderPref": {
    "enabled": true,
    "locks": { "deepseek/deepseek-v4.1-flash": "deepseek" }
  }
}
```

Missing sections fall back to defaults on load, unknown fields are dropped, and
entries missing half their identity are skipped, so a hand edit cannot leave the
extension half-configured. Writes are serialized and atomic (temporary file plus
rename), so several features can update the file in one session, and an
interrupted write leaves the previous file intact.

An allow-list entry naming only a `provider` trusts every model of that backend,
which is how a whole provider is allowed at once. An empty list allows everything.

## How the features work

### remember-model

On `model_select` the selection is saved here and written into PiG's own
`settings.json` as `defaultProvider` and `defaultModel`, so the next session
already begins on it. On a `new` or `startup` session the model is also restored
through the host, because a locked OpenRouter model is stored with a `:provider`
suffix that PiG's resolver cannot parse. A started session that already has
messages keeps its own model.

The one exception is the model guard: a selection the guard would question
(outside the allow-list and not the session's startup model) is **never** saved,
whether the user later approves it or cancels it. Approving means "use it for
this session", not "start the next session on it", so `pmt-settings.json` and
`settings.json` keep the last model the guard did not question.

### openrouter-lock-provider

A lock is expressed two ways. It is appended to the model id written into
`settings.json` as `base:provider`, and at request time the suffix is stripped
again while OpenRouter's `provider.order` is set to put the locked provider first.
The stripping is what keeps the request from 404ing on a model id that OpenRouter
never heard of.

### model-guard

Before a prompt is sent from an interactive TUI session with a model outside the
allow-list, the extension stops it. Nothing is sent to the model, and the user is
told which model it was and which command allows it.

**Selecting** a non-preferred model is confirmed before it is used: a dialog
asks whether to really switch, and

- **yes** keeps the model *for this session only* — prompts to it stop being
  questioned and re-selecting it does not ask again, but remember-model keeps it
  out of the defaults, so the next session starts on the old model;
- **no** cancels the switch by changing back to the model the selection
  replaced (or, when the event carries no previous model, to the session's
  startup model) — and says so.

The confirm is unconditional whenever the guard is active in the TUI; the
`ask`/`noask` setting stays what it was, a statement about *prompts*. Outside the
TUI (RPC, print) there is nobody to ask, so the selection is announced with the
old warning toast and left alone.

Two models are always allowed without a stop and without a question:

- anything the allow-list names, and
- **the model the session started on**, read from PiG's own `settings.json` at
  `session_start`. A session that opens on the model its owner configured is not a
  stray switch; a model chosen later in the session is not that model, so the
  guard still sees it. Without this rule `pmt-remember-model` and the guard would
  contradict each other on every prompt, because the remembered model is by
  definition the one the guard does not list.

`/pmt-model-guard-pref ask` replaces the refusal with a yes/no question. The
question is asked after the input handler has returned, and an accepted prompt is
re-sent through `SendUserMessage`, whose source is the extension rather than the
terminal, so the same prompt is never asked about twice. The cost of `ask` is that
a re-sent prompt skips the host's skill-command and prompt-template expansion,
which both apply only to `/`-prefixed text that no command resolved.

#### Why the guard never asks from inside its own handler

An input handler runs on the goroutine that owns the terminal: PiG dispatches
input handlers on that loop, and the loop is also what installs a dialog and feeds
it keystrokes. A handler that opened a dialog and waited for the answer would be
waiting for itself, and the symptom is a PiG that stops responding to every key
with nothing on screen. This extension therefore never blocks in a handler —
input **or** `model_select`; PiG emits that event from wherever the switch ran,
which is the TUI's own loop when the picker confirmed it. The `ask` and
selection workers run after their handlers return, which the SDK's retained
`Context` allows.

A `model_select` also cannot be refused outright: PiG applies the model before
it emits the event and discards the handler's result, so declining the dialog
means *switching back*, which the event's `previousModel` makes possible. The
switch-back emits its own `model_select`; a flag in the store consumes exactly
that one event so the restored model is never questioned again in a loop.

## Deliberate differences from the pi original

- **No model pickers.** The original opened a filterable multi-select over every
  available model for `pmt-model-guard-pref`, and a model list for
  `pmt-openrouter-lock-provider`. PiG's Go SDK cannot enumerate the model registry
  at the PiG revision this repository's CI pins, so both commands take explicit
  arguments (`add <provider/model>`, `lock <model> <provider>`) and fall back to
  asking for the value. `ModelRegistry.GetAvailable()` exists in PiG main; with it
  the pickers can come back unchanged in behaviour.
- **No subagent profiles**, as described under provenance.
- **Selecting a non-preferred model asks; the original only warns.** The Pi
  original announces a non-preferred `model_select` with a toast and lets the
  switch stand. Here the switch is confirmed off-loop, a decline changes back,
  and an approval lasts the session without being remembered — PiG emits
  `model_select` after applying the model and ignores the handler's result, so
  "cancel" can only be a switch-back.
- **Model identity is read from the host event and `getModelInfo`**, since PiG
  exposes models as untyped maps rather than pi's typed objects.
- **The store is one Go package** rather than pi's shared module: the three
  features are one factory, so they share a `store` value instead of a filesystem
  lock.

## Runtime requirements

- PiG whose SDK exposes the surface this extension compiles against: the validator
  commit pinned in `.github/workflows/ci.yml`. Verified with
  `pig install --validate-only`.
- Node, Go or any other toolchain: none. The extension runs in the Pig process or
  in a Go cell and never spawns a subprocess.

## Check it by hand

```sh
pig package validate ./components/pig-model-tweaks
pig install ./components/pig-model-tweaks/extensions/pig-model-tweaks --validate-only --json
```

Then, in a session, confirm the file appears and the model round-trips:

```
/pmt-remember-model
# pick a model with /model, then:
/pmt-remember-model            # last: provider/model
/pmt-model-guard-pref add opencode-go/space-bunny-free
/pmt-model-guard-pref list
/pmt-model-guard-pref ask        # or: noask
/pmt-openrouter-lock-provider lock vendor/model upstream-provider
/pmt-openrouter-lock-provider list
```

Cycle to a model outside the list with `Ctrl+P` and submit a prompt: the prompt
is not sent, a toast names the model, and the terminal keeps responding. With
`ask` set, the same submit shows the question instead, and answering it re-sends
the prompt.

Open `~/.pig/agent/settings.json` and confirm `defaultModel` matches, and
`~/.pig/agent/pmt-settings.json` for the sections above.

## Install

The Package works on its own; no Piglet is needed:

```sh
pig install ./components/pig-model-tweaks
```

The [`pig-with-batteries` Piglet](../../piglets/pig-with-batteries/README.md)
selects this same Package through `npm run stage`. A local install references
this checkout; keep it at its installed path and remove it with `pig remove`
using the same path.

[MIT](LICENSE), work by Yu Li (original and Go port); see [`provenance.json`](provenance.json).