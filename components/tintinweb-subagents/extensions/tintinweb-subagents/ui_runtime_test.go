package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Layer 1 (the fake host): the runtime wiring of the surfaces and what crosses the host boundary: the widget and the
// status, the fleet list's input subscription, the notification's details, and the registered renderers.

func (r *rig) waitFor(what string, ok func() bool) {
	r.t.Helper()
	for i := 0; i < 1000; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s", what)
}

func (r *rig) uiAgentOf(id string) *uiAgent {
	r.app.ui.mu.Lock()
	defer r.app.ui.mu.Unlock()
	return r.app.ui.agents[id]
}

func writeProjectSettings(t *testing.T, cwd, body string) {
	t.Helper()
	writeSettings(t, filepath.Join(cwd, ".pi", "subagents.json"), body)
}

func (r *rig) widgetCalls(key string) []HostCall {
	var out []HostCall
	for _, c := range r.CallsTo("ui.setWidget") {
		if c.Args["key"] == key {
			out = append(out, c)
		}
	}
	return out
}

func (r *rig) statusCalls() []string {
	var out []string
	for _, c := range r.CallsTo("ui.setStatus") {
		if c.Args["key"] == "subagents" {
			out = append(out, c.Args["text"].(string))
		}
	}
	return out
}

func spawnBackground(r *rig, desc string) string {
	r.t.Helper()
	text := r.must("Agent", obj{"prompt": "go", "description": desc, "subagent_type": "general-purpose", "run_in_background": true})
	return idFrom(r.t, text)
}

func TestFleetWiringTwins(t *testing.T) {
	const file = "fleet-wiring"
	tw(t, file, "captures terminal input on tool_execution_start (fleet hooked into the UI)", func(t *testing.T) {
		r := startRig(t)
		r.Fire("tool_execution_start", obj{})
		r.waitFor("the terminal input subscription", func() bool { return len(r.CallsTo("ui.onTerminalInput")) > 0 })
	})
	tw(t, file, "registers the belowEditor widget once a spawned agent has a session, then clears it on shutdown", func(t *testing.T) {
		r := startRig(t)
		r.Fire("tool_execution_start", obj{}) // the fleet captures THIS ui
		id := spawnBackground(r, "live one")
		// The child has a session once it reports an event.
		r.uiAgentOf(id).observe(obj{"type": "agent_start"}, nil)

		var reg []HostCall
		for _, c := range r.widgetCalls("fleet") {
			if c.Args["view"] != nil {
				reg = append(reg, c)
			}
		}
		if len(reg) == 0 {
			t.Fatal("the fleet widget should register with a render view")
		}
		opts, _ := reg[0].Args["options"].(map[string]any)
		eqStr(t, opts["placement"].(string), "belowEditor")

		r.Fire("session_shutdown", obj{})
		calls := r.widgetCalls("fleet")
		last := calls[len(calls)-1]
		if last.Args["view"] != nil || last.Args["content"] != nil {
			t.Errorf("dispose did not clear the widget: %v", last.Args)
		}
	})
}

func TestWidgetAndStatusReachTheHost(t *testing.T) {
	r := startRig(t)
	r.Fire("tool_execution_start", obj{})
	id := spawnBackground(r, "watch me")

	// The status bar names what runs; the widget registers above the editor, drawn as rows.
	r.waitFor("the status", func() bool { return len(r.statusCalls()) > 0 })
	eqStr(t, r.statusCalls()[0], "1 running agent")
	r.waitFor("the widget", func() bool { return len(r.widgetCalls("agents")) > 0 })
	first := r.widgetCalls("agents")[0]
	opts, _ := first.Args["options"].(map[string]any)
	eqStr(t, opts["placement"].(string), "aboveEditor")
	raw, _ := json.Marshal(first.Args["view"])
	hasStr(t, string(raw), "Agents")
	hasStr(t, string(raw), "watch me")

	// The agent finishes: it lingers for one turn, then the widget and the status clear.
	r.fleet.wait(t, 1)[0].finish("done")
	r.waitFor("the agent to settle", func() bool {
		r.app.mgr.mu.Lock()
		defer r.app.mgr.mu.Unlock()
		return r.app.mgr.records[id].Status == statusCompleted
	})
	r.Fire("tool_execution_start", obj{}) // a turn passes
	r.waitFor("the status to clear", func() bool { s := r.statusCalls(); return s[len(s)-1] == "" })
	calls := r.widgetCalls("agents")
	if last := calls[len(calls)-1]; last.Args["view"] != nil || last.Args["content"] != nil {
		t.Errorf("the widget stayed: %v", last.Args)
	}
}

func TestWidgetModeOffRegistersNothing(t *testing.T) {
	cwd := t.TempDir()
	writeProjectSettings(t, cwd, `{"widgetMode": "off", "fleetView": false}`)
	r := startRigIn(t, cwd)
	r.Fire("tool_execution_start", obj{})
	spawnBackground(r, "quiet")
	time.Sleep(100 * time.Millisecond)
	if n := len(r.widgetCalls("agents")); n != 0 {
		t.Errorf("%d widget calls in off mode", n)
	}
	if n := len(r.statusCalls()); n != 0 {
		t.Errorf("%d status calls in off mode", n)
	}
	if n := len(r.widgetCalls("fleet")); n != 0 {
		t.Errorf("%d fleet widget calls with fleetView off", n)
	}
}

func TestNotificationCarriesDetailsForItsRenderer(t *testing.T) {
	r := startRig(t)
	id := spawnBackground(r, "report back")
	r.fleet.wait(t, 1)[0].finish("all done here")
	r.waitFor("the notification", func() bool { return len(r.messages()) > 0 })
	msg := r.messages()[0]["message"].(map[string]any)
	eqStr(t, msg["customType"].(string), "subagent-notification")
	details, _ := msg["details"].(map[string]any)
	if details == nil {
		t.Fatalf("no details: %v", msg)
	}
	eqStr(t, details["id"].(string), id)
	eqStr(t, details["description"].(string), "report back")
	eqStr(t, details["status"].(string), "completed")
	eqStr(t, details["resultPreview"].(string), "all done here")
	if details["turnCount"].(float64) != 0 {
		t.Errorf("turnCount: %v", details["turnCount"])
	}
}

func TestRegisteredRenderers(t *testing.T) {
	r := startRig(t)
	t.Run("the notification renderer draws the details", func(t *testing.T) {
		out := renderMessage(t, r, "subagent-notification", obj{"details": obj{"id": "a", "description": "did it", "status": "completed", "toolUses": 2, "turnCount": 0, "totalTokens": 1500, "totalCost": 0, "durationMs": 2000, "resultPreview": "line one\nline two"}}, false)
		hasStr(t, out, "did it")
		hasStr(t, out, "completed")
		hasStr(t, out, "2 tool uses")
		hasStr(t, out, "1.5k token")
		hasStr(t, out, "⎿  line one")
		lacksStr(t, out, "line two")
		expanded := renderMessage(t, r, "subagent-notification", obj{"details": obj{"id": "a", "description": "did it", "status": "completed", "resultPreview": "line one\nline two"}}, true)
		hasStr(t, expanded, "line two")
	})
	t.Run("a notification without details is left to the host", func(t *testing.T) {
		raw, failure := r.Host.roundTrip(obj{"method": "render_message", "tool": "subagent-notification", "args": json.RawMessage(`{"message":{},"options":{},"width":80}`)})
		if failure != "" {
			t.Fatal(failure)
		}
		var got struct {
			Lines []string `json:"lines"`
		}
		_ = json.Unmarshal(raw, &got)
		if len(got.Lines) != 0 {
			t.Errorf("%q", got.Lines)
		}
	})
	t.Run("the Agent tool declares its own call and result renderers", func(t *testing.T) {
		res, failure := r.ResolveToolRenderers("Agent", nil)
		if failure != "" {
			t.Fatal(failure)
		}
		_ = res
		lines, failure := r.RenderTool("Agent", ToolRender{Card: "c1", Phase: "call", Args: obj{"subagent_type": "Explore", "description": "find the bug"}, Width: 100})
		if failure != "" {
			t.Fatal(failure)
		}
		out := strings.Join(lines, "\n")
		hasStr(t, out, "▸")
		hasStr(t, out, "Explore")
		hasStr(t, out, "find the bug")

		lines, failure = r.RenderTool("Agent", ToolRender{Card: "c1", Phase: "result", Args: obj{},
			Result:  obj{"content": []any{obj{"type": "text", "text": "Agent started in background."}}, "details": obj{"status": "background", "agentId": "abc", "displayName": "Agent", "description": "d", "subagentType": "general-purpose", "toolUses": 0, "tokens": "", "durationMs": 0}},
			Options: obj{"expanded": false, "isPartial": false}, Width: 100})
		if failure != "" {
			t.Fatal(failure)
		}
		hasStr(t, strings.Join(lines, "\n"), "Running in background (ID: abc)")

		// A Pi tool error shows its reason, not an invented status.
		lines, failure = r.RenderTool("Agent", ToolRender{Card: "c2", Phase: "result", Args: obj{},
			Result:  obj{"content": []any{obj{"type": "text", "text": "blocked by policy"}}, "details": obj{"status": "aborted"}},
			Options: obj{"expanded": false, "isPartial": false}, Context: obj{"isError": true}, Width: 100})
		if failure != "" {
			t.Fatal(failure)
		}
		hasStr(t, strings.Join(lines, "\n"), "blocked by policy")
	})
}

func renderMessage(t *testing.T, r *rig, customType string, message obj, expanded bool) string {
	t.Helper()
	args, _ := json.Marshal(obj{"message": message, "options": obj{"expanded": expanded}, "width": 120})
	raw, failure := r.Host.roundTrip(obj{"method": "render_message", "tool": customType, "args": json.RawMessage(args)})
	if failure != "" {
		t.Fatal(failure)
	}
	var got struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return strings.Join(got.Lines, "\n")
}

func TestUISettings(t *testing.T) {
	t.Run("the defaults", func(t *testing.T) {
		s := loadUISettings(t.TempDir())
		if s.widgetMode != widgetModeBackground || !s.fleetView || s.showCost || s.showModel || s.viewerMarkdown != viewerMarkdownAssistant {
			t.Errorf("%+v", s)
		}
	})
	t.Run("the project file overrides the global one, a wrong shape is dropped", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "subagents.json"), []byte(`{"widgetMode": "all", "showCost": true, "viewerMarkdown": "all"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		cwd := t.TempDir()
		writeProjectSettings(t, cwd, `{"widgetMode": "sideways", "showCost": false, "showModel": true, "fleetView": "yes", "viewerMarkdown": "off"}`)
		s := loadUISettings(cwd)
		if s.widgetMode != widgetModeAll || s.showCost || !s.showModel || !s.fleetView || s.viewerMarkdown != viewerMarkdownOff {
			t.Errorf("%+v", s)
		}
	})
	t.Run("a mode chosen with m is written to the project file, with the whole snapshot", func(t *testing.T) {
		r := settingsRig(t, `{"showCost": true}`)
		r.app.ui.chooseViewerMarkdown(viewerMarkdownAll)
		r.waitEmitted("subagents:settings_changed")
		data, err := os.ReadFile(filepath.Join(r.app.cwdValue(), ".pi", "subagents.json"))
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		// Every setting is written back (a key missing would be erased), but workflowsEnabled and fallbackSubagent,
		// which are written only when the user chose them.
		if raw["viewerMarkdown"] != "all" || raw["showCost"] != true || len(raw) != 22 {
			t.Errorf("%d keys: %v", len(raw), raw)
		}
	})
	t.Run("the display settings are no longer refused", func(t *testing.T) {
		r := settingsRig(t, `{"fleetView": true, "widgetMode": "all", "showCost": true, "showModel": true, "viewerMarkdown": "all"}`)
		eq(t, len(notices(r)), 0)
	})
}
