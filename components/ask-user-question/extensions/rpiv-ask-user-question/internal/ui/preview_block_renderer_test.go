package ui

import (
	"strconv"
	"strings"
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Twin of preview-block-renderer.test.ts. The original mocks pi-tui's Markdown with a FakeMarkdown that renders
// `MD[width]:text.slice(0, max(0, width-4))` and counts constructions; the Go twin swaps newMarkdownRenderer.

const previewBlockKey = "view/components/preview/preview-block-renderer"

var previewMarkdownConstructed int
var previewLastMarkdownText string

type fakeMarkdown struct{ text string }

func (f *fakeMarkdown) Render(width int) []string {
	n := max(0, width-4)
	text := f.text
	if len(text) > n { // the fixtures are ASCII
		text = text[:n]
	}
	return []string{"MD[" + strconv.Itoa(width) + "]:" + text}
}
func (f *fakeMarkdown) Invalidate() {}

// installFakeMarkdown mirrors the vi.mock of Markdown plus the beforeEach counter reset.
func installFakeMarkdown(t *testing.T) {
	t.Helper()
	orig := newMarkdownRenderer
	previewMarkdownConstructed = 0
	previewLastMarkdownText = ""
	newMarkdownRenderer = func(text string, _ *tui.MarkdownTheme) markdownRenderer {
		previewMarkdownConstructed++
		previewLastMarkdownText = text
		return &fakeMarkdown{text: text}
	}
	t.Cleanup(func() { newMarkdownRenderer = orig })
}

func identityMarkdownTheme() *tui.MarkdownTheme {
	id := func(s string) string { return s }
	return &tui.MarkdownTheme{
		Heading: id, Link: id, LinkUrl: id, Code: id, CodeBlock: id, CodeBlockBorder: id, Quote: id, QuoteBorder: id,
		Hr: id, ListBullet: id, Bold: id, Italic: id, Strikethrough: id, Underline: id,
	}
}

func anyLine(lines []string, pred func(string) bool) bool {
	for _, l := range lines {
		if pred(l) {
			return true
		}
	}
	return false
}

func anyLineContains(lines []string, sub string) bool {
	return anyLine(lines, func(l string) bool { return strings.Contains(l, sub) })
}

func TestPreviewBlockRenderer(t *testing.T) {
	theme := identityTheme{}
	mt := identityMarkdownTheme()
	previewQuestion := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{
		{Label: "A", Preview: "## A\n\nbody A"},
		{Label: "B", Preview: "## B\n\nbody B"},
		{Label: "C"},
	}}
	noPreviewQuestion := QuestionData{Question: "pick", Header: "pick", Options: []OptionData{{Label: "A"}, {Label: "B"}}}
	newR := func(q QuestionData) *PreviewBlockRenderer {
		return NewPreviewBlockRenderer(PreviewBlockRendererConfig{Question: q, Theme: theme, MarkdownTheme: mt})
	}

	tw(t, previewBlockKey, "hasAnyPreview returns true when at least one option carries preview", func(t *testing.T) {
		installFakeMarkdown(t)
		if !newR(previewQuestion).HasAnyPreview() {
			t.Fatal("want true")
		}
	})
	tw(t, previewBlockKey, "hasAnyPreview returns false when no option carries preview", func(t *testing.T) {
		installFakeMarkdown(t)
		if newR(noPreviewQuestion).HasAnyPreview() {
			t.Fatal("want false")
		}
	})
	tw(t, previewBlockKey, "has(i) is true for preview-bearing option, false for option without preview", func(t *testing.T) {
		installFakeMarkdown(t)
		r := newR(previewQuestion)
		if !r.Has(0) || r.Has(2) {
			t.Fatal("has(0) must be true and has(2) false")
		}
	})
	tw(t, previewBlockKey, "emits bordered box + blank + affordance when focused on preview-bearing option", func(t *testing.T) {
		installFakeMarkdown(t)
		lines := newR(previewQuestion).RenderBlock(60, 0, LayoutSideBySide, true, false)
		if !anyLine(lines, func(l string) bool { return strings.HasPrefix(l, "┌") }) ||
			!anyLine(lines, func(l string) bool { return strings.HasPrefix(l, "└") }) ||
			!anyLineContains(lines, NotesAffordanceText) {
			t.Fatalf("missing box or affordance: %q", lines)
		}
	})
	tw(t, previewBlockKey, "hides affordance when notesVisible=true (notes mode active)", func(t *testing.T) {
		installFakeMarkdown(t)
		lines := newR(previewQuestion).RenderBlock(60, 0, LayoutSideBySide, true, true)
		if anyLineContains(lines, NotesAffordanceText) {
			t.Fatal("affordance must be hidden")
		}
	})
	tw(t, previewBlockKey, "hides affordance when focused=false (cursor elsewhere)", func(t *testing.T) {
		installFakeMarkdown(t)
		lines := newR(previewQuestion).RenderBlock(60, 0, LayoutSideBySide, false, false)
		if anyLineContains(lines, NotesAffordanceText) {
			t.Fatal("affordance must be hidden")
		}
	})
	tw(t, previewBlockKey, "hides affordance when focused option lacks a preview (height contract preserved)", func(t *testing.T) {
		installFakeMarkdown(t)
		r := newR(previewQuestion)
		linesA := r.RenderBlock(60, 0, LayoutSideBySide, true, false)
		linesB := r.RenderBlock(60, 2, LayoutSideBySide, true, false)
		if !anyLineContains(linesA, NotesAffordanceText) || anyLineContains(linesB, NotesAffordanceText) {
			t.Fatal("affordance gating wrong")
		}
		if len(linesA) != len(linesB) {
			t.Fatalf("heights differ: %d vs %d", len(linesA), len(linesB))
		}
	})
	tw(t, previewBlockKey, "matches renderBlock(...).length under all gating combinations", func(t *testing.T) {
		installFakeMarkdown(t)
		r := newR(previewQuestion)
		for _, idx := range []int{0, 1, 2} {
			for _, mode := range []PreviewLayoutMode{LayoutSideBySide, LayoutStacked} {
				if got, want := r.BlockHeight(60, idx, mode), len(r.RenderBlock(60, idx, mode, true, false)); got != want {
					t.Fatalf("idx %d %s: blockHeight %d != %d", idx, mode, got, want)
				}
			}
		}
	})
	tw(t, previewBlockKey, "creates one Markdown per option lazily; revisit hits cache", func(t *testing.T) {
		installFakeMarkdown(t)
		r := newR(previewQuestion)
		r.RenderBlock(60, 0, LayoutSideBySide, true, false)
		if previewMarkdownConstructed != 1 {
			t.Fatalf("got %d", previewMarkdownConstructed)
		}
		r.RenderBlock(60, 1, LayoutSideBySide, true, false)
		if previewMarkdownConstructed != 2 {
			t.Fatalf("got %d", previewMarkdownConstructed)
		}
		r.RenderBlock(60, 0, LayoutSideBySide, true, false)
		if previewMarkdownConstructed != 2 {
			t.Fatalf("got %d", previewMarkdownConstructed)
		}
	})
	tw(t, previewBlockKey, "invalidate() does NOT delete instances; subsequent renders re-use cache", func(t *testing.T) {
		installFakeMarkdown(t)
		r := newR(previewQuestion)
		r.RenderBlock(60, 0, LayoutSideBySide, true, false)
		if previewMarkdownConstructed != 1 {
			t.Fatalf("got %d", previewMarkdownConstructed)
		}
		r.Invalidate()
		r.RenderBlock(60, 0, LayoutSideBySide, true, false)
		if previewMarkdownConstructed != 1 {
			t.Fatalf("got %d", previewMarkdownConstructed)
		}
	})
}
