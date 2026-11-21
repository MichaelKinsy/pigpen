package pi_subagents

// The fleet's sidecar (sidecar.go): what a session writes of its runs and what a resumed session gets back. The
// registry is a package global, so every test here binds its own directory and leaves the registry empty.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sidecarTestDir resets the registry, unbinds persistence and returns an empty session directory; the cleanup leaves
// the registry as it found it for the tests that share it.
func sidecarTestDir(t *testing.T) string {
	t.Helper()
	fleet.reset()
	fleet.bindPersist("", "")
	t.Cleanup(func() {
		fleet.reset()
		fleet.bindPersist("", "")
	})
	return tmp(t)
}

// sidecarRaw reads a session's sidecar as written.
func sidecarRaw(t *testing.T, dir, sessionID string) string {
	t.Helper()
	data, err := os.ReadFile(sidecarPath(dir, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// liveRun is the registry's own record (a copy would not carry the control state these tests assert on).
func liveRun(t *testing.T, id string) *runRecord {
	t.Helper()
	r := fleet.findLive(id)
	if r == nil {
		t.Fatalf("run %s is not in the registry", id)
	}
	return r
}

func TestSidecarRoundTrip(t *testing.T) {
	dir := sidecarTestDir(t)
	fleet.bindPersist(dir, "s1")

	// a run as the session ran it: launched, going, finished with its child session pinned
	bin := os.Args[0] // an existing program: the restore keeps the recorded one
	run := fleet.beginRun("single", "delegate", "do it", "C:/work")
	run.setTurnNotice("Background task")
	st := run.addStep("delegate", "do it", "high")
	st.begin()
	run.setLaunch(childRequest{Bin: bin, Args: []string{"--print", "--session-dir", dir, "--session-id", "child-1"},
		Stdin: "Task: do it", Cwd: "C:/work"}, dir, "child-1")
	st.pinSession(dir, "child-1")
	st.finish(stateComplete, "the answer", "", filepath.Join(dir, "child-1.jsonl"), 0)

	raw := sidecarRaw(t, dir, "s1")
	if strings.Contains(raw, `"env"`) {
		t.Errorf("the child's environment must never be written: %s", raw)
	}
	for _, want := range []string{`"session": "s1"`, `"id": "run-1"`, `"state": "complete"`, `"output": "the answer"`, `"session": "`} {
		if !strings.Contains(raw, want) {
			t.Errorf("sidecar is missing %s: %s", want, raw)
		}
	}

	fleet.reset()
	if !fleet.restore(dir, "s1") {
		t.Fatal("the sidecar did not restore")
	}
	r := liveRun(t, "run-1")
	eq(t, r.mode, "single", "mode")
	eq(t, r.name, "delegate", "name")
	eq(t, r.task, "do it", "task")
	eq(t, r.cwd, "C:/work", "cwd")
	eq(t, r.state(), stateComplete, "state")
	eq(t, len(r.steps), 1, "steps")
	eq(t, r.steps[0].output, "the answer", "output")
	eq(t, r.steps[0].session, filepath.Join(dir, "child-1.jsonl"), "child session")
	eq(t, r.turnNotice, "Background task", "turn notice")
	eq(t, r.childReq.Bin, bin, "the recorded program still exists, so it is kept")
	eq(t, r.childReq.Stdin, "Task: do it", "stdin")
	eq(t, len(r.childReq.Args), 5, "arguments")
	if len(r.childReq.Env) == 0 {
		t.Error("the restore rebuilt no environment for the child")
	}
	hasSubagent := false
	for _, kv := range r.childReq.Env {
		hasSubagent = hasSubagent || strings.HasPrefix(kv, "PIG_SUBAGENT=1")
	}
	if !hasSubagent {
		t.Errorf("the rebuilt environment is not a subagent's: %v", r.childReq.Env)
	}

	// ids continue past the restored ones: the transcript's run-7 must not be reused
	eq(t, fleet.beginRun("single", "delegate", "again", "C:/work").id, "run-2", "next id")
}

func TestSessionShutdownRecordsGoingRunsPaused(t *testing.T) {
	dir := sidecarTestDir(t)
	fleet.bindPersist(dir, "s1")

	run := fleet.beginRun("single", "delegate", "a long task", "C:/work")
	run.setLaunch(childRequest{Bin: "pig", Args: []string{"--print"}, Cwd: "C:/work"}, dir, "child-1")
	st := run.addStep("delegate", "a long task", "")
	st.begin() // still going when the session ends

	fleet.finalizePersist()
	raw := sidecarRaw(t, dir, "s1")
	if !strings.Contains(raw, `"state": "paused"`) {
		t.Errorf("a run that had not finished must be recorded paused: %s", raw)
	}

	// stopAll's cancellation finishes the step after the snapshot; it must not rewrite the file as stopped
	st.finish(stateStopped, "", "delegate was cancelled", "", 0)
	if raw := sidecarRaw(t, dir, "s1"); strings.Contains(raw, `"stopped"`) {
		t.Errorf("the shutdown's cancellations overwrote the snapshot: %s", raw)
	}

	fleet.reset()
	if !fleet.restore(dir, "s1") {
		t.Fatal("the sidecar did not restore")
	}
	r := liveRun(t, "run-1")
	eq(t, r.state(), statePaused, "state")
	eq(t, r.isLive(), false, "a restored run is not live")
	eq(t, r.resumable(), true, "its child session can be revived")
	if r.steps[0].endedAt.IsZero() {
		t.Error("the paused step must end where the session did")
	}
}

func TestRestoreIgnoresUnusableSidecars(t *testing.T) {
	dir := sidecarTestDir(t)

	fleet.reset()
	if fleet.restore(dir, "s1") {
		t.Error("no file restores nothing")
	}

	writeFile(t, sidecarPath(dir, "s1"), "{not json")
	fleet.reset()
	if fleet.restore(dir, "s1") {
		t.Error("a corrupt file restores nothing")
	}
	eq(t, len(fleet.snapshot()), 0, "no runs from a corrupt file")

	writeFile(t, sidecarPath(dir, "s1"), `{"version":1,"session":"another","next":1,"runs":[]}`)
	fleet.reset()
	if fleet.restore(dir, "s1") {
		t.Error("another session's file restores nothing")
	}

	// a record without a child step is dropped, but the ids it names still advance the counter
	writeFile(t, sidecarPath(dir, "s1"), `{"version":1,"session":"s1","next":3,"runs":[`+
		`{"id":"run-2","mode":"single","name":"x","task":"t","cwd":"c","child":{},"steps":[]}]}`)
	fleet.reset()
	if fleet.restore(dir, "s1") {
		t.Error("a record with no child step has nothing to restore")
	}
	eq(t, fleet.beginRun("single", "x", "t", "c").id, "run-3", "the counter continues past the dropped record")
}
