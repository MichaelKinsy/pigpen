package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The port's own tests of the mission and prompt-workflow behaviors the original's cases leave open (one per mutation the
// twins did not kill).

func TestMissionRecordValidation(t *testing.T) {
	f := newMissionFixture(t)
	base := func() *jsObject {
		return jo("schemaVersion", float64(1), "id", "m1", "title", "T", "objective", "O", "status", "planned", "createdAt", "2026-01-01T00:00:00.000Z", "updatedAt", "2026-01-01T00:00:00.000Z",
			"runs", []any{}, "decisions", []any{}, "artifacts", []any{})
	}
	_, err := parseMissionRecord(base(), "r")
	eq(t, err, nil, "valid base")
	_, err = parseMissionRecord(joOf(base(), "id", "a..b"), "r")
	matchesRe(t, err, `cannot contain '\.\.'`)
	_, err = parseMissionRecord(joOf(base(), "goal", jo("status", "active")), "r")
	matchesRe(t, err, `r\.budget is required for a goal mission`)
	_, err = parseMissionRecord(joOf(base(), "objective", "   "), "r")
	matchesRe(t, err, `r\.objective must be a non-empty string`)
	r, err := parseMissionRecord(joOf(base(), "labels", []any{" a ", "a", "b"}), "r")
	eq(t, err, nil, "labels")
	eq(t, r.Labels, []string{"a", "b"}, "labels are trimmed and unique")
	_, err = parseMissionRecord(joOf(base(), "createdAt", "yesterday"), "r")
	matchesRe(t, err, `r\.createdAt must be an ISO timestamp`)
	_ = f
}

func TestMissionStoreLocations(t *testing.T) {
	root := tmp(t)
	a := resolveMissionStoreLocation(filepath.Join(root, "a"), nil, filepath.Join(root, "agent"))
	b := resolveMissionStoreLocation(filepath.Join(root, "b"), nil, filepath.Join(root, "agent"))
	eq(t, a.MissionDir != b.MissionDir, true, "each project has its own directory")
	eq(t, a.MissionDir, filepath.Join(root, "agent", "missions", "projects", sha256Hex(filepath.Join(root, "a"))), "directory key")
	eq(t, a.GlobalIndexDir, filepath.Join(root, "agent", "missions", "index"), "index directory")
	off := resolveMissionStoreLocation(filepath.Join(root, "a"), &MissionStoreConfig{GlobalIndex: new(false)}, filepath.Join(root, "agent"))
	eq(t, off.WriteGlobalIndex, false, "globalIndex false")
	cfg, err := validateMissionStoreConfig(jo("enabled", true, "retainTerminal", float64(3)), true, "config.missions")
	eq(t, err, nil, "config")
	eq(t, *cfg.RetainTerminal, int64(3), "retainTerminal")
	for _, c := range []struct {
		v  *jsObject
		re string
	}{
		{jo("bogus", 1), `config\.missions\.bogus is unknown`},
		{jo("enabled", "yes"), `enabled must be boolean`},
		{jo("retainTerminal", float64(0)), `retainTerminal must be a positive integer`},
		{jo("directory", " "), `directory must be a non-empty string`},
	} {
		_, err := validateMissionStoreConfig(c.v, true, "config.missions")
		matchesRe(t, err, c.re)
	}
	f := newMissionFixture(t)
	noIndex := f.loc
	noIndex.WriteGlobalIndex = false
	mkMission(t, noIndex, MissionCreateInput{Title: "A", Objective: "B"})
	_, statErr := os.Stat(f.loc.GlobalIndexDir)
	eq(t, os.IsNotExist(statErr), true, "no index written")
}

func TestMissionCreationPrunesTerminalMissions(t *testing.T) {
	f := newMissionFixture(t)
	loc := f.loc
	loc.RetainTerminal = new(int64(1))
	at := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
	first, err := createMission(loc, MissionCreateInput{Title: "a", Objective: "a", Status: "completed"}, at("2026-01-01T00:00:00Z"), -1)
	eq(t, err, nil, "first")
	_, err = createMission(loc, MissionCreateInput{Title: "b", Objective: "b", Status: "failed"}, at("2026-01-02T00:00:00Z"), -1)
	eq(t, err, nil, "second")
	_, err = readMission(loc, first.ID)
	matchesRe(t, err, `was not found`)
}

func TestMissionListingOrdersNewestFirstAndChecksPointers(t *testing.T) {
	f := newMissionFixture(t)
	at := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
	old, _ := createMission(f.loc, MissionCreateInput{Title: "old", Objective: "o"}, at("2026-01-01T00:00:00Z"), -1)
	newer, _ := createMission(f.loc, MissionCreateInput{Title: "new", Objective: "n"}, at("2026-02-01T00:00:00Z"), -1)
	listed, _ := listMissions(f.loc)
	eq(t, []string{listed.Records[0].ID, listed.Records[1].ID}, []string{newer.ID, old.ID}, "newest first")
	global := listGlobalMissions(f.loc.GlobalIndexDir)
	eq(t, []string{global.Entries[0].MissionID, global.Entries[1].MissionID}, []string{newer.ID, old.ID}, "index newest first")
	// a pointer whose record carries another id is stale, not healed
	raw, _ := os.ReadFile(mustRecordPath(f.loc, old.ID))
	writeFile(t, mustRecordPath(f.loc, newer.ID), string(raw))
	global = listGlobalMissions(f.loc.GlobalIndexDir)
	stale := 0
	for _, e := range global.Entries {
		if e.Stale && strings.Contains(e.StaleReason, "does not match index id") {
			stale++
		}
	}
	eq(t, stale, 1, "id mismatch is stale")
}

func TestMissionUpdateMergesAndGoals(t *testing.T) {
	f := newMissionFixture(t)
	m := mkMission(t, f.loc, MissionCreateInput{Title: "T", Objective: "O", Goal: true, Budget: &MissionTokenBudget{Tokens: 100}, Status: "active"})
	child0, child1 := int64(0), int64(1)
	u := updMission(t, f.loc, m.ID, MissionUpdateInput{AddRuns: []MissionRunLink{
		{RunID: "r", Mode: "single", ChildIndex: &child0, Status: "running", Usage: &MissionTokenUsage{Tokens: 30}},
		{RunID: "r", Mode: "single", ChildIndex: &child1, Status: "running", Usage: &MissionTokenUsage{Tokens: 40}},
	}})
	eq(t, len(u.Runs), 2, "the same run id with another child index is another run")
	eq(t, u.Usage.Tokens, int64(70), "usage is the sum of the runs")
	u = updMission(t, f.loc, m.ID, MissionUpdateInput{AddRuns: []MissionRunLink{{RunID: "r", Mode: "single", ChildIndex: &child0, Status: "complete"}}})
	eq(t, u.Runs[0].Status, "complete", "a run link takes the new status")
	eq(t, u.Runs[0].Usage.Tokens, int64(30), "and keeps what the update leaves out")
	u = updMission(t, f.loc, m.ID, MissionUpdateInput{Usage: &MissionTokenUsage{Tokens: 100}})
	eq(t, u.Goal.Status, "budget-exhausted", "spent budget")
	u = updMission(t, f.loc, m.ID, MissionUpdateInput{Budget: &MissionTokenBudget{Tokens: 500}, Usage: &MissionTokenUsage{Tokens: 100}})
	eq(t, u.Goal.Status, "active", "a larger budget reopens the goal")
	_, err := updateMission(f.loc, m.ID, MissionUpdateInput{ResolveDecision: &MissionResolveDecision{ID: "nope", Resolution: "x"}}, time.Now(), -1)
	matchesRe(t, err, `Decision 'nope' was not found in mission '`+m.ID+`'`)
	_, err = updateMission(f.loc, m.ID, MissionUpdateInput{Goal: &MissionGoalUpdate{Off: true}, Budget: nil}, time.Now(), -1)
	eq(t, err, nil, "goal off")
}

func TestMissionWorkflowChildPathsAreAUnion(t *testing.T) {
	f := newMissionFixture(t)
	m := mkMission(t, f.loc, MissionCreateInput{Title: "T", Objective: "O", Status: "active"})
	for _, paths := range [][]string{{"/a", "/b"}, {"/b", "/c"}} {
		updMission(t, f.loc, m.ID, MissionUpdateInput{UpsertWorkflowChildren: []MissionWorkflowChildUpdate{{WorkflowRunID: "w", Key: "k", Status: "running", ArtifactPaths: paths}}})
	}
	r, _ := readMission(f.loc, m.ID)
	eq(t, r.WorkflowChildren[0].ArtifactPaths, []string{"/a", "/b", "/c"}, "union without repeats")
}

func TestMissionActionDetails(t *testing.T) {
	f := newMissionFixture(t)
	ctx := f.ctx()
	created := runAction(t, "mission.create", jo("mission", jo("title", "T", "objective", "O")), ctx)
	id := created.Details["missionId"].(string)
	attached := runAction(t, "mission.attach-run", jo("missionId", id, "runId", " r1 ", "runMode", "single"), ctx)
	eq(t, detailMission(attached).Status, "active", "an attached run makes the mission active")
	eq(t, detailMission(attached).Runs[0].RunID, "r1", "run id is trimmed")
	eq(t, attached.Text, "Attached run  r1  to mission "+id+".", "text keeps the id as given")
	pending := runAction(t, "mission.update", jo("missionId", id, "missionUpdate", jo("decisions", []any{jo("title", "D")})), ctx)
	decision := detailMission(pending).Decisions[0].ID
	resolved := runAction(t, "mission.resolve-decision", jo("missionId", id, "id", decision, "summary", "  yes  "), ctx)
	eq(t, detailMission(resolved).Decisions[0].Resolution, "yes", "resolution is trimmed")
	_, err := handleMissionAction("mission.update", jo("missionId", id, "missionUpdate", jo("receipts", []any{jo("kind", "ci", "status", "ready", "title", "t", "url", "https://x.example/a", "extra", 1)})), ctx)
	matchesRe(t, err, `missionUpdate\.receipts\[0\]\.extra is unknown`)
	_, err = handleMissionAction("mission.close", jo("missionId", id, "missionStatus", "active"), ctx)
	matchesRe(t, err, `completed, failed, or cancelled`)
}

func TestMissionLaunchAttachmentDetails(t *testing.T) {
	f := newLifecycleFixture(t)
	goal := prepare(t, f.projectRoot, jo("mission", jo("title", "G", "goal", true, "budget", jo("tokens", float64(10))), "task", "x"), f.config, "")
	done := attach(t, goal, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "ok"}}, Mode: "single", RunID: "r", Results: []MissionChildResult{{Agent: "a"}}})
	eq(t, done.Mission.Status, "active", "a goal mission stays active after a completed run")
	paused := prepare(t, f.projectRoot, jo("mission", jo("title", "P"), "task", "x"), f.config, "")
	res := attach(t, paused, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "ok"}}, Mode: "single", RunID: "r", Results: []MissionChildResult{{Agent: "a", Interrupted: true}}})
	eq(t, res.Mission.Runs[0].Status, "paused", "an interrupted run is paused")
	eq(t, res.Mission.Status, "waiting", "and the mission waits")
	long := strings.Repeat("é", 2500)
	b := prepare(t, f.projectRoot, jo("mission", jo("title", "L"), "task", "x"), f.config, "")
	res = attach(t, b, MissionToolResult{Content: []MissionContent{{Type: "text", Text: long}}, Mode: "single", RunID: "r", Results: []MissionChildResult{{Agent: "a"}}})
	eq(t, utf16Units(res.Mission.Summary), 2000, "the summary is bounded")
	eq(t, strings.HasSuffix(res.Mission.Summary, "..."), true, "with its marker")
	asyncDir := filepath.Join(f.root, "run")
	os.MkdirAll(asyncDir, 0o755)
	b2 := prepare(t, f.projectRoot, jo("mission", jo("title", "S"), "task", "x"), f.config, "")
	attach(t, b2, MissionToolResult{Content: []MissionContent{{Type: "text", Text: "go"}}, Mode: "single", RunID: "r2", AsyncDir: asyncDir})
	synced, err := syncMissionFromAsyncCompletion(jo("runId", "r2", "asyncDir", asyncDir, "state", "complete", "summary", strings.Repeat("x", 3000), "totalTokens", jo("total", float64(5))))
	eq(t, err, nil, "sync")
	eq(t, len(synced.Summary), 2000, "an async summary is bounded")
	eq(t, synced.Runs[0].Usage.Tokens, int64(5), "usage from the total")
	eq(t, synced.Runs[0].Mode, "external", "a run of unknown mode is external")
}

func TestStateLockProbes(t *testing.T) {
	f := newMissionFixture(t)
	m := mkMission(t, f.loc, MissionCreateInput{Title: "T", Objective: "O"})
	statePath, _ := missionStatePath(f.loc, m.ID)
	lockPath := statePath + ".lock"
	owner := func(pid int, key string) {
		os.RemoveAll(lockPath)
		writeFile(t, filepath.Join(lockPath, "owner.json"), marshalJSON(jo("pid", float64(pid), "token", "t", "createdAt", float64(time.Now().UnixMilli()), "processKey", key), ""))
	}
	// a dead owner is reclaimed
	owner(4242, "k")
	state, _ := NewMissionWorkflowState(f.loc, m.ID, MissionWorkflowStateOptions{IsProcessAlive: func(int) bool { return false }, GetProcessStartKey: func(int) string { return "" }})
	eq(t, state.Set("a", float64(1)), nil, "dead owner")
	// a live foreign owner whose identity cannot be read is kept
	owner(4242, "k")
	state, _ = NewMissionWorkflowState(f.loc, m.ID, MissionWorkflowStateOptions{IsProcessAlive: func(int) bool { return true }, GetProcessStartKey: func(int) string { return "" }, RetryDelaysMs: []int{}})
	matchesRe(t, state.Set("b", float64(2)), `Timed out acquiring mission state lock`)
	// this process as the recorded owner, with no way to read identities, is a leftover of an earlier run
	owner(os.Getpid(), "k")
	state, _ = NewMissionWorkflowState(f.loc, m.ID, MissionWorkflowStateOptions{GetProcessStartKey: func(int) string { return "" }, RetryDelaysMs: []int{}})
	eq(t, state.Set("c", float64(3)), nil, "own pid without identity")
	// a lock of another owner is never removed by us
	owner(4242, "k")
	removeOwnedStateLock(lockPath, &stateLockOwner{token: "someone-else"})
	_, err := os.Stat(lockPath)
	eq(t, err, nil, "a foreign lock stays")
	removeOwnedStateLock(lockPath, &stateLockOwner{token: "t"})
	_, err = os.Stat(lockPath)
	eq(t, os.IsNotExist(err), true, "our own lock goes")
}

func TestGoalNoticeBoundsAndOwners(t *testing.T) {
	f := newMissionFixture(t)
	long := strings.Repeat("word ", 80)
	mkMission(t, f.loc, MissionCreateInput{Title: long, Objective: "o", Goal: true, Budget: &MissionTokenBudget{Tokens: 100}, Status: "active", OwnerSessionID: "session-1"})
	mkMission(t, f.loc, MissionCreateInput{Title: "foreign", Objective: "o", Goal: true, Budget: &MissionTokenBudget{Tokens: 100}, Status: "active", OwnerSessionID: "session-2"})
	n := notices(t, f, nil, 7)
	eq(t, len(n), 1, "only the owner's mission")
	title := strings.Split(n[0].Message, "\n")[0]
	eq(t, utf16Units(strings.TrimPrefix(title, "Goal mission needs attention: ")), maxGoalActionLength, "a title is bounded to 180 units")
	eq(t, strings.HasSuffix(title, "…"), true, "with an ellipsis")
	eq(t, strings.Contains(n[0].Event.RunID, "-turn-7"), true, "turn id")
	eq(t, n[0].Event.Reason, "idle", "reason")
}

func TestPromptWorkflowOptions(t *testing.T) {
	_, cwd := promptFixture(t)
	dir := filepath.Join(cwd, ".pi", "prompts")
	writePrompt(t, dir, "run", "---\ndescription: reserved\n---\nbody\n")
	writePrompt(t, dir, "skillless", "---\nskill: false\nsubagent: true\n---\nDo $1 and ${2:-fallback} costs $$ and $0x\n")
	ws, err := discoverPromptWorkflows(cwd)
	eq(t, err, nil, "discover")
	eq(t, findPrompt(ws, "run") == nil, true, "a reserved command name is not a workflow")
	w := findPrompt(ws, "skillless")
	eq(t, w.Skill, false, "skill: false")
	eq(t, w.Agent, "delegate", "subagent: true is the default agent")
	eq(t, findPrompt(ws, "council") != nil, true, "the package's templates are found")

	p := newPromptEnv(cwd)
	eq(t, runPromptWorkflowCommand(p.env, `skillless "a b" --bg --fork --fresh --subagent=other`), nil, "run")
	params := p.runs[0]
	eq(t, params["async"], true, "--bg")
	script := params["workflowScript"].(string)
	contains(t, script, `"agent":"other"`)
	contains(t, script, `"skill":false`)
	contains(t, script, `"context":"fork"`)
	contains(t, script, `Do a b and fallback costs $$ and `)

	eq(t, substituteArgs("x $ARGUMENTS y", []string{"$$", "z"}), "x $ z y", "a replacement's $$ is one $")
	eq(t, substituteArgs("$@|${1:-d}|${3:-d}|$2|$9", []string{"", "b"}), " b|d|d|b|", "positional arguments and fallbacks")
	eq(t, shellWords(`a "b c" 'd e' f\ g ""`), []string{"a", "b c", "d e", "f g"}, "shell words")

	q := newPromptEnv(cwd)
	eq(t, runPromptWorkflowCommand(q.env, ""), nil, "no name")
	eq(t, len(q.sent), 1, "a bare command lists")
	contains(t, q.sent[0], "Prompt workflows:")
	r := newPromptEnv(cwd)
	eq(t, runPromptWorkflowCommand(r.env, "skillless --subagent reviewer"), nil, "separate override")
	contains(t, r.runs[0]["workflowScript"].(string), `"agent":"reviewer"`)
	writePrompt(t, dir, "broken-chain", "---\nchain: skillless -> missing-step\n---\nx\n")
	s := newPromptEnv(cwd)
	eq(t, runPromptWorkflowCommand(s.env, "broken-chain"), nil, "chain")
	eq(t, s.notes, []string{"error: Unknown prompt workflow in chain 'broken-chain': missing-step"}, "unknown chain step")
	t.Setenv("HOME", cwd)
}
