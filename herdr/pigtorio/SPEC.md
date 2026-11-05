# Pigtorio specification

Status: first playable build, 2026-11-05. This spec describes what Pigtorio is, how it works, what is built and verified, and what comes next. File-level contracts live in `ARCHITECTURE.md`; the design brief is `DESIGN.md`; the plan summary is `PLAN.md`.

## 1. What it is

Pigtorio is a layout and mod for the [Herdr app](https://herdr.dev) plus a small Go helper. It turns the agents you run in Herdr into a Factorio-style farm:

- a **pen** is a Herdr space (a repo or folder);
- a **pig** is an agent that reports its state with OSC 7501 (PiG, Claude Code, Codex, a mock pig);
- a **crate** is a task with a Mandate (who asked, done-when);
- a **belt** carries a pig's finished hand-off to the next stop as a new crate;
- **stations** gate the work: the **Oven** runs the pen's tests, the **Review Station** waits for a person, the **Barn** commits and, with a remote, pushes a branch and opens a pull request.

Every game verb runs a real Herdr or herdr-core action. Nothing on screen is decoration over fake state.

## 2. Goals and non-goals

Goals:
1. Run real multi-agent work (plan → build → test → review → ship) with less typing and copying than tabs and terminals.
2. Make the state of many agents readable at a glance, blocked first.
3. Be a game: build, watch it run, find the jam, fix it.
4. Stay a pure Herdr surface: a layout, one mod, a helper started as a herdr-core terminal.

Non-goals for this build: multiplayer, a game engine outside Herdr, cloud runners, scoring people.

## 3. The player's model

| Thing | Herdr / hc action underneath |
|---|---|
| Pen | `spaces()`; the Pen tool runs `space.new {folder}` after a folder pick |
| Pig | `agents()` + live terminals with an OSC 7501 status; adopting runs `tab.new {space, argv}` |
| Talk | `openOverlay({kind: "window", layout: {terminal}})`: the live terminal over the farm |
| Crate | `hc send` of a brief (title, Mandate, previous hand-off, hand-off instructions) |
| Belt | the engine sees the pig finish (`turn` up, state done/idle), reads its last assistant message, sends it on |
| Oven | `sh -c <pen test command>` in the pen's folder, 10 min limit; exit 0 green, else burnt |
| Review Station | the crate card (Mandate, gates, hand-off, diff incl. new files); Approve / Send back |
| Barn | `git add -A`, commit on `pig/<crate>` when on the default branch; push + `gh pr create` only with `origin` and `gh` and a new commit; only after a person approves |
| Bell | fixed order: blocked, burnt, holding, done, stuck; opens the right place |
| Runt | a guide pig: scripted hints (free) and "Ask the Runt", a read-only PiG agent that drafts ghosts |

## 4. Views

- **Map**: the whole farm on a tile grid (32 px tiles at zoom 1). Drag the ground to pan, wheel to zoom. Bell and Production panels at the top right.
- **Walk**: the same world zoomed in, the camera on the Swineherd (the player). Click ground to walk; click a pig to walk over and open its menu.
- **Terminals**: Herdr's own spaces sidebar, agents list, tab bar and the active tab.
- **Job Board**, **Adopt-a-Pig**, **Workshop** (looks and roles), **Report** (the morning report).

## 5. Building (Factorio style)

The hotbar holds: Hand, Pen, Pig, Belt, Oven, Review, Barn, Job Board, Remove, Runt.

- With a building tool, its ghost follows the cursor on the grid, outlined green where it fits and red with the reason where it does not (stacking, straddling a fence, pens touching).
- Left-click places. Pens ask for a folder and become Herdr spaces.
- Belt: click a pig or station, then the next stop. Rules: both ends in one pen, one belt in and one out per end, no loops, nothing after a Barn. Belts are drawn as an L, down first when the next stop is lower.
- Hand: click to use (pig menu, Job Board, Review, Barn, Runt), drag a building or pen to move it.
- Remove (or right-click a station or belt): takes it away. Pens are spaces and are closed in Terminals, not removed on the map.
- Right-click: a pig or pen opens its menu; open ground opens "Build here"; with a tool in hand, it puts the tool away.
- Line edits are refused while crates ride that pen's belt (crates hold their place by stop index).

The world lives in `farm.json` (`entities`, `belts`); `lines` for the engine are derived from belts. Every space gets a pen automatically the first time it is seen; pens of closed spaces are pulled up.

## 6. The Runt

- **Scripted guide**: hints from the farm's state (no pens, no pigs, no belt, no Oven, no test command, dead-end line, crates waiting, blocked/burnt/holding), each with a Do-it action that drafts ghosts rather than building.
- **Ask the Runt**: starts `pigtorio-runt`, a PiG agent with read tools and bash, briefed to run only `pigtorio farm show` and `pigtorio ghost …`. It drafts buildings, belts and crates into `ghosts.json`.
- **Check before it runs**: drafts show as ghosts; the panel runs the check (every line ends at a Review or Barn, nothing overlaps, belts valid). Nothing is built until a person presses BUILD.

## 7. Architecture

- **Mod** (`mods/pigtorio`, plain JS on gpui-kit): draws and asks. One mod, roles by `props().role` (bar, farm, footer, menus, dialogs, Talk title bar, Runt panel).
- **Engine** (`pigtorio engine`, Go, stdlib only): owns crates, belts' motion, Oven, Review state, Barn, crate log; follows `hc events` and reconciles with `hc ls`. Started by the mod as herdr-core terminal `pigtorio-engine`, restarted if it dies.
- **Files** under `$HERDR_APP_HOME/pigtorio/`: `farm.json` (mod), `inbox/*.json` (mod → engine commands), `state/state.json` (engine → mod, bounded, compact), `crates.jsonl` (event log), `ghosts.json` (drafts), `sessions/` (PiG sessions of pigs Pigtorio started).
- **CLI**: `pigtorio crate add`, `farm show`, `ghost …`, `fakepig` (a mock agent for demos and tests), `install [--keys]`.

## 8. Safety rules

- Pigs never hold push credentials; the Barn pushes only after a person approves, never to the default branch, and only a new commit.
- Briefs go only to terminals reporting an agent status of idle or done (never a plain shell), checked fresh before sending.
- Herdr's keyboard stays on the focused terminal under the farm; Pigtorio's chords live in an opt-in `layout:pigtorio` keys section (`install --keys`), because any edit of `keys.json` stops Herdr refreshing its defaults.
- The installer copies only the mod, the layout and its preview, and the helper binary; it refuses to copy a home onto itself.

## 9. Verified in this build

Sandbox Herdr (private home and core), real mouse and keyboard events:
- three mock pigs → Oven → Review → Barn shipped a commit; Oven burn returns the log and holds after two burns;
- a crate handed to one pig; refusals shown as toasts;
- adoption of mock and real PiG pigs; Workshop looks and roles;
- self-hosting: real PiG pigs (Surveyor, Builder on claude-sonnet-5.5; Inspector on gpt-6.1-sol) built Pigtorio's help panel through its own belt, approved at Review, committed by the Barn;
- building: pen via the folder picker, stations placed, moved by drag, removed by right-click, belts laid pig → pig → Oven → Review → Barn, lines derived;
- tests: `bun test` (52), `go test ./...` (engine, install, farm CLI, fakepig).

## 10. Known limits

- The Runt panel and Ask the Runt have unit tests but no end-to-end run in Herdr yet.
- Belt routing is a single L; long belts can cross buildings.
- One belt out per end: no Splitter yet.
- `git add -A` in the Barn commits everything in the folder (the Review diff shows it all).
- CPU was measured for the Herdr process only, not WindowServer/GPU.

## 11. Next

1. End-to-end Runt: ask → ghosts → check → BUILD.
2. Splitter: worktrees as lanes, file locks, merge funnel re-running the Oven.
3. Pests: GitHub issues as weeds/pests feeding the Job Board.
4. Scores and the morning report from `crates.jsonl` (cost per truffle, gates passed first try).
5. Strays round-up (hand-off, salvage branch, park, keep session).
6. Tutorial farm on mock pigs.
7. Timelapse capture, herdr.dev listing, `RECIPE.md`, preview art.

## 12. Open decisions

Licence, public name, character sprites in public material, minimum Herdr/PiG versions, who may run `install` into a shared home, and how the project describes its HPE relationship.
