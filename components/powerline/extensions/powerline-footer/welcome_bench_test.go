package powerline_footer

import "testing"

// The welcome's hot paths: drawing the box (every repaint of the header and the overlay) and the terminal-input listener (every
// chunk while a welcome is pending). Run: go test -run XXX -bench Welcome -benchmem

func benchWelcomeData() (string, string, []recentSession, loadedCounts, *float64) {
	tokens := 1900.0
	return "Claude Sonnet 4.5", "anthropic", []recentSession{{"pigpen", "just now"}, {"my-dashed-project", "5m ago"}, {"third", "2d ago"}},
		loadedCounts{2, 3, 1}, &tokens
}

func BenchmarkWelcomeHeaderRender(b *testing.B) {
	m, p, s, c, t := benchWelcomeData()
	h := newWelcomeHeader(m, p, s, c, t)
	b.ReportAllocs()
	for b.Loop() {
		h.render(96)
	}
}

func BenchmarkWelcomeOverlayRender(b *testing.B) {
	m, p, s, c, t := benchWelcomeData()
	o := newWelcomeComponent(m, p, s, c, t)
	b.ReportAllocs()
	for b.Loop() {
		o.render(96)
	}
}

// With nothing pending the listener only takes a lock and looks at three fields.
func BenchmarkWelcomeInputIdle(b *testing.B) {
	a := &app{}
	b.ReportAllocs()
	for b.Loop() {
		a.welcomeInput("x")
	}
}
