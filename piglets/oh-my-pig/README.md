# oh-my-pig

An homage to [omp](https://omp.sh) (oh-my-pi, by Can Bölük and Stencil Labs) on PiG: omp's tools, prompt and
commands as a Go extension, drawn natively in [Tern](https://stencil.so/tern) with omp's look.

**Unofficial.** oh-my-pig is not omp, is not affiliated with its authors, and they have not reviewed it. omp
is MIT-licensed; what is taken from it is credited in [CREDITS.md](CREDITS.md).

**Parity is a goal, not a promise.** omp changes daily and builds on native components that PiG does not
have. oh-my-pig follows omp at a pinned commit and ports its core first.
[PARITY.md](PARITY.md) is the list of omp's surface and the state of each item here; nothing is claimed
beyond it.

## What it is

- **A derivative of [pig-tern](../pig-tern/README.md).** The manifest extends pig-tern, and the frontend
  member is pig-tern's with omp's look fixed on: the same roles and node shapes omp sends to Tern, so Tern's
  built-in styles for omp apply. It keeps PiG's own names towards Tern and does not present itself as omp.
  Its mark pairs PiG's active sprite pig head with omp's π on the welcome banner (in Tern and plain terminals)
  and uses the same animated pig head for Tern's working spinner. Outside Tern, PiG draws as usual.
- **omp's tools instead of PiG's.** The Piglet keeps none of PiG's built-in tools. The model sees omp's
  tool names, schemas and descriptions from the [`oh-my-pig` extension](../../components/oh-my-pig/README.md).
- **One Go Binary, no Node.**

## Build and run

```sh
npm run stage
pig piglet validate dist/staged/piglets/oh-my-pig/piglet.yaml
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/oh-my-pig/piglet.yaml --format binary --out ./oh-my-pig
./oh-my-pig          # in a Tern pane
```

Building needs a Go toolchain and, for pig-tern's member, Stencil's Go SDK for Tern from the Go module proxy.

## Browser relay (Chrome extension)

The browser relay lets the `browser` tool drive your existing Chrome tabs through a Chrome extension
(relay only; omp's side-panel chat is not ported).

Once loaded, this extension can read and act on every site in the Chrome profile it is loaded into, with
that profile's signed-in accounts, when an oh-my-pig session uses the relay.

### Install

```sh
./oh-my-pig browser-relay install
```

`install` writes the unpacked extension to `<agent-dir>/browser-relay/extension` (default
`~/.pig/agent/browser-relay/extension`; pass `--dir <path>` to choose another folder), records the folder
in `<agent-dir>/browser-relay/installs.json`, writes the native host launcher and native messaging host
manifest (`dev.pi_in_go.oh_my_pig.browser`), and prints the one-time manual Chrome steps:

1. Open `chrome://extensions` in the Chrome profile you want to drive.
2. Turn on **Developer mode**.
3. Click **Load unpacked** and select the directory printed by `install`.

Unattended Chrome profile installation (`--profile`) is not ported; `--no-chrome` is accepted and is the
only behavior. Its toolbar badge shows `on` while its native host serves the relay.

### Sync and use

- Run `./oh-my-pig browser-relay sync` after updating `oh-my-pig` to update every registered extension
  directory, refresh existing native messaging host manifests, and ask any running relay to reload
  connected extensions once idle.
- Use the relay per call by passing `app: "relay"` to `browser.open(...)` in `eval`, or make it the
  default by setting `"browser": { "relay": true }` in your settings (or `PI_BROWSER_RELAY=1`).

### Remove

1. Open `chrome://extensions` and click **Remove** on the `oh-my-pig` extension.
2. Delete the relay data directory: `~/.pig/agent/browser-relay` (or `$PIG_CODING_AGENT_DIR/browser-relay`
   if overridden; also delete any custom `--dir` folder you passed to `install`).
3. Remove the native messaging host registration for your OS:
   - **macOS**: delete `dev.pi_in_go.oh_my_pig.browser.json` from
     `~/Library/Application Support/<Browser>/NativeMessagingHosts/` (written for `Google/Chrome`,
     `Google/Chrome Beta`, `Google/Chrome Dev`, `Google/Chrome Canary`, `Chromium`, and
     `BraveSoftware/Brave-Browser`).
   - **Linux**: delete `dev.pi_in_go.oh_my_pig.browser.json` from
     `${XDG_CONFIG_HOME:-~/.config}/<browser>/NativeMessagingHosts/` (written for `google-chrome`,
     `google-chrome-beta`, `google-chrome-unstable`, `google-chrome-canary`, `chromium`, and
     `BraveSoftware/Brave-Browser`).
   - **Windows**: delete registry key
     `HKCU\Software\Google\Chrome\NativeMessagingHosts\dev.pi_in_go.oh_my_pig.browser` (the manifest file
     itself lives inside `%USERPROFILE%\.pig\agent\browser-relay`, removed in step 2).

## Where it stands

Against omp `v19.1.1`: all 15 tools omp turns on by default (`read`, `write`, `edit`, `bash`, `grep`, `glob`,
`eval`, `task`, `wait`, `todo`, `ask`, `web_search`, and `lsp`, `ast_edit`, `debug` mounted in `bash`), 35 of omp's
49 model-facing tools in all, omp's five agents, its system prompt, 17 of its 20 internal URL schemes (including
`omp://` and `ssh://`), DOCX/PPTX/XLSX/EPUB/PDF reading, 30 of its 89 slash commands (+ `/autoresearch`), 8 of its
16 modes, full-prompt composer predictions (`composer.predictions`), the `browser`, `commit`, and `ratchet` eval
namespaces, the browser relay above, and omp's look in Tern for the transcript, native floating autocomplete, and
the composer. 111 of omp's 644 settings take effect; the others are accepted and ignored. [PARITY.md](PARITY.md)
has every row, how each was compared, and what is not proven.

## Status

Source only, no published release. Built and run on darwin/arm64; the Go tests also run on Linux in CI; Windows is
compiled only.
