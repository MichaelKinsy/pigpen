# Port record: angry-pigs (relocation of PiG Standard's `angrypigs`)

A **Go-to-Go relocation**, not a Pi port: there is no Pi oracle. Original: MichaelKinsy/PiG
`piglets/standard/extensions/angrypigs` at `d86eb93f217e64b655e9ee6f48c93a9afd107697` (MIT, Copyright (c) 2026 Michael Kinsy, Michael Kinsy). Upstream files are not
vendored; each row names the git blob at that commit and the size of the difference (changed
lines, then how many of them are not import or `standardlogin`-to-`sprite` renames: always 0).
Reproduce with
`git -C <PiG> show d86eb93f:piglets/standard/extensions/angrypigs/<file> | diff - extensions/angrypigs/<file>`.

| File (upstream blob) | Changed lines | Other than the import rewrite |
|---|---|---|
| `art.go` (`6cbf9d3b`) | 2 | 0 |
| `component.go` (`8beace25`) | 10 | 0 |
| `extension.go` (`bca5f82f`) | 10 | 4 (the disposal statement and its comment, see "Deviation") |
| `game.go` (`c17ce95d`) | 0 | 0 |
| `game_test.go` (`672febaa`) | 28 | 0 |
| `particles.go` (`42ed5090`) | 0 | 0 |
| `render.go` (`ff193168`) | 4 | 0 |
| `state.go` (`c527773d`) | 0 | 0 |

The rewrite is the one described in `pig-runner/port/PORT.md`: `standardlogin` to
`pig-play/libraries/sprite`, and `internal/{pixel,arcade,termgame}` to `pig-play/libraries/…`.
Game rules, physics, levels, art, rendering, keys, messages and the saved state path
(`state/pig-standard/angrypigs.json`) are unchanged.

## After the relocation: the opt-in agent server

The table above counts the relocation only. A later Pigpen change lets an agent play the game over MCP through the shared
library `pig-play/libraries/gamemcp` (off by default; see the README). It is not part of the original and has no upstream
twin: new files `mcp.go`, `mcp_test.go`, `mcp_host_test.go`, `fakeclassifier_test.go`, `export_test.go`; edits to
`extension.go` (the command handler tries the `mcp on|off|status` arguments first, and `openAngryPigs` and `show` split the
old `run`), `component.go` (the input handler is `inputLocked`, shared by the keyboard and queued actions; the loop applies
the queue first), `game.go` (`lastShot`, set by `finishShot`; the aim preview is `aimedFlight`, which also gives the landing
point) and `render.go` (the HUD shows the agent line). The physics, scoring, art, keys, messages and saved state are
unchanged, and the 17 mutations below are still all killed. The recorded RPC trace is unchanged by the change; against
the 0.4.0 staging `pig` it differs from the golden only in the `prompt` response's new `disposition` field.

Playing well (later still): `game_state` adds `landing_vs_target` ("on target", "short by N px", "long by N px") and
`landing_vs_target_px`; the game's `instructions` say what each action does to the shot; `stop` freezes the game and any
key then closes (`component.go`, `mcp.go`). The physics are unchanged. New tests: `agentplay_test.go`. The upstream twins
pass unchanged.

## Tests

Upstream has 26 tests in `game_test.go` (game rules, levels, physics, preview, camera, mouse,
render sizes, resize, sprite selection, component controls and disposal, allocation churn, the
registration over a raw pipe, and the shared runner style). All 26 are twins with the same names
and bodies (import rewrite only). **26 twins, 0 skipped.**

New (`host_test.go`, layer 1 through the fake host): loading activates nothing, `/angry-pigs`
opens one full-terminal overlay titled "Angry Pigs" and reports `Angry Pigs score N · high M`,
the stored high score survives an overlay that returns nothing, no overlay without a UI, a host
error is returned, no ticker goroutine survives a command (red on the upstream code, see below), and the command descriptions. 7 cases in `host_test.go`; 3 more in `values_test.go` (added after the first mutation run).

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
`piglets/standard/extensions/angrypigs`, in a `git archive` export whose `extensions/sdk` is the staged
SDK of the `pig` under test) and this one in real PiG RPC hosts with the same scripted scenario,
and requires identical traces.

```sh
pigeq record --scenarios port/scenarios --golden port/golden --go-oracle <upstream>/piglets/standard/extensions/angrypigs --pig $PIG
pigeq check  --scenarios port/scenarios --golden port/golden --go extensions/angrypigs --pig $PIG
pigeq run    --scenarios port/scenarios --go-oracle <upstream>/piglets/standard/extensions/angrypigs --go extensions/angrypigs --pig $PIG
```

Scenario: `angry-pigs-command`. An RPC host has no terminal overlay, so the command ends with the same `extension_error` ("no_ui: ui.custom requires an interactive TUI") on the original and on the port.

Golden traces (`port/golden`, lane `pig-go-upstream`, recorded 2026-09-29 on PiG 0.3.0 with Pi 0.87.1
in the host banner) are committed and `pigeq check` passes without the upstream checkout. Every
`pigeq run` scenario passed: `pig-go == pig-go-upstream`.

## Mutation check

`port/mutations.json`: 17 deliberate defects (command wiring, an event handler (bundling must not activate), scores and saved state, disposal, launch physics, aiming limits, tuning constants, and a shared-library defect). Results in
[`port/mutation-report.txt`](port/mutation-report.txt): with the layer-1 tests (`--unit`) **17 of
17 killed**; scenarios alone kill 0 (the trace is RPC-level and cannot see terminal cells or
saved state, so the rest are killed by layer-1 tests only). The first run left survivors, which were
missing tests, not deleted mutations; (gravity, maximum speed and the scoring constants were referenced symbolically by upstream tests; `values_test.go` and `TestCommandDescription` were added). Review (`rev-pigpen-games`) added `game-listens-to-tool-results`: a game that registers any other event handler survived every test (the event list was fixed); the test now pins the whole handler set.

## Not proven by the trace harness

As for pig-runner: `ui.custom` overlays are not visible to `pigeq`. Rendering and input are
covered by the twin tests and the real-binary terminal run in the lane report.

## Re-verified on PiG 0.4.1 (porter-verify)

The golden traces were recorded again from the unmodified upstream Go extension (PiG d86eb93f217e) under PiG, with PiG 0.4.1 content (`5f948f86a`, `pig --version`
`0.3.1+1.0.1`) and normalizer v3, because Pi 1.0.x changed the trace format: a `prompt` response now carries
`data.disposition` (Pi 0.99.0, #9098). An event-by-event diff against the previous traces shows no other
difference, and `pigeq check` passes on the Go port. Details: `docs/plan/progress/porter-verify.md`.
