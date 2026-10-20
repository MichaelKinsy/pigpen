# Port record: pi-subagents (partial: definitions, discovery, list and get, single-agent and chain runs, background runs, the workflow sandbox)

| Input | Identity |
|---|---|
| Original | `pi-subagents` 0.73.1 by Nico Bailon, https://github.com/nicobailon/pi-subagents, commit `8a403efba6975988cc0488ec8bb941db5ef1a19e`, MIT (its LICENSE file) |
| Oracle | Pi 1.0.0, Node 24.19.0; the original, unmodified except one image (`port/oracle/UPSTREAM.md`; the shim's `dist/` and `node_modules/` under `test/fixtures` are committed with `git add -f`, past this repository's ignore rules); its own suite: 3442 tests, 3428 pass, 13 skipped, 1 fails (`profiles helpers`, the same in a pristine clone) |
| Target | PiG 0.4.0+1.0.0, go1.27.1 |
| Original's tests | 3317 titles in 267 files (102,676 lines of source). Slice: 41 exact twins in 5 files; the other 264 files (and the untwinned part of two of them) are deferred by name in `port/slices.json` |
| Kind | 1: an extension, no CLI built-in, no provider |

## Scope: what is and is not ported

The original is a runtime for delegation: an executor of 7,900 lines, a background runner of 5,100, scripted workflows that run
a JavaScript workflow script, council mode, worktree isolation, fleet and status views, a watchdog, missions, an intercom
and supervisor channel, profiles and settings overrides. Its package manifest also ships skills and prompt templates. This port
takes what can be checked against the original by running it, and one execution path that PiG can do without Node.

**Ported (checked against the original)**

- the frontmatter parser (block scalars, quoted values, lists) and the markdown chain format (`.chain.md`: parse and write, tool
  budgets, outputs, reads, skills, packages);
- agent definitions: discovery in the project (`.agents` and `.pi/agents` of the project root, `.pi/chains`), the user's
  directories (`~/.agents`, `<agent dir>/agents`, `<agent dir>/chains`, `PI_SUBAGENT_EXTRA_AGENT_DIRS`) and the builtin set (the
  original's thirteen agent files, embedded byte for byte), precedence project over user over builtin, the project-root rule
  (the nearest `.pi` or `.agents`, never the home directory; `projectRootResolution` from `.pi/settings.json`), packages in
  frontmatter, aliases, name resolution, diagnostics for a malformed file;
- the tool the model sees: `subagents_enable` (the `subagent` tool is not offered until it is called), `subagent` and the two
  tools the original registers beside them, with the original's descriptions and JSON Schemas (read from the request the original
  made, `port/gen-tooldefs.py`); `subagent {action: "list"}` and `{action: "get"}` with the original's text;
- missions (`mission_*.go`, 28 twins of `mission-store`, `mission-lifecycle` and `mission-goal-driver`): the mission store and its
  global index, retention, the workflow state file and its lock (also offered to a workflow script's `state` global through
  `WorkflowStateStore`), the seven `mission.*` actions, the `mission` and `missionId` launch parameters of a single-agent run
  (every task launch records a mission unless `config.missions.enabled` is false or `mission: false`; the result text ends
  with `Mission: <id> (<status>)`), the completion of a mission from an async result file, and the goal-mission notice after
  each turn (`agent_end`);
- `/prompt-workflow` (`workflow_prompt.go`, 4 twins): template discovery (package, user, project), argument substitution and the
  generated workflow script;
- the startup behavior the trace shows: the git root probe and the clear of the `subagent-async` widget.

**An addition, not a port: running.** The original runs a child through its own executor, scripted workflows and (in
Pi) in-process child sessions. The port starts a child `pig --no-extensions --print` with the agent's model, thinking,
tools and system prompt (`--system-prompt` or `--append-system-prompt` by `systemPromptMode`), as PiG's own subagents do:
`subagent {agent, task}` for one agent, and `/run-chain <chain> <task>` for a `.chain.md` chain (steps in order, `{task}` and
`{previous}`, the first failing step stops it, every agent is resolved before any child starts). The original's trace cannot
check this (a Pi child session is not a pig child process); it is covered by the port's own tests (a fake child runner, a real
child process, the fake host) and shown live in tmux (`port/demo`). What keeps it safe:

- the task reaches the child on stdin as `Task: <task>`, the original's child prompt; it is never an argument, so nothing in it
  is read as an option (`--version`, `- fix it`) or a file to attach (`@notes.txt`), and its length is not bounded by the
  per-argument limit;
- nesting stops at the original's depth (`DEFAULT_SUBAGENT_MAX_DEPTH` 2, changed by `PI_SUBAGENT_MAX_DEPTH`) with its text: a
  child knows its depth from `PIG_SUBAGENT_DEPTH` (`PIG_SUBAGENT` alone counts as 1). `--no-extensions` alone is not enough: a
  Piglet Binary keeps its fused members under it, so a Binary that starts itself would nest without end;
- at most 4 children run at once (the original's `MAX_CONCURRENCY`), whatever one model turn asks for;
- a cancelled call (Escape, the session ending) sends the child SIGTERM and kills it 5 s later; on Linux a child also gets
  SIGTERM when its parent dies, so a host killed outright leaves none behind;
- the child's environment is this process's without the host's internals for this extension (`PIG_EXT_*`, `PIG_HARNESS_*`);
- the answer is the child's stdout, bounded as the original's `truncateOutput` bounds it (200 KB, 5000 lines, its marker);
  stderr (its last 8 KB) explains a failure;
- a relative `cwd` is the session's, and a missing one or a file is refused with the original's text; a `:level` suffix on
  `model` overrides the agent's thinking, as the schema says.

The pig to start is `PIG_SUBAGENT_PIG_BINARY`, else the pig running this extension (`PIG_HARNESS_BINARY`), else a `pig` next
to this program, else `PATH`. A Piglet Binary as the child refuses `--system-prompt`, so a `replace`-mode agent cannot run in one
(finding 9).

**Background runs (async_*.go).** An omitted `async` runs in the background (`asyncByDefault`, config key in `~/.pi/agent/extensions/subagent/config.json`); `async: false` or `asyncByDefault: false` blocks the parent. The child stays a detached `pig` process (own process group) supervised by goroutines of the extension; the state lives in the original's layout (`status.json`, `events.jsonl`, `output-N.log`, `process-terminal.json`, `recovery-descriptor.json`, result files) so a later session adopts a run (stale-run reconciliation, `async_state.go`/`async_resume.go`). Ported: the actions `status`, `interrupt`, `resume`, `stop`, the `bg_wait` tool, the completion notification (batched, deduplicated, replayed, delivered with `SendCustomMessage` + triggerTurn), the `subagent-async` widget with suspension during compaction, the active-capacity slots, auto-drain at `agent_end`, retention, and unknown-action recovery. On platforms without detachable children (Windows) the child is started directly and a run ends with its supervisor.

**Not ported:** parallel runs; scripted workflows (they need the original's JavaScript
workflow runtime, which no Package may carry); council mode; worktree isolation and lanes; the fleet and status views, slash
commands other than `/run-chain`; steering and the supervisor channel (declared so the model sees the same tools; they
answer that they are not ported); the watchdog; profiles, settings overrides and model scopes; the agent serializer and
the management actions create, update, delete, eject and disable; `.chain.json` chains and the acceptance, tool-budget and
dynamic fan-out validators they need; external CLI and external job runners (their agents are listed, not run); the advertised
agent catalog; the original's skills and prompt templates (their names would have to change to be Pigpen skills, and they
document features that are not ported); the original's session-resume memory of whether `subagent` was enabled.

**Refusals (M9, `refuse.go`; the port's own, the original has none).** Everything above that is not ported is refused, never
accepted and ignored: a call to `subagent` that sets a parameter the port does not honor (anything but `agent`, `task`,
`agentScope`, `cwd`, `model`, `async` and the parameters of the background actions; for `list` and `get`, `action`, `agent` and `agentScope`), `workflow`, `workflowScript`, `workflowScriptPath`, `tasks`, `chain`, `parallel`, `concurrency`, `chainDir`, `chainName`, an action
other than `list`, `get`, `status`, `interrupt`, `resume` and `stop`, a run without `task`, and the tool `subagent_supervisor` (declared, as the
original's request shows it). The original's 19 other slash commands are not registered: a command the port lacks is not offered. The text is always `@pi-in-go/pigpen-subagents (Go port) does not support
<feature> yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.`; a tool refuses with an error. The
original's own errors for an empty call, a blank agent, a non-string task and a blank action are kept verbatim. Checked by
`refuse_test.go` (every parameter of the original's schema, one case each) and 19 mutations. The `asyncByDefault` difference is gone: an omitted `async` runs in the background as in the original.

## Owner-approved exception: embedded JavaScript (goja) for workflow scripts

The porting skill forbids embedded JavaScript. The owner decided on 2026-10-20 (relayed by the Tern lead; recorded in
`handoffs/pigpen-parity/PROGRAM.md`, "Owner decisions") that the model-written workflow script (`workflowScript`, `runs.run`,
`runs.all`) may run on the pure-Go interpreter **goja** inside this extension only, exposing exactly what the original's `node:vm`
context exposes, with no change to PiG core. The skill's exception list, this record and the README name it. `go-only-exceptions.json`
lists Piglets that select a non-Go extension; goja is Go, so that file does not list it (the gate would refuse the entry as unneeded).

- Dependency: `github.com/dop251/goja v0.0.0-20261020130706-bae9ed654303` (no tagged release; MIT), with its indirect modules
  `dlclark/regexp2/v2 v2.8.1` (MIT), `go-sourcemap/sourcemap v2.1.3` (BSD-2-Clause), `google/pprof` (Apache-2.0, a goja test dependency
  kept by `go.sum`) and `golang.org/x/text v0.43.0` (BSD-3-Clause). The PR body carries the extension binary's size with and without it.
- What the script can touch: `runs` (`run`, `all`, `lanes`, `host`, `steer`, `status`, `ref`, `refs`), the tracked `Promise`, `emit`,
  `console` (`log`, `info`, `warn`, `error`), a frozen `args`, and `state` when a mission is bound. `eval`, the `Function` constructors
  and their `.constructor` paths are blocked by patching before the script runs; no `require`, `process`, timers, `fetch`, `URL`,
  `TextEncoder` or `structuredClone` (a `node:vm` context has none either).

Differences from V8 that remain (each has a test in `workflow_goja_gaps_test.go`): the call-stack overflow is a RangeError the
script cannot catch (the workflow fails with V8's message); `null.x` reads "of undefined" where V8 says "of null"; `Intl` and
`toLocaleString` grouping are absent; `async function*` and `async` methods are a validation error in the original too (every async
function but the wrapper is), and goja cannot parse them, which the validator reports the same way. `WeakRef`, `FinalizationRegistry`
and `Array.fromAsync` are small polyfills.

## The workflow slice (this change)

Twinned: `workflow-receipt`, `workflow-checklist`, `workflow-launch-params`, `workflow-terminal-proof`, `workflow-worktree-admission`
(the admission check; allocating worktrees is the worktree slice's), `parallel-utils`, `parallel-handoff` (the manifest, evidence and
eligibility; `writeWorktreeSetupHandoff` and `discardPreservedWorktrees` take the worktree slice's setup progress and cleanup and are
ported with it), `dynamic-fanout` (with `chain-outputs` and a JSON Schema validator), `chain-append`, `chain-root-attachment`,
`tool-description`, and 16 of the 22 cases of `workflow-chat-progress` (the ten rendering cases wait for the terminal-views slice's
`renderSubagentResult`, the three foreground-executor cases for the workflow executor, the three mission-ledger cases for the mission
slices). `subagent {action: "validate"}` is registered (static validation, the original's text and JSON, without launching).

**Still refused:** the execution parameters `workflow`, `workflowScript`, `workflowScriptPath`, `args`, `preflight`, `globalConcurrencyLimit`
and `maxSubagentSpawnsPerRun` on a run. The launcher seam (`WorkflowLauncher`, `RunWorkflowScriptOptions`) is defined and tested with
fake launchers; wiring it to real child runs of the background engine (`async_run.go`) and the executor around it (admission,
fan-out budget, receipts written to the async directory, completion notification of a background workflow) is not done in this
change, so a run is refused with the exact message rather than half-run.

The helpers that the workflow, run-policy and agent-management slices each ported are merged into one copy each: gate normalization
(`normalizeGateAcceptance`/`parseGateInput`, policy_acceptance*.go), extension bindings and the launch binding projection (extbindings.go),
the stable JSON digest (`stableJSON`, policy_launch.go), the dynamic fan-out and chain-output validators (dynamic_fanout.go, which `.chain.json`
parsing calls), `sanitizeDisplayText` (policy_display.go), locale ordering (`localeCompare`, parallel_handoff.go), the retry and atomic JSON
writers (policy_fsutil.go), the capability ceiling intersection (policy_ceiling.go; workflow_launch.go reaches it through
`intersectCapabilityCeilings` over decoded objects), the child tool plan (policy_toolplan.go), the live prompt registry (policy_foreground.go),
the refinement overlay (agentmgmt_refine.go) and the structured delegation bridge (policy_delegation.go).

## Mapping

| ID | Original | Go | Checked by |
|---|---|---|---|
| M1 | `agents/frontmatter.ts` | `frontmatter.go` | 5 twins + `extras_test.go` |
| M2 | `agents/chain-serializer.ts`, `identity.ts`, `runs/shared/tool-budget.ts` (validation) | `chain.go`, `identity.go` | 6 twins + `extras_test.go` |
| M3 | `agents/agent-scope.ts`, `agent-selection.ts`, `agents.ts` (resolveAgentName) | `scope.go` | 15 twins |
| M4 | `agents/agents.ts` (discovery, loading, project root) | `discovery.go`, `agents.go`, `builtin/` | 15 twins + 5 Pi scenarios |
| M5 | `agents/agent-management.ts` (list, get) | `management.go` | 5 Pi scenarios, `builtin_test.go` (the original's recorded text), `extras_test.go` |
| M6 | `extension/tool-activation.ts`, `extension/index.ts` (registration) | `extension.go`, `tooldefs.json` | 5 Pi scenarios (tools, system prompt snippets, startup) |
| M7 | (addition) child process runs | `run.go`, `child_*.go` | `run_test.go`, `run_safety_test.go`, `run_cancel_unix_test.go`, `run_host_test.go` |
| M8 | `workflows/workflow-receipt.ts`, `workflow-checklist.ts`, `chat-progress.ts`, `runs/background/workflow-terminal-proof.ts`, `chain-append.ts`, `chain-root-attachment.ts` | `workflow_receipt.go`, `workflow_checklist.go`, `workflow_chat_progress.go`, `workflow_terminal_proof.go`, `chain_append.go`, `chain_root_attachment.go` | twins |
| M9 | `runs/shared/parallel-utils.ts`, `parallel-handoff.ts`, `dynamic-fanout.ts`, `chain-outputs.ts`; executor `prepareWorkflowLaunchParams`, `preflightWorkflowWorktrees` | `parallel_utils.go`, `parallel_handoff.go`, `dynamic_fanout.go`, `jsonschema_validate.go`, `workflow_launch.go`, `workflow_admission.go` | twins |
| M10 | `extension/tool-description.ts`; `validate` of `extension/public-execution.ts` and the executor | `tool_description.go`, `workflow_validate_action.go` | twins, `validate_action_test.go` |
| M11 | `missions/store.ts`, `types.ts`, `workflow-state.ts` | `mission_types.go`, `mission_store.go`, `mission_update.go`, `mission_state.go`, `mission_fsutil.go`, `mission_proc_*.go`, `mission_dyn.go` | 16 twins (`mission_store_test.go`, two with a second process) |
| M12 | `missions/actions.ts` (+ the executor's `mission.*` dispatch) | `mission_actions.go`, `mission_register.go`, `mission_config.go` | `mission_host_test.go` (tool boundary), 3 pigeq scenarios (`mission-validation`, `mission-launch-validation`, `schedule-validation`) |
| M13 | `missions/lifecycle.ts` (+ the executor's launch binding) | `mission_lifecycle.go`, `mission_run.go` | 7 twins |
| M14 | `missions/goal-driver.ts`, `extension/index.ts` (`agent_end`) | `mission_goal.go`, `mission_register.go` | 5 twins |
| M15 | `slash/prompt-workflows.ts` | `workflow_prompt.go`, `prompts/` | 4 twins, `mission_host_test.go` |
| M16 | JavaScript semantics (ordered objects, UTF-16, trim) | `jsjson.go`, `jsutil.go` (copies from the powerline port) | everything that parses |
| M17 | `agents/advertised-agent-prompt.ts`, `extension/index.ts` (advertisement state, `before_agent_start`, `session_start`) | `advertise.go`, `extension.go` | `advertise_test.go`, `advertise_host_test.go` (6 + 2 + 1 twins) |
| M18 | `runs/shared/agent-contract.ts`, `runtime-acknowledged-extensions.ts`, `fast-mode-extension.ts`, `extension-bindings.ts`, `shared/launch-contract.ts` (digest) | `contract.go`, `runtimeack.go`, `fastmode.go`, `extbindings.go` (the single copy of extension bindings and the launch binding projection; `policy_launch.go` calls it) | `misc_twins_test.go`, `policy_launch_test.go` |
| M19 | `runs/foreground/prompt-audit.ts`, `shared/opencode-session-headers.ts` | `promptaudit.go` (the rewrite; the live prompt registry is `policy_foreground.go`) | `misc_twins_test.go`, `promptaudit_host_test.go` |
| M20 | `agents/agent-refinements.ts` | `agentmgmt_refine.go` | `agentmgmt_refine_test.go` |
| M21 | `extension/doctor.ts`, `extension/subagent-guide.ts` | `doctor.go`, `guide.go` (the guide topics are embedded from the original: `guide/`) | `doctor_test.go`, `guide_test.go` |
| M22 | `profiles/profiles.ts` | `profiles.go` | `profiles_test.go` |
| M23 | `slash/slash-commands.ts` (the administration commands), `slash/subagents-admin.ts` | `slash_commands.go`, `slash_admin.go` | `slash_admin_test.go` |
| M24 | `runs/shared/subagent-prompt-runtime.ts` (prompt rewriting and fork context cleaning), `child-tool-plan.ts` (extension plan, fast mode, thinking suffix: `policy_toolplan.go`) | `child_prompt.go` | `child_prompt_test.go`, `default_extensions_test.go` |
| M25 | `slash/prompt-template-bridge.ts`, `delegation-request.ts`, `delegation-adapters.ts`, `api/delegation.ts` | `policy_delegation.go`, `policy_delegation_host.go` (the single bridge) | `delegation_test.go` (the 6 twins of `prompt-template-bridge`) |

## Differences from the original (missions and prompt workflows)

- `config.json` is read for its `missions` key only (`mission_config.go`); the original drops the whole config when any key of it
  is invalid, the port drops `missions` only. The configuration slice replaces this reader.
- The `/prompt-workflow list` message is sent with the custom type `prompt-workflow` (the original sets none; the SDK needs
  one). The package's own prompt templates (`prompts/*.md`, byte for byte) are carried inside the binary and listed under
  `pigpen-subagents:prompts/<name>.md`, not a file path. Running a template calls the workflow runtime; until a launcher is
  wired (`promptWorkflowRunner`) it answers with the port's standard refusal of `workflowScript`.
- The goal notice cannot name retained children until the background-run slice fills `goalRetainedChildren`.
- The ISO-timestamp check of mission records accepts the ECMAScript format and the common legacy forms of `Date.parse`
  (`jsDateParses`), not every string V8 would parse.
- The mission atomic write and retry ladder (`mission_fsutil.go`) cover what the missions call of `atomic-json.ts` and
  `file-system-retry.ts`; the integration branch replaces them with that slice's port.

## Process: what was written before its tests

The twins are tests first: 38 were written from the original's tests, with their inputs and assertions, against stubs that panic
(commit `cc44d72`, `port/red-run.log`), and the code followed; three more (the failing-agent isolation case and two default cases)
were added after the code. The extension is **not** tests first: the six scenarios were recorded from the original under Pi
first (so the goldens are the original's), the extension was written against them, and its own tests came after. The running
addition is tests first (`79ee551` red, then the code), and so are the review's safety fixes (22 tests red on the stubs,
`port/red-run-review.log`, then the code).

## Results

- `go test -race`: pass. 41 twins (`pigeq twins check --deferred port/slices.json`: exit 0).
- `pigeq check` against the Pi 1.0.0 goldens: 5 of 5 scenarios (enable, list, list with scopes, list with diagnostics, get) +
  port-gaps + exec-coverage, three runs in a row. The golden is recorded from `port/oracle/index.ts`, not from the package
  directory: Pi would also load the original's skills when given the directory, which the port does not ship.
- A sixth scenario (`get` on builtin agents) is host-bound: the original prints the install directory of its agent files, which no
  port can share. `port/host-bound` holds its scenario and golden (the path replaced by `<oracle>`); `builtin_test.go` compares the
  port's text with it, that one line aside.
- `pigeq mutate --unit`: 74 mutations, all killed in the lane's final run. The first run of 76 killed 38. The survivors got tests
  (`extras_test.go`: CRLF input, hyphenated list items, tool-budget bounds, trailing spaces, package edges, shadowed and
  out-of-scope diagnostics, the tool-list arithmetic, source precedence); 2 mutants were equivalent (the sanitized-name retry in
  `get` has a second path that finds the same agent; trailing blanks at the end of a file are trimmed before the chain parser
  sees them) and were removed; 4 did not build and were rebuilt. The review (rev-port-popular-5) found the run path untested
  (the real runner's directory and environment, the tool's `cwd` and `agentScope`, `/run-chain`'s task, the binary override all
  survived when mutated) and added 42 mutations for it and its fixes: **116 mutations, all killed**.
- `pig install --validate-only --json`: valid (1 command, 3 handlers, 4 tools).
- Piglet: `piglets/pig-popular` now selects six ports. A Binary built from this member alone gives traces equal to the Pi goldens in
  every event outside the `llm` channel (5 of 5); the `llm` channel differs by the host (a Binary's default tool set), as for the permissions port.
- tmux, detached, interactive pig 0.4.0 (`port/demo/pane-*.txt`): the model enables the tool, lists the project's agents, runs
  `helper`, and the child's answer comes back. `pane-review.txt` (`turns-review.json`): a task that starts with `- ` reaches the
  child as `Task: - say hello` (before the review, pig refused it: `Unknown option: - say hello`).
- The review's live checks, pig 0.4.0 with a scripted model: an aborted run (RPC `abort`) stops its child at once (before, the
  child ran on); killing the host with SIGKILL leaves no child (before, it was re-parented to init); a pig-popular Binary that
  starts itself stops at depth 2 with the original's text (before, it nested as deep as the script went, four children).
- Size: 2,880 hand-written Go lines (non-test; 375 are copied helpers; 364 added by the review), 1,552 test lines beyond the two
  template files (566 added by the review); generated or
  copied: `tooldefs.json` (read from the original's request), `builtin/*.md` (the original's files).
- `pigeq gaps`: Go side 1 PARTIAL (`SetWidget`), 0 blocking; the original has blocking gaps (`provider.streamSimple` in its
  watchdog and fork pruning) that no slice reaches, and its `exec-coverage` cannot pass (it starts `/usr/bin/osascript` by path).

## Comparison with the owner's Go subagent extension (unpublished)

The owner's subagent extension (7,930 lines of Go, 117 tests; read for this comparison, nothing copied) is a PiG-native
design for the same job. What each has:

| | pi-subagents (original and this port) | owner's Go subagent |
|---|---|---|
| Agent files | `.pi/agents`, `.agents` (nearest project root), user directories, packages, a builtin set; frontmatter with aliases, packages, runners, tools, model, thinking, context, acceptance | `<name>.md` in `.pig/agents`, `.pi/agents` (the cwd), then `agent/agents` and `agents` under the PiG config home, first match wins; frontmatter with model, tools, extensions, thinking, auto-exit |
| Single run | `subagent {agent, task}`, many options (context, model, output, async) | `dispatch_subagent {agent, task}`: background by default (`check_subagent`, `cancel_subagent`, the answer delivered when done), `wait` with a timeout (300 s by default, then the user is asked), or an interactive tab |
| Chains | `.chain.md` / `.chain.json` files, run as scripted workflows | **`dispatch_chain` over `agents/chains.yaml`** and `/chain`: sequential, each output threaded into the next step as `## Input`; durable run manifests, resume, human requests between steps |
| Parallel | parallel and dynamic fan-out inside workflows | `dispatch_subagents` (up to 6 agent/task pairs) and **`dispatch_team` over `agents/teams.yaml`**: named teams, 6 at once |
| Member resolution | agent name against the discovered definitions | **piglet first: `pig --piglet <name>` if a piglet of that name exists, else the agent file** |
| Child process | Pi child sessions (in-process or process), depth limit 2 | `pig --print` with `PIG_SUBAGENT=1`; a warning after 2 minutes without output, killed at its timeout or on cancel; the extension registers no dispatch tool inside a child |
| Views | fleet, status widgets, async widget | agent view with grid, zoom and panes; interactive runs in a herdr or tmux tab |

**Features the original lacks that the owner's has, to add as a PiG layer on top of this port:**

1. `dispatch_chain` over `chains.yaml` (and the `/chain` command): this port's `/run-chain` runs the original's chain files with
   the same threading idea; a YAML chain table and durable manifests are the owner's.
2. `teams.yaml` and `dispatch_team`: named parallel teams (the original only has workflow fan-out).
3. Piglet-first member resolution: `pig --piglet <name>` before the agent file. This port resolves agent files only.
4. Durable chain runs with resume and human-in-the-loop requests, interactive runs in a terminal tab, and the grid/zoom/pane views.

Conversely, the original has what the owner's lacks and this port brings: the richer frontmatter (aliases, packages, runners,
context, acceptance), the nearest-project-root rule and the builtin agent set, diagnostics for a malformed definition, the
`subagents_enable` activation that keeps the tool out of the model's tools until delegation is authorized, and `list` and `get`
as model-facing actions. A merged design would keep the owner's execution layer (teams, chains.yaml, piglet-first, durable
runs) and take this port's definitions, discovery and activation. Nothing of the owner's extension was copied or committed.

## Findings (to file against PiG, and notes on the porter)

1. **pigeq normalizer gaps (fixed here):** an exec event's args (`[]string`) and the model request's messages
   (`[]map[string]any`) were not normalized, so a path a run printed (`git -C <cwd>`, a tool result quoting a path) differed
   between runs. Fixed in `eq/normalize.go` with a test; goldens re-recorded.
2. **`commands` mode `missing` for git hides `/usr/bin`:** the host's own launcher then cannot find `dirname`. The scenarios use
   `canned` with exit 1.
3. **A package directory loads its skills under Pi:** `--ts <package dir>` makes the original's skills part of the system prompt,
   which `--go <extension dir>` cannot match; the golden is recorded from the extension entry file.
4. **SDK: an explicit `isError: false`.** `ToolResult.IsError` is omitted when false; the original's results carry `isError: false`
   and the trace compares it. A result type with its own `MarshalJSON` (`is_error: false`) works as a workaround. A refused action
   is a failed tool call in Pi (`details: {}`, the flag on the event): `sdk.NewToolError` matches it, a `ToolResult{IsError: true}` does not.
5. **`exec-coverage` and `sdk-gaps` cannot be scoped to a slice:** they fail for the whole original (a path-started program,
   `provider.streamSimple` in code the slice never reaches).
6. **A tool list that changes at runtime:** `subagents_enable` appends `subagent` to the active tools, so the tool order in the model
   request is registration order of the others, then `subagent`. `Context.SetActiveTools` made it reproducible.
7. **The oracle's own suite has a failing test at the pin** (`profiles helpers`), the same in a pristine clone; the quality gate
   needed to skip `SKILL.md` files under `port/oracle` (the original ships skills named without `pigpen-`).
8. **`--no-extensions` does not remove a Piglet Binary's fused members.** A child started from a Piglet Binary that carries this
   port has the `subagent` tool again, so the port bounds nesting itself (the depth guard above). The review saw a pig-popular
   Binary that starts itself nest four children deep, as far as the scripted model went; with the guard it stops at depth 2.
9. **A Piglet Binary refuses `--system-prompt`** ("a Piglet Binary's baseline system prompt cannot be replaced"): when the child
   is a Piglet Binary, an agent with `systemPromptMode: replace` (the default for an ordinary agent) fails with that message.
10. **An extension process's environment carries the host's internals** (`PIG_EXT_SOCKET_<name>`, `PIG_EXT_PACKED_CELL`,
   `PIG_HARNESS_ARGV_FILE`, ...), and a program the extension starts inherits them unless it removes them; the SDK could
   offer the environment for a child process, or the variables could be documented as not to be passed on.

## Where a generated skeleton (`pig-codegen extension`) would have saved hand work

- Module, `Extension()`, event and tool registration; the tool definition from a JSON Schema (here read from the original).
- The JS helper layer a fourth time (`jsjson.go`, `jsutil.go`: 375 lines copied).
- The twin ledger and slice file (264 entries by name, grouped by hand) and the 41 twins' file layout.
- The mutation generator (116 pairs by hand, 42 of them the review's) and the scenario generator.
- A tool-definition importer that reads a recorded request would replace `gen-tooldefs.py`.

## Run policy lane, second pass (subagents-policy-2)

Merged into this lane: the agent settings lane (discovery engine, model resolution, skills) and the background runs lane
(`origin/subagents-async-1`), whose result files this lane twins. Where both lanes ported the same helper one copy was kept (the
temp scope, run fan-out descriptor and capacity predicate stay here, with their test seams; the index segments, display helpers,
recovery projection and git refs stay with the background runs).

| ID | Original | Go | Checked by |
|---|---|---|---|
| M10 | `runs/background/result-files.ts` (indexes, pending promotion, cleanup, mission observer index) | `async_results.go` (fault seams `resultReadFile`, `resultReadDir`, `resultStat`, `resultRename`) | 23 twins (`policy_resultfiles_test.go`) |
| M11 | `runs/shared/readonly-drain-observation.ts`, `runs/background/auto-drain.ts` (observation), `async-status.ts` (raw status observer) | `policy_readonlydrain.go`, `async_wait.go`, `async_list.go`, `async_status.go` | 5 twins and 1 skip (the in-process child hook factory) |
| M12 | `runs/foreground/foreground-control.ts`, live prompt registry of `prompt-audit.ts` | `policy_foreground.go` | 2 twins + 1 host test |
| M13 | `tui/running-tone.ts` (thinking level reader), `shared/extension-context.ts` (stale and unbound runtime tests) | `policy_tone.go` | 1 twin |

Changes to the background runs' status code made for the observation: `readStatus` inspects before it reads and wraps the cause,
the reconciler reports the raw status it read first (before any repair), `updateActiveRunIndex` and the active index listing
report status errors as `Failed to list async runs`. A background run (`async` unset or true) refuses the policy options that
only a foreground run applies (`output` other than the default, `outputMode`, `artifacts`, `acceptance`, `gate`,
`agentContract`, `includeProgress`) instead of ignoring them; the policy reads the real `config.json`.

## Run policy lane, third pass (subagents-policy-3)

The five ledger files the second pass left open are now twinned (every upstream case has a `tw` or an individually justified `tskip`,
and their entries are gone from `port/slices.json`).

| ID | Original | Go | Checked by |
|---|---|---|---|
| M14 | `api/preflight.ts` (`resolveSubagentLaunchContract`) | `policy_preflight.go` | 31 twins (`policy_preflight_test.go`) |
| M15 | `shared/launch-contract.ts` (stable digests, agent definition projection, launch binding), `runs/shared/extension-bindings.ts`, `shared/required-child-extensions.ts`, `intercom/intercom-bridge.ts`, launch context of `shared/fork-context.ts`, `resolveStepBehavior` of `child-launch-plan.ts` | `policy_launch.go` | the preflight twins, `policy_launch_test.go` |
| M16 | `runs/shared/child-tool-plan.ts` | `policy_toolplan.go` | the preflight twins, `policy_launch_test.go` |
| M17 | `runs/shared/mcp-direct-tool-grant.ts`, `mcp-direct-tool-allowlist.ts`, `mcp-config-sources.ts` | `policy_mcpgrant.go`, `policy_mcpconfig.go` | the preflight twins, `policy_launch_test.go` |
| M18 | `agents/agent-refinements.ts` (the overlay), `runs/shared/effective-system-prompt.ts` | `agentmgmt_refine.go` (overlay and file), `policy_refinement.go` (the effective system prompt) | `policy_launch_test.go`, `agentmgmt_refine_test.go` |
| M19 | `api/delegation.ts`, `slash/delegation-request.ts`, `delegation-adapters.ts`, `delegation-json.ts`, `prompt-template-bridge.ts` | `policy_delegation.go` (the bridge), `policy_delegation_host.go` (SDK event bus, child process executor) | 17 twins (`policy_delegation_test.go`) |
| M20 | the structured output tool of `runs/shared/subagent-prompt-runtime.ts` | `child_role.go` | not yet wired (see the child role below) |

Counts: preflight 31 twins; delegation-api 17 twins; capability-ceiling-agent-allowlist 3 twins here, 1 in the host-test package
(`policy_run_test.go`), 1 from the second pass and 3 skips; writer-budget-guidance 1 twin; usage-reconciliation-owners 2 skips.

**Decisions and differences.**

- The preflight is the in-package function `resolveSubagentLaunchContract`; the original exports it from a Node module
  (`pi-subagents/preflight`), and a Go extension cannot be imported by another extension at run time. Cross-extension access is the
  delegation event bus, as in the original.
- The original's runtime extension files (`fast-mode-extension.ts`, `subagent-prompt-runtime.ts`, `fanout-child.ts`) are the child
  roles `native:pi-subagents-fast-mode`, `-prompt-runtime` and `-fanout-child` (the way the original already names its supervisor channel,
  `native:pi-subagents-supervisor-channel`); the twins assert those names where the original asserts the file names.
  `protocol.packageVersion` is the original package version the port follows (0.73.1).
- The MCP runtime snapshot of pi-mcp-adapter (`pi-mcp-adapter:runtime-snapshot:v1`) is not ported: the original asks for it by emitting a
  request object on the in-process event bus and reading the `result` the adapter's listener sets on it. The SDK's event bus passes payloads by
  value, so a listener cannot answer; a selector of a server that exists only in that snapshot stays unresolved (a preflight failure).
  Pi API mirrored: an event bus request/response, `pi.events.emit` with a mutable payload.
- The permission-system extension of a child (`resolvePermissionSystemExtension`) is found as the original finds it; it is a Pi extension that
  a pig child does not load.
- The package now ships the original's `pi-subagents` skill (renamed `pigpen-subagents`), its six prompt templates and its tool reference
  (`skills/`, `prompts/`, `docs/`) because the writer-budget-guidance twin reads them; they describe features that other lanes still refuse.
- `config.json` is read raw for `defaultSubagentContext`, `intercomBridge` and `permissions`; the original's whole-file validation (a failed one
  gives an empty config) is not ported for the keys outside this lane.
- Delegation: the executor runs on its own goroutine, so an emitter is not held until the delegation ends; the terminal event is emitted after the
  attempt's node is free (the original's `finally` runs after the emit, but one tick before anyone can observe the difference). A delegated leaf runs
  a text result in a child process; `toolBudget`, `skill`, `intercomBridge`, `artifacts` and a structured result are refused with the port's message.
  The legacy prompt-template cancel state (`legacyControllers`, `pendingLegacyCancels`) is not ported: it is unreachable, every legacy request is rejected first.

**Skips.** capability-ceiling-agent-allowlist: three cases need `buildInProcessChildLaunch` / `buildRunnerChildLaunch` (child session runtime slice;
listed in `port/deferred-skips.md` for the final reconciliation). usage-reconciliation-owners: both cases script an in-process
`ChildSessionFactory` (a Pi child session), which the port does not have.

**Mutations.** 203 in `gen-mutations.py` (about 55 added for this lane). Four stale ones (`ext-bgwait`, `ref-async-true`, `ref-async-text` and the
text of `ext-run-model`) targeted code the earlier merges replaced; the first three were removed, the last re-pointed.

**Scenarios.** Four pigeq scenarios of the model-visible validation errors that both the original under Pi and the port answer before any child
starts (so no child model is needed): `run-public-validation`, `run-acceptance-validation`, `run-gate-validation`, `run-limit-validation`.
Not possible yet: invalid `toolBudget` and `outputSchema` calls (the port refuses these parameters with its own message, the original validates them), and
any call that starts a child (it needs a model and the Pi child session).

**Child role (item 3).** Nothing in pig or the SDK blocks it: `-e <path>` loads an explicit extension under `--no-extensions`; the SDK's
`ToolResult.Terminate` ends the step like Pi's `terminate: true`; `ctx.GetAllTools()` reports the `SourceInfo.Path` of this extension's own `subagent`
tool, which is the path to pass to `-e`. `child_role.go` is the child side: with `PIG_SUBAGENT_CHILD_ROLE` naming a role file, `Extension()` returns only
`structured_output` (validates the value against the schema, persists it and the acceptance report to the capture files of `policy_structured.go`,
ends the step). **Not done:** the parent side, which has to create the structured output runtime, write the role file (`writeChildRoleConfig`), start the child
with `-e <extension path>`, `--tools` including `structured_output` and the instructions of the original appended to the system prompt, read the capture after the
child exits (`readStructuredOutput`) and report it as the original's result; and registering `outputSchema` as a run parameter. `outputSchema`,
`structured_output` and `acceptance.report` therefore stay refused. `contact_supervisor` (the supervisor channel) and the watchdog diff tool are other slices.

## Child runtime lane, wave 2 (subagents-w2-childrt): what is ported, what is not

Children are `pig` processes. Twinned (every upstream case a twin or an individually justified skip; entries removed from `slices.json`):
`binary-pi-spawn`, `pi-spawn`, `pi-spawn-host-root` (`spawn_pig.go`: the pig a child is = `PIG_SUBAGENT_PIG_BINARY`, the host image
`PIG_HARNESS_BINARY`, a sibling `pig`, PATH; the Node mechanisms named in each skip: `node <cli.js>`, npm package-root walks,
`process.argv[1]`, the compiled-Bun image layout), `session-lease` (`session_lease.go`; session files are plain files for pig, so the
lease is ported whole), `process-terminal` (+ the lease proof: `canonical-session-*` reasons, `revivalLeaseToken`), `owned-process-tree`
(`owned_process_tree.go`), `stale-run-reconciler`, `nested-events`, `retained-nested-route-tracker`, `pi-web-session-liveness` (the
liveness decision; the `globalThis[Symbol.for('@agegr/pi-web/session-liveness/v1')]` registry of the Node host that runs Pi and pi-web
has no pig counterpart: 2 skips), `result-delivery-ownership`, `run-id-resolver` (nested kind; `inspectStatus` shows the nested exact
status), `retained-children`, `child-lifecycle`, `child-launch-plan` (`planChildLaunch`), `child-session-name`,
`opencode-session-headers`. The retained tracker's native `fs.watch` is an injectable seam (no file-watching dependency approved), the
poll covers it.

The child-facing interface for steering and the watchdog: `child_session.go` (`childSession` over `pig --mode rpc`: Prompt, Steer,
FollowUp, Abort, Subscribe, HasQueuedMessages, Messages, Dispose), `child_registry.go` (`registerChild`, `lookupChild`, `listChildren`,
`onChildRegistered`, `steerConsumer`), and `child_role.go` (`childRoleExtras`, `Watchdog`, `WatchdogStatusFile`, `RequiredTools`).

**Not done in this lane (still refused, still listed in `slices.json`):** `child-runtime-config`, `child-session-host-sdk`,
`child-session-parent-providers`, `child-session-queued`, `child-tool-plan`, `child-tool-plan-permission-system`, `child-transcript`,
`fork-cache-key`, `fork-context`, `pruned-fork`, `types-fork-preamble`, `required-child-extensions`, `run-child-session`,
`runner-bootstrap`, `runner-http-dispatcher`, `runner-peer-loader`, `runner-peer-symlinks`, `index-child-registration`,
`workflow-child-summary`, `rpc`, `inspect-rpc`, `tool-activation`, `tool-activation-stale-peer`, `selector`,
`runtime-agent-registration`, `pi-subagents-policy-guidance`, `mcp-direct-tool-grant`, `mcp-direct-tool-resolution`, and the foreground
run's start of the child role (`outputSchema`, `structured_output`, `acceptance.report`) and its `--mode rpc` launch (the registry and
`rpcChild` exist and compile; no run path uses them yet).
