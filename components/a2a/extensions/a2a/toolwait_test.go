package a2aext_test

// The tools for tasks that outlive a2a_send: a2a_send {wait:false} starts one, a2a_task {action:"wait"} follows it.

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	a2aext "github.com/MichaelKinsy/pigpen/a2a"
)

// gateWorker streams "first half " and waits for the gate before "second half".
type gateWorker struct {
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func newGateWorker() *gateWorker {
	return &gateWorker{gate: make(chan struct{}), started: make(chan struct{})}
}

func (w *gateWorker) Run(ctx context.Context, t a2aext.Turn, up func(a2aext.Update)) (a2aext.Result, error) {
	up(a2aext.Update{Text: "first half "})
	w.once.Do(func() { close(w.started) })
	select {
	case <-w.gate:
	case <-ctx.Done():
		return a2aext.Result{}, ctx.Err()
	}
	up(a2aext.Update{Text: "second half"})
	return a2aext.Result{Text: "first half second half"}, nil
}

func gatePeer(t *testing.T) (*gateWorker, *Host) {
	t.Helper()
	w := newGateWorker()
	cfg := a2aext.Config{Listen: "127.0.0.1:0", Name: "peer-pig", MaxConcurrentTasks: 2, TaskTimeoutSeconds: 30,
		Tokens: []a2aext.TokenConfig{{Name: "caller", TokenEnv: "PEER_TOKEN"}}}
	s, err := a2aext.NewServer(cfg, w, mapEnv(map[string]string{"PEER_TOKEN": extTokenB}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return w, hostWithRemote(t, "http://"+s.Addr())
}

var taskIDRE = regexp.MustCompile(`taskId: ([0-9a-f-]{36})`)

func TestSendWithWaitFalseReturnsTheRunningTask(t *testing.T) {
	w, h := gatePeer(t)
	raw, fail := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "background", "wait": false})
	if fail != "" {
		t.Fatal(fail)
	}
	s := string(raw)
	m := taskIDRE.FindStringSubmatch(s)
	if m == nil || !strings.Contains(s, "contextId") {
		t.Fatalf("the model needs the ids: %s", s)
	}
	if strings.Contains(s, "completed") || !strings.Contains(s, "a2a_task") || !strings.Contains(s, "wait") {
		t.Fatalf("it must say the task is still going and how to follow it: %s", s)
	}
	waitFor(t, w.started)
	close(w.gate)
	got, fail := h.Tool("a2a_task", map[string]any{"agent": "peer", "action": "wait", "taskId": m[1]})
	if fail != "" || !strings.Contains(string(got), "completed") || !strings.Contains(string(got), "first half second half") {
		t.Fatalf("%s %s", got, fail)
	}
}

func waitFor(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

func TestTaskWaitReturnsTheCurrentStateWhenTheBudgetRunsOut(t *testing.T) {
	w, h := gatePeer(t)
	raw, _ := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "slow", "wait": false})
	id := taskIDRE.FindStringSubmatch(string(raw))[1]
	waitFor(t, w.started)
	start := time.Now()
	got, fail := h.Tool("a2a_task", map[string]any{"agent": "peer", "action": "wait", "taskId": id, "waitSeconds": 1})
	if fail != "" {
		t.Fatalf("running out of time is not an error: %s", fail)
	}
	s := string(got)
	if time.Since(start) > 8*time.Second || !strings.Contains(s, "state: working") || !strings.Contains(s, "first half") || !strings.Contains(s, "not finished") {
		t.Fatalf("%s", s)
	}
	close(w.gate)
	if got, fail := h.Tool("a2a_task", map[string]any{"agent": "peer", "action": "wait", "taskId": id, "waitSeconds": 10}); fail != "" || !strings.Contains(string(got), "second half") {
		t.Fatalf("%s %s", got, fail)
	}
}

func TestTaskWaitArgumentsAreChecked(t *testing.T) {
	_, h := gatePeer(t)
	for name, c := range map[string]struct {
		params map[string]any
		want   string
	}{
		"no task id":     {map[string]any{"agent": "peer", "action": "wait"}, "taskId is required"},
		"zero seconds":   {map[string]any{"agent": "peer", "action": "wait", "taskId": "x", "waitSeconds": 0}, "waitSeconds"},
		"negative":       {map[string]any{"agent": "peer", "action": "wait", "taskId": "x", "waitSeconds": -5}, "waitSeconds"},
		"fraction":       {map[string]any{"agent": "peer", "action": "wait", "taskId": "x", "waitSeconds": 1.5}, "waitSeconds"},
		"not a number":   {map[string]any{"agent": "peer", "action": "wait", "taskId": "x", "waitSeconds": "soon"}, "waitSeconds"},
		"too long":       {map[string]any{"agent": "peer", "action": "wait", "taskId": "x", "waitSeconds": 100000}, "waitSeconds"},
		"unknown task":   {map[string]any{"agent": "peer", "action": "wait", "taskId": "no-such-task"}, "peer"},
		"wait not bool":  {map[string]any{"agent": "peer", "message": "hello there", "wait": "no"}, "wait must be a boolean"},
		"seconds on get": {map[string]any{"agent": "peer", "action": "get", "taskId": "x", "waitSeconds": 5}, "waitSeconds"},
	} {
		tool := "a2a_task"
		if _, ok := c.params["message"]; ok {
			tool = "a2a_send"
		}
		if _, fail := h.Tool(tool, c.params); !strings.Contains(fail, c.want) {
			t.Errorf("%s: want a tool error saying %q, got %q", name, c.want, fail)
		}
	}
}

func TestSendStillWaitsByDefault(t *testing.T) {
	_, url := remote(t)
	h := hostWithRemote(t, url)
	raw, fail := h.Tool("a2a_send", map[string]any{"agent": "peer", "message": "default is to wait", "wait": true})
	if fail != "" || !strings.Contains(string(raw), "pong: default is to wait") {
		t.Fatalf("%s %s", raw, fail)
	}
}
