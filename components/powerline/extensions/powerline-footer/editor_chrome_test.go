package powerline_footer

import (
	"fmt"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Twins of what index.ts's renderFastPowerlineEditor and the editor.render override draw, over a fake default editor.

type fakeBase struct {
	rows         []string // what Render answers, whatever the width
	renderWidths []int
	lines        []string
	cursor       sdk.EditorCursor
	showing      bool
	input        []string
}

func (f *fakeBase) HandleInput(data string) error { f.input = append(f.input, data); return nil }
func (f *fakeBase) Render(width int) ([]string, error) {
	f.renderWidths = append(f.renderWidths, width)
	return f.rows, nil
}
func (f *fakeBase) GetLines() ([]string, error)          { return f.lines, nil }
func (f *fakeBase) GetCursor() (sdk.EditorCursor, error) { return f.cursor, nil }
func (f *fakeBase) IsShowingAutocomplete() (bool, error) { return f.showing, nil }

const grayBorder = "\x1b[38;5;240m"

func piRows(inner ...string) []string {
	rows := []string{grayBorder + strings.Repeat("─", 20) + ansiReset}
	rows = append(rows, inner...)
	return append(rows, grayBorder+strings.Repeat("─", 20)+ansiReset)
}

func TestChromeLaysTheDefaultEditorOutAtWidthMinusThree(t *testing.T) {
	base := &fakeBase{rows: piRows("hello", "world")}
	var border editorBorderColor
	got, err := renderPowerlineEditor(base, &border, 30, false, false)
	if err != nil {
		t.Fatal(err)
	}
	rule := " " + grayBorder + strings.Repeat("─", 28) + ansiReset
	want := []string{rule, " " + promptColor + ">" + ansiReset + " hello", "   world", rule}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows\n%q\nwant\n%q", got, want)
	}
	if len(base.renderWidths) != 1 || base.renderWidths[0] != 27 {
		t.Errorf("the default editor rendered at %v, want [27]", base.renderWidths)
	}
}

func TestChromeShowsTheShellPromptInBashMode(t *testing.T) {
	base := &fakeBase{rows: piRows("ls")}
	var border editorBorderColor
	got, _ := renderPowerlineEditor(base, &border, 30, true, false)
	if got[1] != " "+promptColor+"$"+ansiReset+" ls" {
		t.Errorf("prompt row %q", got[1])
	}
}

func TestChromeKeepsTheRowsBelowTheBottomBorder(t *testing.T) {
	rows := piRows("one")
	rows = append(rows, "  /help", "  /hello")
	base := &fakeBase{rows: rows}
	var border editorBorderColor
	got, _ := renderPowerlineEditor(base, &border, 30, false, false)
	if len(got) != 5 || got[3] != "  /help" || got[4] != "  /hello" {
		t.Errorf("autocomplete rows were dropped: %q", got)
	}
}

func TestChromeOfAnEmptyEditorDrawsABlankPromptRow(t *testing.T) {
	base := &fakeBase{rows: piRows()}
	var border editorBorderColor
	got, _ := renderPowerlineEditor(base, &border, 30, false, false)
	want := " " + promptColor + ">" + ansiReset + " " + strings.Repeat(" ", 27)
	if len(got) != 3 || got[1] != want {
		t.Errorf("rows %q, want a blank prompt row %q", got, want)
	}
}

func TestChromeIsSkippedWhenTheTerminalIsNarrow(t *testing.T) {
	base := &fakeBase{rows: piRows("x")}
	var border editorBorderColor
	got, _ := renderPowerlineEditor(base, &border, 9, false, false)
	if len(got) != 3 || got[1] != "x" || base.renderWidths[0] != 9 {
		t.Errorf("rows %q at %v", got, base.renderWidths)
	}
}

func TestChromeNoRowsStayNoRows(t *testing.T) {
	base := &fakeBase{rows: nil}
	var border editorBorderColor
	got, err := renderPowerlineEditor(base, &border, 30, false, false)
	if err != nil || len(got) != 0 {
		t.Errorf("rows %q err %v", got, err)
	}
}

func TestBorderColourFollowsTheDefaultEditor(t *testing.T) {
	var border editorBorderColor
	if got := border.apply("──"); got != separatorAnsi+"──"+ansiReset {
		t.Errorf("fallback colour %q", got)
	}
	border.learn("\x1b[1m\x1b[38;5;99m────\x1b[0m")
	if got := border.apply("──"); got != "\x1b[1m\x1b[38;5;99m──"+ansiReset {
		t.Errorf("learned colour %q", got)
	}
	border.learn("plain row") // no border cells: keep what was learned
	if border.prefix != "\x1b[1m\x1b[38;5;99m" {
		t.Errorf("prefix %q", border.prefix)
	}
}

func fastDoc(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

func TestFastRenderDrawsTheViewportAroundTheCursor(t *testing.T) {
	base := &fakeBase{lines: fastDoc(100), cursor: sdk.EditorCursor{Line: 50, Col: 2}}
	var border editorBorderColor
	got, err := renderFastPowerlineEditor(base, border, 40, false, false)
	if err != nil || got == nil {
		t.Fatalf("no fast render: %v", err)
	}
	// 24 rows * 0.3 = 7 visible rows: 6 above the cursor and the cursor row (the cursor is the last row until nothing follows).
	if len(got) != 7+2 {
		t.Fatalf("%d rows, want 9:\n%q", len(got), got)
	}
	top := " " + border.apply("↑"+strings.Repeat("─", 37))
	bottom := " " + border.apply("↓"+strings.Repeat("─", 37))
	if got[0] != top || got[len(got)-1] != bottom {
		t.Errorf("borders %q / %q, want %q / %q", got[0], got[len(got)-1], top, bottom)
	}
	prompt := " " + promptColor + ">" + ansiReset + " "
	if !strings.HasPrefix(got[1], prompt+"line 44") {
		t.Errorf("first row %q", got[1])
	}
	last := got[len(got)-2]
	if !strings.Contains(last, "   li"+cursorMarker+"\x1b[7mn"+ansiReset+"e 50") {
		t.Errorf("cursor row %q", last)
	}
}

func TestFastRenderMarksTheCursorCellAndPadsTheRow(t *testing.T) {
	doc := fastDoc(90)
	base := &fakeBase{lines: doc, cursor: sdk.EditorCursor{Line: 89, Col: len(doc[89])}}
	var border editorBorderColor
	got, _ := renderFastPowerlineEditor(base, border, 40, false, false)
	last := got[len(got)-2]
	want := "   " + "line 89" + cursorMarker + "\x1b[7m " + ansiReset
	if !strings.HasPrefix(last, want) {
		t.Errorf("cursor row %q, want it to start %q", last, want)
	}
	if got := visibleWidth(strings.ReplaceAll(last, cursorMarker, "")); got != 40 {
		t.Errorf("row width %d, want 40", got)
	}
	if !strings.HasSuffix(got[len(got)-1], ansiReset) || !strings.Contains(got[len(got)-1], "─") {
		t.Errorf("bottom %q", got[len(got)-1])
	}
	if strings.Contains(got[len(got)-1], "↓") {
		t.Errorf("nothing follows the cursor, so no down mark: %q", got[len(got)-1])
	}
}

func TestFastRenderWrapsAVeryLongLine(t *testing.T) {
	long := strings.Repeat("abcdefghij", 130) // 1300 columns
	base := &fakeBase{lines: []string{long}, cursor: sdk.EditorCursor{Line: 0, Col: 1250}}
	var border editorBorderColor
	got, _ := renderFastPowerlineEditor(base, border, 53, false, false)
	if got == nil {
		t.Fatal("a 1300 column line takes the fast path")
	}
	// innerWidth 50, layoutWidth 49: the cursor is in chunk 25 of 27; seven rows end at the cursor row plus the two chunks after it.
	if len(got) != 7+2 {
		t.Fatalf("%d rows:\n%q", len(got), got)
	}
	if !strings.Contains(got[0], "↑") || !strings.Contains(got[len(got)-1], "↓") {
		t.Errorf("marks %q %q", got[0], got[len(got)-1])
	}
	// Six chunks before the cursor's (19 to 24), then the cursor's chunk 25: the first row carries chunk 19, columns 931 to 979.
	if want := long[19*49 : 20*49]; !strings.Contains(got[1], want) {
		t.Errorf("first row %q, want chunk 19 %q", got[1], want)
	}
	if want := long[25*49 : 25*49+25]; !strings.Contains(got[7], want+cursorMarker) { // the cursor is at column 25 of its chunk
		t.Errorf("cursor row %q, want chunk 25 up to the cursor %q", got[7], want)
	}
}

func TestFastRenderStepsAside(t *testing.T) {
	var border editorBorderColor
	cases := map[string]struct {
		base                 *fakeBase
		width                int
		completions          bool
		wantNilWithoutOption bool
	}{
		"a short draft":     {&fakeBase{lines: fastDoc(79), cursor: sdk.EditorCursor{Line: 3}}, 40, false, true},
		"a narrow terminal": {&fakeBase{lines: fastDoc(100)}, 9, false, true},
		"bash completions":  {&fakeBase{lines: fastDoc(100)}, 40, true, true},
		"autocomplete open": {&fakeBase{lines: fastDoc(100), showing: true}, 40, false, true},
		"non-ASCII text":    {&fakeBase{lines: append(fastDoc(99), "héllo"), cursor: sdk.EditorCursor{Line: 99, Col: 1}}, 40, false, true},
		"an empty editor":   {&fakeBase{lines: nil}, 40, false, true},
	}
	for name, c := range cases {
		got, err := renderFastPowerlineEditor(c.base, border, c.width, false, c.completions)
		if err != nil || got != nil {
			t.Errorf("%s: fast render %q (err %v), want the normal render", name, got, err)
		}
	}
	// The same document without the obstacle is fast.
	if got, _ := renderFastPowerlineEditor(&fakeBase{lines: fastDoc(100)}, border, 40, false, false); got == nil {
		t.Error("100 lines of ASCII must take the fast path")
	}
}

func TestFastRenderThresholds(t *testing.T) {
	var border editorBorderColor
	if got, _ := renderFastPowerlineEditor(&fakeBase{lines: fastDoc(80)}, border, 40, false, false); got == nil {
		t.Error("80 lines is the threshold")
	}
	short := &fakeBase{lines: []string{strings.Repeat("a", 1199)}, cursor: sdk.EditorCursor{Col: 5}}
	if got, _ := renderFastPowerlineEditor(short, border, 40, false, false); got != nil {
		t.Error("1199 columns is below the threshold")
	}
	long := &fakeBase{lines: []string{strings.Repeat("a", 1200)}, cursor: sdk.EditorCursor{Col: 5}}
	if got, _ := renderFastPowerlineEditor(long, border, 40, false, false); got == nil {
		t.Error("1200 columns is the threshold")
	}
	// A long line that does not hold the cursor does not count: the threshold reads the cursor's line.
	elsewhere := &fakeBase{lines: []string{strings.Repeat("a", 1500), "short"}, cursor: sdk.EditorCursor{Line: 1, Col: 2}}
	if got, _ := renderFastPowerlineEditor(elsewhere, border, 40, false, false); got != nil {
		t.Error("the cursor's line is short and there are few lines")
	}
}

func TestFastRenderClampsAWildCursor(t *testing.T) {
	var border editorBorderColor
	base := &fakeBase{lines: fastDoc(100), cursor: sdk.EditorCursor{Line: 900, Col: 900}}
	got, _ := renderFastPowerlineEditor(base, border, 40, false, false)
	if got == nil || !strings.Contains(got[len(got)-2], "line 99") {
		t.Errorf("rows %q", got)
	}
	base = &fakeBase{lines: fastDoc(100), cursor: sdk.EditorCursor{Line: -4, Col: -4}}
	got, _ = renderFastPowerlineEditor(base, border, 40, false, false)
	if got == nil || !strings.Contains(got[1], "ine 0") {
		t.Errorf("rows %q", got)
	}
}

func TestRenderPrefersTheFastPath(t *testing.T) {
	base := &fakeBase{rows: piRows("never used"), lines: fastDoc(100), cursor: sdk.EditorCursor{Line: 99}}
	var border editorBorderColor
	got, _ := renderPowerlineEditor(base, &border, 40, false, false)
	if len(base.renderWidths) != 0 || len(got) != 9 {
		t.Errorf("the default editor rendered %v times, rows %d", base.renderWidths, len(got))
	}
}

func TestJsSliceCountsUTF16Units(t *testing.T) {
	if jsLen("a😀b") != 4 {
		t.Errorf("jsLen = %d", jsLen("a😀b"))
	}
	if got := jsSlice("a😀b", 3, 4); got != "b" {
		t.Errorf("slice = %q", got)
	}
	if got := jsSlice("abc", 1, 99); got != "bc" {
		t.Errorf("slice = %q", got)
	}
}

func TestPowerlineEditorSendsPrintableKeysStraightToTheDefaultEditor(t *testing.T) {
	base := &fakeBase{}
	k := &keysExt{}
	e := &powerlineEditor{base: base, k: k}
	_ = e.HandleInput("a")
	if len(base.input) != 1 || base.input[0] != "a" {
		t.Errorf("default editor input %q", base.input)
	}
}

func TestFastRenderKeepsOnlyTheTrailingChunksOfALongLineAboveTheCursor(t *testing.T) {
	lines := fastDoc(79)
	lines = append(lines, strings.Repeat("0123456789", 40), "the cursor line") // 400 columns: 12 chunks of 36 at width 40
	base := &fakeBase{lines: lines, cursor: sdk.EditorCursor{Line: 80, Col: 3}}
	var border editorBorderColor
	got, _ := renderFastPowerlineEditor(base, border, 40, false, false)
	if got == nil || len(got) != 9 {
		t.Fatalf("rows %q", got)
	}
	// Six rows above the cursor: the last six chunks of the long line, chunk 6 first (columns 216 to 251).
	want := strings.Repeat("0123456789", 40)[216:252]
	if !strings.HasPrefix(got[1], " "+promptColor+">"+ansiReset+" "+want) {
		t.Errorf("first row %q, want it to carry %q", got[1], want)
	}
}

func TestEditorInputDismissesTheWelcomeForEveryKey(t *testing.T) {
	var dismissed int
	k := &keysExt{dismissWelcome: func(sdk.Context) { dismissed++ }}
	base := &fakeBase{}
	e := &powerlineEditor{base: base, k: k}
	_ = e.HandleInput("a")
	_ = e.HandleInput("\x1b[A")
	if dismissed != 2 || len(base.input) != 2 {
		t.Errorf("dismissed %d times, default editor got %q", dismissed, base.input)
	}
}
