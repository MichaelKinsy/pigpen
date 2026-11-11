package readashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// headerGrace is how long a missing rea server reads as "starting": on a first run the runtime member unpacks
// its payload and registers the server a few seconds after load.
const headerGrace = 6 * time.Second

const widgetKey = "rea-calls"

type doctorStatus int

const (
	doctorIdle doctorStatus = iota
	doctorRunning
	doctorDone
	doctorFailed
)

type doctorState struct {
	Status doctorStatus
	Report *doctorReport
	Err    string
}

// inspectJob is the state of one quick inspect.
type inspectJob struct {
	Target  string
	Running bool
	Result  inspectResult
}

// model is a consistent copy of everything the views draw, so rendering takes no locks and tests need no host.
type model struct {
	Calls    []call
	Server   reaServer
	Doctor   doctorState
	Inspect  map[string]inspectJob
	Now      time.Time
	Harness  string
	Starting bool // no rea server yet, inside the load grace period
}

// version is the REA version: the payload's, else what the doctor reported.
func (m model) version() string {
	if v := m.Server.version(); v != "" {
		return v
	}
	if m.Doctor.Report != nil {
		return m.Doctor.Report.REAVersion
	}
	return ""
}

// dashboard owns the state behind the header, the widget, the entries and the overlay.
type dashboard struct {
	store *store
	run   runner
	now   func() time.Time

	rootCtx context.Context
	cancel  context.CancelFunc

	mu          sync.Mutex
	server      reaServer
	doctor      doctorState
	doctorKey   string
	inspects    map[string]*inspectJob
	inspectBusy bool
	harness     string
	loadedAt    time.Time
	ctx         sdk.Context
	haveCtx     bool
	hasUI       bool
	subs        map[int]func()
	nextSub     int
	headerSig   string
	widgetSig   string
	widgetShown bool

	dirty    chan struct{}
	loopOnce sync.Once
	stopOnce sync.Once
	done     chan struct{}
}

func newDashboard(run runner, now func() time.Time) *dashboard {
	root, cancel := context.WithCancel(context.Background())
	return &dashboard{
		store: newStore(), run: run, now: now, rootCtx: root, cancel: cancel,
		inspects: map[string]*inspectJob{}, subs: map[int]func(){},
		dirty: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

// attach remembers the newest Context, which the background refresh uses to draw.
func (d *dashboard) attach(ctx sdk.Context) {
	d.mu.Lock()
	d.ctx, d.haveCtx = ctx, true
	first := d.loadedAt.IsZero()
	if first {
		d.loadedAt = d.now()
	}
	d.mu.Unlock()
	ui := safeHasUI(ctx)
	d.mu.Lock()
	d.hasUI = ui
	d.mu.Unlock()
	if ui {
		d.loopOnce.Do(func() { go d.loop() })
	}
	if first {
		harness := harnessVersion(ctx)
		d.mu.Lock()
		d.harness = harness
		d.mu.Unlock()
	}
}

func safeHasUI(ctx sdk.Context) (ui bool) {
	defer func() {
		if recover() != nil {
			ui = false
		}
	}()
	return ctx.HasUI()
}

// harnessVersion is the version in the package.json of the Package that supplied this extension, "" when the
// host does not say. The SDK has no accessor for the Piglet's own version.
func harnessVersion(ctx sdk.Context) (version string) {
	defer func() {
		if recover() != nil {
			version = ""
		}
	}()
	info, ok := ctx.ExtensionSourceInfo()
	if !ok || info.BaseDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(info.BaseDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// refreshServer reads the registered servers again. The doctor runs once per distinct registration.
func (d *dashboard) refreshServer(ctx sdk.Context) {
	servers, err := ctx.GetMcpServers()
	srv := reaServer{}
	if err == nil {
		srv = findREA(servers)
	}
	d.setServer(srv)
}

func (d *dashboard) setServer(srv reaServer) {
	d.mu.Lock()
	changed := d.server != srv
	d.server = srv
	ui := d.hasUI
	d.mu.Unlock()
	// startDoctor runs once per registration. It is not tied to `changed`: the server is often known at load, before the
	// host reports a UI, and the first refresh with a UI must still start it.
	if ui && srv.runnable() {
		d.startDoctor(false)
	}
	if changed {
		d.requestRefresh()
	}
}

// startDoctor runs `doctor --json` in the background; force runs it again for the same registration.
func (d *dashboard) startDoctor(force bool) bool {
	d.mu.Lock()
	srv := d.server
	key := srv.identity()
	if !srv.runnable() || d.doctor.Status == doctorRunning || !force && d.doctorKey == key && d.doctor.Status != doctorIdle {
		d.mu.Unlock()
		return false
	}
	d.doctorKey = key
	d.doctor = doctorState{Status: doctorRunning}
	d.mu.Unlock()
	go func() {
		report, err := runDoctor(d.root(), d.run, srv)
		d.mu.Lock()
		if d.doctorKey == key {
			if err != nil {
				d.doctor = doctorState{Status: doctorFailed, Err: short(err.Error(), 200)}
			} else {
				d.doctor = doctorState{Status: doctorDone, Report: report}
			}
		}
		d.mu.Unlock()
		d.requestRefresh()
	}()
	return true
}

// doctorNow runs the doctor and waits, for the text summary where nothing draws.
func (d *dashboard) doctorNow() {
	d.mu.Lock()
	srv := d.server
	d.mu.Unlock()
	if !srv.runnable() {
		return
	}
	report, err := runDoctor(d.root(), d.run, srv)
	d.mu.Lock()
	d.doctorKey = srv.identity()
	if err != nil {
		d.doctor = doctorState{Status: doctorFailed, Err: short(err.Error(), 200)}
	} else {
		d.doctor = doctorState{Status: doctorDone, Report: report}
	}
	d.mu.Unlock()
}

// startInspect runs a quick inspect of path in the background. It returns "" when the job started, else why not.
func (d *dashboard) startInspect(path string) string {
	d.mu.Lock()
	srv := d.server
	switch {
	case !srv.runnable():
		d.mu.Unlock()
		return "REA is not registered"
	case !localPath(path):
		d.mu.Unlock()
		return "quick inspect needs a local absolute path"
	case d.inspectBusy:
		d.mu.Unlock()
		return "another quick inspect is running"
	}
	d.inspectBusy = true
	d.inspects[path] = &inspectJob{Target: path, Running: true}
	d.mu.Unlock()
	go func() {
		res := runInspect(d.root(), d.run, srv, path)
		d.mu.Lock()
		d.inspects[path] = &inspectJob{Target: path, Result: res}
		d.inspectBusy = false
		d.mu.Unlock()
		d.requestRefresh()
	}()
	return ""
}

func (d *dashboard) model() model {
	calls := d.store.snapshot()
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	m := model{
		Calls: calls, Server: d.server, Doctor: d.doctor, Now: now, Harness: d.harness,
		Inspect:  make(map[string]inspectJob, len(d.inspects)),
		Starting: !d.server.Present && !d.loadedAt.IsZero() && now.Sub(d.loadedAt) < headerGrace,
	}
	for k, j := range d.inspects {
		m.Inspect[k] = *j
	}
	return m
}

// subscribe registers fn to run when the model changes; the returned function removes it.
func (d *dashboard) subscribe(fn func()) func() {
	d.mu.Lock()
	id := d.nextSub
	d.nextSub++
	d.subs[id] = fn
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		d.mu.Unlock()
	}
}

func (d *dashboard) notifySubs() {
	d.mu.Lock()
	fns := make([]func(), 0, len(d.subs))
	for _, fn := range d.subs {
		fns = append(fns, fn)
	}
	d.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// requestRefresh asks the refresh loop to redraw the surfaces and wake the overlay.
func (d *dashboard) requestRefresh() {
	select {
	case d.dirty <- struct{}{}:
	default:
	}
}

// loop redraws the header and widget when their content changed, and ticks once a second so running calls
// show their elapsed time and the load grace period ends.
func (d *dashboard) loop() {
	done := d.stopped()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-d.dirty:
			select { // coalesce a burst of events
			case <-time.After(80 * time.Millisecond):
			case <-done:
				return
			}
		case <-tick.C:
		case <-done:
			return
		}
		d.applyUI()
	}
}

// applyUI pushes the header and widget when their text changed. The Context may be stale after a session
// replacement; its panic is dropped and the next event brings a fresh one.
func (d *dashboard) applyUI() {
	defer func() { _ = recover() }()
	d.mu.Lock()
	ctx, ok, ui := d.ctx, d.haveCtx, d.hasUI
	d.mu.Unlock()
	if !ok || !ui {
		return
	}
	// The doctor runs once per registration; the first refresh that has a UI starts it whichever event brought the server.
	d.startDoctor(false)
	m := d.model()

	hsig := strings.Join(headerLines(m, 200), "\n")
	d.mu.Lock()
	changed := hsig != d.headerSig
	d.headerSig = hsig
	d.mu.Unlock()
	if changed {
		_ = ctx.SetHeaderRenderer(func(width int) []string { return headerLines(d.model(), width) })
	}

	wsig := strings.Join(widgetLines(m, 200), "\n")
	d.mu.Lock()
	wchanged := wsig != d.widgetSig
	d.widgetSig = wsig
	shown := d.widgetShown
	d.mu.Unlock()
	if wchanged {
		opts := sdk.WidgetOptions{"placement": "aboveEditor"}
		if len(m.Calls) == 0 {
			if shown {
				_ = ctx.SetWidget(widgetKey, nil, opts)
			}
			d.setWidgetShown(false)
		} else {
			_ = ctx.SetWidgetRenderer(widgetKey, func(width int) []string { return widgetLines(d.model(), width) }, opts)
			d.setWidgetShown(true)
		}
	}
	if running, _, _ := counts(m.Calls); changed || wchanged || running > 0 {
		d.notifySubs()
	}
}

func (d *dashboard) setWidgetShown(v bool) {
	d.mu.Lock()
	d.widgetShown = v
	d.mu.Unlock()
}

func (d *dashboard) root() context.Context {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rootCtx
}

func (d *dashboard) stopped() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.done
}

// revive restarts what shutdown stopped, for a session that starts in the same process.
func (d *dashboard) revive() {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-d.done:
	default:
		return
	}
	d.rootCtx, d.cancel = context.WithCancel(context.Background())
	d.done = make(chan struct{})
	d.stopOnce = sync.Once{}
	d.loopOnce = sync.Once{}
}

// shutdown stops background work.
func (d *dashboard) shutdown() {
	d.mu.Lock()
	cancel, done := d.cancel, d.done
	once := &d.stopOnce
	d.mu.Unlock()
	once.Do(func() {
		cancel()
		close(done)
	})
}

// summary is the one-line text of /rea without a UI.
func (m model) summary() string {
	if !m.Server.Present {
		return "REA not registered: no \"rea\" MCP server (started with --no-mcp?)"
	}
	parts := []string{"REA"}
	if v := m.version(); v != "" {
		parts[0] += " " + v
	}
	switch {
	case m.Server.Disabled:
		parts = append(parts, "server disabled")
	case m.Doctor.Status == doctorDone && m.Doctor.Report != nil:
		ready, failing, none := m.Doctor.Report.summary()
		parts = append(parts, fmt.Sprintf("providers: %d ready, %d failing, %d not configured", ready, failing, none))
	case m.Doctor.Status == doctorFailed:
		parts = append(parts, "doctor failed: "+m.Doctor.Err)
	}
	running, ok, failed := counts(m.Calls)
	parts = append(parts, fmt.Sprintf("calls: %d (%d ok, %d error, %d running)", len(m.Calls), ok, failed, running))
	return strings.Join(parts, " · ")
}
