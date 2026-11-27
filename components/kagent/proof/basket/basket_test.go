package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIterationAgainstFakeGateway(t *testing.T) {
	fg := newFakeGateway()
	srv := httptest.NewServer(fg)
	defer srv.Close()
	cfg := config{BaseURL: srv.URL, Agent: "kagent/bench", Headers: []string{"X-User-Id: bench"}, Approve: "auto",
		Turn2: true, Timeout: 10 * time.Second, Prompt: "run", Scenario: "basket", IdleWait: 20 * time.Millisecond}
	c := &client{cfg: cfg, http: srv.Client()}
	rr := c.iteration(context.Background(), 0, false)
	if !rr.OK {
		b, _ := json.MarshalIndent(rr, "", " ")
		t.Fatalf("not ok: %s", b)
	}
	if rr.Approval != "approved" {
		t.Errorf("approval = %q", rr.Approval)
	}
	if fg.headers.Get("X-User-Id") != "bench" || fg.headers.Get("A2A-Version") != "1.0" || !strings.Contains(fg.headers.Get("A2A-Extensions"), hitlURI) {
		t.Errorf("headers: %v", fg.headers)
	}
	for _, k := range []string{"turn1_first_event", "turn1_input_required", "approval_to_done", "turn2_first_event", "turn2_total", "usage_inputTokens"} {
		if _, ok := rr.Timings[k]; !ok {
			t.Errorf("missing timing %s: %v", k, rr.Timings)
		}
	}
	names := map[string]bool{}
	for _, p := range rr.Phases {
		names[p.Name] = true
		if p.End < p.Start {
			t.Errorf("phase %s inverted", p.Name)
		}
	}
	for _, n := range []string{"turn1", "turn1_input_required", "turn1_approval", "idle", "turn2"} {
		if !names[n] {
			t.Errorf("missing phase %s (have %v)", n, names)
		}
	}
}

func TestSummarize(t *testing.T) {
	s := summarize([]float64{1, 2, 3, 4, 100})
	if s.Median != 3 || s.Max != 100 || s.N != 5 {
		t.Errorf("%+v", s)
	}
}
