package pi_permission_system

import "regexp"

// Port of src/logging/log-redaction.ts: name-based redaction for the permission logs. A value is masked because of the name it is
// bound to, never because of what it looks like (docs/decisions/0010-permission-log-secret-exposure.md in the original).

// RedactedPlaceholder replaces a masked value (REDACTED_PLACEHOLDER).
const RedactedPlaceholder = "[redacted]"

// The original's /i flag is JavaScript's non-Unicode case folding, under which no non-ASCII character matches an ASCII letter
// (U+212A KELVIN SIGN does not read as k, U+017F as s). Go's (?i) folds those, so the pattern runs on an ASCII-lowered copy instead.
var sensitiveNamePattern = regexp.MustCompile(`authorization|api[-_]?keys?|private[-_]?keys?|secret|token|password|passwd|credential|cookie|(?:^|[-_])keys?(?:$|[-_])`)

// camelKeyPattern: a `key` bound as the tail of a camel-cased name (`apiKey`, `sortKeys`); deliberately case-sensitive.
var camelKeyPattern = regexp.MustCompile(`[a-z0-9](?:Key|Keys)(?:$|[A-Z_-])`)

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
	return sensitiveNamePattern.MatchString(asciiLower(name)) || camelKeyPattern.MatchString(name)
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
