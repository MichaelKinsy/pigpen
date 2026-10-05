# Comprehensive optimization results

Baseline: `5e16557`, the merged first optimization campaign. The second pass covers all 51 production TypeScript modules, including the added text cache; see [AUDIT.md](AUDIT.md) for each subsystem and retained costs.

## Measured changes

Real SDK 0.84.1, Node v26.7.0, this machine. Each row reports per-operation p50 from 21 repeated samples; tiny paths repeat 100–1,000 operations per sample. These are named-path improvements, not claims that the entire extension became thousands of times faster.

| Operation | Before, ms | After, ms | Speedup |
|---|---:|---:|---:|
| Warm settings resolution | 0.003631 | 0.001100 | 3.3× |
| Task index, 50 long tasks | 0.084630 | 0.008546 | 9.9× |
| Cached large-goal prompt | 0.343115 | 0.008909 | 38.5× |
| Expanded dashboard, width 120 | 1.929192 | 0.327010 | 5.9× |
| Next history page, 100,000 events | 103.041000 | 0.003000 | 34347.0× |
| Activity tail, 100,000 events | 0.010352 | 0.000767 | 13.5× |
| Auditor preview, 1-million-character report | 0.950500 | 0.000316 | 3005.5× |
| Session health, 100,000 entries | 45.256416 | 41.486708 | 1.1× |

The history-page result measures a subsequent cached page. First retrieval still processes full history. Auditor preview scans the last five nonempty lines; the full final report is retained. Cache allowances bound retained content and oversized inputs use the uncached path. The stub-SDK measurements independently show the same targeted improvements.

The full 94-case benchmark matrix passes its gate. The stricter campaign gate also passes targeted CPU/real-SDK improvements, non-target timing allowances, filesystem counts and context size checks. Alternating baseline/candidate/candidate/baseline runtime trials give the following averages of per-run p50s:

| Runtime operation | Before, ms | After, ms | Maximum median filesystem ops |
|---|---:|---:|---:|
| cold_history.1000 | 2.618 | 2.691 | 26 → 25 |
| cold_history.10000 | 10.268 | 10.614 | 26 → 25 |
| cold_history.100000 | 86.743 | 80.233 | 26 → 25 |
| task_batch.10 | 1.265 | 1.067 | 20 → 19 |
| task_batch.100 | 2.724 | 2.063 | 22 → 21 |
| startup.50 | 2.372 | 1.360 | 22 → 21 |

Initial trials included slower diagnostic and cold-start cases. CPU profiling prompted a bounded goal-ID sanitation cache and replacement of the manual diagnostic line scanner with native splitting. Parsed session objects are still discarded immediately. Initial observations are retained in `initial-timings/` and `RUNTIME-BEFORE.json` / `RUNTIME-AFTER.json`; the final alternating trials are the acceptance evidence. Cold timings vary materially between runs. Retained-heap observations and V8 allocation samples are in the raw runtime files; they are not exact allocation totals.

## Context and behavior

Across 24 fixtures, extension-attributable content changes from **131,102 to 130,042 characters**. The two auditor fixtures each save 530 serialized characters (about 9% of their complete child request). Executor request sizes stay unchanged on the existing fixtures, preserving the first campaign's 38% active-workflow reduction. The post-compaction delta now additionally bounds oversized current-task content and directs lossless retrieval; a dedicated behavioral test covers that case.

Stable auditor criteria now precede changing payload data, with duplicate policy removed. Goals, contracts, task evidence and reports remain losslessly stored/retrievable. Ordered batches validate on a private clone, keep individual ledger events, and commit atomically. Revision conflicts, focus changes, external refresh, pause controls, independent auditing, Oracle behavior and recovery remain covered.

Derived checkpoint version 3 preserves Oracle results and rebuilds old/corrupt derived data from the unchanged ledger. Archive snapshot removal now matches the actual timestamped path. Public pool Maps, settings values/provenance and wrapped UI rows are independently owned.

## Live comparison within the original allowance

DeepSeek V4 Flash on authenticated `opencode-go`, high reasoning on both sides, isolated artifact fixtures. Each side first audits incorrect content, then corrected content. All four verdicts match the artifacts; the auditor leaves the artifacts unchanged.

| Case | Artifact/verdict grade | Calls | Input | Output | Cache read | Cost |
|---|---|---:|---:|---:|---:|---:|
| before / reject | pass | 3 | 1,920 | 478 | 3,712 | $0.000763864 |
| before / rework | pass | 3 | 1,870 | 536 | 3,712 | $0.000791144 |
| after / reject | pass | 3 | 1,826 | 483 | 3,456 | $0.000744692 |
| after / rework | pass | 2 | 1,921 | 377 | 1,664 | $0.000683088 |

New requests: **11**; input **7,537**, output **1,874**, cache-read **12,544**, cache-write **0** tokens; SDK-priced cost **US$0.002982788**. Cumulative original-campaign spend plus conservative charges for prior unobserved calls: **US$1.032791791 of US$5**; outstanding reservations: **US$0.00**. Every request reserves its maximum supported input/output cost before dispatch.

This is a small auditor smoke comparison, not statistical equivalence. The original campaign's task/Sisyphus live runs and incomplete request-capped outcomes remain documented there. No additional Oracle live coverage is claimed.

## Validation

**923/923 full-suite tests pass.** TypeScript and lint pass. Serial tests pass **888/888 on both SDK 0.84.1 and SDK 0.83.0**. The 24-fixture context gate passes, and six outgoing provider payloads match the harness on each SDK. Both fresh performance gates pass. `VALIDATION.json` records these checks and the production-source hash. Runtime/performance comparisons ran separately from tests.

## Reproduction

Preserve `5e16557` in an isolated checkout. Copy the current `experiments/bench/comprehensive.mjs`, `runtime-token.mjs` and `node-fs.mjs` measurement files into that checkout; use identical dependencies. Run `npm run bench -- before 2026-09-07-comprehensive-optimization` there, and the corresponding `after` command on the candidate. The campaign's full matrix artifacts are under `experiments/bench/`.

Run `comprehensive.mjs` with the benchmark adapter and `--expose-gc --experimental-strip-types`; repeat without the adapter and with `GOAL_BENCH_SDK=real` for real-SDK results. Run `bench:runtime-token` in alternating baseline/candidate/candidate/baseline order, using the four `RUNTIME-REPEAT-*.json` output names. `npm run bench:gate:comprehensive` checks this campaign's artifacts, and `npm run bench:gate -- 2026-09-07-comprehensive-optimization` checks the full matrix. Re-running a live comparison requires the latest cumulative results file so the original allowance cannot reset.
