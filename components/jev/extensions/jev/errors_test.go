package jev_test

import (
	"strings"
	"testing"
	"time"
)

// Failure policy: every error path fails open (README "Every error path fails
// open", AGENTS.md "Do not change an error path to block a tool call").

func errorHost(t *testing.T, next func(int, recordedRequest) jevReply, extra map[string]any) (*Host, *fakeJev) {
	t.Helper()
	e := newEnv(t)
	t.Setenv("TYPESAFE_API_KEY", testKey)
	srv := newFakeJev(t, next)
	e.writeGlobal(t, jevConfig(srv, extra))
	return start(t, e, newHostState(), HostOptions{}), srv
}

func TestFailOpen_ServerErrorNeverBlocks(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply { return jevReply{status: 500, body: "internal"} },
		map[string]any{"gate": map[string]any{"mode": "enforce", "blockWithoutUI": true}})
	if block, _ := h.toolCall("bash", bash("rm -rf /")); block {
		t.Fatal("an unavailable judge blocked a tool call")
	}
	if !h.anyNotification("error: pi-jev: ") || !h.anyNotification("500") || !h.anyNotification("(failing open)") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestFailOpen_OutputJudgeLeavesResultAlone(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply { return jevReply{status: 503, body: "x"} }, nil)
	if _, none := h.toolResult("bash", bash("x"), text("out"), false); !none {
		t.Fatal("patched a result with no verdict")
	}
}

func TestErrors_TimeoutFailsOpen(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply {
		return jevReply{body: clearGate(), delay: 1500 * time.Millisecond}
	}, map[string]any{"timeoutMs": 200, "retries": 0})
	start := time.Now()
	if block, _ := h.toolCall("bash", bash("ls")); block {
		t.Fatal("blocked")
	}
	if time.Since(start) > 1200*time.Millisecond {
		t.Errorf("did not time out: %v", time.Since(start))
	}
	if !h.anyNotification("timed out") || !h.anyNotification("(failing open)") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestErrors_ReportedAtMostOncePerMinute(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply { return jevReply{status: 500, body: "x"} }, nil)
	for i := 0; i < 4; i++ {
		h.toolCall("bash", bash("cmd "+string(rune('a'+i))))
	}
	n := 0
	for _, note := range h.notifications() {
		if strings.Contains(note, "failing open") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("error notifications = %d, want 1", n)
	}
}

// Twin of the 0.2.2 fix (issue #3): a response that skips a question is an
// error, not a clear verdict.
func TestErrors_EmptyAnswersAreNotAClearVerdict(t *testing.T) {
	h, _ := errorHost(t, always(map[string]any{"model": "m", "answers": map[string]any{}}), nil)
	h.toolCall("bash", bash("rm -rf /"))
	if !h.anyNotification(`did not answer "destructive"`) || !h.anyNotification("(failing open)") {
		t.Errorf("notifications = %q", h.notifications())
	}
	if strings.Contains(h.lastStatus(), "clear") {
		t.Errorf("status = %q, an empty answer must not read as clear", h.lastStatus())
	}
}

func TestErrors_WrongAnswerTypeIsAnError(t *testing.T) {
	body := gateBody(0.1, 0.1, 0.1, 1, 0.9)
	body["answers"].(map[string]any)["destructive"] = score(1, 0.9)
	h, _ := errorHost(t, always(body), nil)
	h.toolCall("bash", bash("x"))
	if !h.anyNotification(`"destructive" is not a noul`) {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestErrors_MalformedBodiesFailOpenAndNeverRead(t *testing.T) {
	for _, body := range []string{`{"model":"m"}`, `[1]`, `null`, `not json`} {
		h, _ := errorHost(t, always(body), nil)
		h.toolCall("bash", bash("x"))
		if !h.anyNotification("(failing open)") {
			t.Errorf("body %s: notifications = %q", body, h.notifications())
		}
		if strings.Contains(h.lastStatus(), "clear") {
			t.Errorf("body %s read as clear: %q", body, h.lastStatus())
		}
	}
}

// C5: the original accepts a probability of 1.7 or -2 and compares it to a threshold.
func TestCorrection_OutOfRangeProbabilityIsAnError(t *testing.T) {
	for _, v := range []float64{1.7, -0.2} {
		h, _ := errorHost(t, always(gateBody(v, 0.1, 0.1, 1, 0.9)), nil)
		h.toolCall("bash", bash("x"))
		if !h.anyNotification("outside 0 to 1") {
			t.Errorf("noul %v accepted: %q", v, h.notifications())
		}
	}
}

func TestCorrection_ScoreOutsideTheRubricIsAnError(t *testing.T) {
	h, _ := errorHost(t, always(gateBody(0.1, 0.1, 0.1, 9.0, 0.9)), nil)
	h.toolCall("bash", bash("x"))
	if !h.anyNotification(`"impact" score 9 is outside the 4 levels`) {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestCorrection_ConfidenceOutsideZeroToOneIsAnError(t *testing.T) {
	h, _ := errorHost(t, always(gateBody(0.1, 0.1, 0.1, 1, 4.0)), nil)
	h.toolCall("bash", bash("x"))
	if !h.anyNotification("outside 0 to 1") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

// C6: on an error the original leaves the previous "clear" status on screen.
func TestCorrection_ErrorStatusIsUnavailableNotStaleClear(t *testing.T) {
	h, _ := errorHost(t, func(i int, _ recordedRequest) jevReply {
		if i == 0 {
			return jevReply{body: clearGate()}
		}
		return jevReply{status: 500, body: "x"}
	}, nil)
	h.toolCall("bash", bash("first"))
	if h.lastStatus() != "jev: clear (shadow)" {
		t.Fatalf("setup status %q", h.lastStatus())
	}
	h.toolCall("bash", bash("second"))
	if got := h.lastStatus(); got != "jev: unavailable (failing open)" {
		t.Errorf("status = %q, want jev: unavailable (failing open)", got)
	}
}

func TestCorrection_OutputJudgeErrorAlsoShowsUnavailable(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply { return jevReply{status: 500, body: "x"} }, nil)
	h.toolResult("bash", bash("x"), text("out"), false)
	if got := h.lastStatus(); got != "jev: unavailable (failing open)" {
		t.Errorf("status = %q", got)
	}
}

func TestErrors_ApiKeyIsRedactedFromNotifications(t *testing.T) {
	h, _ := errorHost(t, func(int, recordedRequest) jevReply { return jevReply{status: 400, body: "bad key " + testKey} }, nil)
	h.toolCall("bash", bash("x"))
	for _, n := range h.notifications() {
		if strings.Contains(n, testKey) {
			t.Fatalf("API key leaked into a notification: %q", n)
		}
	}
	if !h.anyNotification("[redacted]") {
		t.Errorf("notifications = %q", h.notifications())
	}
}

func TestErrors_QuestionsAreValidatedBeforeAnyRequest(t *testing.T) {
	// A choice with no option is refused locally by jev_ask (see ask_test.go); the
	// gate's own questions are fixed, so this checks that no request is made for
	// an unparsable input.
	h, srv := errorHost(t, always(clearGate()), nil)
	h.toolCall("read", map[string]any{})
	if srv.count() != 0 {
		t.Fatal("request for an unjudged tool")
	}
}

func TestCorrection_ChoiceConfidenceOutsideZeroToOneIsAnError(t *testing.T) {
	body := outBody(0.01, "transient", 7.0)
	h, _ := errorHost(t, always(body), nil)
	h.toolResult("bash", bash("x"), text("out"), false)
	if !h.anyNotification("outside 0 to 1") {
		t.Errorf("notifications = %q", h.notifications())
	}
}
