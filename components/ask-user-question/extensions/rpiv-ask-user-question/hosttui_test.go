package ask_user_question

import "testing"

// The terminal size the session sees falls back to 80 columns by 24 rows when the host reports none.
func TestHostTUIDefaultsTheTerminalSize(t *testing.T) {
	h := &fakeUIHost{}
	tuiHost := &hostTUI{h: h}
	eq(t, tuiHost.Columns(), 80, "columns")
	eq(t, tuiHost.Rows(), 24, "rows")
	h.cols, h.rows = 100, 30
	eq(t, tuiHost.Columns(), 100, "reported columns")
	eq(t, tuiHost.Rows(), 30, "reported rows")
}
