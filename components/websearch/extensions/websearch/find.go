package websearch

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"
)

// Port of content-find.ts: locate passages in stored content without paging through it.
// All offsets are UTF-16 code units (the original's String indexes), so excerpts cut exactly
// where the original cut.

// FindMode is how findText matches.
type FindMode string

const (
	FindExact           FindMode = "exact"
	FindCaseInsensitive FindMode = "case-insensitive"
	FindFuzzy           FindMode = "fuzzy"
)

const (
	findContextChars = 400
	findMaxOutput    = 20000
)

// QueryMatchCount is the per-query match count of a find.
type QueryMatchCount struct {
	Query      string `json:"query"`
	MatchCount int    `json:"matchCount"`
}

// FindResult is the bounded excerpt text and the counts.
type FindResult struct {
	Text            string
	MatchCount      int
	ReturnedMatches int
	QueryResults    []QueryMatchCount
}

type findMatch struct {
	query      string
	start, end int
}

type findRange struct{ start, end int }

// u16text is a string with UTF-16 offsets.
type u16text struct {
	u []uint16
}

func newU16(s string) u16text { return u16text{u: utf16.Encode([]rune(s))} }

func (t u16text) slice(start, end int) string {
	if end > len(t.u) {
		end = len(t.u)
	}
	if start < 0 {
		start = 0
	}
	if start >= end {
		return ""
	}
	return string(utf16.Decode(t.u[start:end]))
}

func isJSSpace(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF }

func collapseSpaces(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// foldQuery is `normalize`: NFD, drop diacritics, lowercase.
func foldQuery(value string) []uint16 {
	decomposed := norm.NFD.String(value)
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Diacritic, r) {
			continue
		}
		b.WriteRune(r)
	}
	return utf16.Encode([]rune(strings.ToLower(b.String())))
}

func editDistanceWithin(left, right []uint16, maximum int) bool {
	abs := len(left) - len(right)
	if abs < 0 {
		abs = -abs
	}
	if abs > maximum {
		return false
	}
	previous := make([]int, len(right)+1)
	for i := range previous {
		previous[i] = i
	}
	for i := 1; i <= len(left); i++ {
		current := make([]int, len(right)+1)
		current[0] = i
		rowMin := i
		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}
			v := previous[j] + 1
			if current[j-1]+1 < v {
				v = current[j-1] + 1
			}
			if previous[j-1]+cost < v {
				v = previous[j-1] + cost
			}
			current[j] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > maximum {
			return false
		}
		previous = current
	}
	return previous[len(right)] <= maximum
}

func literalMatches(text u16text, raw, query string, caseInsensitive bool) []findMatch {
	hay := text.u
	needle := utf16.Encode([]rune(query))
	if caseInsensitive {
		hay = utf16.Encode([]rune(strings.ToLower(raw)))
		needle = utf16.Encode([]rune(strings.ToLower(query)))
	}
	qlen := jsLen(query)
	var out []findMatch
	step := len(needle)
	if step < 1 {
		step = 1
	}
	for start := indexU16(hay, needle, 0); start >= 0; start = indexU16(hay, needle, start+step) {
		out = append(out, findMatch{query, start, start + qlen})
	}
	return out
}

func indexU16(hay, needle []uint16, from int) int {
	if from < 0 {
		from = 0
	}
	if len(needle) == 0 {
		if from > len(hay) {
			return len(hay)
		}
		return from
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		if hay[i] != needle[0] {
			continue
		}
		ok := true
		for j := 1; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

var wordRun = regexp.MustCompile(`[\p{L}\p{N}]+`)

type token struct {
	text  []uint16
	start int // UTF-16 offset in the paragraph
}

func tokensOf(paragraph string) []token {
	locs := wordRun.FindAllStringIndex(paragraph, -1)
	var out []token
	u := 0
	prev := 0
	for _, loc := range locs {
		u += jsLen(paragraph[prev:loc[0]])
		word := paragraph[loc[0]:loc[1]]
		out = append(out, token{text: foldQuery(word), start: u})
		u += jsLen(word)
		prev = loc[1]
	}
	return out
}

func fuzzyMax(queryToken []uint16) int {
	switch {
	case len(queryToken) >= 9:
		return 2
	case len(queryToken) >= 5:
		return 1
	}
	return 0
}

func fuzzyMatches(text u16text, raw, query string) []findMatch {
	var queryTokens [][]uint16
	for _, w := range wordRun.FindAllString(string(utf16.Decode(foldQuery(query))), -1) {
		queryTokens = append(queryTokens, utf16.Encode([]rune(w)))
	}
	if len(queryTokens) == 0 {
		return nil
	}
	var out []findMatch
	// Paragraphs: maximal runs of non-empty lines joined by single newlines.
	lines := strings.Split(raw, "\n")
	offset := 0 // UTF-16 offset of the current line start
	var group []string
	groupStart := 0
	flush := func() {
		if len(group) == 0 {
			return
		}
		paragraph := strings.Join(group, "\n")
		group = nil
		if jsTrim(paragraph) == "" {
			return
		}
		tokens := tokensOf(paragraph)
		var matched [][]uint16
		for _, qt := range queryTokens {
			max := fuzzyMax(qt)
			for _, tok := range tokens {
				if editDistanceWithin(qt, tok.text, max) {
					matched = append(matched, qt)
					break
				}
			}
		}
		required := 1
		if len(queryTokens) != 1 {
			required = (len(queryTokens)*6 + 9) / 10 // ceil(n * 0.6)
		}
		if len(matched) < required {
			return
		}
		for _, tok := range tokens {
			hit := false
			for _, qt := range matched {
				if editDistanceWithin(qt, tok.text, fuzzyMax(qt)) {
					hit = true
					break
				}
			}
			if hit {
				start := groupStart + tok.start
				// the token's original length (not the folded one)
				out = append(out, findMatch{query, start, start + tokenSourceLen(paragraph, tok.start)})
				return
			}
		}
	}
	for _, line := range lines {
		if line == "" {
			flush()
		} else {
			if len(group) == 0 {
				groupStart = offset
			}
			group = append(group, line)
		}
		offset += jsLen(line) + 1
	}
	flush()
	return out
}

// tokenSourceLen is the UTF-16 length of the word that starts at UTF-16 offset start.
func tokenSourceLen(paragraph string, start int) int {
	u := 0
	for _, loc := range wordRun.FindAllStringIndex(paragraph, -1) {
		pre := jsLen(paragraph[:loc[0]])
		_ = u
		if pre == start {
			return jsLen(paragraph[loc[0]:loc[1]])
		}
	}
	return 0
}

func mergeRanges(ranges []findRange) []findRange {
	sorted := append([]findRange{}, ranges...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].start != sorted[j].start {
			return sorted[i].start < sorted[j].start
		}
		return sorted[i].end < sorted[j].end
	})
	var merged []findRange
	for _, r := range sorted {
		if n := len(merged); n > 0 && r.start <= merged[n-1].end {
			if r.end > merged[n-1].end {
				merged[n-1].end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func contextRanges(textLength int, matches []findMatch) []findRange {
	var ranges []findRange
	for _, m := range matches {
		s := m.start - findContextChars
		if s < 0 {
			s = 0
		}
		e := m.end + findContextChars
		if e > textLength {
			e = textLength
		}
		ranges = append(ranges, findRange{s, e})
	}
	return mergeRanges(ranges)
}

func lowerBound(values []int, target int) int {
	low, high := 0, len(values)
	for low < high {
		mid := (low + high) >> 1
		if values[mid] < target {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low
}

func upperBound(values []int, target int) int {
	low, high := 0, len(values)
	for low < high {
		mid := (low + high) >> 1
		if values[mid] <= target {
			low = mid + 1
		} else {
			high = mid
		}
	}
	return low
}

type occurrence struct {
	query        string
	matches      []findMatch
	starts, ends []int
	id           string
	order        int
}

type rangeCount struct {
	occ        *occurrence
	count      int
	firstStart int
}

func findContent(raw string, queries []string, mode FindMode) FindResult {
	text := newU16(raw)
	textLen := len(text.u)
	seen := map[string]bool{}
	var normalized []string
	for _, q := range queries {
		q = jsTrim(q)
		if q != "" && !seen[q] {
			seen[q] = true
			normalized = append(normalized, q)
		}
	}
	occs := make([]*occurrence, 0, len(normalized))
	for _, q := range normalized {
		var ms []findMatch
		if mode == FindFuzzy {
			ms = fuzzyMatches(text, raw, q)
		} else {
			ms = literalMatches(text, raw, q, mode == FindCaseInsensitive)
		}
		o := &occurrence{query: q, matches: ms}
		for _, m := range ms {
			o.starts = append(o.starts, m.start)
			o.ends = append(o.ends, m.end)
		}
		occs = append(occs, o)
	}
	var matches []findMatch
	var queryResults []QueryMatchCount
	for _, o := range occs {
		matches = append(matches, o.matches...)
		queryResults = append(queryResults, QueryMatchCount{o.query, len(o.matches)})
	}

	heading := "Text matches (" + string(mode) + ")"
	if len(matches) == 0 {
		heading = "Text matches (" + string(mode) + "): no matches"
	}
	var missingList []string
	for _, r := range queryResults {
		if r.MatchCount == 0 {
			missingList = append(missingList, `"`+r.Query+`"`)
		}
	}
	var matching []*occurrence
	for _, o := range occs {
		if len(o.matches) > 0 {
			o.id = "Q" + strconv.Itoa(len(matching)+1)
			o.order = len(matching)
			matching = append(matching, o)
		}
	}
	// whitespace runs
	var wsStarts, wsEnds []int
	{
		pos := 0
		inRun := false
		runStart := 0
		for _, r := range raw {
			w := 1
			if r >= 0x10000 {
				w = 2
			}
			if isJSSpace(r) {
				if !inRun {
					inRun, runStart = true, pos
				}
			} else if inRun {
				wsStarts, wsEnds = append(wsStarts, runStart), append(wsEnds, pos)
				inRun = false
			}
			pos += w
		}
		if inRun {
			wsStarts, wsEnds = append(wsStarts, runStart), append(wsEnds, pos)
		}
	}
	savings := []int{0}
	for i := range wsStarts {
		savings = append(savings, savings[len(savings)-1]+wsEnds[i]-wsStarts[i]-1)
	}
	normalizedLength := func(start, end int) int {
		firstRun := upperBound(wsStarts, start) - 1
		if firstRun >= 0 && wsEnds[firstRun] > start {
			start = wsEnds[firstRun]
		}
		if start >= end {
			return 0
		}
		lastRun := upperBound(wsStarts, end-1) - 1
		if lastRun >= 0 && wsEnds[lastRun] >= end {
			end = wsStarts[lastRun]
		}
		if start >= end {
			return 0
		}
		first := lowerBound(wsStarts, start)
		last := upperBound(wsEnds, end)
		return end - start - (savings[last] - savings[first])
	}
	rangeCounts := func(r findRange) []rangeCount {
		var out []rangeCount
		for _, o := range matching {
			first := lowerBound(o.starts, r.start)
			last := upperBound(o.ends, r.end)
			if last > first {
				out = append(out, rangeCount{o, last - first, o.starts[first]})
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].firstStart != out[j].firstStart {
				return out[i].firstStart < out[j].firstStart
			}
			return out[i].occ.order < out[j].occ.order
		})
		return out
	}
	var legend string
	if len(matching) > 0 {
		parts := make([]string, len(matching))
		for i, o := range matching {
			parts[i] = o.id + ` = "` + o.query + `"`
		}
		legend = "Queries: " + strings.Join(parts, ", ")
	}
	missingNotice := ""
	if len(missingList) > 0 {
		missingNotice = "No matches: " + strings.Join(missingList, ", ")
	}
	measure := func(ranges []findRange, overflow bool, omitted []string) (int, int) {
		length := jsLen(heading)
		returned := 0
		if overflow && legend != "" {
			length += 2 + jsLen(legend)
		}
		for index, r := range ranges {
			counts := rangeCounts(r)
			if len(counts) == 0 {
				continue
			}
			labels := 2 * (len(counts) - 1)
			for _, c := range counts {
				if overflow {
					labels += jsLen(c.occ.id)
				} else {
					labels += jsLen(c.occ.query) + 2
				}
				labels += 2 + len(strconv.Itoa(c.count))
			}
			snippet := normalizedLength(r.start, r.end)
			if r.start > 0 {
				snippet++
			}
			if r.end < textLen {
				snippet++
			}
			length += 2 + len(strconv.Itoa(index+1)) + 2 + labels + 1 + snippet
			for _, c := range counts {
				returned += c.count
			}
		}
		if missingNotice != "" {
			length += 2 + jsLen(missingNotice)
		}
		if len(omitted) > 0 {
			length += 2 + jsLen("No representative excerpt: "+strings.Join(omitted, ", ")+".")
		}
		if returned < len(matches) {
			length += 2 + jsLen("Showing "+strconv.Itoa(returned)+" of "+strconv.Itoa(len(matches))+" matches.")
		}
		return length, returned
	}
	formatRanges := func(ranges []findRange, overflow bool, omitted []string) FindResult {
		sections := []string{heading}
		returned := 0
		if overflow && legend != "" {
			sections = append(sections, legend)
		}
		for index, r := range ranges {
			contained := rangeCounts(r)
			if len(contained) == 0 {
				continue
			}
			prefix, suffix := "", ""
			if r.start > 0 {
				prefix = "…"
			}
			if r.end < textLen {
				suffix = "…"
			}
			snippet := prefix + jsTrim(collapseSpaces(text.slice(r.start, r.end))) + suffix
			parts := make([]string, len(contained))
			for i, c := range contained {
				label := `"` + c.occ.query + `"`
				if overflow {
					label = c.occ.id
				}
				parts[i] = label + " ×" + strconv.Itoa(c.count)
				returned += c.count
			}
			sections = append(sections, strconv.Itoa(index+1)+". "+strings.Join(parts, ", ")+"\n"+snippet)
		}
		if missingNotice != "" {
			sections = append(sections, missingNotice)
		}
		if len(omitted) > 0 {
			sections = append(sections, "No representative excerpt: "+strings.Join(omitted, ", ")+".")
		}
		if returned < len(matches) {
			sections = append(sections, "Showing "+strconv.Itoa(returned)+" of "+strconv.Itoa(len(matches))+" matches.")
		}
		return FindResult{Text: strings.Join(sections, "\n\n"), MatchCount: len(matches), ReturnedMatches: returned, QueryResults: queryResults}
	}

	fullRanges := contextRanges(textLen, matches)
	fullLen, fullReturned := measure(fullRanges, false, nil)
	if fullReturned == len(matches) && fullLen <= findMaxOutput {
		return formatRanges(fullRanges, false, nil)
	}

	var ranges []findRange
	omitted := make([]string, len(matching))
	for i, o := range matching {
		omitted[i] = o.id
	}
	if l, _ := measure(ranges, true, omitted); l > findMaxOutput {
		text := heading + "\n\nUnable to format bounded excerpts: query metadata exceeds " + strconv.Itoa(findMaxOutput) + " characters.\n\nShowing 0 of " + strconv.Itoa(len(matches)) + " matches."
		return FindResult{Text: text, MatchCount: len(matches), ReturnedMatches: 0, QueryResults: queryResults}
	}
	var witnesses []findMatch
	for _, o := range matching {
		var proposedOmitted []string
		for _, id := range omitted {
			if id != o.id {
				proposedOmitted = append(proposedOmitted, id)
			}
		}
		currentLength, _ := measure(ranges, true, omitted)
		var selected *struct {
			witness findMatch
			ranges  []findRange
			cost    int
		}
		for _, w := range o.matches {
			proposedRanges := mergeRanges(append(append([]findRange{}, ranges...), findRange{w.start, w.end}))
			l, _ := measure(proposedRanges, true, proposedOmitted)
			cost := l - currentLength
			if l <= findMaxOutput && (selected == nil || cost < selected.cost ||
				(cost == selected.cost && (w.start < selected.witness.start || (w.start == selected.witness.start && w.end < selected.witness.end)))) {
				selected = &struct {
					witness findMatch
					ranges  []findRange
					cost    int
				}{w, proposedRanges, cost}
			}
		}
		if selected != nil {
			ranges = selected.ranges
			omitted = proposedOmitted
			witnesses = append(witnesses, selected.witness)
		}
	}
	for _, w := range witnesses {
		s := w.start - findContextChars
		if s < 0 {
			s = 0
		}
		e := w.end + findContextChars
		if e > textLen {
			e = textLen
		}
		proposed := mergeRanges(append(append([]findRange{}, ranges...), findRange{s, e}))
		if l, _ := measure(proposed, true, omitted); l <= findMaxOutput {
			ranges = proposed
		}
	}
	all := append([]findRange{}, ranges...)
	for _, m := range matches {
		all = append(all, findRange{m.start, m.end})
	}
	all = mergeRanges(all)
	if l, _ := measure(all, true, omitted); l <= findMaxOutput {
		ranges = all
	}
	return formatRanges(ranges, true, omitted)
}
