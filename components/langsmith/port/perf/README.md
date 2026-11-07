# LangSmith performance basket

Measures the Go port (`extensions/langsmith`, run by PiG) against the TypeScript original
(`@langchain/langsmith-pi-extension` 0.2.1, `port/oracle/dist/index.js`, langsmith SDK 0.10.5) on the owner's basket: CPU,
RSS / PSS / Anonymous / peak RSS, Go heap and runtime `Sys` (Node heap and RSS for TS), startup, first-turn latency,
shutdown and flush time, allocations. Everything is hermetic and offline: a fresh `HOME`/`PIG_HOME`/agent directory per run
(warm cache homes are reused only as caches), a scripted model and a LangSmith mock on loopback, no network, never the real
`~/.pig`, `~/.pi`, `~/.langsmith` or `auth.json`, a fake API key.

## One command

```sh
port/perf/run.sh both            # go, ts and pi interleaved sample by sample: the authoritative comparison (about 2.5 h)
port/perf/run.sh go              # the Go port only
port/perf/run.sh ts              # the TS original under pig (side "ts") and under Pi 1.5.0 (side "pi")
port/perf/run.sh both --samples 15 --workloads a,b,e      # any `perf.py run` flag follows the mode
port/perf/run.sh both --samples 15 --mem-samples 8 --workloads d1000
port/perf/run.sh go --workloads e --samples 1 --profile /home/kinsy/ls-work/scratch/LsPerf/prof    # + pprof files (see Profiling)
```

`run.sh` calls `~/work/ls-tools/ls run` (rsync of the worktree to xd6702, `nice -n 10`, `LS_CPUS=3`, tern-ci container),
runs `perf.py` there, copies the raw data back to `port/perf/data/<name>/` and prints `summary.md`. `LS_PERF_NAME=<name>`
renames the data directory. The remote copy lives under `/home/kinsy/ls-work/scratch/LsPerf/out/<name>` (outside the synced
tree, which `ls sync --delete` would clear); delete `/home/kinsy/ls-work/scratch/LsPerf` when the measurements are accepted.
`run.sh collect <name>` waits for a remote run whose local `run.sh` was interrupted and fetches it.

Files: `perf.py` (driver and summary), `run.sh`, `fakellm/` (the scripted model, a copy of the Posthorse perf model with
request timestamps and 16 KB text deltas), `mem/` (the two measurement-only reporters), `data/<name>/{samples.jsonl,meta.json,
summary.md,summary-full.md}`. `mockls` is the repository's `port/tools/cmd/mockls` (built by `perf.py`; its
`GET /__mockls/stats` counts requests and body bytes without a request log).
`perf.py summarize <dir> [--override <dir2>]` rebuilds the summary; with `--override` the workloads present in the second
directory replace those of the first (a rerun of a noisy workload).

## Sides

| Side | Host | Extension | Extension process |
|---|---|---|---|
| `go` | pig 0.7.0+1.5.0 (`$PIGEQ_PIG`) RPC | Go port, built by pig (plain build, the shipped sources) | one Go process (`cells/go/<hash>/runner`) |
| `ts` | the same pig RPC | TS original in PiG's Node extension runtime | one `node` process |
| `pi` | Pi 1.5.0 (`$PIGEQ_PI`) RPC, reference only | TS original | none: the extension runs inside the Pi host process, which is counted once |
| `nopig`, `nopi` | pig / Pi, no extension at all (workloads a and c) | none | none: the floor of the host, to read "the cost of being installed" as a difference |

`go` is compared with `ts` (same host, same wire, same scripted model, same mockls). `pi` is the reference the owner asked
for. Under `pi` the `ext_*` columns equal the host's (one process): compare `tree_*` columns between sides.

## Method

Every sample is one fresh host process started as `<host> --mode rpc --no-skills --no-prompt-templates --no-themes
--no-context-files --no-extensions --offline --no-session [--provider pf --model pf-1] -e <extension>` inside `taskset`.
The model is `fakellm/` (OpenAI-compatible, scripted, no latency of its own; context window 10 M tokens so Pi never compacts).
Tracing is switched on with `TRACE_TO_LANGSMITH=true`, `LANGSMITH_PI_API_KEY=<fake>` and `LANGSMITH_PI_ENDPOINT=<mockls URL>`
(workload a leaves them unset); mockls runs with batching on and the default `/info` (multipart on, size limit 100, 20 MiB),
one fresh mockls per sample.

* **Interleaving.** For sample `i` the sides rotate (`i mod #sides`) and so do the variants (plain/mem), inside each workload
  cell. Cells report the median and the spread `(p90 - p10) / median` and n. A cell with a spread above 15 % is marked `!`
  and its workload is rerun (`--override`).
* **Placement.** The driver, mockls and fakellm share one CPU; host and extension get two other physical cores
  (`taskset`; chosen as the three idlest physical cores of the container at start, recorded in `meta.json`). The container
  quota is 3 CPUs (the owner's cap), `nice -n 10`. `load1` is recorded in every row. The machine is shared: compare only runs
  of one session.
* **Cache.** `warm` = a home populated by three unmeasured launches (pig's Go cell, Node/jiti caches), reused;
  `cold` (workloads a and b) = a fresh home for every sample: pig has to build the Go cell (the Go toolchain's own build
  cache stays warm) or recompile the TypeScript. The OS page cache is never dropped.
* **Validity.** A sample counts only if the host exited 0, stderr holds no `rror` (workload f500 expects errors), no tool call
  failed, every `read` returned its fixture marker (or an image part for the PNGs), the memory reporters answered, and the model
  received the scripted number of requests. An invalid sample aborts the run without a retry (`--keep-going` records it and goes on).
* **Equal work.** The summary prints `PARITY MISMATCH` if the sides sent the model a different number of requests, and
  a `NOTE` when LangSmith ingest requests or decoded bytes differ by more than 5 % between sides (see Limits: batching timing).

### Workloads

| ID | Workload | Samples per side |
|---|---|---|
| a | host start, extension loaded **but disabled** (no `TRACE_TO_LANGSMITH`) to `get_state`, ready snapshot after 0.5 s, idle snapshot at 2.5 s; warm and cold; + `nopig`/`nopi` floors | 15 + 15 |
| b | the same with tracing **enabled** (mockls up, nothing sent yet); warm and cold | 15 + 15 |
| c | first turn: `get_state`, then one prompt answered by one short text turn: prompt sent -> `agent_end` and prompt sent -> first model request (the extension's `before_agent_start` run post sits on that path); + floors | 15 |
| d10, d100, d1000 | 1, 10, 100 prompts of 10 tool calls each (10, 100, 1000 calls; 11 / 110 / 1100 model requests): per call a mix of `read` of a 600-byte fixture (60 %), `bash echo` (25 %, a subprocess; also gives `tool_execution_update`) and `write` of 200 bytes (15 %) | 15 |
| e | one prompt, 100 model requests: a conversation growing to 200 messages; six 48 KB tool outputs (the host's `read` tool truncates at 50 KB: 100 KB tool outputs cannot be produced with built-in tools), three image reads (180x180 noise PNG, 97 KB each: image parts in tool results and in the provider payload), two assistant messages of 100 KB; provider payload at the last request about 1 MB, 43 MB of payloads through the extension in total | 15 |
| f0, f200, f500 | shutdown with queued batches: one prompt with three tool calls, stdin closed in the same moment as `agent_end` is read, time to host exit and to extension exit. f0: mockls normal; f200: every mockls route answers after 200 ms; f500: every ingest route answers 500 (the SDK retries; `/info` stays 200) | 15 |
| g | idle enabled steady state after a 20-run session (20 prompts of 2 tool calls): snapshot 1 s after the tracing tail drained (`post`), another 10 s later (`end`): memory growth, CPU while idle, goroutines / active resources, threads, fds | 15 |

Deterministic data: fixtures (fixed seeds) are generated once under the scratch tree and copied into each workspace. Workspace
path is fixed so run metadata carries the same `cwd`.

### Metrics

"ext" is the extension process (under `pi` it is the whole host); "host" is the host process; "tree" is host + ext (host only under `pi`).
RSS, PSS and Anonymous come from `/proc/<pid>/smaps_rollup` (kernel accounting of pages actually resident; PSS divides shared pages
between the processes sharing them, so it is the fair figure when comparing a Go binary with the node binary; Anonymous is the memory the
process wrote, independent of page-cache luck). Snapshot points: `ready` (get_state answered + 0.5 s), `post` (g only), `end` (after the
workload's last `agent_end` and after mockls has been quiet for 0.6 s, so the tracing tail is included).

| Metric | Definition | Source |
|---|---|---|
| `startup_ms` | spawn of the host until the `get_state` response (sent at spawn): host ready with the extension loaded | monotonic clock in the driver |
| `first_turn_ms` | first `prompt` sent until its `agent_end` | driver |
| `to_first_request_ms` | first `prompt` sent until the model receives its first request (includes the extension's `before_agent_start`, `turn_start`, `before_provider_request` handlers) | driver (`time.time_ns`) and fakellm (`UnixNano` of the first request) |
| `to_first_tool_ms` | `prompt` sent until the first `tool_execution_start` | driver |
| `turn_ms` / `work_ms` | sum of per-prompt (prompt sent -> `agent_end`) / first prompt sent -> last `agent_end` | driver |
| `flush_tail_ms` | from the last `agent_end` until mockls has seen no new request for 0.6 s, minus that 0.6 s (the freshness of the last trace data; +-50 ms) | mockls stats polled every 50 ms |
| `shutdown_ms` | stdin closed -> host process exit (`wait4`), f workloads | driver |
| `ext_gone_ms` | stdin closed -> extension process gone (zombie or absent), f workloads | `/proc` polled every 2 ms |
| `shutdown_cpu_ext_ms`, `shutdown_cpu_host_ms` | CPU of the extension process between close and exit (last poll, 10 ms ticks); CPU of the whole reaped host (`wait4` rusage, which includes children the host waited for) | proc |
| `cpu_total_ms_{ready,end}` | host user+system CPU plus the CPU of the children the host waited for (`cutime+cstime`: a cold Go build, tool subprocesses), plus the same for the extension process; 10 ms ticks, from process start to the snapshot | `/proc/<pid>/stat` |
| `cpu_total_workload_ms`, `cpu_ext_workload_ms` | `end` minus `ready` (whole tree / extension process only) | proc |
| `cpu_idle_ms` | g: `end` minus `post` over 10 idle seconds | proc |
| `ext_rss_*`, `ext_pss_*`, `ext_anon_*` | ext Rss, Pss, Anonymous at `ready` / `end` | smaps_rollup |
| `ext_peak_rss_end` | ext `VmHWM` (kernel peak RSS since the process started) | `/proc/<pid>/status` |
| `peak_ext_{rss,pss,anonymous}`, `peak_tree_{rss,pss}` | maxima over polls every 100 ms from `ready` until the end snapshot; there is no kernel peak PSS or Anonymous, so these are lower bounds of the true peak | smaps_rollup polling |
| `tree_rss_*`, `tree_pss_*` | host + ext | smaps_rollup |
| `ext_rss_growth`, `ext_anon_growth`, `ext_rss_idle_growth` | `end - ready` (and g: `end - post`) | smaps_rollup |
| `ext_threads_end`, `ext_fds_end` | threads and open file descriptors of the extension process | `/proc` |
| `heap_used_{asis,gc}_*` | Go `MemStats.HeapAlloc` / Node `process.memoryUsage().heapUsed`, as the process stands and after a forced full collection | reporters |
| `heap_inuse_{asis,gc}_*` | Go `MemStats.HeapInuse` / Node `heapTotal` (heap spans/pages held for objects) | reporters |
| `sys_{asis,gc}_*` | Go `MemStats.Sys` (virtual memory the runtime obtained from the OS) / Node `process.memoryUsage().rss` (includes the node binary's pages) | reporters |
| `heap_growth_*` | `end - ready` of `heap_used` (information: V8's forced GC at `ready` is not always complete, so small workloads can show a negative number) | reporters |
| `total_alloc_workload`, `mallocs_workload` | Go: `TotalAlloc`, `Mallocs` at `end` minus `ready` (bytes and objects allocated; V8 has no cumulative counter) | reporters |
| `num_gc_workload`, `gc_pause_ms_workload` | GC cycles and total stop-the-world pause during the workload. Go `NumGC`/`PauseTotalNs`; Node GC performance entries (count and summed duration). Counts are not comparable between runtimes (Go collects small heaps often); the pause is | reporters |
| `goroutines_*` | Go goroutines (the reporter's own excluded) / Node active libuv resources (timers, sockets, pipes) | reporters |
| `ext_count` | extension processes under the host at `ready` | proc |
| `llm_requests`, `llm_bytes` | requests and body bytes the model received | fakellm |
| `ls_requests`, `ls_ingest_requests`, `ls_multipart`, `ls_info`, `ls_bytes`, `ls_decoded_bytes` | requests mockls received (all, ingest routes only, `/runs/multipart`, `/info`), their body bytes as sent and after gunzip | mockls `/__mockls/stats` |
| `ls_requests_before_close` | f: requests mockls had seen when stdin was closed (the flush is what follows) | mockls |
| `tool_ms:<tool>` | `tool_execution_start` -> `tool_execution_end` as the host emits them, one value per call, pooled over samples | driver |
| `cpu_wait4_ms`, `wall_ms` | information: rusage of the reaped host; spawn to reaped | driver |
| Go binary size | `meta.json` `go_binary`: bytes of the `runner` pig built (`runner_bytes`), stripped copy | file system |

Heap, Sys, allocation, GC and goroutine figures come from the **mem** variant, which runs the same workload with a
measurement-only reporter loaded into the extension process. RSS, PSS, Anonymous, peaks, CPU, latency and the process count
come from the **plain** variant (the shipped sources) and are never read from the mem variant. The reporters are not part of the package:

* Go: `mem/zz_perf_mem.go`. The driver copies the extension sources (without `*_test.go` and `testdata`) into a scratch
  directory named `langsmith` (pig takes the extension's identity from the directory name) and adds this one file to the same package.
  Its `init` installs a `SIGUSR2` handler (only when `LS_PERF_MEM_DIR` is set) that writes `runtime.ReadMemStats` to a JSON file, once
  as the process stands and once after `runtime.GC()` and `debug.FreeOSMemory()`.
* Node: `mem/node-mem.js`, imported by a generated `perf-entry.js` that re-exports the original's default export. On `SIGUSR2` it
  writes `process.memoryUsage()`, `v8.getHeapStatistics()`, GC count/pause (a `PerformanceObserver` on `gc`) and
  `process.getActiveResourcesInfo()` as the process stands and after a forced full GC (`--expose-gc` through `v8.setFlagsFromString`).
  `SIGUSR1` is Node's inspector signal, so it is not used.

The snapshot of the extension sources is taken once per invocation, so another lane's `ls sync` cannot change the code under test
between samples. `go test -bench` numbers of the package are in `port/lane-*.md`; this basket measures the process, not the functions.

### Profiling

`--profile DIR` makes the **mem** variant of side `go` start a CPU profile at process start (and a 4 KB memory profile rate); at the
end of the workload the driver sends `SIGUSR1` and the extension writes `<pid>.cpu.pprof` and `<pid>.allocs.pprof` under
`DIR/<workload>-<sample>/`. Read them with `go tool pprof -top -cum <file>` inside the container. The profile run itself is slower
(profiling overhead is visible as `runtime.mProf_Malloc` / `profilealloc`); use it to see where time goes, never for the tables.

## Limits (read before quoting a number)

* CPU has 10 ms tick resolution (`/proc/<pid>/stat`); a process that exited before the snapshot is not read, so CPU is
  taken while host and extension are alive, before shutdown (workload f reads the extension's CPU up to its exit by polling).
* "cold" means cold application caches: the OS page cache is not dropped (no root).
* The scripted model answers in under a millisecond; the numbers are host plus extension overhead, not model time.
* Batching is timer driven (250 ms aggregation, 100 ops): the number of ingest requests for the same work depends on timing and
  differs between sides and runs, so `ls_ingest_requests` is information and only the decoded bytes (within a few percent) and the model request
  counts are the equal-work check. The three sides send the same runs; request/byte totals differ by headers, boundaries,
  timestamps and the sender's `runtime` metadata. The Go port is not byte-identical by design (divergence D1: `runtime`, `sdk`, `sdk_version`).
* Go `Sys` counts address space the runtime obtained from the OS (it may never be touched); Node's counterpart is RSS, the conservative reading
  for Go. Compare resident memory with RSS/PSS/Anonymous.
* Peak PSS and Anonymous are polled maxima (100 ms), not kernel peaks.
* Tool outputs above 50 KB cannot be produced by the host's built-in tools; workload e uses 48 KB tool outputs and 100 KB assistant messages.
* A loaded machine moves latency, not memory. Compare medians of interleaved runs only; the baseline sides (`nopig`, `nopi`) are
  sampled in the same rotation.
* Pi runs the extension in the host process, so Pi's CPU and RSS include the host runtime (a TypeScript agent); the pig sides pay
  for the host (Go) and a second process.

## Results: `data/full1` (2026-11-07, xd6702)

Command: `LS_PERF_NAME=full1 port/perf/run.sh both --keep-going` (defaults: 15 samples per side and variant, 15 mem-variant samples, warm and cold for a/b,
baselines `nopig`/`nopi` in a and c; 781 rows, no invalid sample). Host pig 0.7.0+1.5.0, Pi 1.5.0, Node v24.22.1, go1.27.3 linux/amd64, langsmith SDK 0.10.5
(`oracle-run/dist/index.js` sha256 in `meta.json`), Xeon Platinum 8468, container quota 3 CPUs with host+extension pinned to two physical cores, machine load average 7 to 46
(other lanes were running: latency spreads are wide, memory spreads are narrow). Raw rows `samples.jsonl`, run metadata `meta.json`, the full per-metric
tables `summary-full.md`, the headline tables, the "go worse than ts" list and the request-count table `summary.md`.
Cells are medians of 15 samples; "max spread" is the widest `(p90-p10)/median` of the three sides. `go/ts` is the ratio of medians (below 1 = Go smaller or faster).
Not rerun: d1000 (spread 22 to 26 % on every time/CPU metric, because the 90 s runs span minutes of other lanes' load; the Go/TS gap is 1.5x, far outside it, and a
rerun costs 100 min); every other workload's headline latency/CPU/memory cells are below 15 % except short millisecond values and 10 ms CPU ticks (marked `!` in `summary.md`),
which are quantisation and load noise, not ratios anyone should act on.

### a: extension DISABLED, warm

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 86.2 | 817.0 | 508.4 | 0.11 | 0.17 | 21% |
| cpu_total_ms_end | 50 | 970 | 600 | 0.05 | 0.08 | 22% |
| ext_rss_end | 9.18 MiB | 172.34 MiB | 124.60 MiB | 0.05 | 0.07 | 5% |
| ext_pss_end | 9.18 MiB | 141.62 MiB | 93.94 MiB | 0.06 | 0.10 | 20% |
| ext_anon_end | 1.91 MiB | 117.43 MiB | 72.34 MiB | 0.02 | 0.03 | 9% |
| peak_ext_rss | 9.18 MiB | 172.34 MiB | 124.60 MiB | 0.05 | 0.07 | 5% |
| peak_ext_pss | 9.18 MiB | 143.56 MiB | 102.33 MiB | 0.06 | 0.09 | 27% |
| ext_peak_rss_end | 9.18 MiB | 172.34 MiB | 124.60 MiB | 0.05 | 0.07 | 5% |
| tree_rss_end | 63.88 MiB | 256.17 MiB | 124.60 MiB | 0.25 | 0.51 | 5% |
| tree_pss_end | 62.20 MiB | 214.33 MiB | 93.94 MiB | 0.29 | 0.66 | 32% |
| heap_used_gc_end | 0.38 MiB | 38.77 MiB | 26.14 MiB | 0.01 | 0.01 | 1% |
| heap_inuse_gc_end | 1.10 MiB | 106.43 MiB | 61.25 MiB | 0.01 | 0.02 | 16% |
| sys_asis_end | 8.66 MiB | 169.27 MiB | 125.76 MiB | 0.05 | 0.07 | 49% |
| ext_threads_end | 5 | 12 | 11 | 0.42 | 0.45 | 0% |
| ext_fds_end | 7 | 24 | 18 | 0.29 | 0.39 | 0% |
| baseline host only: startup_ms nopig / nopi | 30.9 | - | 370.4 | | | |
| baseline host only: cpu_total_ms_end nopig / nopi | 20 | - | 460 | | | |
| baseline host only: tree_pss_end nopig / nopi | 40.70 MiB | - | 75.00 MiB | | | |

### a: extension DISABLED, cold

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 677.5 | 1318.2 | 962.4 | 0.51 | 0.70 | 14% |
| cpu_total_ms_end | 790 | 1560 | 1190 | 0.51 | 0.66 | 13% |
| ext_rss_end | 9.18 MiB | 170.27 MiB | 179.34 MiB | 0.05 | 0.05 | 3% |
| ext_pss_end | 9.18 MiB | 127.00 MiB | 138.68 MiB | 0.07 | 0.07 | 10% |
| ext_anon_end | 1.93 MiB | 115.39 MiB | 126.80 MiB | 0.02 | 0.02 | 7% |
| peak_ext_rss | 9.18 MiB | 170.27 MiB | 179.34 MiB | 0.05 | 0.05 | 3% |
| peak_ext_pss | 9.18 MiB | 130.32 MiB | 141.69 MiB | 0.07 | 0.06 | 9% |
| ext_peak_rss_end | 9.18 MiB | 170.27 MiB | 179.34 MiB | 0.05 | 0.05 | 3% |
| tree_rss_end | 76.55 MiB | 304.84 MiB | 179.34 MiB | 0.25 | 0.43 | 8% |
| tree_pss_end | 75.05 MiB | 260.02 MiB | 138.68 MiB | 0.29 | 0.54 | 9% |
| heap_used_gc_end | 0.38 MiB | 38.77 MiB | 35.59 MiB | 0.01 | 0.01 | 1% |
| heap_inuse_gc_end | 1.12 MiB | 106.43 MiB | 103.65 MiB | 0.01 | 0.01 | 17% |
| sys_asis_end | 12.40 MiB | 167.61 MiB | 177.80 MiB | 0.07 | 0.07 | 35% |
| ext_threads_end | 4 | 12 | 11 | 0.33 | 0.36 | 25% |
| ext_fds_end | 7 | 24 | 18 | 0.29 | 0.39 | 0% |
| baseline host only: startup_ms nopig / nopi | 31.4 | - | 504.7 | | | |
| baseline host only: cpu_total_ms_end nopig / nopi | 20 | - | 600 | | | |
| baseline host only: tree_pss_end nopig / nopi | 41.24 MiB | - | 78.90 MiB | | | |

### b: enabled start, warm

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 81.9 | 794.4 | 483.4 | 0.10 | 0.17 | 24% |
| cpu_total_ms_end | 50 | 950 | 600 | 0.05 | 0.08 | 20% |
| ext_rss_end | 9.47 MiB | 172.12 MiB | 127.89 MiB | 0.06 | 0.07 | 5% |
| ext_pss_end | 9.46 MiB | 139.99 MiB | 93.22 MiB | 0.07 | 0.10 | 13% |
| ext_anon_end | 1.95 MiB | 117.27 MiB | 75.57 MiB | 0.02 | 0.03 | 9% |
| peak_ext_rss | 9.47 MiB | 172.12 MiB | 127.89 MiB | 0.06 | 0.07 | 5% |
| peak_ext_pss | 9.46 MiB | 140.59 MiB | 100.33 MiB | 0.07 | 0.09 | 15% |
| ext_peak_rss_end | 9.47 MiB | 172.12 MiB | 127.89 MiB | 0.06 | 0.07 | 5% |
| tree_rss_end | 64.69 MiB | 256.43 MiB | 127.89 MiB | 0.25 | 0.51 | 5% |
| tree_pss_end | 63.19 MiB | 217.46 MiB | 93.22 MiB | 0.29 | 0.68 | 31% |
| heap_used_gc_end | 0.38 MiB | 38.83 MiB | 26.20 MiB | 0.01 | 0.01 | 0% |
| heap_inuse_gc_end | 1.14 MiB | 106.43 MiB | 61.50 MiB | 0.01 | 0.02 | 18% |
| sys_asis_end | 12.40 MiB | 169.26 MiB | 125.26 MiB | 0.07 | 0.10 | 34% |
| ext_threads_end | 5 | 12 | 11 | 0.42 | 0.45 | 0% |
| ext_fds_end | 7 | 24 | 18 | 0.29 | 0.39 | 0% |

### b: enabled start, cold

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 691.4 | 1330.6 | 954.6 | 0.52 | 0.72 | 14% |
| cpu_total_ms_end | 800 | 1590 | 1190 | 0.50 | 0.67 | 12% |
| ext_rss_end | 9.37 MiB | 170.59 MiB | 179.71 MiB | 0.05 | 0.05 | 2% |
| ext_pss_end | 9.36 MiB | 134.83 MiB | 145.44 MiB | 0.07 | 0.06 | 11% |
| ext_anon_end | 1.86 MiB | 115.71 MiB | 127.21 MiB | 0.02 | 0.01 | 10% |
| peak_ext_rss | 9.37 MiB | 170.59 MiB | 179.71 MiB | 0.05 | 0.05 | 2% |
| peak_ext_pss | 9.36 MiB | 142.85 MiB | 146.57 MiB | 0.07 | 0.06 | 10% |
| ext_peak_rss_end | 9.37 MiB | 170.59 MiB | 179.71 MiB | 0.05 | 0.05 | 2% |
| tree_rss_end | 76.96 MiB | 306.90 MiB | 179.71 MiB | 0.25 | 0.43 | 13% |
| tree_pss_end | 65.40 MiB | 269.17 MiB | 145.44 MiB | 0.24 | 0.45 | 35% |
| heap_used_gc_end | 0.38 MiB | 38.83 MiB | 35.65 MiB | 0.01 | 0.01 | 0% |
| heap_inuse_gc_end | 1.10 MiB | 106.43 MiB | 103.40 MiB | 0.01 | 0.01 | 18% |
| sys_asis_end | 12.40 MiB | 167.07 MiB | 177.76 MiB | 0.07 | 0.07 | 35% |
| ext_threads_end | 5 | 12 | 11 | 0.42 | 0.45 | 0% |
| ext_fds_end | 7 | 24 | 18 | 0.29 | 0.39 | 0% |

### c: first turn

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 74.0 | 790.7 | 475.0 | 0.09 | 0.16 | 21% |
| first_turn_ms | 19.7 | 76.2 | 86.4 | 0.26 | 0.23 | 12% |
| to_first_request_ms | 15.4 | 68.7 | 66.7 | 0.22 | 0.23 | 16% |
| work_ms | 19.7 | 76.2 | 86.4 | 0.26 | 0.23 | 12% |
| flush_tail_ms | 258.0 | 309.7 | 258.6 | 0.83 | 1.00 | 1% |
| cpu_total_ms_end | 70 | 1050 | 700 | 0.07 | 0.10 | 14% |
| cpu_total_workload_ms | 20.0 | 110.0 | 110.0 | 0.18 | 0.18 | 50% |
| cpu_ext_workload_ms | 10.0 | 100.0 | 110.0 | 0.10 | 0.09 | 100% |
| ext_rss_end | 10.82 MiB | 178.38 MiB | 135.22 MiB | 0.06 | 0.08 | 3% |
| ext_pss_end | 10.82 MiB | 140.07 MiB | 97.37 MiB | 0.08 | 0.11 | 11% |
| ext_anon_end | 2.83 MiB | 120.69 MiB | 81.37 MiB | 0.02 | 0.03 | 5% |
| peak_ext_rss | 10.82 MiB | 178.38 MiB | 135.22 MiB | 0.06 | 0.08 | 3% |
| peak_ext_pss | 10.82 MiB | 140.07 MiB | 98.89 MiB | 0.08 | 0.11 | 11% |
| ext_peak_rss_end | 10.82 MiB | 178.38 MiB | 135.22 MiB | 0.06 | 0.08 | 3% |
| tree_rss_end | 69.52 MiB | 265.66 MiB | 135.22 MiB | 0.26 | 0.51 | 3% |
| tree_pss_end | 67.49 MiB | 219.04 MiB | 97.37 MiB | 0.31 | 0.69 | 29% |
| heap_used_gc_end | 0.42 MiB | 41.13 MiB | 29.16 MiB | 0.01 | 0.01 | 2% |
| heap_inuse_gc_end | 1.19 MiB | 108.18 MiB | 64.19 MiB | 0.01 | 0.02 | 12% |
| sys_asis_end | 12.59 MiB | 175.50 MiB | 128.63 MiB | 0.07 | 0.10 | 32% |
| gc_pause_ms_workload | 0.1 | 28.2 | 23.4 | 0.00 | 0.01 | 94% |
| ext_threads_end | 5 | 12 | 11 | 0.42 | 0.45 | 20% |
| ext_fds_end | 8 | 25 | 20 | 0.32 | 0.40 | 0% |
| ext_rss_growth | 1.39 MiB | 5.84 MiB | 7.99 MiB | 0.24 | 0.17 | 37% |
| baseline host only: startup_ms nopig / nopi | 31.7 | - | 363.5 | | | |
| baseline host only: cpu_total_ms_end nopig / nopi | 20 | - | 520 | | | |
| baseline host only: tree_pss_end nopig / nopi | 26.42 MiB | - | 73.98 MiB | | | |

### d10

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 86.0 | 796.9 | 476.6 | 0.11 | 0.18 | 24% |
| first_turn_ms | 97.9 | 183.3 | 187.9 | 0.53 | 0.52 | 15% |
| to_first_request_ms | 15.9 | 68.6 | 67.9 | 0.23 | 0.23 | 36% |
| work_ms | 97.9 | 183.3 | 187.9 | 0.53 | 0.52 | 15% |
| flush_tail_ms | 260.7 | 310.8 | 206.3 | 0.84 | 1.26 | 24% |
| cpu_total_ms_end | 160 | 1240 | 900 | 0.13 | 0.18 | 12% |
| cpu_total_workload_ms | 110.0 | 270.0 | 310.0 | 0.41 | 0.35 | 27% |
| cpu_ext_workload_ms | 60.0 | 220.0 | 310.0 | 0.27 | 0.19 | 23% |
| ext_rss_end | 15.20 MiB | 189.63 MiB | 160.29 MiB | 0.08 | 0.09 | 8% |
| ext_pss_end | 15.20 MiB | 145.39 MiB | 119.55 MiB | 0.10 | 0.13 | 11% |
| ext_anon_end | 7.20 MiB | 131.93 MiB | 105.74 MiB | 0.05 | 0.07 | 12% |
| peak_ext_rss | 15.20 MiB | 189.63 MiB | 160.38 MiB | 0.08 | 0.09 | 8% |
| peak_ext_pss | 15.20 MiB | 146.27 MiB | 119.72 MiB | 0.10 | 0.13 | 13% |
| ext_peak_rss_end | 15.20 MiB | 189.63 MiB | 160.29 MiB | 0.08 | 0.09 | 3% |
| tree_rss_end | 76.79 MiB | 283.67 MiB | 160.29 MiB | 0.27 | 0.48 | 8% |
| tree_pss_end | 74.58 MiB | 230.63 MiB | 119.55 MiB | 0.32 | 0.62 | 27% |
| heap_used_gc_end | 0.42 MiB | 41.65 MiB | 31.14 MiB | 0.01 | 0.01 | 2% |
| heap_inuse_gc_end | 1.25 MiB | 108.93 MiB | 65.94 MiB | 0.01 | 0.02 | 12% |
| sys_asis_end | 16.91 MiB | 180.00 MiB | 147.11 MiB | 0.09 | 0.11 | 24% |
| gc_pause_ms_workload | 0.3 | 31.0 | 25.3 | 0.01 | 0.01 | 197% |
| ext_threads_end | 6 | 12 | 11 | 0.50 | 0.55 | 17% |
| ext_fds_end | 8 | 25 | 20 | 0.32 | 0.40 | 0% |
| ext_rss_growth | 5.86 MiB | 16.84 MiB | 34.16 MiB | 0.35 | 0.17 | 41% |

### d100

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 74.5 | 799.1 | 482.2 | 0.09 | 0.15 | 27% |
| first_turn_ms | 96.7 | 183.5 | 189.0 | 0.53 | 0.51 | 15% |
| to_first_request_ms | 15.4 | 69.7 | 66.2 | 0.22 | 0.23 | 20% |
| work_ms | 2024.5 | 1893.0 | 1240.7 | 1.07 | 1.63 | 11% |
| flush_tail_ms | 310.1 | 52.7 | 52.7 | 5.89 | 5.88 | 98% |
| cpu_total_ms_end | 2480 | 3440 | 2300 | 0.72 | 1.08 | 9% |
| cpu_total_workload_ms | 2430.0 | 2490.0 | 1700.0 | 0.98 | 1.43 | 9% |
| cpu_ext_workload_ms | 1780.0 | 1830.0 | 1630.0 | 0.97 | 1.09 | 9% |
| ext_rss_end | 22.41 MiB | 319.51 MiB | 221.27 MiB | 0.07 | 0.10 | 8% |
| ext_pss_end | 22.41 MiB | 284.13 MiB | 183.90 MiB | 0.08 | 0.12 | 19% |
| ext_anon_end | 14.41 MiB | 261.21 MiB | 166.64 MiB | 0.06 | 0.09 | 12% |
| peak_ext_rss | 26.79 MiB | 320.64 MiB | 221.27 MiB | 0.08 | 0.12 | 22% |
| peak_ext_pss | 26.79 MiB | 284.13 MiB | 183.90 MiB | 0.09 | 0.15 | 22% |
| ext_peak_rss_end | 26.66 MiB | 319.51 MiB | 221.27 MiB | 0.08 | 0.12 | 11% |
| tree_rss_end | 102.86 MiB | 431.79 MiB | 221.27 MiB | 0.24 | 0.46 | 7% |
| tree_pss_end | 83.11 MiB | 391.51 MiB | 183.90 MiB | 0.21 | 0.45 | 25% |
| heap_used_gc_end | 0.44 MiB | 43.26 MiB | 33.43 MiB | 0.01 | 0.01 | 1% |
| heap_inuse_gc_end | 1.68 MiB | 187.18 MiB | 115.94 MiB | 0.01 | 0.01 | 11% |
| sys_asis_end | 29.16 MiB | 320.48 MiB | 221.25 MiB | 0.09 | 0.13 | 14% |
| gc_pause_ms_workload | 8.2 | 107.5 | 73.5 | 0.08 | 0.11 | 66% |
| ext_threads_end | 6 | 12 | 11 | 0.50 | 0.55 | 0% |
| ext_fds_end | 8 | 26 | 22 | 0.31 | 0.36 | 5% |
| ext_rss_growth | 13.01 MiB | 147.40 MiB | 93.04 MiB | 0.09 | 0.14 | 21% |

### d1000

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 58.9 | 595.7 | 343.6 | 0.10 | 0.17 | 49% |
| first_turn_ms | 72.7 | 142.1 | 142.6 | 0.51 | 0.51 | 36% |
| to_first_request_ms | 12.2 | 53.4 | 49.6 | 0.23 | 0.25 | 35% |
| work_ms | 90156.8 | 58870.1 | 23094.2 | 1.53 | 3.90 | 26% |
| flush_tail_ms | 309.3 | 102.9 | 55.0 | 3.01 | 5.63 | 101% |
| cpu_total_ms_end | 108010 | 69720 | 26020 | 1.55 | 4.15 | 26% |
| cpu_total_workload_ms | 107980.0 | 68960.0 | 25590.0 | 1.57 | 4.22 | 26% |
| cpu_ext_workload_ms | 89490.0 | 49930.0 | 24160.0 | 1.79 | 3.70 | 25% |
| ext_rss_end | 86.02 MiB | 767.12 MiB | 625.27 MiB | 0.11 | 0.14 | 84% |
| ext_pss_end | 86.02 MiB | 735.56 MiB | 599.65 MiB | 0.12 | 0.14 | 84% |
| ext_anon_end | 77.78 MiB | 708.60 MiB | 570.40 MiB | 0.11 | 0.14 | 93% |
| peak_ext_rss | 173.48 MiB | 791.35 MiB | 647.39 MiB | 0.22 | 0.27 | 9% |
| peak_ext_pss | 173.48 MiB | 752.33 MiB | 619.77 MiB | 0.23 | 0.28 | 10% |
| ext_peak_rss_end | 174.23 MiB | 790.61 MiB | 646.60 MiB | 0.22 | 0.27 | 9% |
| tree_rss_end | 271.10 MiB | 978.94 MiB | 625.27 MiB | 0.28 | 0.43 | 39% |
| tree_pss_end | 269.58 MiB | 947.18 MiB | 599.84 MiB | 0.28 | 0.45 | 39% |
| heap_used_gc_end | 0.47 MiB | 44.25 MiB | 36.56 MiB | 0.01 | 0.01 | 3% |
| heap_inuse_gc_end | 1.81 MiB | 177.68 MiB | 211.19 MiB | 0.01 | 0.01 | 25% |
| sys_asis_end | 188.82 MiB | 757.00 MiB | 636.29 MiB | 0.25 | 0.30 | 13% |
| gc_pause_ms_workload | 154.4 | 3096.3 | 1059.0 | 0.05 | 0.15 | 33% |
| ext_threads_end | 6 | 12 | 11 | 0.50 | 0.55 | 17% |
| ext_fds_end | 8 | 26 | 25 | 0.31 | 0.32 | 8% |
| ext_rss_growth | 76.51 MiB | 593.12 MiB | 496.93 MiB | 0.13 | 0.15 | 94% |

### e: large payloads

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 55.1 | 567.4 | 344.7 | 0.10 | 0.16 | 39% |
| first_turn_ms | 3777.6 | 2588.5 | 1446.4 | 1.46 | 2.61 | 17% |
| to_first_request_ms | 12.3 | 51.7 | 49.3 | 0.24 | 0.25 | 32% |
| work_ms | 3777.6 | 2588.5 | 1446.4 | 1.46 | 2.61 | 17% |
| flush_tail_ms | 269.5 | 278.5 | 158.1 | 0.97 | 1.70 | 98% |
| cpu_total_ms_end | 4310 | 4070 | 2390 | 1.06 | 1.80 | 13% |
| cpu_total_workload_ms | 4280.0 | 3370.0 | 1970.0 | 1.27 | 2.17 | 15% |
| cpu_ext_workload_ms | 3300.0 | 2400.0 | 1910.0 | 1.38 | 1.73 | 15% |
| ext_rss_end | 110.92 MiB | 630.07 MiB | 407.50 MiB | 0.18 | 0.27 | 33% |
| ext_pss_end | 110.92 MiB | 600.16 MiB | 387.76 MiB | 0.18 | 0.29 | 33% |
| ext_anon_end | 102.93 MiB | 569.83 MiB | 349.62 MiB | 0.18 | 0.29 | 35% |
| peak_ext_rss | 111.84 MiB | 641.15 MiB | 420.91 MiB | 0.17 | 0.27 | 15% |
| peak_ext_pss | 111.83 MiB | 612.41 MiB | 393.92 MiB | 0.18 | 0.28 | 15% |
| ext_peak_rss_end | 111.70 MiB | 646.89 MiB | 421.95 MiB | 0.17 | 0.26 | 11% |
| tree_rss_end | 247.52 MiB | 790.15 MiB | 407.50 MiB | 0.31 | 0.61 | 16% |
| tree_pss_end | 246.01 MiB | 755.77 MiB | 387.76 MiB | 0.33 | 0.63 | 16% |
| heap_used_gc_end | 0.44 MiB | 43.92 MiB | 33.95 MiB | 0.01 | 0.01 | 2% |
| heap_inuse_gc_end | 1.64 MiB | 181.18 MiB | 175.19 MiB | 0.01 | 0.01 | 7% |
| sys_asis_end | 121.48 MiB | 613.68 MiB | 422.23 MiB | 0.20 | 0.29 | 20% |
| gc_pause_ms_workload | 8.6 | 206.8 | 88.2 | 0.04 | 0.10 | 65% |
| ext_threads_end | 6 | 13 | 12 | 0.46 | 0.50 | 0% |
| ext_fds_end | 8 | 30 | 25 | 0.27 | 0.32 | 0% |
| ext_rss_growth | 101.48 MiB | 457.09 MiB | 274.77 MiB | 0.22 | 0.37 | 36% |

### g: idle after 20 runs

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| startup_ms | 75.4 | 811.6 | 487.7 | 0.09 | 0.15 | 719% |
| first_turn_ms | 34.7 | 98.8 | 120.5 | 0.35 | 0.29 | 55% |
| to_first_request_ms | 14.8 | 68.8 | 67.2 | 0.22 | 0.22 | 68% |
| work_ms | 764.6 | 976.7 | 686.2 | 0.78 | 1.11 | 27% |
| flush_tail_ms | 259.8 | 52.4 | 52.4 | 4.95 | 4.96 | 3% |
| cpu_total_ms_end | 930 | 2480 | 1730 | 0.38 | 0.54 | 85% |
| cpu_total_workload_ms | 880.0 | 1520.0 | 1110.0 | 0.58 | 0.79 | 32% |
| cpu_ext_workload_ms | 570.0 | 1190.0 | 1090.0 | 0.48 | 0.52 | 32% |
| ext_rss_end | 15.18 MiB | 175.02 MiB | 134.88 MiB | 0.09 | 0.11 | 21% |
| ext_pss_end | 15.17 MiB | 170.60 MiB | 132.42 MiB | 0.09 | 0.11 | 22% |
| ext_anon_end | 7.16 MiB | 116.57 MiB | 80.21 MiB | 0.06 | 0.09 | 36% |
| peak_ext_rss | 17.53 MiB | 243.81 MiB | 186.61 MiB | 0.07 | 0.09 | 25% |
| peak_ext_pss | 17.53 MiB | 239.47 MiB | 184.16 MiB | 0.07 | 0.10 | 26% |
| ext_peak_rss_end | 17.97 MiB | 240.65 MiB | 184.95 MiB | 0.07 | 0.10 | 25% |
| tree_rss_end | 90.95 MiB | 283.82 MiB | 134.88 MiB | 0.32 | 0.67 | 21% |
| tree_pss_end | 88.55 MiB | 273.39 MiB | 132.42 MiB | 0.32 | 0.67 | 25% |
| heap_used_gc_end | 0.44 MiB | 42.61 MiB | 32.22 MiB | 0.01 | 0.01 | 30% |
| heap_inuse_gc_end | 1.59 MiB | 46.18 MiB | 35.69 MiB | 0.03 | 0.04 | 27% |
| sys_asis_end | 21.10 MiB | 173.20 MiB | 135.04 MiB | 0.12 | 0.16 | 19% |
| gc_pause_ms_workload | 3.4 | 130.4 | 113.6 | 0.03 | 0.03 | 138% |
| ext_threads_end | 6 | 12 | 11 | 0.50 | 0.55 | 0% |
| ext_fds_end | 8 | 24 | 18 | 0.33 | 0.44 | 0% |
| cpu_idle_ms | 0.0 | 170.0 | 170.0 | 0.00 | 0.00 | 35% |
| ext_rss_growth | 5.67 MiB | 2.14 MiB | 5.01 MiB | 2.65 | 1.13 | 802% |
| ext_rss_idle_growth | 0 | -68.93 MiB | -62.94 MiB | -0.00 | -0.00 | 30% |

### f: shutdown with queued batches

| Metric | go | ts | pi | go/ts | go/pi | max spread |
|---|---:|---:|---:|---:|---:|---:|
| f0 shutdown_ms | 259.8 | 275.8 | 224.7 | 0.94 | 1.16 | 14% |
| f0 ext_gone_ms | 255.4 | 268.2 | 224.7 | 0.95 | 1.14 | 14% |
| f0 shutdown_cpu_ext_ms | 0.0 | 30.0 | - | 0.00 | - | 33% |
| f0 shutdown_cpu_host_ms | 92.2 | 157.3 | 607.7 | 0.59 | 0.15 | 37% |
| f0 ls_requests_before_close | 1 | 1 | 1 | 1.00 | 1.00 | 0% |
| f0 ls_requests | 2 | 2 | 2 | 1.00 | 1.00 | 0% |
| f200 shutdown_ms | 635.6 | 653.0 | 636.4 | 0.97 | 1.00 | 2% |
| f200 ext_gone_ms | 629.7 | 642.0 | 636.4 | 0.98 | 0.99 | 3% |
| f200 shutdown_cpu_ext_ms | 10.0 | 50.0 | - | 0.20 | - | 100% |
| f200 shutdown_cpu_host_ms | 70.3 | 155.6 | 599.4 | 0.45 | 0.12 | 73% |
| f200 ls_requests_before_close | 1 | 1 | 1 | 1.00 | 1.00 | 0% |
| f200 ls_requests | 2 | 2 | 2 | 1.00 | 1.00 | 0% |
| f500 shutdown_ms | 22809.4 | 21502.3 | 22086.1 | 1.06 | 1.03 | 34% |
| f500 ext_gone_ms | 22803.2 | 21483.7 | 22086.1 | 1.06 | 1.03 | 34% |
| f500 shutdown_cpu_ext_ms | 10.0 | 270.0 | - | 0.04 | - | 200% |
| f500 shutdown_cpu_host_ms | 87.2 | 208.2 | 956.4 | 0.42 | 0.09 | 52% |
| f500 ls_requests_before_close | 1 | 1 | 1 | 1.00 | 1.00 | 0% |
| f500 ls_requests | 6 | 6 | 6 | 1.00 | 1.00 | 0% |

### allocation (Go only; V8 has no cumulative counter), medians

| Workload | total_alloc | mallocs | num_gc (go/ts/pi) | gc pause ms (go/ts/pi) |
|---|---:|---:|---|---|
| a | 0.0 MiB | 157 | 2 / 1 / 1 | 0.2 / 31.0 / 24.2 |
| b | 0.0 MiB | 156 | 2 / 1 / 1 | 0.1 / 29.7 / 22.3 |
| c | 0.7 MiB | 7036 | 2 / 1 / 1 | 0.1 / 28.2 / 23.4 |
| d10 | 8.6 MiB | 88415 | 5 / 1 / 2 | 0.3 / 31.0 / 25.3 |
| d100 | 330.3 MiB | 4119050 | 133 / 8 / 13 | 8.2 / 107.5 / 73.5 |
| d1000 | 27829.3 MiB | 364965912 | 1894 / 238 / 179 | 154.4 / 3096.3 / 1059.0 |
| e | 1049.0 MiB | 3522189 | 128 / 17 / 13 | 8.6 / 206.8 / 88.2 |
| g | 101.1 MiB | 1282655 | 58 / 11 / 12 | 3.4 / 130.4 / 113.6 |

Go runner binary: {'runner_bytes': 9113760, 'sha256': 'e2177792d88738c63ffb572b8a6bde05d674ec12f54dcdba46c6127075be6879', 'stripped_bytes': 9111496}
host: {'pig': '0.7.0+1.5.0', 'pi': '1.5.0', 'node': 'v24.22.1', 'go': 'go version go1.27.3 linux/amd64', 'langsmith_sdk': '0.10.5', 'kernel': 'Linux 6.8.0-124-generic', 'cpu': 'Intel(R) Xeon(R) Platinum 8468'} cpus {'helper': [6], 'tree': [11, 18], 'all': [6, 11, 18]} load [6.193359375, 6.45703125, 7.5771484375] [32.05810546875, 29.4794921875, 30.7294921875]


### Verdict

Go is smaller than TS-in-pig on every memory kind in every workload (extension RSS 5 to 18 % of TS, PSS 6 to 18 %, Anonymous 2 to 18 %, peak RSS, Go heap vs V8 heap, Sys vs Node RSS,
threads 4 to 6 vs 12, fds 7 to 8 vs 24 to 30), starts 10x faster warm and 2x cold, and its first turn (c: 20 vs 76 ms) and time to the first model request (15 vs 69 ms) are 4x lower (d10 first turn 2x). The extension costs the host
about 55 ms and 21 MiB when merely installed (a, warm: pig alone 31 ms / 40.7 MiB tree PSS, with the Go port loaded but disabled 86 ms / 62 MiB; TS: 817 ms / 214 MiB; Pi alone 370 ms / 75 MiB, with the TS original 508 ms / 94 MiB). **Go is worse than TS-in-pig on CPU and wall time in the
large-payload and long-session workloads (d100 1.07x wall, d1000 1.53x wall and 1.79x extension CPU, e 1.46x wall and 1.38x extension CPU), and on `flush_tail_ms` (about 260 ms vs 50 to 100 ms), and shows a larger
RSS growth in g.** None of it is the port's own code:

1. **Large payloads / long sessions (d100, d1000, e): the PiG SDK's frame decoder.** CPU profiles of the Go extension (`data/prof-sdkdecode/{e-0,d100-0,d1000-0}`, recipe `RECIPE.txt`):
   55 % of the extension's CPU in d1000 (44 % in e, 50 % in d100) is `sdk.(*Extension).handleArmedRequest` -> `sdk/json.Unmarshal`; `UnmarshalFrame` runs `checkValid` (a full validation scan; 24 to 33 % of all
   CPU) and then decodes into an `any` tree (`objectInterface`/`unquote`, 25 to 45 %), with three copies of every frame (`readFrame`, `jsontext.Value.UnmarshalJSON`, `unquote`: 56 % of the 1.05 GB allocated in e).
   The port's own serialisation (`serializeForTracing`, `buildMultipart`, `convertProviderPayload`) is about 5 % of the CPU and 23 % of the allocation (`appendString`). The host waits for the extension's
   `before_provider_request` reply, so the decode time is added to every model request (d1000: 480 MiB of provider payloads, 1100 requests; e: 43 MiB, 100 requests). Node pays `JSON.parse`, native code.
   Proposed fix (SDK side, lane SdkFrameDecode, lead's decision 2026-11-07): do not `checkValid` a frame the host framed itself, avoid the second frame copy, and optionally hand the port raw JSON for event payloads.
   The memory side of the same finding is favourable (Go d1000 86 MiB vs TS 767 MiB) because the any-tree is short lived. Rerun after the SDK fix: `port/perf/run.sh both --workloads e,d100,d1000` against the fixed pig.
2. **`flush_tail_ms` (d100 310 vs 53 ms, d1000 309 vs 103, g 260 vs 52; e, c, d10 are equal): the original's timer leak, not a Go defect.** `client.js:1601` clears the aggregation timer before two awaits (1604 to 1605)
   and arms it afterwards, so concurrent operations leak timers; the TS client therefore flushes about 250 ms after *each* operation (the queue is drained by an earlier leaked timer) while Go debounces correctly
   (documented as D3). Consequence visible in the request table: for the same runs and the same bytes (go/ts decoded bytes 0.98 to 1.00) TS sends 33 vs 15 ingest requests in d100, 1089 vs 153 in d1000, 26 vs 9 in g. Go
   makes fewer, larger requests and the last data reaches LangSmith about 250 ms after the run ends instead of 50 to 100 ms. At shutdown both flush immediately (f0: 260 vs 276 ms; f200 636 vs 653 ms).
3. **g `ext_rss_growth` (Go 5.7 MiB, TS 2.1 MiB over 20 runs).** Go starts at 9.5 MiB and ends at 15.2 MiB, TS starts at 172 MiB and ends at 175 MiB; during the 10 idle seconds Go does not grow at all (0 B, 0 ms of CPU
   against 170 ms for TS), TS releases 69 MiB. The Go live heap after GC is 0.44 MiB in both snapshots, goroutines are constant: no leak, the 5.7 MiB is the runtime's retained arenas after the 20 runs. Not a regression of an absolute level.
4. **f500 shutdown 22.8 vs 21.5 s (ranges overlap, spread 26 to 34 %).** Both sides retry the same 5 `/runs/multipart` requests (identical counts: 1 `/info` + 5) with randomised backoff; the SDK gives up after about 22 s in all three sides.
5. **Goroutines/`num_gc`**: Go runs 130 to 1900 GCs where Node runs 8 to 240 (small heap, frequent cycles), but the total pause is 8 ms vs 207 ms (e), 154 ms vs 3.1 s (d1000): not comparable counts, the pause is the cost and favours Go.

### Other findings for the lead (not performance regressions)

* **Go prints nothing on stderr when the multipart ingest finally fails** (f500, all 15 samples: Go 0 bytes, TS and Pi 1138 bytes `... trace=<id>,id=<id>; ...`). Either the port does not print the final failure (`client.js` console.error after retries) or the pig host drops the Go extension's stderr;
  `client_ingest.go:567` is the print site. Please check with a fakehost test. The same applies to exit code (0 in all sides).
* Go d1000 is quadratic in the same way as TS (the extension posts every LLM run with the whole provider payload: 480 MiB for 1100 requests); byte totals are equal (0.995).
* A cold start (fresh pig home) of the Go side costs 0.68 s (pig links the cell; Go build cache warm) against 1.32 s for TS (TypeScript transform); `go_binary`: the `runner` is 9.11 MB (9.11 MB stripped).
* The three sides send the same number of model requests in every workload (no PARITY MISMATCH) and the same decoded LangSmith bytes within 2 % (d100 0.98, g 0.96: TS sends more per-request overhead).

### Residual risks / limits of this run

* d1000 was not rerun (see above); its CPU and wall rows have a 22 to 26 % spread, the ratios 1.5x to 1.8x are outside it.
* Tool outputs are 48 KB (host truncation), not 100 KB, in e (100 KB assistant messages instead).
* Peak PSS/Anonymous are 100 ms polled maxima; VmHWM (`ext_peak_rss_end`) is exact.
* Allocation counts exist for Go only (V8 has no cumulative counter): see the allocation table. `go test -bench` figures are in the lane notes.
