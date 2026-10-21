package pi_background_tasks

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// The notify matcher: a substring or a `/regex/flags` gate for output wakeups, with the original's deadline semantics.
// upstream: extensions/format.ts:79-125.

// outputMatcherDeadline is the maximum time for one notify regex execution.
const outputMatcherDeadline = 25 * time.Millisecond

// errMatcherBudget reports that a notify regex exceeded its deadline and is disabled for this task.
var errMatcherBudget = fmt.Errorf("Notify pattern exceeded %d ms; output matching is disabled for this task.", outputMatcherDeadline/time.Millisecond)

// matcherFunc reports whether a text matches. The error is errMatcherBudget: raised once, when the deadline passes, after
// which the matcher answers false.
type matcherFunc func(text string) (bool, error)

// jsToLower is String.prototype.toLowerCase: Go's simple lowering, plus the two cases ECMAScript's full lowercase mapping
// adds (U+0130 becomes i and a combining dot, and a capital sigma at the end of a word becomes the final sigma).
func jsToLower(s string) string {
	if !strings.ContainsAny(s, "\u0130\u03a3") {
		return strings.ToLower(s)
	}
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case 0x130:
			b.WriteString("i\u0307")
		case 0x3a3:
			// Final_Sigma: preceded by a cased letter (skipping case-ignorable ones), not followed by one.
			if casedBefore(runes, i) && !casedAfter(runes, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isCased(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }

func isCaseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || strings.ContainsRune("'.:^`\u00a8\u00ad\u00af\u00b4\u00b7\u00b8\u2018\u2019\u2024\u2027", r)
}

func casedBefore(runes []rune, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		return isCased(runes[j])
	}
	return false
}

func casedAfter(runes []rune, i int) bool {
	for j := i + 1; j < len(runes); j++ {
		if isCaseIgnorable(runes[j]) {
			continue
		}
		return isCased(runes[j])
	}
	return false
}

// splitRegexLiteral is needle.match(/^\/(.*)\/([gimsuy]*)$/): the source and flags of a `/source/flags` needle.
func splitRegexLiteral(needle string) (source, flags string, ok bool) {
	if len(needle) < 2 || needle[0] != '/' {
		return "", "", false
	}
	last := strings.LastIndexByte(needle, '/')
	if last < 1 {
		return "", "", false
	}
	flags = needle[last+1:]
	for _, f := range flags {
		if !strings.ContainsRune("gimsuy", f) {
			return "", "", false
		}
	}
	source = needle[1:last]
	// `.` does not match a line terminator.
	if strings.ContainsAny(source, "\n\r\u2028\u2029") {
		return "", "", false
	}
	return source, flags, true
}

// parseOutputMatcherChecked compiles a substring or regex matcher. A `/regex/flags` the grammar rejects falls back to a
// substring match, as the original does; one the port cannot translate with certainty returns refused, the feature text
// for the refusal. A nil matcher means no pattern.
func parseOutputMatcherChecked(pattern string) (m matcherFunc, refused string) {
	needle := jsTrim(pattern)
	if needle == "" {
		return nil, ""
	}
	if source, flags, ok := splitRegexLiteral(needle); ok {
		re, err := compileJSRegex(source, flags)
		var unsupported *jsUnsupportedError
		switch {
		case err == nil:
			disabled := false
			return func(text string) (bool, error) {
				if disabled {
					return false, nil
				}
				matched, timedOut := re.test(text, outputMatcherDeadline)
				if timedOut {
					disabled = true
					return false, errMatcherBudget
				}
				return matched, nil
			}, ""
		case errors.As(err, &unsupported):
			return nil, "the notifyPattern regular expression (" + unsupported.msg + ")"
		}
		// A SyntaxError falls through to substring matching.
	}
	lower := jsToLower(needle)
	return func(text string) (bool, error) { return strings.Contains(jsToLower(text), lower), nil }, ""
}

// parseOutputMatcher is parseOutputMatcherChecked for a pattern that was accepted before (a restored task): one the port
// cannot translate matches as a substring and is logged.
func parseOutputMatcher(pattern string) matcherFunc {
	m, refused := parseOutputMatcherChecked(pattern)
	if refused != "" {
		logBackgroundDiagnostic("notify pattern not translatable", map[string]any{"pattern": pattern, "refusal": refused})
		lower := jsToLower(jsTrim(pattern))
		return func(text string) (bool, error) { return strings.Contains(jsToLower(text), lower), nil }
	}
	return m
}
