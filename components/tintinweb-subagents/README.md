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

Each agent is a child `pig --mode rpc` process, started in your workspace with the agent's tools and system prompt,
the parent's model and thinking level (unless the call or the agent file sets its own), and the task as its prompt. The original runs the agent inside Pi's process through Pi's SDK; a Go extension cannot, so an agent here
costs one process (and one start-up of pig). A child registers no agent tools, so there is no nesting. The pig that
is started is, in order: `TINTINWEB_SUBAGENTS_PIG_BINARY`, the pig that started this extension (`PIG_HARNESS_BINARY`,
which PiG sets for its extension processes), the parent process (Linux and Android), this executable (in a Piglet
Binary, the Binary itself, so agents have the same built-in members), `pig` on `PATH`; the first that answers
`--version` is used. The child inherits your environment, so it reads the same credentials and settings.

An agent with a turn limit (`max_turns`, the agent file's `max_turns` or `defaultMaxTurns`) is told to wrap up when it
reaches the limit, and aborted if it is still working `graceTurns` turns later (default 5); it then reports as
`steered` or `aborted`, and a run whose last turn failed (a provider error) reports as an error, as in the original.
Stopping an agent whose conversation is in a session file, or ending the session, stops its process group: SIGTERM,
then SIGKILL five seconds later. Stopping an agent whose conversation is in memory (`persist_session: false`, or
`rememberAgents: false`) only stops its run, as the original's `session.abort()` does: its process, and so the
conversation, stays for a `resume`; a run that does not stop within ten seconds of the stop has its process ended.

## Sessions, resume and context

An agent's conversation is a pi session file by default (the original's `rememberAgents` setting, on unless
`subagents.json` turns it off; the agent file's `persist_session` overrides it either way). The child is started with
a new session recorded under the parent's (`parentSession`, so pi's `/resume` nests it), in pi's normal session
directory or the agent file's `session_dir` (`~`, an absolute path, or relative to the workspace); the session it runs
in is named `<type>#<first 8 characters of the agent id>` (set with `set_session_name` after the new session starts,
because `--name` names only the first session of a pig process). `Agent({ resume: "<agent id>", prompt })` continues the same
conversation: a persisted agent whose process has ended is reopened with `pig --session <file>` (with the spec it
started with and no turn limit), an agent whose conversation is in memory (`persist_session: false`) keeps its process
and takes the prompt into the same session. The call is detached unless `run_in_background: false`, as for a spawn. A
finished agent's record, and the process that holds an in-memory conversation, are removed ten minutes after it
finished (a timer looks every minute, as in the original); a persisted one leaves a tombstone (its handle and session
file) that keeps its handle reserved. A session start or switch removes the finished agents whose result has been read
and forgets every tombstone, and the end of the session closes every process that holds a conversation. Reopening a
tombstone by `@handle` (`resolveMention`, `spawn` with `resumeSessionFile` and `reclaim`) waits for the input hook, which
is the mention slice.

`inherit_context` (the call parameter, or `inherit_context: true` in the agent file, which wins) puts the parent
conversation in front of the task, as text: the user and assistant messages of the current branch and the compaction
summaries, as `# Parent Conversation Context ... # Your Task (below)`.

`skills:` in an agent file: `true` (the default) lets the child load its own skills, `false` starts it with
`--no-skills`, a list preloads those skills into the system prompt (`# Preloaded Skill: <name>`) and starts it with
`--no-skills`. `extensions:` and `exclude_extensions:` choose the extensions the child loads, `tools:` with `ext:`
selectors and `disallowed_tools` narrow its tools, and `isolated` (the call, the agent file, or the spawn RPC) means no
extensions, no skills and no `ext:` selectors, exactly as in the original. A child never loads project context files,
`APPEND_SYSTEM.md`, prompt templates or themes, and trusts the project only when the parent does. `tools:` with `ext:`
selectors and `disallowed_tools` are applied as pig flags (`--exclude-tools`, `--tools`) over the tools the parent's
registry knows; see `port/PORT.md` for what that cannot reach.

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
| `rememberAgents` (true) | an agent's conversation is a pi session file (see Sessions, resume and context below); `false` keeps it in the child's memory |

A model named by the call (or by an agent file) is resolved against the models with credentials: `provider/modelId`, else
a fuzzy match (`haiku`, `sonnet 4.5`, a dot or a dash in a version); one that matches nothing is an error result for the
call's model and a silent fallback to the parent's for the file's. The other keys are parsed and kept for the slices
that own them (see below); asking for one of them is refused.

## What is not ported, and how it answers

This port ships the slice above. The original's other features are named in [port/slices.json](port/slices.json) and
[port/PORT.md](port/PORT.md): the `SubagentWorkflow` JS workflow runtime, scheduled runs, worktree isolation, `@name`
mentions, the widget, fleet and conversation-viewer UI, persistent agent memory, nested delegation, the settings
menu (the store can write `.pi/subagents.json`, nothing calls it yet), and output transcripts. The `Agent` tool is advertised with the
original's text and schema as it is with workflows, scheduling and worktree isolation switched off, and
`SubagentWorkflow` is not registered. (The text for every combination of those three features and the three
`toolDescriptionMode`s is built and proved against what Pi sends, so a slice that ports a feature only switches it on in
`features.go`.)

Nothing that is not ported is accepted and quietly ignored. Each of these is refused with one message, a tool error
(for the settings, a notification when a session starts):

> @pi-in-go/pigpen-tintinweb-subagents (Go port) does not support _the feature_ yet. Install the original extension,
> npm:@tintinweb/pi-subagents@0.19.0, which runs unchanged in PiG.

- Agent call parameters `schedule` and `isolation: "worktree"`.
- Agent file fields that ask for an unported feature: `isolation: worktree`, `memory`, `allowed_subagents`,
  `output_transcript: true`.
- `subagents:rpc:spawn` options other than `description`, `model`, `maxTurns`, `name`, `thinkingLevel`, `isolated`,
  `inheritContext`, `bypassQueue` and `isBackground`. (`parentAgentId`, `workflowId`, `depth`, `maxSubagentDepth`,
  `configCwd`, `rootSessionId`, `resumeSessionFile`, `reclaim` and `blocking` are dropped, as the original drops what a
  caller sends: only the extension itself may set them. `isolated`, `inheritContext` and `bypassQueue` count when truthy,
  as in JavaScript.)
- `subagents.json` settings with a value that asks for an unported feature (`schedulingEnabled: true`,
  `workflowsEnabled: true`, `worktreeIsolation: true`, `widgetMode` other than `off`, and so on). The settings the
  port matches are not flagged: `false` for the switches the port does not have, and the ones it honors.

The proof that the shipped slice matches the original is in
[port/PORT.md](port/PORT.md).
