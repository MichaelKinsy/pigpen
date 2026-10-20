package powerline_footer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of port/oracle/tests/auto-follow-up.test.ts. The original mocks globalThis.fetch; the port talks to an httptest server
// through jevClient.endpoint (the URL is hardcoded in the original, so a scenario server cannot stand in for it).

type recordedSend struct {
	content   any
	deliverAs string
}

type jevRequest struct {
	Model string `json:"model"`
	State struct {
		CurrentTask     string `json:"current_task"`
		AgentLatestText string `json:"agent_latest_text"`
		NewMessage      string `json:"new_message"`
	} `json:"state"`
}

// followUpRig is the harness of the upstream test: a handler with a scripted host, a Jev server and a run that can be aborted.
type followUpRig struct {
	t  *testing.T
	fu *autoFollowUp

	mu        sync.Mutex
	requests  []jevRequest
	rawBodies []string
	headers   []http.Header
	sent      []recordedSend
	warnings  []string
	notices   []string
	editor    string
	branch    []map[string]any
	hasUI     bool
	signal    context.Context
	abort     context.CancelFunc
	release   chan struct{} // when set, the Jev server answers only after it is closed
	active    int
	maxActive int
}

func (r *followUpRig) HasUI() bool { return r.hasUI }
func (r *followUpRig) Notify(message, level string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, level+": "+message)
	if level == "warning" {
		r.warnings = append(r.warnings, message)
	}
}
func (r *followUpRig) EditorText() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.editor, nil
}
func (r *followUpRig) SetEditorText(text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.editor = text
}
func (r *followUpRig) Branch() []map[string]any { return r.branch }
func (r *followUpRig) Signal() context.Context  { return r.signal }
func (r *followUpRig) SendUserMessage(content any, deliverAs string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, recordedSend{content, deliverAs})
	return nil
}

func (r *followUpRig) editorText() string { v, _ := r.EditorText(); return v }

func (r *followUpRig) sentMessages() []recordedSend {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedSend(nil), r.sent...)
}
func (r *followUpRig) requestsSeen() []jevRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]jevRequest(nil), r.requests...)
}
func (r *followUpRig) warningsSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

func (r *followUpRig) input(ev inputEvent) any { return r.fu.handleInput(r, true, ev) }

// pressEsc is the upstream's idle = true; run.abort().
func (r *followUpRig) pressEsc() { r.abort() }

func jevRoute(choice string, confidence float64) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"route": map[string]any{"choice": choice, "confidence": confidence}}})
	}
}

func jevStatus(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// newFollowUpRig is `harness(t, jevAnswer)`: autoFollowUp on, TYPESAFE_API_KEY=test-key, one before_agent_start.
func newFollowUpRig(t *testing.T, answer func(http.ResponseWriter)) *followUpRig {
	t.Helper()
	r := &followUpRig{t: t, hasUI: true}
	r.signal, r.abort = context.WithCancel(context.Background())
	t.Cleanup(r.abort)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var parsed jevRequest
		_ = json.Unmarshal(body, &parsed)
		r.mu.Lock()
		r.requests = append(r.requests, parsed)
		r.rawBodies = append(r.rawBodies, string(body))
		r.headers = append(r.headers, req.Header.Clone())
		release := r.release
		r.active++
		r.maxActive = max(r.maxActive, r.active)
		r.mu.Unlock()
		if release != nil {
			select {
			case <-release:
			case <-req.Context().Done():
			}
		}
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
		answer(w)
	}))
	t.Cleanup(srv.Close)
	env := map[string]string{"TYPESAFE_API_KEY": "test-key"}
	r.fu = newAutoFollowUp(jevClient{endpoint: srv.URL, timeout: jevTimeout, client: srv.Client()}, func(name string) string { return env[name] })
	r.fu.beforeAgentStart("Fix the checkout bug")
	return r
}

func handledResult() any { return map[string]any{"action": "handled"} }

func steerInput(text string) inputEvent {
	return inputEvent{Text: text, Source: "interactive", StreamingBehavior: "steer"}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func asyncInput(r *followUpRig, ev inputEvent) <-chan any {
	out := make(chan any, 1)
	go func() { out <- r.input(ev) }()
	return out
}

func result(t *testing.T, ch <-chan any) any {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("the input handler did not return")
		return nil
	}
}

func TestAutoFollowUpTwins(t *testing.T) {
	tw(t, "auto-follow-up", "a steered message Jev is confident can wait is queued as a follow-up", func(t *testing.T) {
		h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
		got := h.input(steerInput("What is the weather in Lisbon?"))
		if !reflect.DeepEqual(got, handledResult()) {
			t.Fatalf("result = %#v, want handled", got)
		}
		want := []recordedSend{{"What is the weather in Lisbon?", "followUp"}}
		if !reflect.DeepEqual(h.sentMessages(), want) {
			t.Errorf("sent = %#v, want %#v", h.sentMessages(), want)
		}
		reqs := h.requestsSeen()
		if len(reqs) == 0 || reqs[0].State.CurrentTask != "Fix the checkout bug" || reqs[0].State.NewMessage != "What is the weather in Lisbon?" {
			t.Errorf("requests = %+v", reqs)
		}
	})

	tw(t, "auto-follow-up", "corrections and unsure answers stay as steering", func(t *testing.T) {
		for _, answer := range []func(http.ResponseWriter){jevRoute("steer", 0.99), jevRoute("follow_up", 0.5)} {
			h := newFollowUpRig(t, answer)
			got := h.input(steerInput("Actually, preserve the public API."))
			if got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			if len(h.sentMessages()) != 0 {
				t.Errorf("sent = %#v, want none", h.sentMessages())
			}
		}
	})

	tw(t, "auto-follow-up", "Esc while Jev decides returns the messages to the editor in order instead of starting a new turn", func(t *testing.T) {
		for _, answer := range []func(http.ResponseWriter){jevRoute("follow_up", 0.95), jevRoute("steer", 0.99)} {
			h := newFollowUpRig(t, answer)
			h.release = make(chan struct{})
			first := asyncInput(h, steerInput("also lint"))
			waitFor(t, "the first message to be routing", func() bool { return h.fu.routingCount() == 1 })
			second := asyncInput(h, steerInput("and format"))
			waitFor(t, "both messages to be routing", func() bool { return h.fu.routingCount() == 2 })
			h.SetEditorText("draft")
			h.pressEsc()
			waitFor(t, "the editor to get the messages back", func() bool { return h.editorText() != "draft" })
			close(h.release)
			if got := result(t, first); !reflect.DeepEqual(got, handledResult()) {
				t.Errorf("first = %#v, want handled", got)
			}
			if got := result(t, second); !reflect.DeepEqual(got, handledResult()) {
				t.Errorf("second = %#v, want handled", got)
			}
			if len(h.sentMessages()) != 0 {
				t.Errorf("sent = %#v, want none", h.sentMessages())
			}
			if got, want := h.editorText(), "also lint\n\nand format\n\ndraft"; got != want {
				t.Errorf("editor = %q, want %q", got, want)
			}
		}
	})

	tw(t, "auto-follow-up", "a run that finishes on its own during the Jev call still delivers the message", func(t *testing.T) {
		h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
		h.release = make(chan struct{})
		pending := asyncInput(h, steerInput("also lint"))
		waitFor(t, "the message to be routing", func() bool { return h.fu.routingCount() == 1 })
		close(h.release) // finishRun: the agent goes idle, the signal is not aborted
		if got := result(t, pending); !reflect.DeepEqual(got, handledResult()) {
			t.Fatalf("result = %#v, want handled", got)
		}
		want := []recordedSend{{"also lint", "followUp"}}
		if !reflect.DeepEqual(h.sentMessages(), want) {
			t.Errorf("sent = %#v, want %#v", h.sentMessages(), want)
		}
		if h.editorText() != "" {
			t.Errorf("editor = %q, want empty", h.editorText())
		}
	})

	tw(t, "auto-follow-up", "Jev failures stay as steering and say so", func(t *testing.T) {
		h := newFollowUpRig(t, jevStatus(401, "bad key"))
		if got := h.input(steerInput("also lint")); got != nil {
			t.Errorf("result = %#v, want nil", got)
		}
		if len(h.sentMessages()) != 0 {
			t.Errorf("sent = %#v, want none", h.sentMessages())
		}
		warnings := h.warningsSeen()
		if len(warnings) != 1 || !regexp.MustCompile(`401.*sent as steering`).MatchString(warnings[0]) {
			t.Errorf("warnings = %q", warnings)
		}
	})

	tw(t, "auto-follow-up", "Alt+Enter follow-ups, idle prompts, slash commands, and extension messages skip Jev", func(t *testing.T) {
		h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
		for _, ev := range []inputEvent{
			{Text: "later task", Source: "interactive", StreamingBehavior: "followUp"},
			{Text: "new task", Source: "interactive"},
			{Text: "/compact", Source: "interactive", StreamingBehavior: "steer"},
			{Text: "from an extension", Source: "extension", StreamingBehavior: "steer"},
		} {
			if got := h.input(ev); got != nil {
				t.Errorf("%+v: result = %#v, want nil", ev, got)
			}
		}
		if len(h.requestsSeen()) != 0 || len(h.sentMessages()) != 0 {
			t.Errorf("requests = %v, sent = %v, want none", h.requestsSeen(), h.sentMessages())
		}
	})
}

// Everything the upstream tests leave implicit about index.ts 2013-2062 and auto-follow-up.ts.

func TestAutoFollowUpIsOffUnlessEnabled(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	if got := h.fu.handleInput(h, false, steerInput("also lint")); got != nil {
		t.Errorf("result = %#v, want nil", got)
	}
	if len(h.requestsSeen()) != 0 || len(h.warningsSeen()) != 0 {
		t.Errorf("a disabled setting must not call Jev or warn: %v %v", h.requestsSeen(), h.warningsSeen())
	}
}

func TestAutoFollowUpSlashCommandsIgnoreLeadingWhitespace(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	if got := h.input(steerInput("  \t/model")); got != nil || len(h.requestsSeen()) != 0 {
		t.Errorf("result = %#v, requests = %v", got, h.requestsSeen())
	}
	// Only a leading slash (after trimStart) skips: a slash later in the text goes to Jev.
	if got := h.input(steerInput("see /etc/hosts")); !reflect.DeepEqual(got, handledResult()) {
		t.Errorf("result = %#v, want handled", got)
	}
}

func TestAutoFollowUpWithoutAPIKeyWarnsAndSteers(t *testing.T) {
	for name, key := range map[string]string{"unset": "", "blank": " \t\n "} {
		t.Run(name, func(t *testing.T) {
			h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
			h.fu.getenv = func(string) string { return key }
			if got := h.input(steerInput("also lint")); got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			want := []string{"Auto follow-up needs TYPESAFE_API_KEY; sent as steering"}
			if !reflect.DeepEqual(h.warningsSeen(), want) {
				t.Errorf("warnings = %q, want %q", h.warningsSeen(), want)
			}
			if len(h.requestsSeen()) != 0 {
				t.Errorf("requests = %v, want none", h.requestsSeen())
			}
		})
	}
}

func TestAutoFollowUpTrimsTheAPIKey(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.fu.getenv = func(string) string { return "  test-key \n" }
	h.input(steerInput("also lint"))
	h.mu.Lock()
	defer h.mu.Unlock()
	if got := h.headers[0].Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestAutoFollowUpRequestShape(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.branch = []map[string]any{
		{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "  Running the tests now.  "}}}},
	}
	h.input(steerInput("also lint"))
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.rawBodies) != 1 {
		t.Fatalf("requests = %d", len(h.rawBodies))
	}
	want := `{"model":"jev-latest","state":{"current_task":"Fix the checkout bug","agent_latest_text":"Running the tests now.","new_message":"also lint"},` +
		`"questions":{"route":{"type":"choice","instructions":"A coding agent is busy with ` + "`current_task`" + `. Should ` + "`new_message`" + ` be delivered now as steering for that task, or queued until the task finishes?",` +
		`"criteria":{"steer":"The message changes, constrains, corrects, adds scope to, or gives information about the task the agent is doing right now, so the agent should see it before its next step. Includes messages that mix a change to the current task with a later request.",` +
		`"follow_up":"The message is a separate request or question that does not change how the current task should be done, so it can wait until the current task finishes."}}}}`
	if strings.TrimSpace(h.rawBodies[0]) != want {
		t.Errorf("body =\n%s\nwant\n%s", h.rawBodies[0], want)
	}
	if got := h.headers[0].Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := h.headers[0].Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestAutoFollowUpStateDefaults(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.fu.sessionStart() // lastUserPrompt = ""
	h.input(steerInput("also lint"))
	reqs := h.requestsSeen()
	if reqs[0].State.CurrentTask != "Unknown" || reqs[0].State.AgentLatestText != "None yet" {
		t.Errorf("state = %+v, want the Unknown and None yet defaults", reqs[0].State)
	}
}

func TestAutoFollowUpDeliversImagesWithTheText(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	image := map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}
	ev := steerInput("look at this")
	ev.Images = []any{image}
	if got := h.input(ev); !reflect.DeepEqual(got, handledResult()) {
		t.Fatalf("result = %#v", got)
	}
	want := []recordedSend{{[]any{map[string]any{"type": "text", "text": "look at this"}, image}, "followUp"}}
	if !reflect.DeepEqual(h.sentMessages(), want) {
		t.Errorf("sent = %#v, want %#v", h.sentMessages(), want)
	}
}

func TestAutoFollowUpEmptyImageListSendsPlainText(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	ev := steerInput("plain")
	ev.Images = []any{}
	h.input(ev)
	want := []recordedSend{{"plain", "followUp"}}
	if !reflect.DeepEqual(h.sentMessages(), want) {
		t.Errorf("sent = %#v, want %#v", h.sentMessages(), want)
	}
}

func TestAutoFollowUpConfidenceThreshold(t *testing.T) {
	for _, c := range []struct {
		confidence float64
		follow     bool
	}{{0.8, true}, {0.79, false}, {1, true}, {0, false}} {
		h := newFollowUpRig(t, jevRoute("follow_up", c.confidence))
		got := h.input(steerInput("also lint"))
		if (got != nil) != c.follow {
			t.Errorf("confidence %v: result = %#v, want follow-up %v", c.confidence, got, c.follow)
		}
	}
}

func TestAutoFollowUpRejectsAnswersOfTheWrongShape(t *testing.T) {
	for name, body := range map[string]string{
		"unknown choice":      `{"answers":{"route":{"choice":"later","confidence":0.99}}}`,
		"confidence a string": `{"answers":{"route":{"choice":"follow_up","confidence":"0.99"}}}`,
		"no confidence":       `{"answers":{"route":{"choice":"follow_up"}}}`,
		"no route":            `{"answers":{}}`,
		"no answers":          `{}`,
		"route a string":      `{"answers":{"route":"follow_up"}}`,
		"answers a number":    `{"answers":3}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newFollowUpRig(t, jevStatus(200, body))
			if got := h.input(steerInput("also lint")); got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			want := []string{"Auto follow-up unavailable (Jev returned an unexpected route answer); sent as steering"}
			if !reflect.DeepEqual(h.warningsSeen(), want) {
				t.Errorf("warnings = %q, want %q", h.warningsSeen(), want)
			}
			if len(h.sentMessages()) != 0 {
				t.Errorf("sent = %v", h.sentMessages())
			}
		})
	}
}

func TestAutoFollowUpErrorTextOfAFailedCall(t *testing.T) {
	long := strings.Repeat("x", 250)
	h := newFollowUpRig(t, jevStatus(503, long))
	h.input(steerInput("also lint"))
	want := []string{"Auto follow-up unavailable (Jev returned 503: " + long[:200] + "); sent as steering"}
	if !reflect.DeepEqual(h.warningsSeen(), want) {
		t.Errorf("warnings = %q, want %q", h.warningsSeen(), want)
	}
}

func TestAutoFollowUpErrorTextCutsInUTF16Units(t *testing.T) {
	// 199 ASCII units then a pair of astral characters: slice(0, 200) keeps one surrogate of the first, which a Go string cannot hold.
	body := strings.Repeat("a", 199) + "😀😀"
	h := newFollowUpRig(t, jevStatus(500, body))
	h.input(steerInput("also lint"))
	warnings := h.warningsSeen()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Jev returned 500: "+strings.Repeat("a", 199)+"\ufffd); sent") {
		t.Errorf("warnings = %q", warnings)
	}
}

func TestAutoFollowUpServerFailuresAreWarnings(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
		h.fu.jev.endpoint = "http://127.0.0.1:1/v1/systemone"
		h.input(steerInput("also lint"))
		want := []string{"Auto follow-up unavailable (fetch failed); sent as steering"}
		if !reflect.DeepEqual(h.warningsSeen(), want) {
			t.Errorf("warnings = %q, want %q", h.warningsSeen(), want)
		}
	})
	t.Run("a body that is not JSON", func(t *testing.T) {
		h := newFollowUpRig(t, jevStatus(200, "not json"))
		h.input(steerInput("also lint"))
		warnings := h.warningsSeen()
		if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Auto follow-up unavailable (") || !strings.HasSuffix(warnings[0], "); sent as steering") {
			t.Errorf("warnings = %q", warnings)
		}
	})
	t.Run("a null body", func(t *testing.T) {
		h := newFollowUpRig(t, jevStatus(200, "null"))
		h.input(steerInput("also lint"))
		want := []string{"Auto follow-up unavailable (Cannot read properties of null (reading 'answers')); sent as steering"}
		if !reflect.DeepEqual(h.warningsSeen(), want) {
			t.Errorf("warnings = %q, want %q", h.warningsSeen(), want)
		}
	})
}

func TestJevCallsAreBoundByTheTimeout(t *testing.T) {
	if jevTimeout != 1500*time.Millisecond || minFollowUpConfidence != 0.8 || jevDefaultEndpoint != "https://api.typesafe.ai/v1/systemone" {
		t.Errorf("constants: %v %v %q", jevTimeout, minFollowUpConfidence, jevDefaultEndpoint)
	}
	if jevEndpoint != jevDefaultEndpoint {
		t.Errorf("the production endpoint is %q", jevEndpoint)
	}
	if got := newJevClient(); got.endpoint != jevDefaultEndpoint || got.timeout != jevTimeout {
		t.Errorf("newJevClient() = %+v", got)
	}
	slow := func(delay time.Duration) func(w http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			time.Sleep(delay)
			jevRoute("follow_up", 0.95)(w)
		}
	}
	t.Run("a call inside the timeout is used", func(t *testing.T) {
		h := newFollowUpRig(t, slow(900*time.Millisecond))
		h.fu.jev.timeout = jevTimeout
		if got := h.input(steerInput("also lint")); !reflect.DeepEqual(got, handledResult()) {
			t.Errorf("result = %#v, warnings = %q", got, h.warningsSeen())
		}
	})
	t.Run("a slower call stays as steering and says it timed out", func(t *testing.T) {
		h := newFollowUpRig(t, slow(1800*time.Millisecond))
		h.fu.jev.timeout = jevTimeout
		started := time.Now()
		got := h.input(steerInput("also lint"))
		if elapsed := time.Since(started); elapsed > 1700*time.Millisecond {
			t.Errorf("the call took %v, the timeout is 1.5 s", elapsed)
		}
		want := []string{"Auto follow-up unavailable (The operation was aborted due to timeout); sent as steering"}
		if got != nil || !reflect.DeepEqual(h.warningsSeen(), want) {
			t.Errorf("result = %#v, warnings = %q", got, h.warningsSeen())
		}
	})
}

func TestAutoFollowUpRoutesOneMessageAtATime(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.release = make(chan struct{})
	first := asyncInput(h, steerInput("one"))
	waitFor(t, "the first message", func() bool { return h.fu.routingCount() == 1 })
	second := asyncInput(h, steerInput("two"))
	waitFor(t, "the second message", func() bool { return h.fu.routingCount() == 2 })
	third := asyncInput(h, steerInput("three"))
	waitFor(t, "all three to be routing", func() bool { return h.fu.routingCount() == 3 })
	time.Sleep(50 * time.Millisecond)
	h.mu.Lock()
	seen := len(h.requests)
	h.mu.Unlock()
	if seen != 1 {
		t.Errorf("%d requests while the first is undecided, want 1", seen)
	}
	close(h.release)
	result(t, first)
	result(t, second)
	result(t, third)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxActive != 1 {
		t.Errorf("Jev saw %d concurrent calls, want 1", h.maxActive)
	}
	var texts []string
	for _, r := range h.requests {
		texts = append(texts, r.State.NewMessage)
	}
	if len(texts) != 3 {
		t.Errorf("requests = %v", texts)
	}
}

func TestAutoFollowUpAFailedCallDoesNotBlockTheNext(t *testing.T) {
	failing := true
	var mu sync.Mutex
	h := newFollowUpRig(t, func(w http.ResponseWriter) {
		mu.Lock()
		fail := failing
		failing = false
		mu.Unlock()
		if fail {
			jevStatus(500, "boom")(w)
			return
		}
		jevRoute("follow_up", 0.95)(w)
	})
	first := h.input(steerInput("one"))
	second := h.input(steerInput("two"))
	if first != nil || !reflect.DeepEqual(second, handledResult()) {
		t.Errorf("first = %#v, second = %#v", first, second)
	}
}

func TestAutoFollowUpSessionSwitchDuringTheCallDropsTheMessage(t *testing.T) {
	for name, change := range map[string]func(*autoFollowUp){
		"session_start":    (*autoFollowUp).sessionStart,
		"session_shutdown": (*autoFollowUp).sessionShutdown,
	} {
		t.Run(name, func(t *testing.T) {
			h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
			h.release = make(chan struct{})
			pending := asyncInput(h, steerInput("also lint"))
			waitFor(t, "the message to be routing", func() bool { return h.fu.routingCount() == 1 })
			change(h.fu)
			close(h.release)
			if got := result(t, pending); got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			if len(h.sentMessages()) != 0 || len(h.warningsSeen()) != 0 {
				t.Errorf("sent = %v, warnings = %q: a message of the old session must not reach the new one", h.sentMessages(), h.warningsSeen())
			}
		})
	}
}

func TestAutoFollowUpSessionSwitchSilencesAFailureToo(t *testing.T) {
	h := newFollowUpRig(t, jevStatus(500, "boom"))
	h.release = make(chan struct{})
	pending := asyncInput(h, steerInput("also lint"))
	waitFor(t, "the message to be routing", func() bool { return h.fu.routingCount() == 1 })
	h.fu.sessionStart()
	close(h.release)
	if got := result(t, pending); got != nil || len(h.warningsSeen()) != 0 {
		t.Errorf("result = %#v, warnings = %q", got, h.warningsSeen())
	}
}

func TestAutoFollowUpAbortAfterASessionSwitchRestoresNothing(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.release = make(chan struct{})
	pending := asyncInput(h, steerInput("also lint"))
	waitFor(t, "the message to be routing", func() bool { return h.fu.routingCount() == 1 })
	h.SetEditorText("draft")
	h.fu.sessionShutdown()
	h.pressEsc()
	time.Sleep(50 * time.Millisecond)
	close(h.release)
	result(t, pending)
	if h.editorText() != "draft" {
		t.Errorf("editor = %q: a message of the old session was put into the new session's editor", h.editorText())
	}
}

func TestAutoFollowUpAbortAfterTheDecisionRestoresNothing(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	got := h.input(steerInput("also lint"))
	h.SetEditorText("draft")
	h.pressEsc()
	time.Sleep(30 * time.Millisecond)
	if !reflect.DeepEqual(got, handledResult()) || h.editorText() != "draft" || h.fu.routingCount() != 0 {
		t.Errorf("result = %#v, editor = %q, routing = %d", got, h.editorText(), h.fu.routingCount())
	}
}

func TestAutoFollowUpRestoreDropsBlankPartsAndAnEmptyDraft(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.release = make(chan struct{})
	first := asyncInput(h, steerInput("  "))
	waitFor(t, "the first message", func() bool { return h.fu.routingCount() == 1 })
	second := asyncInput(h, steerInput("kept"))
	waitFor(t, "both messages to be routing", func() bool { return h.fu.routingCount() == 2 })
	h.pressEsc()
	waitFor(t, "the editor to be set", func() bool { return h.editorText() != "" })
	close(h.release)
	result(t, first)
	result(t, second)
	if got := h.editorText(); got != "kept" {
		t.Errorf("editor = %q, want %q", got, "kept")
	}
}

func TestAutoFollowUpWithoutARunSignalNeverRestores(t *testing.T) {
	for name, tweak := range map[string]func(*followUpRig){
		"no UI":     func(r *followUpRig) { r.hasUI = false },
		"no signal": func(r *followUpRig) { r.signal = nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
			tweak(h)
			h.release = make(chan struct{})
			pending := asyncInput(h, steerInput("also lint"))
			waitFor(t, "the request", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.requests) == 1 })
			if h.fu.routingCount() != 0 {
				t.Errorf("routing = %d, want none without a signal", h.fu.routingCount())
			}
			h.SetEditorText("draft")
			h.pressEsc()
			close(h.release)
			if got := result(t, pending); !reflect.DeepEqual(got, handledResult()) {
				t.Errorf("result = %#v, want handled", got)
			}
			if h.editorText() != "draft" || len(h.sentMessages()) != 1 {
				t.Errorf("editor = %q, sent = %v", h.editorText(), h.sentMessages())
			}
		})
	}
}

func TestAutoFollowUpOnlyTheAbortedRunIsRestored(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("follow_up", 0.95))
	h.release = make(chan struct{})
	first := asyncInput(h, steerInput("old run"))
	waitFor(t, "the first message", func() bool { return h.fu.routingCount() == 1 })
	otherSignal, otherAbort := context.WithCancel(context.Background())
	defer otherAbort()
	h.mu.Lock()
	h.signal = otherSignal
	h.mu.Unlock()
	second := asyncInput(h, steerInput("new run"))
	waitFor(t, "both messages", func() bool { return h.fu.routingCount() == 2 })
	h.SetEditorText("draft")
	h.pressEsc() // aborts the first run only
	waitFor(t, "the first message to be restored", func() bool { return h.editorText() != "draft" })
	if got := h.editorText(); got != "old run\n\ndraft" {
		t.Errorf("editor = %q", got)
	}
	close(h.release)
	if got := result(t, first); !reflect.DeepEqual(got, handledResult()) {
		t.Errorf("first = %#v", got)
	}
	if got := result(t, second); !reflect.DeepEqual(got, handledResult()) {
		t.Errorf("second = %#v, want the follow-up of the live run", got)
	}
	want := []recordedSend{{"new run", "followUp"}}
	if !reflect.DeepEqual(h.sentMessages(), want) {
		t.Errorf("sent = %#v, want %#v", h.sentMessages(), want)
	}
}

func TestAutoFollowUpBeforeAgentStartSetsTheTask(t *testing.T) {
	h := newFollowUpRig(t, jevRoute("steer", 0.9))
	h.fu.beforeAgentStart("second prompt")
	h.input(steerInput("also lint"))
	if got := h.requestsSeen()[0].State.CurrentTask; got != "second prompt" {
		t.Errorf("current_task = %q", got)
	}
}

func TestRecentAgentContext(t *testing.T) {
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	assistant := func(blocks ...any) map[string]any {
		return map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": blocks}}
	}
	user := map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []any{text("user text")}}}
	long := strings.Repeat("y", 250)
	for name, c := range map[string]struct {
		branch []map[string]any
		want   string
	}{
		"empty":                            {nil, ""},
		"the latest assistant text":        {[]map[string]any{assistant(text("old")), user, assistant(text("  new  "))}, "new"},
		"thinking blocks skipped":          {[]map[string]any{assistant(map[string]any{"type": "thinking", "thinking": "hmm"}, text("answer"))}, "answer"},
		"blank text skipped":               {[]map[string]any{assistant(text("   "), text("real"))}, "real"},
		"empty text skipped":               {[]map[string]any{assistant(text(""), text("real"))}, "real"},
		"falls back to an earlier message": {[]map[string]any{assistant(text("earlier")), assistant(map[string]any{"type": "toolCall", "name": "bash"})}, "earlier"},
		"user messages ignored":            {[]map[string]any{user}, ""},
		"not a message entry":              {[]map[string]any{{"type": "compaction", "message": map[string]any{"role": "assistant", "content": []any{text("x")}}}}, ""},
		"string content ignored":           {[]map[string]any{{"type": "message", "message": map[string]any{"role": "assistant", "content": "plain"}}}, ""},
		"cut at 200 units":                 {[]map[string]any{assistant(text(long))}, long[:200]},
		"cut inside a pair":                {[]map[string]any{assistant(text(strings.Repeat("a", 199) + "😀"))}, strings.Repeat("a", 199) + "\ufffd"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := recentAgentContext(c.branch); got != c.want {
				t.Errorf("recentAgentContext = %q, want %q", got, c.want)
			}
		})
	}
}
