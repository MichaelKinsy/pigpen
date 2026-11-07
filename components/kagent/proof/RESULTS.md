# PiG as a kagent harness: proof results and method

What this is: measurements of PiG running as a native kagent harness inside Agent Substrate (gVisor Actors), next to kagent's Claude Code and Codex
harnesses and the community pi-go harness, on a local Kind cluster. Date of the run: 2026-11-07.

**Scope, stated plainly.** Every agent talked to the same hermetic, deterministic **mock model server** (`mockllm/`), not to a real model. The mock
scripts a fixed task (a shell tool call, an MCP tool call, a final answer) and answers in a fixed time, so these numbers isolate what the harness and
the platform add around a model: process start, tool plumbing, approval handling, memory and CPU. With a real model, model latency would dominate every turn.
Nothing here is a vendor benchmark, and it measures one cluster, one host and one task.

kagent and Agent Substrate are separate Apache-2.0 projects, Claude Code and Codex are their vendors' products, and pi-go is a community project; none of them is
affiliated with this repository or endorses it.

## Summary (medians; n = 20 measured sessions per cell unless stated)

| | PiG | Claude Code | Codex | pi-go |
|---|---:|---:|---:|---:|
| Task `[basket]` (shell tool, MCP tool, answer) completed | 20/20 | 20/20 | 20/20 | 20/20 (shell only) |
| MCP tool call made | 20/20 | 20/20 | 20/20 | 0/20 (no MCP tools on rc3) |
| Approval round trip (input-required, approve, resume) | 20/20 | 20/20 | 20/20 | not possible (no approval path) |
| Restart resume: `[recall]` after suspend and restore answers with the first marker | 20/20 | 20/20 | 20/20 | 20/20 |
| Actor memory, first activation (turn 1), working set max (MiB) | **51** | 142 | 59 | 32 |
| Actor memory, first activation, `memory.peak` (MiB) | 53 | 146 | 64 | 33 |
| CPU time, first activation, sandbox (ms) | 809 | 3001 | 1934 | 625 |
| Agent apply to Ready (s, n = 5, resolution about 0.6 s) | 2.4 | 1.8 | 2.4 | 2.4 |
| Substrate restore on first message, create to running (s) | 0.41 | 0.36 | 0.34 | 0.37 |
| Substrate restore on later messages (s) | 0.37 | 0.31 | 0.29 | 0.32 |
| First event after send (ms) | 551 | 486 | 452 | 565 |
| `[basket]` turn 1, send to done (ms) | 1268 | 3347 | 2375 | 1010 |
| `[recall]` turn 2 after restore, send to done (ms) | 1061 | 2557 | 1657 | 873 |
| `[approve]`: send to input-required (ms) | 892 | 2547 | 1878 | n/a |
| `[approve]`: approval to done (ms) | 1313 | 3061 | 2166 | n/a |
| Image, compressed (MiB) and layers | 198 / 9 | 144 / 4 | 200 / 4 | 310 / 7 |
| **Worker pod replaced** (pool pod deleted by name, new pod Ready after a median 1.7 s): first `[recall]` attempt succeeded | 0/10 | 0/5 | not run | not run |
| ...the same message, retried with backoff, eventually succeeded (attempts; time from first send) | 10/10 (2; median 12.5 s) | 5/5 (2; median 14.0 s) | not run | not run |
| ...with an approval parked before the deletion, first attempt of the approval | 0/10 | not run | not run | not run |
| ...the same approval, eventually | 10/10 (2; median 12.9 s) | not run | not run | not run |
| **Rejection** with a reason: run completes, the reason reaches the model, the answer is over the rejection text | 10/10 | 3/3 | 3/3 | not possible |

How to read it, with the honest limits:
- The per-turn labels in the memory and CPU rows are FIRST-ACTIVATION medians: the activation that serves turn 1 of a session (the full tables also give the second
  activation). They are not averages over all turns of a session. The `[basket]` columns use the basket agents, the approval rows the approve agents.
- PiG uses about 0.36 times the memory of Claude Code and about 0.88 times that of Codex in that activation, and about 1.6 times that of pi-go. Its `[basket]` turns
  complete in about 0.38 times (Claude Code) and 0.53 times (Codex) the time, and 1.26 times that of pi-go. CPU per activation is 0.27 times that of Claude Code
  (809/3001), 0.42 times that of Codex (809/1934) and 1.30 times that of pi-go (809/625). These ratios hold for this mock-driven task on this cluster only.
- CPU per activation is the sum over the activation's cgroup leaves of each leaf's maximum `cpu_usec` seen in the activation (an earlier version of `analyze.py` took the leaves present at the
  last sample and dropped a leaf that had already gone; that is fixed and unit-tested, and the raw samples are in `data/` so the table can be re-derived).
- The numbers above supersede an earlier run of the same schedule on an earlier PiG build and mock (10 measured rounds, memory 51/142/58/31 MiB). Where the two runs differ by more than the run to run spread it is
  noted here: PiG's first event (551 ms) is now about 20 percent behind Codex (452 ms) and Claude Code (486 ms) and its restore times are about 0.05 to 0.07 s longer; in the earlier run those were within noise.
- pi-go is not like-for-like: on kagent rc3 it gets no MCP tools, so its turn does less work (one shell call, no MCP call), and it has no approval path.
- **The mock's per-call floor is not subtracted.** Every model call costs about 0.11 s (50 ms first token plus chunk delays) and that is included in every reported time. It is
  identical per call, but the number of calls differs: PiG, Claude Code and Codex make 3 model calls for `[basket]` (shell call, MCP call, answer), pi-go makes 2 (it has no MCP
  tools here), so pi-go's times carry about 0.11 s less floor than the others'.
- The memory figure includes the gVisor sentry overhead that every Actor pays, so ratios between harnesses understate the difference in the harness process itself.
- Differences below roughly 15 percent (for example first event 452 to 565 ms among the others, restore times) are within the run-to-run spread and should not be read as an ordering.
- Per-turn numbers include harness process start, because kagent suspends the Actor after every turn and restores it into a new process each time.
- After the worker pod is replaced the gateway does not accept the first message to an existing session until the session is available again: every first attempt (25 of 25) returned JSON-RPC
  error -32004 (`KAGENT_SEND_NOT_ACCEPTED`, "input was not accepted; retry after the session becomes available") after a median 11.0 s, and the retry succeeded at once. So a client that does not retry sees a
  failure on the first message after a pod replacement, for about 11 s, and a client that retries the same message gets its answer about 12 to 14 s after the first send. This is what the gateway did on
  this cluster for all three harness families that were tried; it is a property of kagent rc3 and Substrate, not of PiG.

## Method

**The task.** One session per measurement, through kagent's public A2A gateway (`POST /agents/{namespace}/{name}`, JSON-RPC `SendStreamingMessage`, header
`A2A-Version: 1.0`; approvals use the `https://kagent.dev/extensions/hitl/v1` extension). Turn 1 sends `[run-<id>] [basket] ...`: the mock answers with a shell tool call
(`echo basket-ok > /tmp/basket.txt && cat /tmp/basket.txt`), then, after the tool result, a call to the MCP tool whose name ends in `kagent_probe`, then
`BASKET-DONE <sha256 of the concatenated tool results>`. The `[approve]` variant targets an approval-gated MCP binding and parks in `input-required`; the client approves it. After
the Actor was suspended, turn 2 sends `[recall]` and the mock answers `RECALL <first marker of the history>`, which proves the session survived the restore.
The mock chooses the shell tool from the tools the client offers (`bash`, `Bash`, `exec_command`, `shell_command`, `shell`) and builds arguments from that tool's schema, so one
scenario drives all harnesses. It speaks OpenAI Chat Completions, OpenAI Responses and Anthropic Messages, streaming and not, plus a Streamable HTTP MCP server, with
deterministic token usage and a fixed 50 ms first-token latency and 5 ms per chunk. One measured model call costs about 0.11 s, identical per call for every harness; it is not subtracted from any reported time.
The final `BASKET-DONE <sha>` differs per harness because each formats tool results differently, so correctness is checked by pattern, not across harnesses.

**Schedule and statistics.** 7 agents (basket and approve variants), 2 warmup rounds (excluded) and 20 measured rounds, one session per invocation (a fresh Actor), the order of
agents rotated each round so none always goes first. The schedule took 22 minutes. Medians with p90, min and max; with n = 20 no confidence interval is claimed. A host gate waits, before every session,
until the host's CPU pressure (PSI some avg10) is at most 35 percent, for at most `GATE_WAIT` seconds. By default it then runs the session anyway and records it as
proceeded-despite-pressure (`GATE_MODE=refuse` stops the schedule instead); in this run the pressure stayed under the gate, so no session proceeded over it (`pressure.tsv` has a line per session and
`analyze.py` lists every session that did proceed over the gate).

**Memory.** Memory is read from cgroup v2 files on the node, sampled every 0.25 s: for each Actor activation the working set (`memory.current - inactive_file`, the quantity the
kubelet, cAdvisor and Substrate itself use) of the cgroups `<actorUID>-*` that Substrate creates under the worker container scope. The gVisor sentry and gofer live in
`<actorUID>-_sandbox`; the other leaf is empty. The leaf's `memory.peak` high-water mark is read before the leaf disappears. Alternatives considered and why they lost: process RSS
overstates (it counts mapped binary pages charged elsewhere; PiG idle in a plain container read 48 MiB RSS against a 14 MiB cgroup working set); PSS depends on neighbours (the same idle
process read 31 and then 52 MiB with nothing changed); numbers from inside the sandbox are approximations, per gVisor's own resource-model documentation; `kubectl top` and
Prometheus working-set series are the same quantity but coarser and not always installed; Substrate's per-actor usage log records exist but are sampled once a minute, which
is longer than an activation, so they are used only as a cross-check. gVisor accounts application memory as shmem, not anon, so the breakdown is kept in the raw samples.

**Time.** Client times come from the `basket` client, from the moment of sending a message. Server-side times come from Substrate's own records for the same sessions: the
`Actor state changed` log records of the API server (create, resuming, running, suspending, suspended) and the node agent's `Restore timing breakdown` and
`Checkpoint timing breakdown` records, joined by actor name and UID. Cold start is the time from applying a cloned Harness and Agent to the Agent's `Ready` condition. Image size is the sum
of the linux/amd64 layer sizes in the manifest, and "registry pull" is `crane pull` from the host.

**Lifecycle facts that shape the numbers.** kagent suspends the Actor right after each turn (volumes-only snapshots) and restores it into a new process for the next message, so
there is no warm idle state to measure and every turn includes harness start-up. Server records show 2 restores per `[basket]` session and 3 per `[approve]` session for every agent
(20 of 20 sessions), so the `[recall]` turn is a true restore.

## Full tables from the run

### Capability and correctness (n = measured sessions per agent)

| agent | sessions | ok | `[basket]`/`[approve]` done | MCP probe called | approval round trip | recall after suspend | tool names seen |
|---|---:|---:|---:|---:|---:|---:|---|
| claude-approve | 20 | 20 | 20 | 20 | 20 | 20 | mcp__mock-mcp-approval__kagent_probe |
| claude-basket | 20 | 20 | 20 | 20 | 0 | 20 | Bash, mcp__mock-mcp__kagent_probe |
| codex-approve | 20 | 20 | 20 | 20 | 20 | 20 | mock-mcp-approval.kagent_probe |
| codex-basket | 20 | 20 | 20 | 20 | 0 | 20 | command_execution, mock-mcp.kagent_probe |
| pig-approve | 20 | 20 | 20 | 20 | 20 | 20 | mcp__kagent_mock__kagent_probe |
| pig-basket | 20 | 20 | 20 | 20 | 0 | 20 | bash, mcp__kagent_mock__kagent_probe |
| pigo-basket | 20 | 20 | 20 | 0 | 0 | 20 |  |

### Client latency through the A2A gateway (ms, median / p90 / min / max)

| agent | n | turn1_first_event | turn1_first_text | turn1_end_to_end | turn1_input_required | approval_to_done | turn2_first_event | turn2_first_text | turn2_total |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| claude-approve | 20 | 478 / 530 / 413 / 553 | 2547 / 2670 / 2286 / 2796 | 5559 / 5911 / 5038 / 6174 | 2547 / 2670 / 2286 / 2796 | 3061 / 3256 / 2665 / 3376 | 435 / 574 / 326 / 634 | 2216 / 2364 / 2005 / 2783 | 2424 / 2577 / 2203 / 3126 |
| claude-basket | 20 | 486 / 553 / 403 / 573 | 3118 / 3326 / 2923 / 3443 | 3347 / 3580 / 3142 / 3726 | n/a | n/a | 439 / 579 / 341 / 726 | 2343 / 2471 / 2081 / 2830 | 2557 / 2686 / 2307 / 3205 |
| codex-approve | 20 | 443 / 527 / 379 / 632 | 1878 / 2023 / 1796 / 2063 | 4067 / 4229 / 3872 / 4369 | 1878 / 2023 / 1796 / 2063 | 2166 / 2303 / 2066 / 2346 | 417 / 497 / 354 / 617 | 1515 / 1637 / 1363 / 1667 | 1671 / 1767 / 1525 / 1879 |
| codex-basket | 20 | 452 / 489 / 394 / 546 | 2158 / 2256 / 1974 / 2447 | 2375 / 2478 / 2161 / 2675 | n/a | n/a | 411 / 480 / 319 / 573 | 1515 / 1617 / 1372 / 1769 | 1657 / 1819 / 1545 / 1898 |
| pig-approve | 20 | 540 / 605 / 465 / 636 | 892 / 965 / 816 / 1032 | 2246 / 2360 / 2028 / 2462 | 892 / 965 / 816 / 1032 | 1313 / 1459 / 1170 / 1579 | 614 / 773 / 526 / 822 | 916 / 1112 / 790 / 1253 | 1051 / 1352 / 914 / 1411 |
| pig-basket | 20 | 551 / 609 / 484 / 625 | 1080 / 1143 / 971 / 1188 | 1268 / 1333 / 1142 / 1439 | n/a | n/a | 613 / 700 / 533 / 795 | 930 / 1079 / 822 / 1107 | 1061 / 1192 / 925 / 1374 |
| pigo-basket | 20 | 565 / 602 / 496 / 674 | 718 / 783 / 641 / 895 | 1010 / 1097 / 881 / 1190 | n/a | n/a | 607 / 720 / 508 / 827 | 699 / 856 / 596 / 931 | 873 / 1118 / 790 / 1126 |

### Server side lifecycle (s, median / p90), from Substrate records of the same sessions

| agent | first resume (create to running) | restore total, first | restore kind | 2nd resume | restore total, 2nd | restore kind | checkpoint total | suspend start vs turn end (negative = before the client saw the end) | suspend duration |
|---|---:|---:|---|---:|---:|---|---:|---:|---:|
| claude-approve | 0.345 / 0.382 | 0.341 / 0.378 | latest | 0.307 / 0.348 | 0.301 / 0.343 | local | 0.228 / 0.251 | -0.042 / -0.021 | 0.238 / 0.261 |
| claude-basket | 0.359 / 0.381 | 0.352 / 0.376 | latest | 0.311 / 0.365 | 0.307 / 0.359 | local | 0.223 / 0.263 | -0.041 / -0.028 | 0.229 / 0.272 |
| codex-approve | 0.317 / 0.366 | 0.312 / 0.363 | latest | 0.292 / 0.313 | 0.287 / 0.308 | local | 0.254 / 0.279 | -0.043 / -0.026 | 0.262 / 0.287 |
| codex-basket | 0.336 / 0.358 | 0.331 / 0.353 | latest | 0.286 / 0.33 | 0.28 / 0.325 | local | 0.254 / 0.286 | -0.044 / -0.023 | 0.262 / 0.294 |
| pig-approve | 0.404 / 0.458 | 0.399 / 0.452 | latest | 0.363 / 0.398 | 0.358 / 0.392 | local | 0.228 / 0.24 | -0.042 / -0.009 | 0.235 / 0.25 |
| pig-basket | 0.414 / 0.469 | 0.41 / 0.463 | latest | 0.374 / 0.412 | 0.369 / 0.406 | local | 0.225 / 0.247 | -0.038 / -0.011 | 0.233 / 0.256 |
| pigo-basket | 0.374 / 0.45 | 0.368 / 0.445 | latest | 0.319 / 0.379 | 0.314 / 0.375 | local | 0.223 / 0.242 | -0.037 / -0.013 | 0.232 / 0.25 |

### Memory per actor activation (MiB, median / p90 / max over sessions)

Sum of the actor's cgroup leaves `<actorUID>-*` below the worker pod, working set = memory.current - inactive_file, sampled every 0.25 s; `peak` is the leaf's memory.peak high-water mark read before the leaf disappeared.

| agent | activation | samples | ws first sample | ws median | ws max | sandbox ws max | memory.peak | cpu ms |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| claude-approve | act1 | 20 | 7.7 / 24.8 / 26.7 | 91.3 / 95.9 / 109.5 | 138.2 / 142.1 / 143.1 | 138.2 / 142.1 / 143.1 | 143.4 / 144.6 / 145.2 | 2449.6 / 2665.5 / 2717.2 |
| claude-approve | act2 | 10 | 10.8 / 35.3 / 63.6 | 91.5 / 96.0 / 106.7 | 135.6 / 139.3 / 141.3 | 135.6 / 139.3 / 141.3 | 140.2 / 141.6 / 142.6 | 2177.2 / 2364.1 / 2857.3 |
| claude-basket | act1 | 13 | 16.6 / 27.3 / 34.5 | 120.3 / 125.4 / 126.0 | 141.5 / 145.2 / 146.2 | 141.5 / 145.2 / 146.2 | 145.8 / 147.3 / 148.5 | 3000.9 / 3242.6 / 3401.4 |
| claude-basket | act2 | 10 | 11.8 / 33.3 / 56.6 | 92.9 / 106.4 / 111.8 | 135.9 / 139.6 / 140.7 | 135.9 / 139.6 / 140.7 | 140.8 / 142.2 / 142.9 | 2296.2 / 2477.2 / 3039.7 |
| codex-approve | act1 | 14 | 12.4 / 35.1 / 39.7 | 46.3 / 51.2 / 52.2 | 60.0 / 61.7 / 62.5 | 60.0 / 61.7 / 62.5 | 61.5 / 62.5 / 63.1 | 1605.0 / 1768.4 / 1795.8 |
| codex-approve | act2 | 7 | 26.8 / 38.4 / 54.5 | 51.2 / 57.3 / 61.5 | 60.5 / 61.6 / 75.9 | 60.5 / 61.6 / 75.9 | 61.6 / 63.0 / 77.1 | 1435.1 / 1511.0 / 1561.1 |
| codex-basket | act1 | 9 | 7.2 / 27.6 / 32.9 | 49.4 / 55.4 / 65.1 | 58.6 / 60.8 / 78.0 | 58.6 / 60.8 / 78.0 | 63.7 / 65.4 / 80.7 | 1934.0 / 1999.8 / 2272.0 |
| codex-basket | act2 | 6 | 12.1 / 33.4 / 53.0 | 50.6 / 55.9 / 59.6 | 59.5 / 61.6 / 62.8 | 59.5 / 61.6 / 62.8 | 61.5 / 63.7 / 63.8 | 1410.3 / 1527.3 / 1667.0 |
| pig-approve | act1 | 7 | 7.1 / 27.2 / 49.2 | 32.0 / 36.6 / 44.2 | 49.2 / 50.2 / 50.5 | 49.2 / 50.2 / 50.5 | 49.8 / 50.9 / 51.2 | 701.0 / 764.9 / 778.3 |
| pig-approve | act2 | 4 | 9.8 / 30.4 / 31.4 | 31.9 / 40.7 / 46.6 | 47.6 / 49.3 / 50.2 | 47.6 / 49.3 / 50.2 | 49.4 / 50.4 / 50.8 | 601.8 / 705.3 / 715.6 |
| pig-basket | act1 | 5 | 8.8 / 28.5 / 49.8 | 34.7 / 46.7 / 50.6 | 51.3 / 52.2 / 52.8 | 51.3 / 52.2 / 52.8 | 52.5 / 53.2 / 53.8 | 809.1 / 852.7 / 854.1 |
| pig-basket | act2 | 4 | 13.2 / 30.4 / 48.0 | 31.6 / 42.9 / 48.0 | 48.1 / 49.5 / 50.5 | 48.1 / 49.5 / 50.5 | 49.5 / 50.6 / 51.3 | 620.7 / 653.8 / 662.6 |
| pigo-basket | act1 | 4 | 6.7 / 23.9 / 31.8 | 30.4 / 32.2 / 32.5 | 31.6 / 32.7 / 33.2 | 31.6 / 32.5 / 33.2 | 33.2 / 33.8 / 33.8 | 624.5 / 654.5 / 672.7 |
| pigo-basket | act2 | 3 | 12.9 / 27.4 / 30.7 | 29.7 / 30.7 / 31.3 | 30.9 / 31.2 / 31.5 | 30.9 / 31.2 / 31.5 | 31.6 / 32.0 / 32.8 | 428.4 / 494.9 / 541.9 |

### Images

| harness | image | compressed size (MiB, amd64 layers) | layers | registry pull, crane (s) |
|---|---|---:|---:|---:|
| claude | `ghcr.io/kagent-dev/kagent/claude-harness@sha256:216613bf155d3d437765675144fee19c9045cb21cd2e481189f2dcae36c24b3b` | 144.4 | 4 | 4.2 |
| codex | `ghcr.io/kagent-dev/kagent/codex-harness@sha256:84c3275e36c0d98472a8b930a628e47e08277e7ce4438908e2a44fd1d8a9efd2` | 200.5 | 4 | 5.3 |
| pi-go | `localhost:5001/pi-go-kagent@sha256:a1ed87082497dfa37b6e60586877c379121b86d3a472e1dc076f0c62172d1d8b` | 310.0 | 7 | 0.3 |
| pig | `localhost:5101/kagent-pig-proof/pig-kagent@sha256:ffbf14d786ed2bd238cac19299d88ce820eca006cd4e4ebac30a20ea97123e07` | 198.3 | 9 | 0.2 |

### Cold start: Agent apply to Ready (s)

| harness | n | median | min | max | values |
|---|---:|---:|---:|---:|---|
| claude-basket | 5 | 1.8 | 1.7 | 2.4 | 2.4, 2.4, 1.7, 1.8, 1.7 |
| codex-basket | 5 | 2.4 | 1.7 | 2.4 | 2.4, 1.7, 1.7, 2.4, 2.4 |
| pigo-basket | 5 | 2.4 | 1.7 | 2.4 | 1.7, 1.7, 2.4, 2.4, 2.4 |
| pig-basket | 5 | 2.4 | 1.7 | 2.4 | 2.4, 1.8, 1.7, 2.4, 2.4 |


### Worker pod (shared by all actors)

Working set of the `kagent-default` worker pod: idle (no actor cgroup) median 545.8 MiB (n=2471 samples), with an actor running median 600.9 MiB (n=2591). Actor memory in the tables above is the actor's own sandbox cgroup, not this pod number.

### Host pressure gate

154 sessions were gated on host CPU pressure; 0 measured session(s) started although pressure stayed above the gate after the wait ran out (none).

### Rejection series (`reject.sh`): `[approve]`, then REJECT with the reason `rejected by basket`

| agent | runs | passed | first attempt without gateway retry | reason visible in the mock's request log | answer sha = sha of the logged rejection text | rejection to done, ms (median / p90 / max) | text the model was shown |
|---|---:|---:|---:|---:|---:|---|---|
| pig-approve | 10 | 10 | 10/10 | 10/10 | 10/10 | 1429 / 1493 / 1672 | `Tool call was rejected by user. Reason: rejected by basket` |
| claude-approve | 3 | 3 | 3/3 | 3/3 | 3/3 | 2940 / 3047 / 3074 | `PreToolUse:mcp__mock-mcp-approval__kagent_probe hook error: rejected by basket` |
| codex-approve | 3 | 3 | 3/3 | 3/3 | 3/3 | 2230 / 2238 / 2240 | `aborted by user after 0.1s{"content":[{"type":"text","text":"Tool call denied: rejected by basket"}],"isError":true}` |

A run passes when the session reaches the final answer after the rejection, the mock saw the reason text in the tool result it was asked to answer, and the answer's sha is the sha256 of exactly
that logged text (so it is the rejected path's sha, not the approved path's). All three harnesses pass the reason on to the model, each in its own wording; the answer sha therefore differs per harness
(one distinct sha per harness across the runs). Per-run records: `data/reject-20261107.json.gz`.

### Worker pod replacement series (`restart.sh`)

Per run: a session is started (idle variant: a marker is remembered; approval variant: an `[approve]` task parks in input-required), the worker pod is deleted by its exact name, the script waits for a
different pod to be Ready (median 1.7 s, p90 1.9 s, max 2.0 s after the delete returned), and the follow-up (`[recall] go`, or the approval) is sent on the same context. The first attempt is recorded exactly, then the same message
is retried with backoff (0.5 s doubling to 5 s) until it succeeds.

| agent and variant | runs | first attempt succeeded | first-attempt outcomes | first attempt took (ms, median / max) | eventually succeeded | attempts to success | time from first send to success (ms, median / p90 / max) |
|---|---:|---:|---|---|---:|---|---|
| pig-basket, idle (`[recall]` names the marker) | 10 | 0/10 (0%) | JSON-RPC -32004: 10 | 11041 / 11140 | 10/10 (100%) | 2 (all) | 12495 / 12868 / 12903 |
| pig-approve, approval parked, then resumed | 10 | 0/10 (0%) | JSON-RPC -32004: 10 | 11041 / 11046 | 10/10 (100%) | 2 (all) | 12906 / 13161 / 13524 |
| claude-basket, idle | 5 | 0/5 (0%) | JSON-RPC -32004: 5 | 11038 / 11045 | 5/5 (100%) | 2 (all) | 14039 / 14102 / 14127 |

In the approval variant the resumed task completed with `APPROVE-DONE` and a following `[recall] go` still named the marker in 10 of 10 runs. A wrong answer, an empty reply or a timeout never occurred; every
failure was the same gateway error. Per-run records: `data/restart-20261107.json.gz`.

## Caveats that apply to every number here

- **Mock model.** All agents talk to the same hermetic mock (built from `mockllm/` in this directory). No real model is involved, so
  model latency is a fixed 0.1 s per call and identical for every harness. The final `BASKET-DONE <sha>` differs per harness because each
  harness formats tool results differently; correctness is checked by pattern, not by comparing shas across harnesses.
- **What a turn contains.** kagent suspends the actor right after each turn (Substrate checkpoint, volumes-only snapshots) and restores it
  into a **new process** for the next message. Every turn therefore includes harness process start-up; there is no warm idle state to measure.
  Server records show 2 restores per `[basket]` session and 3 per `[approve]` session for every agent (20 of 20 sessions), so the `[recall]` turn is a
  true restore, not a warm actor. "restore total" is Substrate's restore of the snapshot (about 0.3 s), not harness boot.
- **n and statistics.** 2 warmup rounds (excluded), then 20 measured sessions per agent, one session per invocation, agents rotated each
  round so none always goes first. Medians with p90, min and max; with n = 20 the p90 is interpolated between the 18th and 19th
  sample and no confidence intervals are claimed. The restart and rejection series have n = 10 (PiG), 5 (Claude Code restart) and 3 (Claude Code and Codex rejection). Differences under roughly 15 percent should not be read as real.
- **Shared noisy host.** The run used a 192-core amd64 Linux host that was shared with other work (load average 2.4 when the schedule began; the Kind node was capped at 24 CPUs and 128 GiB). `baseline.sh` gates on host
  CPU pressure (PSI some avg10 at most 35 percent before each session); the observed range was 2.7 to 13.1 percent, so the gate never fired and no session proceeded over it.
  Other lanes of the same project had agreed to stay idle during the schedule and the two series; that was an agreement, not enforcement.
- **Memory accounting (gVisor).** Per actor activation the working set is summed (`memory.current - inactive_file`) of the cgroup leaves
  `<actorUID>-*` that Substrate creates under the worker container scope. The gVisor sentry and gofer live in `<actorUID>-_sandbox`; the
  number therefore includes the sentry's own overhead (Go heap, page tables, netstack), which any harness pays, and gVisor accounts
  application memory as shmem, not anon (gvisor.dev resource model). Guest-internal `/proc` numbers were not used. Samples are every 0.25 s and an
  activation lasts about 0.5 to 4 s (3 to 20 samples), so "ws max" can miss a short spike; the leaf's `memory.peak` high-water mark, read before the
  leaf disappears, is reported beside it and stays within about 5 MiB of it. Numbers are per activation (one turn), not per session lifetime.
- **Cold start.** "Agent apply to Ready" clones a Harness and Agent, applies them and polls the Ready condition every 0.5 s (about 0.3 s of
  kubectl overhead per poll), so the resolution is about plus or minus 0.6 s and the values mostly show a floor of about 1.7 s. The image digest is already in the node
  image cache, so no registry pull is included. It is golden-snapshot build time, not a first-ever cold start. The first message to a new
  session resumes the actor from the golden snapshot; its server-side time is in the lifecycle table.
- **Images.** Compressed size is the sum of the linux/amd64 layer sizes from the manifest. "Registry pull" is `crane pull` from the test host and is
  not comparable across rows: Claude and Codex come from ghcr.io, PiG and pi-go from the local Kind registry. Substrate actors pull through
  atelet, which was not timed.
- **Version skew.** Claude and Codex are kagent's built-in harness images (rc3 catalog digests above); the CLI versions inside were not inspected.
  pi-go is v0.3.10 rebuilt for amd64 from its upstream Dockerfile with `TARGETARCH=amd64`, because the published image's amd64 manifest holds an arm64 binary.
  On kagent rc3 the pi-go harness receives no MCP tools and has no approval path, so its MCP step is skipped (`MCP probe called` = 0) and no approval row exists. pi-go streams
  its tool activity as text, not as `function_call` parts. PiG is a development build of the `pig-kagent` harness image built from the commit `8be0f394` of this repository (digest `sha256:ffbf14d7...`), not a release; the PiG version inside it was not inspected.
- **Latency definitions.** Client times are measured by `basket` through the public A2A gateway (`/agents/kagent/<agent>`, `SendStreamingMessage`),
  from the send of the message. `turn1_first_event` includes session creation, actor restore from the golden snapshot and the harness start; `turn1_end_to_end` for `[approve]`
  includes the automatic approval round trip with no human delay.
- **Not measured.** Suspended footprint (snapshot size), per-turn growth over many turns, concurrency and throughput, image unpack time, and the worker pod replacement for pi-go and Codex.
  The pod replacement series is one pod per run on a single-node cluster, so it shows the gateway's behaviour when the only worker is replaced, not a rolling update.
- **Worker pod memory.** The shared worker pod's working set (idle median 546 MiB, with an actor 601 MiB) is far above the 61 and 107 MiB of the earlier run; the cause was not investigated. It does not enter the
  per-actor tables, which are the actor's own cgroup leaves.

## Reproducing and raw data

See `README.md` in this directory. The files in `data/` (about 0.9 MB in all):
- `baseline-20261107.json.gz`: the per-session records and the aggregates (`analyze.py` output; host name removed).
- `baseline-20261107-mem.jsonl.gz`: the collector's raw 0.25 s samples of the whole run (5062 samples, all of them; the first-activation fallback of `ship_raw` was not needed). `python3 analyze.py --mem data/baseline-20261107-mem.jsonl.gz ...`
  re-derives the memory and CPU columns (the logs and basket runs are also needed for the full tables).
- `reject-20261107.json.gz`, `restart-20261107.json.gz`: per-run records and summaries of the two series.
