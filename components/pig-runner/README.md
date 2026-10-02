# pig-runner

PiG Runner, from PiG Standard's `pigrunner`: a half-block pixel-art runner drawn over the whole
terminal, with the pig you picked with `/sprite`. Chrome-Dino-style difficulty: speed climbs to a
cap, obstacles come faster, in groups, and flying.

**It does not start by itself.** The extension registers two commands and no event handlers (only with
`PIG_GAMES_MCP=1` set does it add one `session_start` handler, for the opt-in agent server below), so
bundling it into a Piglet (as `pig-with-batteries` does) does nothing until you type one:

```text
/runner        open PiG Runner
/pig-runner    the same
```

Keys: Up / Space / W jump (start), Down / S duck, P pause, R restart after game over, Esc / Q
leave. Your high score is kept in `$PIG_HOME/state/pig-standard/pigrunner.json`. The game
draws a modal overlay and a fixed 60 Hz ticker of its own; it never waits on the agent and
Esc closes only the overlay.

## Let an agent play (opt-in)

Off by default: nothing listens until you ask.

```text
/runner mcp on       serve PiG Runner to an agent on 127.0.0.1 for this session (also /pig-runner)
/runner mcp off      stop it
/runner mcp status
```

or start pig with `PIG_GAMES_MCP=1`. The server is `games-runner`: five tools, found from a codemode script with
`searchTools("game")` (`mcp__games_runner__...`), behind a random bearer token that only the registered config holds.
The mechanism is the shared library [`gamemcp`](../pig-play/README.md#let-an-agent-play-gamemcp).

| Tool | Result |
|---|---|
| `games_list()` | `[{name: "pig-runner", description, instructions, actions}]`; `instructions` say how to play from the state |
| `game_start({name: "pig-runner"})` | opens the same overlay `/runner` opens, past the title screen: `{ok, game}` |
| `game_state()` | `{running, game_over, stopped, score, speed, next_obstacle: {kind, distance_px, time_to_impact_ms, height, width_px}, decision_window_ms, decide_now, pig: {jumping, ducking}}` |
| `game_act({action, confidence?})` | `jump`, `duck`, `none`, `restart` after a crash, or `stop`; queued, applied on the next frame as the key press: `{ok, decisions}` |
| `game_score({wait_closed_ms?})` | `{score, high_score, decisions, game_over, open, stopped}`, also after the game closed; the high score is saved in `<config home>/state/pig-standard/pigrunner.json` |

`speed` is playfield pixels per second and distances are playfield pixels (the pig stands at x 8..18; its feet at
the ground). `next_obstacle.kind` uses the game's own words: `pie_small` (a pie), `pie_large` (a pie crust),
`flying_pie_low`, `flying_pie_mid`, `flying_pie_high` (a flying pie, classed by how high its underside is: this game has
only the mid one) or `none`; `height` is the obstacle's top above the ground and `time_to_impact_ms` how long it takes to
reach the pig.

**The AI's pace.** A classifier takes about 0.7 s per decision, about 80 px of road at the start speed, so an answer
computed from the distance would come after the pie. While an agent plays, the run therefore holds at each obstacle's
decision point: when `time_to_impact_ms` reaches `decision_window_ms` (150 ms), `decide_now` turns true, the game stops
advancing (the HUD says "waiting for the AI's decision") and the agent's next `game_act` lands on the frame the run
resumes. Nothing is played for the agent: `none`, or a duck at a pie, crashes, and a jump sent early lands early. An
action from a state read before the hold began does not release it, so play one decision at a time (read `game_state`,
then `game_act`). Between obstacles the run moves at its normal speed. A person playing with the keys never waits.

The overlay shows "AI playing: last decision jump p=0.93 · decisions 12" while an agent plays. `game_act` `stop` ends
the play: the run freezes, the high score is saved, and the HUD shows "AI stopped · final score N · high score M ·
decisions K · any key closes"; the next key closes the overlay and reports the score. (A Go extension cannot close its own
overlay yet: the SDK has no done callback, so the closing key is the user's.) Esc closes the overlay at any time and
ends the game for the agent too. The demo that has Jev play it is [`demo/jev-plays.js`](../../demo/README.md).

Needs the sibling Package [`pig-play`](../pig-play/README.md) (`../pig-play`) when built from
source, and a Go toolchain unless fused into a Piglet Binary.

```sh
pig package validate ./components/pig-runner
pig install ./components/pig-runner/extensions/pigrunner --validate-only --json
```

Credits and licence: [CREDITS.md](CREDITS.md), [LICENSE](LICENSE) (MIT).
