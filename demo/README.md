# demo

## jev-plays.js: Jev plays both Pigpen games

A codemode script in which Jev, TypeSafe's typed classifier, plays PiG Runner and Angry Pigs, about 10 seconds each,
through MCP. Every decision is Jev's answer to a choice question; the script calls no chat model. It is the 0.4.0 demo of
the games' opt-in MCP server ([`gamemcp`](../components/pig-play/README.md#let-an-agent-play-gamemcp)) and of what a
codemode script can do with `models.classify`.

What it does:

1. `searchTools("game")` finds the tools of each game's server (`mcp__games_runner__...`, `mcp__games_angry_pigs__...`).
2. `games_list()` on each, then Jev answers "Which game is most fun to watch an AI play first in a short clip?" over the
   listed games. That game is played first, the other second.
3. For each game: `game_start(name)` opens its overlay in the terminal, the same one `/runner` or `/angry-pigs` opens.
4. For `SECONDS_PER_GAME` (10): `game_state()`, a choice question in the words of the state, then
   `game_act(choice, confidence)`, one decision at a time. Runner: "decide_now is true when the pig must act now. Which
   action avoids the next obstacle?" over `jump` (a `pie_small` or `pie_large`), `duck` (a `flying_pie_*`) and `none`.
   The run waits at each obstacle's decision point for the answer, so Jev's 0.7 s per decision is in time (see the
   [Runner README](../components/pig-runner/README.md)). Angry Pigs: "landing_vs_target says where the aimed shot ends
   against the nearest bird. Which action brings the shot onto the bird, or fires it?" over `fire` (`on target`),
   `power_up` (`short by N px`), `power_down` (`long by N px`), `aim_up` (short at full power) and `none`. A crash is
   answered with `restart` and a cleared level with `next_level`, with no classification.
5. `game_act("stop")`: the game freezes, saves its high score and shows "AI stopped · final score N · high score M ·
   ... · any key closes". `game_score({wait_closed_ms: 8000})` waits for that key, then the next game opens. (A Go
   extension cannot close its own overlay yet, so the closing key is the person's or the recording driver's; without it
   the next game opens on top after 8 s.)
6. It returns `{order, chosen_by_jev, games: [{game, decisions, score, final_score, high_score, restarts,
   levels_cleared, failed, closed}], decisions, classify_calls, total_ms}`.

### Run it

You need a PiG with codemode, the games with their MCP servers on, a terminal UI, and the typesafe key for the
`jev-latest` classifier (`TYPESAFE_API_KEY`, or `/login typesafe`):

```sh
export PIG_GAMES_MCP=1                       # or type /runner mcp on and /angry-pigs mcp on in the session
pig --piglet dist/staged/piglets/pig-games/piglet.yaml     # after: npm ci --ignore-scripts && npm run stage
```

then have the model run the script with codemode (`"defaultTools": ["+codemode"]` in settings), for example:

```text
Run the codemode script demo/jev-plays.js as it is and tell me what it returned.
```

Interactive mode is needed (the overlay is the point). In the PiG 0.4.0 staging build the codemode `models`
helper is not defined in interactive sessions (print and RPC modes have it), so the script stops with
`models is not defined` until a PiG that fixes it; the game side was checked in a real interactive session with the
classifier lines (between the `// -- classifier --` markers) replaced by a rule.

While the script runs, the game overlay is full-terminal and covers the tool block and its live nested-call rows; they are
there again when you close the overlay with Esc. Keys you type go to the overlay (`p` pauses the game; the HUD shows
PAUSED and the agent line stays). Esc mid-run closes the game for the agent, `game_state` then answers "no game is open", and
the script fails; the failure report lists every nested call so far, which is long after thousands of decisions.

### Tests

`scripts/jev-plays.test.mjs` (part of `npm test`) runs the script against stub tools and a stub classifier on a
simulated clock with 0.7 s per classification. The Go tests of each game play the real game with a rule-based player
that decides from the state's words, as a classifier would, with 0.7 s per decision: the Runner survives a minute on
every one of 500 seeds headless and 20 s in real time over MCP, and Angry Pigs clears level 1 (headless and over MCP),
then `stop` ends each game.
