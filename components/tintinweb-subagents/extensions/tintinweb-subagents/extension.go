package tintinweb_subagents

import (
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

	cwd         string // the workspace the custom agents were last read for
	settingsCwd string // the workspace the settings were last applied for
	rt          runtimeSettings
	latest      *sdk.Context
	// timers hold a completion notification for a moment, so a read of the result can cancel it.
	timers         map[string]func()
	settingsNotice string
	loaded         *settings // what `subagents:settings_loaded` still has to announce
	after          afterFunc
	groups         *groupJoinManager
	batch          []batchEntry // the background agents of the turn in progress, for smart and group joins
	batchStop      func()
	batchCounter   int
	rpcOff         []func() // the RPC listeners, registered once the session has started
	entry          *registryEntry
}

// agentFileWarn reports an agent file that was skipped (the original's console.warn). A test replaces it.
var agentFileWarn = func(w string) { fmt.Fprintf(os.Stderr, "[pi-subagents] %s\n", w) }

func newApp(e *sdk.Extension, start childStarter) *app {
	registerAgents(newRegistry())
	a := &app{e: e, timers: map[string]func(){}, rt: defaultRuntimeSettings(), after: realAfter}
	a.mgr = newManager(start, a.onSettle)
	a.groups = newGroupJoinManager(a.deliverGroup, groupJoinTimeout, func(d time.Duration, f func()) func() { return a.after(d, f) })
	a.entry = claimManagerRegistry(a)
	return a
}

// startup reads the settings and the custom agents of the workspace once, before the tools are registered: the
// description and the schema are fixed once pi has them. An unreadable agent file aborts the load when
// strictAgentFiles is set, here and nowhere else: a bad edit mid-session must not kill the session on the next
// unrelated spawn. upstream: index.ts (strictAgentFiles read directly, reloadCustomAgents(strict), applyAndEmitLoaded).
func (a *app) startup(wd string) {
	st := loadSettings(wd)
	setSettingsView(st)
	custom, warnings, err := loadCustomAgentsStrict(wd, st.strictAgentFiles())
	if err != nil {
		panic(fmt.Errorf("[pi-subagents] cannot load the agent file %w", err))
	}
	for _, w := range warnings {
		agentFileWarn(w)
	}
	a.mu.Lock()
	a.cwd, a.settingsCwd, a.loaded = wd, wd, &st
	a.mu.Unlock()
	registerAgents(custom)
	applySettings(st, a.appliers())
}

// reload re-reads the custom agent files for a workspace (a bad file only warns), and the settings when the
// workspace is not the one they were applied for.
func (a *app) reload(cwd string) {
	a.mu.Lock()
	changed := a.settingsCwd != cwd
	a.mu.Unlock()
	if changed {
		st := loadSettings(cwd)
		setSettingsView(st)
		applySettings(st, a.appliers())
		a.mu.Lock()
		a.settingsCwd, a.loaded = cwd, &st
		a.mu.Unlock()
	}
	a.mu.Lock()
	a.cwd = cwd
	a.mu.Unlock()
	a.reregisterAgents()
}

// reregisterAgents reads the custom agent files again and registers them with the defaults.
func (a *app) reregisterAgents() {
	a.mu.Lock()
	cwd := a.cwd
	a.mu.Unlock()
	custom, warnings := loadCustomAgents(cwd)
	for _, w := range warnings {
		agentFileWarn(w)
	}
	registerAgents(custom)
}

// refuseSettings tells the user once per distinct text that a setting of subagents.json asks for a feature that is
// not ported. The setting has no effect (the port does what it did without it); it is never taken for a partial run.
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
	cwd := a.cwd
	a.mu.Unlock()
	if ctx.Cwd() != cwd {
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
		return e
	}
	a := newApp(e, startRPCChild)
	a.register(e)
	return e
}

func (a *app) register(e *sdk.Extension) {
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.touch(ctx)
		a.reload(ctx.Cwd())
		// A new session starts clean: what the model has not read yet is kept (the GC evicts it later).
		a.mgr.clearCompleted(true)
		a.refuseSettings(ctx)
		a.announce(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionBeforeSwitch, func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.mgr.clearCompleted(true)
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	e.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		a.shutdown()
		return nil, nil
	})
	a.registerTools(e)
	e.Command("agents", "List the agent types and the running agents", a.agentsCommand)
}

// announce emits `subagents:settings_loaded` and, once, registers the RPC listeners and announces the extension:
// both wait for the first session_start (a bound lifecycle event), so a session that filtered this extension out
// behaves like one without it. upstream: index.ts session_start (#142), applyAndEmitLoaded.
func (a *app) announce(ctx sdk.Context) {
	a.mu.Lock()
	loaded := a.loaded
	a.loaded = nil
	first := a.rpcOff == nil
	a.mu.Unlock()
	if loaded != nil {
		_ = ctx.Events().Emit("subagents:settings_loaded", map[string]any{"settings": loaded.payload()})
	}
	if !first {
		return
	}
	off := a.registerRPC(ctx.Events())
	a.mu.Lock()
	a.rpcOff = off
	a.mu.Unlock()
	// Announce this extension, as the original does once its handlers are in place. upstream: index.ts:827.
	_ = ctx.Events().Emit("subagents:ready", map[string]any{})
}

// shutdown aborts every agent: with the session going down nobody is left to read a result. upstream: index.ts
// session_shutdown.
func (a *app) shutdown() {
	a.mu.Lock()
	off := a.rpcOff
	a.rpcOff = nil
	a.mu.Unlock()
	for _, f := range off {
		f()
	}
	releaseManagerRegistry(a.entry)
	a.mgr.abortAll()
	a.cancelBatch()
	a.mu.Lock()
	for key, stop := range a.timers {
		stop()
		delete(a.timers, key)
	}
	a.mu.Unlock()
	a.groups.dispose()
	a.mgr.dispose()
}

// onSettle announces a finished agent on the bus and, for a background one, to the model. upstream: index.ts:560-600.
func (a *app) onSettle(r *agentRecord) {
	if !r.topLevel() { // an owned child reports through its owner
		return
	}
	ctx, ok := a.context()
	if !ok {
		return
	}
	a.mgr.mu.Lock()
	data := map[string]any{"id": r.ID, "type": r.Type, "description": r.Description, "status": r.Status,
		"toolUses": r.ToolUses, "durationMs": r.CompletedAt.Sub(r.StartedAt).Milliseconds()}
	if r.Result != "" {
		data["result"] = r.Result
	}
	status, errText, consumed := r.Status, r.Error, r.Consumed
	a.mgr.mu.Unlock()
	channel := "subagents:completed"
	if isErrorStatus(status) {
		channel = "subagents:failed"
		if errText != "" {
			data["error"] = errText
		}
	}
	_ = ctx.Events().Emit(channel, data)
	if consumed { // the result was already delivered
		return
	}
	// An agent of the batch still being collected is picked up by finalizeBatch.
	if a.inBatch(r.ID) {
		return
	}
	if a.groups.onAgentComplete(r) == "pass" {
		a.sendIndividualNudge(r)
	}
}

func (a *app) scheduleNudge(key string, send func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if stop := a.timers[key]; stop != nil {
		stop()
	}
	a.timers[key] = a.after(nudgeHold, func() {
		a.mu.Lock()
		delete(a.timers, key)
		a.mu.Unlock()
		send()
	})
}

// cancelNudge cancels a held notification, as a read of the result does.
func (a *app) cancelNudge(key string) {
	a.mu.Lock()
	if stop := a.timers[key]; stop != nil {
		stop()
		delete(a.timers, key)
	}
	a.mu.Unlock()
}

// escapeXML escapes &, < and >, as the original does. upstream: src/xml.ts.
func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

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

// formatTaskNotification is the completion notice the model receives. upstream: index.ts formatTaskNotification.
func formatTaskNotification(r *agentRecord, resultMaxLen int) string {
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
	return strings.Join(append(lines,
		"<status>"+escapeXML(statusLabel(r))+"</status>",
		fmt.Sprintf("<summary>Agent \"%s\" %s%s</summary>", escapeXML(r.Description), r.Status, statusNote(r.Status)),
		"<result>"+escapeXML(preview)+"</result>",
		fmt.Sprintf("<usage><total_tokens>%d</total_tokens><tool_uses>%d</tool_uses><duration_ms>%d</duration_ms></usage>", r.Tokens, r.ToolUses, dur),
		"</task-notification>",
	), "\n")
}
