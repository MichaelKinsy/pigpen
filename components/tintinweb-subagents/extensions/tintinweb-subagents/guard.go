package tintinweb_subagents

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"unicode"
)

// guard runs fn and recovers from a panic in it, so that nothing a handler, a timer or a goroutine of this extension
// does can take the extension process down. It reports whether fn panicked. The panic goes to stderr with its stack.
func guard(what string, fn func()) (panicked bool) {
	defer func() {
		if v := recover(); v != nil {
			panicked = true
			fmt.Fprintf(os.Stderr, "[pi-subagents] internal error in %s: %v\n%s", what, v, debug.Stack())
		}
	}()
	fn()
	return false
}

// goGuarded starts fn in a goroutine that recovers from a panic.
func goGuarded(what string, fn func()) { go guard(what, fn) }

// isJSSpace is what ECMAScript's String.prototype.trim strips: white space and line terminators. Go's unicode.IsSpace
// differs in two places: it strips U+0085, which JS keeps, and it keeps U+FEFF, which JS strips.
func isJSSpace(r rune) bool {
	switch r {
	case 0x85:
		return false
	case 0xFEFF, 0x2028, 0x2029:
		return true
	}
	return unicode.IsSpace(r)
}

// jsTrim is JS `s.trim()`.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }
