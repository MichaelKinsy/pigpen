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

Reviews (all parity-breaks fixed unless listed in the lane notes): [review/](review/). Mutations: [mutations/](mutations/).
Red logs (retroactive: the twins run against stubs, see `red/`): honest deviation from red-first.

## Results

See `~/PiG-launch-newer/handoffs/0.7.1/posthorse.md` summary: 117/117 scenarios identical to goldens recorded from the original under Pi;
original under PiG-Node == Pi on all recorded scenarios; host differences kept in `scenarios-hostdiff/` and `scn-findings/` (not normalised).
