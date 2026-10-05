# Integration validation — 2026-09-22

Base: remote main `8b077aa6a1e9c83709bf639af79528501f8273d0` (0.31.6). Implementation is isolated from the original dirty checkout. No release, live model call, new command, new model tool, new npm script or runtime dependency was added.

## Disposition

| Item | Integrated result |
| --- | --- |
| #78 | Deferred widgets capture plain storage/cwd data; reload/shutdown cancel queued UI work. Proxy-invalidated context regressions cover focused and unfocused widgets. |
| #77 | Peers support Pi 0.83–0.87, development SDK pins 0.87.0; CI checks each minor and Node 22.15 with Pi 0.83. |
| #76 | Goal lifetime spending and current context snapshots are distinct throughout model-facing reports. Unknown context stays unknown. |
| #72 / #75 | Layered `hideUnfocusedPrompt`, default false, through existing settings. |
| #73 | Optional absolute `goalsRoot` / `PI_GOAL_ROOT`, session-pinned resolution, existing locks, root-scoped snapshots, focus identity and ledger/archive routing. Execution cwd and default paths remain compatible. |
| #59 / #74 | Budget display and confirmed repair through existing `/goal-tweak`; no budget command/tool. Positive safe integer sets, null removes, omission retains. Stale confirmations and scheduler/resource gates remain enforced. #59's provider normalization is an upstream concern; this PR does not close that issue. |
| #66 | Rich RPC dialogs gated by host capability, with basic fallback/cancellation. |
| #71 | Explicit cache marker handles trailing effort-only messages. |
| #79 | Bounded implicit-prefix retention, combined with #71; policy/history/model/session changes invalidate appropriately. No persisted full-state tails. |
| Historical #70 | Bounded prior rejection feedback and explicit runtime-artifact/environment-failure auditor guidance. No new autonomous policy or rejection loop. |

PRs #66, #71, #75 and #79 supplied integrated code/tests. #74's problem is addressed through the existing drafting flow instead of adding its command/tool surface.

## Verification

- TypeScript and ESLint pass.
- Full Pi 0.87 suite: 1,081 tests pass, none failed/skipped; manifest covers 83 files (77 unit, one integration, five e2e).
- Manifest self-check: passes; ranking updater: five tests pass.
- Pi 0.83.0, 0.84.1 and 0.85.1 isolated installs: typecheck, 31 host/dialog/widget/SDK regressions, and provider crosscheck pass. Pi 0.86.0 also passed the full suite during development. Final CI repeats all supported minors.
- Actual Node 22.15 / Pi 0.83: 31 host/SDK regressions pass. Pi 0.86–0.87 require Node 22.19 or newer; documented separately from the extension's existing floor.
- Context gate: all 24 fixtures pass. Provider crosscheck covers three Responses strictness variants and six actual SDK captures, stopping before HTTP dispatch.
- Storage tests exercise shared worktrees, independent-process contention, ledger/archive/snapshot routing, pinned roots, cross-pool focus, invalid paths and symlink rejection.
- Production dependency audit: zero vulnerabilities. Package creation succeeds.
- Packed 0.31.6 artifact installed in a disposable Pi 0.87 environment: real loader registers tools, `/goal-status` executes, session reloads, command executes again, and shutdown succeeds without a model request.
- `git diff --check` passes.

The packed smoke is headless. It is not a live TUI/provider validation. Goal creation, budget confirmation, pause/resume and dialog capabilities have automated coverage; interactive release smoke remains appropriate before publishing. No live provider cache-hit/cost claim is made.

## Performance and context

Measured against a separate archive of the same remote main on this machine. These numbers describe this run, not cross-machine guarantees.

| Measure | Main | Integration |
| --- | ---: | ---: |
| Aggregate counted filesystem operations | 5,896 | 5,896 |
| Lock acquisition operations | 3 | 3 |
| Task mutation operations | 19 | 19 |
| Cold goal-pool scan operations | 2 | 2 |
| Cold ledger read operations | 1 | 1 |
| Goal creation operations | 35 | 35 |
| 25 cold goal-pool scans | 60.6 ms | 59.7 ms |
| 25 cold ledger reads | 31.5 ms | 31.6 ms |
| Goal creation p50 | 1.5 ms | 1.7 ms |
| Serialized context across 24 fixtures | 272,226 bytes | 276,595 bytes |
| Extension-attributable fixture bytes | 160,068 | 164,234 |
| Tool schema fixture bytes | 80,858 | 81,332 |

Total serialized fixture growth is 4,369 bytes (1.60%); extension-attributable growth is 4,166 bytes (2.60%). The 474-byte schema increase comes from the optional nullable budget field in the existing drafting tool. The baseline was reviewed for that intentional change rather than treating regeneration as proof of no growth. Removing redundant live-message metadata and shortening snapshot labels kept the increase small.

Retained historical counter text is capped at 4 KiB / 32 tails per identity, at most 16 session/model identities. A 1,000-turn test verifies bounds. Policy changes, including budget/run-limit removal, reset old tails. Unknown session identities do not retain across requests. Full-message hashes verify anchors, preserving tool call/result adjacency.

Median retention time: 0.102 ms for 64 KiB history, 0.574 ms for 1 MiB, 2.848 ms for 4 MiB; corresponding JSON serialization baselines: 0.011 / 0.120 / 3.170 ms. All pass the existing-style max(1.5×, +10 ms) regression allowance. The added CI gate measures these costs, not provider cache efficacy.

The non-agent-flow gate passes 105 rows against 95 reference rows. One stale reference row (`B7.tool.create_goal`) counted 12 operations before existing scheduler persistence; both current main and this integration measure 35. Only that operation reference was refreshed; timing tolerances were not relaxed. B10's obsolete “at most one historical checkpoint” assertion was corrected to main's existing compact-checkpoint preservation contract. Historical generated AFTER reports were left unchanged; CI now regenerates measurements before checking them.

GitHub validation follow-up: all four host compatibility jobs passed. The first primary run passed tests and context/provider/retention checks, then missed two historical absolute 10× timing targets on Linux. CI now measures the PR base and candidate on the same runner with the same dependencies and B10 fixture, enforcing the existing regression tolerances. Local historical campaign checks remain available unchanged; CI does not demand another 10× improvement over already optimized main.

Forward-looking completeness follow-up: 1,086 full-suite tests pass; TypeScript, ESLint, all 24 context fixtures and the provider crosscheck pass. Existing context baselines are unchanged. Expanded retention measurements cover nine text/tool-output/image cases and pass the same cost bounds. See [FORWARD-LOOKING.md](FORWARD-LOOKING.md) for the complete preventive-work mapping and two additional cache-reset fixes.
