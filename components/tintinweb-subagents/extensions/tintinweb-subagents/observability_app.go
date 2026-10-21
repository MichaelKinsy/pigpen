package tintinweb_subagents

import (
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The observability wiring of the app: the `.output` transcript, usage reporting to the parent session, cost in the
// texts, the `subagents:started` and `subagents:compacted` events and the live stats of an agent. upstream:
// src/index.ts (attachTranscript, withUsageReporting, buildEventData, formatTaskNotification), src/output-file.ts.

// obsState is the settings of this slice (settings.go) and the usage pool. Defaults: transcripts on,
// usage reporting and cost display off. upstream: output-file.ts getOutputTranscriptDefault; index.ts reportUsage, showCost.
type obsState struct {
	mu                    sync.Mutex
	noTranscript          bool // outputTranscript: false
	reportUsage, showCost bool
	pool                  pendingUsagePool
}

// reloadObs applies the settings of a workspace.
func (a *app) reloadObs(cwd string) {
	s := loadSettings(cwd)
	a.obs.mu.Lock()
	a.obs.noTranscript = s.OutputTranscript != nil && !*s.OutputTranscript
	a.obs.reportUsage = s.ReportUsage != nil && *s.ReportUsage
	a.obs.showCost = s.ShowCost != nil && *s.ShowCost
	report := a.obs.reportUsage
	a.obs.mu.Unlock()
	if !report {
		// Whatever accumulated while it was on is stale the moment it goes off. upstream: index.ts setReportUsage.
		a.obs.pool.drain()
	}
}

func (a *app) outputTranscriptDefault() bool {
	a.obs.mu.Lock()
	defer a.obs.mu.Unlock()
	return !a.obs.noTranscript
}

func (a *app) showCost() bool {
	a.obs.mu.Lock()
	defer a.obs.mu.Unlock()
	return a.obs.showCost
}

// wireObs connects the manager's hooks. upstream: index.ts `new AgentManager(...)` callbacks.
func (a *app) wireObs() {
	a.mgr.onSpawn = a.attachTranscript
	a.mgr.onStart = func(r *agentRecord) {
		if ctx, ok := a.context(); ok {
			_ = ctx.Events().Emit("subagents:started", map[string]any{"id": r.ID, "type": r.Type, "description": r.Description})
		}
	}
	a.mgr.onCompact = func(r *agentRecord, reason string, tokensBefore, count int) {
		if ctx, ok := a.context(); ok {
			_ = ctx.Events().Emit("subagents:compacted", map[string]any{"id": r.ID, "type": r.Type, "description": r.Description,
				"reason": reason, "tokensBefore": tokensBefore, "compactionCount": count})
		}
	}
	a.mgr.onUsage = func(_ *agentRecord, u lifetimeUsage) {
		// Every assistant message of every agent, exactly once; skipped when the feature is off so no pool grows
		// in a session that will never drain it.
		a.obs.mu.Lock()
		on := a.obs.reportUsage
		a.obs.mu.Unlock()
		if on {
			a.obs.pool.add(u)
		}
	}
}

// attachTranscript creates the agent's `.output` file with its initial prompt entry and the stream that appends the
// run's turns; record.OutputFile is the sole gate every consumer keys off. upstream: index.ts attachTranscript.
func (a *app) attachTranscript(r *agentRecord) {
	if !r.spec.Transcript {
		return
	}
	path, err := createOutputFilePath(r.spec.Cwd, r.ID, r.spec.SessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[pi-subagents] no transcript for agent %s: %v\n", r.ID, err)
		return
	}
	writeInitialEntry(path, r.ID, r.spec.Prompt, r.spec.Cwd)
	a.mgr.mu.Lock()
	r.OutputFile = path
	r.transcript = newTranscriptStream(path, r.ID, r.spec.Cwd, 1)
	a.mgr.mu.Unlock()
}

// withUsageReporting wraps a tool so its result carries back whatever subagent spend the parent session has not been
// told about yet. Nothing is attached to a call with no tool-call id: the spend stays pending for the next real
// result. upstream: index.ts withUsageReporting.
func (a *app) withUsageReporting(fn sdk.ToolFunc) sdk.ToolFunc {
	return func(ctx sdk.Context, p map[string]any) (any, error) {
		out, err := fn(ctx, p)
		if err != nil {
			return out, err
		}
		a.obs.mu.Lock()
		on := a.obs.reportUsage
		a.obs.mu.Unlock()
		if !on || ctx.ToolCallID() == "" {
			return out, nil
		}
		res, ok := out.(sdk.ToolResult)
		if !ok {
			return out, nil
		}
		if usage := a.obs.pool.drain(); usage != nil {
			res.Usage = usage
		}
		return res, nil
	}
}

// contextPercent is an agent's context-window utilization: live for a running agent, as it settled otherwise; nil
// when unavailable. upstream: usage.ts getSessionContextPercent.
func (a *app) contextPercent(r *agentRecord) *float64 {
	a.mgr.mu.Lock()
	c, status, done := r.child, r.Status, r.ContextPercent
	a.mgr.mu.Unlock()
	if status == statusRunning {
		if lc, ok := c.(liveChild); ok {
			return lc.stats()
		}
		return nil
	}
	return done
}

// roundPercent is Math.round on a percentage.
func roundPercent(p float64) int { return int(math.Floor(p + 0.5)) }

// conversation is the agent's messages as text, for get_subagent_result's verbose. "" when it has none.
func (a *app) conversation(r *agentRecord) string {
	a.mgr.mu.Lock()
	c, status, settled := r.child, r.Status, r.Conversation
	a.mgr.mu.Unlock()
	if status == statusRunning {
		lc, ok := c.(liveChild)
		if !ok {
			return ""
		}
		return conversationOf(lc.messages())
	}
	return settled
}

// contentText is context.ts extractText: the text parts of a content array, joined by newlines.
func contentText(content any) string {
	var parts []string
	for _, x := range asList(content) {
		if c, ok := x.(map[string]any); ok && c["type"] == "text" {
			t, _ := c["text"].(string)
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

// getAgentConversation formats an agent's messages as text. upstream: agent-runner.ts getAgentConversation.
func getAgentConversation(messages []map[string]any) string {
	var parts []string
	for _, msg := range messages {
		parts = append(parts, conversationParts(msg)...)
	}
	return strings.Join(parts, "\n\n")
}

// conversationParts is what one message adds to the conversation text (nothing for a system message or an empty one).
func conversationParts(msg map[string]any) []string {
	var parts []string
	switch msg["role"] {
	case "user":
		text, ok := msg["content"].(string)
		if !ok {
			text = contentText(msg["content"])
		}
		if t := strings.TrimSpace(text); t != "" {
			parts = append(parts, "[User]: "+t)
		}
	case "assistant":
		var texts, calls []string
		for _, x := range asList(msg["content"]) {
			c, _ := x.(map[string]any)
			switch c["type"] {
			case "text":
				if t, _ := c["text"].(string); t != "" {
					texts = append(texts, t)
				}
			case "toolCall":
				name, _ := c["name"].(string)
				if name == "" {
					name, _ = c["toolName"].(string)
				}
				if name == "" {
					name = "unknown"
				}
				calls = append(calls, "  Tool: "+name)
			}
		}
		if len(texts) > 0 {
			parts = append(parts, "[Assistant]: "+strings.Join(texts, "\n"))
		}
		if len(calls) > 0 {
			parts = append(parts, "[Tool Calls]:\n"+strings.Join(calls, "\n"))
		}
	case "toolResult":
		text := contentText(msg["content"])
		if u := []rune(text); len(u) > 200 { // JavaScript counts UTF-16 units; the two agree below the astral plane
			text = string(u[:200]) + "..."
		}
		name, ok := msg["toolName"].(string)
		if !ok {
			name = "undefined" // as a template literal prints a missing name
		}
		parts = append(parts, "[Tool Result ("+name+")]: "+text)
	}
	return parts
}
