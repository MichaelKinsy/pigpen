package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of steering-action.test.ts (the in-process child session of its first case is the childSession interface here: the
// consumer twins are in steer_child_test.go).

type saEnv struct {
	root string
	dirs asyncDirs
}

func newSaEnv(t *testing.T) saEnv {
	t.Helper()
	withTempHome(t)
	root := t.TempDir()
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", root)
	return saEnv{root: root, dirs: asyncDirs{Results: filepath.Join(root, "results"), Async: filepath.Join(root, "async")}}
}

func (e saEnv) dir(id string) string { return filepath.Join(e.dirs.Async, id) }

// saStatus is a running status of the original's runningStatus helper.
func saStatus(runID, mode string, count int) *AsyncStatus {
	steps := make([]map[string]any, count)
	for i := range steps {
		steps[i] = map[string]any{"agent": "worker-" + string(rune('0'+i)), "status": "running", "startedAt": nowMs()}
	}
	b, _ := json.Marshal(map[string]any{"runId": runID, "sessionId": "session", "mode": mode, "state": "running", "pid": 12345, "cwd": os.TempDir(),
		"startedAt": nowMs(), "lastUpdate": nowMs(), "steps": steps, "steering": createSteeringStatus()})
	var st AsyncStatus
	if err := json.Unmarshal(b, &st); err != nil {
		panic(err)
	}
	return &st
}

func saWrite(t *testing.T, dir string, st *AsyncStatus) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeStatus(dir, st); err != nil {
		t.Fatal(err)
	}
}

// saProject is projectRequest: the request recorded in the ledger of the status and of each of its steps.
func saProject(st *AsyncStatus, req steerRequest, states ...string) {
	targets := make([]steeringTarget, len(states))
	for i, s := range states {
		targets[i] = steeringTarget{Index: i, State: s}
	}
	l := statusSteering(st)
	if l == nil {
		l = createSteeringStatus()
	}
	recordSteeringRequest(l, steerRequestInput{ID: req.ID, RequestedAt: int64(req.TS), Source: req.Source, Message: req.Message, Targets: targets})
	setStatusSteering(st, l)
	for _, tg := range targets {
		if tg.Index >= len(st.Steps) {
			continue
		}
		ss := createSteeringStatus()
		recordSteeringRequest(ss, steerRequestInput{ID: req.ID, RequestedAt: int64(req.TS), Source: req.Source, Message: req.Message, Targets: []steeringTarget{tg}})
		setStepSteering(&st.Steps[tg.Index], ss)
	}
}

// saUpdate moves a target of the status ledger and of its step.
func saUpdate(st *AsyncStatus, id string, index int, state string) {
	l := statusSteering(st)
	updateSteeringTarget(l, id, index, state, nowMs(), steeringTargetFields{})
	setStatusSteering(st, l)
	if ss := stepSteering(&st.Steps[index]); ss != nil {
		updateSteeringTarget(ss, id, index, state, nowMs(), steeringTargetFields{})
		setStepSteering(&st.Steps[index], ss)
	}
}

func saReadRequestFile(t *testing.T, p string) steerRequest {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	r := parseSteerRequest(b)
	if r == nil {
		t.Fatalf("not a steer request: %s", b)
	}
	return *r
}

// saReadRequest waits for the steer request the action wrote.
func saReadRequest(t *testing.T, asyncDir string) steerRequest {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(steerRequestsDir(asyncDir)); err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") {
					return saReadRequestFile(t, filepath.Join(steerRequestsDir(asyncDir), e.Name()))
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the steer request")
	return steerRequest{}
}

func saRecoveryDescriptor(t *testing.T, runID string) map[string]any {
	t.Helper()
	b, err := createRunFanoutBudget(runID, 64)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"version": 1, "runFanoutBudget": map[string]any{"version": 1, "rootRunId": b.RootRunID, "directory": b.Directory, "limit": b.Limit},
		"sourceRunId": runID, "agent": "worker-0", "cwd": os.TempDir(), "systemPromptMode": "replace", "inheritGlobalContext": false, "inheritProjectContext": false,
		"inheritSkills": false, "outputMode": "inline", "absoluteDeadlineAt": nowMs() + 10_000, "initialToolBudget": map[string]any{"soft": 8, "hard": 12, "block": []string{"read"}},
		"maxSubagentDepth": 2, "share": false}
}

func saRun(in steerRunInput) <-chan steerReply {
	ch := make(chan steerReply, 1)
	go func() { ch <- steerAsyncRun(in) }()
	return ch
}

func saBase(e saEnv, runID, message string) steerRunInput {
	return steerRunInput{CurrentSessionID: "session", RunID: runID, Message: message, AsyncDir: e.dir(runID), Dirs: e.dirs, ResultsDir: e.dirs.Results,
		Kill: func(pid, signal int) error { return nil }}
}

func TestSteeringActionTwins(t *testing.T) {
	const f = "steering-action"
	idx := func(i int) *int { return &i }
	i64 := func(v int64) *int64 { return &v }
	text := func(r steerReply) string { return r.Text }

	tw(t, f, "does not acknowledge queued steering when the child transport rejects it", func(t *testing.T) {
		// The child session is the childSession interface of the pig --mode rpc child; a steer it rejects is "failed" with the
		// transport's message and is not recorded as accepted (the consumer's twin is TestSteerConsumerRejection in steer_child_test.go).
		var outcomes []string
		c := &fakeChild{steerErr: os.ErrInvalid}
		router := newSteerRouter(c, func(_ steerRequest, d steerDelivery) { outcomes = append(outcomes, d.State) })
		got := router.steer(steerRequest{Type: "steer", ID: "rejected", TS: 1, Message: "new direction"})
		equal(t, [2]string{got.State, got.Message}, [2]string{"failed", os.ErrInvalid.Error()}, "result")
		equal(t, len(outcomes), 0, "outcomes")
	})

	tw(t, f, "returns delivered only after the runner records child-session acceptance", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-delivered"
		saWrite(t, e.dir(runID), saStatus(runID, "single", 1))
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs = i64(500)
		action := saRun(in)
		req := saReadRequest(t, e.dir(runID))
		equal(t, req.TargetIndexes, []int{0}, "target indexes")
		st := saStatus(runID, "single", 1)
		saProject(st, req, "routed")
		saUpdate(st, req.ID, 0, "delivered")
		saWrite(t, e.dir(runID), st)
		r := <-action
		equal(t, r.IsError, false, "isError")
		equal(t, r.Steering.State, "delivered", "state")
		polMatches(t, r.Text, "Steering delivered")
		polMatches(t, r.Text, "Message sent:\n```text\ncorrect course\n```")
	})

	tw(t, f, "returns a tool error when the runner has closed its steering inbox", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-closed"
		saWrite(t, e.dir(runID), saStatus(runID, "single", 1))
		closeSteerInbox(e.dir(runID), "complete")
		r := steerAsyncRun(saBase(e, runID, "too late"))
		equal(t, r.IsError, true, "isError")
		polMatches(t, r.Text, "no longer accepts steering requests")
	})

	tw(t, f, "queues follow-up as the next revival brief for a completed retained child", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-retained"
		dir := e.dir(runID)
		sessionFile := filepath.Join(dir, "child.jsonl")
		os.MkdirAll(dir, 0o755)
		writeFile(t, sessionFile, "")
		st := saStatus(runID, "single", 1)
		st.State = "complete"
		st.EndedAt = new(ms(nowMs()))
		st.SessionFile = sessionFile
		st.Extra["parentWorkflowRunId"] = json.RawMessage(`"workflow"`)
		st.Steps = []AsyncStep{{Agent: "worker-0", Status: "complete", SessionFile: sessionFile}}
		saWrite(t, dir, st)
		in := saBase(e, runID, "Review the docs")
		in.Mode = steerModeFollowUp
		r := steerAsyncRun(in)
		equal(t, r.IsError, false, "isError")
		equal(t, r.Steering.DeliveryStatus, "queued", "delivery status")
		polMatches(t, r.Text, "next resume")
		queueDir := filepath.Join(dir, "control", "revival-briefs")
		entries, _ := os.ReadDir(queueDir)
		queued := saReadRequestFile(t, filepath.Join(queueDir, entries[0].Name()))
		equal(t, [2]string{queued.Message, string(queued.Mode)}, [2]string{"Review the docs", "follow_up"}, "queued brief")
	})

	tw(t, f, "does not commit recovery when the caller aborts the acknowledgment wait", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-abort"
		saWrite(t, e.dir(runID), saStatus(runID, "single", 1))
		interrupted, recovered := false, false
		in := saBase(e, runID, "correct course")
		in.Cancelled = func() bool { return true }
		in.Kill = func(pid, signal int) error {
			if signal != 0 {
				interrupted = true
			}
			return nil
		}
		in.Recover = func(steeringRecoveryLimits) steerRecoverResult {
			recovered = true
			return steerRecoverResult{AsyncID: "replacement"}
		}
		r := steerAsyncRun(in)
		equal(t, r.Steering.State, "pending", "state")
		equal(t, [2]bool{interrupted, recovered}, [2]bool{false, false}, "interrupted, recovered")
		equal(t, ccExists(filepath.Join(e.dir(runID), "control", "steer-recovery")), false, "recovery claim")
	})

	// queuedAck runs the action with a status that records the request as routed in OnRequestQueued and as queued (or delivered) when
	// ack says so; it returns what the callbacks saw.
	type outcome struct {
		interrupted, recovered, attempted, claimed bool
		request                                    steerRequest
	}
	run := func(t *testing.T, name string, hooks func(e saEnv, runID string, o *outcome, in *steerRunInput)) (steerReply, *outcome, saEnv, string) {
		e := newSaEnv(t)
		runID := name
		saWrite(t, e.dir(runID), saStatus(runID, "single", 1))
		o := &outcome{}
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs = i64(25)
		in.Kill = func(pid, signal int) error {
			if signal != 0 {
				o.interrupted = true
			}
			return nil
		}
		in.Recover = func(steeringRecoveryLimits) steerRecoverResult {
			o.recovered = true
			return steerRecoverResult{AsyncID: "replacement"}
		}
		hooks(e, runID, o, &in)
		return steerAsyncRun(in), o, e, runID
	}
	markerOf := func(e saEnv, runID string, o *outcome) string {
		return filepath.Join(e.dir(runID), "control", "steer-recovery", base64URL(o.request.ID)+".json")
	}

	tw(t, f, "does not recover after the runner records queued acceptance", func(t *testing.T) {
		r, o, e, runID := run(t, "steer-queued-ack", func(e saEnv, runID string, o *outcome, in *steerRunInput) {
			in.OnRequestQueued = func(p string) {
				o.request = saReadRequestFile(t, p)
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saUpdate(st, o.request.ID, 0, "queued")
				saWrite(t, e.dir(runID), st)
			}
			in.OnBeforeRecoveryClaim = func(string, int64) { o.attempted = true }
		})
		equal(t, r.IsError, false, "isError")
		equal(t, r.Steering.DeliveryStatus, "queued", "delivery")
		polMatches(t, r.Text, "Steering queued")
		equal(t, [3]bool{o.interrupted, o.recovered, o.attempted}, [3]bool{false, false, false}, "interrupted, recovered, attempted")
		equal(t, ccExists(markerOf(e, runID, o)), false, "marker")
	})

	tw(t, f, "cancels in-claim recovery when queued acceptance arrives before the claim", func(t *testing.T) {
		r, o, e, runID := run(t, "steer-queued-before-claim", func(e saEnv, runID string, o *outcome, in *steerRunInput) {
			in.OnRequestQueued = func(p string) {
				o.request = saReadRequestFile(t, p)
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saWrite(t, e.dir(runID), st)
			}
			in.OnBeforeRecoveryClaim = func(string, int64) {
				o.attempted = true
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saUpdate(st, o.request.ID, 0, "queued")
				saWrite(t, e.dir(runID), st)
			}
		})
		equal(t, r.IsError, false, "isError")
		equal(t, r.Steering.DeliveryStatus, "queued", "delivery")
		polMatches(t, r.Text, "Steering queued")
		equal(t, [3]bool{o.interrupted, o.recovered, o.attempted}, [3]bool{false, false, true}, "interrupted, recovered, attempted")
		equal(t, ccExists(interruptRequestPath(e.dir(runID))), false, "interrupt request")
		equal(t, ccExists(filepath.Join(e.dir(runID), "control", "steer-recovery", "claim.json")), false, "claim")
		equal(t, ccExists(markerOf(e, runID, o)), false, "marker")
	})

	tw(t, f, "cancels in-claim recovery when queued acceptance arrives after the claim", func(t *testing.T) {
		r, o, e, runID := run(t, "steer-queued-after-claim", func(e saEnv, runID string, o *outcome, in *steerRunInput) {
			in.OnRequestQueued = func(p string) {
				o.request = saReadRequestFile(t, p)
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saWrite(t, e.dir(runID), st)
			}
			in.OnRecoveryCommitted = func(string, int64) {
				o.claimed = true
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saUpdate(st, o.request.ID, 0, "queued")
				saWrite(t, e.dir(runID), st)
			}
		})
		equal(t, r.IsError, false, "isError")
		equal(t, r.Steering.DeliveryStatus, "queued", "delivery")
		polMatches(t, r.Text, "Steering queued")
		equal(t, [3]bool{o.interrupted, o.recovered, o.claimed}, [3]bool{false, false, true}, "interrupted, recovered, claimed")
		equal(t, ccExists(interruptRequestPath(e.dir(runID))), false, "interrupt request")
		equal(t, ccExists(filepath.Join(e.dir(runID), "control", "steer-recovery", "claim.json")), false, "claim")
		equal(t, ccExists(markerOf(e, runID, o)), false, "marker")
	})

	tw(t, f, "honors an acknowledgment persisted before recovery commit without interrupting", func(t *testing.T) {
		r, o, e, runID := run(t, "steer-final-ack", func(e saEnv, runID string, o *outcome, in *steerRunInput) {
			in.OnRequestQueued = func(p string) {
				o.request = saReadRequestFile(t, p)
				st := saStatus(runID, "single", 1)
				saProject(st, o.request, "routed")
				saUpdate(st, o.request.ID, 0, "delivered")
				saWrite(t, e.dir(runID), st)
			}
			in.OnBeforeRecoveryClaim = func(string, int64) { o.attempted = true }
		})
		equal(t, r.Steering.State, "delivered", "state")
		equal(t, [3]bool{o.interrupted, o.recovered, o.attempted}, [3]bool{false, false, false}, "interrupted, recovered, attempted")
		equal(t, ccExists(markerOf(e, runID, o)), false, "marker")
	})

	tw(t, f, "keeps the claim after an unconfirmed pause to prevent delayed duplicate recovery", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-pause-unconfirmed"
		saWrite(t, e.dir(runID), saStatus(runID, "single", 1))
		type kill struct{ pid, signal int }
		var kills []kill
		claimed := false
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs, in.RecoveryTimeoutMs = i64(25), i64(50)
		in.Kill = func(pid, signal int) error { kills = append(kills, kill{pid, signal}); return nil }
		in.OnRequestQueued = func(p string) {
			st := saStatus(runID, "single", 1)
			saProject(st, saReadRequestFile(t, p), "routed")
			saWrite(t, e.dir(runID), st)
		}
		in.OnRecoveryCommitted = func(string, int64) { claimed = true }
		in.Recover = func(steeringRecoveryLimits) steerRecoverResult { return steerRecoverResult{AsyncID: "replacement"} }
		r := steerAsyncRun(in)
		equal(t, claimed, true, "claimed")
		polMatches(t, r.Text, "claim remains committed to prevent a delayed duplicate")
		equal(t, ccExists(interruptRequestPath(e.dir(runID))), true, "interrupt request")
		equal(t, kills, []kill{{12345, 0}}, "kills")
		equal(t, ccExists(filepath.Join(e.dir(runID), "control", "steer-recovery", "claim.json")), true, "claim")
	})

	tw(t, f, "pauses and revives a single run with only its remaining budgets", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-recover"
		dir := e.dir(runID)
		sessionFile := filepath.Join(dir, "child.jsonl")
		saWrite(t, dir, saStatus(runID, "single", 1))
		writeFile(t, sessionFile, "")
		descPath := filepath.Join(dir, "recovery-descriptor.json")
		if err := asyncWritePrivateJSON(descPath, saRecoveryDescriptor(t, runID)); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(descPath); info.Mode().Perm() != 0o600 {
			t.Errorf("descriptor mode %o", info.Mode().Perm())
		}
		var limits steeringRecoveryLimits
		var request steerRequest
		var routed *AsyncStatus
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs, in.RecoveryTimeoutMs = i64(250), i64(1000)
		in.OnRequestQueued = func(p string) {
			request = saReadRequestFile(t, p)
			routed = saStatus(runID, "single", 1)
			saProject(routed, request, "routed")
			saWrite(t, dir, routed)
		}
		in.OnRecoveryCommitted = func(string, int64) {
			paused := *routed
			paused.State = "paused"
			paused.EndedAt = new(ms(nowMs()))
			paused.Extra = map[string]json.RawMessage{"steering": routed.Extra["steering"], "toolBudget": json.RawMessage(`{"soft":8,"hard":12,"block":["read"],"toolCount":9,"outcome":"soft-reached"}`)}
			paused.Steps = []AsyncStep{routed.Steps[0]}
			paused.Steps[0].Status = "paused"
			paused.Steps[0].SessionFile = sessionFile
			saWrite(t, dir, &paused)
		}
		in.Recover = func(l steeringRecoveryLimits) steerRecoverResult {
			st, _ := readStatus(dir)
			equal(t, st.State, "paused", "paused before the replacement")
			equal(t, st.EndedAt != nil, true, "endedAt")
			limits = l
			return steerRecoverResult{AsyncID: "replacement"}
		}
		r := steerAsyncRun(in)
		equal(t, r.IsError, false, text(r))
		equal(t, r.Steering.State, "recovered", "state")
		polMatches(t, r.Text, "Message sent:\n```text\ncorrect course\n```")
		equal(t, r.Steering.ReplacementRunID, "replacement", "replacement")
		if *limits.TimeoutMs <= 0 || *limits.TimeoutMs > 10_000 {
			t.Errorf("timeout %d", *limits.TimeoutMs)
		}
		if *limits.AbsoluteDeadlineAt < nowMs() {
			t.Errorf("deadline %d", *limits.AbsoluteDeadlineAt)
		}
		equal(t, *limits.ToolBudget, resolvedToolBudget{Hard: 3, Block: []string{"read"}}, "tool budget")
		persisted, _ := readStatus(dir)
		equal(t, statusSteering(persisted).Recent[0].Targets[0].State, "recovered", "ledger")
		equal(t, stepSteering(&persisted.Steps[0]).Recent[0].Targets[0].State, "recovered", "step ledger")
	})

	tw(t, f, "leaves a single run paused when no session can be revived", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-no-session"
		dir := e.dir(runID)
		saWrite(t, dir, saStatus(runID, "single", 1))
		if err := asyncWritePrivateJSON(filepath.Join(dir, "recovery-descriptor.json"), saRecoveryDescriptor(t, runID)); err != nil {
			t.Fatal(err)
		}
		recovered, committed := false, false
		var routed *AsyncStatus
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs, in.RecoveryTimeoutMs = i64(250), i64(1000)
		in.OnRequestQueued = func(p string) {
			routed = saStatus(runID, "single", 1)
			saProject(routed, saReadRequestFile(t, p), "failed")
			saWrite(t, dir, routed)
		}
		in.OnRecoveryCommitted = func(string, int64) {
			committed = true
			paused := *routed
			paused.State = "paused"
			paused.EndedAt = new(ms(nowMs()))
			paused.Steps = []AsyncStep{routed.Steps[0]}
			paused.Steps[0].Status = "paused"
			saWrite(t, dir, &paused)
		}
		in.Recover = func(steeringRecoveryLimits) steerRecoverResult {
			recovered = true
			return steerRecoverResult{AsyncID: "replacement"}
		}
		r := steerAsyncRun(in)
		equal(t, committed && routed != nil, true, "committed")
		equal(t, r.IsError, true, "isError")
		equal(t, recovered, false, "recovered")
		polMatches(t, r.Text, "(?i)no persisted child session|does not have a persisted session file")
	})

	tw(t, f, "never auto-interrupts a nested single run", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-nested"
		initial := saStatus(runID, "single", 1)
		initial.Extra["isNested"] = json.RawMessage("true")
		saWrite(t, e.dir(runID), initial)
		interrupted, recovered := false, false
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs = i64(25)
		in.Kill = func(pid, signal int) error {
			if signal != 0 {
				interrupted = true
			}
			return nil
		}
		in.Recover = func(steeringRecoveryLimits) steerRecoverResult {
			recovered = true
			return steerRecoverResult{AsyncID: "replacement"}
		}
		action := saRun(in)
		req := saReadRequest(t, e.dir(runID))
		routed := saStatus(runID, "single", 1)
		routed.Extra["isNested"] = json.RawMessage("true")
		saProject(routed, req, "routed")
		saWrite(t, e.dir(runID), routed)
		r := <-action
		equal(t, r.Steering.State, "pending", "state")
		equal(t, [2]bool{interrupted, recovered}, [2]bool{false, false}, "interrupted, recovered")
		equal(t, ccExists(interruptRequestPath(e.dir(runID))), false, "interrupt request")
	})

	tw(t, f, "reports partial multi-child delivery without interrupting the run", func(t *testing.T) {
		e := newSaEnv(t)
		runID := "steer-partial"
		saWrite(t, e.dir(runID), saStatus(runID, "parallel", 2))
		var mu sync.Mutex
		killed := false
		in := saBase(e, runID, "correct course")
		in.AckTimeoutMs = i64(500)
		in.Kill = func(pid, signal int) error {
			if signal != 0 {
				mu.Lock()
				killed = true
				mu.Unlock()
			}
			return nil
		}
		action := saRun(in)
		req := saReadRequest(t, e.dir(runID))
		equal(t, req.TargetIndexes, []int{0, 1}, "target indexes")
		st := saStatus(runID, "parallel", 2)
		saProject(st, req, "delivered", "failed")
		saWrite(t, e.dir(runID), st)
		r := <-action
		equal(t, r.IsError, true, "isError")
		equal(t, r.Steering.State, "partial", "state")
		mu.Lock()
		equal(t, killed, false, "killed")
		mu.Unlock()
		equal(t, ccExists(interruptRequestPath(e.dir(runID))), false, "interrupt request")
	})
	_ = idx
}
