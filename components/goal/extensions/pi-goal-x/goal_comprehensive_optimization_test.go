package pi_goal_x

// Twins of tests/goal-comprehensive-optimization.test.ts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const gcoFile = "goal-comprehensive-optimization"

type gcoFixture struct {
	cwd  string
	goal *jsObject
	ctx  storage
}

func gcoNew(t *testing.T) gcoFixture {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi/goals"), 0o755); err != nil {
		t.Fatal(err)
	}
	invalidateGoalPoolCache()
	invalidateGoalLedgerCache()
	invalidateGoalSettingsCache()
	t.Cleanup(func() { invalidateGoalPoolCache(); invalidateGoalLedgerCache(); invalidateGoalSettingsCache() })
	return gcoFixture{cwd: cwd, goal: createGoal("Implement every requirement 🧭", true, false, false, 1_700_000_000_000), ctx: storage{cwd: cwd}}
}

func gcoWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoalComprehensiveOptimization(t *testing.T) {
	tw(t, gcoFile, "auditor stream tails match complete report processing, including blank lines and CRLF", func(t *testing.T) {
		for _, text := range []string{"", "\n", "one\n\n two \r\n\t\nthree\n", strings.Repeat("🧭", 10000), strings.Repeat("line\n", 10000)} {
			for _, limit := range []int{1, 5, 8} {
				var all []string
				for _, line := range strings.Split(text, "\n") {
					if jsTrim(line) != "" {
						all = append(all, line)
					}
				}
				if len(all) > limit {
					all = all[len(all)-limit:]
				}
				got := recentNonEmptyLines(text, limit)
				if len(got) != len(all) || (len(all) > 0 && !reflect.DeepEqual(got, all)) {
					t.Errorf("limit %d: got %d lines, want %d", limit, len(got), len(all))
				}
			}
		}
	})

	tw(t, gcoFile, "resolved settings invalidate on environment, layer mutations and explicit refresh; results remain caller-owned", func(t *testing.T) {
		f := gcoNew(t)
		env := map[string]string{piGoalSettingsFileEnv: filepath.Join(f.cwd, "project.json"), piGoalGlobalSettingsFileEnv: filepath.Join(f.cwd, "global.json"), "PI_GOAL_DISABLE_TASKS": "false"}
		first := loadGoalSettings(f.cwd, env)
		first.disableTasks = true
		first.oracle.enabled = true
		first.keybindings.dashboard.toggleExpand = "escape"
		if loadGoalSettings(f.cwd, env).disableTasks {
			t.Error("disableTasks leaked")
		}
		if loadGoalSettings(f.cwd, env).oracle.enabled {
			t.Error("oracle.enabled leaked")
		}
		if loadGoalSettings(f.cwd, env).keybindings.dashboard.toggleExpand == "escape" {
			t.Error("keybinding leaked")
		}
		env["PI_GOAL_DISABLE_TASKS"] = "true"
		if !loadGoalSettings(f.cwd, env).disableTasks {
			t.Error("environment change not observed")
		}
		snapshot := loadSettingsSnapshot(f.cwd, env)
		snapshot.provenance["disableTasks"].source = "default"
		for k := range snapshot.provenance {
			delete(snapshot.provenance, k)
		}
		if got := loadSettingsSnapshot(f.cwd, env).provenance["disableTasks"].source; got != "environment" {
			t.Errorf("provenance source %q", got)
		}
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: settingsScopeProject, cwd: f.cwd, env: env, mutation: settingsMutation{op: "set", path: []string{"hideUnfocusedBanner"}, value: true}}); err != nil {
			t.Fatal(err)
		}
		if !loadGoalSettings(f.cwd, env).hideUnfocusedBanner {
			t.Error("layer mutation not observed")
		}
		gcoWrite(t, env[piGoalSettingsFileEnv], `{"hideUnfocusedBanner":false}`)
		invalidateGoalSettingsCache()
		if loadGoalSettings(f.cwd, env).hideUnfocusedBanner {
			t.Error("explicit refresh not observed")
		}
	})

	tw(t, gcoFile, "task and prompt caches observe nested edits, reparenting, focus and settings without retaining mutable inputs", func(t *testing.T) {
		f := gcoNew(t)
		tasks := []any{
			jo(t, `{"id":"p","title":"Parent","status":"pending","subtasks":[{"id":"c","title":"Child","status":"pending","verificationContract":"Original contract"}]}`),
			jo(t, `{"id":"n","title":"Next","status":"pending"}`),
		}
		goal := with(f.goal, "taskList", with(newObject(), "tasks", tasks, "blockCompletion", true, "proposedAt", "today"), "currentTaskId", "c")
		old := taskIndex(tasks)
		original := goalPrompt(goal, nil)
		child := tasks[0].(*jsObject).vals["subtasks"].([]any)[0].(*jsObject)
		child.set("verificationContract", "Revised contract")
		child.set("evidence", "Full evidence 🧭")
		if taskIndex(tasks) == old {
			t.Error("index not rebuilt after nested edit")
		}
		if got := gstr(old.byId["c"], "verificationContract"); got != "Original contract" {
			t.Errorf("old index retained mutable input: %q", got)
		}
		if !strings.Contains(goalPrompt(goal, nil), "Revised contract") || goalPrompt(goal, nil) == original {
			t.Error("prompt did not observe the edit")
		}
		child.set("status", "complete")
		tasks[0].(*jsObject).set("subtasks", []any{})
		tasks[1].(*jsObject).set("subtasks", []any{child})
		found := false
		for _, row := range taskIndex(tasks).ordered {
			if gstr(row.task, "id") == "c" {
				found = true
				if row.parentId != "n" {
					t.Errorf("parentId %q", row.parentId)
				}
			}
		}
		if !found {
			t.Error("c missing")
		}
		goal.set("currentTaskId", "n")
		if !strings.Contains(goalPrompt(goal, nil), "Current: n") {
			t.Error("current task not reflected")
		}
		if strings.Contains(goalPrompt(goal, &goalSettings{disableTasks: true}), "TASK LIST") {
			t.Error("disableTasks ignored")
		}
		copied := deepClone(tasks).([]any)
		if taskIndex(copied) != taskIndex(tasks) {
			t.Error("equal task lists missed the cache")
		}
	})

	tw(t, gcoFile, "cached history pages remain lossless and reject stale cursors after append or external refresh", func(t *testing.T) {
		f := gcoNew(t)
		var events []*jsObject
		for i := range 80 {
			events = append(events, ledgerEvent("task_complete", "goalId", gstr(f.goal, "id"), "taskId", "t"+strconv.Itoa(i), "evidence", strings.Repeat("🧭", 100), "at", "2026-09-07"))
		}
		appendGoalEvents(f.ctx, events)
		read := readGoalLedger(f.ctx)
		q := goalDetailQuery{section: "history"}
		first := goalDetailPage(f.goal, q, read.events)
		if !first.ok || first.nextCursor == "" {
			t.Fatal("first page")
		}
		cursor, content := "", ""
		for {
			page := goalDetailPage(f.goal, goalDetailQuery{section: "history", cursor: cursor}, read.events)
			if !page.ok {
				t.Fatal("page not ok")
			}
			content += page.content
			cursor = page.nextCursor
			if cursor == "" {
				break
			}
		}
		var lines []string
		for _, e := range events {
			lines = append(lines, marshalJSON(e, ""))
		}
		if content != strings.Join(lines, "\n") {
			t.Error("pages are not lossless")
		}
		extra := events[0].clone()
		extra.set("taskId", "new")
		appendGoalEvents(f.ctx, []*jsObject{extra})
		appended := readGoalLedger(f.ctx)
		if appended.revision == read.revision {
			t.Error("revision unchanged after append")
		}
		if goalDetailPage(f.goal, goalDetailQuery{section: "history", cursor: first.nextCursor}, appended.events).ok {
			t.Error("stale cursor accepted after append")
		}
		gcoWrite(t, filepath.Join(f.cwd, ".pi/goals/goal_events.jsonl"), marshalJSON(events[0], "")+"\n")
		invalidateGoalLedgerCache()
		refreshed := readGoalLedger(f.ctx)
		if goalDetailPage(f.goal, goalDetailQuery{section: "history", cursor: first.nextCursor}, refreshed.events).ok {
			t.Error("stale cursor accepted after refresh")
		}
		own := append([]*jsObject{}, events...)
		ownPage := goalDetailPage(f.goal, q, own)
		if !ownPage.ok {
			t.Fatal("own page")
		}
		own = own[:len(own)-1]
		if goalDetailPage(f.goal, goalDetailQuery{section: "history", cursor: ownPage.nextCursor}, own).ok {
			t.Error("stale cursor accepted for edited own events")
		}
	})

	tw(t, gcoFile, "ledger sanitation remains correct beyond the bounded goal-ID intern cache", func(t *testing.T) {
		f := gcoNew(t)
		var events []*jsObject
		for i := range 1100 {
			events = append(events, ledgerEvent("goal_paused", "goalId", "goal/"+strconv.Itoa(i), "reason", "Keep full Unicode evidence 🧭", "at", "2026-09-07"))
		}
		events = append(events, events[0].clone(), events[1099].clone())
		var lines []string
		for _, e := range events {
			lines = append(lines, marshalJSON(e, ""))
		}
		gcoWrite(t, filepath.Join(f.cwd, ".pi/goals/goal_events.jsonl"), strings.Join(lines, "\n"))
		invalidateGoalLedgerCache()
		result := readGoalLedger(f.ctx)
		if result.malformed != 0 || len(result.events) != 1102 {
			t.Fatalf("malformed %d events %d", result.malformed, len(result.events))
		}
		for i, e := range result.events {
			if evStr(e, "type") != "goal_paused" {
				t.Fatalf("type at %d", i)
			}
			if want := strings.Replace(evStr(events[i], "goalId"), "/", "_", 1); evStr(e, "goalId") != want {
				t.Fatalf("goalId at %d: %q want %q", i, evStr(e, "goalId"), want)
			}
			if evStr(e, "reason") != evStr(events[i], "reason") {
				t.Fatalf("reason at %d", i)
			}
		}
	})

	tw(t, gcoFile, "text caches preserve SDK ANSI, Unicode, padding and width semantics and return independent wrapped rows", func(t *testing.T) {
		for _, text := range []string{"plain words", "á 🧭 中文 👩‍💻", "\x1b[31mred\x1b[0m normal\nnext", "\x1b]8;;https://example.com\x07linked\x1b]8;;\x07", ""} {
			for _, width := range []int{0, 1, 4, 10, 80} {
				for _, pad := range []bool{false, true} {
					for _, ellipsis := range []string{"...", "…", ""} {
						if got, want := truncateToWidth(text, width, ellipsis, pad), widthx.TruncateToWidth(text, width, ellipsis, pad); got != want {
							t.Errorf("truncate %q w=%d pad=%v e=%q: %q want %q", text, width, pad, ellipsis, got, want)
						}
					}
				}
				rows := wrapTextWithAnsi(text, width)
				if !reflect.DeepEqual(rows, widthx.WrapTextWithAnsi(text, width)) {
					t.Errorf("wrap %q w=%d", text, width)
				}
				rows = append(rows, "poison")
				if !reflect.DeepEqual(wrapTextWithAnsi(text, width), widthx.WrapTextWithAnsi(text, width)) {
					t.Errorf("wrap rows not independent %q w=%d", text, width)
				}
			}
		}
	})

	tw(t, gcoFile, "context normalization preserves all marker positions and ignores mutable goal state", func(t *testing.T) {
		f := gcoNew(t)
		marker := jo(t, `{"customType":"pi-goal-event","content":"legacy","details":{"goalId":"`+gstr(f.goal, "id")+`","kind":"checkpoint","version":1}}`)
		a, b, c := jo(t, `{"role":"user","content":"a"}`), jo(t, `{"role":"assistant","content":"b"}`), jo(t, `{"role":"toolResult","content":"c"}`)
		result := compactGoalCheckpointContext([]any{a, marker, b, marker, c})
		if len(result) != 5 || result[0] != any(a) || result[2] != any(b) || result[4] != any(c) {
			t.Fatalf("positions not preserved: %v", result)
		}
		kind := func(m any) any { return asRecord(m).obj("details").vals["kind"] }
		if kind(result[1]) != "checkpoint" || kind(result[3]) != "checkpoint" {
			t.Error("marker details.kind")
		}
		if kind(compactGoalCheckpointContext([]any{marker})[0]) != "checkpoint" {
			t.Error("lone marker")
		}
		if compactGoalCheckpointContext([]any{a, b, c}) != nil {
			t.Error("no markers must be null")
		}
	})

	tw(t, gcoFile, "cold pool results do not expose the cached Map and archive updates remove timestamped paths from snapshots", func(t *testing.T) {
		f := gcoNew(t)
		goal := mustWrite(t, f.ctx, f.goal)
		invalidateGoalPoolCache()
		f.ctx.readActiveGoalPool().delete(gstr(goal, "id"))
		if n := f.ctx.readActiveGoalPool().size(); n != 1 {
			t.Errorf("pool size %d", n)
		}
		invalidateGoalPoolCache()
		// readActiveGoalPoolAsync is the same cold read in Go (no event loop to yield to).
		f.ctx.readActiveGoalPool().delete(gstr(goal, "id"))
		if n := f.ctx.readActiveGoalPool().size(); n != 1 {
			t.Errorf("pool size %d after cold read", n)
		}
		mustArchive(t, f.ctx, goal)
		raw, err := os.ReadFile(filepath.Join(f.cwd, ".pi/.goals-pool-snapshot.json"))
		if err != nil {
			t.Fatal(err)
		}
		var snap struct {
			Goals []any `json:"goals"`
		}
		if err := json.Unmarshal(raw, &snap); err != nil || len(snap.Goals) != 0 {
			t.Errorf("snapshot goals %v (%v)", snap.Goals, err)
		}
	})

	tw(t, gcoFile, "streamed diagnostic accumulation matches parsed entries despite Unicode and malformed lines", func(t *testing.T) {
		f := gcoNew(t)
		entries := []any{
			jo(t, `{"type":"message","message":{"content":"ordinary 🧭"}}`),
			jo(t, `{"type":"custom_message","customType":"pi-goal-event","content":"`+strings.Repeat("🧭", 4000)+`","details":{"version":1}}`),
		}
		var lines []string
		for _, e := range entries {
			lines = append(lines, marshalJSON(e, ""))
		}
		file := filepath.Join(f.cwd, "session.jsonl")
		gcoWrite(t, file, strings.Join(lines, "\nBAD JSON\n")+"\n\n")
		if got, want := readSessionCheckpointHealth(file), inspectCheckpointHealth(entries); !reflect.DeepEqual(got, want) {
			t.Errorf("streamed %+v parsed %+v", got, want)
		}
	})

	tw(t, gcoFile, "task updates copy only changed ancestors and leave unrelated branches and the original intact", func(t *testing.T) {
		tasks := []any{
			jo(t, `{"id":"p","title":"Parent","status":"pending","subtasks":[{"id":"c","title":"Child","status":"pending"}]}`),
			jo(t, `{"id":"n","title":"Next","status":"pending","subtasks":[]}`),
		}
		next := updateTaskInTree(tasks, "c", func(task *jsObject) *jsObject {
			return with(task, "status", "complete", "evidence", "Verified")
		})
		sub := func(list []any) *jsObject { return list[0].(*jsObject).vals["subtasks"].([]any)[0].(*jsObject) }
		if gstr(sub(tasks), "status") != "pending" {
			t.Error("original mutated")
		}
		if gstr(sub(next), "status") != "complete" {
			t.Error("update missing")
		}
		if next[1] != tasks[1] {
			t.Error("unrelated branch copied")
		}
		missing := updateTaskInTree(next, "missing", func(task *jsObject) *jsObject { return task })
		if len(missing) != len(next) || reflect.ValueOf(missing).Pointer() != reflect.ValueOf(next).Pointer() {
			t.Error("missing id must return the same list")
		}
	})

	tw(t, gcoFile, "Oracle state survives checkpoint reload and corrupt derived state rebuilds from the ledger", func(t *testing.T) {
		f := gcoNew(t)
		id := gstr(f.goal, "id")
		appendGoalEvents(f.ctx, []*jsObject{
			ledgerEvent("goal_created", "goalId", id, "objective", gstr(f.goal, "objective"), "sisyphus", false, "autoContinue", true, "at", "2026-09-07"),
			ledgerEvent("oracle_result", "goalId", id, "fingerprint", "fp", "adviceId", "advice", "disposition", "actionable", "summary", "Try the verified alternative 🧭", "at", "2026-09-07"),
		})
		invalidateGoalLedgerCache()
		full := loadLedgerState(f.ctx)
		invalidateGoalLedgerCache()
		cached := loadLedgerState(f.ctx)
		if cached.source != "checkpoint" {
			t.Fatalf("source %q", cached.source)
		}
		oracle := func(r ledgerStateReadResult) string {
			g, _ := r.state.goals.get(id)
			return marshalJSON(g.latestOracleResult, "")
		}
		if oracle(cached) != oracle(full) {
			t.Error("checkpoint reload lost the oracle result")
		}
		file := filepath.Join(f.cwd, ".pi/goals", ledgerCheckpointFile)
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		damages := []func(m map[string]any){
			func(m map[string]any) {
				m["acc"].(map[string]any)["goals"].([]any)[0].(map[string]any)["latestOracleResult"].(map[string]any)["disposition"] = "invalid"
			},
			func(m map[string]any) {
				rt := m["runtimeIndex"].([]any)[0].([]any)[1].(map[string]any)
				rt["oracle"].([]any)[0].([]any)[1].(map[string]any)["result"].(map[string]any)["summary"] = nil
			},
			func(m map[string]any) { delete(m["acc"].(map[string]any), "goals") },
		}
		for i, damage := range damages {
			var copyOf map[string]any
			if err := json.Unmarshal(raw, &copyOf); err != nil {
				t.Fatal(err)
			}
			damage(copyOf)
			out, _ := json.Marshal(copyOf)
			gcoWrite(t, file, string(out))
			invalidateGoalLedgerCache()
			rebuilt := loadLedgerState(f.ctx)
			if rebuilt.source != "full" {
				t.Errorf("damage %d: source %q", i, rebuilt.source)
			}
			if oracle(rebuilt) != oracle(full) {
				t.Errorf("damage %d: oracle result differs", i)
			}
		}
	})

	tw(t, gcoFile, "compaction resync bounds oversized task text and points to lossless requirements", func(t *testing.T) {
		f := gcoNew(t)
		contract := strings.Repeat("Verify 🧭 ", 10000)
		task := jo(t, `{"id":"task","title":"`+strings.Repeat("Long title ", 1000)+`","status":"pending"}`)
		task.set("verificationContract", contract)
		goal := with(f.goal, "currentTaskId", "task", "taskList", with(newObject(), "tasks", []any{task}, "proposedAt", "today", "blockCompletion", true))
		delta := buildPostCompactionGoalDelta(postCompactionDeltaArgs{goal: goal, ledgerEvents: nil, otherOpenCount: 0})
		if len(delta) >= 1500 {
			t.Errorf("delta %d chars", len(delta))
		}
		if !strings.Contains(delta, `get_goal(section="tasks")`) {
			t.Error("delta must point to get_goal(section=\"tasks\")")
		}
		tid := "task"
		detail := goalDetailPage(goal, goalDetailQuery{section: "tasks", taskId: &tid}, nil)
		if !detail.ok || detail.nextCursor == "" || detail.totalChars <= utf16Len(contract) {
			t.Errorf("detail ok=%v cursor=%q total=%d", detail.ok, detail.nextCursor, detail.totalChars)
		}
	})
}
