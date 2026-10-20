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

## Transcripts, usage and cost

Each agent started by the `Agent` tool writes a JSON-lines transcript to
`<temp dir>/pi-subagents-<uid>/<encoded cwd>/<session id>/tasks/<agent id>.output` (directory mode 0700), in the original's
format: the prompt first, then every message at each turn end, surviving compactions. The path is in the background
start text (`Output file:`) and the completion notification (`<output-file>`, and a `Full transcript available at:`
line). `outputTranscript` in `subagents.json` sets the default (on) and an agent file's `output_transcript` overrides it.
The data comes from the child's `--mode rpc` event stream: a token count is the sum of its assistant messages'
input, output and cache-write tokens (cache reads are excluded, as in the original), and the cost is pi's own estimate.
`<context_percent>` and `<compactions>` appear in the notification, `Context:` and `Compactions:` in
`get_subagent_result` (also `verbose: true`, which appends the conversation) and in `steer_subagent`'s answer;
`subagents:started` and `subagents:compacted` are emitted, and `subagents:completed` / `subagents:failed` carry `tokens` and
a pi `usage`. `reportUsage: true` attaches the agents' spend to the next tool result so the parent session counts it.
`showCost: true` adds `~$0.0042` to the tool results and `<estimated_cost_usd>` to the notification; the widgets that
show it are another slice, so the setting is still announced as not fully supported.

## What is not ported, and how it answers

This port ships the slice above. The original's other features are named in [port/slices.json](port/slices.json) and
[port/PORT.md](port/PORT.md): the `SubagentWorkflow` JS workflow runtime, scheduled runs, worktree isolation, `@name`
mentions, the widget, fleet and conversation-viewer UI, persistent agent memory, nested delegation, `inherit_context`,
resuming a finished agent's session and the settings menu. The `Agent` tool is advertised with the
original's text and schema as it is with workflows, scheduling and worktree isolation switched off, and
`SubagentWorkflow` is not registered.

Nothing that is not ported is accepted and quietly ignored. Each of these is refused with one message, a tool error
(for the settings, a notification when a session starts):

> @pi-in-go/pigpen-tintinweb-subagents (Go port) does not support _the feature_ yet. Install the original extension,
> npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.

- Agent call parameters `schedule`, `isolation: "worktree"`, `inherit_context: true` and `resume`.
- Agent file fields that ask for an unported feature: `inherit_context: true`, `isolation: worktree`, `memory`,
  `allowed_subagents`, `ext:` tool selectors, `disallowed_tools`, an `extensions` list, `exclude_extensions`, `skills` as
  `false` or a list, `persist_session: true`, `session_dir`.
- `subagents:rpc:spawn` options other than `description`, `model`, `maxTurns`, `name`, `thinkingLevel` and
  `isBackground: true`.
- `subagents.json` settings with a value that asks for an unported feature (`schedulingEnabled: true`,
  `workflowsEnabled: true`, `worktreeIsolation: true`, `scopeModels: true`, `defaultJoinMode: "group"`, `widgetMode`
  other than `off`, and so on). The settings the port matches are not flagged: `false` for the switches the port does
  not have, and the five it honors.

`isolated: true` starts the child with no extensions. The proof that the shipped slice matches the original is in
[port/PORT.md](port/PORT.md).
