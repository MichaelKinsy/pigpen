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

- `subagent {action: "list"}` lists the agents (`agentScope`: user, project or both; `capabilities: true` adds each one's tools, model
  and thinking), `{action: "get", agent: "helper"}` shows one;
- `subagent {agent: "helper", task: "..."}` runs one agent in a child `pig --print` process and returns its answer;
- `/run-chain <chain> <task>` runs a `.chain.md` chain (`.pi/chains/` or `~/.pi/agent/chains/`): each step's answer flows into the
  next as `{previous}`, the request as `{task}`.

Thirteen agents ship with the package (the original's `scout`, `worker`, `reviewer`, ...). `PIG_SUBAGENT_PIG_BINARY` names the pig
to start for a child; otherwise the pig running this extension, then a `pig` beside this program, then `PATH`. A child gets the
task on stdin, discovers extensions as the original's background child does (a model provider and its login come from one), and
may itself delegate only to depth 2 (`PI_SUBAGENT_MAX_DEPTH`, as in the original); at most 4 children run at once, and a cancelled call (Escape) stops its child. An agent with
`systemPromptMode: replace` cannot run in a Piglet Binary child (a Binary keeps its own system prompt).

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

## Panes in Herdr Desktop (opt-in)

Set `herdrPanes` to show each child in a pane beside yours. It is read from the same settings files as the options above
(project over user, PiG's file over Pi's):

```json
{ "subagents": { "herdrPanes": true } }
```

It applies only when PiG runs in a Herdr Desktop terminal (`HC_TERMINAL_SOCKET` and `HC_TERMINAL_ID` are set), and never on
Windows. Each `subagent` child and `/run-chain` step then starts as an interactive `pig` in a herdr-core terminal labelled
with your Herdr space and tab, so Herdr Desktop shows it as a pane beside yours (split to the right, focus unchanged).
Without those labels the child runs with no pane and is listed under yours in Herdr's agents list. herdr-core is told not to
type notes about the child into your terminal.

The tool waits until the child's first turn ends, returns the same text the hidden child would (its last assistant message,
read from its session file), then ends the child and closes its pane. Escape ends it too. A child that stops on a dialog
(`blocked`) waits for you, with one notification per block. A child that cannot start its turn waits for you in its pane, and
Escape in the parent cancels it.

The child's session file goes in your session's directory, so you can resume it with `pig --session <file>`. When your session
has no file, it goes in a temporary directory that is removed afterwards.

The child's environment (yours, as for the hidden child) is passed through herdr-core, which keeps it in its terminal record, so
secrets in your environment reach herdr-core. In a pane the task is an argument of the child's command line, visible in process
listings, not stdin. A parent PiG that is killed outright can leave pane children running until its own Herdr terminal ends.

The child runs hidden, as without the setting, with a warning, when herdr-core cannot be reached or your terminal is unknown,
and when the task is over 100 KiB (it travels as an argument).

Checked with PiG 0.5.3, herdr-core 0.1.16 and 0.1.17, and the Herdr app 0.1.11.

## What is ported

Frontmatter parsing, agent and chain definitions (discovery in the project, the user's directories and the builtin set,
precedence project over user over builtin, packages, aliases, name resolution), markdown chain files (parse and write), the
`subagent` tool's `list` and `get`, the `subagents_enable` loader, and running one agent or a chain through a child process (a Go
addition: the original runs children through its own executor and scripted workflows).

**Not ported, and refused:** parallel, async and background runs, scripted workflows (`workflow`, `workflowScript`,
`workflowScriptPath`) and the legacy `tasks`/`chain` inputs, council mode, worktree isolation, the fleet and status views, steering and
the supervisor channel, the watchdog, missions, profiles, `agentOverrides` other than `model` and `thinking`, `.chain.json` chains, the management actions other
than `list` and `get`. The tool honors `agent`, `task`, `agentScope`, `cwd`, `model` and
`async: false` (the port always runs in the foreground) and, for `list` and `get`, `action`, `agent` and `agentScope` (and `capabilities` for `list`). Any other
parameter or action, and the tools `bg_wait` and `subagent_supervisor` (declared so the model sees the original's tools), is refused
with one message and nothing runs:

> @pi-in-go/pigpen-subagents (Go port) does not support async (background) runs (`async: true`) yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.

The original's slash commands other than the port's own `/run-chain` (`/run`, `/subagents`, `/subagents-stop`, `/prompt-workflow`, ...) are not
registered, and neither are its skills and prompt templates. See [port/PORT.md](port/PORT.md).
