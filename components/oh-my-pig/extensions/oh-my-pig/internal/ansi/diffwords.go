package ansi

import (
	"strings"
	"unicode/utf16"
)

// wordChange represents one run from diffWords, matching jsdiff / omp crates/diff
// Change<Vec<u16>> semantics.
type wordChange struct {
	value   string
	added   bool
	removed bool
}

// isWordChar matches crates/diff/src/lib.rs is_word_char (jsdiff extendedWordChars).
func isWordChar(cp uint32) bool {
	return (cp >= 0x30 && cp <= 0x39) || // 0-9
		(cp >= 0x41 && cp <= 0x5A) || // A-Z
		cp == 0x5F || // _
		(cp >= 0x61 && cp <= 0x7A) || // a-z
		cp == 0xAD ||
		(cp >= 0xC0 && cp <= 0xD6) ||
		(cp >= 0xD8 && cp <= 0xF6) ||
		(cp >= 0xF8 && cp <= 0x2C6) ||
		(cp >= 0x2C8 && cp <= 0x2D7) ||
		(cp >= 0x2DE && cp <= 0x2FF) ||
		(cp >= 0x1E00 && cp <= 0x1EFF)
}

// isJSWhitespace matches crates/diff/src/lib.rs is_js_whitespace (ECMAScript \s).
func isJSWhitespace(cp uint32) bool {
	switch cp {
	case 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0xa0, 0x1680,
		0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	default:
		return cp >= 0x2000 && cp <= 0x200a
	}
}

func isWSUnit(u uint16) bool {
	return isJSWhitespace(uint32(u))
}

func trimLeadingWS(s []uint16) []uint16 {
	i := 0
	for i < len(s) && isWSUnit(s[i]) {
		i++
	}
	return s[i:]
}

func trimTrailingWS(s []uint16) []uint16 {
	end := len(s)
	for end > 0 && isWSUnit(s[end-1]) {
		end--
	}
	return s[:end]
}

func leadingWS(s []uint16) []uint16 {
	return s[:len(s)-len(trimLeadingWS(s))]
}

func trailingWS(s []uint16) []uint16 {
	return s[len(trimTrailingWS(s)):]
}

func jsTrim(s []uint16) []uint16 {
	return trimTrailingWS(trimLeadingWS(s))
}

type u16Slice []uint16

func u16Equal(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func u16EndsWith(s, suffix []uint16) bool {
	if len(suffix) > len(s) {
		return false
	}
	return u16Equal(s[len(s)-len(suffix):], suffix)
}

func u16StartsWith(s, prefix []uint16) bool {
	if len(prefix) > len(s) {
		return false
	}
	return u16Equal(s[:len(prefix)], prefix)
}

func longestCommonPrefixU16(a, b []uint16) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func longestCommonSuffixU16(a, b []uint16) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

// maximumOverlapU16 returns the length of the longest prefix of b that is also
// a suffix of a, matching crates/diff/src/lib.rs maximum_overlap.
func maximumOverlapU16(a, b []uint16) int {
	startA := max(0, len(a)-len(b))
	endB := min(len(b), len(a))
	if endB == 0 {
		return 0
	}
	table := make([]int, endB)
	k := 0
	for j := 1; j < endB; j++ {
		if b[j] == b[k] {
			table[j] = table[k]
		} else {
			table[j] = k
		}
		for k > 0 && b[j] != b[k] {
			k = table[k]
		}
		if b[j] == b[k] {
			k++
		}
	}
	k = 0
	for _, u := range a[startA:] {
		for k > 0 && u != b[k] {
			k = table[k]
		}
		if u == b[k] {
			k++
		}
	}
	return k
}

type u16CodePoint struct {
	start int
	cp    uint32
	n     int
}

func nextCodePointU16(text []uint16, pos int) (u16CodePoint, bool) {
	if pos >= len(text) {
		return u16CodePoint{}, false
	}
	unit := text[pos]
	if unit >= 0xD800 && unit <= 0xDBFF && pos+1 < len(text) {
		low := text[pos+1]
		if low >= 0xDC00 && low <= 0xDFFF {
			cp := 0x10000 + ((uint32(unit) - 0xD800) << 10) + (uint32(low) - 0xDC00)
			return u16CodePoint{start: pos, cp: cp, n: 2}, true
		}
	}
	return u16CodePoint{start: pos, cp: uint32(unit), n: 1}, true
}

// wordParts splits text into word runs, whitespace runs, or single other code points.
func wordParts(text []uint16) [][]uint16 {
	var parts [][]uint16
	pos := 0
	for {
		cur, ok := nextCodePointU16(text, pos)
		if !ok {
			break
		}
		class := 0
		if isWordChar(cur.cp) {
			class = 1
		} else if isJSWhitespace(cur.cp) {
			class = 2
		}
		end := cur.start + cur.n
		if class != 0 {
			for {
				nxt, ok := nextCodePointU16(text, end)
				if !ok {
					break
				}
				same := (class == 1 && isWordChar(nxt.cp)) || (class == 2 && isJSWhitespace(nxt.cp))
				if !same {
					break
				}
				end += nxt.n
			}
		}
		parts = append(parts, text[cur.start:end])
		pos = end
	}
	return parts
}

// wordTokens stitches whitespace runs onto adjacent word or punctuation parts,
// matching crates/diff/src/lib.rs word_tokens (jsdiff WordDiff.tokenize).
func wordTokens(text []uint16) [][]uint16 {
	parts := wordParts(text)
	tokens := make([][]uint16, 0, len(parts))
	var prevPart []uint16
	hasPrev := false
	offset := 0
	for _, part := range parts {
		end := offset + len(part)
		partIsWS := len(part) > 0 && isWSUnit(part[0])
		if partIsWS {
			if !hasPrev {
				tokens = append(tokens, part)
			} else {
				last := tokens[len(tokens)-1]
				tokens[len(tokens)-1] = text[offset-len(last) : end]
			}
		} else if hasPrev && len(prevPart) > 0 && isWSUnit(prevPart[0]) {
			tok := text[offset-len(prevPart) : end]
			if len(tokens) > 0 && u16Equal(tokens[len(tokens)-1], prevPart) {
				tokens[len(tokens)-1] = tok
			} else {
				tokens = append(tokens, tok)
			}
		} else {
			tokens = append(tokens, part)
		}
		prevPart = part
		hasPrev = true
		offset = end
	}
	return tokens
}

// wordJoin concatenates tokens, stripping leading whitespace from every token
// after the first (crates/diff/src/lib.rs word_join).
func wordJoin(tokens [][]uint16) []uint16 {
	capLen := 0
	for i, t := range tokens {
		if i == 0 {
			capLen += len(t)
		} else {
			capLen += len(trimLeadingWS(t))
		}
	}
	out := make([]uint16, 0, capLen)
	for i, t := range tokens {
		if i == 0 {
			out = append(out, t...)
		} else {
			out = append(out, trimLeadingWS(t)...)
		}
	}
	return out
}

type diffRun struct {
	count   int
	added   bool
	removed bool
}

type componentNode struct {
	count   int
	prev    *componentNode
	added   bool
	removed bool
}

type pathState struct {
	oldPos int
	last   *componentNode
}

func extractCommon(path *pathState, newIDs, oldIDs []uint32, diagonal int) int {
	newLen := len(newIDs)
	oldLen := len(oldIDs)
	oldPos := path.oldPos
	newPos := oldPos - diagonal
	common := 0
	for newPos+1 < newLen && oldPos+1 < oldLen && oldIDs[oldPos+1] == newIDs[newPos+1] {
		newPos++
		oldPos++
		common++
	}
	if common > 0 {
		path.last = &componentNode{count: common, prev: path.last}
	}
	path.oldPos = oldPos
	return newPos
}

func addToPath(path pathState, added, removed bool, oldPosInc int) pathState {
	last := path.last
	if last != nil && last.added == added && last.removed == removed {
		return pathState{
			oldPos: path.oldPos + oldPosInc,
			last: &componentNode{
				count:   last.count + 1,
				prev:    last.prev,
				added:   added,
				removed: removed,
			},
		}
	}
	return pathState{
		oldPos: path.oldPos + oldPosInc,
		last: &componentNode{
			count:   1,
			prev:    last,
			added:   added,
			removed: removed,
		},
	}
}

func buildRuns(last *componentNode) []diffRun {
	var runs []diffRun
	for cur := last; cur != nil; cur = cur.prev {
		runs = append(runs, diffRun{
			count:   cur.count,
			added:   cur.added,
			removed: cur.removed,
		})
	}
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs
}

// myersDiff replicates crates/diff/src/lib.rs myers_diff_within (jsdiff order).
func myersDiff(oldIDs, newIDs []uint32) []diffRun {
	oldLen := len(oldIDs)
	newLen := len(newIDs)
	maxEdit := oldLen + newLen
	offset := maxEdit + 1
	best := make([]*pathState, 2*maxEdit+3)

	seed := pathState{oldPos: -1}
	seedNewPos := extractCommon(&seed, newIDs, oldIDs, 0)
	if seed.oldPos+1 >= oldLen && seedNewPos+1 >= newLen {
		return buildRuns(seed.last)
	}
	best[offset] = &seed

	minDiag := -maxEdit - 1
	maxDiag := maxEdit + 1
	for editLen := 1; editLen <= maxEdit; editLen++ {
		diagStart := max(minDiag, -editLen)
		diagEnd := min(maxDiag, editLen)
		for diag := diagStart; diag <= diagEnd; diag += 2 {
			idx := diag + offset
			removePath := best[idx-1]
			best[idx-1] = nil
			addPath := best[idx+1]

			canAdd := false
			if addPath != nil {
				addNewPos := addPath.oldPos - diag
				canAdd = addNewPos >= 0 && addNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				best[idx] = nil
				continue
			}

			var basePath pathState
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				basePath = addToPath(*addPath, true, false, 0)
			} else {
				basePath = addToPath(*removePath, false, true, 1)
			}

			newPos := extractCommon(&basePath, newIDs, oldIDs, diag)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildRuns(basePath.last)
			}
			if basePath.oldPos+1 >= oldLen {
				maxDiag = min(maxDiag, diag-1)
			}
			if newPos+1 >= newLen {
				minDiag = max(minDiag, diag+1)
			}
			bp := basePath
			best[idx] = &bp
		}
	}
	return nil
}

type rawWordChange struct {
	value   []uint16
	added   bool
	removed bool
}

func dedupeWhitespace(changes []rawWordChange, startKeep, deletion, insertion, endKeep int) {
	hasStart := startKeep >= 0
	hasDel := deletion >= 0
	hasIns := insertion >= 0
	hasEnd := endKeep >= 0

	switch {
	case hasDel && hasIns:
		oldWSPrefix := leadingWS(changes[deletion].value)
		oldWSSuffix := trailingWS(changes[deletion].value)
		newWSPrefix := leadingWS(changes[insertion].value)
		newWSSuffix := trailingWS(changes[insertion].value)
		commonPrefixLen := longestCommonPrefixU16(oldWSPrefix, newWSPrefix)
		commonSuffixLen := longestCommonSuffixU16(oldWSSuffix, newWSSuffix)
		if hasStart && u16EndsWith(changes[startKeep].value, newWSPrefix) {
			trim := len(newWSPrefix) - commonPrefixLen
			changes[startKeep].value = changes[startKeep].value[:len(changes[startKeep].value)-trim]
			changes[deletion].value = changes[deletion].value[commonPrefixLen:]
			changes[insertion].value = changes[insertion].value[commonPrefixLen:]
		}
		if hasEnd && u16StartsWith(changes[endKeep].value, newWSSuffix) {
			trim := len(newWSSuffix) - commonSuffixLen
			changes[endKeep].value = changes[endKeep].value[trim:]
			changes[deletion].value = changes[deletion].value[:len(changes[deletion].value)-commonSuffixLen]
			changes[insertion].value = changes[insertion].value[:len(changes[insertion].value)-commonSuffixLen]
		}
	case !hasDel && hasIns:
		if hasStart {
			wsLen := len(leadingWS(changes[insertion].value))
			changes[insertion].value = changes[insertion].value[wsLen:]
		}
		if hasEnd {
			wsLen := len(leadingWS(changes[endKeep].value))
			changes[endKeep].value = changes[endKeep].value[wsLen:]
		}
	case hasDel && !hasIns:
		switch {
		case hasStart && hasEnd:
			newWSFull := leadingWS(changes[endKeep].value)
			delWSStart := leadingWS(changes[deletion].value)
			delWSEnd := trailingWS(changes[deletion].value)
			if u16EndsWith(changes[startKeep].value, newWSFull) {
				newWSStartLen := longestCommonPrefixU16(newWSFull, delWSStart)
				newWSEndLen := longestCommonSuffixU16(newWSFull[newWSStartLen:], delWSEnd)
				changes[deletion].value = changes[deletion].value[newWSStartLen : len(changes[deletion].value)-newWSEndLen]
				changes[endKeep].value = changes[endKeep].value[len(newWSFull)-newWSEndLen:]
				changes[startKeep].value = changes[startKeep].value[:len(changes[startKeep].value)-newWSEndLen]
			}
		case !hasStart && hasEnd:
			endKeepWSPrefix := leadingWS(changes[endKeep].value)
			delWSSuffix := trailingWS(changes[deletion].value)
			overlapLen := maximumOverlapU16(delWSSuffix, endKeepWSPrefix)
			changes[deletion].value = changes[deletion].value[:len(changes[deletion].value)-overlapLen]
		case hasStart && !hasEnd:
			startKeepWSSuffix := trailingWS(changes[startKeep].value)
			delWSPrefix := leadingWS(changes[deletion].value)
			overlapLen := maximumOverlapU16(startKeepWSSuffix, delWSPrefix)
			changes[deletion].value = changes[deletion].value[overlapLen:]
		}
	}
}

func wordPostProcess(changes []rawWordChange) {
	lastKeep := -1
	insertion := -1
	deletion := -1
	for i := range changes {
		if changes[i].added {
			insertion = i
		} else if changes[i].removed {
			deletion = i
		} else {
			if insertion >= 0 || deletion >= 0 {
				dedupeWhitespace(changes, lastKeep, deletion, insertion, i)
			}
			lastKeep = i
			insertion = -1
			deletion = -1
		}
	}
	if insertion >= 0 || deletion >= 0 {
		dedupeWhitespace(changes, lastKeep, deletion, insertion, -1)
	}
}

func internU16Keys(oldKeys, newKeys [][]uint16) ([]uint32, []uint32) {
	ids := make(map[string]uint32, len(oldKeys)+len(newKeys))
	assign := func(key []uint16) uint32 {
		s := string(utf16.Decode(key))
		if id, ok := ids[s]; ok {
			return id
		}
		id := uint32(len(ids))
		ids[s] = id
		return id
	}
	oldIDs := make([]uint32, len(oldKeys))
	for i, k := range oldKeys {
		oldIDs[i] = assign(k)
	}
	newIDs := make([]uint32, len(newKeys))
	for i, k := range newKeys {
		newIDs[i] = assign(k)
	}
	return oldIDs, newIDs
}

// diffWords computes a word-level diff matching omp crates/diff/src/lib.rs
// diff_words_u16 (jsdiff diffWords semantics).
func diffWords(oldText, newText string) []wordChange {
	oldU16 := utf16.Encode([]rune(oldText))
	newU16 := utf16.Encode([]rune(newText))
	oldRefs := wordTokens(oldU16)
	newRefs := wordTokens(newU16)

	oldKeys := make([][]uint16, len(oldRefs))
	for i, t := range oldRefs {
		oldKeys[i] = jsTrim(t)
	}
	newKeys := make([][]uint16, len(newRefs))
	for i, t := range newRefs {
		newKeys[i] = jsTrim(t)
	}

	oldIDs, newIDs := internU16Keys(oldKeys, newKeys)
	runs := myersDiff(oldIDs, newIDs)

	raw := make([]rawWordChange, 0, len(runs))
	oldPos, newPos := 0, 0
	for _, r := range runs {
		var val []uint16
		if r.removed {
			val = wordJoin(oldRefs[oldPos : oldPos+r.count])
			oldPos += r.count
		} else {
			val = wordJoin(newRefs[newPos : newPos+r.count])
			newPos += r.count
			if !r.added {
				oldPos += r.count
			}
		}
		raw = append(raw, rawWordChange{value: val, added: r.added, removed: r.removed})
	}

	wordPostProcess(raw)

	out := make([]wordChange, len(raw))
	for i, c := range raw {
		out[i] = wordChange{
			value:   string(utf16.Decode(c.value)),
			added:   c.added,
			removed: c.removed,
		}
	}
	return out
}

// renderIntraLineDiff computes word-level diff and wraps changed spans in SGR
// inverse (\x1b[7m...\x1b[27m), stripping leading whitespace from the first
// removed/added inverse span to avoid highlighting indentation.
// upstream: packages/tui/src/chrome/diff.ts renderIntraLineDiff
func (p palette) renderIntraLineDiff(oldContent, newContent string) (removedLine, addedLine string) {
	parts := diffWords(oldContent, newContent)
	var remBuf, addBuf strings.Builder
	isFirstRemoved := true
	isFirstAdded := true

	splitLeadingWS := func(s string) (ws, rest string) {
		for idx, r := range s {
			if !isJSWhitespace(uint32(r)) {
				return s[:idx], s[idx:]
			}
		}
		return s, ""
	}

	for _, part := range parts {
		if part.removed {
			val := part.value
			if isFirstRemoved {
				ws, rest := splitLeadingWS(val)
				remBuf.WriteString(ws)
				val = rest
				isFirstRemoved = false
			}
			if val != "" {
				remBuf.WriteString(p.inverse(val))
			}
		} else if part.added {
			val := part.value
			if isFirstAdded {
				ws, rest := splitLeadingWS(val)
				addBuf.WriteString(ws)
				val = rest
				isFirstAdded = false
			}
			if val != "" {
				addBuf.WriteString(p.inverse(val))
			}
		} else {
			remBuf.WriteString(part.value)
			addBuf.WriteString(part.value)
		}
	}
	return remBuf.String(), addBuf.String()
}
