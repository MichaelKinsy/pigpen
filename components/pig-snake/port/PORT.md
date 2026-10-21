# Port record: pig-snake

Pig Snake is an **original game**, not a port of a Pi extension, so most of the porting Skill's
proof machinery has nothing to compare against. This record says which steps applied, which did
not and why, and what replaced them. Nothing is claimed that the evidence below does not show.

| Input | Identity |
|---|---|
| Upstream code and art | PiG `MichaelKinsy/PiG` commit `d86eb93f217e64b655e9ee6f48c93a9afd107697`, `piglets/standard/`: `internal/{pixel,termgame,arcade}`, `extensions/{pigrunner,angrypigs,piglogin}`. MIT, Michael Kinsy. Vendored unmodified in `port/upstream/` (`.go.txt`, with PiG's license), including the upstream test files the twin table below counts; the exceptions are a comment line in `piglogin-variants.go.txt` and the default sprite id, renamed `pig-default` in three of the files (see CREDITS.md). |
| Pi oracle | **none**: no Pi extension exists for this game. |
| Target | PiG 0.3.0+0.87.1 from a 0.3.0 pre-release build (`63c6ba456`), go1.27.1, linux/amd64 |
| Original's tests | PiG's game tests (below): ported as twins where the behavior exists here, as named skipped twins where it does not |

## Skill steps

| Step | Applied? | Evidence |
|---|---|---|
| 0 scope, authority, inputs | yes | this table; `provenance.json` (`origin: ported` because code and art are adapted from PiG; the game itself is original) |
| skip what PiG has | yes | nothing in PiG 0.3.0 or the 0.4.0 notes is a snake game |
| 1 gap check | yes, on the Go source (`port-gaps` pseudo-scenario of `pigeq record --self`, newer harness): one `PARTIAL`, `Context.Custom` (stand-in: the component runs in the extension, the host overlay owns focus), which is the design here and is covered by the real-terminal tests. `pigeq gaps --ts` does not apply (no TypeScript). The SDK surface used was also read by hand (below) | |
| 2 inventory and map | yes | contract table below |
| 3 tests first, red | yes | commit `test(pig-snake): RED ...`: compilable stubs, 35 test functions fail, `red-run.md` |
| 3 layer 2 (record from Pi) | **not applicable**; replaced by `pigeq record --self`: golden traces of the Go extension itself under PiG (`golden/`, header lane `pig-go-self`), **self-recorded**: a regression baseline, **not** equivalence | |
| 4 implement, green | yes | commit `feat(pig-snake): GREEN ...` |
| 5 differential run (`pigeq run`) | **not applicable** (needs `--ts`); `pigeq check` runs the extension against its own self-recorded golden traces | |
| 6 mutations | yes | `mutations.json`, 84 mutants, all killed (`mutation-run.txt`) |
| 7 binary build | yes | fused into a Piglet Binary (below) |
| 8 Package with provenance | yes | `package.json` (`pig-package`), `provenance.json`, `CREDITS.md`, `LICENSE` |

## Contract table

| ID | Behavior | Where | Tests |
|---|---|---|---|
| C1 | Only an explicit command starts anything: `/pig-snake`, `/snake`; no event handler, tool, flag, timer or output otherwise | `extension.go` | `TestExtensionRegistersSnakeCommandsOnly`, `TestOnlyAnExplicitCommandStartsAGame`, scenario `idle-bundled`; real terminal "starts nothing by itself" |
| C2 | Full-terminal overlay titled "Pig Snake"; only the interactive TUI plays (print, JSON and RPC only say so) | `run` | `TestCommandOpensAFullTerminalOverlayReportsAndSavesTheScores`, `TestOnlyTheInteractiveTUICanPlay`, `TestATUIHostWithoutAUIStillDoesNotPlay`, scenarios `command-pig-snake`, `command-snake` |
| C3 | Herd: the leader is `Body[0]`, each apple adds one pig at the tail, the line stays contiguous | `snake` | `TestEatingAddsOnePigHeadAndScores`, `TestTheHerdFollowsTheLeaderInLine`, `TestBotPlaythrough...`, `TestRandomPlayNeverBreaksTheHerd` (invariants after every step) |
| C4 | Movement, turn queue of two, no reversal into the herd (a lone leader may turn around), repeat heading is not a turn | `snake` | `TestStepMoves...`, `TestReverseIsIgnored...`, `TestTwoQuickTurns...`, `TestTheTurnQueueHoldsAtMostTwo` |
| C5 | Walls mode: the edge ends the game, the leader stays inside | `snake` | `TestWallsEndTheGameAtEveryEdgeWithoutMovingTheLeader` |
| C6 | Wrap mode: the edge carries the leader across; apples across the edge are eaten | `snake` | `TestWrapCarriesTheLeaderAroundEveryEdge`, `TestWrapFoodAcrossTheEdgeIsEaten` |
| C7 | Collision with the herd; the vacating tail cell is free unless eating | `snake` | `TestRunningIntoTheHerdEndsTheGame`, `TestTheVacatingTailCellIsFreeWhenNotEating`, `TestTheTailCellIsNotFreeWhenEating` |
| C8 | Apples land on a uniformly chosen free cell; a full board wins | `snake` | `TestFoodNeverLandsOnTheHerdAndPicksTheNthFreeCell`, `TestFoodIsAlwaysOnAFreeCellAcrossManyGames`, `TestFillingTheBoardWins` |
| C9 | Speed: 160 ms, 5 ms per apple, floor 70 ms | `snake` | `TestIntervalSpeedsUpWithTheHerdAndClamps` |
| C10 | Pause, retry, mode switch only on the title screen, per-mode high scores | `snake`, `component` | `TestPauseTogglesOnlyWhilePlaying`, `TestRestartKeeps...`, `TestModeKeyToggles...`, `TestStateReportsTheModesOwnHighScoreOnly` |
| C11 | Keys: arrows, WASD, hjkl, space/enter, p, r, m, q/Esc; Kitty releases ignored | `component`, `termgame` | `TestSteeringKeysArrowsWasdAndVi`, `TestKeyReleasesAndOtherKeysDoNothing`, `TestEscapeAndQuitLeaveFromEveryState`, `TestParseKeyDecodesLegacyApplicationAndKitty` |
| C12 | Clock: fixed tick, bounded catch-up, nothing fires after `Dispose`, height change redraws, the title screen does not tick, paused time is not banked | `component` | `TestSnakeComponentTimerInvalidatesAndStops`, `TestAStalledClock...`, `TestNothingFiresAfterDisposeReturns`, `TestDisposeWaitsForAFrameRequestInFlight`, `TestAPausedGameDoesNotBankTime...`, `TestTimerInvalidatesWhenTheTerminalHeightChanges` |
| C13 | Narrow and short terminals: exact-size frames at every width and height; the board picks 8, 6 or 4 pixel pigs; a running game keeps its board, shrinks its pigs and pauses when even the smallest do not fit; a "terminal too small" hint names the size needed | `scene`, `component` | `TestGridFor...`, `TestFit...`, `TestRenderFillsEveryViewportExactly` (18 widths x 11 heights x 5 states x 2 sources), `TestTooSmallMessageFitsNarrowWidths`, `TestATooSmallTerminalPausesTheGame...`, `TestBoardFollowsTheTerminalWhileWaiting...`; real terminals 80x24, 140x40, resize to 30x10 |
| C14 | Herd of assorted pigs; leader wears the `/sprite` choice; apple; crashed leader tinted; title, pause, game-over and herd-full cards | `sprites`, `scene` | `TestEveryHerdMemberIsDrawnAsItsOwnPigHead`, `TestTheLeaderWearsTheSpriteChosenWithSprite`, `TestNoFollowerEver...`, `TestGameOverTintsTheLeaderRed`, `TestCardPixels`, `TestAppleArt...` |
| C15 | One sprite seam: the renderer touches pigs only through `sprites.Source` | `sprites`, `scene` | `fakeSource` in `scene_test.go` (a source with one 6 px size and its own colors drives every scene test), `TestRenderSwitchesSourceWithoutStalePalettes` |
| C16 | HUD: score, herd, high, mode, hints, status; 24-bit or 256 colors | `scene` | `TestHUD...`, `TestSnakeSharesTheRunnerStyle`, `TestRenderDownsamplesWithoutTrueColor` |
| C17 | Strict private state file `state/pigpen/pig-snake.json` (0600, atomic, size-limited, unknown fields rejected) | `state.go` | `TestSnakeHighScoreStateIsStrictAndProtected`, `TestSnakeStateLivesUnderTheConfigHomeAndRejectsBadFiles` |
| C18 | Result handling: wire result over fallback, notification text incl. wrap | `state.go`, `run` | `TestSnakeScoresUsesWireResultAndFallback`, `TestSnakeResultState...`, `TestWrapModeIsNamedInTheNotification`, `TestAnAbandonedGame...` |

SDK surface used (read from `docs/extension-sdk-surface.md` by hand, since `pigeq gaps` needs a
TypeScript source): `Extension.Command`, `Context.HasUI`, `Mode`, `Height`, `ConfigHome`, `Custom`
with `RemoteComponent` (+ `RemoteComponentInvalidator`, `RemoteComponentDisposer`),
`RemoteOverlayOptions`, `Notify`. All implemented in the Go SDK of 0.3.0 (`ui.custom` is
"stand-in/partial" in the table: the component runs inside the extension and the host overlay owns focus).

## Test counts and twins

Final tree (after review): 113 test functions pass (107 at the lane's READY commit), 29 named skipped twins, 0 fail
(`go test -v ./...`), `go test -race -count=10 ./...` passes, `go vet` clean for linux, windows and
darwin.

| Upstream file (PiG d86eb93) | Cases | Exact or adapted twins | Named skipped twins |
|---|---:|---|---|
| `pigrunner/extension_test.go` | 6 | 6 (registration, timer, controls and quit, restart keeps high score and sprite, strict state, wire scores), adapted to this game's names; expectations kept | 0 |
| `internal/pixel/pixel_test.go` | 6 | 6, same inputs and expectations (now in `pig-play`, `libraries/pixel`) | 0 |
| `internal/termgame/termgame_test.go` | 3 | 2 (`ParseKey` gained four keys: WASD text, empty, LF; `Viewport`/`Overlay` unchanged) | 0 (`ParseMouse` is the shared library's, tested in `pig-play`) |
| `internal/arcade/arcade_test.go` | 3 | 2 in spirit: `TestHUDLinesShareOneLayout` -> `TestHUDLinesAreExactlyTheRequestedWidth` + `TestHUDShows...`; `TestDrawCardCentersTheTitleOrDeclines` -> same name | 1: `TestHillsMatchTheRunnerProfiles` |
| `pigrunner/pigrunner_test.go` | 23 | 14 in spirit (new game, render output and fill at every size, downsampling, pause, pig sprite uniformity, speed, variant palette, component pause/resume, resize width, height redraw, Kitty and legacy keys, steady-state allocations, title and game-over screens) | 9: `TestJump`, `TestDuck`, `TestJumpClearsGroundObstacleAtApex`, `TestDuckClearsAirObstacle`, `TestObstacleDensityRisesWithSpeed`, `TestUpdate_ScoreAdvances`, `TestPieSpritesHaveUniformWidth`, `TestCustomVariantRunnerSprites`, `TestTallViewKeepsTheGameBandAtTheBottom` |
| `pigrunner/{human_bot,bot_playthrough}_test.go` | 2 | 1 in spirit: bots play whole games (`internal/snake/playthrough_test.go`) | 1: `TestHumanLikeBotDiesWithinABoundedScore` |
| `angrypigs/game_test.go` | 26 | 9 in spirit (render every size, resize, height redraw, selected sprite, controls, quit and dispose, allocations, registration, shared style) | 17: aiming, launch, birds, blocks, levels, power, preview, camera, mouse (all listed by name in `upstream_skipped_test.go`) |

The skipped twins are real test functions with the upstream names, so `go test -v` lists every gap.
Every mechanic listed as skipped is one Pig Snake does not have. Two things a reader could
expect and will not find: **mini mode** and **background-work isolation proof**. Neither upstream game
has a mini mode (the roadmap plans it in a shared shell), so Pig Snake has full mode only, as they do;
see "Gaps" below.

Test changes made after the red commit, none loosening an expectation: two arithmetic slips in the snake
tests (the random source consumed by `New`, the score of a hand-built herd) and two log-only branches
removed (green commit); the too-small tests rewritten when the behavior changed from "freeze the clock"
to "pause the game" after the real-terminal run showed a resized game crashing into the wall the moment it
fit again; and tests added when mutation survivors showed missing coverage (paused time banking, dispose
waiting for an in-flight frame request, the HUD and palette caches, the comfort threshold, the herd
inferred from a wire score, `Mode` without a UI).

## Proof-like checks

- **Scenarios under real PiG** (`pigeq record --self`, then `pigeq check`, host `pig 0.3.0+0.87.1`; the new harness also passes its `port-gaps` (one PARTIAL, `Context.Custom`, as described above) and `exec-coverage` (the extension starts no process) pseudo-scenarios): `idle-bundled` (an
  agent turn with the extension loaded: no game UI at all), `command-pig-snake` and `command-snake` (RPC has
  no custom components: `ui.custom` answers `no_ui`, the extension shows the warning "Pig Snake needs an
  interactive terminal."). The golden traces in `golden/` are **self-recorded** (this extension recorded from itself and reviewed by hand), so they are a
  regression baseline (`npm run test:port`), **not** equivalence: there is no oracle.
- **Mutations**: 84 deliberate defects across rules, component, extension, state, scene, sprites, termgame and pixel,
  every one killed by the layer-1 Go tests (`mutation-run.txt`); the RPC scenarios cannot reach the overlay, so they
  kill none, which is stated rather than hidden. Equivalent mutants considered and not listed: a board that is
  resized while running (`Game.Resize` refuses), `TogglePause` on a waiting game (no-op), drawing the herd tail-first
  (pigs never overlap).
- **Piglet Binary**: `pig-with-batteries` cannot build a Binary on this branch because it selects the Node herdr
  reporter (`lock extension "extension": selected origin is unavailable`, the existing blocker in RELEASE-BLOCKERS.md).
  A copy of the staged composition **without the herdr member** builds: "Preparing fused Go members (pig-snake,
  seed-check)", 57,737,479 bytes, `Built ... in 28s`. That Binary passes `test:pig-snake` (9 tests) in a real terminal.
  `pig install components/pig-snake/extensions/pig-snake --validate-only` and `pig package validate components/pig-snake` pass (`--validate-only` takes the extension directory, not the Package root).
  *Release-branch note:* the herdr reporter is a Go extension since `pigpen-herdr-go`, so on `pigpen-release` the full
  `pig-with-batteries` (herdr, the games and Pig Snake) builds a Binary; the blocker above described the pre-merge branch.
- **Real terminal** (`piglets/pig-with-batteries/tests/pig-snake.integration.test.mjs`, tmux, temp HOME, PIG_HOME and
  PIG_CODING_AGENT_DIR, test-faux provider), for both the Binary and the staged manifest: nothing starts by itself;
  `/pig-snake`, `m`, space, pause, crash and retry, resize to 30x10 (hint, then "PAUSED" and it stays paused),
  `q` (report and state file), `/snake` in wrap mode survives 3.5 s that would kill a walls game, an agent stream
  that continues under the open game and completes with the transcript intact, exact overlay geometry at 80x24 and 140x40.

## Review

- **Fixed (red, then green):** the "terminal too small" hint named the smallest *new* board (34x16) even for a
  game under way, whose board is kept: a 12x6 board from a 100x30 terminal, shrunk to 40x16, said
  "needs 34x16, have 40x16" while nothing fit. It now names what the board in play needs at 4 px pigs
  (`scene.BoardView`; 50x16 for that board, 162x48 for the 40x22 maximum); a waiting game still asks for
  34x16. Tests: `TestTheTooSmallHintNamesTheSizeTheBoardInPlayNeeds`,
  `TestATooSmallRunningGameAsksForTheSizeOfItsOwnBoard`, and the real-terminal resize now expects
  "needs 50x16" (red on the lane's Binary, green on the rebuilt one).
- **Mutation spot check:** 4 further mutants survived the tests (winning with one free cell left, apples only
  on the first two free cells, a turn queued before a crash steering the next game, the wall frame colour);
  each now has a test that kills it (`TestOneFreeCellLeftIsNotAWin`, `TestEveryFreeCellCanGetTheApple`,
  `TestRestartForgetsTurnsQueuedBeforeTheCrash`, `TestTheBoardFrameShowsWhetherTheEdgeIsAWall`). They and two
  mutants of the hint fix are in `mutations.json` as `review-*` (90 mutants; two find texts updated for the fix).
  `npm run test:port` in a scratch merge with lane `pigpen-porter-driver`'s branch (its harness and scripts,
  `fakehost_test.go` refreshed from the template): baseline passes, all 90 caught. `mutation-run.txt` is still the
  lane's 84-mutant run.
- The upstream files `pigrunner/render.go` (named in CREDITS) and the test files counted in the twin table
  (`pigrunner_test.go`, `human_bot_test.go`, `bot_playthrough_test.go`, `angrypigs/game_test.go`,
  `arcade/arcade_test.go`) are now vendored unmodified in `port/upstream/` so the counts can be checked.
- `port/upstream/piglogin-variants.go.txt:46` named a private source repository of the website art in a comment;
  that comment line is reworded (the same edit the pigpen-games review made to its copy), recorded in CREDITS.md.

## Findings about the host and the harness

1. **RPC mode cannot open a custom component**: `ui.custom requires an interactive TUI` (`no_ui`), although
   `ctx.HasUI()` is true in RPC. The extension checks `ctx.Mode() != "tui"` and says so instead of failing with an
   `extension_error`.
2. The porting Skill's `fakehost_test.go` template (first build) sent commands in the wrong wire shape (`unknown command:`).
   `host_test.go` carries a correct `runCommand` next to the unchanged template copy (a repository test pins every copy);
   the porter-driver's build fixes the template.
3. The first harness build's `pigeq mutate --unit` counted 84 of 84 mutants killed when no test had run (a
   version-manager shim tried to download a Go toolchain). Reported in the friction notes and fixed in the
   porter-driver's build (it now refuses without a passing unmutated baseline); the final mutation run below used that build.
4. `quality-gates` rejects the lane's `.upstream` symlink; checks were run with it moved aside.

## Gaps (named, not silent)

- **Mini mode / footer widget** (roadmap "Mode contract"): needs a PiG SDK capability. `Context.SetWidget` can show a
  `kit.View` since 0.5.0, but a widget takes no keys; a snake you cannot steer is not a game. Mirrors Pi's
  `ctx.ui.setWidget` plus a focus handoff (the editor's keys go to the widget on a toggle key). Recorded in the
  parity program's `core-items.md`. Not built, not refused: no option exists to refuse.
- **Sound**: needs a PiG SDK capability (a terminal bell or an audio call from an extension; `ui.custom` renders lines only,
  and the SDK has no bell or audio call). No upstream game has sound. Recorded in `core-items.md`.
- **Background-work proof** for the shared shell (roadmap "Required proof before more games"): the Pig Snake slice is shown
  (an agent stream continues under the overlay and completes; a real-terminal test). The full PTY scenario with a slow tool,
  a question dialog and 100 ms restoration is not built: the test provider has a slow tool ("Run: sleep for a while") but no
  scenario that opens a question dialog, so the scenario cannot be driven from a Pigpen test. Recorded in `core-items.md`.
- Windows and macOS execution: `go vet` only.

Closed in this release: **mouse steering** (`HandleMouse`, `mouse_test.go`), the **Sheriff's hat** (`sprites.Hatted`, three
hat drawings, tests in `sprites` and `scene`), and the **shared libraries**: the pixel canvas, the terminal plumbing and the
sprite catalogue are `pig-play`'s (`libraries/pixel`, `libraries/termgame`, `libraries/sprite`); the cases Pig Snake had
added to its copies are `internal/scene/shared_pixel_test.go` and `shared_keys_test.go`, and the twin table's pixel and termgame
rows are now exercised in `pig-play`. The named skipped twin `TestParseMouseDecodesSGRReports` is gone with the copy; the mouse
twin `TestMouseDragPullsAndReleaseFires` stays skipped (no slingshot). The mutation list lost the five mutants of the removed
copies and gained 16 for the mouse and the hat.

## PiG 0.4.0 (Pi 0.99.1) notes

Compared with PiG's `docs/extension-sdk-surface.md` from its Pi 0.99.1 port (in PiG `v0.4.0`): the rows the game uses (`ctx.ui.custom`,
`custom(options.overlay)`, `ui.notify`, `ctx.hasUI`, command registration) keep their 0.3.0 status; `onHandle` stays
missing for Go. Things to re-check when the pin moves: whether RPC mode gains custom components (then the mode
guard can relax), the TUI's new mouse forwarding (mouse steering becomes possible), and that the overlay
frame is still one cell (`termgame.Viewport` subtracts 2). Built-in MCP has no effect on the game.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the port itself (`--self`, a regression baseline), with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.

## Owner decisions (2026-10-20)

- Accepted: the 29 named skipped upstream twins (mechanics Pig Snake has no counterpart for) and the self-recorded golden traces (`port/golden`, a regression baseline, not equivalence).
- Mini mode and sound stay tracked PiG SDK items (see the Gaps section).
- The opt-in `gamemcp` server (PiG Runner and Angry Pigs have one) stays an owner-optional feature, not excluded.
