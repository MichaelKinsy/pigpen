package tintinweb_subagents

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
)

// The renderers of the subagent surfaces in the transcript: the Agent tool call and result, and the completion
// notification. upstream: src/index.ts (renderCall, renderResult, registerMessageRenderer "subagent-notification").

// agentDetails is the metadata attached to Agent tool results for custom rendering. The key order is the original's.
// upstream: ui/agent-widget.ts AgentDetails.
type agentDetails struct {
	DisplayName  string   `json:"displayName"`
	Description  string   `json:"description"`
	SubagentType string   `json:"subagentType"`
	ModelName    string   `json:"modelName,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	ToolUses     int      `json:"toolUses"`
	Tokens       string   `json:"tokens"`
	Cost         *float64 `json:"cost,omitempty"`
	TurnCount    *int     `json:"turnCount,omitempty"`
	MaxTurns     *int     `json:"maxTurns,omitempty"`
	DurationMs   int64    `json:"durationMs"`
	// Status is queued, running, completed, steered, aborted, stopped, error or background.
	Status       string `json:"status"`
	Activity     string `json:"activity,omitempty"`
	SpinnerFrame *int   `json:"spinnerFrame,omitempty"`
	AgentID      string `json:"agentId,omitempty"`
	Error        string `json:"error,omitempty"`
}

// notificationDetails is the details of a "subagent-notification" message. upstream: types.ts NotificationDetails.
type notificationDetails struct {
	ID            string                `json:"id"`
	Description   string                `json:"description"`
	Status        string                `json:"status"`
	ToolUses      int                   `json:"toolUses"`
	TurnCount     int                   `json:"turnCount"`
	MaxTurns      *int                  `json:"maxTurns,omitempty"`
	TotalTokens   int                   `json:"totalTokens"`
	TotalCost     float64               `json:"totalCost"`
	DurationMs    int64                 `json:"durationMs"`
	OutputFile    string                `json:"outputFile,omitempty"`
	Error         string                `json:"error,omitempty"`
	ResultPreview string                `json:"resultPreview"`
	Others        []notificationDetails `json:"others,omitempty"`
}

// renderText is a pi-tui Text(text, 0, 0) drawn at width.
func renderText(text string, width int) []string { return tui.NewText(text, 0, 0, nil).Render(width) }

// renderRunningAgentStatus is the running status as separate lines: the spinner frame with the stats, then the activity.
// upstream: index.ts renderRunningAgentStatus.
func renderRunningAgentStatus(frame, statsText, activity string, th uiTheme, width int) []string {
	first := th.Fg("accent", frame)
	if statsText != "" {
		first += " " + statsText
	}
	return append(renderText(first, width), renderText(th.Fg("dim", "  ⎿  "+activity), width)...)
}

// renderAgentCall is the Agent tool call header. The row tint is opened by the line itself and restored after the
// badge, so the line reads the same whether or not the caller paints one; the line is left open on purpose (the host
// pads to width and then wraps, so a reset here would leave the padding untinted).
func renderAgentCall(args map[string]any, isPartial, isError bool, th uiTheme, width int) []string {
	subagentType := msgStr(args["subagent_type"])
	rowBackground := ""
	if hasAgentBadge(subagentType) {
		token := "toolSuccessBg"
		if isPartial {
			token = "toolPendingBg"
		} else if isError {
			token = "toolErrorBg"
		}
		rowBackground = th.GetBgAnsi(token)
	}
	desc := msgStr(args["description"])
	name := renderAgentName(subagentType, th, agentNameStyle{FallbackColor: "toolTitle", RestoreBackground: rowBackground, Bold: true})
	line := rowBackground + "▸ " + name
	if desc != "" {
		line += "  " + th.Fg("muted", desc)
	}
	return renderText(line, width)
}

// agentStats is the "haiku · thinking: high · ↻5≤30 · 3 tool uses · 33.8k token" line of a result.
func agentStats(d agentDetails, th uiTheme, showCost bool) string {
	var parts []string
	if d.ModelName != "" {
		parts = append(parts, d.ModelName)
	}
	parts = append(parts, d.Tags...)
	if d.TurnCount != nil && *d.TurnCount > 0 {
		parts = append(parts, formatTurns(*d.TurnCount, d.MaxTurns))
	}
	if d.ToolUses > 0 {
		parts = append(parts, fmt.Sprintf("%d tool %s", d.ToolUses, plural(d.ToolUses, "use", "uses")))
	}
	if d.Tokens != "" {
		parts = append(parts, d.Tokens)
	}
	if showCost && d.Cost != nil {
		if c := formatCost(*d.Cost); c != "" {
			parts = append(parts, c)
		}
	}
	styled := make([]string, len(parts))
	for i, p := range parts {
		styled[i] = fgPreservingNestedStyles(th, "dim", p)
	}
	return strings.Join(styled, " "+th.Fg("dim", "·")+" ")
}

// renderAgentResult is the Agent tool result. text is the first text block of the result, details its details (nil
// when there are none). A Pi tool error, a result with no details or one with no known status shows the real result
// text rather than an invented status (#199).
func renderAgentResult(text string, details *agentDetails, expanded, isPartial, isError bool, th uiTheme, showCost bool, width int) []string {
	if isError || details == nil || details.Status == "" {
		return renderText(text, width)
	}
	d := *details
	stats := agentStats(d, th, showCost)

	// While running (streaming).
	if isPartial || d.Status == "running" {
		frame := 0
		if d.SpinnerFrame != nil {
			frame = *d.SpinnerFrame
		}
		activity := d.Activity
		if activity == "" {
			activity = "thinking…"
		}
		return renderRunningAgentStatus(spinner[((frame%len(spinner))+len(spinner))%len(spinner)], stats, activity, th, width)
	}
	// Background agent launched.
	if d.Status == "background" {
		return renderText(th.Fg("dim", fmt.Sprintf("  ⎿  Running in background (ID: %s)", d.AgentID)), width)
	}
	// Completed / steered.
	if d.Status == "completed" || d.Status == "steered" {
		duration := formatMs(d.DurationMs)
		icon := th.Fg("success", "✓")
		if d.Status == "steered" {
			icon = th.Fg("warning", "✓")
		}
		line := icon
		if stats != "" {
			line += " " + stats
		}
		line += " " + th.Fg("dim", "·") + " " + th.Fg("dim", duration)
		if expanded {
			if text != "" {
				all := strings.Split(text, "\n")
				for _, l := range all[:min(50, len(all))] {
					line += "\n" + th.Fg("dim", "  "+l)
				}
				if len(all) > 50 {
					line += "\n" + th.Fg("muted", "  ... (use get_subagent_result with verbose for full output)")
				}
			}
		} else {
			done := "Done"
			if d.Status == "steered" {
				done = "Wrapped up (turn limit)"
			}
			line += "\n" + th.Fg("dim", "  ⎿  "+done)
		}
		return renderText(line, width)
	}
	// Stopped (user-initiated abort).
	if d.Status == "stopped" {
		line := th.Fg("dim", "■")
		if stats != "" {
			line += " " + stats
		}
		line += "\n" + th.Fg("dim", "  ⎿  Stopped")
		return renderText(line, width)
	}
	// Anything left ("queued", or a status added later) has no rendering of its own.
	if d.Status != "error" && d.Status != "aborted" {
		return renderText(text, width)
	}
	// Error / aborted (hard max_turns).
	line := th.Fg("error", "✗")
	if stats != "" {
		line += " " + stats
	}
	if d.Status == "error" {
		e := d.Error
		if e == "" {
			e = "unknown"
		}
		line += "\n" + th.Fg("error", "  ⎿  Error: "+e)
	} else {
		line += "\n" + th.Fg("warning", "  ⎿  Aborted (max turns exceeded)")
	}
	return renderText(line, width)
}

// renderNotification is the completion notification: per agent an icon, the description and the status, a stats line,
// the result preview (the first line, or up to 30 when expanded) and the transcript path. A group adds a total line
// when costs are shown.
func renderNotification(d notificationDetails, expanded bool, th uiTheme, showCost bool, width int) []string {
	renderOne := func(d notificationDetails) string {
		isError := d.Status == "error" || d.Status == "stopped" || d.Status == "aborted"
		icon := th.Fg("success", "✓")
		if isError {
			icon = th.Fg("error", "✗")
		}
		statusText := "completed"
		if isError {
			statusText = d.Status
		} else if d.Status == "steered" {
			statusText = "completed (steered)"
		}
		line := icon + " " + th.Bold(d.Description) + " " + th.Fg("dim", statusText)

		var parts []string
		if d.TurnCount > 0 {
			parts = append(parts, formatTurns(d.TurnCount, d.MaxTurns))
		}
		if d.ToolUses > 0 {
			parts = append(parts, fmt.Sprintf("%d tool %s", d.ToolUses, plural(d.ToolUses, "use", "uses")))
		}
		if d.TotalTokens > 0 {
			parts = append(parts, widgetTokens(d.TotalTokens))
		}
		if showCost {
			if c := formatCost(d.TotalCost); c != "" {
				parts = append(parts, c)
			}
		}
		if d.DurationMs > 0 {
			parts = append(parts, formatMs(d.DurationMs))
		}
		if len(parts) > 0 {
			styled := make([]string, len(parts))
			for i, p := range parts {
				styled[i] = th.Fg("dim", p)
			}
			line += "\n  " + strings.Join(styled, " "+th.Fg("dim", "·")+" ")
		}
		if expanded {
			all := strings.Split(d.ResultPreview, "\n")
			for _, l := range all[:min(30, len(all))] {
				line += "\n" + th.Fg("dim", "  "+l)
			}
		} else {
			line += "\n  " + th.Fg("dim", "⎿  "+jsstring.Slice(strings.Split(d.ResultPreview, "\n")[0], 0, 80))
		}
		if d.OutputFile != "" {
			line += "\n  " + th.Fg("muted", "transcript: "+d.OutputFile)
		}
		return line
	}
	all := append([]notificationDetails{d}, d.Others...)
	rendered := make([]string, 0, len(all)+1)
	for _, a := range all {
		rendered = append(rendered, renderOne(a))
	}
	// A group of agents lands as one notification; the number a user wants from it is what the batch cost.
	if showCost && len(all) > 1 {
		cost, tokens := 0.0, 0
		for _, a := range all {
			cost += a.TotalCost
			tokens += a.TotalTokens
		}
		if total := formatCost(cost); total != "" {
			rendered = append([]string{th.Fg("dim", fmt.Sprintf("%d agents · %s · %s", len(all), widgetTokens(tokens), total))}, rendered...)
		}
	}
	return renderText(strings.Join(rendered, "\n"), width)
}

// ---- registration with the SDK ----

func decodeDetails[T any](v any) *T {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out T
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}

func toolResultText(r sdk.ToolRenderResult) string {
	if len(r.Content) > 0 && r.Content[0]["type"] == "text" {
		return msgStr(r.Content[0]["text"])
	}
	return ""
}

// registerRenderers registers the notification renderer; the Agent tool's renderers are part of its definition.
func (a *app) registerRenderers(e *sdk.Extension) {
	// A workflow launched from a flag has no tool call to hang its result card on, so it renders as a session entry
	// through the same layout. Registered at activation: entries with no renderer are silently dropped by the host.
	e.EntryRenderer(workflowEntryType, a.renderWorkflowEntry)
	e.MessageRenderer("subagent-notification", func(ctx sdk.Context, message map[string]any, opts sdk.MessageRenderOptions, width int) ([]string, error) {
		return protectLines("notification renderer", func() ([]string, error) {
			d := decodeDetails[notificationDetails](message["details"])
			if d == nil {
				return nil, nil
			}
			return renderNotification(*d, opts.Expanded, sdkTheme{ctx.UITheme()}, a.ui.getShowCost(), width), nil
		})
	})
}

func (a *app) renderAgentCall(ctx sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
	return protectLines("Agent call renderer", func() ([]string, error) {
		return renderAgentCall(args, render.IsPartial, render.IsError, sdkTheme{ctx.UITheme()}, width), nil
	})
}

func (a *app) renderAgentResult(ctx sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
	return protectLines("Agent result renderer", func() ([]string, error) {
		return renderAgentResult(toolResultText(result), decodeDetails[agentDetails](result.Details), opts.Expanded, opts.IsPartial, render.IsError, sdkTheme{ctx.UITheme()}, a.ui.getShowCost(), width), nil
	})
}
