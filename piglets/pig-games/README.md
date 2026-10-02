# pig-games

PiG with two pixel-art games, all Go, moved from PiG Standard (Michael Kinsy, MIT).
**Nothing starts until you type a command.**

| Command | From | What it does |
|---|---|---|
| `/runner`, `/pig-runner` | Package [`pig-runner`](../../components/pig-runner/README.md) | PiG Runner, an obstacle runner over the whole terminal. |
| `/angry-pigs` | Package [`angry-pigs`](../../components/angry-pigs/README.md) | Angry Pigs, a slingshot game over the whole terminal. |

Both games share the pig from the library Package
[`pig-play`](../../components/pig-play/README.md), which selects no extension of its own. Each
game registers commands only (no event handler, no tool), so bundling it costs nothing until
it is opened; Esc closes only the game overlay.

**Which pig.** The sprite login header and `/sprite` are part of PiG itself (built in since 0.4.0), so this
Piglet no longer bundles a login of its own. The games draw the pig you chose with PiG's `/sprite`: PiG saves the
choice in `$PIG_HOME/state/pig-standard/login.json` and the games read that file. A sprite the games do not know
(one of PiG's character sprites, or one an extension registered) is drawn as `pig-default`.

**Let an agent play (opt-in, off by default).** `/runner mcp on` and `/angry-pigs mcp on` (or `PIG_GAMES_MCP=1` in the
environment, which adds one `session_start` handler to each game) serve that game to an agent as an MCP server on
`127.0.0.1` behind a random bearer token, for this session. Until then nothing listens. A codemode script finds the tools with
`searchTools("game")`; [`demo/jev-plays.js`](../../demo/README.md) has Jev play. Details in each game's README.

The same Packages are selected by [`pig-with-batteries`](../pig-with-batteries/README.md).

The built-in tools, models and ambient extensions and Skills stay at PiG's defaults.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-games/piglet.yaml
pig --piglet dist/staged/piglets/pig-games/piglet.yaml
```

Build a Piglet Binary (needs a git checkout of the PiG source the `pig` was built from):

```sh
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-games/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-games
```

The Binary fuses both extensions (`extensionRealization: fused`). Linux/amd64 is the
development default; no release is published and no support is claimed.

## Credits

The games and the art are PiG's (Michael Kinsy, MIT). See each Package's `CREDITS.md` and `port/PORT.md`.
