# Port record: pi-posthorse

| Input | Identity |
|---|---|
| Original | `pi-posthorse` 0.10.11 by Mitch Fultz, https://github.com/fitchmultz/pi-posthorse, commit `c8596fff2cec7123ef37486fd9701a24019c934f`, MIT; unmodified copy at `port/oracle` |
| Oracle | Pi 1.5.0 (`@earendil-works/pi-coding-agent` 1.5.0), Node 24.22.1, typebox 1.3.27 |
| Target | PiG 0.7.0+1.5.0 (`fix/071-next` 979bf718f7), go1.27.3; Pigpen pin must be 0.7.x (branch `feat/pig-0.7-requirement`) |
| Original's tests | 152 ledger cases (`port/upstream-tests.json`): **93 exact twins, 59 named skips** (TS lint/release tooling); plus `test/official-host.ts` (53) and `test/native-host.ts` (34) real-Pi suites: [coverage-ScenarioOfficial.md](coverage-ScenarioOfficial.md), [coverage-ScenarioNative.md](coverage-ScenarioNative.md) |
| Kind | 1: a Pi TypeScript extension |

## Contract tables, findings, named skips (one record per lane)

[CoreLane](lane-CoreLane.md) (foundation, budget, guidance, paging, files, context tools, registration) |
[HistoryLane](lane-HistoryLane.md) | [NotesStoreLane](lane-NotesStoreLane.md) | [NotesToolsLane](lane-NotesToolsLane.md) |
[RecoveryLane](lane-RecoveryLane.md) | [RolloverLane](lane-RolloverLane.md) | [UiLane](lane-UiLane.md) (incl. tmux comparison and the click proposal) |
[TestInfra](lane-TestInfra.md) | [HarnessExt](lane-HarnessExt.md) | [ScenarioOfficial](lane-ScenarioOfficial.md) | [ScenarioNative](lane-ScenarioNative.md) |
[PerfLane](lane-PerfLane.md).

Reviews (all parity-breaks fixed unless listed in the lane notes): [review/](review/), audit of the evidence [review/coverage.md](review/coverage.md).
Red logs (retroactive: the twins run against stubs, see `red/`): honest deviation from red-first.

## Results

See `~/PiG-launch-newer/handoffs/0.7.1/posthorse.md` summary: 117/117 scenarios identical to goldens recorded from the original under Pi;
original under PiG-Node == Pi on all recorded scenarios; host differences kept in `scenarios-hostdiff/` and `scn-findings/` (not normalised).

## Mutation check (`pigeq mutate --unit-only`, lists and equivalence reasons in [mutations/](mutations/))

Run on frozen self-contained snapshots with per-shard `TMPDIR` (the stock command on the shared tree gave false kills; commands in `mutations/UiLane.md`).

| Lane | Files | Mutants | Killed by Go tests | Equivalent (removed or documented) |
|---|---|---:|---:|---|
| CoreLane | contracts, js, message_text, files, session_lines, budget, guidance, paging, reset_requests, context_tools, extension | 114 | 114 (98 first run, 16 after tests, re-run confirmed) | 0 |
| HistoryLane | history*.go, session_files.go | 254 | 254 | 8 removed |
| NotesStoreLane | note_scopes/snapshots/store/inventory/discovery/provenance/evidence | 347 | 347 | 41 removed, `disc-digest-abort` untestable |
| NotesToolsLane | notes.go, note_tool_effects.go, note_guards.go | 340 | 340 (three runs, each against the tests that kill them) | 33 removed or untestable |
| RecoveryLane | recovery*.go | 299 | 299 | 8 removed |
| RolloverLane | retention.go, reminders.go, rollover.go | 227 | 227 | 4 removed |
| UiLane | ui*.go and the displayXxx sites | 276 | 269 | 7 equivalent, kept with reasons |

## Fused Piglet Binary

`npm run build:piglet -- pig-posthorse` (pig 0.7.0+1.5.0, PiG source 0382a61c): "Preparing fused Go members: posthorse" ok, 72,106,144 bytes.
`pigeq check --builtin` against the Pi goldens cannot compare `llm.system` (no baseline prompt in builtin mode) so the Binary's own traces
were recorded (`record --self --builtin`, 117 scenarios) and compared with the Pi goldens by `port/tools/builtin-compare.py`
(ignores llm.system, the Piglet's `addendum` prompt section, and tool order): **106 of 117 identical**. The 11 others all load a helper
`-e` extension whose handler order relative to posthorse differs in a Binary (the fused extension loads first): nat-guidance-idle-wakeup-forced-user-to-custom,
nat-guidance-survives-tool-additions-forced-before, nat-settlement-in-reminder-band-{boundary,followup,steer}-stop, off-automatic-cancellation-checks-active-turn-signal,
off-earlier-redaction-drafts-defer-fallback, off-earlier-turn-start-drafts-leave-fallback-pi-owned, off-explicit-reset-preserves-state,
off-fresh-window-truncated-answers-native-retry, off-guidance-follows-model-change-forced. Composition order is observable by design (the
original's README says two policy extensions make the result depend on load order); they are not port defects but are not proven by the Binary run.

## Evidence-audit items still open
C-13..C-16 owner items (key order, click expansion, stderr, perf of the cold reminder walk) are in the decisions list of the hand-off.
