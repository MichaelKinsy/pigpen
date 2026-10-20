package powerline_footer

import (
	"cmp"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
)

// The editor performance profiler's options and counters. upstream: editor-performance.ts.
//
// The profiler measures the editor's render and input (index.ts 3323-3500), which needs a Go-reachable editor, and the
// SDK's setEditorComponent (PiG v0.5.0 returns ErrUnsupportedSubprocessUI for it). Until that exists only the options are used:
// refusal.go reads them to refuse POWERLINE_DEBUG_PERF, and /powerline-perf stays refused. The profiler is kept here, proven
// by twins, so the editor port finds it done.

// editorPerfOptions is EditorPerfOptions: the profiling switch and the A/B switches that turn one editor feature off.
type editorPerfOptions struct {
	Enabled      bool
	FastRender   bool
	EditorChrome bool
	Widgets      bool
	BashWidgets  bool
	LastPrompt   bool
}

// readEditorPerfOptions is readEditorPerfOptions(env): profiling is opt-in (POWERLINE_DEBUG_PERF=1) and each A/B switch turns
// its feature off only with "0", and only while profiling. A nil env reads the process environment.
func readEditorPerfOptions(env func(string) string) editorPerfOptions {
	if env == nil {
		env = os.Getenv
	}
	enabled := env("POWERLINE_DEBUG_PERF") == "1"
	on := func(name string) bool { return !enabled || env(name) != "0" }
	return editorPerfOptions{
		Enabled:      enabled,
		FastRender:   on("POWERLINE_PERF_FAST_RENDER"),
		EditorChrome: on("POWERLINE_PERF_EDITOR_CHROME"),
		Widgets:      on("POWERLINE_PERF_WIDGETS"),
		BashWidgets:  on("POWERLINE_PERF_BASH_WIDGETS"),
		LastPrompt:   on("POWERLINE_PERF_LAST_PROMPT"),
	}
}

type editorPerfMetric struct {
	count   int
	totalMs float64
	maxMs   float64
}

// editorPerfProfiler is EditorPerfProfiler. It is not safe for concurrent use, as the original is single-threaded.
type editorPerfProfiler struct {
	options               editorPerfOptions
	metrics               map[string]*editorPerfMetric
	startedAt             float64 // Date.now() at construction or reset, in ms
	maxDraftChars         int
	draftObservationCount int

	nowMs  func() float64 // Date.now()
	perfMs func() float64 // performance.now()
}

var perfOrigin = time.Now()

func newEditorPerfProfiler(options editorPerfOptions) *editorPerfProfiler {
	p := &editorPerfProfiler{
		options: options,
		metrics: map[string]*editorPerfMetric{},
		nowMs:   func() float64 { return float64(time.Now().UnixMilli()) },
		perfMs:  func() float64 { return float64(time.Since(perfOrigin)) / float64(time.Millisecond) },
	}
	p.startedAt = p.nowMs()
	return p
}

// measureEditorPerf is measure(name, operation): the operation's time is recorded even when it panics (try/finally). Go methods
// cannot take type parameters, hence the function.
func measureEditorPerf[T any](p *editorPerfProfiler, name string, operation func() T) T {
	startedAt := p.perfMs()
	defer func() { p.record(name, p.perfMs()-startedAt) }()
	return operation()
}

func (p *editorPerfProfiler) metric(name string) *editorPerfMetric {
	m := p.metrics[name]
	if m == nil {
		m = &editorPerfMetric{}
		p.metrics[name] = m
	}
	return m
}

// count adds one to a counter without a duration.
func (p *editorPerfProfiler) count(name string) { p.metric(name).count++ }

// observeDraft tracks the longest draft in UTF-16 units. A draft of several lines is measured on every 32nd observation only
// (the first, 33rd, 65th ...), to keep the profiler cheap while typing.
func (p *editorPerfProfiler) observeDraft(lines []string) {
	p.draftObservationCount++
	if len(lines) > 1 && p.draftObservationCount%32 != 1 {
		return
	}
	chars := max(0, len(lines)-1)
	for _, line := range lines {
		chars += utf16Len(line)
	}
	p.maxDraftChars = max(p.maxDraftChars, chars)
}

func (p *editorPerfProfiler) reset() {
	p.metrics = map[string]*editorPerfMetric{}
	p.startedAt = p.nowMs()
	p.maxDraftChars = 0
	p.draftObservationCount = 0
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// report is the text /powerline-perf shows.
func (p *editorPerfProfiler) report() string {
	inputCount, renderCount := 0, 0
	if m := p.metrics["input.total"]; m != nil {
		inputCount = m.count
	}
	if m := p.metrics["editor.render.total"]; m != nil {
		renderCount = m.count
	}
	perInput := "0.00"
	if inputCount > 0 {
		perInput = jsToFixed(float64(renderCount)/float64(inputCount), 2)
	}
	o := p.options
	lines := []string{
		"Powerline editor perf (" + jsToFixed((p.nowMs()-p.startedAt)/1000, 1) + "s)",
		"A/B: fast-render=" + onOff(o.FastRender) + ", editor-chrome=" + onOff(o.EditorChrome) + ", widgets=" + onOff(o.Widgets) +
			", bash-widgets=" + onOff(o.BashWidgets) + ", last-prompt=" + onOff(o.LastPrompt),
		"inputs=" + jsNumber(float64(inputCount)) + ", editor-renders=" + jsNumber(float64(renderCount)) + ", renders/input=" + perInput +
			", max-draft=" + jsNumber(float64(p.maxDraftChars)),
	}
	names := make([]string, 0, len(p.metrics))
	for name := range p.metrics {
		names = append(names, name)
	}
	slices.SortFunc(names, jsLocaleCompare)
	for _, name := range names {
		m := p.metrics[name]
		if m.totalMs == 0 && m.maxMs == 0 {
			lines = append(lines, name+": n="+jsNumber(float64(m.count)))
			continue
		}
		lines = append(lines, name+": n="+jsNumber(float64(m.count))+", avg="+jsToFixed(m.totalMs/float64(m.count), 3)+"ms, max="+
			jsToFixed(m.maxMs, 3)+"ms, total="+jsToFixed(m.totalMs, 1)+"ms")
	}
	return strings.Join(lines, "\n")
}

func (p *editorPerfProfiler) record(name string, durationMs float64) {
	m := p.metric(name)
	m.count++
	m.totalMs += durationMs
	m.maxMs = max(m.maxMs, durationMs)
}

// localeOrder is the printable ASCII characters in the order of ICU's root collation (Node 24, ICU 78): punctuation and
// symbols, digits, then letters with the lower case of a letter just before its upper case.
const localeOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789aAbBcCdDeEfFgGhHiIjJkKlLmMnNoOpPqQrRsStTuUvVwWxXyYzZ"

var localePrimary = func() map[rune]int {
	weights := map[rune]int{}
	next := 0
	for _, r := range localeOrder {
		if unicode.IsUpper(r) {
			weights[r] = weights[unicode.ToLower(r)]
			continue
		}
		next++
		weights[r] = next
	}
	return weights
}()

// jsLocaleCompare is a.localeCompare(b) for the names the profiler sorts: exact for printable ASCII (compared by primary weight
// first, then lower case before upper case), and by code point for any other character.
func jsLocaleCompare(a, b string) int {
	weight := func(r rune) int {
		if w, ok := localePrimary[r]; ok {
			return w
		}
		return 1000 + int(r)
	}
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if c := cmp.Compare(weight(ra[i]), weight(rb[i])); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(len(ra), len(rb)); c != 0 {
		return c
	}
	for i := range ra {
		if ra[i] != rb[i] {
			if unicode.IsLower(ra[i]) {
				return -1
			}
			return 1
		}
	}
	return 0
}
