package ui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// Twin of preview-pane.test.ts (Markdown replaced by the FakeMarkdown of installFakeMarkdown).

const previewPaneKey = "view/components/preview/preview-pane"

var (
	mdRe  = regexp.MustCompile(`MD\[\d+\]:`)
	boxRe = regexp.MustCompile(`┌─+┐`)
)

func isMD(l string) bool { return mdRe.MatchString(l) }

func selectThemeForTest() WrappingSelectTheme {
	th := identityTheme{}
	return WrappingSelectTheme{
		SelectedText: func(s string) string { return th.Fg("accent", th.Bold(s)) },
		Description:  func(s string) string { return th.Fg("muted", s) },
		ScrollInfo:   func(s string) string { return th.Fg("dim", s) },
	}
}

type testPane struct {
	pane           *PreviewPane
	optionListView *OptionListView
	previewBlock   *PreviewBlockRenderer
	items          []WrappingSelectItem
}

func makePreviewPane(question QuestionData, getWidth func() int) testPane {
	if getWidth == nil {
		getWidth = func() int { return 120 }
	}
	items := make([]WrappingSelectItem, len(question.Options))
	for i, o := range question.Options {
		items[i] = WrappingSelectItem{Kind: KindOption, Label: o.Label, Description: o.Description}
	}
	olv := NewOptionListView(OptionListViewConfig{Items: items, Theme: selectThemeForTest()})
	pb := NewPreviewBlockRenderer(PreviewBlockRendererConfig{Question: question, Theme: identityTheme{}, MarkdownTheme: identityMarkdownTheme()})
	pane := NewPreviewPane(PreviewPaneConfig{Question: question, GetTerminalWidth: getWidth, OptionListView: olv, PreviewBlock: pb})
	// Auto-inject adaptive left width, as the original's makePane does (mirrors buildQuestionnaire.injectGlobalLeftWidth).
	total := len(items) + 1
	if question.MultiSelect {
		total = len(items)
	}
	pane.SetGlobalLeftWidth(func(w int) int { return AdaptiveLeftWidth(items, total, w) })
	return testPane{pane, olv, pb, items}
}

func (p testPane) focusOption(i int) {
	p.optionListView.SetProps(OptionListViewProps{SelectedIndex: i, Focused: true, InputBuffer: ""})
}

func findIdx(lines []string, pred func(string) bool) int {
	for i, l := range lines {
		if pred(l) {
			return i
		}
	}
	return -1
}

func find(lines []string, pred func(string) bool) (string, bool) {
	if i := findIdx(lines, pred); i >= 0 {
		return lines[i], true
	}
	return "", false
}

func requireWidths(t *testing.T, lines []string, w int) {
	t.Helper()
	for _, l := range lines {
		if vw := widthx.VisibleWidth(l); vw > w {
			t.Fatalf("line wider than %d: %d %q", w, vw, l)
		}
	}
}

func TestPreviewPane(t *testing.T) {
	// ---- layout switching
	{
		question := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "A", Preview: "## A\n\nbody A content"},
			{Label: "B", Preview: "## B\n\nbody B content"},
			{Label: "C"},
		}}
		tw(t, previewPaneKey, "side-by-side at width 120 (>= PREVIEW_MIN_WIDTH)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			lines := p.pane.Render(120)
			if len(lines) == 0 || !anyLine(lines, isMD) {
				t.Fatalf("no MD line: %q", lines)
			}
		})
		tw(t, previewPaneKey, "stacked at width 80 (< PREVIEW_MIN_WIDTH)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 80 })
			p.focusOption(0)
			lines := p.pane.Render(80)
			md := findIdx(lines, isMD)
			if md <= 0 {
				t.Fatalf("md index %d", md)
			}
			trailing := len(lines[md:])
			if trailing < 4 || trailing > MaxPreviewHeightStacked {
				t.Fatalf("trailing %d", trailing)
			}
		})
		tw(t, previewPaneKey, "a resize from width 99 to 100 switches from stacked to right-aligned side-by-side", func(t *testing.T) {
			installFakeMarkdown(t)
			terminalWidth := 99
			p := makePreviewPane(question, func() int { return terminalWidth })
			p.pane.SetGlobalLeftWidth(func(w int) int {
				return CrossTabLeftWidthWithDonation(1, [][]WrappingSelectItem{p.items}, []QuestionData{question}, w)
			})
			p.focusOption(0)
			if findIdx(p.pane.Render(99), isMD) <= 0 {
				t.Fatal("narrow: MD must follow the options")
			}
			terminalWidth = PreviewMinWidth
			wide := p.pane.Render(PreviewMinWidth)
			if !anyLine(wide, isMD) {
				t.Fatal("wide: no MD")
			}
			top, ok := find(wide, func(l string) bool { return strings.Contains(l, "┌") })
			if !ok {
				t.Fatal("no top border")
			}
			eqInt(t, widthx.VisibleWidth(top), PreviewMinWidth)
		})
	}

	// ---- cache + invalidate
	{
		question := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "A", Preview: "alpha preview"}, {Label: "B", Preview: "beta preview"},
		}}
		tw(t, previewPaneKey, "creates one Markdown per option lazily; revisit hits cache", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			for _, step := range []struct{ idx, want int }{{0, 1}, {1, 2}, {0, 2}} {
				p.focusOption(step.idx)
				p.pane.SetProps(PreviewPaneProps{SelectedIndex: step.idx})
				p.pane.Render(120)
				eqInt(t, previewMarkdownConstructed, step.want)
			}
		})
		tw(t, previewPaneKey, "invalidate() does NOT delete instances; subsequent renders still re-use cache", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			p.pane.Render(120)
			eqInt(t, previewMarkdownConstructed, 1)
			p.previewBlock.Invalidate()
			p.pane.Render(120)
			eqInt(t, previewMarkdownConstructed, 1)
		})
	}

	// ---- empty preview placeholder
	tw(t, previewPaneKey, "hides the preview block entirely when no option provides a preview", func(t *testing.T) {
		installFakeMarkdown(t)
		q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "only"}}}
		p := makePreviewPane(q, func() int { return 80 })
		p.focusOption(0)
		lines := p.pane.Render(80)
		if anyLineContains(lines, NoPreviewText) || anyLine(lines, isMD) {
			t.Fatalf("preview must be hidden: %q", lines)
		}
	})
	tw(t, previewPaneKey, "still shows 'No preview available' for an item lacking a preview when SOME option in the question has one", func(t *testing.T) {
		installFakeMarkdown(t)
		q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "with", Preview: "alpha"}, {Label: "without"},
		}}
		p := makePreviewPane(q, func() int { return 80 })
		p.focusOption(1)
		p.pane.SetProps(PreviewPaneProps{SelectedIndex: 1})
		lines := p.pane.Render(80)
		md := findIdx(lines, func(l string) bool { return strings.Contains(l, NoPreviewText) })
		if md <= -1 {
			t.Fatal("placeholder missing")
		}
		if len(lines[md:]) > MaxPreviewHeightStacked {
			t.Fatalf("tail %d", len(lines[md:]))
		}
	})

	tw(t, previewPaneKey, "renders ONLY the options list when question.multiSelect === true", func(t *testing.T) {
		installFakeMarkdown(t)
		q := QuestionData{Question: "areas", Header: "areas", MultiSelect: true, Options: []OptionData{
			{Label: "FE", Preview: "would not show"}, {Label: "BE"},
		}}
		p := makePreviewPane(q, func() int { return 120 })
		lines := p.pane.Render(120)
		if anyLine(lines, isMD) || anyLineContains(lines, NoPreviewText) {
			t.Fatalf("preview leaked: %q", lines)
		}
	})

	tw(t, previewPaneKey, "every emitted line satisfies visibleWidth(line) <= width", func(t *testing.T) {
		installFakeMarkdown(t)
		q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "A", Preview: strings.Repeat("x", 500)}, {Label: "B"},
		}}
		for _, w := range []int{60, 80, 100, 120} {
			w := w
			p := makePreviewPane(q, func() int { return w })
			p.focusOption(0)
			requireWidths(t, p.pane.Render(w), w)
		}
	})

	// ---- naturalHeight
	tw(t, previewPaneKey, "naturalHeight(w) === render(w).length parametric across modes and fixtures", func(t *testing.T) {
		installFakeMarkdown(t)
		fixtures := []QuestionData{
			{Question: "q", Header: "q", Options: []OptionData{{Label: "A"}, {Label: "B"}}},
			{Question: "q", Header: "q", Options: []OptionData{{Label: "A", Description: "desc-a"}, {Label: "B", Description: "desc-b"}, {Label: "C", Description: "desc-c"}, {Label: "D"}}},
			{Question: "q", Header: "q", Options: []OptionData{{Label: "only"}}},
		}
		for _, q := range fixtures {
			multiQ := q
			multiQ.MultiSelect = true
			multi := makePreviewPane(multiQ, func() int { return 120 })
			for _, w := range []int{60, 80, 100, 120, 160} {
				eqInt(t, multi.pane.NaturalHeight(w), len(multi.pane.Render(w)))
			}
			wide := makePreviewPane(q, func() int { return 120 })
			for _, w := range []int{100, 120, 160} {
				eqInt(t, wide.pane.NaturalHeight(w), len(wide.pane.Render(w)))
			}
			narrow := makePreviewPane(q, func() int { return 80 })
			for _, w := range []int{60, 80} {
				eqInt(t, narrow.pane.NaturalHeight(w), len(narrow.pane.Render(w)))
			}
		}
	})

	// ---- maxNaturalHeight
	{
		mixed := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "short", Preview: "tiny"}, {Label: "long", Preview: strings.Repeat("x", 800)}, {Label: "no-preview"},
		}}
		tw(t, previewPaneKey, "multiSelect returns options height (no preview branch)", func(t *testing.T) {
			installFakeMarkdown(t)
			multiQ := mixed
			multiQ.MultiSelect = true
			p := makePreviewPane(multiQ, func() int { return 120 })
			eqInt(t, p.pane.MaxNaturalHeight(120), len(p.pane.Render(120)))
		})
		tw(t, previewPaneKey, "no-preview question returns options height (early-return parity with naturalHeight)", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "q", Header: "q", Options: []OptionData{{Label: "A"}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 120 })
			eqInt(t, p.pane.MaxNaturalHeight(120), len(p.pane.Render(120)))
			eqInt(t, p.pane.MaxNaturalHeight(80), len(p.pane.Render(80)))
		})
		bounded := func(t *testing.T, termW int) {
			installFakeMarkdown(t)
			p := makePreviewPane(mixed, func() int { return termW })
			maxH := p.pane.MaxNaturalHeight(termW)
			for i := range mixed.Options {
				p.focusOption(i)
				p.pane.SetProps(PreviewPaneProps{SelectedIndex: i, Focused: true})
				if h := p.pane.NaturalHeight(termW); h > maxH {
					t.Fatalf("naturalHeight %d > max %d at %d", h, maxH, i)
				}
			}
		}
		tw(t, previewPaneKey, "side-by-side: maxNaturalHeight >= naturalHeight for any selectedIndex", func(t *testing.T) { bounded(t, 120) })
		tw(t, previewPaneKey, "stacked: maxNaturalHeight >= naturalHeight for any selectedIndex", func(t *testing.T) { bounded(t, 80) })
		tw(t, previewPaneKey, "maxNaturalHeight is index-independent (does not depend on the current props.selectedIndex)", func(t *testing.T) {
			installFakeMarkdown(t)
			a := makePreviewPane(mixed, func() int { return 120 })
			a.focusOption(0)
			a.pane.SetProps(PreviewPaneProps{SelectedIndex: 0, Focused: true})
			b := makePreviewPane(mixed, func() int { return 120 })
			b.focusOption(1)
			b.pane.SetProps(PreviewPaneProps{SelectedIndex: 1, Focused: true})
			eqInt(t, a.pane.MaxNaturalHeight(120), b.pane.MaxNaturalHeight(120))
		})
	}

	// ---- right-aligned preview
	{
		question := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "A", Preview: "short body"}, {Label: "B"}}}
		mdLines := func(lines []string) []string {
			var out []string
			for _, l := range lines {
				if isMD(l) {
					out = append(out, l)
				}
			}
			return out
		}
		tw(t, previewPaneKey, "content-sized preview boxes share the right edge while retaining their natural widths", func(t *testing.T) {
			installFakeMarkdown(t)
			short := makePreviewPane(question, func() int { return 120 })
			short.focusOption(0)
			shortLines := short.pane.Render(120)
			shortMD := jsstring.IndexOf(mdLines(shortLines)[0], "MD[", 0)

			longQ := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "A", Preview: strings.Repeat("x", 500)}, {Label: "B"}}}
			long := makePreviewPane(longQ, func() int { return 120 })
			long.focusOption(0)
			longLines := long.pane.Render(120)
			longMD := jsstring.IndexOf(mdLines(longLines)[0], "MD[", 0)

			for _, lines := range [][]string{shortLines, longLines} {
				top, _ := find(lines, func(l string) bool { return strings.Contains(l, "┌") })
				eqInt(t, widthx.VisibleWidth(top), 120)
			}
			if shortMD <= longMD {
				t.Fatalf("shortMD %d <= longMD %d", shortMD, longMD)
			}
		})
		tw(t, previewPaneKey, "side-by-side: adaptive left width adjusts options column based on label content", func(t *testing.T) {
			installFakeMarkdown(t)
			longQ := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "A", Preview: strings.Repeat("x", 500)}, {Label: "B"}}}
			items := []WrappingSelectItem{{Kind: KindOption, Label: "A"}, {Kind: KindOption, Label: "B"}}
			p := makePreviewPane(longQ, func() int { return 200 })
			p.focusOption(0)
			preview := mdLines(p.pane.Render(200))
			if len(preview) == 0 {
				t.Fatal("no preview lines")
			}
			// MD column starts at leftWidth + gap(2) + leftPad(1) + leftBorderBar(1) + innerLeftPad(1).
			eqInt(t, jsstring.IndexOf(preview[0], "MD[", 0), AdaptiveLeftWidth(items, 3, 200)+PreviewColumnGap+1+2)
		})
		tw(t, previewPaneKey, "side-by-side: first MD row is preceded by the top border row (no top padding)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			lines := p.pane.Render(120)
			first := findIdx(lines, isMD)
			if first <= 0 || !boxRe.MatchString(lines[first-1]) {
				t.Fatalf("row above first MD: %q", lines)
			}
		})
		tw(t, previewPaneKey, "stacked mode: an empty gap row separates the options block from the bordered preview block", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 80 })
			p.focusOption(0)
			lines := p.pane.Render(80)
			first := findIdx(lines, isMD)
			if first <= 1 || !boxRe.MatchString(lines[first-1]) || lines[first-2] != "" {
				t.Fatalf("layout wrong: %q", lines)
			}
		})
		tw(t, previewPaneKey, "multiSelect mode unchanged (options-only, no preview, no padding logic)", func(t *testing.T) {
			installFakeMarkdown(t)
			multiQ := question
			multiQ.MultiSelect = true
			p := makePreviewPane(multiQ, func() int { return 120 })
			if anyLine(p.pane.Render(120), isMD) {
				t.Fatal("preview leaked")
			}
		})
		tw(t, previewPaneKey, "width safety: visibleWidth(line) <= width across boundary widths", func(t *testing.T) {
			installFakeMarkdown(t)
			for _, w := range []int{100, 120, 160} {
				w := w
				p := makePreviewPane(question, func() int { return w })
				p.focusOption(0)
				requireWidths(t, p.pane.Render(w), w)
			}
		})
	}

	// ---- slack donation
	{
		const longLabel = "Verbose Descriptive Option"
		donate := func(p testPane, q QuestionData) func(int) int {
			return func(w int) int {
				return CrossTabLeftWidthWithDonation(1, [][]WrappingSelectItem{p.items}, []QuestionData{q}, w)
			}
		}
		tw(t, previewPaneKey, "short labels donate slack and move the preview to the right", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "A", Preview: "tiny"}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 200 })
			get := donate(p, q)
			p.pane.SetGlobalLeftWidth(get)
			p.focusOption(0)
			eqInt(t, get(200), int(float64(200)*MaxLeftRatio))
			top, _ := find(p.pane.Render(200), func(l string) bool { return strings.Contains(l, "┌") })
			eqInt(t, widthx.VisibleWidth(top), 200)
		})
		tw(t, previewPaneKey, "long labels + narrow previews → donation engaged, MD at wider offset", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: longLabel, Preview: "tiny"}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 120 })
			get := donate(p, q)
			p.pane.SetGlobalLeftWidth(get)
			p.focusOption(0)
			if AdaptiveLeftWidth(p.items, 3, 120) <= MinLeft {
				t.Fatal("labelDriven must exceed MinLeft")
			}
			lines := p.pane.Render(120)
			if !anyLine(lines, isMD) {
				t.Fatal("no MD line")
			}
			eqInt(t, get(120), int(float64(120)*MaxLeftRatio))
			top, _ := find(lines, func(l string) bool { return strings.Contains(l, "┌") })
			eqInt(t, widthx.VisibleWidth(top), 120)
		})
		tw(t, previewPaneKey, "long labels + wide previews → donation suppressed, MD at label-driven offset", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: longLabel, Preview: strings.Repeat("x", 500)}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 120 })
			p.pane.SetGlobalLeftWidth(donate(p, q))
			p.focusOption(0)
			md, ok := find(p.pane.Render(120), isMD)
			if !ok {
				t.Fatal("no MD line")
			}
			eqInt(t, jsstring.IndexOf(md, "MD[", 0), AdaptiveLeftWidth(p.items, 3, 120)+PreviewColumnGap+1+2)
		})
		tw(t, previewPaneKey, "right column never below MIN_PREVIEW_WIDTH when donation engages", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: longLabel, Preview: "tiny"}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 100 })
			get := donate(p, q)
			p.pane.SetGlobalLeftWidth(get)
			p.focusOption(0)
			if right := 100 - get(100) - PreviewColumnGap; right < MinPreviewWidth {
				t.Fatalf("right %d", right)
			}
			requireWidths(t, p.pane.Render(100), 100)
		})
		tw(t, previewPaneKey, "naturalHeight(w) === render(w).length holds with donation active", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: longLabel, Preview: "tiny"}, {Label: "B"}}}
			p := makePreviewPane(q, func() int { return 120 })
			p.pane.SetGlobalLeftWidth(donate(p, q))
			for _, w := range []int{100, 120, 160} {
				p.focusOption(0)
				eqInt(t, p.pane.NaturalHeight(w), len(p.pane.Render(w)))
			}
		})
	}

	// ---- renderBorderedBox
	id := func(s string) string { return s }
	tw(t, previewPaneKey, "wraps lines in 4-sided border with `┌─┐│└┘` corners", func(t *testing.T) {
		out := RenderBorderedBox([]string{"hello"}, 20, id, 0)
		last := out[len(out)-1]
		if !strings.HasPrefix(out[0], "┌") || !strings.HasSuffix(out[0], "┐") ||
			!strings.HasPrefix(out[1], "│") || !strings.HasSuffix(out[1], "│") ||
			!strings.HasPrefix(last, "└") || !strings.HasSuffix(last, "┘") {
			t.Fatalf("bad box: %q", out)
		}
	})
	tw(t, previewPaneKey, "right-pads content lines so the right `│` lands at fixed column", func(t *testing.T) {
		eqInt(t, widthx.VisibleWidth(RenderBorderedBox([]string{"hi"}, 20, id, 0)[1]), 20)
	})
	tw(t, previewPaneKey, "emits truncation indicator on bottom row when hidden > 0", func(t *testing.T) {
		out := RenderBorderedBox([]string{"a", "b"}, 30, id, 5)
		bottom := out[len(out)-1]
		if !strings.Contains(bottom, "✂") || !strings.Contains(bottom, "5 lines hidden") ||
			!strings.HasPrefix(bottom, "└") || !strings.HasSuffix(bottom, "┘") {
			t.Fatalf("bad bottom: %q", bottom)
		}
	})

	tw(t, previewPaneKey, "passes raw multi-line markdown to Markdown (oneLine collapse removed)", func(t *testing.T) {
		installFakeMarkdown(t)
		q := QuestionData{Question: "q", Header: "q", Options: []OptionData{{Label: "A", Preview: "## Heading\n\n- item 1\n- item 2"}, {Label: "B"}}}
		p := makePreviewPane(q, func() int { return 120 })
		p.focusOption(0)
		p.pane.Render(120)
		if previewLastMarkdownText != "## Heading\n\n- item 1\n- item 2" || !strings.Contains(previewLastMarkdownText, "\n") {
			t.Fatalf("got %q", previewLastMarkdownText)
		}
	})

	// ---- notes affordance
	{
		question := QuestionData{Question: "q", Header: "q", Options: []OptionData{{Label: "A", Preview: "alpha body"}, {Label: "B"}}}
		props := func(idx int, notes bool) PreviewPaneProps {
			return PreviewPaneProps{NotesVisible: notes, SelectedIndex: idx, Focused: true}
		}
		tw(t, previewPaneKey, "renders 'Notes: press n to add notes' below preview when focused on preview-bearing option", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			p.pane.SetProps(props(0, false))
			if !anyLineContains(p.pane.Render(120), NotesAffordanceText) {
				t.Fatal("affordance missing")
			}
		})
		tw(t, previewPaneKey, "hides notes affordance text when option lacks preview (height contract preserved)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			p.pane.SetProps(props(0, false))
			linesA := p.pane.Render(120)
			if !anyLineContains(linesA, NotesAffordanceText) {
				t.Fatal("affordance missing for A")
			}
			p.focusOption(1)
			p.pane.SetProps(props(1, false))
			linesB := p.pane.Render(120)
			if anyLineContains(linesB, NotesAffordanceText) {
				t.Fatal("affordance must be hidden for B")
			}
			eqInt(t, len(linesA), len(linesB))
		})
		tw(t, previewPaneKey, "hides notes affordance when notesVisible (notes mode active)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			p.pane.SetProps(props(0, true))
			if anyLineContains(p.pane.Render(120), NotesAffordanceText) {
				t.Fatal("affordance must be hidden")
			}
		})
		tw(t, previewPaneKey, "does not render the affordance text when MAX_PREVIEW_HEIGHT_SIDE_BY_SIDE is reached but option lacks preview", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(1)
			p.pane.SetProps(props(1, false))
			if anyLineContains(p.pane.Render(120), NotesAffordanceText) {
				t.Fatal("affordance must be hidden")
			}
			eqInt(t, MaxPreviewHeightSideBySide, 20)
		})
		tw(t, previewPaneKey, "an affordance wider than the preview box slides left instead of clipping (long-locale headroom)", func(t *testing.T) {
			installFakeMarkdown(t)
			const long = "Notes : appuyez longuement sur la touche n pour ajouter des notes"
			// registerStrings + applyLocale("fr"): inject the French string into the locale tables and select it.
			i18nMu.Lock()
			tables := loadLocaleTables()
			prevFr := tables["fr"]
			overlay := map[string]string{}
			for k, v := range prevFr {
				overlay[k] = v
			}
			overlay["preview.notes_affordance"] = long
			tables["fr"] = overlay
			prevOverride := localeOverride
			localeOverride = "fr"
			i18nMu.Unlock()
			t.Cleanup(func() {
				i18nMu.Lock()
				tables["fr"] = prevFr
				localeOverride = prevOverride
				i18nMu.Unlock()
			})
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			p.pane.SetProps(props(0, false))
			lines := p.pane.Render(120)
			line, ok := find(lines, func(l string) bool { return strings.Contains(l, "Notes :") })
			if !ok || !strings.Contains(line, long) {
				t.Fatalf("affordance missing or clipped: %q", line)
			}
			if widthx.VisibleWidth(line) > 120 {
				t.Fatalf("wider than 120: %d", widthx.VisibleWidth(line))
			}
		})
	}

	// ---- composes OptionListView state
	{
		noPreview := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "Alpha"}, {Label: "Beta"}, {Label: "Gamma"}}}
		tw(t, previewPaneKey, "setConfirmedIndex(1) renders ` ✔` on row 2 even when cursor is on row 0", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(noPreview, func() int { return 120 })
			p.optionListView.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true, Confirmed: &OptionListConfirmed{Index: 1}})
			lines := p.pane.Render(120)
			if !anyLineContains(lines, "Beta ✔") || anyLineContains(lines, "Alpha ✔") {
				t.Fatalf("marker wrong: %q", lines)
			}
		})
		tw(t, previewPaneKey, "setConfirmedIndex(undefined) clears the marker", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(noPreview, func() int { return 120 })
			p.optionListView.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true, Confirmed: &OptionListConfirmed{Index: 1}})
			p.optionListView.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true})
			if strings.Contains(strings.Join(p.pane.Render(120), "\n"), "✔") {
				t.Fatal("marker not cleared")
			}
		})
		tw(t, previewPaneKey, "OptionListView.setProps({inputBuffer:'Hello'}) flows to the inline-input row render", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "Alpha"}, {Label: "Beta"}}}
			items := []WrappingSelectItem{{Kind: KindOption, Label: "Alpha"}, {Kind: KindOption, Label: "Beta"}, {Kind: KindOther, Label: "Type something."}}
			olv := NewOptionListView(OptionListViewConfig{Items: items, Theme: selectThemeForTest()})
			pb := NewPreviewBlockRenderer(PreviewBlockRendererConfig{Question: q, Theme: identityTheme{}, MarkdownTheme: identityMarkdownTheme()})
			pane := NewPreviewPane(PreviewPaneConfig{Question: q, GetTerminalWidth: func() int { return 120 }, OptionListView: olv, PreviewBlock: pb})
			pane.SetGlobalLeftWidth(func(w int) int { return AdaptiveLeftWidth(items, len(items), w) })
			pane.SetProps(PreviewPaneProps{SelectedIndex: 2, Focused: true})
			olv.SetProps(OptionListViewProps{SelectedIndex: 2, Focused: true, InputBuffer: "Hello"})
			lines := pane.Render(120)
			if !anyLineContains(lines, "Hello") || !anyLineContains(lines, widthx.CursorMarker) {
				t.Fatalf("inline input missing: %q", lines)
			}
		})
	}

	// ---- adaptive left column width
	{
		question := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "Short", Preview: "body"}, {Label: "Another"}}}
		tw(t, previewPaneKey, "short labels floor at MIN_LEFT (30)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			p.focusOption(0)
			eqInt(t, AdaptiveLeftWidth(p.items, 3, 120), MinLeft)
			requireWidths(t, p.pane.Render(120), 120)
		})
		tw(t, previewPaneKey, "long labels produce wider left column up to MAX_LEFT_RATIO", func(t *testing.T) {
			installFakeMarkdown(t)
			q := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
				{Label: "A very long option label that tests the ratio cap", Preview: "body"}, {Label: "B"},
			}}
			p := makePreviewPane(q, func() int { return 120 })
			p.focusOption(0)
			leftW := AdaptiveLeftWidth(p.items, 3, 120)
			if leftW <= MinLeft || leftW > int(float64(120)*MaxLeftRatio) {
				t.Fatalf("leftW %d", leftW)
			}
			requireWidths(t, p.pane.Render(120), 120)
		})
		tw(t, previewPaneKey, "MIN_PREVIEW_WIDTH safety net prevents right-side collapse", func(t *testing.T) {
			items := []WrappingSelectItem{{Kind: KindOption, Label: strings.Repeat("x", 60)}, {Kind: KindOption, Label: "y"}}
			if leftW := AdaptiveLeftWidth(items, 3, 82); leftW > 82-PreviewColumnGap-MinPreviewWidth {
				t.Fatalf("leftW %d", leftW)
			}
		})
		tw(t, previewPaneKey, "render() throws if setGlobalLeftWidth was not injected — render is illegal pre-injection", func(t *testing.T) {
			installFakeMarkdown(t)
			items := []WrappingSelectItem{{Kind: KindOption, Label: "Short"}, {Kind: KindOption, Label: "Another"}}
			olv := NewOptionListView(OptionListViewConfig{Items: items, Theme: selectThemeForTest()})
			pb := NewPreviewBlockRenderer(PreviewBlockRendererConfig{Question: question, Theme: identityTheme{}, MarkdownTheme: identityMarkdownTheme()})
			pane := NewPreviewPane(PreviewPaneConfig{Question: question, GetTerminalWidth: func() int { return 120 }, OptionListView: olv, PreviewBlock: pb})
			olv.SetProps(OptionListViewProps{SelectedIndex: 0, Focused: true})
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				pane.Render(120)
			}()
			if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "setGlobalLeftWidth") {
				t.Fatalf("want panic mentioning setGlobalLeftWidth, got %v", recovered)
			}
		})
		tw(t, previewPaneKey, "cross-tab max keeps width stable — same getter produces same leftWidth for different items", func(t *testing.T) {
			shortItems := []WrappingSelectItem{{Kind: KindOption, Label: "A"}}
			longItems := []WrappingSelectItem{{Kind: KindOption, Label: "A very long label indeed"}}
			shortW := AdaptiveLeftWidth(shortItems, 2, 120)
			longW := AdaptiveLeftWidth(longItems, 2, 120)
			globalMax := max(shortW, longW)
			eqInt(t, globalMax, longW)
			getter := func(w int) int { return max(AdaptiveLeftWidth(shortItems, 2, w), AdaptiveLeftWidth(longItems, 2, w)) }
			eqInt(t, getter(120), globalMax)
		})
		tw(t, previewPaneKey, "naturalHeight(w) === render(w).length still holds with adaptive width", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makePreviewPane(question, func() int { return 120 })
			for _, w := range []int{100, 120, 160} {
				p.focusOption(0)
				eqInt(t, p.pane.NaturalHeight(w), len(p.pane.Render(w)))
			}
		})
	}

	// ---- inputMode
	{
		previewQuestion := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
			{Label: "A", Preview: "alpha body"}, {Label: "B", Preview: "beta body"},
		}}
		makeWithOther := func() testPane {
			items := []WrappingSelectItem{{Kind: KindOption, Label: "A"}, {Kind: KindOption, Label: "B"}, {Kind: KindOther, Label: "Type something."}}
			olv := NewOptionListView(OptionListViewConfig{Items: items, Theme: selectThemeForTest()})
			pb := NewPreviewBlockRenderer(PreviewBlockRendererConfig{Question: previewQuestion, Theme: identityTheme{}, MarkdownTheme: identityMarkdownTheme()})
			pane := NewPreviewPane(PreviewPaneConfig{Question: previewQuestion, GetTerminalWidth: func() int { return 120 }, OptionListView: olv, PreviewBlock: pb})
			pane.SetGlobalLeftWidth(func(w int) int { return AdaptiveLeftWidth(items, len(items), w) })
			return testPane{pane, olv, pb, items}
		}
		typing := func(p testPane, buf string) {
			p.pane.SetProps(PreviewPaneProps{SelectedIndex: 2, Focused: true, InputMode: true})
			p.optionListView.SetProps(OptionListViewProps{SelectedIndex: 2, Focused: true, InputBuffer: buf})
		}
		tw(t, previewPaneKey, "inputMode: true → render is a single full-width option list (no side-by-side split, no preview border)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makeWithOther()
			typing(p, "custom text")
			lines := p.pane.Render(120)
			if anyLine(lines, isMD) || anyLine(lines, boxRe.MatchString) {
				t.Fatalf("preview leaked: %q", lines)
			}
			if !reflect.DeepEqual(lines, p.optionListView.Render(120)) {
				t.Fatal("not byte-identical to the full-width option list")
			}
		})
		tw(t, previewPaneKey, "inputMode: false → side-by-side layout renders as before (MD + border present; inputMode branch skipped)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makeWithOther()
			p.pane.SetProps(PreviewPaneProps{SelectedIndex: 0, Focused: true})
			p.focusOption(0)
			lines := p.pane.Render(120)
			if !anyLine(lines, isMD) || !anyLine(lines, boxRe.MatchString) {
				t.Fatalf("preview missing: %q", lines)
			}
		})
		tw(t, previewPaneKey, "inputMode: true → naturalHeight(w) === render(w).length and maxNaturalHeight(w) === render(w).length", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makeWithOther()
			typing(p, strings.Repeat("x", 200))
			for _, w := range []int{100, 120, 160} {
				eqInt(t, p.pane.NaturalHeight(w), len(p.pane.Render(w)))
				eqInt(t, p.pane.MaxNaturalHeight(w), len(p.pane.Render(w)))
				eqInt(t, p.pane.MaxNaturalHeight(w), len(p.optionListView.Render(w)))
			}
		})
		tw(t, previewPaneKey, "inputMode: true → focusedItemRowRange computed at FULL width (matches the full-width option list)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makeWithOther()
			typing(p, "")
			a0, a1 := p.pane.FocusedItemRowRange(120)
			b0, b1 := p.optionListView.FocusedItemRowRange(120)
			if a0 != b0 || a1 != b1 {
				t.Fatalf("[%d %d] != [%d %d]", a0, a1, b0, b1)
			}
		})
		tw(t, previewPaneKey, "preview layout restored on nav-away (inputMode clears → side-by-side returns)", func(t *testing.T) {
			installFakeMarkdown(t)
			p := makeWithOther()
			typing(p, "half-typed")
			if anyLine(p.pane.Render(120), isMD) {
				t.Fatal("preview shown while typing")
			}
			p.pane.SetProps(PreviewPaneProps{SelectedIndex: 0, Focused: true})
			p.focusOption(0)
			restored := p.pane.Render(120)
			if !anyLine(restored, isMD) || !anyLine(restored, boxRe.MatchString) {
				t.Fatalf("preview not restored: %q", restored)
			}
		})
	}
}
