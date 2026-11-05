# Pigtorio architecture

Pigtorio is a Herdr app layout plus one mod (`pigtorio`) and a Go helper (`pigtorio`). The mod draws and asks. The helper owns crates, lines (belts), stations and the crate log. They talk through files under the Herdr home.

## Repository

```
mods/pigtorio/          the Herdr mod (copied whole into $HERDR_APP_HOME/mods/pigtorio)
  main.js               View: routes props().role to a view
  model.js              pure logic (no herdr imports): pens, pigs, Bell order, layout math
  *.js                  views (map, walk, board, adopt, review, report, menu, talkbar, workshop)
  art/                  generated: sprites/<skin>/<state>.webp, props/*.png, tiles/*, ui/*
  fonts/Silkscreen-Regular.ttf
layouts/pigtorio.json   the layout
art/                    art sources and generators (cmd in engine/cmd/artgen), gen.sh
engine/                 Go module `pigtorio` (go 1.26): cmd/pigtorio, internal/...
test/                   bun tests for model.js; e2e scripts for the sandbox
scripts/                sandbox.sh (private Herdr + core), install.sh
```

## Homes

- `H` = `$HERDR_APP_HOME`, default `~/.config/herdr-app`.
- Data dir `D` = `H/pigtorio/`.
- Sandbox: `HERDR_APP_HOME=/tmp/hfarm/home`, core `HC_SESSION=hfarm HC_SOCKET=/tmp/hfarm/rt/hc.sock XDG_RUNTIME_DIR=/tmp/hfarm/rt XDG_STATE_HOME=/tmp/hfarm/state XDG_CONFIG_HOME=/tmp/hfarm/cfg`. Never point tests at the real home or the default core.

## Files in D

| file | writer | reader | content |
|---|---|---|---|
| `farm.json` | mod | engine, mod | pen layout, pig names/roles/skins, lines |
| `inbox/<ms>-<rand>.json` | mod, `pigtorio crate add` | engine (deletes after handling) | one command |
| `state/state.json` | engine (atomic tmp+rename) | mod (`watchFolder(D/state)`) | crates, engine heartbeat, stats |
| `crates.jsonl` | engine (append) | engine, report | one event per line |
| `sessions/<pig>/` | PiG (`--session-dir`) | engine | session JSONL of pigs Pigtorio started |
| `engine.lock` | engine | engine | pid; one engine per home |
| `ghosts.json` | `pigtorio ghost …` (the Runt), mod (Build/Discard) | mod | drafted buildings, belts and crates, not yet built |

### farm.json

```json
{
  "pens": { "<spaceId>": { "test": "go test ./...", "fence": 4 } },
  "pigs": { "<address>": { "name": "Sandy", "role": "builder", "skin": "sandy" } },
  "lines": [
    { "id": "l1", "pen": "<spaceId>", "stops": [
      { "kind": "pig", "pig": "t3" },
      { "kind": "pig", "pig": "t4" },
      { "kind": "oven" },
      { "kind": "review" },
      { "kind": "barn" }
    ] }
  ],
  "swineherd": { "skin": "pink" }
}
```

A line is an ordered list of stops: the belt runs between consecutive stops. `pen` gives the folder (the space's `folder`, or the first pig's cwd) and the Oven's test command. Unknown keys are kept.

### The world (`entities`, `belts`)

The farm is a Factorio-style world on a tile grid (`TILE` = 32 px at zoom 1). The mod owns it; `mods/pigtorio/farmworld.js` is its only writer of `entities`, `belts` and `lines` (pure functions, tested in `test/farmworld.test.js`).

```json
{
  "entities": {
    "pA1": { "id": "pA1", "kind": "pen", "x": 2, "y": 6, "w": 14, "h": 8, "space": "<spaceId>" },
    "o9": { "id": "o9", "kind": "oven", "x": 4, "y": 11, "w": 2, "h": 2, "pen": "<spaceId>" },
    "jobboard": { "id": "jobboard", "kind": "jobboard", "x": 2, "y": 1, "w": 4, "h": 3 },
    "barn": { "id": "barn", "kind": "barn", "x": 34, "y": 1, "w": 3, "h": 3, "pen": null },
    "runt": { "id": "runt", "kind": "runt", "x": 7, "y": 2, "w": 1, "h": 1 }
  },
  "belts": [ { "id": "b1", "from": "pig:t3", "to": "ent:o9" } ]
}
```

- Kinds and sizes (tiles): `pen` 14×8, `oven` 2×2, `review` 2×2, `barn` 3×3, `jobboard` 4×3, `runt` 1×1. Pens never touch; stations stand inside a pen (then `pen` is its space) or on open ground; nothing straddles a fence or stacks.
- Belt ends are `pig:<address>` or `ent:<entity id>` (Oven, Review, Barn only). Both ends stand in the same pen; one belt out and one in per end; no loops; nothing leaves a Barn.
- `lines` is derived from `belts` after every change (`deriveLines`): one line per chain, id `l-<first end>`, stops `{kind:"pig", pig}` or `{kind, entity}`. The engine reads `lines` as before.
- Every Herdr space gets a pen the first time the mod sees it; the Job Board, a Barn and the Runt are placed once (`settle`). A pen whose space closed is pulled up with its stations and belts (`prune`). An old farm with `lines` and no `belts` is converted once (`migrateLines`).

### ghosts.json

Drafted changes that nothing builds until a person presses Build. `pigtorio ghost …` (used by the Runt) appends; the mod applies them through `farmworld.js` and then clears the file.

```json
{
  "note": "why the Runt drafted this",
  "by": "runt",
  "ghosts": [
    { "id": "g1", "op": "place", "kind": "oven", "pen": "<spaceId>", "near": "pig:t4" },
    { "id": "g2", "op": "place", "kind": "review", "pen": "<spaceId>", "x": 9, "y": 11 },
    { "id": "g3", "op": "belt", "from": "pig:t4", "to": "ghost:g1" },
    { "id": "g4", "op": "crate", "crate": { "title": "…", "doneWhen": "…", "text": "…", "size": "one-shot", "pen": "<spaceId>" } }
  ]
}
```

`place` takes `x`/`y` (tiles) or `near` (a pig or entity ref) or neither (a free spot in the pen). Belt ends may name a ghost (`ghost:<id>`) placed in the same draft. Writers replace the file atomically (temp + rename).

### Commands (inbox)

Every command is one JSON object with `op`. The engine answers in `state.json` `acks` (last 50: `{id, op, ok, error}`), where `id` is the command file's base name.

| op | fields | effect |
|---|---|---|
| `crate.add` | `crate` (`title`, `text`, `doneWhen` required; `size` one-shot/large, `pen`, `afk`, `blockedBy` []) | new crate on the Job Board, `state: "board"` |
| `crate.update` | `id`, `patch` | edit a board crate |
| `crate.remove` | `id` | remove a board crate |
| `crate.hand` | `id`, `to` (address) | send the crate to one pig; `state: "working"`, `at: {kind: "pig", pig}` |
| `crate.run` | `id`, `line` | put the crate on a line at stop 0 |
| `review.approve` | `id` | the crate leaves Review for the next stop (the Barn) |
| `review.sendback` | `id`, `note` | return belt: back to the last pig stop with the note |
| `barn.ship` | `id` | run the Barn for a crate waiting at the Barn |
| `oven.run` | `id` | (re)run the Oven for a crate at the Oven |

A crate with an empty `doneWhen` is refused (`ok: false`): it cannot leave the board.

### state.json

```json
{
  "engine": { "pid": 123, "started": 1793837000000, "beat": 1793837061000, "version": "0.1.0" },
  "crates": [ {
    "id": "c7", "title": "...", "text": "...", "doneWhen": "...", "size": "one-shot", "pen": "<spaceId>",
    "afk": false, "blockedBy": [], "askedBy": "you", "created": 1793837000000,
    "state": "board|working|blocked|oven|burnt|holding|barn|shipped|failed",
    "line": "l1", "stop": 1, "at": { "kind": "pig", "pig": "t4" },
    "burns": 0, "report": "last hand-off text", "log": "last oven log tail",
    "pr": "https://...", "commit": "abc123", "tokens": 0, "cost": 0,
    "folder": "/path/to/repo", "updated": 1793837061000,
    "history": [ { "t": 1793837000000, "what": "Slate plans", "detail": "..." } ]
  } ],
  "pigs": { "t4": { "crate": "c7", "since": 1793837000000, "status": "working", "statusSince": 1793837010000 },
            "t5": { "status": "blocked", "statusSince": 1793837040000 } },
  "acks": [ { "id": "1793837000000-ab12", "op": "crate.add", "ok": true } ],
  "stats": { "cratesIn24h": 0, "trufflesOut24h": 0, "burnt24h": 0, "tokens24h": 0 }
}
```

### crates.jsonl

One event per line: `{"t": ms, "crate": "c7", "event": "added|handed|done|moved|oven-green|oven-burnt|returned|holding|approved|sent-back|shipped|failed|blocked", "pig": "t4", "detail": "...", "tokens": 0, "cost": 0}`. `tokens` and `cost` (omitted when 0) ride on `done` events and feed the 24 h stats. A crate on a line logs `moved` (with `pig` for pig stops) each time it enters a stop; a crate handed to one pig logs `handed`, `done`, `holding`.

## Engine behaviour

- `pigtorio engine --home H` runs until killed. It holds `D/engine.lock` (an `flock`, so a lock file left by a dead engine is harmless: the new engine comes up); a second live engine exits 0 with a message. The mod starts it when the heartbeat is older than 30 s: `hc run --bg --no-parent --name pigtorio-engine -- H/pigtorio/bin/pigtorio engine --home H` (no `--keep`: a lost engine is restarted by the heartbeat check, not replaced by a shell).
- It follows `hc events --json` (reconnects on exit) and reconciles with `hc ls --json` every 5 s. A pig's status is `terminal.status.state`; `turn` increments when work ends.
- The engine's own `hc` calls: it uses the inherited environment. A terminal started by the core carries `HC_SOCKET`, `HC_TERMINAL_SOCKET` (the same socket), `HC_SESSION`, `HC_TERMINAL_ID`, `XDG_*` and `HERDR_APP_HOME` of the core (verified in the sandbox core with `hc run --bg -- env`), so an engine started by the mod reaches the same core without configuration. If only `HC_TERMINAL_SOCKET` is set, the engine sets `HC_SOCKET` from it. `hc` is found on `PATH`, else `~/.local/bin/hc`.
- Liveness: `hc ls --json` gives `status` as a string (`working|idle|done|blocked`, null when the terminal reports none) and `turn`; the turn also increments when a pig leaves `working` for `blocked`. A crate finishes its pig stop when the pig is `done`/`idle` and its turn is higher than when the brief was sent. A busy pig (working, blocked, or holding another crate) keeps further crates waiting at its stop. A pig that disappears from `hc ls` twice in a row fails its crate.
- The folder of a crate is the pen's optional `folder` in `farm.json`, else the cwd of the first pig stop of its line, else of the pig it was handed to.
- A pig with a crate that leaves `working` for `done` or `idle` (turn increased) has finished its stop: the engine reads its report, records it and moves the crate to the next stop. `blocked` marks the crate `blocked` until the pig works again.
- Report read path, first that yields text: (1) newest `*.jsonl` in the pig's `--session-dir` (from its argv) or `D/sessions/<address>/`; (2) newest PiG session in `~/.pig/agent/sessions/<cwd slug>/` modified after the crate reached the pig; (3) `hc read <t> --lines 80`. The report is the text parts of the last `message` whose `message.role` is `assistant`. Token totals come from `message.usage` of assistant messages after the crate reached the pig.
- Stops:
  - `pig`: the brief goes only to an agent that reports `idle` or `done` and whose foreground is not a shell; never to a terminal with no status, `working` or `blocked`. The engine takes a fresh `hc get` right before sending and uses `hc send --if idle` when the status is `idle` (`--force` never). Sends run off the main loop (a slow `hc send` never stalls the heartbeat). A crate whose pig cannot take it stays queued; a pig that is gone, or no longer an agent (no status), for two reconciles in a row and 8 s fails the crate (`failed`, with the reason in its history), also before the brief was sent. The message is the crate brief: title, Mandate (asked by, done-when), text, the previous stop's hand-off (the crate's `report`), and the instruction to end with a hand-off of Goal / Done / Unverified / Files touched.
  - `oven`: runs the pen's `test` with `sh -c` in the crate's folder, 10 min limit. Exit 0 is green (next stop). Otherwise burnt: the crate goes back to the previous pig stop with the last 60 lines of output; after 2 burns it is held (`state: "burnt"`) for the person. A pen with no test command is never green: the crate is held as `burnt` with `log` = "No test command: set one in the pen menu (Oven's test command)" (event `oven-burnt`, detail `no test command`); `oven.run` works once a command is set.
  - `review`: `state: "holding"` until `review.approve` or `review.sendback`.
  - `barn`: waits for `barn.ship` unless the previous stop was an approved review. In the folder: `git add -A`; if something changed: when HEAD is `main`, `master`, the branch `origin/HEAD` points to, or detached, it switches to a new branch `pig/<crate-id>` first (a default branch is never committed to or pushed), then `git commit -m "<title>"`. Only when a new commit was made, `origin` exists and `gh` is installed: `git push -u origin <branch>` and `gh pr create --fill`. With nothing to commit it ships with the note "nothing to commit" (no commit, no push, no PR; `commit` stays empty). Records commit and PR URL; `state: "shipped"`. One Barn job runs per crate: `barn.ship` is refused while it runs, and a result for a crate no longer at the Barn is ignored.
- Every change writes `state.json` and appends `crates.jsonl`. The heartbeat updates every 15 s.
- `state.json` `pigs` lists every live terminal that reports a status: `status` (the word) and `statusSince` (ms). `statusSince` comes from `hc get <t> --json` `status_ms` the first time the engine sees the terminal (and after an engine restart), then from the time the engine notices each change of the word. `crate`/`since` are present only for a pig that holds a crate. Terminals that are gone or report no status have no entry.
- `state.json` stays small (the mod reads at most 1 MiB): compact JSON; at most the 60 most recent `shipped`/`failed` crates (the full story stays in `crates.jsonl`; if the file would still pass 900 kB, the oldest finished crates go first); `report`, `log` and `note` text capped at 4000 characters; at most 100 history lines per crate. The hand-off is stored once, as `report`. Every crate carries `folder` (where the Oven and Barn work; empty while unknown) and `updated` (ms of its last change).

## CLI

- `pigtorio engine --home H`
- `pigtorio crate add [--home H] --title T --done-when W [--text X] [--pen ID] [--size one-shot|large] [--afk]` (writes an inbox command)
- `pigtorio farm show [--home H] [--json]`: what the Runt reads. Pens (space id, name and folder from `herdr-app ls` run with `HERDR_APP_HOME=H`; omitted with a warning when no window answers), each pen's entities and Oven test command, its pigs (address, name/role from farm.json, live status from `hc ls`, `gone` when not live; a pig belongs to the pen of the space holding its terminal, else of its line), open-ground entities, belts, lines, crates on the board / moving (`working`, `oven`, `barn`) / needing a person (`blocked`, `burnt`, `holding`, `failed`) with titles and states only, and the current ghosts. Default is short text; `--json` prints the same data.
- `pigtorio ghost place --kind oven|review|barn|pen|jobboard [--pen SPACE] [--x N --y N | --near pig:<addr>|ent:<id>|ghost:<id>]`, `ghost belt --from REF --to REF` (REF = `pig:<addr>` | `ent:<id>` | `ghost:<id>`; an end is a pig, Oven, Review or Barn; nothing leaves a Barn), `ghost crate --title T --done-when W [--text X] [--size one-shot|large] [--pen SPACE]`, `ghost note TEXT`, `ghost list [--json]`, `ghost clear` (all take `--home H`, and `--by NAME`, default `runt`). Each add prints the new ghost id (`g<n>`, one past the highest in the draft). Refused, writing nothing: unknown kind, `--x` without `--y`, `--near` with `--x/--y`, a pig address missing from `hc ls`, an `ent:` id missing from farm.json `entities`, a `ghost:` ref missing from the draft, a crate without `--done-when`, a `--pen` space with no pen entity (a `pen` ghost instead needs a space with none). The draft is read, changed and replaced under an flock on `D/ghosts.lock` (temp file + rename); `clear` deletes `ghosts.json`. Flags may follow the text.
- `pigtorio fakepig [--session-dir DIR] [--name N] [--work 2s] [--home H]`: a mock agent for tests and the tutorial. Speaks OSC 7501 (`ESC ] 7501 ; state=working:app=pig ESC \`), reads a message (input until 300 ms of quiet; bracketed-paste markers are stripped), works, writes PiG-shaped session JSONL with an assistant reply and usage, then reports `done` (then `idle` after 2 s). A message containing `ASK:` blocks with `kind=question` until the next message. A message containing `FAIL` creates no change. It writes a file `pigtorio-fake-<n>.txt` (n counts its messages from 1) in its cwd for each message so the Barn has something to commit. Without `--session-dir` the session goes to `D/sessions/$HC_TERMINAL_ID/`.
- `pigtorio install [--home H] [--src DIR] [--keys]`: copies `mods/pigtorio` and `layouts/pigtorio.json` into H, and the running `pigtorio` binary to `H/pigtorio/bin/pigtorio` (the mod starts the engine from there). The mod is copied into a staging folder under `H/pigtorio/.install` and swapped in by rename, so a failed copy leaves the installed mod as it was. Real copies. Refuses, changing nothing, when `H/installed.json` has the key `pigtorio` or `layout:pigtorio` (names Herdr ships), when the source mod folder is (or resolves through symlinks to, or overlaps) the installed one, and with `--keys` when `H/keys.json` does not exist (it never creates a keys.json holding only our section). `--src` defaults to the repo found next to the binary (`bin/../..`) or the cwd, never a folder inside H.

## Mod

- One mod, roles by `props().role`: `bar` (top bar), `farm` (Map/Walk/Board/Adopt/Workshop/Report views), `footer`, `menu` (pig popup), `talkbar` (title bar of a Talk window), `review`, `report`, `help` (the Pigpedia dialog: keys and what each verb runs).
- Mod commands (`herdr-app do pigtorio.<cmd>` and keys): `map`, `walk`, `board`, `adopt`, `workshop`, `report`, `bell`, `find`, `help`, `calm`, `terminals`.
- The Bell order: blocked, burnt crates, holding (review), done, stuck (working > 2 h), idle.

## Look (from the storyboard)

Chrome `#0f1512`, raised `#1a2620`/`#182420`, border `#223129`/`#2b3d33`, text `#e6ece8`, muted `#8fa39a`/`#aab8b0`, accent `#4ed1a1`, warn `#e3a63b`, danger `#e05a4f`. Grass `#2f6b45` with a 16 px grid of `rgba(0,0,0,.06)` 2 px lines. Pen: fill `#35804f`, 6 px border `#9a6a3a` with 3 px inner `#5e3f22`; a blocked pen's border is `#e05a4f`. Pen tag: Silkscreen 11 px `#e6ece8` on `#0f1512`. Pig tag: 11 px on `rgba(15,21,18,.55)`. River `#2c5f86`, bridge `#8b5a2b`. Barn `#8f2f2a`/`#4f1a17`, roof `#5e3f22`. Belt: `#3a3a3a`/`#6b6b6b` 12 px stripes, 2 px `#222` edges. Crate `#8a5a3a` border `#5e3f22`. Oven `#5a2a1a`/`#2e1510` text `#ffb347`. Review `#1f3d5a`/`#10202f` text `#9ec9ee`. Job Board sign `#8b5a2b` border `#5e3f22` text `#f3e3c3`. Menu popup: cream `#f6f0e6`, selected `#e05a4f` white text.
