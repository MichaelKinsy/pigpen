# Pigtorio

Factorio, but every worker is a coding agent. Pigtorio is a layout and a mod for the [Herdr app](https://herdr.dev), plus a small Go helper. Your Herdr spaces are pens, your agents are pigs, and belts carry one pig's finished hand-off to the next pig, through an Oven (your tests), a Review Station (you) and the Barn (git).

Every game verb runs a real Herdr or herdr-core action. See `DESIGN.md` for the idea, `PLAN.md` for the plan, and `ARCHITECTURE.md` for how the parts talk.

## Install

```sh
cd engine && go build -o bin/pigtorio ./cmd/pigtorio && cd ..
engine/bin/pigtorio install            # into ~/.config/herdr-app (HERDR_APP_HOME picks another)
engine/bin/pigtorio install --keys     # also add the ⌘⇧ shortcuts (see Keys)
```

`install` copies `mods/pigtorio`, `layouts/pigtorio.json` and its preview, and the helper binary to `~/.config/herdr-app/pigtorio/bin/pigtorio`. It changes nothing else. Switch to the layout with `herdr-app do layout.switch '{"name":"pigtorio"}'`, Herdr's layout picker, or `ctrl-shift-l`. Switch back to `main` the same way; your agents keep running.

The mod starts the helper (`pigtorio engine`) itself, as a herdr-core terminal named `pigtorio-engine`, and restarts it within a minute if it stops.

## Play

- **Map**: the whole farm on a grid. Every space is a pen, every agent a pig. A pig's animation shows its state: digging while working, a red `!` and a shaking fence when blocked, napping when idle. Drag the ground to look around, wheel to zoom.
- **Build** from the hotbar at the bottom: Pen, Pig, Belt, Oven, Review, Barn, Job Board, Remove, Runt. The building's ghost follows the cursor, green where it fits, red with the reason where it doesn't. Left-click builds. A Pen asks for a folder and becomes a Herdr space.
- **Belt**: click a pig or station, then the next stop (pig → pig → Oven → Review → Barn). When a pig finishes, its last message rides the belt as the next stop's crate.
- **Hand**: click a pig for its menu (Talk, Hand crate, Peek, Focus in tab, Dress, Nap, Retire), a station or sign to use it; drag a building or pen to move it.
- **Right-click**: a pig or pen opens its menu, a station or belt is taken down, open ground offers "Build here", and with a tool in hand it puts the tool away.
- **The Runt** (the foreman by the Job Board): hints for the next step, drafts you can BUILD or DISCARD, and "Ask the Runt" for a read-only PiG agent that drafts for you.
- **Job Board**: crates with a Mandate. A crate with no done-when cannot leave the board. Pin it, hand it to one pig, or put it on the pen's belt.
- **Oven**: runs the pen's test command in the pen's folder. A burnt crate goes back to the last pig with the log; after two burns it waits for you.
- **Review Station**: the crate's Mandate, gates, the last hand-off and the diff. Approve sends it to the Barn; Send back returns it with your note.
- **Barn**: `git add -A` and commit; when the repo has an `origin` and `gh` is installed, push and open a pull request. Pigs never push.
- **Bell** (top bar): the next thing that needs you, in a fixed order: blocked, burnt, holding, done, stuck.
- **Walk**: the same world, closer; the Swineherd walks where you click and builds where you are.
- **Adopt-a-Pig**: any installed agent (Herdr's `knownAgents()`), a role, a look, a pen. PiG pigs get `--session-dir`, `--model` and the role's brief. **Mock pig** answers instantly and spends no tokens: use it to learn the belts.
- **Workshop**: dress any pig in one of 15 looks; give it a role.
- **Report**: the morning report: shipped, burnt, waiting on you, tokens.

## Keys

Herdr keeps the keyboard on the focused terminal even while the farm covers it, so the farm's bare letter keys only work while the farm holds the keyboard. `pigtorio install --keys` adds a `layout:pigtorio` section to `keys.json` with chords that work anywhere in the layout and take nothing from typing:

| keys | does |
|---|---|
| ⌘⇧B | ring the Bell |
| ⌘⇧M / ⌘⇧G / ⌘⇧J / ⌘⇧A / ⌘⇧K / ⌘⇧R / ⌘⇧T | Map, Walk, Job Board, Adopt, Workshop, Report, Terminals |
| ⌘⇧F | find a pig or pen |
| ⌘⇧N | new crate |
| ⌘⇧H | Pigpedia (help) |

Herdr treats any edit of `keys.json` as yours: after it, Herdr no longer refreshes that file's default shortcuts on upgrade. That is why `--keys` is opt-in.

Every view and action is also a mod command: `herdr-app do pigtorio.bell`, `pigtorio.talk:t7`, `pigtorio.review:c3`, `pigtorio.walk:400,300`.

## Intake from outside

```sh
pigtorio crate add --title "Fix the flaky race" --done-when "go test -race passes 10 times" --pen <space id>
```

## Develop

- `scripts/sandbox.sh env|install|check|shot`: a private Herdr home (`/tmp/hfarm/home`) and core (`hfarm`). Never test against your real home.
- `cd test && bun test` (model logic), `cd engine && go test ./...` (engine), `test/e2e-belt.sh` (a three-pig mock belt on the sandbox core).
- `art/gen.sh` rebuilds every sprite, tile and font.
- `scripts/click.swift` and `scripts/type.swift`: real mouse and keyboard events for driving the sandbox window.

## Built with itself

The Pigpedia (help panel) was built by Pigtorio: a Surveyor pig (claude-sonnet-5.5) planned it, a Builder pig (claude-sonnet-5.5) wrote it, the Oven ran `bun test` and `herdr-app check`, an Inspector pig (gpt-6.1-sol) reviewed it, a person approved it at the Review Station, and the Barn committed it (`39d375f` on `pig/help`, merged into `main`). 424k tokens, about $0.32.
