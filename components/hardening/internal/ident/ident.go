// Package ident holds the identifier rules shared by the hardening packages. An identifier is the only kind of
// free text that may appear in an error, a status report or an audit line.
package ident

// Package reports whether s is a Package name: a lower-case letter, then up to 31 lower-case letters, digits or hyphens.
func Package(s string) bool {
	if len(s) == 0 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// Tool reports whether s matches ^[A-Za-z0-9._-]{1,64}$, the identifier rule of the a2a Package.
func Tool(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
