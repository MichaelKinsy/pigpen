# Pigtorio

Pigtorio is a game you play inside the Herdr app, and it is also a complete way to run coding agents. Your agents are pigs. Your repos are pens. You connect pigs with belts, and the belts carry real work from one agent to the next. Every action in the game runs a real Herdr or herdr-core command.

The pitch in one line: **Factorio, but every worker is a coding agent.**

## Goals

1. **Viral.** A first screenshot or 15-second clip must make a developer want it. The hook is "my agents ran a factory overnight."
2. **Highly functional.** Pigtorio must do everything a person needs to run many agents, at least as well as a plain Herdr layout: start, watch, answer, review, merge, and stop agents across machines. The game layer must never slow down a real action.
3. **On brand.** PiG's pixel pigs, its warm farm vocabulary, and animation better than the current sprites.
4. **A Herdr surface.** A layout and mods for the Herdr app, listed on herdr.dev. No pigstyd, no Tern.

## Who you play

Factorio's player is an engineer who walks the factory, carries items, and builds by hand. Factorio 2.0 added a remote view whose goal is "to be able to use remote view for everything you can do locally" ([FFF-380](https://factorio.com/blog/post/fff-380)). Pigtorio copies both halves:

- **The Swineherd** is your avatar. You design the Swineherd in the Workshop. You click the ground to walk, walk up to a pig to talk to it, and carry crates by hand. Walking gives presence, discovery, and the fun of the farm.
- **The Map view** is Factorio's remote view. You command any pig, pen, belt, or station from the overhead map without walking. Every keyboard shortcut and every palette command also acts at once.

Rule: walking is never required to act. The Swineherd is for feel; the Map view and the keys are for speed.

## Inspirations and what we take from each

| Inspiration | What it is | What Pigtorio takes |
|---|---|---|
| [Factorio](https://factorio.com) | Logistics and automation game | The core loop: build a system, watch it run, find the bottleneck, fix it. Belts, stations, research, remote view. |
| [Herdr Empires](https://github.com/cauldr0nx/herdr-empires) | Age of Empires theme for Herdr; Herdr's own account reposted it ("wololo-ing your coding agents") | A whole game look is possible on Herdr mods. Its notes on mod limits ([SPIKES.md](https://github.com/cauldr0nx/herdr-empires/blob/main/SPIKES.md)): no rotation, no per-frame scripts, measured CPU cost. |
| [Club Penguin / claude-penguin](https://github.com/Robert-Conover/claude-penguin) | Puffles as Claude Code agents in Club Penguin | Pets as agents, the adoption catalog (one page per model), click a pet to get a task card. |
| [pixtuoid](https://github.com/pixtuoid/pixtuoid) | Pixel-art office of coding agents in the terminal (548 stars) | Glanceable status as animation (`?` when it needs you, naps when done), token use shown as a growing pile, day and weather. It only watches; Pigtorio also acts. |
| [Ansenjeo's ASCII MMORPG](https://coop-land.ru/news/36972-anonsirovana-ascii-mmorpg-eto-igra-celikom-iz-ascii.html) | A fantasy MMORPG drawn in colored characters, trending in October 2026 | Light, wind, and weather make simple art feel alive. |
| Stardew Valley, Animal Crossing | Cozy life sims | Warm palette, seasons, a greeting with news each morning, collecting. |
| [Shepherdr](https://github.com/eliasstravik/shepherdr) | Mission control layout for Herdr (1,061 stars) | The functional bar: tasks in worktrees, review before merge, questions as cards, many machines. It is not a game. |

## The space

The Herdr app turned its window into files and mods, so anyone can rebuild it. The ecosystem is young and growing:

- 287 GitHub repos carry the `herdr-mod` topic and 138 carry `herdr-layout` (GitHub search, 2026-11-04). herdr.dev lists them.
- Herdr has about 33.8K followers on X and raised a $6M seed ([blog](https://herdr.dev/blog/herdr-raised-a-seed)).
- The most-starred layout, Shepherdr, is purely functional. The viral theme, Empires, is a reskin of Herdr's existing actions. pixtuoid is a viewer outside Herdr.

**The gap:** nothing yet combines a real game loop with real agent orchestration. Pigtorio's relays, stations, and splitters are new capabilities, not a skin. That is the claim to virality: the game makes multi-agent work easier to set up and easier to see.

## The world

A top-down 3/4 pixel world in PiG's style: a farm that grows into a factory.

| In the game | What it does in Herdr |
|---|---|
| **Pen** | A Herdr space: a repo or folder on a machine. Build one on open ground, pick a folder, and `space.new` runs. Pens on other machines sit across a river. |
| **Pig** | An agent. Its breed shows its model (Opus is a big Berkshire, Haiku a fast piglet). Its animation shows its state: digging while working, squealing at the fence when blocked, holding a truffle when done, napping when idle. |
| **Crate** | A task. Write one at the Job Board, or drop it on a pig to send it (`tab.new {task}` for a new pig, `hc send` for a running one). |
| **Belt** | A relay. When the upstream pig finishes, its final report rides the belt as a crate into the next pig. This builds real chains: planner, then coder, then reviewer. |
| **Splitter** | Git worktrees. One crate in, one branch and one pig per worktree out. |
| **Review Station** | Opens the diff of a finished pig. Approve, or send it back with a note. |
| **Test Oven** | Runs the pen's test command. Crates come out green or burnt. |
| **Barn** | Git: commit, push, and open a pull request. A delivered truffle is shipped work. |
| **Trough** | Tokens spent, filling and draining per pen. |
| **Squeal and red beacon** | A blocked agent. It always sorts first. The Bell key goes to it. |
| **Adopt-a-Pig** | The catalog of agents and models: `knownAgents()` and `pig --list-models`. One page per model: maker, context ("favorite toy: a 1M-token scroll"), output size, thinking, images. Adopt places a new pig in a chosen pen. |
| **Workshop** | The sprite builder for your Swineherd and your pigs. |

## The core loop

1. **Adopt** pigs into pens.
2. **Hand out** crates (tasks).
3. **Wire** belts between pigs and stations.
4. **Watch** truffles roll into the Barn; find the jam; fix it.
5. **Unlock** automations in the research tree: auto-relay, auto-retry, auto-review, night shift.

## Talking to a pig

Walk up and click, or click on the Map. A speech-bubble menu offers: **Talk** (its live terminal opens as a Herdr window over the farm, `openOverlay({kind: "window", layout: {terminal}})`, so you type to it in place), **Hand crate**, **Relay to…**, **Peek**, **Focus in tab**, **Rename**, **Nap** (settle its tab), and **Retire** (with confirm).

## The Workshop

- A layered pixel editor: body, spots, ears, snout, eyes, hat, tool, and palette.
- The 15 existing PiG skins as presets.
- From the parts, a generator builds 4-direction walk cycles and the state animations.
- A share code (a short string) so a pig travels through screenshots and posts.
- Breeds per model as defaults, which you can override per pig.

## Animation and rendering on Herdr

Measured and documented limits:

- No rotation and no per-frame scripts. Empires measured the cost of continuous motion ([SPIKES.md R15](https://github.com/cauldr0nx/herdr-empires/blob/main/SPIKES.md)).
- `.transition("left"/"top")` and `.loop(...)` move elements natively. Use them for walking and wandering.
- In the 2026-11-04 spike, animated WebP, GIF, and APNG in `image()` stayed on one frame (provisional; confirm with the window visible).
- `.frames(list, ms)` cycles text natively on one shared clock.

The plan: **layered pixel fonts.** A Go generator turns each animation into a font with one glyph per palette color per frame. The mod stacks one text layer per color, each tinted its color and cycling its glyphs with `.frames()`. All layers share one clock, so they stay aligned. This gives smooth 8 to 12 frame cycles with no script per frame. Static scenery (ground, buildings) stays as baked images.

Performance rules: stop motion when the farm is hidden or the window is inactive; a calm mode with no motion; no timer faster than 1 s; measure CPU with 4, 12, and 24 pigs before each release.

## Functional bar

Pigtorio is not done until a person can, without leaving it:

- see every agent on every machine and which ones need them, blocked first;
- start an agent of any installed kind and model in any repo;
- answer a blocked agent in its live terminal;
- review a diff, run tests, commit, and open a pull request;
- chain agents so one's result starts the next;
- run many tasks in parallel worktrees;
- find any pig or pen by key or palette in under two seconds.

## Virality plan

- **Factory timelapse:** one key records the day as a short GIF of the farm.
- **Share codes** for pigs and Swineherds from the Workshop.
- **Screenshot moments:** belts jamming at the slow pig, the morning report, the first truffle.
- **herdr.dev listing** with a `RECIPE.md` and a `preview.png`, and the `herdr-layout` topic.
- A launch clip: an empty field, three pens, a belt chain, a squeal, an answer, a truffle shipped as a pull request.

## Milestones

| Milestone | Delivers | Done when |
|---|---|---|
| M0 Spike | Layered-font animation and CPU in a sandbox Herdr | A pig walks and digs at 10+ fps; CPU measured at 4, 12, and 24 pigs |
| M1 Farm floor | Pens from spaces, pigs from agents, Swineherd click-to-walk, Map view, pig menu, live terminal window | Every agent shows; a blocked pig is reachable by key and by click |
| M2 Workshop and breeds | Sprite builder, presets, share codes, breeds per model, Adopt-a-Pig | A new pig of a chosen model starts in a chosen pen |
| M3 Relays and stations | Belts, Splitter, Review Station, Test Oven, Barn | A three-pig chain runs a task end to end into a pull request |
| M4 Farm life | Research tree, trough, morning report, seasons, sounds | Unlocks change behavior; calm mode costs near zero CPU |
| M5 Launch | Timelapse, RECIPE, preview, listing | Listed on herdr.dev |

## Out of scope

- pigstyd and the Tern plugin.
- Multiplayer between people (other machines appear as pens across the river).
- A separate game window or engine outside Herdr.

## Open questions

- Whether `.frames()` with a custom font renders pixel-exact layers at every zoom.
- How a relay reads a finished pig's final report reliably (`hc read`, or the agent's own last message).
- Where pen positions, belts, and Workshop designs persist so they follow the person across Herdr windows (`shared()` versus files under the Herdr home).
