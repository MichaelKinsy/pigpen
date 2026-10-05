package websearch_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ws "github.com/MichaelKinsy/pigpen/websearch"
)

// Wire-level tests: the extension against the fake PiG host, through the real SDK.

func agentDir(t *testing.T, config string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PIG_HOME", dir)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(dir, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	ws.ResetCaches()
	t.Cleanup(ws.ResetCaches)
	if config != "" {
		if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent", "web-search.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// runCommand drives a slash command the way the SDK expects (the template's Host.Command puts the
// name in args, which the 0.3.0 SDK does not read; a known template issue, see port/PORT.md).
func runCommand(t *testing.T, h *Host, name string) string {
	t.Helper()
	if !h.cmds[name] {
		return "command " + name + " is not registered"
	}
	_, failure := h.roundTrip(map[string]any{"method": "command", "tool": name, "args": json.RawMessage(`""`)})
	return failure
}

func text(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content any  `json:"content"`
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	if s, ok := r.Content.(string); ok {
		return s
	}
	b, _ := json.Marshal(r.Content)
	return string(b)
}

func TestExtensionRegistersEventsAndCommand(t *testing.T) {
	agentDir(t, "")
	h := StartHost(t, ws.Extension(), HostOptions{})
	for _, ev := range []string{"session_start", "session_tree", "session_shutdown", "before_agent_start"} {
		if !h.Registered(ev) {
			t.Errorf("no handler for %s", ev)
		}
	}
	// No stored results: the command only notifies.
	if errText := runCommand(t, h, "search"); errText != "" {
		t.Fatal(errText)
	}
	calls := h.CallsTo("ui.notify")
	if len(calls) != 1 || calls[0].Args["message"] != "No stored search results" {
		t.Fatalf("%+v", calls)
	}
}

func TestSearchCommandCanBeDisabled(t *testing.T) {
	agentDir(t, `{"commands":{"search":{"enabled":false}}}`)
	h := StartHost(t, ws.Extension(), HostOptions{})
	if errText := runCommand(t, h, "search"); errText == "" {
		t.Fatal("a disabled command must not be registered")
	}
}

// web_enable end to end: the tool activates every web tool through the host's tool registry and
// reads the active set back.
func TestWebEnableThroughTheHost(t *testing.T) {
	agentDir(t, "")
	all := []string{"read", "web_search", "source_check", "fetch_content", "get_search_content", "web_enable"}
	active := []string{"read", "web_enable"}
	h := StartHost(t, ws.Extension(), HostOptions{OnCall: func(method string, args map[string]any) (map[string]any, string) {
		switch method {
		case "getAllTools":
			tools := make([]map[string]any, len(all))
			for i, n := range all {
				tools[i] = map[string]any{"name": n, "description": "", "parameters": map[string]any{}, "sourceInfo": map[string]any{}}
			}
			return map[string]any{"tools": tools}, ""
		case "getActiveTools":
			return map[string]any{"tools": active}, ""
		case "setActiveTools":
			active = nil
			for _, n := range args["tools"].([]any) {
				active = append(active, n.(string))
			}
		}
		return nil, ""
	}})
	raw, failure := h.Tool("web_enable", map[string]any{})
	if failure != "" {
		t.Fatal(failure)
	}
	if !strings.Contains(text(t, raw), "web_search") || strings.Join(active, ",") != "read,web_enable,web_search,source_check,fetch_content,get_search_content" {
		t.Fatalf("%s / %v", raw, active)
	}
}

// Every registered tool runs through the SDK bridge: get_search_content needs no network.
func TestToolsRunThroughTheSDK(t *testing.T) {
	agentDir(t, `{"toolActivation":"eager"}`)
	h := StartHost(t, ws.Extension(), HostOptions{})
	raw, failure := h.Tool("get_search_content", map[string]any{"responseId": "nope"})
	if failure != "" {
		t.Fatal(failure)
	}
	if got := text(t, raw); !strings.Contains(got, "nope") {
		t.Fatalf("%s", got)
	}
	// fetch_content on a blocked address is refused by the SSRF guard, not attempted.
	raw, failure = h.Tool("fetch_content", map[string]any{"url": "http://127.0.0.1:1/"})
	if failure != "" {
		t.Fatal(failure)
	}
	if got := text(t, raw); !strings.Contains(strings.ToLower(got), "private") && !strings.Contains(strings.ToLower(got), "blocked") && !strings.Contains(strings.ToLower(got), "loopback") {
		t.Fatalf("expected an SSRF refusal: %s", got)
	}
}

// An invalid web-search.json cannot fail the load: nothing is registered and the problem is shown.
func TestInvalidConfigIsShownNotFatal(t *testing.T) {
	agentDir(t, `{"fetch":{"defaultMode":"readable","allowedModes":["raw"]}}`)
	h := StartHost(t, ws.Extension(), HostOptions{})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	calls := h.CallsTo("ui.notify")
	if len(calls) != 1 || !strings.Contains(calls[0].Args["message"].(string), "fetch.defaultMode") {
		t.Fatalf("%+v", calls)
	}
}

type fakeAPI struct{ reply string }

func (f fakeAPI) Do(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(f.reply)), Request: r}, nil
}

// web_search with includeContent starts a background fetch; when it finishes the extension stores
// the fetched content and tells the host (triggering a turn), through the real SDK.
func TestBackgroundFetchReachesTheHost(t *testing.T) {
	agentDir(t, `{"provider":"tavily"}`)
	t.Setenv("TAVILY_API_KEY", "tvly-test")
	t.Cleanup(ws.SetHTTP(fakeAPI{`{"answer":"an answer","results":[{"title":"Src","url":"https://93.184.216.34/src","content":"snippet"}]}`}))
	t.Cleanup(ws.SetPageFetch(func(ctx context.Context, u *url.URL, _ ws.RequestInit) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("page body")), Request: &http.Request{URL: u}}, nil
	}))
	h := StartHost(t, ws.Extension(), HostOptions{OnCall: func(method string, _ map[string]any) (map[string]any, string) {
		if method == "sessionRead" {
			return nil, "not available in this fake host" // arrays cannot be answered by the template host
		}
		return nil, ""
	}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	raw, failure := h.Tool("web_search", map[string]any{"query": "background", "includeContent": true, "workflow": "none"})
	if failure != "" {
		t.Fatal(failure)
	}
	if !strings.Contains(text(t, raw), "Content fetching in background") {
		t.Fatal(text(t, raw))
	}
	deadline := time.Now().Add(10 * time.Second)
	var ready []HostCall
	for time.Now().Before(deadline) {
		for _, c := range h.CallsTo("sendMessage") {
			if m, _ := c.Args["message"].(map[string]any); m["customType"] == "web-search-content-ready" {
				ready = append(ready, c)
			}
		}
		if len(ready) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(ready) != 1 {
		t.Fatalf("no content-ready message: %+v", h.Calls())
	}
	opts, _ := ready[0].Args["options"].(map[string]any)
	if opts["triggerTurn"] != true {
		t.Fatalf("content-ready must trigger a turn: %v", opts)
	}
	if len(h.CallsTo("appendEntry")) < 2 {
		t.Fatalf("search and fetch results must be appended to the session: %+v", h.CallsTo("appendEntry"))
	}
	h.Fire("session_shutdown", nil)
}
