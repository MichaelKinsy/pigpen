package pi_subagents

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const missionStoreFile = "mission-store"

type missionFixture struct {
	root, projectRoot, agentDir string
	loc                         MissionStoreLocation
}

func newMissionFixture(t *testing.T) missionFixture {
	t.Helper()
	root := tmp(t)
	f := missionFixture{root: root, projectRoot: filepath.Join(root, "project"), agentDir: filepath.Join(root, "agent")}
	if err := os.MkdirAll(f.projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	f.loc = resolveMissionStoreLocation(f.projectRoot, nil, f.agentDir)
	return f
}

func (f missionFixture) ctx() MissionActionContext {
	return MissionActionContext{Cwd: f.projectRoot, AgentDir: f.agentDir, CurrentSessionID: "session-1"}
}

func mustMission(t *testing.T, r *MissionRecord, err error) *MissionRecord {
	t.Helper()
	if err != nil {
		t.Fatalf("mission: %v", err)
	}
	return r
}

func mkMission(t *testing.T, loc MissionStoreLocation, in MissionCreateInput) *MissionRecord {
	t.Helper()
	r, err := createMission(loc, in, time.Now(), -1)
	return mustMission(t, r, err)
}

func updMission(t *testing.T, loc MissionStoreLocation, id string, u MissionUpdateInput) *MissionRecord {
	t.Helper()
	r, err := updateMission(loc, id, u, time.Now(), -1)
	return mustMission(t, r, err)
}

func matchesRe(t *testing.T, err error, re string) {
	t.Helper()
	if err == nil {
		t.Errorf("no error, want one matching %s", re)
		return
	}
	if !regexp.MustCompile(re).MatchString(err.Error()) {
		t.Errorf("error %q does not match %s", err.Error(), re)
	}
}

func runAction(t *testing.T, name string, params *jsObject, ctx MissionActionContext) MissionActionResult {
	t.Helper()
	res, err := handleMissionAction(name, params, ctx)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func detailMission(res MissionActionResult) *MissionRecord {
	m, _ := res.Details["mission"].(*MissionRecord)
	return m
}

func jsonFileEq(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, msg := missionParseJSON(string(raw))
	if msg != "" {
		t.Fatalf("%s: %s", path, msg)
	}
	wantV, msg := missionParseJSON(want)
	if msg != "" {
		t.Fatal(msg)
	}
	jsonEq(t, got, marshalJSON(wantV, ""))
}

func TestMissionStoreTwins(t *testing.T) {
	tw(t, missionStoreFile, "stores default missions outside the project and preserves configured project storage", func(t *testing.T) {
		f := newMissionFixture(t)
		rel, _ := filepath.Rel(f.projectRoot, f.loc.MissionDir)
		eq(t, strings.HasPrefix(rel, ".."), true, "outside the project")
		eq(t, strings.HasPrefix(f.loc.MissionDir, filepath.Join(f.agentDir, "missions", "projects")), true, "under the agent directory")
		cfg := &MissionStoreConfig{Directory: ".pi/subagents/missions"}
		eq(t, resolveMissionStoreLocation(f.projectRoot, cfg, f.agentDir).MissionDir, filepath.Join(f.projectRoot, ".pi/subagents", "missions"), "configured directory")
	})

	tw(t, missionStoreFile, "creates, reads, updates, lists, and globally indexes project missions", func(t *testing.T) {
		f := newMissionFixture(t)
		created := mkMission(t, f.loc, MissionCreateInput{Title: "Ship durable missions", Objective: "Make delegated work resumable", Labels: []string{"phase-1"}, HasLabels: true})
		updated := updMission(t, f.loc, created.ID, MissionUpdateInput{
			Status:       "active",
			Summary:      new("Implementation started"),
			AddRuns:      []MissionRunLink{{RunID: "run-1", Mode: "single", Status: "running"}, {RunID: "workflow-1", Mode: "workflow", Status: "completed"}},
			AddArtifacts: []MissionArtifact{{Kind: "status", Path: filepath.Join(f.root, "status.json")}},
			AddReceipts:  []MissionReceipt{{Kind: "pull_request", Status: "ready", Title: "PR 733", URL: "https://github.com/example/repo/pull/733"}},
			AddDecisions: []MissionDecisionInput{{Title: "Choose release window", Options: []string{"now", "later"}}},
		})
		read, err := readMission(f.loc, created.ID)
		eq(t, err, nil, "read")
		eq(t, read.Status, "needs_decision", "gated on the open decision")
		eq(t, updated.Runs[0].RunID, "run-1", "first run")
		eq(t, read.Runs[1].Mode, "workflow", "second run mode")
		eq(t, updated.Decisions[0].Status, "open", "decision")
		eq(t, updated.Receipts[0].URL, "https://github.com/example/repo/pull/733", "receipt")
		receiptUpdated := updMission(t, f.loc, created.ID, MissionUpdateInput{AddReceipts: []MissionReceipt{{Kind: "pull_request", Status: "succeeded", Title: "PR 733", URL: "https://github.com/example/repo/pull/733"}}})
		eq(t, receiptUpdated.Receipts[0].Status, "succeeded", "receipt status")
		eq(t, receiptUpdated.Receipts[0].CreatedAt, updated.Receipts[0].CreatedAt, "receipt keeps its creation time")
		listed, _ := listMissions(f.loc)
		eq(t, len(listed.Records), 1, "listed")
		eq(t, listed.Records[0].ID, created.ID, "listed id")
		global := listGlobalMissions(f.loc.GlobalIndexDir)
		eq(t, global.Entries[0].MissionID, created.ID, "indexed")
		eq(t, global.Entries[0].Stale, false, "not stale")
	})

	tw(t, missionStoreFile, "persists workflow child attempts and projects their latest heartbeat", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Dispatch ledger", Objective: "Track workflow children", Status: "active"})
		_, err := updateMission(f.loc, mission.ID, MissionUpdateInput{UpsertWorkflowChildren: []MissionWorkflowChildUpdate{{
			WorkflowRunID: "workflow-1", Key: "review", Status: "running", Agent: "reviewer", Task: "Review the diff", Phase: "review", SessionPath: "/tmp/review.jsonl",
			Heartbeat: &MissionHeartbeatUpdate{Status: "running", Phase: "review"},
		}}}, time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC), -1)
		eq(t, err, nil, "first upsert")
		completed, err := updateMission(f.loc, mission.ID, MissionUpdateInput{UpsertWorkflowChildren: []MissionWorkflowChildUpdate{{
			WorkflowRunID: "workflow-1", Key: "review", Status: "completed", RunID: "child-1", CompletedAt: "2026-08-11T10:05:00.000Z", ArtifactPaths: []string{"/tmp/review.md"},
			Heartbeat: &MissionHeartbeatUpdate{Status: "completed", Phase: "review"},
		}}}, time.Date(2026, 8, 11, 10, 5, 0, 0, time.UTC), -1)
		eq(t, err, nil, "second upsert")
		eq(t, len(completed.WorkflowChildren), 1, "one child")
		eq(t, completed.WorkflowChildren[0], MissionWorkflowChild{
			WorkflowRunID: "workflow-1", Key: "review", Status: "completed", StartedAt: "2026-08-11T10:00:00.000Z", UpdatedAt: "2026-08-11T10:05:00.000Z",
			RunID: "child-1", Agent: "reviewer", Task: "Review the diff", Phase: "review", CompletedAt: "2026-08-11T10:05:00.000Z", SessionPath: "/tmp/review.jsonl",
			ArtifactPaths: []string{"/tmp/review.md"}, Heartbeat: &MissionChildHeartbeat{Status: "completed", Phase: "review", UpdatedAt: "2026-08-11T10:05:00.000Z"},
		}, "merged child")
		shown := runAction(t, "mission.show", jo("missionId", mission.ID), f.ctx())
		textMatches(t, shown.Text, `review \(child-1\): completed — reviewer \[review\]; updated .*; heartbeat completed/review at`)
	})

	tw(t, missionStoreFile, "gates a mission on open decisions and resolves them explicitly", func(t *testing.T) {
		f := newMissionFixture(t)
		ctx := f.ctx()
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Decision gate", Objective: "Wait for owner", Status: "active"})
		pending := runAction(t, "mission.update", jo("missionId", mission.ID, "missionUpdate", jo("decisions", []any{jo("title", "Choose release window", "options", []any{"now", "later"}, "recommendation", "later")})), ctx)
		decisionID := detailMission(pending).Decisions[0].ID
		eq(t, decisionID != "", true, "decision id")
		eq(t, detailMission(pending).Status, "needs_decision", "gated")
		if !strings.Contains(runAction(t, "mission.list", jo(), ctx).Text, "decisions: 1 open, 0 resolved") {
			t.Errorf("list does not count decisions")
		}
		refreshed := updMission(t, f.loc, mission.ID, MissionUpdateInput{Status: "active", AddRuns: []MissionRunLink{{RunID: "run-1", Mode: "single", Status: "running"}}})
		eq(t, refreshed.Status, "needs_decision", "still gated")
		resolved := runAction(t, "mission.resolve-decision", jo("missionId", mission.ID, "id", decisionID, "summary", "Release later"), ctx)
		eq(t, detailMission(resolved).Status, "active", "reopened")
		eq(t, detailMission(resolved).Decisions[0].Status, "resolved", "resolved")
		eq(t, detailMission(resolved).Decisions[0].Resolution, "Release later", "resolution")
		if !strings.Contains(resolved.Text, "resolved — Choose release window; resolution: Release later") {
			t.Errorf("text: %s", resolved.Text)
		}
		_, err := handleMissionAction("mission.resolve-decision", jo("missionId", mission.ID, "id", decisionID, "summary", "Again"), ctx)
		matchesRe(t, err, "already resolved")
	})

	tw(t, missionStoreFile, "keeps planned and waiting missions in their lifecycle state while decisions are resolved", func(t *testing.T) {
		f := newMissionFixture(t)
		for _, status := range []string{"planned", "waiting"} {
			mission := mkMission(t, f.loc, MissionCreateInput{Title: status + " decision", Objective: "Keep state", Status: status})
			pending := updMission(t, f.loc, mission.ID, MissionUpdateInput{AddDecisions: []MissionDecisionInput{{Title: "Choose next step"}}})
			eq(t, pending.Status, status, "pending")
			resolved := updMission(t, f.loc, mission.ID, MissionUpdateInput{ResolveDecision: &MissionResolveDecision{ID: pending.Decisions[0].ID, Resolution: "Later"}})
			eq(t, resolved.Status, status, "resolved")
			eq(t, resolved.Decisions[0].Status, "resolved", "decision")
		}
	})

	tw(t, missionStoreFile, "persists bounded workflow state and reads its file once per workflow", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Stateful mission", Objective: "Keep workflow state"})
		state, err := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{})
		eq(t, err, nil, "state")
		v, _ := state.Get("missing")
		eq(t, v, undef{}, "missing key")
		writeFile(t, state.Path(), `{"external":true}`)
		v, _ = state.Get("external")
		eq(t, v, undef{}, "the file is read once")

		next, _ := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{})
		v, _ = next.Get("external")
		eq(t, v, true, "next workflow reads the file")
		eq(t, next.Set("review.stage", jo("count", float64(2))), nil, "set")
		concurrent, _ := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{})
		v, _ = concurrent.Get("review.stage")
		jsonEq(t, v, `{"count":2}`)
		eq(t, next.Set("approved", false), nil, "set approved")
		eq(t, concurrent.Set("reviewer", "ready"), nil, "set reviewer")
		jsonFileEq(t, state.Path(), `{"external":true,"review.stage":{"count":2},"approved":false,"reviewer":"ready"}`)
		matchesRe(t, next.Set("bad key", true), "state key must be 1-128 characters")
		matchesRe(t, next.Set("too-large", strings.Repeat("x", MissionStateMaxBytes)), "256 KiB limit")
		v, _ = next.Get("too-large")
		eq(t, v, undef{}, "the oversized value was not stored")
	})

	tw(t, missionStoreFile, "serializes workflow state writes behind the state-file lock", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Locked state", Objective: "Serialize state writes"})
		statePath, _ := missionStatePath(f.loc, mission.ID)
		writeFile(t, statePath, `{"external": true}`)
		lockPath := statePath + ".lock"
		readyPath := filepath.Join(f.root, "writer-ready")
		if err := os.Mkdir(lockPath, 0o755); err != nil {
			t.Fatal(err)
		}
		writer := startStateWriter(t, f, mission.ID, "reviewer", "ready", readyPath)
		waitForFile(t, readyPath)
		writeFile(t, statePath, `{"external": true, "approved": false}`)
		os.RemoveAll(lockPath)
		writer.wait(t, 10*time.Second)
		jsonFileEq(t, statePath, `{"external":true,"approved":false,"reviewer":"ready"}`)
	})

	tw(t, missionStoreFile, "recovers abandoned workflow state locks", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Stale lock", Objective: "Recover state writes"})
		state, _ := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{})
		lockPath := state.Path() + ".lock"
		if err := os.MkdirAll(lockPath, 0o755); err != nil {
			t.Fatal(err)
		}
		staleAt := time.Now().Add(-120 * time.Second)
		if err := os.Chtimes(lockPath, staleAt, staleAt); err != nil {
			t.Fatal(err)
		}
		eq(t, state.Set("reviewer", "ready"), nil, "set")
		_, err := os.Stat(lockPath)
		eq(t, os.IsNotExist(err), true, "lock removed")
		jsonFileEq(t, state.Path(), `{"reviewer":"ready"}`)
	})

	tw(t, missionStoreFile, "recovers abandoned workflow state locks even when the owner pid was reused", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Reused pid", Objective: "Recover by lock age"})
		state, _ := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{})
		lockPath := state.Path() + ".lock"
		owner := jo("pid", float64(os.Getpid()), "token", "abandoned", "createdAt", float64(time.Now().UnixMilli()), "processKey", "stale-process")
		writeFile(t, filepath.Join(lockPath, "owner.json"), marshalJSON(owner, ""))
		eq(t, state.Set("reviewer", "ready"), nil, "set")
		_, err := os.Stat(lockPath)
		eq(t, os.IsNotExist(err), true, "lock removed")
		jsonFileEq(t, state.Path(), `{"reviewer":"ready"}`)
	})

	tw(t, missionStoreFile, "does not reclaim a live replacement owner after observing a reused pid identity", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Replacement owner", Objective: "Keep a reused pid lock safe"})
		statePath, _ := missionStatePath(f.loc, mission.ID)
		lockPath := statePath + ".lock"
		ownerPath := filepath.Join(lockPath, "owner.json")
		reusedPid := os.Getpid() + 100_000
		replacement := marshalJSON(jo("pid", float64(reusedPid), "token", "replacement", "createdAt", float64(time.Now().UnixMilli()), "processKey", "replacement-process"), "")
		writeFile(t, ownerPath, marshalJSON(jo("pid", float64(reusedPid), "token", "old", "createdAt", float64(time.Now().UnixMilli()), "processKey", "old-process"), ""))
		currentIdentity := "old-process"
		state, _ := NewMissionWorkflowState(f.loc, mission.ID, MissionWorkflowStateOptions{
			IsProcessAlive: func(pid int) bool { return pid == reusedPid },
			GetProcessStartKey: func(pid int) string {
				if pid == reusedPid {
					return currentIdentity
				}
				return "current-process"
			},
			RetryDelaysMs: []int{},
		})
		matchesRe(t, state.Set("reviewer", "ready"), "Timed out acquiring mission state lock")
		writeFile(t, ownerPath, replacement)
		currentIdentity = "replacement-process"
		matchesRe(t, state.Set("reviewer", "ready"), "Timed out acquiring mission state lock")
		got, _ := os.ReadFile(ownerPath)
		eq(t, string(got), replacement, "the replacement owner is untouched")
		_, err := os.Stat(statePath)
		eq(t, os.IsNotExist(err), true, "nothing written")
	})

	tw(t, missionStoreFile, "serializes competing abandoned-lock recovery", func(t *testing.T) {
		f := newMissionFixture(t)
		mission := mkMission(t, f.loc, MissionCreateInput{Title: "Competing stale lock", Objective: "Recover once"})
		statePath, _ := missionStatePath(f.loc, mission.ID)
		writeFile(t, statePath, `{"external": true}`)
		lockPath := statePath + ".lock"
		if err := os.MkdirAll(lockPath, 0o755); err != nil {
			t.Fatal(err)
		}
		staleAt := time.Now().Add(-120 * time.Second)
		if err := os.Chtimes(lockPath, staleAt, staleAt); err != nil {
			t.Fatal(err)
		}
		a := startStateWriter(t, f, mission.ID, "reviewer", "ready", "")
		b := startStateWriter(t, f, mission.ID, "approved", "false", "")
		a.wait(t, 20*time.Second)
		b.wait(t, 20*time.Second)
		jsonFileEq(t, statePath, `{"external":true,"reviewer":"ready","approved":"false"}`)
	})

	tw(t, missionStoreFile, "loads older records that do not have receipts", func(t *testing.T) {
		f := newMissionFixture(t)
		created := mkMission(t, f.loc, MissionCreateInput{Title: "Older record", Objective: "Stay readable"})
		recordPath := filepath.Join(f.loc.MissionDir, created.ID+".json")
		raw, _ := os.ReadFile(recordPath)
		parsed, _ := missionParseJSON(string(raw))
		obj := parsed.(*jsObject)
		delete(obj.vals, "receipts")
		obj.keys = dropMissionKey(obj.keys, "receipts")
		writeFile(t, recordPath, marshalJSON(obj, ""))
		mission, err := readMission(f.loc, created.ID)
		eq(t, err, nil, "read")
		eq(t, mission.Objective, "Stay readable", "objective")
		eq(t, mission.Goal == nil, true, "no goal")
		eq(t, len(mission.Receipts), 0, "no receipts")
		eq(t, len(mission.WorkflowChildren), 0, "no workflow children")
	})

	tw(t, missionStoreFile, "skips corrupt records, removes missing pointers, and preserves parse-error pointers", func(t *testing.T) {
		f := newMissionFixture(t)
		missing := mkMission(t, f.loc, MissionCreateInput{Title: "Missing record", Objective: "Heal its pointer"})
		corrupt := mkMission(t, f.loc, MissionCreateInput{Title: "Corrupt record", Objective: "Keep evidence"})
		writeFile(t, filepath.Join(f.loc.MissionDir, "broken.json"), "{not json")
		listed, _ := listMissions(f.loc)
		eq(t, len(listed.Warnings), 1, "one corrupt file")
		os.Remove(filepath.Join(f.loc.MissionDir, missing.ID+".json"))
		writeFile(t, filepath.Join(f.loc.MissionDir, corrupt.ID+".json"), "{not json")
		global := listGlobalMissions(f.loc.GlobalIndexDir)
		eq(t, len(global.Entries), 1, "one pointer left")
		eq(t, global.Entries[0].MissionID, corrupt.ID, "corrupt pointer kept")
		eq(t, global.Entries[0].Stale, true, "stale")
		textMatches(t, strings.Join(global.Warnings, "\n"), "Removed stale global mission pointer")
	})

	tw(t, missionStoreFile, "prunes only the oldest terminal missions at the configured bound", func(t *testing.T) {
		f := newMissionFixture(t)
		loc := f.loc
		retain := int64(1)
		loc.RetainTerminal = &retain
		at := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
		create := func(in MissionCreateInput, when string) *MissionRecord {
			r, err := createMission(loc, in, at(when), -1)
			return mustMission(t, r, err)
		}
		oldest := create(MissionCreateInput{Title: "Old terminal", Objective: "Prune me"}, "2026-01-01T00:00:00Z")
		newest := create(MissionCreateInput{Title: "New terminal", Objective: "Keep me"}, "2026-01-02T00:00:00Z")
		active := create(MissionCreateInput{Title: "Active", Objective: "Never prune", Status: "active"}, "2026-01-03T00:00:00Z")
		planned := create(MissionCreateInput{Title: "Planned", Objective: "Never prune", Status: "planned"}, "2026-01-04T00:00:00Z")
		_, err := updateMission(loc, oldest.ID, MissionUpdateInput{Status: "completed"}, at("2026-01-05T00:00:00Z"), -1)
		eq(t, err, nil, "complete")
		_, err = updateMission(loc, newest.ID, MissionUpdateInput{Status: "failed"}, at("2026-01-06T00:00:00Z"), -1)
		eq(t, err, nil, "fail")
		_, err = readMission(loc, oldest.ID)
		matchesRe(t, err, "was not found")
		for id, want := range map[string]string{newest.ID: "failed", active.ID: "active", planned.ID: "planned"} {
			r, err := readMission(loc, id)
			eq(t, err, nil, "read "+want)
			eq(t, r.Status, want, "status")
		}
		for _, e := range listGlobalMissions(loc.GlobalIndexDir).Entries {
			eq(t, e.MissionID == oldest.ID, false, "pruned pointer")
		}
	})

	tw(t, missionStoreFile, "shows missions with warnings when linked run status is unreadable", func(t *testing.T) {
		f := newMissionFixture(t)
		ctx := f.ctx()
		created := runAction(t, "mission.create", jo("mission", jo("title", "Unreadable status", "objective", "Keep mission readable")), ctx)
		missionID := created.Details["missionId"].(string)
		asyncDir := filepath.Join(f.root, "async-run")
		os.MkdirAll(asyncDir, 0o755)
		runAction(t, "mission.attach-run", jo("missionId", missionID, "runId", "run-3", "runMode", "single", "runStatus", "running", "dir", asyncDir), ctx)
		writeFile(t, filepath.Join(asyncDir, "status.json"), "{not json")
		shown := runAction(t, "mission.show", jo("missionId", missionID), ctx)
		eq(t, detailMission(shown).Runs[0].Status, "running", "status unchanged")
		if !strings.Contains(shown.Text, "Warning: Failed to read linked run status") {
			t.Errorf("no warning: %s", shown.Text)
		}
		eq(t, len(shown.Details["missions"].(map[string]any)["warnings"].([]string)), 1, "one warning")
	})

	tw(t, missionStoreFile, "supports the mission management actions with structured details", func(t *testing.T) {
		f := newMissionFixture(t)
		ctx := f.ctx()
		created := runAction(t, "mission.create", jo("mission", jo("title", "Action mission", "objective", "Exercise actions")), ctx)
		missionID := created.Details["missionId"].(string)
		asyncDir := filepath.Join(f.root, "async-run")
		os.MkdirAll(asyncDir, 0o755)
		attached := runAction(t, "mission.attach-run", jo("missionId", missionID, "runId", "run-2", "runMode", "parallel", "runStatus", "running", "dir", asyncDir), ctx)
		eq(t, detailMission(attached).Runs[0].RunID, "run-2", "attached")
		writeFile(t, filepath.Join(asyncDir, "status.json"), `{"state":"complete"}`)
		shown := runAction(t, "mission.show", jo("missionId", missionID), ctx)
		eq(t, detailMission(shown).Status, "completed", "completed from the linked status")
		textMatches(t, shown.Text, `State: .*`+missionID+`[/\\]state\.json`)
		eq(t, detailMission(shown).Runs[0].Status, "complete", "run status")
		receipt := runAction(t, "mission.update", jo("missionId", missionID, "missionUpdate", jo("receipts", []any{jo("kind", "ci", "status", "succeeded", "title", "Unit tests", "url", "https://github.com/example/repo/actions/runs/1", "description", "All checks passed")})), ctx)
		eq(t, detailMission(receipt).Receipts[0].Status, "succeeded", "receipt")
		if !strings.Contains(receipt.Text, "Delivery receipts:\n  ci (succeeded): Unit tests") {
			t.Errorf("text: %s", receipt.Text)
		}
		updatedReceipt := runAction(t, "mission.update", jo("missionId", missionID, "missionUpdate", jo("receipts", []any{jo("kind", "ci", "status", "ready", "title", "Unit tests", "url", "https://github.com/example/repo/actions/runs/1")})), ctx)
		eq(t, len(detailMission(updatedReceipt).Receipts), 1, "one receipt")
		eq(t, detailMission(updatedReceipt).Receipts[0].Status, "ready", "receipt replaced")
		closed := runAction(t, "mission.close", jo("missionId", missionID, "missionStatus", "completed", "summary", "Done"), ctx)
		eq(t, detailMission(closed).Status, "completed", "closed")
		global := runAction(t, "mission.list", jo("missionScope", "global"), ctx)
		eq(t, len(global.Details["missions"].(map[string]any)["globalEntries"].([]GlobalMissionIndexRecord)), 1, "global list")
		_, err := handleMissionAction("mission.list", jo("missionScope", "everywhere"), ctx)
		matchesRe(t, err, "missionScope")
		_, err = handleMissionAction("mission.update", jo("missionId", missionID, "missionUpdate", jo("unsupported", true)), ctx)
		matchesRe(t, err, "unknown")
		_, err = handleMissionAction("mission.update", jo("missionId", missionID, "missionUpdate", jo("receipts", []any{jo("kind", "ci", "status", "ready", "title", "Bad URL", "url", "relative")})), ctx)
		matchesRe(t, err, "absolute URL")
	})
}

func dropMissionKey(keys []string, k string) []string {
	var out []string
	for _, x := range keys {
		if x != k {
			out = append(out, x)
		}
	}
	return out
}

func textMatches(t *testing.T, text, re string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(text) {
		t.Errorf("text %q does not match %s", text, re)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

type stateWriter struct {
	cmd    *exec.Cmd
	stderr *strings.Builder
	done   chan error
}

// TestMissionStateWriterHelper is the child process of the lock tests: a second process writing one key of a mission state.
func TestMissionStateWriterHelper(t *testing.T) {
	if os.Getenv("MISSION_STATE_HELPER") != "1" {
		t.Skip("helper process")
	}
	loc := MissionStoreLocation{ProjectRoot: os.Getenv("MISSION_PROJECT"), MissionDir: os.Getenv("MISSION_DIR")}
	if ready := os.Getenv("READY_FILE"); ready != "" {
		os.WriteFile(ready, []byte("ready"), 0o644)
	}
	state, err := NewMissionWorkflowState(loc, os.Getenv("MISSION_ID"), MissionWorkflowStateOptions{})
	if err == nil {
		err = state.Set(os.Getenv("STATE_KEY"), os.Getenv("STATE_VALUE"))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startStateWriter(t *testing.T, f missionFixture, id, key, value, ready string) *stateWriter {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMissionStateWriterHelper$")
	cmd.Env = append(os.Environ(), "MISSION_STATE_HELPER=1", "MISSION_PROJECT="+f.loc.ProjectRoot, "MISSION_DIR="+f.loc.MissionDir, "MISSION_ID="+id, "STATE_KEY="+key, "STATE_VALUE="+value, "READY_FILE="+ready)
	w := &stateWriter{cmd: cmd, stderr: &strings.Builder{}, done: make(chan error, 1)}
	cmd.Stderr, cmd.Stdout = w.stderr, w.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { w.done <- cmd.Wait() }()
	return w
}

func (w *stateWriter) wait(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case err := <-w.done:
		if err != nil {
			t.Fatalf("writer failed: %v\n%s", err, w.stderr.String())
		}
	case <-time.After(d):
		w.cmd.Process.Kill()
		t.Fatalf("writer timed out\n%s", w.stderr.String())
	}
}
