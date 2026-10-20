package pi_subagents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const missionLifecycleFile = "mission-lifecycle"

type lifecycleFixture struct {
	root, projectRoot string
	config            *MissionStoreConfig
}

func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	root := tmp(t)
	f := lifecycleFixture{root: root, projectRoot: filepath.Join(root, "project")}
	os.MkdirAll(f.projectRoot, 0o755)
	f.config = &MissionStoreConfig{Directory: ".pi/subagents/missions", GlobalIndexDir: filepath.Join(root, "global-index")}
	return f
}

func prepare(t *testing.T, root string, params *jsObject, cfg *MissionStoreConfig, owner string) *MissionLaunchBinding {
	t.Helper()
	b, err := prepareMissionLaunch(params, root, cfg, owner)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if b == nil {
		t.Fatal("no binding")
	}
	return b
}

func readOf(t *testing.T, b *MissionLaunchBinding) *MissionRecord {
	t.Helper()
	r, err := readMission(b.Location, b.MissionID)
	return mustMission(t, r, err)
}

func attach(t *testing.T, b *MissionLaunchBinding, r MissionToolResult) MissionToolResult {
	t.Helper()
	out, err := attachMissionToLaunchResult(*b, r)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	return out
}

func TestMissionLifecycleTwins(t *testing.T) {
	tw(t, missionLifecycleFile, "validates the public mission launch shape", func(t *testing.T) {
		got, err := validateMissionLaunch(jo("summary", "Review the active backlog", "objective", "Find ready work", "goal", true, "budget", jo("tokens", float64(400000)), "labels", []any{"review"}))
		eq(t, err, nil, "valid")
		eq(t, got, MissionLaunchInput{Title: "Review the active backlog", Objective: "Find ready work", Goal: true, Budget: &MissionTokenBudget{Tokens: 400000}, Labels: []string{"review"}}, "normalized")
		for _, c := range []struct {
			v  any
			re string
		}{
			{jo(), `title or mission\.summary`},
			{true, `mission must be an object`},
			{false, `mission must be an object`},
			{jo("title", "Title", "summary", "Summary"), `cannot both be set`},
			{jo("title", " "), `non-empty string`},
			{jo("title", "Title", "goal", false), `must be true`},
			{jo("title", "Title", "goal", true), `budget is required`},
		} {
			_, err := validateMissionLaunch(c.v)
			matchesRe(t, err, c.re)
		}
	})

	tw(t, missionLifecycleFile, "creates missions by default for task launches and honors explicit opt-out", func(t *testing.T) {
		f := newLifecycleFixture(t)
		_, err := prepareMissionLaunch(jo("mission", true, "task", "Invalid mission"), f.projectRoot, f.config, "")
		matchesRe(t, err, `mission must be an object`)
		entries, _ := os.ReadDir(f.projectRoot)
		eq(t, len(entries), 0, "invalid mission must not create records")
		_, statErr := os.Stat(f.config.GlobalIndexDir)
		eq(t, os.IsNotExist(statErr), true, "no global index")
		binding := prepare(t, f.projectRoot, jo("task", "Map the auth flow"), f.config, "session-1")
		mission := readOf(t, binding)
		eq(t, mission.Objective, promptRedacted, "objective")
		eq(t, mission.Title, promptRedacted, "title")
		eq(t, mission.Status, "active", "status")
		result := attach(t, binding, MissionToolResult{
			Content: []MissionContent{{Type: "text", Text: `{"ok":true}`}},
			Mode:    "single", RunID: "default-mission-run",
			Results: []MissionChildResult{{Index: 0, Agent: "worker", Task: "Map the auth flow"}},
		})
		eq(t, result.Content[0].Text, `{"ok":true}`, "JSON text is left alone")

		disabled := &MissionStoreConfig{Directory: f.config.Directory, GlobalIndexDir: f.config.GlobalIndexDir, Enabled: new(false)}
		b, err := prepareMissionLaunch(jo("task", "Tiny one-off"), f.projectRoot, disabled, "")
		eq(t, err, nil, "disabled")
		eq(t, b == nil, true, "disabled config makes no mission")

		parallelOnly := prepare(t, f.projectRoot, jo("chain", []any{jo("parallel", []any{jo("task", ""), jo("task", "Review parallel work")})}), f.config, "")
		eq(t, readOf(t, parallelOnly).Objective, promptRedacted, "parallel objective")

		_, err = prepareMissionLaunch(jo("missionId", ""), f.projectRoot, f.config, "")
		matchesRe(t, err, `missionId`)

		b, err = prepareMissionLaunch(jo("mission", false, "task", "Ephemeral check"), f.projectRoot, f.config, "")
		eq(t, err, nil, "per-launch opt-out")
		eq(t, b == nil, true, "mission false makes none")

		_, err = prepareMissionLaunch(jo("missionId", binding.MissionID, "mission", false, "task", "Contradictory mission request"), f.projectRoot, f.config, "")
		matchesRe(t, err, `Use missionId or mission`)

		explicit := prepare(t, f.projectRoot, jo("mission", jo("title", "Explicit mission"), "task", "Tiny one-off"), disabled, "")
		eq(t, readOf(t, explicit).Objective, "Explicit mission", "explicit objective")

		alias := prepare(t, f.projectRoot, jo("mission", jo("summary", "Review active backlog", "labels", []any{"review"}), "task", "Review the current diff"), f.config, "")
		record := readOf(t, alias)
		eq(t, record.Labels, []string{"review"}, "labels")
		eq(t, record.Title, "Review active backlog", "title")
		eq(t, record.Objective, "Review active backlog", "objective")
	})

	tw(t, missionLifecycleFile, "explains when an explicit mission belongs to another worktree", func(t *testing.T) {
		f := newLifecycleFixture(t)
		worktreeRoot := filepath.Join(f.root, "worktree")
		os.MkdirAll(worktreeRoot, 0o755)
		binding := prepare(t, f.projectRoot, jo("mission", jo("title", "Root mission"), "task", "Prepare root work"), f.config, "")
		missionDir := filepath.Join(worktreeRoot, ".pi", "subagents", "missions")
		_, err := prepareMissionLaunch(jo("missionId", binding.MissionID, "task", "Prepare worktree work"), worktreeRoot, f.config, "")
		if err == nil || err.Error() != "Mission '"+binding.MissionID+"' was not found in mission directory '"+missionDir+"' for project root '"+worktreeRoot+"'. If it was created in another worktree, run the request from that worktree." {
			t.Fatalf("error: %v", err)
		}
	})

	tw(t, missionLifecycleFile, "creates a mission shortcut and records a completed foreground run", func(t *testing.T) {
		f := newLifecycleFixture(t)
		binding := prepare(t, f.projectRoot, jo("mission", jo("title", "Implement feature", "objective", "Ship it", "labels", []any{"phase-1"}), "task", "Do the work"), f.config, "session-1")
		result := attach(t, binding, MissionToolResult{
			Content: []MissionContent{{Type: "text", Text: "Implemented and tested."}}, Mode: "single", RunID: "foreground-1",
			Results: []MissionChildResult{{Index: 0, Agent: "worker", Task: "Do the work"}},
		})
		eq(t, result.MissionID, binding.MissionID, "mission id")
		if !regexp.MustCompile(`Implemented and tested\.\nMission: ` + binding.MissionID + ` \(completed\)$`).MatchString(result.Content[0].Text) {
			t.Errorf("text: %q", result.Content[0].Text)
		}
		mission := readOf(t, binding)
		eq(t, mission.Status, "completed", "status")
		eq(t, mission.Runs[0].RunID, "foreground-1", "run")
		eq(t, mission.Runs[0].Status, "completed", "run status")
	})

	tw(t, missionLifecycleFile, "marks a prepared mission failed when launch reservation returns an error before a run id exists", func(t *testing.T) {
		f := newLifecycleFixture(t)
		binding := prepare(t, f.projectRoot, jo("mission", jo("title", "Budget race"), "task", "Run after preflight"), f.config, "")
		result := attach(t, binding, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "Spawn budget exhausted after preflight."}}, IsError: true, Mode: "single"})
		eq(t, result.MissionID, binding.MissionID, "mission id")
		eq(t, readOf(t, binding).Status, "failed", "status")
	})

	tw(t, missionLifecycleFile, "records a bounded warning when an async mission record disappeared before completion", func(t *testing.T) {
		f := newLifecycleFixture(t)
		asyncDir := filepath.Join(f.root, "async-run-missing-mission")
		os.MkdirAll(asyncDir, 0o755)
		binding := prepare(t, f.projectRoot, jo("mission", jo("title", "Disposable mission record"), "task", "Run later"), f.config, "")
		attach(t, binding, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "Async started"}}, Mode: "single", RunID: "async-missing-mission", AsyncID: "async-missing-mission", AsyncDir: asyncDir})
		os.RemoveAll(binding.Location.MissionDir)
		event := func() *jsObject {
			return jo("runId", "async-missing-mission", "asyncDir", asyncDir, "mode", "single", "state", "complete", "success", true)
		}
		completed, err := syncMissionFromAsyncCompletion(event())
		eq(t, err, nil, "first")
		repeated, err := syncMissionFromAsyncCompletion(event())
		eq(t, err, nil, "second")
		eq(t, completed == nil && repeated == nil, true, "nothing recorded")
		raw, _ := os.ReadFile(filepath.Join(asyncDir, "events.jsonl"))
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		eq(t, len(lines), 1, "one event")
		parsed, _ := missionParseJSON(lines[0])
		ev := parsed.(*jsObject)
		ts, isNum := ev.vals["ts"].(float64)
		eq(t, isNum && ts > 0, true, "ts is a number")
		jsonEq(t, ev, marshalJSON(jo("type", "subagent.mission.sync.skipped", "ts", ts, "runId", "async-missing-mission", "missionId", binding.MissionID, "reason", "mission-record-missing", "missionPath", filepath.Join(binding.Location.MissionDir, binding.MissionID+".json")), ""))
		markers, _ := filepath.Glob(filepath.Join(asyncDir, ".mission-sync-skipped-*"))
		eq(t, len(markers), 1, "one marker")
	})

	tw(t, missionLifecycleFile, "binds an async workflow launch and preserves its mode through completion", func(t *testing.T) {
		f := newLifecycleFixture(t)
		asyncDir := filepath.Join(f.root, "async-run")
		os.MkdirAll(asyncDir, 0o755)
		binding := prepare(t, f.projectRoot, jo("mission", jo("title", "Background mission"), "task", "Run later"), f.config, "")
		attach(t, binding, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "Async started"}}, Mode: "workflow", RunID: "async-1", AsyncID: "async-1", AsyncDir: asyncDir})
		bound, err := readMissionBinding(asyncDir)
		eq(t, err, nil, "binding")
		eq(t, bound.MissionID, binding.MissionID, "bound mission")
		eq(t, readOf(t, binding).Status, "active", "status")
		completed, err := syncMissionFromAsyncCompletion(jo("id", "async-1", "runId", "async-1", "asyncDir", asyncDir, "mode", "workflow", "state", "complete", "success", true, "summary", "Background work completed",
			"results", []any{jo("artifactPath", filepath.Join(asyncDir, "output-0.log"))}))
		eq(t, err, nil, "sync")
		eq(t, completed.Status, "completed", "mission status")
		eq(t, completed.Runs[0].Status, "complete", "run status")
		eq(t, completed.Runs[0].Mode, "workflow", "run mode")
		found := false
		for _, a := range completed.Artifacts {
			found = found || a.Kind == "output"
		}
		eq(t, found, true, "output artifact")
	})
}
