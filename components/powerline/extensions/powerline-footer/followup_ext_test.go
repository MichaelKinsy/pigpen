package powerline_footer_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// Auto follow-up through the SDK adapter (followup_ext.go): the input event as the host delivers it, the run signal, and the
// host calls the handler makes. The logic itself is proven by the twins in auto_follow_up_twin_test.go.

type jevServer struct {
	mu       sync.Mutex
	requests []map[string]any
	headers  []http.Header
	release  chan struct{}
	choice   string
	conf     float64
}

func newJevServer(t *testing.T, choice string, confidence float64) *jevServer {
	t.Helper()
	s := &jevServer{choice: choice, conf: confidence}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		s.mu.Lock()
		s.requests = append(s.requests, parsed)
		s.headers = append(s.headers, r.Header.Clone())
		release := s.release
		s.mu.Unlock()
		if release != nil {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"route": map[string]any{"choice": s.choice, "confidence": s.conf}}})
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(powerline.SetJevEndpoint(srv.URL))
	return s
}

func (s *jevServer) seen() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.requests...)
}

// followUpHost starts the extension in a TUI host with autoFollowUp on (unless settings say otherwise).
type followUpHostRig struct {
	*Host
	mu      sync.Mutex
	editor  string
	branch  []any
	setText []string
}

func startFollowUp(t *testing.T, settings string) *followUpHostRig {
	t.Helper()
	home := t.TempDir()
	agent := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	os.MkdirAll(agent, 0o755)
	os.WriteFile(filepath.Join(agent, "settings.json"), []byte(settings), 0o644)
	rig := &followUpHostRig{}
	rig.Host = StartHost(t, powerline.Extension(), HostOptions{Mode: "tui", OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "sessionRead":
			if args["method"] == "getBranch" {
				rig.mu.Lock()
				defer rig.mu.Unlock()
				return append([]any{}, rig.branch...), ""
			}
			return nil, "unsupported"
		case "ui.getEditorText":
			rig.mu.Lock()
			defer rig.mu.Unlock()
			return map[string]any{"text": rig.editor}, ""
		case "ui.setEditorText":
			rig.mu.Lock()
			defer rig.mu.Unlock()
			rig.editor, _ = args["text"].(string)
			rig.setText = append(rig.setText, rig.editor)
			return map[string]any{}, ""
		case "getModelInfo", "getContextUsage", "getSessionName":
			return nil, "unavailable"
		case "getSessionID":
			return map[string]any{"sessionId": "0123456789abcdef"}, ""
		case "getThinkingLevel":
			return map[string]any{"level": "medium"}, ""
		}
		return map[string]any{}, ""
	}})
	rig.Host.Fire("session_start", map[string]any{"reason": "startup"})
	return rig
}

func (r *followUpHostRig) runSignal(aborted bool) {
	r.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "run_signal", "args": map[string]any{"run": 1, "active": true, "aborted": aborted}}})
}

func (r *followUpHostRig) setEditorTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.setText...)
}

func fireInput(h *Host, text string, extra map[string]any) json.RawMessage {
	h.t.Helper()
	data := map[string]any{"text": text, "source": "interactive", "streamingBehavior": "steer"}
	for k, v := range extra {
		data[k] = v
	}
	return h.Fire("input", data)
}

const followUpSettings = `{"powerline":{"welcome":false,"autoFollowUp":true}}`

func TestAutoFollowUpIsNoLongerRefused(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "k")
	h := startFollowUp(t, followUpSettings)
	for _, n := range notifications(h.Host) {
		if strings.Contains(n, "auto follow-up") {
			t.Errorf("auto follow-up is ported but a notification refuses it: %q", n)
		}
	}
	if !h.Registered("input") {
		t.Error("the input handler is not registered")
	}
}

func TestAutoFollowUpMovesAConfidentMessageToTheFollowUpQueue(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	h.branch = []any{map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Reading the code."}}}}}
	h.FireAll("before_agent_start", map[string]any{"prompt": "Fix the checkout bug"})
	h.runSignal(false)

	image := map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}
	got := fireInput(h.Host, "What is the weather in Lisbon?", map[string]any{"images": []any{image}})
	if string(got) != `{"action":"handled"}` {
		t.Fatalf("result = %s, want handled", got)
	}
	sends := h.CallsTo("sendUserMessage")
	if len(sends) != 1 {
		t.Fatalf("sendUserMessage calls = %d", len(sends))
	}
	wantContent := []any{map[string]any{"type": "text", "text": "What is the weather in Lisbon?"}, image}
	if !reflect.DeepEqual(sends[0].Args["content"], wantContent) || !reflect.DeepEqual(sends[0].Args["options"], map[string]any{"deliverAs": "followUp"}) {
		t.Errorf("sendUserMessage args = %#v", sends[0].Args)
	}
	reqs := jev.seen()
	state, _ := reqs[0]["state"].(map[string]any)
	if len(reqs) != 1 || state["current_task"] != "Fix the checkout bug" || state["agent_latest_text"] != "Reading the code." || state["new_message"] != "What is the weather in Lisbon?" {
		t.Errorf("Jev requests = %v", reqs)
	}
	if auth := jev.headers[0].Get("Authorization"); auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestAutoFollowUpKeepsSteeringWhenJevIsUnsure(t *testing.T) {
	newJevServer(t, "follow_up", 0.5)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	if got := fireInput(h.Host, "Actually, preserve the public API.", nil); len(got) != 0 && string(got) != "null" {
		t.Errorf("result = %s, want none", got)
	}
	if n := len(h.CallsTo("sendUserMessage")); n != 0 {
		t.Errorf("sendUserMessage calls = %d, want 0", n)
	}
}

func TestAutoFollowUpIsOffByDefault(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, `{"powerline":{"welcome":false}}`)
	fireInput(h.Host, "also lint", nil)
	if len(jev.seen()) != 0 || len(h.CallsTo("sendUserMessage")) != 0 {
		t.Errorf("a disabled setting called Jev or sent a message")
	}
}

func TestAutoFollowUpWithoutAKeyWarns(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	t.Setenv("TYPESAFE_API_KEY", "")
	h := startFollowUp(t, followUpSettings)
	fireInput(h.Host, "also lint", nil)
	got := notifications(h.Host)
	want := "warning: Auto follow-up needs TYPESAFE_API_KEY; sent as steering"
	if len(got) == 0 || got[len(got)-1] != want || len(jev.seen()) != 0 {
		t.Errorf("notifications = %q, Jev requests = %d", got, len(jev.seen()))
	}
}

func TestAutoFollowUpEscReturnsTheMessagesToTheEditor(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	jev.release = make(chan struct{})
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	h.runSignal(false)
	results := make(chan json.RawMessage, 2)
	for _, text := range []string{"also lint", "and format"} {
		go func() { results <- fireInput(h.Host, text, nil) }()
		time.Sleep(60 * time.Millisecond) // submission order
	}
	h.mu.Lock()
	h.editor = "draft"
	h.mu.Unlock()
	h.runSignal(true)
	deadline := time.Now().Add(5 * time.Second)
	for len(h.setEditorTexts()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the messages were not returned to the editor")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(jev.release)
	for i := 0; i < 2; i++ {
		if got := <-results; string(got) != `{"action":"handled"}` {
			t.Errorf("result = %s, want handled", got)
		}
	}
	if got := h.setEditorTexts(); !reflect.DeepEqual(got, []string{"also lint\n\nand format\n\ndraft"}) {
		t.Errorf("editor texts = %q", got)
	}
	if n := len(h.CallsTo("sendUserMessage")); n != 0 {
		t.Errorf("sendUserMessage calls = %d, want 0: an aborted run starts no new turn", n)
	}
}

func TestAutoFollowUpSessionSwitchDuringTheCallDropsTheMessage(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	jev.release = make(chan struct{})
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	result := make(chan json.RawMessage, 1)
	go func() { result <- fireInput(h.Host, "also lint", nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(jev.seen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no Jev request")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.FireAll("session_start", map[string]any{"reason": "new"})
	close(jev.release)
	if got := <-result; len(got) != 0 && string(got) != "null" {
		t.Errorf("result = %s, want none", got)
	}
	if n := len(h.CallsTo("sendUserMessage")); n != 0 {
		t.Errorf("sendUserMessage calls = %d, want 0", n)
	}
}

func TestAutoFollowUpShutdownDuringTheCallDropsTheMessage(t *testing.T) {
	jev := newJevServer(t, "follow_up", 0.95)
	jev.release = make(chan struct{})
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	result := make(chan json.RawMessage, 1)
	go func() { result <- fireInput(h.Host, "also lint", nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(jev.seen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no Jev request")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.FireAll("session_shutdown", map[string]any{"reason": "quit"})
	close(jev.release)
	if got := <-result; len(got) != 0 && string(got) != "null" {
		t.Errorf("result = %s, want none", got)
	}
}

func TestAutoFollowUpSessionStartClearsTheTask(t *testing.T) {
	jev := newJevServer(t, "steer", 0.9)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	h := startFollowUp(t, followUpSettings)
	h.FireAll("before_agent_start", map[string]any{"prompt": "old task"})
	h.FireAll("session_start", map[string]any{"reason": "new"})
	fireInput(h.Host, "also lint", nil)
	state, _ := jev.seen()[0]["state"].(map[string]any)
	if state["current_task"] != "Unknown" {
		t.Errorf("current_task = %v, want Unknown after a new session", state["current_task"])
	}
}
