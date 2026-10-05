# Runtime and token optimization results

Original baseline: `59826ec818aa8883329a74c62000d18aa1e1dbfe`. Both versions were measured with identical corrected harnesses in separate checkouts on Node v26.7.0. No model runs were used for runtime timing.

## Context

The 14 representative active-workflow fixtures decreased from **145,134 to 89,993 extension-attributable characters (38.0%)**. Individual reductions ranged from 27.9% to 47.3%. This includes injected state, active goal schemas, SDK-added guidance, extension tool/custom messages, and applicable child requests. All 24 fixtures are retained in the JSON evidence. Ordinary host schemas and conversation history are also captured; they are not attributed to the extension.

Character counts and `chars/4` estimates are not actual model token counts. Six real SDK outgoing payloads were intercepted before HTTP dispatch to cross-check executor systems/schemas/history and isolated auditor/Oracle systems/tools/requests. Both supported SDK lines passed these checks.

## Runtime

30 repeated timing samples after warm-up. Allocation sampling uses the V8 heap profiler separately, at 1 KB sampling intervals, including collected objects. Retained heap deltas after explicit GC are observations, not exact allocation totals. Cold-history fixtures explicitly delete the derived checkpoint and rebuild from the full ledger; startup fixtures exercise 1/10/50 saved goals. Task batches compare identical ordered focus updates through legacy single calls versus the new batch form.

| Operation | Before p50 ms | After p50 ms | Before p95 ms | After p95 ms | After FS operations |
|---|---:|---:|---:|---:|---:|
| cold_history.1000 | 1.636 | 2.105 | 3.421 | 3.013 | 26 |
| activity.1000 | 0.106 | 0.022 | 0.165 | 0.035 | 0 |
| dashboard.1000 | 0.114 | 0.024 | 0.164 | 0.037 | 0 |
| before_agent_start.1000 | 0.021 | 0.029 | 0.037 | 0.075 | 0 |
| append.1000 | 0.031 | 0.031 | 0.189 | 0.129 | 1 |
| cold_history.10000 | 6.292 | 7.583 | 12.705 | 14.617 | 26 |
| activity.10000 | 1.002 | 0.010 | 1.200 | 0.013 | 0 |
| dashboard.10000 | 0.984 | 0.011 | 1.287 | 0.014 | 0 |
| before_agent_start.10000 | 0.043 | 0.027 | 0.058 | 0.044 | 0 |
| append.10000 | 0.041 | 0.026 | 0.188 | 0.218 | 1 |
| cold_history.100000 | 68.135 | 64.959 | 79.712 | 82.118 | 26 |
| activity.100000 | 12.203 | 0.010 | 13.506 | 0.011 | 0 |
| dashboard.100000 | 12.785 | 0.011 | 14.192 | 0.013 | 0 |
| before_agent_start.100000 | 0.389 | 0.027 | 1.316 | 0.039 | 0 |
| append.100000 | 0.335 | 0.032 | 1.560 | 0.324 | 1 |
| task_batch.10 | 0.980 | 0.864 | 1.442 | 1.269 | 20 |
| task_batch.100 | 3.937 | 2.130 | 4.789 | 2.552 | 22 |
| startup.1 | 0.982 | 0.953 | 1.414 | 1.245 | 22 |
| startup.10 | 0.986 | 0.955 | 3.089 | 1.359 | 22 |
| startup.50 | 1.152 | 1.028 | 2.268 | 1.680 | 22 |

The long-history activity improvement exceeds the 2× acceptance target. At 100k events the measured p50 speedup is about 1200×; this is a fixture-specific result, not a general application speed claim. Warm reads still perform zero filesystem operations. The gate requires ≥25% active context reduction, ≥2× long-history activity speedup, and no material regression: p50 within 20% or 2 ms, p95 within 50% or 2 ms to account for short-path noise. The committed gate passes.

| 100k-event allocation sample | Before bytes/op | After bytes/op |
|---|---:|---:|
| activity.100000 | 21,170,861 | 28,069 |
| dashboard.100000 | 21,314,960 | 30,042 |
| append.100000 | 2,006,782 | 5,264 |

| Ledger events | Ledger bytes | Derived checkpoint bytes |
|---|---:|---:|
| 1,000 | 116,890 | 20,988 |
| 10,000 | 1,178,890 | 21,142 |
| 100,000 | 11,888,890 | 21,296 |

Activity/recent projections remain bounded per goal. Oracle state grows with distinct blocker fingerprints, and total checkpoint size also depends on goal count. Saved JSONL history remains lossless.

## Live comparisons

The requested model was **OpenCode Go / DeepSeek V4 Flash**, with **high** reasoning on both sides. The available authenticated OpenCode connection was `opencode-go`. Each case used an isolated temporary repository and exact artifact-content grading, with independent audits. The run ceiling was eight requests, including child audits. Oracle was disabled in these smoke fixtures; its metering/configuration is covered offline, and live Oracle behavior remains untested.

| DeepSeek case | Version | Artifacts | Audited completion | Initial rejection verified | Requests | Input | Output | Cache read | Cache write | SDK-priced USD |
|---|---|---|---|---|---:|---:|---:|---:|---:|---:|
| tasks | before | pass | incomplete | n/a | 8 | 7,076 | 1,831 | 22,272 | 0 | 0.002921 |
| tasks | after | pass | pass | n/a | 8 | 8,243 | 1,539 | 19,456 | 0 | 0.002965 |
| sisyphus-compaction | before | pass | incomplete | n/a | 8 | 10,253 | 1,234 | 28,416 | 0 | 0.003269 |
| sisyphus-compaction | after | pass | incomplete | n/a | 8 | 6,902 | 2,019 | 21,760 | 0 | 0.003003 |
| audit-rework | before | pass | incomplete | pass | 8 | 11,013 | 2,063 | 16,384 | 0 | 0.003899 |
| audit-rework | after | pass | incomplete | pass | 8 | 6,047 | 1,433 | 15,616 | 0 | 0.002385 |

All six DeepSeek runs produced the correct artifacts. Both audit/rework cases independently rejected the deliberately missing artifact. Only the optimized task run completed the audited lifecycle within the eight-request ceiling; the others did not reach approval/archive. Do not interpret this limited sample as statistical equivalence or a reliable live token/cost improvement.

Earlier GLM fallback runs, started before the model-selection reply, passed task and Sisyphus/compaction pairs. A stalled audit attempt was terminated and conservatively charged its maximum 12-request reservation ($0.924576). A second interrupted request retained $0.077048 when switching to the requested model. These amounts remain within the original combined allowance.

Across **94 requests with reported usage**, known usage was **151,493 input / 15,184 output / 179,136 cache-read / 0 cache-write tokens**, costing **$0.028185** using reported token usage and the SDK registry’s configured prices (not an invoice lookup). Interrupted-request usage is unavailable (at most 13 additional requests); **$1.001624** is conservatively reserved for it. **Total charged or reserved: $1.029809 of $5**, with no outstanding requests. Future evaluation must retain this spend; `--resume` preserves campaign accounting.

## Validation

- Full unit, integration, and e2e suite: 911 tests passed.
- TypeScript, ESLint, runner manifest/self-check, 24-fixture context gate: passed.
- New runtime/context acceptance gate: passed. Historical benchmark gates also passed against their existing committed artifacts; those campaigns were not rerun or overwritten.
- SDK 0.84.1: serial compatibility suite passed; final changed-runtime checks included in the full suite.
- SDK 0.83.0: TypeScript and serial suite passed after correcting an omitted copied shell-harness fixture; the final changed runtime tests were rerun. Six provider payload checks passed on both SDK lines.

## Reproduce

```sh
npm run test:all
npm run check
npm run lint
npm run context:gate
npm run context:provider-check
npm run bench:gate:runtime-token
# Emit a fresh candidate measurement to a separate file:
npm run bench:runtime-token -- /tmp/pi-goal-runtime.json
```

For the original comparison, archive the original commit into an isolated checkout, supply the same dependencies, and copy the current context/runtime measurement harnesses into it before measuring. Do not compare against the old incomplete context baseline. `CONTEXT_OUTPUT` selects an isolated context result path. Live evaluation is explicitly opt-in; the harness defaults to the selected DeepSeek model and reserves each request before dispatch.
