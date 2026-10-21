package pi_goal_x

// Benchmarks for the performance section of port/PORT.md. They drive the core in process with the replay test's scripted host
// (replay_test.go), so what they time is the extension's own work: no SDK wire, no host. port/perf/bench-original.mjs runs the
// same operations on the same fixture against the original TypeScript, and port/perf/run.sh interleaves the two.
//
//	go test -run '^$' -bench . -benchmem -benchtime 300x
//
// Fixture: five open goals (the fifth, focused, with a twelve-task tree), settings {"maxAutonomousRuns": 0}, PI_GOAL_AUTO_CONFIRM=1.
// A read-only operation runs on one booted core. A mutating operation runs on a freshly booted core over the fixture's files, restored
// outside the timed region. The state a second operation needs (a second create_goal must not see the first's goal) is exactly what
// the restore gives back.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type perfHost struct {
	*fakeHost
	keyCB func(string) *goalInputResult
}

func (h *perfHost) OnTerminalInput(cb func(string) *goalInputResult) func() {
	h.keyCB = cb
	return func() {}
}

func perfPark(time.Duration, func()) *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

type perfFixture struct {
	b       *testing.B
	cwd     string
	h       *perfHost
	co      *core
	files   map[string]string
	entries []any
}

var perfTools = map[string]goalToolExec{
	"get_goal":         executeGetGoal,
	"create_goal":      executeCreateGoal,
	"update_goal":      executeUpdateGoal,
	"set_goal_tasks":   executeSetGoalTasks,
	"update_goal_task": executeUpdateGoalTask,
}

func perfTasks() []any {
	var tasks []any
	for g := 1; g <= 3; g++ {
		parent := fmt.Sprintf("group-%d", g)
		tasks = append(tasks, map[string]any{"id": parent, "title": fmt.Sprintf("Milestone %d of the quarterly report", g), "verification_contract": "The section is written and reviewed."})
		for s := 1; s <= 3; s++ {
			tasks = append(tasks, map[string]any{"id": fmt.Sprintf("%s-step-%d", parent, s), "parent_id": parent, "title": fmt.Sprintf("Step %d of milestone %d", s, g)})
		}
	}
	return tasks
}

func (f *perfFixture) tool(name string, params map[string]any) goalAgentResult {
	var r goalAgentResult
	var err error
	f.co.locked(func() { r, err = perfTools[name](f.co, params) })
	if err != nil {
		f.b.Fatalf("%s: %v", name, err)
	}
	return r
}

func (f *perfFixture) command(name, args string) {
	for _, d := range commandDefs {
		if d.name == name {
			var err error
			f.co.locked(func() { err = d.fn(f.co, args) })
			if err != nil {
				f.b.Fatalf("/%s: %v", name, err)
			}
			return
		}
	}
	f.b.Fatalf("no command %s", name)
}

// boot starts a fresh core on the files as they are.
func (f *perfFixture) boot() {
	invalidateGoalPoolCache()
	ledgerDirKnown = map[string]bool{}
	f.h.fakeHost.events = nil
	f.h.fakeHost.entries = append([]any(nil), f.entries...)
	f.co = newCore(f.h)
	f.co.afterBase = perfPark
	invalidateGoalSettingsCache()
	invalidateGoalLedgerCache()
	f.co.locked(func() {
		_ = f.co.sessionStart()
		f.co.syncTerminalInputPause()
	})
	f.h.fakeHost.events = f.h.fakeHost.events[:0]
}

// restore puts the fixture's files back and boots a fresh core.
func (f *perfFixture) restore() {
	if err := os.RemoveAll(filepath.Join(f.cwd, ".pi")); err != nil {
		f.b.Fatal(err)
	}
	for rel, text := range f.files {
		p := filepath.Join(f.cwd, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			f.b.Fatal(err)
		}
	}
	f.boot()
}

func newPerfFixture(b *testing.B, settings string) *perfFixture {
	b.Helper()
	b.Setenv("PI_CODING_AGENT_DIR", b.TempDir())
	b.Setenv("PI_GOAL_AUTO_CONFIRM", "1")
	for _, k := range os.Environ() {
		if strings.HasPrefix(k, "PI_GOAL_") && !strings.HasPrefix(k, "PI_GOAL_AUTO_CONFIRM") {
			os.Unsetenv(strings.SplitN(k, "=", 2)[0])
		}
	}
	cwd := b.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "pi-goal-x-settings.json"), []byte(settings), 0o644); err != nil {
		b.Fatal(err)
	}
	f := &perfFixture{b: b, cwd: cwd, h: &perfHost{fakeHost: &fakeHost{cwd: cwd}}}
	f.boot()
	for i := 1; i <= 5; i++ {
		f.tool("create_goal", map[string]any{"objective": fmt.Sprintf("Write the quarterly report %d\nSuccess criteria: three sections, each reviewed, with the figures checked against the ledger.", i)})
	}
	f.tool("set_goal_tasks", map[string]any{"tasks": perfTasks()})
	f.tool("update_goal_task", map[string]any{"task_id": "group-1-step-1", "status": "start"})
	f.files = map[string]string{}
	_ = filepath.Walk(filepath.Join(cwd, ".pi"), func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(cwd, p)
		data, _ := os.ReadFile(p)
		f.files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	f.entries = append([]any(nil), f.h.fakeHost.entries...)
	if perfLedgerRepeat > 0 {
		const ledger = ".pi/goals/goal_events.jsonl"
		if f.files[ledger] == "" {
			b.Fatal("the fixture has no ledger")
		}
		f.files[ledger] = strings.Repeat(f.files[ledger], perfLedgerRepeat)
		delete(f.files, ".pi/goals/.goal-ledger-checkpoint.json")
	}
	f.restore()
	return f
}

// perfLedgerRepeat, when set, repeats the fixture's ledger that many times (the scale benchmarks below).
var perfLedgerRepeat int

const perfMaxRunsOff = `{"maxAutonomousRuns": 0}`

// benchRead times an operation that leaves the state as it found it.
func benchRead(b *testing.B, op func(f *perfFixture)) {
	f := newPerfFixture(b, perfMaxRunsOff)
	op(f) // one warm-up: the first read of a ledger or a settings file fills its cache
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.h.fakeHost.events = f.h.fakeHost.events[:0]
		op(f)
	}
}

// benchWrite times an operation on a freshly booted core over the restored files.
func benchWrite(b *testing.B, settings string, op func(f *perfFixture)) {
	f := newPerfFixture(b, settings)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		f.restore()
		b.StartTimer()
		op(f)
	}
}

func BenchmarkSessionStart(b *testing.B) {
	// The session_start handler's work (load, widget, input listener). What registration costs once per process is measured end to
	// end by run.sh (cold start).
	f := newPerfFixture(b, perfMaxRunsOff)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.boot()
	}
}

func BenchmarkToolGetGoalSummary(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.tool("get_goal", map[string]any{}) })
}

func BenchmarkToolGetGoalTasks(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.tool("get_goal", map[string]any{"section": "tasks"}) })
}

func BenchmarkToolCreateGoal(b *testing.B) {
	benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
		f.tool("create_goal", map[string]any{"objective": "Deploy to production\nSuccess criteria: the checks are green."})
	})
}

func BenchmarkToolUpdateGoalPause(b *testing.B) {
	benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
		f.tool("update_goal", map[string]any{"status": "paused", "reason": "Waiting for the review of the second milestone.", "suggested_action": "Ask the reviewer."})
	})
}

func BenchmarkToolSetGoalTasks(b *testing.B) {
	tasks := perfTasks()
	benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
		f.tool("set_goal_tasks", map[string]any{"tasks": tasks, "block_completion": true})
	})
}

func BenchmarkToolUpdateGoalTask(b *testing.B) {
	benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
		f.tool("update_goal_task", map[string]any{"task_id": "group-1-step-1", "status": "complete", "evidence": "The step is written."})
	})
}

func BenchmarkCmdGoalList(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.command("goal-list", "") })
}

func BenchmarkCmdGoalStatus(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.command("goal-status", "") })
}

func BenchmarkCmdGoalStatusVerbose(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.command("goal-status", "verbose") })
}

func BenchmarkCmdGoalRefresh(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.command("goal-refresh", "") })
}

func BenchmarkCmdGoalDirect(b *testing.B) {
	benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
		f.command("goal-direct", "Ship the release\nSuccess criteria: tagged and published.")
	})
}

func BenchmarkRenderDashboardCompact(b *testing.B) {
	benchRead(b, func(f *perfFixture) {
		f.co.dashboardExpanded = false
		f.co.widgetRef.current.Render(100)
	})
}

func BenchmarkRenderDashboardExpanded(b *testing.B) {
	benchRead(b, func(f *perfFixture) {
		f.co.dashboardExpanded = true
		f.co.widgetRef.current.Render(100)
	})
}

// BenchmarkRenderPush is a render request: the dashboard laid out at the host's width and pushed (what every UI update costs).
func BenchmarkRenderPush(b *testing.B) {
	benchRead(b, func(f *perfFixture) { f.co.locked(f.co.pushWidget) })
}

func BenchmarkKeyPlain(b *testing.B) {
	benchRead(b, func(f *perfFixture) {
		if r := f.h.keyCB("a"); r != nil {
			b.Fatal("a plain key must not be consumed")
		}
	})
}

func BenchmarkKeyToggleExpand(b *testing.B) {
	benchRead(b, func(f *perfFixture) {
		if r := f.h.keyCB("\x1b[116;6u"); r == nil || !r.consume {
			b.Fatal("the toggle chord must be consumed")
		}
	})
}

// BenchmarkSchedulerTick is one autonomous run's scheduling: agent_start, the agent's ready declaration through update_goal, and the
// settled run, which arms the next dispatch (the timer is parked).
func BenchmarkSchedulerTick(b *testing.B) {
	benchWrite(b, `{"maxAutonomousRuns": 10}`, func(f *perfFixture) {
		f.co.locked(f.co.scheduler.begin)
		f.tool("update_goal", map[string]any{"continuation": map[string]any{"kind": "ready"}})
		f.co.locked(func() { f.co.scheduler.settled(true) })
	})
}

// ---- scale: the same operations over a ledger of about 16,000 events (the fixture's ledger repeated 2,000 times), to show that the
// render and input paths do bounded work whatever the ledger holds ----

func withBigLedger(b *testing.B, run func(b *testing.B)) {
	perfLedgerRepeat = 2000
	defer func() { perfLedgerRepeat = 0 }()
	run(b)
}

func BenchmarkBigLedgerSessionStart(b *testing.B) {
	withBigLedger(b, func(b *testing.B) {
		f := newPerfFixture(b, perfMaxRunsOff)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			f.boot()
		}
	})
}

func BenchmarkBigLedgerGetGoalSummary(b *testing.B) {
	withBigLedger(b, func(b *testing.B) { benchRead(b, func(f *perfFixture) { f.tool("get_goal", map[string]any{}) }) })
}

func BenchmarkBigLedgerGoalStatus(b *testing.B) {
	withBigLedger(b, func(b *testing.B) { benchRead(b, func(f *perfFixture) { f.command("goal-status", "") }) })
}

func BenchmarkBigLedgerRender(b *testing.B) {
	withBigLedger(b, func(b *testing.B) { benchRead(b, func(f *perfFixture) { f.co.widgetRef.current.Render(100) }) })
}

func BenchmarkBigLedgerKeyToggleExpand(b *testing.B) {
	withBigLedger(b, func(b *testing.B) {
		benchRead(b, func(f *perfFixture) { f.h.keyCB("\x1b[116;6u") })
	})
}

func BenchmarkBigLedgerUpdateGoalTask(b *testing.B) {
	withBigLedger(b, func(b *testing.B) {
		benchWrite(b, perfMaxRunsOff, func(f *perfFixture) {
			f.tool("update_goal_task", map[string]any{"task_id": "group-1-step-1", "status": "complete", "evidence": "The step is written."})
		})
	})
}
