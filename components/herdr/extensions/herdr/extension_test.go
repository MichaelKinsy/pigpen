package herdr_test

// Layer-1 cases beyond the original's seven: the request line a socket carries, the 0.5 s then 1.5 s retry, the
// dedupe and queue rules of publishState and drainStateQueue, the herdr:blocked counter with JavaScript's truthiness
// and strict equality, the session lifecycle, release, and the gate of the PiG-only prompt-based blocked state.
// Each case cites the line of extensions/herdr-agent-state.ts it holds (port/oracle).

import (
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	herdr "github.com/MichaelKinsy/pigpen/components/herdr/extensions/herdr"
)

func TestMain(m *testing.M) {
	// The attempt bounds are the original's 0.5 s and 1.5 s in production. A test on a loaded host must not lose a
	// request to them; the tests about the bounds set their own.
	*herdr.FirstAttempt, *herdr.RetryAttempt = 5*time.Second, 5*time.Second
	os.Exit(m.Run())
}

// shortAttempts gives a test about a hung or failing herdr short bounds.
func shortAttempts(t *testing.T, first, retry time.Duration) {
	t.Helper()
	pf, pr := *herdr.FirstAttempt, *herdr.RetryAttempt
	*herdr.FirstAttempt, *herdr.RetryAttempt = first, retry
	t.Cleanup(func() { *herdr.FirstAttempt, *herdr.RetryAttempt = pf, pr })
}

func startSession(t *testing.T) (*fakeHost, *fakeHerdr) {
	t.Helper()
	host, h := startTUI(t)
	host.fire("session_start", map[string]any{"reason": "startup"})
	h.waitFor(1)
	return host, h
}

func param(t *testing.T, r herdrRequest, key string) any {
	t.Helper()
	return r.Params[key]
}

func hasParam(r herdrRequest, key string) bool {
	_, ok := r.Params[key]
	return ok
}

func TestRequestShape(t *testing.T) {
	// herdr-agent-state.ts:120-143: params in the order pane_id, source, agent, state, message, seq, resume_argv; the
	// request is {id, method, params} on one line.
	t.Run("a state report is one JSON line in the original's key order", func(t *testing.T) {
		host, h := startSession(t)
		got := h.requests()[0].Raw
		want := regexp.MustCompile(`^\{"id":"pig:state:\d+:[0-9a-z]+","method":"pane\.report_agent","params":\{"pane_id":"w1:p1","source":"pig","agent":"pig","state":"idle","seq":\d+,"resume_argv":\["pig","--session","/home/user/\.pig/agent/sessions/one\.jsonl"\]\}\}$`)
		if !want.MatchString(got) {
			t.Fatalf("request = %s", got)
		}
		_ = host
	})

	t.Run("a release is {pane_id, source, agent, seq} with a release id and no resume command", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		got := h.waitFor(2)[1].Raw
		want := regexp.MustCompile(`^\{"id":"pig:release:\d+:[0-9a-z]+","method":"pane\.release_agent","params":\{"pane_id":"w1:p1","source":"pig","agent":"pig","seq":\d+\}\}$`)
		if !want.MatchString(got) {
			t.Fatalf("release = %s", got)
		}
	})

	t.Run("every request has its own id", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		all := h.waitFor(2)
		if all[0].ID == all[1].ID || all[0].ID == "" {
			t.Fatalf("ids %q and %q", all[0].ID, all[1].ID)
		}
	})

	// JSON.stringify drops an undefined message and keeps null, a number and "" (herdr-agent-state.ts:123-127, 232-233).
	for _, tc := range []struct {
		name    string
		payload map[string]any
		want    string // the message member as it appears in the request, "" when absent
	}{
		{"a string label", map[string]any{"active": true, "label": "approve"}, `"message":"approve",`},
		{"an empty label", map[string]any{"active": true, "label": ""}, `"message":"",`},
		{"a null label", map[string]any{"active": true, "label": nil}, `"message":null,`},
		{"a number label", map[string]any{"active": true, "label": 7}, `"message":7,`},
		{"a missing label", map[string]any{"active": true}, ``},
		{"a label with markup", map[string]any{"active": true, "label": "a<b>&c\nd"}, `"message":"a<b>&c\nd",`},
	} {
		t.Run("blocked with "+tc.name, func(t *testing.T) {
			host, h := startSession(t)
			host.emit("herdr:blocked", tc.payload)
			raw := h.waitFor(2)[1].Raw
			if !strings.Contains(raw, `"state":"blocked",`) {
				t.Fatalf("request = %s", raw)
			}
			if tc.want == "" {
				if strings.Contains(raw, `"message"`) {
					t.Fatalf("message present: %s", raw)
				}
			} else if !strings.Contains(raw, tc.want) {
				t.Fatalf("want %s in %s", tc.want, raw)
			}
		})
	}
}

func TestTransport(t *testing.T) {
	t.Run("production keeps the original's 0.5 s then 1.5 s attempt bounds", func(t *testing.T) {
		if herdr.OriginalFirstAttempt != 500*time.Millisecond || herdr.OriginalRetryAttempt != 1500*time.Millisecond {
			t.Fatalf("bounds %v and %v, want 500ms and 1.5s", herdr.OriginalFirstAttempt, herdr.OriginalRetryAttempt)
		}
	})

	t.Run("a delivered request is sent once", func(t *testing.T) {
		_, h := startSession(t)
		settle()
		if n := h.connections(); n != 1 {
			t.Fatalf("%d connections for one request", n)
		}
	})

	t.Run("a connection that ends without an answer is retried once with the same request", func(t *testing.T) {
		host, h := startTUI(t)
		h.behave = func(n int) behavior { return behavior{Close: n == 0} }
		host.fire("session_start", nil)
		all := h.waitFor(2)
		settle()
		if all[0].Raw != all[1].Raw {
			t.Fatalf("retry differs from the first attempt:\n%s\n%s", all[0].Raw, all[1].Raw)
		}
		if n := h.connections(); n != 2 {
			t.Fatalf("%d connections, want 2", n)
		}
	})

	t.Run("an attempt that gets no answer in time is retried", func(t *testing.T) {
		shortAttempts(t, 100*time.Millisecond, 2*time.Second)
		host, h := startTUI(t)
		h.behave = func(n int) behavior { return behavior{Silent: n == 0} }
		begin := time.Now()
		host.fire("session_start", nil)
		h.waitFor(2)
		if took := time.Since(begin); took < 90*time.Millisecond {
			t.Fatalf("retried after %v, before the first attempt's bound", took)
		}
	})

	t.Run("a late answer still counts when it comes within the bound", func(t *testing.T) {
		shortAttempts(t, 2*time.Second, 2*time.Second)
		host, h := startTUI(t)
		h.behave = func(int) behavior { return behavior{Delay: 150 * time.Millisecond} }
		host.fire("session_start", nil)
		h.waitFor(1)
		time.Sleep(400 * time.Millisecond)
		if n := h.connections(); n != 1 {
			t.Fatalf("%d connections, want 1", n)
		}
	})

	t.Run("a herdr that never answers gets two attempts and no third, and quit still releases", func(t *testing.T) {
		shortAttempts(t, 80*time.Millisecond, 120*time.Millisecond)
		host, h := startTUI(t)
		h.behave = func(int) behavior { return behavior{Silent: true} }
		host.fire("session_start", nil)
		time.Sleep(500 * time.Millisecond)
		if n := h.connections(); n != 2 {
			t.Fatalf("%d connections for one report, want 2", n)
		}
		begin := time.Now()
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		if took := time.Since(begin); took > 2*time.Second {
			t.Fatalf("quit took %v behind a hung herdr", took)
		}
		all := h.requests()
		if last := all[len(all)-1]; last.Method != "pane.release_agent" {
			t.Fatalf("last request = %s", last.Raw)
		}
	})

	t.Run("survives a herdr that is not listening", func(t *testing.T) {
		shortAttempts(t, 200*time.Millisecond, 200*time.Millisecond)
		host, h := startTUI(t)
		_ = h.ln.Close()
		host.fire("session_start", nil)
		host.fire("agent_start", nil)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
	})

	t.Run("socket endpoint: the path as is, a Windows pipe name under \\\\.\\pipe\\", func(t *testing.T) {
		if got := herdr.Endpoint("linux", "/run/herdr.sock"); got != "/run/herdr.sock" {
			t.Fatalf("linux endpoint %q", got)
		}
		if got := herdr.Endpoint("darwin", "/run/herdr.sock"); got != "/run/herdr.sock" {
			t.Fatalf("darwin endpoint %q", got)
		}
		if got := herdr.Endpoint("windows", "herdr-1234.sock"); got != `\\.\pipe\herdr-1234.sock` {
			t.Fatalf("windows endpoint %q", got)
		}
	})
}

func TestDiagnostic(t *testing.T) {
	captureStderr := func(t *testing.T) func() string {
		t.Helper()
		orig := os.Stderr
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = w
		t.Cleanup(func() { os.Stderr = orig })
		return func() string {
			settle()
			os.Stderr = orig
			_ = w.Close()
			b, _ := io.ReadAll(r)
			return string(b)
		}
	}

	t.Run("a request herdr never took is written once to stderr, and not again until one is delivered", func(t *testing.T) {
		shortAttempts(t, 60*time.Millisecond, 60*time.Millisecond)
		host, h := startTUI(t)
		h.behave = func(int) behavior { return behavior{Close: true} }
		read := captureStderr(t)
		host.fire("session_start", nil)
		host.fire("agent_start", nil)
		host.fire("agent_settled", nil)
		time.Sleep(800 * time.Millisecond)
		got := read()
		if n := strings.Count(got, "herdr: pane.report_agent failed"); n != 1 {
			t.Fatalf("diagnostic written %d times: %q", n, got)
		}
	})

	t.Run("a healthy herdr is silent", func(t *testing.T) {
		host, h := startTUI(t)
		read := captureStderr(t)
		host.fire("session_start", nil)
		h.waitFor(1)
		if got := read(); got != "" {
			t.Fatalf("stderr = %q", got)
		}
	})
}

func TestStateReports(t *testing.T) {
	// publishState, herdr-agent-state.ts:200-215.
	t.Run("does not repeat an unchanged state", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_settled", nil)
		host.fire("agent_settled", nil)
		settle()
		equalStrings(t, states(h.requests()), []string{"idle"})
	})

	t.Run("does not report working twice for two agent_start events", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.fire("agent_start", nil)
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "working"})
	})

	t.Run("reports an unchanged state again when the resume command changed", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.setState(func(s *hostState) { s.sessionFile = "/home/user/.pig/agent/sessions/two.jsonl" })
		host.fire("agent_start", nil)
		all := h.waitFor(3)
		equalStrings(t, states(all), []string{"idle", "working", "working"})
		resume, _ := all[2].Params["resume_argv"].([]any)
		if len(resume) != 3 || resume[2] != "/home/user/.pig/agent/sessions/two.jsonl" {
			t.Fatalf("resume_argv = %v", resume)
		}
	})

	t.Run("session_start reports even when nothing changed", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_start", nil)
		equalStrings(t, states(h.waitFor(2)), []string{"idle", "idle"})
	})

	t.Run("does not report idle while the agent is still running", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.setState(func(s *hostState) { s.idle = false })
		host.fire("agent_settled", nil)
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "working"})
	})

	t.Run("a failed idle check at agent_settled reports nothing and the handler fails, as an isIdle that throws does", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.setState(func(s *hostState) { s.idleFailure = true })
		if _, failed := host.fireResult("agent_settled", nil); !failed {
			t.Fatal("the handler did not fail")
		}
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "working"})
	})

	t.Run("a failed idle check at session_start fails the handler but the session still reports from the next turn", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.idleFailure = true })
		if _, failed := host.fireResult("session_start", nil); !failed {
			t.Fatal("the handler did not fail")
		}
		settle()
		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests from a failed session_start", n)
		}
		host.fire("agent_start", nil)
		equalStrings(t, states(h.waitFor(1)), []string{"working"})
	})

	t.Run("starts as working when a reload lands mid-turn", func(t *testing.T) {
		host, h := startTUI(t)
		host.setState(func(s *hostState) { s.idle = false })
		host.fire("session_start", map[string]any{"reason": "reload"})
		equalStrings(t, states(h.waitFor(1)), []string{"working"})
	})

	t.Run("seq is a safe integer seeded from the clock in microseconds and increases", func(t *testing.T) {
		before := time.Now().UnixMicro()
		host, h := startSession(t)
		host.fire("agent_start", nil)
		order := seqs(t, h.waitFor(2))
		if order[0] < float64(before) || order[0] > 1<<53-1 {
			t.Fatalf("seq %v is not a safe integer at or above the clock %d", order[0], before)
		}
		if order[1] <= order[0] {
			t.Fatalf("seq not increasing: %v", order)
		}
	})

	t.Run("keeps seq increasing across extension instances in one process", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_shutdown", map[string]any{"reason": "new"})
		second := startHost(t, herdr.Extension(), "tui")
		second.fire("session_start", map[string]any{"reason": "new"})
		order := seqs(t, h.waitFor(2))
		if order[1] <= order[0] {
			t.Fatalf("seq did not increase across instances: %v", order)
		}
	})

	// queueState and drainStateQueue, herdr-agent-state.ts:145-174: calls never overlap and only the newest state is kept.
	t.Run("sends only the newest state when requests queue behind a slow herdr, still in order", func(t *testing.T) {
		host, h := startTUI(t)
		h.behave = func(int) behavior { return behavior{Delay: 300 * time.Millisecond} }
		host.fire("session_start", nil)                                          // in flight
		host.fire("agent_start", nil)                                            // queued, then replaced
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "A"}) // queued, then replaced
		host.emit("herdr:blocked", map[string]any{"active": false})
		host.fire("agent_settled", nil) // the newest state: idle
		h.waitFor(2)
		settle()
		final := h.requests()
		equalStrings(t, states(final), []string{"idle", "idle"})
		order := seqs(t, final)
		if order[1] <= order[0] {
			t.Fatalf("seq not increasing: %v", order)
		}
	})

	t.Run("a queued state carries the resume command current when it is sent", func(t *testing.T) {
		host, h := startTUI(t)
		h.behave = func(int) behavior { return behavior{Delay: 300 * time.Millisecond} }
		host.fire("session_start", nil) // in flight, with one.jsonl
		host.setState(func(s *hostState) { s.sessionFile = "/home/user/.pig/agent/sessions/two.jsonl" })
		host.fire("agent_start", nil) // queued; the session identity changes before it is sent
		all := h.waitFor(2)
		resume, _ := all[1].Params["resume_argv"].([]any)
		if len(resume) != 3 || resume[2] != "/home/user/.pig/agent/sessions/two.jsonl" {
			t.Fatalf("resume_argv = %v", resume)
		}
	})
}

func TestBlocked(t *testing.T) {
	// herdr-agent-state.ts:217-235: a counter, the last label, and `!data?.active` (JavaScript truthiness).
	t.Run("stays blocked until every holder let go, and keeps the latest label", func(t *testing.T) {
		host, h := startSession(t)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "A"})
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "B"})
		host.emit("herdr:blocked", map[string]any{"active": false})
		settle()
		all := h.requests()
		equalStrings(t, states(all), []string{"idle", "blocked", "blocked"})
		if param(t, all[2], "message") != "B" {
			t.Fatalf("message = %v", param(t, all[2], "message"))
		}
		host.emit("herdr:blocked", map[string]any{"active": false})
		all = h.waitFor(4)
		equalStrings(t, states(all), []string{"idle", "blocked", "blocked", "idle"})
		if hasParam(all[3], "message") {
			t.Fatalf("message after the last release: %s", all[3].Raw)
		}
	})

	t.Run("a release with nothing held keeps the counter at zero", func(t *testing.T) {
		host, h := startSession(t)
		host.emit("herdr:blocked", map[string]any{"active": false})
		host.emit("herdr:blocked", map[string]any{"active": false})
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "A"})
		all := h.waitFor(2)
		equalStrings(t, states(all), []string{"idle", "blocked"})
		host.emit("herdr:blocked", map[string]any{"active": false})
		equalStrings(t, states(h.waitFor(3)), []string{"idle", "blocked", "idle"})
	})

	t.Run("returns to working, not idle, while a turn runs", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "A"})
		host.emit("herdr:blocked", map[string]any{"active": false})
		equalStrings(t, states(h.waitFor(4)), []string{"idle", "working", "blocked", "working"})
	})

	// `!data?.active`: falsy releases, truthy blocks.
	for _, tc := range []struct {
		name    string
		payload any
		blocks  bool
	}{
		{"active true", map[string]any{"active": true}, true},
		{"active 1", map[string]any{"active": 1}, true},
		{"active a non-empty string", map[string]any{"active": "yes"}, true},
		{"active an object", map[string]any{"active": map[string]any{}}, true},
		{"active 0", map[string]any{"active": 0}, false},
		{"active an empty string", map[string]any{"active": ""}, false},
		{"active null", map[string]any{"active": nil}, false},
		{"active false", map[string]any{"active": false}, false},
		{"no active member", map[string]any{"label": "x"}, false},
		{"a string payload", "active", false},
		{"a null payload", nil, false},
		{"an array payload", []any{true}, false},
	} {
		t.Run("truthiness: "+tc.name, func(t *testing.T) {
			host, h := startSession(t)
			host.emit("herdr:blocked", tc.payload)
			if tc.blocks {
				equalStrings(t, states(h.waitFor(2)), []string{"idle", "blocked"})
				return
			}
			settle()
			equalStrings(t, states(h.requests()), []string{"idle"})
		})
	}

	t.Run("equal string labels are not reported twice, a label object is a new value every time", func(t *testing.T) {
		host, h := startSession(t)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "same"})
		h.waitFor(2)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "same"})
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "blocked"})
		// JavaScript compares objects by reference: two label objects are never ===.
		host.emit("herdr:blocked", map[string]any{"active": true, "label": map[string]any{"k": 1}})
		h.waitFor(3)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": map[string]any{"k": 1}})
		equalStrings(t, states(h.waitFor(4)), []string{"idle", "blocked", "blocked", "blocked"})
	})

	t.Run("a null label then a missing label are two different messages", func(t *testing.T) {
		host, h := startSession(t)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": nil})
		h.waitFor(2)
		host.emit("herdr:blocked", map[string]any{"active": true})
		all := h.waitFor(3)
		if hasParam(all[2], "message") || !hasParam(all[1], "message") {
			t.Fatalf("requests %s then %s", all[1].Raw, all[2].Raw)
		}
	})

	t.Run("ignores the signal before a TUI session started and in other modes", func(t *testing.T) {
		host, h := startTUI(t)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "early"})
		settle()
		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests before session_start", n)
		}
		rpc := startHost(t, herdr.Extension(), "rpc")
		rpc.fire("session_start", nil)
		rpc.emit("herdr:blocked", map[string]any{"active": true, "label": "rpc"})
		settle()
		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests from an rpc session", n)
		}
	})
}

func TestResumeCommand(t *testing.T) {
	// resumeArgvFor, herdr-agent-state.ts:86-118.
	for _, tc := range []struct {
		name  string
		state hostState
		want  []string // nil: no resume_argv member
	}{
		{"an absolute posix file", hostState{sessionFile: "/h/s.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "/h/s.jsonl"}},
		{"an absolute Windows file", hostState{sessionFile: `C:\Users\u\s.jsonl`, sessionID: "abc", idle: true}, []string{"pig", "--session", `C:\Users\u\s.jsonl`}},
		{"a Windows file with a forward slash", hostState{sessionFile: `D:/u/s.jsonl`, sessionID: "abc", idle: true}, []string{"pig", "--session", `D:/u/s.jsonl`}},
		{"a UNC file", hostState{sessionFile: `\\srv\share\s.jsonl`, sessionID: "abc", idle: true}, []string{"pig", "--session", `\\srv\share\s.jsonl`}},
		{"a relative file falls back to the id", hostState{sessionFile: "s.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"a drive without a separator is relative", hostState{sessionFile: "C:s.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"an apostrophe in the file falls back to the id", hostState{sessionFile: "/h/o'b/s.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"a control character in the file falls back to the id", hostState{sessionFile: "/h/a\x01b.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"DEL in the file falls back to the id", hostState{sessionFile: "/h/a\x7fb.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"a C1 control is allowed", hostState{sessionFile: "/h/a\u0085b.jsonl", sessionID: "abc", idle: true}, []string{"pig", "--session", "/h/a\u0085b.jsonl"}},
		{"an in-memory session resumes by its id, as the original does", hostState{noFile: true, sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"a file whose lookup fails falls back to the id", hostState{fileFailure: true, sessionID: "abc", idle: true}, []string{"pig", "--session", "abc"}},
		{"an unsafe id and an unsafe file: no command", hostState{sessionFile: "/h/o'b/s.jsonl", sessionID: "it's", idle: true}, nil},
		{"no id and no file: no command", hostState{noFile: true, noID: true, idle: true}, nil},
		{"both lookups fail: no command", hostState{fileFailure: true, idFailure: true, idle: true}, nil},
		{"an empty id: no command", hostState{noFile: true, sessionID: "", idle: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, h := startTUI(t)
			host.setState(func(s *hostState) { *s = tc.state })
			host.fire("session_start", nil)
			r := h.waitFor(1)[0]
			if tc.want == nil {
				if hasParam(r, "resume_argv") {
					t.Fatalf("resume_argv present: %s", r.Raw)
				}
				return
			}
			got, _ := r.Params["resume_argv"].([]any)
			var gs []string
			for _, v := range got {
				s, _ := v.(string)
				gs = append(gs, s)
			}
			equalStrings(t, gs, tc.want)
		})
	}

	t.Run("a safe resume argument is any non-empty string without an apostrophe or ASCII control", func(t *testing.T) {
		for _, v := range []string{"abc", "a b", "é", "a\u0085b", "a\u2028b", "\"q\""} {
			if !herdr.IsSafeResumeArg(v) {
				t.Errorf("%q should be safe", v)
			}
		}
		for _, v := range []string{"", "'", "a'b", "a\x00b", "a\tb", "a\nb", "a\x1fb", "a\x7fb"} {
			if herdr.IsSafeResumeArg(v) {
				t.Errorf("%q should be unsafe", v)
			}
		}
	})

	t.Run("there is no length or argument-count rule: the original sends what it was given", func(t *testing.T) {
		long := "/h/" + strings.Repeat("x", 9000) + ".jsonl"
		got := herdr.ResumeArgvFor(&long, nil)
		if len(got) != 3 || got[2] != long {
			t.Fatalf("argv = %d items", len(got))
		}
	})
}

func TestSession(t *testing.T) {
	t.Run("agent_start refreshes the resume command from the current session", func(t *testing.T) {
		host, h := startSession(t)
		host.setState(func(s *hostState) { s.sessionFile = "/x/s3.jsonl" })
		host.fire("agent_start", nil)
		resume, _ := h.waitFor(2)[1].Params["resume_argv"].([]any)
		if len(resume) != 3 || resume[2] != "/x/s3.jsonl" {
			t.Fatalf("resume_argv = %v", resume)
		}
	})

	t.Run("a session replacement reports again from a fresh state", func(t *testing.T) {
		// Pi loads a fresh copy of the extension for every session (core/extensions/loader.ts moduleCache: false, after
		// session_shutdown in core/agent-session-runtime.ts), so the blocked counter and the reporting epoch start
		// over. A Go host may keep the extension alive: the shutdown silences it and the next session_start resets it.
		host, h := startSession(t)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "stale"})
		h.waitFor(2)
		host.fire("session_shutdown", map[string]any{"reason": "new"})
		host.fire("agent_start", nil)
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "blocked"})
		host.setState(func(s *hostState) { s.sessionFile, s.sessionID = "/x/s2.jsonl", "s2" })
		host.fire("session_start", map[string]any{"reason": "new"})
		all := h.waitFor(3)
		equalStrings(t, states(all), []string{"idle", "blocked", "idle"})
		resume, _ := all[2].Params["resume_argv"].([]any)
		if len(resume) != 3 || resume[2] != "/x/s2.jsonl" {
			t.Fatalf("resume_argv = %v", resume)
		}
		host.fire("agent_start", nil)
		equalStrings(t, states(h.waitFor(4)), []string{"idle", "blocked", "idle", "working"})
	})

	t.Run("agent events before session_start report nothing", func(t *testing.T) {
		host, h := startTUI(t)
		host.fire("agent_start", nil)
		host.fire("agent_settled", nil)
		settle()
		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests before session_start", n)
		}
	})
}

func TestRelease(t *testing.T) {
	// session_shutdown, herdr-agent-state.ts:266-275.
	for _, reason := range []string{"reload", "new", "resume", "fork", "", "other"} {
		t.Run("does not release on shutdown reason "+reason, func(t *testing.T) {
			host, h := startSession(t)
			data := map[string]any{"reason": reason}
			if reason == "" {
				data = nil
			}
			host.fire("session_shutdown", data)
			settle()
			equalStrings(t, states(h.requests()), []string{"idle"})
		})
	}

	t.Run("sends no report after the release", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		host.fire("agent_start", nil)
		host.fire("agent_settled", nil)
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "late"})
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "release"})
	})

	t.Run("does not release a pane it never reported on", func(t *testing.T) {
		h := installFakeHerdr(t)
		host := startHost(t, herdr.Extension(), "print")
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		settle()
		if n := len(h.requests()); n != 0 {
			t.Fatalf("%d requests from a print-mode session", n)
		}
	})

	t.Run("quit twice releases once", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "release"})
	})

	t.Run("drops a queued report and does not wait for the one in flight", func(t *testing.T) {
		host, h := startTUI(t)
		h.behave = func(n int) behavior {
			return behavior{Delay: time.Duration(map[bool]int{true: 600, false: 0}[n == 0]) * time.Millisecond}
		}
		host.fire("session_start", nil) // in flight, answered after 600 ms
		host.fire("agent_start", nil)   // queued: must be dropped
		begin := time.Now()
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		if took := time.Since(begin); took > 400*time.Millisecond {
			t.Fatalf("quit waited %v for the in-flight report", took)
		}
		time.Sleep(900 * time.Millisecond)
		got := states(h.requests())
		if len(got) != 2 || got[0] != "idle" || got[1] != "release" {
			t.Fatalf("requests = %v, want the in-flight report and the release", got)
		}
	})

	t.Run("the release seq is above every report's", func(t *testing.T) {
		host, h := startSession(t)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.fire("session_shutdown", map[string]any{"reason": "quit"})
		order := seqs(t, h.waitFor(3))
		if order[2] <= order[1] || order[1] <= order[0] {
			t.Fatalf("seq %v", order)
		}
	})
}

func TestPromptBlocked(t *testing.T) {
	// ui_prompt_start and ui_prompt_end are PiG's; Pi has no such event and the original never reports a prompt. The
	// state is therefore opt-in (PIGPEN_HERDR_UI_PROMPTS=1), so that by default the port reports exactly what the
	// original does in the same session.
	prompt := map[string]any{"reason": "ui_prompt", "kind": "confirm", "title": "Allow rm -rf build?"}

	t.Run("is off by default: no handler, no report", func(t *testing.T) {
		host, h := startSession(t)
		if host.registered("ui_prompt_start") || host.registered("ui_prompt_end") {
			t.Fatal("prompt handlers registered without the opt-in")
		}
		host.fire("ui_prompt_start", prompt)
		settle()
		equalStrings(t, states(h.requests()), []string{"idle"})
	})

	t.Run("anything but 1 leaves it off", func(t *testing.T) {
		h := installFakeHerdr(t)
		t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "true")
		host := startHost(t, herdr.Extension(), "tui")
		if host.registered("ui_prompt_start") {
			t.Fatal("registered for PIGPEN_HERDR_UI_PROMPTS=true")
		}
		_ = h
	})

	t.Run("with the opt-in, a prompt is blocked with its title and then restores the state", func(t *testing.T) {
		h := installFakeHerdr(t)
		t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "1")
		host := startHost(t, herdr.Extension(), "tui")
		host.fire("session_start", nil)
		host.fire("agent_start", nil)
		h.waitFor(2)
		host.fire("ui_prompt_start", prompt)
		all := h.waitFor(3)
		if all[2].Params["state"] != "blocked" || all[2].Params["message"] != "Allow rm -rf build?" {
			t.Fatalf("request = %s", all[2].Raw)
		}
		host.fire("ui_prompt_end", prompt)
		equalStrings(t, states(h.waitFor(4)), []string{"idle", "working", "blocked", "working"})
	})

	t.Run("with the opt-in, an untitled prompt uses its kind, a long title is one short line, and holders do not release each other", func(t *testing.T) {
		h := installFakeHerdr(t)
		t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "1")
		host := startHost(t, herdr.Extension(), "tui")
		host.fire("session_start", nil)
		h.waitFor(1)
		host.fire("ui_prompt_start", map[string]any{"kind": "custom"})
		if got := h.waitFor(2)[1].Params["message"]; got != "custom" {
			t.Fatalf("message = %v", got)
		}
		host.fire("ui_prompt_end", map[string]any{"kind": "custom"})
		h.waitFor(3)
		host.fire("ui_prompt_start", map[string]any{"kind": "editor", "title": "first line\n" + strings.Repeat("x", 400)})
		message, _ := h.waitFor(4)[3].Params["message"].(string)
		if strings.Contains(message, "\n") || len([]rune(message)) > 120 || !strings.HasPrefix(message, "first line x") {
			t.Fatalf("message = %q", message)
		}
		host.emit("herdr:blocked", map[string]any{"active": true, "label": "bus"})
		h.waitFor(5)
		host.emit("herdr:blocked", map[string]any{"active": false}) // the prompt's block stays
		settle()
		equalStrings(t, states(h.requests()), []string{"idle", "blocked", "idle", "blocked", "blocked"})
		host.fire("ui_prompt_end", nil)
		equalStrings(t, states(h.waitFor(6)), []string{"idle", "blocked", "idle", "blocked", "blocked", "idle"})
	})

	t.Run("with the opt-in, an unmatched prompt end is ignored", func(t *testing.T) {
		h := installFakeHerdr(t)
		t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "1")
		host := startHost(t, herdr.Extension(), "tui")
		host.fire("session_start", nil)
		h.waitFor(1)
		host.fire("ui_prompt_end", prompt)
		host.fire("ui_prompt_start", prompt)
		equalStrings(t, states(h.waitFor(2)), []string{"idle", "blocked"})
	})
}

func TestOutsideHerdr(t *testing.T) {
	// enabled(), herdr-agent-state.ts:25-27: HERDR_ENV=1 and a socket path and a pane id. HERDR_BIN_PATH is not part of it.
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T)
		active bool
	}{
		{"HERDR_ENV unset", func(t *testing.T) { unset(t, "HERDR_ENV") }, false},
		{"HERDR_ENV is not 1", func(t *testing.T) { t.Setenv("HERDR_ENV", "0") }, false},
		{"HERDR_ENV is true", func(t *testing.T) { t.Setenv("HERDR_ENV", "true") }, false},
		{"HERDR_PANE_ID unset", func(t *testing.T) { unset(t, "HERDR_PANE_ID") }, false},
		{"HERDR_PANE_ID empty", func(t *testing.T) { t.Setenv("HERDR_PANE_ID", "") }, false},
		{"HERDR_SOCKET_PATH unset", func(t *testing.T) { unset(t, "HERDR_SOCKET_PATH") }, false},
		{"HERDR_SOCKET_PATH empty", func(t *testing.T) { t.Setenv("HERDR_SOCKET_PATH", "") }, false},
		{"HERDR_BIN_PATH unset (the original does not need it)", func(t *testing.T) { unset(t, "HERDR_BIN_PATH") }, true},
	} {
		t.Run("when "+tc.name, func(t *testing.T) {
			h := installFakeHerdr(t)
			tc.mutate(t)
			host := startHost(t, herdr.Extension(), "tui")
			if got := len(host.register.Handlers) != 0; got != tc.active {
				t.Fatalf("handlers registered = %v, want %v", got, tc.active)
			}
			if len(host.register.Tools) != 0 || len(host.register.Commands) != 0 {
				t.Fatal("registered tools or commands")
			}
			host.fire("session_start", nil)
			if tc.active {
				h.waitFor(1)
				return
			}
			settle()
			if n := len(h.requests()); n != 0 {
				t.Fatalf("%d requests outside herdr", n)
			}
		})
	}

	t.Run("the headless modes report nothing", func(t *testing.T) {
		for _, mode := range []string{"rpc", "json", "print"} {
			h := installFakeHerdr(t)
			t.Setenv("PIGPEN_HERDR_UI_PROMPTS", "1")
			host := startHost(t, herdr.Extension(), mode)
			host.fire("session_start", nil)
			host.fire("agent_start", nil)
			host.fire("ui_prompt_start", map[string]any{"kind": "confirm"})
			host.emit("herdr:blocked", map[string]any{"active": true, "label": "x"})
			host.fire("session_shutdown", map[string]any{"reason": "quit"})
			settle()
			if n := len(h.requests()); n != 0 {
				t.Fatalf("%d requests in %s mode", n, mode)
			}
		}
	})
}

func unset(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "") // registers the restore
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
