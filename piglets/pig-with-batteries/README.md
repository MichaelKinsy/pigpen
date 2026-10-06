# pig-with-batteries

The batteries-included composition combines default PiG with curated extensions.
**Source only, not a finished composition or published release.** Today it selects
these batteries, all explicit (nothing starts or sends anything by itself):
the [herdr reporter](../../components/herdr/README.md), which reports PiG's `idle`,
`working` and `blocked` state to herdr and does nothing outside a herdr pane; two pig
games (below), which start only on an explicit command and draw the pig chosen with PiG's
built-in `/sprite`; the
[Pig Snake game](../../components/pig-snake/README.md), which does nothing until you type
`/pig-snake`; [`session-ingest`](../../components/session-ingest/README.md), one tool
(`read_session`) that lets the model query session transcripts in bounded pages;
[`context-info`](../../components/context-info/README.md), the `/context`, `/tools`,
`/cost` and `/prompts` commands (its status footer stays off unless PiG starts with
`--context-footer`); the [`pig-doctor`](../../components/pig-doctor/README.md) command
(nothing runs automatically); [`jev`](../../components/jev/README.md), a typed judge for tool calls that is
off until you run `/jev on`; the [a2a Package](../../components/a2a/README.md), which adds the
`a2a_agents`, `a2a_send` and `a2a_task` tools and `/a2a`, and opens no listener unless
`a2a.json`, `PIG_A2A_LISTEN` or `--a2a-listen` configures one (it then refuses to start without
authentication tokens); and the [`websearch`](../../components/websearch/README.md) first slice
(`web_search`, `fetch_content`, `get_search_content`, `source_check`, `web_enable`); and [`pig-music`](../../components/pig-music/README.md), a full-screen YouTube Music player that does nothing until you type `/music`. The owner approved the
[roadmap](../../docs/plan/pigpen-roadmap.md); the rest of its capability ports are not in this composition yet.

- `piglet.yaml`: authored composition input. Its name, description, release
  version, Resource selections, and build targets feed staging and the catalog.
- `catalog.json`: presentation-only fields that do not belong in PiG's closed
  manifest schema (status, featured flag, review date, languages, caveats).
- `extensions/`, `skills/`, `prompts/`: owned resource source directories.

Keep `status: planned` until curation, validation, source distribution and signed
releases work. `release.version: 0.1.1` reserves a development version; it does
not assert that a release exists. The index generator deliberately does not
turn build targets into supported binary platforms.

Local inspection after building a compatible PiG:

```sh
npm run stage
pig piglet validate dist/staged/piglets/pig-with-batteries/piglet.yaml
pig --piglet dist/staged/piglets/pig-with-batteries/piglet.yaml
```

## What it selects

| Extension | Origin | Purpose |
| --- | --- | --- |
| `herdr` | `package:herdr` (`local:../../components/herdr`, materialized by `npm run stage`) | Reports agent state to herdr. Same selection as the optional [`herdr` Piglet](../herdr/README.md), which documents what it reports. |
| `pigrunner` | `package:pig-runner` | `/runner`, `/pig-runner`: PiG Runner. Registers commands only. |
| `angrypigs` | `package:angry-pigs` | `/angry-pigs`: Angry Pigs. Registers commands only. |
| `pig-snake` | `package:pig-snake` (`local:../../components/pig-snake`, materialized by `npm run stage`) | The snake game with a herd of pigs. Registers only the commands `/pig-snake` and `/snake`: no event handler, tool or flag, no timer or output until a user runs one. Needs an interactive terminal; in print, JSON and RPC mode it only says so. Pure Go, so it fuses into the Binary. |
| `session-ingest` | `package:session-ingest` (`local:../../components/session-ingest`) | Adds the `read_session` tool. Idle until the model calls it. |
| `context-info` | `package:context-info` (`local:../../components/context-info`) | Adds `/context`, `/tools`, `/cost`, `/prompts`, `/context-footer` and `Ctrl+Shift+I`. No footer or status change unless started with `--context-footer`. |
| `pig-doctor` | `package:pig-doctor` (`local:../../components/pig-doctor`) | The [`/doctor` command](../../components/pig-doctor/README.md): finds cruft in a PiG setup (duplicate or broken extensions, unpruned caches, orphaned agent directories, legacy files) and fixes it with a confirmation per group and a backup. Command only: `tools: []` leaves the Package's read-only `pig_doctor` tool off in every mode (interactive, RPC, print and JSON), and it registers no event handlers. PiG 0.3.x ignored that empty scope in print and JSON mode (`pig -p`, `--mode json`); PiG 0.4.0 applies it (`npm run test:tool-scope`). |
| `jev` | `package:jev` (`local:../../components/jev`; its shared client library is the `typesafe` Package) | A typed judge for tool calls and output. **Off by default**: it judges nothing and sends nothing until you run `/jev on` (which shows what leaves the machine and where) or set `"enabled": true` in your own `pi-jev.json`. It fails open. See the [Package README](../../components/jev/README.md). |
| `a2a` | `package:a2a` (`local:../../components/a2a`, materialized by `npm run stage`) | The `a2a_agents`, `a2a_send` and `a2a_task` tools and `/a2a`: calls remote A2A agents and can serve PiG tasks to them. **No listener** unless `a2a.json`, `PIG_A2A_LISTEN` or `--a2a-listen` configures one, and a listener refuses to start without authentication tokens. Same selection as the [`a2a` Piglet](../a2a/README.md); configuration is in the [Package README](../../components/a2a/README.md). |
| `websearch` | `package:websearch` (`local:../../components/websearch`, materialized by `npm run stage`) | Web search, page fetch, retained sources and bounded retrieval: tools `web_search`, `fetch_content`, `get_search_content`, `source_check`, and the loader `web_enable` (dormant until the model asks). A Go port of [pi-web-access](https://github.com/nicobailon/pi-web-access), **first slice only**: deferred features are listed in [`PORT.md`](../../components/websearch/port/PORT.md). It reads `web-search.json` from the agent directory. With nothing configured, a search goes to the keyless Exa MCP (`mcp.exa.ai`), as in the original; no paid provider is called without configuration (see its README). |
| `pig-music` | `package:pig-music` (`local:../../components/pig-music`, materialized by `npm run stage`) | `/music`: a full-screen YouTube Music player (search, queue, library, cover art and a palette that follows the track) and quick commands (`/music play`, `pause`, `now`, `queue`, `vol`...). **Off until you type `/music`** (or a `/music` subcommand or `alt+m`): at startup it spawns no mpv or yt-dlp, makes no network call, reads no cookies, and shows no footer, widget or timer; it registers no tool (`tools: []`), so the model is offered nothing. At startup it only reads its `settings.json` and looks for its own mpv socket: if an mpv from an earlier session is **already playing** (`stopOnExit` off, a crash, another PiG), it follows it and shows the track in the footer, as after a `/reload`. Needs **mpv** (0.35 or later) and **yt-dlp** with a JS runtime on the machine: inside PiG, `/music` and its quick commands always play through mpv. The pure-Go native engine (WaxTap, WaxFlow, oto) is used only by the separate `pigmusic` command (`engine` in `settings.json` applies to it), not by `/music`, and it is **not** in this Binary: it lives in `pigmusic` (build it with `go build` in `components/pig-music/extensions/pig-music/cmd/pigmusic`; see the Package README). The first `/music` runs the doctor, which says what is missing with one copy-paste fix per OS; `/music setup` also offers to download yt-dlp and Deno into its own data directory, only with your consent (never sudo). The Library tab reads your browser's **cookies** only after a one-time consent per browser (`cookieBrowser` in its `settings.json`), for the library and liked-song lists only, kept in memory. Pure Go, fused into the Binary (about +1 MB); the music keeps playing in mpv across `/reload`, and `/quit` stops it (`stopOnExit`, on by default). |
| `seed-check` | `local:./extensions/seed-check` | An empty Go factory with no tools, commands or hooks: a build-pipeline fixture, not a battery. |

**Games do not start by themselves.** Bundling is not activating: `pigrunner` and
`angrypigs` register a command each and no event handler (a test per Package checks that
loading them makes no host call), so nothing plays, draws or ticks until you type the
command. The pig they draw is the shared `pig-play` library Package, staged alongside them.
Their Go code moved from PiG Standard (Michael Kinsy, MIT); credits are in each Package.

Selecting the Package needs no copy: `components/herdr` stays the single owner of
the reporter source. Omitted tools use PiG's defaults. Discovery is empty, so a
maintainer's own extensions and skills are not picked up; the reporter comes from
its Package, not from discovery. (The `herdr` Piglet keeps `user` and `workspace`
discovery because it is only a reporter.)

**Every extension is Go.** The owner rule is that a Piglet's extensions are Go SDK
extensions, so herdr is a Go extension and `pig piglet build
dist/staged/piglets/pig-with-batteries/piglet.yaml --format binary --out <path>`
builds a Binary with the reviewed PiG (set `PIG_SOURCE_ROOT` to a git checkout of
it). `seed-check` is no longer what lets the build succeed; it is kept only so the
composition still has a second, empty Go member until the owner decides to drop it.
See the root [release blockers](../../RELEASE-BLOCKERS.md) before publication.

Pig Snake fuses like the others: its Package is pure Go and the composition builds a Piglet Binary
with PiG 0.4.0 (evidence in
[`components/pig-snake/port/PORT.md`](../../components/pig-snake/port/PORT.md)).
