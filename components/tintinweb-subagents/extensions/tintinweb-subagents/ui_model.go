package tintinweb_subagents

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// The data model of the subagent surfaces (widget, fleet list, conversation viewer, renderers): what they read of an
// agent, its live activity and the invocation snapshot. upstream: src/types.ts (AgentRecord, AgentInvocation),
// ui/agent-widget.ts (AgentActivity, AgentDetails).

// widgetMode selects which agents the widget shows. upstream: types.ts WidgetMode.
type widgetMode string

const (
	widgetModeAll        widgetMode = "all"
	widgetModeBackground widgetMode = "background"
	widgetModeOff        widgetMode = "off"
)

// viewerMarkdownMode is how much of the conversation viewer renders as Markdown. upstream: types.ts ViewerMarkdownMode.
type viewerMarkdownMode string

const (
	viewerMarkdownOff       viewerMarkdownMode = "off"
	viewerMarkdownAssistant viewerMarkdownMode = "assistant"
	viewerMarkdownAll       viewerMarkdownMode = "all"
)

// invocation is the snapshot of what a spawn asked for and got. upstream: types.ts AgentInvocation.
type invocation struct {
	// ModelName is the short label for tight rows, e.g. "haiku 4.5"; ModelID the canonical provider/id.
	ModelName, ModelID string
	Thinking           string
	// RequestedThinking and RequestedModel are what the caller asked for, kept only when they did not get it.
	RequestedThinking, RequestedModel string
	MaxTurns                          *int
	Isolated, InheritContext          bool
	RunInBackground                   bool
	Isolation                         string
}

// agentView is what the surfaces read of one agent. upstream: types.ts AgentRecord (the fields the UI reads).
type agentView struct {
	ID, Type, Description, Status string
	ToolUses                      int
	StartedAt                     int64 // Unix ms
	CompletedAt                   int64 // Unix ms, 0 while unfinished
	Error                         string
	Usage                         lifetimeUsage
	CompactionCount               int
	Invocation                    *invocation
	// IsBackground is nil when unknown (a cross-extension RPC spawn). upstream: AgentRecord.isBackground.
	IsBackground *bool
	// ParentAgentID and WorkflowID name the owner of a child agent; an owned child reports through its owner.
	ParentAgentID, WorkflowID string
	// HasSession: a pending agent has no session yet and is hidden from the fleet list until it starts.
	HasSession bool
}

// isTopLevelAgent: a record that is one of the session's own agents rather than one another agent or a workflow owns.
// upstream: agent-manager.ts isTopLevelAgent.
func isTopLevelAgent(a *agentView) bool { return a.ParentAgentID == "" && a.WorkflowID == "" }

// activeTool is one running tool of an agent: the key it is tracked under and its name.
type activeTool struct{ key, name string }

// agentActivity is an agent's live activity. upstream: ui/agent-widget.ts AgentActivity.
type agentActivity struct {
	ActiveTools  []activeTool // insertion ordered, as the original's Map
	ToolUses     int
	ResponseText string
	TurnCount    int
	MaxTurns     *int // nil: unlimited
	Session      sessionLike
}

// activitySource is where the surfaces find an agent's activity (a Map in the original).
type activitySource interface {
	activityOf(id string) *agentActivity
}

// activityMap is a plain activitySource.
type activityMap map[string]*agentActivity

func (m activityMap) activityOf(id string) *agentActivity { return m[id] }

// errorStatuses are the statuses of an error/non-success outcome (linger behavior, icon rendering).
var errorStatuses = map[string]bool{"error": true, "aborted": true, "steered": true, "stopped": true}

// toolDisplay maps a tool name to a human-readable action for activity descriptions.
var toolDisplay = map[string]string{
	"read": "reading", "bash": "running command", "edit": "editing", "write": "writing",
	"grep": "searching", "find": "finding files", "ls": "listing",
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// getDisplayName is the display name of any agent type (built-in or custom). upstream: getDisplayName.
func getDisplayName(typ string) string { return agentConfigView(typ).displayName }

// getPromptModeLabel is "twin" for append, nothing for replace. upstream: getPromptModeLabel.
func getPromptModeLabel(typ string) string {
	if agentConfigView(typ).promptMode == "append" {
		return "twin"
	}
	return ""
}

// invocationTags is upstream buildInvocationTags: both model forms come back so each surface can pick by width, and
// the "(asked X)" annotation is applied here so a value the spawn did not honor cannot be rendered as honored.
type invocationTags struct {
	ModelName, ModelID string
	Tags               []string
}

func asked(value, requested string) string {
	if value != "" && requested != "" && requested != value {
		return value + " (asked " + requested + ")"
	}
	return value
}

func buildInvocationTags(inv *invocation) invocationTags {
	if inv == nil {
		return invocationTags{}
	}
	var tags []string
	if t := asked(inv.Thinking, inv.RequestedThinking); t != "" {
		tags = append(tags, "thinking: "+t)
	}
	if inv.Isolated {
		tags = append(tags, "isolated")
	}
	if inv.Isolation == "worktree" {
		tags = append(tags, "worktree")
	}
	if inv.InheritContext {
		tags = append(tags, "inherit context")
	}
	if inv.RunInBackground {
		tags = append(tags, "background")
	}
	if inv.MaxTurns != nil {
		tags = append(tags, fmt.Sprintf("max turns: %d", *inv.MaxTurns))
	}
	return invocationTags{ModelName: asked(inv.ModelName, inv.RequestedModel), ModelID: asked(inv.ModelID, inv.RequestedModel), Tags: tags}
}

// truncateLine is text on a single line, at most n UTF-16 units: the first non-blank line, trimmed.
func truncateLine(text string, n int) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if jsstring.Trim(l) != "" {
			line = jsstring.Trim(l)
			break
		}
	}
	if jsstring.Length(line) <= n {
		return line
	}
	return jsstring.Slice(line, 0, n) + "…"
}

// describeActivity is a human-readable activity from the running tools or the response text.
func describeActivity(active []activeTool, responseText string) string {
	if len(active) > 0 {
		var order []string
		counts := map[string]int{}
		for _, t := range active {
			action := toolDisplay[t.name]
			if action == "" {
				action = t.name
			}
			if _, seen := counts[action]; !seen {
				order = append(order, action)
			}
			counts[action]++
		}
		var parts []string
		for _, action := range order {
			if n := counts[action]; n > 1 {
				noun := "files"
				if action == "searching" {
					noun = "patterns"
				}
				parts = append(parts, fmt.Sprintf("%s %d %s", action, n, noun))
			} else {
				parts = append(parts, action)
			}
		}
		return strings.Join(parts, ", ") + "…"
	}
	// No tools active: show truncated response text if available.
	if jsstring.Trim(responseText) != "" {
		return truncateLine(responseText, 60)
	}
	return "thinking…"
}

// formatDuration is the duration between two timestamps, a running agent's time so far marked.
// upstream: ui/agent-widget.ts formatDuration.
func formatDuration(startedAt, completedAt int64) string {
	if completedAt != 0 {
		return formatMs(completedAt - startedAt)
	}
	return formatMs(nowMs()-startedAt) + " (running)"
}
