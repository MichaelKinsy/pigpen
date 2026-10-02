package pi_typesafe_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	pi_typesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe"
)

// rig is one fake PiG host running the extension, with a fake TypeSafe server behind its HTTP client and
// scripted answers for the dialogs the extension opens. No test here reaches the network or the real agent
// directory (rule 17).
type rig struct {
	t             *testing.T
	host          *Host
	agentDir      string
	mu            sync.Mutex
	notices       []string
	messages      []string
	confirmResult bool
	confirmations int
	confirmTitles []string
	confirmBodies []string
	editorText    string
	editorOK      bool
	inputText     string
	customResult  any // nil: cancelled; string: the typed key; "unsupported": the host has no custom components
	network       atomic.Int32
	modelList     atomic.Int32
	respond       func(r *http.Request) *http.Response
	entries       []json.RawMessage
	entryTypes    []string
	levels        []string
	customTypes   []string
	// onConfirm runs when a confirm dialog opens, before it is answered (outside the rig's lock).
	onConfirm func()
	// modelInfo answers getModelInfo (the model PiG is configured with); nil answers {} (none selected).
	modelInfo map[string]any
}

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	agent := filepath.Join(dir, "agent")
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", filepath.Join(dir, "pighome"))
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, "pi-agent"))
	t.Setenv("PIG_USE_PI_DIRS", "")
	for _, name := range []string{"TYPESAFE_API_KEY", "OPENROUTER_API_KEY", "PI_TYPESAFE_ENABLED", "PI_TYPESAFE_BACKEND",
		"PI_TYPESAFE_MAX_REQUESTS_PER_DAY", "PI_TYPESAFE_MAX_INPUT_TOKENS_PER_DAY", "PI_TYPESAFE_MAX_USD_PER_DAY", "TYPESAFE_BASE_URL", "TYPESAFE_LOG_LEVEL"} {
		t.Setenv(name, "")
	}
	return agent
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}
}

func newRig(t *testing.T, opts ...func(*pi_typesafe.Options)) *rig {
	t.Helper()
	r := &rig{t: t, agentDir: isolate(t), confirmResult: true}
	options := pi_typesafe.Options{HTTPClient: doerFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/v1/models") {
			r.modelList.Add(1)
			return jsonResponse(200, map[string]any{"models": []any{map[string]any{"name": "jev-latest", "description": "", "release_date": "2026-01-01"}}}), nil
		}
		r.network.Add(1)
		if r.respond != nil {
			return r.respond(req), nil
		}
		return jsonResponse(200, map[string]any{"model": "jev-test", "answers": map[string]any{"yes": map[string]any{"type": "noul", "noul": 0.9}}, "usage": map[string]any{"input_tokens": 12, "output_tokens": 0}}), nil
	})}
	for _, o := range opts {
		o(&options)
	}
	r.host = StartHost(t, pi_typesafe.New(options), HostOptions{OnCall: r.onCall})
	return r
}

func (r *rig) onCall(method string, args map[string]any) (map[string]any, string) {
	if method == "ui.confirm" && r.onConfirm != nil {
		r.onConfirm()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch method {
	case "getModelInfo":
		return r.modelInfo, ""
	case "ui.notify":
		r.notices = append(r.notices, args["message"].(string))
		r.levels = append(r.levels, args["level"].(string))
	case "sendMessage":
		if m, ok := args["message"].(map[string]any); ok {
			r.messages = append(r.messages, m["content"].(string))
			r.customTypes = append(r.customTypes, m["customType"].(string))
		}
	case "appendEntry":
		r.entryTypes = append(r.entryTypes, args["customType"].(string))
		data, _ := json.Marshal(args["data"])
		r.entries = append(r.entries, data)
	case "ui.confirm":
		r.confirmations++
		r.confirmTitles = append(r.confirmTitles, args["title"].(string))
		r.confirmBodies = append(r.confirmBodies, args["message"].(string))
		return map[string]any{"confirmed": r.confirmResult}, ""
	case "ui.editor":
		return map[string]any{"text": r.editorText, "ok": r.editorOK}, ""
	case "ui.input":
		if r.customResult != "unsupported" {
			return nil, "plain input must not be used when custom UI exists"
		}
		return map[string]any{"text": r.inputText, "ok": true}, ""
	case "ui.custom":
		switch v := r.customResult.(type) {
		case nil:
			return map[string]any{"ok": false}, ""
		case string:
			if v == "unsupported" {
				return nil, "custom components are unavailable"
			}
			return map[string]any{"ok": true, "result": v}, ""
		}
	}
	return nil, ""
}

func (r *rig) lastNotice() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.notices) == 0 {
		return ""
	}
	return r.notices[len(r.notices)-1]
}

func (r *rig) noticeCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.notices) }

func (r *rig) noticesSince(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.notices[n:]...)
}

func (r *rig) command(args string) {
	r.t.Helper()
	if failure := r.host.Command("typesafe", args); failure != "" {
		r.t.Fatalf("/typesafe %s failed: %s", args, failure)
	}
}

var defaultParams = map[string]any{"state": "synthetic", "questions": map[string]any{"yes": map[string]any{"type": "noul", "instructions": "Is this synthetic?"}}}

// tool runs typesafe_evaluate and returns its details (nil on failure) and the failure text.
func (r *rig) tool(params map[string]any) (map[string]any, string) {
	r.t.Helper()
	if params == nil {
		params = defaultParams
	}
	raw, failure := r.host.Tool("typesafe_evaluate", params)
	if failure != "" {
		return nil, failure
	}
	var result struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		r.t.Fatalf("tool result %s: %v", raw, err)
	}
	return result.Details, ""
}

func (r *rig) startSession(reason string) {
	r.t.Helper()
	r.host.Fire("session_start", map[string]any{"reason": reason})
}

func yes(details map[string]any) float64 {
	answers, _ := details["answers"].(map[string]any)
	answer, _ := answers["yes"].(map[string]any)
	f, _ := answer["noul"].(float64)
	return f
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
