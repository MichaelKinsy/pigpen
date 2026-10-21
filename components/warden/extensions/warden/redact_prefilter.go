package warden

import "strings"

// Redact runs a dozen regular expressions over its whole input, and Go's engine tries each of them at every position:
// on a 200 KB build log that costs about 1 µs a character, where V8 skips to the next literal. Every rule needs a
// literal the text must contain, so a text without any of them cannot change and is returned as it is. The literals
// are those of redact.go's patterns (the credential key words, `authorization`, `bearer`, `://`, the token prefixes,
// `PRIVATE KEY-----`); a pattern added there needs its literal here (TestRedactPrefilterNeverSkipsARedaction).

// credentialLiterals are the lower-case literals of the case-insensitive rules: one of them is in every text those
// rules can change.
var credentialLiterals = []string{"authorization", "bearer", "key", "secret", "passw", "passphrase", "token", "credential"}

// credentialLiteralsExact are the literals of the case-sensitive rules.
var credentialLiteralsExact = []string{"PRIVATE KEY-----", "sk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "AKIA", "xox", "AIza", "eyJ"}

// mayHaveCredential reports whether Redact could change text. False means it cannot.
func mayHaveCredential(text string) bool {
	if isASCII(text) {
		return mayHaveCredentialASCII(text)
	}
	return mayHaveCredentialScan(text)
}

// mayHaveCredentialScan is mayHaveCredential for any text: substring searches, and a Unicode-lower-cased copy for the
// case-insensitive literals (which can come from a character that is not ASCII, such as the Kelvin sign).
func mayHaveCredentialScan(text string) bool {
	for _, lit := range credentialLiteralsExact {
		if strings.Contains(text, lit) {
			return true
		}
	}
	if strings.Contains(text, "://") && strings.Contains(text, "@") {
		return true
	}
	lower := strings.ToLower(text)
	for _, lit := range credentialLiterals {
		if strings.Contains(lower, lit) {
			return true
		}
	}
	return false
}
