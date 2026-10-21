package pi_background_tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Test seams: the refusal text and the embedded tool definitions.
func Refusal(feature string) string { return refusal(feature) }
func ToolDefsJSON() []byte          { return tooldefsJSON }

// ResetSettings drops the settings store's memo, as a new process would start with.
func ResetSettings() { settings.clear() }

// Rig is a runtime whose timers and log appends the test drives by hand, so the windows of the original's write path
// (settle, log flush, UI refresh, persist, stall) fire exactly when the test says.
type Rig struct {
	timers *fakeTimers
	rt     *bgRuntime
	mu     sync.Mutex
	hold   bool
	held   []heldAppend

	wakeMu   sync.Mutex
	wakePath string
	wakeLogs []string
}

type heldAppend struct {
	file, text string
	done       func(error)
}

// NewRig builds the runtime and swaps the process-wide log writer for one on the rig's timers and held appends; restore
// puts the old one back.
func NewRig(t testing.TB) *Rig {
	r := &Rig{timers: &fakeTimers{}}
	r.rt = newRuntime(r.timers)
	r.rt.logs = newLogWriter(logWriterDeps{append: r.append, timers: r.timers})
	return r
}

func (r *Rig) append(file, text string, done func(error)) {
	r.mu.Lock()
	if r.hold {
		r.held = append(r.held, heldAppend{file, text, done})
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	appendFileAsync(file, text, done)
}

// Extension is the port on the rig's runtime.
func (r *Rig) Extension() *sdk.Extension { return extensionFor(sdk.New(extensionID), r.rt) }

// HoldAppends makes every later log append wait until ReleaseAppends.
func (r *Rig) HoldAppends() { r.mu.Lock(); r.hold = true; r.mu.Unlock() }

// HeldAppends is how many appends wait.
func (r *Rig) HeldAppends() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.held) }

// ReleaseAppends performs the held appends for real and stops holding.
func (r *Rig) ReleaseAppends() {
	r.mu.Lock()
	held := r.held
	r.held, r.hold = nil, false
	r.mu.Unlock()
	for _, h := range held {
		appendFileAsync(h.file, h.text, h.done)
	}
}

// TimerSets counts the timers ever set, by "kind:ms".
func (r *Rig) TimerSets() map[string]int {
	r.timers.mu.Lock()
	defer r.timers.mu.Unlock()
	out := map[string]int{}
	for _, h := range r.timers.handles {
		out[fmt.Sprintf("%s:%d", h.kind, int(h.d/time.Millisecond))]++
	}
	return out
}

func (r *Rig) matching(kind string, ms int) []int {
	r.timers.mu.Lock()
	defer r.timers.mu.Unlock()
	var idx []int
	for i, h := range r.timers.handles {
		if h.kind == kind && int(h.d/time.Millisecond) == ms && !h.stopped && !h.fired {
			idx = append(idx, i)
		}
	}
	return idx
}

// ActiveTimeouts is how many timeouts of ms are armed.
func (r *Rig) ActiveTimeouts(ms int) int { return len(r.matching("timeout", ms)) }

// ActiveTimers lists the armed timers as "kind:ms".
func (r *Rig) ActiveTimers() []string {
	r.timers.mu.Lock()
	defer r.timers.mu.Unlock()
	out := []string{}
	for _, h := range r.timers.handles {
		if !h.stopped && !h.fired {
			out = append(out, fmt.Sprintf("%s:%d", h.kind, int(h.d/time.Millisecond)))
		}
	}
	return out
}

// FireTimeout fires the one armed timeout of ms; any other count fails the test.
func (r *Rig) FireTimeout(t testing.TB, ms int) {
	t.Helper()
	idx := r.matching("timeout", ms)
	if len(idx) != 1 {
		t.Fatalf("timeout %dms: %d armed, want 1", ms, len(idx))
	}
	r.timers.fire(idx[0])
}

// FireInterval fires the one interval of ms.
func (r *Rig) FireInterval(t testing.TB, ms int) {
	t.Helper()
	idx := r.matching("interval", ms)
	if len(idx) != 1 {
		t.Fatalf("interval %dms: %d armed, want 1", ms, len(idx))
	}
	r.timers.fire(idx[0])
}

// Settle waits until the runtime's queued callbacks and host calls have run.
func (r *Rig) Settle() { r.rt.loop.barrier(); r.rt.settle() }

// Paused reports whether the task's output is held back by its log writer.
func (r *Rig) Paused(id string) bool {
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	t := r.rt.tasks.get(id)
	return t != nil && t.gate != nil
}

// Task is a copy of the task's snapshot, or false.
func (r *Rig) Task(id string) (map[string]any, bool) {
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	t := r.rt.tasks.get(id)
	if t == nil {
		return nil, false
	}
	b, _ := json.Marshal(t.snapshot)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m, true
}

// DrainLogs waits for every pending log write.
func (r *Rig) DrainLogs() { r.rt.logs.drain().wait() }

// Constants of the log writer.
const (
	LogWriteNowBytes   = logWriteNowBytes
	LogMaxPendingBytes = logMaxPendingByte
	LogWriteStallMs    = int(logWriteStall / time.Millisecond)
)

func StalledLogMarker(bytes int) string { return stalledLogMarker(bytes) }

// HoldIdentityReads makes spawn-time and restore identity reads block until released; it returns the release function and a
// reader of the highest number of reads in flight at once and of the reads started.
func HoldIdentityReads(t testing.TB) (release func(), stats func() (maxInFlight, started int)) {
	saved := readProcessIdentity
	gate := make(chan struct{})
	var mu sync.Mutex
	inFlight, maxIn, total := 0, 0, 0
	var once sync.Once
	readProcessIdentity = func(pid int) identityReading {
		mu.Lock()
		inFlight++
		total++
		maxIn = max(maxIn, inFlight)
		mu.Unlock()
		<-gate
		mu.Lock()
		inFlight--
		mu.Unlock()
		return identityReading{kind: "unknown", reason: "held"}
	}
	t.Cleanup(func() { once.Do(func() { close(gate) }); readProcessIdentity = saved })
	return func() { once.Do(func() { close(gate) }) }, func() (int, int) { mu.Lock(); defer mu.Unlock(); return maxIn, total }
}

const MaxFinishedTasks = maxFinishedTasks

// CountSignals records every signal the runtime sends to a task process: mode "pass" sends it, "swallow" does not, and "gone"
// answers that the process does not exist.
func CountSignals(t testing.TB, mode string) func() []string {
	saved := sendKill
	var mu sync.Mutex
	var got []string
	sendKill = func(pid int, sig string, group bool) error {
		mu.Lock()
		got = append(got, sig)
		mu.Unlock()
		switch mode {
		case "pass":
			return saved(pid, sig, group)
		case "gone":
			return errGone
		}
		return nil
	}
	t.Cleanup(func() { sendKill = saved })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }
}

// ProbeHooks answers the restore-time probes: identity(pid) is the start token and command of a live process (ok false: gone),
// unit(name) whether a transient unit is active. Both are called from the restore's worker goroutines.
func ProbeHooks(t testing.TB, identity func(pid int) (token, comm string, ok bool), unit func(name string) bool) {
	savedIdent, savedUnit := readProcessIdentity, systemdUnitActive
	readProcessIdentity = func(pid int) identityReading {
		token, comm, ok := identity(pid)
		if !ok {
			return identityReading{kind: "gone"}
		}
		return identityReading{kind: "identity", identity: processIdentity{Pid: pid, StartToken: token, Comm: comm}}
	}
	systemdUnitActive = func(name string) tri {
		if unit(name) {
			return triTrue
		}
		return triFalse
	}
	t.Cleanup(func() { readProcessIdentity, systemdUnitActive = savedIdent, savedUnit })
}

// HookSystemctl answers the unit stops and kills: fails(args) says whether the call exits 1 (stderr "fixture_systemctl.exit=1").
// It returns the calls as "systemctl <args>".
func HookSystemctl(t testing.TB, fails func(args []string) bool) func() []string {
	saved := systemctlRunner
	var mu sync.Mutex
	var calls []string
	systemctlRunner = func(command string, args []string) stopRunResult {
		mu.Lock()
		calls = append(calls, command+" "+strings.Join(args, " "))
		mu.Unlock()
		if fails(args) {
			return stopRunResult{status: new(1), stderr: "fixture_systemctl.exit=1", hasStderr: true}
		}
		return stopRunResult{status: new(0), hasStderr: true}
	}
	t.Cleanup(func() { systemctlRunner = saved })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, calls...) }
}

// SetResourceControl makes a running task a systemd-run one under the unit, as spawn records it.
func (r *Rig) SetResourceControl(id, unit string) {
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	t := r.rt.tasks.get(id)
	t.ResourceControl = &resourceControlMeta{Mode: "systemd-run", RequestedMode: "auto", UnitName: unit}
	rememberSnapshot(t)
}

// TimerEvents is every timer set and clear so far, in order.
func (r *Rig) TimerEvents() []string {
	r.timers.mu.Lock()
	defer r.timers.mu.Unlock()
	return append([]string{}, r.timers.events...)
}

const WakeManifestFieldMaxChars = wakeManifestFieldMaxChars

// SidecarStatePath is where a session's task state is mirrored.
func SidecarStatePath(sessionID string) string { return sidecarStatePath(sessionID) }

// LogOpens counts and holds the log opens of the task output reader.
type LogOpens struct {
	mu        sync.Mutex
	requested []string
	passed    int
	held      int
	active    int
	maxActive int
	tokens    chan struct{}
	open      bool
	closed    chan struct{}
}

type countedFile struct {
	readableFile
	o    *LogOpens
	once sync.Once
}

func (c *countedFile) Close() error {
	c.once.Do(func() { c.o.mu.Lock(); c.o.active--; c.o.mu.Unlock() })
	return c.readableFile.Close()
}

// HoldLogOpens makes every log open of the rig's reader wait for ReleaseLogOpens.
func (r *Rig) HoldLogOpens() *LogOpens {
	o := &LogOpens{tokens: make(chan struct{}, 1<<16), closed: make(chan struct{})}
	r.rt.reader.open = func(path string) (readableFile, error) {
		o.mu.Lock()
		o.requested = append(o.requested, path)
		o.held++
		o.active++
		o.maxActive = max(o.maxActive, o.active)
		o.mu.Unlock()
		select {
		case <-o.tokens:
		case <-o.closed:
		}
		o.mu.Lock()
		o.held--
		o.passed++
		o.mu.Unlock()
		f, err := os.Open(path)
		if err != nil {
			o.mu.Lock()
			o.active--
			o.mu.Unlock()
			return nil, err
		}
		return &countedFile{readableFile: f, o: o}, nil
	}
	return o
}

// Release lets n held opens proceed; n < 0 lets every open proceed from now on.
func (o *LogOpens) Release(n int) {
	if n < 0 {
		close(o.closed)
		return
	}
	for i := 0; i < n; i++ {
		o.tokens <- struct{}{}
	}
}

func (o *LogOpens) Requested() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string{}, o.requested...)
}
func (o *LogOpens) Held() int      { o.mu.Lock(); defer o.mu.Unlock(); return o.held }
func (o *LogOpens) Passed() int    { o.mu.Lock(); defer o.mu.Unlock(); return o.passed }
func (o *LogOpens) Active() int    { o.mu.Lock(); defer o.mu.Unlock(); return o.active }
func (o *LogOpens) MaxActive() int { o.mu.Lock(); defer o.mu.Unlock(); return o.maxActive }

// SetWakeLog makes the rig record the content of the file at the moment each message is handed to the host.
func (r *Rig) SetWakeLog(path string) {
	r.wakeMu.Lock()
	r.wakePath = path
	r.wakeMu.Unlock()
	r.rt.beforeSend = func(customMessage) {
		b, err := os.ReadFile(path)
		r.wakeMu.Lock()
		defer r.wakeMu.Unlock()
		if err != nil {
			r.wakeLogs = append(r.wakeLogs, "")
			return
		}
		r.wakeLogs = append(r.wakeLogs, string(b))
	}
}

// WakeLogs is the file content seen at each message since SetWakeLog.
func (r *Rig) WakeLogs() []string {
	r.wakeMu.Lock()
	defer r.wakeMu.Unlock()
	return append([]string{}, r.wakeLogs...)
}
