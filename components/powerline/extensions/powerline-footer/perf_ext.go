package powerline_footer

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The editor performance profiler's wiring. upstream: index.ts 1204 (editorPerf), 2704-2716 (/powerline-perf), 2982 (the segment
// context), 3125 (measureWidget), 3323-3500 (the editor's input and render). The profiler itself is editor_performance.go.
// Profiling is opt-in (POWERLINE_DEBUG_PERF=1, read when the extension is created) and each A/B switch turns one feature off.

// perfOptions are the profiler's options; every switch is on when there is no profiler.
func (p *powerline) perfOptions() editorPerfOptions {
	if p.perf == nil {
		return readEditorPerfOptions(func(string) string { return "" })
	}
	return p.perf.options
}

// measure records the time of a widget's render when profiling (measureWidget).
func (p *powerline) measure(name string, render func() []string) []string {
	if p.perf == nil || !p.perf.options.Enabled {
		return render()
	}
	return measureEditorPerf(p.perf, "widget."+name, render)
}

// measureContext records the time of building the segment context when profiling.
func (p *powerline) measureContext(build func() segmentContext) segmentContext {
	if p.perf == nil || !p.perf.options.Enabled {
		return build()
	}
	return measureEditorPerf(p.perf, "layout.segment-context", build)
}

// perfCommand is /powerline-perf: the report, or `reset`.
func (a *app) perfCommand(ctx sdk.Context, args string) error {
	if !a.perf.options.Enabled {
		ctx.Notify("Set POWERLINE_DEBUG_PERF=1 and reload to enable editor profiling", "info")
		return nil
	}
	if strings.ToLower(jsTrim(args)) == "reset" {
		a.perf.reset()
		ctx.Notify("Powerline editor performance counters reset", "info")
		return nil
	}
	ctx.Notify(a.perf.report(), "info")
	return nil
}

// perfBase measures the default editor's own input and render (input.base-editor, editor.render.base).
type perfBase struct {
	powerlineBase
	perf *editorPerfProfiler
}

func (b perfBase) HandleInput(data string) error {
	return measureEditorPerf(b.perf, "input.base-editor", func() error { return b.powerlineBase.HandleInput(data) })
}

func (b perfBase) Render(width int) ([]string, error) {
	lines := []string(nil)
	err := measureEditorPerf(b.perf, "editor.render.base", func() error {
		var err error
		lines, err = b.powerlineBase.Render(width)
		return err
	})
	return lines, err
}
