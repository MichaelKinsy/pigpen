package tintinweb_subagents

import (
	"fmt"
	"strconv"
)

// selectUI is the part of ctx.ui that selectItem needs: strings in, string out, no index or value form.
type selectUI interface {
	Select(title string, options []string) (choice string, ok bool, err error)
}

// selectItem shows a numbered picker and returns the chosen item (not its label), or false when the user escapes or
// the returned string is not one we offered. Numbering every row makes the labels unique by construction, so two rows
// that format identically (truncated, LLM-authored text) cannot resolve to the first match. upstream: ui/select-item.ts.
func selectItem[T any](ui selectUI, title string, items []T, format func(item T, index int) string) (T, bool) {
	var zero T
	// Pad the number so a 10+ item list keeps its columns aligned.
	width := len(strconv.Itoa(len(items)))
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = fmt.Sprintf("%*d. %s", width, i+1, format(item, i))
	}
	choice, ok, err := ui.Select(title, labels)
	if err != nil || !ok || choice == "" {
		return zero, false
	}
	for i, l := range labels {
		if l == choice {
			return items[i], true
		}
	}
	return zero, false
}
