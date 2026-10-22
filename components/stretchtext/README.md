# stretchtext

Replies that read short by default and grow where you click, as a PiG extension Package.

The model answers in one to three short sentences. The phrases that hold more are underlined; click one and it opens in
place into longer text, which may hold further phrases to open. Click the `«` after it and it closes. The words you
already read never move: new ones open up around them. When you send your next prompt the model is told which phrases
you opened, so it does not assume you read the rest.

It is a Go port of [pi-stretchtext](https://github.com/MonadicMike/pi-stretchtext) by Michal Pikosz, which is itself a port
of [cc-stretch](https://github.com/pro-vi/cc-stretch) by pro-vi (both MIT). See [`CREDITS.md`](CREDITS.md),
[`provenance.json`](provenance.json) and [`port/PORT.md`](port/PORT.md).

## Use

| Action | Fullscreen TUI | Any TUI mode |
|---|---|---|
| Open a stretch | click its underlined short form | `alt+o`, then its letter |
| Close a stretch | click the `«` after it | `alt+c`, then its letter |

Clicking needs PiG's fullscreen TUI (`"tuiMode": "fullscreen"`, `--tui-mode fullscreen` or `/settings`). In the regular
TUI the keyboard shortcuts still work. In print, JSON and RPC modes the extension changes nothing a reader sees and
asks the host for no UI.

`alt+o` puts a letter over each closed stretch of the latest three replies that hold one (up to 26); typing the letter
opens it. `alt+c` does the same over each `«`. Any other key puts the letters away.

## What it needs from PiG

The extension reads PiG's [Extension SDK](https://pkg.go.dev/github.com/MichaelKinsy/PiG/extensions/sdk) at the version
that carries the `tui` handle (`ctx.TUI()`: `Invalidate` and `RequestRender`, Pi's `TUI.invalidate` and
`TUI.requestRender`) and OSC 8 link claims (`ctx.OnLinkClick`, PiG D193). An older PiG fails to load the extension
rather than drawing stretches that cannot be clicked.

Replies that carry `{{short|long}}` markup show it as raw text wherever the extension is not loaded. The model writes
every level up front, so a reply costs about as many tokens as before: you read less, you do not pay less.

## Build and test

```sh
cd components/stretchtext/extensions/stretchtext
go test ./...                       # layer 1: the port against the original's recorded states and a fake host
cd ../.. && python3 port/mutate.py  # every mutation of port/mutations.json must be killed
```

[`port/PORT.md`](port/PORT.md) lists what is proven against the original and how, and the one difference that stays.
