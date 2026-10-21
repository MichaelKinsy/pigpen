package ui

import (
	"strings"
	"testing"
)

// Twin of preview-layout-decider.test.ts. The original's `tabs` argument only contributes its length, so the Go
// functions take tabCount; the twins pass the same number of tabs.

const previewDeciderKey = "view/components/preview/preview-layout-decider"

func dOpt(label string) WrappingSelectItem { return WrappingSelectItem{Kind: KindOption, Label: label} }

type dOptSpec struct {
	label   string
	preview string
}

func dQuestion(options ...dOptSpec) QuestionData {
	q := QuestionData{Question: "Test?", Header: "Test"}
	for _, o := range options {
		q.Options = append(q.Options, OptionData{Label: o.label, Preview: o.preview})
	}
	return q
}

func eqInt(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestPreviewLayoutDecider(t *testing.T) {
	maxLeftCeiling := func(w int) int { return int(float64(w) * MaxLeftRatio) }

	tw(t, previewDeciderKey, "returns MIN_LEFT for empty input", func(t *testing.T) {
		eqInt(t, CrossTabMaxLeftWidth(0, nil, 120), MinLeft)
	})
	tw(t, previewDeciderKey, "floors at MIN_LEFT when every tab has short labels", func(t *testing.T) {
		items := [][]WrappingSelectItem{{dOpt("A"), dOpt("B")}, {dOpt("C")}}
		eqInt(t, CrossTabMaxLeftWidth(2, items, 120), MinLeft)
	})
	tw(t, previewDeciderKey, "returns the widest tab — single long label dominates short tabs", func(t *testing.T) {
		longLabel := "A very long option label that exceeds MIN_LEFT comfortably"
		items := [][]WrappingSelectItem{{dOpt("A"), dOpt("B")}, {dOpt(longLabel), dOpt("y")}}
		longSingleTab := AdaptiveLeftWidth([]WrappingSelectItem{dOpt(longLabel), dOpt("y")}, 3, 120)
		eqInt(t, CrossTabMaxLeftWidth(2, items, 120), longSingleTab)
		if longSingleTab <= MinLeft {
			t.Fatalf("longSingleTab %d must exceed MinLeft", longSingleTab)
		}
	})
	tw(t, previewDeciderKey, "respects MAX_LEFT_RATIO ceiling — never exceeds floor(paneWidth * 0.5)", func(t *testing.T) {
		items := [][]WrappingSelectItem{{dOpt(strings.Repeat("x", 120)), dOpt("y")}}
		if r := CrossTabMaxLeftWidth(1, items, 120); r > maxLeftCeiling(120) {
			t.Fatalf("%d > %d", r, maxLeftCeiling(120))
		}
	})
	tw(t, previewDeciderKey, "respects MIN_PREVIEW_WIDTH safety net on narrow panes", func(t *testing.T) {
		items := [][]WrappingSelectItem{{dOpt(strings.Repeat("x", 60)), dOpt("y")}}
		if r := CrossTabMaxLeftWidth(1, items, 82); r > 82-PreviewColumnGap-MinPreviewWidth {
			t.Fatalf("%d too wide", r)
		}
	})
	tw(t, previewDeciderKey, "single- and multi-select tabs use items.length for numbering (no chat row slot)", func(t *testing.T) {
		longLabel := strings.Repeat("x", 30)
		items := make([]WrappingSelectItem, 9)
		for i := range items {
			items[i] = dOpt(longLabel)
		}
		// Both tab kinds feed the same items.length; the multiSelect flag is not an input.
		multi := CrossTabMaxLeftWidth(1, [][]WrappingSelectItem{items}, 200)
		single := CrossTabMaxLeftWidth(1, [][]WrappingSelectItem{items}, 200)
		eqInt(t, single, multi)
	})
	tw(t, previewDeciderKey, "idempotent across tab order — max is permutation-invariant", func(t *testing.T) {
		a := []WrappingSelectItem{dOpt("short")}
		b := []WrappingSelectItem{dOpt("a much longer option label here")}
		eqInt(t, CrossTabMaxLeftWidth(2, [][]WrappingSelectItem{a, b}, 120), CrossTabMaxLeftWidth(2, [][]WrappingSelectItem{b, a}, 120))
	})
	tw(t, previewDeciderKey, "missing itemsByTab[i] is treated as an empty tab, not a crash", func(t *testing.T) {
		items := [][]WrappingSelectItem{{dOpt("a")}}
		eqInt(t, CrossTabMaxLeftWidth(2, items, 120), MinLeft)
	})

	tw(t, previewDeciderKey, "returns 0 when no option has a preview", func(t *testing.T) {
		eqInt(t, PreviewSourceWidth(dQuestion(dOptSpec{label: "A"}, dOptSpec{label: "B"})), 0)
	})
	tw(t, previewDeciderKey, "returns max source-line width for single-line preview", func(t *testing.T) {
		q := dQuestion(dOptSpec{"A", "short"}, dOptSpec{"B", "a longer line here"})
		eqInt(t, PreviewSourceWidth(q), len("a longer line here"))
	})
	tw(t, previewDeciderKey, "returns max across multiple lines in a single preview", func(t *testing.T) {
		q := dQuestion(dOptSpec{"A", "short\na much longer line here"})
		eqInt(t, PreviewSourceWidth(q), len("a much longer line here"))
	})
	tw(t, previewDeciderKey, "returns max across all options' previews", func(t *testing.T) {
		q := dQuestion(dOptSpec{"A", "tiny"}, dOptSpec{"B", "small\na very wide preview line"})
		eqInt(t, PreviewSourceWidth(q), len("a very wide preview line"))
	})
	tw(t, previewDeciderKey, "skips options without preview", func(t *testing.T) {
		q := dQuestion(dOptSpec{label: "A"}, dOptSpec{"B", "wide content"})
		eqInt(t, PreviewSourceWidth(q), len("wide content"))
	})

	const overhead = 5 // BORDER_HORIZONTAL_OVERHEAD(2) + 2*BORDER_INNER_PADDING_HORIZONTAL(2) + PREVIEW_PADDING_LEFT(1)
	tw(t, previewDeciderKey, "floors at MIN_PREVIEW_WIDTH when no question has previews", func(t *testing.T) {
		qs := []QuestionData{dQuestion(dOptSpec{label: "A"}), dQuestion(dOptSpec{label: "B"})}
		eqInt(t, CrossTabPreviewBudget(qs, 120), MinPreviewWidth)
	})
	tw(t, previewDeciderKey, "returns preview content width + overhead when previews are present", func(t *testing.T) {
		qs := []QuestionData{dQuestion(dOptSpec{"A", strings.Repeat("x", 50)})}
		eqInt(t, CrossTabPreviewBudget(qs, 200), 50+overhead)
	})
	tw(t, previewDeciderKey, "takes the max across all questions", func(t *testing.T) {
		qs := []QuestionData{dQuestion(dOptSpec{"A", "short"}), dQuestion(dOptSpec{"B", strings.Repeat("x", 80)})}
		eqInt(t, CrossTabPreviewBudget(qs, 200), 80+overhead)
	})
	tw(t, previewDeciderKey, "ceilings at paneWidth - GAP - MIN_LEFT", func(t *testing.T) {
		qs := []QuestionData{dQuestion(dOptSpec{"A", strings.Repeat("x", 200)})}
		eqInt(t, CrossTabPreviewBudget(qs, 120), (120-PreviewColumnGap-MinLeft)+overhead)
	})
	tw(t, previewDeciderKey, "is permutation-invariant", func(t *testing.T) {
		a := dQuestion(dOptSpec{"A", strings.Repeat("x", 30)})
		b := dQuestion(dOptSpec{"B", strings.Repeat("x", 70)})
		eqInt(t, CrossTabPreviewBudget([]QuestionData{a, b}, 120), CrossTabPreviewBudget([]QuestionData{b, a}, 120))
	})

	const longLabel = "Verbose Descriptive Option" // 26 chars: labelDriven = 26 + 5 + 2 = 33 > MIN_LEFT
	tw(t, previewDeciderKey, "short labels still donate newly available width", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt("npm"), dOpt("yarn")}}
		qs := []QuestionData{dQuestion(dOptSpec{"npm", "npm install"}, dOptSpec{"yarn", "yarn install"})}
		eqInt(t, CrossTabMaxLeftWidth(1, itemsByTab, 200), MinLeft)
		eqInt(t, CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 200), maxLeftCeiling(200))
	})
	tw(t, previewDeciderKey, "donation uses the tighter of the ratio and preview-safety ceilings", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel), dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{label: longLabel}, dOptSpec{label: "B"})}
		if CrossTabMaxLeftWidth(1, itemsByTab, 120) <= MinLeft {
			t.Fatal("labelDriven must exceed MinLeft")
		}
		result := CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120)
		eqInt(t, result, min(120-PreviewColumnGap-MinPreviewWidth, maxLeftCeiling(120)))
	})
	tw(t, previewDeciderKey, "long labels + narrow previews → slack donation engaged", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel), dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, "tiny"}, dOptSpec{label: "B"})}
		if d, l := CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120), CrossTabMaxLeftWidth(1, itemsByTab, 120); d <= l {
			t.Fatalf("donated %d <= labelDriven %d", d, l)
		}
	})
	tw(t, previewDeciderKey, "long labels + any wide preview → reverts to label-driven (donation suppressed)", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel), dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, strings.Repeat("x", 100)})}
		eqInt(t, CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120), CrossTabMaxLeftWidth(1, itemsByTab, 120))
	})
	tw(t, previewDeciderKey, "any wide preview anywhere suppresses donation across all tabs", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel)}, {dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, "tiny"}), dQuestion(dOptSpec{"B", strings.Repeat("x", 100)})}
		eqInt(t, CrossTabLeftWidthWithDonation(2, itemsByTab, qs, 120), CrossTabMaxLeftWidth(2, itemsByTab, 120))
	})
	tw(t, previewDeciderKey, "respects MIN_PREVIEW_WIDTH ceiling — right column never below MIN_PREVIEW_WIDTH when donating", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel), dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, "tiny"})}
		result := CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120)
		if right := 120 - result - PreviewColumnGap; right < MinPreviewWidth {
			t.Fatalf("right column %d", right)
		}
	})
	tw(t, previewDeciderKey, "is permutation-invariant across tab order", func(t *testing.T) {
		a := []WrappingSelectItem{dOpt(longLabel)}
		b := []WrappingSelectItem{dOpt("B")}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, "tiny"}), dQuestion(dOptSpec{"B", strings.Repeat("x", 60)})}
		eqInt(t, CrossTabLeftWidthWithDonation(2, [][]WrappingSelectItem{a, b}, qs, 120),
			CrossTabLeftWidthWithDonation(2, [][]WrappingSelectItem{b, a}, qs, 120))
	})
	tw(t, previewDeciderKey, "floors at MIN_LEFT even with donation", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt(longLabel)}}
		qs := []QuestionData{dQuestion(dOptSpec{longLabel, "tiny"})}
		if r := CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 200); r < MinLeft {
			t.Fatalf("%d < MinLeft", r)
		}
	})
	tw(t, previewDeciderKey, "is deterministic — same args always produce same result", func(t *testing.T) {
		itemsByTab := [][]WrappingSelectItem{{dOpt("A"), dOpt("B")}}
		qs := []QuestionData{dQuestion(dOptSpec{"A", "medium content"})}
		eqInt(t, CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120), CrossTabLeftWidthWithDonation(1, itemsByTab, qs, 120))
	})
}
