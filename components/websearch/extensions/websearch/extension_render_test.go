package websearch_test

import (
	"testing"

	ws "github.com/MichaelKinsy/pigpen/websearch"
)

func TestRenderersDrawThroughTheHost(t *testing.T) {
	agentDir(t, `{"toolActivation":"eager"}`)
	h := StartHost(t, ws.Extension(), HostOptions{})
	// The tools that draw their own card are the three of the original; the others use the default.
	for _, tool := range []string{"web_search", "fetch_content", "get_search_content"} {
		lines, failure := h.RenderTool(tool, ToolRender{Card: "c-" + tool, Args: map[string]any{"query": "q", "url": "https://example.com", "responseId": "abcdefghij"}})
		if failure != "" {
			t.Fatalf("%s call: %s", tool, failure)
		}
		_ = lines
		_, failure = h.RenderTool(tool, ToolRender{Card: "r-" + tool, Phase: "result",
			Result:  map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}}, "details": map[string]any{"error": "boom"}},
			Options: map[string]any{"expanded": true}})
		if failure != "" {
			t.Fatalf("%s result: %s", tool, failure)
		}
	}
	// A progress the original's bar cannot draw fails the renderer, which the host answers with its fallback.
	if _, failure := h.RenderTool("web_search", ToolRender{Card: "bad", Phase: "result", Result: map[string]any{"content": []any{}, "details": map[string]any{"progress": 2.0}}, Options: map[string]any{"isPartial": true}}); failure == "" {
		t.Fatal("a progress of 2 rendered")
	}
	if _, failure := h.RenderTool("source_check", ToolRender{Card: "none"}); failure == "" {
		t.Fatal("source_check has no renderer of its own")
	}
}

func TestExtensionRegistersTheCuratorCommandsAndLifecycle(t *testing.T) {
	agentDir(t, "")
	h := StartHost(t, ws.Extension(), HostOptions{})
	for _, name := range []string{"websearch", "curator", "google-account", "search"} {
		if !h.cmds[name] {
			t.Errorf("command %s is not registered", name)
		}
	}
	// The approval of the remaining searches ends at every run boundary.
	for _, ev := range []string{"before_agent_start", "agent_settled", "session_start", "session_tree", "session_shutdown"} {
		if !h.Registered(ev) {
			t.Errorf("no handler for %s", ev)
		}
	}
	if h.ToolRenderers() != 0 {
		t.Errorf("the renderers belong to the tools, not to resolvers: %d", h.ToolRenderers())
	}
}

func TestCuratorCommandThroughTheHost(t *testing.T) {
	agentDir(t, `{"workflow":"summary-review"}`)
	h := StartHost(t, ws.Extension(), HostOptions{})
	if failure := runCommand(t, h, "curator"); failure != "" {
		t.Fatal(failure)
	}
	calls := h.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("%+v", h.Calls())
	}
	msg, _ := calls[0].Args["message"].(map[string]any)
	opts, _ := calls[0].Args["options"].(map[string]any)
	details, _ := msg["details"].(map[string]any)
	blocks, _ := msg["content"].([]any)
	first, _ := blocks[0].(map[string]any)
	if msg["customType"] != "curator-config" || msg["display"] != true || details["workflow"] != "none" ||
		first["text"] != "Curator disabled — web_search will return raw results" || opts["triggerTurn"] != false || opts["deliverAs"] != "followUp" {
		t.Fatalf("%+v", calls[0].Args)
	}
}

func TestGoogleAccountCommandThroughTheHost(t *testing.T) {
	agentDir(t, "")
	h := StartHost(t, ws.Extension(), HostOptions{})
	if failure := runCommand(t, h, "google-account"); failure != "" {
		t.Fatal(failure)
	}
	calls := h.CallsTo("sendMessage")
	if len(calls) != 1 {
		t.Fatalf("%+v", h.Calls())
	}
	msg, _ := calls[0].Args["message"].(map[string]any)
	details, _ := msg["details"].(map[string]any)
	if msg["customType"] != "google-account" || details["cookieAccessAllowed"] != false {
		t.Fatalf("%+v", calls[0].Args)
	}
}
