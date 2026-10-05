package warden

import (
	"strings"
)

// Redact scrubs credentials from text that leaves the machine (src/redact.ts `redact`). It is
// best-effort and ordered: multi-token shapes before bare tokens.

const redacted = "[redacted]"

// credentialKeys are the credential-key names of assignments and config values (redact.ts CREDENTIAL_KEYS).
const credentialKeys = `(?:api[_-]?key|apikey|access[_-]?key|secret[_-]?key|client[_-]?secret|private[_-]?key|passw(?:or)?d|passphrase|token|secret|credentials?)`

var (
	rePrivateKey    = lazyRE(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	reAuthorization = lazyRE(`(?i)(authorization\s*[:=]\s*)(?:basic|bearer|token)?\s*\S+`)
	reBearer        = lazyRE(`(?i)\b(bearer\s+)\S+`)
	// Quoted values can hold spaces (passphrases), so a quoted value is redacted whole before the
	// unquoted rule. RE2 has no backreference: one alternative per quote character.
	reQuotedDouble = lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*)"[^"'\n]*"`)
	reQuotedSingle = lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*)'[^"'\n]*'`)
	// The head of an unquoted assignment; the value is scanned by hand (see redactUnquoted).
	reAssignHead = lazyRE(`(?i)((?:` + credentialKeys + `)[a-z0-9_-]*\s*[=:]\s*["']?)`)
	reURLPass    = lazyRE(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@:]+:[^\s/@]+@`)
	reTokenRules = []*lazyRegexp{
		lazyRE(`\bsk-[A-Za-z0-9_-]{8,}`),
		lazyRE(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
		lazyRE(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
		lazyRE(`\bAKIA[0-9A-Z]{16}\b`),
		lazyRE(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
		lazyRE(`\bAIza[0-9A-Za-z_-]{30,}`),
		lazyRE(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	}
	// `&` inside an unquoted value is a separator only before another `name=`, a second `&`, whitespace
	// or the end (redact.ts VALUE_AMPERSAND, a negative lookahead RE2 does not have).
	reAmpSeparator = lazyRE(`(?i)^[a-z_][\w.-]*=`)
)

// Redact returns text with credential-shaped values replaced by [redacted].
func Redact(text string) string {
	out := rePrivateKey.ReplaceAllString(text, redacted)
	out = reAuthorization.ReplaceAllString(out, "${1}"+redacted)
	out = reBearer.ReplaceAllString(out, "${1}"+redacted)
	out = reQuotedDouble.ReplaceAllString(out, `${1}"`+redacted+`"`)
	out = reQuotedSingle.ReplaceAllString(out, `${1}'`+redacted+`'`)
	out = redactUnquoted(out)
	out = reURLPass.ReplaceAllString(out, "${1}"+redacted+"@")
	for _, re := range reTokenRules {
		out = re.ReplaceAllString(out, redacted)
	}
	return out
}

// redactUnquoted replaces the unquoted value after a credential key: characters other than
// whitespace, quotes, `;` and `&`, plus a `&` that is part of the value.
func redactUnquoted(text string) string {
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		loc := reAssignHead.FindStringSubmatchIndex(text[pos:])
		if loc == nil {
			break
		}
		headStart, headEnd := pos+loc[0], pos+loc[3]
		end := valueEnd(text, headEnd)
		if end == headEnd {
			// No value characters: the pattern needs at least one, so this head does not match; the
			// engine retries one character after the head's start.
			out.WriteString(text[pos : headStart+1])
			pos = headStart + 1
			continue
		}
		out.WriteString(text[pos:headEnd])
		out.WriteString(redacted)
		pos = end
	}
	out.WriteString(text[pos:])
	return out.String()
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
