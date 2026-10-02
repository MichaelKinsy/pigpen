# herdr reporter Package

Go extension, written against PiG's public Go extension SDK, that reports PiG's `idle`, `working` and `blocked` state to [herdr](https://github.com/herdrdev/herdr), the terminal workspace manager. Outside a herdr pane it registers nothing. It needs no Node.js: PiG builds it from source with a Go toolchain, and a Piglet Binary fuses it. It implements herdr's published [Add Herdr support to your agent](https://herdr.dev/docs/add-herdr-support/) contract: state, the resume command (herdr 0.9.2+) and release. See the [herdr Piglet](../../piglets/herdr/README.md) for what it reports and how it behaves.

**It works on its own.** Installing this Package is enough: no Piglet is needed.
The [`herdr` Piglet](../../piglets/herdr/README.md) is an optional composition that
selects this same Package. Use it directly from the Pigpen checkout root:

```sh
pig package validate ./components/herdr
pig install ./components/herdr
```

The local install references this checkout; keep it at its installed path and remove it with `pig remove` using the same path. The `herdr` Piglet selects this Package through `npm run stage`.

`PIG_BIN=/path/to/pig npm run test:go` runs `go test` in `extensions/herdr` against a fake `HERDR_BIN_PATH` (with `HERDR_SOCKET_PATH`) and a wire-level fake PiG host; it needs a Go toolchain and PiG's SDK (`PIG_SDK_DIR`, `PIG_SOURCE_ROOT` or `PIG_BIN` selects it). The tests are a port of the earlier TypeScript suite, case for case; the two `herdr:blocked` bus cases are named, skipped tests until the bridge below exists.

**Not ported: `herdr:blocked`.** The earlier TypeScript extension also listened to the `herdr:blocked` event on `pi.events`, the bus herdr's own Pi integration handles, so that another extension could mark a block that is not a UI prompt. PiG's Go SDK has no `pi.events` bridge (its SDK surface table lists `pi.events.*` as a Go exception), so this Go extension reports `blocked` only for UI prompts (`ui_prompt_start` and `ui_prompt_end`). The Go `pi.events` bridge is being ported in PiG 0.4.0; the extension can listen for `herdr:blocked` once it ships. It does not run on Pi: the TypeScript file was removed with this port, and Pi users keep herdr's own Pi integration.

[MIT](LICENSE), original work by Michael Kinsy; see `provenance.json`.
