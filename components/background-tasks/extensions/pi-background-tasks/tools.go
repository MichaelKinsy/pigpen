package pi_background_tasks

import (
	"fmt"
	"math"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The bg_status and bg_task tools, the /bg commands and the forced-background window. upstream: extensions/registrations.ts.

// toolResult is makeToolResult(text, details).
func toolResult(text string, details any) sdk.ToolResult {
	return sdk.ToolResult{Content: text, Details: details}
}

type listDetails struct {
	Action string `json:"action"`
	Tasks  any    `json:"tasks"`
}

type taskDetails struct {
	Action string          `json:"action"`
	Task   compactSnapshot `json:"task"`
}

type logDetails struct {
	Action         string          `json:"action"`
	Task           compactSnapshot `json:"task"`
	FullOutputPath string          `json:"fullOutputPath,omitempty"`
	Truncation     *logTruncation  `json:"truncation,omitempty"`
}

type clearDetails struct {
	Action  string `json:"action"`
	Removed int    `json:"removed"`
}

// formatTaskListText is the list the tools and `/bg list` answer.
func (rt *bgRuntime) formatTaskListText() string {
	sorted := rt.sortedTasks()
	if len(sorted) == 0 {
		return "No background tasks."
	}
	now := clockNow()
	lines := make([]string, len(sorted))
	for i, t := range sorted {
		lines[i] = buildTaskSummaryLine(taskSnapshot(t), now)
	}
	return strings.Join(lines, "\n\n")
}

func (rt *bgRuntime) listResult() sdk.ToolResult {
	snaps := make([]snapshot, 0)
	for _, t := range rt.sortedTasks() {
		snaps = append(snaps, *rememberSnapshot(t))
	}
	return toolResult(rt.formatTaskListText(), listDetails{Action: "list", Tasks: bgToolResultTasks(snaps, toolResultOptions{})})
}

// readTaskOutput is getTaskOutput: the task's output, from memory or its log. It must be called without the lock.
func (rt *bgRuntime) readTaskOutput(t *task, maxChars float64) string {
	return rt.reader.read(t, maxChars)
}

func (rt *bgRuntime) outputBufferMax() float64 {
	return settingNumber("outputBufferMaxChars", defaultOutputBufferMaxChars, rt.cwd())
}

// logResult is the log action of both tools.
func (rt *bgRuntime) logResult(t *task) sdk.ToolResult {
	rt.mu.Lock()
	limit := rt.outputBufferMax()
	rt.mu.Unlock()
	output := rt.readTaskOutput(t, limit)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cwd := rt.cwd()
	d := logDetails{Action: "log", Task: compactBackgroundTaskSnapshot(rememberSnapshot(t))}
	if trunc := taskLogTruncation(output, t.LogFile, cwd); trunc != nil {
		d.FullOutputPath, d.Truncation = truncateForTranscript(t.LogFile, wakeManifestFieldMaxChars), trunc
	}
	return toolResult(formatTaskLog(output, t.LogFile, cwd), d)
}

func (rt *bgRuntime) stopResult(t *task) (sdk.ToolResult, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ok, message := rt.requestStop(t, stopUser)
	if !ok {
		return sdk.ToolResult{}, fmt.Errorf("%s", message)
	}
	return toolResult(message, taskDetails{Action: "stop", Task: compactBackgroundTaskSnapshot(rememberSnapshot(t))}), nil
}

// withLock runs fn with the lock held and waits for the host calls it queued, so the host sees them before the tool result.
func (rt *bgRuntime) withLock(fn func()) {
	rt.mu.Lock()
	fn()
	rt.mu.Unlock()
	rt.settle()
}

func (rt *bgRuntime) tokenOf(params map[string]any) string {
	if id, ok := params["id"].(string); ok {
		return id
	}
	if pid, ok := params["pid"].(float64); ok {
		return jsNumberString(pid)
	}
	return ""
}

// bgStatusTool is the bg_status tool.
func (rt *bgRuntime) bgStatusTool() sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		action, _ := params["action"].(string)
		if action == "list" {
			var res sdk.ToolResult
			rt.withLock(func() { res = rt.listResult() })
			return res, nil
		}
		var t *task
		rt.mu.Lock()
		if pid, ok := params["pid"].(float64); ok {
			t = rt.resolveTask(jsNumberString(pid))
		}
		rt.mu.Unlock()
		if t == nil {
			return nil, fmt.Errorf("No background task matched that pid.")
		}
		if action == "log" {
			return rt.logResult(t), nil
		}
		res, err := rt.stopResult(t)
		rt.settle()
		return res, err
	}
}

func optBool(params map[string]any, key string) *bool {
	if v, ok := params[key].(bool); ok {
		return &v
	}
	return nil
}

func optString(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// bgTaskTool is the bg_task tool.
func (rt *bgRuntime) bgTaskTool() sdk.ToolFunc {
	return func(ctx sdk.Context, params map[string]any) (any, error) {
		action, _ := params["action"].(string)
		switch action {
		case "list":
			var res sdk.ToolResult
			rt.withLock(func() { res = rt.listResult() })
			return res, nil
		case "clear":
			var removed int
			rt.withLock(func() { removed = rt.clearFinishedTasks() })
			return toolResult(fmt.Sprintf("Removed %d finished background task(s).", removed), clearDetails{Action: "clear", Removed: removed}), nil
		case "spawn":
			return rt.spawnResult(params)
		}
		var t *task
		rt.mu.Lock()
		t = rt.resolveTask(rt.tokenOf(params))
		rt.mu.Unlock()
		if t == nil {
			return nil, fmt.Errorf("No background task matched that id or pid.")
		}
		if action == "log" {
			return rt.logResult(t), nil
		}
		res, err := rt.stopResult(t)
		rt.settle()
		return res, err
	}
}

func (rt *bgRuntime) spawnResult(params map[string]any) (any, error) {
	o := spawnOptions{
		command: optString(params, "command"), cwd: optString(params, "cwd"), notifyOnExit: optBool(params, "notifyOnExit"), notifyOnOutput: optBool(params, "notifyOnOutput"),
		notifyPattern: optString(params, "notifyPattern"), notifyMode: optString(params, "notifyMode"), dedupeKey: optString(params, "dedupeKey"), title: optString(params, "title"),
	}
	if v, ok := params["timeoutSeconds"].(float64); ok {
		o.timeoutSeconds = &v
	}
	// A pattern the port cannot translate with certainty is refused, never matched differently.
	if _, refused := parseOutputMatcherChecked(o.notifyPattern); refused != "" {
		return nil, fmt.Errorf("%s", refusal(refused))
	}
	var res sdk.ToolResult
	var spawnErr error
	rt.withLock(func() {
		t, err := rt.spawnTask(o)
		if err != nil {
			spawnErr = err
			return
		}
		bound := func(v string) string { return truncateForTranscript(v, wakeManifestFieldMaxChars) }
		resource := ""
		if rc := t.ResourceControl; rc != nil {
			resource = "\nResource controls: " + rc.Mode
			if rc.UnitName != "" {
				resource += " (" + rc.UnitName + ")"
			}
		}
		expiry := "none"
		if t.ExpiresAt != nil {
			expiry = formatRelativeTime(*t.ExpiresAt, float64(clockNow()))
		}
		output := "no"
		if t.NotifyOnOutput {
			output = "yes"
			if t.NotifyPattern != "" {
				output = bound(t.NotifyPattern)
			}
		}
		mode := t.NotifyMode
		if mode == "" {
			mode = notifyAlways
		}
		dedupe := ""
		if t.DedupeKey != "" {
			dedupe = ", dedupeKey=" + bound(t.DedupeKey)
		}
		exit := "no"
		if t.NotifyOnExit {
			exit = "yes"
		}
		text := fmt.Sprintf("Started %s (pid %d) in the background.\nCommand: %s\nCwd: %s\nLog: %s\nExpiry: %s\nWakeups: exit=%s, output=%s, mode=%s%s%s",
			t.ID, t.Pid, bound(t.Command), bound(t.Cwd), bound(t.LogFile), expiry, exit, output, mode, dedupe, resource)
		res = toolResult(text, taskDetails{Action: "spawn", Task: compactBackgroundTaskSnapshot(rememberSnapshot(t))})
	})
	if spawnErr != nil {
		return nil, spawnErr
	}
	return res, nil
}

// ── forced background ───────────────────────────────────────────────────────

func (rt *bgRuntime) forcedBackgroundWindowMs(cwd string) float64 {
	return math.Max(1000, settingNumber("forcedBackgroundWindowSeconds", defaultForcedBackgroundMs/1000, cwd)*1000)
}

func (rt *bgRuntime) consumeForcedBackground(cwd string) bool {
	if rt.forceAt == nil {
		return false
	}
	if float64(clockNow()-*rt.forceAt) > rt.forcedBackgroundWindowMs(cwd) {
		rt.forceAt = nil
		return false
	}
	rt.forceAt = nil
	return true
}

// armForcedBackground arms the window for the next bash command; source is "shortcut" or "command".
func (rt *bgRuntime) armForcedBackground(ctx sdk.Context, source string) {
	idle, _ := ctx.IsIdle()
	rt.mu.Lock()
	rt.forceAt = new(clockNow())
	seconds := max(1, int(jsRound(rt.forcedBackgroundWindowMs(ctx.Cwd())/1000)))
	text := "/" + bgCommand + " next"
	if source == "shortcut" {
		text = formatShortcutHint(rt.backgroundBashShortcut)
	}
	note := fmt.Sprintf("%s armed. Next not-yet-started bash command in this turn will start as a background task. Already-running bash cannot be detached safely.", text)
	if idle {
		note = fmt.Sprintf("%s armed. Next bash command in the next %ds will start as a background task.", text, seconds)
	}
	rt.notify(note, "info")
	rt.mu.Unlock()
	rt.settle()
}

// decisionForBashCommand is the auto-background decision for a command; refused is set when a configured pattern cannot be honored.
func (rt *bgRuntime) decisionForBashCommand(command, cwd string) (d *bashBackgroundDecision, refused string) {
	if jsTrim(command) == "" {
		return nil, ""
	}
	if rt.consumeForcedBackground(cwd) {
		f := forcedBackgroundDecision(command, cwd)
		return &f, ""
	}
	if !settingBoolean("autoBackgroundBash", true, cwd) {
		return nil, ""
	}
	return autoBackgroundDecision(command, cwd)
}

// shouldAdoptActiveContext: a UI-bearing context is never replaced by a non-UI one (a direct RPC bash command reaches user_bash
// with a context that carries neither the session's UI nor its cwd). upstream: active-context.ts.
func shouldAdoptActiveContext(haveCurrent, currentHasUI, incomingHasUI bool) bool {
	if !haveCurrent {
		return true
	}
	if incomingHasUI {
		return true
	}
	return !currentHasUI
}

// onToolCall is the tool_call handler: a bash command the original would move into a background task becomes one, and the
// command the model's bash tool runs prints the acknowledgement.
func (rt *bgRuntime) onToolCall(ctx sdk.Context, data map[string]any) (any, error) {
	rt.mu.Lock()
	settings.recordProjectTrust(ctx.Cwd(), ctx.IsProjectTrusted)
	rt.setContext(ctx)
	rt.mu.Unlock()
	if name, _ := data["toolName"].(string); name != "bash" {
		return nil, nil
	}
	input, _ := data["input"].(map[string]any)
	command, _ := input["command"].(string)
	var block any
	var failure error
	rt.withLock(func() {
		decision, refused := rt.decisionForBashCommand(command, ctx.Cwd())
		if refused != "" {
			block = map[string]any{"block": true, "reason": refusal(refused)}
			return
		}
		if decision == nil {
			return
		}
		t, err := rt.spawnTask(spawnOptions{command: command, cwd: ctx.Cwd(), origin: originAutoBackground, notifyOnExit: &decision.notifyOnExit,
			notifyOnOutput: &decision.notifyOnOut, notifyPattern: decision.notifyPattern, title: decision.title})
		if err != nil {
			failure = err
			return
		}
		input["command"] = bashBackgroundAck(rememberSnapshot(t), *decision)
		if ctx.HasUI() {
			label := "Auto-backgrounded bash"
			if decision.forced {
				label = "Shortcut moved bash to background"
			}
			rt.notify(fmt.Sprintf("%s: %s (pid %d)", label, t.ID, t.Pid), "info")
		}
	})
	return block, failure
}

// onUserBash is the user_bash handler: a command the user typed (`!command`) becomes a task and its result is the acknowledgement.
func (rt *bgRuntime) onUserBash(ctx sdk.Context, data map[string]any) (any, error) {
	rt.mu.Lock()
	settings.recordProjectTrust(ctx.Cwd(), ctx.IsProjectTrusted)
	// Pi routes direct RPC bash through this handler too, so the context is not guaranteed to be the interactive session's.
	if shouldAdoptActiveContext(rt.active, rt.active && rt.ctx.HasUI(), ctx.HasUI()) {
		rt.setContext(ctx)
	}
	rt.mu.Unlock()
	command, _ := data["command"].(string)
	cwd := ctx.Cwd()
	if c, ok := data["cwd"].(string); ok {
		cwd = c
	}
	var result any
	var failure error
	rt.withLock(func() {
		decision, refused := rt.decisionForBashCommand(command, cwd)
		if refused != "" {
			result = map[string]any{"result": map[string]any{"output": refusal(refused), "exitCode": 1, "cancelled": false, "truncated": false}}
			return
		}
		if decision == nil {
			return
		}
		t, err := rt.spawnTask(spawnOptions{command: command, cwd: cwd, origin: originAutoBackground, notifyOnExit: &decision.notifyOnExit,
			notifyOnOutput: &decision.notifyOnOut, notifyPattern: decision.notifyPattern, title: decision.title})
		if err != nil {
			failure = err
			return
		}
		output := bashBackgroundAckText(rememberSnapshot(t), *decision)
		if ctx.HasUI() {
			label := "Auto-backgrounded user bash"
			if decision.forced {
				label = "Shortcut moved user bash to background"
			}
			rt.notify(fmt.Sprintf("%s: %s (pid %d)", label, t.ID, t.Pid), "info")
		}
		result = map[string]any{"result": map[string]any{"output": output, "exitCode": 0, "cancelled": false, "truncated": false}}
	})
	return result, failure
}

// ── commands ────────────────────────────────────────────────────────────────

func (rt *bgRuntime) taskCompletion(t *task, value string) sdk.AutocompleteItem {
	return sdk.AutocompleteItem{Label: t.ID, Value: value, Description: summarizeTaskStatus(t.Status, t.ExitCode, t.TerminationReason) + " · " + t.Command}
}

func startsWithFold(task *task, query string) bool {
	return query == "" || strings.HasPrefix(strings.ToLower(task.ID), query) || strings.HasPrefix(itoa(task.Pid), query)
}

// bgCompletions is getArgumentCompletions of /bg.
func (rt *bgRuntime) bgCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	trimmed := strings.TrimLeftFunc(prefix, isJSSpace)
	parts := strings.FieldsFunc(trimmed, isJSSpace)
	if len(parts) == 0 || (len(parts) == 1 && !strings.HasSuffix(trimmed, " ")) {
		all := []sdk.AutocompleteItem{
			{Label: "list", Value: "list", Description: "Show tracked tasks"},
			{Label: "next", Value: "next", Description: "Move the next bash command to background"},
			{Label: "run", Value: "run ", Description: "Spawn a background shell task"},
			{Label: "log", Value: "log ", Description: "Show task log tail"},
			{Label: "watch", Value: "watch ", Description: "Open the dashboard focused on a task"},
			{Label: "stop", Value: "stop ", Description: "Terminate a running task"},
			{Label: "clear", Value: "clear", Description: "Remove finished tasks"},
		}
		var out []sdk.AutocompleteItem
		for _, item := range all {
			if strings.HasPrefix(jsTrim(item.Value), strings.ToLower(trimmed)) {
				out = append(out, item)
			}
		}
		return out, nil
	}
	sub := parts[0]
	if sub != "log" && sub != "stop" && sub != "watch" {
		return nil, nil
	}
	if len(parts) > 2 || (len(parts) == 2 && strings.HasSuffix(trimmed, " ")) {
		return nil, nil
	}
	query := ""
	if len(parts) > 1 {
		query = strings.ToLower(parts[1])
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var items []sdk.AutocompleteItem
	for _, t := range rt.sortedTasks() {
		if startsWithFold(t, query) {
			items = append(items, rt.taskCompletion(t, sub+" "+t.ID))
		}
	}
	return items, nil
}

func (rt *bgRuntime) stopCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	query := strings.ToLower(strings.TrimLeftFunc(prefix, isJSSpace))
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var items []sdk.AutocompleteItem
	for _, t := range rt.sortedTasks() {
		if startsWithFold(t, query) {
			items = append(items, rt.taskCompletion(t, t.ID))
		}
	}
	return items, nil
}

// adopt is deps.setActiveCtx(ctx) of a command or shortcut.
func (rt *bgRuntime) adopt(ctx sdk.Context) {
	rt.mu.Lock()
	rt.setContext(ctx)
	rt.mu.Unlock()
}

func (rt *bgRuntime) spawnForCommand(ctx sdk.Context, command string) error {
	var spawnErr error
	rt.withLock(func() {
		t, err := rt.spawnTask(spawnOptions{command: command, cwd: ctx.Cwd()})
		if err != nil {
			spawnErr = err
			return
		}
		rt.notify(fmt.Sprintf("Started %s (pid %d) in the background.", t.ID, t.Pid), "info")
	})
	return spawnErr
}

func (rt *bgRuntime) notifyLocked(message, level string) {
	rt.withLock(func() { rt.notify(message, level) })
}

func (rt *bgRuntime) stopFor(token string) {
	rt.withLock(func() {
		ok, message := rt.requestStop(rt.resolveTask(token), stopUser)
		level := "warning"
		if ok {
			level = "info"
		}
		rt.notify(message, level)
	})
}

// bgHandler is the handler of /bg. The dashboard it opens is refused until the slice that ports it.
func (rt *bgRuntime) bgHandler() sdk.CommandFunc {
	return func(ctx sdk.Context, args string) error {
		rt.adopt(ctx)
		trimmed := jsTrim(args)
		switch {
		case trimmed == "":
			ctx.Notify(refusal("the background task dashboard"), "warning")
			return nil
		case trimmed == "list":
			rt.withLock(func() { rt.notify(rt.formatTaskListText(), "info") })
			return nil
		case trimmed == "next":
			rt.armForcedBackground(ctx, "command")
			return nil
		case trimmed == "clear":
			rt.withLock(func() {
				rt.notify(fmt.Sprintf("Removed %d finished background task(s).", rt.clearFinishedTasks()), "info")
			})
			return nil
		case strings.HasPrefix(trimmed, "run "):
			return rt.spawnForCommand(ctx, trimmed[4:])
		}
		if _, rest, ok := inspectArgs(trimmed); ok {
			var t *task
			rt.mu.Lock()
			t = rt.resolveTask(jsTrim(rest))
			rt.mu.Unlock()
			if t == nil {
				rt.notifyLocked("No background task matched that id or pid.", "warning")
				return nil
			}
			if strings.HasPrefix(trimmed, "log ") {
				rt.mu.Lock()
				limit := rt.outputBufferMax()
				rt.mu.Unlock()
				output := rt.readTaskOutput(t, limit)
				rt.notifyLocked(formatTaskLog(output, t.LogFile, ctx.Cwd()), "info")
				return nil
			}
			// watch opens the dashboard (slice 3)
			ctx.Notify(refusal("the background task dashboard"), "warning")
			return nil
		}
		if strings.HasPrefix(trimmed, "stop ") {
			rt.stopFor(jsTrim(trimmed[5:]))
			return nil
		}
		rt.notifyLocked("Unknown /"+bgCommand+" action. Try run <command>, list, log <id>, watch <id>, stop <id>, or clear.", "warning")
		return nil
	}
}

// inspectArgs is trimmed.match(/^(?:watch|log)\s+(.+)$/): the verb and the rest, which must be one line.
func inspectArgs(trimmed string) (verb, rest string, ok bool) {
	for _, v := range []string{"watch", "log"} {
		if !strings.HasPrefix(trimmed, v) {
			continue
		}
		tail := trimmed[len(v):]
		body := strings.TrimLeftFunc(tail, isJSSpace)
		if len(body) == len(tail) || body == "" || strings.ContainsAny(body, "\n\r\u2028\u2029") {
			return "", "", false
		}
		return v, body, true
	}
	return "", "", false
}

func (rt *bgRuntime) simpleCommands(e *sdk.Extension) {
	e.Command(bgCommand+":list", "Show tracked background tasks", func(ctx sdk.Context, _ string) error {
		rt.adopt(ctx)
		rt.withLock(func() { rt.notify(rt.formatTaskListText(), "info") })
		return nil
	})
	e.Command(bgCommand+":next", "Move the next bash command to a background task", func(ctx sdk.Context, _ string) error {
		rt.adopt(ctx)
		rt.armForcedBackground(ctx, "command")
		return nil
	})
	e.Command(bgCommand+":clear", "Remove finished background tasks", func(ctx sdk.Context, _ string) error {
		rt.adopt(ctx)
		rt.withLock(func() {
			rt.notify(fmt.Sprintf("Removed %d finished background task(s).", rt.clearFinishedTasks()), "info")
		})
		return nil
	})
	e.Command(bgCommand+":run", "Spawn a background shell task: /bg:run <command>", func(ctx sdk.Context, args string) error {
		rt.adopt(ctx)
		command := jsTrim(args)
		if command == "" {
			rt.notifyLocked("Usage: /bg:run <command>", "warning")
			return nil
		}
		return rt.spawnForCommand(ctx, command)
	})
	e.RegisterCommand(bgCommand+":stop", sdk.CommandOptions{
		Description: "Terminate a running background task: /bg:stop <id>", GetArgumentCompletions: rt.stopCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			rt.adopt(ctx)
			rt.stopFor(jsTrim(args))
			return nil
		},
	})
}
