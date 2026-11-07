package langsmith

// Differential test: the vectors in testdata/caller_golden.json were produced by the real langsmith 0.10.5
// AsyncCaller / raiseForStatus / p-retry running against a loopback server (port/tools/gen/caller-differential.mjs).
// Each vector is replayed here against a raw loopback server that answers byte-for-byte what the JS server did.

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type goldenStep struct {
	Kind   string `json:"kind"`
	Status int    `json:"status"`
	Reason string `json:"reason"`
	Body   string `json:"body"` // base64
}

type goldenExpect struct {
	Outcome  string `json:"outcome"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Status   *int   `json:"status"`
	Message  string `json:"message"`
	Attempts int    `json:"attempts"`
	DelaysMs []int  `json:"delaysMs"`
}

type goldenRetry struct {
	Name       string       `json:"name"`
	MaxRetries *int         `json:"maxRetries"`
	Jitter     []float64    `json:"jitter"`
	TimeoutMs  int          `json:"timeoutMs"`
	Steps      []goldenStep `json:"steps"`
	Expect     goldenExpect `json:"expect"`
}

type goldenRaise struct {
	Name    string       `json:"name"`
	Op      string       `json:"op"`
	Consume bool         `json:"consume"`
	Step    goldenStep   `json:"step"`
	Expect  goldenExpect `json:"expect"`
}

type goldenDelay struct {
	RetriesConsumed int     `json:"retriesConsumed"`
	Jitter          float64 `json:"jitter"`
	Ms              int64   `json:"ms"`
}

type callerGolden struct {
	Retry []goldenRetry `json:"retry"`
	Raise []goldenRaise `json:"raise"`
	Delay []goldenDelay `json:"delay"`
}

func loadCallerGolden(t testing.TB) *callerGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/caller_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g callerGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

// stepServer answers the n-th request with steps[min(n, len-1)], writing the status line itself so reason
// phrases are exactly the JS server's.
type stepServer struct {
	ln    net.Listener
	steps []goldenStep
	n     atomic.Int32
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newStepServer(t *testing.T, steps []goldenStep) *stepServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &stepServer{ln: ln, steps: steps, conns: map[net.Conn]struct{}{}}
	s.wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns[c] = struct{}{}
			s.mu.Unlock()
			s.wg.Go(func() { s.serve(c) })
		}
	})
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s
}

func (s *stepServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		req, err := http.ReadRequest(r)
		if err != nil {
			return
		}
		req.Body.Close()
		step := s.steps[min(int(s.n.Add(1))-1, len(s.steps)-1)]
		switch step.Kind {
		case "reset":
			if tc, ok := c.(*net.TCPConn); ok {
				tc.SetLinger(0)
			}
			return
		case "hang":
			buf := make([]byte, 1)
			c.Read(buf) // until the client gives up and closes
			return
		}
		body, _ := base64.StdEncoding.DecodeString(step.Body)
		fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", step.Status, step.Reason, len(body))
		c.Write(body)
		return
	}
}

func (s *stepServer) url() string { return "http://" + s.ln.Addr().String() }

func goldenClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
}

func TestCallerGoldenRetry(t *testing.T) {
	g := loadCallerGolden(t)
	if len(g.Retry) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range g.Retry {
		t.Run(v.Name, func(t *testing.T) {
			srv := newStepServer(t, v.Steps)
			maxRetries := -1
			if v.MaxRetries != nil {
				maxRetries = *v.MaxRetries
			}
			c, clock := newTestCaller(callerParams{MaxRetries: maxRetries, MaxQueueSizeBytes: -1}, v.Jitter...)
			client := goldenClient()
			resp, err := c.call(context.Background(), func(ctx context.Context) (*http.Response, error) {
				ctx, cancel := context.WithTimeout(ctx, time.Duration(v.TimeoutMs)*time.Millisecond)
				defer cancel()
				req, _ := http.NewRequestWithContext(ctx, "GET", srv.url()+"/x", nil)
				r, err := client.Do(req)
				if err != nil {
					return nil, err
				}
				if err := raiseForStatus(r, "fetch /x", true); err != nil {
					return nil, err
				}
				return r, nil
			})
			e := v.Expect
			if int(srv.n.Load()) != e.Attempts {
				t.Errorf("attempts %d, want %d", srv.n.Load(), e.Attempts)
			}
			var delays []int
			for _, d := range clock.got() {
				delays = append(delays, int(d/time.Millisecond))
			}
			if fmt.Sprint(delays) != fmt.Sprint(e.DelaysMs) {
				t.Errorf("delays %v, want %v", delays, e.DelaysMs)
			}
			if e.Outcome == "ok" {
				if err != nil || resp.StatusCode != *e.Status {
					t.Errorf("want ok %d, got %v %v", *e.Status, resp, err)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error")
			}
			var se *statusError
			kind := "network"
			switch {
			case errors.As(err, &se):
				kind = "status"
				if se.Message != e.Message || se.ErrorName() != e.Name || e.Status == nil || se.Status != *e.Status {
					t.Errorf("status error %q (%s %d)\n want %q (%s %v)", se.Message, se.ErrorName(), se.Status, e.Message, e.Name, e.Status)
				}
			case errors.Is(err, context.DeadlineExceeded):
				kind = "timeout"
			}
			if kind != e.Kind {
				t.Errorf("error kind %q (%v), want %q", kind, err, e.Kind)
			}
		})
	}
}

func TestCallerGoldenRaise(t *testing.T) {
	g := loadCallerGolden(t)
	if len(g.Raise) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range g.Raise {
		t.Run(v.Name, func(t *testing.T) {
			srv := newStepServer(t, []goldenStep{v.Step})
			resp, err := goldenClient().Get(srv.url() + "/x")
			if err != nil {
				t.Fatal(err)
			}
			err = raiseForStatus(resp, v.Op, v.Consume)
			if v.Expect.Outcome == "ok" {
				if err != nil {
					t.Fatalf("want no error: %v", err)
				}
				resp.Body.Close()
				return
			}
			var se *statusError
			if !errors.As(err, &se) {
				t.Fatalf("want a statusError: %v", err)
			}
			if se.Message != v.Expect.Message {
				t.Errorf("message %q\n  want %q", se.Message, v.Expect.Message)
			}
			if se.ErrorName() != v.Expect.Name || v.Expect.Status == nil || se.Status != *v.Expect.Status {
				t.Errorf("name %s status %d, want %s %v", se.ErrorName(), se.Status, v.Expect.Name, v.Expect.Status)
			}
			if isLangSmithNotFoundError(err) != (v.Expect.Name == "LangSmithNotFoundError") ||
				isLangSmithConflictError(err) != (v.Expect.Name == "LangSmithConflictError") {
				t.Error("error name predicates disagree with the JS name")
			}
		})
	}
}

func TestCallerGoldenDelay(t *testing.T) {
	g := loadCallerGolden(t)
	if len(g.Delay) < 100 {
		t.Fatalf("only %d delay vectors", len(g.Delay))
	}
	for _, v := range g.Delay {
		if got := int64(retryDelay(v.RetriesConsumed, v.Jitter) / time.Millisecond); got != v.Ms {
			t.Errorf("retryDelay(%d, %s) = %d ms, JS %d ms", v.RetriesConsumed, strconv.FormatFloat(v.Jitter, 'g', -1, 64), got, v.Ms)
		}
	}
}
