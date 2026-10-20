package pi_subagents

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Twins of control-channel.test.ts. The original watches the control inbox with Node's fs.watch (and a slow safety poll) where the
// platform has it, and polls every 250 ms where it does not (Darwin, Windows). Go's standard library has no file watcher, so the
// port takes the polling branch on every platform; the cases that test the native watcher itself are the skips below.

func ccTmpAsyncDir(t *testing.T) string { return filepath.Join(t.TempDir(), "run") }

func ccClock(t *testing.T, fn func() int64) {
	old := controlClock
	t.Cleanup(func() { controlClock = old })
	controlClock = fn
}

func ccFixedClock(t *testing.T, ts int64) { ccClock(t, func() int64 { return ts }) }

func ccReadJSON(t *testing.T, p string) map[string]any {
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

func ccExists(p string) bool { _, err := os.Stat(p); return err == nil }

func ccListDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// hookFS is osControlFS with replaceable operations.
type hookFS struct {
	osControlFS
	mkdir    func(string) error
	readDir  func(string) ([]string, error)
	readFile func(string) ([]byte, error)
	remove   func(string) error
}

func (h hookFS) MkdirAll(d string) error {
	if h.mkdir != nil {
		return h.mkdir(d)
	}
	return h.osControlFS.MkdirAll(d)
}
func (h hookFS) ReadDir(d string) ([]string, error) {
	if h.readDir != nil {
		return h.readDir(d)
	}
	return h.osControlFS.ReadDir(d)
}
func (h hookFS) ReadFile(p string) ([]byte, error) {
	if h.readFile != nil {
		return h.readFile(p)
	}
	return h.osControlFS.ReadFile(p)
}
func (h hookFS) Remove(p string) error {
	if h.remove != nil {
		return h.remove(p)
	}
	return h.osControlFS.Remove(p)
}

// manualTimers records the polling intervals and fires a poll by hand (the watch event of the original's harness).
type manualTimers struct {
	mu      sync.Mutex
	delays  []time.Duration
	fns     []func()
	stopped int
}

func (m *manualTimers) Every(d time.Duration, fn func()) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delays = append(m.delays, d)
	m.fns = append(m.fns, fn)
	return func() { m.mu.Lock(); m.stopped++; m.mu.Unlock() }
}

func (m *manualTimers) tick() {
	m.mu.Lock()
	fns := append([]func(){}, m.fns...)
	m.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func steerReq(id string, ts float64, message string) steerRequest {
	return steerRequest{Type: "steer", ID: id, TS: ts, Message: message}
}

func TestControlChannelTwins(t *testing.T) {
	const f = "control-channel"
	idx := func(i int) *int { return &i }

	tw(t, f, "writes a parseable interrupt request, creating the inbox dir", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		ccFixedClock(t, 999)
		p, err := requestAsyncInterrupt(dir, "test")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, p, interruptRequestPath(dir), "path")
		data := ccReadJSON(t, p)
		equal(t, [3]any{data["type"], data["ts"], data["source"]}, [3]any{"interrupt", float64(999), "test"}, "request")
	})

	tw(t, f, "writes and consumes a stop request", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		ccFixedClock(t, 1234)
		p, err := requestAsyncStop(dir, "test", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		equal(t, filepath.Dir(p), stopRequestsDir(dir), "directory")
		data := ccReadJSON(t, p)
		equal(t, [3]any{data["type"], data["ts"], data["source"]}, [3]any{"stop", float64(1234), "test"}, "request")
		equal(t, consumeStopRequest(dir, nil), true, "consumed")
		equal(t, ccExists(p), false, "removed")
		equal(t, consumeStopRequest(dir, nil), false, "second consume")
	})

	tw(t, f, "removes a stop request if the inbox closes while it is being written", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		ccFixedClock(t, 1234)
		old := controlStopWrite
		t.Cleanup(func() { controlStopWrite = old })
		controlStopWrite = func(p string, req controlRequest) error {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			b, _ := json.Marshal(req)
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return err
			}
			return closeStopInbox(dir)
		}
		_, err := requestAsyncStop(dir, "test", nil, "")
		hasMatch(t, err, "Retry stop after runner shutdown is observed")
		equal(t, ccListDir(t, stopRequestsDir(dir)), []string{}, "stop requests")
	})

	tw(t, f, "keeps concurrent stop requests instead of replacing the first one", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		next := int64(1233)
		ccClock(t, func() int64 { next++; return next })
		for _, c := range []struct {
			i  int
			id string
		}{{1, "slow"}, {2, "review"}} {
			if _, err := requestAsyncStop(dir, "test", idx(c.i), c.id); err != nil {
				t.Fatal(err)
			}
		}
		equal(t, consumeStopRequestPayloads(dir, nil, nil), []controlRequest{
			{Type: "stop", TS: 1234, Source: "test", TargetIndex: idx(1), ChildID: "slow"},
			{Type: "stop", TS: 1235, Source: "test", TargetIndex: idx(2), ChildID: "review"},
		}, "payloads")
		equal(t, len(consumeStopRequestPayloads(dir, nil, nil)), 0, "second consume")
	})

	tw(t, f, "writes and consumes a child-scoped stop request", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		ccFixedClock(t, 1234)
		if _, err := requestAsyncStop(dir, "test", idx(2), "review"); err != nil {
			t.Fatal(err)
		}
		got := consumeStopRequestPayloads(dir, nil, nil)
		equal(t, got, []controlRequest{{Type: "stop", TS: 1234, Source: "test", TargetIndex: idx(2), ChildID: "review"}}, "payload")
		equal(t, len(consumeStopRequestPayloads(dir, nil, nil)), 0, "second consume")
	})

	tw(t, f, "drops malformed stop files instead of widening them to run-level stops", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		p := filepath.Join(stopRequestsDir(dir), "0000000001234-bad.json")
		writeFile(t, p, "{ not json")
		equal(t, len(consumeStopRequestPayloads(dir, nil, nil)), 0, "payloads")
		equal(t, ccExists(p), false, "removed")
	})

	tw(t, f, "drops invalid child-scoped stop fields instead of widening to a run stop", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		p := filepath.Join(stopRequestsDir(dir), "0000000001234-bad-child.json")
		writeFile(t, p, `{"type":"stop","targetIndex":-1,"childId":"bad\nchild"}`)
		equal(t, len(consumeStopRequestPayloads(dir, nil, nil)), 0, "payloads")
		equal(t, ccExists(p), false, "removed")
	})

	tw(t, f, "keeps the request type authoritative even for untyped callers", func(t *testing.T) {
		// The Go request writers take no type: the type of each file is fixed by the function that writes it.
		dir := ccTmpAsyncDir(t)
		ccFixedClock(t, 999)
		p, err := requestAsyncInterrupt(dir, "test")
		if err != nil {
			t.Fatal(err)
		}
		data := ccReadJSON(t, p)
		equal(t, [2]any{data["type"], data["source"]}, [2]any{"interrupt", "test"}, "request")
	})

	tw(t, f, "consumes a pending request exactly once and removes the file", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if _, err := requestAsyncInterrupt(dir, ""); err != nil {
			t.Fatal(err)
		}
		equal(t, consumeInterruptRequest(dir, nil), true, "first")
		equal(t, ccExists(interruptRequestPath(dir)), false, "removed")
		equal(t, consumeInterruptRequest(dir, nil), false, "second")
	})

	tw(t, f, "removes a malformed request directory instead of firing forever", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if err := os.MkdirAll(interruptRequestPath(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		equal(t, consumeInterruptRequest(dir, nil), true, "first")
		equal(t, ccExists(interruptRequestPath(dir)), false, "removed")
		equal(t, consumeInterruptRequest(dir, nil), false, "second")
	})

	tw(t, f, "writes and consumes ordered steer requests", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if _, err := requestAsyncSteer(dir, steerPayload{Message: "  later guidance  ", Mode: steerModeFollowUp, TargetIndex: idx(1), ID: "b", TS: 200, Source: "test"}); err != nil {
			t.Fatal(err)
		}
		if _, err := requestAsyncSteer(dir, steerPayload{Message: "first guidance", TargetIndexes: []int{0, 2}, HasTargetIndexes: true, ID: "a", TS: 100}); err != nil {
			t.Fatal(err)
		}
		equal(t, len(ccListDir(t, steerRequestsDir(dir))), 2, "queued files")
		equal(t, consumeSteerRequests(dir, nil, nil), []steerRequest{
			{Type: "steer", ID: "a", TS: 100, Message: "first guidance", TargetIndexes: []int{0, 2}},
			{Type: "steer", ID: "b", TS: 200, Message: "later guidance", Mode: steerModeFollowUp, TargetIndex: idx(1), Source: "test"},
		}, "consumed")
		equal(t, len(consumeSteerRequests(dir, nil, nil)), 0, "second consume")
	})

	tw(t, f, "rejects requests after the runner closes the steering inbox", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if err := closeSteerInbox(dir, "complete"); err != nil {
			t.Fatal(err)
		}
		equal(t, ccExists(steerInboxClosedPath(dir)), true, "marker")
		_, err := requestAsyncSteer(dir, steerPayload{Message: "too late", TargetIndexes: []int{0}, HasTargetIndexes: true})
		hasMatch(t, err, "no longer accepts steering")
		equal(t, ccExists(steerRequestsDir(dir)), false, "no request directory")
	})

	tw(t, f, "keeps steer request ids out of filesystem paths", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		const id = `../outside\bad:thing`
		p, err := requestAsyncSteer(dir, steerPayload{Message: "safe", ID: id, TS: 1})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, filepath.Dir(p), steerRequestsDir(dir), "directory")
		equal(t, filepath.Base(p), "0000000000001-"+base64URL(id)+".json", "file name")
		equal(t, consumeSteerRequests(dir, nil, nil), []steerRequest{steerReq(id, 1, "safe")}, "consumed")
	})

	tw(t, f, "keeps steer requests for retry when the inbox cannot be scanned", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if _, err := requestAsyncSteer(dir, steerPayload{Message: "retry me", ID: "retry", TS: 1}); err != nil {
			t.Fatal(err)
		}
		fail := true
		fsys := hookFS{readDir: func(d string) ([]string, error) {
			if fail {
				fail = false
				return nil, &os.PathError{Op: "readdir", Path: d, Err: syscall.ENFILE}
			}
			return osControlFS{}.ReadDir(d)
		}}
		equal(t, len(consumeSteerRequests(dir, fsys, nil)), 0, "failed scan")
		equal(t, consumeSteerRequests(dir, fsys, nil), []steerRequest{steerReq("retry", 1, "retry me")}, "retry")
	})

	tw(t, f, "does not deliver a steer request if another consumer removed it first", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if _, err := requestAsyncSteer(dir, steerPayload{Message: "already taken", ID: "s", TS: 1}); err != nil {
			t.Fatal(err)
		}
		fsys := hookFS{remove: func(p string) error {
			_ = osControlFS{}.Remove(p)
			return &os.PathError{Op: "remove", Path: p, Err: os.ErrNotExist}
		}}
		equal(t, len(consumeSteerRequests(dir, fsys, nil)), 0, "first consumer")
		equal(t, len(consumeSteerRequests(dir, nil, nil)), 0, "second consumer")
	})

	tw(t, f, "bounds retained revival briefs and keeps them FIFO", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		var want []string
		for i := range maxSteerQueueSize {
			id := "brief-" + string(rune('a'+i))
			want = append(want, id)
			if _, err := queueRevivalBrief(dir, steerRequest{Type: "steer", ID: id, TS: float64(i + 1), Message: "brief", Mode: steerModeFollowUp}); err != nil {
				t.Fatal(err)
			}
		}
		var got []string
		for _, b := range readRevivalBriefs(dir) {
			got = append(got, b.Request.ID)
		}
		equal(t, got, want, "FIFO")
		_, err := queueRevivalBrief(dir, steerRequest{Type: "steer", ID: "full", TS: 99, Message: "too much", Mode: steerModeFollowUp})
		hasMatch(t, err, "queue is full")
	})

	tw(t, f, "rejects empty steer messages and invalid target indexes", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		for _, c := range []struct {
			p       steerPayload
			pattern string
		}{
			{steerPayload{Message: "   "}, "steer message must not be empty"},
			{steerPayload{Message: "ok", TargetIndex: idx(-1)}, "targetIndex"},
			{steerPayload{Message: "ok", TargetIndex: idx(1_000_001)}, "targetIndex"},
			{steerPayload{Message: "ok", HasTargetIndexes: true}, "targetIndexes"},
			{steerPayload{Message: "ok", TargetIndexes: []int{0, 0}, HasTargetIndexes: true}, "targetIndexes"},
			{steerPayload{Message: "ok", TargetIndex: idx(0), TargetIndexes: []int{0}, HasTargetIndexes: true}, "targetIndexes"},
			{steerPayload{Message: strings.Repeat("x", 128*1024+1)}, "exceeds"},
			{steerPayload{Message: "ok", ID: "contains whitespace"}, "malformed"},
		} {
			_, err := requestAsyncSteer(dir, c.p)
			hasMatch(t, err, c.pattern)
		}
	})

	tw(t, f, "writes the portable request without signaling an unverified pid", func(t *testing.T) {
		// The port has no signal path at all: an interrupt is only the file. Prove no process is probed or signaled.
		dir := ccTmpAsyncDir(t)
		old := asyncKill
		t.Cleanup(func() { asyncKill = old })
		var kills []int
		asyncKill = func(pid, signal int) error { kills = append(kills, pid); return nil }
		if _, err := requestAsyncInterrupt(dir, ""); err != nil {
			t.Fatal(err)
		}
		equal(t, ccExists(interruptRequestPath(dir)), true, "request file")
		equal(t, len(kills), 0, "kills")
	})

	nativeWatch := "Node's fs.watch (a native directory watcher plus a slow safety poll) has no counterpart in Go's standard library and the port takes no file-watcher dependency: it polls the inbox every 250 ms on every platform, which is the original's own branch for Darwin and Windows"
	tskip(t, f, "registers the native canonical control inbox path", nativeWatch)
	tskip(t, f, "does not start fast polling when native watch is available", nativeWatch)
	tskip(t, f, "starts portable polling once when the native watcher fails", nativeWatch)
	tskip(t, f, "delivers later steer files from the watched request directory without polling", nativeWatch)

	watch := func(dir string, o controlWatch) (*manualTimers, func()) {
		m := &manualTimers{}
		o.Timers = m
		return m, watchAsyncControlInbox(dir, o)
	}

	tw(t, f, "uses portable polling without native watchers on Darwin", func(t *testing.T) {
		m, dispose := watch(ccTmpAsyncDir(t), controlWatch{OnInterrupt: func() {}})
		defer dispose()
		equal(t, m.delays, []time.Duration{250 * time.Millisecond}, "intervals")
	})

	tw(t, f, "fires on a request that arrived before the watcher started", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		if _, err := requestAsyncInterrupt(dir, ""); err != nil {
			t.Fatal(err)
		}
		fired := 0
		_, dispose := watch(dir, controlWatch{OnInterrupt: func() { fired++ }})
		defer dispose()
		equal(t, fired, 1, "fired")
		equal(t, ccExists(interruptRequestPath(dir)), false, "removed")
	})

	tw(t, f, "fires once per request via the watch event and stops after dispose", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		fired := 0
		m, dispose := watch(dir, controlWatch{OnInterrupt: func() { fired++ }})
		equal(t, fired, 0, "before")
		requestAsyncInterrupt(dir, "")
		m.tick()
		equal(t, fired, 1, "after the request")
		equal(t, ccExists(interruptRequestPath(dir)), false, "removed")
		m.tick() // no pending request: a spurious event is a no-op
		equal(t, fired, 1, "spurious event")
		dispose()
		equal(t, m.stopped, 1, "polling stopped")
		requestAsyncInterrupt(dir, "")
		m.tick()
		equal(t, fired, 1, "after dispose")
	})

	tw(t, f, "delivers stop requests before interrupt requests", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		requestAsyncStop(dir, "", nil, "")
		requestAsyncInterrupt(dir, "")
		requestAsyncTimeout(dir, "", "")
		requestAsyncSteer(dir, steerPayload{ID: "all-handlers", Message: "guide"})
		var events []string
		m, dispose := watch(dir, controlWatch{
			OnInterrupt: func() { events = append(events, "interrupt") },
			OnStop:      func(controlRequest) { events = append(events, "stop") },
			OnTimeout:   func() { events = append(events, "timeout") },
			OnSteer:     func(steerRequest) { events = append(events, "steer") },
		})
		defer dispose()
		want := []string{"stop", "timeout", "interrupt", "steer"}
		equal(t, events, want, "order")
		m.tick()
		equal(t, events, want, "an all-handler runner consumes each request once")
	})

	tw(t, f, "delivers stop payloads to the watcher", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		requestAsyncStop(dir, "", idx(1), "slow")
		var stops []controlRequest
		_, dispose := watch(dir, controlWatch{OnInterrupt: func() {}, OnStop: func(r controlRequest) { stops = append(stops, r) }})
		defer dispose()
		equal(t, len(stops), 1, "stops")
		equal(t, [2]any{*stops[0].TargetIndex, stops[0].ChildID}, [2]any{1, "slow"}, "payload")
	})

	tw(t, f, "delivers steer requests without firing interrupt", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		interrupted := 0
		var steers []steerRequest
		m, dispose := watch(dir, controlWatch{OnInterrupt: func() { interrupted++ }, OnSteer: func(r steerRequest) { steers = append(steers, r) }})
		defer dispose()
		requestAsyncSteer(dir, steerPayload{Message: "go narrower", TargetIndex: idx(0), ID: "s", TS: 1})
		m.tick()
		equal(t, interrupted, 0, "interrupted")
		equal(t, len(steers), 1, "steers")
		equal(t, [2]any{steers[0].Message, *steers[0].TargetIndex}, [2]any{"go narrower", 0}, "steer")
	})

	tw(t, f, "consumes a steer request that arrived before the watcher started", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		requestAsyncSteer(dir, steerPayload{Message: "queued before install", ID: "s1", TS: 1})
		var steers []string
		_, dispose := watch(dir, controlWatch{OnSteer: func(r steerRequest) { steers = append(steers, r.Message) }})
		defer dispose()
		equal(t, steers, []string{"queued before install"}, "steers")
		equal(t, ccListDir(t, steerRequestsDir(dir)), []string{}, "inbox")
	})

	tw(t, f, "consumes later requests via the watch event and stops after dispose", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		var steers []string
		m, dispose := watch(dir, controlWatch{OnSteer: func(r steerRequest) { steers = append(steers, r.Message) }})
		equal(t, len(steers), 0, "before")
		requestAsyncSteer(dir, steerPayload{Message: "first", ID: "s1", TS: 1})
		m.tick()
		equal(t, steers, []string{"first"}, "after the first")
		m.tick() // a spurious event with nothing queued is a no-op
		equal(t, steers, []string{"first"}, "spurious")
		dispose()
		equal(t, m.stopped, 1, "polling stopped")
		requestAsyncSteer(dir, steerPayload{Message: "after dispose", ID: "s2", TS: 2})
		m.tick()
		equal(t, steers, []string{"first"}, "a disposed watcher must not consume further requests")
	})

	tw(t, f, "watches only the steer directory, so a stop request stays for its own consumer", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		requestAsyncStop(dir, "test", nil, "")
		requestAsyncInterrupt(dir, "")
		requestAsyncTimeout(dir, "", "")
		var steers []string
		m, dispose := watch(dir, controlWatch{OnSteer: func(r steerRequest) { steers = append(steers, r.Message) }})
		defer dispose()
		m.tick()
		equal(t, len(steers), 0, "steers")
		equal(t, len(ccListDir(t, stopRequestsDir(dir))), 1, "a stop request must not be consumed by the steer watcher")
		equal(t, ccExists(interruptRequestPath(dir)), true, "interrupt request")
		equal(t, ccExists(timeoutRequestPath(dir)), true, "timeout request")
	})

	tw(t, f, "keeps watching when a consumer throws", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		var seen, failures []string
		m, dispose := watch(dir, controlWatch{
			OnError: func(_ error, phase string, r *steerRequest) {
				id := ""
				if r != nil {
					id = r.ID
				}
				failures = append(failures, phase+":"+id)
			},
			OnSteer: func(r steerRequest) {
				seen = append(seen, r.Message)
				if r.Message == "boom" {
					panic(errors.New("consumer failed"))
				}
			},
		})
		defer dispose()
		requestAsyncSteer(dir, steerPayload{Message: "boom", ID: "s1", TS: 1})
		requestAsyncSteer(dir, steerPayload{Message: "still delivered", ID: "s2", TS: 2})
		m.tick()
		equal(t, seen, []string{"boom", "still delivered"}, "a throwing consumer must not kill the watcher")
		equal(t, failures, []string{"callback:s1"}, "failures")
	})

	tw(t, f, "retries stop request ${operation} failures without losing readable siblings", func(t *testing.T) {
		for _, operation := range []string{"read", "remove"} {
			t.Run(operation, func(t *testing.T) {
				dir := ccTmpAsyncDir(t)
				denied := &os.PathError{Op: operation, Path: "x", Err: syscall.EACCES}
				ccFixedClock(t, 1)
				pending, _ := requestAsyncStop(dir, "", nil, "retry")
				requestAsyncStop(dir, "", nil, "ready")
				var seen []string
				var failures []error
				failing := true
				fsys := hookFS{
					readFile: func(p string) ([]byte, error) {
						if failing && operation == "read" && p == pending {
							return nil, denied
						}
						return os.ReadFile(p)
					},
					remove: func(p string) error {
						if failing && operation == "remove" && p == pending {
							return denied
						}
						return osControlFS{}.Remove(p)
					},
				}
				m, dispose := watch(dir, controlWatch{
					OnStop: func(r controlRequest) { seen = append(seen, r.ChildID) },
					OnError: func(err error, phase string, _ *steerRequest) {
						equal(t, phase, "scan", "phase")
						failures = append(failures, err)
					},
					FS: fsys,
				})
				defer dispose()
				equal(t, seen, []string{"ready"}, "first scan")
				equal(t, len(failures), 1, "failures")
				equal(t, ccExists(pending), true, "pending request kept")
				failing = false
				m.tick()
				equal(t, seen, []string{"ready", "retry"}, "retry")
				equal(t, ccExists(pending), false, "pending request consumed")
				m.tick()
				equal(t, seen, []string{"ready", "retry"}, "no third delivery")
			})
		}
	})

	tw(t, f, "reports scan/read/install failures, retains retryable files, and distinguishes healthy watch fallback", func(t *testing.T) {
		dir := ccTmpAsyncDir(t)
		var failures, seen []string
		fault := "scan"
		denied := &os.PathError{Op: "x", Path: "x", Err: syscall.EACCES}
		fsys := hookFS{
			mkdir: func(string) error { return denied },
			readDir: func(d string) ([]string, error) {
				if fault == "scan" {
					return nil, denied
				}
				return osControlFS{}.ReadDir(d)
			},
			readFile: func(p string) ([]byte, error) {
				if fault == "read" {
					return nil, denied
				}
				return os.ReadFile(p)
			},
		}
		m, dispose := watch(dir, controlWatch{
			OnSteer: func(r steerRequest) { seen = append(seen, r.ID) },
			OnError: func(_ error, phase string, _ *steerRequest) { failures = append(failures, phase) },
			FS:      fsys,
		})
		defer dispose()
		requestAsyncSteer(dir, steerPayload{ID: "retry", Message: "retry"})
		m.tick()
		equal(t, failures, []string{"install", "scan", "scan"}, "failures")
		equal(t, len(ccListDir(t, steerRequestsDir(dir))), 1, "retained after a scan failure")
		fault = "read"
		m.tick()
		equal(t, failures[len(failures)-1], "scan", "read failure is a scan failure")
		equal(t, len(ccListDir(t, steerRequestsDir(dir))), 1, "retained after a read failure")
		fault = "none"
		m.tick()
		equal(t, seen, []string{"retry"}, "delivered")
		equal(t, ccListDir(t, steerRequestsDir(dir)), []string{}, "inbox")
		equal(t, len(failures), 4, "healthy polling does not report a failure")
	})

	// Only the portable mode applies: the native and fallback modes of the original measure fs.watch handles.
	tw(t, f, "measures bounded empty-inbox ${mode} and cleanup for ${owners} active owners", func(t *testing.T) {
		for _, owners := range []int{1, 8} {
			t.Run("portable/"+string(rune('0'+owners)), func(t *testing.T) {
				root := ccTmpAsyncDir(t)
				var mu sync.Mutex
				scans, handles := 0, 0
				timers := countingTimers{real: realControlTimers{}, mu: &mu, handles: &handles}
				var disposers []func()
				for i := range owners {
					dir := filepath.Join(root, string(rune('a'+i)))
					disposers = append(disposers, watchAsyncControlInbox(dir, controlWatch{OnSteer: func(steerRequest) {}, Timers: timers,
						FS: hookFS{readDir: func(d string) ([]string, error) {
							mu.Lock()
							scans++
							mu.Unlock()
							return osControlFS{}.ReadDir(d)
						}}}))
				}
				time.Sleep(1100 * time.Millisecond)
				mu.Lock()
				s, h := scans, handles
				mu.Unlock()
				if s < owners || s > owners*6 {
					t.Errorf("bounded scans: %d", s)
				}
				equal(t, h, owners, "polling handles")
				for _, dispose := range disposers {
					dispose()
					dispose()
				}
				mu.Lock()
				equal(t, handles, 0, "all polling cleared")
				final := scans
				mu.Unlock()
				time.Sleep(300 * time.Millisecond)
				mu.Lock()
				equal(t, scans, final, "no scans after owner cleanup")
				mu.Unlock()
			})
		}
	})
}

// countingTimers counts the live polling handles of the real timers.
type countingTimers struct {
	real    realControlTimers
	mu      *sync.Mutex
	handles *int
}

func (c countingTimers) Every(d time.Duration, fn func()) func() {
	c.mu.Lock()
	*c.handles++
	c.mu.Unlock()
	stop := c.real.Every(d, fn)
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			c.mu.Lock()
			*c.handles--
			c.mu.Unlock()
		})
	}
}
