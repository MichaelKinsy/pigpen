// Package archive implements omp's read-only local history eval prelude (`archive`),
// exposing recent projects, past sessions with recaps, full-text session search,
// and prompt history over PiG and omp session JSONL files without cgo or SQLite.
//
// upstream: packages/coding-agent/src/archive/{archive.ts,prelude-definition.ts,prelude.js,prelude.py},
//
//	packages/coding-agent/src/session/{search-query.ts,session-listing.ts,transcript-index.ts},
//	packages/coding-agent/src/prompts/tools/archive.md
package archive

import (
	"strings"
	"unicode"
)

// WordQuery is a parsed word-search query matching upstream's search-query.ts semantics:
// every whitespace-separated term is split into letter/digit runs (tokens) and matched
// as consecutive words with the last token as a word prefix, case- and Latin-accent-insensitive.
//
// upstream: packages/coding-agent/src/session/search-query.ts:13-100
type WordQuery struct {
	phrases [][]string
	longest int
}

// TextRange is a byte offset range [Start, End) in the original string.
type TextRange struct {
	Start int
	End   int
}

// ParseWordQuery parses query into a WordQuery. Returns nil when no term contains
// a letter or digit (which matches nothing, matching upstream wordQuery returning undefined).
//
// upstream: packages/coding-agent/src/session/search-query.ts:65-100
func ParseWordQuery(query string) *WordQuery {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return nil
	}
	var phrases [][]string
	longest := 0
	for _, term := range fields {
		toks := ftsTokens(ftsFold(term))
		if len(toks) == 0 {
			continue
		}
		phrases = append(phrases, toks)
		if len(toks) > longest {
			longest = len(toks)
		}
	}
	if len(phrases) == 0 {
		return nil
	}
	return &WordQuery{phrases: phrases, longest: longest}
}

// Test reports whether text holds every term of q as consecutive words with the last
// token matching as a word prefix, case- and Latin-accent-insensitive.
//
// upstream: packages/coding-agent/src/session/search-query.ts:73-76
func (q *WordQuery) Test(text string) bool {
	if q == nil || len(q.phrases) == 0 {
		return false
	}
	tokens := ftsTokens(ftsFold(text))
	if len(tokens) == 0 {
		return false
	}
	for _, phrase := range q.phrases {
		if !hasPhrasePrefix(tokens, phrase) {
			return false
		}
	}
	return true
}

type placedToken struct {
	token string
	start int
	end   int
}

// Locate returns the byte range [Start, End) in text of the first place any term of q
// matches, or (TextRange{}, false) when none does.
//
// upstream: packages/coding-agent/src/session/search-query.ts:77-98
func (q *WordQuery) Locate(text string) (TextRange, bool) {
	if q == nil || len(q.phrases) == 0 || text == "" {
		return TextRange{}, false
	}
	recent := make([]placedToken, 0, q.longest+1)

	i := 0
	for i < len(text) {
		// Advance to next letter or digit run.
		for i < len(text) {
			r, size := decodeRuneAt(text, i)
			if isWordRune(r) {
				break
			}
			i += size
		}
		if i >= len(text) {
			break
		}
		start := i
		for i < len(text) {
			r, size := decodeRuneAt(text, i)
			if !isWordRune(r) {
				break
			}
			i += size
		}
		end := i
		tok := foldToken(text[start:end])
		if tok == "" {
			continue
		}
		recent = append(recent, placedToken{token: tok, start: start, end: end})
		if len(recent) > q.longest {
			copy(recent, recent[1:])
			recent = recent[:q.longest]
		}
		for _, phrase := range q.phrases {
			first := len(recent) - len(phrase)
			if first < 0 {
				continue
			}
			if placedHasPhrasePrefix(recent[first:], phrase) {
				return TextRange{Start: recent[first].start, End: recent[len(recent)-1].end}, true
			}
		}
	}
	return TextRange{}, false
}

func placedHasPhrasePrefix(window []placedToken, phrase []string) bool {
	last := len(phrase) - 1
	if last < 0 || len(window) != len(phrase) {
		return false
	}
	if !strings.HasPrefix(window[last].token, phrase[last]) {
		return false
	}
	for i := range last {
		if window[i].token != phrase[i] {
			return false
		}
	}
	return true
}

func hasPhrasePrefix(tokens, phrase []string) bool {
	last := len(phrase) - 1
	if last < 0 || len(tokens) < len(phrase) {
		return false
	}
	for start := range len(tokens) - last {
		if !strings.HasPrefix(tokens[start+last], phrase[last]) {
			continue
		}
		matched := true
		for idx := range last {
			if tokens[start+idx] != phrase[idx] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func decodeRuneAt(s string, i int) (rune, int) {
	if s[i] < 0x80 {
		return rune(s[i]), 1
	}
	for _, r := range s[i:] {
		if r == unicode.ReplacementChar && i < len(s) && s[i] != 0xef {
			return r, 1
		}
		return r, len(string(r))
	}
	return unicode.ReplacementChar, 1
}

// ftsTokens splits s on runs of non-(Letter|Number) characters.
// upstream: packages/coding-agent/src/session/search-query.ts:33-35
func ftsTokens(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if isWordRune(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			out = append(out, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// foldToken folds one token; ASCII tokens only need lowercasing.
// upstream: packages/coding-agent/src/session/search-query.ts:60-62
func foldToken(token string) string {
	for i := range len(token) {
		if token[i] >= 0x80 {
			return ftsFold(token)
		}
	}
	return strings.ToLower(token)
}

// ftsFold lowercases text and strips combining marks following Latin letters
// (and decomposes precomposed Latin-1 / Latin Extended-A/B accented letters)
// matching SQLite FTS5 unicode61 remove_diacritics=1 / search-query.ts ftsFold.
//
// upstream: packages/coding-agent/src/session/search-query.ts:38-44
func ftsFold(text string) string {
	// Fast path for pure ASCII.
	ascii := true
	for i := range len(text) {
		if text[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return strings.ToLower(text)
	}

	var b strings.Builder
	b.Grow(len(text))
	prevLatin := false
	for _, r := range text {
		lr := unicode.ToLower(r)
		if unicode.Is(unicode.Mn, lr) || unicode.Is(unicode.Mc, lr) || unicode.Is(unicode.Me, lr) {
			if prevLatin {
				// Strip diacritic mark attached to a preceding Latin letter.
				continue
			}
			b.WriteRune(lr)
			continue
		}
		if base, ok := foldLatinBase(lr); ok {
			b.WriteRune(base)
			prevLatin = true
			continue
		}
		b.WriteRune(lr)
		prevLatin = unicode.Is(unicode.Latin, lr)
	}
	return b.String()
}

// foldLatinBase maps precomposed lowercase Latin letters with diacritics to their
// ASCII base letter (matching Unicode NFD + strip combining marks for Latin script).
func foldLatinBase(r rune) (rune, bool) {
	switch r {
	// Latin-1 Supplement (U+00E0..U+00FF)
	case 'à', 'á', 'â', 'ã', 'ä', 'å':
		return 'a', true
	case 'ç':
		return 'c', true
	case 'è', 'é', 'ê', 'ë':
		return 'e', true
	case 'ì', 'í', 'î', 'ï':
		return 'i', true
	case 'ñ':
		return 'n', true
	case 'ò', 'ó', 'ô', 'õ', 'ö':
		return 'o', true
	case 'ù', 'ú', 'û', 'ü':
		return 'u', true
	case 'ý', 'ÿ':
		return 'y', true
	// Latin Extended-A (U+0100..U+017F)
	case 'ā', 'ă', 'ą':
		return 'a', true
	case 'ć', 'ĉ', 'ċ', 'č':
		return 'c', true
	case 'ď':
		return 'd', true
	case 'ē', 'ĕ', 'ė', 'ę', 'ě':
		return 'e', true
	case 'ĝ', 'ğ', 'ġ', 'ģ':
		return 'g', true
	case 'ĥ':
		return 'h', true
	case 'ĩ', 'ī', 'ĭ', 'į':
		return 'i', true
	case 'ĵ':
		return 'j', true
	case 'ķ':
		return 'k', true
	case 'ĺ', 'ļ', 'ľ':
		return 'l', true
	case 'ń', 'ņ', 'ň':
		return 'n', true
	case 'ō', 'ŏ', 'ő':
		return 'o', true
	case 'ŕ', 'ŗ', 'ř':
		return 'r', true
	case 'ś', 'ŝ', 'ş', 'š':
		return 's', true
	case 'ţ', 'ť':
		return 't', true
	case 'ũ', 'ū', 'ŭ', 'ů', 'ű', 'ų':
		return 'u', true
	case 'ŵ':
		return 'w', true
	case 'ŷ':
		return 'y', true
	case 'ź', 'ż', 'ž':
		return 'z', true
	// Latin Extended-B & Latin Extended Additional (Vietnamese, Indic transliteration, etc.)
	case 'ǎ', 'ȁ', 'ȃ', 'ȧ', 'ạ', 'ả', 'ấ', 'ầ', 'ẩ', 'ẫ', 'ậ', 'ắ', 'ằ', 'ẳ', 'ẵ', 'ặ', 'ḁ', 'ẚ':
		return 'a', true
	case 'ḃ', 'ḅ', 'ḇ':
		return 'b', true
	case 'ḉ':
		return 'c', true
	case 'ḋ', 'ḍ', 'ḏ', 'ḑ', 'ḓ':
		return 'd', true
	case 'ȅ', 'ȇ', 'ȩ', 'ḕ', 'ḗ', 'ḙ', 'ḛ', 'ḝ', 'ẹ', 'ẻ', 'ẽ', 'ế', 'ề', 'ể', 'ễ', 'ệ':
		return 'e', true
	case 'ḟ':
		return 'f', true
	case 'ǧ', 'ǵ', 'ḡ':
		return 'g', true
	case 'ȟ', 'ḣ', 'ḥ', 'ḧ', 'ḩ', 'ḫ', 'ẖ':
		return 'h', true
	case 'ǐ', 'ȉ', 'ȋ', 'ḭ', 'ḯ', 'ỉ', 'ị':
		return 'i', true
	case 'ǰ':
		return 'j', true
	case 'ǩ', 'ḱ', 'ḳ', 'ḵ':
		return 'k', true
	case 'ḷ', 'ḹ', 'ḻ', 'ḽ':
		return 'l', true
	case 'ḿ', 'ṁ', 'ṃ':
		return 'm', true
	case 'ǹ', 'ṅ', 'ṇ', 'ṉ', 'ṋ':
		return 'n', true
	case 'ơ', 'ǒ', 'ǫ', 'ǭ', 'ȍ', 'ȏ', 'ȫ', 'ȭ', 'ȯ', 'ȱ', 'ṍ', 'ṏ', 'ṑ', 'ṓ', 'ọ', 'ỏ', 'ố', 'ồ', 'ổ', 'ỗ', 'ộ', 'ớ', 'ờ', 'ở', 'ỡ', 'ợ':
		return 'o', true
	case 'ṕ', 'ṗ':
		return 'p', true
	case 'ȑ', 'ȓ', 'ṙ', 'ṛ', 'ṝ', 'ṟ':
		return 'r', true
	case 'ș', 'ṡ', 'ṣ', 'ṥ', 'ṧ', 'ṩ':
		return 's', true
	case 'ț', 'ṫ', 'ṭ', 'ṯ', 'ṱ', 'ẗ':
		return 't', true
	case 'ư', 'ǔ', 'ǖ', 'ǘ', 'ǚ', 'ǜ', 'ȕ', 'ȗ', 'ṳ', 'ṵ', 'ṷ', 'ṹ', 'ṻ', 'ụ', 'ủ', 'ứ', 'ừ', 'ử', 'ữ', 'ự':
		return 'u', true
	case 'ṽ', 'ṿ':
		return 'v', true
	case 'ẁ', 'ẃ', 'ẅ', 'ẇ', 'ẉ', 'ẘ':
		return 'w', true
	case 'ẋ', 'ẍ':
		return 'x', true
	case 'ȳ', 'ẏ', 'ẙ', 'ỳ', 'ỵ', 'ỷ', 'ỹ':
		return 'y', true
	case 'ẑ', 'ẓ', 'ẕ':
		return 'z', true
	}
	return 0, false
}

// snippetContext is the number of runes kept on each side of a search match hit.
// upstream: packages/coding-agent/src/session/transcript-index.ts:72 (SNIPPET_CONTEXT = 100)
const snippetContext = 100

// makeSnippet cuts text to snippetContext runes around the first hit of q,
// marking each cut end with "…" and collapsing whitespace runs to single spaces.
//
// upstream: packages/coding-agent/src/session/transcript-index.ts:467-476
func makeSnippet(text string, q *WordQuery) string {
	hit, ok := q.Locate(text)
	if !ok {
		hit = TextRange{Start: 0, End: 0}
	}
	// Convert byte offsets to rune offsets so slicing never splits a multi-byte UTF-8 rune.
	runes := []rune(text)
	runeStart := len([]rune(text[:hit.Start]))
	runeEnd := len([]rune(text[:hit.End]))

	start := runeStart - snippetContext
	if start < 0 {
		start = 0
	}
	end := runeEnd + snippetContext
	if end > len(runes) {
		end = len(runes)
	}
	body := flattenLine(string(runes[start:end]))
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(body)
	if end < len(runes) {
		b.WriteString("…")
	}
	return b.String()
}
