# Port record: herdr (herdr-pig's agent-state extension)

Kind 1: a Pi TypeScript extension ported to Go. Oracle: the unmodified original under Pi (`record`, `run`), plus a
handler-level replay of the original in Node (`port/drive`, below) for everything an RPC host cannot reach.

| Input | Identity |
|---|---|
| Original | `extensions/herdr-agent-state.ts` (276 lines) of [Hai-Fai-Solutions/herdr-pig](https://github.com/Hai-Fai-Solutions/herdr-pig) v0.2.1, commit `3ec680471e8282e0a8a64545781069f822c0c212`, Apache-2.0 (Hai-Fai Solutions), itself based on herdr's Pi integration. Not on npm; the install form is `git:github.com/Hai-Fai-Solutions/herdr-pig@3ec680471e8282e0a8a64545781069f822c0c212` or `herdr plugin install Hai-Fai-Solutions/herdr-pig`. Vendored byte for byte in `port/oracle` with its `LICENSE` and `NOTICE`; its tests in `port/upstream/test` |
| Oracle host | Pi `1402973a9dbe8c627341e04c9744c54953e285ca` (`pi --version` prints `1.3.0.1402973a`), Node 24.19.0 |
| Target | PiG 0.5.0 (`0.5.0+1.3.0.1402973a`, built from PiG `6af0806f7a`), go1.27.2 |
| Original's tests | 7 cases in `test/herdr-agent-state.test.ts`; its `test/pig-support.test.ts` (the plugin scripts) is covered under "The plugin" below |

## What the original does

It reads `HERDR_ENV`, `HERDR_SOCKET_PATH` and `HERDR_PANE_ID`; outside herdr it registers nothing. In a TUI session it keeps the
agent's state (`idle`, `working`, `blocked`) and the command that resumes the session, and tells herdr over its
socket: one connection per request, one JSON line `{id, method, params}` (`pane.report_agent`, `pane.release_agent`), the
first data back is the answer, 0.5 s and then, if that failed, one more attempt of 1.5 s with the same request. It
releases the pane only when PiG quits.

## Mapping

| ID | Original (`ts:` line) | Go (`extensions/herdr`) | Proof |
|---|---|---|---|
| M1 | enabled: `HERDR_ENV === "1"` and a socket path and a pane id (25-27) | `readEnv`; `HERDR_BIN_PATH` is not part of it | twin 1; drive `outside-herdr`, `env-*`, `no-*`, `bin-path-not-needed`; `TestOutsideHerdr` |
| M2 | Windows socket is the named pipe `\\.\pipe\<name>` (21-22) | `endpointFor`, `transport_windows.go` | `TestTransport` (the name); **the pipe transport itself is unverified: no Windows host** (finding F3) |
| M3 | `sendRequestAttempt`: connect, write the line, any data back is delivery, `end` or `error` or the timeout is failure (29-55) | `attempt` (`transport_other.go`) and `exchange` | `TestTransport`; drive `retry-*`, `both-*` |
| M4 | `sendRequest`: 0.5 s, then one attempt of 1.5 s with the same request (57-62) | `reporter.send`, `firstAttempt`, `retryAttempt` | drive `retry-after-close`, `retry-after-silence`, `both-attempts-fail`; `TestTransport` |
| M5 | `reportSeq = Date.now() * 1000`, +1 per report (74-80) | `nextSeq`: `max(last+1, clock in microseconds)`, one counter per process | twin 3, 6; drive `seqFromClock` and the rank of every seq |
| M6 | request id `pig:<kind>:<ms>:<random>` (82-84) | `requestID` | drive (shape), `TestRequestShape` |
| M7 | `isSafeResumeArg`: a non-empty string without `'` and U+0000-U+001F, U+007F (86-91) | `isSafeResumeArg` | twin 7; drive `resume-*`; `TestResumeCommand` |
| M8 | `resumeArgvFor`: `pig --session <file>` for an absolute posix or win32 file that is safe, else `pig --session <id>` for a safe id, else none; a getter that throws is undefined (93-118) | `resumeArgvFor`, `sessionIdentity` | twin 7; drive `resume-*` (22 cases) |
| M9 | `sendState`: params `pane_id, source, agent, state, message, seq, resume_argv`, undefined fields left out (120-135); source and agent are `pig` (17-18) | `statePayload`, `encodeLine` (no HTML escaping, U+2028/2029 raw, as `JSON.stringify`) | twin 2; drive (byte-exact lines) |
| M10 | `sendRelease`: `pane.release_agent` with `pane_id, source, agent, seq` (137-143) | `reporter.release` | twin 6; drive `quit*` |
| M11 | `queueState`/`drainStateQueue`: one request at a time, a newer state replaces a queued one, the resume command is read when the request is built (145-174) | `queueLocked`, `drain` | drive `coalesce-behind-slow-herdr`, `queued-resume-at-send`; `TestStateReports` |
| M12 | `publishState`: `desiredState` (blocked, else working, else idle); unchanged state, message and resume command are not sent again (190-215) | `desiredLocked`, `publishLocked` (`jsValue.strictEquals`: `===`, objects by reference) | drive `turn-repeated-events`, `resume-changes-mid-turn`, `bus-*-twice`; `TestStateReports` |
| M13 | `herdr:blocked`: `!data?.active` (JavaScript truthiness) decrements to a floor of 0, clears the label at 0; else increments and keeps `data.label` as given (219-235); ignored unless the TUI session started | `Events().On("herdr:blocked")`, `truthy`, `block`/`unblock` | twin 5; drive `bus-*` (40 cases); `TestBlocked` |
| M14 | `session_start`: only in `tui`; resume command; `agentActive = isIdle() === false`; always publishes (237-247) | `OnSessionStart`, `reporter.start` | twin 2, 4; drive `startup-idle`, `start-*`, `mode-*` |
| M15 | `agent_start`: refresh the resume command, working (249-256) | `OnEvent(agent_start)` | twin 3; drive `turn`, `resume-changes-mid-turn` |
| M16 | `agent_settled`: idle only when `isIdle() === true` (258-264) | `OnEvent(agent_settled)`; a failed `IsIdle` is the handler's error, as a throwing `isIdle()` is | twin 3; drive `settled-*` |
| M17 | `session_shutdown`: only reason `quit` releases; queued state dropped; `rootSession` false (266-275); the release does not wait for a request in flight | `OnSessionShutdown`, `reporter.release` | twin 6; drive `shutdown-*`, `quit*`, `quit-drops-queued` |

SDK gap check (`pigeq gaps --ts port/oracle/herdr-agent-state.ts`): see the results below; the original uses `pi.on`,
`pi.events.on`, `ctx.mode`, `ctx.isIdle` and `ctx.sessionManager.getSessionFile/getSessionId`, all provided to Go.

## Twins

`pigeq twins check --ledger port/upstream-tests.json --go extensions/herdr`: 7 exact twins, 0 skipped (the ledger `port/upstream-tests.json` is `pigeq twins list --tests port/upstream/test`, unchanged)

## Differences from the original

Behavior the original does not have, kept or added on purpose, each with its reason and what it costs in fidelity.

| ID | Difference | Why | Observable to herdr? |
|---|---|---|---|
| D1 | Prompt-based `blocked` (`ui_prompt_start` / `ui_prompt_end`), **off by default**; on with `PIGPEN_HERDR_UI_PROMPTS=1` | PiG tells every extension when a dialog opens; Pi has no such event and the original never reports a prompt. By default the port reports exactly what the original reports in the same session | Only with the opt-in |
| D2 | One line on stderr when herdr did not take a request (once per failure until a request is delivered) | herdr-pig fails silently; a pane herdr never hears about is otherwise impossible to diagnose. The line came from the herdr-agent-state extension of pigpen PR 2 (Yu Li) | No: stderr is PiG's extension output |
| D3 | A shutdown that is not a quit leaves the reporter as it is, and `session_start` resets nothing (the blocked counters carry over) | As the original; owner decision 2026-10-20. See F4 for what a host that keeps the extension alive then sees | No |
| D4 | The seq is one counter per process, `max(last + 1, clock)`, not `Date.now() * 1000` per module | Several instances in one process (a replacement) must not go back; herdr drops a lower seq | The values differ, the order does not |

Removed from the previous Go port, because the original does not do it and herdr can observe it: the `herdr pane
report-agent` CLI transport and the `HERDR_BIN_PATH` requirement, source `custom:pig`, `--agent-session-path` and
`--agent-session-id`, the resume command `pig --session-id <id>` (the original sends `pig --session <id>`, also for an in-memory
session) and the omission of a command for one, the 64-argument and 8 KiB limits, the retry without a resume command
after an old herdr refused it, whitespace folding and a 120-character cut of a `herdr:blocked` label, and the 3 s single call.

## The plugin (`piglets/herdr/herdr-plugin`)

herdr-pig also ships a herdr plugin (`herdr-plugin.toml`, `scripts/pig-support.sh` and `.ps1`, tests in `test/pig-support.test.ts`) that
copies the TypeScript extension into PiG's agent directory. Pigpen's plugin is its own, adapted to install the Go Package with
`pig install` (PiG installs packages; there is no file to copy), so its commands differ and its tests are its own
(`piglets/herdr/tests/plugin.test.mjs`, run by `npm run test:pig`), not twins of `pig-support.test.ts`. What the original's plugin
does, and where it stands:

| Original | Pigpen's plugin |
|---|---|
| `install`, `uninstall`, `status` actions and the `build` step, each with a PowerShell form | Same actions and forms; they run `pig install`, `pig remove`, `pig list` |
| Refuses `PIG_USE_PI_DIRS=1` | Same |
| Refuses to overwrite or remove a file it does not manage (ownership marker) | `pig install`/`pig remove` own the Package; the marker is used to replace herdr-pig's old copy |
| `[[startup]]` **`refresh`**: if the installed extension differs from the plugin's copy, copy it; never installs on its own | **Ported.** The plugin pins the Package version it was released with (`PACKAGE_VERSION`, kept equal to `components/herdr/package.json` by a test). `refresh` replaces an installed pinned entry of another version by the pin (`pig remove`, `pig install`), never installs on its own, leaves an unpinned entry, a `PIGPEN_HERDR_PACKAGE` override and a missing `pig` alone, and never fails herdr's startup. `status` says "outdated", `install` updates |

Unverified: the shape of a `pig list` line for an npm source (the scripts look for `npm:@pi-in-go/pigpen-herdr@<version>` anywhere in
the output; the tests use a fake `pig`, and no npm registry was available to install the Package for real), and the PowerShell script
(never run: no Windows host and no `pwsh`).

## Findings about the hosts made while porting

F1. **The RPC host cannot show the original working.** `ctx.mode` is `rpc` in every `pigeq` lane, and the original (and so
the port) does nothing outside `tui`. The `pigeq` scenarios therefore prove the silent paths under Pi and PiG; everything
else is proven by the twins, the drive replay (the original's own handler contract, run in Node) and a TUI run
(`piglets/herdr/tests`, real `pig` in tmux against a fake herdr socket).
F2. **The scenario harness has no socket server**, so a scenario cannot receive the herdr requests. A mutation that
reports in RPC mode is therefore killed by the layer-1 tests and the drive, not by a scenario (`notify-in-every-mode` is the
mutant the scenarios kill).
F3. **The Windows named pipe is not exercised.** The original opens `\\.\pipe\<name>` with Node's `net`; the port opens
it as a file and cancels pending I/O at the deadline (`CancelIoEx`). `GOOS=windows go vet` passes; no Windows host ran it.
F4. **Under Pi a session replacement is a new extension instance; PiG may keep the Go one.** Pi shuts the old extension down
and loads a fresh copy of the module for the next session (`core/agent-session-runtime.ts`, `core/extensions/loader.ts`
`moduleCache: false`), so the original's blocked counters start at zero in every session. The port, as the owner decided, does not reset
them at `session_start` (the original does not either, on one instance). If the PiG host keeps one Go extension instance across
`/new`, `/resume` and `/fork`, a `herdr:blocked` block that was never released stays held after the replacement, where it would not under
Pi. This is not tested against a real PiG replacement here (the harness reaches no TUI); `TestSession` and the drive case
`second-session-start` pin the same-instance behavior.

## Results (this revision)

Run on xd670 (tern-ci:v3, CPUs 64-127), with `pigeq env`, Pi `1402973a` (`1.3.0.1402973a`) and the PiG `0.5.0+1.3.0.1402973a` binary, Node 24.19.0, go1.27.2:

- `pigeq gaps --ts port/oracle/herdr-agent-state.ts` and `pigeq gaps --go extensions/herdr`: no SDK gaps, no PiG-internal import or fuse hazard.
- `pigeq twins check`: 7 exact twins, 0 skipped.
- `pigeq record` (original under Pi): 3 scenarios recorded, host check pi-ts == pig-ts on all three. `pigeq run`: port == Pi on 3 of 3
  (`outside-herdr` 10 events, `rpc-mode-silent` 10, `rpc-new-session` 11). `pigeq check` against the committed goldens: 3 of 3 (without Pi).
- Drive replay: 98 cases, 176 request lines; the Go port reproduces the original's lines byte for byte. The golden was re-recorded
  from the original in a second run and is identical (`golden-reproduced`).
- `go test -race -count=4` in `extensions/herdr`: pass (83 s). `GOOS=linux|darwin|windows go vet`: pass.
- `pig install extensions/herdr --validate-only --json`: `valid: true`, `form: factory`, `language: go`; `pig package validate components/herdr`: valid.

Mutation run on xd670 (`pigeq mutate --unit --jobs 4`, 71 mutants, before the fixes below): 71 mutants, 6 not killed. The last 100 lines of
that log show 3 survivors and 1 invalid mutant, all handled in the next commit: `dedupe-ignores-resume-presence` and
`release-keeps-message` are equivalent mutants (a resume argv is never empty; a cleared label is overwritten by the next block) and were
removed; `backslash-before-separator-ignored` is killed by a new `TestRequestShape` case (a label that spells `\u2028`); `diagnostic-repeats`
did not build and is rewritten (`if !repeat || true`). The other two not-killed entries scrolled out of the captured tail and are
unknown: the re-run that prints every non-killed mutant (`~/pp-smallports/herdr/stepH.log` on xd670) had not finished. All mutants
killed is therefore NOT claimed.

Not yet run when this was written (queued behind other lanes on the shared lock; logs `~/pp-smallports/herdr/stepF.log`, `stepP.log` on
xd670): `pigeq mutate` over the 71 mutations in `mutations.json` (the unit tests of the unmutated port pass; an earlier attempt stopped
at a mutation whose find text a refactor had changed, since fixed), the real-pig tmux test (`npm run test:pig`) and the performance
measurement. The PR body says so.

