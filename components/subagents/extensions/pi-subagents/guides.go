package pi_subagents

import "strings"

// The port's guide topics (the original's readSubagentGuide serves the
// original's packaged docs; the port authors its own so every line describes
// what this port actually runs).

func portGuideTopics() []string {
	return []string{
		"overview", "agents", "configuration", "models", "tool-reference",
		"workflows", "missions", "observability", "watchdog", "extension-api", "council",
	}
}

const guideUnported = "\n\nThis topic documents the original extension's runtime, which this Go port does not carry: install the original (npm:pi-subagents@0.73.1), which runs unchanged in PiG, or read the port's tool-reference topic for the actions this port runs."

// portGuide returns the port's guide for a topic.
func portGuide(topic string) (string, bool) {
	switch topic {
	case "overview":
		return `# pi-subagents (Go port)

Delegate one step of your work to a specialist agent defined in a markdown file.

Activation: the subagent tool is not in your tools until subagents_enable is called, as in the original. Call it when delegation is authorized by the operator's current request or applicable instructions; complexity alone never authorizes delegation.

Definitions live in .pi/agents (the project), ~/.pi/agent/agents (you), and the builtin set that ships with the package. An agent file is frontmatter (name, description, model, thinking, tools, skills, aliases, systemPromptMode, ...) followed by its system prompt.

Actions this port runs:
- subagent {agent, task} — run one agent. The run detaches by default (asyncByDefault); async:false blocks the parent, as in the original.
- subagent {action: "list"|"get"|"models"|"create"|"update"|"delete"|"eject"|"disable"|"enable"|"reset"|"status"|"stop"|"interrupt"} — management, models and run control.
- subagent {action: "guide", topic: "..."} — these guides.
- bg_wait — block the turn until tracked async runs finish.
- /run-chain <chain> <task> — run a .chain.md or .chain.json chain, one child per step.

Refused with a message that names the original: workflow scripts, parallel fan-out, worktree isolation, council mode, missions, the watchdog, profiles, steering and resume.`, true
	case "agents":
		return `# Agents

An agent is a markdown file with frontmatter and a system prompt. Discovery order: project (.agents and .pi/agents of the nearest project root), user (~/.agents, ~/.pi/agent/agents, PI_SUBAGENT_EXTRA_AGENT_DIRS), builtin. A project definition shadows a user one of the same name, which shadows a builtin.

Frontmatter this port reads: name, package, description, advertise, aliases, tools (mcp: names become direct MCP tools), excludeTools, allowNestedSubagents, allowedAgents, model, fast, thinking, systemPromptMode, inheritProjectContext, inheritGlobalContext, inheritSkills, defaultContext, runner, async, timeoutMs, toolTimeoutMs, acceptance, acceptanceRole, skills, skillPath, extensions, subagentOnlyExtensions, mutationTools, machine, output, outputMode, outputSchema, defaultReads, defaultProgress, interactive, maxSubagentDepth, toolBudget. Unknown keys are kept when the file is rewritten.

Management actions: create, update (renames move the file), delete, eject (copy a builtin or package agent into user or project scope), disable and enable (a settings override at settings.subagents.agentOverrides), reset (back to the bundled default). agentScope picks user or project; list and get show the effective set, and a disabled agent is hidden from both.`, true
	case "configuration":
		return `# Configuration

Extension config: <agent dir>/extensions/subagent/config.json, the keys this port runs:
- asyncByDefault (default true): a subagent call without async detaches.
- timeoutMs: the default run deadline (30 minutes when unset).
- maxActiveAsyncRunsPerSession: cap on this session's concurrent async runs (0 = unlimited).
- waitTool: {enabled, defaultTimeoutMs} for bg_wait; PI_SUBAGENT_WAIT_TOOL_ENABLED and PI_SUBAGENT_WAIT_TOOL_DEFAULT_TIMEOUT_MS override it.

Environment: PI_SUBAGENT_PIG_BINARY names the pig to start for a child; PI_SUBAGENT_MAX_DEPTH changes the nesting limit (2). PI_SUBAGENTS_TEMP_ROOT moves the async run artifacts.`, true
	case "models":
		return `# Models

An agent's model is its frontmatter model. Pass an exact provider/id; a bare id resolves only when it is unique in the session's registry, and a :level suffix (off, minimal, low, medium, high, xhigh, max) overrides the agent's thinking. subagent {action: "models"} lists every agent's effective model, its source and thinking, and the registry's exact ids. A run's model can be overridden per call with the model parameter.`, true
	case "tool-reference":
		return `# Tool reference

subagent:
- {agent, task, agentScope?, cwd?, model?, async?} — run one agent. cwd is the child's directory (relative to the session); a missing or file cwd is refused before launch.
- {action: "list", agentScope?} / {action: "get", agent, agentScope?} — the effective agent set, one definition in detail.
- {action: "models", agent?, agentScope?} — effective models and the registry's exact ids.
- {action: "create"|"update", config} / {action: "delete"|"eject"|"disable"|"enable"|"reset", agent, agentScope?} — management.
- {action: "status", id?, view?} — the active async runs, one run's status, or its transcript tail (view: fleet|transcript).
- {action: "stop"|"interrupt", id} — stop a running async run's child.
- {action: "guide", topic} — these guides.

bg_wait: {} waits for the first tracked async run to finish, {all: true} for every one, {id} for one run; timeoutMs bounds the wait.

/run-chain <chain> <task> — runs a .chain.md or .chain.json chain; each step's answer flows into the next as {previous}, named outputs as {outputs.name}.`, true
	case "workflows":
		return "# Workflows (scripted, JavaScript)" + guideUnported, true
	case "missions":
		return "# Missions" + guideUnported, true
	case "observability":
		return "# Observability (fleet, transcripts, inspectors)" + guideUnported, true
	case "watchdog":
		return "# Watchdog" + guideUnported, true
	case "extension-api":
		return "# Extension API" + guideUnported, true
	case "council":
		return "# Council mode" + guideUnported, true
	}
	return "", false
}

// guideTopicList is the original's list order for the unknown-topic message.
func guideTopicList() string { return strings.Join(portGuideTopics(), ", ") }
