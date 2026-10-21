package schedule_prompt

import (
	"math"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// The Jobs overlay of `/schedule-prompt` (upstream: ui/jobs-view.ts JobsView): one TUI overlay that consolidates view, add, toggle,
// scope, remove and cleanup. Jobs of other sessions render read-only in a separate group; the actions t, s and x ignore them.

const (
	jobsNameW   = 18
	jobsSchedW  = 16
	jobsPromptW = 30
)

// viewTheme is the part of the host's theme the overlay uses (sdk.UITheme satisfies it).
type viewTheme interface {
	Fg(token, text string) string
	Bold(text string) string
}

type confirmState struct {
	kind  string // "remove" or "cleanup"
	id    string
	name  string
	count int
}

type jobsView struct {
	mu          sync.Mutex
	storage     jobStore
	sched       jobScheduler
	mySessionID string
	runAdd      func() error
	theme       viewTheme
	invalidate  func()

	mine, foreign []*jsobj.Object
	selected      int
	confirm       *confirmState
}

var (
	_ sdk.RemoteComponent            = (*jobsView)(nil)
	_ sdk.RemoteComponentInvalidator = (*jobsView)(nil)
)

func newJobsView(storage jobStore, sched jobScheduler, mySessionID string, runAdd func() error, theme viewTheme) *jobsView {
	v := &jobsView{storage: storage, sched: sched, mySessionID: mySessionID, runAdd: runAdd, theme: theme}
	v.refresh()
	return v
}

// SetInvalidate installs the redraw callback (the original's tui.requestRender).
func (v *jobsView) SetInvalidate(f func()) {
	v.mu.Lock()
	v.invalidate = f
	v.mu.Unlock()
}

func (v *jobsView) requestRender() {
	v.mu.Lock()
	f := v.invalidate
	v.mu.Unlock()
	if f != nil {
		f()
	}
}

func (v *jobsView) refresh() {
	all, err := v.storage.GetAllJobs()
	if err != nil {
		logf("[pi-schedule-prompt] Failed to read the scheduled prompts: %v", err)
		return
	}
	v.mine, v.foreign = nil, nil
	for _, j := range all {
		if isLoadedFor(j, v.mySessionID) {
			v.mine = append(v.mine, j)
		} else {
			v.foreign = append(v.foreign, j)
		}
	}
	if max := max(0, len(v.mine)+len(v.foreign)-1); v.selected > max {
		v.selected = max
	}
}

func (v *jobsView) selectedJob() *jsobj.Object {
	if v.selected < len(v.mine) {
		return v.mine[v.selected]
	}
	if i := v.selected - len(v.mine); i < len(v.foreign) {
		return v.foreign[i]
	}
	return nil
}

func (v *jobsView) isSelectionForeign() bool { return v.selected >= len(v.mine) }

// HandleInput routes a key to the confirm prompt while one is open, else to the list.
func (v *jobsView) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.confirm != nil {
		v.handleConfirmInput(data)
		return sdk.RemoteComponentResult{}, nil
	}
	return sdk.RemoteComponentResult{Done: v.handleNormalInput(data)}, nil
}

func (v *jobsView) handleConfirmInput(data string) {
	if tui.MatchesKey(data, "y") {
		switch v.confirm.kind {
		case "remove":
			v.removeJob(v.confirm.id)
		case "cleanup":
			for _, j := range v.mine {
				if !truthy(j.Get("enabled")) {
					v.removeJob(jobID(j))
				}
			}
		}
		v.confirm = nil
		v.refresh()
		return
	}
	if tui.MatchesKey(data, "n") || tui.MatchesKey(data, "escape") {
		v.confirm = nil
	}
}

func (v *jobsView) removeJob(id string) {
	if _, err := v.storage.RemoveJob(id); err != nil {
		logf("[pi-schedule-prompt] Failed to remove job %s: %v", id, err)
	}
	v.sched.removeJob(id)
}

// handleNormalInput reports whether the overlay is done.
func (v *jobsView) handleNormalInput(data string) bool {
	if tui.MatchesKey(data, "q") || tui.MatchesKey(data, "escape") {
		return true
	}
	if tui.MatchesKey(data, "up") {
		if v.selected > 0 {
			v.selected--
		}
		return false
	}
	if tui.MatchesKey(data, "down") {
		if v.selected < len(v.mine)+len(v.foreign)-1 {
			v.selected++
		}
		return false
	}
	if tui.MatchesKey(data, "a") {
		// The add flow runs on its own: its dialogs are answered through the host while this overlay is hidden.
		go func() {
			if err := v.runAdd(); err != nil {
				logf("[pi-schedule-prompt] add flow failed: %v", err)
			}
			v.mu.Lock()
			v.refresh()
			v.mu.Unlock()
			v.requestRender()
		}()
		return false
	}

	sel := v.selectedJob()
	if sel == nil {
		return false
	}
	id := jobID(sel)
	switch {
	case tui.MatchesKey(data, "t"):
		if v.isSelectionForeign() {
			return false
		}
		enabled := !truthy(sel.Get("enabled"))
		v.update(id, sel, "enabled", enabled)
		v.refresh()
	case tui.MatchesKey(data, "s"):
		// Toggle this job's binding: session-bound or shared. Other sessions already running in this directory do not see the change
		// until they restart; their in-memory schedulers are stale until session_start.
		if v.isSelectionForeign() {
			return false
		}
		var session any = jsobj.Undefined
		if !truthy(sel.Get("session")) {
			session = v.mySessionID
		}
		v.update(id, sel, "session", session)
		v.refresh()
	case tui.MatchesKey(data, "x"):
		if v.isSelectionForeign() {
			return false
		}
		v.confirm = &confirmState{kind: "remove", id: id, name: jsStr(sel.Get("name"))}
	case tui.MatchesKey(data, "c"):
		count := 0
		for _, j := range v.mine {
			if !truthy(j.Get("enabled")) {
				count++
			}
		}
		if count == 0 {
			return false
		}
		v.confirm = &confirmState{kind: "cleanup", count: count}
	}
	return false
}

// update is `storage.updateJob(id, { key: value }); scheduler.updateJob(id, { ...sel, key: value })`.
func (v *jobsView) update(id string, sel *jsobj.Object, key string, value any) {
	partial := jsobj.New()
	partial.Set(key, value)
	if _, err := v.storage.UpdateJob(id, partial); err != nil {
		logf("[pi-schedule-prompt] Failed to update job %s: %v", id, err)
	}
	updated := sel.Clone()
	updated.Set(key, value)
	v.sched.updateJob(id, updated)
}

func jobsTruncate(s string, n int) string {
	if jsLen(s) > n {
		return jsSubstring(s, 0, n-3) + "..."
	}
	return s
}

// Render draws the overlay at width columns.
func (v *jobsView) Render(width int) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	th := v.theme
	total := len(v.mine) + len(v.foreign)
	rule := th.Fg("dim", strings.Repeat("─", max(1, width)))
	lines := []string{rule}

	// The header, or the confirm prompt in its place.
	switch {
	case v.confirm != nil && v.confirm.kind == "remove":
		lines = append(lines, th.Fg("warning", ` Remove "`+v.confirm.name+`"? (y/n)`))
	case v.confirm != nil && v.confirm.kind == "cleanup":
		lines = append(lines, th.Fg("warning", " Remove "+jsNumber(float64(v.confirm.count))+" disabled job(s) in this session? (y/n)"))
	case total == 0:
		lines = append(lines, " "+th.Fg("accent", th.Bold("Jobs"))+"  —  a add   q quit")
	default:
		lines = append(lines, " "+th.Fg("accent", th.Bold("Jobs"))+"  —  ↑↓ select   a add   t toggle   s scope   x remove   c cleanup   q quit")
	}
	lines = append(lines, rule)

	if total == 0 {
		lines = append(lines, "", "  No scheduled prompts. Press `a` to add one or `q` to quit.", "", rule)
		return lines
	}

	for i, j := range v.mine {
		lines = append(lines, v.formatRow(j, i, false))
	}
	if len(v.foreign) > 0 {
		lines = append(lines, "", th.Fg("dim", "  Other sessions (read-only):"))
		for i, j := range v.foreign {
			lines = append(lines, v.formatRow(j, len(v.mine)+i, true))
		}
	}

	if sel := v.selectedJob(); sel != nil {
		lines = append(lines, rule)
		next := math.NaN()
		if !v.isSelectionForeign() {
			next = v.sched.getNextRun(jobID(sel))
		}
		meta := []string{"Type: " + jsStr(sel.Get("type"))}
		if !math.IsNaN(next) {
			if iso, ok := isoString(next); ok {
				meta = append(meta, "Next: "+iso)
			}
		}
		if truthy(sel.Get("lastRun")) {
			meta = append(meta, "Last: "+jsStr(sel.Get("lastRun")))
		}
		meta = append(meta, "Runs: "+jsStr(sel.Get("runCount")))
		lines = append(lines,
			th.Fg("dim", " Selected: "+jsStr(sel.Get("name"))+" ("+jsStr(sel.Get("id"))+")"),
			th.Fg("dim", " "+strings.Join(meta, "   ")),
			th.Fg("dim", " Prompt: "+jobsTruncate(jsStr(sel.Get("prompt")), 200)))
	}
	lines = append(lines, rule)
	return lines
}

func (v *jobsView) formatRow(job *jsobj.Object, index int, foreign bool) string {
	th := v.theme
	indicator := " "
	if index == v.selected {
		indicator = "▶"
	}
	// Foreign rows are colorless: the "Other sessions" label and the dim details footer carry the cue, and a colored icon inside a
	// dim-wrapped string would nest ANSI sequences some terminals render inconsistently.
	var icon string
	status := job.Get("lastStatus")
	switch {
	case foreign:
		switch {
		case !truthy(job.Get("enabled")):
			icon = "✗"
		case strictEqualString(status, "error"):
			icon = "!"
		default:
			icon = "✓"
		}
	case !truthy(job.Get("enabled")):
		icon = th.Fg("muted", "✗")
	case strictEqualString(status, "running"):
		icon = th.Fg("warning", "⟳")
	case strictEqualString(status, "error"):
		icon = th.Fg("error", "!")
	default:
		icon = th.Fg("success", "✓")
	}
	name := jsPadEnd(jobsTruncate(jsStr(job.Get("name")), jobsNameW), jobsNameW)
	sched := jsPadEnd(jobsTruncate(describeSchedule(jsStr(job.Get("type")), jsStr(job.Get("schedule"))), jobsSchedW), jobsSchedW)
	prompt := jsPadEnd(jobsTruncate(jsStr(job.Get("prompt")), jobsPromptW), jobsPromptW)
	tag := ""
	if !truthy(job.Get("session")) && !foreign {
		tag = th.Fg("dim", " [shared]")
	}
	return " " + indicator + " " + icon + " " + name + "  " + sched + "  " + prompt + tag
}
