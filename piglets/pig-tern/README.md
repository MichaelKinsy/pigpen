# pig-tern

PiG drawn natively in [Tern](https://stencil.so/tern). Run directly in a Tern pane, PiG's interactive mode becomes a Tern Surface Protocol surface: messages render as Markdown, tool calls as tool cards, edits as diffs, and the input editor as a Tern editor. Outside Tern, PiG draws as usual.

**Needs PiG 0.4.2 or later.** The Piglet fills the frontend slot (`slots.frontend`, PiG D91), which PiG 0.4.1 rejects.

**Source only, not a published release.** No release Binary is published. A frontend member is fused into a Piglet Binary, so build one with the reviewed PiG (set `PIG_SOURCE_ROOT` to a git checkout of it):

```sh
npm run stage
pig piglet validate dist/staged/piglets/pig-tern/piglet.yaml
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-tern/piglet.yaml --format binary --out ./pig-tern
```

Then run `./pig-tern` in a Tern pane. Building needs a Go toolchain, not Node.js. See the root [release blockers](../../RELEASE-BLOCKERS.md).

## When it draws natively

The member draws only when all of these hold. Otherwise it declines and PiG uses its own ANSI renderer, with no difference in behavior.

- PiG runs in a Tern pane (`TERM_PROGRAM=tern`).
- No multiplexer sits between them: `TMUX`, `STY`, `ZELLIJ` and `HERDR_ENV` are unset. A multiplexer swallows the protocol's APC strings.
- `PIG_TUI_NATIVE` is not `0`. Set `PIG_TUI_NATIVE=0` to opt out.
- Tern answers the protocol hello within one second. If it does not, PiG falls back for the rest of the run.

## What is native

| PiG | Tern |
| --- | --- |
| User and assistant messages | `md` nodes; assistant text streams by append |
| Thinking | a collapsible card |
| Tool calls | `tool` nodes with name, target, status (including cancelled) and output |
| Edit results | `diff` nodes |
| Input editor | an `editor` node with `edit`, `undo` and `send`, without PiG's border lines; it reports `sendable` when PiG can take a prompt |
| Working indicator | a `spinner` above the editor that Tern animates, so spinner ticks send no frames; a retry countdown has the warning tone |
| Footer | a `status` strip: directory, branch, session name, token usage and cost, context with a `meter` (warning above 70%, error above 90%), model with an `effort` for the thinking level, and extension statuses |
| Everything else (selectors, overlays, widgets, an extension's own footer, other extension output) | plain lines inside the surface |

Because the editor takes `send`, Tern agent blocks and Carly can prompt PiG. A prompt that arrives while PiG cannot take input, for example during first-time setup, waits until it can.

Frames respect Tern's credits. Tern acknowledges no frame while a pane is hidden, so PiG's startup fits in two frames: an agent started in a background tab still receives its prompt.

## Credits

The composition and the frontend member are MIT, Michael Kinsy.
