# Lane P (perf): tooling and TS baseline

Files (all under `port/perf/`): `README.md` (method, exact commands, ratchet rules), `run.sh`, `perf.py`, `fakellm/`, `mem/`,
`data/ts-baseline/{samples.jsonl,meta.json,summary.md,summary-full.md}`. No production Go, nothing outside `port/perf/` edited.

## Commands

```sh
port/perf/run.sh both        # the lead's run once the port is green: go, ts and pi interleaved; prints the ratchet table
port/perf/run.sh go          # Go only (compare: python3 port/perf/perf.py summarize port/perf/data/go --vs port/perf/data/ts-baseline)
port/perf/run.sh ts          # TS under pig and Pi 1.5.0 (how the baseline was made)
port/perf/run.sh collect <name>   # if the local run.sh was killed (the remote job keeps going): wait, fetch, summarize
```

Pipeline proven on the TS side (630 valid samples, 0 invalid) and, for the Go side, on a stub extension (workloads a and f
through the same Go-cell path, plain and mem variants, binary size); workloads b..e on the Go side first run when the port
registers its tools (an absent tool fails the sample loudly).

## Baseline: TS original, 15 samples per cell (median, p90 in `data/ts-baseline/summary.md`)

Host: xd6702, Intel Xeon Platinum 8468, Linux 6.8.0-124, tern-ci:v7 container (Node v24.22.1, Go 1.27.3), pig 0.7.0+1.5.0,
Pi 1.5.0, six pinned CPUs (186-191), `nice -n 10`, load average at start 9.3/10.8/9.7, at end 5.4/7.1/8.6 (shared machine).
Session 2026-11-06 22:57 to 2026-11-07 00:53 UTC. Memory in MiB, times in ms; `ts` = TS under pig's Node runtime (the ratchet
reference), `pi` = Pi 1.5.0 reference. Extension processes: ts 1 (`node`), pi 0 (in-process), Go expected 1.

| Cell | startup | first turn | tool call (median) | CPU total end | ext RSS | ext PSS | heap used (GC) | Sys / Node RSS |
|---|---:|---:|---|---:|---:|---:|---:|---:|
| a idle warm: ts | 759 | - | - | 1030 | 161.3 | 118.8 | 34.9 | 152.9 |
| a idle warm: pi | 409 | - | - | 590 | 111.0 | 71.0 | 21.8 | 104.2 |
| a idle cold: ts | 2167 | - | - | 3520 | 312.3 | 268.5 | 47.2 | 301.1 |
| a idle cold: pi | 1533 | - | - | 2560 | 270.5 | 231.7 | 33.9 | 264.4 |
| b first turn warm: ts | 771 | 56.9 | get_context_remaining 9.5 | 1130 | 167.0 | 123.3 | 35.2 | 158.1 |
| b first turn cold: ts | 2173 | 55.5 | 8.9 | 3580 | 316.6 | 274.1 | 47.4 | 307.3 |
| c1 notes: ts | 765 | - | append 6.2, list 6.6, read 6.3, search 6.4, write 7.8 | 1810 | 209.0 | 170.8 | 35.9 | 187.5 |
| c100 notes: ts | 765 | - | 6.2 / 17.0 / 6.3 / 23.2 / 7.9 | 2110 | 225.0 | 181.1 | 36.1 | 213.7 |
| c1000 notes: ts | 766 | - | 6.5 / 37.0 / 6.1 / 83.6 / 7.9 | 2910 | 232.2 | 185.6 | 36.2 | 219.5 |
| d50 history 50 MB: ts | 765 | - | search rare 200.4, common 25.7, read 27.2 | 1570 | 208.6 | 161.2 | 36.3 | 207.9 |
| d200 history 200 MB: ts | 761 | - | rare 275.2, common 36.6, read 48.6 | 1910 | 212.5 | 162.0 | 36.3 | 207.8 |
| e 200 turns, 3 rollovers, 3 reminders: ts | 766 | - | get_context_remaining 5.8, notes.append 8.2, notes.write 10.1, read 10.9 | 18130 | 416.1 | 364.0 | 35.9 | 412.9 |
| f 60 sessions: ts | all ready 46052 | - | - | 68170 | 8104.6 (135.1 each) | 4823.9 (80.4 each) | - | - |
| f 60 sessions: pi | all ready 24875 | - | - | 40740 | 6165.3 (102.8 each) | 3084.9 (51.4 each) | - | - |

Full rows (p90, n, peak RSS, Anonymous, host columns, heap in use, GC events, `pi` for every cell, per-op latencies for pi):
`data/ts-baseline/summary.md` and `summary-full.md`. Workload e `ts`: `llm_requests` 200, `session_compaction` 3,
`session_custom_message` 3 (the parity rows the Go run must reproduce exactly).

Go binary size: measured by the `go` run (`meta.json` `go_binary`: pig's `runner` and its stripped copy; the runner is
already without symbols: stub 6,172,832 B). Allocations: no V8 counter exists, so the Go budget is Go-only (README rule 4).

## Notes and decisions for the lead

* Ratchet is median of every `RATCHET` metric plus every `tool_ms:*` on the `both` run; a repeated p90-only miss counts too.
* `cpu_tree_ms` (wait4) does not include the extension process (pig does not reap it) so the compared CPU is `cpu_total_ms_*`
  from `/proc` ticks (host + extension, each with waited-for children): 10 ms resolution.
* The Go `Sys` column is compared with Node RSS (README "Limits"); the owner may prefer another Node pairing.
* The TS original loads a 160 MiB idle process under pig (111 MiB inside Pi): Go beating these is expected; the cells most
  at risk for Go are the notes list/search at 1000 notes and history search over 200 MB (pure I/O and scan work, where the
  TS code is tuned: ripgrep prefilter, batched reads).
* The remote run dir `/home/kinsy/ph-work/scratch/PerfLane` is removed; `run.sh` recreates it (archives regenerate in about
  a minute, 250 MB).
