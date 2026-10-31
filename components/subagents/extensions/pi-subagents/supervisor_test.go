package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The supervisor channel: a child's question over files, the parent's tool
// actions with the original's texts.

func TestSupervisorDiscoveryAndReply(t *testing.T) {
	isolateTemp(t)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		<-done
		return childResult{}, errCancelled
	})
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: &AgentConfig{Name: "helper"}, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	// Wait for the running status write to settle (a concurrent rename on
	// Windows can briefly hide status.json).
	for i := 0; i < 200; i++ {
		st := readStatusFile(r.Dir)
		if st != nil && len(st.Steps) > 0 && st.Steps[0].Agent == "helper" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The child's channel exists in the run's directory.
	reqDir := filepath.Join(supervisorDir(r.Dir), "requests")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(reqDir, "q-1.json"), `{"id":"q-1","message":"Ship it or hold?","ts":1}`)

	pending := discoverSupervisorRequests("", "s1")
	if len(pending) != 1 {
		t.Fatalf("pending: %+v", pending)
	}
	eq(t, pending[0].ID, "q-1", "id")
	eq(t, pending[0].RunID, r.ID, "run")
	eq(t, pending[0].Agent, "helper", "agent")
	// Another session sees nothing.
	if len(discoverSupervisorRequests("", "other")) != 0 {
		t.Error("session scoping")
	}
	// The pending line is the original's shape.
	line := formatPendingSupervisorLine(pending[0])
	if !strings.Contains(line, "- q-1: helper [#0] decision. Reply: subagent_supervisor({ action: \"reply\", replyTo: \"q-1\", message: \"...\" })") {
		t.Errorf("pending line: %s", line)
	}
	if !strings.Contains(line, "\n  Ship it or hold?") {
		t.Errorf("message line: %s", line)
	}
	// A running run with a pending question reports needs attention.
	view := viewOfRun(r)
	status := formatRunStatus(view)
	if !strings.Contains(status, "Activity: needs attention") {
		t.Errorf("status: %s", status)
	}
	if !runHasPendingSupervisor(r.ID, "s1") {
		t.Error("attention signal")
	}
	// The reply file resolves the request.
	if err := os.MkdirAll(filepath.Dir(pending[0].ReplyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, pending[0].ReplyPath, `{"message":"Ship it.","ts":2}`)
	if len(discoverSupervisorRequests("", "s1")) != 0 {
		t.Error("replied request still pending")
	}
	if runHasPendingSupervisor(r.ID, "s1") {
		t.Error("attention after reply")
	}
	// The async child's stdin carries the protocol and its env the channel dir.
	// (Covered end-to-end in the host test; here the prompt text itself.)
instructions := supervisorPromptInstructions("/chan")
	if !strings.Contains(instructions, "Supervisor channel:") || !strings.Contains(instructions, filepath.Join("/chan", "requests")) || !strings.Contains(instructions, filepath.Join("/chan", "replies")) {
		t.Errorf("instructions: %s", instructions)
	}
}

func TestAsyncChildCarriesTheChannel(t *testing.T) {
	isolateTemp(t)
	var gotReq childRequest
	started := make(chan struct{}, 1)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		gotReq = req
		started <- struct{}{}
		<-done
		return childResult{}, errCancelled
	})
	r, err := startDetachedRun(asyncRunRequest{SessionID: "s1", Cwd: t.TempDir(), Agent: &AgentConfig{Name: "helper"}, Task: "t", TimeoutMs: 60000}, "s1", nil, nil)
	eq(t, err, nil, "start")
	<-started
	if !strings.HasPrefix(gotReq.Stdin, "Task: t") || !strings.Contains(gotReq.Stdin, "Supervisor channel:") {
		t.Errorf("stdin: %s", gotReq.Stdin)
	}
	found := false
	for _, kv := range gotReq.Env {
		if kv == supervisorDirEnv+"="+filepath.Join(supervisorDir(r.Dir)) {
			found = true
		}
	}
	if !found {
		t.Errorf("env missing %s: %v", supervisorDirEnv, gotReq.Env)
	}
	r.requestStop()
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
}
