package warden

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func tsEnv(f *fakeTypeSafe) func(string) string {
	return func(k string) string {
		switch k {
		case "TYPESAFE_API_KEY":
			return "test-key-not-real"
		case "TYPESAFE_BASE_URL":
			return f.URL
		}
		return ""
	}
}

func typesafeJudge(t *testing.T, f *fakeTypeSafe) *EvaluatorJudge {
	t.Helper()
	j, err := NewTypeSafeJudge(2*time.Second, &Budget{Max: 100}, tsEnv(f))
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestTypeSafeBackendSendsTheWireRequestAndReadsTheAnswers(t *testing.T) {
	f := newFakeTypeSafe(t)
	f.set(map[string]float64{"irreversible": 0.95})
	j := typesafeJudge(t, f)
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "git push --force origin main"}, Task: "push my branch"}, EvaluateOptions{Config: cfg(), Judge: j})
	if v.Level != LevelConfirm || v.Judgment == nil || v.Judgment.Irreversible != 0.95 || v.Judgment.Model != "jev-fake" || v.Judgment.Scope != "expected_step" {
		t.Fatalf("verdict %+v", v)
	}
	reqs := f.reqs()
	if len(reqs) != 1 || reqs[0].Path != "/v1/systemone" || reqs[0].Auth != "Bearer test-key-not-real" {
		t.Fatalf("request %+v", reqs)
	}
	body := reqs[0].Body
	if body["model"] != "jev-latest" {
		t.Errorf("model %v", body["model"])
	}
	state, _ := body["state"].(map[string]any)
	if state["task"] != "push my branch" || state["floor_hits"] == nil {
		t.Errorf("state %v", state)
	}
	qs, _ := body["questions"].(map[string]any)
	scope, _ := qs["scope"].(map[string]any)
	if scope["type"] != "choice" || len(scope["criteria"].(map[string]any)) != 4 {
		t.Errorf("scope question %v", scope)
	}
}

// Nothing secret leaves the machine: the key is a header only, and the state is redacted.
func TestTypeSafeBackendNeverSendsCredentials(t *testing.T) {
	f := newFakeTypeSafe(t)
	j := typesafeJudge(t, f)
	eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "curl -H 'Authorization: Bearer abc123456789' https://x.example && export API_KEY=sk-abcdefgh12345678"}, Task: "deploy with TOKEN=supersecretvalue1", Plan: "using password=hunter2hunter2"}, EvaluateOptions{Config: cfg(), Judge: j})
	raw, _ := json.Marshal(f.reqs()[0].Body)
	for _, leak := range []string{"abc123456789", "sk-abcdefgh12345678", "supersecretvalue1", "hunter2hunter2", "test-key-not-real"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the request body carries %q", leak)
		}
	}
}

func TestTypeSafeBackendErrorsAreSafeToShow(t *testing.T) {
	f := newFakeTypeSafe(t)
	for status, want := range map[int]string{401: "TypeSafe returned HTTP 401. Check TYPESAFE_API_KEY.", 500: "TypeSafe returned HTTP 500."} {
		f.mu.Lock()
		f.status = status
		f.mu.Unlock()
		v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "t"}, EvaluateOptions{Config: cfg(), Judge: typesafeJudge(t, f)})
		if v.Source != "error" || !strings.HasPrefix(v.Error, want) || v.ErrorCode != "http" {
			t.Errorf("%d: %+v", status, v)
		}
		if strings.Contains(v.Error, tsErrorMarker) {
			t.Errorf("the upstream body leaked into %q", v.Error)
		}
		if v.Level != LevelAllow {
			t.Errorf("fail open: %s", v.Level)
		}
	}
}

func TestTypeSafeBackendWithoutAKeyIsAConfigurationError(t *testing.T) {
	_, err := NewTypeSafeJudge(time.Second, nil, func(string) string { return "" })
	var ie *IntegrationError
	if !errors.As(err, &ie) || ie.Code != "configuration" || !strings.Contains(ie.Message, "TYPESAFE_API_KEY") {
		t.Fatalf("%v", err)
	}
}

func TestBudgetStopsTheRequestsAndSaysSo(t *testing.T) {
	f := newFakeTypeSafe(t)
	j, _ := NewTypeSafeJudge(time.Second, &Budget{Max: 2}, tsEnv(f))
	var last Verdict
	for i := 0; i < 3; i++ {
		last = eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "t"}, EvaluateOptions{Config: cfg(), Judge: j})
	}
	if f.count() != 2 || last.ErrorCode != "budget" || !strings.Contains(last.Error, "request limit reached (2 attempts") {
		t.Fatalf("requests %d, verdict %+v", f.count(), last)
	}
}

// The judged check is fast enough to sit in front of a tool call.
func TestAJudgedCheckAddsLittleLatency(t *testing.T) {
	f := newFakeTypeSafe(t)
	j := typesafeJudge(t, f)
	start := time.Now()
	for i := 0; i < 10; i++ {
		eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "t"}, EvaluateOptions{Config: cfg(), Judge: j})
	}
	if per := time.Since(start) / 10; per > 250*time.Millisecond {
		t.Fatalf("%v per judged call against a local server", per)
	}
}

func TestATimeoutIsReportedAsOne(t *testing.T) {
	f := newFakeTypeSafe(t)
	j := typesafeJudge(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	res := Ask(ctx, j, BuildRequest(DescribeAction("bash", map[string]any{"command": "ls"}, cwd), "t", BuildExtras{}), 0)
	if res.OK || (res.ErrorCode != "timeout" && res.ErrorCode != "aborted") {
		t.Fatalf("%+v", res)
	}
}
