package ask_user_question

import "testing"

func TestBuildItemsForQuestion(t *testing.T) {
	other := selectItem{Kind: "other", Label: "Type something."}
	next := selectItem{Kind: "next", Label: "Next"}
	a, b := selectItem{Kind: "option", Label: "A", Description: "a"}, selectItem{Kind: "option", Label: "B", Description: "b"}
	tw(t, fMain, "appends the Type-something sentinel", func(t *testing.T) {
		eq(t, buildItemsForQuestion(qn("Q?", "H", two()...)), []selectItem{a, b, other}, "items")
	})
	tw(t, fMain, "appends 'Type something.' + Next sentinels when multiSelect is true", func(t *testing.T) {
		eq(t, buildItemsForQuestion(multi(qn("Q?", "H", two()...))), []selectItem{a, b, other, next}, "items")
	})
	tw(t, fMain, "appends the sentinel when multiSelect is false", func(t *testing.T) {
		q := qn("Q?", "H", two()...)
		q.MultiSelect = false
		eq(t, buildItemsForQuestion(q), []selectItem{a, b, other}, "items")
	})
	tw(t, fMain, "appends the sentinel when multiSelect is undefined (default single-select)", func(t *testing.T) {
		eq(t, at(buildItemsForQuestion(qn("Q?", "H", two()...)), 2), other, "sentinel")
	})
	tw(t, fMain, "appends the Type-something sentinel even when a single-select option carries a preview", func(t *testing.T) {
		q := qn("Q?", "H", option{Label: "A", Description: "a", Preview: "P"}, opt("B", "b"))
		eq(t, at(buildItemsForQuestion(q), 2), other, "sentinel")
	})
	tw(t, fMain, "appends the Type-something sentinel for single-select regardless of preview content", func(t *testing.T) {
		for _, p := range []string{"", "x", "line\nline"} {
			items := buildItemsForQuestion(qn("Q?", "H", option{Label: "A", Description: "a", Preview: p}, opt("B", "b")))
			eq(t, at(items, len(items)-1), other, "last row")
			eq(t, len(items), 3, "rows")
		}
	})
	tw(t, fMain, "appends 'Type something.' + Next for multiSelect even if an option has a preview (preview is dropped)", func(t *testing.T) {
		items := buildItemsForQuestion(multi(qn("Q?", "H", option{Label: "A", Description: "a", Preview: "P"}, opt("B", "b"))))
		eq(t, items, []selectItem{a, b, other, next}, "items")
	})
}
