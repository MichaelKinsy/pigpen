package tintinweb_subagents

import (
	"regexp"
	"strings"
	"sync"
)

var claudePrefix = regexp.MustCompile(`(?i)^Claude\s+`)

// uiAgent is the live, UI-side state of one agent: what its child's event stream says about it (transcript, activity,
// usage, model), kept beside the manager's record. upstream: AgentActivity, the session the viewer reads, and the
// record's lifetimeUsage/compactionCount/invocation.
type uiAgent struct {
	mu          sync.Mutex
	hasSession  bool
	usage       lifetimeUsage
	compactions int
	inv         *invocation
	act         agentActivity
	percent     *float64 // the child's context-window utilization, nil when unknown
	messages    []map[string]any
	open        bool // the last message is still streaming
	nextSub     int
	turns       int
	subs        map[int]func()
	// onChange runs (outside the lock) after any change, for the hub to repaint.
	onChange func()
}

func newUIAgent(inv *invocation, maxTurns *int) *uiAgent {
	return &uiAgent{inv: inv, act: agentActivity{TurnCount: 1, MaxTurns: maxTurns}, subs: map[int]func(){}}
}

// GetSessionStats makes a uiAgent the session the context percent is read from (upstream: record.session).
func (u *uiAgent) GetSessionStats() (sessionStats, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return sessionStats{ContextPercent: u.percent}, nil
}

// uiAgent is the viewer's session.
func (u *uiAgent) Messages() []map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]map[string]any(nil), u.messages...)
}

func (u *uiAgent) Subscribe(f func()) func() {
	u.mu.Lock()
	id := u.nextSub
	u.nextSub++
	u.subs[id] = f
	u.mu.Unlock()
	return func() {
		u.mu.Lock()
		delete(u.subs, id)
		u.mu.Unlock()
	}
}

func (u *uiAgent) notify() {
	u.mu.Lock()
	subs := make([]func(), 0, len(u.subs))
	for _, f := range u.subs {
		subs = append(subs, f)
	}
	onChange := u.onChange
	u.mu.Unlock()
	for _, f := range subs {
		f()
	}
	if onChange != nil {
		onChange()
	}
}

// snapshotActivity is a copy of the activity (the renderers read it while events keep arriving).
func (u *uiAgent) snapshotActivity() *agentActivity {
	u.mu.Lock()
	defer u.mu.Unlock()
	a := u.act
	a.ActiveTools = append([]activeTool(nil), u.act.ActiveTools...)
	a.Session = u
	return &a
}

// observe consumes one event of the child's rpc stream. query sends a command to the child and returns its answer.
func (u *uiAgent) observe(ev map[string]any, query func(cmd string) (map[string]any, error)) {
	first := false
	u.mu.Lock()
	if !u.hasSession {
		u.hasSession, first = true, true
	}
	u.mu.Unlock()
	if first && query != nil {
		go u.refreshModel(query)
	}
	switch ev["type"] {
	case "message_start":
		u.onMessage(ev, false)
	case "message_update":
		u.onUpdate(ev)
	case "message_end":
		u.onMessage(ev, true)
	case "tool_execution_start":
		u.mu.Lock()
		name := msgStr(ev["toolName"])
		u.act.ActiveTools = append(u.act.ActiveTools, activeTool{key: name + "_" + msgStr(ev["toolCallId"]), name: name})
		u.mu.Unlock()
	case "tool_execution_end":
		u.mu.Lock()
		name := msgStr(ev["toolName"])
		for i, t := range u.act.ActiveTools {
			if t.name == name {
				u.act.ActiveTools = append(u.act.ActiveTools[:i], u.act.ActiveTools[i+1:]...)
				break
			}
		}
		u.act.ToolUses++
		u.mu.Unlock()
	case "turn_end":
		u.mu.Lock()
		u.turns++
		u.act.TurnCount = max(1, u.turns)
		u.mu.Unlock()
		if query != nil {
			go u.refreshStats(query)
		}
	case "compaction_end":
		if r, _ := ev["result"].(map[string]any); r != nil {
			if aborted, _ := ev["aborted"].(bool); !aborted {
				u.mu.Lock()
				u.compactions++
				u.mu.Unlock()
			}
		}
	}
	u.notify()
}

// refreshModel reads the model and thinking level the child resolved and writes them over the pre-session snapshot
// (the requested* fields are never overwritten). upstream: agent-manager.ts, on the session reporting them.
func (u *uiAgent) refreshModel(query func(cmd string) (map[string]any, error)) {
	res, err := query("get_state")
	if err != nil {
		return
	}
	data, _ := res["data"].(map[string]any)
	u.mu.Lock()
	if model, _ := data["model"].(map[string]any); model != nil {
		if u.inv == nil {
			u.inv = &invocation{}
		}
		// Read the kept request first: a caller's level survives being clamped AND, one line later, being replaced by
		// the effective one.
		requested := u.inv.RequestedThinking
		if requested == "" {
			requested = u.inv.Thinking
		}
		d := describeModel(msgStr(model["provider"]), msgStr(model["id"]), msgStr(model["name"]))
		u.inv.ModelName, u.inv.ModelID = d.name, d.id
		// A session that reports no level keeps the request rather than losing it.
		if level := msgStr(data["thinkingLevel"]); level != "" {
			u.inv.Thinking = level
			if requested != "" && requested != level {
				u.inv.RequestedThinking = requested
			}
		}
	}
	u.mu.Unlock()
	u.notify()
}

func (u *uiAgent) refreshStats(query func(cmd string) (map[string]any, error)) {
	res, err := query("get_session_stats")
	if err != nil {
		return
	}
	data, _ := res["data"].(map[string]any)
	cu, _ := data["contextUsage"].(map[string]any)
	var p *float64
	if f, ok := cu["percent"].(float64); ok {
		p = &f
	}
	u.mu.Lock()
	u.percent = p
	u.mu.Unlock()
}

// onMessage records a message_start/message_end. The message map is updated in place so the viewer's per-message
// Markdown cache (keyed by identity) keeps its component while the text streams.
func (u *uiAgent) onMessage(ev map[string]any, end bool) {
	msg, _ := ev["message"].(map[string]any)
	if msg == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	var cur map[string]any
	if u.open && len(u.messages) > 0 {
		cur = u.messages[len(u.messages)-1]
		for k, v := range msg {
			cur[k] = v
		}
	} else {
		cur = map[string]any{}
		for k, v := range msg {
			cur[k] = v
		}
		u.messages = append(u.messages, cur)
	}
	u.open = !end
	if !end {
		return
	}
	if msg["role"] == "assistant" {
		u.act.ResponseText = textOf(cur["content"])
		if us, _ := msg["usage"].(map[string]any); us != nil {
			u.usage.Input += asInt(us["input"])
			u.usage.Output += asInt(us["output"])
			u.usage.CacheWrite += asInt(us["cacheWrite"])
			u.usage.CacheRead += asInt(us["cacheRead"])
			if c, _ := us["cost"].(map[string]any); c != nil {
				if t, ok := c["total"].(float64); ok {
					u.usage.Cost += t
				}
			}
		}
	}
}

// onUpdate folds a streaming delta into the message being built.
func (u *uiAgent) onUpdate(ev map[string]any) {
	e, _ := ev["assistantMessageEvent"].(map[string]any)
	if e == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.open || len(u.messages) == 0 {
		u.messages = append(u.messages, map[string]any{"role": "assistant", "content": []any{}})
		u.open = true
	}
	cur := u.messages[len(u.messages)-1]
	content, _ := cur["content"].([]any)
	idx := asInt(e["contentIndex"])
	switch e["type"] {
	case "text_delta":
		for len(content) <= idx {
			content = append(content, map[string]any{"type": "text", "text": ""})
		}
		if b, ok := content[idx].(map[string]any); ok && b["type"] == "text" {
			b["text"] = msgStr(b["text"]) + msgStr(e["delta"])
		}
		cur["content"] = content
		u.act.ResponseText = textOf(content)
	case "toolcall_start":
		content = append(content, map[string]any{"type": "toolCall", "name": msgStr(e["toolName"])})
		cur["content"] = content
	}
}

func asInt(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// textOf is the text blocks of a content array concatenated (the response text so far).
func textOf(content any) string {
	var b strings.Builder
	for _, c := range contentMaps(content) {
		if c["type"] == "text" {
			b.WriteString(msgStr(c["text"]))
		}
	}
	return b.String()
}

// modelDescription is upstream describeModel: both display forms of a model.
type modelDescription struct{ name, id string }

// describeModel labels a model: its name without a leading "Claude", lowercased, and the canonical provider/id.
// upstream: model-resolver.ts describeModel.
func describeModel(provider, id, name string) modelDescription {
	if name == "" {
		name = id
	}
	return modelDescription{name: strings.ToLower(claudePrefix.ReplaceAllString(name, "")), id: provider + "/" + id}
}
