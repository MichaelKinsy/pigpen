package tintinweb_subagents

import (
	"fmt"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// The details the Agent tool results and the completion notifications carry for the renderers.
// upstream: src/index.ts (buildDetails, buildNotificationDetails).

// usageOf is the lifetime usage and compaction count of an agent.
func (h *uiHub) usageOf(id string) (lifetimeUsage, int) {
	h.mu.Lock()
	u := h.agents[id]
	h.mu.Unlock()
	if u == nil {
		return lifetimeUsage{}, 0
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.usage, u.compactions
}

// usageOfAgent is the lifetime usage of an agent.
func (h *uiHub) usageOfAgent(id string) lifetimeUsage {
	usage, _ := h.usageOf(id)
	return usage
}

// costText is the cost of an agent as `~$0.0123`, or "" when showCost is off or the model has no pricing data (an
// unpriced model reports 0, and `$0.00` would claim the run was measured and free). upstream: the
// `showCost` + formatCost(getLifetimeCost(record.lifetimeUsage)) of index.ts.
func (h *uiHub) costText(id string) string {
	if !h.getShowCost() {
		return ""
	}
	return formatCost(getLifetimeCost(h.usageOfAgent(id)))
}

// invocationOf is a copy of an agent's invocation snapshot (nil when it has none).
func (h *uiHub) invocationOf(id string) *invocation {
	h.mu.Lock()
	u := h.agents[id]
	h.mu.Unlock()
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.inv == nil {
		return nil
	}
	c := *u.inv
	return &c
}

// notificationDetails is buildNotificationDetails: the data a notification renders from. The turn count and limit are
// those of the live activity tracker, which is gone by the time a settled agent is announced (so 0, as the original's).
func (h *uiHub) notificationDetails(r *agentRecord, resultMaxLen int) notificationDetails {
	usage, _ := h.usageOf(r.ID)
	h.app.mgr.mu.Lock()
	d := notificationDetails{ID: r.ID, Description: r.Description, Status: r.Status, ToolUses: r.ToolUses,
		TotalTokens: getLifetimeTotal(usage), TotalCost: getLifetimeCost(usage), Error: r.Error}
	if !r.CompletedAt.IsZero() {
		d.DurationMs = r.CompletedAt.Sub(r.StartedAt).Milliseconds()
	}
	result := r.Result
	h.app.mgr.mu.Unlock()
	switch {
	case result == "":
		d.ResultPreview = "No output."
	case jsstring.Length(result) > resultMaxLen:
		d.ResultPreview = jsstring.Slice(result, 0, resultMaxLen) + "…"
	default:
		d.ResultPreview = result
	}
	return d
}

// detailBase is what a result carries of the invocation: the display name, the model label and the tags (the mode
// label, which hangs off the agent type, ahead of the invocation's own tags).
type detailBase struct {
	displayName, description, subagentType, modelName string
	tags                                              []string
}

func newDetailBase(typ, description string, inv *invocation) detailBase {
	t := buildInvocationTags(inv)
	tags := t.Tags
	if label := getPromptModeLabel(typ); label != "" {
		tags = append([]string{label}, tags...)
	}
	return detailBase{displayName: getDisplayName(typ), description: description, subagentType: typ, modelName: t.ModelName, tags: tags}
}

func (b detailBase) details() agentDetails {
	return agentDetails{DisplayName: b.displayName, Description: b.description, SubagentType: b.subagentType, ModelName: b.modelName, Tags: b.tags}
}

// recordDetails is buildDetails for a record that exists: the effective invocation outranks the requested snapshot.
func (h *uiHub) recordDetails(r *agentRecord, status string, durationMs int64) agentDetails {
	usage, _ := h.usageOf(r.ID)
	d := newDetailBase(r.Type, r.Description, h.invocationOf(r.ID)).details()
	d.ToolUses = r.ToolUses
	if t := getLifetimeTotal(usage); t > 0 {
		d.Tokens = formatTokens(t)
	}
	cost := getLifetimeCost(usage)
	d.Cost = &cost
	if act := h.activityOf(r.ID); act != nil {
		tc := act.TurnCount
		d.TurnCount = &tc
		d.MaxTurns = act.MaxTurns
	}
	d.DurationMs = durationMs
	d.Status = status
	d.AgentID = r.ID
	d.Error = r.Error
	return d
}

// streamForeground streams the running agent to the tool card (a partial result with the spinner frame and the
// activity) every 80 ms until the returned stop is called. upstream: index.ts streamUpdate and the spinner interval.
func (a *app) streamForeground(ctx sdk.Context, r *agentRecord, typ, description string) (stop func()) {
	frame := 0
	var mu sync.Mutex
	update := func() {
		mu.Lock()
		defer mu.Unlock()
		d := a.runningDetails(r, typ, description, frame)
		_ = ctx.OnUpdate(map[string]any{
			"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%d tool uses...", d.ToolUses)}},
			"details": d,
		})
	}
	update()
	stopTicker := startTicker(80*time.Millisecond, func() {
		mu.Lock()
		frame++
		mu.Unlock()
		update()
	})
	return stopTicker
}

// runningDetails is the details of a partial result of a foreground agent. Deliberately still "running" while queued:
// the renderer routes any status it does not know to raw text, which would drop the spinner and read as hung.
// upstream: index.ts streamUpdate.
func (a *app) runningDetails(r *agentRecord, typ, description string, frame int) agentDetails {
	act := a.ui.activityOf(r.ID)
	if act == nil {
		act = &agentActivity{TurnCount: 1}
	}
	usage, _ := a.ui.usageOf(r.ID)
	d := newDetailBase(typ, description, a.ui.invocationOf(r.ID)).details()
	d.ToolUses = act.ToolUses
	if t := getLifetimeTotal(usage); t > 0 {
		d.Tokens = formatTokens(t)
	}
	cost := getLifetimeCost(usage)
	d.Cost = &cost
	tc := act.TurnCount
	d.TurnCount, d.MaxTurns = &tc, act.MaxTurns
	a.mgr.mu.Lock()
	d.DurationMs = nowMs() - r.StartedAt.UnixMilli()
	a.mgr.mu.Unlock()
	d.Status = "running"
	d.Activity = describeActivity(act.ActiveTools, act.ResponseText)
	f := frame % len(spinner)
	d.SpinnerFrame = &f
	return d
}
