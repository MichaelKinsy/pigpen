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
`<workspace>/.pi/subagents.json` (the project file wins key by key), read once when the extension loads, as in the
original; all 24 keys are parsed alike, and a value of the wrong shape is dropped silently. Honored:

| Key | Meaning |
|---|---|
| `maxConcurrent` (10), `maxConcurrentForeground` (0 = unlimited) | the two independent pools: background agents, and the agents a call blocks on inline; the rest wait in one queue, first in first out within a pool |
| `defaultMaxTurns` (0 = unlimited), `graceTurns` (5) | the turn limit and the turns allowed after the wrap-up message |
| `backgroundByDefault` (true) | what an `Agent` call that does not say `run_in_background` means; an agent file's own `run_in_background` and then the call's win over it |
| `defaultJoinMode` (`smart`) | `async`: one notification per finished background agent; `group` and `smart`: one notification for the agents spawned in the same turn (a 100 ms debounce window), partial after 30 s |
| `scopeModels` (false) | a model the call names outside `enabledModels` of Pi's settings is an error result listing the allowed ones; a model an agent file pins, or the parent's, only warns |
| `strictAgentFiles` (false) | an unreadable agent file aborts the load, naming the file (at start-up only; every later reload warns) |
| `toolDescriptionMode` (`full`) | `compact` is the short Agent description; `custom` renders `.pi/agent-tool-description.md` (else `<agent dir>/agent-tool-description.md`) with `{{typeList}}`, `{{compactTypeList}}`, `{{agentDir}}`, `{{scheduleGuideline}}` and `{{isolationGuideline}}`; read once at registration |
| `fallbackSubagent`, `disableDefaultAgents` | an unknown type goes to `general-purpose`, to a named agent, or with `"none"` is refused; the three default agents can be switched off |

A model named by the call (or by an agent file) is resolved against the models with credentials: `provider/modelId`, else
a fuzzy match (`haiku`, `sonnet 4.5`, a dot or a dash in a version); one that matches nothing is an error result for the
call's model and a silent fallback to the parent's for the file's. The other keys are parsed and kept for the slices
that own them (see below); asking for one of them is refused.

## What is not ported, and how it answers

This port ships the slice above. The original's other features are named in [port/slices.json](port/slices.json) and
[port/PORT.md](port/PORT.md): the `SubagentWorkflow` JS workflow runtime, scheduled runs, worktree isolation, `@name`
mentions, the widget, fleet and conversation-viewer UI, persistent agent memory, nested delegation, `inherit_context`,
resuming a finished agent's session, the settings menu (the store can write `.pi/subagents.json`, nothing calls it yet), and output transcripts. The `Agent` tool is advertised with the
original's text and schema as it is with workflows, scheduling and worktree isolation switched off, and
`SubagentWorkflow` is not registered. (The text for every combination of those three features and the three
`toolDescriptionMode`s is built and proved against what Pi sends, so a slice that ports a feature only switches it on in
`features.go`.)

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
  `workflowsEnabled: true`, `worktreeIsolation: true`, `widgetMode` other than `off`, and so on). The settings the
  port matches are not flagged: `false` for the switches the port does not have, and the ones it honors.

`isolated: true` starts the child with no extensions. The proof that the shipped slice matches the original is in
[port/PORT.md](port/PORT.md).
