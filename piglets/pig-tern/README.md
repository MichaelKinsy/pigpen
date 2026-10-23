# pig-tern

PiG drawn natively in [Tern](https://stencil.so/tern). Run directly in a Tern pane, PiG's interactive mode becomes a Tern Surface Protocol surface: messages render as Markdown, tool calls as tool cards, edits as diffs, selectors as pickers, settings as a prefs sheet, and the input editor as a Tern editor. Outside Tern, PiG draws as usual.

**Needs PiG 0.5.3 or later (the member requires SDK v0.5.3).** The Piglet fills the frontend slot (`slots.frontend`, PiG D91) and the member uses PiG's component kit and extension mouse delivery (D103), all in PiG's SDK since 0.5.0. Pigpen builds and tests it on PiG 0.5.3, its pinned release. PiG 0.4.1 rejects `slots.frontend`.

**Source only, not a published release.** No release Binary is published. A frontend member is fused into a Piglet Binary, so build one with the reviewed PiG (set `PIG_SOURCE_ROOT` to a git checkout of it):

```sh
npm run stage
pig piglet validate dist/staged/piglets/pig-tern/piglet.yaml
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-tern/piglet.yaml --format binary --out ./pig-tern
```

Then run `./pig-tern` in a Tern pane. Building needs a Go toolchain, not Node.js. The member links Stencil's Go SDK for Tern (`github.com/stencil-hq/tern-sdk/go/tern`), a module outside PiG, so the build needs network access to the Go module proxy or a Go module cache that already holds `tern-sdk` v0.1.0 and its `golang.org/x` requirements. See the root [release blockers](../../RELEASE-BLOCKERS.md).

## When it draws natively

The member draws only when all of these hold. Otherwise it declines and PiG uses its own ANSI renderer, with no difference in behavior.

- PiG runs in a Tern pane (`TERM_PROGRAM=tern`).
- No multiplexer sits between them: `TMUX`, `STY`, `ZELLIJ` and `HERDR_ENV` are unset. A multiplexer swallows the protocol's APC strings.
- `PIG_TUI_NATIVE` is not `0`, and neither is the SDK's `TERN_TSP`. Set either to `0` to opt out.
- Tern answers the protocol hello within one second. If it does not, PiG falls back for the rest of the run.

## What is native

| PiG | Tern |
| --- | --- |
| User messages | a card around an `md` node |
| Assistant messages | `md` nodes; streamed text arrives as appends |
| Thinking | a collapsible card that follows PiG's thinking toggle |
| Tool calls | collapsible `tool` nodes with name, target, status (including cancelled), elapsed time and output |
| Edit results | `diff` nodes inside the tool card, with the added and removed line counts |
| Input editor | an `editor` node with `edit`, `undo` and `send`, without PiG's border lines; it reports `sendable` when PiG can take a prompt |
| Working indicator | a `spinner` above the editor that Tern animates, so spinner ticks send no frames; a retry countdown has the warning tone |
| Working and idle state | OSC 9;4 terminal progress while PiG works, repeated every second, and cleared when it stops, so Tern's agent list and `cx.agents:wait` see a working PiG whatever PiG's `showTerminalProgress` setting says |
| Footer | a `status` strip: directory, branch, session name, token usage and cost, context with a `meter` (warning above 70%, error above 90%), model with an `effort` for the thinking level, and extension statuses |
| Selectors (for example `/model`, `/resume`, the session tree, fork, thinking level, login, and an extension's selects) | a picker sheet over the transcript: title, description, search query, tabs, nested rows as a tree, and Cancel and Select actions. Clicks pick, choose, dismiss, clear the search and switch tabs |
| `/settings` | a prefs sheet: on/off settings as switches, settings with a submenu as buttons, the rest as a choice of values |
| Overlays | a card over the transcript around the overlay's lines |
| Session file | a hidden `text` node with id and role `pig.session` whose `href` is the session file PiG writes (none while the session is not persisted), so a Tern plugin can find a pane's session |
| Extension components drawn with PiG's component kit (D103), for example pig-music | Tern elements: text, Markdown, boxes as cards, stacks, lists, select and settings lists, progress bars, loaders, images in their cells, and a titled overlay's frame as its card. A view's theme overrides apply to its own element only |
| Clicks on an extension's view | PiG's terminal click, with the view path, the clicked list item and the click count (so a double click counts), reaches the extension's component as a mouse click does in PiG's own renderer |
| Footer folder | a path span that opens in Tern's Files pane on click |
| PiG's theme | Tern's program palette, sent before the first frame and whenever PiG's colors change. PiG picks a light or dark theme from Tern's appearance, so switching Tern's appearance switches PiG's theme |
| Contrast | an extension view's theme overrides are corrected as Tern corrects a palette: text reads at least 4.5:1 (3:1 for muted, dim and border tokens) on every Tern surface it can land on, in light and dark, so a theme made for a dark terminal stays readable on Tern's light surface |
| PiG's mark | the pig head of PiG's active sprite in the settings sheet's brand tile, as pixel art in place of Tern's π icon |
| Fullscreen easter eggs | a Tern screen surface that plays the animation as one `ansi` node, with the dock (editor and footer) kept under it |
| External editor | PiG lends the pane to the editor: the surface closes, keeping its transcript, and is adopted again when the editor exits |
| Everything else (widgets drawn as raw lines, an extension's own footer, other extension output) | plain lines inside the surface |

Keys still reach PiG directly, so a picker or prefs sheet behaves like PiG's own selector; Tern's pointer events on it become the same actions.

Because the editor takes `send`, Tern agent blocks and Carly can prompt PiG. A prompt that arrives while PiG cannot take input, for example during first-time setup, waits until it can.

Frames respect Tern's credits. Tern acknowledges no frame while a pane is hidden, so PiG's startup fits in two frames and later frames wait until the pane shows: an agent started in a background tab still receives its prompt.

## Credits

The composition and the frontend member are MIT, Michael Kinsy. The member is built on **tern-sdk**, Stencil's official Go SDK for Tern (`github.com/stencil-hq/tern-sdk/go/tern` v0.1.0, MIT, © Stencil Labs, Inc., <https://github.com/stencil-hq/tern-sdk>). A Binary that fuses the member redistributes it; see [CREDITS.md](CREDITS.md) and [licenses/tern-sdk-LICENSE](licenses/tern-sdk-LICENSE).
