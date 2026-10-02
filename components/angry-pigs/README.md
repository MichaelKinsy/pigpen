# angry-pigs

Angry Pigs, from PiG Standard's `angrypigs`: a pixel-art slingshot game drawn over the whole
terminal. Your pig (the sprite chosen with `/sprite`) flies at bird-topped towers of wood, stone
and ice that crack, shatter and collapse; a gauge, a trajectory preview and particles show the
shot. Physics runs at a fixed 120 Hz step; a camera pans over the fixed-size world and a minimap
shows the whole field when the terminal is narrower than the world.

**It does not start by itself.** The extension registers one command and no event handlers (only with
`PIG_GAMES_MCP=1` set does it add one `session_start` handler, for the opt-in agent server below), so
bundling it into a Piglet (as `pig-with-batteries` does) does nothing until you type:

```text
/angry-pigs
```

Keys: Up / Down pull the band back for power, Left / Right aim, Space start and fire, P pause,
R restart, Esc / Q leave; a mouse drag aims when the terminal reports the mouse. The high score
is kept in `$PIG_HOME/state/pig-standard/angrypigs.json`. The game draws a modal overlay with its
own ticker and never waits on the agent; Esc closes only the overlay.

## Let an agent play (opt-in)

Off by default: nothing listens until you ask.

```text
/angry-pigs mcp on       serve Angry Pigs to an agent on 127.0.0.1 for this session
/angry-pigs mcp off      stop it
/angry-pigs mcp status
```

or start pig with `PIG_GAMES_MCP=1`. The server is `games-angry-pigs`: five tools, found from a codemode script with
`searchTools("game")` (`mcp__games_angry_pigs__...`), behind a random bearer token that only the registered config holds.
The mechanism is the shared library [`gamemcp`](../pig-play/README.md#let-an-agent-play-gamemcp).

| Tool | Result |
|---|---|
| `games_list()` | `[{name: "angry-pigs", description, instructions, actions}]`; `instructions` say what each action does to the shot |
| `game_start({name: "angry-pigs"})` | opens the same overlay `/angry-pigs` opens, past the title screen: `{ok, game}` |
| `game_state()` | `{shots_left, score, level, level_done, game_over, flying, targets: [{x, y, material}], pig: {power, angle}, aim_landing: {x, y, hit} or null, landing_vs_target, landing_vs_target_px, last_shot_result: {outcome, birds_knocked}}` |
| `game_act({action, confidence?})` | `aim_up`, `aim_down` (the angle, one degree: the Left and Right keys), `power_up`, `power_down` (the pull, three percent: the Up and Down keys), `fire`, `none`, after a result `next_level` or `restart`, and `stop`; queued, applied on the next frame as the key press: `{ok, decisions}` |
| `game_score({wait_closed_ms?})` | `{score, high_score, decisions, game_over, open, stopped}`, also after the game closed; the high score is saved in `<config home>/state/pig-standard/angrypigs.json` |

`x` and `y` are world pixels: x grows right from the left edge, y grows up from the ground, and the slingshot rests at
(44, 15). `targets` lists every bird (material `bird`) and the top block of each tower column (`wood`, `stone`, `ice`) at the
centre of its 6-pixel cell. `aim_landing` is where the aimed shot ends, with what stops it (`ground`, a block material,
`bird`, or `""` when it leaves the field): the preview's flight carried on to its end, with no randomness in it. It reaches
farther than the screen does. The 24 preview dots show the first 1.2 s of the flight, so a person sees where the dots head
and judges the rest, while `aim_landing` gives the agent the end point (on level 1 most shots that reach the bird reach it
after the last dot). A shot it says reaches a bird can still miss, since the pig is a disc and the flight a point. `landing_vs_target` says it
in words against the nearest bird: `on target` (the shot ends at a bird), `short by N px` or `long by N px`
(`landing_vs_target_px` is the signed difference, negative when short; `no shot to aim` while a pig flies or after a
result). `last_shot_result.outcome` is `none`, `landed`, `hit_structure` or `off_field`.

**What the actions do** (also in `instructions`): `power_up` pulls the band further, so the shot flies faster and lands
further away; it is the main way to reach a far bird (on level 1 the start aim, 45 degrees at power 70, lands about 37 px
short, and a few `power_up` reach the bird). `power_down` brings the landing closer. `aim_up` raises the arc (over a tower
in the way; above 45 degrees it also lands a little closer), `aim_down` flattens it; an angle step moves the landing far
less than a power step. Fire when `landing_vs_target` is `on target`: short means `power_up` (at full power `aim_up`),
long means `power_down`.

The overlay shows "AI playing: last decision power_up p=0.92 · decisions 12" while an agent plays. `game_act` `stop` ends
the play: the game freezes, the high score is saved, and the HUD shows "AI stopped · final score N · high score M ·
decisions K · any key closes"; the next key closes the overlay and reports the score. (A Go extension cannot close its own
overlay yet: the SDK has no done callback, so the closing key is the user's.) Esc closes the overlay at any time and ends
the game for the agent too. The demo that has Jev play it is
[`demo/jev-plays.js`](../../demo/README.md).

Needs the sibling Package [`pig-play`](../pig-play/README.md) (`../pig-play`) when built from
source, and a Go toolchain unless fused into a Piglet Binary.

```sh
pig package validate ./components/angry-pigs
pig install ./components/angry-pigs/extensions/angrypigs --validate-only --json
```

Credits and licence: [CREDITS.md](CREDITS.md), [LICENSE](LICENSE) (MIT).
