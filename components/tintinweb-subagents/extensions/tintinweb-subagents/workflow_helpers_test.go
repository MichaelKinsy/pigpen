package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// Shared helpers of the workflow twin tests: the port of the original tests' stub hosts and run() helpers.

const wfHead = `export const meta = { name: "probe", description: "a test workflow" };` + "\n"

// wfStub is a host that never spawns anything. reply sees each request and decides what comes back; the default
// echoes the prompt so a script can assert plumbing. upstream: stubHost in test/workflow-runtime.test.ts.
type wfStub struct {
	mu      sync.Mutex
	calls   []*workflowSpawnRequest
	aborted []string
	reply   func(*workflowSpawnRequest) workflowSpawnResult
}

func wfStubHost(reply func(*workflowSpawnRequest) workflowSpawnResult) *wfStub {
	return &wfStub{reply: reply}
}

func (s *wfStub) SpawnAgent(req *workflowSpawnRequest) (workflowSpawnResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	s.mu.Unlock()
	if s.reply != nil {
		return s.reply(req), nil
	}
	return workflowSpawnResult{OK: true, Text: "ok:" + req.Prompt}, nil
}

func (s *wfStub) AbortAgent(id string) {
	s.mu.Lock()
	s.aborted = append(s.aborted, id)
	s.mu.Unlock()
}

func (s *wfStub) Calls() []*workflowSpawnRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*workflowSpawnRequest(nil), s.calls...)
}

func (s *wfStub) Aborted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.aborted...)
}

// wfRun runs body as a workflow with the meta header prepended.
func wfRun(t *testing.T, body string, mod func(*workflowRunOptions)) *workflowRunResult {
	t.Helper()
	opts := workflowRunOptions{Script: wfHead + body}
	if mod != nil {
		mod(&opts)
	}
	res, err := runWorkflow(opts)
	if err != nil {
		t.Fatalf("runWorkflow: %v", err)
	}
	return res
}

func wfRunErr(body string, mod func(*workflowRunOptions)) error {
	opts := workflowRunOptions{Script: wfHead + body}
	if mod != nil {
		mod(&opts)
	}
	_, err := runWorkflow(opts)
	return err
}

func wfAgentRows(progress []workflowEntry) []*workflowAgentEntry {
	var out []*workflowAgentEntry
	for _, e := range progress {
		if a, ok := e.(*workflowAgentEntry); ok {
			out = append(out, a)
		}
	}
	return out
}

func wfLastRow(progress []workflowEntry) *workflowAgentEntry {
	rows := wfAgentRows(progress)
	if len(rows) == 0 {
		return nil
	}
	return rows[len(rows)-1]
}

// wfUntil waits for predicate to hold, so a test never races the worker.
func wfUntil(t *testing.T, predicate func() bool, what string) {
	t.Helper()
	for i := 0; i < 800; i++ {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// wfJSON decodes JSON text the way a script's result is decoded.
func wfJSON(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %q", text)
	}
	return v
}

func wfCancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// recorder records the journal a run would have written, so the next run can replay it.
type wfRecorder struct {
	mu      sync.Mutex
	entries []workflowJournalEntry
}

func (r *wfRecorder) append(e workflowJournalEntry) {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	r.mu.Unlock()
}

func (r *wfRecorder) journal() *workflowJournalOptions {
	return &workflowJournalOptions{Append: r.append}
}

func wfStr(s string) *string { return &s }
