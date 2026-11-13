package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seriesClient(srv *httptest.Server) *client {
	cfg := config{BaseURL: srv.URL, Agent: "kagent/x", Timeout: 10 * time.Second, NoSendRetry: true}
	return &client{cfg: cfg, http: srv.Client()}
}

var fastOpts = retryOpts{AttemptTimeout: 300 * time.Millisecond, MaxTotal: 5 * time.Second, BackoffInitial: 5 * time.Millisecond, BackoffMax: 20 * time.Millisecond}

// remember sends a plain message and returns the session context and the marker the recall must answer with.
func remember(t *testing.T, c *client, marker string) string {
	t.Helper()
	rr := &runResult{}
	tr, err := c.runTurn(context.Background(), rr, "remember", userMessage(marker+" remember this", "", ""))
	if err != nil || tr.ContextID == "" {
		t.Fatalf("remember: %v %+v", err, tr)
	}
	return tr.ContextID
}

func TestRecallFirstAttemptSuccess(t *testing.T) {
	fg := newFakeGateway()
	srv := httptest.NewServer(fg)
	defer srv.Close()
	c := seriesClient(srv)
	ctx := remember(t, c, "[run-aa]")
	rs := c.runRetrySeries(context.Background(), "recall", func() map[string]any { return userMessage("[recall] go", ctx, "") }, ctx, "[run-aa]", fastOpts)
	if !rs.FirstSuccess || !rs.EventualSuccess || rs.AttemptsToSuccess != 1 || len(rs.Attempts) != 1 {
		t.Fatalf("%+v", rs)
	}
}

// The first attempt is recorded exactly (outcome and code), later attempts are retried with the same message until success.
func TestRecallFaultsRecordedThenRetried(t *testing.T) {
	cases := []struct {
		fault   string
		outcome string
		code    int
		http    int
	}{
		{"rpc:-32004", outJSONRPC, -32004, 200},
		{"sse:-32004", outJSONRPC, -32004, 0},
		{"http:503", outHTTP, 0, 503},
		{"empty", outEmpty, 0, 0},
		{"hang", outTimeout, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.fault, func(t *testing.T) {
			fg := newFakeGatewayFaults("")
			srv := httptest.NewServer(fg)
			defer srv.Close()
			c := seriesClient(srv)
			ctx := remember(t, c, "[run-bb]")
			fg.faults = []string{tc.fault, "rpc:-32004"}
			rs := c.runRetrySeries(context.Background(), "recall", func() map[string]any { return userMessage("[recall] go", ctx, "") }, ctx, "[run-bb]", fastOpts)
			if rs.FirstSuccess || !rs.EventualSuccess || rs.AttemptsToSuccess != 3 || len(rs.Attempts) != 3 {
				t.Fatalf("%+v", rs)
			}
			f := rs.First
			if f.Outcome != tc.outcome || f.HTTPStatus != tc.http || (tc.code != 0) != (f.RPCCode != nil) || (f.RPCCode != nil && *f.RPCCode != tc.code) {
				t.Fatalf("first attempt %+v, want %s code %d http %d", f, tc.outcome, tc.code, tc.http)
			}
			if rs.Attempts[1].Outcome != outJSONRPC || rs.Attempts[2].Outcome != outSuccess {
				t.Fatalf("attempts %+v", rs.Attempts)
			}
			if rs.Attempts[2].StartMs <= rs.Attempts[1].StartMs || rs.MsToSuccess < rs.Attempts[2].StartMs {
				t.Fatalf("timing %+v", rs)
			}
		})
	}
}

func TestRecallWrongAnswerIsNotSuccess(t *testing.T) {
	srv := httptest.NewServer(newFakeGateway())
	defer srv.Close()
	c := seriesClient(srv)
	ctx := remember(t, c, "[run-cc]")
	rs := c.runRetrySeries(context.Background(), "recall", func() map[string]any { return userMessage("[recall] go", ctx, "") }, ctx, "[run-OTHER]", fastOpts)
	if rs.EventualSuccess || rs.First.Outcome != outWrongAnswer || rs.Error == "" {
		t.Fatalf("%+v", rs)
	}
}

func TestRetryBudgetExhausted(t *testing.T) {
	fg := newFakeGatewayFaults("")
	srv := httptest.NewServer(fg)
	defer srv.Close()
	c := seriesClient(srv)
	ctx := remember(t, c, "[run-dd]")
	for range 50 {
		fg.faults = append(fg.faults, "rpc:-32004")
	}
	o := fastOpts
	o.MaxTotal = 150 * time.Millisecond
	rs := c.runRetrySeries(context.Background(), "recall", func() map[string]any { return userMessage("[recall] go", ctx, "") }, ctx, "[run-dd]", o)
	if rs.EventualSuccess || rs.FirstSuccess || len(rs.Attempts) < 2 || rs.Error == "" {
		t.Fatalf("%+v", rs)
	}
}

// A gateway "not accepted" answer must surface as the first attempt's failure, not be retried silently inside sendStream.
func TestNoSendRetryExposesNotAccepted(t *testing.T) {
	fg := newFakeGatewayFaults("")
	srv := httptest.NewServer(fg)
	defer srv.Close()
	c := seriesClient(srv)
	ctx := remember(t, c, "[run-ee]")
	fg.faults = []string{"notaccepted"}
	rs := c.runRetrySeries(context.Background(), "recall", func() map[string]any { return userMessage("[recall] go", ctx, "") }, ctx, "[run-ee]", fastOpts)
	if rs.First.Outcome != outJSONRPC || rs.First.HTTPStatus != 400 || rs.First.RPCCode == nil || *rs.First.RPCCode != -32004 || !rs.EventualSuccess || rs.AttemptsToSuccess != 2 {
		t.Fatalf("%+v", rs)
	}
}

// The variant with an approval parked before the pod is replaced: resume it through the same retry path.
func TestResumeApprovalAfterFaults(t *testing.T) {
	fg := newFakeGatewayFaults("")
	srv := httptest.NewServer(fg)
	defer srv.Close()
	c := seriesClient(srv)
	st := filepath.Join(t.TempDir(), "state.json")
	if code := demoSend(c, "[run-ff] [approve] go", "", "", st); code != 0 {
		t.Fatalf("demoSend %d", code)
	}
	fg.faults = []string{"rpc:-32004", "empty"}
	out := filepath.Join(t.TempDir(), "out.json")
	if code := seriesMain(c, "resume", "", "", st, "APPROVE-DONE", out, fastOpts); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if b, _ := os.ReadFile(out); len(b) == 0 {
		t.Fatal("no output")
	}
	if fg.nFault != 2 {
		t.Fatalf("faults served %d", fg.nFault)
	}
}

func TestSeriesMainInputErrors(t *testing.T) {
	srv := httptest.NewServer(newFakeGateway())
	defer srv.Close()
	c := seriesClient(srv)
	if code := seriesMain(c, "recall", "[recall] go", "", "", "", "", fastOpts); code != 2 {
		t.Errorf("recall without context: %d", code)
	}
	if code := seriesMain(c, "resume", "", "", filepath.Join(t.TempDir(), "none.json"), "", "", fastOpts); code != 2 {
		t.Errorf("resume without state: %d", code)
	}
}

func TestRejectionCarriesConfiguredReason(t *testing.T) {
	old := rejectReason
	defer func() { rejectReason = old }()
	rejectReason = "not today, thanks"
	ar := &approvalRequest{}
	ar.Tools = append(ar.Tools, struct {
		ID   string         `json:"id"`
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}{ID: "t1", Name: "x"})
	m := approvalMessage(ar, false, "c", "k")
	item := m["metadata"].(map[string]any)[hitlURI].(map[string]any)["approvals"].([]any)[0].(map[string]any)
	if item["approved"] != false || item["rejection_reason"] != "not today, thanks" {
		t.Fatalf("%v", item)
	}
	if a := approvalMessage(ar, true, "c", "k")["metadata"].(map[string]any)[hitlURI].(map[string]any)["approvals"].([]any)[0].(map[string]any); a["rejection_reason"] != nil {
		t.Fatalf("approval carries a reason: %v", a)
	}
}

// A rejected [approve] session through the bench path ends in APPROVE-DONE over the rejection text, with the configured reason.
func TestRejectedApproveSessionAgainstFake(t *testing.T) {
	old := rejectReason
	defer func() { rejectReason = old }()
	rejectReason = "because"
	srv := httptest.NewServer(newFakeGateway())
	defer srv.Close()
	cfg := config{BaseURL: srv.URL, Agent: "kagent/x", Approve: "reject", Timeout: 10 * time.Second, Prompt: "go", Scenario: "approve"}
	c := &client{cfg: cfg, http: srv.Client()}
	rr := c.iteration(context.Background(), 0, false)
	if !rr.OK || rr.Approval != "rejected" {
		t.Fatalf("%+v", rr)
	}
	want := "APPROVE-DONE " + fakeSha(fakeRejectionText("because"))
	if last := rr.Turns[len(rr.Turns)-1].Text; !strings.Contains(last, want) {
		t.Fatalf("answer %q lacks %q", last, want)
	}
}
