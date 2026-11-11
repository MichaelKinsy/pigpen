package pi_subagents

// The subagent fleet inspector and the async agents view. The port tracks every child it starts (a single-agent run or a
// chain run's step) in an in-memory registry and renders the two observability surfaces the original has (port/PORT.md):
//
//   - the `subagent-async` widget ("Async agents"): live rows under the editor while children run, in the original's
//     row shapes (docs/observability.md, src/tui/render.ts);
//   - the fleet inspector: /subagents-fleet and `subagent({ action: "status", view: "fleet" })`, in the original's
//     textual fallback shape (src/runs/background/fleet-view.ts: inspectSubagentFleet).
//
// `subagent({ action: "status" })` (the async runs list) and `{ action: "status", id }` (one run, with
// `view: "transcript"`) are the same data as text (src/runs/background/async-status.ts: formatAsyncRunList,
// src/runs/background/run-status.ts: formatRememberedForegroundStatus/Transcript). Rows the port cannot fill (a child's
// current tool, token accounting, worktrees) are omitted, never invented.

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// runState is a run's or step's state; the strings are the original's.
type runState string

const (
	stateQueued   runState = "queued"
	stateRunning  runState = "running"
	stateComplete runState = "complete"
	stateFailed   runState = "failed"
	statePaused   runState = "paused"
	stateStopped  runState = "stopped"
)

// maxFleetRuns bounds what the registry remembers, as the original's DEFAULT_MAX_RUNS bounds its snapshot.
const maxFleetRuns = 20

// stepRecord is one child process: a single run's only step, or one step of a chain run.
type stepRecord struct {
	agent     string
	task      string
	thinking  string // the child's thinking level (the running glyph's tone in the fleet inspector)
	state     runState
	startedAt time.Time
	endedAt   time.Time
	// lastActivity is when the child last produced output; zero when nothing was seen (the original's activity facts).
	lastActivity time.Time
	output       string // the answer it gave back, remembered for the transcript view
	err          string
	exit         int
	session      string // the child's session file, when it has one (pane children do)
}

// runRecord is one `subagent` call's run: a single child, or a chain's steps in order. It carries the run's control
// channel (stop, interrupt and steer close it) and the child's request (steer and resume continue the session with it).
type runRecord struct {
	id        string
	mode      string // "single" or "chain"
	name      string // the agent (single) or the chain's name
	task      string
	cwd       string
	startedAt time.Time
	endedAt   time.Time
	steps     []*stepRecord

	done        chan struct{}
	ctrlMu      sync.Mutex
	terminated  bool
	endIntent   runState // the state a terminated step takes: statePaused (interrupt, steer) or stateStopped
	childReq    childRequest
	sessionDir  string // where the child's session file lives (hidden children), for steer and resume
	sessionID   string
	resumedFrom string
}

// terminate stops the run's child process: intent is stateStopped (stop) or statePaused (interrupt, steer). It reports
// whether this call was the one that stopped it.
func (r *runRecord) terminate(intent runState) bool {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	if r.terminated {
		return false
	}
	r.terminated = true
	r.endIntent = intent
	close(r.done)
	return true
}

// rearm gives the run a fresh control channel for its next turn (steer pivots in place).
func (r *runRecord) rearm() {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	r.done = make(chan struct{})
	r.terminated = false
}

// currentDone is the run's control channel as of now.
func (r *runRecord) currentDone() <-chan struct{} {
	r.ctrlMu.Lock()
	defer r.ctrlMu.Unlock()
	return r.done
}

// terminatedState is the state a cancelled step takes for this run.
func (r *runRecord) terminatedState() runState {
	if r.endIntent == statePaused {
		return statePaused
	}
	return stateStopped
}

// isLive is whether the run has a child going.
func (r *runRecord) isLive() bool {
	s := r.state()
	return s == stateRunning || s == stateQueued
}

// resumable is whether the run's session may be continued (the original: a stopped run is not resumable).
func (r *runRecord) resumable() bool {
	base, _, _ := r.getLaunch()
	return !r.isLive() && r.state() != stateStopped && base.Bin != ""
}

// fleetState is the session's run registry and the widget's source.
type fleetState struct {
	mu       sync.Mutex
	runs     []*runRecord
	next     int
	sink     func([]string)
	stop     chan struct{}
	wake     chan struct{}
	renderMu sync.Mutex // serializes sink calls (the ticker and a run's transitions may both push)
}

var fleet = &fleetState{next: 1}

// fleetNow is the clock the views read; tests replace it.
var fleetNow = time.Now

// beginRun registers a run at its first child. mode is "single" or "chain"; name the agent or chain name.
func (f *fleetState) beginRun(mode, name, task, cwd string) *runRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &runRecord{id: "run-" + strconv.Itoa(f.next), mode: mode, name: name, task: task, cwd: cwd, startedAt: fleetNow(), done: make(chan struct{})}
	f.next++
	f.runs = append(f.runs, r)
	if len(f.runs) > maxFleetRuns {
		f.runs = f.runs[len(f.runs)-maxFleetRuns:]
	}
	return r
}

// addStep registers the run's next child; the step index is the order of the calls. The step is complete when it is
// appended (registry.mu makes it visible).
func (r *runRecord) addStep(agent, task, thinking string) *stepRecord {
	s := &stepRecord{agent: agent, task: task, thinking: thinking, state: stateQueued}
	fleet.mu.Lock()
	r.steps = append(r.steps, s)
	fleet.mu.Unlock()
	return s
}

// setLaunch records what a later turn needs to continue the child (steer, resume).
func (r *runRecord) setLaunch(req childRequest, dir, id string) {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	r.childReq, r.sessionDir, r.sessionID = req, dir, id
}

// getLaunch is setLaunch's reader.
func (r *runRecord) getLaunch() (childRequest, string, string) {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	return r.childReq, r.sessionDir, r.sessionID
}

// lastThinking is the thinking level of the run's last turn (a new turn shares it).
func (r *runRecord) lastThinking() string {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	if len(r.steps) == 0 {
		return ""
	}
	return r.steps[len(r.steps)-1].thinking
}

// setResumedFrom marks the run as a follow-up of another.
func (r *runRecord) setResumedFrom(id string) {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	r.resumedFrom = id
}

// begin marks a step as running.
func (s *stepRecord) begin() {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	s.state = stateRunning
	s.startedAt = fleetNow()
}

// touch records child activity (output arrived, a pane poll answered).
func (s *stepRecord) touch() {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	s.lastActivity = fleetNow()
}

// finish ends a step; state is complete, failed, paused or stopped.
func (s *stepRecord) finish(state runState, output, errText, session string, exit int) {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	s.state = state
	s.endedAt = fleetNow()
	s.output = output
	s.err = errText
	s.exit = exit
	s.session = session
}

// state is the run's state: its steps' aggregate (the original's aggregateStepStatus), or stopped/queued while it lives.
func (r *runRecord) state() runState {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	return r.stateLocked()
}

func (r *runRecord) stateLocked() runState {
	running, failed, stopped, paused, complete, queued := false, false, false, false, 0, 0
	for _, s := range r.steps {
		switch s.state {
		case stateRunning:
			running = true
		case stateFailed:
			failed = true
		case stateStopped:
			stopped = true
		case statePaused:
			paused = true
		case stateComplete:
			complete++
		case stateQueued:
			queued++
		}
	}
	switch {
	case running:
		return stateRunning
	case failed:
		return stateFailed
	case stopped:
		return stateStopped
	case paused:
		return statePaused
	case complete > 0 && complete+queued == len(r.steps):
		return stateComplete
	default:
		return stateQueued
	}
}

// endedAt is when the run's last step ended, or the zero time while it lives.
func (r *runRecord) ended() time.Time {
	fleet.mu.Lock()
	defer fleet.mu.Unlock()
	return r.endedLocked()
}

func (r *runRecord) endedLocked() time.Time {
	var end time.Time
	for _, s := range r.steps {
		if s.endedAt.After(end) {
			end = s.endedAt
		}
	}
	return end
}

// currentStep is the running step's index and total, or the last step's when the run is done.
func (r *runRecord) currentStep() (int, int) {
	for i, s := range r.steps {
		if s.state == stateRunning {
			return i, len(r.steps)
		}
	}
	return len(r.steps) - 1, len(r.steps)
}

// snapshot copies the registry's runs, newest last, for rendering: the copies are private, so renderers never read a
// record a run goroutine is writing.
func (f *fleetState) snapshot() []*runRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*runRecord, len(f.runs))
	for i, r := range f.runs {
		out[i] = r.copyLocked()
	}
	return out
}

// copyLocked is a run's private copy (registry.mu held): the rendering fields only, no control state.
func (r *runRecord) copyLocked() *runRecord {
	c := &runRecord{
		id: r.id, mode: r.mode, name: r.name, task: r.task, cwd: r.cwd,
		startedAt: r.startedAt, endedAt: r.endedAt, childReq: r.childReq,
		sessionDir: r.sessionDir, sessionID: r.sessionID, resumedFrom: r.resumedFrom,
	}
	c.steps = make([]*stepRecord, len(r.steps))
	for i, s := range r.steps {
		cp := *s
		c.steps[i] = &cp
	}
	return c
}

// find returns a private copy of the run with this id (the status views read it).
func (f *fleetState) find(id string) *runRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if r.id == id {
			return r.copyLocked()
		}
	}
	return nil
}

// findLive returns the run itself (stop, interrupt, resume and steer act on it).
func (f *fleetState) findLive(id string) *runRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if r.id == id {
			return r
		}
	}
	return nil
}

// active is the runs still going.
func (f *fleetState) active() []*runRecord {
	var out []*runRecord
	for _, r := range f.snapshot() {
		if r.state() == stateRunning || r.state() == stateQueued {
			out = append(out, r)
		}
	}
	return out
}

// changed pushes fresh widget rows after a run's transition.
func (f *fleetState) changed() {
	f.mu.Lock()
	wake := f.wake
	f.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// setSink routes widget rows to the host (SetWidget), or stops the router when fn is nil. Rows are the widget's content;
// an empty list clears the widget.
func (f *fleetState) setSink(fn func([]string)) {
	f.mu.Lock()
	f.sink = fn
	if fn != nil && f.stop == nil {
		f.stop = make(chan struct{})
		f.wake = make(chan struct{}, 1)
		stop, wake := f.stop, f.wake
		go func() {
			t := time.NewTicker(2 * time.Second) // re-render while children run: elapsed times and activity age move
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-wake:
				case <-t.C:
				}
				f.pushWidget()
			}
		}()
	}
	if fn == nil && f.stop != nil {
		close(f.stop)
		f.stop, f.wake = nil, nil
	}
	f.mu.Unlock()
	f.pushWidget()
}

// pushWidget renders the async agents view and hands it to the host.
func (f *fleetState) pushWidget() {
	f.mu.Lock()
	sink := f.sink
	f.mu.Unlock()
	if sink == nil {
		return
	}
	f.renderMu.Lock()
	defer f.renderMu.Unlock()
	sink(asyncWidgetRows(f.snapshot(), fleetNow()))
}

// reset empties the registry (a new session, a test).
func (f *fleetState) reset() {
	f.mu.Lock()
	f.runs, f.next = nil, 1
	f.mu.Unlock()
	f.changed()
}

// stopAll ends every live run's child (the session is ending).
func (f *fleetState) stopAll() {
	f.mu.Lock()
	runs := make([]*runRecord, len(f.runs))
	copy(runs, f.runs)
	f.mu.Unlock()
	for _, r := range runs {
		if r.isLive() {
			r.terminate(stateStopped)
		}
	}
}

// ---- the original's display formatters (src/shared/formatters.ts, src/shared/status-format.ts) ----

// formatTokens is the original's: 999, 1.5k, 24k, 1M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10000:
		return jsToFixed(float64(n)/1000, 1) + "k"
	case n < 999500:
		return strconv.Itoa(int(jsRound(float64(n)/1000))) + "k"
	}
	return strings.TrimSuffix(jsToFixed(float64(n)/1000000, 1), ".0") + "M"
}

// formatDuration is the original's: 42ms, 2.0s, 1m12s.
func formatDuration(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return jsToFixed(float64(ms)/1000, 1) + "s"
	}
	return fmt.Sprintf("%dm%ds", ms/60000, (ms%60000)/1000)
}

// formatActivityAge is the original's: now, 5s, 3m.
func formatActivityAge(ms int64) string {
	switch {
	case ms < 1000:
		return "now"
	case ms < 60000:
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dm", ms/60000)
}

// formatActivityLabel is the original's: "active now", "active 5s ago", or "" when nothing was seen.
func formatActivityLabel(last time.Time, now time.Time) string {
	if last.IsZero() {
		return ""
	}
	age := formatActivityAge(max64(0, now.Sub(last).Milliseconds()))
	if age == "now" {
		return "active now"
	}
	return "active " + age + " ago"
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// shortenPath is the original's: the home directory becomes ~.
func shortenPath(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// oneLine collapses a task to one line of plain text (the original's oneLine + sanitizeDisplayText, control strings
// stripped).
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == 0x1b: // an escape sequence: dropped with what follows up to its end
			space = true
		case r <= 0x20 || r == 0x7f:
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}

// previewText is the original's previewDisplayText: one line, cut to max with "...".
func previewText(s string, max int) string {
	s = oneLine(s)
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// compactTaskText is the original's (render.ts): the task on one line, 96 characters at most.
func compactTaskText(task string) string {
	t := previewText(task, 96)
	return t
}

// ---- the async agents view (the `subagent-async` widget; src/tui/render.ts) ----

// stateGlyph is the original's widget glyph per state (runningGlyph is animated in the original; a static ● here).
func stateGlyph(s runState) string {
	switch s {
	case stateRunning:
		return "●"
	case stateQueued:
		return "◦"
	case stateComplete:
		return "✓"
	case stateFailed:
		return "✗"
	case stateStopped:
		return "■"
	}
	return "◦"
}

// jobName is the original's widgetJobName: the agent (single) or the mode.
func jobName(r *runRecord) string {
	if r.mode == "chain" {
		return "chain"
	}
	if len(r.steps) == 1 {
		return r.steps[0].agent
	}
	return r.name
}

// widgetStats is the original's (the parts the port can fill): step progress and elapsed time.
func widgetStats(r *runRecord, now time.Time) string {
	var parts []string
	if r.mode == "chain" {
		i, total := r.currentStep()
		if r.state() == stateRunning {
			parts = append(parts, fmt.Sprintf("step %d/%d", i+1, total))
		} else {
			parts = append(parts, fmt.Sprintf("steps %d", total))
		}
	}
	end := now
	if r.state() != stateRunning && r.state() != stateQueued {
		if e := r.ended(); !e.IsZero() {
			end = e
		}
	}
	if !r.startedAt.IsZero() {
		parts = append(parts, formatDuration(max64(0, end.Sub(r.startedAt).Milliseconds())))
	}
	return strings.Join(parts, " · ")
}

// widgetActivity is the original's: the activity label, then the state's word (a running child nothing was seen from is
// "thinking…"); "" when a finished child's row has nothing to add.
func widgetActivity(s *stepRecord, now time.Time) string {
	if a := formatActivityLabel(s.lastActivity, now); a != "" {
		return a
	}
	switch s.state {
	case stateRunning:
		return "thinking…"
	case stateQueued:
		return "queued…"
	case stateStopped:
		return "Stopped"
	case statePaused:
		return "Paused"
	case stateFailed:
		return "Failed"
	}
	return ""
}

// liveDetailHintText is the original's expandKeyHint when no expand key is configured (the port reads no keybindings).
const liveDetailHintText = "Configure the expand key for live detail"

// asyncWidgetRows is the async agents view: the `subagent-async` widget's rows (docs/observability.md's example).
func asyncWidgetRows(runs []*runRecord, now time.Time) []string {
	if len(runs) == 0 {
		return nil
	}
	if len(runs) == 1 {
		return widgetJobRows(runs[0], now)
	}
	// Several runs: the original's aggregate header, then one row per run (buildWidgetLinesWithProjection).
	running := false
	for _, r := range runs {
		running = running || r.state() == stateRunning || r.state() == stateQueued
	}
	glyph := "○"
	if running {
		glyph = "●"
	}
	lines := []string{fmt.Sprintf("%s Async agents · background", glyph)}
	const maxShown = 8
	for i, r := range runs {
		if i >= maxShown {
			finished := len(runs) - maxShown
			lines = append(lines, fmt.Sprintf("  +%d more (%d finished)", finished, finished))
			break
		}
		lines = append(lines, widgetJobBody(r, now)...)
	}
	return lines
}

// widgetJobRows is one run's block: its title line, then its body (buildSingleWidgetLines). A finished single child
// collapses to its summary row, as the original's shouldCollapseSingleChildDetails collapses it.
func widgetJobRows(r *runRecord, now time.Time) []string {
	title := fmt.Sprintf("async subagent %s · background", jobName(r))
	if r.mode == "chain" {
		title = fmt.Sprintf("async subagent chain (%d) · background", len(r.steps))
	}
	return append([]string{title}, widgetJobBody(r, now)...)
}

// widgetJobBody is one run's rows under its title or the aggregate header: its summary and its steps
// (foregroundStyleWidgetStepLines).
func widgetJobBody(r *runRecord, now time.Time) []string {
	state := r.state()
	if len(r.steps) == 1 && state != stateRunning && state != stateQueued {
		return []string{fmt.Sprintf("%s %s · %s · %s", stateGlyph(state), jobName(r), stateWord(state), widgetStats(r, now))}
	}
	lines := []string{fmt.Sprintf("%s %s · %s", stateGlyph(state), jobName(r), widgetStats(r, now))}
	for i, s := range r.steps {
		rowLabel := fmt.Sprintf("Step %d/%d: %s", i+1, len(r.steps), s.agent)
		lines = append(lines, fmt.Sprintf("  %s %s · %s", stateGlyph(s.state), rowLabel, stateWord(s.state)))
		if task := compactTaskText(s.task); task != "" {
			lines = append(lines, "    task: "+task)
		}
		if activity := widgetActivity(s, now); activity != "" {
			lines = append(lines, "    ⎿  "+activity)
		}
		if s.state == stateRunning {
			lines = append(lines, "    "+liveDetailHintText)
		}
	}
	return lines
}

// stateWord is the original's widgetStepStatus word per state.
func stateWord(s runState) string {
	switch s {
	case stateRunning:
		return "running"
	case stateComplete:
		return "complete"
	case stateFailed:
		return "failed"
	case statePaused:
		return "paused"
	case stateStopped:
		return "stopped"
	case stateQueued:
		return "pending"
	}
	return string(s)
}

// ---- the fleet inspector (/subagents-fleet and `subagent({ action: "status", view: "fleet" })`;
// the original's textual fallback, fleet-view.ts: inspectSubagentFleet) ----

const (
	fleetNoActive = `No active subagent fleet. Background runs that already finished are available through completion notifications or subagent({ action: "status", id: "..." }).`
	fleetCommands = `Commands:
  Refresh fleet: subagent({ action: "status", view: "fleet" })
  Tail run transcript: subagent({ action: "status", id: "<run-id>", view: "transcript" })
  Tail child transcript: subagent({ action: "status", id: "<run-id>", index: 0, view: "transcript" })`
	fleetForegroundTranscript = "transcript: live in the expanded foreground result; persisted session transcript appears after completion when sessions are enabled."
)

// fleetText renders the fleet inspector: active runs first (the original's Foreground runs), then the runs this session
// finished with their session paths (a port addition: the port runs children in the foreground, so the inspector's
// "recent children and completed output/session paths" live here).
func fleetText(runs []*runRecord, now time.Time) string {
	var active, recent []*runRecord
	for i := len(runs) - 1; i >= 0; i-- { // most recently updated first, as the original orders its controls
		r := runs[i]
		if r.state() == stateRunning || r.state() == stateQueued {
			active = append(active, r)
		} else {
			recent = append(recent, r)
		}
	}
	if len(active) == 0 && len(recent) == 0 {
		return fleetNoActive
	}
	lines := []string{fmt.Sprintf("Subagent fleet: %d tracked", len(active)), ""}
	if len(active) > 0 {
		lines = append(lines, "Foreground runs:")
		for _, r := range active {
			lines = append(lines, fleetForegroundEntry(r, now)...)
		}
		lines = append(lines, "")
	}
	if len(recent) > 0 {
		lines = append(lines, "Recent runs:")
		for _, r := range recent {
			lines = append(lines, fleetRecentEntry(r, now)...)
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(append(lines, strings.Split(fleetCommands, "\n")...), "\n"), "\n")
}

// fleetForegroundEntry is the original's formatForegroundFleetLines entry for one active run.
func fleetForegroundEntry(r *runRecord, now time.Time) []string {
	mode := jobName(r)
	if r.mode == "chain" {
		mode = "chain"
	}
	current := ""
	if r.mode == "chain" {
		i, _ := r.currentStep()
		if i >= 0 && i < len(r.steps) {
			current = fmt.Sprintf(" | %s #%d", r.steps[i].agent, i+1)
		}
	}
	activity := ""
	for _, s := range r.steps {
		if a := formatActivityLabel(s.lastActivity, now); a != "" {
			activity = " | " + a
			break
		}
	}
	return []string{
		fmt.Sprintf("- %s | running | %s%s%s", r.id, mode, current, activity),
		fmt.Sprintf(`  status: subagent({ action: "status", id: "%s" })`, r.id),
		"  " + fleetForegroundTranscript,
	}
}

// fleetRecentEntry is a finished run, in the original's line grammar, with the paths the port keeps.
func fleetRecentEntry(r *runRecord, now time.Time) []string {
	elapsed := ""
	if e := r.ended(); !e.IsZero() {
		elapsed = " | " + formatDuration(max64(0, e.Sub(r.startedAt).Milliseconds()))
	}
	lines := []string{
		fmt.Sprintf("- %s | %s | %s%s", r.id, r.state(), jobName(r), elapsed),
		fmt.Sprintf(`  status: subagent({ action: "status", id: "%s" })`, r.id),
	}
	for _, s := range r.steps {
		if s.session != "" {
			lines = append(lines, "  session: "+shortenPath(s.session))
		}
	}
	return lines
}

// ---- the status action's text (async-status.ts: formatAsyncRunList; run-status.ts: the remembered blocks) ----

// statusListText is `subagent({ action: "status" })`: the active runs, the original's list (heading included).
func statusListText(runs []*runRecord, now time.Time) string {
	active := runs
	for i := len(active) - 1; i >= 0; i-- {
		if active[i].state() != stateRunning && active[i].state() != stateQueued {
			active = append(active[:i], active[i+1:]...)
		}
	}
	if len(active) == 0 {
		return "No active async runs."
	}
	lines := []string{fmt.Sprintf("Active async runs: %d", len(active)), ""}
	for _, r := range active {
		lines = append(lines, "- "+statusRunHeader(r, now))
		for i, s := range r.steps {
			lines = append(lines, "  "+statusStepLine(r, i, s, now))
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// statusRunHeader is the original's formatRunHeader.
func statusRunHeader(r *runRecord, now time.Time) string {
	activity := ""
	for _, s := range r.steps {
		if a := formatActivityLabel(s.lastActivity, now); a != "" {
			activity = " | " + a
			break
		}
	}
	return fmt.Sprintf("%s | %s%s | %s | %s | %s", r.id, r.state(), activity, r.mode, statusProgressLabel(r), shortenPath(r.cwd))
}

// statusProgressLabel is the original's formatAsyncRunProgressLabel for a plain run: the running step, or the count.
func statusProgressLabel(r *runRecord) string {
	for i, s := range r.steps {
		if s.state == stateRunning {
			if r.mode == "chain" {
				return fmt.Sprintf("step %d/%d", i+1, len(r.steps))
			}
			return fmt.Sprintf("step %d/%d", i+1, len(r.steps))
		}
	}
	return fmt.Sprintf("steps %d", len(r.steps))
}

// statusStepLine is the original's formatStepLine for the rows the port can fill.
func statusStepLine(r *runRecord, i int, s *stepRecord, now time.Time) string {
	parts := []string{fmt.Sprintf("%d. %s", i+1, s.agent), string(s.state)}
	if a := formatActivityLabel(s.lastActivity, now); a != "" && s.state == stateRunning {
		parts = append(parts, a)
	}
	if !s.endedAt.IsZero() {
		parts = append(parts, formatDuration(max64(0, s.endedAt.Sub(s.startedAt).Milliseconds())))
	}
	return strings.Join(parts, " | ")
}

// statusRunText is `subagent({ action: "status", id })`: the remembered status block (formatRememberedForegroundStatus).
func statusRunText(r *runRecord) string {
	lines := []string{
		"Run: " + r.id,
		"State: " + string(r.state()),
		"Mode: " + r.mode,
	}
	if r.resumedFrom != "" {
		lines = append(lines, "Resumed: "+r.resumedFrom)
	}
	if !r.ended().IsZero() {
		lines = append(lines, "Updated: "+r.ended().UTC().Format(time.RFC3339Nano))
	} else {
		lines = append(lines, "Updated: "+fleetNow().UTC().Format(time.RFC3339Nano))
	}
	lines = append(lines, "Cwd: "+r.cwd)
	for i, s := range r.steps {
		parts := []string{fmt.Sprintf("%d. %s %s", i+1, s.agent, s.state)}
		if s.state != stateRunning && s.state != stateQueued {
			parts = append(parts, fmt.Sprintf("exit %d", s.exit))
		}
		if s.err != "" {
			parts = append(parts, "error: "+previewText(s.err, 160))
		}
		if out := firstLine(s.output); out != "" {
			parts = append(parts, "output: "+previewText(out, 160))
		}
		lines = append(lines, strings.Join(parts, ", "))
		if s.session != "" {
			lines = append(lines, "  Session: "+s.session)
		}
	}
	lines = append(lines, "", fmt.Sprintf(`Status: subagent({ action: "status", id: "%s" })`, r.id))
	if len(r.steps) == 1 {
		lines = append(lines, fmt.Sprintf(`Transcript: subagent({ action: "status", id: "%s", view: "transcript" })`, r.id))
	} else {
		lines = append(lines, fmt.Sprintf(`Transcript: subagent({ action: "status", id: "%s", index: 0, view: "transcript" })`, r.id))
	}
	if session := runSessionFile(r); session != "" {
		if len(r.steps) == 1 {
			lines = append(lines, fmt.Sprintf(`Revive: subagent({ action: "resume", id: "%s", message: "..." })`, r.id))
		} else {
			lines = append(lines, fmt.Sprintf(`Revive child: subagent({ action: "resume", id: "%s", index: %d, message: "..." })`, r.id, len(r.steps)-1))
		}
	} else {
		lines = append(lines, "Resume: unavailable; no child session file was persisted.")
	}
	return strings.Join(lines, "\n")
}

// transcriptIndexError is the original's validateTranscriptIndex: "" when index may run.
func transcriptIndexError(r *runRecord, index *int) string {
	if index == nil {
		return ""
	}
	i := *index
	if i < 0 || i >= len(r.steps) {
		return fmt.Sprintf("Transcript index %d is out of range for %d child step%s.", i, len(r.steps), plural(len(r.steps)))
	}
	return ""
}

// transcriptText is `subagent({ action: "status", id, view: "transcript" })`: the result transcript tail
// (formatRememberedForegroundTranscript). index is already validated (transcriptIndexError).
func transcriptText(r *runRecord, index *int, lines int) string {
	i := 0
	if index != nil {
		i = *index
	}
	s := r.steps[i]
	out := []string{
		"Run: " + r.id,
		"State: " + string(s.state),
		fmt.Sprintf("Child: %d (%s)", i, s.agent),
	}
	if s.session != "" {
		out = append(out, "Session: "+s.session)
	}
	out = append(out, "Result transcript tail:")
	tail := outputTail(s.output, lines)
	if len(tail) == 0 {
		out = append(out, "  (no recovered final output available yet)")
	} else {
		for _, l := range tail {
			out = append(out, "  "+l)
		}
	}
	return strings.Join(out, "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// firstLine is the first non-empty output line.
func firstLine(output string) string {
	for _, l := range strings.Split(output, "\n") {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// outputTail is the last lines non-empty lines of a child's answer.
func outputTail(output string, lines int) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out
}

// runSessionFile is the first session file a run's children have.
func runSessionFile(r *runRecord) string {
	for _, s := range r.steps {
		if s.session != "" {
			return s.session
		}
	}
	return ""
}

// sortedIDs is the runs' ids, oldest first (used by error messages).
func sortedIDs(runs []*runRecord) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.id)
	}
	sort.Slice(ids, func(i, j int) bool { return localeLess(ids[i], ids[j]) })
	return ids
}

// ---- the model's and the operator's entry points ----

func init() {
	// `subagent({ action: "status" })` is the views' text surface; the original's run-status.ts serves it.
	registerAction("status", []string{"id", "runId", "view", "index", "lines"}, func(_ sdk.Context, params map[string]any) (any, error) {
		text, mode, errMsg := statusView(params)
		if errMsg != "" {
			return nil, sdk.NewToolError(errMsg)
		}
		return explicitOK{Content: text, Details: map[string]any{"mode": mode, "results": []any{}}}, nil
	})
}

// fleetCommand is `/subagents-fleet`, the fleet inspector: the original's TUI overlay (its SubagentFleetComponent via
// ui.custom) when the host has a UI, and its textual fallback otherwise (observability.md: "Without a TUI,
// /subagents-fleet retains the textual ... fallback").
func fleetCommand(ctx sdk.Context, _ string) error {
	if !ctx.HasUI() {
		ctx.Notify(fleetText(fleet.snapshot(), fleetNow()), "info")
		return nil
	}
	if !fleetInspectorOpen.CompareAndSwap(false, true) {
		ctx.Notify("Subagent fleet inspector is already open.", "info")
		return nil
	}
	defer fleetInspectorOpen.Store(false)
	margin := 1
	_, err := ctx.Custom(newFleetInspector(ctx), sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Anchor:    "center",
			Width:     sdk.OverlayPercent(95),
			MinWidth:  60,
			MaxHeight: sdk.OverlayPercent(85),
			Margin:    &sdk.OverlayMargin{All: &margin},
		},
	})
	if err != nil {
		ctx.Notify(err.Error(), "error")
	}
	return nil
}

// statusView is the original's inspectSubagentStatus over the port's runs: the fleet view, the active runs list, one
// run's status block, or one run's transcript tail. It returns the text and its detail mode, or an error message.
func statusView(params map[string]any) (text, mode, errMsg string) {
	view, _ := params["view"].(string)
	if view != "" && view != "fleet" && view != "transcript" {
		return "", "", fmt.Sprintf("Unknown status view: %s. Valid: fleet, transcript.", view)
	}
	if view == "fleet" {
		return fleetText(fleet.snapshot(), fleetNow()), "management", ""
	}
	id, _ := params["id"].(string)
	if id == "" {
		id, _ = params["runId"].(string)
	}
	if id == "" {
		active := fleet.active()
		if view == "transcript" {
			switch len(active) {
			case 0:
				return "", "", "No active async run transcript is available."
			case 1:
				id = active[0].id
			default:
				return "", "", fmt.Sprintf("Transcript view requires an id when %d active async runs exist. Use subagent({ action: \"status\", view: \"fleet\" }) to choose one.", len(active))
			}
		} else {
			return statusListText(fleet.snapshot(), fleetNow()), "single", ""
		}
	}
	r := fleet.find(id)
	if r == nil {
		return "", "", fmt.Sprintf("Async run '%s' was not found in the active session.", id)
	}
	if view != "transcript" {
		return statusRunText(r), "single", ""
	}
	var index *int
	if v, ok := params["index"]; ok && v != nil {
		i, isInteger := paramInt(v)
		if !isInteger {
			return "", "", "Transcript index must be an integer."
		}
		index = &i
	}
	if index == nil && len(r.steps) > 1 {
		return "", "", fmt.Sprintf("Transcript view requires index for foreground run '%s' with %d children.", r.id, len(r.steps))
	}
	if msg := transcriptIndexError(r, index); msg != "" {
		return "", "", msg
	}
	return transcriptText(r, index, transcriptLineCount(params)), "management", ""
}

// paramInt reads a whole number from a request parameter (the host sends JSON numbers; tests may send ints).
func paramInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	}
	return 0, false
}

// transcriptLineCount is the original's transcript `lines` option: 80 by default, 1..1000.
func transcriptLineCount(params map[string]any) int {
	const defaultLines = 80
	v, ok := params["lines"]
	if !ok || v == nil {
		return defaultLines
	}
	n, isNumber := paramInt(v)
	if !isNumber {
		return defaultLines
	}
	if n < 1 {
		return 1
	}
	if n > 1000 {
		return 1000
	}
	return n
}
