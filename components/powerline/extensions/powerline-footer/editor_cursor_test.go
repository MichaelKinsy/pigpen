package powerline_footer

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestFastRenderPlacesTheCursorInItsChunk(t *testing.T) {
	var border editorBorderColor
	line := strings.Repeat("0123456789", 130) // layout width 36 at width 40: chunk 0 ends at column 35
	rowOfCursor := func(col int) int {
		rows, _ := renderFastPowerlineEditor(&fakeBase{lines: []string{line}, cursor: sdk.EditorCursor{Col: col}}, border, 40, false, false)
		for i, row := range rows {
			if strings.Contains(row, cursorMarker) {
				return i
			}
		}
		t.Fatalf("no cursor row for column %d: %q", col, rows)
		return -1
	}
	if got := rowOfCursor(35); got != 1 {
		t.Errorf("column 35 is the last of the first chunk: the cursor is on the first content row, not row %d", got)
	}
	if got := rowOfCursor(36); got != 2 {
		t.Errorf("column 36 starts the second chunk: the cursor is on the second content row, not row %d", got)
	}
}
