package powerline_footer

import (
	"strings"
	"testing"
)

// tagTheme shows what the overlay asks of its theme: bold becomes visible.
type tagTheme struct{}

func (tagTheme) Fg(_, text string) string { return text }
func (tagTheme) Bold(text string) string  { return "<b>" + text + "</b>" }

func TestSelectOverlayTitleIsBold(t *testing.T) {
	o := newSelectOverlay(tagTheme{}, "Powerline queue", "hint", []selectItem{{Value: "a", Label: "a"}}, 5)
	lines := o.Render(40)
	if !strings.Contains(lines[1], "<b>Powerline queue</b>") {
		t.Errorf("title row %q is not bold", lines[1])
	}
}

// pi-tui's SelectList shows a description only when more than 10 cells remain for it.
func TestSelectListDescriptionNeedsMoreThanTenCells(t *testing.T) {
	l := &selectList{items: []selectItem{{Value: "x", Label: "x", Description: "abcdefghijklmnop"}, {Value: "y"}}, selectedIndex: 1, maxVisible: 5, theme: tagTheme{}}
	eq(t, l.render(46)[0], "  x", "10 cells left: no description")
	eq(t, l.render(47)[0], "  x"+strings.Repeat(" ", 31)+"abcdefghijk", "11 cells left: the description, cut to them")
}

func TestBuildStashPreview(t *testing.T) {
	eq(t, buildStashPreview(" \n\t ", 10), "(empty)", "blank text")
	eq(t, buildStashPreview("  fix \n the   test ", 40), "fix the test", "collapsed and fitted")
}
