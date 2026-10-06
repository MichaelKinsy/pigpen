package tintinweb_tasks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// RPC timeouts of the subagents integration. upstream: index.ts spawnSubagent (30 s) and stopSubagent (10 s).
var (
	spawnTimeout = 30 * time.Second
	stopTimeout  = 10 * time.Second
)

const (
	// reminderInterval is how many turns without task tool usage before a reminder is injected. upstream: index.ts:54.
	reminderInterval = 4
	// activeReminderInterval is the shorter interval while any task is in_progress. upstream: index.ts:57.
	activeReminderInterval = 2
	// reminderMaxTasks caps how many tasks the reminder echoes. upstream: index.ts:60.
	reminderMaxTasks = 10
	// autoClearDelay is how many turns completed tasks linger before auto-clearing. upstream: index.ts:68.
	autoClearDelay = 4
	// protocolVersion is the subagents RPC protocol version this extension speaks. upstream: index.ts:190.
	protocolVersion = 2
)

var taskToolNames = map[string]bool{"TaskCreate": true, "TaskList": true, "TaskGet": true, "TaskUpdate": true,
	"TaskOutput": true, "TaskStop": true, "TaskExecute": true}

func newRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// debug traces the RPC conversation to stderr when PI_TASKS_DEBUG is set. upstream: index.ts:37-40.
func debug(args ...any) {
	if os.Getenv("PI_TASKS_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, append([]any{"[pi-tasks]"}, args...)...)
	}
}

// jsonString is JSON.stringify: compact, with no HTML escaping.
func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimRight(buf.String(), "\n")
}

// jsSlice is s.slice(0, n) for JavaScript strings, which count UTF-16 code units.
func jsSlice(s string, n int) (string, bool) {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s, false
	}
	return string(utf16.Decode(units[:n])), true
}

func jsLength(s string) int { return len(utf16.Encode([]rune(s))) }

var (
	reminderTagRe  = regexp.MustCompile(`(?i)</?system-reminder>`)
	jsWhitespaceRe = " \t\n\v\f\r\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
)

// sanitizeField neutralizes a task field for the echo: it collapses newlines and strips reminder tags.
// upstream: index.ts:71-73.
func sanitizeField(value string) string {
	collapsed := collapseNewlines(value)
	return strings.Trim(reminderTagRe.ReplaceAllString(collapsed, ""), jsWhitespaceRe)
}

// collapseNewlines replaces each run of CR and LF characters with one space (/[\r\n]+/g).
func collapseNewlines(s string) string {
	var b strings.Builder
	inRun := false
	for _, r := range s {
		if r == '\r' || r == '\n' {
			if !inRun {
				b.WriteByte(' ')
			}
			inRun = true
			continue
		}
		inRun = false
		b.WriteRune(r)
	}
	return b.String()
}

// buildSystemReminder builds the system reminder, shaped after Claude Code's todo reminders: an empty-list
// nudge, or an echo of the current list as JSON. upstream: index.ts:80-126.
func buildSystemReminder(tasks []*task) string {
	if len(tasks) == 0 {
		return strings.Join([]string{
			"<system-reminder>",
			"This is a reminder that your task list is currently empty. DO NOT mention this to the user explicitly because they are already aware. If you are working on tasks that would benefit from a task list please use the TaskCreate tool to create one. If not, please feel free to ignore. Again do not mention this message to the user.",
			"</system-reminder>",
		}, "\n")
	}
	// Bound the echo on large lists, dropping completed tasks first (the reminder exists to surface unfinished
	// work); ties keep task order, as the stable sort does.
	shown := tasks
	if len(tasks) > reminderMaxTasks {
		rank := func(t *task) int {
			switch t.Status {
			case statusInProgress:
				return 0
			case statusPending:
				return 1
			}
			return 2
		}
		sorted := slices.Clone(tasks)
		slices.SortStableFunc(sorted, func(a, b *task) int { return rank(a) - rank(b) })
		shown = sorted[:reminderMaxTasks]
	}
	hidden := len(tasks) - len(shown)
	overflow := ""
	if hidden > 0 {
		plural := "s"
		if hidden == 1 {
			plural = ""
		}
		overflow = fmt.Sprintf(" (%d more task%s not shown — use TaskList for the full list.)", hidden, plural)
	}
	items := make([]orderedItem, 0, len(shown))
	for _, t := range shown {
		items = append(items, orderedItem{ID: t.ID, Content: sanitizeField(t.Subject), Status: t.Status, ActiveForm: sanitizeField(t.ActiveForm)})
	}
	// When truncated, do not claim these are the full contents.
	prefix := "The task tools haven't been used recently. DO NOT mention this explicitly to the user."
	header := prefix + " Here are the latest contents of your task list:"
	if hidden > 0 {
		header = prefix + " Here are your most relevant tasks (list truncated):"
	}
	return strings.Join([]string{
		"<system-reminder>",
		header,
		"",
		jsonString(items) + "." + overflow + " Continue on with the tasks at hand if applicable.",
		"</system-reminder>",
	}, "\n")
}

// orderedItem is one echoed task; activeForm is present only when the task has one. upstream: index.ts:106-113.
type orderedItem struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

// intervalFor is the effective reminder interval for a task list. upstream: index.ts:63-65.
func intervalFor(tasks []*task) int {
	for _, t := range tasks {
		if t.Status == statusInProgress {
			return activeReminderInterval
		}
	}
	return reminderInterval
}

type storeTarget struct{ key, path string }

type cascadeConfig struct {
	additionalContext, model string
	maxTurns                 any
}

// agentMap is the agent id → task id map, iterated in insertion order (a JavaScript Map).
type agentMap struct {
	keys []string
	m    map[string]string
}

func newAgentMap() *agentMap { return &agentMap{m: map[string]string{}} }
func (a *agentMap) set(agent, taskID string) {
	if _, ok := a.m[agent]; !ok {
		a.keys = append(a.keys, agent)
	}
	a.m[agent] = taskID
}
func (a *agentMap) get(agent string) (string, bool) { t, ok := a.m[agent]; return t, ok }
func (a *agentMap) has(agent string) bool           { _, ok := a.m[agent]; return ok }
func (a *agentMap) delete(agent string) {
	delete(a.m, agent)
	a.keys = slices.DeleteFunc(a.keys, func(k string) bool { return k == agent })
}
func (a *agentMap) clear() { a.keys, a.m = nil, map[string]string{} }

// resolve maps an agent id (or a prefix of one) to its task id, in insertion order.
func (a *agentMap) resolve(id string) (string, bool) {
	for _, k := range a.keys {
		if k == id || strings.HasPrefix(k, id) {
			return a.m[k], true
		}
	}
	return "", false
}

// app is the extension's state. One lock stands in for JavaScript's single thread: every handler holds it
// except while it waits (a model wait, an RPC reply, a dialog), as an `await` would give the thread up.
type app struct {
	pingOnce sync.Once
	e        *sdk.Extension
	mu       sync.Mutex

	cfg       tasksConfig
	piTasks   string
	taskScope string

	target    storeTarget
	store     *taskStore
	tracker   *processTracker
	widget    *taskWidget
	autoClear *autoClearManager
	cadence   *cadenceState

	latest              *sdk.Context
	cascade             *cascadeConfig
	agents              *agentMap
	subagentsAvailable  bool
	pendingWarning      string
	configured          bool
	configuredCwd       string
	persistedTasksShown bool
	agentsReattached    bool
}

func newApp(e *sdk.Extension) *app {
	a := &app{e: e, piTasks: os.Getenv("PI_TASKS"), agents: newAgentMap(), tracker: newProcessTracker(), cadence: createCadenceState()}
	// Project overrides need the context's cwd, which is unavailable while the extension is built. Start with
	// the global defaults, then merge the active workspace's overrides on the first context-bearing event.
	a.cfg = loadGlobalTasksConfig(agentDir())
	a.taskScope = a.cfg.str("taskScope", "session")
	a.target = a.resolveStoreTarget("", "")
	a.store = newTaskStore(a.target.path)
	a.widget = newTaskWidget(a.store, a.cfg)
	a.autoClear = newAutoClearManager(func() *taskStore { return a.store },
		func() string { return a.cfg.str("autoClearCompleted", "on_list_complete") }, autoClearDelay)
	return a
}

// isSessionScope: both session scopes persist one file per session; they differ only in where it lives.
func (a *app) isSessionScope() bool {
	return a.taskScope == "session" || a.taskScope == "session-global"
}

// resolveStoreTarget resolves both the backing path and a stable identity for the active store. upstream: index.ts:150-166.
func (a *app) resolveStoreTarget(cwd, sessionID string) storeTarget {
	p := a.piTasks
	switch {
	case p == "off":
		return storeTarget{key: "memory:env"}
	case strings.HasPrefix(p, "/"):
		return storeTarget{key: "path:" + p, path: p}
	case strings.HasPrefix(p, "."):
		if cwd == "" {
			return storeTarget{key: "pending:relative"}
		}
		path := filepath.Join(cwd, p)
		return storeTarget{key: "path:" + path, path: path}
	case p != "":
		return storeTarget{key: "named:" + p, path: p}
	case a.taskScope == "memory":
		return storeTarget{key: "memory:config"}
	case cwd == "":
		return storeTarget{key: "pending:workspace"}
	case a.isSessionScope() && sessionID != "":
		path := sessionTaskFile(cwd, sessionID, a.taskScope)
		return storeTarget{key: "path:" + path, path: path}
	case a.isSessionScope():
		return storeTarget{key: "pending:session"}
	}
	path := filepath.Join(cwd, ".pi", "tasks", "tasks.json")
	return storeTarget{key: "path:" + path, path: path}
}

// widgetUI draws on a retained host context. In Pi's RPC mode a component widget is not rendered (only its
// removal reaches the client); the rows are not sent there either.
type ctxUI struct{ ctx sdk.Context }

func (u ctxUI) setWidget(key string, lines []string) {
	if !u.ctx.HasUI() {
		return
	}
	if lines == nil {
		_ = u.ctx.SetWidget(key, nil)
		return
	}
	if u.ctx.Mode() == "rpc" {
		return
	}
	_ = u.ctx.SetWidget(key, lines, sdk.WidgetOptions{"placement": widgetPlacement})
}

// alive reports whether the host connection still exists (the spinner timer stops when it does not).
func (u ctxUI) alive() bool { return u.ctx.Err() == nil }

func (u ctxUI) theme() widgetTheme { return u.ctx.UITheme() }
func (u ctxUI) columns() int {
	if w := u.ctx.Width(); w > 0 {
		return w
	}
	return 80
}

// touch records the latest context and points the widget at its UI.
func (a *app) touch(ctx sdk.Context) {
	c := ctx
	a.latest = &c
	a.widget.setUI(ctxUI{ctx})
}

// unlocked runs fn without the app lock (a wait), and takes it back.
func (a *app) unlocked(fn func()) {
	a.mu.Unlock()
	defer a.mu.Lock()
	fn()
}

// ---- RPC with pi-subagents ----

type rpcReply struct {
	ok   bool
	data any
	err  string
}

// rpcCall emits a request on a channel and waits for its scoped reply: `<channel>:reply:<requestId>` carries
// {success, data|error}. The app lock is released while it waits. upstream: index.ts:101-123.
func (a *app) rpcCall(ctx sdk.Context, channel string, params map[string]any, timeout time.Duration) (any, error) {
	requestID := newRequestID()
	debug("rpc:send "+channel, requestID)
	replies := make(chan rpcReply, 1)
	var result any
	var err error
	a.unlocked(func() {
		unsub, onErr := ctx.Events().On(channel+":reply:"+requestID, func(_ sdk.Context, raw any) error {
			r := rpcReply{}
			if m, ok := raw.(map[string]any); ok {
				r.ok, _ = m["success"].(bool)
				r.data = m["data"]
				r.err, _ = m["error"].(string)
			}
			select {
			case replies <- r:
			default:
			}
			return nil
		})
		if onErr != nil {
			err = onErr
			return
		}
		defer unsub()
		payload := map[string]any{"requestId": requestID}
		for k, v := range params {
			payload[k] = v
		}
		if emitErr := ctx.Events().Emit(channel, payload); emitErr != nil {
			err = emitErr
			return
		}
		debug("rpc:emitted "+channel, requestID)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case r := <-replies:
			if r.ok {
				result = r.data
			} else {
				err = errors.New(r.err)
			}
		case <-timer.C:
			debug("rpc:timeout "+channel, requestID)
			err = errors.New(channel + " timeout")
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return result, err
}

// spawnSubagent spawns a subagent through the bus (needs @tintinweb/pi-subagents). upstream: index.ts:126-131.
func (a *app) spawnSubagent(ctx sdk.Context, agentType, prompt string, options map[string]any) (string, error) {
	data, err := a.rpcCall(ctx, "subagents:rpc:spawn", map[string]any{"type": agentType, "prompt": prompt, "options": options}, spawnTimeout)
	if err != nil {
		return "", err
	}
	m, _ := data.(map[string]any)
	id, _ := m["id"].(string)
	debug("spawn:ok", id)
	return id, nil
}

// stopSubagent stops a subagent; a failure is ignored. upstream: index.ts:134-136.
func (a *app) stopSubagent(ctx sdk.Context, agentID string) {
	_, _ = a.rpcCall(ctx, "subagents:rpc:stop", map[string]any{"agentId": agentID}, stopTimeout)
}

// consumeSubagentResult tells pi-subagents its result has been handed to the model, which suppresses the
// completion notification it would otherwise deliver. Fire-and-forget and outside the version handshake, so
// a pi-subagents without the handler keeps notifying. upstream: index.ts:138-148.
func (a *app) consumeSubagentResult(ctx sdk.Context, agentID string) {
	a.unlocked(func() {
		_ = ctx.Events().Emit("subagents:rpc:consume", map[string]any{"requestId": newRequestID(), "agentId": agentID})
	})
}

// ping asks pi-subagents for its protocol version on a scoped reply channel; any handler version answers.
// It reports false when the bus is not connected yet. upstream: index.ts:194-217 (checkSubagentsVersion).
func (a *app) ping(bus sdk.EventBus) bool {
	requestID := newRequestID()
	unsub, err := bus.On("subagents:rpc:ping:reply:"+requestID, func(_ sdk.Context, raw any) error {
		a.applyPingReply(raw)
		return nil
	})
	if err != nil {
		return false
	}
	if bus.Emit("subagents:rpc:ping", map[string]any{"requestId": requestID}) != nil {
		unsub()
		return false
	}
	time.AfterFunc(5*time.Second, unsub)
	return true
}

func (a *app) applyPingReply(raw any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var version *float64
	if m, ok := raw.(map[string]any); ok {
		if d, ok := m["data"].(map[string]any); ok {
			if v, ok := d["version"].(float64); ok {
				version = &v
			}
		}
	}
	switch {
	case version == nil:
		a.pendingWarning = "@tintinweb/pi-subagents is outdated — please update for task execution support."
	case *version > protocolVersion:
		a.pendingWarning = fmt.Sprintf("@tintinweb/pi-tasks is outdated (protocol v%d, pi-subagents has v%d) — please update for task execution support.", protocolVersion, int(*version))
	case *version < protocolVersion:
		a.pendingWarning = fmt.Sprintf("@tintinweb/pi-subagents is outdated (protocol v%d, pi-tasks has v%d) — please update for task execution support.", int(*version), protocolVersion)
	default:
		a.subagentsAvailable = true
	}
}

// buildTaskPrompt builds the prompt for a task run by a subagent, injecting the stored results of completed
// dependencies so cascaded agents have the context of their prerequisites. upstream: index.ts:222-247.
func (a *app) buildTaskPrompt(t *task, additionalContext string) string {
	prompt := fmt.Sprintf("You are executing task #%s: \"%s\"\n\n%s", t.ID, t.Subject, t.Description)
	if len(t.BlockedBy) > 0 {
		var results []string
		for _, depID := range t.BlockedBy {
			dep := a.store.get(depID)
			if dep == nil {
				continue
			}
			res, ok := dep.Metadata["result"].(string)
			if !ok || res == "" {
				continue
			}
			if jsLength(res) > 4000 {
				cut, _ := jsSlice(res, 4000)
				res = cut + "\n\n[... truncated — use TaskGet for full output]"
			}
			results = append(results, fmt.Sprintf("### Task #%s: %s\n%s", depID, dep.Subject, res))
		}
		if len(results) > 0 {
			prompt += "\n\n## Prerequisite task results\n\n" + strings.Join(results, "\n\n")
		}
	}
	if additionalContext != "" {
		prompt += "\n\n" + additionalContext
	}
	prompt += "\n\nComplete this task fully. Do not attempt to manage tasks yourself."
	return prompt
}

func withMeta(base map[string]any, kv ...any) map[string]any {
	m := cloneMap(base)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func strField(m any, key string) string {
	if mm, ok := m.(map[string]any); ok {
		s, _ := mm[key].(string)
		return s
	}
	return ""
}

// onSubagentCompleted marks the task completed and cascades into its unblocked dependents when auto-cascade
// is on. upstream: index.ts:255-337 (the completed listener).
func (a *app) onSubagentCompleted(ctx sdk.Context, raw any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, _ := raw.(map[string]any)
	id, _ := data["id"].(string)
	taskID, ok := a.agents.get(id)
	if !ok {
		return nil
	}
	a.agents.delete(id)
	t := a.store.get(taskID)
	if t == nil {
		return nil
	}
	var result any = data["result"] // absent: the key is dropped, as `result: undefined` is
	a.store.update(t.ID, updateFields{Status: strPtr(statusCompleted), Metadata: withMeta(t.Metadata, "result", result)})
	a.widget.setActiveTask(t.ID, false)

	// Auto-cascade: find unblocked dependents with an agentType.
	if a.cfg.flag("autoCascade") && a.cascade != nil && a.latest != nil {
		var unblocked []*task
		for _, c := range a.store.list(nil) {
			if c.Status != statusPending || c.Metadata["agentType"] == nil || c.Metadata["agentType"] == "" || !slices.Contains(c.BlockedBy, t.ID) {
				continue
			}
			all := true
			for _, dep := range c.BlockedBy {
				if d := a.store.get(dep); d == nil || d.Status != statusCompleted {
					all = false
				}
			}
			if all {
				unblocked = append(unblocked, c)
			}
		}
		for _, next := range unblocked {
			a.store.update(next.ID, updateFields{Status: strPtr(statusInProgress)})
			prompt := a.buildTaskPrompt(next, a.cascade.additionalContext)
			options := map[string]any{"description": next.Subject, "isBackground": true, "maxTurns": a.cascade.maxTurns}
			if a.cascade.maxTurns == nil {
				delete(options, "maxTurns")
			}
			if a.cascade.model != "" {
				options["model"] = a.cascade.model
			}
			agentType, _ := next.Metadata["agentType"].(string)
			agentID, err := a.spawnSubagent(ctx, agentType, prompt, options)
			if err != nil {
				a.store.update(next.ID, updateFields{Status: strPtr(statusPending), Metadata: withMeta(next.Metadata, "result", nil, "lastError", err.Error())})
				continue
			}
			a.agents.set(agentID, next.ID)
			a.store.update(next.ID, updateFields{Owner: strPtr(agentID), Metadata: withMeta(next.Metadata, "agentId", agentID)})
			a.widget.setActiveTask(next.ID, true)
		}
	}
	a.autoClear.trackCompletion(t.ID, a.cadence.CurrentTurn)
	a.widget.update()
	return nil
}

// onSubagentFailed stores the error and reverts the task to pending (the branch stops); an intentional stop
// completes the task and keeps the partial result. upstream: index.ts:339-363.
func (a *app) onSubagentFailed(_ sdk.Context, raw any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, _ := raw.(map[string]any)
	id, _ := data["id"].(string)
	taskID, ok := a.agents.get(id)
	if !ok {
		return nil
	}
	a.agents.delete(id)
	t := a.store.get(taskID)
	if t == nil {
		return nil
	}
	status, _ := data["status"].(string)
	errText, _ := data["error"].(string)
	if status == "stopped" {
		// Intentional stop: completed, with the partial result preserved.
		result, _ := data["result"].(string)
		var keep any = t.Metadata["result"]
		if result != "" {
			keep = result
		}
		a.store.update(t.ID, updateFields{Status: strPtr(statusCompleted), Metadata: withMeta(t.Metadata, "result", keep)})
		a.autoClear.trackCompletion(t.ID, a.cadence.CurrentTurn)
	} else {
		// An actual error: revert to pending. `result: null` drops the key: a task back to pending has no
		// current result, and an earlier run's would outrank this error everywhere it is read.
		lastError := errText
		if lastError == "" {
			lastError = status
		}
		a.store.update(t.ID, updateFields{Status: strPtr(statusPending), Metadata: withMeta(t.Metadata, "result", nil, "lastError", lastError)})
		a.autoClear.resetBatchCountdown()
	}
	a.widget.setActiveTask(t.ID, false)
	a.widget.update()
	return nil
}

// initializeStoreForContext resolves the store for the context's workspace and session, re-pointing it when
// they changed. upstream: index.ts:374-401.
func (a *app) initializeStoreForContext(ctx sdk.Context, reloadConfig bool) {
	cwd := ctx.Cwd()
	// The config map keeps its identity (the widget and the auto-clear manager hold it), but every value is
	// replaced, so overrides from a previous workspace cannot leak into the next one.
	if reloadConfig || !a.configured || a.configuredCwd != cwd {
		fresh := loadTasksConfig(cwd, agentDir())
		for k := range a.cfg {
			delete(a.cfg, k)
		}
		for k, v := range fresh {
			a.cfg[k] = v
		}
		a.taskScope = a.cfg.str("taskScope", "session")
	}
	// `pi --no-session` mints a session ID but never a session file; keying off the ID alone would write a
	// file for a session that can never be resumed. If the conversation is not persisted, neither is the list.
	sessionID := ""
	if a.isSessionScope() && a.piTasks == "" {
		if file, err := ctx.SessionManager().GetSessionFile(); err == nil && file != nil && *file != "" {
			sessionID, _ = ctx.SessionManager().GetSessionID()
		}
	}
	next := a.resolveStoreTarget(cwd, sessionID)
	if next.key != a.target.key {
		a.store = newTaskStore(next.path)
		a.widget.setStore(a.store)
		a.target = next
		// The new store owns a different task list, so the agent map is rebuilt from it.
		a.agentsReattached = false
	}
	a.configured, a.configuredCwd = true, cwd
}

// deleteSessionFileIfEmpty deletes an emptied session file and, under session-global only, the directory that
// held it once its last session is gone. A PI_TASKS path can point anywhere, and `<workspace>/.pi/tasks/` is
// left standing as it always has been. upstream: index.ts:408-413.
func (a *app) deleteSessionFileIfEmpty() {
	if !a.store.deleteFileIfEmpty() {
		return
	}
	if a.taskScope == "session-global" && a.piTasks == "" && a.configured {
		reclaimGlobalSessionTasksDir(a.configuredCwd)
	}
}

// reattachAgents re-links persisted in-progress tasks to the subagents still running for them: the agent →
// task map lives only in this instance, so a reload starts empty while the agents keep going. Only
// in_progress tasks are relinked (a task reverted to pending keeps its agentId, and relinking it would let a
// late event resurrect work the user reset). Runs once. upstream: index.ts:425-433.
func (a *app) reattachAgents() {
	if a.agentsReattached {
		return
	}
	a.agentsReattached = true
	for _, t := range a.store.list(nil) {
		if id, ok := t.Metadata["agentId"].(string); ok && id != "" && t.Status == statusInProgress {
			a.agents.set(id, t.ID)
		}
	}
}

// showPersistedTasks restores the widget on session start or resume when there is unfinished work: a new
// session clears an all-completed list (a clean slate), a resumed one shows everything. Runs once.
// upstream: index.ts:440-452.
func (a *app) showPersistedTasks(isResume bool) {
	if a.persistedTasksShown {
		return
	}
	a.persistedTasksShown = true
	tasks := a.store.list(nil)
	if len(tasks) == 0 {
		return
	}
	if !isResume && allCompleted(tasks) {
		a.store.clearCompleted()
		if a.isSessionScope() {
			a.deleteSessionFileIfEmpty()
		}
	} else {
		a.widget.update()
	}
}

func (a *app) warnIfPending(ctx sdk.Context) {
	if a.pendingWarning != "" {
		ctx.Notify(a.pendingWarning, "warning")
		a.pendingWarning = ""
	}
}

func (a *app) onTurnStart(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cadence.onTurnStart()
	a.touch(ctx)
	a.initializeStoreForContext(ctx, false)
	if a.autoClear.onTurnStart(a.cadence.CurrentTurn) {
		if a.isSessionScope() {
			a.deleteSessionFileIfEmpty()
		}
		a.widget.update()
	}
	return nil, nil
}

// onAgentSettled marks the end of a run: the only signal that separates a new batch of tasks from the same
// batch still being built. Nothing is cleared here. upstream: index.ts:469-471.
func (a *app) onAgentSettled(_ sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.autoClear.onRunEnded()
	return nil, nil
}

// onTurnEnd feeds per-turn token counts into the widget and detects a task left in_progress after a
// text-only turn (no tool calls, so tool_result never fires). upstream: index.ts:478-496.
func (a *app) onTurnEnd(_ sdk.Context, data map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if msg, ok := data["message"].(map[string]any); ok && msg["role"] == "assistant" {
		if usage, ok := msg["usage"].(map[string]any); ok {
			in, _ := usage["input"].(float64)
			out, _ := usage["output"].(float64)
			a.widget.addTokenUsage(int(in), int(out))
		}
	}
	if !a.cadence.ReminderInjectedThisCycle && !a.cadence.ReminderDue {
		gap := a.cadence.CurrentTurn - a.cadence.LastTaskToolUseTurn
		if gap >= activeReminderInterval {
			for _, t := range a.store.list(nil) {
				if t.Status == statusInProgress {
					a.cadence.ReminderDue = true
					break
				}
			}
		}
	}
	return nil, nil
}

// onToolResult only tracks cadence: non-task tool results are never mutated (a reminder appended to a bash
// result would corrupt the transcript); the injection happens in `context`. upstream: index.ts:498-530.
func (a *app) onToolResult(_ sdk.Context, data map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name, _ := data["toolName"].(string)
	cfg := cadenceConfig{ReminderInterval: reminderInterval, TaskToolNames: taskToolNames}
	if taskToolNames[name] {
		evaluateToolResult(a.cadence, name, false, cfg)
		return map[string]any{}, nil
	}
	if a.cadence.ReminderInjectedThisCycle {
		return map[string]any{}, nil
	}
	// Cheap first: avoid reading the store until the turn gap could matter.
	if a.cadence.CurrentTurn-a.cadence.LastTaskToolUseTurn < activeReminderInterval {
		return map[string]any{}, nil
	}
	tasks := a.store.list(nil)
	cfg.ReminderInterval = intervalFor(tasks)
	evaluateToolResult(a.cadence, name, len(tasks) > 0, cfg)
	return map[string]any{}, nil
}

// onContext injects the transient system reminder into the upcoming LLM call's messages, as a user message
// (so models without custom message types receive it); it is not persisted. upstream: index.ts:532-552.
func (a *app) onContext(_ sdk.Context, data map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !drainReminderForContext(a.cadence) {
		return map[string]any{}, nil
	}
	messages, _ := data["messages"].([]any)
	out := append([]any{}, messages...)
	out = append(out, map[string]any{
		"role":      "user",
		"content":   []any{map[string]any{"type": "text", "text": buildSystemReminder(a.store.list(nil))}},
		"timestamp": nowMs(),
	})
	return map[string]any{"messages": out}, nil
}

// onSessionStart rehydrates the session's tasks (before_agent_start only fires once the user prompts).
// upstream: index.ts:554-603.
func (a *app) onSessionStart(ctx sdk.Context, data map[string]any) (any, error) {
	// Announce this extension to pi-subagents, once the host has finished loading it. The original pings while its
	// factory runs; a ping sent while a Go extension is still loading deadlocks the host when pi-subagents is loaded
	// too (its reply is dispatched to this extension before it is ready), so the ping waits for the first session.
	a.pingOnce.Do(func() { go a.ping(ctx.Events()) })
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch(ctx)
	reason, _ := data["reason"].(string)
	// new/resume/fork reuse the running extension instance, so session-scoped state must be reset;
	// startup/reload re-run the factory and start clean.
	isSwitch := reason == "new" || reason == "resume" || reason == "fork"
	// A fork branches the conversation, so its tasks carry over as an independent copy: snapshot before the
	// store re-points to the new (empty) session file.
	var forkSeed *storeData
	if reason == "fork" {
		s := a.store.snapshot()
		forkSeed = &s
	}
	if isSwitch {
		a.persistedTasksShown = false
		a.agentsReattached = false
		// Task IDs restart at 1 in every session, so a mapping held over from the previous one points at an
		// unrelated task here.
		a.agents.clear()
		resetCadenceState(a.cadence)
		a.autoClear.reset()
		// Memory mode has no file to switch: clear tasks explicitly on /new.
		if reason == "new" && a.taskScope == "memory" {
			a.store.clearAll()
		}
	}
	a.initializeStoreForContext(ctx, true)
	if forkSeed != nil && len(forkSeed.Tasks) > 0 {
		a.store.seed(*forkSeed) // carry the parent's tasks into the fork
	}
	a.reattachAgents() // subagents outlive a reload; relink them before events arrive
	// resume/reload/fork keep tasks; startup/new clear an all-completed list.
	keepsTasks := reason == "reload" || reason == "resume" || reason == "fork"
	a.showPersistedTasks(keepsTasks)
	// Those tasks are shown for review, but the run that produced them ended with the session before.
	if keepsTasks {
		a.autoClear.onRunEnded()
	}
	a.warnIfPending(ctx)
	return nil, nil
}

// onBeforeAgentStart is the fallback for hosts that init the UI lazily; showPersistedTasks makes it run once.
func (a *app) onBeforeAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch(ctx)
	a.initializeStoreForContext(ctx, false)
	a.reattachAgents()
	a.showPersistedTasks(false)
	a.warnIfPending(ctx)
	return nil, nil
}

// onToolExecutionStart keeps the latest context fresh on every tool execution.
func (a *app) onToolExecutionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.touch(ctx)
	a.initializeStoreForContext(ctx, false)
	a.widget.update()
	return nil, nil
}

// Extension returns the tasks extension.
func Extension() *sdk.Extension {
	e := sdk.New("tintinweb-tasks")
	a := newApp(e)
	a.register(e)
	return e
}

func (a *app) register(e *sdk.Extension) {
	bus := e.Events()
	// Load order does not matter: ping on init (below) and the ready broadcast both detect pi-subagents.
	_, _ = bus.On("subagents:ready", func(ctx sdk.Context, _ any) error { a.ping(ctx.Events()); return nil })
	_, _ = bus.On("subagents:completed", a.onSubagentCompleted)
	_, _ = bus.On("subagents:failed", a.onSubagentFailed)

	e.OnEvent(sdk.EventTurnStart, a.onTurnStart)
	e.OnEvent(sdk.EventAgentSettled, a.onAgentSettled)
	e.OnEvent(sdk.EventTurnEnd, a.onTurnEnd)
	e.OnEvent(sdk.EventToolResult, a.onToolResult)
	e.OnEvent(sdk.EventContext, a.onContext)
	e.OnEvent(sdk.EventSessionStart, a.onSessionStart)
	e.OnEvent(sdk.EventBeforeAgentStart, a.onBeforeAgentStart)
	e.OnEvent(sdk.EventToolExecutionStart, a.onToolExecutionStart)
	a.registerTools(e)
	e.Command("tasks", "Manage tasks — view, create, clear completed", a.tasksCommand)
}
