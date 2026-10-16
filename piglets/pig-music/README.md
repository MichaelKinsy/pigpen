# pig-music

PiG with one extension: the [pig-music](../../components/pig-music/README.md) player (Michael Kinsy, MIT), all Go.
**Nothing starts until you type `/music` or press `alt+m`.**

| Command | What it does |
|---|---|
| `/music`, `alt+m` | Opens the full-screen player: search, library, queue, transport, volume, shuffle, repeat, settings, cover art. Esc or `q` hides it and the music keeps playing. |
| `/music setup`, `/music doctor` | Check that `mpv` and `yt-dlp` are installed and say what is missing. |
| `/music stop`, `/music pause`, `/music vol 50` | Quick commands that work without opening the player. |

The extension registers no tool, so the model is offered nothing. YouTube Music is the source: `yt-dlp` resolves tracks and a
detached `mpv` plays them, so the queue survives hiding the player and `/reload`. Neither program is bundled. The library tab
reads your signed-in browser's cookies only after you agree, once per browser; search and playback never carry them.

The same Package is selected by [`pig-with-batteries`](../pig-with-batteries/README.md), and installs on its own.

The built-in tools, models and ambient extensions and Skills stay at PiG's defaults.

## Run from a checkout

```sh
npm ci --ignore-scripts
npm run stage
pig piglet validate dist/staged/piglets/pig-music/piglet.yaml
pig --piglet dist/staged/piglets/pig-music/piglet.yaml
```

Build a Piglet Binary (needs a git checkout of the PiG source the `pig` was built from):

```sh
PIG_SOURCE_ROOT=/path/to/pig-git-checkout \
  pig piglet build dist/staged/piglets/pig-music/piglet.yaml --format binary --targets <os>/<arch> --out ./pig-music
```

The Binary fuses the extension (`extensionRealization: fused`). Linux/amd64 is the development default; no release is
published and no support is claimed.
