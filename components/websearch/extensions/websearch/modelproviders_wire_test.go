package websearch_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	ws "github.com/MichaelKinsy/pigpen/websearch"
)

// The model providers through the real SDK: the registry calls and the session id travel over the
// wire to the fake host, as they do to PiG.

type recordingAPI struct {
	mu      sync.Mutex
	body    string
	headers []http.Header
	urls    []string
	bodies  []string
}

func (a *recordingAPI) Do(r *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.headers = append(a.headers, r.Header.Clone())
	a.urls = append(a.urls, r.URL.String())
	a.bodies = append(a.bodies, string(raw))
	a.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(a.body)), Request: r}, nil
}

const xaiWireBody = `{"output":[{"type":"message","content":[{"type":"output_text","text":"Answer from Grok."}]}],"citations":["https://x.ai/news"]}`

func TestXaiResolvesAuthThroughTheSDKModelRegistry(t *testing.T) {
	agentDir(t, `{}`)
	api := &recordingAPI{body: xaiWireBody}
	t.Cleanup(ws.SetHTTP(api))
	h := StartHost(t, ws.Extension(), HostOptions{OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getModel":
			if args["provider"] == "xai" && args["modelId"] == "grok-4.5" {
				return map[string]any{"provider": "xai", "id": "grok-4.5", "api": "openai-responses"}, ""
			}
			return nil, ""
		case "getModelAuth":
			return map[string]any{"ok": true, "apiKey": "grok-subscription-key", "headers": map[string]any{"X-Grok": "yes"}}, ""
		}
		return map[string]any{}, ""
	}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	raw, failure := h.Tool("web_search", map[string]any{"query": "what is new", "provider": "xai", "workflow": "none"})
	if failure != "" {
		t.Fatal(failure)
	}
	if !strings.Contains(text(t, raw), "Answer from Grok.") {
		t.Fatal(text(t, raw))
	}
	if len(api.urls) != 1 || api.urls[0] != "https://api.x.ai/v1/responses" ||
		api.headers[0].Get("Authorization") != "Bearer grok-subscription-key" || api.headers[0].Get("X-Grok") != "yes" ||
		!strings.Contains(api.bodies[0], `"model":"grok-4.5"`) {
		t.Fatalf("%v %v %s", api.urls, api.headers, api.bodies)
	}
	var asked []string
	for _, c := range h.CallsTo("getModel") {
		asked = append(asked, c.Args["modelId"].(string))
	}
	if len(asked) == 0 || asked[0] != "grok-4.5" {
		t.Fatalf("model lookups %v", asked)
	}
}

func TestOpenAISearchSendsTheSessionIDToOpenCode(t *testing.T) {
	agentDir(t, `{"openaiSearchProviders":["opencode-go"],"openaiUseProviderBaseUrl":true}`)
	api := &recordingAPI{body: `{"output":[{"type":"web_search_call"},{"type":"message","content":[{"type":"output_text","text":"OpenCode answer"}]}]}`}
	t.Cleanup(ws.SetHTTP(api))
	model := map[string]any{"provider": "opencode-go", "id": "gpt-6-luna", "api": "openai-responses", "baseUrl": "https://opencode.ai/zen/go/v1"}
	h := StartHost(t, ws.Extension(), HostOptions{OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getModelRegistryState":
			return map[string]any{"models": []any{model}, "providers": map[string]any{}}, ""
		case "getModelAuth":
			return map[string]any{"ok": true, "apiKey": "opencode-key", "headers": map[string]any{}}, ""
		case "sessionRead":
			if args["method"] == "getSessionId" {
				return "session-wire-1", ""
			}
		}
		return map[string]any{}, ""
	}})
	h.Fire("session_start", map[string]any{"reason": "startup"})
	raw, failure := h.Tool("web_search", map[string]any{"query": "attribution", "provider": "openai", "workflow": "none"})
	if failure != "" {
		t.Fatal(failure)
	}
	if !strings.Contains(text(t, raw), "OpenCode answer") {
		t.Fatal(text(t, raw))
	}
	if len(api.urls) != 1 || api.urls[0] != "https://opencode.ai/zen/go/v1/responses" ||
		api.headers[0].Get("X-Opencode-Session") != "session-wire-1" || api.headers[0].Get("X-Opencode-Client") != "pi" ||
		api.headers[0].Get("Authorization") != "Bearer opencode-key" {
		t.Fatalf("%v %v", api.urls, api.headers)
	}
}
