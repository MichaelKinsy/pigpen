# pi-subagents (Go port, partial)

A Go extension for PiG: delegate work to specialist agents defined in markdown files. It is a port of **pi-subagents** by Nico
Bailon (see [CREDITS.md](CREDITS.md)), made with the [extension porting Skill](../extension-port/README.md), and needs no Node.

## Use

Define an agent in `.pi/agents/<name>.md` (the project) or `~/.pi/agent/agents/<name>.md` (you), the original's format:

```markdown
---
name: helper
description: Helps with things
model: provider/model
tools: read, grep
---

You help.
```

The model sees two tools. `subagents_enable` makes the `subagent` tool available (it is not offered until then, as in the
original); then:

- `subagent {agent: "helper", task: "..."}` runs one agent in a child `pig --print` process; a **background** child
  runs in `pig --mode json` instead, so its work is visible live: tool and message activity stream into the run's
  `output-<n>.log` (the file `view: "transcript"` tails while the run is live), child events are mirrored into
  `events.jsonl`, and `status.json` heartbeats at the original's 100ms coalescing so `Updated` shows child activity.
  The under-editor async widget and `/subagents-fleet` show each run's current tool with its duration, the latest
  activity line, activity freshness and token spend (`status` reports `Tokens: ↓window · spent` and `Tool:` lines).
  The run **detaches by default**
  (`asyncByDefault`, as in the original): the tool returns a started message, the answer arrives as a native completion
  notification, and `bg_wait` can block the turn on it. `async: false` blocks the parent and returns the answer directly;
  `timeoutMs` (alias `maxRuntimeMs`) bounds the run, `output` binds the answer to a file, `cwd` and `model` steer the child.
- `subagent {action: "list"|"get"|"models"|"create"|"update"|"delete"|"eject"|"disable"|"enable"|"reset"}` — the original's
  management actions: the effective agent set, one definition, effective models with the registry's exact ids, and the
  file/settings writers (a disabled builtin is hidden from list, get, models and runtime discovery); the mission actions
  keep a durable record of the work (title, objective, status, linked runs, decisions).
- `subagent {action: "status", id?, view?}` inspects the active async runs (list, one run, `view: "fleet"` or `"transcript"`),
  `{action: "stop"|"interrupt", id}` stops one, `{action: "steer", id, message}` sends guidance to a running child.
- `subagent_supervisor` is the supervisor channel: a child that needs a decision writes a request into its run's channel
  directory (the child's prompt carries the protocol); `{action: "pending"|"status"|"list"|"reply"}` surfaces and answers it.
  A run blocked on a question reports `needs attention` in status and yields `bg_wait`.
- `subagent {action: "guide", topic}` serves the port's guides (overview, agents, configuration, models, tool-reference, ...);
- `subagent {action: "mission.create"|"mission.list"|"mission.show"|"mission.update"|"mission.resolve-decision"|"mission.attach-run"|"mission.close"}` — the original's mission lifecycle over its durable JSON store;
- `isolation: "worktree"` runs a child in a managed git worktree on its own branch (the original's clean-tree and baseRef checks), and `subagent {action: "worktree.cleanup"}` plans — never applies — their removal.
- `/run-chain <chain> <task>` runs a `.chain.md` **or `.chain.json`** chain (`.pi/chains/` or `~/.pi/agent/chains/`): each step's
  answer flows into the next as `{previous}`, named outputs as `{outputs.name}`.

Slash commands: `/run <agent> [task] [--bg] [--fork]`, `/subagents [agent] [model|thinking|details]` (an interactive admin
picker in a TUI), `/subagents-models`, `/subagents-guide`, `/subagents-stop`, `/subagents-steer`, `/subagents-fleet`, and
`/run-chain`.

Thirteen agents ship with the package (the original's `scout`, `worker`, `reviewer`, ...). `PIG_SUBAGENT_PIG_BINARY` names the pig
to start for a child; otherwise the pig running this extension, then a `pig` beside this program, then `PATH`. A child gets the
task on stdin, runs with `--no-extensions`, and may itself delegate only to depth 2 (`PI_SUBAGENT_MAX_DEPTH`, as in the
original); at most 4 children run at once, and a cancelled call (Escape) stops its child. An agent with
`systemPromptMode: replace` cannot run in a Piglet Binary child (a Binary keeps its own system prompt). An agent with a
`runner: external-cli` runs its own binary (the task on stdin); an unavailable binary or an `external-job` provider is reported,
never launched through pig.

Config: `<agent dir>/extensions/subagent/config.json` carries the keys the original shares with this port — `asyncByDefault`,
`timeoutMs`, `maxActiveAsyncRunsPerSession` and `waitTool`. Async run artifacts (status.json, events.jsonl, output logs, the
result file and the supervisor channel) live under the original's temp layout (`PI_SUBAGENTS_TEMP_ROOT` moves it) and are plain
JSON the model and the user can read, as the original's tool description promises.

An agent chooses its child's extensions as in the original. `extensions: a, b` loads only those (an empty `extensions:` loads
none, not even a model provider), and `subagentOnlyExtensions: c` loads `c` in the child on top of the rest. Entries are what
`pig --extension` takes: a path (`./` and `../` are relative to the agent file), `builtin:<name>`, or an `npm:`/`git:`
source. For agents that set neither, `subagents.defaultExtensions` and `subagents.defaultSubagentOnlyExtensions` in
`settings.json` are the default: PiG's own files (`.pig/settings.json` in the cwd, `~/.pig/agent/settings.json`) and the
original's (`.pi/settings.json` at the project root, `~/.pi/agent/settings.json`) are all read, project over user and PiG's
over Pi's at each level:

```json
{ "subagents": { "defaultSubagentOnlyExtensions": ["/path/to/child-only-extension"] } }
```

A child's model and thinking level come, strongest first, from the call's `model`, `subagents.agentOverrides.<name>.model`
and `.thinking` (`false` clears the agent's own), the agent's frontmatter, then `subagents.defaultModel` and
`defaultThinking`, read from the same files, each from the first file that sets it. Without any of them the child runs on
the host's default model. Other `agentOverrides` fields, `defaultProvider` and `modelScope` are not read:

```json
{ "subagents": { "defaultModel": "provider/small-model", "agentOverrides": { "oracle": { "model": "provider/large-model", "thinking": "high" } } } }
```

```sh
pig install ./components/subagents
```

## What is ported

Frontmatter parsing (including the management fields), agent and chain definitions (discovery in the project, the user's
directories and the builtin set, precedence project over user over builtin, packages, aliases, name resolution, the
`disabled` settings override), `.chain.md` **and `.chain.json`** files (parse and write) with the acceptance, tool-budget,
dynamic fan-out shape and output-binding validators, the `subagents_enable` loader, the `subagent` tool's `list`, `get`,
`models`, `create`, `update`, `delete`, `eject`, `disable`, `enable`, `reset`, `status`, `stop`, `interrupt`, `steer` and
`guide`, both tools (`bg_wait`, `subagent_supervisor`), model scopes enforced at launch (an explicit out-of-scope model is
an error, an inherited one warns), the settings layer (disableBuiltins, defaultModel/defaultThinking, and the
`disabled`/`model`/`thinking`/`machine` override fields applied to builtin, package, user and project agents), profiles
loaded from `~/.pi/agent/profiles/pi-subagents/` into settings, running one agent or a chain through a child process (a Go addition:
the original runs children through its own executor and scripted workflows), background runs with the original's started
message, completion notifications, deadlines and durable output binding, and the supervisor channel over the run directory.

**Not ported, and refused:** scripted workflows (`workflow`, `workflowScript`, `workflowScriptPath`) and the legacy
`tasks`/`chain` inputs (they need the original's JavaScript workflow runtime, which no Go package can carry), lanes,
council mode, the watchdog, profile generation and the provider model catalog checks (the profile application layer is
ported: `/subagents-profiles`, `/subagents-load-profile`), scheduled runs (whose only target is a
workflowScript), the `doctor`, `validate`, `resume` and `debug.run` actions, the run parameters that need the child's runtime
cooperation (`acceptance`, `toolBudget`, `context`, `skills`, `gate`, `fast`, ...), the settings fields the port does not
apply (`defaultProvider`, `maxThinking`, `agentScanDirs`, `agentOverridesByProvider`, ...), and the original's skills and prompt
templates. Everything in that list is refused with one message naming the original and nothing runs:

> @pi-in-go/pigpen-subagents (Go port) does not support workflow runs (`workflowScript`) yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.

See [port/PORT.md](port/PORT.md).
