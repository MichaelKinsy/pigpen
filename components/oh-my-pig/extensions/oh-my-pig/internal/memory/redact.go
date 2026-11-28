// Package memory ports omp's long-term memory backends (local, mnemopi, hindsight),
// memory tools (retain, recall, reflect, memory_edit, learn), and the memory://
// internal URL handler.
package memory

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// upstream: packages/coding-agent/src/memory-backend/redact.ts:1-149
// upstream: packages/coding-agent/src/memories/index.ts:1469-1492

var (
	// Fixed-prefix provider tokens (AWS, GitHub, npm, Slack, Google).
	// upstream: packages/coding-agent/src/memory-backend/redact.ts:11-19
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
		regexp.MustCompile(`(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`npm_[A-Za-z0-9]{30,}`),
		regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
		regexp.MustCompile(`AIza[A-Za-z0-9_-]{30,}`),
	}

	// Longest first, so `token_` wins over `tok`.
	// upstream: packages/coding-agent/src/memory-backend/redact.ts:22
	secretKeywords = []string{"password", "secret", "token", "key", "tok", "sk", "pk", "rk"}

	multiTildeRE = regexp.MustCompile(`~{2,}`)
)

const (
	minMixedSegment   = 12
	minLettersSegment = 16
	minJWTSegment     = 16
)

func isSecretDelimiter(b byte) bool {
	return b == '-' || b == '_'
}

func isTokenByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || isSecretDelimiter(b)
}

func isCredentialSegment(length int, letter, digit bool) bool {
	if !letter && !digit {
		return false
	}
	if letter && digit {
		return length >= minMixedSegment
	}
	return length >= minLettersSegment
}

func keywordStart(input string, delimiter int) int {
	for _, kw := range secretKeywords {
		start := delimiter - len(kw)
		if start >= 0 && strings.HasPrefix(input[start:], kw) {
			return start
		}
	}
	return -1
}

// redactKeywordSecrets redacts `<keyword>[-_]<credential-run>` in linear time.
// upstream: packages/coding-agent/src/memory-backend/redact.ts:58-106
func redactKeywordSecrets(input string) string {
	var out strings.Builder
	copied := 0
	index := 0
	n := len(input)
	for index < n {
		if !isTokenByte(input[index]) {
			index++
			continue
		}
		runStart := index
		starts := []int{runStart}
		var credential []bool
		length := 0
		letter := false
		digit := false
		for index < n && isTokenByte(input[index]) {
			ch := input[index]
			if isSecretDelimiter(ch) {
				credential = append(credential, isCredentialSegment(length, letter, digit))
				starts = append(starts, index+1)
				length = 0
				letter = false
				digit = false
			} else {
				length++
				if ch >= '0' && ch <= '9' {
					digit = true
				} else {
					letter = true
				}
			}
			index++
		}
		credential = append(credential, isCredentialSegment(length, letter, digit))
		runEnd := index

		suffix := make([]bool, len(credential)+1)
		for k := len(credential) - 1; k >= 0; k-- {
			suffix[k] = suffix[k+1] || credential[k]
		}
		for k := 1; k < len(starts); k++ {
			if !suffix[k] {
				continue
			}
			start := keywordStart(input, starts[k]-1)
			if start < 0 || start < copied || start < runStart {
				continue
			}
			out.WriteString(input[copied:start])
			out.WriteString("[REDACTED]")
			copied = runEnd
			break
		}
	}
	if copied == 0 {
		return input
	}
	out.WriteString(input[copied:])
	return out.String()
}

// redactJWTs redacts `header.payload.signature` tokens in linear time.
// upstream: packages/coding-agent/src/memory-backend/redact.ts:118-143
func redactJWTs(input string) string {
	var out strings.Builder
	copied := 0
	dot := strings.IndexByte(input, '.')
	for dot > 0 {
		start := dot
		for start > copied && isTokenByte(input[start-1]) {
			start--
		}
		middle := dot + 1
		for middle < len(input) && isTokenByte(input[middle]) {
			middle++
		}
		end := middle + 1
		for end < len(input) && isTokenByte(input[end]) {
			end++
		}
		looksLikeJWT := dot-start >= minJWTSegment &&
			middle < len(input) &&
			input[middle] == '.' &&
			middle-dot-1 >= minJWTSegment &&
			end-middle-1 >= minJWTSegment
		if looksLikeJWT {
			out.WriteString(input[copied:start])
			out.WriteString("[REDACTED]")
			copied = end
			if end >= len(input) {
				break
			}
			nextDot := strings.IndexByte(input[end:], '.')
			if nextDot < 0 {
				break
			}
			dot = end + nextDot
			continue
		}
		if dot+1 >= len(input) {
			break
		}
		nextDot := strings.IndexByte(input[dot+1:], '.')
		if nextDot < 0 {
			break
		}
		dot = dot + 1 + nextDot
	}
	if copied == 0 {
		return input
	}
	out.WriteString(input[copied:])
	return out.String()
}

// RedactMemorySecrets scrubs credential-like tokens, JWTs, and provider prefixes.
// upstream: packages/coding-agent/src/memory-backend/redact.ts:145-149
func RedactMemorySecrets(input string) string {
	out := redactJWTs(redactKeywordSecrets(input))
	for _, pat := range secretPatterns {
		out = pat.ReplaceAllString(out, "[REDACTED]")
	}
	return out
}

func isECMASpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		'\u00A0', '\u1680',
		'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A',
		'\u2028', '\u2029', '\u202F', '\u205F', '\u3000', '\uFEFF':
		return true
	default:
		return false
	}
}

// NeutralizeInjection strips prompt-injection delimiters (Unicode Cc/Cf, `<`, `>`,
// backticks, `~~+`) and collapses whitespace into single spaces.
// upstream: packages/coding-agent/src/memories/index.ts:1469-1476
func NeutralizeInjection(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			b.WriteByte(' ')
			continue
		}
		if r == '<' || r == '>' || r == '`' {
			continue
		}
		b.WriteRune(r)
	}
	s := multiTildeRE.ReplaceAllString(b.String(), "~")
	var out strings.Builder
	out.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if isECMASpace(r) {
			if out.Len() > 0 {
				inSpace = true
			}
			continue
		}
		if inSpace {
			out.WriteByte(' ')
			inSpace = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

// boundRunes caps s at maxRunes UTF-8 runes cleanly without splitting multi-byte sequences.
// upstream: packages/coding-agent/src/memories/index.ts:1479-1483
func boundRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// NormalizeLearnedText neutralizes injection delimiters FIRST, then redacts secrets,
// then bounds length.
// upstream: packages/coding-agent/src/memories/index.ts:1490-1492
func NormalizeLearnedText(text string, maxChars int) string {
	return boundRunes(strings.TrimSpace(RedactMemorySecrets(NeutralizeInjection(text))), maxChars)
}

// sanitizeControlText removes ASCII/Unicode control characters (preserving tabs and newlines)
// for persisted Hindsight outbox payloads.
func sanitizeControlText(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
