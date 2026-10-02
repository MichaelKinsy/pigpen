# Port record: dirty-repo-guard

| Input | Identity |
|---|---|
| Original | `packages/coding-agent/examples/extensions/dirty-repo-guard.ts`, Pi v0.87.1, commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe`, MIT (Mario Zechner) |
| Oracle | Pi 0.87.1 (`extensions/sdk-ts/node_modules/.bin/pi` of the PiG tree), Node 24.19.0 |
| Target | PiG 0.3.0+0.87.1 from `staging/team/lead/release-all-next` `63c6ba456bf5edd35c8042ce420ad700dd59a4ad`, go1.27.1 |
| Original's tests | none upstream; the scenarios are derived from every branch of the source |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `pi.on("session_before_switch")`, `event.reason === "new" ? "new session" : "switch session"` | `OnEvent("session_before_switch")`, `reason` from the event data | decline, proceed; `TestActionWordingFollowsTheEvent` (reason `resume` and none) |
| M2 | `pi.on("session_before_fork")` | `OnEvent("session_before_fork")` | fork-decline, fork-proceed |
| M3 | `await pi.exec("git", ["status", "--porcelain"])` | `ctx.Exec("git", ...)` | every scenario; `TestRunsGitStatusPorcelain` |
| M4 | `code !== 0` allows (not a repository) | `decide`: code != 0 allows | not-a-repo, git-canned-failure, git-missing (spawn failure resolves with code 1 in Pi) |
| M5 | `stdout.trim().length > 0` | `len(jsTrim(stdout)) > 0` | git-canned-whitespace (U+00A0, U+FEFF); `TestJSTrim` |
| M6 | `!ctx.hasUI` cancels | `!ctx.HasUI()` cancels | **not reachable through RPC**; `TestHeadlessDirtyCancelsWithoutAsking` (fake host, print and JSON mode) |
| M7 | `split("\n").filter(Boolean).length` | `countChangedFiles` | git-canned-dirty; `TestCountChangedFiles` |
| M8 | `ctx.ui.select(title, [yes, no])`, `choice !== yes` cancels (including `undefined`) | `ctx.Select`, `!ok \|\| choice != allow` | decline, dismissed, proceed; `TestAnyOtherChoiceCancels` |
| M9 | `ctx.ui.notify("Commit your changes first", "warning")` | `ctx.Notify(..., "warning")` | decline, dismissed, fork-decline |
| M10 | return `{ cancel: true }` | return `map[string]any{"cancel": true}` | new-session and fork cancelled notices from the harness driver |

SDK gap check (`pigeq gaps`): none. The original uses `pi.on`, `pi.exec`, `ctx.hasUI`,
`ctx.ui.select` and `ctx.ui.notify`, all implemented for Go.

## Results (this revision)

- `pigeq run` (original under Pi, original under PiG's Node runtime, port under PiG): 11 of 11
  scenarios identical, host check and port check.
- `pigeq check` against the golden traces: 11 of 11.
- Mutations: 26 of 26 killed. 24 by the scenarios and the `exec-coverage` check alone (`pigeq mutate`
  without `--unit`); the two `hasUI` mutations (M6) survive the scenarios by construction and are killed by
  the fake-host tests (`pigeq mutate --unit`). Nine of the 26 were added by the adversarial review
  (`rev-pigpen-extension-porter`) to prove the harness catches each kind of defect: a notification moved
  before the dialog, one changed character in the notification and in an option, an off-by-one count, an
  extra status call, an error instead of a cancellation, an extra `git` call, `git` by absolute path and an
  undeclared command (the last two by `exec-coverage`).
- Golden traces are projection `v2` (full tool definitions and the system-prompt difference in model
  requests, final quiet period), re-recorded from Pi 0.87.1 with the host check (Pi == PiG's Node runtime)
  on every scenario.
- `go test -race` and `GOOS=windows|darwin|linux go vet`: pass.

## Findings about the hosts made while porting

1. **PiG's RPC loop blocks on `new_session`.** A dialog raised by a `session_before_switch` handler
   during the RPC `new_session` command cannot be answered: the loop handles the command inline and
   never reads the `extension_ui_response` (Pi handles input lines concurrently). The scenarios start
   session operations through slash commands of the harness driver instead.
2. **PiG does not raise Pi's stale-context error** when an extension uses a captured command context after
   `newSession()`. Pi reports `extension_error: This extension ctx is stale ...`.
3. **`withSession` is missing in the Go SDK, and a TypeScript extension's `withSession` notification is
   not delivered under PiG's RPC UI.** The gap scanner reports `ctx.newSession(options.withSession)`.
