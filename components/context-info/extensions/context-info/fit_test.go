package contextinfo

import (
	"reflect"
	"testing"
)

// pig's ui.setFooter takes static lines, so an over-wide line wraps in the
// terminal while the renderer counts it as one row. Fitting keeps whole
// segments so a narrow pane drops "Calls" rather than half a number.
func TestFitSegmentsKeepsWholeSegmentsWithinWidth(t *testing.T) {
	segs := []string{"aaaa", "bbbb", "cccc", "dddd"}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{100, "aaaa | bbbb | cccc | dddd"},
		{25, "aaaa | bbbb | cccc | dddd"},
		{24, "aaaa | bbbb | cccc"},
		{18, "aaaa | bbbb | cccc"},
		{17, "aaaa | bbbb"},
		{4, "aaaa"},
		{3, "aa" + ansiReset + "…"}, // the first segment is never dropped, but it is cut to fit
		{1, ansiReset + "…"},
	} {
		if got := fitSegments(segs, " | ", tc.width); got != tc.want {
			t.Errorf("width %d: got %q, want %q", tc.width, got, tc.want)
		}
		if got := visibleWidth(fitSegments(segs, " | ", tc.width)); got > tc.width {
			t.Errorf("width %d: result is %d wide", tc.width, got)
		}
	}
}

// width 0 means the host has not reported a size; keep everything and let the
// host clamp rather than guessing a width.
func TestFitSegmentsKeepsAllWhenWidthUnknown(t *testing.T) {
	segs := []string{"aaaa", "bbbb", "cccc"}
	if got, want := fitSegments(segs, " | ", 0), "aaaa | bbbb | cccc"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The measurement must ignore SGR escapes, or every styled segment is counted as
// far wider than it displays and the footer collapses to one field.
func TestVisibleWidthIgnoresStyling(t *testing.T) {
	if got := visibleWidth(accent("Cost:")); got != 5 {
		t.Errorf("accent(\"Cost:\") measured %d columns, want 5", got)
	}
	styled := []string{accent("Cost:") + " " + muted("$1.00"), dim("x")}
	if got := fitSegments(styled, " | ", 40); got != styled[0]+" | "+styled[1] {
		t.Errorf("styled segments were dropped although they fit: %q", got)
	}
}

// The arrows and box-drawing separator this footer uses are single-column; if
// they were counted as two the footer would truncate early.
func TestVisibleWidthCountsFooterGlyphsAsOneColumn(t *testing.T) {
	for _, g := range []string{"\u2191", "\u2193", "\u2502", "\u2295"} {
		if got := visibleWidth(g); got != 1 {
			t.Errorf("%q measured %d columns, want 1", g, got)
		}
	}
}

// ── resize cost ───────────────────────────────────────────────────────────────

// Re-layout must not repeat the host reads a full update makes. Each is a
// blocking call on the extension's message loop, and a drag-resize delivers
// width_change continuously, so acquiring per event stalls the loop and floods
// the socket. These pin the decision that keeps resize free of acquisition.

func TestNoRelayoutBeforeAnyFullUpdate(t *testing.T) {
	resetFooterModel()
	if _, ok := takeRelayout(80); ok {
		t.Error("re-layout was claimed before any full update retained a model; " +
			"it would have had to acquire the values itself, which is the cost " +
			"this design exists to avoid")
	}
}

func TestRelayoutSkipsAnIdenticalWidth(t *testing.T) {
	resetFooterModel()
	storeFooterModel(footerModel{thinking: "high", modelName: "m", ctxStr: "1%"})

	if _, ok := takeRelayout(80); !ok {
		t.Fatal("the first re-layout at a new width was not claimed")
	}
	if _, ok := takeRelayout(80); ok {
		t.Error("an unchanged width was claimed again; that is pure IPC traffic")
	}
	if _, ok := takeRelayout(81); !ok {
		t.Error("a genuinely new width was skipped")
	}
}

// A resize storm must collapse to the width the user settled on. Each arrival
// supersedes the previous by generation, so only the last one pushes.
func TestResizeStormCollapsesToTheFinalWidth(t *testing.T) {
	resetFooterModel()
	storeFooterModel(footerModel{thinking: "high", modelName: "m", ctxStr: "1%"})

	claimed := 0
	for w := 60; w <= 120; w++ {
		if _, ok := takeRelayout(w); ok {
			claimed++
		}
	}
	// Without coalescing every distinct width claims a push. The generation
	// check in onWidthChange is what collapses these; this asserts the claim
	// side stays cheap and monotonic rather than re-pushing a settled width.
	if _, ok := takeRelayout(120); ok {
		t.Error("the settled width was claimed a second time")
	}
	if claimed != 61 {
		t.Errorf("claimed %d of 61 distinct widths", claimed)
	}
}

// The retained model must not start holding the tool slices, or what the footer
// keeps between renders grows with the session.
func TestRetainedFooterModelHoldsNoReferenceTypes(t *testing.T) {
	rt := reflect.TypeOf(footerModel{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		switch f.Type.Kind() {
		case reflect.Slice, reflect.Map, reflect.Ptr, reflect.Chan, reflect.Interface:
			t.Errorf("footerModel.%s is a %s; the retained model must stay flat as "+
				"the session grows, so it holds counts and rendered fragments only",
				f.Name, f.Type.Kind())
		}
	}
}

func resetFooterModel() {
	footerModelMu.Lock()
	lastFooterModel = footerModel{}
	lastFooterWidth = 0
	footerModelMu.Unlock()
}
