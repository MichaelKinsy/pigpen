# Port record: pi-subagents (partial: definitions, discovery, list and get, single-agent and chain runs)

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

**Not ported:** parallel, async and background runs and their status; scripted workflows (they need the original's JavaScript
workflow runtime, which no Package may carry); council mode; worktree isolation and lanes; the fleet and status views, slash
commands other than `/run-chain`; steering, the supervisor channel and `bg_wait` (declared so the model sees the same tools; they
answer that they are not ported); the watchdog; missions; profiles, settings overrides and model scopes; the agent serializer and
the management actions create, update, delete, eject and disable; `.chain.json` chains and the acceptance, tool-budget and
dynamic fan-out validators they need; external CLI and external job runners (their agents are listed, not run); the advertised
agent catalog; the original's skills and prompt templates (their names would have to change to be Pigpen skills, and they
document features that are not ported); the original's session-resume memory of whether `subagent` was enabled.

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
| M8 | JavaScript semantics (ordered objects, UTF-16, trim) | `jsjson.go`, `jsutil.go` (copies from the powerline port) | everything that parses |

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
