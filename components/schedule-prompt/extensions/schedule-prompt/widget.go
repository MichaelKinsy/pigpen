package schedule_prompt

import (
	"math"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// The live widget below the editor (upstream: ui/cron-widget.ts CronWidget): every job this session loads, one row each, relative times
// refreshed every 30 seconds and on every change event.

const widgetID = "schedule-prompts"

// widgetRefresh is the interval of the original's relative-time refresh.
const widgetRefresh = 30 * time.Second

// widgetUI is the part of ctx.ui the widget uses.
type widgetUI interface {
	// SetWidget shows the view below the editor, or clears the widget for a nil view.
	SetWidget(key string, view *kit.View, placement string)
	Theme() sdk.UITheme
}

// formatRelativeTime renders a time as "in 5m" or "2h ago".
func formatRelativeTime(target float64, now float64) string {
	diff := target - now
	abs := math.Abs(diff)
	seconds := math.Floor(abs / 1000)
	minutes := math.Floor(seconds / 60)
	hours := math.Floor(minutes / 60)
	days := math.Floor(hours / 24)
	var timeStr string
	switch {
	case days > 0:
		timeStr = jsNumber(days) + "d"
	case hours > 0:
		timeStr = jsNumber(hours) + "h"
	case minutes > 0:
		timeStr = jsNumber(minutes) + "m"
	default:
		timeStr = jsNumber(seconds) + "s"
	}
	if diff > 0 {
		return "in " + timeStr
	}
	return timeStr + " ago"
}

type cronWidget struct {
	mu        sync.Mutex
	storage   jobStore
	sched     jobScheduler
	isVisible func() bool
	sessionID string
	ui        widgetUI
	ticker    chan struct{}
	gen       uint64
}

func newCronWidget(storage jobStore, sched jobScheduler, isVisible func() bool, sessionID string) *cronWidget {
	return &cronWidget{storage: storage, sched: sched, isVisible: isVisible, sessionID: sessionID}
}

// loadedJobs are the jobs this session loads, the scheduler's predicate.
func (w *cronWidget) loadedJobs() []*jsobj.Object {
	all, err := w.storage.GetAllJobs()
	if err != nil {
		logf("Failed to read the scheduled prompts: %v", err)
		return nil
	}
	var out []*jsobj.Object
	for _, j := range all {
		if isLoadedFor(j, w.sessionID) {
			out = append(out, j)
		}
	}
	return out
}

// show mounts the widget, or hides it when it is not visible or there is no job; the 30 s refresh starts with the first mount.
func (w *cronWidget) show(ui widgetUI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.showLocked(ui)
}

func (w *cronWidget) showLocked(ui widgetUI) {
	w.ui = ui
	jobs := w.loadedJobs()
	if !w.isVisible() || len(jobs) == 0 {
		w.hideLocked(ui)
		return
	}
	view := widgetView(ui.Theme(), uniqueJobs(jobs), w.sched, jsdate.Now())
	ui.SetWidget(widgetID, &view, "belowEditor")
	if w.ticker == nil {
		w.ticker = make(chan struct{})
		go w.tick(w.ticker)
	}
}

func (w *cronWidget) tick(done chan struct{}) {
	t := time.NewTicker(widgetRefresh)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			w.refresh()
		case <-done:
			return
		}
	}
}

func (w *cronWidget) hide(ui widgetUI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hideLocked(ui)
}

func (w *cronWidget) hideLocked(ui widgetUI) {
	ui.SetWidget(widgetID, nil, "")
	if w.ticker != nil {
		close(w.ticker)
		w.ticker = nil
	}
}

// refresh mounts the widget again against the latest storage state; show handles the visibility, empty-list and first-mount cases alike.
func (w *cronWidget) refresh() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ui != nil {
		w.showLocked(w.ui)
	}
}

func (w *cronWidget) destroy() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ticker != nil {
		close(w.ticker)
		w.ticker = nil
	}
	w.ui = nil
}

// uniqueJobs deduplicates by id, as the original's Map does: the first position, the last value.
func uniqueJobs(jobs []*jsobj.Object) []*jsobj.Object {
	idx := map[string]int{}
	var out []*jsobj.Object
	for _, j := range jobs {
		id := jobID(j)
		if i, ok := idx[id]; ok {
			out[i] = j
			continue
		}
		idx[id] = len(out)
		out = append(out, j)
	}
	return out
}

func truncCol(s string, width, keep int) string {
	if jsLen(s) > width {
		return jsSubstring(s, 0, keep) + "..."
	}
	return s
}

// widgetRows are the job rows of the widget (the column layout of the original).
func widgetRows(theme sdk.UITheme, jobs []*jsobj.Object, sched jobScheduler, now float64) []string {
	rows := make([]string, 0, len(jobs))
	for _, job := range jobs {
		enabled := truthy(job.Get("enabled"))
		var statusIcon string
		switch {
		case !enabled:
			statusIcon = theme.Fg("muted", "✗")
		case strictEqualString(job.Get("lastStatus"), "running"):
			statusIcon = theme.Fg("warning", "⟳")
		case strictEqualString(job.Get("lastStatus"), "error"):
			statusIcon = theme.Fg("error", "!")
		default:
			statusIcon = theme.Fg("success", "✓")
		}

		// Columns are cut and padded before they are colored.
		name := jsPadEnd(truncCol(jsStr(job.Get("name")), 15, 12), 15)
		nameText := theme.Fg("muted", name)
		if enabled {
			nameText = theme.Fg("text", name)
		}

		var schedule string
		schedStr := jsStr(job.Get("schedule"))
		switch {
		case strictEqualString(job.Get("type"), "cron"):
			schedule = humanizeCron(schedStr)
		case strictEqualString(job.Get("type"), "once") && containsT(schedStr):
			schedule = formatISOShort(schedStr)
		default:
			schedule = schedStr
		}
		scheduleText := theme.Fg("dim", jsPadEnd(truncCol(schedule, 15, 12), 15))

		promptText := theme.Fg("dim", jsPadEnd(truncCol(jsStr(job.Get("prompt")), 25, 22), 25))

		nextRaw := "-"
		if next := sched.getNextRun(jobID(job)); !math.IsNaN(next) {
			nextRaw = formatRelativeTime(next, now)
		}
		nextText := jsPadEnd(nextRaw, 10)

		lastRaw := "never"
		if truthy(job.Get("lastRun")) {
			lastRaw = formatRelativeTime(toDate(job.Get("lastRun")), now)
		}
		lastPadded := jsPadEnd(lastRaw, 10)
		lastText := theme.Fg("dim", lastPadded)
		if truthy(job.Get("lastRun")) {
			lastText = lastPadded
		}

		count := job.Get("runCount")
		if isNullish(count) {
			count = float64(0)
		}
		countText := theme.Fg("accent", jsPadEnd(jsStr(count), 3))

		// A model badge only for a job that runs in a subagent; the trailing "!" marks one that wakes the parent when it finishes.
		badge := ""
		if truthy(job.Get("model")) {
			model := truncCol(jsStr(job.Get("model")), 12, 9)
			bang := ""
			if truthy(job.Get("notify")) {
				bang = "!"
			}
			badge = " " + theme.Fg("accent", "["+model+bang+"]")
		}
		rows = append(rows, " "+statusIcon+" "+nameText+" "+scheduleText+" "+promptText+" "+nextText+" "+lastText+" "+countText+badge)
	}
	return rows
}

func containsT(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 'T' {
			return true
		}
	}
	return false
}

// widgetView is the component tree of the original's renderWidget: a border, the header, a spacer, the rows and a border.
func widgetView(theme sdk.UITheme, jobs []*jsobj.Object, sched jobScheduler, now float64) kit.View {
	rows := widgetRows(theme, jobs, sched, now)
	header := theme.Fg("accent", theme.Bold("Scheduled Prompts")) + theme.Fg("dim", " ("+jsNumber(float64(len(jobs)))+" jobs)")
	text := ""
	for i, r := range rows {
		if i > 0 {
			text += "\n"
		}
		text += r
	}
	return kit.View{Root: kit.NewContainer(
		kit.NewDynamicBorder("accent"),
		kit.NewText(header, 1, 0),
		kit.NewSpacer(1),
		kit.NewText(text, 1, 0),
		kit.NewDynamicBorder("accent"),
	)}
}

// sdkWidgetUI draws on a retained host context. In Pi's RPC mode a component widget is not rendered (only its removal reaches the client),
// so the view is not sent there either.
type sdkWidgetUI struct{ ctx sdk.Context }

func (u sdkWidgetUI) SetWidget(key string, view *kit.View, placement string) {
	if !u.ctx.HasUI() {
		return
	}
	if view == nil {
		_ = u.ctx.SetWidget(key, nil)
		return
	}
	if u.ctx.Mode() == "rpc" {
		return
	}
	_ = u.ctx.SetWidget(key, *view, sdk.WidgetOptions{"placement": placement})
}

func (u sdkWidgetUI) Theme() sdk.UITheme { return u.ctx.UITheme() }
