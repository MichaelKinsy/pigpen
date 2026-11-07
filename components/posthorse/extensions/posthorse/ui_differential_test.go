package posthorse

// Differential proof of the UI port (ui.go, ui_cards.go, ui_display.go, ui_text.go) against the original's cards and
// message renderers. The TS side is port/review/uireview-probes/cases.ts, run under Node 24 with Pi 1.5.0's pi-tui; it
// writes the inputs and the rows it rendered to /home/kinsy/ph-work/scratch/uireview/ui-cases.json (regenerate with
// port/review/uireview-probes/run-ts.sh inside the xd6702 container). The tests skip when that file is absent.

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

const uiDiffCases = "/home/kinsy/ph-work/scratch/uireview/ui-cases.json"

type uiDiffCase struct {
	ID               string              `json:"id"`
	Type             string              `json:"type"`
	Name             string              `json:"name"`
	Args             any                 `json:"args"`
	Content          []map[string]any    `json:"content"`
	Details          any                 `json:"details"`
	IsError          bool                `json:"isError"`
	IsPartial        bool                `json:"isPartial"`
	Expanded         bool                `json:"expanded"`
	ShowImages       bool                `json:"showImages"`
	ExecutionStarted bool                `json:"executionStarted"`
	Key              string              `json:"key"`
	Message          map[string]any      `json:"message"`
	OutputPad        int                 `json:"outputPad"`
	Rows             map[string][]string `json:"rows"`
}

type uiDiffFile struct {
	NumsText  []string          `json:"numsText"`
	Summaries map[string]string `json:"summaries"`
	Usages    [][2]any          `json:"usages"`
	Cases     []uiDiffCase      `json:"cases"`
}

func uiDiffLoad(t *testing.T) *uiDiffFile {
	raw, err := os.ReadFile(uiDiffCases)
	if err != nil {
		t.Skip("TS probe output absent: " + err.Error())
	}
	var f uiDiffFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func uiDiffNums(t *testing.T, f *uiDiffFile) []float64 {
	out := make([]float64, len(f.NumsText))
	for i, s := range f.NumsText {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func uiDiffRender(c *uiDiffCase, nums []float64, width int) []string {
	theme := sdk.UITheme{}
	switch c.Type {
	case "call":
		args := c.Args
		if strings.HasPrefix(c.ID, "call-num:") { // JSON.stringify drops the sign of -0: rebuild from the exact numbers
			i, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(c.ID, "call-num:"), "/c"), "/x"))
			v := nums[i]
			args = map[string]any{"op": "read", "id": "e", "offset": v, "imageOffset": v, "limit": v}
		}
		state := cardCallState{isError: c.IsError, isPartial: c.IsPartial, executionStarted: c.ExecutionStarted, expanded: c.Expanded}
		return cardCallComponent(c.Name, cardArgsOf(args), theme, state, width)
	case "result":
		images := 0
		for _, part := range c.Content {
			if part["type"] == "image" {
				images++
			}
		}
		facts := &cardResultFacts{
			name: c.Name, raw: cardTextOfBlocks(c.Content), args: cardArgsOf(c.Args), display: displayOf(c.Details),
			isError: c.IsError, isPartial: c.IsPartial, expanded: c.Expanded, imageCount: images, showImages: c.ShowImages, expandKey: c.Key,
		}
		return cardResultComponent(facts, theme, width)
	case "message":
		return cardMessageRows(theme, c.Message, sdk.MessageRenderOptions{Expanded: c.Expanded, OutputPad: c.OutputPad}, width)
	}
	panic(c.Type)
}

// TestUiDifferential compares every case at every width, row by row, and lists the differing case ids.
func TestUiDifferential(t *testing.T) {
	f := uiDiffLoad(t)
	if sdk.KeyText("app.tools.expand") != "" {
		t.Skip("message cases assume an empty keyText")
	}
	nums := uiDiffNums(t, f)
	var bad []string
	for i := range f.Cases {
		c := &f.Cases[i]
		widths := make([]int, 0, len(c.Rows))
		for w := range c.Rows {
			n, _ := strconv.Atoi(w)
			widths = append(widths, n)
		}
		sort.Ints(widths)
		var failed []string
		for _, w := range widths {
			want := c.Rows[strconv.Itoa(w)]
			got := uiDiffRender(c, nums, w)
			if d := uiDiffFirstDiff(want, got); d != "" {
				failed = append(failed, fmt.Sprintf("w%d %s", w, d))
			}
		}
		if len(failed) > 0 {
			bad = append(bad, c.ID+": "+failed[0]+fmt.Sprintf(" (%d widths differ)", len(failed)))
		}
	}
	for _, b := range bad {
		t.Log(b)
	}
	t.Logf("%d cases, %d differ", len(f.Cases), len(bad))
	if len(bad) > 0 {
		t.Fail()
	}
}

func uiDiffFirstDiff(want, got []string) string {
	for i := 0; i < max(len(want), len(got)); i++ {
		var a, b string
		if i < len(want) {
			a = want[i]
		}
		if i < len(got) {
			b = got[i]
		}
		if a != b || (i >= len(want)) != (i >= len(got)) {
			return fmt.Sprintf("row %d/%d,%d: ts %q go %q", i, len(want), len(got), uiDiffCut(a), uiDiffCut(b))
		}
	}
	return ""
}

func uiDiffCut(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// TestUiSummaries compares pageSummary (n) and the context summary with the original's strings.
func TestUiSummaries(t *testing.T) {
	f := uiDiffLoad(t)
	nums := uiDiffNums(t, f)
	for i, v := range nums {
		key := "page:" + strconv.Itoa(i)
		if got, want := pageSummary(pageRange{offset: v, end: 1, total: 2}, false), f.Summaries[key]; got != want {
			t.Errorf("DIVERGENCE: display.ts:209 n(%s): go %q ts %q", f.NumsText[i], got, want)
		}
	}
	for _, u := range f.Usages {
		key := "ctx:" + u[0].(string)
		if got, want := displaySummary(displayOf(u[1]), map[string]any{}, true), f.Summaries[key]; got != want {
			t.Errorf("DIVERGENCE: display.ts:231 %s: go %q ts %q", key, got, want)
		}
	}
}

// uiDiffPage is a 40,000-unit single-line note page of the given word.
func uiDiffPage(word string) string {
	var b strings.Builder
	for u16len(b.String()) < 40_000 {
		b.WriteString(word)
	}
	return u16slice(b.String(), 0, 40_000)
}

// text.ts:45-81: the original wraps (count+1)*width+1024 UTF-16 units of a collapsed preview whatever the script; so must
// safeCut, for every script.
func benchmarkUiLeadingRows(b *testing.B, word string) {
	page := uiDiffPage(word)
	theme := sdk.UITheme{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rows := cardLeadingRows(page, theme, colorToolOutput, 3)(100)
		if len(rows) < 4 {
			b.Fatal(len(rows))
		}
	}
}

func BenchmarkUiLeadingRowsLatin(b *testing.B) { benchmarkUiLeadingRows(b, "route station ") }
func BenchmarkUiLeadingRowsCyrillic(b *testing.B) {
	benchmarkUiLeadingRows(b, "станция маршрут ")
}
func BenchmarkUiLeadingRowsThai(b *testing.B) {
	benchmarkUiLeadingRows(b, "ภาษาไทยเป็นภาษา ")
}
func BenchmarkUiLeadingRowsArabic(b *testing.B) {
	benchmarkUiLeadingRows(b, "مرحبا بالعالم، ")
}
func BenchmarkUiLeadingRowsHangul(b *testing.B) { benchmarkUiLeadingRows(b, "한국어 텍스트 ") }
func BenchmarkUiLeadingRowsEmoji(b *testing.B)  { benchmarkUiLeadingRows(b, "👩🏽‍💻 ") }

// TestUiSafeCutScripts: safeCut cuts a 40,000-unit page of any script within a few units of where the original cuts (the
// grapheme that contains unit (3+1)*100+1024 = 1,424).
func TestUiSafeCutScripts(t *testing.T) {
	for _, word := range []string{"route station ", "станция маршрут ", "ภาษาไทยเป็นภาษา ", "مرحبا بالعالم، ", "한국어 텍스트 ", "👩🏽‍💻 "} {
		page := uiDiffPage(word)
		cut := safeCut(page, 1424)
		t.Logf("%-20q cut at unit %d of %d", word, u16len(page[:cut]), u16len(page))
	}
}
