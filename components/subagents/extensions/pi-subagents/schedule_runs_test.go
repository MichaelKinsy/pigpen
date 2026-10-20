package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of scheduled-runs.test.ts: the project schedule store, the actions and the timers, over a fake clock, fake timers and a
// launch the test resolves by hand (the original's FakeTimers and Launch promises).

const schedFile = "scheduled-runs"

type fakeSchedTimers struct {
	mu     sync.Mutex
	values map[int]schedTimer
	id     int
	h      *schedHarness
}

type schedTimer struct {
	fn    func()
	delay int64
}

func (f *fakeSchedTimers) SetTimeout(fn func(), delayMs int64) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id++
	f.values[f.id] = schedTimer{fn, delayMs}
	return f.id
}

func (f *fakeSchedTimers) ClearTimeout(h any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, h.(int))
}

func (f *fakeSchedTimers) size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.values)
}

func (f *fakeSchedTimers) delays() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int64
	for _, v := range f.values {
		out = append(out, v.delay)
	}
	return out
}

// fireAll runs every armed timer's callback (each on its own goroutine, as a timer does) and returns once each has finished or
// is waiting for a launch the test resolves later.
func (f *fakeSchedTimers) fireAll() {
	f.mu.Lock()
	pending := f.values
	f.values = map[int]schedTimer{}
	f.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range pending {
		wg.Add(1)
		go func(fn func()) { defer wg.Done(); fn() }(t.fn)
	}
	f.h.settle(&wg, len(pending))
}

type testLaunch struct {
	params map[string]any
	ctx    scheduleCtx
	done   chan scheduleLaunchResult
}

func (l *testLaunch) resolve(r scheduleLaunchResult) { l.done <- r }

func asyncLaunched(id string, dir ...string) scheduleLaunchResult {
	r := scheduleLaunchResult{Text: "Async", AsyncID: id}
	if len(dir) > 0 {
		r.AsyncDir = dir[0]
	}
	return r
}

type schedHarness struct {
	t        *testing.T
	mgr      *scheduledRunManager
	ctx      scheduleCtx
	clock    *int64
	clockMu  sync.Mutex
	timers   *fakeSchedTimers
	mu       sync.Mutex
	launches []*testLaunch
	blocked  int
	root     string
	stores   string
}

func (h *schedHarness) now() int64 {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	return *h.clock
}

func (h *schedHarness) advance(ms int64) {
	h.clockMu.Lock()
	*h.clock += ms
	h.clockMu.Unlock()
}

func (h *schedHarness) setNow(ms int64) {
	h.clockMu.Lock()
	*h.clock = ms
	h.clockMu.Unlock()
}

func (h *schedHarness) launchCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.launches)
}

func (h *schedHarness) launch(i int) *testLaunch {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.launches) {
		h.t.Fatalf("launch %d does not exist (%d)", i, len(h.launches))
	}
	return h.launches[i]
}

func (h *schedHarness) waitLaunches(n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.launchCount() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("waiting for %d launches, have %d", n, h.launchCount())
}

func (h *schedHarness) settle(wg *sync.WaitGroup, spawned int) {
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-finished:
			return
		default:
		}
		h.mu.Lock()
		blocked := h.blocked
		h.mu.Unlock()
		if blocked > 0 {
			time.Sleep(20 * time.Millisecond) // the others have run to their own end by now
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *schedHarness) launchFn(params map[string]any, ctx scheduleCtx) scheduleLaunchResult {
	l := &testLaunch{params: params, ctx: ctx, done: make(chan scheduleLaunchResult, 1)}
	h.mu.Lock()
	h.launches = append(h.launches, l)
	h.blocked++
	h.mu.Unlock()
	r := <-l.done
	h.mu.Lock()
	h.blocked--
	h.mu.Unlock()
	return r
}

func unusedLaunch(map[string]any, scheduleCtx) scheduleLaunchResult {
	return scheduleLaunchResult{Text: "unused"}
}

type schedOpts struct {
	cwd       string
	sessionID string
	now       int64
	config    scheduleConfig
	randomID  func() string
	hasConfig bool
}

func schedCtx(cwd, sessionID string) scheduleCtx {
	return scheduleCtx{Cwd: cwd, SessionID: sessionID, SessionFile: filepath.Join(cwd, sessionID+".jsonl")}
}

func newSchedHarness(t *testing.T, o schedOpts) *schedHarness {
	t.Helper()
	withTempHome(t)
	root := tmp(t)
	project := o.cwd
	if project == "" {
		project = filepath.Join(root, "project")
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if o.sessionID == "" {
		o.sessionID = "session-a"
	}
	start := o.now
	if start == 0 {
		start = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	}
	h := &schedHarness{t: t, clock: &start, root: root, stores: filepath.Join(root, "stores")}
	h.timers = &fakeSchedTimers{values: map[int]schedTimer{}, h: h}
	h.ctx = schedCtx(project, o.sessionID)
	cfg := o.config
	if !o.hasConfig {
		on := true
		cfg = scheduleConfig{Enabled: &on}
	}
	id := 0
	rnd := o.randomID
	if rnd == nil {
		rnd = func() string { id++; return fmt.Sprintf("id-%d", id) }
	}
	h.mgr = newScheduledRunManager(scheduledRunDeps{Config: cfg, StoreRoot: h.stores, Now: h.now, RandomID: rnd, Timers: h.timers, Launch: h.launchFn})
	t.Cleanup(func() { // release every launch still waiting so no goroutine outlives the test
		h.mu.Lock()
		pending := append([]*testLaunch(nil), h.launches...)
		h.mu.Unlock()
		for _, l := range pending {
			select {
			case l.done <- scheduleLaunchResult{IsError: true, Text: "test ended"}:
			default:
			}
		}
		time.Sleep(200 * time.Millisecond) // the released launches finish writing before the temp directories go
	})
	if err := h.mgr.bindSession(h.ctx); err != nil {
		t.Fatalf("bindSession: %v", err)
	}
	return h
}

// another manager over the same stores, with its own timers and launches
func (h *schedHarness) another(cfg scheduleConfig, launch func(map[string]any, scheduleCtx) scheduleLaunchResult, rnd func() string) (*scheduledRunManager, *fakeSchedTimers) {
	timers := &fakeSchedTimers{values: map[int]schedTimer{}, h: h}
	on := true
	if cfg.Enabled == nil {
		cfg.Enabled = &on
	}
	m := newScheduledRunManager(scheduledRunDeps{Config: cfg, StoreRoot: h.stores, Now: h.now, RandomID: rnd, Timers: timers, Launch: launch})
	return m, timers
}

func (h *schedHarness) call(params map[string]any) scheduleResult {
	return h.mgr.handleToolCall(params, h.ctx)
}

func (h *schedHarness) callAsync(params map[string]any) <-chan scheduleResult {
	ch := make(chan scheduleResult, 1)
	go func() { ch <- h.mgr.handleToolCall(params, h.ctx) }()
	return ch
}

func (h *schedHarness) listAll(cwd string) []*scheduleRecord {
	recs, err := listScheduledRunSummaries(cwd, h.stores)
	if err != nil {
		h.t.Fatal(err)
	}
	return recs
}

func (h *schedHarness) storeDir(cwd string) string { return scheduledRunStorePath(cwd, "", h.stores) }

func recordsOf(r scheduleResult) []*jsObject {
	sched := r.Details.obj("schedules")
	if sched == nil {
		return nil
	}
	recs, _ := sched.vals["records"].([]any)
	var out []*jsObject
	for _, x := range recs {
		out = append(out, x.(*jsObject))
	}
	return out
}

func mustMatchRe(t *testing.T, pattern, s string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("%q does not match /%s/", s, pattern)
	}
}

func mustNotErr(t *testing.T, r scheduleResult) {
	t.Helper()
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Text)
	}
}

func mustErr(t *testing.T, r scheduleResult, pattern string) {
	t.Helper()
	if !r.IsError {
		t.Fatalf("expected an error matching /%s/, got %q", pattern, r.Text)
	}
	if pattern != "" {
		mustMatchRe(t, pattern, r.Text)
	}
}

func createParams(id string, extra map[string]any) map[string]any {
	p := map[string]any{"action": "schedule.create", "id": id, "workflowScript": "return runs.run('main', { agent: 'worker' })"}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func sched(action, id string) map[string]any { return map[string]any{"action": action, "id": id} }

func readSchedJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func gitRun(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestScheduledRunsTwins(t *testing.T) {
	const flag = "scheduled-runs"
	_ = flag
	tw(t, schedFile, "recognizes only the dot-action schedule API", func(t *testing.T) {
		eq2(t, scheduledRunActions, []string{"schedule.create", "schedule.list", "schedule.show", "schedule.history", "schedule.pause", "schedule.resume", "schedule.run", "schedule.run-due", "schedule.delete"})
		eq2(t, isScheduledRunAction("schedule.create"), true)
		eq2(t, isScheduledRunAction("schedule"), false)
	})
	tw(t, schedFile, "uses a stable project store independent of session id", func(t *testing.T) {
		root := filepath.Join("tmp", "schedules")
		eq2(t, scheduledRunStorePath("project", "a", root), scheduledRunStorePath("project", "b", root))
		if scheduledRunStorePath("project", "a", root) == scheduledRunStorePath("other", "a", root) {
			t.Error("two projects share a store")
		}
		abs, _ := filepath.Abs("/project")
		eq2(t, scheduledRunStorePath("/project", "", ""), filepath.Join(abs, ".pi/subagents", "schedules"))
	})
	tw(t, schedFile, "parses one-shot and fixed interval forms strictly", func(t *testing.T) {
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		at := func(s string) int64 {
			v, err := parseScheduledRunTime(s, now)
			if err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			return v
		}
		eq2(t, at("+10m"), now+600_000)
		eq2(t, at("2030-01-02T00:00:00Z"), now+86_400_000)
		eq2(t, at("2030-01-02T09:00:00+05:30"), time.Date(2030, 1, 2, 3, 30, 0, 0, time.UTC).UnixMilli())
		every := func(s string) int64 {
			v, err := parseScheduleInterval(s)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		eq2(t, every("30m"), int64(1_800_000))
		eq2(t, every("2w"), int64(1_209_600_000))
		if _, err := parseScheduleInterval("day"); err == nil || !strings.Contains(err.Error(), "fixed intervals") {
			t.Errorf("day: %v", err)
		}
		if _, err := parseScheduledRunTime("2030-01-01T00:00:00", now); err == nil || !strings.Contains(err.Error(), "timezone") {
			t.Errorf("no zone: %v", err)
		}
	})
	tw(t, schedFile, "honors the explicit feature opt-out", func(t *testing.T) {
		off := false
		eq2(t, scheduledRunsEnabled(scheduleConfig{}), true)
		eq2(t, scheduledRunsEnabled(scheduleConfig{Enabled: &off}), false)
	})
}

func eq2(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("got %s, want %s", g, w)
	}
}

func TestProjectScheduleManagementTwins(t *testing.T) {
	tw(t, schedFile, "creates a project one-shot schedule and restores it in another session", func(t *testing.T) {
		first := newSchedHarness(t, schedOpts{})
		result := first.call(map[string]any{"action": "schedule.create", "id": "night-review", "name": "Night review", "at": "+10m", "workflowScript": "return runs.run('main', { agent: 'reviewer', task: 'Review the diff' })"})
		mustNotErr(t, result)
		mustMatchRe(t, `Created schedule night-review`, result.Text)
		eq2(t, first.timers.size(), 1)
		records := first.listAll(first.ctx.Cwd)
		eq2(t, records[0].Name, "Night review")
		eq2(t, records[0].Cwd, first.ctx.Cwd)
		first.mgr.stop()
		second, secondTimers := first.another(scheduleConfig{}, unusedLaunch, nil)
		ctxB := schedCtx(first.ctx.Cwd, "session-b")
		if err := second.bindSession(ctxB); err != nil {
			t.Fatal(err)
		}
		eq2(t, secondTimers.size(), 1) // a different session restores the project schedule
		shown := second.handleToolCall(sched("schedule.show", "night-review"), ctxB)
		mustMatchRe(t, `Night review`, shown.Text)
	})
	tw(t, schedFile, "restores session-only schedules only in the creating session", func(t *testing.T) {
		owner := newSchedHarness(t, schedOpts{sessionID: "owner-session"})
		created := owner.call(map[string]any{"action": "schedule.create", "id": "owner-only", "name": "Owner only", "every": "1h", "cwd": owner.ctx.Cwd, "sessionOnly": true, "workflowScript": "return 1"})
		mustNotErr(t, created)
		eq2(t, recordsOf(created)[0].vals["sessionOnly"], true)
		if recordsOf(created)[0].has("ownerSessionFile") {
			t.Error("the owner session file is public")
		}
		root := owner.storeDir(owner.ctx.Cwd)
		rec := owner.listAll(owner.ctx.Cwd)[0]
		eq2(t, rec.SessionOnly != nil && *rec.SessionOnly, true)
		eq2(t, rec.OwnerSessionFile, owner.ctx.SessionFile)
		owner.mgr.stop()
		var otherLaunches []*testLaunch
		other, otherTimers := owner.another(scheduleConfig{}, func(p map[string]any, c scheduleCtx) scheduleLaunchResult {
			otherLaunches = append(otherLaunches, &testLaunch{params: p, ctx: c})
			return scheduleLaunchResult{Text: "unused"}
		}, nil)
		otherCtx := schedCtx(owner.ctx.Cwd, "other-session")
		if err := other.bindSession(otherCtx); err != nil {
			t.Fatal(err)
		}
		eq2(t, otherTimers.size(), 0) // non-owner sessions must not arm session-only schedules
		manual := other.handleToolCall(sched("schedule.run", "owner-only"), otherCtx)
		mustMatchRe(t, `not its owner`, manual.Text)
		for _, r := range []scheduleResult{manual, other.handleToolCall(sched("schedule.show", "owner-only"), otherCtx), other.handleToolCall(map[string]any{"action": "schedule.list"}, otherCtx)} {
			if recordsOf(r)[0].has("ownerSessionFile") {
				t.Error("the owner session file leaks")
			}
		}
		eq2(t, len(otherLaunches), 0)
		eq2(t, owner.listAll(owner.ctx.Cwd)[0].OwnerSessionFile, owner.ctx.SessionFile)
		if fileExists(filepath.Join(root, "owner-only", "active.lock")) {
			t.Error("a non-owner took the lock")
		}
		again, ownerTimers := owner.another(scheduleConfig{}, unusedLaunch, nil)
		if err := again.bindSession(schedCtx(owner.ctx.Cwd, "owner-session")); err != nil {
			t.Fatal(err)
		}
		eq2(t, ownerTimers.size(), 1) // the creating session must restore its schedule
	})
	tw(t, schedFile, "re-arms an owner timer after a non-owner context consumes it", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{sessionID: "owner-session"})
		mustNotErr(t, h.call(createParams("owner-only", map[string]any{"cwd": h.ctx.Cwd, "every": "1h", "sessionOnly": true})))
		next := h.listAll(h.ctx.Cwd)[0].Trigger.NextRunAt
		if err := h.mgr.bindSession(schedCtx(h.ctx.Cwd, "other-session")); err != nil {
			t.Fatal(err)
		}
		h.advance(3_600_000)
		h.timers.fireAll()
		eq2(t, h.launchCount(), 0) // a non-owner context must not launch the schedule
		eq2(t, h.timers.size(), 0) // the consumed non-owner timer is not left spinning
		eq2(t, h.listAll(h.ctx.Cwd)[0].Trigger.NextRunAt, next)
		if err := h.mgr.bindSession(h.ctx); err != nil {
			t.Fatal(err)
		}
		eq2(t, h.timers.size(), 1) // rebinding the owner must restore the timer
		h.timers.fireAll()
		h.waitLaunches(1)
		eq2(t, h.launch(0).ctx.SessionID, "owner-session")
		h.launch(0).resolve(asyncLaunched("owner-async"))
	})
	tw(t, schedFile, "does not let completed one-shot schedules consume maxPending capacity", func(t *testing.T) {
		one := 1
		on := true
		h := newSchedHarness(t, schedOpts{hasConfig: true, config: scheduleConfig{Enabled: &on, MaxPending: &one}})
		mustNotErr(t, h.call(createParams("completed", map[string]any{"at": "+1h"})))
		h.advance(3_600_000)
		h.timers.fireAll()
		eq2(t, h.launchCount(), 1)
		mustErr(t, h.call(createParams("active-blocked", map[string]any{"at": "+2h"})), "")
		h.launch(0).resolve(asyncLaunched("completed-async"))
		time.Sleep(30 * time.Millisecond)
		h.mgr.handleAsyncCompletion(map[string]any{"runId": "completed-async", "success": true})
		mustNotErr(t, h.call(createParams("replacement", map[string]any{"at": "+2h"})))
		dir := filepath.Join(h.storeDir(h.ctx.Cwd), "completed")
		if !fileExists(filepath.Join(dir, "history.json")) || !fileExists(filepath.Join(dir, "events.jsonl")) {
			t.Error("the completed schedule keeps no history")
		}
		mustMatchRe(t, `completed.*async completed-async`, h.call(sched("schedule.history", "completed")).Text)
	})
	tw(t, schedFile, "stores explicit cwd schedules in the target project", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		target := filepath.Join(h.root, "other-project")
		_ = os.Mkdir(target, 0o755)
		mustNoError(t, h.mgr.bindSession(schedCtx(target, "target-session")))
		mustNoError(t, h.mgr.bindSession(h.ctx))
		h.call(createParams("other", map[string]any{"cwd": target, "every": "1h"}))
		eq2(t, len(h.listAll(h.ctx.Cwd)), 0)
		eq2(t, h.listAll(target)[0].Cwd, target)
		mustMatchRe(t, `other`, h.call(map[string]any{"action": "schedule.list", "cwd": target}).Text)
	})
	tw(t, schedFile, "treats a deleted project cwd as having no schedules during restore and listing", func(t *testing.T) {
		withTempHome(t)
		project := filepath.Join(tmp(t), "project")
		recs, err := listScheduledRunSummaries(project, "")
		if err != nil || len(recs) != 0 {
			t.Errorf("deleted project: %v %v", recs, err)
		}
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		if err := m.bindSession(schedCtx(project, "s")); err != nil {
			t.Errorf("bindSession: %v", err)
		}
	})
	tw(t, schedFile, "rejects direct schedule targets and requires workflowScript", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		mustErr(t, h.call(map[string]any{"action": "schedule.create", "id": "direct", "every": "1h", "agent": "worker", "task": "Review"}), `requires workflowScript`)
	})
	tw(t, schedFile, "fails closed on persisted legacy agent targets", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.mgr.stop()
		dir := filepath.Join(h.storeDir(h.ctx.Cwd), "legacy")
		_ = os.MkdirAll(dir, 0o755)
		ts := func(ms int64) string { return scheduleTimestamp(ms) }
		rec := map[string]any{"schemaVersion": 1, "id": "legacy", "name": "Legacy direct target", "cwd": h.ctx.Cwd,
			"trigger": map[string]any{"kind": "interval", "every": "1h", "everyMs": 3_600_000, "anchorAt": ts(h.now()), "nextRunAt": ts(h.now() + 3_600_000)},
			"target":  map[string]any{"agent": "worker", "task": "Review backlog"}, "overlap": "skip", "catchUp": "latest", "paused": false, "createdAt": ts(h.now()), "updatedAt": ts(h.now())}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(filepath.Join(dir, "schedule.json"), b, 0o644)
		m, _ := h.another(scheduleConfig{}, unusedLaunch, nil)
		err := m.bindSession(schedCtx(h.ctx.Cwd, "session-b"))
		if err == nil {
			t.Fatal("bindSession succeeded")
		}
		mustMatchRe(t, `(?i)removed legacy agent target.*target\.workflowScript`, err.Error())
	})
	tw(t, schedFile, "supports workflowScript targets and rejects unsafe or deferred shapes", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		wf := h.call(map[string]any{"action": "schedule.create", "id": "workflow", "every": "6h", "workflowScript": "return await runs.run('review', {agent:'reviewer'})"})
		mustNotErr(t, wf)
		mustMatchRe(t, `workflowScript -> agent reviewer`, wf.Text)
		dyn := h.call(map[string]any{"action": "schedule.create", "id": "dynamic", "every": "6h", "workflowScript": "const agent = 'worker'; return runs.run('main', { agent })"})
		mustNotErr(t, dyn)
		mustMatchRe(t, `workflowScript \(dynamic\)`, dyn.Text)
		run := "return runs.run('main', { agent: 'worker' })"
		for _, p := range []map[string]any{
			{"action": "schedule.create", "id": "../escape", "every": "1h", "workflowScript": run},
			{"action": "schedule.create", "id": "both", "at": "+1h", "every": "1h", "workflowScript": run},
			{"action": "schedule.create", "id": "calendar", "every": "day", "at": "09:00", "timezone": "UTC", "workflowScript": run},
			{"action": "schedule.create", "id": "two-targets", "every": "1h", "agent": "worker", "workflowScript": "return 1"},
			{"action": "schedule.create", "id": "fork", "every": "1h", "workflowScript": run, "context": "fork"},
			{"action": "schedule.create", "id": "invalid-base-ref", "every": "1h", "workflowScript": run, "baseRef": "unsafe..ref"},
			{"action": "schedule.create", "id": "revision-alias-base-ref", "every": "1h", "workflowScript": run, "baseRef": "@"},
			{"action": "schedule.create", "id": "object-id-base-ref", "every": "1h", "workflowScript": run, "baseRef": strings.Repeat("a", 40)},
			{"action": "schedule.create", "id": "sha256-object-id-base-ref", "every": "1h", "workflowScript": run, "baseRef": strings.Repeat("a", 64)},
			{"action": "schedule.create", "id": "invalid-args", "every": "1h", "workflowScript": "return 1", "args": map[string]any{"task": ""}},
			{"action": "schedule.create", "id": "mission-id", "every": "1h", "workflowScript": run, "missionId": "mission-1"},
			{"action": "schedule.create", "id": "mission-off", "every": "1h", "workflowScript": run, "mission": false},
		} {
			if r := h.call(p); !r.IsError {
				t.Errorf("%v was accepted", p)
			}
		}
	})
	tw(t, schedFile, "persists and forwards scheduled workflow arguments and baseRef", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		args := map[string]any{"task": "review", "nested": map[string]any{"retries": float64(2)}}
		created := h.call(createParams("base-ref", map[string]any{"every": "1h", "baseRef": "@/foo", "args": args}))
		mustNotErr(t, created)
		if regexp.MustCompile(`review|retries`).MatchString(created.Text) {
			t.Errorf("the text shows the args: %s", created.Text)
		}
		rec := h.listAll(h.ctx.Cwd)[0]
		eq2(t, rec.Target.BaseRef, "@/foo")
		eq2(t, rec.Target.Args, map[string]any{"task": "review", "nested": map[string]any{"retries": 2}})
		h.mgr.stop()
		var got []*testLaunch
		restored, _ := h.another(scheduleConfig{}, func(p map[string]any, c scheduleCtx) scheduleLaunchResult {
			got = append(got, &testLaunch{params: p, ctx: c})
			return asyncLaunched("base-ref-async")
		}, nil)
		mustNoError(t, restored.bindSession(h.ctx))
		res := restored.handleToolCall(sched("schedule.run", "base-ref"), h.ctx)
		eq2(t, got[0].params["baseRef"], "@/foo")
		eq2(t, got[0].params["args"], map[string]any{"task": "review", "nested": map[string]any{"retries": 2}})
		mustNotErr(t, res)
	})
	tw(t, schedFile, "pauses, resumes, lists, and deletes an inactive schedule", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("maintenance", map[string]any{"every": "1h"}))
		mustMatchRe(t, `maintenance`, h.call(map[string]any{"action": "schedule.list"}).Text)
		mustMatchRe(t, `Paused`, h.call(sched("schedule.pause", "maintenance")).Text)
		eq2(t, h.timers.size(), 0)
		mustMatchRe(t, `Resumed`, h.call(sched("schedule.resume", "maintenance")).Text)
		eq2(t, h.timers.size(), 1)
		mustMatchRe(t, `Deleted`, h.call(sched("schedule.delete", "maintenance")).Text)
		mustMatchRe(t, `No project schedules`, h.call(map[string]any{"action": "schedule.list"}).Text)
	})
	tw(t, schedFile, "deletes a foreign session-only schedule only after its exact async run is terminal", func(t *testing.T) {
		owner := newSchedHarness(t, schedOpts{sessionID: "owner-session"})
		owner.call(createParams("owner-only", map[string]any{"every": "1h", "sessionOnly": true}))
		running := owner.callAsync(sched("schedule.run", "owner-only"))
		owner.waitLaunches(1)
		asyncDir := filepath.Join(owner.root, "async-owner-only")
		_ = os.Mkdir(asyncDir, 0o755)
		owner.launch(0).resolve(asyncLaunched("async-owner-only", asyncDir))
		<-running
		owner.mgr.stop()
		status := func(state any) {
			b, _ := json.Marshal(map[string]any{"runId": "async-owner-only", "mode": "single", "state": state, "startedAt": owner.now(), "endedAt": owner.now()})
			_ = os.WriteFile(filepath.Join(asyncDir, "status.json"), b, 0o644)
		}
		status("running")
		other, _ := owner.another(scheduleConfig{}, unusedLaunch, nil)
		otherCtx := schedCtx(owner.ctx.Cwd, "other-session")
		mustNoError(t, other.bindSession(otherCtx))
		mustErr(t, other.handleToolCall(sched("schedule.delete", "owner-only"), otherCtx), `has active run`)
		status([]string{"failed"})
		mustErr(t, other.handleToolCall(sched("schedule.delete", "owner-only"), otherCtx), `has active run`)
		status("failed")
		mustMatchRe(t, `Deleted`, other.handleToolCall(sched("schedule.delete", "owner-only"), otherCtx).Text)
		mustMatchRe(t, `No project schedules`, other.handleToolCall(map[string]any{"action": "schedule.list"}, otherCtx).Text)
	})
	tw(t, schedFile, "ignores schedules deleted by another session while a timer is still armed", func(t *testing.T) {
		first := newSchedHarness(t, schedOpts{})
		first.call(createParams("stale", map[string]any{"every": "1h"}))
		second, secondTimers := first.another(scheduleConfig{}, unusedLaunch, nil)
		secondCtx := schedCtx(first.ctx.Cwd, "session-b")
		mustNoError(t, second.bindSession(secondCtx))
		eq2(t, first.timers.size(), 1)
		eq2(t, secondTimers.size(), 1)
		mustMatchRe(t, `Deleted`, second.handleToolCall(sched("schedule.delete", "stale"), secondCtx).Text)
		eq2(t, secondTimers.size(), 0)
		eq2(t, first.timers.size(), 1)
		first.advance(3_600_000)
		var warnings []string
		old := scheduleWarn
		scheduleWarn = func(m string) { warnings = append(warnings, m) }
		defer func() { scheduleWarn = old }()
		first.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, len(warnings), 0)
		eq2(t, first.launchCount(), 0)
		eq2(t, first.timers.size(), 0)
		mustMatchRe(t, `No project schedules`, first.call(map[string]any{"action": "schedule.list"}).Text)
	})
	tw(t, schedFile, "reports corrupt project schedule records instead of dropping them", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		dir := filepath.Join(h.storeDir(h.ctx.Cwd), "broken")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "schedule.json"), []byte("{ bad"), 0o644)
		mustErr(t, h.call(map[string]any{"action": "schedule.list"}), `Failed to read schedule record`)
	})
	tw(t, schedFile, "fails closed when persisted schedule arguments are invalid", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("tampered-args", map[string]any{"every": "1h", "workflowScript": "return 1", "args": map[string]any{"task": "valid"}}))
		h.mgr.stop()
		path := filepath.Join(h.storeDir(h.ctx.Cwd), "tampered-args", "schedule.json")
		rec := readSchedJSON(t, path)
		rec["target"].(map[string]any)["args"] = map[string]any{"task": ""}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(path, b, 0o644)
		restored, _ := h.another(scheduleConfig{}, unusedLaunch, nil)
		err := restored.bindSession(h.ctx)
		if err == nil || !regexp.MustCompile(`workflow args\.task must not be empty`).MatchString(err.Error()) {
			t.Errorf("bindSession: %v", err)
		}
	})
	tw(t, schedFile, "skips orphan schedule directories during restore and listing", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.mgr.stop()
		_ = os.MkdirAll(filepath.Join(h.storeDir(h.ctx.Cwd), "orphan"), 0o755)
		m, _ := h.another(scheduleConfig{}, unusedLaunch, nil)
		mustNoError(t, m.bindSession(h.ctx))
		mustMatchRe(t, `No project schedules`, m.handleToolCall(map[string]any{"action": "schedule.list"}, h.ctx).Text)
	})
	tw(t, schedFile, "rejects same-id create when an orphan directory contains stale state", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		dir := filepath.Join(h.storeDir(h.ctx.Cwd), "orphan")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "active.lock"), []byte("stale"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "history.json"), []byte(`{"schemaVersion":1,"runs":[]}`), 0o644)
		mustErr(t, h.call(createParams("orphan", map[string]any{"every": "1h"})), `already exists`)
		if fileExists(filepath.Join(dir, "schedule.json")) {
			t.Error("schedule.json was written")
		}
		b, _ := os.ReadFile(filepath.Join(dir, "active.lock"))
		eq2(t, string(b), "stale")
	})
	tw(t, schedFile, "rejects schedule directories that escape through a symlink", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		root := h.storeDir(h.ctx.Cwd)
		outside := filepath.Join(h.root, "outside")
		_ = os.MkdirAll(root, 0o755)
		_ = os.Mkdir(outside, 0o755)
		if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		mustErr(t, h.call(createParams("escaped", map[string]any{"every": "1h"})), `must be a real directory`)
		if fileExists(filepath.Join(outside, "schedule.json")) {
			t.Error("wrote through the link")
		}
	})
	tw(t, schedFile, "rejects a default project schedule root that escapes through .pi/subagents", func(t *testing.T) {
		withTempHome(t)
		root := tmp(t)
		project, outside := filepath.Join(root, "project"), filepath.Join(root, "outside")
		_ = os.Mkdir(project, 0o755)
		_ = os.Mkdir(outside, 0o755)
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		ctx := schedCtx(project, "s")
		mustNoError(t, m.bindSession(ctx))
		_ = os.Mkdir(filepath.Join(project, ".pi"), 0o755)
		if err := os.Symlink(outside, filepath.Join(project, ".pi/subagents")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		mustErr(t, m.handleToolCall(createParams("escaped-root", map[string]any{"every": "1h"}), ctx), `resolves outside the real project`)
		if fileExists(filepath.Join(outside, "schedules")) {
			t.Error("created the store outside")
		}
	})
	tw(t, schedFile, "allows a default project schedule root through a shared Git worktree .pi symlink", func(t *testing.T) {
		withTempHome(t)
		repo, worktree := gitWorktreeRepo(t)
		_ = os.MkdirAll(filepath.Join(repo, ".pi"), 0o755)
		if err := os.Symlink(filepath.Join(repo, ".pi"), filepath.Join(worktree, ".pi")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		ctx := schedCtx(worktree, "s")
		mustNoError(t, m.bindSession(ctx))
		res := m.handleToolCall(map[string]any{"action": "schedule.create", "id": "shared", "every": "1h", "workflowScript": "return 1"}, ctx)
		mustNotErr(t, res)
		if !fileExists(filepath.Join(repo, ".pi", "subagents", "schedules", "shared", "schedule.json")) {
			t.Error("the schedule is not in the shared config root")
		}
	})
	tw(t, schedFile, "rejects a Git worktree .pi symlink outside the shared config root", func(t *testing.T) {
		withTempHome(t)
		repo, worktree := gitWorktreeRepo(t)
		outside := filepath.Join(filepath.Dir(repo), "outside")
		_ = os.MkdirAll(filepath.Join(repo, ".pi"), 0o755)
		_ = os.Mkdir(outside, 0o755)
		if err := os.Symlink(outside, filepath.Join(worktree, ".pi")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		err := m.bindSession(schedCtx(worktree, "s"))
		if err == nil || !strings.Contains(err.Error(), "resolves outside the real project") {
			t.Errorf("bindSession: %v", err)
		}
		if fileExists(filepath.Join(outside, "subagents")) {
			t.Error("created outside")
		}
	})
	tw(t, schedFile, "rejects separate-git-dir primary checkout sharing without a registered root", func(t *testing.T) {
		withTempHome(t)
		root := tmp(t)
		repo, gitDir, worktree := filepath.Join(root, "repository"), filepath.Join(root, "git-data", "metadata"), filepath.Join(root, "worktree")
		_ = os.MkdirAll(repo, 0o755)
		_ = os.MkdirAll(filepath.Dir(gitDir), 0o755)
		gitRun(t, repo, "init", "--separate-git-dir", gitDir)
		gitRun(t, repo, "config", "user.email", "test@example.com")
		gitRun(t, repo, "config", "user.name", "Test User")
		_ = os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644)
		gitRun(t, repo, "add", "tracked.txt")
		gitRun(t, repo, "commit", "-m", "base")
		gitRun(t, repo, "worktree", "add", "-b", "feature", worktree)
		primary := filepath.Join(repo, ".pi")
		_ = os.MkdirAll(primary, 0o755)
		if err := os.Symlink(primary, filepath.Join(worktree, ".pi")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		err := m.bindSession(schedCtx(worktree, "s"))
		if err == nil || !strings.Contains(err.Error(), "resolves outside the real project") {
			t.Errorf("bindSession: %v", err)
		}
		if fileExists(filepath.Join(primary, "subagents")) {
			t.Error("created in the primary checkout")
		}
	})
	tw(t, schedFile, "rejects an unregistered checkout ancestor with the same Git directory", func(t *testing.T) {
		withTempHome(t)
		repo, worktree := gitWorktreeRepo(t)
		outside := filepath.Join(filepath.Dir(repo), "outside")
		outsideConfig := filepath.Join(outside, ".pi")
		_ = os.MkdirAll(outsideConfig, 0o755)
		_ = os.WriteFile(filepath.Join(outside, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git")+"\n"), 0o644)
		if err := os.Symlink(outsideConfig, filepath.Join(worktree, ".pi")); err != nil {
			t.Skip("symlinks unavailable: " + err.Error())
		}
		on := true
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: &on}, Launch: unusedLaunch})
		err := m.bindSession(schedCtx(worktree, "s"))
		if err == nil || !strings.Contains(err.Error(), "resolves outside the real project") {
			t.Errorf("bindSession: %v", err)
		}
		if fileExists(filepath.Join(outsideConfig, "subagents")) {
			t.Error("created outside")
		}
	})
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// gitWorktreeRepo is a repository with one commit and a linked worktree at .worktrees/feature.
func gitWorktreeRepo(t *testing.T) (repo, worktree string) {
	t.Helper()
	root := tmp(t)
	repo = filepath.Join(root, "repository")
	worktree = filepath.Join(repo, ".worktrees", "feature")
	_ = os.MkdirAll(repo, 0o755)
	gitRun(t, repo, "init")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test User")
	_ = os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644)
	gitRun(t, repo, "add", "tracked.txt")
	gitRun(t, repo, "commit", "-m", "base")
	_ = os.MkdirAll(filepath.Dir(worktree), 0o755)
	gitRun(t, repo, "worktree", "add", "-b", "feature", worktree)
	return repo, worktree
}

func TestQuietScheduleTwins(t *testing.T) {
	script := "return runs.run('main', { agent: 'worker', task: 'Maintain backlog' })"
	tw(t, schedFile, "persists quiet only on recurring create and applies it to automatic fires", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		created := h.call(map[string]any{"action": "schedule.create", "id": "quiet-hourly", "every": "1h", "quiet": true, "workflowScript": script})
		eq2(t, recordsOf(created)[0].vals["quiet"], true)
		onDisk := readSchedJSON(t, filepath.Join(h.storeDir(h.ctx.Cwd), "quiet-hourly", "schedule.json"))
		eq2(t, onDisk["quiet"], true)
		h.advance(3_600_000)
		h.timers.fireAll()
		eq2(t, h.launch(0).params["scheduleOrigin"], map[string]any{"id": "quiet-hourly", "name": "workflowScript -> agent worker", "quiet": true})
	})
	tw(t, schedFile, "rejects a non-boolean quiet value", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		mustErr(t, h.call(map[string]any{"action": "schedule.create", "id": "bad-quiet", "every": "1h", "quiet": "yes", "workflowScript": script}), `quiet must be a boolean`)
	})
	tw(t, schedFile, "keeps one-shot at schedules noisy", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		mustErr(t, h.call(map[string]any{"action": "schedule.create", "id": "quiet-once", "at": "+10m", "quiet": true, "workflowScript": script}), `quiet is only supported for recurring schedules`)
		created := h.call(map[string]any{"action": "schedule.create", "id": "once", "at": "+10m", "workflowScript": script})
		if recordsOf(created)[0].has("quiet") {
			t.Error("a one-shot carries quiet")
		}
		h.advance(10 * 60_000)
		h.timers.fireAll()
		h.waitLaunches(1)
		if _, has := h.launch(0).params["scheduleOrigin"].(map[string]any)["quiet"]; has {
			t.Error("the origin of a one-shot is quiet")
		}
	})
	tw(t, schedFile, "keeps schedule.run noisy unless that launch asks for quiet", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(map[string]any{"action": "schedule.create", "id": "quiet-hourly", "every": "1h", "quiet": true, "workflowScript": script})
		manual := h.callAsync(sched("schedule.run", "quiet-hourly"))
		h.waitLaunches(1)
		if _, has := h.launch(0).params["scheduleOrigin"].(map[string]any)["quiet"]; has {
			t.Error("a manual run is quiet")
		}
		h.launch(0).resolve(asyncLaunched("manual-loud"))
		<-manual
		h.mgr.handleAsyncCompletion(map[string]any{"runId": "manual-loud", "success": true, "summary": "Done"})
		explicit := h.callAsync(map[string]any{"action": "schedule.run", "id": "quiet-hourly", "quiet": true})
		h.waitLaunches(2)
		eq2(t, h.launch(1).params["scheduleOrigin"], map[string]any{"id": "quiet-hourly", "name": "workflowScript -> agent worker", "quiet": true})
		h.launch(1).resolve(asyncLaunched("manual-quiet"))
		<-explicit
	})
}

func TestRecurringScheduleExecutionTwins(t *testing.T) {
	var warnings []string
	var wmu sync.Mutex
	captureWarn := func() func() {
		old := scheduleWarn
		scheduleWarn = func(m string) { wmu.Lock(); warnings = append(warnings, m); wmu.Unlock() }
		wmu.Lock()
		warnings = nil
		wmu.Unlock()
		return func() { scheduleWarn = old }
	}
	failID := func() string { panic("persistent id failure") }
	tw(t, schedFile, "launches a fixed interval from its planned time and records durable history/events", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		script := "return runs.run('main', { agent: 'worker', task: 'Maintain backlog' })"
		h.call(map[string]any{"action": "schedule.create", "id": "hourly", "every": "1h", "workflowScript": script})
		h.advance(3_600_000)
		h.timers.fireAll()
		eq2(t, h.launchCount(), 1)
		eq2(t, h.launch(0).params, map[string]any{"workflowScript": script, "args": map[string]any{}, "async": true, "context": "fresh", "cwd": h.ctx.Cwd, "mission": false, "scheduleOrigin": map[string]any{"id": "hourly", "name": "workflowScript -> agent worker"}})
		h.launch(0).resolve(asyncLaunched("async-1", "/tmp/async-1"))
		time.Sleep(40 * time.Millisecond)
		eq2(t, h.mgr.observedCompletionRunIDs(), []string{"async-1"})
		mustMatchRe(t, `running.*async async-1`, h.call(sched("schedule.history", "hourly")).Text)
		dir := filepath.Join(h.storeDir(h.ctx.Cwd), "hourly")
		if !fileExists(filepath.Join(dir, "history.json")) || !fileExists(filepath.Join(dir, "events.jsonl")) {
			t.Error("no durable history")
		}
		entries, _ := os.ReadDir(filepath.Join(dir, "runs"))
		eq2(t, len(entries), 1)
		h.mgr.handleAsyncCompletion(map[string]any{"runId": "async-1", "success": true, "summary": "Done"})
		eq2(t, len(h.mgr.observedCompletionRunIDs()), 0)
		mustMatchRe(t, `completed.*async async-1`, h.call(sched("schedule.history", "hourly")).Text)
		if fileExists(filepath.Join(dir, "active.lock")) {
			t.Error("the lock stays")
		}
		mustMatchRe(t, `2030-01-01T02:00:00.000Z`, h.call(sched("schedule.show", "hourly")).Text) // no completion drift
	})
	tw(t, schedFile, "delays retrying an overdue recurring schedule after an unexpected timer failure", func(t *testing.T) {
		defer captureWarn()()
		h := newSchedHarness(t, schedOpts{randomID: failID})
		h.call(createParams("hourly", map[string]any{"every": "1h"}))
		h.advance(3_600_000)
		h.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, h.launchCount(), 0)
		eq2(t, h.timers.size(), 1)
		eq2(t, h.timers.delays()[0], int64(3_600_000))
		mustMatchRe(t, `failed to fire: persistent id failure`, warnings[0])
		shown := h.call(sched("schedule.show", "hourly"))
		mustMatchRe(t, `State: scheduled`, shown.Text)
		mustMatchRe(t, `2030-01-01T01:00:00.000Z`, shown.Text)
	})
	tw(t, schedFile, "re-arms overdue catch-up-none schedules when missed-run recovery fails", func(t *testing.T) {
		defer captureWarn()()
		h := newSchedHarness(t, schedOpts{randomID: failID})
		h.call(createParams("none", map[string]any{"every": "1h", "catchUp": "none"}))
		h.advance(2 * 3_600_000)
		h.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, h.launchCount(), 0)
		eq2(t, h.timers.size(), 1)
		eq2(t, h.timers.delays()[0], int64(3_600_000))
		mustMatchRe(t, `failed to fire: persistent id failure`, warnings[0])
		mustMatchRe(t, `could not be restored after fire failure: persistent id failure`, warnings[1])
		mustMatchRe(t, `2030-01-01T01:00:00.000Z`, h.call(sched("schedule.show", "none")).Text)
	})
	tw(t, schedFile, "does not immediately retry an overdue one-shot after an unexpected timer failure", func(t *testing.T) {
		defer captureWarn()()
		h := newSchedHarness(t, schedOpts{randomID: failID})
		h.call(createParams("once", map[string]any{"at": "+1h"}))
		h.advance(3_600_000)
		h.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, h.launchCount(), 0)
		eq2(t, h.timers.size(), 0)
		mustMatchRe(t, `failed to fire: persistent id failure`, warnings[0])
		shown := h.call(sched("schedule.show", "once"))
		mustMatchRe(t, `State: scheduled`, shown.Text)
		mustMatchRe(t, `2030-01-01T01:00:00.000Z`, shown.Text)
	})
	tw(t, schedFile, "run-due launches the latest missed occurrence while catchUp none records a miss", func(t *testing.T) {
		latest := newSchedHarness(t, schedOpts{})
		latest.call(createParams("latest", map[string]any{"every": "1h", "catchUp": "latest"}))
		latest.mgr.stop()
		latest.advance(3 * 3_600_000)
		mustNoError(t, latest.mgr.bindSession(latest.ctx))
		due := latest.callAsync(map[string]any{"action": "schedule.run-due"})
		latest.waitLaunches(1)
		latest.launch(0).resolve(asyncLaunched("late-1"))
		mustMatchRe(t, `Processed 1 due schedule`, (<-due).Text)
		mustMatchRe(t, `2030-01-01T03:00:00.000Z`, latest.call(sched("schedule.history", "latest")).Text) // the latest missed slot
		none := newSchedHarness(t, schedOpts{})
		none.call(createParams("none", map[string]any{"every": "1h", "catchUp": "none"}))
		none.mgr.stop()
		none.advance(3 * 3_600_000)
		mustNoError(t, none.mgr.bindSession(none.ctx))
		mustMatchRe(t, `missed`, none.call(sched("schedule.history", "none")).Text)
		eq2(t, none.launchCount(), 0)
	})
	tw(t, schedFile, "keeps project timers, contexts, and completion ownership across session_start bindings", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("project-a", map[string]any{"every": "1h"}))
		projectB := filepath.Join(h.root, "project-b")
		_ = os.Mkdir(projectB, 0o755)
		ctxB := schedCtx(projectB, "session-b")
		mustNoError(t, h.mgr.bindSession(ctxB))
		mustNotErr(t, h.mgr.handleToolCall(createParams("project-b", map[string]any{"every": "1h"}), ctxB))
		eq2(t, h.timers.size(), 2) // both project timers remain armed after session_start binds project B
		h.advance(3_600_000)
		h.timers.fireAll()
		h.waitLaunches(2)
		byCwd := map[string]*testLaunch{}
		for i := 0; i < 2; i++ {
			byCwd[h.launch(i).ctx.Cwd] = h.launch(i)
		}
		a, b := byCwd[h.ctx.Cwd], byCwd[projectB]
		eq2(t, a.ctx.SessionID, "session-a")
		eq2(t, a.ctx.SessionFile, filepath.Join(h.ctx.Cwd, "session-a.jsonl"))
		eq2(t, b.ctx.SessionID, "session-b")
		eq2(t, b.ctx.SessionFile, filepath.Join(projectB, "session-b.jsonl"))
		a.resolve(asyncLaunched("async-a"))
		b.resolve(asyncLaunched("async-b"))
		time.Sleep(40 * time.Millisecond)
		h.mgr.handleAsyncCompletion(map[string]any{"runId": "async-a", "success": true})
		mustMatchRe(t, `completed.*async async-a`, h.call(sched("schedule.history", "project-a")).Text)
		if fileExists(filepath.Join(h.storeDir(h.ctx.Cwd), "project-a", "active.lock")) {
			t.Error("the lock stays")
		}
	})
	tw(t, schedFile, "uses only a bound target session for an explicit cross-project cwd", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		target := filepath.Join(h.root, "explicit-target")
		_ = os.Mkdir(target, 0o755)
		mustErr(t, h.call(createParams("targeted", map[string]any{"cwd": target, "every": "1h"})), `until that project has been opened`)
		targetCtx := schedCtx(target, "target-session")
		mustNoError(t, h.mgr.bindSession(targetCtx))
		mustNoError(t, h.mgr.bindSession(h.ctx))
		mustErr(t, h.call(createParams("session-only-targeted", map[string]any{"cwd": target, "every": "1h", "sessionOnly": true})), `sessionOnly schedules cannot use an explicit cross-project cwd`)
		eq2(t, len(h.listAll(target)), 0) // a rejected session-only schedule must not dead-end in the target project
		h.call(createParams("targeted", map[string]any{"cwd": target, "every": "1h"}))
		h.advance(3_600_000)
		h.timers.fireAll()
		eq2(t, h.launchCount(), 1)
		l := h.launch(0)
		eq2(t, l.ctx.Cwd, target)
		eq2(t, l.ctx.SessionID, "target-session")
		eq2(t, l.ctx.SessionFile, filepath.Join(target, "target-session.jsonl"))
		eq2(t, l.params["cwd"], target)
		l.resolve(asyncLaunched("target-async"))
	})
	tw(t, schedFile, "reconciles owner completion after an unrelated store becomes malformed", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		badProject := filepath.Join(h.root, "bad-project")
		_ = os.Mkdir(badProject, 0o755)
		badCtx, ownerCtx := schedCtx(badProject, "bad-session"), schedCtx(h.ctx.Cwd, "owner-session")
		isolated := filepath.Join(h.root, "isolated-stores")
		done := make(chan scheduleLaunchResult, 1)
		n := 0
		m := newScheduledRunManager(scheduledRunDeps{Config: scheduleConfig{Enabled: new(true)}, StoreRoot: isolated, Now: h.now, RandomID: func() string { n++; return fmt.Sprintf("isolated-%d", n) }, Timers: &fakeSchedTimers{values: map[int]schedTimer{}, h: h},
			Launch: func(p map[string]any, c scheduleCtx) scheduleLaunchResult {
				return <-done
			}})
		mustNoError(t, m.bindSession(badCtx))
		mustNoError(t, m.bindSession(ownerCtx))
		mustNotErr(t, m.handleToolCall(createParams("owner", map[string]any{"every": "1h"}), ownerCtx))
		manual := make(chan scheduleResult, 1)
		go func() { manual <- m.handleToolCall(sched("schedule.run", "owner"), ownerCtx) }()
		time.Sleep(50 * time.Millisecond)
		done <- asyncLaunched("owner-async")
		<-manual
		badDir := filepath.Join(scheduledRunStorePath(badProject, "", isolated), "broken")
		_ = os.MkdirAll(badDir, 0o755)
		_ = os.WriteFile(filepath.Join(badDir, "schedule.json"), []byte("{ bad"), 0o644)
		ownerRoot := scheduledRunStorePath(ownerCtx.Cwd, "", isolated)
		sibling := filepath.Join(ownerRoot, "broken-sibling")
		_ = os.MkdirAll(sibling, 0o755)
		_ = os.WriteFile(filepath.Join(sibling, "schedule.json"), []byte("{ bad"), 0o644)
		old := scheduleLogError
		scheduleLogError = func(string, error) {}
		m.handleAsyncCompletion(map[string]any{"runId": "owner-async", "success": true})
		scheduleLogError = old
		mustMatchRe(t, `completed.*async owner-async`, m.handleToolCall(sched("schedule.history", "owner"), ownerCtx).Text)
		if fileExists(filepath.Join(ownerRoot, "owner", "active.lock")) {
			t.Error("the lock stays")
		}
	})
	tw(t, schedFile, "reconciles a terminal async status after a new session binds", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("restart", map[string]any{"every": "1h"}))
		h.advance(3_600_000)
		h.timers.fireAll()
		asyncDir := filepath.Join(h.root, "async-restart")
		_ = os.Mkdir(asyncDir, 0o755)
		h.launch(0).resolve(asyncLaunched("async-restart", asyncDir))
		time.Sleep(40 * time.Millisecond)
		h.mgr.stop()
		b, _ := json.Marshal(map[string]any{"runId": "async-restart", "mode": "single", "state": "complete", "startedAt": h.now(), "endedAt": h.now()})
		_ = os.WriteFile(filepath.Join(asyncDir, "status.json"), b, 0o644)
		next, _ := h.another(scheduleConfig{}, unusedLaunch, func() string { return "reconciled-id" })
		nextCtx := schedCtx(h.ctx.Cwd, "session-b")
		mustNoError(t, next.bindSession(nextCtx))
		mustMatchRe(t, `completed.*async async-restart`, next.handleToolCall(sched("schedule.history", "restart"), nextCtx).Text)
	})
	tw(t, schedFile, "records elapsed overlap and catch-up-none slots without an immediate rerun", func(t *testing.T) {
		latest := newSchedHarness(t, schedOpts{})
		latest.call(createParams("overlap", map[string]any{"every": "1h", "catchUp": "latest"}))
		latest.advance(3_600_000)
		latest.timers.fireAll()
		latest.launch(0).resolve(asyncLaunched("long-run"))
		time.Sleep(40 * time.Millisecond)
		latest.advance(3 * 3_600_000)
		latest.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, latest.launchCount(), 1) // an overlapping occurrence is skipped instead of queued
		latest.mgr.handleAsyncCompletion(map[string]any{"runId": "long-run", "success": true})
		hist := latest.call(sched("schedule.history", "overlap")).Text
		mustMatchRe(t, `skipped`, hist)
		mustMatchRe(t, `completed.*async long-run`, hist)
		mustMatchRe(t, `2030-01-01T05:00:00.000Z`, latest.call(sched("schedule.show", "overlap")).Text)
		none := newSchedHarness(t, schedOpts{})
		none.call(createParams("none-overlap", map[string]any{"every": "1h", "catchUp": "none"}))
		none.advance(3_600_000)
		none.timers.fireAll()
		none.launch(0).resolve(asyncLaunched("none-long-run"))
		time.Sleep(40 * time.Millisecond)
		none.advance(3 * 3_600_000)
		none.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, none.launchCount(), 1)
		none.mgr.handleAsyncCompletion(map[string]any{"runId": "none-long-run", "success": true})
		mustMatchRe(t, `skipped`, none.call(sched("schedule.history", "none-overlap")).Text)
		mustMatchRe(t, `2030-01-01T05:00:00.000Z`, none.call(sched("schedule.show", "none-overlap")).Text)
	})
	tw(t, schedFile, "manual run uses the normal async target and overlap skip prevents a second launch", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(map[string]any{"action": "schedule.create", "id": "manual", "every": "1h", "workflowScript": "return 1"})
		first := h.callAsync(sched("schedule.run", "manual"))
		h.waitLaunches(1)
		h.launch(0).resolve(asyncLaunched("workflow-1"))
		mustMatchRe(t, `async workflow-1`, (<-first).Text)
		eq2(t, h.mgr.referencedAsyncRunIDs(), []string{"workflow-1"})
		second := h.call(sched("schedule.run", "manual"))
		mustMatchRe(t, `skipped`, second.Text)
		eq2(t, h.launchCount(), 1)
	})
	tw(t, schedFile, "a successful manual launch satisfies the next natural fire", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("manual-interval", map[string]any{"every": "1h"}))
		h.advance(5 * 60_000)
		manual := h.callAsync(sched("schedule.run", "manual-interval"))
		h.waitLaunches(1)
		h.launch(0).resolve(asyncLaunched("manual-async"))
		<-manual
		mustMatchRe(t, `Next: 2030-01-01T01:05:00.000Z`, h.call(sched("schedule.show", "manual-interval")).Text)
		h.mgr.handleAsyncCompletion(map[string]any{"runId": "manual-async", "success": true})
		h.setNow(time.Date(2030, 1, 1, 1, 0, 0, 0, time.UTC).UnixMilli())
		h.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, h.launchCount(), 1)
	})
	tw(t, schedFile, "a failed manual launch still honors a natural fire that overlapped while attachment was pending", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("overlap-fail", map[string]any{"every": "1h"}))
		manual := h.callAsync(sched("schedule.run", "overlap-fail"))
		h.waitLaunches(1)
		h.advance(3_600_000)
		h.timers.fireAll()
		time.Sleep(30 * time.Millisecond)
		eq2(t, h.launchCount(), 1)
		h.launch(0).resolve(scheduleLaunchResult{Text: "spawn failed", IsError: true})
		mustMatchRe(t, `failed_launch`, (<-manual).Text)
		mustMatchRe(t, `Next: 2030-01-01T01:00:00.000Z`, h.call(sched("schedule.show", "overlap-fail")).Text)
		h.timers.fireAll()
		h.waitLaunches(2)
	})
	tw(t, schedFile, "distinguishes failed launch from failed async completion", func(t *testing.T) {
		h := newSchedHarness(t, schedOpts{})
		h.call(createParams("failures", map[string]any{"every": "1h"}))
		first := h.callAsync(sched("schedule.run", "failures"))
		h.waitLaunches(1)
		h.launch(0).resolve(scheduleLaunchResult{Text: "spawn failed", IsError: true})
		mustMatchRe(t, `failed_launch`, (<-first).Text)
		second := h.callAsync(sched("schedule.run", "failures"))
		h.waitLaunches(2)
		h.launch(1).resolve(asyncLaunched("async-fail"))
		<-second
		h.mgr.handleAsyncCompletion(map[string]any{"id": "async-fail", "success": false, "summary": "child failed"})
		eq2(t, h.mgr.referencedAsyncRunIDs(), []string{"async-fail"})
		hist := h.call(sched("schedule.history", "failures")).Text
		mustMatchRe(t, `failed_run.*async async-fail`, hist)
		mustMatchRe(t, `failed_launch`, hist)
	})
}
