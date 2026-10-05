package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGoalCore(t *testing.T) {
	tw(t, "goal-core", "displayObjectiveTitle strips goal block boilerplate", func(t *testing.T) {
		eq(t, displayObjectiveTitle("=== Goal ===\nObjective: Build tests first\nSuccess criteria: pass"), "Build tests first", "english")
		eq(t, displayObjectiveTitle("=== Sisyphus Goal ===\n目标：严格执行三步\nSteps:\n1. x"), "严格执行三步", "chinese")
		eq(t, displayObjectiveTitle("Just a plain objective"), "Just a plain objective", "plain")
	})
	tw(t, "goal-core", "formatters preserve existing compact duration/token/status behavior", func(t *testing.T) {
		eq(t, formatDuration(-10), "0s", "negative")
		eq(t, formatDuration(65), "1m05s", "minutes")
		eq(t, formatDuration(3661), "1h01m01s", "hours")
		eq(t, formatTokenValue(999), "999 tokens", "999")
		eq(t, formatTokenValue(1200), "1.2K (1,200) tokens", "1200")
		eq(t, formatTokenValue(12000), "12K (12,000) tokens", "12000")
		eq(t, formatTokenValue(2_500_000), "2.5M (2,500,000) tokens", "2.5M")
		eq(t, truncateText(" a\n b\t c ", 20), "a b c", "white space")
		eq(t, truncateText("abcdefghij", 8), "abcde...", "cut")
	})
	tw(t, "goal-core", "goal display helpers derive labels and footer", func(t *testing.T) {
		g := jo(t, `{"objective":"=== Goal ===\nObjective: Build test scaffolding and split helpers","status":"active","autoContinue":true,"usage":{"activeSeconds":125,"tokensUsed":4500},"sisyphus":false}`)
		eq(t, statusLabel(g), "running", "label")
		if !regexp.MustCompile(`^goal: running \[2m05s 4.5K\] - === Goal === Objective:`).MatchString(footerStatus(g)) {
			t.Errorf("footer %q", footerStatus(g))
		}
		s := g.clone()
		s.set("sisyphus", true)
		eq(t, statusLabel(s), "sisyphus running", "sisyphus")
		p := g.clone()
		p.set("status", "paused")
		p.set("stopReason", "agent")
		eq(t, statusLabel(p), "paused (agent)", "agent pause")
	})
}

func TestGoalNotifications(t *testing.T) {
	n := buildGoalRunningNotification
	tw(t, "goal-notifications", "buildGoalRunningNotification shows Goal mode with auto-continue on", func(t *testing.T) {
		eq(t, n("=== Goal ===\nObjective: 研究 pi-goal 的 compact 行为\nSuccess criteria: answer", false, true), "● Goal running\n├─ ⟡ 研究 pi-goal 的 compact 行为\n└─ auto-continue on", "text")
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification shows Sisyphus mode with manual mode", func(t *testing.T) {
		eq(t, n("=== Sisyphus Goal ===\nObjective: Ship safely", true, false), "◆ Sisyphus running\n├─ ⟡ Ship safely\n└─ manual mode", "text")
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification handles empty objective", func(t *testing.T) {
		r := n("", false, true)
		for _, want := range []string{"● Goal running", "├─ ⟡", "└─ auto-continue on"} {
			if !strings.Contains(r, want) {
				t.Errorf("%q lacks %q", r, want)
			}
		}
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification handles very long objective title", func(t *testing.T) {
		r := n("=== Goal ===\nObjective: "+strings.Repeat("A", 200), true, false)
		if l := utf16Len(strings.Split(r, "\n")[1]); l >= 120 {
			t.Errorf("title line has %d units", l)
		}
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification shows Goal mode with manual mode", func(t *testing.T) {
		r := n("=== Goal ===\nObjective: Fix bug\nSuccess criteria: done", false, false)
		for _, want := range []string{"● Goal running", "├─ ⟡ Fix bug", "└─ manual mode"} {
			if !strings.Contains(r, want) {
				t.Errorf("%q lacks %q", r, want)
			}
		}
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification shows Sisyphus mode with auto-continue on", func(t *testing.T) {
		r := n("=== Sisyphus Goal ===\nObjective: Research", true, true)
		for _, want := range []string{"◆ Sisyphus running", "├─ ⟡ Research", "└─ auto-continue on"} {
			if !strings.Contains(r, want) {
				t.Errorf("%q lacks %q", r, want)
			}
		}
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification handles multiline objective gracefully", func(t *testing.T) {
		if !strings.Contains(n("=== Goal ===\nObjective: Task A\nStep 1: do x\nStep 2: do y", false, true), "├─ ⟡") {
			t.Error("no title line")
		}
	})
	tw(t, "goal-notifications", "notification works through ctx.ui.notify with mocked TUI", func(t *testing.T) {
		h := &fakeHost{}
		text := n("=== Goal ===\nObjective: Test\nSuccess criteria: pass", true, false)
		h.Notify(text, "info")
		eq(t, h.events[0][2], text, "message passed through")
		eq(t, h.events[0][1], "info", "type")
		if !strings.Contains(h.events[0][2].(string), "◆ Sisyphus running") {
			t.Error("not sisyphus")
		}
	})
	tw(t, "goal-notifications", "buildGoalRunningNotification produces consistent 3-line format", func(t *testing.T) {
		for _, a := range []struct {
			obj      string
			sis, aut bool
		}{{"=== Goal ===\nObjective: A", false, true}, {"=== Sisyphus Goal ===\nObjective: B", true, false}, {"=== Goal ===\nObjective: C", false, false}, {"=== Sisyphus Goal ===\nObjective: D", true, true}} {
			lines := strings.Split(n(a.obj, a.sis, a.aut), "\n")
			eq(t, len(lines), 3, "lines")
			eq(t, regexp.MustCompile(`[●◆] (Goal|Sisyphus) running`).MatchString(lines[0]), true, lines[0])
			eq(t, strings.HasPrefix(lines[1], "├─ ⟡"), true, lines[1])
			eq(t, regexp.MustCompile(`└─ (auto-continue on|manual mode)`).MatchString(lines[2]), true, lines[2])
		}
	})
}

func TestGoalFiles(t *testing.T) {
	useTimeZone(t)
	at := ms(2026, 1, 2, 3, 4, 5)
	tw(t, "goal-files", "serializeGoalFile and parseGoalFile round-trip metadata while allowing prompt body edits", func(t *testing.T) {
		g := baseGoal("=== Goal ===\nObjective: original", false, at)
		file := filepath.Join(t.TempDir(), "goal.md")
		edited := strings.Replace(serializeGoalFile(g), "=== Goal ===\nObjective: original\n\n## Progress", "=== Goal ===\nObjective: edited on disk\n\n## Progress", 1)
		os.WriteFile(file, []byte(edited), 0o644)
		p := parseGoalFile(file)
		if p == nil {
			t.Fatal("nil")
		}
		eq(t, gstr(p, "id"), gstr(g, "id"), "id")
		eq(t, gstr(p, "objective"), "=== Goal ===\nObjective: edited on disk", "objective")
		eq(t, gstr(p, "status"), "active", "status")
	})
	tw(t, "goal-files", "goal file paths stay under active and archive roots even with unsafe metadata", func(t *testing.T) {
		cwd := t.TempDir()
		s := storage{cwd: cwd}
		g := baseGoal("Persist safely", true, at)
		g.set("activePath", "../escape.md")
		g.set("archivedPath", ".pi/goals/not-archive.md")
		eq(t, regexp.MustCompile(`^\.pi/goals/active_goal_`).MatchString(activePathForGoal(s, g)), true, "active path")
		eq(t, regexp.MustCompile(`^\.pi/goals/archived/goal_`).MatchString(archivedPathForGoal(s, g)), true, "archived path")
		active := must(s.writeActiveGoalFile(g))
		eq(t, regexp.MustCompile(`^\.pi/goals/active_goal_`).MatchString(strOrEmpty(active, "activePath")), true, "written active path")
		b, err := os.ReadFile(filepath.Join(cwd, strOrEmpty(active, "activePath")))
		eq(t, err == nil && strings.Contains(string(b), "# Goal Prompt"), true, "file text")
		_, err = os.Stat(filepath.Join(cwd, "..", "escape.md"))
		eq(t, err != nil, true, "nothing escaped")
		archived := must(s.archiveGoalFile(active))
		eq(t, archived.has("activePath"), false, "archived has no active path")
		eq(t, regexp.MustCompile(`^\.pi/goals/archived/goal_`).MatchString(strOrEmpty(archived, "archivedPath")), true, "archived path")
	})
	tw(t, "goal-files", "readActiveGoalFiles scans deterministic safe active goal files only", func(t *testing.T) {
		cwd := t.TempDir()
		s := storage{cwd: cwd}
		os.MkdirAll(filepath.Join(cwd, ".pi/goals"), 0o755)
		a := baseGoal("First", false, at)
		a.set("id", "b-goal")
		first := must(s.writeActiveGoalFile(a))
		b := baseGoal("Second", true, ms(2026, 1, 1, 3, 4, 5))
		b.set("id", "a-goal")
		second := must(s.writeActiveGoalFile(b))
		os.WriteFile(filepath.Join(cwd, ".pi/goals", "active_goal_invalid.md"), []byte("not json"), 0o644)
		os.WriteFile(filepath.Join(cwd, ".pi/goals", "note.md"), []byte(serializeGoalFile(first)), 0o644)
		_ = os.Symlink(filepath.Join(cwd, strOrEmpty(first, "activePath")), filepath.Join(cwd, ".pi/goals", "active_goal_symlink.md"))
		goals := s.scanActiveGoalFiles()
		eq(t, ids(goals), []string{"a-goal", "b-goal"}, "ids")
		eq(t, []string{strOrEmpty(goals[0], "activePath"), strOrEmpty(goals[1], "activePath")}, []string{strOrEmpty(second, "activePath"), strOrEmpty(first, "activePath")}, "paths")
		pool := s.readActiveGoalPool()
		keys := append([]string{}, pool.ids...)
		eq(t, len(keys), 2, "pool size")
		eq(t, pool.has("a-goal") && pool.has("b-goal"), true, "pool keys")
	})
	tw(t, "goal-files", "writeActiveGoalFile no longer auto-archives for complete status (deferred archival)", func(t *testing.T) {
		cwd := t.TempDir()
		s := storage{cwd: cwd}
		g := baseGoal("Complete goal defer archival", false, ms(2026, 6, 1, 12, 0, 0))
		active := must(s.writeActiveGoalFile(g))
		eq(t, regexp.MustCompile(`^\.pi/goals/active_goal_`).MatchString(strOrEmpty(active, "activePath")), true, "active path")
		eq(t, active.has("archivedPath"), false, "no archive yet")
		done := active.clone()
		done.set("status", "complete")
		result := must(s.writeActiveGoalFile(done))
		eq(t, regexp.MustCompile(`^\.pi/goals/active_goal_`).MatchString(strOrEmpty(result, "activePath")), true, "still active path")
		eq(t, result.has("archivedPath"), false, "not auto-archived")
		raw, _ := os.ReadFile(filepath.Join(cwd, strOrEmpty(result, "activePath")))
		eq(t, strings.Contains(string(raw), `"status": "complete"`), true, "status on disk")
		archived := must(s.archiveGoalFile(result))
		eq(t, archived.has("activePath"), false, "archived has no active path")
		eq(t, regexp.MustCompile(`^\.pi/goals/archived/goal_`).MatchString(strOrEmpty(archived, "archivedPath")), true, "archived path")
	})
	tasks := `{"tasks":[{"id":"t1","title":"Write tests","status":"complete","evidence":"all pass"},{"id":"t2","title":"Add migration","status":"pending"},
		{"id":"t3","title":"Update docs","status":"skipped","skipReason":"superseded"}],"blockCompletion":true,"proposedAt":"2026-05-27T00:00:00.000Z"}`
	tw(t, "goal-files", "serializeGoalFile includes ## Tasks section when taskList is present", func(t *testing.T) {
		g := baseGoal("Do stuff", false, at)
		g.set("taskList", jo(t, tasks))
		out := serializeGoalFile(g)
		for _, want := range []string{"## Tasks", "[x] t1: Write tests", "[ ] t2: Add migration", "[~] t3: Update docs", "blockCompletion: true", "evidence: all pass", "skipped: superseded"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q", want)
			}
		}
	})
	tw(t, "goal-files", "serializeGoalFile omits ## Tasks section when no taskList", func(t *testing.T) {
		eq(t, strings.Contains(serializeGoalFile(baseGoal("Simple goal", false, at)), "## Tasks"), false, "no tasks")
	})
	tw(t, "goal-files", "parseGoalFile round-trips taskList through JSON header", func(t *testing.T) {
		g := baseGoal("Stuff", false, at)
		g.set("taskList", jo(t, `{"tasks":[{"id":"t1","title":"Task 1","status":"complete"},{"id":"t2","title":"Task 2","status":"pending"}],"blockCompletion":false,"proposedAt":"2026-05-27T00:00:00.000Z"}`))
		file := filepath.Join(t.TempDir(), "test-goal.md")
		os.WriteFile(file, []byte(serializeGoalFile(g)), 0o644)
		p := parseGoalFile(file)
		tl := p.obj("taskList")
		list := tl.vals["tasks"].([]any)
		eq(t, len(list), 2, "tasks")
		eq(t, gstr(list[0].(*jsObject), "id"), "t1", "id 0")
		eq(t, gstr(list[0].(*jsObject), "status"), "complete", "status 0")
		eq(t, gstr(list[1].(*jsObject), "id"), "t2", "id 1")
		eq(t, gstr(list[1].(*jsObject), "status"), "pending", "status 1")
		eq(t, gflag(tl, "blockCompletion"), false, "blockCompletion")
	})
	tw(t, "goal-files", "parseGoalFile parses goal without taskList unchanged", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "test-goal.md")
		os.WriteFile(file, []byte(serializeGoalFile(baseGoal("Simple task", false, at))), 0o644)
		eq(t, parseGoalFile(file).obj("taskList") == nil, true, "no task list")
	})
}

func TestGoalPool(t *testing.T) {
	g := func(id string, kv ...any) *jsObject {
		day := 1
		for _, c := range id {
			if c >= '0' && c <= '9' {
				day = int(c - '0')
			}
		}
		o := baseGoal("=== Goal ===\nObjective: "+id, false, ms(2026, 1, day, 3, 4, 5))
		o.set("id", id)
		o.set("activePath", ".pi/goals/active_goal_"+id+".md")
		for i := 0; i+1 < len(kv); i += 2 {
			o.set(kv[i].(string), kv[i+1])
		}
		return o
	}
	tw(t, "goal-pool", "goal pool helpers sort open goals and resolve focused records", func(t *testing.T) {
		pool := poolOf(g("g2"), g("done", "status", "complete"), g("g1", "sisyphus", true))
		eq(t, ids(openGoalsFromPool(pool)), []string{"g1", "g2"}, "open goals")
		eq(t, pool.get("g1") != nil && gstr(pool.get("g1"), "id") == "g1", true, "focused")
		eq(t, pool.get("missing") == nil, true, "missing")
		eq(t, otherOpenGoalCount(pool, strp("g1")), 1, "others")
	})
	tskip(t, "goal-pool", "mergeFocusedGoalWithDisk uses disk lifecycle but preserves monotonic usage", "mergeFocusedGoalWithDisk serves reconcileFocused(preserveMemoryUsage) in the original's usage accounting, which this port does not have; the pool and focus reconcile it feeds is checked by the replay cases")
	tw(t, "goal-pool", "resolveSessionFocus prefers valid branch focus, then legacy goal, then single open goal", func(t *testing.T) {
		entry := func(id *string, reason string) *focusEntry { return &focusEntry{goalID: id, reason: reason} }
		pool := poolOf(g("g1"), g("g2"))
		eq(t, *resolveSessionFocus(pool, entry(strp("g2"), "selected"), nil, false), "g2", "valid focus")
		eq(t, resolveSessionFocus(pool, entry(strp("missing"), "selected"), nil, false) == nil, true, "missing focus")
		eq(t, resolveSessionFocus(pool, entry(nil, "cleared"), g("legacy"), false) == nil, true, "null focus beats legacy")
		eq(t, resolveSessionFocus(poolOf(g("only")), entry(nil, "completed"), nil, false) == nil, true, "completed")
		eq(t, resolveSessionFocus(poolOf(g("only")), entry(strp("missing"), "selected"), nil, false) == nil, true, "missing with one open")
		legacyPool := poolOf(g("g1"))
		eq(t, *resolveSessionFocus(legacyPool, nil, g("legacy"), false), "legacy", "legacy")
		eq(t, legacyPool.has("legacy"), true, "legacy added to pool")
		disk := poolOf(g("g1", "objective", "disk wins", "usage", jo(t, `{"tokensUsed":50,"activeSeconds":3}`)))
		eq(t, *resolveSessionFocus(disk, nil, g("g1", "objective", "stale legacy"), false), "g1", "disk goal")
		eq(t, gstr(disk.get("g1"), "objective"), "disk wins", "disk objective")
		eq(t, gnum(disk.get("g1").obj("usage"), "tokensUsed"), 50.0, "disk usage")
		single := poolOf(g("only"))
		eq(t, resolveSessionFocus(single, nil, nil, false) == nil, true, "default unfocused")
		eq(t, *resolveSessionFocus(single, nil, nil, true), "only", "opt-in single")
	})
	tw(t, "goal-pool", "goal list and selector labels expose focus without storing it in goals", func(t *testing.T) {
		pool := poolOf(g("g1"), g("g2", "status", "paused", "autoContinue", false))
		label := goalSelectorLabel(g("g1"), strp("g1"))
		eq(t, regexp.MustCompile(`^\* g1 \| running \| goal \| g1`).MatchString(label), true, label)
		eq(t, strings.Contains(label, ".pi/goals/active_goal_g1.md"), true, "path")
		list := buildGoalListText(pool, strp("g2"))
		eq(t, strings.HasPrefix(list, "Open goals: 2"), true, "header")
		eq(t, regexp.MustCompile(`(?m)^\* g2`).MatchString(list), true, "focused row")
		eq(t, strings.Contains(buildUnfocusedOpenGoalsSummary(2), "No goal is focused"), true, "summary")
		eq(t, strings.Contains(buildUnfocusedOpenGoalsSummary(2), "/goal-focus"), true, "hint")
	})
}
