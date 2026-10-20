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

**Twins:** 1048 exact twins and 1 named skip of 1049 upstream titles (`pigeq twins check`; the skip is the real-SDK prompt cache e2e), `slices.json` is empty. **Go tests:** `go vet`
on linux, windows and darwin and `go test -race -count=3` pass on the merged tree. **Mutations:** `mutations.json` holds 1983 mutants (every find
text occurs exactly once). The first `pigeq mutate --unit-only --jobs 10` run over the 2034 original mutants killed 1241, left 740
alive and found 53 that do not build. The survivors got tests (`mutants_a_test.go` to `mutants_d_test.go`, plain tests, not twins);
the 53 invalid mutants were rewritten (51) or dropped (2: `health-marker-version-required`, `draft-unavailable`, both equivalent or
unreachable); 51 building mutants are equivalent (same observable behavior) and are listed with a reason in
`mutations-equivalent.json`, outside the run list. The rerun of only those lanes' survivors and rewritten mutants left no survivor
outside that list: slice A 181 mutants (5 equivalent listed), B 214 (19), C 207 (22), D 154 (7). Two Go bugs the survivors exposed
were fixed: `Date.parse` of `T24:00:00` (`jsDateParse`) and the blocker oracle wait holding the core lock. The lead faithfulness review (five
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

**Completion auditor and provider outages (pigeq):** nine scenarios, `audit-*` and `net-*` (the GoalScenAuditNet block of `gen-scenarios.py`), all recorded from the original under Pi 1402973a; the port equals the original on every one, and the two named skips for `tests/e2e/network-recovery-rpc.test.ts` are replaced by Go twins (`goal_events_e2e_test.go`, on the rig's mock clock) plus the live scenarios.

- Auditor: the original runs the auditor on an in-process nested session, the port on a child `pig --mode json` that inherits the lane's agent directory, so it reaches the harness's scripted model server and its requests enter the same trace (messages, the five read-only tool definitions, the auditor's system prompt as the delta against the parent's). `audit-approved`, `audit-rejected-then-approved` (feedback in the tool result, the next turn's `[AUDITOR REJECTION]` block, the second audit's warm context), `audit-tool-calls` (read, grep, ls inside the audit) and `audit-no-marker`. The goal is seeded and auto-selected because `create_goal` ends the run: no scripted parent turn follows it. The scenarios run under `--system-prompt`: the auditor's prompt replaces the host's whole prompt, so the harness records the host's baseline wording as the removed text, and Pi and PiG word it differently; a scenario-chosen base prompt makes that text equal.
- Difference, not fixable in the port: the original's `update_goal(complete)` returns the completion commit result, which carries `ok: true` beside `content`, `details` and `terminate`, and Pi forwards `ok` in the RPC `tool_execution_end` result. `sdk.ToolResult` has no field for extra properties (SDK request: extra tool result properties). It is not visible to the model; the three scenarios that end in an approval do not capture `tool_execution_end` (the same result stays compared as the `toolResult` message, and `tool_execution_start` as the call).
- Outages: a scenario server answers 503, 429 or 401 on the provider's route (`net-503-ladder`, `net-429-ladder`, `net-503-recovers`, `net-429-allowance`, `net-401-not-recovered`); the model is not scripted and the request bodies enter the trace. `message_end` is not captured because the recovery checkpoint's `details.timestamp` is a JSON number (`Date.now()`) that the normalizer's string rules do not reach; the checkpoint stays visible as the marker message in the next request body and as the run it starts.
- Finding about the original under Pi 1402973a (the port equals it): a recovery checkpoint reaches `before_agent_start` as a custom trigger with no `prompt`, so the original's `extractGoalIdFromInjectedMessage(event.prompt)` finds nothing, treats the run as user driven and resets the recovery counter. A live outage therefore announces `5s (recovery 1)` every time; the ladder never escalates and `networkRecovery.maxAttempts` never exhausts. The original's own e2e asserts escalation and cannot hold against this Pi. The Go twins assert the escalation on the event shape the e2e expects (the marker as the prompt); the scenarios pin the live behavior.
- Host checks: with the base prompt the original under PiG's Node runtime equals Pi on the outage scenarios apart from the factory-widget rows (G3). On the audit scenarios it differs because the nested `createAgentSession` of PiG's Node runtime fails (`Auditor error: this._modelRuntime.hasConfiguredAuth is not a function`): a PiG core finding for the Tern lead, not an extension difference.


**pigeq, tasks and scheduler (GoalScenTasksSched):** `tools-tasks`, `tools-tasks-edge`, `tools-tasks-disabled` and nine `scheduler-*` scenarios
(`gen-scenarios.py`, block `GoalScenTasksSched`; goldens recorded under Pi 1402973a). The earlier note that `set_goal_tasks` never asked its select
dialog under Pi was a scenario error: `create_goal` ends the model's turn (`terminate: true`), so the scripted `set_goal_tasks` call answered the next prompt
and never ran. Under Pi's RPC mode `ctx.hasUI` is true and `ctx.mode` is `"rpc"` (`rpc-mode.ts:319-321`, `runner.ts:572-574`, `:628-630`), so
`showTaskConfirmation` skips `ui.custom` and asks `ui.select("Task list confirmation\n\n<proposal>", ["Confirm task list", "Keep current tasks"])`; PiG binds the
same (`coding/cli/rpc_mode_runtime.go:196`, `coding/extension/host/inproc/runner.go:736-741`), the original under PiG asks the identical select, and the Go port
(`goal_task_confirmation.go`) asks it too: no difference. Nine of the twelve scenarios pass `pigeq check` (3 runs in a row). Findings, all PiG against Pi, none normalized
except where stated:

- **PiG core bug, first run.** A run that an extension starts with `pi.sendMessage(msg, {triggerTurn: true, deliverAs: "followUp"})` as the *first run of a session* sends the
  model no system prompt under PiG (Pi sends it). Reproduced without the goal extension (a one-tool probe; `before_agent_start` and `turn_start` still see a 2715-character
  `ctx.getSystemPrompt()`): the request's system text is empty for that run and correct for every later user-started run. Scenario `scheduler-direct-first-run` (the kickoff run of
  `/goal-direct` is the first run) therefore differs from the Pi golden in the llm channel's `system` field (`inserted ""`, `removed <host prompt>` against the tool snippets).
- **PiG core bug, stale snippets.** A run started by `sendMessage(triggerTurn)` after a previous run's tool changed the active tools (`pi.setActiveTools` in `execute`, what `create_goal` does) sends the
  tool list correctly but the system prompt of the previous run: the new tool's snippet is missing (probe: `late_tool` absent from the system text, present in `tools`). A user-started run is
  correct. Scenario `scheduler-budget` (create_goal, then the scheduler's continuation run) differs the same way (`update_goal` and `set_goal_tasks` snippets missing) and in the same request's `Context snapshot` figure (733 against 1200; not explained, not masked).
  The other scheduler scenarios keep the goal focused before the first run and start with a user prompt, so no run changes the tool set and they pass.
- **D22 consequence, masked (`CTXEST`).** The extension prints the host's context estimate (`Context snapshot: N/100000 tokens`). Before the model has reported a usage it includes the host's system prompt, whose wording differs between
  the hosts (D22; 2666 against 2715 characters in the probe, `pi` against `pig` in the first line and the documentation block), so the figure differs by 12 tokens (2527 against 2539). Only estimates of four or more digits are masked; the figures after a model reply (the
  scripted usage) are compared exactly.
- Epoch milliseconds (the checkpoint message's `details.timestamp`, the scheduler's `claimedAt` and `nextCheckAt` in goal tool results) are different in every run and no normalize rule can reach a number, so the scheduler scenarios capture
  neither `message_end` nor `tool_execution_end`; the model request, the notifications and `tool_execution_start` still show every continuation prompt, scheduler message and tool call.
- Existing scenarios `tools-create-get`, `tools-blocked` and `tools-pause-and-bad-args` script turns after `create_goal` that are never requested (the turn ends); they only exercise `create_goal`.
**pigeq, commands (35 scenarios in the `Remaining commands` block of `gen-scenarios.py`):** `/goal-status` (standard, verbose, health), `/goal-focus`, `/goal-settings` (every row kind: boolean, integer, thinking level, model selector with manual entry, Blocker Oracle rows, `disableTasks` changing the next prompt's tool list), `/goal-resume`, `/goal-cancel`, `/goal-recovery` (+ `repair`, declined and confirmed, malformed files, dead and unreadable locks, an orphaned snapshot entry made by the scripted model's `rm`), `/goal-refresh` (pool and ledger changes made by the scripted model's `cp`), `/goal-tweak`, and the guided `/goal` and `/sisyphus` flow with scripted model turns and dialog answers (`goal_question`, `goal_questionnaire`, `propose_goal_draft`: confirm, continue chatting, cancel, mode mismatch, empty objective, budget, tweak of an active and of a paused goal). Goals are seeded files with fixed ids; goldens are recorded under Pi 1402973a. Rules beyond `HOSTDOCS`/ids: `NOW` (wall-clock time of runtime ledger events; seeded events use 2020), `AGE`/`BACKUP` (lock ages and the repair backup stamp), and `CTX`: the goal snapshot quotes `ctx.getContextUsage()`, an estimate over the whole prompt, which differs by a constant 12 tokens between the hosts because of the same D22 docs block, so only the number and percentage are masked.
Not exercised over RPC: Esc pausing a running goal (`ctx.ui.onTerminalInput` does nothing in RPC mode; twins: `TestGoalModalEscape`, goal-widget twins "escape still pauses a running goal when the dashboard is compact", "Escape with no goal modal open pauses the goal"); the rich terminal questionnaire/proposal dialog (Pi's RPC `ui.custom` resolves to `undefined`, so the original uses its per-question select/input dialogs, which is what the scenarios record; the rich dialog's twins are the goal-questionnaire and goal-rpc-dialogs twins); the settings branch of `/goal-refresh` (the extension re-reads settings on every turn, so an edit made by the scripted model is already loaded when the command runs; twin: the `diffGoalRefreshState` twin).
**Open:** `/goal-resume` with a nonzero autonomous allowance (continuation turn): under PiG the Go lane's first model request of the resumed run reached the scripted server with the base system prompt removed (system delta empty), 3 of 3 runs, where Pi's carries the extension's tool list; not understood, no scenario recorded. Two adapter fixes came out of the scenarios (twins in `goal_send_user_message_test.go`): `SendUserMessage` releases the core lock (PiG's host prepares the run inside the call and waits for the extension's own `before_agent_start`, where Pi's call returns first: the guided `/goal` deadlocked), and a dialog now emits its request before the handler's queued UI refresh, as Pi's RPC mode does (the SDK has no after-send hook, so the refresh runs when the dialog answers or after 20 ms).

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
| extension binary (PiG-built runner) | TypeScript, 807,526 bytes of sources (`du -sb`) | 6,213,792 | 15,519,904 | +150% |
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

Per-call cost through pig (ms; port in the `-e` form, the fused Piglet form is between 24% lower and 4% higher, see `results.md`):

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
