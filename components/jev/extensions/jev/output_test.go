package jev_test

import (
	"strings"
	"testing"
)

// Output judge: src/index.ts tool_result, src/output.ts.

func outputHost(t *testing.T, body any, extra map[string]any) (*Host, *fakeJev) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, always(body))
	e.writeGlobal(t, jevConfig(srv, extra))
	return start(t, e, newHostState(), HostOptions{}), srv
}

func lastText(t *testing.T, patched []string) string {
	t.Helper()
	if len(patched) == 0 {
		t.Fatal("result was not patched")
	}
	return patched[len(patched)-1]
}

func TestOutput_LeakAppendsNoticeAndWarns(t *testing.T) {
	h, _ := outputHost(t, outBody(0.94, "no_failure", 0.9), nil)
	patched, none := h.toolResult("bash", bash("cat .env"), text("API_KEY=abc"), false)
	if none {
		t.Fatal("result not patched")
	}
	if patched[0] != "API_KEY=abc" {
		t.Errorf("original block changed: %q", patched)
	}
	want := "[pi-jev] Jev flagged this output as containing a secret (0.94). Do not repeat the value in a reply, a file, or a command; refer to it by name instead."
	if got := lastText(t, patched); got != want {
		t.Errorf("notice = %q", got)
	}
	if !h.anyNotification("warning: jev: bash output may carry a secret (0.94)") {
		t.Errorf("notifications = %q", h.notifications())
	}
	if h.lastStatus() != "jev: leak (bash)" {
		t.Errorf("status = %q", h.lastStatus())
	}
}

func TestOutput_FailureClassAdviceTable(t *testing.T) {
	advice := map[string]string{
		"transient":   "retrying the same command unchanged is reasonable",
		"environment": "fix the environment (missing tool, port, or service) before retrying",
		"code_bug":    "fix the code or types; retrying unchanged will not help",
		"permission":  "access was denied; change what is being accessed or ask the user",
		"user_error":  "the invocation itself was wrong; fix the command",
	}
	for class, line := range advice {
		t.Run(class, func(t *testing.T) {
			h, _ := outputHost(t, outBody(0.01, class, 1.0), nil)
			patched, none := h.toolResult("bash", bash("x"), text("boom"), true)
			if none {
				t.Fatal("no advice")
			}
			want := "[pi-jev] Jev read this as a " + class + " failure (confidence 1.00): " + line + "."
			if got := lastText(t, patched); got != want {
				t.Errorf("notice = %q, want %q", got, want)
			}
			if len(h.notifications()) != 0 {
				t.Errorf("advice notified: %q", h.notifications())
			}
			if h.lastStatus() != "jev: advice (bash)" {
				t.Errorf("status = %q", h.lastStatus())
			}
		})
	}
}

func TestOutput_NoFailureAndUnknownClassSayNothing(t *testing.T) {
	for _, class := range []string{"no_failure", "brand_new_class"} {
		h, _ := outputHost(t, outBody(0.01, class, 1.0), nil)
		if _, none := h.toolResult("bash", bash("x"), text("ok"), false); !none {
			t.Errorf("class %q patched the result", class)
		}
	}
}

func TestOutput_LowClassConfidenceIsSilent(t *testing.T) {
	h, _ := outputHost(t, outBody(0.01, "environment", 0.42), nil)
	if _, none := h.toolResult("bash", bash("x"), text("fatal: not a git repository"), true); !none {
		t.Fatal("advice below output.minConfidence")
	}
}

func TestOutput_LeakThresholdIsInclusiveAndLeakWinsOverClass(t *testing.T) {
	h, _ := outputHost(t, outBody(0.9, "transient", 1.0), nil)
	patched, _ := h.toolResult("bash", bash("x"), text("t"), false)
	mustContain(t, "notice", lastText(t, patched), "containing a secret (0.90)")
}

func TestOutput_OnlyConfiguredToolsAreJudged(t *testing.T) {
	h, srv := outputHost(t, outBody(0.99, "no_failure", 1), nil)
	h.toolResult("read", map[string]any{"path": "a"}, text("secret"), false)
	if srv.count() != 0 {
		t.Fatal("judged the output of read")
	}
}

func TestOutput_EmptyOrNonTextResultIsNotJudged(t *testing.T) {
	h, srv := outputHost(t, outBody(0.99, "no_failure", 1), nil)
	h.toolResult("bash", bash("x"), text("  \n "), false)
	h.toolResult("bash", bash("x"), []any{map[string]any{"type": "image", "data": "AAAA"}}, false)
	if srv.count() != 0 {
		t.Fatalf("requests = %d", srv.count())
	}
}

func TestOutput_JoinsTextBlocksWithNewline(t *testing.T) {
	h, srv := outputHost(t, outBody(0.01, "no_failure", 1), nil)
	h.toolResult("bash", bash("x"), []any{map[string]any{"type": "text", "text": "one"}, map[string]any{"type": "text", "text": "two"}}, false)
	st := stateOf(t, srv.first(t))
	if st["output"] != "one\ntwo" || st["is_error"] != false || st["tool"] != "bash" {
		t.Errorf("state = %v", st)
	}
}

func TestOutput_RequestQuestions(t *testing.T) {
	h, srv := outputHost(t, outBody(0.01, "no_failure", 1), nil)
	h.toolResult("bash", bash("x"), text("hi"), false)
	qs := srv.first(t).Body["questions"].(map[string]any)
	leak := qs["leaks_secret"].(map[string]any)
	if leak["type"] != "noul" || leak["instructions"] != "Does `output` contain a secret or credential that must not be written to a session transcript?" {
		t.Errorf("leaks_secret = %v", leak)
	}
	fc := qs["failure_class"].(map[string]any)
	crit := fc["criteria"].(map[string]any)
	if fc["type"] != "choice" || len(crit) != 6 || crit["transient"] != "A network or resource hiccup that may succeed on a retry" {
		t.Errorf("failure_class = %v", fc)
	}
	if got := strings.Join(questionOrder(srv.first(t).Raw), ","); got != "leaks_secret,failure_class" {
		t.Errorf("order = %s", got)
	}
}

func TestOutput_LongOutputIsElided(t *testing.T) {
	h, srv := outputHost(t, outBody(0.01, "no_failure", 1), nil)
	h.toolResult("bash", bash("x"), text(strings.Repeat("o", 2600)), false)
	want := strings.Repeat("o", 2000) + "…[600 chars elided]"
	if got := stateOf(t, srv.first(t))["output"]; got != want {
		t.Errorf("output not elided: len %d", len(got.(string)))
	}
}

func TestOutput_ArgumentsAreElidedAt400(t *testing.T) {
	h, srv := outputHost(t, outBody(0.01, "no_failure", 1), nil)
	h.toolResult("bash", map[string]any{"command": strings.Repeat("c", 500)}, text("hi"), false)
	args := stateOf(t, srv.first(t))["arguments"].(map[string]any)
	if args["command"] != strings.Repeat("c", 400)+"…[100 chars elided]" {
		t.Errorf("command = %v", args["command"])
	}
}

func TestOutput_IdenticalOutputIsJudgedOnce(t *testing.T) {
	h, srv := outputHost(t, outBody(0.01, "no_failure", 1), nil)
	h.toolResult("bash", bash("a"), text("same"), false)
	h.toolResult("bash", bash("b"), text("same"), false)
	if srv.count() != 1 {
		t.Fatalf("requests = %d, want 1", srv.count())
	}
	h.toolResult("bash", bash("c"), text("different"), false)
	if srv.count() != 2 {
		t.Fatalf("requests = %d, want 2", srv.count())
	}
}

func TestOutput_DisabledSkips(t *testing.T) {
	h, srv := outputHost(t, outBody(0.99, "no_failure", 1), map[string]any{"output": map[string]any{"enabled": false}})
	h.toolResult("bash", bash("x"), text("secret"), false)
	if srv.count() != 0 {
		t.Fatal("judged with output.enabled=false")
	}
}

func TestOutput_LeakThresholdConfigurable(t *testing.T) {
	h, _ := outputHost(t, outBody(0.5, "no_failure", 1), map[string]any{"output": map[string]any{"leakThreshold": 0.4}})
	if _, none := h.toolResult("bash", bash("x"), text("t"), false); none {
		t.Fatal("0.5 < configured 0.4 threshold not flagged")
	}
}
