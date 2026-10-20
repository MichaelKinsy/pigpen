package pi_permission_system

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Twins of test/authority/forwarding-liveness.test.ts.

type fwLivenessEnv struct {
	t             *testing.T
	root          string
	forwardingDir string
	clock         int64
}

func fwNewLivenessEnv(t *testing.T) *fwLivenessEnv {
	root := t.TempDir()
	return &fwLivenessEnv{t: t, root: root, forwardingDir: filepath.Join(root, "forwarding"), clock: 1_700_000_000_000}
}

// store builds a ServingHeartbeatStore on the env's clock with pid 4242; override edits the deps before construction.
func (e *fwLivenessEnv) store(override func(*ServingHeartbeatStoreDeps)) (*ServingHeartbeatStore, *fwLogger) {
	logger := &fwLogger{}
	deps := ServingHeartbeatStoreDeps{ForwardingDir: e.forwardingDir, Logger: logger, Now: func() int64 { return e.clock }, PID: 4242}
	if override != nil {
		override(&deps)
	}
	return NewServingHeartbeatStore(deps), logger
}

func (e *fwLivenessEnv) readRecord(sessionID string) *jsObject {
	e.t.Helper()
	raw, err := os.ReadFile(ServingHeartbeatPath(e.forwardingDir, sessionID))
	if err != nil {
		e.t.Fatal(err)
	}
	return fwParse(e.t, string(raw)).(*jsObject)
}

// publishRecord publishes a record directly, standing in for another process's session.
func (e *fwLivenessEnv) publishRecord(sessionID string, overrides ...any) {
	e.t.Helper()
	record := fwObj("sessionId", sessionID, "pid", 4242, "updatedAt", e.clock)
	for i := 0; i < len(overrides); i += 2 {
		record.set(overrides[i].(string), fwNorm(overrides[i+1]))
	}
	e.publishRaw(sessionID, marshalJSON(record, ""))
}

// publishRaw publishes an unusable record, standing in for a truncated or foreign write.
func (e *fwLivenessEnv) publishRaw(sessionID, contents string) {
	e.t.Helper()
	if err := os.MkdirAll(ServingHeartbeatDir(e.forwardingDir), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(ServingHeartbeatPath(e.forwardingDir, sessionID), []byte(contents), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// onlyOwnPidAlive: only pid 4242 is running, unless a test says otherwise.
func onlyOwnPidAlive(pid int) bool { return pid == 4242 }

func fwNum(o *jsObject, k string) float64 { v, _ := o.num(k); return v }

func TestServingHeartbeatTimingConstants(t *testing.T) {
	const f = "authority/forwarding-liveness"
	tw(t, f, "refreshes less often than the inbox is polled, so a per-tick call is cheap", func(t *testing.T) {
		if !(ServingHeartbeatRefreshMs > PermissionForwardingPollIntervalMs) {
			t.Fatal("refresh not slower than the poll")
		}
	})
	tw(t, f, "tolerates several missed refreshes before calling a record stale", func(t *testing.T) {
		if !(ServingHeartbeatStaleMs > ServingHeartbeatRefreshMs*2) {
			t.Fatal("stale too soon")
		}
	})
}

func TestServingHeartbeatPath(t *testing.T) {
	const f = "authority/forwarding-liveness"
	tw(t, f, "places the record beside the sessions tree, not inside it", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		if ServingHeartbeatDir(e.forwardingDir) != filepath.Join(e.forwardingDir, "serving") {
			t.Fatal(ServingHeartbeatDir(e.forwardingDir))
		}
		if ServingHeartbeatPath(e.forwardingDir, "sess-1") != filepath.Join(e.forwardingDir, "serving", "sess-1.json") {
			t.Fatal(ServingHeartbeatPath(e.forwardingDir, "sess-1"))
		}
	})
	tw(t, f, "encodes a session id that would otherwise escape the directory", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		if got := ServingHeartbeatPath(e.forwardingDir, "a/../b"); got != filepath.Join(e.forwardingDir, "serving", "a%2F..%2Fb.json") {
			t.Fatal(got)
		}
	})
}

func TestServingHeartbeatStoreMarkServing(t *testing.T) {
	const f = "authority/forwarding-liveness"
	tw(t, f, "publishes the session id, the serving process, and the write time", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		if !fwEqual(e.readRecord("sess-1"), fwObj("sessionId", "sess-1", "pid", 4242, "updatedAt", e.clock)) {
			t.Fatalf("%s", marshalJSON(e.readRecord("sess-1"), ""))
		}
	})
	tw(t, f, "creates the record owner-only inside an owner-only directory", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		fwSkipWithoutPosixModes(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		rec, _ := os.Stat(ServingHeartbeatPath(e.forwardingDir, "sess-1"))
		dir, _ := os.Stat(ServingHeartbeatDir(e.forwardingDir))
		if rec.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Fatalf("modes %o %o", rec.Mode().Perm(), dir.Mode().Perm())
		}
	})
	tw(t, f, "does not rewrite within the refresh window, so a per-tick caller is cheap", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		first := e.clock
		e.clock += ServingHeartbeatRefreshMs - 1
		store.MarkServing("sess-1")
		if got := fwNum(e.readRecord("sess-1"), "updatedAt"); got != float64(first) {
			t.Fatalf("rewritten: %v", got)
		}
	})
	tw(t, f, "rewrites once the refresh window has elapsed", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		e.clock += ServingHeartbeatRefreshMs
		store.MarkServing("sess-1")
		if got := fwNum(e.readRecord("sess-1"), "updatedAt"); got != float64(e.clock) {
			t.Fatalf("not rewritten: %v", got)
		}
	})
	tw(t, f, "rewrites immediately for a different session id", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		e.clock++
		store.MarkServing("sess-2")
		if fwNum(e.readRecord("sess-2"), "updatedAt") != float64(e.clock) || fwNum(e.readRecord("sess-1"), "updatedAt") != float64(e.clock-1) {
			t.Fatal("records wrong")
		}
	})
	tw(t, f, "republishes at the next refresh boundary when the record was removed underneath it", func(t *testing.T) {
		// The gap is bounded by the refresh window, which is shorter than the grace a forwarding child waits out, so a pruned or
		// externally deleted record cannot make a live session look unserved for long enough to abandon a request.
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		if err := os.Remove(ServingHeartbeatPath(e.forwardingDir, "sess-1")); err != nil {
			t.Fatal(err)
		}
		e.clock += ServingHeartbeatRefreshMs
		store.MarkServing("sess-1")
		if fwNum(e.readRecord("sess-1"), "updatedAt") != float64(e.clock) {
			t.Fatal("not republished")
		}
	})
	tw(t, f, "reports an unusable directory instead of throwing out of the poll timer", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		if err := os.WriteFile(filepath.Join(e.root, "blocker"), []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		store, logger := e.store(func(d *ServingHeartbeatStoreDeps) { d.ForwardingDir = filepath.Join(e.root, "blocker", "forwarding") })
		store.MarkServing("sess-1")
		found := false
		for _, c := range logger.reviews() {
			if c.event == "permission_forwarding.error" {
				if m, _ := c.details.str("message"); containsStr(m, "serving") {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("no error entry: %+v", logger.reviews())
		}
	})
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestServingHeartbeatStoreClearServing(t *testing.T) {
	const f = "authority/forwarding-liveness"
	tw(t, f, "withdraws the record", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		store.ClearServing("sess-1")
		if pathExists(ServingHeartbeatPath(e.forwardingDir, "sess-1")) {
			t.Fatal("record survived")
		}
	})
	tw(t, f, "leaves the directory in place, so a sibling session's write cannot race it", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		store.ClearServing("sess-1")
		if !pathExists(ServingHeartbeatDir(e.forwardingDir)) {
			t.Fatal("directory removed")
		}
	})
	tw(t, f, "leaves a sibling session's record untouched", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		store.MarkServing("sess-2")
		store.ClearServing("sess-1")
		if s, _ := e.readRecord("sess-2").str("sessionId"); s != "sess-2" {
			t.Fatal(s)
		}
	})
	tw(t, f, "is a no-op for a session that was never marked", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, logger := e.store(nil)
		store.ClearServing("sess-1")
		if len(logger.reviews()) != 0 {
			t.Fatalf("%+v", logger.reviews())
		}
	})
	tw(t, f, "republishes after a withdrawal rather than staying throttled", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(nil)
		store.MarkServing("sess-1")
		store.ClearServing("sess-1")
		e.clock++
		store.MarkServing("sess-1")
		if fwNum(e.readRecord("sess-1"), "updatedAt") != float64(e.clock) {
			t.Fatal("still throttled")
		}
	})
}

func TestServingHeartbeatStoreRead(t *testing.T) {
	const f = "authority/forwarding-liveness"
	own := func(d *ServingHeartbeatStoreDeps) { d.IsProcessAlive = onlyOwnPidAlive }
	tw(t, f, "reports absent when the session has published nothing", func(t *testing.T) {
		store, _ := fwNewLivenessEnv(t).store(own)
		if store.Read("sess-1") != HeartbeatAbsent {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports alive for a fresh record whose process is running", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1")
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatAlive {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports alive one tick short of the staleness window", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1")
		e.clock += ServingHeartbeatStaleMs - 1
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatAlive {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports stale once the record outlives the staleness window", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1")
		e.clock += ServingHeartbeatStaleMs
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatStale {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports dead_pid when the recorded process is gone", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1", "pid", 9999)
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatDeadPID {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "names the dead process rather than the age, when the record is both", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1", "pid", 9999)
		e.clock += ServingHeartbeatStaleMs
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatDeadPID {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports absent for an unparseable record", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRaw("sess-1", "{ truncated")
		store, _ := e.store(own)
		if store.Read("sess-1") != HeartbeatAbsent {
			t.Fatal(store.Read("sess-1"))
		}
	})
	tw(t, f, "reports absent rather than probing a pid that names no process", func(t *testing.T) {
		// kill(0, 0) addresses the caller's own process group, so a malformed record must never reach the liveness probe.
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1", "pid", 0)
		probed := false
		store, _ := e.store(func(d *ServingHeartbeatStoreDeps) {
			d.IsProcessAlive = func(pid int) bool { probed = true; return onlyOwnPidAlive(pid) }
		})
		if store.Read("sess-1") != HeartbeatAbsent || probed {
			t.Fatalf("state=%s probed=%v", store.Read("sess-1"), probed)
		}
	})
	tw(t, f, "does not flood the log while a child polls an unreadable record", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRaw("sess-1", "{ truncated")
		store, logger := e.store(own)
		store.Read("sess-1")
		store.Read("sess-1")
		if len(logger.reviews()) != 0 {
			t.Fatalf("%+v", logger.reviews())
		}
	})
}

func TestServingHeartbeatStoreServingIds(t *testing.T) {
	const f = "authority/forwarding-liveness"
	own := func(d *ServingHeartbeatStoreDeps) { d.IsProcessAlive = onlyOwnPidAlive }
	tw(t, f, "is empty when nothing has been published", func(t *testing.T) {
		store, _ := fwNewLivenessEnv(t).store(own)
		if !reflect.DeepEqual(store.ServingIDs(), []string{}) {
			t.Fatalf("%#v", store.ServingIDs())
		}
	})
	tw(t, f, "lists the sessions whose records read as alive", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1")
		e.publishRecord("sess-2")
		store, _ := e.store(own)
		ids := store.ServingIDs()
		sort.Strings(ids)
		if !reflect.DeepEqual(ids, []string{"sess-1", "sess-2"}) {
			t.Fatalf("%v", ids)
		}
	})
	tw(t, f, "omits a session whose process is gone", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("sess-1")
		e.publishRecord("sess-2", "pid", 9999)
		store, _ := e.store(own)
		if !reflect.DeepEqual(store.ServingIDs(), []string{"sess-1"}) {
			t.Fatalf("%v", store.ServingIDs())
		}
	})
	tw(t, f, "reports the session's own id, not its encoded filename", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("a/b")
		store, _ := e.store(own)
		if !reflect.DeepEqual(store.ServingIDs(), []string{"a/b"}) {
			t.Fatalf("%v", store.ServingIDs())
		}
	})
}

func TestServingHeartbeatStorePruning(t *testing.T) {
	const f = "authority/forwarding-liveness"
	own := func(d *ServingHeartbeatStoreDeps) { d.IsProcessAlive = onlyOwnPidAlive }
	tw(t, f, "removes a record left behind by a process that is gone", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("dead-session", "pid", 9999)
		store, _ := e.store(own)
		store.MarkServing("sess-1")
		if pathExists(ServingHeartbeatPath(e.forwardingDir, "dead-session")) {
			t.Fatal("not swept")
		}
	})
	tw(t, f, "removes a record no reader could use", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRaw("corrupt-session", "{ truncated")
		store, _ := e.store(own)
		store.MarkServing("sess-1")
		if pathExists(ServingHeartbeatPath(e.forwardingDir, "corrupt-session")) {
			t.Fatal("not swept")
		}
	})
	tw(t, f, "keeps a stale record whose process is still running", func(t *testing.T) {
		// Being behind on refreshes is not proof of death, and the reader already reports it as stale without removing it.
		e := fwNewLivenessEnv(t)
		e.publishRecord("slow-session", "updatedAt", e.clock-ServingHeartbeatStaleMs)
		store, _ := e.store(own)
		store.MarkServing("sess-1")
		if !pathExists(ServingHeartbeatPath(e.forwardingDir, "slow-session")) {
			t.Fatal("swept")
		}
	})
	tw(t, f, "publishes its own record alongside the sweep", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		e.publishRecord("dead-session", "pid", 9999)
		store, _ := e.store(own)
		store.MarkServing("sess-1")
		if s, _ := e.readRecord("sess-1").str("sessionId"); s != "sess-1" {
			t.Fatal(s)
		}
	})
	tw(t, f, "sweeps once per session rather than on every refresh", func(t *testing.T) {
		e := fwNewLivenessEnv(t)
		store, _ := e.store(own)
		store.MarkServing("sess-1")
		e.publishRecord("dead-session", "pid", 9999)
		e.clock += ServingHeartbeatRefreshMs
		store.MarkServing("sess-1")
		if !pathExists(ServingHeartbeatPath(e.forwardingDir, "dead-session")) {
			t.Fatal("swept twice")
		}
	})
}

type fwRegistryFake struct {
	marked      []string
	isServingOf []string
}

func (r *fwRegistryFake) IsServing(id string) bool {
	r.isServingOf = append(r.isServingOf, id)
	for _, m := range r.marked {
		if m == id {
			return true
		}
	}
	return false
}
func (r *fwRegistryFake) ServingIDs() []string { return r.marked }

type fwHeartbeatsFake struct {
	state HeartbeatState
	ids   []string
	reads int
}

func (h *fwHeartbeatsFake) Read(string) HeartbeatState { h.reads++; return h.state }
func (h *fwHeartbeatsFake) ServingIDs() []string       { return h.ids }

var (
	fwRegistryTarget = PermissionForwardingTarget{SessionID: "parent", Source: TargetSourceRegistry}
	fwEnvTarget      = PermissionForwardingTarget{SessionID: "parent", Source: TargetSourceEnv}
)

func TestForwardingLivenessJudgeIsServing(t *testing.T) {
	const f = "authority/forwarding-liveness"
	answer := func(j *ForwardingLivenessJudge, target PermissionForwardingTarget) bool {
		serving, known := j.IsServing(target)
		if !known {
			t.Fatal("the judge must always know")
		}
		return serving
	}
	tw(t, f, "answers an in-process target from the registry", func(t *testing.T) {
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{marked: []string{"parent"}}, Heartbeats: &fwHeartbeatsFake{state: HeartbeatAbsent}})
		if !answer(j, fwRegistryTarget) {
			t.Fatal("not serving")
		}
	})
	tw(t, f, "reports an unmarked in-process target as not serving", func(t *testing.T) {
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{}, Heartbeats: &fwHeartbeatsFake{state: HeartbeatAlive}})
		if answer(j, fwRegistryTarget) {
			t.Fatal("serving")
		}
	})
	tw(t, f, "answers an out-of-process target from the filesystem heartbeat", func(t *testing.T) {
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{}, Heartbeats: &fwHeartbeatsFake{state: HeartbeatAlive}})
		if !answer(j, fwEnvTarget) {
			t.Fatal("not serving")
		}
	})
	// it.each(["absent", "stale", "dead_pid"]): outside the ledger (the title carries the state).
	for _, state := range []HeartbeatState{HeartbeatAbsent, HeartbeatStale, HeartbeatDeadPID} {
		t.Run("reports an out-of-process target as not serving when its heartbeat is "+string(state), func(t *testing.T) {
			j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{marked: []string{"parent"}}, Heartbeats: &fwHeartbeatsFake{state: state}})
			if answer(j, fwEnvTarget) {
				t.Fatal("serving")
			}
		})
	}
	tw(t, f, "does not touch the filesystem for an in-process target", func(t *testing.T) {
		heartbeats := &fwHeartbeatsFake{state: HeartbeatAbsent}
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{marked: []string{"parent"}}, Heartbeats: heartbeats})
		j.IsServing(fwRegistryTarget)
		if heartbeats.reads != 0 {
			t.Fatal("heartbeat read")
		}
	})
	tw(t, f, "does not consult the registry for an out-of-process target", func(t *testing.T) {
		// Its parent lives in another process, so an absent mark would say nothing: reading one would fast-fail every
		// out-of-process child.
		registry := &fwRegistryFake{}
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: registry, Heartbeats: &fwHeartbeatsFake{state: HeartbeatAlive}})
		j.IsServing(fwEnvTarget)
		if len(registry.isServingOf) != 0 {
			t.Fatal("registry consulted")
		}
	})
}

func TestForwardingLivenessJudgeDescribe(t *testing.T) {
	const f = "authority/forwarding-liveness"
	tw(t, f, "names the registry channel and the ids it observed", func(t *testing.T) {
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{marked: []string{"other-parent"}}, Heartbeats: &fwHeartbeatsFake{state: HeartbeatAlive, ids: []string{"unrelated"}}})
		if got := j.Describe(fwRegistryTarget); !reflect.DeepEqual(got, ServingObservation{Channel: "registry", ServingIDs: []string{"other-parent"}}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "names the heartbeat channel, the state it read, and the ids it observed", func(t *testing.T) {
		j := NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{Registry: &fwRegistryFake{marked: []string{"unrelated"}}, Heartbeats: &fwHeartbeatsFake{state: HeartbeatDeadPID, ids: []string{"other-parent"}}})
		if got := j.Describe(fwEnvTarget); !reflect.DeepEqual(got, ServingObservation{Channel: "heartbeat", State: HeartbeatDeadPID, ServingIDs: []string{"other-parent"}}) {
			t.Fatalf("%+v", got)
		}
	})
}
