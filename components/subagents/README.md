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

The model sees the `subagents_enable` tool, which makes the `subagent` tool available (it is not offered until then, as in the
original), the `bg_wait` tool, and `subagent_supervisor` (declared so the model sees the original's tools; it is refused, see below). Then:

- `subagent {action: "list"}` lists the agents (`agentScope`: user, project or both), `{action: "get", agent: "helper"}` shows one;
- `subagent {action: "validate", workflowScript: "..."}` (or `workflowScriptPath`) checks a workflow script without launching a child
  and answers the original's JSON (`{ok, errors, warnings?}`: the nested-async rule, the static rules, syntax errors, preflight);
- the management actions `models`, `create`, `update`, `delete`, `eject`, `disable`, `enable`, `reset`, the refinement actions
  `refine`, `refine.show`, `refine.rollback`, and the `doctor` and `guide` actions work as in the original;
- `subagent {agent: "helper", task: "..."}` runs one agent in a child `pig --print` process;
- a run without `async` goes to the background (the original's `asyncByDefault`): the call returns at once, the result arrives as
  a message that wakes the session, `{action: "status"}`, `interrupt`, `resume` and `stop` manage runs, `grant-spawn-budget`
  raises the spawn budget, and `bg_wait` waits for runs. `async: false` runs in the foreground. A run survives its session and
  the next session adopts it;
- the run options `output`, `outputMode`, `artifacts`, `acceptance`, `gate`, `agentContract`, `timeoutMs`, `maxRuntimeMs` and `includeProgress`;
- `/run-chain <chain> <task>` runs a `.chain.md` chain (`.pi/chains/` or `~/.pi/agent/chains/`): each step's answer flows into the
  next as `{previous}`, the request as `{task}`;
- `mission`, `missionId` and the `mission.create`, `mission.list`, `mission.show`, `mission.update`, `mission.resolve-decision`,
  `mission.attach-run`, `mission.close` actions: every task launch records a mission (turn it off with `mission: false` or
  `config.missions.enabled: false` in `~/.pi/agent/extensions/subagent/config.json`) and the result ends with `Mission: <id> (<status>)`;
  a background run does not take mission options yet and refuses them;
- `/prompt-workflow <name> [args]` lists and resolves prompt templates (package, user, project) into the workflow script they
  generate; running one needs the workflow runtime and is refused until the launcher is wired;
- the commands `/subagents` (inspect, model, thinking, system prompt), `/subagents-models`, `/subagents-profiles`,
  `/subagents-load-profile`, `/subagents-refresh-provider-models`, `/subagents-generate-profiles`, `/subagents-check-profile`,
  `/subagents-doctor`, `/subagents-guide` and `/subagents-refine`.

Thirteen agents ship with the package (the original's `scout`, `worker`, `reviewer`, ...). `PIG_SUBAGENT_PIG_BINARY` names the pig
to start for a child; otherwise the pig running this extension, then a `pig` beside this program, then `PATH`. A child gets the
task on stdin, runs with `--no-extensions`, and may itself delegate only to depth 2 (`PI_SUBAGENT_MAX_DEPTH`, as in the
original); at most 4 children run at once, and a cancelled foreground call (Escape) stops its child. An agent with
`systemPromptMode: replace` cannot run in a Piglet Binary child (a Binary keeps its own system prompt).

```sh
pig install ./components/subagents
```

## What is ported

Frontmatter parsing, agent and chain definitions (discovery in the project, the user's directories and the builtin set,
precedence project over user over builtin, packages, aliases, name resolution, settings overrides, disabled and excluded agents,
extra agent directories), markdown and `.chain.json` chain files, the `subagent` tool's management actions above, agent
refinements, subagent profiles and the system-prompt advertisement of agents that opt in with `advertise: true` (an
`advertised_subagents` section, refreshed by management mutations, held back until the global package catalog is complete),
background runs with the original's status, event and result files and its recovery (a later session adopts a run), the run policy
(output and artifact handling, acceptance and gates, deadlines, spawn budget, capability ceiling, read-only drain observation,
tool timeouts, mutation evidence), and the prompt-template delegation bridge on the event bus (a text result of one child).

**Background runs, completed in this PR:** retention of finished runs and their results, the status projection and snapshots
(`{action: "status"}` with the original's rows), the resume target (a run id, a prefix or a run directory), the interrupt action
and the completed-results store a `bg_wait` reads while the result watcher writes it, with the original's `async-*` twins and three
pigeq scenarios (`async-status`, `bg-wait-empty`, `unknown-action`). Steering a background child and workflow children
(`steer`) are still refused.

## Embedded JavaScript (owner-approved exception)

A model-written workflow script needs a JavaScript interpreter, which the Pigpen porting skill otherwise forbids. The owner approved
the pure-Go interpreter [goja](https://github.com/dop251/goja) (MIT) inside this extension only (decision of 2026-10-20, recorded in
[port/PORT.md](port/PORT.md) and the skill's exception list). This PR ships the sandbox the original's `node:vm` context provides
(`runs`, `args`, `emit`, `state`, `console`, the observed-promise rule, the static `validate` rules), the receipts, checklists,
graphs and chain helpers around it, and the `validate` action. A run that names `workflow`, `workflowScript` or `workflowScriptPath`
is still refused until the launcher drives real child runs (a later PR).

## Not ported yet, and refused

A call that names anything below is refused with one message and nothing runs, never accepted and ignored:

> @pi-in-go/pigpen-subagents (Go port) does not support <feature> yet. Install the original extension, npm:pi-subagents@0.73.1, which runs unchanged in PiG.

Parallel and chained runs through a workflow script (`workflow`, `workflowScript`, `workflowScriptPath`) and the legacy `tasks`,
`chain`, `parallel` inputs, council mode, worktree isolation, the fleet and status views, steering
and the supervisor channel (the `subagent_supervisor` tool), the watchdog, external CLI runners, the original's other slash
commands, and every management or control action not listed above. See [port/PORT.md](port/PORT.md) for the full ledger.
