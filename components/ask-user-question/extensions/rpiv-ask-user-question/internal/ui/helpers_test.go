package ui

// Test doubles shared by the twin files. upstream: @juicesharp/rpiv-test-utils makeTheme and makeTui, and the
// fixtures of test-fixtures.ts (each lane adds its own fixtures in its own *_test.go files).

// identityTheme is rpiv-test-utils makeTheme(): every styling function returns its text unchanged.
type identityTheme struct{}

func (identityTheme) Fg(_ string, text string) string  { return text }
func (identityTheme) Bg(_ string, text string) string  { return text }
func (identityTheme) Bold(text string) string          { return text }
func (identityTheme) Italic(text string) string        { return text }
func (identityTheme) Underline(text string) string     { return text }
func (identityTheme) Strikethrough(text string) string { return text }
func (identityTheme) Inverse(text string) string       { return text }

// fakeTUI is rpiv-test-utils makeTui(): it counts requestRender calls.
type fakeTUI struct {
	renders int
	columns int
	rows    int
}

func (f *fakeTUI) RequestRender() { f.renders++ }
func (f *fakeTUI) Columns() int {
	if f.columns == 0 {
		return 80
	}
	return f.columns
}
func (f *fakeTUI) Rows() int {
	if f.rows == 0 {
		return 24
	}
	return f.rows
}
