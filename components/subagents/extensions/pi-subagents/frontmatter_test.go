package pi_subagents

import (
	"strings"
	"testing"
)

func TestFrontmatterBlocks(t *testing.T) {
	const f = "agent-frontmatter"
	tw(t, f, "folds ordinary lines and paragraph breaks for > and >-", func(t *testing.T) {
		expected := "first line second line\nthird line"
		folded, _ := ParseFrontmatter("---\nname: worker\ndescription: >\n  first line\n  second line\n\n  third line\n---\nbody")
		stripped, _ := ParseFrontmatter("---\nname: worker\ndescription: >-\n  first line\n  second line\n\n  third line\n---\nbody")
		eq(t, folded.Get("description"), expected, "folded")
		eq(t, stripped.Get("description"), expected, "stripped")
		eq(t, folded.Get("name"), "worker", "name")
	})
	tw(t, f, "keeps quoted indicators as literal values", func(t *testing.T) {
		parsed, _ := ParseFrontmatter("---\ndescription: \">\"\nother: '>-'\n---\nbody")
		eq(t, parsed.Get("description"), ">", "description")
		eq(t, parsed.Get("other"), ">-", "other")
	})
	tw(t, f, "preserves lines for | and |-", func(t *testing.T) {
		for _, indicator := range []string{"|", "|-"} {
			parsed, _ := ParseFrontmatter("---\ndescription: " + indicator + "\n  first line\n  second line\n---\nbody")
			eq(t, parsed.Get("description"), "first line\nsecond line", "description for "+indicator)
		}
	})
	tw(t, f, "preserves more-indented lines and repeated whitespace-only separators", func(t *testing.T) {
		parsed, _ := ParseFrontmatter("---\ndescription: >\n  normal\n    code\n  next\n\n  first\n\n  " + strings.Repeat(" ", 3) + "\n  second\nname: worker\n---\nbody")
		eq(t, parsed.Get("description"), "normal\n  code\nnext\nfirst\n\nsecond", "description")
		eq(t, parsed.Get("name"), "worker", "name")
	})
	tw(t, f, "keeps paragraph breaks adjacent to more-indented lines", func(t *testing.T) {
		after, _ := ParseFrontmatter("---\ndescription: >\n  normal\n    code\n\n  next\n---\nbody")
		before, _ := ParseFrontmatter("---\ndescription: >\n  normal\n\n    code\n  next\n---\nbody")
		eq(t, after.Get("description"), "normal\n  code\n\nnext", "after")
		eq(t, before.Get("description"), "normal\n\n  code\nnext", "before")
	})
}
