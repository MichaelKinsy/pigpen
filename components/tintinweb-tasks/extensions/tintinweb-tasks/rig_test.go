package tintinweb_tasks_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tintinweb_tasks "github.com/MichaelKinsy/pigpen/tintinweb-tasks"
)

// eqv fails the test unless got and want are deeply equal.
func eqv(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

type (
	obj = map[string]any
	arr = []any
)

// rig is a fake host for the tasks extension. It answers the session reads the extension makes, scripts the
// dialogs, keeps the widget and notify calls, and plays the event bus: an emit reaches the extension's
// listeners (events.dispatch) and the Go-side listeners of a fake pi-subagents.
type rig struct {
	*Host
	cwd string

	mu        sync.Mutex
	sessionID string
	persisted bool
	script    []any // dialog answers in order: a string, an int (index into the choices) or nil (cancelled)
	selects   []selectCall
	inputs    []string
	extBus    map[string][]string // channel -> the extension's handler ids
	goBus     map[string][]func(data any)
}

type selectCall struct {
	Title   string
	Choices []string
}

// startRig boots the extension against a fake host whose workspace is cwd ("" = a fresh temp dir).
func startRig(t *testing.T, cwd string) *rig {
	t.Helper()
	if cwd == "" {
		cwd = t.TempDir()
	}
	r := &rig{cwd: cwd, sessionID: "s1", persisted: true, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	r.Host = StartHost(t, tintinweb_tasks.Extension(), HostOptions{Cwd: cwd, OnCallValue: r.answer})
	// The extension pings pi-subagents on the first session_start (not while it loads: see onSessionStart); a test
	// that needs the ping calls r.startSession.
	return r
}

// startSession fires the session_start the host sends after loading, and waits for the extension's ping.
func (r *rig) startSession() {
	r.t.Helper()
	r.fireEvent("session_start", obj{"reason": "startup"})
	r.waitEmit("subagents:rpc:ping")
}

// emitted lists the channels the extension emitted on, in order.
func (r *rig) emitted() []string {
	var out []string
	for _, c := range r.CallsTo("events.emit") {
		ch, _ := c.Args["channel"].(string)
		out = append(out, ch)
	}
	return out
}

// emittedOn lists the payloads the extension emitted on a channel.
func (r *rig) emittedOn(channel string) []any {
	var out []any
	for _, c := range r.CallsTo("events.emit") {
		if c.Args["channel"] == channel {
			out = append(out, c.Args["json"])
		}
	}
	return out
}

// waitEmit waits until the extension has emitted on channel (a failure fails the test).
func (r *rig) waitEmit(channel string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, ch := range r.emitted() {
			if ch == channel {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("the extension never emitted on %s", channel)
}

func (r *rig) answer(method string, args map[string]any) (any, string) {
	switch method {
	case "sessionRead":
		r.mu.Lock()
		defer r.mu.Unlock()
		switch args["method"] {
		case "getSessionId":
			return r.sessionID, ""
		case "getSessionFile":
			if !r.persisted {
				return json.RawMessage("null"), "" // a session pi does not persist reports no file
			}
			return "/sessions/" + r.sessionID + ".jsonl", ""
		}
	case "events.on":
		r.mu.Lock()
		defer r.mu.Unlock()
		ch, _ := args["channel"].(string)
		id, _ := args["handlerId"].(string)
		r.extBus[ch] = append(r.extBus[ch], id)
		return obj{}, ""
	case "events.off":
		r.mu.Lock()
		defer r.mu.Unlock()
		id, _ := args["handlerId"].(string)
		for ch, ids := range r.extBus {
			for i, got := range ids {
				if got == id {
					r.extBus[ch] = append(append([]string{}, ids[:i]...), ids[i+1:]...)
				}
			}
		}
		return obj{}, ""
	case "events.emit":
		ch, _ := args["channel"].(string)
		r.emitWithin(ch, args["json"])
		return obj{}, ""
	case "ui.select":
		title, _ := args["title"].(string)
		var choices []string
		for _, c := range args["options"].([]any) {
			choices = append(choices, c.(string))
		}
		r.mu.Lock()
		r.selects = append(r.selects, selectCall{title, choices})
		answer := r.next()
		r.mu.Unlock()
		switch a := answer.(type) {
		case int:
			return obj{"selected": choices[a], "ok": true}, ""
		case string:
			return obj{"selected": a, "ok": true}, ""
		}
		return obj{"ok": false}, ""
	case "ui.input":
		title, _ := args["title"].(string)
		r.mu.Lock()
		r.inputs = append(r.inputs, title)
		answer := r.next()
		r.mu.Unlock()
		if s, ok := answer.(string); ok {
			return obj{"text": s, "ok": true}, ""
		}
		return obj{"ok": false}, ""
	}
	return obj{}, ""
}

// next pops the next scripted answer (nil once the script runs out). The caller holds r.mu.
func (r *rig) next() any {
	if len(r.script) == 0 {
		return nil
	}
	a := r.script[0]
	r.script = r.script[1:]
	return a
}

func (r *rig) setSession(id string, persisted bool) {
	r.mu.Lock()
	r.sessionID, r.persisted = id, persisted
	r.mu.Unlock()
}

func (r *rig) setScript(answers ...any) {
	r.mu.Lock()
	r.script = answers
	r.mu.Unlock()
}

// emitWithin delivers a bus event to the Go-side listeners and then to the extension's.
func (r *rig) emitWithin(channel string, data any) {
	r.mu.Lock()
	goL := append([]func(any){}, r.goBus[channel]...)
	ids := append([]string{}, r.extBus[channel]...)
	r.mu.Unlock()
	for _, fn := range goL {
		fn(data)
	}
	for _, id := range ids {
		args, _ := json.Marshal(obj{"handlerId": id, "channel": channel, "json": data})
		r.Host.roundTrip(obj{"method": "events.dispatch", "args": json.RawMessage(args)})
	}
}

// emit is a Go-side emitter (a fake pi-subagents announcing an agent's end).
func (r *rig) emit(channel string, data any) { r.emitWithin(channel, data) }

// onBus subscribes a Go-side listener, returning its unsubscribe.
func (r *rig) onBus(channel string, fn func(data any)) func() {
	r.mu.Lock()
	r.goBus[channel] = append(r.goBus[channel], fn)
	idx := len(r.goBus[channel]) - 1
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if idx < len(r.goBus[channel]) {
			r.goBus[channel][idx] = func(any) {}
		}
	}
}

// fire delivers an event to the extension's handler (a handler error fails the test).
func (r *rig) fireEvent(event string, data obj) json.RawMessage {
	if event == "before_agent_start" { // the host always sends the prompt and the system prompt options
		data = obj{"prompt": "hello", "systemPrompt": "", "systemPromptOptions": obj{}}
	}
	return r.Host.Fire(event, data)
}

// tool runs a tool and returns its text; a failing tool reports its error text instead.
func (r *rig) tool(name string, params obj) (text string, failure string) {
	r.t.Helper()
	// The tool-execution lifecycle the host runs around a tool: tool_execution_start before, tool_result after.
	r.fireEvent("tool_execution_start", obj{"toolName": name})
	raw, failure := r.Host.Tool(name, params)
	if failure != "" {
		return "", failure
	}
	r.fireEvent("tool_result", obj{"toolName": name})
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		r.t.Fatalf("tool result %s: %v", raw, err)
	}
	return res.Content, ""
}

// must runs a tool that is expected to succeed.
func (r *rig) must(name string, params obj) string {
	r.t.Helper()
	text, failure := r.tool(name, params)
	if failure != "" {
		r.t.Fatalf("%s %v failed: %s", name, params, failure)
	}
	return text
}

func (r *rig) widgetCalls() []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("ui.setWidget") {
		out = append(out, c.Args)
	}
	return out
}

func (r *rig) notifies() []map[string]any {
	var out []map[string]any
	for _, c := range r.CallsTo("ui.notify") {
		out = append(out, c.Args)
	}
	return out
}

// runTasks runs the /tasks command (a failure fails the test).
func (r *rig) runTasks() {
	r.t.Helper()
	if failure := r.Host.Command("tasks", ""); failure != "" {
		r.t.Fatalf("/tasks failed: %s", failure)
	}
}

// contextMessages fires the `context` event with no messages and returns the messages the handler returned.
func (r *rig) contextMessages() []any {
	raw := r.fireEvent("context", obj{"messages": arr{}})
	var res struct {
		Messages []any `json:"messages"`
	}
	json.Unmarshal(raw, &res)
	return res.Messages
}

// reminderText is the text of the reminder a `context` event appended ("" when none).
func (r *rig) reminderText() string {
	msgs := r.contextMessages()
	if len(msgs) == 0 {
		return ""
	}
	last := msgs[len(msgs)-1].(map[string]any)
	return last["content"].([]any)[0].(map[string]any)["text"].(string)
}

func writeConfig(t *testing.T, path string, v any) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	data, _ := json.Marshal(v)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// useConfig sets the effective config for a test (the original mocks the config loaders): a global
// tasks-config.json in a scratch agent directory.
func useConfig(t *testing.T, cfg obj) {
	t.Helper()
	agent := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	writeConfig(t, filepath.Join(agent, "tasks-config.json"), cfg)
}

func useEnv(t *testing.T, kv ...string) {
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

// subMock is a fake @tintinweb/pi-subagents: it answers the ping, spawn, stop and consume RPCs, announces
// ready and settles agents the way the real extension does (the completion notification is held briefly and
// sent only if nothing consumed the result meanwhile). upstream: test/helpers/mock-pi.ts installSubagentsMock.
type subMock struct {
	r  *rig
	mu sync.Mutex

	spawned  []spawnRec
	stopped  []string
	consumed []string
	notified []string
	idN      int
	unsubs   []func()
	opts     subOpts
}

type subOpts struct {
	spawnError     string
	version        *float64
	withoutConsume bool
}

type spawnRec struct {
	ID, Type, Prompt string
	Options          map[string]any
}

const nudgeHold = 20 * time.Millisecond

func installSubagents(r *rig, opts ...subOpts) *subMock {
	m := &subMock{r: r}
	if len(opts) > 0 {
		m.opts = opts[0]
	}
	reply := func(channel, requestID string, payload obj) { r.emit(channel+":reply:"+requestID, payload) }
	rid := func(data any) string { return data.(map[string]any)["requestId"].(string) }
	m.unsubs = append(m.unsubs, r.onBus("subagents:rpc:ping", func(data any) {
		v := 2.0
		if m.opts.version != nil {
			v = *m.opts.version
		}
		reply("subagents:rpc:ping", rid(data), obj{"success": true, "data": obj{"version": v}})
	}))
	m.unsubs = append(m.unsubs, r.onBus("subagents:rpc:spawn", func(data any) {
		d := data.(map[string]any)
		if m.opts.spawnError != "" {
			reply("subagents:rpc:spawn", rid(data), obj{"success": false, "error": m.opts.spawnError})
			return
		}
		m.mu.Lock()
		m.idN++
		id := fmt.Sprintf("agent-%d", m.idN)
		options, _ := d["options"].(map[string]any)
		typ, _ := d["type"].(string)
		prompt, _ := d["prompt"].(string)
		m.spawned = append(m.spawned, spawnRec{id, typ, prompt, options})
		m.mu.Unlock()
		reply("subagents:rpc:spawn", rid(data), obj{"success": true, "data": obj{"id": id}})
	}))
	m.unsubs = append(m.unsubs, r.onBus("subagents:rpc:stop", func(data any) {
		agentID, _ := data.(map[string]any)["agentId"].(string)
		if m.known(agentID) {
			m.mu.Lock()
			m.stopped = append(m.stopped, agentID)
			m.mu.Unlock()
			reply("subagents:rpc:stop", rid(data), obj{"success": true})
		} else {
			reply("subagents:rpc:stop", rid(data), obj{"success": false, "error": "Agent not found"})
		}
	}))
	if !m.opts.withoutConsume {
		m.unsubs = append(m.unsubs, r.onBus("subagents:rpc:consume", func(data any) {
			agentID, _ := data.(map[string]any)["agentId"].(string)
			if m.known(agentID) {
				m.mu.Lock()
				m.consumed = append(m.consumed, agentID)
				m.mu.Unlock()
				reply("subagents:rpc:consume", rid(data), obj{"success": true})
			} else {
				reply("subagents:rpc:consume", rid(data), obj{"success": false, "error": "Agent not found"})
			}
		}))
	}
	r.emit("subagents:ready", obj{})
	return m
}

func (m *subMock) known(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.spawned {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (m *subMock) settle(channel, agentID string, data obj) {
	payload := obj{"id": agentID}
	for k, v := range data {
		payload[k] = v
	}
	m.r.emit(channel, payload)
	time.AfterFunc(nudgeHold, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, c := range m.consumed {
			if c == agentID {
				return
			}
		}
		m.notified = append(m.notified, agentID)
	})
}

// complete is an agent that finished successfully; fail one that failed (or was stopped: status "stopped").
func (m *subMock) complete(agentID, result string) {
	m.settle("subagents:completed", agentID, obj{"result": result})
}

func (m *subMock) fail(agentID, errText, status string) {
	m.settle("subagents:failed", agentID, obj{"error": errText, "status": status})
}

// afterNudgeHold waits past the notification hold, so notified is final.
func (m *subMock) afterNudgeHold() { time.Sleep(2 * nudgeHold) }

func (m *subMock) unsub() {
	for _, u := range m.unsubs {
		u()
	}
}

func (m *subMock) snapshot() (spawned []spawnRec, stopped, consumed, notified []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]spawnRec{}, m.spawned...), append([]string{}, m.stopped...), append([]string{}, m.consumed...), append([]string{}, m.notified...)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func itoa(n int) string                      { return fmt.Sprint(n) }

// extBusSnapshot copies the extension's listeners per channel.
func (r *rig) extBusSnapshot() map[string][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][]string{}
	for k, v := range r.extBus {
		out[k] = append([]string{}, v...)
	}
	return out
}
