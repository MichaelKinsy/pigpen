package powerline_footer

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Twins of port/oracle/tests/editor-performance.test.ts, and the rest of editor-performance.ts's contract.

func perfEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func TestEditorPerformanceTwins(t *testing.T) {
	tw(t, "editor-performance", "editor profiling and A/B switches are opt-in", func(t *testing.T) {
		want := editorPerfOptions{Enabled: false, FastRender: true, EditorChrome: true, Widgets: true, BashWidgets: true, LastPrompt: true}
		if got := readEditorPerfOptions(perfEnv(map[string]string{})); got != want {
			t.Errorf("empty env = %+v, want %+v", got, want)
		}
		got := readEditorPerfOptions(perfEnv(map[string]string{
			"POWERLINE_DEBUG_PERF":         "1",
			"POWERLINE_PERF_FAST_RENDER":   "0",
			"POWERLINE_PERF_EDITOR_CHROME": "0",
			"POWERLINE_PERF_WIDGETS":       "0",
			"POWERLINE_PERF_BASH_WIDGETS":  "0",
			"POWERLINE_PERF_LAST_PROMPT":   "0",
		}))
		if got != (editorPerfOptions{Enabled: true}) {
			t.Errorf("all switches off = %+v", got)
		}
	})

	tw(t, "editor-performance", "editor profiler reports timings, counts, and draft size", func(t *testing.T) {
		profiler := newEditorPerfProfiler(readEditorPerfOptions(perfEnv(map[string]string{"POWERLINE_DEBUG_PERF": "1"})))
		if got := measureEditorPerf(profiler, "input.total", func() int { return 42 }); got != 42 {
			t.Errorf("measure returned %d, want 42", got)
		}
		profiler.count("editor.render.fast-hit")
		profiler.observeDraft([]string{"hello", "world"})
		report := profiler.report()
		for _, pattern := range []string{
			`inputs=1, editor-renders=0, renders/input=0\.00, max-draft=11`,
			`editor\.render\.fast-hit: n=1`,
			`input\.total: n=1, avg=`,
		} {
			if !regexp.MustCompile(pattern).MatchString(report) {
				t.Errorf("report does not match /%s/:\n%s", pattern, report)
			}
		}
	})
}

// perfClock scripts Date.now() and performance.now().
type perfClock struct{ wall, perf float64 }

func (c *perfClock) attach(p *editorPerfProfiler) {
	p.nowMs = func() float64 { return c.wall }
	p.perfMs = func() float64 { return c.perf }
	p.startedAt = c.wall
}

func TestEditorPerfOptionsOnlyAZeroSwitchesOffAndOnlyWhenEnabled(t *testing.T) {
	// A switch applies only when profiling is on: with POWERLINE_DEBUG_PERF unset every option stays on.
	got := readEditorPerfOptions(perfEnv(map[string]string{"POWERLINE_PERF_FAST_RENDER": "0", "POWERLINE_PERF_WIDGETS": "0"}))
	if !got.FastRender || !got.Widgets || got.Enabled {
		t.Errorf("switches without POWERLINE_DEBUG_PERF = %+v", got)
	}
	// Only "1" enables profiling; only "0" turns a switch off.
	for _, v := range []string{"", "0", "true", "yes", "2", " 1"} {
		if readEditorPerfOptions(perfEnv(map[string]string{"POWERLINE_DEBUG_PERF": v})).Enabled {
			t.Errorf("POWERLINE_DEBUG_PERF=%q enabled profiling", v)
		}
	}
	for _, v := range []string{"", "1", "off", "false"} {
		on := readEditorPerfOptions(perfEnv(map[string]string{"POWERLINE_DEBUG_PERF": "1", "POWERLINE_PERF_LAST_PROMPT": v}))
		if !on.LastPrompt {
			t.Errorf("POWERLINE_PERF_LAST_PROMPT=%q turned the switch off", v)
		}
	}
	// Each switch controls its own option.
	for name, field := range map[string]func(editorPerfOptions) bool{
		"POWERLINE_PERF_FAST_RENDER":   func(o editorPerfOptions) bool { return o.FastRender },
		"POWERLINE_PERF_EDITOR_CHROME": func(o editorPerfOptions) bool { return o.EditorChrome },
		"POWERLINE_PERF_WIDGETS":       func(o editorPerfOptions) bool { return o.Widgets },
		"POWERLINE_PERF_BASH_WIDGETS":  func(o editorPerfOptions) bool { return o.BashWidgets },
		"POWERLINE_PERF_LAST_PROMPT":   func(o editorPerfOptions) bool { return o.LastPrompt },
	} {
		o := readEditorPerfOptions(perfEnv(map[string]string{"POWERLINE_DEBUG_PERF": "1", name: "0"}))
		if field(o) {
			t.Errorf("%s=0 did not turn its option off: %+v", name, o)
		}
		on := 0
		for _, f := range []bool{o.FastRender, o.EditorChrome, o.Widgets, o.BashWidgets, o.LastPrompt} {
			if f {
				on++
			}
		}
		if on != 4 {
			t.Errorf("%s=0 changed %d other options: %+v", name, 5-on, o)
		}
	}
}

func TestEditorPerfOptionsReadTheProcessEnvironmentByDefault(t *testing.T) {
	t.Setenv("POWERLINE_DEBUG_PERF", "1")
	t.Setenv("POWERLINE_PERF_WIDGETS", "0")
	got := readEditorPerfOptions(nil)
	if !got.Enabled || got.Widgets || !got.FastRender {
		t.Errorf("readEditorPerfOptions(nil) = %+v", got)
	}
}

func TestEditorPerfReportFormat(t *testing.T) {
	clock := &perfClock{wall: 1_000_000}
	p := newEditorPerfProfiler(editorPerfOptions{Enabled: true, FastRender: true, EditorChrome: false, Widgets: true, BashWidgets: false, LastPrompt: true})
	clock.attach(p)

	// input.total twice: 2 ms and 4 ms. editor.render.total once: 1.25 ms (exact in binary, a tie for toFixed(1)).
	for _, ms := range []float64{2, 4} {
		measureEditorPerf(p, "input.total", func() struct{} { clock.perf += ms; return struct{}{} })
	}
	measureEditorPerf(p, "editor.render.total", func() struct{} { clock.perf += 1.25; return struct{}{} })
	p.count("editor.render.fast-hit")
	p.count("editor.render.fast-hit")
	p.observeDraft([]string{"abc", "de"})
	clock.wall += 12_340

	want := strings.Join([]string{
		"Powerline editor perf (12.3s)",
		"A/B: fast-render=on, editor-chrome=off, widgets=on, bash-widgets=off, last-prompt=on",
		"inputs=2, editor-renders=1, renders/input=0.50, max-draft=6",
		"editor.render.fast-hit: n=2",
		"editor.render.total: n=1, avg=1.250ms, max=1.250ms, total=1.3ms", // (1.25).toFixed(1) is "1.3": a tie goes up
		"input.total: n=2, avg=3.000ms, max=4.000ms, total=6.0ms",
	}, "\n")
	if got := p.report(); got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

func TestEditorPerfCountDoesNotAddTime(t *testing.T) {
	clock := &perfClock{}
	p := newEditorPerfProfiler(editorPerfOptions{})
	clock.attach(p)
	p.count("a")
	measureEditorPerf(p, "a", func() struct{} { return struct{}{} }) // a timed call of zero length keeps the plain form
	if got := p.report(); !strings.Contains(got, "\na: n=2\n") && !strings.HasSuffix(got, "\na: n=2") {
		t.Errorf("a metric whose total and max are zero reads n only:\n%s", got)
	}
}

func TestEditorPerfMeasureRecordsWhenTheOperationPanics(t *testing.T) {
	clock := &perfClock{}
	p := newEditorPerfProfiler(editorPerfOptions{})
	clock.attach(p)
	func() {
		defer func() { _ = recover() }()
		measureEditorPerf(p, "boom", func() int { clock.perf += 5; panic("x") })
	}()
	if got := p.report(); !strings.Contains(got, "boom: n=1, avg=5.000ms, max=5.000ms, total=5.0ms") {
		t.Errorf("a failing operation is still timed (try/finally):\n%s", got)
	}
}

func TestEditorPerfReportSortsNamesLikeLocaleCompare(t *testing.T) {
	p := newEditorPerfProfiler(editorPerfOptions{})
	for _, name := range []string{"widget.powerline-status", "input.total", "editor.render.fast-probe", "Editor.render.base", "editor.render.fast-hit", "editor.render.base", "layout.segment-context", "input.base-editor", "widget.powerline-top"} {
		p.count(name)
	}
	var names []string
	for _, line := range strings.Split(p.report(), "\n")[3:] {
		names = append(names, strings.SplitN(line, ":", 2)[0])
	}
	want := []string{"editor.render.base", "Editor.render.base", "editor.render.fast-hit", "editor.render.fast-probe", "input.base-editor", "input.total", "layout.segment-context", "widget.powerline-status", "widget.powerline-top"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("order = %q\nwant    %q", names, want)
	}
}

func TestJSLocaleCompareMatchesICUOnASCII(t *testing.T) {
	// Expected values produced by Node 24 (ICU 78) `a.localeCompare(b)`.
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"a", "A", -1}, {"A", "b", -1}, {"a-b", "a.b", -1}, {"ab", "a-b", 1}, {"a", "a", 0}, {"a", "ab", -1}, {"ab", "a", 1},
		{"a.b", "a-b", 1}, {"a_b", "a-b", -1}, {"1", "a", -1}, {"B", "a", 1}, {"aB", "Ab", -1}, {"x y", "x-y", -1},
	} {
		if got := jsLocaleCompare(c.a, c.b); got != c.want {
			t.Errorf("jsLocaleCompare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestEditorPerfObserveDraft(t *testing.T) {
	p := newEditorPerfProfiler(editorPerfOptions{})
	maxDraft := func() int {
		var n int
		for _, line := range strings.Split(p.report(), "\n") {
			if rest, ok := strings.CutPrefix(line, "inputs=0, editor-renders=0, renders/input=0.00, max-draft="); ok {
				n = atoiOrFail(t, rest)
			}
		}
		return n
	}
	// A one-line draft is measured on every observation; a longer one only on observations 1, 33, 65...
	p.observeDraft([]string{"héllo😀"}) // h é l l o = 5 units, the astral character 2
	if got := maxDraft(); got != 7 {
		t.Errorf("one line = %d, want 7 UTF-16 units", got)
	}
	p.reset()
	p.observeDraft([]string{"ab", "cd", "ef"}) // 1st observation: counted, 2+2+2 chars + 2 newlines
	if got := maxDraft(); got != 8 {
		t.Errorf("first multi-line observation = %d, want 8", got)
	}
	p.reset()
	p.observeDraft([]string{"x"})                // 1st: counted (1)
	p.observeDraft([]string{"a", "b", "c", "d"}) // 2nd: multi-line, skipped
	if got := maxDraft(); got != 1 {
		t.Errorf("a multi-line draft on observation 2 is skipped, got %d", got)
	}
	for i := 3; i <= 32; i++ {
		p.observeDraft([]string{"a", "b", "c", "d"})
	}
	if got := maxDraft(); got != 1 {
		t.Errorf("observations 3..32 are skipped, got %d", got)
	}
	p.observeDraft([]string{"a", "b", "c", "d"}) // 33rd: counted: 4 chars + 3 newlines
	if got := maxDraft(); got != 7 {
		t.Errorf("observation 33 is counted, got %d", got)
	}
	p.observeDraft(nil) // an empty draft: max(0, -1) = 0 newlines
	if got := maxDraft(); got != 7 {
		t.Errorf("an empty draft never lowers the maximum, got %d", got)
	}
}

func TestEditorPerfResetClearsEverythingAndRestartsTheClock(t *testing.T) {
	clock := &perfClock{wall: 5_000}
	p := newEditorPerfProfiler(editorPerfOptions{Enabled: true})
	clock.attach(p)
	p.count("x")
	p.observeDraft([]string{"abc"})
	clock.wall += 20_000
	p.reset()
	clock.wall += 1_500
	want := "Powerline editor perf (1.5s)\nA/B: fast-render=off, editor-chrome=off, widgets=off, bash-widgets=off, last-prompt=off\ninputs=0, editor-renders=0, renders/input=0.00, max-draft=0"
	if got := p.report(); got != want {
		t.Errorf("report after reset =\n%s\nwant\n%s", got, want)
	}
	// The draft counter restarts too: the next multi-line draft is the first observation again.
	p.observeDraft([]string{"a", "b"})
	if got := p.report(); !strings.Contains(got, "max-draft=3") {
		t.Errorf("after reset the first multi-line draft is counted:\n%s", got)
	}
}

func TestEditorPerfRendersPerInput(t *testing.T) {
	clock := &perfClock{}
	p := newEditorPerfProfiler(editorPerfOptions{})
	clock.attach(p)
	for i := 0; i < 4; i++ {
		measureEditorPerf(p, "input.total", func() int { clock.perf++; return 0 })
	}
	for i := 0; i < 3; i++ {
		measureEditorPerf(p, "editor.render.total", func() int { clock.perf++; return 0 })
	}
	if got := p.report(); !strings.Contains(got, "inputs=4, editor-renders=3, renders/input=0.75, max-draft=0") {
		t.Errorf("report:\n%s", got)
	}
}

func TestEditorPerfProfilerOptionsAreKept(t *testing.T) {
	o := editorPerfOptions{Enabled: true, Widgets: true}
	if got := newEditorPerfProfiler(o).options; got != o {
		t.Errorf("options = %+v, want %+v", got, o)
	}
}

func atoiOrFail(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}
