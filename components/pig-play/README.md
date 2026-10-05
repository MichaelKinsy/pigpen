# pig-play

Shared foundation for Pigpen's pig games. A Go module
(`github.com/MichaelKinsy/pigpen/components/pig-play`) of five libraries and their
art. It registers no extension and does nothing by itself; game Packages import it.

| Library | What it holds |
|---|---|
| `libraries/sprite` | The one canonical pixel pig: the 16-by-14 mascot grid, its palette, the ten colour variants (`pig-default` is the default), the "PiG." wordmark, and the persisted selection. Imports no SDK. |
| `libraries/pixel` | RGBA canvases drawn as half-block terminal lines (24-bit colour, or the nearest 256 colour), with unchanged-line reuse and a pixel font. |
| `libraries/arcade` | Shared game scenery and HUD: night sky, hills, turf, two HUD lines, title and game-over cards. |
| `libraries/termgame` | Terminal plumbing: the full-terminal overlay options, the viewport inside the frame, key decoding (legacy, application-cursor, Kitty) and SGR mouse reports. Imports the SDK. |
| `libraries/gamemcp` | The opt-in MCP server that lets an agent play a game: five tools (`games_list`, `game_start`, `game_state`, `game_act`, `game_score`) on `127.0.0.1` behind a random bearer token, registered through the SDK's `RegisterMcpServer`, and the HUD line "AI playing: ...". Off until the user enables it; see below. Imports the SDK. `gamemcptest` is its test client. |
| `assets/pig/website-art.txt` | The sampled website colours the default variant reproduces, with source hashes. |

Games take their pig from `sprite.ActiveVariant(ctx.ConfigHome())` and derive scaled crops
from `sprite.MascotSpriteFor` and `sprite.MascotPalette`, so every game shows the same
recognisable pig. `ActiveVariant` reads the sprite chosen with PiG's built-in `/sprite` (PiG 0.4.0
and later; Pigpen no longer ships a login of its own): PiG saves it as `{"variant":"<id>"}` in
`$PIG_HOME/state/pig-standard/login.json`, the same file PiG Standard used. The catalogue here has
PiG's ten base sprites (`pig-default`, the eight colours and `sheriff`); a saved id it does not know,
such as one of PiG's character sprites or a sprite an extension registered, is drawn as `pig-default`.

## Use from a game Package

The consuming extension directory holds a `go.mod` that requires the module, and a `go.work`
that names it by a relative path. PiG's extension resolver reads that `go.work` and adds the
require and replace itself when it runs from source or fuses a Piglet Binary (a `replace`
in `go.mod` is ignored: the build first tries to download the module). The path is the same
in the repository and in a staged Piglet (Packages stage as `packages/<alias>`, with the alias
equal to the directory name, so the Piglet must select `pig-play` under the alias `pig-play`):

```
// go.work in components/<game>/extensions/<name>
go 1.26

use (
	.
	../../../pig-play
)
```

```
// go.mod
require github.com/MichaelKinsy/pigpen/components/pig-play v0.0.0
```

## Let an agent play: `gamemcp`

A game attaches the library to its extension with `gamemcp.Attach(ext, gamemcp.Config{...})` and gives it a
`gamemcp.Game` (its name, one-line description, instructions for playing from its state, the actions it accepts, where
it saves its high score, and how to open its overlay). The library then
does the rest, and only when asked:

- **Off by default.** `Attach` registers no tool and no command. It registers an event handler only when
  `PIG_GAMES_MCP` is `1`, `true`, `yes` or `on` in the environment pig started with. Otherwise nothing listens.
- **Turn it on** with the game's command, `mcp on` (for example `/runner mcp on`), or with `PIG_GAMES_MCP=1` for the
  whole session. `mcp off` and `mcp status` do what they say. The game's `session_shutdown` subscription is made
  only once the server is on.
- **What listens.** One streamable-HTTP MCP server on `127.0.0.1`, a random port, and a random 256-bit bearer token that
  only the registered config carries (`Authorization: Bearer ...`). A request from a non-loopback peer, with another `Host`
  header, or without the token is refused. It is never bound to another address.
- **What registers.** `RegisterMcpServer(<server>, {type: http, url, headers, description, exposure: codemode})`. The
  description is what Pi 0.99.2 ranks tools by; `codemode` exposure means the tools are not declared to the model:
  a codemode script finds them with `searchTools("game")` and calls `tools.mcp__<server>__game_state({})`.
- **When it stops.** `mcp off`, session end and reload (`session_shutdown`) unregister the server and close the port.

The agent reads what a person would see (`game_state`), answers with `game_act`, and the game keeps its own ticker: an
action is queued and applied, as the matching key press, on the next frame. `game_act` takes an optional `confidence`
(0 to 1) that the HUD shows. `games_list` and the server's MCP `instructions` carry the game's instructions (what each
action does and when it is right). `game_act` `stop` ends the play: the game freezes, saves its high score and shows
"AI stopped · final score N · high score M · ..." until a key closes the overlay; later actions are refused. `game_score`
answers before, during and after a game (then with the last final score and the saved high score), and
`game_score({wait_closed_ms})` waits up to 20 s for the overlay to close, so a script can play several games one after
another. A Go extension cannot close its own overlay: the SDK's custom components close only from a key
(`HandleInput` returning `Done`), with no done callback like Pi's `ctx.ui.custom`. See each game's README for its state
and actions.

## Test

```sh
PIG_SDK_DIR=$(pig reload --sdk-path | tail -1) npm run test:go-ports
```

Credits and the upstream record: [CREDITS.md](CREDITS.md), [port/PORT.md](port/PORT.md).
The original PiG code is MIT, Copyright (c) 2026 Michael Kinsy.
