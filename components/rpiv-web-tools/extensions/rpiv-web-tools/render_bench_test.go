package rpiv_web_tools

import (
	"strings"
	"testing"
)

// layout is the line layout of every renderer; the benchmark times it on an expanded web_fetch result of 20 styled
// lines at 80 columns, the largest text a card draws (port/bench-tools.mjs times the original's component alike).
var layout = textLines

type benchTheme struct{}

func (benchTheme) Fg(_, text string) string { return "\x1b[38;5;33m" + text + "\x1b[39m" }
func (benchTheme) Bold(text string) string  { return "\x1b[1m" + text + "\x1b[22m" }

func BenchmarkRenderLayout(b *testing.B) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line " + itoa(i+1) + " of the fetched page, long enough to take a good part of the row it is on"
	}
	text := renderFetchResultText(map[string]any{"title": "Bench"}, strings.Join(lines, "\n"), true, true, false, benchTheme{})
	b.ReportAllocs()
	for b.Loop() {
		layout(text, 80)
	}
}
