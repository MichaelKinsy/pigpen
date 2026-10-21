package pi_permission_system

import "strings"

// Port of src/logging/log-redaction.ts: name-based redaction for the permission logs. A value is masked because of the name it is
// bound to, never because of what it looks like (docs/decisions/0010-permission-log-secret-exposure.md in the original).

// RedactedPlaceholder replaces a masked value (REDACTED_PLACEHOLDER).
const RedactedPlaceholder = "[redacted]"

// The original's /i flag is JavaScript's non-Unicode case folding, under which no non-ASCII character matches an ASCII letter
// (U+212A KELVIN SIGN does not read as k, U+017F as s). Go's (?i) folds those, so the match runs on an ASCII-lowered copy instead.
//
// The original's two patterns are matched by hand: Go's backtracking regexp took 80% of the time of a redacted stringify (about 5x the
// original's V8 irregexp). Their text, kept in logredaction_test.go as the reference the matcher is fuzzed against:
//   sensitive (on the lowered name): authorization|api[-_]?keys?|private[-_]?keys?|secret|token|password|passwd|credential|cookie|(?:^|[-_])keys?(?:$|[-_])
//   camel (case-sensitive): [a-z0-9](?:Key|Keys)(?:$|[A-Z_-])  -- a `key` bound as the tail of a camel-cased name (`apiKey`, `sortKeys`)

var sensitiveWords = [...]string{"authorization", "secret", "token", "password", "passwd", "credential", "cookie"}

func isSepByte(c byte) bool { return c == '-' || c == '_' }

// keyAfter reports whether s[i:] reads `key` after an optional separator: api[-_]?key / private[-_]?key (the trailing `s?` cannot change
// whether the pattern matches).
func keyAfter(s string, i int) bool {
	if i < len(s) && isSepByte(s[i]) {
		i++
	}
	return strings.HasPrefix(s[i:], "key")
}

// matchesSensitive is the first pattern on an ASCII-lowered name.
func matchesSensitive(s string) bool {
	for _, w := range sensitiveWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	for off := 0; ; {
		i := strings.Index(s[off:], "api")
		if i < 0 {
			break
		}
		if keyAfter(s, off+i+3) {
			return true
		}
		off += i + 1
	}
	for off := 0; ; {
		i := strings.Index(s[off:], "private")
		if i < 0 {
			break
		}
		if keyAfter(s, off+i+7) {
			return true
		}
		off += i + 1
	}
	// (?:^|[-_])keys?(?:$|[-_])
	for off := 0; ; {
		i := strings.Index(s[off:], "key")
		if i < 0 {
			return false
		}
		i += off
		if i == 0 || isSepByte(s[i-1]) {
			j := i + 3
			if j == len(s) || isSepByte(s[j]) || (s[j] == 's' && (j+1 == len(s) || isSepByte(s[j+1]))) {
				return true
			}
		}
		off = i + 1
	}
}

// matchesCamelKey is the case-sensitive second pattern.
func matchesCamelKey(s string) bool {
	for off := 0; ; {
		i := strings.Index(s[off:], "Key")
		if i < 0 {
			return false
		}
		i += off
		if i > 0 && (s[i-1] >= 'a' && s[i-1] <= 'z' || s[i-1] >= '0' && s[i-1] <= '9') {
			j := i + 3
			if j == len(s) || camelEnd(s[j]) || (s[j] == 's' && (j+1 == len(s) || camelEnd(s[j+1]))) {
				return true
			}
		}
		off = i + 1
	}
}

func camelEnd(c byte) bool { return c >= 'A' && c <= 'Z' || c == '_' || c == '-' }

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// IsSensitiveName reports a name that binds a credential-bearing value (isSensitiveName).
func IsSensitiveName(name string) bool {
	return matchesSensitive(asciiLower(name)) || matchesCamelKey(name)
}

// RedactedJSONStringify is SafeJSONStringify with sensitive-keyed values masked. Masking runs inside the replacer, so the
// structure beneath a sensitive key is never visited; a null or undefined value is left alone so an absent field does not read as
// a suppressed one.
func RedactedJSONStringify(value any) (string, bool) {
	return jsonStringifyWith(value, CreateJSONSafeReplacer(func(key string, current any) any {
		if !isNullish(current) && IsSensitiveName(key) {
			return RedactedPlaceholder
		}
		return current
	}))
}
