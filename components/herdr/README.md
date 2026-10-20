# herdr reporter Package

Go extension, written against PiG's public Go extension SDK, that reports PiG's `idle`, `working` and `blocked` state to [herdr](https://github.com/herdrdev/herdr), the terminal workspace manager, over herdr's socket, as [herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) does. Outside a herdr pane it registers nothing. It needs no Node.js: PiG builds it from source with a Go toolchain, and a Piglet Binary fuses it. It reports state, the resume command (herdr 0.9.2+) and release. See the [herdr Piglet](../../piglets/herdr/README.md) for what it reports and how it behaves.

**It works on its own.** Installing this Package is enough: no Piglet is needed.
The [`herdr` Piglet](../../piglets/herdr/README.md) is an optional composition that
selects this same Package. Use it directly from the Pigpen checkout root:

```sh
pig package validate ./components/herdr
pig install ./components/herdr
```

The local install references this checkout; keep it at its installed path and remove it with `pig remove` using the same path. The `herdr` Piglet selects this Package through `npm run stage`.

`PIG_BIN=/path/to/pig npm run test:go` runs `go test` in `extensions/herdr` against a fake herdr socket (a unix socket server) and a wire-level fake PiG host that also dispatches `pi.events`; it needs a Go toolchain and PiG's SDK (`PIG_SDK_DIR`, `PIG_SOURCE_ROOT` or `PIG_BIN` selects it).

**It does what herdr-pig does.** The original is [herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) at `3ec680471e82` (`extensions/herdr-agent-state.ts`, Apache-2.0, vendored unmodified in [`port/oracle`](port/oracle)). The Go extension talks to herdr the way it does: newline-delimited JSON over herdr's socket (`pane.report_agent`, `pane.release_agent`), source and agent `pig`, one connection per request, a retry of the same request after 0.5 s and then 1.5 s, the resume command `pig --session <session file>` (or `<id>`), and a release only on a quit. `HERDR_ENV=1`, `HERDR_SOCKET_PATH` and `HERDR_PANE_ID` switch it on; `herdr` itself need not be on `PATH`. Its seven tests are twins, a replay of 98 event sequences against the original in Node must give the same request lines byte for byte, and `pigeq` scenarios run both under Pi and PiG. [`port/PORT.md`](port/PORT.md) is the record, with the contract table and every difference.

What the port adds or does differently, and why it does not change what herdr sees from the original:

| Addition | Behavior |
|---|---|
| Prompt-based `blocked` | Off by default. `PIGPEN_HERDR_UI_PROMPTS=1` also reports `blocked` while a PiG dialog (`ui_prompt_start` / `ui_prompt_end`) is open, with its title as the message. Pi has no such event, so without the opt-in the port reports what the original reports |
| A stderr line | One line when herdr did not take a request, repeated only after a request was delivered in between |
| `session_start` | Changes nothing but the session: the blocked counters carry over, as in the original (Pi loads a fresh copy of the extension for every session) |

`agent_settled` reports idle only when `isIdle()` succeeds and returns true, as in the original.

**`herdr:blocked`.** The extension listens for `herdr:blocked` on `pi.events` (`{ active, label }`), the bus herdr's own Pi integration handles, so another extension can mark a block that is not a UI prompt. It uses PiG's Go SDK bridge (`Extension.Events()`, PiG 0.4.0 and later). The label is sent as given, and `active` is read with JavaScript's truthiness, as in [herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) by Christian Polzer and Hai-Fai Solutions (Apache-2.0). It does not run on Pi: Pi users keep herdr's own Pi integration.

To install it from herdr instead, use the herdr plugin in [`piglets/herdr/herdr-plugin`](../../piglets/herdr/README.md#install-from-herdr).

[MIT](LICENSE), by Michael Kinsy, with the port of herdr-pig's extension under the Apache License 2.0 ([LICENSE-herdr-pig](LICENSE-herdr-pig), [NOTICE](NOTICE)); see `provenance.json`.
