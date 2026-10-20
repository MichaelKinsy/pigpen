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

- `subagent {action: "list"}` lists the agents (`agentScope`: user, project or both), `{action: "get", agent: "helper"}` shows one;
- `subagent {agent: "helper", task: "..."}` runs one agent in a child `pig --print` process and returns its answer;
- `subagent {agent, task}` without `async` runs in the background (the original's `asyncByDefault`): the call returns at once, the result arrives as a message that wakes the session, `{action: "status"}`, `interrupt`, `resume` and `stop` manage runs, and the `bg_wait` tool waits for them. `async: false` runs in the foreground. A run survives its session and is adopted by the next one;
- `/run-chain <chain> <task>` runs a `.chain.md` chain (`.pi/chains/` or `~/.pi/agent/chains/`): each step's answer flows into the
  next as `{previous}`, the request as `{task}`.

Thirteen agents ship with the package (the original's `scout`, `worker`, `reviewer`, ...). `PIG_SUBAGENT_PIG_BINARY` names the pig
to start for a child; otherwise the pig running this extension, then a `pig` beside this program, then `PATH`. A child gets the
task on stdin, runs with `--no-extensions`, and may itself delegate only to depth 2 (`PI_SUBAGENT_MAX_DEPTH`, as in the
original); at most 4 children run at once, and a cancelled call (Escape) stops its child. An agent with
`systemPromptMode: replace` cannot run in a Piglet Binary child (a Binary keeps its own system prompt).

```sh
pig install ./components/subagents
```

## What is ported

Frontmatter parsing, agent and chain definitions (discovery in the project, the user's directories and the builtin set,
precedence project over user over builtin, packages, aliases, name resolution), markdown chain files (parse and write), the
`subagent` tool's `list` and `get`, the `subagents_enable` loader, and running one agent or a chain through a child process (a Go
addition: the original runs children through its own executor and scripted workflows).

**Not ported, and refused:** parallel, async and background runs, scripted workflows (`workflow`, `workflowScript`,
`workflowScriptPath`) and the legacy `tasks`/`chain` inputs, council mode, worktree isolation, the fleet and status views, steering and
the supervisor channel, the watchdog, missions, profiles and settings overrides, `.chain.json` chains, the management actions other
than `list` and `get`. The tool honors `agent`, `task`, `agentScope`, `cwd`, `model` and
`async: false` (the port always runs in the foreground) and, for `list` and `get`, `action`, `agent` and `agentScope`. Any other
parameter or action, and the tools `bg_wait` and `subagent_supervisor` (declared so the model sees the original's tools), is refused
with one message and nothing runs:

> @pi-in-go/pigpen-subagents (Go port) does not support async (background) runs (`async: true`) yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.

The original's slash commands other than the port's own `/run-chain` (`/run`, `/subagents`, `/subagents-stop`, `/prompt-workflow`, ...) are not
registered. The package ships the original's `pi-subagents` skill (as `pigpen-subagents`), its prompt templates and its tool reference
(`skills/`, `prompts/`, `docs/`); they describe features that other parts of the port still refuse. See [port/PORT.md](port/PORT.md).

**Run policy (third pass).** Besides the run options of the foreground run (output, acceptance, gate, deadlines, spawn budget,
capability ceiling), the extension carries the launch contract preflight (`resolveSubagentLaunchContract`: the resolved agent,
model, skills, tools, extensions, MCP tools, session and lifecycle paths and the digests that bind them), the child tool plan, the
Intercom bridge state, and the structured delegation API over the event bus (`prompt-template:subagent:request`, `started`,
`update`, `response`, `cancel`): another extension delegates one owned leaf and receives the terminal response. A delegated leaf
runs a text result in a child process; `toolBudget`, `skill`, `intercomBridge`, `artifacts` and a structured result are refused.
`outputSchema`, `structured_output` and `acceptance.report` stay refused; the child role that would host `structured_output`
(`child_role.go`) is written but not yet started by the foreground run (see PORT.md).
