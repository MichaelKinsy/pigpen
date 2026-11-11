package readashboard_test

// Layer-1 cases through the fake host: what crosses the host boundary when the dashboard loads, sees REA tool
// events, opens /rea and renders tool cards.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/readashboard"
)

// payload writes an unpacked payload directory and returns the "mcpServers" state for a rea server in it.
func payload(t *testing.T) (dir string, servers []any) {
	t.Helper()
	dir = t.TempDir()
	meta := `{"target":"linux/amd64","rea":{"package":"rea-agents","version":"6.8.0","tag":"rea-agents-6.8.0"},"node":{"version":"24.22.2"},"treeSha256":"abc123","files":3,"bytes":9}`
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	servers = []any{map[string]any{
		"name":          "rea",
		"config":        map[string]any{"type": "stdio", "command": filepath.Join(dir, "node", "bin", "node"), "args": []string{filepath.Join(dir, "rea", "scripts", "rea.mjs"), "mcp"}},
		"extensionPath": "/ext/rea",
	}}
	return dir, servers
}

type fakeREA struct {
	mu      sync.Mutex
	doctors int
}

func (f *fakeREA) run(_ context.Context, _, _ string, args []string, _ int) ([]byte, bool, error) {
	f.mu.Lock()
	f.doctors++
	f.mu.Unlock()
	raw, err := os.ReadFile("testdata/doctor.json")
	return raw, false, err
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func lastLines(h *Host, method string) string {
	calls := h.CallsTo(method)
	if len(calls) == 0 {
		return ""
	}
	raw, _ := json.Marshal(calls[len(calls)-1].Args)
	return string(raw)
}

func TestLoadRegistersTheSurfaces(t *testing.T) {
	h := StartHost(t, readashboard.Extension(), HostOptions{})
	if !h.cmds["rea"] || len(h.cmds) != 1 {
		t.Fatalf("commands = %v (the runtime member owns /rea-runtime)", h.cmds)
	}
	for _, event := range []string{"session_start", "session_tree", "session_shutdown", "mcp_servers_change", "tool_execution_start", "tool_execution_update", "tool_execution_end", "before_agent_start"} {
		if !h.Registered(event) {
			t.Errorf("no handler for %s", event)
		}
	}
	if len(h.tools) != 0 {
		t.Fatalf("tools = %v", h.tools)
	}
	if n := h.ToolRenderers(); n != 1 {
		t.Fatalf("tool renderer resolvers = %d", n)
	}
}

func TestHeaderTitleAndNoServer(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "my-project")
	h := StartHost(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{Cwd: cwd})
	eventually(t, "title", func() bool { return len(h.CallsTo("ui.setTitle")) > 0 })
	if got := h.CallsTo("ui.setTitle")[0].Args["title"]; got != "REA · my-project" {
		t.Fatalf("title = %v", got)
	}
	eventually(t, "header", func() bool { return len(h.CallsTo("ui.setHeader")) > 0 })
	if got := lastLines(h, "ui.setHeader"); !strings.Contains(got, "starting") && !strings.Contains(got, "not registered") {
		t.Fatalf("header = %s", got)
	}
	// Nothing else noisy: no widget, no notification, no status key (the runtime member owns "rea").
	time.Sleep(100 * time.Millisecond)
	for _, method := range []string{"ui.setWidget", "ui.notify", "ui.setStatus", "appendEntry"} {
		if n := len(h.CallsTo(method)); n != 0 {
			t.Errorf("%s called %d times with no REA server", method, n)
		}
	}
}

func TestHeaderShowsVersionAndProvidersOnceTheServerRegisters(t *testing.T) {
	_, servers := payload(t)
	fake := &fakeREA{}
	h := StartHost(t, readashboard.WithRunner(fake.run), HostOptions{})
	eventually(t, "first header", func() bool { return len(h.CallsTo("ui.setHeader")) > 0 })

	// The runtime member registers rea a few seconds after load: the host pushes the new list and fires the event.
	h.PushState(map[string]any{"mcpServers": servers})
	h.Fire("mcp_servers_change", map[string]any{"servers": servers})
	eventually(t, "provider row", func() bool {
		got := lastLines(h, "ui.setHeader")
		return strings.Contains(got, "REA") && strings.Contains(got, "6.8.0") && strings.Contains(got, "Ghidra")
	})
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.doctors != 1 {
		t.Fatalf("doctor ran %d times", fake.doctors)
	}
}

func TestDoctorRunsOncePerRegistration(t *testing.T) {
	_, servers := payload(t)
	fake := &fakeREA{}
	h := startWithServers(t, readashboard.WithRunner(fake.run), HostOptions{}, servers)
	eventually(t, "provider row", func() bool {
		got := lastLines(h, "ui.setHeader")
		return strings.Contains(got, "6.8.0") && strings.Contains(got, "Hopper")
	})
	// A session_start does not run the doctor again for the same registration.
	h.Fire("session_start", map[string]any{"reason": "startup"})
	time.Sleep(100 * time.Millisecond)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.doctors != 1 {
		t.Fatalf("doctor ran %d times", fake.doctors)
	}
}

func TestNoUIDrawsNothing(t *testing.T) {
	_, servers := payload(t)
	no := false
	h := startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{Mode: "print", HasUI: &no}, servers)
	h.Fire("session_start", map[string]any{"reason": "startup"})
	h.Fire("tool_execution_start", map[string]any{"toolCallId": "c1", "toolName": "mcp__rea__open_binary", "args": map[string]any{"path": "/bin/ls"}})
	time.Sleep(150 * time.Millisecond)
	for _, method := range []string{"ui.setHeader", "ui.setWidget", "ui.setTitle"} {
		if n := len(h.CallsTo(method)); n != 0 {
			t.Errorf("%s called without a UI", method)
		}
	}
	// /rea notifies a one-line summary instead of opening the overlay.
	if err := h.Command("rea", ""); err != "" {
		t.Fatal(err)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || len(h.CallsTo("ui.custom")) != 0 {
		t.Fatalf("notes=%v custom=%d", notes, len(h.CallsTo("ui.custom")))
	}
	msg, _ := notes[0].Args["message"].(string)
	if !strings.HasPrefix(msg, "REA 6.8.0") || !strings.Contains(msg, "providers: 0 ready, 0 failing, 5 not configured") || !strings.Contains(msg, "calls: 1 (0 ok, 0 error, 1 running)") || strings.Contains(msg, "\n") {
		t.Fatalf("summary = %q", msg)
	}
}

func TestSummaryWithoutAServer(t *testing.T) {
	h := StartHost(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{Mode: "print", HasUI: boolp(false)})
	if err := h.Command("rea", ""); err != "" {
		t.Fatal(err)
	}
	notes := h.CallsTo("ui.notify")
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Args["message"].(string), "REA not registered") {
		t.Fatalf("notes = %v", notes)
	}
}

func boolp(b bool) *bool { return &b }

func TestToolEventsFeedTheWidgetAndPersistEntries(t *testing.T) {
	_, servers := payload(t)
	h := startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{}, servers)
	h.Fire("session_start", map[string]any{"reason": "startup"})

	// A call that is not REA's is ignored.
	h.Fire("tool_execution_start", map[string]any{"toolCallId": "b1", "toolName": "bash", "args": map[string]any{"command": "ls"}})
	h.Fire("tool_execution_end", map[string]any{"toolCallId": "b1", "toolName": "bash", "result": map[string]any{}})
	if n := len(h.CallsTo("appendEntry")); n != 0 {
		t.Fatalf("bash call persisted (%d entries)", n)
	}

	// A call made inside a codemode script carries the parent id.
	h.Fire("tool_execution_start", map[string]any{"toolCallId": "cm/1", "parentToolCallId": "cm", "toolName": "mcp__rea__inspect_artifact", "args": map[string]any{"path": "/bin/ls"}})
	eventually(t, "running widget", func() bool {
		got := lastLines(h, "ui.setWidget")
		return strings.Contains(got, "inspect_artifact") && strings.Contains(got, "/bin/ls") && strings.Contains(got, "aboveEditor")
	})
	h.Fire("tool_execution_update", map[string]any{"toolCallId": "cm/1", "parentToolCallId": "cm", "toolName": "mcp__rea__inspect_artifact", "args": map[string]any{"path": "/bin/ls"}, "partialResult": map[string]any{}})
	if n := len(h.CallsTo("appendEntry")); n != 0 {
		t.Fatalf("an update persisted an entry")
	}
	h.Fire("tool_execution_end", map[string]any{
		"toolCallId": "cm/1", "parentToolCallId": "cm", "toolName": "mcp__rea__inspect_artifact", "durationMs": 250.0,
		"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"result":{"evidence":[{"evidence_id":"ev_1"}],"limitations":["stripped"]}}`}}},
	})
	entries := h.CallsTo("appendEntry")
	if len(entries) != 1 || entries[0].Args["customType"] != "rea.call" {
		t.Fatalf("entries = %+v", entries)
	}
	data, _ := entries[0].Args["data"].(map[string]any)
	if data["id"] != "cm/1" || data["parent"] != "cm" || data["tool"] != "inspect_artifact" || data["target"] != "/bin/ls" || data["ok"] != true || data["ms"] != 250.0 || data["evidence"] != 1.0 || data["limitations"] != 1.0 {
		t.Fatalf("entry data = %v", data)
	}
	if raw, _ := json.Marshal(data); len(raw) > 1024 || strings.Contains(string(raw), "content") {
		t.Fatalf("entry is not a bounded summary: %s", raw)
	}
	eventually(t, "finished widget", func() bool { return strings.Contains(lastLines(h, "ui.setWidget"), "ev 1 · lim 1") })
}

func reaEntry(id, parent, call, tool, target string, ok bool, evidence, limitations int) map[string]any {
	return map[string]any{
		"type": "custom", "id": id, "parentId": parent, "customType": "rea.call",
		"data": map[string]any{"v": 1, "id": call, "tool": tool, "target": target, "ok": ok, "ms": 40, "evidence": evidence, "limitations": limitations},
	}
}

func TestSessionStartRebuildsTheTimelineFromPersistedEntries(t *testing.T) {
	_, servers := payload(t)
	entries := []any{
		map[string]any{"type": "message", "id": "m1", "parentId": nil, "message": map[string]any{"role": "user", "content": "hi"}},
		reaEntry("e1", "m1", "call-1", "open_binary", "/bin/ls", true, 0, 0),
		reaEntry("e2", "e1", "call-2", "inspect_artifact", "/bin/ls", false, 1, 2),
		reaEntry("e3", "m1", "other-branch", "decompile", "/bin/cat", true, 0, 0), // a sibling branch: not the current one
	}
	h := startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method != "watchSessionLog" {
				return nil, ""
			}
			if args["complete"] == true {
				return map[string]any{"entries": []any{}, "entryCount": len(entries), "hasMore": false, "leafId": "e2"}, ""
			}
			return map[string]any{"entries": entries, "entryCount": len(entries), "hasMore": false, "leafId": "e2"}, ""
		},
	}, servers)
	h.Fire("session_start", map[string]any{"reason": "resume"})
	eventually(t, "restored widget", func() bool {
		got := lastLines(h, "ui.setWidget")
		return strings.Contains(got, "open_binary") && strings.Contains(got, "inspect_artifact") && strings.Contains(got, "ev 1 · lim 2")
	})
	if got := lastLines(h, "ui.setWidget"); strings.Contains(got, "decompile") {
		t.Fatalf("a call from another branch was restored: %s", got)
	}
	// Restoring appends nothing.
	if n := len(h.CallsTo("appendEntry")); n != 0 {
		t.Fatalf("%d entries appended on restore", n)
	}
}

func TestBeforeAgentStartAppendsTheIdentitySection(t *testing.T) {
	_, servers := payload(t)
	h := startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{}, servers)
	eventually(t, "server", func() bool { return strings.Contains(lastLines(h, "ui.setHeader"), "6.8.0") })
	event := map[string]any{"prompt": "hi", "systemPromptOptions": map[string]any{"sections": map[string]any{"existing": "keep me"}, "cwd": "/x"}}
	got := string(h.Fire("before_agent_start", event))
	for _, want := range []string{"rea_harness", "REA harness", "6.8.0", `MCP server \"rea\"`, "mcp__rea__", "existing", "keep me"} {
		if !strings.Contains(got, want) {
			t.Errorf("result lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"systemPrompt"`) && strings.Contains(got, `"systemPrompt":"`) {
		t.Fatalf("the prompt was replaced: %s", got)
	}

	// Without a rea server the prompt is left alone.
	bare := StartHost(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{})
	got = string(bare.Fire("before_agent_start", map[string]any{"prompt": "hi", "systemPromptOptions": map[string]any{"sections": map[string]any{}, "cwd": "/x"}}))
	if strings.Contains(got, "rea_harness") {
		t.Fatalf("identity added with no server: %s", got)
	}
}

func TestOverlayCommandOpensAFullScreenOverlay(t *testing.T) {
	_, servers := payload(t)
	h := startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "ui.custom" {
				return CustomResult(nil), ""
			}
			return nil, ""
		},
	}, servers)
	for _, args := range []string{"", "providers"} {
		if err := h.Command("rea", args); err != "" {
			t.Fatal(err)
		}
	}
	custom := h.CallsTo("ui.custom")
	if len(custom) != 2 {
		t.Fatalf("ui.custom calls = %d", len(custom))
	}
	a := custom[0].Args
	if a["title"] != "REA" || a["overlay"] != true || a["widthFraction"] != 1.0 || a["heightFraction"] != 1.0 {
		t.Fatalf("overlay options = %v", a)
	}
	if n := len(h.CallsTo("sendUserMessage")); n != 0 {
		t.Fatalf("closing the overlay sent %d messages", n)
	}
}

func runAction(t *testing.T, action string, answer map[string]any, idle bool) (h *Host, asked []map[string]any) {
	t.Helper()
	_, servers := payload(t)
	var mu sync.Mutex
	h = startWithServers(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			mu.Lock()
			defer mu.Unlock()
			switch method {
			case "ui.custom":
				return CustomResult(map[string]any{"action": action, "target": "/bin/ls"}), ""
			case "ui.input":
				asked = append(asked, args)
				return answer, ""
			case "isIdle":
				return map[string]any{"idle": idle}, ""
			}
			return nil, ""
		},
	}, servers)
	if err := h.Command("rea", ""); err != "" {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return h, append([]map[string]any(nil), asked...)
}

func sentContent(t *testing.T, h *Host) (string, map[string]any) {
	t.Helper()
	sent := h.CallsTo("sendUserMessage")
	if len(sent) != 1 {
		t.Fatalf("sent %d messages", len(sent))
	}
	content, _ := sent[0].Args["content"].(string)
	opts, _ := sent[0].Args["options"].(map[string]any)
	return content, opts
}

func TestInvestigateSendsATemplatedRequest(t *testing.T) {
	h, asked := runAction(t, "investigate", map[string]any{"ok": true, "text": "what does it import?"}, true)
	if len(asked) != 1 {
		t.Fatalf("asked %d times", len(asked))
	}
	content, opts := sentContent(t, h)
	want := "Investigate this target with the REA tools (MCP server \"rea\"): /bin/ls\n\nQuestion: what does it import?"
	if !strings.Contains(content, want) {
		t.Fatalf("message %q lacks %q", content, want)
	}
	if v := opts["deliverAs"]; v != nil && v != "" {
		t.Fatalf("idle agent: deliverAs = %v", v)
	}
	// A busy agent gets it queued.
	h, _ = runAction(t, "investigate", map[string]any{"ok": true, "text": "q"}, false)
	if _, opts = sentContent(t, h); opts["deliverAs"] != "followUp" {
		t.Fatalf("busy agent: options = %v", opts)
	}
}

func TestDeriveSendsATemplatedRequest(t *testing.T) {
	for _, tc := range []struct{ typed, name string }{
		{"", "pig-ls-like"}, // an empty answer keeps the default
		{"  ", "pig-ls-like"},
		{"My Name!", "my-name"}, // sanitized
		{"pig-ls-like", "pig-ls-like"},
	} {
		h, asked := runAction(t, "derive", map[string]any{"ok": true, "text": tc.typed}, true)
		if len(asked) != 1 || asked[0]["placeholder"] != "pig-ls-like" {
			t.Fatalf("%q: prompts = %v", tc.typed, asked)
		}
		content, _ := sentContent(t, h)
		want := "Use the pigpen-derive-a-harness skill to derive a piglet named " + tc.name + " that works like /bin/ls. " +
			"Keep REA output under evidence/, write the behavior spec under spec/ with each claim linked to an evidence_id, " +
			"author piglets/" + tc.name + "/ from spec/ only, and end the iteration with pig piglet validate and pig piglet show."
		if content != want {
			t.Fatalf("%q: message\n%s\nwant\n%s", tc.typed, content, want)
		}
	}
}

func TestCancelledInputSendsNothing(t *testing.T) {
	for _, action := range []string{"investigate", "derive"} {
		h, asked := runAction(t, action, map[string]any{"ok": false}, true)
		if len(asked) != 1 || len(h.CallsTo("sendUserMessage")) != 0 {
			t.Fatalf("%s: asked=%d sent=%d", action, len(asked), len(h.CallsTo("sendUserMessage")))
		}
	}
	// An investigation with an empty question is not sent either.
	if h, _ := runAction(t, "investigate", map[string]any{"ok": true, "text": " "}, true); len(h.CallsTo("sendUserMessage")) != 0 {
		t.Fatal("empty question sent")
	}
}

func TestDirectREAToolCardsAndOtherToolsLeftToPiG(t *testing.T) {
	h := StartHost(t, readashboard.WithRunner((&fakeREA{}).run), HostOptions{})
	own, failure := h.ResolveToolRenderers("mcp__rea__inspect_artifact", nil)
	if failure != "" || own.Use != "own" || !own.RendersCall || !own.RendersResult {
		t.Fatalf("resolve = %+v %s", own, failure)
	}
	lines, failure := h.RenderTool("mcp__rea__inspect_artifact", ToolRender{Card: "c1", Renderers: own.Renderers, Args: map[string]any{"path": "/bin/ls"}})
	if failure != "" || len(lines) != 1 || !strings.Contains(lines[0], "inspect/inspect_artifact") || !strings.Contains(lines[0], "/bin/ls") {
		t.Fatalf("call lines = %q %s", lines, failure)
	}
	lines, failure = h.RenderTool("mcp__rea__inspect_artifact", ToolRender{
		Card: "c1", Renderers: own.Renderers, Phase: "result",
		Result:  map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"result":{"evidence":[{"evidence_id":"e"}],"limitations":[]}}`}}},
		Options: map[string]any{}, Context: map[string]any{"durationMs": 90},
	})
	if failure != "" || len(lines) != 1 || !strings.Contains(lines[0], "ok") || !strings.Contains(lines[0], "1 evidence") || !strings.Contains(lines[0], "0 limitations") {
		t.Fatalf("result lines = %q %s", lines, failure)
	}
	other, failure := h.ResolveToolRenderers("bash", &ToolRenderersDecl{RendersCall: true})
	if failure != "" || other.Use != "next" {
		t.Fatalf("bash resolve = %+v %s", other, failure)
	}
	none, _ := h.ResolveToolRenderers("read", nil)
	if none.Use != "none" {
		t.Fatalf("read resolve = %+v", none)
	}
}
