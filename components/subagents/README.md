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

- `subagent {action: "validate", workflowScript: "..."}` (or `workflowScriptPath`) checks a workflow script without launching a child and answers the original's JSON (`{ok, errors, warnings?}`);
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

**Also ported (agent administration):** the system-prompt advertisement of agents that opt in with `advertise: true` (an
`advertised_subagents` prompt section, refreshed by management mutations, held back until the global package catalog is complete),
agent refinements (`refine`, `refine.show`, `refine.rollback`), the `doctor` and `guide` actions, subagent profiles, the commands
`/subagents` (inspect, model, thinking, system prompt), `/subagents-models`, `/subagents-profiles`, `/subagents-load-profile`,
`/subagents-refresh-provider-models`, `/subagents-generate-profiles`, `/subagents-check-profile`, `/subagents-doctor`,
`/subagents-guide` and `/subagents-refine`, the prompt-template delegation bridge on the event bus (a text result of one child; the
options a port child cannot honor fail with the refusal text), and the child prompt rewriting, extension plan, extension bindings,
fast-mode and prompt-audit helpers. The runtime agent registry (`registerAgent`), the selector overlay and the live slash result
widget are not ported.

**Not ported, and refused:** parallel, async and background runs, scripted workflows (`workflow`, `workflowScript`,
`workflowScriptPath`) and the legacy `tasks`/`chain` inputs, council mode, worktree isolation, the fleet and status views, steering and
the supervisor channel, the watchdog, profiles and settings overrides, `.chain.json` chains, the management actions other
than `list`, `get` and the `mission.*` actions. The tool honors `agent`, `task`, `agentScope`, `cwd`, `model`, `mission`, `missionId` and
`async: false` (the port always runs in the foreground) and, for `list` and `get`, `action`, `agent` and `agentScope`.
**Missions** work as in the original: every task launch records a mission (disable with `mission: false` or
`config.missions.enabled: false` in `~/.pi/agent/extensions/subagent/config.json`), and `subagent {action: "mission.create" | "mission.list" | "mission.show" | "mission.update" | "mission.resolve-decision" | "mission.attach-run" | "mission.close"}` manages them.
`/prompt-workflow <name> [args]` lists and resolves prompt templates; running one needs the workflow runtime and answers with the refusal above until it is wired. Any other
parameter or action, and the tools `bg_wait` and `subagent_supervisor` (declared so the model sees the original's tools), is refused
with one message and nothing runs:

> @pi-in-go/pigpen-subagents (Go port) does not support async (background) runs (`async: true`) yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.

The original's slash commands other than the port's own `/run-chain` (`/run`, `/subagents`, `/subagents-stop`, ...) are not
registered (`/prompt-workflow` is). The package ships the original's `pi-subagents` skill (as `pigpen-subagents`), its prompt templates and its tool reference
(`skills/`, `prompts/`, `docs/`); they describe features that other parts of the port still refuse. See [port/PORT.md](port/PORT.md).

**Run policy (third pass).** Besides the run options of the foreground run (output, acceptance, gate, deadlines, spawn budget,
capability ceiling), the extension carries the launch contract preflight (`resolveSubagentLaunchContract`: the resolved agent,
model, skills, tools, extensions, MCP tools, session and lifecycle paths and the digests that bind them), the child tool plan, the
Intercom bridge state, and the structured delegation API over the event bus (`prompt-template:subagent:request`, `started`,
`update`, `response`, `cancel`): another extension delegates one owned leaf and receives the terminal response. A delegated leaf
runs a text result in a child process; `toolBudget`, `skill`, `intercomBridge`, `artifacts` and a structured result are refused.
`outputSchema`, `structured_output` and `acceptance.report` stay refused; the child role that would host `structured_output`
(`child_role.go`) is written but not yet started by the foreground run (see PORT.md).

## Embedded JavaScript (owner-approved exception)

Running a model-written workflow script needs a JavaScript interpreter; the Pigpen porting skill otherwise forbids one. The owner approved
the pure-Go interpreter [goja](https://github.com/dop251/goja) (MIT) inside this extension only, exposing exactly what the original's
`node:vm` sandbox exposes (the decision of 2026-10-20 is recorded in [port/PORT.md](port/PORT.md) and the skill's exception list). This
change ships the sandbox, the static `validate` action and the receipts, checklists and chain helpers around it; a run that names
`workflow`, `workflowScript` or `workflowScriptPath` is still refused until the launcher drives real child runs.
