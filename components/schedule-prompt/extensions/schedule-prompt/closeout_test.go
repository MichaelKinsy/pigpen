package schedule_prompt

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsobj"
)

// Assertions that pin the mutation run's survivors: each test names the behavior of the original it holds.

func pad(s string, n int) string { return s + strings.Repeat(" ", n-len([]rune(s))) }

// ---- the widget's rows, header and refresh ----

func TestWidgetRowColumns(t *testing.T) {
	loc := time.FixedZone("x", 2*3600)
	old := jsdate.Location()
	jsdate.SetLocation(loc)
	defer jsdate.SetLocation(old)
	lastRun := "2026-01-01T00:00:00.000Z"
	now := jsdate.Parse(lastRun) + 5*60_000
	short := exampleJob(obj{"id": "a", "name": "alpha", "type": "interval", "schedule": "30s", "prompt": "short"})
	long := exampleJob(obj{"id": "b", "name": "a-name-of-sixteen", "type": "interval", "schedule": "1234567890123456789", "prompt": "a prompt of exactly thirty chars", "lastRun": lastRun, "runCount": float64(12)})
	once := exampleJob(obj{"id": "c", "name": "once", "type": "once", "schedule": "2026-02-13T15:30:00.000Z", "prompt": "p"})
	rows := widgetRows(tagTheme{}, []*jsobj.Object{short, long, once}, stubScheduler{}, now)
	fg := func(token, text string) string { return "<fg:" + token + ">" + text + "</fg>" }
	want := []string{
		" " + fg("success", "✓") + " " + fg("text", pad("alpha", 15)) + " " + fg("dim", pad("30s", 15)) + " " + fg("dim", pad("short", 25)) + " " + pad("-", 10) + " " + fg("dim", pad("never", 10)) + " " + fg("accent", pad("0", 3)),
		" " + fg("success", "✓") + " " + fg("text", "a-name-of-s...") + " " + fg("dim", "123456789012...") + " " + fg("dim", "a prompt of exactly th...") + " " + pad("-", 10) + " " + pad("5m ago", 10) + " " + fg("accent", pad("12", 3)),
		" " + fg("success", "✓") + " " + fg("text", pad("once", 15)) + " " + fg("dim", pad("Feb 13 17:30", 15)) + " " + fg("dim", pad("p", 25)) + " " + pad("-", 10) + " " + fg("dim", pad("never", 10)) + " " + fg("accent", pad("0", 3)),
	}
	// the name cut keeps 12 characters and adds "...": 15 in all
	want[1] = strings.Replace(want[1], "a-name-of-s...", "a-name-of-si...", 1)
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d:\n got %q\nwant %q", i, rows[i], want[i])
		}
	}
}

func TestWidgetHeaderAndTitle(t *testing.T) {
	jobs := []*jsobj.Object{exampleJob(obj{"id": "a"}), exampleJob(obj{"id": "b", "name": "b"})}
	view := widgetView(tagTheme{}, jobs, stubScheduler{}, jsdate.Now())
	children := view.Root.(*kit.Container).Children
	if len(children) != 5 {
		t.Fatalf("children = %d", len(children))
	}
	if got := children[1].(*kit.Text).Text; got != "<fg:accent><b>Scheduled Prompts</b></fg><fg:dim> (2 jobs)</fg>" {
		t.Fatalf("header = %q", got)
	}
}

func TestUniqueJobsKeepsTheFirstPositionAndTheLastValue(t *testing.T) {
	first := exampleJob(obj{"id": "a", "name": "first"})
	other := exampleJob(obj{"id": "b", "name": "other"})
	last := exampleJob(obj{"id": "a", "name": "last"})
	got := uniqueJobs([]*jsobj.Object{first, other, last})
	if len(got) != 2 || got[0].Get("name") != "last" || got[1].Get("name") != "other" {
		t.Fatalf("uniqueJobs = %v", got)
	}
}

func TestWidgetRefreshInterval(t *testing.T) {
	if widgetRefresh != 30*time.Second {
		t.Fatalf("the relative times are refreshed every 30 s, not %v", widgetRefresh)
	}
}

func TestHidingTheWidgetStopsItsTicker(t *testing.T) {
	visible := true
	w := newCronWidget(makeStorage(exampleJob(obj{"session": "s1"})), stubScheduler{}, func() bool { return visible }, "s1")
	ui := &fakeWidgetUI{}
	ticking := func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.ticker != nil
	}
	w.show(ui)
	if !ticking() {
		t.Fatal("no ticker after the first mount")
	}
	visible = false
	w.refresh()
	if ticking() {
		t.Fatal("the ticker keeps running behind a hidden widget")
	}
	w.destroy()
}

// ---- the renderers' colors ----

func TestSubagentErrorMarkerIsRed(t *testing.T) {
	msg := map[string]any{"details": map[string]any{"mode": "subagent_error", "jobName": "j", "model": "m", "error": "boom"}}
	lines := scheduledPromptLines(tagTheme{}, msg, 80)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "<fg:error>✗ Scheduled (subagent: m) failed: j") {
		t.Fatalf("lines = %q", lines)
	}
}

// ---- the scheduler ----

func TestFireMarksTheJobRunningBeforeItSendsThePrompt(t *testing.T) {
	job := exampleJob(obj{"type": "cron", "schedule": "0 0 0 1 1 *"})
	st := makeStorage(job)
	var seen any
	pi := &mockPi{sendError: func(int) error {
		j, _ := st.GetJob("job-1")
		seen = j.Get("lastStatus")
		return nil
	}}
	s := newSched(st, pi, makeCtx(), nil)
	s.scheduleJob(job)
	defer s.stop()
	s.executeJob(job)
	if seen != "running" {
		t.Fatalf("lastStatus while the prompt was sent = %v", seen)
	}
	if j, _ := st.GetJob("job-1"); j.Get("lastStatus") != "success" {
		t.Fatalf("lastStatus after the fire = %v", j.Get("lastStatus"))
	}
}

// A one-shot due exactly now is already past: Date.now() >= target is not a future delay (the clock is pinned to the target).
func TestOnceDueExactlyNowIsPast(t *testing.T) {
	target := "2026-06-01T12:00:00.000Z"
	old := schedulerNow
	schedulerNow = func() float64 { return jsdate.Parse(target) }
	defer func() { schedulerNow = old }()
	job := exampleJob(obj{"type": "once", "schedule": target})
	st := makeStorage(job)
	pi := &mockPi{}
	s := newSched(st, pi, makeCtx(), nil)
	logged := captureLog(t)
	s.scheduleJob(job)
	defer s.stop()
	j, _ := st.GetJob("job-1")
	if j.Get("enabled") != false || j.Get("lastStatus") != "error" {
		t.Fatalf("job = %v", j)
	}
	if got := strings.Join(eventTypes(pi), ","); got != "error" {
		t.Fatalf("events = %s", got)
	}
	if l := logged(); len(l) != 1 || !strings.Contains(l[0], "scheduled for past time") {
		t.Fatalf("log = %q", l)
	}
	s.mu.Lock()
	n := len(s.intervals)
	s.mu.Unlock()
	if n != 0 {
		t.Fatal("a timer was set for a time that is not in the future")
	}
}

// ---- settings ----

func TestMalformedSettingsAreIgnoredWithAWarning(t *testing.T) {
	logged := captureLog(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readSettings(path); got.widgetVisible != nil || got.defaultJobScope != "" {
		t.Fatalf("settings = %+v", got)
	}
	l := logged()
	if len(l) != 1 || !strings.HasPrefix(l[0], "[pi-schedule-prompt] Ignoring malformed settings at "+path+": ") {
		t.Fatalf("log = %q", l)
	}
}

// ---- the JavaScript helpers ----

func TestNanoidUsesTheWholeAlphabet(t *testing.T) {
	seen := map[rune]bool{}
	for i := 0; i < 400; i++ {
		id := nanoid(100)
		if len(id) != 100 {
			t.Fatalf("len = %d", len(id))
		}
		for _, r := range id {
			if !strings.ContainsRune(nanoidAlphabet, r) {
				t.Fatalf("%q is not in the alphabet", r)
			}
			seen[r] = true
		}
	}
	if len(seen) != 64 || len(nanoidAlphabet) != 64 {
		t.Fatalf("%d of %d characters seen in 40000", len(seen), len(nanoidAlphabet))
	}
}

func TestJsTruthiness(t *testing.T) {
	for v, want := range map[string]bool{"0": false, "nan": false, "1": true, "-1": true, "0.5": true} {
		var f float64
		switch v {
		case "nan":
			f = math.NaN()
		case "0":
			f = 0
		case "1":
			f = 1
		case "-1":
			f = -1
		default:
			f = 0.5
		}
		if got := truthy(f); got != want {
			t.Errorf("truthy(%s) = %v", v, got)
		}
	}
	if truthy("") || !truthy("0") || truthy(nil) || truthy(false) || !truthy(true) || truthy(jsobj.Undefined) {
		t.Error("truthy on the other types")
	}
}

func TestJsNumberToString(t *testing.T) {
	for f, want := range map[float64]string{
		0: "0", 1: "1", -1.5: "-1.5", 1e15: "1000000000000000", 123456789012345680000: "123456789012345680000", 1e20: "100000000000000000000",
		1e21: "1e+21", 1.5e22: "1.5e+22", 1e-6: "0.000001", 1.5e-7: "1.5e-7", 1e-7: "1e-7",
	} {
		if got := jsNumber(f); got != want {
			t.Errorf("jsNumber(%v) = %q, want %q", f, got, want)
		}
	}
	if jsStr(nil) != "null" || jsStr(true) != "true" || jsStr(float64(3)) != "3" {
		t.Error("jsStr")
	}
}

// ---- the subagent's model lookup and answer ----

func TestResolveModelOrder(t *testing.T) {
	mini := map[string]any{"provider": "x", "id": "gpt-4o-mini", "name": "Mini"}
	full := map[string]any{"provider": "x", "id": "gpt-4o", "name": "Full"}
	haiku := map[string]any{"provider": "y", "id": "model-1", "name": "Claude Haiku"}
	reg := registryOf(mini, full, haiku)
	for _, c := range []struct {
		needle string
		want   map[string]any
	}{
		{"gpt-4o", full},      // the exact id wins over an earlier substring match
		{"GPT-4O-MIN", mini},  // case-insensitive, a substring of no other id
		{"4o-m", mini},        // a substring of an id
		{"haiku", haiku},      // the name, when no id matches
		{"y/haiku", haiku},    // the slash form falls back to the name too
		{"nothing-like", nil}, // no match
	} {
		got, err := resolveModel(reg, c.needle)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("resolveModel(%q) = %v, %v; want %v", c.needle, got, err, c.want)
		}
	}
}

func TestDescribeAvailableModelsListsFiveAndCountsTheRest(t *testing.T) {
	var models []map[string]any
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		models = append(models, map[string]any{"provider": "p", "id": id})
	}
	got, err := describeAvailableModels(registryOf(models[:5]...))
	if err != nil || got != "Available: p/a, p/b, p/c, p/d, p/e" {
		t.Fatalf("five: %q %v", got, err)
	}
	got, err = describeAvailableModels(registryOf(models...))
	if err != nil || got != "Available: p/a, p/b, p/c, p/d, p/e, … (1 more)" {
		t.Fatalf("six: %q %v", got, err)
	}
}

func TestLastAssistantTextTakesOnlyTextParts(t *testing.T) {
	msgs := []map[string]any{{"role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "text": "hidden"},
		map[string]any{"type": "text", "text": "shown"},
		map[string]any{"type": "toolCall", "text": "also hidden"},
	}}}
	if got := getLastAssistantText(msgs); got != "shown" {
		t.Fatalf("text = %q", got)
	}
}

// ---- the jobs overlay ----

func newTestJobsView(jobs ...*jsobj.Object) (*jobsView, *memStorage) {
	st := makeStorage(jobs...)
	return newJobsView(st, stubScheduler{}, "me", func() error { return nil }, tagTheme{}), st
}

func TestJobsOverlayRuleIsAtLeastOneColumn(t *testing.T) {
	v, _ := newTestJobsView()
	for _, w := range []int{1, 0} {
		if lines := v.Render(w); lines[0] != "<fg:dim>─</fg>" {
			t.Errorf("width %d: rule %q", w, lines[0])
		}
	}
}

func TestJobsCleanupCountsAndRemovesOnlyTheDisabledOnesOfThisSession(t *testing.T) {
	live := exampleJob(obj{"id": "live", "name": "live"})
	off1 := exampleJob(obj{"id": "off1", "name": "off1", "enabled": false})
	off2 := exampleJob(obj{"id": "off2", "name": "off2", "enabled": false, "session": "me"})
	foreign := exampleJob(obj{"id": "foreign", "name": "foreign", "enabled": false, "session": "other"})
	v, st := newTestJobsView(live, off1, off2, foreign)
	if _, err := v.HandleInput("c"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(v.Render(100), "\n"); !strings.Contains(got, "Remove 2 disabled job(s) in this session? (y/n)") {
		t.Fatalf("no prompt for two jobs:\n%s", got)
	}
	if _, err := v.HandleInput("y"); err != nil {
		t.Fatal(err)
	}
	jobs, _ := st.GetAllJobs()
	var ids []string
	for _, j := range jobs {
		ids = append(ids, jobID(j))
	}
	if !reflect.DeepEqual(ids, []string{"live", "foreign"}) {
		t.Fatalf("jobs left = %v", ids)
	}
	// nothing disabled is left in this session: the key does nothing
	if _, err := v.HandleInput("c"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(v.Render(100), "\n"); strings.Contains(got, "Remove") {
		t.Fatalf("a prompt for no job:\n%s", got)
	}
}

// ---- the command's overlay ----

type fakeOverlay struct{ calls []string }

func (f *fakeOverlay) SetHidden(h bool) error {
	if h {
		f.calls = append(f.calls, "hide")
	} else {
		f.calls = append(f.calls, "show")
	}
	return nil
}
func (f *fakeOverlay) Focus() error { f.calls = append(f.calls, "focus"); return nil }

func TestTheOverlayIsHiddenWhileTheAddFlowRunsAndFocusedAfter(t *testing.T) {
	o := &fakeOverlay{}
	var during []string
	wrapped := hideWhileAdding(func() overlayControl { return o }, func() error {
		during = append([]string(nil), o.calls...)
		return context.Canceled
	})
	if err := wrapped(); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(during, []string{"hide"}) || !reflect.DeepEqual(o.calls, []string{"hide", "show", "focus"}) {
		t.Fatalf("during %v, in all %v", during, o.calls)
	}
	// before the host has handed the overlay over, the flow runs on its own
	ran := false
	if err := hideWhileAdding(func() overlayControl { return nil }, func() error { ran = true; return nil })(); err != nil || !ran {
		t.Fatalf("ran %v err %v", ran, err)
	}
}

func TestTheJobsOverlayTakesTheWholeTerminal(t *testing.T) {
	o := jobsOverlayOptions()
	if *o.Width != (sdk.OverlaySize{Value: 100, Percent: true}) || *o.MaxHeight != (sdk.OverlaySize{Value: 100, Percent: true}) {
		t.Fatalf("options = %+v", o)
	}
}

func TestWidgetStatusIcons(t *testing.T) {
	jobs := []*jsobj.Object{
		exampleJob(obj{"id": "a", "enabled": false}),
		exampleJob(obj{"id": "b", "lastStatus": "running"}),
		exampleJob(obj{"id": "c", "lastStatus": "error"}),
		exampleJob(obj{"id": "d", "lastStatus": "success"}),
	}
	rows := widgetRows(tagTheme{}, jobs, stubScheduler{}, jsdate.Now())
	for i, icon := range []string{"<fg:muted>✗</fg>", "<fg:warning>⟳</fg>", "<fg:error>!</fg>", "<fg:success>✓</fg>"} {
		if !strings.HasPrefix(rows[i], " "+icon+" ") {
			t.Errorf("row %d = %q, want the icon %s", i, rows[i], icon)
		}
	}
}
