package tui

import (
	"strings"
	"testing"
)

// Pins found by the ask-user-question frame harness against Pi 1402973a: the markdown cases the PiG v0.5.0 re-sync
// brought in (marked's GFM autolinks, indented code blocks, images), and the kitty shift+letter decode.

func plainRender(t *testing.T, src string) []string {
	t.Helper()
	f := false
	SetHyperlinksSupported(&f)
	t.Cleanup(func() { SetHyperlinksSupported(nil) })
	return stripANSILines(NewMarkdownWithOptions(src, 0, 0, testMarkdownTheme(), nil, nil).Render(60))
}

func TestMarkdownBareWwwIsAutolinkedLikePi(t *testing.T) {
	got := strings.Join(plainRender(t, "see www.example.org now"), "\n")
	if !strings.Contains(got, "www.example.org") || !strings.Contains(got, "(http://www.example.org)") {
		t.Fatalf("www autolink: %q", got)
	}
}

func TestMarkdownIndentedCodeBlockRendersAsCodeLikePi(t *testing.T) {
	got := plainRender(t, "para\n\n    indented code block\n\nafter")
	want := []string{"para", "", "```", "  indented code block", "```", "", "after"}
	for i := range want {
		if strings.TrimRight(got[i], " ") != want[i] {
			t.Fatalf("row %d = %q, want %q (all: %q)", i, got[i], want[i], got)
		}
	}
	if !strings.Contains(strings.Join(NewMarkdownWithOptions("    x", 0, 0, testMarkdownTheme(), nil, nil).Render(20), ""), ActiveTheme().MDCodeBlock) {
		t.Fatal("indented code is not styled with the code block style")
	}
}

func TestMarkdownImageRendersItsAltTextLikePi(t *testing.T) {
	got := strings.TrimRight(plainRender(t, "![image alt](http://x/y.png)")[0], " ")
	if got != "image alt" {
		t.Fatalf("image row = %q, want %q", got, "image alt")
	}
}
