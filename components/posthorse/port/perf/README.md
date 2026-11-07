# Posthorse performance basket

Measures the Go port (the extension in `extensions/posthorse`, run by PiG) against the TypeScript original
(`port/oracle/index.ts`) on the owner's basket: CPU time, RSS, PSS, heap in use, Go runtime Sys, startup, first-turn
latency, per-tool-call latency, allocations, extension process count and binary size. Everything is hermetic and offline:
a fresh `HOME`/`PIG_HOME`/agent directory per run, a scripted model on loopback, no network, never the real `~/.pig`,
`~/.pi` or `auth.json`.

## One command

```sh
port/perf/run.sh both            # go, ts and pi interleaved sample by sample: the authoritative ratchet comparison
port/perf/run.sh go              # the Go port only (compare with data/ts-baseline, which is an earlier session)
port/perf/run.sh ts              # the TS original under pig (side "ts") and under Pi 1.5.0 (side "pi")
port/perf/run.sh both --samples 15 --workloads a,b,e     # any `perf.py run` flag follows the mode
```

`run.sh` calls `~/work/ph-tools/ph run` (rsync of the Mac worktree to xd6702, `nice -n 10`, 8 CPUs, tern-ci container),
runs `perf.py` there, copies the raw data back to `port/perf/data/<go|both|ts-baseline>/` and prints `summary.md`.
`PH_PERF_NAME=<name>` renames the data directory. The remote copy lives under
`/home/kinsy/ph-work/scratch/PerfLane/out/<name>` (outside the synced tree, which `ph sync --delete` would clear);
delete `/home/kinsy/ph-work/scratch/PerfLane` when the measurements are accepted.

Files: `perf.py` (driver and summary), `run.sh`, `fakellm/` (the scripted model), `mem/` (the two measurement-only
reporters), `data/<name>/{samples.jsonl,meta.json,summary.md,summary-full.md}`.
`perf.py summarize <dir> [--vs <dir2>]` rebuilds the summary; with `--vs` the rows of the second directory join the
first (for example `go` against `ts-baseline`); a miss on the ratchet makes the exit status 1.

## Sides

| Side | Host | Extension | Extension process |
|---|---|---|---|
| `go` | pig 0.7.0+1.5.0 (`$PIGEQ_PIG`, PiG 979bf718f7) RPC | Go port, built by pig | one Go process (`cells/go/<hash>/runner`) |
| `ts` | the same pig RPC | TS original in PiG's Node extension runtime | one `node --import ...` process |
| `pi` | Pi 1.5.0 (`$PIGEQ_PI`) RPC, reference only | TS original | none: the extension runs inside the Pi host process, which is counted once |

The ratchet compares `go` with `ts` (same host, same wire, same scripted model). `pi` is the reference the owner asked
for; it is not part of the pass/fail rule.

## Method

Every sample is one fresh host process (workload f: 60 of them) started as `<host> --mode rpc --no-skills
--no-prompt-templates --no-themes --no-context-files --no-extensions --offline [--provider pf --model pf-1]
[--session-dir D | --no-session] -e <extension>`. The scripted model is `fakellm/` (the loopback model of `pigeq llm`,
plus usage that follows the request size, so Pi's compaction threshold, which Posthorse hooks for its rollover, fires from
real transcript growth; the model has no latency of its own). The workspace is a git repository at a fixed path so that
archive headers carry the same `cwd` in every run.

* **Interleaving.** For sample `i` the sides rotate (`i mod #sides`) and so do the variants, inside each workload cell.
* **Samples.** 15 per cell by default (`--samples`). Cells report median and p90 (`ceil(0.9*(n-1))`-th order statistic)
  and n. Tool-call latencies pool all calls of all samples of the cell.
* **Cache.** `warm` = a home populated by three unmeasured launches (pig's cell and jiti caches, Pi's jiti cache), reused;
  `cold` (workloads a and b) = a fresh home for every sample: pig has to build the Go cell (the Go toolchain's own build
  cache stays warm) or transform the TypeScript. The OS page cache is never dropped.
* **Priority and placement.** `nice -n 10` (by `ph run`), pinned to six CPUs of the container's affinity set (`--cpus`
  or `PH_PERF_CPUS`; default the last six), `load1` recorded in every row and `loadavg_start/end` in `meta.json`. The
  machine is shared: compare only runs of one session; `run.sh both` is built for that.
* **Validity.** A sample counts only if the host exited 0, stderr holds no `rror`, no tool call failed, the extension
  process exists (pig sides), the planted needles appear in the search results, and the memory reporters answered. An
  invalid sample aborts the run without a retry (`--keep-going` records it and goes on). A table cell also shows the
  parity of model requests and session records across sides.

### Metrics

"ext" is the extension process (the sum over extension processes when there are several); "host" is the host process.
Memory is read from `/proc/<pid>/smaps_rollup` (as `internal/perfgate/procmem_linux.go` does) at two points: `ready`
(`get_state` answered plus 0.5 s idle) and `end` (after the workload's last `agent_end` settled, before shutdown).

| Metric | Definition | Source |
|---|---|---|
| `startup_ms` | spawn of the host until the `get_state` response (sent at spawn, as `startup_timing.py` sends `get_commands`): the host is ready with the extension loaded | monotonic clock in the driver |
| `first_turn_ms` (workload b) | `prompt` sent until `agent_end`, on a fresh host whose model calls `get_context_remaining` once then answers | driver |
| `to_first_tool_ms` | `prompt` sent until the first `tool_execution_start` | driver |
| `tool_ms:<tool.op>` | `tool_execution_start` to `tool_execution_end` as the host emits them; one value per call, pooled | driver |
| `turn_ms` | sum over prompts of `prompt` sent until the final `agent_end` (an `agent_start` within 0.4 s means the run continues) | driver |
| `cpu_ext_workload_ms` | ext user+system CPU at `end` minus at `ready` (10 ms ticks, `/proc/<pid>/stat`) | proc |
| `ext_cpu_ms_end` / `_ready` | ext cumulative CPU (load plus workload), 10 ms ticks | proc |
| `cpu_total_ms_end` / `_ready` | host user+system CPU plus the CPU of the children the host waited for (`cutime+cstime`: a cold Go build) plus the same for the extension process (git run by the extension); host only under `pi`. 10 ms ticks, whole run up to the snapshot | proc |
| `cpu_total_workload_ms` | `cpu_total_ms_end` minus `cpu_total_ms_ready` | proc |
| `cpu_wait4_ms` (information) | `wait4` rusage of the host: it does not include the extension process (pig does not reap it before exiting), so it is not compared | `os.wait4` |
| `ext_rss_*`, `ext_pss_*`, `ext_anon_*` | ext Rss, Pss, Anonymous (the bytes the process wrote; independent of page-cache luck, the gated figure of `process_test.go`) | smaps_rollup |
| `ext_peak_rss_*` | ext `VmHWM` | `/proc/<pid>/status` |
| `tree_rss_*`, `tree_pss_*` | host + ext (host only for `pi`) | smaps_rollup |
| `heap_used_{asis,gc}_*` | Go `MemStats.HeapAlloc` / Node `process.memoryUsage().heapUsed`, as the process stands and after a forced full collection | reporters (below) |
| `heap_inuse_{asis,gc}_*` | Go `MemStats.HeapInuse` / Node `heapTotal` (heap spans/pages held for objects) | reporters |
| `sys_{asis,gc}_*` | Go `MemStats.Sys` / Node `process.memoryUsage().rss` (the Node figure includes the node binary's pages: the conservative reading for Go) | reporters |
| `total_alloc_workload`, `mallocs_workload`, `num_gc_workload` | Go: `TotalAlloc`, `Mallocs`, `NumGC` at `end` minus `ready`. Node: only the number of GC events (V8 has no cumulative allocation counter) | reporters |
| `ext_count` | extension processes under the host at `ready` | proc |
| `ext_avg_rss_end`, `ext_avg_pss_end`, `all_ready_ms` (workload f) | per-session average and the time to bring 60 sessions up | proc |
| Go binary size | `meta.json` `go_binary`: bytes of the `runner` pig built (`runner_bytes`), its stripped copy (`stripped_bytes`), sha256 | file system |

Heap, Sys and allocation figures come from the **mem** variant, which runs the same workload with a
measurement-only reporter loaded into the extension process. RSS, PSS, CPU, latency and the process count come from the
**plain** variant (the shipped sources) and are never read from the mem variant. The reporters are not part of the package:

* Go: `mem/zz_perf_mem.go`. The driver copies the extension sources into a scratch directory named `posthorse` (pig
  takes the extension's identity from the directory name) and adds this one file to the same package. Its `init` installs
  a `SIGUSR2` handler (only when `PH_PERF_MEM_DIR` is set) that writes `runtime.ReadMemStats` to a JSON file, once as the
  process stands and once after `runtime.GC()` and `debug.FreeOSMemory()`. The driver reads `/proc` first and signals second.
* Node: `mem/node-mem.ts`, imported by a generated entry that re-exports the original's default export. On `SIGUSR2`
  it writes `process.memoryUsage()` and `v8.getHeapStatistics()` as the process stands and after a forced full GC
  (`--expose-gc` is enabled at run time with `v8.setFlagsFromString`). `SIGUSR1` is Node's inspector signal, so it is not used.

The snapshot of the extension sources is taken once per invocation, so another lane's `ph sync` cannot change the code
under test between samples.

### Workloads

| ID | Workload | Session | Samples per side |
|---|---|---|---|
| a | idle startup to `get_state`, warm and cold | none | 15 + 15 |
| b | `get_state`, then one prompt: the model calls `get_context_remaining`, then answers (first-turn latency), warm and cold | none | 15 + 15 |
| c1, c100, c1000 | 6 rounds of `notes` list, search (`quartz-needle`, planted in about a third of the notes at 1, 3 in 100, 1000), read, write, append over a pre-seeded `.pi/notes` of 1, 100 or 1000 notes (30 tool calls) | none | 15 each |
| d50, d200 | `history` search for a planted rare string (`all=true`), a common phrase (`all=true`, `limit=50`) and a `history read` over a deterministic archive of 50 MB / 200 MB (10 / 40 Pi-format session files of 5 MB, same `cwd`; hard links per run, `ripgrep` from the container used by both sides) | `--session-dir` | 15 each |
| e | 200 model requests in 20 prompts (9 tool turns of `notes write/append`, `get_context_remaining` and a 24 KB `read`, then a text turn): reminders in the band below the rollover line and automatic Posthorse rollover at run end; the session file proves it (`session_compaction`, `session_custom_message`) | `--session-dir` | 15 |
| f | 60 idle RPC sessions at once (one extension process each): sum and per-session average of RSS and PSS, time to all ready | none | 15 |

Generated data (archives, notes) are deterministic (fixed seeds) and cached under the scratch tree. A tool call that
fails, or a search whose result lacks the planted needle, invalidates the sample.

## Diagnosis hooks and top-up runs

* `--first N` (with `PH_PERF_KEEP=1` in `run.sh`) appends a top-up of `--samples` runs from index N to the same data directory; `summarize` pools them.
* `PH_PERF_CPUPROF=1`: the Go mem reporter also writes a CPU profile of the extension process (start to the second snapshot);
  `PH_PERF_HOSTPROF=1`: the host runs with `PIG_PROFILE=cpu`. Profiles land in `$PH_PERF_SCRATCH/prof` (never in a measured sample's
  data); read them with `go tool pprof -top -cum`. Extension write/read syscall counters (`ext_sys*_workload`) are information only.
* `summarize` ends with the relative-spread list ((p90-p50)/p50 above 5%).

## Ratchet rules

1. On the `both` run, Go must be `<=` TS (side `ts`, same pig host) on the median of every metric in `perf.py`'s `RATCHET`
   list: startup, first-turn, first-tool and turn latency, total and extension CPU, ext/tree RSS and PSS, peak RSS, Anonymous,
   heap used / in use (as-is and after GC), Sys, and in workload f the per-session averages, for every cell (warm and cold).
   Per-tool-call medians (`tool_ms:*`) are also compared: a slower tool call is a regression.
2. p90 must hold too. A p90-only miss with a better median is run again once as an independent `both` run and both are
   kept; a miss that repeats is a regression.
3. A median miss, or a repeated p90 miss, is not tuned away and not argued: the lead brings a D/decision draft for the owner
   (what is slower or fatter, by how much, why, what it buys), never a quiet trade (brief section 0).
4. Allocations: V8 exposes no cumulative allocation counter, so Go's `total_alloc_workload` / `mallocs_workload` have no TS
   twin here. The rule is the repository's own tooling: every hot path a lane owns has a `Benchmark*` with `-benchmem`
   (`go test -run '^$' -bench . -benchmem`), the lane notes record allocs/op, and the workload figures of the first green
   `both` run become the budget the later runs must not exceed. Go `num_gc_workload` is compared with Node's GC events as
   information.
5. Behaviour parity of the workloads is part of the result: `llm_requests` and the session record counts must be identical
   across sides (the summary prints `PARITY MISMATCH` otherwise): a Go port that rolls over differently is wrong, not fast.

## Limits (read before quoting a number)

* CPU has 10 ms tick resolution (`/proc/<pid>/stat`); a process that exits before the snapshot is not read, so the figures are
  taken while host and extension are alive, before shutdown.
* The OS page cache is not dropped (no root); "cold" means cold application caches, as in `startup_timing.py`.
* The scripted model answers in under a millisecond; the numbers are host plus extension overhead, not model time.
* Go `Sys` counts reserved address space the process may never touch; it is reported as the owner asked and compared with the
  Node RSS (the larger of the two Node-side options).
* The pi side's "ext" columns equal its host columns: Pi runs the extension in-process.
* The container is shared: a loaded machine moves latency, not memory. Compare medians of interleaved runs only.
