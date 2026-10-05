# games-play-well: Jev plays both games well, then stops cleanly

Status: READY. Base: the pigpen-drop-login work (`2eeec29`). Owner request 2026-10-02 08:35 MDT, from the
analysis of a recorded demo: Jev scored 8 and 0 because the Runner's state spoke of cacti while everything else
said pies, its answers came ~0.7 s (~80 px) after the obstacle, Angry Pigs' physics were undiscoverable, and a game
could not be ended from the script.

## What changed

1. **One vocabulary.** Runner `next_obstacle.kind` is `pie_small`, `pie_large`, `flying_pie_low|mid|high` or `none`
   in the state, description, instructions, demo and tests (was `cactus_*`, `bird_*`).
2. **The AI's pace (Runner).** `game_state` adds `next_obstacle.time_to_impact_ms`, `decision_window_ms` (150) and
   `decide_now`. While an agent plays (`game_start` only; a person's game never holds), the run holds when the next
   obstacle is 150 ms from the pig, until the agent's next `game_act`, which lands on the frame the run resumes. The
   answer still decides: `none` or a wrong action crashes, an early jump lands early, an action read before the hold
   does not release it, and once answered `decide_now` turns false so a quick re-read draws no second answer. Between
   obstacles the run moves at full speed; the HUD says "waiting for the AI's decision" while it holds. Chosen over a
   slowed game because the game stays the same game and the contract is exact (not "fast enough at this speed").
3. **Angry Pigs, discoverable.** `instructions` (in `games_list` and the MCP server's instructions) say what each action
   does (power_up lands further, aim_up raises the arc, angle moves the landing far less than power) and when to fire.
   `game_state` adds `landing_vs_target` ("on target" / "short by N px" / "long by N px" against the nearest bird) and
   `landing_vs_target_px`.
4. **Stop.** `game_act("stop")` (gamemcp, both games): the game freezes, saves its high score, and the HUD shows
   "AI stopped · final score N · high score M · decisions K · any key closes"; the next key closes the overlay and
   the usual "score · high" notification follows. Later actions are refused. `game_score` answers before, during and
   after a game (`open`, `stopped`; the saved high score when none is open; its description names
   `<config home>/state/pig-standard/<game>.json`), and `game_score({wait_closed_ms})` waits up to 20 s for the
   overlay to close. A Go extension cannot close its own overlay: the SDK's custom component closes only when
   `HandleInput` returns `Done` (no done callback like Pi's `ctx.ui.custom`; raised with PiG),
   so the closing key is the person's or the recording driver's. No panic/error trick.
5. **Demo.** `demo/jev-plays.js` plays both games, Jev picks the first, 10 s each, one decision at a time, questions in
   the state's words, `stop` after each window, waits for the close, returns per-game results.

## Proof

- Runner, rule player from the state's words (decide_now + kind), 0.7 s per decision, one in flight: 500 seeds x 60 s
  headless, no crash (`TestRulePlayerWithSlowDecisionsSurvivesTheRun`); 20 s in real time over MCP, then stop
  (`TestSlowRulePlayerSurvivesTwentySecondsOverMCPThenStops`). Without the hold the same latency crashes (control test).
  Reverting the "answered" flag makes the headless test fail (the real-time run found that race first).
- Angry Pigs, rule player from `landing_vs_target`, 0.7 s per decision: clears level 1 headless (6.5 s, 1 pig) and over
  MCP in real time (~7 s), then stop. Firing at the start aim does not clear it (control).
- Real interactive pig (tmux, a PiG 0.4.0 release-candidate binary, a JS rule with a 0.7 s busy wait in place of Jev):
  Runner 10 s, score 75, no crash, HUD "waiting for the AI's decision" at obstacles, then "AI stopped · final score 75";
  Esc closed it, Angry Pigs opened, cleared level 1 (4050) in 10 s, stopped, Esc closed it; the script returned both
  games with `closed: true`. High scores saved (75, 4050).
- Gates: `go vet` (linux, windows, darwin/arm64) and `go test -race -count=1` for pig-play, pigrunner, angrypigs;
  `npm run check`, `npm test` (117 pass), `npm run stage`.
