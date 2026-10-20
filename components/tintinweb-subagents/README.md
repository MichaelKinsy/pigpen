# tintinweb-subagents (Go port, partial)

A Go extension for PiG that lets the model hand work to **subagents**, as Claude Code does: the `Agent` tool starts a
specialist (`general-purpose`, `Explore` or `Plan`, or one you define in a Markdown file), by default in the
background; you are notified when it finishes, `get_subagent_result` fetches the full answer, `steer_subagent`
redirects a running one, and `/agents` lists the types and the agents of the session. It is a port of
`@tintinweb/pi-subagents` 0.19.0 by tintinweb (see [CREDITS.md](CREDITS.md)), made with the
[extension porting Skill](../extension-port/README.md). It needs no Node runtime, and **starts no process until an
agent is started**.

**This is not [`components/subagents`](../subagents/README.md)**, the port of `nicobailon/pi-subagents`: a different
project with a different tool. The two can be installed side by side.

## Install

```sh
pig install ./components/tintinweb-subagents
```

or take the [`pig-essentials`](../../piglets/pig-essentials/README.md) Piglet, which selects it together with
`tintinweb-tasks`: `TaskExecute` of that port starts its agents through this one, over the same event-bus protocol
(version 2: `subagents:rpc:ping`, `spawn`, `stop`, `consume`, and the `subagents:ready`, `created`, `completed`,
`failed` and `steered` events).

## How an agent runs

Each agent is a child `pig --mode rpc --no-session` process, started in your workspace with the agent's tools and
system prompt, the parent's model and thinking level (unless the call or the agent file sets its own), and the task as
its prompt. The original runs the agent inside Pi's process through Pi's SDK; a Go extension cannot, so an agent here
costs one process (and one start-up of pig). A child registers no agent tools, so there is no nesting. The pig that
is started is, in order: `TINTINWEB_SUBAGENTS_PIG_BINARY`, the pig that started this extension (`PIG_HARNESS_BINARY`,
which PiG sets for its extension processes), the parent process (Linux and Android), this executable (in a Piglet
Binary, the Binary itself, so agents have the same built-in members), `pig` on `PATH`; the first that answers
`--version` is used. The child inherits your environment, so it reads the same credentials and settings.

An agent with a turn limit (`max_turns`, the agent file's `max_turns` or `defaultMaxTurns`) is told to wrap up when it
reaches the limit, and aborted if it is still working `graceTurns` turns later (default 5); it then reports as
`steered` or `aborted`, and a run whose last turn failed (a provider error) reports as an error, as in the original.
Stopping an agent (or ending the session) stops its process group: SIGTERM, then SIGKILL five seconds later.

## Agent files

Custom agents are Markdown files with YAML frontmatter, read from `<agent dir>/agents/`, `<workspace>/.agents/agents/`
and `<workspace>/.pi/agents/` (later ones win), exactly as in the original: `name`, `description`, `tools`
(`all`, `none`, a list, `ext:` selectors), `model`, `thinking`, `max_turns`, `prompt_mode`, `run_in_background`,
`enabled`, `disallowed_tools`, `extensions`, `skills`, `isolated` and the rest are parsed the same way (the 76 cases of
the original's `custom-agents` tests are twinned). Settings come from `<agent dir>/subagents.json` and
`<workspace>/.pi/subagents.json`: `maxConcurrent` (default 10), `defaultMaxTurns`, `graceTurns`, `fallbackSubagent` (an
unknown type goes to `general-purpose`, to a named agent, or with `"none"` is refused) and `disableDefaultAgents`.

## Workflows

`SubagentWorkflow` runs a user- or model-authored JavaScript script that orchestrates many agents: `agent()`, `parallel()`,
`pipeline()`, `phase()`, `log()`, `workflow()` (one level of nesting), `budget`, `args`, with caps (1000 agents, 4096 items
per fan-out, 256 nested runs), a concurrency semaphore, a journal for `resumeFromRunId`, saved workflows
(`.pi/workflows/<name>.js`, `.agents/workflows/`, `<agent dir>/workflows/`), `gate` commands, pause/skip/retry, and the
`--subagents-workflow-file=<path>` flag. It is on by default (`"workflowsEnabled": false` in `subagents.json` removes the
tool), and stands down for another extension's `Workflow` tool unless the setting is pinned. The script runs in-process in
a pure-Go JavaScript engine ([goja](https://github.com/dop251/goja)), with no Node; the engine's limits and every
difference from the original are in [port/PORT.md](port/PORT.md). A workflow's agents are children of the workflow: not in
`/agents`, no completion notice of their own, no slot in the session's concurrency limit. `isolation: "worktree"`,
`agent({ schema })` and `agent({ resume })` need features the port does not have yet and fail the run with the refusal
message below.
The `/agents → Workflows` menu (stop, pause, skip, retry, the run dialog) is not ported yet: `/agents` lists the runs and
says so with the same refusal message.

## What is not ported, and how it answers

This port ships the slice above. The original's other features are named in [port/slices.json](port/slices.json) and
[port/PORT.md](port/PORT.md): the workflow card, dialog and menu drawing (the runtime, the tool and the flag are ported, see
"Workflows"), scheduled runs, worktree isolation, `@name`
mentions, the widget, fleet and conversation-viewer UI, persistent agent memory, nested delegation, `inherit_context`,
resuming a finished agent's session, the settings menu, and output transcripts. The `Agent` tool is advertised with the
original's text and schema as it is with scheduling and worktree isolation switched off.

Nothing that is not ported is accepted and quietly ignored. Each of these is refused with one message, a tool error
(for the settings, a notification when a session starts):

> @pi-in-go/pigpen-tintinweb-subagents (Go port) does not support _the feature_ yet. Install the original extension,
> npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.

- Agent call parameters `schedule`, `isolation: "worktree"`, `inherit_context: true` and `resume`.
- Agent file fields that ask for an unported feature: `inherit_context: true`, `isolation: worktree`, `memory`,
  `allowed_subagents`, `ext:` tool selectors, `disallowed_tools`, an `extensions` list, `exclude_extensions`, `skills` as
  `false` or a list, `persist_session: true`, `output_transcript: true`, `session_dir`.
- `subagents:rpc:spawn` options other than `description`, `model`, `maxTurns`, `name`, `thinkingLevel` and
  `isBackground: true`.
- `subagents.json` settings with a value that asks for an unported feature (`schedulingEnabled: true`,
  `worktreeIsolation: true`, `scopeModels: true`, `defaultJoinMode: "group"`, `widgetMode`
  other than `off`, and so on). The settings the port matches are not flagged: `false` for the switches the port does
  not have, and the five it honors.

`isolated: true` starts the child with no extensions. The proof that the shipped slice matches the original is in
[port/PORT.md](port/PORT.md).
