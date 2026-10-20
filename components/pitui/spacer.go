package tui

// Spacer renders empty lines. Port of pi-tui's Spacer.
type Spacer struct {
	lines int
}

// NewSpacer returns a Spacer of n lines (pi-tui default is 1).
func NewSpacer(lines int) *Spacer { return &Spacer{lines: lines} }

// SetLines changes the number of lines.
func (s *Spacer) SetLines(lines int) { s.lines = lines }

// Invalidate is a no-op (no cached state).
func (s *Spacer) Invalidate() {}

// Render returns s.lines empty strings.
func (s *Spacer) Render(width int) []string {
	out := make([]string, 0, max(s.lines, 0))
	for i := 0; i < s.lines; i++ {
		out = append(out, "")
	}
	return out
}
