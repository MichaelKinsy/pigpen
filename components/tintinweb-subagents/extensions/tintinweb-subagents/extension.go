package tintinweb_subagents

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	protocolVersion = 2
	nudgeHold       = 200 * time.Millisecond // a completion notification is held this long for a consume. upstream: index.ts NUDGE_HOLD_MS
)

// app is the extension. upstream: src/index.ts.
type app struct {
	e   *sdk.Extension
	mgr *manager
	mu  sync.Mutex

	cwd             string
	defaultMaxTurns *int
	graceTurns      int
	latest          *sdk.Context
	timers          map[string]*time.Timer
	settingsNotice  string
	obs             obsState
}

func newApp(e *sdk.Extension, start childStarter) *app {
	registerAgents(newRegistry())
	a := &app{e: e, timers: map[string]*time.Timer{}}
	a.mgr = newManager(start, a.onSettle)
	a.wireObs()
	return a
}

// reload re-reads the custom agent files for a workspace.
func (a *app) reload(cwd string) {
	st := loadSettings(cwd)
	if st.MaxConcurrent > 0 {
		a.mgr.mu.Lock()
		a.mgr.maxConcurrent = st.MaxConcurrent
		a.mgr.mu.Unlock()
	}
	a.mu.Lock()
	a.defaultMaxTurns = st.DefaultMaxTurns
	a.graceTurns = st.GraceTurns
	a.mu.Unlock()
	a.reloadObs(cwd)
	setFallbackSubagent(st.FallbackSubagent)
	setDefaultsDisabled(st.DisableDefaultAgents != nil && *st.DisableDefaultAgents)
	custom, warnings := loadCustomAgents(cwd)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "[pi-subagents] %s\n", w)
	}
	a.mu.Lock()
	a.cwd = cwd
	a.mu.Unlock()
	registerAgents(custom)
}

// refuseSettings tells the user once per distinct text that a setting of subagents.json asks for a feature that is
// not ported. The setting has no effect for the unported part (the port does what it did without it), which the
// notice names when the setting is honored in part (showCost: the texts show the cost, the widget does not); it is
// never taken for a full run.
func (a *app) refuseSettings(ctx sdk.Context) {
	text := settingsRefusal(ctx.Cwd())
	a.mu.Lock()
	same := text == a.settingsNotice
	a.settingsNotice = text
	a.mu.Unlock()
	if text != "" && !same {
		ctx.Notify(text, "warning")
	}
}

func (a *app) touch(ctx sdk.Context) {
	c := ctx
	a.mu.Lock()
	a.latest = &c
	a.mu.Unlock()
	if ctx.Cwd() != a.cwd {
		a.reload(ctx.Cwd())
	}
}

func (a *app) context() (sdk.Context, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.latest == nil {
		return sdk.Context{}, false
	}
	return *a.latest, true
}

// Extension returns the subagents extension. A child (TINTINWEB_SUBAGENT_DEPTH set) registers nothing: there is no
// nesting in this port.
func Extension() *sdk.Extension {
	e := sdk.New("tintinweb-subagents")
	if os.Getenv(depthEnv) != "" {
		// A schema-bound child reports through a StructuredOutput tool and registers nothing else.
		if path := os.Getenv(structuredSchemaFileEnv); path != "" {
			if schema, err := os.ReadFile(path); err == nil {
				registerStructuredOutput(e, string(schema))
			}
		}
		return e
	}
	a := newApp(e, startRPCChild)
	a.register(e)
	return e
}

func (a *app) register(e *sdk.Extension) {
	a.registerRPC(e.Events())
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.touch(ctx)
		a.reload(ctx.Cwd())
		a.refuseSettings(ctx)
		// Announce this extension, as the original does when it initializes. upstream: index.ts:827.
		_ = ctx.Events().Emit("subagents:ready", map[string]any{})
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	e.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.mgr.abortAll()
		return nil, nil
	})
	a.registerTools(e)
	e.Command("agents", "List the agent types and the running agents", a.agentsCommand)
}

// onSettle announces a finished agent on the bus and, for a background one, to the model. upstream: index.ts:560-600.
func (a *app) onSettle(r *agentRecord) {
	ctx, ok := a.context()
	if !ok {
		return
	}
	data := map[string]any{"id": r.ID, "type": r.Type, "description": r.Description, "status": r.Status,
		"toolUses": r.ToolUses, "durationMs": r.CompletedAt.Sub(r.StartedAt).Milliseconds()}
	// Lifetime spend, which survives compaction: `tokens` is the flat display view (cacheRead excluded), `usage` a
	// complete pi Usage; each is omitted when nothing was spent. Ungated by showCost. upstream: index.ts buildEventData.
	if total := getLifetimeTotal(r.Usage); total > 0 {
		data["tokens"] = map[string]any{"input": r.Usage.Input, "output": r.Usage.Output, "total": total}
	}
	if usage := toReportedUsage(r.Usage); usage != nil {
		data["usage"] = usage
	}
	if r.Result != "" {
		data["result"] = r.Result
	}
	channel := "subagents:completed"
	if isErrorStatus(r.Status) {
		channel = "subagents:failed"
		if r.Error != "" {
			data["error"] = r.Error
		}
	}
	_ = ctx.Events().Emit(channel, data)
	if !r.Background {
		return
	}
	a.scheduleNudge(r.ID, func() {
		a.mgr.mu.Lock()
		consumed := r.Consumed
		a.mgr.mu.Unlock()
		if consumed {
			return
		}
		turn := true
		_ = ctx.SendMessage("subagent-notification", formatTaskNotification(r, 500, a.showCost())+outputFooter(r), true, sdk.SendMessageOptions{DeliverAs: "followUp", TriggerTurn: &turn})
	})
}

func (a *app) scheduleNudge(key string, send func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.timers[key]; t != nil {
		t.Stop()
	}
	a.timers[key] = time.AfterFunc(nudgeHold, func() {
		a.mu.Lock()
		delete(a.timers, key)
		a.mu.Unlock()
		send()
	})
}

// escapeXML escapes &, < and >, as the original does. upstream: src/xml.ts.
func escapeXML(s string) string { return xmlEscaper.Replace(s) }

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// isErrorStatus: the statuses announced on subagents:failed. upstream: index.ts:576.
func isErrorStatus(status string) bool {
	return status == statusError || status == statusStopped || status == statusAborted
}

// statusLabel is the human-readable label of a finished agent. upstream: index.ts getStatusLabel.
func statusLabel(r *agentRecord) string {
	switch r.Status {
	case statusError:
		e := r.Error
		if e == "" {
			e = "unknown"
		}
		return "Error: " + e
	case statusAborted:
		return "Aborted (max turns exceeded)"
	case statusSteered:
		return "Wrapped up (turn limit)"
	case statusStopped:
		return "Stopped"
	}
	return "Done"
}

// statusNote is the parenthetical note of a non-normal outcome. upstream: src/status-note.ts getStatusNote.
func statusNote(status string) string {
	switch status {
	case statusStopped:
		return " (STOPPED BY THE USER before completion — output is partial; the task was NOT finished)"
	case statusAborted:
		return " (aborted — hit the turn limit before completion; output may be incomplete)"
	case statusSteered:
		return " (wrapped up at the turn limit — output may be partial)"
	}
	return ""
}

// foregroundOutcomeNote is statusNote for a foreground caller, which already holds the whole output.
// upstream: src/status-note.ts getForegroundOutcomeNote.
func foregroundOutcomeNote(status string) string {
	switch status {
	case statusStopped:
		return " (STOPPED BY THE USER — everything the agent produced is above; the task is unfinished)"
	case statusAborted:
		return " (aborted at the turn limit — everything the agent produced is above; the task is unfinished)"
	case statusSteered:
		return " (wrapped up at the turn limit — everything the agent produced is above; the task may be unfinished)"
	}
	return ""
}

// partialOutputSuffix is the output a failed run produced, as a labelled suffix ("" when there is none).
// upstream: src/status-note.ts partialOutputSuffix.
func partialOutputSuffix(result string) string {
	if p := strings.TrimSpace(result); p != "" {
		return "\n\nPartial output before the failure:\n" + p
	}
	return ""
}

// outputFooter points the model at the transcript. upstream: index.ts emitIndividualNudge.
func outputFooter(r *agentRecord) string {
	if r.OutputFile == "" {
		return ""
	}
	return "\nFull transcript available at: " + r.OutputFile
}

// formatTaskNotification is the completion notice the model receives. upstream: index.ts formatTaskNotification.
func formatTaskNotification(r *agentRecord, resultMaxLen int, showCost bool) string {
	preview := "No output."
	if r.Result != "" {
		preview = r.Result
		if u := []rune(preview); len(u) > resultMaxLen {
			preview = string(u[:resultMaxLen]) + "\n...(truncated, use get_subagent_result for full output)"
		}
	}
	dur := r.CompletedAt.Sub(r.StartedAt).Milliseconds()
	lines := []string{"<task-notification>", "<task-id>" + r.ID + "</task-id>"}
	if r.ToolCallID != "" {
		lines = append(lines, "<tool-use-id>"+escapeXML(r.ToolCallID)+"</tool-use-id>")
	}
	if r.OutputFile != "" {
		lines = append(lines, "<output-file>"+escapeXML(r.OutputFile)+"</output-file>")
	}
	ctxXML, compactXML, costXML := "", "", ""
	if r.ContextPercent != nil {
		ctxXML = fmt.Sprintf("<context_percent>%d</context_percent>", roundPercent(*r.ContextPercent))
	}
	if r.CompactionCount > 0 {
		compactXML = fmt.Sprintf("<compactions>%d</compactions>", r.CompactionCount)
	}
	// Only under showCost: this is LLM context, and a figure the orchestrator did not ask for is a figure it may
	// start reporting unprompted.
	if cost := getLifetimeCost(r.Usage); showCost && cost > 0 {
		costXML = "<estimated_cost_usd>" + jsToFixed(cost, 4) + "</estimated_cost_usd>"
	}
	return strings.Join(append(lines,
		"<status>"+escapeXML(statusLabel(r))+"</status>",
		fmt.Sprintf("<summary>Agent \"%s\" %s%s</summary>", escapeXML(r.Description), r.Status, statusNote(r.Status)),
		"<result>"+escapeXML(preview)+"</result>",
		fmt.Sprintf("<usage><total_tokens>%d</total_tokens><tool_uses>%d</tool_uses>%s%s%s<duration_ms>%d</duration_ms></usage>", getLifetimeTotal(r.Usage), r.ToolUses, ctxXML, compactXML, costXML, dur),
		"</task-notification>",
	), "\n")
}

var _ = context.Background
