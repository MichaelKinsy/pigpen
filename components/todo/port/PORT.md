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
| M7 | `todo-overlay.ts`: component widget above the editor, collapse key, width clip, hide completed on `agent_start` | `overlay.go`, `extension.go`: `ctx.SetWidget` with a kit view of verbatim rows (`ui.setWidget` with a view and `aboveEditor`), laid out again on a terminal resize (`OnWidthChange`), collapse shortcut | `overlay_test.go`, `widget_view_test.go`, `register_test.go`; the tmux runs |
| M8 | `config.ts` via `@juicesharp/rpiv-config` | `config.go` (XDG, legacy fallback, validation, read on every use) | `config_test.go` |
| M9 | `index.ts` session hooks: `session_start`, `session_compact`, `session_tree` (replay + refresh), `session_shutdown` (dispose), `tool_execution_end`, `agent_start`; the stale-context handling of `sid(ctx)` | `extension.go` (`ctxSid`: asks the context before the session read, as Pi's `ctx.sessionManager` getter throws when stale) | `extension_test.go`, `stale_ctx_test.go` (fake host), `new-session-resets` |
| M11 | `agent-loop.ts:619-647`: the calls of a parallel batch start in source order, the synchronous reducer commits in that order and the calls end in that order | `sequencer.go`, `extension.go` (`execute`, `onToolExecutionEnd`): a call waits for the end of the todo call ahead of it in the assistant message that is the session's leaf; arrival ticket when the leaf does not list the call | `TestParallelCallsCommitInSourceOrderWhicheverArrivesFirst` and the four cases beside it, `TestParallelCallsCommitInStartOrder`; every scenario with several calls in a turn |
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

## Port 2 (this revision): the component widget, the batch order, the stale context

Branch `port/todo-2`, stacked on the refusal PR (`refuse/todo-unported`). Against PiG 0.5.0+1.3.0.1402973a (SDK
source of PiG commit `6af0806f7a`), the original under Pi 1402973a.

- **Overlay (M7, was G1).** The SDK now takes a kit view as a widget (`Context.SetWidget(key, kit.View, options)`,
  `ui.setWidget` with `view`). The overlay goes out as one `kit.Lines` node of verbatim rows with placement
  `aboveEditor`, which the host draws like Pi draws a component widget: every row (the 10-row cap and the
  `... (widget truncated)` row belong to string[] widgets), no `Text(line, 1, 0)` padding or wrapping. With the cap
  gone the PR 1 refusal of `maxWidgetLines` above 12 is removed and the value is honoured (the default 12 content
  rows plus the spacer, as the original). A terminal resize lays the rows out again (`Context.OnWidthChange`, one
  subscription per bound overlay, removed at shutdown).
- **Batch order (M11, was G4).** See the mapping. The arrival ticket is kept as the fallback for a call the leaf does
  not list. Before this change the `bad-input` scenario (4 parallel calls, first batch of a cold process) diverged
  from Pi in 1 of 17 first `pigeq run` runs and in 1 of 40 `pigeq check` runs (two adjacent calls swapped); see
  Results for the rate after.
- **Stale context (M9, was finding 8).** The SDK throws the host's stale message from its own getters (`Context.Cwd`,
  `Mode`, `HasUI`; PiG's default message is Pi's text, `inproc/runner.go` `defaultStaleMessage`) but answers session
  reads regardless, so `ctxSid` asks the context first, as Pi's `ctx.sessionManager` getter throws. The teardown
  clears the widget through the sink captured at bind time (the original clears through its captured `uiCtx`).
  Five fake-host cases (`stale_ctx_test.go`); not reachable in a scenario (no RPC way to hold a stale ctx).
- **Goldens.** All 16 goldens were recorded again from the original under Pi 1.3.0.1402973a (`pigeq record`): only
  the header line changed (`host`, `extension` hash), the traces equal the Pi 1.0.0 ones. New scenario
  `max-widget-lines-above-twelve` (config written by a `setup` command into the lane HOME).

**Differences from the original (also in README):** locales are not ported (owner decision pending, below); a change
of the theme or of Pi's tool-output expansion shows in the overlay at the next refresh, not at once (SDK items
below); metadata key order in the details and in the replayed state follows Go's sorted maps where JS keeps
insertion order (SDK item below).

**Exclusion E1 (written owner approval not located): locales.** `@juicesharp/rpiv-i18n` (2.13.9 inspected) resolves
the locale in process: `--locale`, `~/.config/rpiv-i18n/locale.json`, `LANG`/`LC_ALL`, else English, and `/languages`
switches it live through `globalThis[Symbol.for("rpiv-i18n.runtime")]`, which a Go extension process cannot read.
The 9 locale files of this extension are 7 KB. PORT.md recorded "English only" as an owner ruling; the program
requires it in writing. **Question for the Tern lead:** (a) approve English-only in writing (and say whether a
notice is wanted when `~/.config/rpiv-i18n/locale.json` exists), or (b) have the port embed the 9 locale files and
resolve the locale at `session_start` from `locale.json`/`LANG`/`LC_ALL` (no live `/languages` switch without an SDK
channel from rpiv-i18n)? Not ported meanwhile, as instructed.

**Host and SDK findings:**
1. **G1 closed** (kit view widget). Remaining differences: the widget is not re-rendered per frame (below).
2. **G2 No pi-tui text helpers** (`visibleWidth`, `truncateToWidth`, `wrapTextWithAnsi`): reimplemented per
   rune in `text.go`, tested against the original's own width cases.
3. **G3 PiG's RPC mode emits the rows of a factory widget; Pi's RPC mode drops them** (PiG core: Pi emits a bare
   `setWidget{widgetKey}` on a clear only). The host check pig-ts vs pi-ts fails on every scenario that creates a
   task (the 17 `run` results: 15 differ at the first widget event, `command-empty` and `guidance-in-request`
   pass). The port draws nothing in `rpc` mode (it still clears on `session_shutdown`), which is what the golden
   shows (`rpcSink`). The `errors` host check also differs in the order of two parallel results in PiG's Node
   runtime (a tool-end order difference of the PiG Node host, not of the port). Both are PiG-vs-Pi differences, reported, not normalized.
4. **G4 narrowed**: the commit and end order no longer depend on the SDK's handler start order when the host's
   session leaf is the assistant message (it is in PiG 0.5.0: the `bad-input` and `many-tasks` runs below). The
   arrival ticket remains for hosts or batches where the leaf does not list the call.
5. **G5 PiG's request body escapes U+2028/U+2029** in tool-call arguments where Pi's `JSON.stringify` does
   not; the scenario avoids them (PiG core).
6. **G6 One host round trip for the session id** on every tool call (two since the batch order reads the leaf
   entry); Pi reads it synchronously. SDK item below.
7. **G7 Compaction is not reachable over RPC with a small session** ("Nothing to compact"), so there is no
   compact scenario; the compact handler is covered in the fake host.
8. **Harness:** `pigeq record` writes the golden but exits non-zero when the host check fails, so a known host
   difference cannot be accepted; the 15 host-check failures above are expected. The new scenario's golden has a
   `llm`/`turn_end` arrival race for 25 parallel calls (see Results).

**SDK requests (also in `core-items.md`, C3+):** (a) `Context.OnThemeChange`: a kit.Lines widget keeps the colors of
its last push, Pi calls the component's `invalidate()` on a theme change and the original reads `uiCtx.theme` at
every render; (b) `Context.OnToolsExpandedChange`: the original reads `ui.getToolsExpanded()` at every frame and
Pi redraws on its expand key; (c) `Context.SetWidgetRenderer(key, render(width), options)` like `SetFooterRenderer`
(rows tagged with their width, the host never paints rows laid out for another width), mirroring
`setWidget(key, factory, {placement})`; (d) ordered input: `ToolFunc` params and `SessionManager` entries are Go
maps, so JS insertion order of `metadata` keys (details of the result, replayed state) is lost; Pi keeps it
(`Context.ToolParams() *OrderedObject` or raw JSON, and raw entries); (e) a synchronous session id
(`ctx.sessionManager.getSessionId()`; the SDK mirror already knows it); (f) per-call batch index
(`ctx.ToolCallIndex()`) so no leaf read is needed to order a batch.

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
