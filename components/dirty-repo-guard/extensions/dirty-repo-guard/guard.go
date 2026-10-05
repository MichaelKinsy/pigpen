package dirty_repo_guard

import "strings"

// verdict is what the guard decides after `git status --porcelain`.
type verdict int

const (
	allow  verdict = iota // let the action proceed without asking
	cancel                // block the action without asking (no UI)
	ask                   // ask the user
)

// isJSSpace reports whether r is trimmed by JavaScript's String.prototype.trim:
// WhiteSpace and LineTerminator of ECMAScript. Go's unicode.IsSpace differs (it
// includes U+0085 and lacks U+FEFF), so it cannot be used.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is JavaScript's String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// countChangedFiles is `stdout.trim().split("\n").filter(Boolean).length`.
func countChangedFiles(stdout string) int {
	n := 0
	for _, line := range strings.Split(jsTrim(stdout), "\n") {
		if line != "" {
			n++
		}
	}
	return n
}

// decide is the guard's decision for a finished `git status --porcelain`:
// a non-zero exit (not a repository, or git failed) allows the action, a clean
// tree allows it, a dirty tree cancels without a UI and asks with one.
func decide(code int, stdout string, hasUI bool) (v verdict, changed int) {
	if code != 0 || len(jsTrim(stdout)) == 0 {
		return allow, 0
	}
	if !hasUI {
		return cancel, 0
	}
	return ask, countChangedFiles(stdout)
}
