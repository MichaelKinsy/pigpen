# Port record: pi-goal-x (full port, 0.2.0)

| Input | Identity |
|---|---|
| Original | `pi-goal-x` 0.32.3 by tmonk ("Copyright (c) 2026 Lucas"), https://github.com/tmonk/pi-goal-x, commit `64c5ace87f3400c0b55ef87f2d2912167f53dbaa`, MIT (its LICENSE file, kept at `port/oracle/LICENSE`) |
| Oracle | Pi 1.0.0, Node 24.19.0, the unmodified package at `port/oracle` (dev dependencies `@earendil-works/pi-*` 1.0.0 from its own `package-lock.json`); its own test suite passes there (1053 tests, 83 files; one goal-files test fails if a file `escape.md` exists in the parent of the system temp directory, which the old `pool-unsafe-paths` session used to leave there) |
| Target | PiG 0.4.0+1.0.0, go1.27.1 |
| Original's tests | 1049 titles in 83 files. Slice: 45 titles in 8 files, 41 exact twins and 4 named skips; the other 1004 (75 files) are deferred by name in `port/slices.json` |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope (0.2.0)

Everything the original registers is ported: the 16 commands, the goal, task and drafting tools, the durable scheduler, accounting,
prompt injection, compaction, prompt cache shaping, live retention, network recovery, the activity trail, the ledger with checkpoints and
recovery, layered settings, the dashboard, widgets and overlays, the questionnaire and rpc dialogs, the completion auditor and the
oracle. The refusal layer of 0.1.1 is removed. Stand-ins (not differences in behavior the model or user can see): the auditor and the
oracle run in a child `pig` process because the SDK has no child session API (SDK requests C2, C2a to C2c in the owner's core-items
file: custom tools in the child, compaction off in the child, the parent's model registry in the child); widgets are laid out at the
terminal width.

**Twins:** 1046 exact twins and 3 named skips of 1049 upstream titles (`pigeq twins check`), `slices.json` is empty. **Go tests:** `go vet`
on linux, windows and darwin and `go test -race -count=3` pass on the merged tree. **Mutations:** `mutations.json` holds one list of
2034 mutants (every find text occurs exactly once); the run results are in the PR bodies. The lead faithfulness review (five
reviewers, function by function against the TypeScript) found and fixed: `set_goal_tasks` item schema without `additionalProperties:false`,
`clearContinuationState` default in the blocked and pause flows, a failed tweak apply propagating as a tool error, the active-pool read
that must fail for a vanished external root, `timestampForFile` for any ISO form, the external-pool ledger symlink check, stale-lock
date parsing, quoting of task ids in policy messages, `localeCompare` ordering, and RE2 `\s` against JavaScript `\s` in the
questionnaire. Not fixed (listed in the status file): a failing authoritative write is logged where the original throws, `format.go`
`localeCompare` is ASCII-exact only, and the host items noted there.

**pigeq:** scenarios are recorded under Pi 1402973a (`port/scenarios`, `port/golden`, `port/gen-scenarios.py`); the new
scripted-model scenarios cover the goal tools. The host's own system-prompt documentation block (`Pi documentation ...` against `PiG
documentation ...`) differs between the hosts and falls inside the delta of a request that adds tool snippets; the scenarios mask it with
a scenario-level normalize rule (`HOSTDOCS`); the difference is PiG's documented additive difference D22 (PiG's docs bundle named in the system prompt).

## Mapping

| ID | Original | Go | Checked by |
|---|---|---|---|
| M1 | `goal-record.ts`: records, scheduler normalization, focus entries | `record.go` | the goal-record twins; `extras_test.go`; replayed sessions |
| M2 | `goal-format.ts`, `goal-notifications.ts`, `goal-draft.ts`'s contract extraction | `format.go` | the goal-core, goal-notifications, goal-draft and verification-contract twins; `extras_test.go` |
| M3 | `goal-files.ts`, `goal-pool.ts`, the pool snapshot, locks, ledger | `storage.go`, `proc_*.go` | the goal-files, goal-pool and goal-pool-snapshot twins; every replayed session compares the files written |
| M4 | `goal-core.ts`, `goal-scheduler.ts` (restore, implicit ready, pause, takeover), commands | `core.go` | 86 sessions; `extras_test.go` |
| M5 | `goal-settings.ts` | `settings.go` | sessions with settings files and environment; `extras_test.go` |
| M6 | host side (`ctx`, `ctx.ui`, session entries) | `extension.go` | `adapter_test.go` (the real SDK through the Skill's fake host: terminal, RPC and print mode); the Pi-recorded scenarios (RPC) |
| M7 | JavaScript semantics: ordered objects, `JSON.parse`/`JSON.stringify` (numbers beyond the double range, non-finite numbers), `Number` text, UTF-16 lengths, `/i` without the u flag | `jsjson.go`, `jsutil.go` (copied from the powerline port, then fixed here), `format.go` (`jsFoldSafe`) | every file comparison; the bad-input sessions |

## Two oracles

1. **Seventeen scenarios recorded from the original under Pi 1.0.0** (`port/scenarios`, `port/golden`, `port/gen-scenarios.py`):
   what the host sees in RPC mode: the startup clears, the notifications, the status line, the focus entries. Files and goal ids
   are not visible over RPC. The host check differs from Pi's only by factory-widget rows that PiG's RPC emits and Pi's drops
   (G3), so the port drops non-clear widget pushes in RPC mode.
2. **Eighty-six sessions run by the original's own code** (`port/drive/drive.mjs`; cases from `port/gen-cases.py`; golden by
   `port/record-drive.sh`): the original is imported and driven in process by a scripted host (a fake `pi`, a ctx, a frozen
   `Date.now` and `Math.random`, a temporary project directory), and every notification, status call, widget call, session
   entry and **every file under `.pi`** is recorded. `replay_test.go` runs the same sessions through the Go core and compares all
   of it. The sessions seed goal files, legacy state and snapshots, change files behind the extension's back and set settings
   and environment. The scripted host can be headless (`noUI`) or busy (`busy`: `isIdle()` is false, `abort` is recorded), and
   a step can remove files while the clear confirmation is open (`duringConfirm`). The snapshot file is not compared (it is a
   cache; its content is checked through what is served and by the goal-pool-snapshot twins); the scheduler pause event's `at`,
   stamped from the wall clock in the original, is masked.

## Process: the order was not tests first

For the storage and command core the order was **not** tests first, and this should be read as such. The oracle driver, the case
generator and the recorded golden were written first; the Go core was then written against them and the replay test was written
after the core, against the recorded golden (there is no red commit for the core). The twins came last. The recorded golden comes
from the original's own code, never from the Go port, so the oracle is not self-recorded. What the later order cost: the first
mutation run killed only 106 of 200 mutants and the sessions had to be extended (46 to 70) to reach the rest.

The missing red evidence was added afterwards, in review, by mutation: each mutant is the core with one defect, run against the
replay test alone (`go test -run TestReplayDriveCases`).

- Of the lane's 188 mutants, the replayed sessions alone kill 126, and 71 of the 80 in `core.go`. The 9 core survivors are:
  - the kickoff with a nonzero allowance (3), where the original starts a run and there is nothing to record;
  - the terminal status line (1);
  - the headless and busy branches (3), which the scripted host could not reach then;
  - two branches that the unit tests pin.
- 24 mutants written in review: 17 were killed and 6 survived the whole suite (one did not compile). Of the 6:
  - two are equivalent in this slice, because the port holds its lock across the dialog and completed goals never enter the pool;
  - four led to new sessions and twins: a wait deadline equal to now, a focus entry from another storage root, the legacy
    snapshot left behind, and a wrong `dirMtimeMs`.
- The SDK adapter had no unit test: all 12 mutants of `extension.go` survived. `adapter_test.go` now kills 11 of them. The
  twelfth ignores the select's `ok` flag; it is equivalent, because the core matches the returned label, and was dropped.

The review's own changes went red first: the tests in one commit, the fixes in the next.

## Results

- `go test -race`: pass. 41 twins and 4 named skips (`pigeq twins check --deferred port/slices.json`: exit 0), 86 replayed
  sessions, `extras_test.go`, `adapter_test.go`.
- `pigeq check` against the Pi 1.0.0 goldens: 17 of 17 scenarios + port-gaps + exec-coverage, 3 runs in a row.
- `pigeq mutate --unit`: **210 mutations, all killed** (the lane's 188 plus 22 from review: 11 on the adapter, 4 for the
  survivors above, 7 on the review's fixes; every one is killed at the unit layer). The lane's own final run killed 188 of 188. The first run of 200 killed 106. The survivors led to
  new sessions (restore, lost goals, stale revisions, legacy state, pool snapshots, unfocus takeover) and unit tests (record
  normalization, scheduler normalization, token and duration text, the file parser, settings parsing, the clear confirmation
  racing a focus change). Mutants that changed nothing observable were removed rather than killed, each by name in the commit history of
  `port/gen-mutations.py`: a cut at 79 where the string is cut at 80 anyway, a trim that the later normalization repeats, a
  lock-name dot-strip no path reaches, a project-root check that can differ only for the unported external root, and scheduler
  ownership and interrupted checks that the restore at session start makes unreachable in this slice. Four mutants that did
  not compile were rebuilt.
- Review sessions found three more defects:
  - the scheduler's implicit ready write at restore was missing;
  - RE2's `(?i)` matched U+017F and U+212A where JavaScript's `/i` does not (a Sisyphus objective was accepted, and a title
    was taken from the wrong line);
  - a number beyond the double range made the port drop a whole goal file or settings file.
- The sessions found four real defects the twins could not: the port had no scheduler restore; a write that leaves a goal paused
  also appends a `goal_paused` event; the original lets queued UI updates run between loading and restoring; and the wording of
  the allowance text for a nonzero limit.
- `pig install --validate-only --json` on the extension: valid (6 commands, 2 handlers). `pigeq gaps`: Go side 1 PARTIAL
  stand-in (`SetWidget`), 0 blocking; the original has 45 gaps, 1 blocking, in code that is not ported (`provider.streamSimple`,
  an experiment script).
- Piglet: `piglets/pig-popular` now selects four ports and `pig-0.4.0 piglet build` fuses them. The goldens equal the Binary's traces (`pigeq check --builtin`, 17 of 17) for a Binary built from this member alone; in the four-member Binary each scenario also sees the powerline member's startup events (`setStatus stash`), which the goal goldens do not contain, so that Binary cannot be checked against a single member's goldens (a porter finding: a fused Binary's traces need a per-member filter).
- tmux, detached, in an interactive pig 0.4.0 (`port/demo/pane-*.txt`): `/goal-direct`, `/goal-list`, `/goal-pause`, the clear
  confirmation, the cleared notice, with the status line below the editor.
- Size: 3482 hand-written Go lines (non-test), 2181 test lines (548 of them the Skill's fake-host template); nothing is generated except the recorded golden and the case
  files (`testdata/`).

## Performance

Measured on xd670 (Xeon Platinum 8468; the measured processes pinned to CPUs 64-71 inside a container limited to them, other jobs
running on the host: load average 14 at the end), 2026-10-20, 12 interleaved rounds after a warm-up round, medians. Raw numbers,
per-round values and the host: `port/perf/results.md`. Three subjects: the port (this branch), the previous release (0.1.1,
`origin/goal-honest-refusal`: the commands it implemented, refuses the rest) and the original under PiG's runtime (Node 24.19 and PiG
v0.5.0, Pi 1402973a for the pin).

Method (`port/perf/`, one command: `NEW=... OLD=... PIG=... OUT=... CPUS=64-71 bash port/perf/run.sh`, then `summarize.py`):

- **Size**: the extension as PiG builds and caches it (`cache/cells/go/<hash>/runner`), and a single-extension Piglet Binary
  (`build-piglets.mjs`; the shipped Piglets fuse the extension) against a control Piglet whose extension registers nothing.
- **In process** (`extensions/pi-goal-x/perf_bench_test.go`, `go test -bench -benchmem`; `bench-original.mjs` for the original on
  the same fixture and operations; `old_bench_test.go.txt` for 0.1.1): five goals, a twelve-task tree, the same inputs; a mutating
  operation runs on a freshly booted core over restored files, outside the timed region. The Go side times the core with the replay
  test's scripted host, so no SDK wire and no host; the original is driven the way its own tests drive it. B/op for the original is V8
  heap growth with no collection (a lower bound; not comparable with Go's allocated bytes). Rounds alternate port, original, 0.1.1.
- **Through a real pig** (`rpc.mjs`, RPC mode): cold start is the spawn of pig to the `get_commands` response (pig reads RPC after it
  has loaded its extensions; the control's span is pig alone). Tools run under a scripted OpenAI-compatible model, timed from
  `tool_execution_start` to `tool_execution_end`; commands (`prompt` "/name") from the send to the first notification. PiG hosts the Go port
  as a subprocess and the original as a Node subprocess, so both pay the same IPC.

Size and cold start (bytes, ms):

| | original | 0.1.1 | port | port over 0.1.1 |
|---|---|---|---|---|
| extension binary (PiG-built runner) | TypeScript, 0.6 MB of sources | 6,213,792 | 15,519,904 | +150% |
| single-extension Piglet Binary (control 70,861,063) | not a binary | 71,192,839 | 73,347,335 | +3.0% (+2.15 MB) |
| cold start, `pig -e <extension>` | 683.5 (first start 2,064: jiti) | 51.4 (first start 410: build) | 121.8 (first start 593: build) | +70.4 ms |
| cold start, fused Piglet (control 33.9) | not applicable | 40.9 | 50.1 | +9.2 ms |
| pig's own extension span, spawn to handshake | 532.0 | 15.0 | 19.0 | +4.0 ms |

The runner grew by 9.3 MB: `.rodata` +6.5 MB, of which 6.32 MB are the five word-segmentation dictionaries that `pitui` embeds
(`wordsegmenter/*.bin`, linked through `pitui.Editor`); `.text` +1.4 MB and `.gopclntab` +0.9 MB are the port's own code (1.11 MB of
symbols against 0.18 MB in 0.1.1; 0.1.1 only refused) plus `widthx`, `pitui`, `uax29`, `x/text` tables. **Owner decision needed:** the growth
over 10% is the scope of the port (0.1.1 had no tools, dashboard, scheduler or auditor) and a library cost; the fused Piglet, the
shipping vehicle, grows 3.0%. A `pitui` build tag that drops the dictionaries (PiG's `pigstrip` switch, removed from the copy) would
cut 6.3 MB from every port that links `pitui.Editor`; that is the `pitui` lane's change. The cold-start rise with `-e` is mostly
pig hashing and checking the larger source tree (`build-check` 21 ms against 4 ms, discovery 48 against 12), which a Piglet does not
do; in the fused Piglet the extension's own span is 3.0 ms against 2.0 ms and the whole start 9.2 ms over the control (0.1.1: 7.0 ms), above
the 1 ms of the registration allowance, so it is stated here: it is the larger Go program (more type data and package initialisers) and
the registration of 16 commands and 8 tools.

Per-call cost through pig (ms; port in the `-e` form, the fused form is about 10-20% lower):

| operation | original | port | |
|---|---|---|---|
| create_goal | 5.01 | 2.79 | -44% |
| set_goal_tasks | 5.01 | 4.15 | -17% |
| update_goal (pause) | 3.79 | 2.20 | -42% |
| get_goal / tasks | 2.94 / 3.24 | 1.74 / 2.31 | -41% / -29% |
| update_goal_task | 0.84 | 0.52 | -38% |
| /goal-list, /goal-status, status verbose, /goal-refresh | 2.62, 3.72, 4.06, 2.53 | 1.69, 2.70, 2.43, 1.94 | -36%, -27%, -40%, -23% |

0.1.1 has no tools; its commands `/goal-direct` and `/goal-list` are in the in-process table. In pig the port is faster than the original on every
operation measured, because the original's Node host costs more per call than the Go extension.

In process (median; port against the original, against 0.1.1 where it had the command): see `results.md` for B/op and allocs/op.

| operation | port | original | port/original | 0.1.1 |
|---|---|---|---|---|
| session_start (load, widget, input listener) | 506 us | 1.00 ms | -50% | 131 us (+286%) |
| get_goal summary / tasks | 65 / 150 us | 71 / 41 us | -9% / +270% | n/a (refuses) |
| create_goal / update_goal pause | 1.49 / 1.11 ms | 1.56 / 1.02 ms | -4% / +9% | n/a |
| set_goal_tasks / update_goal_task | 1.22 / 1.16 ms | 1.03 / 1.06 ms | +18% / +10% | n/a |
| /goal-list | 79 us | 12.6 us | +528% | 16.7 us (+373%) |
| /goal-status, verbose | 137, 142 us | 78, 90 us | +77%, +58% | n/a |
| /goal-refresh | 383 us | 111 us | +245% | n/a |
| /goal-direct | 1.43 ms | 1.39 ms | +3% | 1.14 ms (+25%) |
| dashboard render, compact / expanded (width 100) | 74 / 101 us | 67 / 91 us | +9% / +11% | n/a |
| terminal key, plain / toggle chord | 2.5 / 81 us | 17.6 / 10.5 us | -86% / +673% | n/a |
| scheduler tick (begin, ready declaration, settled) | 1.20 ms | 1.15 ms | +5% | n/a |
| 16,000-event ledger: session_start, /goal-status, update_goal_task | 1.01, 2.92, 1.34 ms | 1.28, 2.09, 1.06 ms | -21%, +39%, +27% | n/a |

Regressions over 10% in process and what is behind them (profiles with `go test -cpuprofile` on the benchmarks):

1. **Quadratic activity sort (fixed).** `/goal-status` on a ledger whose timestamps run backwards took 23.7 ms at 16,000 events;
   `activitySortStable` was an insertion sort. It is now `slices.SortStableFunc` with the original's comparator (3 ms; twin
   `TestActivitySortStableMatchesInsertionSort` against the old order, ties included).
2. **Dashboard laid out on every UI update (owner decision).** The SDK carries no component widget (G1), so the port lays the
   dashboard out at the host's width and pushes the lines; the original hands the host a component that the host renders per frame.
   Profile of `/goal-list`: 60% of its time is `pushWidget` and `Render` (about 60 us per update); the toggle chord is the same
   render. This explains the small commands (`/goal-list`, `/goal-refresh`, the toggle key) and about 60 us of every tool. The cost is
   bounded (flat over a 16,000-event ledger: 65 us), and the original's component is rendered on every host frame, which this
   benchmark does not count. In RPC mode the lines are dropped after the layout (G3); skipping the layout there would save the work and
   was not done because it changes `previousLinesLen` state the faithful twins read.
3. **`get_goal` tasks and the detail page cache (owner decision).** The original caches the compiled detail source by object
   identity; the port omitted the cache (`goal_detail.go`: "it only reuses pure results"). Profile: half of the call is
   `goalDetailSource` (marshalling every task). Repeated identical reads are 3.7 times the original's cache hit; paging a long history
   recompiles the source on every page. Porting the cache needs generation counters on the goal record (identity is not stable in
   the port), so it is listed rather than done.
4. **Ordered-object representation (owner decision).** Remaining differences on pure computation (`/goal-status` +77%, 16,000-event
   status +39%, `update_goal_task` +27% at 16,000 events, set_goal_tasks +18%) are the `jsObject` maps and the decoder that
   builds them (`parseJSON` over `encoding/json` tokens) against V8's `JSON.parse`; faithful key order and number text are why
   they exist (M7). The absolute differences are 10 to 300 microseconds per call, and through pig the port is faster.

No unbounded work or blocking host call on the render and input paths (read in `core.go`, `goal_widget*.go`, `extension.go`, the SDK
and PiG's `ui_bridge.go`): a render reads the settings cache and the bounded per-goal ledger projection and makes no host call (width and
height are fields the SDK keeps); the terminal-input listener is subscribed for the whole TUI session, PiG waits for its verdict on every
key with no deadline (`handleOnTerminalInput`; the original under PiG pays the same IPC), and the handler takes the core lock, so a key
waits for the unit of work in flight (the longest measured is 1.4 ms, a tool that writes files; a file lock held by another process
retries for up to 1 s). Every UI update makes one blocking `SetStatus` round trip (the widget push is one way). The sort above was the
only unbounded step found. Not measured: `update_goal` complete and the auditor and oracle (they start a child pig and a model call),
the tmux TUI frame cost, and per-event handlers (`context`, `before_provider_request`) whose cost is dominated by the SDK's
JSON of the payload; they are covered only inside the through-pig numbers.

## Findings (to file against PiG, and notes on the porter)

1. **G1 again: no component widget.** The dashboard is a component factory; the port shows one status line in a terminal and
   nothing over RPC (as Pi does).
2. **G3 again:** PiG's RPC mode emits factory-widget rows Pi drops.
3. **G12 again: no settings API for foreign files.** The four settings are read from the original's own files with the original's
   precedence (`PI_CODING_AGENT_DIR`, `PI_GOAL_*`).
4. **No registerMessageRenderer, no active-tools switch or prompt injection in the SDK.** These are what the goal tools and the
   drafting flow need; they are why the slice stops at the commands.
5. **No host event for the shutdown that precedes a new session.** The original relies on `session_shutdown` then
   `session_start`; a PiG `new_session` emits the pair, and the pool and focus state live in the extension process.
6. **The oracle depends on time.** The original reads directory mtimes for its pool snapshot; an external delete within the same
   file-system tick of the extension's own write is seen or not by timing. The sessions that did this were dropped; the
   lost-goal sessions remove the snapshot as well. The scheduler's pause event is stamped from the wall clock, not `Date.now`.
7. **Ids from `Math.random`.** The scenarios avoid generated ids; the sessions record the original's with a seeded random, and
   the tests inject the same ids.
8. **JavaScript semantics, again.** Ordered objects (integer-like keys first), `JSON.stringify(x, null, 2)`, UTF-16 length
   limits (an astral character counts 2), number text. `jsjson.go` and `jsutil.go` are copies from the powerline port: five ports
   now carry such helpers.
9. **Twin counting.** The original's `npm run test:unit` reports 1053 tests; `pigeq twins list` finds 1049 titles (repeated and
   dynamically built titles).
10. **RE2's `(?i)` is not JavaScript's `/i`.** RE2 folds U+017F onto s and U+212A onto k, which JavaScript does without the u
    flag. Every port that translates a case-insensitive JavaScript regexp to Go has this difference. `jsFoldSafe` hides the two
    characters before a yes/no match; a shared helper would fix it once for all ports.
11. **`JSON.parse` keeps going where `encoding/json` stops.** A number beyond the double range is ±Infinity or 0 in JavaScript;
    Go reports an error, and the port used to drop the whole file. The powerline port's copy of `jsjson.go` still does this.
    Still open: a lone surrogate escape (`"\ud800"`) in a goal file decodes to U+FFFD, so a rewrite of that goal writes U+FFFD
    where the original writes the escape back. The case is not in the sessions.
12. **The session branch crosses the SDK as Go maps.** `SessionManager().GetBranch` returns `[]map[string]any`, so the key order
    of a nested object in an entry is lost. That object is the scheduler of a legacy `pi-goal-state` goal. If such a goal is
    written back, its scheduler keys are in alphabetical order, while the original keeps the entry's order. Legacy entries
    predate the scheduler, so this is not expected in practice. An SDK accessor for raw entry JSON would remove the difference.
13. **One lock around each handler.** `app.with` holds a mutex for the whole command, dialogs included. A second event waits
    until the dialog is answered, so the original's "Goal changed while confirming" race cannot happen inside this process; it
    can only come from the files. The original's async handlers interleave there.
14. **The case generator once wrote outside its workspace.** `seed()` put a goal file at its metadata `activePath`, so the
    `pool-unsafe-paths` session wrote `../escape.md` into the system temp directory and tested an empty pool. Seeds now live at
    their own name. A porter check that the replay harness writes only under its case directory would catch this class of bug.

## Where a generated skeleton (`pig-codegen extension`) would have saved hand work

- Module, `Extension()`, command table and event wiring (6 commands, 2 handlers).
- The JS helper layer (ordered JSON, trim, number text, UTF-16 prefix): a library, not a skeleton, would remove roughly 375 lines
  copied between ports.
- The scripted-host driver, the case generator and the replay test: the same pattern as the powerline port; a skeleton for
  "invisible over RPC" ports would have saved the most here.
- The twin ledger (41 titles by hand) and the slice file (76 deferred entries) are tedious to produce by hand: `twins list`
  could write the first draft.

## Refusals (0.1.1)

Until the rest is ported (the inventory is in the owner's parity status file for this component), each unported command,
setting and mode is refused with `@pi-in-go/pigpen-goal (Go port) does not support <feature> yet. Install the original
extension, npm:pi-goal-x@0.32.3, which runs unchanged in PiG.` (`refusal.go`; one test per refused option in
`refusal_test.go`). The earlier notice that a goal "is stored and ready but will not start by itself" is gone: a goal that would
need a run is not created.
