package pi_goal_x

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// ported from widgets/text-cache.ts: pure text transforms memoized by complete ANSI content and layout arguments.
// Theme changes naturally miss. The cache is insertion-ordered (oldest evicted first) and bounded by entry count and
// retained UTF-16 units, like the original.

const (
	textCacheMaxEntries = 1024
	textCacheMaxChars   = 500_000
)

type textCacheEntry struct {
	str   string
	lines []string
	chars int
}

var textCache = struct {
	entries  map[string]textCacheEntry
	order    []string
	retained int
}{entries: map[string]textCacheEntry{}}

func textCacheStore(key string, e textCacheEntry) {
	if e.chars > textCacheMaxChars {
		return
	}
	for len(textCache.entries) >= textCacheMaxEntries || textCache.retained+e.chars > textCacheMaxChars {
		oldest := textCache.order[0]
		textCache.order = textCache.order[1:]
		textCache.retained -= textCache.entries[oldest].chars
		delete(textCache.entries, oldest)
	}
	textCache.entries[key] = e
	textCache.order = append(textCache.order, key)
	textCache.retained += e.chars
}

// truncateToWidth is the memoized widthx.TruncateToWidth (TS defaults: ellipsis "...", pad false).
func truncateToWidth(text string, width int, ellipsis string, pad bool) string {
	key := "t:" + strconv.Itoa(width) + ":" + strconv.FormatBool(pad) + ":" + strconv.Itoa(utf16Len(ellipsis)) + ":" + ellipsis + text
	if c, ok := textCache.entries[key]; ok {
		return c.str
	}
	value := widthx.TruncateToWidth(text, width, ellipsis, pad)
	textCacheStore(key, textCacheEntry{str: value, chars: utf16Len(key) + utf16Len(value)})
	return value
}

// wrapTextWithAnsi is the memoized widthx.WrapTextWithAnsi; callers may edit the returned rows.
func wrapTextWithAnsi(text string, width int) []string {
	key := "w:" + strconv.Itoa(width) + ":" + text
	if c, ok := textCache.entries[key]; ok {
		return append([]string(nil), c.lines...)
	}
	value := widthx.WrapTextWithAnsi(text, width)
	chars := utf16Len(key)
	for _, l := range value {
		chars += utf16Len(l)
	}
	textCacheStore(key, textCacheEntry{lines: append([]string(nil), value...), chars: chars})
	return value
}

// visibleWidth is widthx.VisibleWidth under the TS name.
func visibleWidth(s string) int { return widthx.VisibleWidth(s) }

// jsPadEnd is String.prototype.padEnd(n) with spaces, measured in UTF-16 units.
func jsPadEnd(s string, n int) string {
	if l := utf16Len(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}
