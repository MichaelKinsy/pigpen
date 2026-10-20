package warden

import (
	"strings"
)

// Redact scrubs credentials from text that leaves the machine (src/redact.ts `redact`). It is
// best-effort and ordered: multi-token shapes before bare tokens.

const redacted = "[redacted]"

// credentialKeys are the credential-key names of assignments and config values (redact.ts CREDENTIAL_KEYS).
const credentialKeys = `(?:api[_-]?key|apikey|access[_-]?key|secret[_-]?key|client[_-]?secret|private[_-]?key|passw(?:or)?d|passphrase|token|secret|credentials?)`

// tokenShape is a token regexp that opens with a literal (so Go can skip to it) and whose first character is a word
// character: the `\b` the original writes before it is checked by hand (shapeSpans).
type tokenShape struct{ re *lazyRegexp }

var (
	rePrivateKey = lazyRE(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	// The patterns below are anchored: they are tried at the candidate offsets of redact_fast.go, not scanned.
	reAuthorizationAt = lazyRE(`(?i)^(authorization\s*[:=]\s*)(?:basic|bearer|token)?\s*\S+`)
	reBearerAt        = lazyRE(`(?i)^(bearer\s+)\S+`)
	// A quoted value can hold spaces (passphrases), so a quoted value is redacted whole before the unquoted rule;
	// the credential-key head and the quoted and unquoted values are matched by hand (assignHeadAt, quotedValueAt,
	// redactUnquoted): `credentialKeys` is the pattern they implement.
	reURLPassAt  = lazyRE(`(?i)^([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	reTokenRules = []tokenShape{
		{lazyRE(`sk-[A-Za-z0-9_-]{8,}`)},
		{lazyRE(`(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`)},
		{lazyRE(`github_pat_[A-Za-z0-9_]{20,}`)},
		{lazyRE(`AKIA[0-9A-Z]{16}\b`)},
		{lazyRE(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
		{lazyRE(`AIza[0-9A-Za-z_-]{30,}`)},
		{lazyRE(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	}
	// `&` inside an unquoted value is a separator only before another `name=`, a second `&`, whitespace
	// or the end (redact.ts VALUE_AMPERSAND, a negative lookahead RE2 does not have).
	reAmpSeparator = lazyRE(`(?i)^[a-z_][\w.-]*=`)
)

// Redact returns text with credential-shaped values replaced by [redacted]. The rules run in the original's order;
// each is tried only where its pattern can start (redact_fast.go), which keeps a tool output of tens of kilobytes at
// tens of microseconds instead of milliseconds.
func Redact(text string) string {
	out := rePrivateKey.ReplaceAllString(text, redacted)
	lower := asciiLower(out)
	step := func(next string, changed bool) {
		if changed {
			out = next
			lower = asciiLower(out)
		}
	}
	if strings.Contains(lower, "authorization") {
		step(rewriteAt(out, indexAll(lower, "authorization", nil), func(p int) (int, string, bool) {
			loc := reAuthorizationAt.FindStringSubmatchIndex(out[p:])
			if loc == nil {
				return 0, "", false
			}
			return p + loc[1], out[p:p+loc[3]] + redacted, true
		}))
	}
	if strings.Contains(lower, "bearer") {
		step(rewriteAt(out, boundaryCandidates(out, indexAll(lower, "bearer", nil)), func(p int) (int, string, bool) {
			loc := reBearerAt.FindStringSubmatchIndex(out[p:])
			if loc == nil {
				return 0, "", false
			}
			return p + loc[1], out[p:p+loc[3]] + redacted, true
		}))
	}
	if cands := keyCandidates(lower); len(cands) > 0 {
		for _, quote := range []byte{'"', '\''} {
			cur, curLower := out, lower
			next, changed := rewriteAt(cur, cands, func(p int) (int, string, bool) {
				headEnd, end, ok := quotedValueAt(cur, curLower, p, quote)
				if !ok {
					return 0, "", false
				}
				return end, cur[p:headEnd] + string(quote) + redacted + string(quote), true
			})
			if changed {
				step(next, true)
				cands = keyCandidates(lower)
			}
		}
		step(redactUnquoted(out, lower, cands))
	}
	if strings.Contains(out, "://") {
		cur := out
		step(rewriteAt(cur, urlStarts(cur), func(p int) (int, string, bool) {
			loc := reURLPassAt.FindStringSubmatchIndex(cur[p:])
			if loc == nil {
				return 0, "", false
			}
			return p + loc[1], cur[p:p+loc[3]] + redacted + "@", true
		}))
	}
	for _, shape := range reTokenRules {
		out = replaceSpans(out, shapeSpans(shape.re, out, true), redacted)
	}
	return out
}

// redactUnquoted replaces the unquoted value after a credential key: characters other than whitespace, quotes, `;`
// and `&`, plus a `&` that is part of the value. lower is text lower-cased (ASCII) and cands are its key offsets.
func redactUnquoted(text, lower string, cands []int) (string, bool) {
	return rewriteAt(text, cands, func(p int) (int, string, bool) {
		valueStart, ok := assignValueStart(text, lower, p)
		if !ok {
			return 0, "", false
		}
		end := valueEnd(text, valueStart)
		if end == valueStart {
			// No value characters: the pattern needs at least one, so this head does not match; the scan goes on
			// at the next candidate, one character after the head's start at the earliest.
			return 0, "", false
		}
		return end, text[p:valueStart] + redacted, true
	})
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// valueEnd returns the end of the value that starts at from.
func valueEnd(text string, from int) int {
	end := from
	for end < len(text) {
		c := text[end]
		if isSpaceByte(c) || c == '"' || c == '\'' || c == ';' {
			break
		}
		if c == '&' {
			rest := text[end+1:]
			if rest == "" || rest[0] == '&' || isSpaceByte(rest[0]) || reAmpSeparator.MatchString(rest) {
				break
			}
		}
		end++
	}
	return end
}
