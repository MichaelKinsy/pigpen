package filetools

import (
	"fmt"
	"strings"
)

type span struct {
	start int
	end   int
}

type fuzzyStrategy struct {
	name string
	find func(content, pattern string) []span
}

var unicodeReplacer = strings.NewReplacer(
	"\u201c", "\"", "\u201d", "\"",
	"\u2018", "'", "\u2019", "'",
	"\u2014", "--", "\u2013", "-",
	"\u2026", "...", "\u00a0", " ",
	"\u2212", "-",
	"\u2000", " ", "\u2001", " ", "\u2002", " ", "\u2003", " ",
	"\u2004", " ", "\u2005", " ", "\u2006", " ", "\u2007", " ",
	"\u2008", " ", "\u2009", " ", "\u200a", " ", "\u202f", " ",
	"\u205f", " ", "\u3000", " ",
)

var fuzzyStrategies = []fuzzyStrategy{
	{"exact", strategyExact},
	{"line_trimmed", strategyLineTrimmed},
	{"whitespace_normalized", strategyWhitespaceNormalized},
	{"indentation_flexible", strategyIndentationFlexible},
	{"escape_normalized", strategyEscapeNormalized},
	{"trimmed_boundary", strategyTrimmedBoundary},
	{"unicode_normalized", strategyUnicodeNormalized},
}

// fuzzyFindAndReplace locates oldStr in content using Hermes's multi-strategy chain
// and replaces it with newStr.
func fuzzyFindAndReplace(content, oldStr, newStr string, replaceAll bool) (string, int, string, error) {
	if oldStr == "" {
		return "", 0, "", fmt.Errorf("old_string must not be empty")
	}
	if oldStr == newStr {
		return "", 0, "", fmt.Errorf("no edit was applied because old_string and new_string are identical")
	}

	var matched []span
	var usedStrategy string
	for _, st := range fuzzyStrategies {
		spans := st.find(content, oldStr)
		if len(spans) > 0 {
			matched = spans
			usedStrategy = st.name
			break
		}
	}

	if len(matched) == 0 {
		if isAlreadyApplied(content, oldStr, newStr) {
			return content, 0, "already_applied", nil
		}
		return "", 0, "", fmt.Errorf("could not find old_string in file")
	}

	if len(matched) > 1 && !replaceAll {
		return "", 0, "", fmt.Errorf(
			"old_string matched %d locations (strategy: %s); provide more surrounding context or set replace_all=true:\n%s",
			len(matched), usedStrategy, formatMatchLocations(content, matched, 5),
		)
	}

	// Apply replacements in reverse order so earlier offsets stay valid.
	out := content
	for i := len(matched) - 1; i >= 0; i-- {
		sp := matched[i]
		repl := adjustReplacement(content[sp.start:sp.end], oldStr, newStr, usedStrategy)
		out = out[:sp.start] + repl + out[sp.end:]
	}
	return out, len(matched), usedStrategy, nil
}

func isAlreadyApplied(content, oldStr, newStr string) bool {
	if len(strings.TrimSpace(newStr)) < 8 {
		return false
	}
	if !strings.Contains(content, newStr) {
		return false
	}
	return oldStr == newStr || !strings.Contains(content, oldStr)
}

// adjustReplacement preserves base indentation when indentation_flexible matched.
func adjustReplacement(matchedText, oldStr, newStr, strategy string) string {
	if strategy != "indentation_flexible" {
		return newStr
	}
	matchedLines := strings.Split(matchedText, "\n")
	oldLines := strings.Split(oldStr, "\n")
	if len(matchedLines) == 0 || len(oldLines) == 0 {
		return newStr
	}
	matchedIndent := leadingWhitespace(firstNonEmpty(matchedLines))
	oldIndent := leadingWhitespace(firstNonEmpty(oldLines))
	if matchedIndent == oldIndent {
		return newStr
	}
	newLines := strings.Split(newStr, "\n")
	for i, l := range newLines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, oldIndent) {
			newLines[i] = matchedIndent + strings.TrimPrefix(l, oldIndent)
		}
	}
	return strings.Join(newLines, "\n")
}

func firstNonEmpty(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	if len(lines) > 0 {
		return lines[0]
	}
	return ""
}

func leadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

func strategyExact(content, pattern string) []span {
	var spans []span
	offset := 0
	for {
		idx := strings.Index(content[offset:], pattern)
		if idx < 0 {
			break
		}
		start := offset + idx
		end := start + len(pattern)
		spans = append(spans, span{start: start, end: end})
		offset = end
	}
	return spans
}

func strategyLineTrimmed(content, pattern string) []span {
	return matchTransformedLines(content, pattern, func(lines []string) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = strings.TrimSpace(l)
		}
		return out
	})
}

func strategyIndentationFlexible(content, pattern string) []span {
	return matchTransformedLines(content, pattern, func(lines []string) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = strings.TrimLeft(l, " \t")
		}
		return out
	})
}

func strategyTrimmedBoundary(content, pattern string) []span {
	return matchTransformedLines(content, pattern, func(lines []string) []string {
		out := make([]string, len(lines))
		copy(out, lines)
		if len(out) > 0 {
			out[0] = strings.TrimSpace(out[0])
		}
		if len(out) > 1 {
			out[len(out)-1] = strings.TrimSpace(out[len(out)-1])
		}
		return out
	})
}

func strategyEscapeNormalized(content, pattern string) []span {
	unescaped := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(pattern)
	if unescaped == pattern {
		return nil
	}
	return strategyExact(content, unescaped)
}

func strategyWhitespaceNormalized(content, pattern string) []span {
	normContent, origToNorm := collapseHorizontalWhitespace(content)
	normPattern, _ := collapseHorizontalWhitespace(pattern)
	if normPattern == "" {
		return nil
	}
	normSpans := strategyExact(normContent, normPattern)
	if len(normSpans) == 0 {
		return nil
	}
	return mapNormSpansToOrig(content, normContent, origToNorm, normSpans)
}

func collapseHorizontalWhitespace(s string) (string, []int) {
	var sb strings.Builder
	sb.Grow(len(s))
	origToNorm := make([]int, len(s)+1)
	inWS := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			if !inWS {
				origToNorm[i] = sb.Len()
				sb.WriteByte(' ')
				inWS = true
			} else {
				origToNorm[i] = sb.Len() - 1
			}
		} else {
			inWS = false
			origToNorm[i] = sb.Len()
			sb.WriteByte(c)
		}
	}
	origToNorm[len(s)] = sb.Len()
	return sb.String(), origToNorm
}

func mapNormSpansToOrig(orig, norm string, origToNorm []int, normSpans []span) []span {
	normToOrigStart := make(map[int]int, len(norm)+1)
	normToOrigEnd := make(map[int]int, len(norm)+1)
	for i := 0; i < len(orig); i++ {
		np := origToNorm[i]
		if _, exists := normToOrigStart[np]; !exists {
			normToOrigStart[np] = i
		}
		normToOrigEnd[np] = i
	}
	out := make([]span, 0, len(normSpans))
	for _, ns := range normSpans {
		origStart, ok := normToOrigStart[ns.start]
		if !ok {
			continue
		}
		var origEnd int
		if ns.end > 0 {
			if oe, ok := normToOrigEnd[ns.end-1]; ok {
				origEnd = oe + 1
			} else {
				origEnd = origStart + (ns.end - ns.start)
			}
		} else {
			origEnd = origStart
		}
		if origEnd > len(orig) {
			origEnd = len(orig)
		}
		out = append(out, span{start: origStart, end: origEnd})
	}
	return out
}

func strategyUnicodeNormalized(content, pattern string) []span {
	normContent := unicodeReplacer.Replace(content)
	normPattern := unicodeReplacer.Replace(pattern)
	if normContent == content && normPattern == pattern {
		return nil
	}
	normSpans := strategyExact(normContent, normPattern)
	if len(normSpans) == 0 {
		normSpans = strategyLineTrimmed(normContent, normPattern)
	}
	if len(normSpans) == 0 {
		return nil
	}
	// Build byte-level orig -> norm index map.
	origToNorm := make([]int, len(content)+1)
	normPos := 0
	for i, r := range content {
		rs := string(r)
		repl := unicodeReplacer.Replace(rs)
		for b := 0; b < len(rs); b++ {
			origToNorm[i+b] = normPos
		}
		normPos += len(repl)
	}
	origToNorm[len(content)] = normPos
	return mapNormSpansToOrig(content, normContent, origToNorm, normSpans)
}

func matchTransformedLines(content, pattern string, transform func([]string) []string) []span {
	contentLines := strings.Split(content, "\n")
	patternNorm := transform(strings.Split(pattern, "\n"))
	n := len(patternNorm)
	if n == 0 || n > len(contentLines) {
		return nil
	}
	// Precompute line start byte offsets.
	lineStarts := make([]int, len(contentLines)+1)
	pos := 0
	for i, l := range contentLines {
		lineStarts[i] = pos
		pos += len(l) + 1
	}
	lineStarts[len(contentLines)] = len(content) + 1

	var spans []span
	for i := 0; i <= len(contentLines)-n; {
		window := transform(contentLines[i : i+n])
		if slicesEqual(window, patternNorm) {
			start := lineStarts[i]
			end := lineStarts[i+n] - 1
			if end > len(content) {
				end = len(content)
			}
			spans = append(spans, span{start: start, end: end})
			i += n // non-overlapping
		} else {
			i++
		}
	}
	return spans
}

func slicesEqual(a, b []string) bool {
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

func formatMatchLocations(content string, matches []span, capCount int) string {
	var rows []string
	limit := len(matches)
	if limit > capCount {
		limit = capCount
	}
	for _, sp := range matches[:limit] {
		lineNo := strings.Count(content[:sp.start], "\n") + 1
		lineStart := strings.LastIndex(content[:sp.start], "\n") + 1
		lineEnd := strings.Index(content[lineStart:], "\n")
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
		}
		snippet := strings.TrimSpace(content[lineStart:lineEnd])
		if len(snippet) > 80 {
			snippet = snippet[:77] + "..."
		}
		rows = append(rows, fmt.Sprintf("  L%d: %s", lineNo, snippet))
	}
	if len(matches) > capCount {
		rows = append(rows, fmt.Sprintf("  ... and %d more", len(matches)-capCount))
	}
	return strings.Join(rows, "\n")
}

// generateUnifiedDiff produces a compact unified diff between oldContent and newContent.
func generateUnifiedDiff(path, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	oldLines := splitLinesForDiff(oldContent)
	newLines := splitLinesForDiff(newContent)

	ops := diffLines(oldLines, newLines)
	if len(ops) == 0 {
		return ""
	}

	const ctxLines = 3
	type hunkRange struct{ start, end int }
	var hunks []hunkRange
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		s := i - ctxLines
		if s < 0 {
			s = 0
		}
		e := i + ctxLines + 1
		if e > len(ops) {
			e = len(ops)
		}
		if len(hunks) > 0 && s <= hunks[len(hunks)-1].end {
			hunks[len(hunks)-1].end = e
		} else {
			hunks = append(hunks, hunkRange{start: s, end: e})
		}
	}
	if len(hunks) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", path, path)
	for _, h := range hunks {
		oldStart, newStart := 1, 1
		for i := 0; i < h.start; i++ {
			switch ops[i].kind {
			case ' ':
				oldStart++
				newStart++
			case '-':
				oldStart++
			case '+':
				newStart++
			}
		}
		oldCount, newCount := 0, 0
		for i := h.start; i < h.end; i++ {
			switch ops[i].kind {
			case ' ':
				oldCount++
				newCount++
			case '-':
				oldCount++
			case '+':
				newCount++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for i := h.start; i < h.end; i++ {
			sb.WriteByte(ops[i].kind)
			sb.WriteString(ops[i].text)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

type diffOp struct {
	kind byte // ' ', '-', '+'
	text string
}

func splitLinesForDiff(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffLines computes an LCS-based line diff; trims common prefix/suffix first to stay fast on large files.
func diffLines(a, b []string) []diffOp {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	var ops []diffOp
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{kind: ' ', text: a[i]})
	}

	midA := a[prefix : len(a)-suffix]
	midB := b[prefix : len(b)-suffix]

	// Guard against quadratic memory on huge diffs: fall back to delete-all/add-all if product is large.
	if len(midA)*len(midB) > 2_000_000 {
		for _, l := range midA {
			ops = append(ops, diffOp{kind: '-', text: l})
		}
		for _, l := range midB {
			ops = append(ops, diffOp{kind: '+', text: l})
		}
	} else {
		m, n := len(midA), len(midB)
		dp := make([][]int, m+1)
		for i := range dp {
			dp[i] = make([]int, n+1)
		}
		for i := m - 1; i >= 0; i-- {
			for j := n - 1; j >= 0; j-- {
				if midA[i] == midB[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else if dp[i+1][j] >= dp[i][j+1] {
					dp[i][j] = dp[i+1][j]
				} else {
					dp[i][j] = dp[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < m && j < n {
			if midA[i] == midB[j] {
				ops = append(ops, diffOp{kind: ' ', text: midA[i]})
				i++
				j++
			} else if dp[i+1][j] >= dp[i][j+1] {
				ops = append(ops, diffOp{kind: '-', text: midA[i]})
				i++
			} else {
				ops = append(ops, diffOp{kind: '+', text: midB[j]})
				j++
			}
		}
		for ; i < m; i++ {
			ops = append(ops, diffOp{kind: '-', text: midA[i]})
		}
		for ; j < n; j++ {
			ops = append(ops, diffOp{kind: '+', text: midB[j]})
		}
	}

	for i := len(a) - suffix; i < len(a); i++ {
		ops = append(ops, diffOp{kind: ' ', text: a[i]})
	}
	return ops
}
