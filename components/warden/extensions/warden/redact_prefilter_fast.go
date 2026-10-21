package warden

import "strings"

// credentialPair marks the first two bytes (either case for the case-insensitive literals) of every literal above, so
// one pass over an ASCII text finds the places where a literal can start. A text of 12 KB cost about 40 microseconds
// as twenty substring searches (each slowed by frequent first letters) and a lower-cased copy.
var credentialPair [65536]bool

func init() {
	mark := func(a, b byte) { credentialPair[uint(a)<<8|uint(b)] = true }
	for _, lit := range credentialLiteralsExact {
		mark(lit[0], lit[1])
	}
	for _, lit := range credentialLiterals {
		for _, a := range []byte{lit[0], lit[0] - 'a' + 'A'} {
			for _, b := range []byte{lit[1], lit[1] - 'a' + 'A'} {
				mark(a, b)
			}
		}
	}
}

// credentialAt reports whether a literal of either list starts at the beginning of s (an ASCII text).
func credentialAt(s string) bool {
	for _, lit := range credentialLiteralsExact {
		if strings.HasPrefix(s, lit) {
			return true
		}
	}
	for _, lit := range credentialLiterals {
		if foldPrefix(s, lit) {
			return true
		}
	}
	return false
}

// mayHaveCredentialASCII is mayHaveCredentialScan for an ASCII text: its lower-casing is the ASCII one, so the
// case-insensitive literals are found in place.
func mayHaveCredentialASCII(text string) bool {
	if strings.Contains(text, "://") && strings.Contains(text, "@") {
		return true
	}
	for i := 0; i+1 < len(text); i++ {
		if credentialPair[uint(text[i])<<8|uint(text[i+1])] && credentialAt(text[i:]) {
			return true
		}
	}
	return false
}
