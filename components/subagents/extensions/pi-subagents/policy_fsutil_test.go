package pi_subagents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAtomicFS struct {
	files       map[string]string
	madeDirs    []string
	renameCalls int
	failMkdir   []string
	failRename  []string
	writeModes  map[string]string
	failCleanup bool
	writeOrder  []string
}

func newFakeAtomicFS() *fakeAtomicFS {
	return &fakeAtomicFS{files: map[string]string{}, writeModes: map[string]string{}}
}

func shift(list *[]string) string {
	if len(*list) == 0 {
		return ""
	}
	v := (*list)[0]
	*list = (*list)[1:]
	return v
}

func (f *fakeAtomicFS) MkdirAll(dir string) error {
	f.madeDirs = append(f.madeDirs, dir)
	if code := shift(&f.failMkdir); code != "" {
		return newFSError(code, "mkdir failed with "+code)
	}
	return nil
}

func (f *fakeAtomicFS) WriteFile(path string, data []byte, mode os.FileMode, hasMode bool) error {
	f.files[path] = string(data)
	f.writeOrder = append(f.writeOrder, path)
	if hasMode {
		f.writeModes[path] = "utf-8 mode " + mode.String()
	} else {
		f.writeModes[path] = "utf-8"
	}
	return nil
}

func (f *fakeAtomicFS) Rename(from, to string) error {
	f.renameCalls++
	if code := shift(&f.failRename); code != "" {
		return newFSError(code, "rename failed with "+code)
	}
	c, ok := f.files[from]
	if !ok {
		return errors.New("missing source file: " + from)
	}
	delete(f.files, from)
	f.files[to] = c
	return nil
}

func (f *fakeAtomicFS) Remove(path string) error {
	if f.failCleanup {
		return errors.New("cleanup failed")
	}
	delete(f.files, path)
	return nil
}

func boolPtr(b bool) *bool { return &b }

func TestFileSystemRetryBudget(t *testing.T) {
	base := []int{10, 25, 50, 100, 200, 500, 1000, 2000, 4000}
	total := func(d []int) int {
		s := 0
		for _, v := range d {
			s += v
		}
		return s
	}
	resolve := func(env map[string]string) ([]int, error) { return resolveFileSystemRetryDelays(envOf(env), base) }
	tw(t, "file-system-retry", "is unchanged when the environment variable is unset or blank", func(t *testing.T) {
		for _, env := range []map[string]string{{}, {fsRetryMaxTotalMsEnv: ""}, {fsRetryMaxTotalMsEnv: "   "}} {
			d, err := resolve(env)
			equal(t, err, nil, "error")
			equal(t, d, base, "delays")
		}
	})
	tw(t, "file-system-retry", "clamps the total sleep to the configured budget", func(t *testing.T) {
		d, _ := resolve(map[string]string{fsRetryMaxTotalMsEnv: "1000"})
		equal(t, total(d), 1000, "total")
		equal(t, d, []int{10, 25, 50, 100, 200, 500, 115, 0, 0}, "delays")
	})
	tw(t, "file-system-retry", "keeps the ladder length, because callers use it as an attempt budget", func(t *testing.T) {
		for _, b := range []string{"0", "1", "250", "100000"} {
			d, _ := resolve(map[string]string{fsRetryMaxTotalMsEnv: b})
			equal(t, len(d), len(base), "length for "+b)
			for _, v := range d {
				if v < 0 {
					t.Errorf("negative delay %d", v)
				}
			}
		}
	})
	tw(t, "file-system-retry", "never exceeds the base ladder, even for a huge budget", func(t *testing.T) {
		d, _ := resolve(map[string]string{fsRetryMaxTotalMsEnv: "99999999"})
		equal(t, d, base, "delays")
	})
	tw(t, "file-system-retry", "supports a zero budget as retry-without-sleeping", func(t *testing.T) {
		d, _ := resolve(map[string]string{fsRetryMaxTotalMsEnv: "0"})
		equal(t, total(d), 0, "total")
		attempts := 0
		var slept []int
		err := runFileSystemOperationWithRetry(func() error { attempts++; return newFSError("EPERM", "EPERM") }, d, func(ms int) { slept = append(slept, ms) })
		if err == nil || !strings.Contains(err.Error(), "EPERM") {
			t.Fatal(err)
		}
		equal(t, attempts, len(base)+1, "all attempts still run")
		for _, s := range slept {
			equal(t, s, 0, "no non-zero sleep is requested")
		}
	})
	tw(t, "file-system-retry", "rejects values that are not a non-negative integer", func(t *testing.T) {
		for _, v := range []string{"-1", "abc", "1.5", "NaN", "1e3ms"} {
			_, err := resolve(map[string]string{fsRetryMaxTotalMsEnv: v})
			if err == nil || !strings.Contains(err.Error(), fsRetryMaxTotalMsEnv) {
				t.Errorf("%s: %v", v, err)
			}
		}
	})
}

func TestWriteAtomicJSON(t *testing.T) {
	target := filepath.Join("/tmp", "status.json")
	payload := newObject()
	payload.set("state", "running")
	want := "{\n  \"state\": \"running\"\n}"
	writer := func(fk *fakeAtomicFS, waits *[]int, o atomicJSONOptions) func(string, any) error {
		o.FS, o.Now, o.PID, o.Random, o.Wait = fk, func() int64 { return 12345 }, 678, func() float64 { return 0.5 }, func(ms int) { *waits = append(*waits, ms) }
		return createAtomicJSONWriter(o)
	}
	std := atomicJSONOptions{RetryRenameErrors: boolPtr(true), RetryDelaysMs: []int{1, 2, 3}, HasRetryDelays: true}
	tw(t, "atomic-json", "retries transient rename failures before replacing the target", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failRename = []string{"EPERM", "EBUSY"}
		var waits []int
		if err := writer(fk, &waits, std)(target, payload); err != nil {
			t.Fatal(err)
		}
		equal(t, fk.renameCalls, 3, "renames")
		equal(t, waits, []int{1, 2}, "waits")
		equal(t, fk.madeDirs, []string{filepath.Dir(target)}, "dirs")
		equal(t, fk.files[target], want, "contents")
		equal(t, len(fk.files), 1, "files")
	})
	tw(t, "atomic-json", "retries transient directory creation failures before writing", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failMkdir = []string{"EPERM", "EACCES"}
		var waits []int
		if err := writer(fk, &waits, std)(target, payload); err != nil {
			t.Fatal(err)
		}
		equal(t, fk.renameCalls, 1, "renames")
		equal(t, waits, []int{1, 2}, "waits")
		equal(t, len(fk.madeDirs), 3, "mkdir calls")
		equal(t, fk.files[target], want, "contents")
	})
	tw(t, "atomic-json", "writes the temporary descriptor with the requested private mode", func(t *testing.T) {
		fk := newFakeAtomicFS()
		var waits []int
		mode := os.FileMode(0o600)
		if err := writer(fk, &waits, atomicJSONOptions{Mode: &mode})(filepath.Join("/tmp", "recovery-descriptor.json"), payload); err != nil {
			t.Fatal(err)
		}
		equal(t, len(fk.writeModes), 1, "writes")
		for _, m := range fk.writeModes {
			equal(t, m, "utf-8 mode -rw-------", "mode")
		}
	})
	tw(t, "atomic-json", "keeps temporary names below the component limit for long target names", func(t *testing.T) {
		fk := newFakeAtomicFS()
		var waits []int
		long := filepath.Join("/tmp", strings.Repeat("x", 250)+".json")
		if err := writer(fk, &waits, std)(long, payload); err != nil {
			t.Fatal(err)
		}
		if len(fk.writeOrder) != 1 || len(filepath.Base(fk.writeOrder[0])) > 255 {
			t.Fatalf("temp names: %v", fk.writeOrder)
		}
		equal(t, fk.files[long], want, "contents")
	})
	tw(t, "atomic-json", "uses longer default retries for transient Windows rename locks", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failRename = []string{"EPERM", "EPERM", "EPERM", "EPERM", "EPERM", "EPERM"}
		var waits []int
		if err := writer(fk, &waits, atomicJSONOptions{RetryRenameErrors: boolPtr(true)})(target, payload); err != nil {
			t.Fatal(err)
		}
		equal(t, fk.renameCalls, 7, "renames")
		equal(t, waits, []int{10, 25, 50, 100, 200, 500}, "waits")
	})
	tw(t, "atomic-json", "throws non-retryable rename failures without retrying", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failRename = []string{"ENOENT"}
		var waits []int
		err := writer(fk, &waits, std)(target, payload)
		if err == nil || !strings.Contains(err.Error(), "ENOENT") {
			t.Fatal(err)
		}
		equal(t, fk.renameCalls, 1, "renames")
		equal(t, len(waits), 0, "waits")
		equal(t, len(fk.files), 0, "files")
	})
	tw(t, "atomic-json", "does not let cleanup failures mask a write failure", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failRename = []string{"ENOSPC"}
		fk.failCleanup = true
		var waits []int
		err := writer(fk, &waits, std)(target, payload)
		if err == nil || !strings.Contains(err.Error(), "ENOSPC") {
			t.Fatal(err)
		}
	})
	tw(t, "atomic-json", "can ignore cleanup failures after the target is atomically published", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failCleanup = true
		var waits []int
		p := newObject()
		p.set("action", "proceed")
		p.set("token", "runner")
		if err := writer(fk, &waits, atomicJSONOptions{IgnoreCleanupErrorAfterSuccess: true})(filepath.Join("/tmp", "runner-startup-proceed.json"), p); err != nil {
			t.Fatal(err)
		}
		equal(t, fk.files[filepath.Join("/tmp", "runner-startup-proceed.json")], "{\n  \"action\": \"proceed\",\n  \"token\": \"runner\"\n}", "contents")
	})
	tw(t, "atomic-json", "cleans up the temp file after retryable failures are exhausted", func(t *testing.T) {
		fk := newFakeAtomicFS()
		fk.failRename = []string{"EPERM", "EPERM", "EPERM", "EPERM"}
		var waits []int
		err := writer(fk, &waits, std)(target, payload)
		if err == nil || !strings.Contains(err.Error(), "EPERM") {
			t.Fatal(err)
		}
		equal(t, fk.renameCalls, 4, "renames")
		equal(t, waits, []int{1, 2, 3}, "waits")
		_, has := fk.files[target]
		equal(t, has, false, "target written")
		equal(t, len(fk.files), 0, "files")
	})
}

type fakeTimers struct {
	tasks []*fakeTimer
}
type fakeTimer struct {
	cb      func()
	delayMs int
}

func (f *fakeTimers) SetTimeout(h func(), d int) any {
	t := &fakeTimer{h, d}
	f.tasks = append(f.tasks, t)
	return t
}
func (f *fakeTimers) ClearTimeout(h any) {
	for i, t := range f.tasks {
		if any(t) == h {
			f.tasks = append(f.tasks[:i], f.tasks[i+1:]...)
			return
		}
	}
}
func (f *fakeTimers) runNext() {
	if len(f.tasks) == 0 {
		return
	}
	t := f.tasks[0]
	f.tasks = f.tasks[1:]
	t.cb()
}
func (f *fakeTimers) runAll() {
	batch := f.tasks
	f.tasks = nil
	for _, t := range batch {
		t.cb()
	}
}

func TestCapacityResilientJSONWriter(t *testing.T) {
	cap := func(code string) error { return newFSError(code, code) }
	state := func(s string) any { o := newObject(); o.set("state", s); return o }
	tw(t, "capacity-resilient-json", "retains only the latest payload and retries after capacity recovers", func(t *testing.T) {
		timers := &fakeTimers{}
		var writes []any
		available := false
		w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, RetryDelayMs: 25, HasRetryMs: true, Write: func(_ string, p any) error {
			if !available {
				return cap("ENOSPC")
			}
			writes = append(writes, p)
			return nil
		}})
		w.Write("status.json", state("running"), nil)
		w.Write("status.json", state("complete"), nil)
		equal(t, w.PendingCount(), 1, "pending")
		equal(t, len(timers.tasks), 1, "timers")
		available = true
		timers.runNext()
		equal(t, marshalJSON(writes[0], ""), `{"state":"complete"}`, "written")
		equal(t, len(writes), 1, "writes")
		equal(t, w.PendingCount(), 0, "pending after")
		equal(t, len(timers.tasks), 0, "timers after")
	})
	tw(t, "capacity-resilient-json", "handles every storage-capacity code without escaping the retry timer", func(t *testing.T) {
		for _, code := range []string{"ENOSPC", "EDQUOT", "EMFILE", "ENFILE"} {
			timers := &fakeTimers{}
			attempts := 0
			available := false
			w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, Write: func(string, any) error {
				attempts++
				if !available {
					return cap(code)
				}
				return nil
			}})
			w.Write("status.json", state("running"), nil)
			timers.runNext()
			equal(t, attempts, 2, code+" attempts")
			available = true
			timers.runNext()
			equal(t, w.PendingCount(), 0, code+" pending")
		}
	})
	tw(t, "capacity-resilient-json", "preserves synchronous throwing for non-capacity failures", func(t *testing.T) {
		w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Write: func(string, any) error { return errors.New("invalid path") }})
		err := w.Write("status.json", newObject(), nil)
		if err == nil || !strings.Contains(err.Error(), "invalid path") {
			t.Fatal(err)
		}
	})
	tw(t, "capacity-resilient-json", "does not let error callbacks escape a retry timer", func(t *testing.T) {
		timers := &fakeTimers{}
		w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, OnError: func(error, string) { panic("reporting failed") }, Write: func(p string, _ any) error {
			if p == "status.json" {
				return cap("ENOSPC")
			}
			return nil
		}})
		w.Write("status.json", state("running"), nil)
		timers.runNext()
		equal(t, w.PendingCount(), 1, "pending")
		w.Dispose()
	})
	tw(t, "capacity-resilient-json", "notifies only after a write succeeds, including a deferred retry", func(t *testing.T) {
		timers := &fakeTimers{}
		var ok []string
		available := false
		w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, OnSuccess: func(_ string, p any) { ok = append(ok, marshalJSON(p, "")) }, Write: func(string, any) error {
			if !available {
				return cap("ENOSPC")
			}
			return nil
		}})
		w.Write("status.json", state("running"), nil)
		equal(t, len(ok), 0, "before")
		available = true
		timers.runNext()
		equal(t, ok, []string{`{"state":"running"}`}, "after")
	})
	tw(t, "capacity-resilient-json", "orders deferred index work after authoritative status recovery", func(t *testing.T) {
		timers := &fakeTimers{}
		statusAvailable := false
		indexUpdates := 0
		index := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, Write: func(string, any) error { indexUpdates++; return nil }})
		status := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, OnSuccess: func(string, any) { index.Write("run", state("complete"), nil) }, Write: func(string, any) error {
			if !statusAvailable {
				return cap("ENOSPC")
			}
			return nil
		}})
		status.Write("status", state("complete"), nil)
		equal(t, indexUpdates, 0, "before")
		statusAvailable = true
		timers.runNext()
		equal(t, indexUpdates, 1, "after")
		status.Dispose()
		index.Dispose()
	})
	tw(t, "capacity-resilient-json", "reports non-capacity failures from a retry without throwing from the timer", func(t *testing.T) {
		timers := &fakeTimers{}
		var errs []error
		attempts := 0
		w := newCapacityResilientJSONWriter(capacityResilientJSONOptions{Timers: timers, OnError: func(e error, _ string) { errs = append(errs, e) }, Write: func(string, any) error {
			attempts++
			if attempts == 1 {
				return cap("ENOSPC")
			}
			return errors.New("permission denied")
		}})
		if err := w.Write("status.json", state("running"), nil); err != nil {
			t.Fatal(err)
		}
		timers.runNext()
		equal(t, len(errs), 1, "errors")
		polMatches(t, errs[0].Error(), `permission denied`)
	})
}

func TestFileCoalescer(t *testing.T) {
	tw(t, "file-coalescer", "coalesces duplicate schedule calls per file", func(t *testing.T) {
		var events []string
		timers := &fakeTimers{}
		c := createFileCoalescer(func(file string) { events = append(events, file) }, 50, timers)
		equal(t, c.Schedule("a.json", -1), true, "first")
		equal(t, c.Schedule("a.json", -1), false, "second")
		equal(t, len(timers.tasks), 1, "timers")
		timers.runAll()
		equal(t, events, []string{"a.json"}, "events")
		equal(t, c.Schedule("a.json", -1), true, "again")
	})
	tw(t, "file-coalescer", "allows different files to schedule independently", func(t *testing.T) {
		var events []string
		timers := &fakeTimers{}
		c := createFileCoalescer(func(file string) { events = append(events, file) }, 50, timers)
		c.Schedule("a.json", -1)
		c.Schedule("b.json", -1)
		equal(t, len(timers.tasks), 2, "timers")
		timers.runAll()
		equal(t, events, []string{"a.json", "b.json"}, "events")
	})
	tw(t, "file-coalescer", "flushes a pending file immediately without a duplicate timer write", func(t *testing.T) {
		var events []string
		timers := &fakeTimers{}
		c := createFileCoalescer(func(file string) { events = append(events, file) }, 50, timers)
		c.Schedule("status.json", -1)
		c.Schedule("status.json", -1)
		equal(t, len(timers.tasks), 1, "timers")
		equal(t, c.Flush("status.json"), true, "flush")
		equal(t, events, []string{"status.json"}, "events")
		equal(t, len(timers.tasks), 0, "timers after")
		timers.runAll()
		equal(t, events, []string{"status.json"}, "events after run")
		equal(t, c.Flush("status.json"), false, "flush again")
	})
	tw(t, "file-coalescer", "clear cancels all pending handlers", func(t *testing.T) {
		var events []string
		timers := &fakeTimers{}
		c := createFileCoalescer(func(file string) { events = append(events, file) }, 50, timers)
		c.Schedule("a.json", -1)
		c.Schedule("b.json", -1)
		equal(t, len(timers.tasks), 2, "timers")
		c.Clear()
		equal(t, len(timers.tasks), 0, "timers after")
		timers.runAll()
		equal(t, len(events), 0, "events")
	})
}

type mockSource struct{ paused, resumed int }

func (m *mockSource) Pause()  { m.paused++ }
func (m *mockSource) Resume() { m.resumed++ }

type mockStream struct {
	writes       []string
	ended        bool
	drainHandler func()
	results      []bool
}

func (m *mockStream) Write(chunk string) bool {
	m.writes = append(m.writes, chunk)
	if len(m.results) == 0 {
		return true
	}
	r := m.results[0]
	m.results = m.results[1:]
	return r
}
func (m *mockStream) OnceDrain(l func())  { m.drainHandler = l }
func (m *mockStream) OnError(func(error)) {}
func (m *mockStream) End(cb func())       { m.ended = true; cb() }
func (m *mockStream) emitDrain() {
	if m.drainHandler != nil {
		m.drainHandler()
	}
}

func TestJSONLWriter(t *testing.T) {
	deps := func(s *mockStream) jsonlWriterDeps {
		return jsonlWriterDeps{CreateWriteStream: func(string) (jsonlWriteStream, error) { return s, nil }}
	}
	tw(t, "jsonl-writer", "writes lines with trailing newline", func(t *testing.T) {
		src, s := &mockSource{}, &mockStream{}
		w := createJSONLWriter("/tmp/out.jsonl", src, deps(s))
		w.WriteLine(`{"type":"a"}`)
		w.WriteLine(`{"type":"b"}`)
		equal(t, s.writes, []string{"{\"type\":\"a\"}\n", "{\"type\":\"b\"}\n"}, "writes")
	})
	tw(t, "jsonl-writer", "pauses on backpressure and resumes on drain", func(t *testing.T) {
		src, s := &mockSource{}, &mockStream{results: []bool{false, true}}
		w := createJSONLWriter("/tmp/out.jsonl", src, deps(s))
		w.WriteLine(`{"type":"a"}`)
		equal(t, src.paused, 1, "paused")
		equal(t, src.resumed, 0, "resumed")
		s.emitDrain()
		equal(t, src.resumed, 1, "resumed after drain")
		w.WriteLine(`{"type":"b"}`)
		equal(t, s.writes, []string{"{\"type\":\"a\"}\n", "{\"type\":\"b\"}\n"}, "writes")
	})
	tw(t, "jsonl-writer", "closes stream once", func(t *testing.T) {
		src, s := &mockSource{}, &mockStream{}
		w := createJSONLWriter("/tmp/out.jsonl", src, deps(s))
		w.Close()
		equal(t, s.ended, true, "ended")
		w.Close()
		equal(t, s.ended, true, "ended again")
	})
	tw(t, "jsonl-writer", "returns no-op writer when file path is undefined", func(t *testing.T) {
		src := &mockSource{}
		w := createJSONLWriter("", src, jsonlWriterDeps{})
		w.WriteLine(`{"type":"a"}`)
		w.Close()
		equal(t, [2]int{src.paused, src.resumed}, [2]int{0, 0}, "source")
	})
	tw(t, "jsonl-writer", "stops writing when maxBytes exceeded without pausing source", func(t *testing.T) {
		src, s := &mockSource{}, &mockStream{}
		d := deps(s)
		d.MaxBytes, d.HasMaxBytes = 30, true
		w := createJSONLWriter("/tmp/out.jsonl", src, d)
		w.WriteLine(`{"type":"a"}`)
		w.WriteLine(`{"type":"b"}`)
		w.WriteLine(`{"type":"c"}`)
		equal(t, s.writes, []string{"{\"type\":\"a\"}\n", "{\"type\":\"b\"}\n"}, "writes")
		equal(t, src.paused, 0, "paused")
	})
	tw(t, "jsonl-writer", "allows writes up to exactly maxBytes", func(t *testing.T) {
		src, s := &mockSource{}, &mockStream{}
		line := `{"x":"a"}`
		d := deps(s)
		d.MaxBytes, d.HasMaxBytes = (len(line)+1)*2, true
		w := createJSONLWriter("/tmp/out.jsonl", src, d)
		w.WriteLine(line)
		w.WriteLine(line)
		w.WriteLine(line)
		equal(t, len(s.writes), 2, "writes")
	})
}

type fakeAccessFS struct {
	mkdirCalls, accessCalls int
	failMkdir, failAccess   []string
	created                 []string
	blockedMkdir, blocked   map[string]bool
}

func (f *fakeAccessFS) MkdirAll(dir string) error {
	f.mkdirCalls++
	if code := shift(&f.failMkdir); code != "" {
		return newFSError(code, "mkdir failed with "+code)
	}
	if f.blockedMkdir[dir] {
		return newFSError("EPERM", "mkdir blocked")
	}
	f.created = append(f.created, dir)
	return nil
}
func (f *fakeAccessFS) Access(dir string) error {
	f.accessCalls++
	if code := shift(&f.failAccess); code != "" {
		return newFSError(code, "access failed with "+code)
	}
	if f.blocked[dir] {
		return newFSError("EPERM", "access blocked")
	}
	return nil
}

func TestEnsureAccessibleDirRetry(t *testing.T) {
	opts := func(fk *fakeAccessFS, waits *[]int) accessibleDirOptions {
		return accessibleDirOptions{FS: fk, RetryDirectoryErrors: boolPtr(true), RetryDelaysMs: []int{1, 2, 3}, HasRetryDelays: true, Wait: func(ms int) { *waits = append(*waits, ms) }}
	}
	dir := "/tmp/pi-subagents-user/async-subagent-results"
	tw(t, "accessible-dir", "retries transient shared-directory mkdir failures before validating access", func(t *testing.T) {
		fk := &fakeAccessFS{failMkdir: []string{"EPERM", "EBUSY"}}
		var waits []int
		if _, err := ensureAccessibleDir(dir, opts(fk, &waits)); err != nil {
			t.Fatal(err)
		}
		equal(t, [2]int{fk.mkdirCalls, fk.accessCalls}, [2]int{3, 1}, "calls")
		equal(t, waits, []int{1, 2}, "waits")
	})
	tw(t, "accessible-dir", "retries transient shared-directory access failures without recreating the directory", func(t *testing.T) {
		fk := &fakeAccessFS{failAccess: []string{"EPERM", "EACCES"}}
		var waits []int
		if _, err := ensureAccessibleDir(dir, opts(fk, &waits)); err != nil {
			t.Fatal(err)
		}
		equal(t, [2]int{fk.mkdirCalls, fk.accessCalls}, [2]int{1, 3}, "calls")
		equal(t, waits, []int{1, 2}, "waits")
	})
	tw(t, "accessible-dir", "throws non-retryable access failures without retrying", func(t *testing.T) {
		fk := &fakeAccessFS{failAccess: []string{"ENOENT"}}
		var waits []int
		_, err := ensureAccessibleDir(dir, opts(fk, &waits))
		if err == nil || !strings.Contains(err.Error(), "ENOENT") {
			t.Fatal(err)
		}
		equal(t, [2]int{fk.mkdirCalls, fk.accessCalls}, [2]int{1, 1}, "calls")
		equal(t, len(waits), 0, "waits")
	})
}

func TestEnsureAccessibleDirFallback(t *testing.T) {
	opts := func(fk accessibleDirFS) accessibleDirOptions {
		return accessibleDirOptions{FS: fk, PID: 12345, RetryDirectoryErrors: boolPtr(true), RetryDelaysMs: []int{}, HasRetryDelays: true, Wait: func(int) {}}
	}
	tw(t, "ensure-accessible-dir", "returns the requested path when it is usable", func(t *testing.T) {
		fk := &fakeAccessFS{}
		dir := "/tmp/pi-subagents-ok/results"
		got, _ := ensureAccessibleDir(dir, opts(fk))
		equal(t, got, dir, "result")
		equal(t, fk.created, []string{dir}, "created")
	})
	tw(t, "ensure-accessible-dir", "falls back to a pid-scoped sibling when mkdir is persistently blocked", func(t *testing.T) {
		dir := "/tmp/pi-subagents-mkdir-eperm/results"
		fk := &fakeAccessFS{blockedMkdir: map[string]bool{dir: true}}
		got, _ := ensureAccessibleDir(dir, opts(fk))
		equal(t, got, dir+"-12345", "result")
		equal(t, fk.created, []string{dir + "-12345"}, "created")
	})
	tw(t, "ensure-accessible-dir", "falls back to a pid-scoped sibling when access is persistently blocked", func(t *testing.T) {
		dir := "/tmp/pi-subagents-access-eperm/results"
		fk := &fakeAccessFS{blocked: map[string]bool{dir: true}}
		got, _ := ensureAccessibleDir(dir, opts(fk))
		equal(t, got, dir+"-12345", "result")
		equal(t, fk.created, []string{dir, dir + "-12345"}, "created")
	})
	tw(t, "ensure-accessible-dir", "does not delete existing primary directory contents when falling back", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, "async-subagent-runs")
		status := filepath.Join(dir, "run-1", "status.json")
		writeFile(t, status, "{}")
		got, err := ensureAccessibleDir(dir, opts(blockingFS{dir}))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got, dir+"-12345", "result")
		b, _ := os.ReadFile(status)
		equal(t, string(b), "{}", "status kept")
	})
	tw(t, "ensure-accessible-dir", "allows runtime reassignment for live async and results paths", func(t *testing.T) {
		origResults, origAsync := DIRS.Results, DIRS.Async
		defer func() { DIRS.Results, DIRS.Async = origResults, origAsync }()
		DIRS.Results, DIRS.Async = "/tmp/fallback-results", "/tmp/fallback-async"
		equal(t, DIRS.Results, "/tmp/fallback-results", "results")
		equal(t, DIRS.Async, "/tmp/fallback-async", "async")
	})
}

// blockingFS is the real file system except that access to one directory is refused (EPERM).
type blockingFS struct{ blocked string }

func (blockingFS) MkdirAll(d string) error { return osAccessibleFS{}.MkdirAll(d) }
func (b blockingFS) Access(d string) error {
	if d == b.blocked {
		return newFSError("EPERM", "access blocked")
	}
	return osAccessibleFS{}.Access(d)
}
