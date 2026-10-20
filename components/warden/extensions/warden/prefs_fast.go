package warden

import "strings"

// Cheap prefilters for the memory tools' hot paths. Go's regexp is a backtracking or NFA engine with no literal
// scan, and a profile of extractPreferences, recordLesson and recallText put about two thirds of the time in
// Redact and most of the rest in patterns that cannot match a text with none of their trigger words. Every
// prefilter below is a necessary condition of the pattern it guards (a superset test): it can send a text on to the
// regexp for nothing, never skip a text the regexp would match. prefs_differential_test.go and
// TestRedactFastEqualsRedact hold them to that.

// hasAny reports whether s contains one of the (lowercase) words.
func hasAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// credentialTriggers are substrings, one for every rule of Redact (redact.go), that a text must contain, in
// lowercase, for that rule to change it: the private key header, `authorization`, `bearer`, the credential key words
// (api_key, apikey, access_key, secret_key, client_secret, private_key, password, passphrase, token, secret,
// credential), a URL with a password, and the token shapes (sk-, gh?_, github_pat_, AKIA, xox, AIza, eyJ). A rule
// added to Redact must add its trigger here.
var credentialTriggers = []string{
	"-----", "authorization", "bearer", "key", "secret", "passw", "passphrase", "token", "credential", "://",
	"sk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "akia", "xox", "aiza", "eyj",
}

// redactFast is Redact for text that usually holds nothing to redact: it skips the fifteen passes when no trigger of
// any rule is present.
func redactFast(s string) string {
	if s == "" || !hasAny(strings.ToLower(s), credentialTriggers...) {
		return s
	}
	return Redact(s)
}

// endsWithQuestion is `/\?\s*$/`.
func endsWithQuestion(s string) bool {
	s = strings.TrimRightFunc(s, isJSSpace)
	return strings.HasSuffix(s, "?")
}

// questionStart is a necessary condition of the question-word pattern: why, how, what, when, where, who, which, can,
// could, would, should, will, is, are, did, does, do.
func questionStart(s string) bool {
	if s == "" {
		return false
	}
	switch s[0] | 0x20 {
	case 'w', 'h', 'c', 's', 'i', 'a', 'd':
		return true
	}
	return false
}

// startsNegation is `/^(?:don['’]?t|do\s+not|never|stop|no|not)\b/i` on s as it is (not trimmed).
func startsNegation(s string) bool {
	n := len(s)
	if n > 24 {
		n = 24
	}
	l := asciiLower(s[:n])
	boundary := func(end int) bool { return end <= len(s) && !wordAt(s, end) }
	if strings.HasPrefix(l, "don") {
		end := 3
		if strings.HasPrefix(s[end:], "'") {
			end++
		} else if strings.HasPrefix(s[end:], "’") {
			end += len("’")
		}
		if end < len(s) && (s[end] == 't' || s[end] == 'T') && boundary(end+1) {
			return true
		}
	}
	if strings.HasPrefix(l, "do") {
		if run := spaceRun(s[2:]); run > 0 && len(s) >= 2+run+3 && asciiLower(s[2+run:2+run+3]) == "not" && boundary(2+run+3) {
			return true
		}
	}
	for _, w := range [...]string{"never", "stop", "not", "no"} {
		if strings.HasPrefix(l, w) && boundary(len(w)) {
			return true
		}
	}
	return false
}

// endsPastForm is a necessary condition of NOT_A_PREFERENCE's second alternative `[a-z]+[^s\W]s\b|[a-z]+[^e\W]ed\b`
// at the start of s: the first word (a run of word characters) ends in s or ed.
func endsPastForm(s string) bool {
	end := 0
	for end < len(s) && isWordByte(s[end]) {
		end++
	}
	if end < 3 {
		return false
	}
	last := s[end-1] | 0x20
	return last == 's' || (end >= 4 && last == 'd' && s[end-2]|0x20 == 'e')
}

// hasDigitOrSlash is a necessary condition of every alternative of SPECIFIC_REFERENCE but the commit hash: a ticket
// (`ABC-123`), `#12`, `PR 12`, `branch a/b` and `feat/x` all hold a digit or a slash.
func hasDigitOrSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= '0' && c <= '9' || c == '/' {
			return true
		}
	}
	return false
}

var apostropheReplacer = strings.NewReplacer("'", "", "’", "")
