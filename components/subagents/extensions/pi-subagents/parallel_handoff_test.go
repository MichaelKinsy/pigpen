package pi_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/unit/parallel-handoff.test.ts.

func phSetup(repoRoot, baseCommit string, worktrees ...HandoffWorktree) HandoffSetup {
	return HandoffSetup{Cwd: repoRoot, BaseCommit: baseCommit, Worktrees: worktrees}
}

func phDiff(t *testing.T, dir string, index int, agent string, changed bool) HandoffDiff {
	t.Helper()
	patch := filepath.Join(dir, "task-"+itoa(index)+"-"+agent+".patch")
	body := ""
	d := HandoffDiff{Index: index, Agent: agent, Branch: "branch-" + itoa(index), PatchPath: patch}
	if changed {
		body = "diff --git a/a b/a\n"
		d.DiffStat, d.FilesChanged, d.Insertions = " a | 1 +", 1, 1
	}
	writeFile(t, patch, body)
	return d
}

func phCleanup(state string) *jsObject {
	task := jo("index", float64(0), "path", "/tmp/worktree-0", "branch", "branch-0", "worktreeRemoved", true, "branchRemoved", state == "complete")
	if state == "partial" {
		task.set("errors", []any{"branch removal failed"})
	}
	return jo("state", state, "pruned", state == "complete", "tasks", []any{task})
}

func phWT0() HandoffWorktree {
	return HandoffWorktree{Path: "/tmp/worktree-0", Branch: "branch-0", Index: 0}
}

func phRaw(t *testing.T, path string) *jsObject {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := parseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsObject)
}

func phWrite(t *testing.T, in WriteParallelHandoffGroupInput) ParallelHandoffReference {
	t.Helper()
	r, err := WriteParallelHandoffGroup(in)
	if err != nil {
		t.Fatalf("WriteParallelHandoffGroup: %v", err)
	}
	return r
}

func TestParallelHandoffTwins(t *testing.T) {
	const f = "parallel-handoff"
	i64 := func(v int64) *int64 { return &v }
	tw(t, f, "journals worktree ownership before child results exist", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		lane := jo("version", float64(1), "key", "writer", "mode", "mutation")
		binding := jo("index", float64(0), "taskIndex", float64(0), "workflowKey", "writer", "runId", "run-pending", "lane", lane)
		ref := phWrite(t, WriteParallelHandoffGroupInput{Diffs: nil, ManifestPath: manifestPath, RunID: "run-pending", Mode: "parallel", Source: "foreground", Cwd: "/repo",
			Setup: phSetup("/repo", "base-1", phWT0()), LaneBindings: []*jsObject{binding}})
		eq(t, ref.ChildCount, 0, "childCount")
		eq(t, ref.CleanupState, "partial", "cleanupState")
		manifest := phRaw(t, manifestPath)
		group := manifest.vals["groups"].([]any)[0].(*jsObject)
		task := group.obj("cleanup").vals["tasks"].([]any)[0].(*jsObject)
		eq(t, task.vals["path"], "/tmp/worktree-0", "path")
		eq(t, task.vals["preserved"], true, "preserved")
		jsonEq(t, group.vals["laneBindings"].([]any)[0], marshalJSON(binding, ""))
		mustMatch(t, "cleanup pending", task.vals["reason"].(string))
	})
	tw(t, f, "writes and aggregates versioned worktree handoff groups", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		first := phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "run-1", Mode: "chain", Source: "async", Cwd: "/repo", StepIndex: 2, FlatStartIndex: 3,
			Setup: phSetup("/repo", "base-1"), Diffs: []HandoffDiff{phDiff(t, dir, 0, "worker", true)}, Cleanup: phCleanup("complete"),
			Results: []ParallelHandoffResult{{Agent: "worker", Status: "completed", Summary: "implemented", OutputPath: "/artifacts/output.md", StructuredOutput: jo("ok", true), HasStructuredOutput: true,
				StructuredOutputPath: "/artifacts/structured.json", SessionPath: "/sessions/worker.jsonl"}}, Now: i64(100)})
		eq(t, first, ParallelHandoffReference{Version: 1, Path: manifestPath, GroupCount: 1, ChildCount: 1, ChangedPatches: 1, CleanupState: "complete"}, "first reference")
		second := phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "run-1", Mode: "chain", Source: "async", Cwd: "/repo", StepIndex: 1, FlatStartIndex: 1,
			Setup: phSetup("/repo", "base-1"), Diffs: []HandoffDiff{phDiff(t, dir, 1, "reviewer", false)}, Cleanup: phCleanup("partial"),
			Results: []ParallelHandoffResult{{Agent: "reviewer", Status: "failed", Summary: "blocked"}}, Now: i64(200)})
		eq(t, second.GroupCount, 2, "groupCount")
		eq(t, second.ChildCount, 2, "childCount")
		eq(t, second.ChangedPatches, 1, "changedPatches")
		eq(t, second.CleanupState, "partial", "cleanupState")
		mustMatch(t, "2 children, 1 changed patches, cleanup partial", FormatParallelHandoffReference(second))
		m := phRaw(t, manifestPath)
		eq(t, m.vals["createdAt"], float64(100), "createdAt")
		eq(t, m.vals["updatedAt"], float64(200), "updatedAt")
		groups := m.vals["groups"].([]any)
		eq(t, []any{groups[0].(*jsObject).vals["stepIndex"], groups[1].(*jsObject).vals["stepIndex"]}, []any{float64(1), float64(2)}, "step order")
		c0 := groups[0].(*jsObject).vals["children"].([]any)[0].(*jsObject)
		c1 := groups[1].(*jsObject).vals["children"].([]any)[0].(*jsObject)
		eq(t, c0.vals["index"], float64(1), "first index")
		eq(t, c1.vals["index"], float64(3), "second index")
		jsonEq(t, c1.vals["structuredOutput"], `{"ok":true}`)
		eq(t, c1.obj("patch").vals["changed"], true, "changed")
	})
	tw(t, f, "records missing diff artifacts instead of throwing", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		ref := phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "run-missing-diff", Mode: "parallel", Source: "foreground", Cwd: "/repo",
			Setup: phSetup("/repo", "base-1", phWT0()), Cleanup: phCleanup("complete"), Results: []ParallelHandoffResult{{Agent: "bad/name agent", Status: "completed", Summary: "done"}}})
		eq(t, ref.ChildCount, 1, "childCount")
		eq(t, ref.ChangedPatches, 0, "changedPatches")
		patch := phRaw(t, manifestPath).vals["groups"].([]any)[0].(*jsObject).vals["children"].([]any)[0].(*jsObject).obj("patch")
		eq(t, patch.vals["changed"], false, "changed")
		mustMatch(t, "diff artifact unavailable", patch.vals["error"].(string))
		eq(t, filepath.Base(patch.vals["path"].(string)), "missing-diff-step-0-task-0-bad_name_agent.patch", "patch name")
		if _, err := os.Stat(patch.vals["path"].(string)); err != nil {
			t.Error("the patch file does not exist")
		}
	})
	tw(t, f, "rejects reusing a manifest path for another run", func(t *testing.T) {
		dir := tmp(t)
		common := WriteParallelHandoffGroupInput{ManifestPath: filepath.Join(dir, "handoff.json"), Mode: "parallel", Source: "foreground", Cwd: "/repo", Setup: phSetup("/repo", "base-1"),
			Diffs: []HandoffDiff{phDiff(t, dir, 0, "worker", false)}, Cleanup: phCleanup("complete"), Results: []ParallelHandoffResult{{Agent: "worker", Status: "completed", Summary: "done"}}}
		a := common
		a.RunID = "run-1"
		phWrite(t, a)
		b := common
		b.RunID = "run-2"
		_, err := WriteParallelHandoffGroup(b)
		rcFails(t, err, "belongs to a different run")
	})
	tw(t, f, "round-trips lane identity and resolves a workflow child to its manifest task", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		lane := jo("version", float64(1), "key", "writer", "mode", "mutation", "sourceRef", "nicobailon/pi-subagents#1621", "claims", []any{"src/shared/types.ts"}, "outputPaths", []any{"reports/lane.md"})
		phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "workflow-run", Mode: "single", Source: "async", Cwd: "/repo", FlatStartIndex: 3, Setup: phSetup("/repo", "base-1"),
			Diffs: []HandoffDiff{phDiff(t, dir, 0, "writer", false)}, Cleanup: phCleanup("complete"),
			Results: []ParallelHandoffResult{{Agent: "worker", Status: "completed", Summary: "done", WorkflowKey: "writer", RunID: "child-run", Lane: lane}}})
		manifest, err := ReadParallelHandoffManifest(manifestPath)
		eq(t, err, nil, "read")
		child := manifest.vals["groups"].([]any)[0].(*jsObject).vals["children"].([]any)[0].(*jsObject)
		jsonEq(t, child.vals["lane"], marshalJSON(lane, ""))
		eq(t, child.vals["workflowKey"], "writer", "workflowKey")
		eq(t, child.vals["runId"], "child-run", "runId")
		match, err := ResolveParallelHandoffChild(manifestPath, "workflow-run", "writer", "child-run")
		eq(t, err, nil, "resolve")
		eq(t, match.Child.vals["taskIndex"], float64(0), "taskIndex")
		eq(t, match.Child.vals["index"], float64(3), "index")
		none, err := ResolveParallelHandoffChild(manifestPath, "workflow-run", "missing", "")
		eq(t, err, nil, "resolve missing")
		eq(t, none == nil, true, "no match")
		_, err = ResolveParallelHandoffChild(manifestPath, "other-run", "writer", "")
		rcFails(t, err, "belongs to run")
	})
	tw(t, f, "fails closed when persisted lane identity does not match the workflow key", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "workflow-run", Mode: "single", Source: "async", Cwd: "/repo", Setup: phSetup("/repo", "base-1"),
			Diffs: []HandoffDiff{phDiff(t, dir, 0, "writer", false)}, Cleanup: phCleanup("complete"),
			Results: []ParallelHandoffResult{{Agent: "worker", Status: "completed", Summary: "done", WorkflowKey: "writer", Lane: jo("version", float64(1), "key", "writer")}}})
		raw := phRaw(t, manifestPath)
		raw.vals["groups"].([]any)[0].(*jsObject).vals["children"].([]any)[0].(*jsObject).obj("lane").set("key", "other")
		b, _ := json.Marshal(raw)
		writeFile(t, manifestPath, string(b))
		_, err := ReadParallelHandoffManifest(manifestPath)
		rcFails(t, err, "does not match workflow key")
	})
	laneWrite := func(t *testing.T, dir, runID, source, base string, status string, cleanup string) ParallelHandoffReference {
		return phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: filepath.Join(dir, "handoff.json"), RunID: runID, Mode: "single", Source: source, Cwd: "/repo", Setup: phSetup("/repo", base),
			Diffs: []HandoffDiff{phDiff(t, dir, 0, "worker", false)}, Cleanup: phCleanup(cleanup), Results: []ParallelHandoffResult{{Agent: "worker", Status: status, Summary: "done"}}})
	}
	tw(t, f, "records merge evidence and renders stored terminal eligibility without deleting", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		laneWrite(t, dir, "lane-merge", "async", "base-1", "completed", "partial")
		superseded, err := RecordParallelHandoffSupersession(manifestPath, "lane-merge", jo("supersededBy", "replacement-lane", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:22:00.000Z"), nil)
		eq(t, err, nil, "supersession")
		jsonEq(t, superseded.Manifest.vals["cleanupEligibility"], `{"state":"superseded-eligible"}`)
		recorded, err := RecordParallelHandoffMerge(manifestPath, "lane-merge", jo("prNumber", float64(1623), "reviewedHead", "1111111111111111111111111111111111111111",
			"mergeCommit", "2222222222222222222222222222222222222222", "treeEquivalent", true, "postMergeChecks", "recorded", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z"), i64(200))
		eq(t, err, nil, "merge")
		jsonEq(t, recorded.Manifest.vals["cleanupEligibility"], `{"state":"terminal-eligible"}`)
		if _, has := recorded.Manifest.vals["supersession"]; has {
			t.Error("supersession kept")
		}
		eq(t, recorded.Reference.CleanupEligibility.State, "terminal-eligible", "reference eligibility")
		mustMatch(t, "Cleanup eligibility: terminal-eligible", recorded.Text)
		mustMatch(t, `action: "worktree\.cleanup"`, recorded.Text)
		mustMatch(t, "handoffPath:", recorded.Text)
		mustMatch(t, `mode: "plan"`, recorded.Text)
		stored := phRaw(t, manifestPath)
		eq(t, stored.obj("merge").vals["prNumber"], float64(1623), "prNumber")
		if _, has := stored.vals["supersession"]; has {
			t.Error("supersession persisted")
		}
		mustMatch(t, `^[0-9a-f]{64}$`, stored.obj("merge").vals["manifestDigest"].(string))

		active := laneWrite(t, dir, "lane-merge", "async", "base-1", "running", "partial")
		eq(t, active.CleanupEligibility.State, "active", "active")
		mustMatch(t, "Cleanup eligibility: active", FormatStoredParallelHandoffCleanup(manifestPath, nil))
		settled := laneWrite(t, dir, "lane-merge", "async", "base-1", "completed", "partial")
		eq(t, settled.CleanupEligibility.State, "terminal-eligible", "settled")
		mustMatch(t, "Cleanup eligibility: terminal-eligible", FormatStoredParallelHandoffCleanup(manifestPath, nil))
		stale := laneWrite(t, dir, "lane-merge", "async", "base-2", "completed", "partial")
		eq(t, *stale.CleanupEligibility, CleanupEligibility{State: "terminal-blocked", Reason: "stored merge evidence is stale"}, "stale")
		mustMatch(t, "Cleanup eligibility: terminal-blocked", FormatStoredParallelHandoffCleanup(manifestPath, nil))
	})
	tw(t, f, "rejects mismatched lanes and stale reviewed heads", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		laneWrite(t, dir, "lane-stale", "foreground", "base-1", "completed", "partial")
		merge := func(head string) *jsObject {
			return jo("prNumber", float64(1623), "reviewedHead", head, "mergeCommit", "4444444444444444444444444444444444444444", "treeEquivalent", true, "postMergeChecks", "recorded", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z")
		}
		_, err := RecordParallelHandoffMerge(manifestPath, "lane-stale", merge("3333333333333333333333333333333333333333"), nil)
		eq(t, err, nil, "first merge")
		_, err = RecordParallelHandoffMerge(manifestPath, "another-lane", merge("3333333333333333333333333333333333333333"), nil)
		rcFails(t, err, "does not match manifest run")
		_, err = RecordParallelHandoffMerge(manifestPath, "lane-stale", merge("5555555555555555555555555555555555555555"), nil)
		rcFails(t, err, "stale: reviewed head is already recorded")
	})
	tw(t, f, "keeps pending, unknown, and ineligible stored states fail closed", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		phWrite(t, WriteParallelHandoffGroupInput{ManifestPath: manifestPath, RunID: "lane-unknown", Mode: "single", Source: "foreground", Cwd: "/repo", Setup: phSetup("/repo", "base-1", phWT0())})
		pending := FormatStoredParallelHandoffCleanup(manifestPath, nil)
		mustMatch(t, "Cleanup eligibility: active", pending)
		mustMatch(t, "child owner is still active", pending)
		mustMatch(t, `mode: "plan"`, pending)
		merge := jo("prNumber", float64(1623), "reviewedHead", "6666666666666666666666666666666666666666", "mergeCommit", "7777777777777777777777777777777777777777", "treeEquivalent", "unknown", "postMergeChecks", "unknown", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z")
		_, err := RecordParallelHandoffMerge(manifestPath, "lane-unknown", merge, nil)
		rcFails(t, err, "active child owner")
		_, err = RecordParallelHandoffSupersession(manifestPath, "lane-unknown", jo("supersededBy", "replacement-lane", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z"), nil)
		rcFails(t, err, "active child owner")

		laneWrite(t, dir, "lane-unknown", "foreground", "base-1", "completed", "partial")
		blocked, err := RecordParallelHandoffMerge(manifestPath, "lane-unknown", merge, nil)
		eq(t, err, nil, "merge")
		jsonEq(t, blocked.Manifest.vals["cleanupEligibility"], `{"state":"terminal-blocked","reason":"merged tree equivalence was not attested"}`)
		mustMatch(t, "Cleanup eligibility: terminal-blocked", blocked.Text)

		unsafe := phRaw(t, manifestPath)
		unsafe = lpWithout(unsafe, "merge")
		unsafe.set("cleanupEligibility", jo("state", "terminal-eligible"))
		mustMatch(t, "Cleanup eligibility: unknown", FormatStoredParallelHandoffCleanup(manifestPath, unsafe))
		withNull := unsafe.clone()
		withNull.set("groups", []any{nil})
		mustMatch(t, "Cleanup eligibility: unknown", FormatStoredParallelHandoffCleanup(manifestPath, withNull))
		invalidPath := filepath.Join(dir, "invalid.json")
		writeFile(t, invalidPath, "{not-json")
		mustMatch(t, "Cleanup eligibility: unknown", FormatStoredParallelHandoffCleanup(invalidPath, nil))
	})
	tw(t, f, "refuses to record evidence while a stored child owner is active", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		for _, status := range []string{"running", "detached"} {
			laneWrite(t, dir, "lane-active", "async", "base-1", status, "partial")
			mustMatch(t, "Cleanup eligibility: active", FormatStoredParallelHandoffCleanup(manifestPath, nil))
			_, err := RecordParallelHandoffSupersession(manifestPath, "lane-active", jo("supersededBy", "lane-replacement", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z"), nil)
			rcFails(t, err, "active child owner")
		}
	})
	t.Run("refuses a supersession by the lane itself (not an upstream case)", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		laneWrite(t, dir, "lane-self", "foreground", "base-1", "completed", "partial")
		_, err := RecordParallelHandoffSupersession(manifestPath, "lane-self", jo("supersededBy", "lane-self", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z"), nil)
		rcFails(t, err, "must identify a different replacement lane")
	})
	tw(t, f, "records supersession evidence as an eligible stored state", func(t *testing.T) {
		dir := tmp(t)
		manifestPath := filepath.Join(dir, "handoff.json")
		laneWrite(t, dir, "lane-review", "foreground", "base-1", "completed", "partial")
		recorded, err := RecordParallelHandoffSupersession(manifestPath, "lane-review", jo("supersededBy", "lane-replacement", "attestedBy", "nicobailon", "attestedAt", "2026-08-27T16:23:00.000Z"), nil)
		eq(t, err, nil, "supersession")
		jsonEq(t, recorded.Manifest.vals["cleanupEligibility"], `{"state":"superseded-eligible"}`)
		mustMatch(t, "Cleanup eligibility: superseded-eligible", recorded.Text)
		stale := laneWrite(t, dir, "lane-review", "foreground", "base-2", "completed", "partial")
		eq(t, *stale.CleanupEligibility, CleanupEligibility{State: "terminal-blocked", Reason: "stored supersession evidence is stale"}, "stale")
		mustMatch(t, "Cleanup eligibility: terminal-blocked", FormatStoredParallelHandoffCleanup(manifestPath, nil))
	})
}
