# Lane P (perf): end-to-end basket, Go port vs TS original

Tip measured: 8a4d39f5 (Go extension from `components/posthorse/extensions/posthorse`). Tooling and method: `port/perf/README.md`.
Data: `port/perf/data/both/` (authoritative: go, ts and pi interleaved sample by sample in one session), `data/go/` and
`data/ts-baseline/` (the same rows split by side; `ts-baseline` = TS under pig plus Pi 1.5.0 reference, this session, replacing
the earlier session), `data/go/benchmem.txt`. Each directory has `samples.jsonl` (every sample, raw), `meta*.json`, `summary.md`
(headline cells, ratchet verdict, spread), `summary-full.md` (every metric, every cell, with the Pi reference column).

## Session

Host xd6702 (Intel Xeon Platinum 8468, Linux 6.8.0-124), tern-ci:v7 container (Node v24.22.1, Go 1.27.3), pig 0.7.0+1.5.0 (PiG 979bf718f7),
Pi 1.5.0, six pinned CPUs (186-191), `nice -n 10`, fresh homes, warm and cold caches as in the README. Main run 2026-11-07
05:22-07:42 UTC, load average at start/end 3.5/4.8/7.6 and 2.7/3.5/4.4;
top-up of workloads c1, c100, c1000, d50, d200 (35 more samples each) 07:43-08:23 UTC. Samples per cell and side: 25 (a, b, e, f), 60 (c1, c100,
c1000, d50, d200). 2625 samples, 0 invalid, parity of the scripted workloads identical across sides (model requests, session records:
workload e leaves 200 requests, 3 compactions, 3 reminders on every side).

Extension process count: Go 1, TS under pig 1 (`node`), Pi 0 (in-process); workload f: 60 / 60 / 0 processes for 60 sessions.
Go binary: pig's `runner` 7,803,040 B (7,800,072 B after `strip -s`; it carries no symbols already), sha256 `8837336760ca4fc1...`.
For comparison the Node extension runtime process is the node binary plus the jiti-transformed TS (RSS 161 MiB idle).

## Ratchet verdict (Go <= TS, median, same pig host)

246 metric rows compared (every RATCHET metric and every tool-call latency, every cell): 238 pass, 8 FAIL; no p90-only miss.
Every memory row (RSS, PSS, Anonymous, peak RSS, tree RSS/PSS, heap used and in use, Go Sys vs Node RSS, per-session averages in f),
every startup, first-turn and per-tool-call latency of every cell passes with Go lower (memory 3x to 100x lower, startup 3.5x (cold) to 7x (warm) faster, tool calls 1.3x to 5x faster). The misses are all time/CPU in two cells (c1 and e):

| Cell | Metric | Go median | TS median | Go p90 | TS p90 |
|---|---|---:|---:|---:|---:|
| c1/warm | turn_ms | 825.8 | 682.0 | 861.1 | 825.2 |
| c1/warm | cpu_total_workload_ms | 990.0 | 850.0 | 1040.0 | 1060.0 |
| c1/warm | cpu_ext_workload_ms | 520.0 | 515.0 | 540.0 | 650.0 |
| e/warm | turn_ms | 26358.7 | 15996.7 | 26858.5 | 16444.8 |
| e/warm | cpu_total_ms_end | 31510 | 19490 | 32330 | 19910 |
| e/warm | cpu_total_workload_ms | 31430.0 | 18370.0 | 32250.0 | 18830.0 |
| e/warm | cpu_ext_workload_ms | 18130.0 | 9300.0 | 18630.0 | 9650.0 |
| e/warm | ext_cpu_ms_end | 18140 | 10230 | 18640 | 10550 |

### Causes of the misses (investigation; no production code changed)

CPU profiles: Go extension process via `PH_PERF_CPUPROF=1` (the mem reporter's pprof, never in a measured sample), pig host via
`PH_PERF_HOSTPROF=1` (`PIG_PROFILE=cpu`), one e run per side and merged c1 runs.

**e (200 requests, 3 rollovers): Go 26.4 s vs TS 16.0 s wall, ext CPU 18.1 vs 9.3 s, host CPU 13.3 vs 9.1 s.**
* Extension process (16.6 s sampled): only about 10% is posthorse code (1.69 s: `registerReminderFilter` -> `countRetainedMessages` ->
  `BuildSessionProjection` host round trip 1.05 s, notes 0.2 s). About 85% is Go SDK protocol work on the two full-transcript events per
  turn (`context` and `context_with_system`, each up to ~300 KB as the session grows, so quadratic over the run): `sdk/json.checkValid`
  (a full validation pass) 24%, decode into `map[string]any` (`decodeState`, second `skip` pass) ~26%, response encode 8%, GC 14%. The Node
  extension receives the same bytes through V8 `JSON.parse`.
* Host (12.8 s sampled): `UIBridge.handleSessionRead` 2.42 s (19% of host CPU, Go-only: it serves the Go extension's `BuildSessionProjection`:
  `ExtensionSessionRead` 1.67 s + marshal 0.75 s) and `appendCompact` 3.1 s vs 1.8 s on the Node path.
* Posthorse part: `countRetainedMessages` (reminders.go:458) reads the whole projection on every `context` event once a rollover has
  happened (first message is the retained-rollover compaction summary), O(session) per turn. Fix proposal (posthorse): cache the retained
  count / retained projection span keyed by the compaction entry id and leaf (it only changes when the boundary or the leaf changes) and
  fetch the projection only on a miss; this removes the host `handleSessionRead` cost and the ext-side decode (about 3.5 s of the ~13 s excess).
* SDK part (not posthorse): decode the event frame once (`UnmarshalFrame` already validates; `json.Unmarshal` validates again), decode
  `messages` lazily or with a reusable buffer; both are PiG SDK changes (an owner decision per brief section 7). Without them the Go
  extension's per-event cost on large transcripts stays above Node's `JSON.parse`.

**c1 (31 requests over 1 note): turn_ms 826 vs 682 (+21%), total workload CPU 990 vs 850; ext CPU equal (520 vs 515, within one 10 ms tick).**
Per-tool-call latency is lower in Go (4.5-5.2 ms vs 6.4-8.1 ms) and so is the first tool call (18 vs 32 ms); the wall difference
(about 4.6 ms per turn) and the 140 ms of extra host CPU are outside the tool call: the blocking per-turn hooks (`context`,
`context_with_system`, `turn_end`) cost the host more with a Go extension (host `extensionContextHook` 1.36 s vs 0.98 s per 8 runs in the
merged profile; ext profile: 45% `handleArmedRequest`, of which `json.Unmarshal` of the event payload is the largest part, posthorse code
7%). The extension writes fewer frames than the Node one (2,090 vs 3,450 write syscalls per run, 1.13 vs 1.05 MB), so it is not round-trip
count; allocations are 835,605 mallocs and 57 MiB per run (about 27,000 mallocs and 1.85 MiB per turn, the decoded events).
Same SDK decode cost as e at small scale. Warning: c100 only passes by a hair (turn_ms 1080 vs 1108, cpu_total_workload 1320 vs 1355, spread 3% and 10%; at the first 25 samples it was a median miss, 1037 vs 955), so it is a near tie, and the larger the transcript per turn the more the Go decode cost weighs (c1000 turn passes because tool time dominates). [INFERENCE] for the per-hook split: only the aggregate profile was taken.

## Go vs TS tables (median / p90, n per side in each heading; memory in MiB, times in ms)

Go `Sys` is compared with Node RSS ("sys_asis_end"); heap used / in use are after a forced collection (`*_gc_end`);
`total_alloc_workload`/`mallocs_workload` are Go-only (V8 has no allocation counter; `num_gc_workload` of the TS side is V8 GC events).
`spread` = (p90-p50)/p50. `cpu_*` have 10 ms resolution. Pi 1.5.0 reference columns: `data/both/summary.md`.

#### a / cold (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 634.7 / 681.2 | 2193.8 / 2272.0 | 0.29 | 7% | 4% |
| cpu_total_ms_end | 930 / 1020 | 3530 / 3670 | 0.26 | 10% | 4% |
| cpu_total_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | - | - | - |
| ext_cpu_ms_end | 10 / 10 | 2950 / 3080 | 0.00 | 0% | 4% |
| ext_rss_end | 9.20 MiB / 9.46 MiB | 311.45 MiB / 313.56 MiB | 0.03 | 3% | 1% |
| ext_pss_end | 9.20 MiB / 9.46 MiB | 307.33 MiB / 309.50 MiB | 0.03 | 3% | 1% |
| ext_anon_end | 2.39 MiB / 2.55 MiB | 255.66 MiB / 257.89 MiB | 0.01 | 7% | 1% |
| ext_peak_rss_end | 9.20 MiB / 9.46 MiB | 311.45 MiB / 313.56 MiB | 0.03 | 3% | 1% |
| tree_rss_end | 79.00 MiB / 85.05 MiB | 467.43 MiB / 471.72 MiB | 0.17 | 8% | 1% |
| tree_pss_end | 77.54 MiB / 83.62 MiB | 461.96 MiB / 466.17 MiB | 0.17 | 8% | 1% |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB | 47.18 MiB / 47.18 MiB | 0.01 | 0% | 0% |
| heap_inuse_gc_end | 1.55 MiB / 1.60 MiB | 181.25 MiB / 181.50 MiB | 0.01 | 4% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 13.41 MiB / 13.41 MiB | 302.14 MiB / 304.50 MiB | 0.04 | 0% | 1% |
| total_alloc_workload (Go; TS: GC events only) | 0.04 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 188 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 2 | 1 | | | |

#### a / warm (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 113.0 / 119.8 | 768.1 / 788.7 | 0.15 | 6% | 3% |
| cpu_total_ms_end | 80 / 90 | 1040 / 1070 | 0.08 | 12% | 3% |
| cpu_total_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | - | - | - |
| ext_cpu_ms_end | 10 / 10 | 870 / 900 | 0.01 | 0% | 3% |
| ext_rss_end | 9.21 MiB / 9.58 MiB | 160.74 MiB / 162.50 MiB | 0.06 | 4% | 1% |
| ext_pss_end | 9.21 MiB / 9.58 MiB | 156.74 MiB / 158.50 MiB | 0.06 | 4% | 1% |
| ext_anon_end | 2.40 MiB / 2.61 MiB | 105.25 MiB / 106.96 MiB | 0.02 | 9% | 2% |
| ext_peak_rss_end | 9.21 MiB / 9.58 MiB | 160.74 MiB / 162.50 MiB | 0.06 | 4% | 1% |
| tree_rss_end | 69.78 MiB / 70.37 MiB | 245.24 MiB / 246.99 MiB | 0.28 | 1% | 1% |
| tree_pss_end | 68.27 MiB / 68.84 MiB | 239.74 MiB / 241.51 MiB | 0.28 | 1% | 1% |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB | 34.94 MiB / 34.94 MiB | 0.01 | 0% | 0% |
| heap_inuse_gc_end | 1.52 MiB / 1.61 MiB | 69.84 MiB / 101.59 MiB | 0.02 | 6% | 45% |
| sys_asis_end (Go Sys vs Node RSS) | 13.41 MiB / 13.66 MiB | 150.00 MiB / 155.94 MiB | 0.09 | 2% | 4% |
| total_alloc_workload (Go; TS: GC events only) | 0.04 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 188 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 2 | 1 | | | |

#### b / cold (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 626.9 / 730.5 | 2176.7 / 2267.8 | 0.29 | 17% | 4% |
| first_turn_ms | 34.8 / 44.8 | 53.8 / 60.1 | 0.65 | 29% | 12% |
| to_first_tool_ms | 17.6 / 21.7 | 28.1 / 34.1 | 0.63 | 23% | 21% |
| turn_ms | 34.8 / 44.8 | 53.8 / 60.1 | 0.65 | 29% | 12% |
| cpu_total_ms_end | 950 / 1100 | 3540 / 3740 | 0.27 | 16% | 6% |
| cpu_total_workload_ms | 40.0 / 50.0 | 80.0 / 90.0 | 0.50 | 25% | 12% |
| ext_cpu_ms_end | 20 / 30 | 2950 / 3160 | 0.01 | 50% | 7% |
| ext_rss_end | 11.02 MiB / 12.11 MiB | 317.11 MiB / 320.46 MiB | 0.03 | 10% | 1% |
| ext_pss_end | 11.02 MiB / 12.10 MiB | 313.13 MiB / 316.39 MiB | 0.04 | 10% | 1% |
| ext_anon_end | 4.09 MiB / 5.07 MiB | 256.87 MiB / 260.14 MiB | 0.02 | 24% | 1% |
| ext_peak_rss_end | 11.02 MiB / 12.11 MiB | 317.11 MiB / 320.46 MiB | 0.03 | 10% | 1% |
| tree_rss_end | 84.84 MiB / 90.24 MiB | 480.90 MiB / 485.38 MiB | 0.18 | 6% | 1% |
| tree_pss_end | 83.33 MiB / 88.71 MiB | 475.41 MiB / 479.85 MiB | 0.18 | 6% | 1% |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB | 47.42 MiB / 47.43 MiB | 0.01 | 0% | 0% |
| heap_inuse_gc_end | 1.63 MiB / 1.71 MiB | 181.25 MiB / 181.75 MiB | 0.01 | 5% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 13.41 MiB / 13.66 MiB | 306.83 MiB / 309.00 MiB | 0.04 | 2% | 1% |
| tool_ms:get_context_remaining | 2.7 / 5.5 | 9.0 / 10.9 | 0.30 | 105% | 21% |
| total_alloc_workload (Go; TS: GC events only) | 1.31 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 14324 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 2 | 1 | | | |

#### b / warm (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 114.6 / 127.2 | 812.5 / 878.0 | 0.14 | 11% | 8% |
| first_turn_ms | 41.0 / 47.3 | 62.3 / 75.1 | 0.66 | 15% | 21% |
| to_first_tool_ms | 19.3 / 21.8 | 33.1 / 38.0 | 0.58 | 12% | 15% |
| turn_ms | 41.0 / 47.3 | 62.3 / 75.1 | 0.66 | 15% | 21% |
| cpu_total_ms_end | 130 / 150 | 1180 / 1300 | 0.11 | 15% | 10% |
| cpu_total_workload_ms | 50.0 / 70.0 | 80.0 / 120.0 | 0.62 | 40% | 50% |
| ext_cpu_ms_end | 30 / 30 | 980 / 1070 | 0.03 | 0% | 9% |
| ext_rss_end | 10.91 MiB / 12.12 MiB | 167.31 MiB / 169.08 MiB | 0.07 | 11% | 1% |
| ext_pss_end | 10.91 MiB / 12.11 MiB | 163.33 MiB / 165.14 MiB | 0.07 | 11% | 1% |
| ext_anon_end | 4.06 MiB / 5.08 MiB | 107.14 MiB / 108.89 MiB | 0.04 | 25% | 2% |
| ext_peak_rss_end | 10.91 MiB / 12.12 MiB | 167.31 MiB / 169.08 MiB | 0.07 | 11% | 1% |
| tree_rss_end | 73.92 MiB / 75.27 MiB | 257.10 MiB / 258.81 MiB | 0.29 | 2% | 1% |
| tree_pss_end | 72.40 MiB / 73.75 MiB | 251.59 MiB / 253.28 MiB | 0.29 | 2% | 1% |
| heap_used_gc_end | 0.54 MiB / 0.55 MiB | 35.17 MiB / 35.17 MiB | 0.02 | 1% | 0% |
| heap_inuse_gc_end | 1.64 MiB / 1.71 MiB | 70.09 MiB / 101.84 MiB | 0.02 | 4% | 45% |
| sys_asis_end (Go Sys vs Node RSS) | 13.66 MiB / 17.36 MiB | 156.00 MiB / 161.62 MiB | 0.09 | 27% | 4% |
| tool_ms:get_context_remaining | 4.3 / 5.7 | 10.4 / 13.8 | 0.42 | 33% | 34% |
| total_alloc_workload (Go; TS: GC events only) | 1.32 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 14339 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 2 | 1 | | | |

#### c1 / warm (n=60 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 116.3 / 132.3 | 845.4 / 880.3 | 0.14 | 14% | 4% |
| to_first_tool_ms | 19.8 / 22.6 | 33.8 / 37.3 | 0.59 | 14% | 11% |
| turn_ms | 825.8 / 861.1 | 682.0 / 825.2 | 1.21 | 4% | 21% |
| cpu_total_ms_end | 1080 / 1130 | 2015 / 2250 | 0.54 | 5% | 12% |
| cpu_total_workload_ms | 990.0 / 1040.0 | 850.0 / 1060.0 | 1.16 | 5% | 25% |
| ext_cpu_ms_end | 525 / 550 | 1490 / 1650 | 0.35 | 5% | 11% |
| ext_rss_end | 14.51 MiB / 14.73 MiB | 208.80 MiB / 209.95 MiB | 0.07 | 2% | 1% |
| ext_pss_end | 14.51 MiB / 14.73 MiB | 204.87 MiB / 206.02 MiB | 0.07 | 2% | 1% |
| ext_anon_end | 7.39 MiB / 7.59 MiB | 148.63 MiB / 149.70 MiB | 0.05 | 3% | 1% |
| ext_peak_rss_end | 14.51 MiB / 14.73 MiB | 208.80 MiB / 209.95 MiB | 0.07 | 2% | 1% |
| tree_rss_end | 85.43 MiB / 86.29 MiB | 318.20 MiB / 323.78 MiB | 0.27 | 1% | 2% |
| tree_pss_end | 84.02 MiB / 84.92 MiB | 312.90 MiB / 318.61 MiB | 0.27 | 1% | 2% |
| heap_used_gc_end | 0.57 MiB / 0.57 MiB | 35.89 MiB / 35.89 MiB | 0.02 | 2% | 0% |
| heap_inuse_gc_end | 1.71 MiB / 1.80 MiB | 102.59 MiB / 102.84 MiB | 0.02 | 5% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 188.18 MiB / 199.11 MiB | 0.09 | 1% | 6% |
| tool_ms:notes.append | 5.2 / 7.2 | 7.1 / 10.7 | 0.73 | 38% | 50% |
| tool_ms:notes.list | 5.5 / 7.5 | 8.0 / 14.1 | 0.68 | 38% | 75% |
| tool_ms:notes.read | 4.9 / 7.3 | 7.2 / 10.4 | 0.68 | 48% | 44% |
| tool_ms:notes.search | 5.1 / 7.2 | 7.7 / 11.4 | 0.67 | 41% | 49% |
| tool_ms:notes.write | 5.9 / 7.4 | 9.1 / 13.0 | 0.65 | 25% | 42% |
| total_alloc_workload (Go; TS: GC events only) | 57.41 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 835399 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 32 | 4 | | | |

#### c100 / warm (n=60 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 115.6 / 131.6 | 858.5 / 887.4 | 0.13 | 14% | 3% |
| to_first_tool_ms | 20.0 / 22.5 | 34.4 / 38.0 | 0.58 | 12% | 10% |
| turn_ms | 1080.3 / 1114.9 | 1108.2 / 1215.3 | 0.97 | 3% | 10% |
| cpu_total_ms_end | 1415 / 1470 | 2505 / 2710 | 0.56 | 4% | 8% |
| cpu_total_workload_ms | 1320.0 / 1370.0 | 1355.0 / 1500.0 | 0.97 | 4% | 11% |
| ext_cpu_ms_end | 760 / 800 | 1845 / 2030 | 0.41 | 5% | 10% |
| ext_rss_end | 14.81 MiB / 15.11 MiB | 224.54 MiB / 226.93 MiB | 0.07 | 2% | 1% |
| ext_pss_end | 14.81 MiB / 15.10 MiB | 220.61 MiB / 222.96 MiB | 0.07 | 2% | 1% |
| ext_anon_end | 7.65 MiB / 7.95 MiB | 164.36 MiB / 166.60 MiB | 0.05 | 4% | 1% |
| ext_peak_rss_end | 14.84 MiB / 15.11 MiB | 224.54 MiB / 226.93 MiB | 0.07 | 2% | 1% |
| tree_rss_end | 87.72 MiB / 88.68 MiB | 334.80 MiB / 340.58 MiB | 0.26 | 1% | 2% |
| tree_pss_end | 86.30 MiB / 87.25 MiB | 329.27 MiB / 335.28 MiB | 0.26 | 1% | 2% |
| heap_used_gc_end | 0.62 MiB / 0.63 MiB | 36.07 MiB / 36.09 MiB | 0.02 | 2% | 0% |
| heap_inuse_gc_end | 1.80 MiB / 1.88 MiB | 103.34 MiB / 104.84 MiB | 0.02 | 4% | 1% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 205.40 MiB / 217.65 MiB | 0.09 | 1% | 6% |
| tool_ms:notes.append | 6.2 / 7.6 | 8.0 / 11.3 | 0.77 | 23% | 41% |
| tool_ms:notes.list | 10.5 / 13.0 | 21.4 / 28.8 | 0.49 | 24% | 35% |
| tool_ms:notes.read | 5.7 / 7.6 | 8.2 / 11.3 | 0.69 | 33% | 38% |
| tool_ms:notes.search | 11.0 / 13.5 | 31.2 / 41.8 | 0.35 | 23% | 34% |
| tool_ms:notes.write | 6.3 / 8.0 | 10.0 / 13.9 | 0.64 | 26% | 40% |
| total_alloc_workload (Go; TS: GC events only) | 84.33 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 915169 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 48 | 6 | | | |

#### c1000 / warm (n=60 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 115.4 / 131.6 | 824.3 / 881.1 | 0.14 | 14% | 7% |
| to_first_tool_ms | 18.8 / 22.0 | 33.0 / 38.3 | 0.57 | 17% | 16% |
| turn_ms | 1242.9 / 1335.4 | 1645.1 / 2083.1 | 0.76 | 7% | 27% |
| cpu_total_ms_end | 1705 / 1890 | 3205 / 3920 | 0.53 | 11% | 22% |
| cpu_total_workload_ms | 1615.0 / 1800.0 | 2080.0 / 2710.0 | 0.78 | 11% | 30% |
| ext_cpu_ms_end | 1100 / 1210 | 2610 / 3210 | 0.42 | 10% | 23% |
| ext_rss_end | 15.22 MiB / 15.41 MiB | 231.21 MiB / 233.26 MiB | 0.07 | 1% | 1% |
| ext_pss_end | 15.21 MiB / 15.41 MiB | 227.34 MiB / 229.33 MiB | 0.07 | 1% | 1% |
| ext_anon_end | 8.06 MiB / 8.25 MiB | 170.95 MiB / 173.08 MiB | 0.05 | 2% | 1% |
| ext_peak_rss_end | 15.30 MiB / 16.50 MiB | 231.21 MiB / 233.26 MiB | 0.07 | 8% | 1% |
| tree_rss_end | 88.06 MiB / 89.16 MiB | 338.65 MiB / 346.00 MiB | 0.26 | 1% | 2% |
| tree_pss_end | 86.57 MiB / 87.77 MiB | 333.39 MiB / 340.69 MiB | 0.26 | 1% | 2% |
| heap_used_gc_end | 0.71 MiB / 0.71 MiB | 36.16 MiB / 36.17 MiB | 0.02 | 1% | 0% |
| heap_inuse_gc_end | 1.88 MiB / 1.96 MiB | 104.09 MiB / 109.09 MiB | 0.02 | 5% | 5% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 219.95 MiB / 223.60 MiB | 0.08 | 1% | 2% |
| tool_ms:notes.append | 5.9 / 7.7 | 6.8 / 10.6 | 0.86 | 31% | 55% |
| tool_ms:notes.list | 20.8 / 24.0 | 42.9 / 61.9 | 0.49 | 15% | 44% |
| tool_ms:notes.read | 5.6 / 7.6 | 7.3 / 11.4 | 0.77 | 35% | 55% |
| tool_ms:notes.search | 32.0 / 36.5 | 114.1 / 176.4 | 0.28 | 14% | 55% |
| tool_ms:notes.write | 6.0 / 7.9 | 9.0 / 13.2 | 0.67 | 30% | 46% |
| total_alloc_workload (Go; TS: GC events only) | 140.61 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 1346482 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 79 | 13 | | | |

#### d50 / warm (n=60 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 112.5 / 129.1 | 804.9 / 881.8 | 0.14 | 15% | 10% |
| to_first_tool_ms | 19.3 / 22.8 | 35.7 / 39.5 | 0.54 | 18% | 11% |
| turn_ms | 151.2 / 171.3 | 367.8 / 402.9 | 0.41 | 13% | 10% |
| cpu_total_ms_end | 335 / 380 | 1715 / 1870 | 0.20 | 13% | 9% |
| cpu_total_workload_ms | 250.0 / 290.0 | 610.0 / 670.0 | 0.41 | 16% | 10% |
| ext_cpu_ms_end | 90 / 110 | 1330 / 1470 | 0.07 | 22% | 11% |
| ext_rss_end | 14.45 MiB / 14.70 MiB | 208.75 MiB / 210.27 MiB | 0.07 | 2% | 1% |
| ext_pss_end | 14.45 MiB / 14.69 MiB | 204.81 MiB / 206.42 MiB | 0.07 | 2% | 1% |
| ext_anon_end | 7.23 MiB / 7.47 MiB | 148.40 MiB / 150.04 MiB | 0.05 | 3% | 1% |
| ext_peak_rss_end | 14.45 MiB / 14.71 MiB | 208.75 MiB / 210.27 MiB | 0.07 | 2% | 1% |
| tree_rss_end | 77.61 MiB / 78.06 MiB | 301.36 MiB / 303.11 MiB | 0.26 | 1% | 1% |
| tree_pss_end | 76.16 MiB / 76.71 MiB | 296.03 MiB / 297.63 MiB | 0.26 | 1% | 1% |
| heap_used_gc_end | 0.55 MiB / 0.56 MiB | 36.26 MiB / 36.27 MiB | 0.02 | 2% | 0% |
| heap_inuse_gc_end | 1.65 MiB / 1.73 MiB | 104.09 MiB / 104.59 MiB | 0.02 | 5% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 208.38 MiB / 210.00 MiB | 0.08 | 1% | 1% |
| tool_ms:history.read | 20.0 / 23.5 | 34.1 / 39.0 | 0.59 | 17% | 14% |
| tool_ms:history.search:common | 19.3 / 23.3 | 30.5 / 36.7 | 0.63 | 20% | 20% |
| tool_ms:history.search:rare | 49.7 / 57.2 | 231.9 / 245.0 | 0.21 | 15% | 6% |
| total_alloc_workload (Go; TS: GC events only) | 13.00 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 46455.5 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 8 | 5 | | | |

#### d200 / warm (n=60 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 114.6 / 126.1 | 785.5 / 879.4 | 0.15 | 10% | 12% |
| to_first_tool_ms | 19.4 / 22.0 | 32.5 / 38.7 | 0.60 | 14% | 19% |
| turn_ms | 221.2 / 246.6 | 465.5 / 529.7 | 0.48 | 11% | 14% |
| cpu_total_ms_end | 655 / 740 | 2000 / 2310 | 0.33 | 13% | 16% |
| cpu_total_workload_ms | 570.0 / 650.0 | 940.0 / 1100.0 | 0.61 | 14% | 17% |
| ext_cpu_ms_end | 100 / 120 | 1355 / 1560 | 0.07 | 20% | 15% |
| ext_rss_end | 14.51 MiB / 14.87 MiB | 212.39 MiB / 214.01 MiB | 0.07 | 2% | 1% |
| ext_pss_end | 14.51 MiB / 14.86 MiB | 208.41 MiB / 210.02 MiB | 0.07 | 2% | 1% |
| ext_anon_end | 7.29 MiB / 7.64 MiB | 152.07 MiB / 153.75 MiB | 0.05 | 5% | 1% |
| ext_peak_rss_end | 14.52 MiB / 14.96 MiB | 212.39 MiB / 214.01 MiB | 0.07 | 3% | 1% |
| tree_rss_end | 77.59 MiB / 78.48 MiB | 305.02 MiB / 306.48 MiB | 0.25 | 1% | 0% |
| tree_pss_end | 76.18 MiB / 77.03 MiB | 299.67 MiB / 301.15 MiB | 0.25 | 1% | 0% |
| heap_used_gc_end | 0.56 MiB / 0.57 MiB | 36.31 MiB / 36.32 MiB | 0.02 | 2% | 0% |
| heap_inuse_gc_end | 1.66 MiB / 1.73 MiB | 104.34 MiB / 104.84 MiB | 0.02 | 4% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 207.85 MiB / 210.00 MiB | 0.09 | 1% | 1% |
| tool_ms:history.read | 42.7 / 47.9 | 54.5 / 66.0 | 0.78 | 12% | 21% |
| tool_ms:history.search:common | 31.4 / 36.9 | 41.3 / 49.9 | 0.76 | 17% | 21% |
| tool_ms:history.search:rare | 83.6 / 93.1 | 293.5 / 329.0 | 0.28 | 11% | 12% |
| total_alloc_workload (Go; TS: GC events only) | 15.42 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 48217.5 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 9 | 7 | | | |

#### e / warm (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| startup_ms | 115.1 / 128.4 | 796.4 / 882.3 | 0.14 | 11% | 11% |
| to_first_tool_ms | 19.8 / 23.0 | 33.9 / 41.3 | 0.58 | 16% | 22% |
| turn_ms | 26358.7 / 26858.5 | 15996.7 / 16444.8 | 1.65 | 2% | 3% |
| cpu_total_ms_end | 31510 / 32330 | 19490 / 19910 | 1.62 | 3% | 2% |
| cpu_total_workload_ms | 31430.0 / 32250.0 | 18370.0 / 18830.0 | 1.71 | 3% | 3% |
| ext_cpu_ms_end | 18140 / 18640 | 10230 / 10550 | 1.77 | 3% | 3% |
| ext_rss_end | 15.40 MiB / 22.08 MiB | 459.14 MiB / 487.18 MiB | 0.03 | 43% | 6% |
| ext_pss_end | 15.39 MiB / 22.07 MiB | 455.12 MiB / 483.25 MiB | 0.03 | 43% | 6% |
| ext_anon_end | 8.24 MiB / 14.86 MiB | 398.77 MiB / 426.95 MiB | 0.02 | 80% | 7% |
| ext_peak_rss_end | 21.66 MiB / 22.70 MiB | 516.88 MiB / 540.95 MiB | 0.04 | 5% | 5% |
| tree_rss_end | 128.54 MiB / 141.33 MiB | 591.01 MiB / 619.77 MiB | 0.22 | 10% | 5% |
| tree_pss_end | 127.08 MiB / 139.91 MiB | 585.53 MiB / 614.21 MiB | 0.22 | 10% | 5% |
| heap_used_gc_end | 0.66 MiB / 0.67 MiB | 35.93 MiB / 36.81 MiB | 0.02 | 2% | 2% |
| heap_inuse_gc_end | 2.12 MiB / 2.22 MiB | 168.84 MiB / 169.34 MiB | 0.01 | 5% | 0% |
| sys_asis_end (Go Sys vs Node RSS) | 25.94 MiB / 26.01 MiB | 446.05 MiB / 487.97 MiB | 0.06 | 0% | 9% |
| tool_ms:get_context_remaining | 5.2 / 6.8 | 6.7 / 9.1 | 0.77 | 30% | 35% |
| tool_ms:notes.append | 6.7 / 8.3 | 9.1 / 12.1 | 0.73 | 24% | 32% |
| tool_ms:notes.write | 7.1 / 8.8 | 11.3 / 14.7 | 0.63 | 23% | 30% |
| tool_ms:read | 10.9 / 12.8 | 11.8 / 14.0 | 0.92 | 18% | 19% |
| total_alloc_workload (Go; TS: GC events only) | 2417.73 MiB | - | | | |
| mallocs_workload (Go; TS: GC events only) | 15140566 | - | | | |
| num_gc_workload (Go; TS: GC events only) | 1154 | 41 | | | |

#### f / warm (n=25 per side)

| Metric | Go median / p90 | TS median / p90 | Go/TS | spread Go | spread TS |
|---|---:|---:|---:|---:|---:|
| cpu_total_ms_end | 4730 / 4930 | 70830 / 72760 | 0.07 | 4% | 3% |
| ext_cpu_ms_end | 290 / 400 | 60270 / 62060 | 0.00 | 38% | 3% |
| ext_rss_end | 558.06 MiB / 559.03 MiB | 8093.95 MiB / 8132.78 MiB | 0.07 | 0% | 0% |
| ext_pss_end | 153.04 MiB / 153.98 MiB | 4814.08 MiB / 4852.40 MiB | 0.03 | 1% | 1% |
| ext_anon_end | 146.11 MiB / 147.05 MiB | 4761.07 MiB / 4799.39 MiB | 0.03 | 1% | 1% |
| ext_peak_rss_end | 558.06 MiB / 559.03 MiB | 9332.15 MiB / 9349.92 MiB | 0.06 | 0% | 0% |
| tree_rss_end | 4191.91 MiB / 4196.93 MiB | 13149.36 MiB / 13182.81 MiB | 0.32 | 0% | 0% |
| tree_pss_end | 1386.53 MiB / 1388.46 MiB | 7461.77 MiB / 7495.47 MiB | 0.19 | 0% | 0% |
| ext_avg_rss_end | 9.30 MiB / 9.32 MiB | 134.90 MiB / 135.55 MiB | 0.07 | 0% | 0% |
| ext_avg_pss_end | 2.55 MiB / 2.57 MiB | 80.23 MiB / 80.87 MiB | 0.03 | 1% | 1% |
| all_ready_ms | 6695.5 / 6868.4 | 47176.5 / 48278.1 | 0.14 | 3% | 2% |


## Allocations (`go test -bench . -benchmem`, the lanes' benchmarks, 200 ms each, xd6702, Go 1.27.3; raw: `data/go/benchmem.txt`)

```
BenchmarkBudgetFor-8                               	 2737945	        87.77 ns/op	     112 B/op	       2 allocs/op
BenchmarkBuildGuidance-8                           	  106502	      2314 ns/op	    4488 B/op	      10 allocs/op
BenchmarkGuidanceTokens-8                          	   47179	      4711 ns/op	    7192 B/op	      12 allocs/op
BenchmarkFreshPayloadChars-8                       	   44222	      5150 ns/op	    7240 B/op	      13 allocs/op
BenchmarkPageCapacity-8                            	   46975	      4487 ns/op	    7240 B/op	      13 allocs/op
BenchmarkContextReport-8                           	  244675	       970.4 ns/op	     704 B/op	      17 allocs/op
BenchmarkLfLines-8                                 	      39	   5627342 ns/op	1785.92 MB/s	10705877 B/op	     319 allocs/op
BenchmarkIndexLF-8                                 	    9934	     28002 ns/op	37446.43 MB/s	     105 B/op	       0 allocs/op
BenchmarkSearchArchive/rg/rare-needle-marker-8     	       3	  88290251 ns/op	 701.02 MB/s	        34.29 peakRSS-MiB	 1405056 B/op	    2941 allocs/op
BenchmarkSearchArchive/rg/entry_1999-8             	       3	  72078706 ns/op	 858.68 MB/s	        34.29 peakRSS-MiB	 1307896 B/op	    1763 allocs/op
BenchmarkSearchArchive/noRg/rare-needle-marker-8   	       3	  74742208 ns/op	 828.08 MB/s	        34.29 peakRSS-MiB	 1339045 B/op	    2808 allocs/op
BenchmarkSearchArchive/noRg/entry_1999-8           	       4	  66471453 ns/op	 931.12 MB/s	        34.29 peakRSS-MiB	 1251588 B/op	    1644 allocs/op
BenchmarkSearchArchiveEveryLine-8                  	       1	 666288323 ns/op	  92.89 MB/s	        34.29 peakRSS-MiB	199589136 B/op	 2600324 allocs/op
BenchmarkFindEntry-8                               	       5	  44845500 ns/op	1380.13 MB/s	        34.29 peakRSS-MiB	 1125112 B/op	     123 allocs/op
BenchmarkSearchBranch-8                            	     196	   1163098 ns/op	  339307 B/op	     149 allocs/op
BenchmarkReadPage-8                                	      80	   2845895 ns/op	 140.55 MB/s	  414273 B/op	     156 allocs/op
BenchmarkSearchPage-8                              	    1632	    146579 ns/op	   66828 B/op	    1059 allocs/op
BenchmarkLineMatcher/rare-needle-marker-8          	    1296	    175023 ns/op	5913.51 MB/s	       0 B/op	       0 allocs/op
BenchmarkLineMatcher/dolor_sit-8                   	    1136	    210007 ns/op	4928.41 MB/s	       0 B/op	       0 allocs/op
BenchmarkLineMatcher/sk-8                          	    1039	    211029 ns/op	4904.53 MB/s	       0 B/op	       0 allocs/op
BenchmarkLineMatcher/unicode-8                     	    3404	     70359 ns/op	14710.34 MB/s	       0 B/op	       0 allocs/op
BenchmarkU16LenASCII-8                             	   10000	     25244 ns/op	2596.14 MB/s	       6 B/op	       0 allocs/op
BenchmarkU16LenMixed-8                             	     798	    253121 ns/op	 388.37 MB/s	     123 B/op	       0 allocs/op
BenchmarkU16SliceASCII-8                           	   10174	     24173 ns/op	       6 B/op	       0 allocs/op
BenchmarkU16SliceMixed-8                           	     507	    442712 ns/op	     193 B/op	       0 allocs/op
BenchmarkU16IndexASCII-8                           	   14996	     27175 ns/op	       9 B/op	       0 allocs/op
BenchmarkU16IndexMixed-8                           	    1119	    192474 ns/op	     183 B/op	       0 allocs/op
BenchmarkJSFromBytesValid-8                        	    1099	    210542 ns/op	 466.91 MB/s	   98393 B/op	       1 allocs/op
BenchmarkJSToLocaleInt-8                           	 3734964	        68.88 ns/op	      24 B/op	       2 allocs/op
BenchmarkTextOf-8                                  	  103930	      2097 ns/op	    1680 B/op	      14 allocs/op
BenchmarkExcerpt-8                                 	     307	    723439 ns/op	    2848 B/op	       1 allocs/op
BenchmarkShortKey-8                                	  530751	       424.5 ns/op	      96 B/op	       2 allocs/op
BenchmarkNoteParityParseLeakedForkOfFork-8         	    1693	    118389 ns/op	   34088 B/op	     712 allocs/op
BenchmarkNoteSnapshot300-8                         	       8	  26262444 ns/op	10009227 B/op	  105944 allocs/op
BenchmarkNotesUnder500-8                           	      39	   5381204 ns/op	  745727 B/op	    7028 allocs/op
BenchmarkNoteCollapse600Cold-8                     	       8	  34255803 ns/op	 4905947 B/op	   37266 allocs/op
BenchmarkNoteCollapse600Warm-8                     	       7	  30084543 ns/op	 4076560 B/op	   33634 allocs/op
BenchmarkNoteBindingsOf3000-8                      	      62	   3691975 ns/op	 1097471 B/op	   23008 allocs/op
BenchmarkNoteTouchRecords3000-8                    	     184	   1272983 ns/op	  615425 B/op	    3012 allocs/op
BenchmarkNoteSessionNoteTouches3000-8              	      24	   8528192 ns/op	 3443993 B/op	   30098 allocs/op
BenchmarkNoteParseLeakedForkOfFork100-8            	    1893	    126045 ns/op	   34096 B/op	     714 allocs/op
BenchmarkNoteParseLeakedForkOfFork300-8            	     492	    479819 ns/op	  100848 B/op	    2114 allocs/op
BenchmarkNotesList1000-8                           	      13	  15464216 ns/op	 4418404 B/op	   32988 allocs/op
BenchmarkNotesSearch1000-8                         	       8	  25340551 ns/op	 5209867 B/op	   38487 allocs/op
BenchmarkNotesAppendLarge-8                        	   14373	     22214 ns/op	     816 B/op	       9 allocs/op
BenchmarkNotesMatchingLines/ascii-8                	      57	   4020743 ns/op	 228.20 MB/s	   16394 B/op	       4 allocs/op
BenchmarkNotesMatchingLines/unicode-8              	      13	  15962314 ns/op	  59.53 MB/s	  960152 B/op	       6 allocs/op
BenchmarkNotesToolsASCIIFoldWorst/matchingLines-8  	      60	   3450824 ns/op	       0 B/op	       0 allocs/op
BenchmarkNotesToolsASCIIFoldWorst/stdlibIndexOfLowered-8         	      57	   4122368 ns/op	       0 B/op	       0 allocs/op
BenchmarkPageSize-8                                              	   42861	      5288 ns/op	    7240 B/op	      13 allocs/op
BenchmarkPageNotes-8                                             	    4474	     49604 ns/op	   61335 B/op	      33 allocs/op
BenchmarkNotesRange-8                                            	   66782	      3799 ns/op	    1984 B/op	       5 allocs/op
BenchmarkBuildAutoHandoff5000-8                                  	      18	  13019348 ns/op	 5113796 B/op	   27669 allocs/op
BenchmarkBuildAutoHandoff5000Retained-8                          	      16	  13028732 ns/op	 5104272 B/op	   27644 allocs/op
BenchmarkResetRequests-8                                         	  162193	      1325 ns/op	     448 B/op	       7 allocs/op
BenchmarkReminderLookupCold20000NoWindow-8                       	      81	   2654661 ns/op	  320080 B/op	   20001 allocs/op
BenchmarkReminderLookupCold20000WindowNearLeaf-8                 	  319003	       650.4 ns/op	     128 B/op	       4 allocs/op
BenchmarkReminderLookupCertified20000-8                          	 1225131	       195.8 ns/op	       0 B/op	       0 allocs/op
BenchmarkContextHookNoReminder1000-8                             	   16670	     14224 ns/op	       0 B/op	       0 allocs/op
BenchmarkContextHookStaleReminders1000-8                         	    2754	     84125 ns/op	   16384 B/op	       1 allocs/op
BenchmarkRetainedTailCut-8                                       	      74	   3150812 ns/op	 1952996 B/op	   17875 allocs/op
BenchmarkSessionLinesScan-8                                      	      12	  18402188 ns/op	1715.01 MB/s	 6311455 B/op	      16 allocs/op
BenchmarkSessionLinesScanLatin1-8                                	      15	  15469786 ns/op	2040.11 MB/s	 5259286 B/op	      15 allocs/op
BenchmarkCardNotePage40k/collapsed-8                             	     241	    992978 ns/op	   29360 B/op	     546 allocs/op
BenchmarkCardNotePage40k/expanded-8                              	      27	   7498368 ns/op	 1468688 B/op	   14761 allocs/op
BenchmarkCardHistoryPage/collapsed-8                             	    7062	     34946 ns/op	    4016 B/op	      27 allocs/op
BenchmarkCardHistoryPage/expanded-8                              	     141	   1543297 ns/op	  627124 B/op	    7423 allocs/op
BenchmarkCardMessage40k/collapsed-8                              	     422	    555315 ns/op	   28312 B/op	     522 allocs/op
BenchmarkCardMessage40k/expanded-8                               	      25	  11257545 ns/op	 2105012 B/op	   22149 allocs/op
BenchmarkDisplayOf-8                                             	  564980	       483.2 ns/op	     248 B/op	       5 allocs/op
BenchmarkUiLeadingRowsLatin-8                                    	     920	    264794 ns/op	   26784 B/op	     461 allocs/op
BenchmarkUiLeadingRowsCyrillic-8                                 	     283	    842941 ns/op	   31872 B/op	     588 allocs/op
BenchmarkUiLeadingRowsThai-8                                     	     228	   1043176 ns/op	   31168 B/op	     497 allocs/op
BenchmarkUiLeadingRowsArabic-8                                   	     279	    861399 ns/op	   32184 B/op	     623 allocs/op
BenchmarkUiLeadingRowsHangul-8                                   	     235	   1016635 ns/op	   68872 B/op	     410 allocs/op
BenchmarkUiLeadingRowsEmoji-8                                    	     360	    657358 ns/op	   31856 B/op	     393 allocs/op
```

Workload-level allocations (mem variant, Go): see the `total_alloc_workload` / `mallocs_workload` rows above (c1: 57.4 MiB, 835,605
mallocs per 31 requests; a: 0.04 MiB, 188 mallocs per idle second).

## Relative spread (p90-p50)/p50 target < 5%: not reachable, and why

Of 484 (cell, side, metric) rows of the ratchet metrics, 206 exceed 5% (list: `data/both/summary.md`, last section). By kind:
latency 101 of 112, CPU 51 of 76, heap/Sys as-is (GC timing) 23 of 60, memory (RSS, PSS, Anonymous, after-GC heap) 31 of 236.
More samples do not change a spread (it is a property of the distribution, not of the estimate): pooling 60 samples per cell and 1,500
tool calls per cell (e) left latency at 15-35%. Causes: (1) millisecond-scale latencies and 10-50 ms CPU values on a shared host
(load average 2.7-7.6, other users) with six pinned CPUs, scheduling jitter of the same order as the value; (2) 10 ms `/proc` CPU
ticks make a 20-90 ms CPU value move in steps of 11-50%; (3) small absolute memory (Go idle ext 9 MiB, Anonymous 2.4 MiB: 0.2 MiB is
8%); (4) V8 `heapTotal` is bimodal (70 vs 101 MiB) by GC timing. The memory rows with a median above 20 MiB that exceed 5% are TS
workload-e (6-11%, the heap grows by 120-240 MiB during 200 turns) and the Go e `tree_*` (host heap, 9-10%). Every ratchet decision above
is on medians whose Go/TS gap is far larger than the spread, except c1 turn_ms (826 vs 682, spreads 5% and 21%) and e (26.4 vs 16.0 s, spread 2%).

## Notes

* Tooling additions of this session (all under `port/perf/`): `--first N` and `PH_PERF_KEEP=1` (top-up runs append to the same data),
  `PH_PERF_CPUPROF=1` and `PH_PERF_HOSTPROF=1` diagnosis hooks, spread section in `summarize`, extension write/read syscall counters
  (`ext_sys*_workload`, information only; present in later runs).
* Scratch on xd6702 (`~/ph-work/scratch/PerfLane`) removed.
