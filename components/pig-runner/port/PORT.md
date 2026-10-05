# Port record: pig-runner (relocation of PiG Standard's `pigrunner`)

A **Go-to-Go relocation**, not a Pi port: there is no Pi oracle. Original: MichaelKinsy/PiG
`piglets/standard/extensions/pigrunner` at `d86eb93f217e64b655e9ee6f48c93a9afd107697` (MIT, Copyright (c) 2026 Michael Kinsy, Michael Kinsy). Upstream files are not
vendored; each row names the git blob at that commit and the size of the difference (changed
lines, then how many of them are not import or `standardlogin`-to-`sprite` renames: always 0).
Reproduce with
`git -C <PiG> show d86eb93f:piglets/standard/extensions/pigrunner/<file> | diff - extensions/pigrunner/<file>`.

| File (upstream blob) | Changed lines | Other than the import rewrite |
|---|---|---|
| `art.go` (`bbe0c8f4`) | 10 | 0 |
| `bot_playthrough_test.go` (`c9ab9ea3`) | 4 | 0 |
| `component.go` (`daedae02`) | 8 | 0 |
| `extension.go` (`2821fa68`) | 10 | 4 (the disposal statement and its comment, see "Deviation") |
| `extension_test.go` (`ccc9a197`) | 8 | 0 |
| `game.go` (`13b89246`) | 10 | 0 |
| `human_bot_test.go` (`202d7f37`) | 4 | 0 |
| `pigrunner_test.go` (`853e7fcc`) | 22 | 0 |
| `render.go` (`da5ea8c0`) | 4 | 0 |
| `state.go` (`8b4599e3`) | 0 | 0 |

The rewrite: `standardlogin "…/piglets/standard/extensions/piglogin"` to
`"…/pigpen/components/pig-play/libraries/sprite"` (`standardlogin.X` to `sprite.X`; the
`Variant`, `FindVariant`, `ActiveVariant`, `MascotPalette` API is identical), and
`…/piglets/standard/internal/{pixel,arcade,termgame}` to `pig-play/libraries/…`. Nothing else in
game rules, art, rendering, keys, messages or the saved state path (`state/pig-standard/pigrunner.json`).

## After the relocation: the opt-in agent server

The table above counts the relocation only. A later Pigpen change lets an agent play the game over MCP through the shared
library `pig-play/libraries/gamemcp` (off by default; see the README). It is not part of the original and has no upstream
twin: new files `mcp.go`, `mcp_test.go`, `mcp_host_test.go`, `fakeclassifier_test.go`, `export_test.go`; edits to
`extension.go` (the command handler tries the `mcp on|off|status` arguments first, and `openRunner` and `show` split the
old `run`), `component.go` (the key handler is `inputLocked`, shared by the keyboard and queued actions; a ticker frame applies
the queue first), `game.go` (the `AgentLine` field) and `render.go` (the HUD shows it). The game rules, art, keys, messages and
saved state are unchanged, and the 17 mutations below are still all killed. The recorded RPC trace is unchanged by the
change; against the 0.4.0 staging `pig` it differs from the golden only in the `prompt` response's new `disposition` field.

Playing well (later still): `game_state` names obstacles in the game's words (`pie_small`, `pie_large`,
`flying_pie_low|mid|high`) and adds `time_to_impact_ms`, `decision_window_ms`, `decide_now` and `stopped`; while an agent
plays, the run holds at each obstacle's decision point until its answer (new `agentpace.go`; `game.go` gains the
`AIPaced`, `Holding` and `Stopped` fields, an `Obstacle.decided` flag and one check at the top of `Update`); `stop` freezes
the run and any key then closes (`component.go`). A person's game never holds: `AIPaced` is set only by `game_start`.
New tests: `agentpace_test.go`. The upstream twins pass unchanged.

## Tests

Upstream has 31 tests: `pigrunner_test.go` 23, `extension_test.go` 6, `bot_playthrough_test.go` 1,
`human_bot_test.go` 1 (the bot tests play the game). All 31 are twins with the same names and bodies
(import rewrite only). **31 twins, 0 skipped.**

New (`host_test.go`, layer 1 through the fake host): loading the extension activates nothing (no
event handler, tool or host call), `/runner` and `/pig-runner` open one full-terminal overlay
titled "PiG Runner" and report `PiG Runner score N · high M`, the stored high score survives an
overlay that returns nothing, no overlay without a UI, a host error is returned, no ticker goroutine survives a command (red on the upstream code, see below), and the command descriptions. 7 cases in `host_test.go`; 4 more in `values_test.go` (added after the first mutation run).

## Deviation from upstream: one resource leak fixed

`extension.go` has one added statement, `defer component.Dispose()` (`defer game.Dispose()` in
Angry Pigs). Upstream creates the component, whose constructor starts its ticker goroutine, and
then calls `ctx.Custom`, which returns at once when there is no UI (print and JSON mode) and never
disposes the component, so the ticker ran until the process exited. With a UI the SDK disposes it
when the overlay closes, and `Dispose` is idempotent. Not observable in behaviour, output or state;
recorded here for owner veto. Test: `TestCommandLeavesNoTickerRunning` (red on the upstream code).
This is the only difference in `extension.go` besides the import rewrite.

## Differential proof (Go-to-Go)

`pigeq` runs the **unmodified upstream extension** (MichaelKinsy/PiG `d86eb93`,
`piglets/standard/extensions/pigrunner`, in a `git archive` export whose `extensions/sdk` is the staged
SDK of the `pig` under test) and this one in real PiG RPC hosts with the same scripted scenario,
and requires identical traces.

```sh
pigeq record --scenarios port/scenarios --golden port/golden --go-oracle <upstream>/piglets/standard/extensions/pigrunner --pig $PIG
pigeq check  --scenarios port/scenarios --golden port/golden --go extensions/pigrunner --pig $PIG
pigeq run    --scenarios port/scenarios --go-oracle <upstream>/piglets/standard/extensions/pigrunner --go extensions/pigrunner --pig $PIG
```

Scenario: `runner-command` (`/runner`, `/pig-runner`). An RPC host has no terminal overlay, so both commands end with the same `extension_error` ("no_ui: ui.custom requires an interactive TUI") on the original and on the port.

Golden traces (`port/golden`, lane `pig-go-upstream`, recorded 2026-09-29 on PiG 0.3.0 with Pi 0.87.1
in the host banner) are committed and `pigeq check` passes without the upstream checkout. Every
`pigeq run` scenario passed: `pig-go == pig-go-upstream`.

## Mutation check

`port/mutations.json`: 17 deliberate defects (command wiring, an event handler (bundling must not activate), scores and saved state, disposal, jump/duck rules, the speed cap, and two shared-library defects). Results in
[`port/mutation-report.txt`](port/mutation-report.txt): with the layer-1 tests (`--unit`) **17 of
17 killed**; scenarios alone kill 1 (the trace is RPC-level and cannot see terminal cells or
saved state, so the rest are killed by layer-1 tests only). The first run left survivors, which were
missing tests, not deleted mutations; (jump velocity, the mid-air drop, one jump at a time and the speed cap were not pinned; `values_test.go` and `TestCommandDescriptions` were added). Review (`rev-pigpen-games`) added `game-listens-to-tool-results`: a game that registers any other event handler survived every test (the event list was fixed); the test now pins the whole handler set.

## Not proven by the trace harness

`ui.custom` overlays are not visible to `pigeq` (RPC traces carry no terminal cells), so the
differential lane covers registration and the command's host calls only. The rendering is
covered by the twin tests (`TestRenderFillsTheViewAtEverySize`, allocation and redraw tests) and
by the real-binary terminal run described in the lane report.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the unmodified upstream Go extension (PiG d86eb93f217e) under PiG, with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
