package powerline_footer

// The editor performance profiler's options and counters. upstream: editor-performance.ts.

type editorPerfOptions struct {
	Enabled, FastRender, EditorChrome, Widgets, BashWidgets, LastPrompt bool
}

func readEditorPerfOptions(env func(string) string) editorPerfOptions { return editorPerfOptions{} }

type editorPerfProfiler struct {
	options   editorPerfOptions
	nowMs     func() float64
	perfMs    func() float64
	startedAt float64
}

func jsLocaleCompare(a, b string) int { return 0 }

func newEditorPerfProfiler(options editorPerfOptions) *editorPerfProfiler {
	return &editorPerfProfiler{options: options}
}

func measureEditorPerf[T any](p *editorPerfProfiler, name string, operation func() T) T {
	return operation()
}
func (p *editorPerfProfiler) count(name string)           {}
func (p *editorPerfProfiler) observeDraft(lines []string) {}
func (p *editorPerfProfiler) reset()                      {}
func (p *editorPerfProfiler) report() string              { return "" }
