# Port record: rpiv-todo

| Input | Identity |
|---|---|
| Original | `@juicesharp/rpiv-todo` 2.11.0, `packages/rpiv-todo` of https://github.com/juicesharp/rpiv-mono, commit `61904e69e1a50e12585bdf15f0310e633a62ba36`, MIT (juicesharp) |
| Oracle | Pi 1.0.0, Node 24.19.0, with the unmodified package at `port/oracle` (dependency `@juicesharp/rpiv-config` 2.12.0 from npm; its `config.ts` is identical to the pinned commit's) |
| Target | PiG 0.4.0+1.0.0 (build `76022638`), go1.27.1 |
| Original's tests | 231 cases in `port/oracle/**/*.test.ts` (vitest), ported as twins: 209 exact, 22 named skips (below) |
| Kind | 1: an extension, no CLI built-in, no provider |

## Mapping

| ID | Original | Go | Scenario / test |
|---|---|---|---|
| M1 | `todo.ts`: tool `todo` with a TypeBox schema, label, description, promptSnippet, promptGuidelines | `types.go`/`extension.go`: `ToolDefinition` with the same JSON Schema (incl. `Record` as `patternProperties`) and the same text, `promptGuidelines` | every scenario; `guidance-in-request` (model request carries the tool definition and guideline text) |
| M2 | `state/reducer.ts` (create, update, list, get, delete, clear, cycle rejection, `blockedBy` validation, no-change detection) | `reducer.go` | `create-update-list`, `errors`, `blocked-by`, `no-change`, `delete-clear`, `metadata`; `reducer_test.go` (twins) |
| M3 | `state/store.ts`, `state/replay.ts`: state rebuilt from the last `toolResult` of tool `todo` on the branch | `store.go`, `replay.go` (`ctx.SessionManager().GetBranch`) | `new-session-resets`; `store_test.go`, `replay_test.go` |
| M4 | `tool/envelope.ts`, `tool/sanitize.ts` | `envelope.go`, `sanitize.go` | `sanitize`, `bad-input`; `envelope_test.go` |
| M5 | `view/format.ts`: result text, `renderCall`, `renderResult` | `view.go` (`RenderCall`, `RenderResult` of `ToolDefinition`) | scenarios (result text); `format_test.go` |
| M6 | `/todos` command with groups, empty and per-status variants | `RegisterCommand("todos")` | `command-empty`, `command-groups`, `command-only-deleted`, `command-pending-only`, `command-completed-only`, `many-tasks` |
| M7 | `todo-overlay.ts`: widget above the editor, collapse key, width clip, hide completed on `agent_start` | `overlay.go`: `ctx.SetWidget` with pre-rendered rows, collapse shortcut | `overlay_test.go`, `register_test.go`; the tmux run |
| M8 | `config.ts` via `@juicesharp/rpiv-config` | `config.go` (XDG, legacy fallback, validation, read on every use) | `config_test.go` |
| M9 | `index.ts` session hooks: `session_start`, `session_compact`, `session_tree` (replay + refresh), `session_shutdown` (dispose), `tool_execution_end`, `agent_start` | `extension.go` | `extension_test.go` (fake host), `new-session-resets` |
| M10 | Locales via `@juicesharp/rpiv-i18n` | **not ported** (exclusion E1, below) | 3 named skips |

Twins: `pigeq twins check --ledger port/upstream-tests.json --go extensions/rpiv-todo` lists all 231 upstream
titles: 209 exact twins. The 22 skips, each with its reason in the ledger: 8 for the lazy overlay import and its
jiti failure modes (Go has no lazy module graph), 1 for the npm `files` manifest, 2 for localized lookups (E1),
5 for the shape of the i18n bridge and entry imports (TypeScript source text), 2 for the JavaScript module
loader, 1 for a null session id (a Go string cannot be null), 2 for `requestRender` and `setUICtx` identity
(a subprocess extension has no TUI handle).

## SDK gap check (`pigeq gaps --ts port/oracle`)

3 gaps, 0 blocking, all PARTIAL: `tool.promptSnippet`, `tool.renderCall`, `tool.renderResult`. The first did
not bite: the `guidance-in-request` trace shows PiG and Pi send the same tool definition. `tool.renderCall` and
`tool.renderResult` return lines at the requested width, since a Pi Component cannot cross the process
boundary. rpiv-todo 2.11.0 does not call `registerToolRenderer`, so the PiG 0.4.1 `Extension.ToolRenderer`
(D89) is not needed and the 0.4.0 SDK used here lacks it.

## Results (this revision)

- `go test -race ./...`: pass, also at `-count=24`.
- `pigeq check` against the golden traces **recorded from the original under Pi 1.0.0** (not self-recorded):
  16 of 16 scenarios, plus the `port-gaps` and `exec-coverage` checks; three runs, identical. (15 at `c2d58c9`;
  review added `bad-input`: an ESC before a line break, and ids no task can have, which Pi 1.0.0 formats as
  JavaScript formats numbers; both differed in the port and were fixed.)
- `pigeq mutate --unit`: 105 mutations (101 at `c2d58c9`, 4 added in review for the sanitizer and number
  formatting), all killed (first run: 89 killed by scenarios or unit tests, 6 survived
  and 5 did not build; each survivor got a test and each invalid mutation was rewritten; the rerun is clean).
  Killed by the scenarios only: `end-order-released-at-return` (3 of 3 runs) and `rpc-draws-widget`. The
  sanitizer mutations are killed by the unit tests as well as by the `sanitize` scenario. Killed by the unit tests only: the stale-state, host-failure,
  session-isolation and ordering mutations (`TestParallelCallsCommitInStartOrder`, `TestSessionIsolation`,
  `TestToolReportsAHostFailure`, `TestRenderTodoResultEchoesTheRequestedStatusOfARejectedUpdate`,
  `TestOwnerAloneIsAMutation`, `TestOverlayExactSummaryAndClear`, `TestTruncateToWidth`).
  `port/gen-mutations.py` generates `port/mutations.json` and checks that every find string is unique.
- `pig install ... --validate-only --json`: valid (tool `todo`, command `todos`, shortcut, 7 handlers).
  `pig package validate components/todo`: valid.
- Red commit `9dd3994`: 272 failing, 18 passing (stubs returning zero values), 23 skipped (a review rerun of
  `go test -json`; 25 was reported); `port/red-run.log` keeps the failures only.

## Differences and findings

**Differences from the original (also in README):** locales are not ported; the overlay is pre-rendered rows
(at most ten host rows, so nine task rows with the default 12, not re-laid-out on resize); metadata key order
in a result follows Go's sorted maps where JS keeps insertion order (not observable in the traces, which sort keys).

**Exclusion E1 (approved by the owner: English only): locales.** `@juicesharp/rpiv-i18n` translates the tool's
text when installed. Go has no counterpart and the translations are 9 locale files under `port/oracle/locales`. The port is
English only, as the owner ruled; the 3 related twins are named skips that cite the ruling.

**Host and SDK findings (to file against PiG):**
1. **G1 No component widget in the Go SDK.** `SetWidget` takes lines; the host truncates to 10 rows plus a
   "... (widget truncated)" row. Needs a `SetWidgetRenderer` like `SetFooterRenderer`.
2. **G2 No pi-tui text helpers** (`visibleWidth`, `truncateToWidth`, `wrapTextWithAnsi`): reimplemented per
   rune in `text.go`, tested against the original's own width cases.
3. **G3 PiG's RPC mode emits the rows of a factory widget; Pi's RPC mode drops them** (it emits a bare
   `setWidget{widgetKey}` with no lines). The host check pig-ts vs pi-ts therefore fails on every scenario that
   creates a task. Pi's golden is still the oracle; the port draws nothing in `rpc` mode (it still clears on
   `session_shutdown`), which is what the golden shows, and the overlay is covered in the fake host and the tmux run.
4. **G4 Parallel tool calls end out of order.** A Go tool's `tool_execution_end` events arrive in completion
   order; Pi's synchronous TypeScript tool ends in start order. The port takes a start-order ticket
   (`TestParallelCallsCommitInStartOrder`) and holds each call's slot until its own end event
   (`agent_end` releases leftovers). Cost: a call waits for the previous one's end event. The ticket relies on
   the SDK starting handlers in arrival order, which the SDK itself documents as having a residual window
   (`tool_start_order.go`: a handler thread preempted before its first statement can be overtaken), so the
   commit order is not guaranteed under preemption; the fix belongs in the SDK.
5. **G5 PiG's request body escapes U+2028/U+2029** in tool-call arguments where Pi's `JSON.stringify` does
   not; the scenario avoids them.
6. **G6 One host round trip for the session id** on every tool call; Pi reads it synchronously.
7. **G7 Compaction is not reachable over RPC with a small session** ("Nothing to compact"), so there is no
   compact scenario; the compact handler is covered in the fake host.
8. **Stale context:** the original treats "stale after session replacement" errors as a no-op; the Go SDK has
   no such error that I could find, so the predicate matches Pi's message only (unverified).
9. **Harness:** `pigeq record` writes the golden but exits non-zero when the host check fails, so a known host
   difference cannot be accepted; the scenarios were kept clear of them where possible (the widget difference G3 cannot be avoided: the
   host check is expected to fail on the scenarios that create tasks).

## Run in pig 0.4.0 (tmux, detached)

`scripts/tmux-demo.sh` runs the extension in a real interactive `pig-0.4.0` (private HOME, PiG home and agent
directory) in a detached tmux session, against the equivalence harness's scripted model (`pigeq llm`), types
into it and captures the screen. The model creates three tasks, then completes one and starts another
(`port/demo/turns.json`); the captures are in `port/demo/`:

- `pane-overlay.txt`: the tool call and result lines (`renderCall`, `renderResult`) and the overlay
  `● Todos (1/3)` with its tree rows;
- `pane-todos.txt`: after `/todos`: the Pending, In Progress and Completed groups;
- `pane-collapsed.txt`: after the collapse key (`alt+o`, set through the config file): the overlay folds to
  `● Todos (1/3)` and `alt+o to expand`.

```sh
DEMO_CONFIG_FILE=.config/rpiv-todo/config.json DEMO_CONFIG='{"collapseKey":"alt+o"}' \
  PIG_BIN=<pig-0.4.0> PIGEQ=<pigeq> scripts/tmux-demo.sh todo components/todo/extensions/rpiv-todo \
  components/todo/port/demo/turns.json 'type:plan the work' wait:3 grab:overlay 'type:/todos' grab:todos key:M-o grab:collapsed
```

## Piglet build

`piglets/pig-popular` selects this Package (`todo: local:../../components/todo`, extension `rpiv-todo`, tool
`todo`). `npm run stage` stages it; `pig-0.4.0 piglet build dist/staged/piglets/pig-popular/piglet.yaml --format
binary --targets linux/amd64` (with `PIG_SOURCE_ROOT` at a checkout of the PiG source) reports "Preparing fused
Go members — rpiv-todo" and "Checking fused Go members" and builds a 61 MB Binary in 4 s.
(`npm run build:piglet` refuses to run here: `scripts/pig-requirement.json` wants pig 0.3.x.)

The Binary was run against the scenarios with `pigeq check --builtin`. Against the Pi goldens that fails at the
first `llm` event of each scenario, as the Skill anticipates: the Binary has PiG's default tool set (it adds
`powershell`, `grep`, `find`, `ls`) where the oracle run uses Pi's four tools, and in builtin mode there is no
baseline run without the extension, so the system-prompt delta is absent. The comparison that matters was made
differently: the Binary recorded its own traces (`record --self --builtin`, `check --builtin`: 15 of 15 pass; 16 of 16 in the review rerun),
and those traces equal the Pi goldens in **every event outside the `llm` channel** (15 of 15 scenarios, 16 of 16 in review, event
by event: host UI calls, tool results, commands, responses). The `llm` events differ only by the tool list.
