package settings

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Port of omp's word-local fuzzy matcher (packages/tui/src/fuzzy.ts) so
// `cfg://` "Similar:" suggestions match omp v19.1.1 exactly (fixes A4).

const (
	alphanumericSwapPenalty = 5.0
	compactPhraseBonus      = 1200.0
	phraseBonus             = 1000.0
	minShadowRescanLength   = 3
)

type fuzzyMatchResult struct {
	matches bool
	score   float64
}

type searchWord struct {
	text    string
	index   int
	ordinal int
}

type searchIndex struct {
	normalized        string
	compact           string
	compactWordStarts map[int]bool
	words             []searchWord
}

// normalizeForSearch splits camelCase boundaries, lowercases, and collapses
// non-letter/mark/number runs to single ASCII spaces.
// upstream: packages/tui/src/fuzzy.ts:55
func normalizeForSearch(value string) string {
	runes := []rune(value)
	var split []rune
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			// ([A-Z]+)([A-Z][a-z]) -> insert space before r when prev is upper, r is upper, next is lower
			if i+1 < len(runes) && isASCIIUpper(prev) && isASCIIUpper(r) && isASCIILower(runes[i+1]) {
				split = append(split, ' ')
			} else if (isASCIILower(prev) || isASCIIDigit(prev)) && isASCIIUpper(r) {
				// ([a-z0-9])([A-Z])
				split = append(split, ' ')
			}
		}
		split = append(split, unicode.ToLower(r))
	}
	var b strings.Builder
	inSpace := true
	for _, r := range split {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			inSpace = false
		} else if !inSpace {
			b.WriteByte(' ')
			inSpace = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func isASCIIUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isASCIILower(r rune) bool { return r >= 'a' && r <= 'z' }
func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func buildSearchIndex(text string) searchIndex {
	norm := normalizeForSearch(text)
	if norm == "" {
		return searchIndex{compactWordStarts: map[int]bool{}}
	}
	parts := strings.Split(norm, " ")
	words := make([]searchWord, 0, len(parts))
	starts := make(map[int]bool, len(parts))
	idx := 0
	compactIdx := 0
	for ord, w := range parts {
		words = append(words, searchWord{text: w, index: idx, ordinal: ord})
		starts[compactIdx] = true
		idx += len(w) + 1
		compactIdx += len(w)
	}
	return searchIndex{
		normalized:        norm,
		compact:           strings.ReplaceAll(norm, " ", ""),
		compactWordStarts: starts,
		words:             words,
	}
}

func scoreCharacters(queryLower, textLower string) (matches bool, score float64, span int) {
	if len(queryLower) == 0 {
		return true, 0, 0
	}
	if len(queryLower) > len(textLower) {
		return false, 0, 0
	}
	qi := 0
	firstMatch := -1
	lastMatch := -1
	consec := 0
	for i := 0; i < len(textLower) && qi < len(queryLower); i++ {
		if textLower[i] == queryLower[qi] {
			if firstMatch < 0 {
				firstMatch = i
			}
			if lastMatch == i-1 {
				consec++
				score -= float64(consec) * 5
			} else {
				consec = 0
				if lastMatch >= 0 {
					score += float64(i-lastMatch-1) * 2
				}
			}
			score += float64(i) * 0.1
			lastMatch = i
			qi++
		}
	}
	if qi < len(queryLower) {
		return false, 0, 0
	}
	return true, score, lastMatch - firstMatch + 1
}

func buildAlphanumericSwapQueries(q string) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < len(q)-1; i++ {
		cur := q[i]
		nxt := q[i+1]
		isSwap := (cur >= 'a' && cur <= 'z' && nxt >= '0' && nxt <= '9') ||
			(cur >= '0' && cur <= '9' && nxt >= 'a' && nxt <= 'z')
		if !isSwap {
			continue
		}
		swapped := q[:i] + string([]byte{nxt, cur}) + q[i+2:]
		if !seen[swapped] {
			seen[swapped] = true
			out = append(out, swapped)
		}
	}
	return out
}

func withPosition(score float64, index int) float64 {
	return score + float64(index)*0.01
}

func isWordBoundaryPhrase(norm string, index, length int) bool {
	before := index == 0 || norm[index-1] == ' '
	afterIdx := index + length
	after := afterIdx == len(norm) || norm[afterIdx] == ' '
	return before && after
}

func findWordBoundaryPhrase(norm, phrase string) int {
	if len(phrase) == 0 {
		return -1
	}
	first := strings.Index(norm, phrase)
	if first < 0 {
		return -1
	}
	if isWordBoundaryPhrase(norm, first, len(phrase)) {
		return first
	}
	if first == 0 || norm[first-1] == ' ' {
		return -1
	}
	if len(phrase) < minShadowRescanLength {
		return -1
	}
	for pos := first + 1; pos < len(norm); {
		rel := strings.Index(norm[pos:], phrase)
		if rel < 0 {
			return -1
		}
		at := pos + rel
		if isWordBoundaryPhrase(norm, at, len(phrase)) {
			return at
		}
		pos = at + 1
	}
	return -1
}

func findCompactWordStart(idx searchIndex, needle string) int {
	if len(needle) == 0 {
		return -1
	}
	first := strings.Index(idx.compact, needle)
	if first < 0 {
		return -1
	}
	if idx.compactWordStarts[first] {
		return first
	}
	if len(needle) < minShadowRescanLength {
		return -1
	}
	for pos := first + 1; pos < len(idx.compact); {
		rel := strings.Index(idx.compact[pos:], needle)
		if rel < 0 {
			return -1
		}
		at := pos + rel
		if idx.compactWordStarts[at] {
			return at
		}
		pos = at + 1
	}
	return -1
}

func scoreTokenAgainstWord(token string, word searchWord) (fuzzyMatchResult, bool) {
	if word.text == token {
		return fuzzyMatchResult{matches: true, score: withPosition(-200, word.index)}, true
	}
	if strings.HasPrefix(word.text, token) {
		return fuzzyMatchResult{matches: true, score: withPosition(-170+float64(len(word.text)-len(token))*0.5, word.index)}, true
	}
	if strings.HasPrefix(token, word.text) && len(token)-len(word.text) <= 2 {
		return fuzzyMatchResult{matches: true, score: withPosition(-150+float64(len(token)-len(word.text)), word.index)}, true
	}
	if subIdx := strings.Index(word.text, token); subIdx >= 0 {
		return fuzzyMatchResult{matches: true, score: withPosition(-20+float64(subIdx), word.index)}, true
	}
	ok, sc, span := scoreCharacters(token, word.text)
	if !ok {
		return fuzzyMatchResult{}, false
	}
	maxSpan := max(len(token)+2, int(math.Ceil(float64(len(token))*1.8)))
	if span > maxSpan {
		return fuzzyMatchResult{}, false
	}
	return fuzzyMatchResult{matches: true, score: withPosition(-40+sc, word.index)}, true
}

func scoreAcronym(token string, idx searchIndex) (fuzzyMatchResult, bool) {
	if len(token) < 2 || len(token) > 4 || len(idx.words) == 0 {
		return fuzzyMatchResult{}, false
	}
	qi := 0
	firstOrd := -1
	lastOrd := -1
	firstTextIdx := 0
	for _, w := range idx.words {
		if len(w.text) == 0 || w.text[0] != token[qi] {
			continue
		}
		if firstOrd < 0 {
			firstOrd = w.ordinal
			firstTextIdx = w.index
		}
		lastOrd = w.ordinal
		qi++
		if qi == len(token) {
			break
		}
	}
	if qi < len(token) || firstOrd < 0 || lastOrd < 0 {
		return fuzzyMatchResult{}, false
	}
	wordSpan := lastOrd - firstOrd + 1
	if wordSpan > len(token)+2 {
		return fuzzyMatchResult{}, false
	}
	return fuzzyMatchResult{matches: true, score: withPosition(-30+float64(wordSpan*4-len(token)*2), firstTextIdx)}, true
}

func scoreTokenDirect(token string, idx searchIndex) fuzzyMatchResult {
	if token == "" {
		return fuzzyMatchResult{matches: true, score: 0}
	}
	var best *fuzzyMatchResult
	if compactIdx := findCompactWordStart(idx, token); compactIdx >= 0 {
		m := fuzzyMatchResult{matches: true, score: withPosition(-140, compactIdx)}
		best = &m
	}
	for _, w := range idx.words {
		if m, ok := scoreTokenAgainstWord(token, w); ok {
			if best == nil || m.score < best.score {
				cp := m
				best = &cp
			}
		}
	}
	if acr, ok := scoreAcronym(token, idx); ok {
		if best == nil || acr.score < best.score {
			cp := acr
			best = &cp
		}
	}
	if best == nil {
		return fuzzyMatchResult{}
	}
	return *best
}

func scoreToken(token string, swapVariants []string, idx searchIndex) fuzzyMatchResult {
	best := scoreTokenDirect(token, idx)
	if best.matches {
		return best
	}
	for _, v := range swapVariants {
		m := scoreTokenDirect(v, idx)
		if !m.matches {
			continue
		}
		sc := m.score + alphanumericSwapPenalty
		if !best.matches || sc < best.score {
			best = fuzzyMatchResult{matches: true, score: sc}
		}
	}
	return best
}

type preparedQuery struct {
	normalized string
	tokens     []string
	tokenSwaps [][]string
	compact    string
}

func prepareQuery(query string) *preparedQuery {
	norm := normalizeForSearch(query)
	if norm == "" {
		return nil
	}
	tokens := strings.Split(norm, " ")
	swaps := make([][]string, len(tokens))
	for i, tok := range tokens {
		swaps[i] = buildAlphanumericSwapQueries(tok)
	}
	return &preparedQuery{
		normalized: norm,
		tokens:     tokens,
		tokenSwaps: swaps,
		compact:    strings.ReplaceAll(norm, " ", ""),
	}
}

func fuzzyMatchCore(pq *preparedQuery, idx searchIndex) fuzzyMatchResult {
	if pq == nil {
		return fuzzyMatchResult{matches: true, score: 0}
	}
	if len(idx.words) == 0 {
		return fuzzyMatchResult{}
	}
	total := 0.0
	if phraseIdx := findWordBoundaryPhrase(idx.normalized, pq.normalized); phraseIdx >= 0 {
		total -= phraseBonus
		total += float64(phraseIdx) * 0.01
	}
	if compactIdx := findCompactWordStart(idx, pq.compact); compactIdx >= 0 {
		total -= compactPhraseBonus
		total += float64(compactIdx) * 0.01
	}
	for i, tok := range pq.tokens {
		m := scoreToken(tok, pq.tokenSwaps[i], idx)
		if !m.matches {
			return fuzzyMatchResult{}
		}
		total += m.score
	}
	return fuzzyMatchResult{matches: true, score: total}
}

// fuzzyFilterSettings mirrors omp's fuzzyFilter(allSettings().map(s => s.id), query, s => s).
// upstream: packages/coding-agent/src/internal-urls/cfg-protocol.ts:170
func fuzzyFilterSettings(query string, all []*Entry, limit int) []string {
	if strings.TrimSpace(query) == "" {
		n := min(len(all), limit)
		out := make([]string, n)
		for i := range n {
			out[i] = all[i].ID
		}
		return out
	}
	pq := prepareQuery(query)
	type scored struct {
		id    string
		score float64
		order int
	}
	var hits []scored
	for i, e := range all {
		m := fuzzyMatchCore(pq, buildSearchIndex(e.ID))
		if m.matches {
			hits = append(hits, scored{id: e.ID, score: m.score, order: i})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return hits[i].score < hits[j].score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}
