package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A rig for the nested tools: the session of a child that may delegate, its children fake processes.

// writeAgentFile writes <cwd>/.pi/agents/<name>.md with the frontmatter lines (as nested-tools.test.ts writeAgent).
func writeAgentFile(t *testing.T, cwd, name, extra string) {
	t.Helper()
	dir := filepath.Join(cwd, ".pi", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("---\ndescription: "+name+"\ntools: read\n"+extra+"---\n"+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startNestedRig starts a session in cwd whose app is the child described by cfg. The tools are called through
// call, the way the SDK handlers do, with the call's own view.
func startNestedRig(t *testing.T, cwd string, cfg *nestedConfig) *rig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	r := startRigIn(t, cwd)
	r.app.nested = cfg
	return r
}

// scoutRig is the rig most cases use: agents "scout" and "reviewer" in the workspace, the parent "parent-1" at depth 1
// of 2 that may use any agent.
func scoutRig(t *testing.T) (*rig, string) {
	t.Helper()
	cwd := t.TempDir()
	writeAgentFile(t, cwd, "scout", "")
	writeAgentFile(t, cwd, "reviewer", "")
	return startNestedRig(t, cwd, &nestedConfig{AgentID: "parent-1", Depth: 1, MaxDepth: 2, Allowed: "all", ConfigCwd: cwd}), cwd
}

var stubView = parentView{
	SystemPrompt: func() string { return "" },
	Env:          func(string) envInfo { return envInfo{} },
	Model:        func() string { return "" },
	Thinking:     func() string { return "" },
}

type nestedOut struct {
	Text    string
	IsError bool
	Err     error
}

// nestedExec calls a nested tool and returns what it answered. done is the call's cancellation (nil: never).
func nestedExec(r *rig, tool string, params obj, done <-chan struct{}) nestedOut {
	c := nestedCall{Done: done, View: stubView}
	var res struct {
		Content string
		IsError bool
	}
	var err error
	switch tool {
	case "Agent":
		x, e := r.app.nestedAgent(c, params)
		res.Content, res.IsError, err = x.Content, x.IsError, e
	case "get_subagent_result":
		x, e := r.app.nestedResult(done, params)
		res.Content, res.IsError, err = x.Content, x.IsError, e
	case "steer_subagent":
		x, e := r.app.nestedSteer(params)
		res.Content, res.IsError, err = x.Content, x.IsError, e
	default:
		panic("no such nested tool " + tool)
	}
	return nestedOut{res.Content, res.IsError, err}
}

// nestedAsync starts a call that blocks (a foreground spawn).
func nestedAsync(r *rig, tool string, params obj, done <-chan struct{}) <-chan nestedOut {
	ch := make(chan nestedOut, 1)
	go func() { ch <- nestedExec(r, tool, params, done) }()
	return ch
}

func (o nestedOut) must(t *testing.T) nestedOut {
	t.Helper()
	if o.Err != nil {
		t.Fatalf("the tool failed: %v", o.Err)
	}
	return o
}

func recv(t *testing.T, ch <-chan nestedOut) nestedOut {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(10 * time.Second):
		t.Fatal("the tool call did not return")
	}
	return nestedOut{}
}

// plant puts a record into the manager as another agent's or a hand-made one (the original writes its fake manager's map).
func plant(r *rig, rec *agentRecord) *agentRecord {
	if rec.done == nil {
		rec.done = make(chan struct{})
	}
	r.app.mgr.mu.Lock()
	r.app.mgr.records[rec.ID] = rec
	r.app.mgr.order = append(r.app.mgr.order, rec.ID)
	r.app.mgr.mu.Unlock()
	return rec
}

func setStatus(r *rig, rec *agentRecord, status, result string) {
	r.app.mgr.mu.Lock()
	rec.Status, rec.Result = status, result
	r.app.mgr.mu.Unlock()
}

func toolJSON(raw json.RawMessage) (text string, isError bool) {
	var res struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.Content, res.IsError
}
