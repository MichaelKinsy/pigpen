package pi_background_tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
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
func (r *Rig) Settle() { r.rt.loop.barrier(); r.rt.settle(); r.rt.timerOut.barrier() }

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
	waiting   []chan struct{}
	credit    int
	open      bool
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
	o := &LogOpens{}
	r.rt.reader.open = func(path string) (readableFile, error) {
		o.mu.Lock()
		o.requested = append(o.requested, path)
		o.active++
		o.maxActive = max(o.maxActive, o.active)
		switch {
		case o.open:
		case o.credit > 0:
			o.credit--
		default:
			turn := make(chan struct{})
			o.waiting = append(o.waiting, turn)
			o.held++
			o.mu.Unlock()
			<-turn
			o.mu.Lock()
			o.held--
		}
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

// Release lets n held opens proceed, the earliest arrival first (the original's fake host resolves its held opens in order), and
// lets the next n opens that arrive pass at once when fewer are held; n < 0 lets every open proceed from now on.
func (o *LogOpens) Release(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if n < 0 {
		o.open = true
		for _, turn := range o.waiting {
			close(turn)
		}
		o.waiting = nil
		return
	}
	for range n {
		if len(o.waiting) > 0 {
			close(o.waiting[0])
			o.waiting = o.waiting[1:]
			continue
		}
		o.credit++
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

// SetClock pins the clock of the lifecycle and the widget (the original's fixtures replace Date.now) until the test ends.
func SetClock(t testing.TB, now func() int64) {
	prev := clockNow
	clockNow = now
	t.Cleanup(func() { clockNow = prev })
}

// WidgetEntries is the keys of the entries of the mini-dashboard stack.
func (r *Rig) WidgetEntries() []string {
	r.rt.stack.mu.Lock()
	defer r.rt.stack.mu.Unlock()
	keys := []string{}
	for k := range r.rt.stack.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// StackLines is what the stack draws at placement and width now, as the host's component renders it.
func (r *Rig) StackLines(placement string, width int) []string {
	r.rt.mu.Lock()
	defer r.rt.mu.Unlock()
	host := r.rt.currentHost()
	return r.rt.stack.lines(placement, width, 40, host.theme())
}

// SetSibling adds, replaces or (with nil lines) removes an entry that another extension's mini-dashboard would put in the stack.
func (r *Rig) SetSibling(rank int, lines []string, placement string) {
	r.rt.withLock(func() {
		host := r.rt.currentHost()
		var render func(width, rows int, theme uiTheme) []string
		if lines != nil {
			render = func(int, int, uiTheme) []string { return lines }
		}
		r.rt.stack.set(host, "sibling", rank, render, placement)
	})
}

// LogIO counts what the rig's task output reader asks of the files: opens, reads and the bytes they return.
type LogIO struct {
	mu    sync.Mutex
	opens int
	reads int
	bytes int64
}

type countingFile struct {
	readableFile
	io *LogIO
}

func (c countingFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.readableFile.ReadAt(p, off)
	c.io.mu.Lock()
	c.io.reads++
	c.io.bytes += int64(n)
	c.io.mu.Unlock()
	return n, err
}

// CountLogIO makes the rig's reader count its opens, reads and bytes.
func (r *Rig) CountLogIO() *LogIO {
	io := &LogIO{}
	r.rt.reader.open = func(path string) (readableFile, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		io.mu.Lock()
		io.opens++
		io.mu.Unlock()
		return countingFile{readableFile: f, io: io}, nil
	}
	return io
}

// Counts is the opens, reads and bytes so far.
func (l *LogIO) Counts() (opens, reads int, bytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.opens, l.reads, l.bytes
}

// Dashboard is the dashboard of the rig's runtime with the terminal replaced by a test: plain colors, the host's render request
// counted, and the wrap of the command (a text that starts with "cache-me") counted.
type Dashboard struct {
	d       *dashboard
	reads   sync.WaitGroup
	mu      sync.Mutex
	renders int
	wraps   int
}

// plainTheme styles nothing, as the original's fixtures' theme.
type plainTheme struct{}

func (plainTheme) Fg(_, text string) string { return text }
func (plainTheme) Bg(_, text string) string { return text }
func (plainTheme) Bold(text string) string  { return text }
func (plainTheme) Inverse(text string) string {
	return text
}

// OpenDashboard opens the dashboard on the rig's tasks at a terminal of 40 rows.
func (r *Rig) OpenDashboard() *Dashboard {
	out := &Dashboard{}
	deps := r.rt.dashboardDeps()
	read := deps.getTaskOutput
	// Every read is waited for between frames, as the original's fixture does (`await Promise.all([...pendingReads])`).
	deps.getTaskOutput = func(id string, done func(string)) {
		out.reads.Add(1)
		read(id, func(text string) {
			done(text)
			out.reads.Done()
		})
	}
	wrap := func(text string, width int) []string {
		if strings.HasPrefix(text, "cache-me") {
			out.mu.Lock()
			out.wraps++
			out.mu.Unlock()
		}
		return widthx.WrapTextWithAnsi(text, width)
	}
	out.d = newDashboard(deps, plainTheme{}, "", func() int { return 40 }, wrap)
	out.d.SetInvalidate(func() {
		out.mu.Lock()
		out.renders++
		out.mu.Unlock()
	})
	return out
}

func (d *Dashboard) Render(width int) []string { return d.d.Render(width) }
func (d *Dashboard) Input(data string)         { _, _ = d.d.HandleInput(data) }
func (d *Dashboard) Invalidate()               { d.d.resetTheme() }
func (d *Dashboard) Dispose()                  { d.d.Dispose() }

// WaitReads waits for the log reads the frames started.
func (d *Dashboard) WaitReads() { d.reads.Wait() }

// CommandWraps is how many times a command was wrapped.
func (d *Dashboard) CommandWraps() int { d.mu.Lock(); defer d.mu.Unlock(); return d.wraps }

// Renders is how many redraws the dashboard asked the host for.
func (d *Dashboard) Renders() int { d.mu.Lock(); defer d.mu.Unlock(); return d.renders }

// AppendToCommand changes the command of a task, as the original's fixture does to its task.
func (r *Rig) AppendToCommand(id, text string) {
	r.rt.withLock(func() { r.rt.tasks.get(id).Command += text })
}
