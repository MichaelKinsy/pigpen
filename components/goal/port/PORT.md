# Port record: pi-goal-x (partial: goal storage and the direct lifecycle)

| Input | Identity |
|---|---|
| Original | `pi-goal-x` 0.32.3 by tmonk ("Copyright (c) 2026 Lucas"), https://github.com/tmonk/pi-goal-x, commit `64c5ace87f3400c0b55ef87f2d2912167f53dbaa`, MIT (its LICENSE file, kept at `port/oracle/LICENSE`) |
| Oracle | Pi 1.0.0, Node 24.19.0, the unmodified package at `port/oracle` (dev dependencies `@earendil-works/pi-*` 1.0.0 from its own `package-lock.json`); its own test suite passes there (1053 tests, 83 files; one goal-files test fails if a file `escape.md` exists in the parent of the system temp directory, which the old `pool-unsafe-paths` session used to leave there) |
| Target | PiG 0.4.0+1.0.0, go1.27.1 |
| Original's tests | 1049 titles in 83 files. Slice: 45 titles in 8 files, 41 exact twins and 4 named skips; the other 1004 (75 files) are deferred by name in `port/slices.json` |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope: what is and is not ported

pi-goal-x is a runtime, not an extension of one feature: a durable scheduler that starts and continues model runs, an auditor
child session, goal drafting through LLM tools and a questionnaire, a dashboard widget, 16 commands, goal and task tools, layered
settings, network recovery. Porting all of it is a project of its own; this port takes the part that can be checked against the
original without a model: the files and the commands that act on them directly.

**Ported:** the goal record and its normalization; the goal file (Markdown with a JSON header, written byte for byte as the original
writes it, key order included); the pool of open goals and its snapshot; locks; the event ledger (append); archiving; session
start with the focus entry (and legacy state); the scheduler restore at session start (a goal this session owns whose allowance,
token budget or wait deadline is spent is paused, an interrupted one is paused, one owned by another session is reported, and one
left ready for a repair run is first written back as plain ready); the
commands `/goal-direct`, `/sisyphus-direct`, `/goal-list`, `/goal-pause`, `/goal-clear`, `/goal-unfocus`; the persist on
`session_shutdown`; the settings `maxAutonomousRuns`, `disableContracts`, `autoSelectSingleGoal` and `strictExecutionContract` (project
file, agent-dir file, `PI_GOAL_*` environment).

**Not ported** (each deferred test file is named in `port/slices.json` with this reason): `/goal` and `/sisyphus` drafting (the
LLM-driven flow, the questionnaire, confirmation); the scheduler's dispatch, continuation, waits and timers; the auditor; the goal
and task tools (`get_goal`, `create_goal`, `update_goal`, the task tools); the dashboard, widgets and overlays; `/goal-resume`,
`/goal-focus`, `/goal-settings`, `/goal-tweak`, `/goal-refresh`, `/goal-recovery`; usage and time accounting (usage stays 0);
`mergeFocusedGoalWithDisk`; ledger reading, checkpoints and recovery; the external `goalsRoot`; settings beyond the four keys and
the original's settings cache (the port re-reads the file on each call); prompt cache and compaction handling; network recovery.
With an allowance other than 0 (the default is none), the original would start a run; this port stores the goal as `ready`
and says so in a warning. It does not pretend to continue. The scenarios and sessions below use `maxAutonomousRuns=0`, where the
original too starts nothing.

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
