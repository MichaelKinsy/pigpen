# Lane P (perf): end-to-end basket, Go port vs TS original

# Round 2: Go extension with the retained-count cache (tip 8c8cd837) on pig@979bf718 and on pig built from PiG 25652299 (sdkframe)

Data: `port/perf/data/sf/` (`samples.jsonl` 1,400 samples, 0 invalid; `meta.json`; `summary.md` with all four ratchet pairs;
`summary-full.md`; `profiles/` raw pprof files and `go-sf-e-cpu-report.txt`). Round 1 (below) stays as recorded, tip 8a4d39f5.

**Setup.** Second pig: PiG commit 25652299a7b6805043337f5017ae72df2bdc6048 ("docs: bug ledger entry for the SDK frame decode"; the branch
`fix/071-sdkframe` has moved on to fe1fa18d, I built the commit you named) from a private shallow checkout under the scratch tree
(never `~/ph-work/PiG`), same build as the reference pig (`go build -trimpath`, cgo default, symbols kept) in the tern-ci:v7 container,
`pig reload --sdk-path` in an isolated PIG_HOME gave its SDK `sdk-946f155a549f8be9`; the Go extension on that side is built by that pig
against that SDK (reference pig: `sdk-5ee25f8133d42249`). Reproduce: `ph run "components/posthorse/port/perf/build-pig.sh <commit> sdkframe"`
then `port/perf/run.sh sf --samples 25 --workloads e,c1,c100,a,b`. Four sides interleaved sample by sample in one session
(sides `go`, `ts` = pig@979bf718; `go-sf`, `ts-sf` = pig@25652299; the TS original is identical on both pigs), 25 samples per cell and side,
workloads e, c1, c100, a, b (warm; a and b also cold), host quiet (load average 4.7/5.0/4.9 at start, 7.3/7.5/6.2 at end, other users
present), six pinned CPUs 186-191, `nice -n 10`. Run 2026-11-07 09:33-11:38 UTC. Parity: model requests and session records identical on
all four sides (e: 200 requests, 3 compactions, 3 reminders). Process count 1 on every pig side. Go binary (pig's `runner`): 7,811,232 B
on pig@979bf718, 7,823,520 B on sdkframe (7,808,264 / 7,820,552 B after `strip -s`).

## Verdict per metric: Go <= TS (median, same pig)

| Pair | Ratchet rows | Median misses |
|---|---:|---|
| Go vs TS on pig@979bf718 (old SDK, new extension) | 161 (`data/sf/summary.md`) | 6: c1 `turn_ms` (845 vs 786 ms) and e `turn_ms` (21.6 vs 16.4 s), `cpu_ext_workload_ms` (15.5 vs 9.5 s), `ext_cpu_ms_end`, `cpu_total_workload_ms` (26.1 vs 18.8 s), `cpu_total_ms_end` |
| **Go vs TS on pig@sdkframe (new SDK, new extension)** | 161 | **1: e `cpu_total_workload_ms` 16.57 s vs 16.11 s (+2.9%, p90 17.26 vs 17.12 s)** |

On sdkframe every other metric is Go <= TS: startup, first turn, first tool, per-tool-call latency (all ops), RSS, PSS, Anonymous, peak RSS,
tree RSS/PSS, heap used/in use, Go Sys vs Node RSS in e, c1, c100, a, b, warm and cold; **e `turn_ms` 13.15 s vs 13.63 s (-3.6%), e extension CPU
8.68 s vs 9.36 s (-7%), c1 `turn_ms` 654 vs 722 ms, c1 total CPU 770 vs 950 ms, c100 `turn_ms` 802 vs 994 ms**. Only e total CPU stays above TS because
the host spends more CPU serving a Go extension than a Node one (host CPU over the workload, derived: e 7.86 vs 6.83 s, c1 410 vs 350 ms, c100
490 vs 410 ms; not a ratchet metric, but it is the whole e gap: extension 8.68 vs 9.36 s is -0.68 s, host +1.03 s). The e miss is of the size of the spread (p90-p50: Go-sf 4%, TS-sf 6%); it is a near tie, and the extension itself is faster, so it is
for the owner to decide whether a host-side cost of +1 s per 200 turns blocks the ratchet.

Effect of the two fixes on Go (round 1 -> now, pig@979bf718 -> sdkframe, e): `turn_ms` 26.4 s -> 21.6 s (cache alone) -> 13.1 s (cache + SDK frame fix); extension
CPU 18.1 -> 15.5 -> 8.7 s; host CPU 13.3 -> 10.6 -> 7.9 s; mallocs 12.2 M -> 7.3 M and 2.0 -> 1.4 GiB allocated per run (`num_gc` 1006 -> 768).
The cache removed the host `handleSessionRead` cost (2.42 s -> 0.27 s per run).

## Fresh CPU profile of the Go extension, workload e on pig@sdkframe (2 runs merged, 15.57 s sampled; `data/sf/profiles/go-sf-e-cpu-report.txt`)

Top 15 by cumulative time (function: cum, %):

| # | Function | cum | % |
|---|---|---:|---:|
| 1 | sdk `(*Extension).loop.func1` / `sync.(*WaitGroup).Go.func1` (one handler goroutine per inbound request) | 7.62 s | 48.9 |
| 2 | sdk `(*Extension).handleArmedRequest` | 7.61 s | 48.9 |
| 3 | `runtime.systemstack` | 4.01 s | 25.8 |
| 4 | sdk/json `(*decodeState).unmarshal` | 3.87 s | 24.9 |
| 5 | sdk/json `(*decodeState).object` | 3.86 s | 24.8 |
| 6 | sdk/json `(*decodeState).value` | 3.86 s | 24.8 |
| 7 | sdk `unmarshalFrameMember` (the new single decode of the event payload) | 3.24 s | 20.8 |
| 8 | sdk/json `UnmarshalTrusted` | 3.24 s | 20.8 |
| 9 | sdk/json `arrayInterface` | 3.17 s | 20.4 |
| 10 | sdk/json `objectInterface` | 3.16 s | 20.3 |
| 11 | sdk/json `valueInterface` | 3.15 s | 20.2 |
| 12 | sdk/json `MarshalJS` (all outbound encoding) | 3.07 s | 19.7 |
| 13 | `runtime.gcBgMarkWorker` | 3.07 s | 19.7 |
| 14 | sdk/json `(*encodeState).marshal` | 2.86 s | 18.4 |
| 15 | sdk `(*conn).respond` / `encodeResponse` | 2.68 s / 2.59 s | 17.2 / 16.6 |

Exclusive split (each sample counted once, priority posthorse > SDK > GC/allocator): **posthorse code 8.6%** (1.34 s, including the SDK calls
its own code makes, e.g. host reads), **Go SDK 49.5%** (7.71 s: event decode 20.8%, response encode 16.6%, call-envelope encode 10.5%, rest
request plumbing), **GC and allocator 21.9%** (3.41 s, background mark workers 19.7%), other runtime (syscalls, scheduler, frame reads) about 20%.
Before the fix the same split was posthorse 10%, SDK 85% (of which `checkValid` 24%): the second validation pass is gone (`UnmarshalTrusted`), the
projection read is gone (cache), and the extension is no longer CPU-bound on validation.

Host side (pig profile, `PH_PERF_HOSTPROF=1`, go-sf vs ts-sf, e, one run diff): the Go-only host cost is `AgentMessage.UnmarshalJSON`/`keepSource` +0.98 s
(15% of the host profile, via `inproc.coerceResult`): the host decodes the full message list the Go extension returns from `context_with_system`.
Cause (guidance.go:57-83): every turn the handler writes `system["sections"]` in place, so the SDK cannot report the list unchanged; it re-decodes the
pristine copy to compare and re-encodes all messages (the `encodeResponse` 16.6% above), and the host decodes the reply again. [INFERENCE] The Node extension's reply does not pay the host-side `keepSource` decode (the diff shows it as Go-only); I did not profile the Node side's reply path. Remaining host `handleSessionRead` is 0.27 s per run (was 2.42 s). Fix options (not mine to apply):
(SDK) a `context_with_system` reply form that carries only the changed system message/sections, or a host-side skip of `AgentMessage` decode for messages the host
already holds; (posthorse) none that keeps the byte-exact behaviour, since the TS also mutates the system message per turn.

## Tables: Go vs TS on both pigs (median / p90; spread = (p90-p50)/p50 of Go-sf and TS-sf)

`host_cpu_workload_ms` is derived (host CPU at the end minus at ready, 10 ms ticks) and is information, not a ratchet metric. `Go<=TS` compares medians.
`sys_asis_end` is Go `Sys` against Node RSS. Allocation rows are Go-only (V8 has no allocation counter; `num_gc_workload` of TS = V8 GC events).

#### e / warm (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 113.5 / 128.2 | 813.1 / 887.1 | <= | 116.1 / 135.6 | 870.8 / 884.3 | <= | 17% / 2% |
| to_first_tool_ms | 21.4 / 23.9 | 36.6 / 40.5 | <= | 19.6 / 23.0 | 36.0 / 38.8 | <= | 17% / 8% |
| turn_ms | 21590.2 / 21962.6 | 16413.8 / 16877.1 | **WORSE** | 13147.6 / 13603.5 | 13632.6 / 14213.6 | <= | 3% / 4% |
| cpu_ext_workload_ms | 15530.0 / 15900.0 | 9480.0 / 9950.0 | **WORSE** | 8680.0 / 9100.0 | 9360.0 / 10060.0 | <= | 5% / 7% |
| host_cpu_workload_ms | 10580.0 / 10900.0 | 9230.0 / 9420.0 | **WORSE** | 7860.0 / 8200.0 | 6830.0 / 7070.0 | **WORSE** | 4% / 4% |
| cpu_total_workload_ms | 26080.0 / 26820.0 | 18760.0 / 19370.0 | **WORSE** | 16570.0 / 17260.0 | 16110.0 / 17120.0 | **WORSE** | 4% / 6% |
| ext_cpu_ms_end | 15530 / 15910 | 10450 / 10820 | **WORSE** | 8690 / 9110 | 10310 / 11060 | <= | 5% / 7% |
| cpu_total_ms_end | 26170 / 26920 | 19880 / 20410 | **WORSE** | 16640 / 17360 | 17250 / 18320 | <= | 4% / 6% |
| ext_rss_end | 15.56 MiB / 22.21 MiB | 484.84 MiB / 514.46 MiB | <= | 19.70 MiB / 24.93 MiB | 454.79 MiB / 495.83 MiB | <= | 27% / 9% |
| ext_pss_end | 15.56 MiB / 22.21 MiB | 480.96 MiB / 510.55 MiB | <= | 19.69 MiB / 24.93 MiB | 450.86 MiB / 491.91 MiB | <= | 27% / 9% |
| ext_anon_end | 8.39 MiB / 15.04 MiB | 424.49 MiB / 454.18 MiB | <= | 12.56 MiB / 17.80 MiB | 394.29 MiB / 435.34 MiB | <= | 42% / 10% |
| ext_peak_rss_end | 21.43 MiB / 24.06 MiB | 534.12 MiB / 546.41 MiB | <= | 20.24 MiB / 24.93 MiB | 516.20 MiB / 539.96 MiB | <= | 23% / 5% |
| tree_rss_end | 130.51 MiB / 135.41 MiB | 615.47 MiB / 640.92 MiB | <= | 132.29 MiB / 139.77 MiB | 590.32 MiB / 628.05 MiB | <= | 6% / 6% |
| tree_pss_end | 129.15 MiB / 134.00 MiB | 610.23 MiB / 635.60 MiB | <= | 130.89 MiB / 138.40 MiB | 585.01 MiB / 622.76 MiB | <= | 6% / 6% |
| heap_used_gc_end | 0.71 MiB / 0.72 MiB | 35.94 MiB / 36.81 MiB | <= | 0.71 MiB / 0.72 MiB | 35.94 MiB / 36.86 MiB | <= | 1% / 3% |
| heap_inuse_gc_end | 2.41 MiB / 2.68 MiB | 169.09 MiB / 169.84 MiB | <= | 2.46 MiB / 2.77 MiB | 169.09 MiB / 169.59 MiB | <= | 13% / 0% |
| sys_asis_end (Go Sys vs Node RSS) | 25.94 MiB / 26.26 MiB | 470.60 MiB / 492.88 MiB | <= | 25.94 MiB / 26.19 MiB | 423.32 MiB / 522.68 MiB | <= | 1% / 23% |
| tool_ms:get_context_remaining | 5.5 / 6.9 | 7.5 / 9.4 | <= | 5.1 / 6.8 | 5.9 / 9.1 | <= | 33% / 56% |
| tool_ms:notes.append | 6.9 / 8.4 | 9.8 / 12.3 | <= | 6.6 / 8.1 | 8.7 / 11.8 | <= | 23% / 36% |
| tool_ms:notes.write | 7.3 / 9.0 | 12.1 / 15.3 | <= | 7.0 / 8.6 | 11.2 / 14.6 | <= | 23% / 31% |
| tool_ms:read | 11.1 / 13.1 | 12.4 / 14.3 | <= | 7.7 / 9.2 | 11.3 / 13.8 | <= | 19% / 22% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 2009.56 MiB | - | | 1425.69 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 12183835 | - | | 7319241 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 1006 | 29 | | 768 | 44 | | |

#### c1 / warm (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 116.5 / 126.8 | 801.3 / 884.8 | <= | 116.0 / 125.0 | 823.0 / 881.1 | <= | 8% / 7% |
| to_first_tool_ms | 20.5 / 22.3 | 34.3 / 37.3 | <= | 18.2 / 20.4 | 34.5 / 37.7 | <= | 12% / 9% |
| turn_ms | 844.8 / 867.8 | 785.9 / 847.6 | **WORSE** | 653.9 / 726.3 | 721.7 / 818.7 | <= | 11% / 13% |
| cpu_ext_workload_ms | 530.0 / 550.0 | 620.0 / 670.0 | <= | 360.0 / 420.0 | 590.0 / 670.0 | <= | 17% / 14% |
| host_cpu_workload_ms | 480.0 / 500.0 | 380.0 / 420.0 | **WORSE** | 410.0 / 470.0 | 350.0 / 410.0 | **WORSE** | 15% / 17% |
| cpu_total_workload_ms | 1010.0 / 1050.0 | 1030.0 / 1090.0 | <= | 770.0 / 900.0 | 950.0 / 1070.0 | <= | 17% / 13% |
| ext_cpu_ms_end | 540 / 560 | 1510 / 1670 | <= | 370 / 430 | 1500 / 1680 | <= | 16% / 12% |
| cpu_total_ms_end | 1110 / 1130 | 2070 / 2270 | <= | 850 / 990 | 2030 / 2280 | <= | 16% / 12% |
| ext_rss_end | 14.42 MiB / 14.64 MiB | 208.82 MiB / 210.01 MiB | <= | 14.24 MiB / 14.39 MiB | 209.09 MiB / 210.13 MiB | <= | 1% / 1% |
| ext_pss_end | 14.42 MiB / 14.64 MiB | 205.03 MiB / 206.10 MiB | <= | 14.23 MiB / 14.39 MiB | 205.18 MiB / 206.22 MiB | <= | 1% / 1% |
| ext_anon_end | 7.31 MiB / 7.47 MiB | 148.70 MiB / 149.79 MiB | <= | 7.13 MiB / 7.27 MiB | 148.77 MiB / 149.91 MiB | <= | 2% / 1% |
| ext_peak_rss_end | 14.42 MiB / 14.64 MiB | 208.82 MiB / 210.01 MiB | <= | 14.24 MiB / 14.39 MiB | 209.09 MiB / 210.13 MiB | <= | 1% / 1% |
| tree_rss_end | 85.35 MiB / 86.51 MiB | 319.34 MiB / 323.37 MiB | <= | 86.04 MiB / 87.70 MiB | 321.11 MiB / 323.86 MiB | <= | 2% / 1% |
| tree_pss_end | 83.92 MiB / 85.08 MiB | 314.12 MiB / 318.11 MiB | <= | 84.61 MiB / 86.30 MiB | 315.85 MiB / 318.60 MiB | <= | 2% / 1% |
| heap_used_gc_end | 0.57 MiB / 0.57 MiB | 35.89 MiB / 35.89 MiB | <= | 0.57 MiB / 0.58 MiB | 35.89 MiB / 35.90 MiB | <= | 2% / 0% |
| heap_inuse_gc_end | 1.71 MiB / 1.83 MiB | 102.59 MiB / 103.09 MiB | <= | 1.70 MiB / 1.79 MiB | 102.59 MiB / 103.09 MiB | <= | 6% / 0% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 188.50 MiB / 198.81 MiB | <= | 17.69 MiB / 17.94 MiB | 187.50 MiB / 197.18 MiB | <= | 1% / 5% |
| tool_ms:notes.append | 5.8 / 7.6 | 7.8 / 11.3 | <= | 5.0 / 6.9 | 7.4 / 11.2 | <= | 39% / 53% |
| tool_ms:notes.list | 5.8 / 7.5 | 9.0 / 14.8 | <= | 4.9 / 7.4 | 8.8 / 15.0 | <= | 51% / 70% |
| tool_ms:notes.read | 5.4 / 7.5 | 7.9 / 12.1 | <= | 4.2 / 6.4 | 8.0 / 12.0 | <= | 54% / 49% |
| tool_ms:notes.search | 5.5 / 7.5 | 8.9 / 12.4 | <= | 4.7 / 7.0 | 8.7 / 12.2 | <= | 50% / 40% |
| tool_ms:notes.write | 6.1 / 7.8 | 10.1 / 14.0 | <= | 5.1 / 6.6 | 9.7 / 13.9 | <= | 30% / 44% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 57.29 MiB | - | | 47.73 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 835118 | - | | 496264 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 32 | 4 | | 28 | 4 | | |

#### c100 / warm (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 116.6 / 123.3 | 831.3 / 886.3 | <= | 116.5 / 129.1 | 825.3 / 877.7 | <= | 11% / 6% |
| to_first_tool_ms | 20.3 / 23.1 | 34.3 / 38.0 | <= | 18.6 / 20.9 | 34.5 / 36.5 | <= | 12% / 6% |
| turn_ms | 1054.1 / 1132.0 | 1058.9 / 1206.3 | <= | 801.7 / 859.9 | 994.3 / 1130.1 | <= | 7% / 14% |
| cpu_ext_workload_ms | 730.0 / 800.0 | 850.0 / 1000.0 | <= | 510.0 / 560.0 | 830.0 / 980.0 | <= | 10% / 18% |
| host_cpu_workload_ms | 540.0 / 590.0 | 430.0 / 490.0 | **WORSE** | 490.0 / 520.0 | 410.0 / 440.0 | **WORSE** | 6% / 7% |
| cpu_total_workload_ms | 1260.0 / 1390.0 | 1280.0 / 1490.0 | <= | 1000.0 / 1070.0 | 1240.0 / 1420.0 | <= | 7% / 15% |
| ext_cpu_ms_end | 740 / 800 | 1810 / 2040 | <= | 510 / 570 | 1830 / 1980 | <= | 12% / 8% |
| cpu_total_ms_end | 1350 / 1470 | 2420 / 2710 | <= | 1080 / 1170 | 2440 / 2610 | <= | 8% / 7% |
| ext_rss_end | 14.88 MiB / 15.16 MiB | 223.55 MiB / 226.24 MiB | <= | 14.61 MiB / 15.01 MiB | 224.95 MiB / 226.31 MiB | <= | 3% / 1% |
| ext_pss_end | 14.88 MiB / 15.15 MiB | 219.67 MiB / 222.36 MiB | <= | 14.61 MiB / 15.01 MiB | 221.08 MiB / 222.47 MiB | <= | 3% / 1% |
| ext_anon_end | 7.70 MiB / 7.98 MiB | 163.34 MiB / 166.02 MiB | <= | 7.48 MiB / 7.88 MiB | 164.78 MiB / 166.06 MiB | <= | 5% / 1% |
| ext_peak_rss_end | 14.91 MiB / 15.16 MiB | 223.55 MiB / 226.24 MiB | <= | 14.61 MiB / 15.01 MiB | 224.95 MiB / 226.31 MiB | <= | 3% / 1% |
| tree_rss_end | 87.14 MiB / 88.12 MiB | 332.32 MiB / 338.35 MiB | <= | 87.70 MiB / 88.73 MiB | 333.23 MiB / 339.05 MiB | <= | 1% / 2% |
| tree_pss_end | 85.80 MiB / 86.77 MiB | 327.10 MiB / 333.11 MiB | <= | 86.29 MiB / 87.37 MiB | 327.94 MiB / 333.79 MiB | <= | 1% / 2% |
| heap_used_gc_end | 0.62 MiB / 0.64 MiB | 36.07 MiB / 36.09 MiB | <= | 0.62 MiB / 0.64 MiB | 36.08 MiB / 36.10 MiB | <= | 3% / 0% |
| heap_inuse_gc_end | 1.77 MiB / 1.88 MiB | 103.59 MiB / 104.34 MiB | <= | 1.77 MiB / 1.85 MiB | 103.59 MiB / 104.34 MiB | <= | 4% / 1% |
| sys_asis_end (Go Sys vs Node RSS) | 17.69 MiB / 17.94 MiB | 206.28 MiB / 218.06 MiB | <= | 17.76 MiB / 17.94 MiB | 205.31 MiB / 217.72 MiB | <= | 1% / 6% |
| tool_ms:notes.append | 5.9 / 7.6 | 7.7 / 11.2 | <= | 5.5 / 7.2 | 7.5 / 11.1 | <= | 30% / 48% |
| tool_ms:notes.list | 10.4 / 13.2 | 21.0 / 30.6 | <= | 9.1 / 11.9 | 20.6 / 29.1 | <= | 31% / 41% |
| tool_ms:notes.read | 5.9 / 7.7 | 7.9 / 11.2 | <= | 5.0 / 6.9 | 7.8 / 11.7 | <= | 37% / 50% |
| tool_ms:notes.search | 11.0 / 13.4 | 31.0 / 41.5 | <= | 9.9 / 13.0 | 29.2 / 39.1 | <= | 31% / 34% |
| tool_ms:notes.write | 6.3 / 8.2 | 9.7 / 14.1 | <= | 5.7 / 7.0 | 9.5 / 13.5 | <= | 23% / 43% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 84.39 MiB | - | | 67.16 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 915027 | - | | 578618 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 48 | 6 | | 41 | 6 | | |

#### a / warm (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 117.1 / 126.5 | 855.7 / 885.6 | <= | 116.0 / 127.4 | 849.6 / 880.7 | <= | 10% / 4% |
| cpu_ext_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| host_cpu_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| cpu_total_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| ext_cpu_ms_end | 10 / 10 | 1000 / 1020 | <= | 10 / 10 | 970 / 1020 | <= | 0% / 5% |
| cpu_total_ms_end | 90 / 100 | 1180 / 1220 | <= | 90 / 100 | 1160 / 1210 | <= | 11% / 4% |
| ext_rss_end | 9.40 MiB / 9.59 MiB | 161.08 MiB / 161.93 MiB | <= | 9.36 MiB / 9.58 MiB | 160.73 MiB / 161.91 MiB | <= | 2% / 1% |
| ext_pss_end | 9.40 MiB / 9.59 MiB | 157.22 MiB / 158.02 MiB | <= | 9.36 MiB / 9.58 MiB | 156.81 MiB / 158.04 MiB | <= | 2% / 1% |
| ext_anon_end | 2.48 MiB / 2.67 MiB | 105.54 MiB / 106.34 MiB | <= | 2.50 MiB / 2.71 MiB | 104.99 MiB / 106.43 MiB | <= | 9% / 1% |
| ext_peak_rss_end | 9.40 MiB / 9.59 MiB | 161.08 MiB / 161.93 MiB | <= | 9.36 MiB / 9.58 MiB | 160.73 MiB / 161.91 MiB | <= | 2% / 1% |
| tree_rss_end | 70.00 MiB / 70.67 MiB | 245.41 MiB / 246.10 MiB | <= | 70.17 MiB / 71.06 MiB | 245.02 MiB / 246.19 MiB | <= | 1% / 0% |
| tree_pss_end | 68.55 MiB / 69.34 MiB | 240.09 MiB / 240.77 MiB | <= | 68.82 MiB / 69.63 MiB | 239.67 MiB / 240.93 MiB | <= | 1% / 1% |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB | 34.94 MiB / 34.94 MiB | <= | 0.51 MiB / 0.51 MiB | 34.95 MiB / 34.95 MiB | <= | 0% / 0% |
| heap_inuse_gc_end | 1.48 MiB / 1.60 MiB | 69.84 MiB / 101.59 MiB | <= | 1.53 MiB / 1.57 MiB | 69.84 MiB / 101.09 MiB | <= | 3% / 45% |
| sys_asis_end (Go Sys vs Node RSS) | 13.40 MiB / 13.66 MiB | 149.09 MiB / 154.30 MiB | <= | 13.41 MiB / 13.66 MiB | 149.61 MiB / 152.32 MiB | <= | 2% / 2% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 0.04 MiB | - | | 0.04 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 187 | - | | 183 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 2 | 1 | | 2 | 1 | | |

#### a / cold (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 736.5 / 762.1 | 2293.2 / 2485.4 | <= | 739.3 / 794.9 | 2324.2 / 2526.1 | <= | 8% / 9% |
| cpu_ext_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| host_cpu_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| cpu_total_workload_ms | 0.0 / 0.0 | 0.0 / 0.0 | <= | 0.0 / 0.0 | 0.0 / 0.0 | <= | - / - |
| ext_cpu_ms_end | 10 / 10 | 3090 / 3420 | <= | 10 / 10 | 3150 / 3490 | <= | 0% / 11% |
| cpu_total_ms_end | 1140 / 1190 | 3740 / 4070 | <= | 1130 / 1200 | 3810 / 4170 | <= | 6% / 9% |
| ext_rss_end | 9.34 MiB / 9.59 MiB | 311.43 MiB / 314.92 MiB | <= | 9.37 MiB / 9.58 MiB | 311.56 MiB / 314.60 MiB | <= | 2% / 1% |
| ext_pss_end | 9.33 MiB / 9.59 MiB | 307.50 MiB / 311.01 MiB | <= | 9.36 MiB / 9.58 MiB | 307.60 MiB / 310.63 MiB | <= | 2% / 1% |
| ext_anon_end | 2.42 MiB / 2.61 MiB | 255.71 MiB / 259.29 MiB | <= | 2.45 MiB / 2.71 MiB | 255.86 MiB / 258.83 MiB | <= | 11% / 1% |
| ext_peak_rss_end | 9.34 MiB / 9.59 MiB | 311.43 MiB / 314.92 MiB | <= | 9.37 MiB / 9.58 MiB | 311.56 MiB / 314.60 MiB | <= | 2% / 1% |
| tree_rss_end | 82.64 MiB / 85.45 MiB | 463.91 MiB / 473.21 MiB | <= | 83.06 MiB / 86.72 MiB | 460.76 MiB / 473.34 MiB | <= | 4% / 3% |
| tree_pss_end | 81.28 MiB / 84.00 MiB | 458.49 MiB / 467.87 MiB | <= | 81.67 MiB / 85.31 MiB | 455.43 MiB / 468.04 MiB | <= | 4% / 3% |
| heap_used_gc_end | 0.51 MiB / 0.51 MiB | 47.18 MiB / 47.19 MiB | <= | 0.51 MiB / 0.51 MiB | 47.19 MiB / 47.19 MiB | <= | 1% / 0% |
| heap_inuse_gc_end | 1.55 MiB / 1.59 MiB | 181.25 MiB / 182.00 MiB | <= | 1.51 MiB / 1.60 MiB | 181.25 MiB / 182.00 MiB | <= | 6% / 0% |
| sys_asis_end (Go Sys vs Node RSS) | 13.41 MiB / 13.60 MiB | 301.98 MiB / 304.50 MiB | <= | 13.40 MiB / 13.66 MiB | 302.02 MiB / 304.50 MiB | <= | 2% / 1% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 0.04 MiB | - | | 0.04 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 188 | - | | 187 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 2 | 1 | | 2 | 1 | | |

#### b / warm (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 116.2 / 126.3 | 876.4 / 893.9 | <= | 116.7 / 135.8 | 861.2 / 878.5 | <= | 16% / 2% |
| first_turn_ms | 42.8 / 48.8 | 69.4 / 74.3 | <= | 40.7 / 43.6 | 68.5 / 72.6 | <= | 7% / 6% |
| to_first_tool_ms | 19.9 / 21.9 | 35.3 / 37.3 | <= | 19.0 / 21.2 | 34.9 / 37.9 | <= | 12% / 9% |
| turn_ms | 42.8 / 48.8 | 69.4 / 74.3 | <= | 40.7 / 43.6 | 68.5 / 72.6 | <= | 7% / 6% |
| cpu_ext_workload_ms | 20.0 / 30.0 | 60.0 / 80.0 | <= | 20.0 / 30.0 | 70.0 / 80.0 | <= | 50% / 14% |
| host_cpu_workload_ms | 30.0 / 40.0 | 30.0 / 40.0 | <= | 30.0 / 40.0 | 30.0 / 40.0 | <= | 33% / 33% |
| cpu_total_workload_ms | 40.0 / 60.0 | 90.0 / 120.0 | <= | 50.0 / 60.0 | 100.0 / 110.0 | <= | 20% / 10% |
| ext_cpu_ms_end | 30 / 40 | 1070 / 1090 | <= | 20 / 30 | 1060 / 1100 | <= | 50% / 4% |
| cpu_total_ms_end | 140 / 160 | 1300 / 1320 | <= | 140 / 150 | 1280 / 1320 | <= | 7% / 3% |
| ext_rss_end | 10.99 MiB / 12.04 MiB | 167.53 MiB / 169.23 MiB | <= | 10.71 MiB / 11.95 MiB | 167.82 MiB / 169.37 MiB | <= | 12% / 1% |
| ext_pss_end | 10.98 MiB / 12.04 MiB | 163.66 MiB / 165.44 MiB | <= | 10.70 MiB / 11.95 MiB | 163.97 MiB / 165.58 MiB | <= | 12% / 1% |
| ext_anon_end | 4.01 MiB / 5.00 MiB | 107.22 MiB / 109.08 MiB | <= | 3.78 MiB / 4.96 MiB | 107.57 MiB / 109.24 MiB | <= | 31% / 2% |
| ext_peak_rss_end | 10.99 MiB / 12.04 MiB | 167.53 MiB / 169.23 MiB | <= | 10.71 MiB / 11.95 MiB | 167.82 MiB / 169.37 MiB | <= | 12% / 1% |
| tree_rss_end | 73.94 MiB / 74.91 MiB | 257.04 MiB / 258.77 MiB | <= | 73.59 MiB / 74.59 MiB | 257.28 MiB / 259.01 MiB | <= | 1% / 1% |
| tree_pss_end | 72.57 MiB / 73.55 MiB | 251.77 MiB / 253.46 MiB | <= | 72.22 MiB / 73.16 MiB | 251.98 MiB / 253.75 MiB | <= | 1% / 1% |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB | 35.17 MiB / 35.17 MiB | <= | 0.54 MiB / 0.55 MiB | 35.18 MiB / 35.18 MiB | <= | 1% / 0% |
| heap_inuse_gc_end | 1.66 MiB / 1.75 MiB | 70.34 MiB / 101.59 MiB | <= | 1.63 MiB / 1.72 MiB | 70.09 MiB / 101.59 MiB | <= | 5% / 45% |
| sys_asis_end (Go Sys vs Node RSS) | 13.66 MiB / 13.91 MiB | 158.67 MiB / 162.00 MiB | <= | 13.41 MiB / 13.92 MiB | 156.00 MiB / 160.07 MiB | <= | 4% / 3% |
| tool_ms:get_context_remaining | 4.8 / 6.6 | 12.9 / 14.4 | <= | 4.6 / 6.5 | 12.1 / 14.8 | <= | 42% / 22% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 1.32 MiB | - | | 1.12 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 13881 | - | | 9599 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 2 | 1 | | 2 | 1 | | |

#### b / cold (n=25 per side; median / p90)

| Metric | Go @979bf718 | TS @979bf718 | Go<=TS | Go @sdkframe | TS @sdkframe | Go<=TS | spread Go-sf / TS-sf |
|---|---:|---:|:--:|---:|---:|:--:|---:|
| startup_ms | 753.7 / 790.7 | 2382.2 / 2548.0 | <= | 774.1 / 787.1 | 2351.1 / 2542.0 | <= | 2% / 8% |
| first_turn_ms | 47.0 / 50.6 | 65.8 / 71.6 | <= | 40.7 / 46.3 | 62.2 / 71.3 | <= | 14% / 15% |
| to_first_tool_ms | 20.8 / 25.0 | 34.4 / 37.3 | <= | 19.8 / 22.3 | 33.5 / 36.7 | <= | 13% / 10% |
| turn_ms | 47.0 / 50.6 | 65.8 / 71.6 | <= | 40.7 / 46.3 | 62.2 / 71.3 | <= | 14% / 15% |
| cpu_ext_workload_ms | 20.0 / 30.0 | 60.0 / 80.0 | <= | 20.0 / 20.0 | 70.0 / 80.0 | <= | 0% / 14% |
| host_cpu_workload_ms | 30.0 / 40.0 | 30.0 / 40.0 | <= | 30.0 / 40.0 | 30.0 / 40.0 | <= | 33% / 33% |
| cpu_total_workload_ms | 50.0 / 60.0 | 90.0 / 110.0 | <= | 40.0 / 60.0 | 90.0 / 110.0 | <= | 50% / 22% |
| ext_cpu_ms_end | 30 / 40 | 3280 / 3560 | <= | 20 / 30 | 3240 / 3550 | <= | 50% / 10% |
| cpu_total_ms_end | 1210 / 1250 | 3980 / 4270 | <= | 1230 / 1250 | 3920 / 4260 | <= | 2% / 9% |
| ext_rss_end | 11.06 MiB / 12.18 MiB | 316.81 MiB / 319.59 MiB | <= | 10.71 MiB / 10.89 MiB | 316.57 MiB / 319.12 MiB | <= | 2% / 1% |
| ext_pss_end | 11.05 MiB / 12.18 MiB | 312.89 MiB / 315.62 MiB | <= | 10.71 MiB / 10.89 MiB | 312.64 MiB / 315.20 MiB | <= | 2% / 1% |
| ext_anon_end | 4.08 MiB / 5.13 MiB | 256.34 MiB / 259.21 MiB | <= | 3.78 MiB / 3.96 MiB | 256.14 MiB / 258.83 MiB | <= | 5% / 1% |
| ext_peak_rss_end | 11.06 MiB / 12.18 MiB | 316.81 MiB / 319.59 MiB | <= | 10.71 MiB / 10.89 MiB | 316.57 MiB / 319.12 MiB | <= | 2% / 1% |
| tree_rss_end | 85.83 MiB / 87.69 MiB | 473.58 MiB / 483.43 MiB | <= | 84.25 MiB / 87.10 MiB | 480.62 MiB / 484.38 MiB | <= | 3% / 1% |
| tree_pss_end | 84.43 MiB / 86.28 MiB | 468.35 MiB / 478.04 MiB | <= | 82.92 MiB / 85.69 MiB | 475.35 MiB / 479.08 MiB | <= | 3% / 1% |
| heap_used_gc_end | 0.54 MiB / 0.54 MiB | 47.42 MiB / 47.43 MiB | <= | 0.54 MiB / 0.54 MiB | 47.43 MiB / 47.44 MiB | <= | 0% / 0% |
| heap_inuse_gc_end | 1.61 MiB / 1.71 MiB | 181.25 MiB / 181.50 MiB | <= | 1.63 MiB / 1.76 MiB | 181.50 MiB / 182.00 MiB | <= | 8% / 0% |
| sys_asis_end (Go Sys vs Node RSS) | 13.41 MiB / 17.40 MiB | 307.50 MiB / 309.00 MiB | <= | 13.42 MiB / 17.41 MiB | 307.12 MiB / 310.00 MiB | <= | 30% / 1% |
| tool_ms:get_context_remaining | 4.9 / 6.5 | 10.9 / 14.2 | <= | 4.6 / 6.0 | 10.4 / 13.9 | <= | 30% / 34% |
| total_alloc_workload (Go only; TS = V8 GC events for num_gc) | 1.31 MiB | - | | 1.12 MiB | - | | |
| mallocs_workload (Go only; TS = V8 GC events for num_gc) | 13866 | - | | 9617 | - | | |
| num_gc_workload (Go only; TS = V8 GC events for num_gc) | 2 | 1 | | 2 | 1 | | |


## Notes for round 2

* Spreads: CPU and latency spreads above 5% remain (10 ms ticks, ms-scale latencies, shared host); the memory rows with medians above 20 MiB are within
  5% except the TS e rows (9-10%); Go e RSS/Anonymous (15-20 MiB, 12 MiB) spread 23-42%. Round 1's explanation applies unchanged.
* Tooling added: `build-pig.sh`, `run.sh sf`, sides `go-sf`/`ts-sf` (`PH_PERF_PIG_SF`, `PH_PERF_SDK_SF`), `PAIRS` in the summary (Go vs TS per pig, plus
  go-sf vs go as information), per-side Go binary sizes in `meta.json`.
* Scratch (`~/ph-work/scratch/PerfLane`: the pig checkout, the sdkframe pig, SDK home, profiles, outputs) removed; `build-pig.sh` recreates it.

---

# Round 1 (tip 8a4d39f5, pig@979bf718 only)

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
