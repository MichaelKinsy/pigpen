package bashparse

import (
	"sort"
	"unicode/utf16"
)

// HeredocFreeLinesWithin is the heredoc-free spelling of each line whose heredoc sits in a
// statement the primary parse could not resolve, in source order (heredoc-free-lines.ts).
//
// tree-sitter-bash 0.25.1's heredoc_redirect accepts one tail form after the delimiter, so valid
// bash such as `cat <<EOF ; rm -rf x` or `cat <<EOF arg > /tmp/o` yields an ERROR directly under
// the redirect. The innermost unresolved node is then the redirect itself, whose own text fails to
// re-parse too, so the region salvage recovers nothing and a deny on the command after the heredoc
// never fires. The same line with its heredoc operators cut out (`cat ; rm -rf x`,
// `cat arg > /tmp/o`) is what bash runs on that line, and it parses.
//
// A heredoc's host is its nearest redirected_statement, or, when no statement holds it, the
// nearest enclosing ERROR (`cat 0<<EOF | ...` lexes `0<<EOF` as one delimiter inside a top-level
// ERROR). Only a host the parse could not resolve is spelled, so a clean heredoc beside an
// unrelated failure yields nothing. The spelling runs from the host statement's start, or the
// heredoc's line start for an ERROR host, to the end of the heredoc's line.
//
// Each cut removes an operator the scanner tokenized: the `<<` / `<<-` token, a file_descriptor
// before it, the delimiter, and the blanks before them. It only removes text; the caller admits a
// spelling only when it re-parses cleanly (unresolved-salvage.ts).
func HeredocFreeLinesWithin(root *Node) []string {
	source := utf16.Encode([]rune(root.Text()))
	offset := root.StartIndex()
	var order []int
	spans := map[int]*heredocLine{}
	for _, heredoc := range unresolvedHeredocsWithin(root) {
		var start int
		if heredoc.host.typ == "redirected_statement" {
			start = heredoc.host.StartIndex()
		} else {
			start = lineStartOf(source, offset, heredoc.delimiter.StartIndex())
		}
		end := lineEndOf(source, offset, heredoc.delimiter.EndIndex())
		line, ok := spans[start]
		if !ok {
			line = &heredocLine{start: start, end: end}
			spans[start] = line
			order = append(order, start)
		}
		line.cuts = append(line.cuts, operatorSpanOf(heredoc, source, offset, start))
	}
	lines := []string{}
	for _, start := range order {
		lines = append(lines, spell(spans[start], source, offset))
	}
	return lines
}

type unresolvedHeredoc struct {
	// delimiter is the heredoc_start token.
	delimiter *Node
	// siblings are the delimiter's siblings, in order, so its operator can be found.
	siblings []*Node
	index    int
	host     *Node
}

type heredocLine struct {
	start, end int
	cuts       [][2]int
}

func unresolvedHeredocsWithin(root *Node) []unresolvedHeredoc {
	var found []unresolvedHeredoc
	collectUnresolvedHeredocs(root, nil, nil, &found)
	return found
}

// collectUnresolvedHeredocs descends every node, ERRORs included: the heredoc_start token is the
// scanner's rather than recovery's invention, and it can sit inside one.
func collectUnresolvedHeredocs(node, statement, errNode *Node, found *[]unresolvedHeredoc) {
	siblings := childrenOf(node)
	for index, child := range siblings {
		host := statement
		if host == nil {
			host = errNode
		}
		if child.typ == "heredoc_start" && host != nil && ParseUnresolvedWithin(host) {
			*found = append(*found, unresolvedHeredoc{child, siblings, index, host})
		}
		nextStatement, nextError := statement, errNode
		if child.typ == "redirected_statement" {
			nextStatement = child
		}
		if child.typ == "ERROR" {
			nextError = child
		}
		collectUnresolvedHeredocs(child, nextStatement, nextError, found)
	}
}

// operatorSpanOf is the span of a heredoc's operator: its delimiter, the `<<` / `<<-` token and a
// file_descriptor before it, and the blanks before those, but never reaching before the line's own
// start.
func operatorSpanOf(h unresolvedHeredoc, source []uint16, offset, lineStart int) [2]int {
	start := h.delimiter.StartIndex()
	before := h.index - 1
	operator := at(h.siblings, before)
	if before >= 0 && operator != nil && !operator.named && startsWithUnits(operator.Text(), "<<") {
		start = operator.StartIndex()
		before--
		descriptor := at(h.siblings, before)
		if before >= 0 && descriptor != nil && descriptor.typ == "file_descriptor" {
			start = descriptor.StartIndex()
		}
	}
	for start > lineStart && isBlank(source, start-1-offset) {
		start--
	}
	return [2]int{start, h.delimiter.EndIndex()}
}

func startsWithUnits(text, prefix string) bool {
	return len(text) >= len(prefix) && text[:len(prefix)] == prefix
}

func spell(line *heredocLine, source []uint16, offset int) string {
	cuts := append([][2]int(nil), line.cuts...)
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i][0] < cuts[j][0] })
	text := ""
	at := line.start
	for _, c := range cuts {
		text += unitsSlice(source, at-offset, c[0]-offset)
		at = c[1]
	}
	return text + unitsSlice(source, at-offset, line.end-offset)
}

// unitsSlice is String.prototype.slice over UTF-16 units for in-range, non-negative indices.
func unitsSlice(source []uint16, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	if start >= end {
		return ""
	}
	return string(utf16.Decode(source[start:end]))
}

// lineStartOf is source.lastIndexOf("\n", index - offset - 1) + 1 + offset.
func lineStartOf(source []uint16, offset, index int) int {
	from := index - offset - 1
	if from < 0 {
		from = 0
	}
	if from > len(source)-1 {
		from = len(source) - 1
	}
	for i := from; i >= 0; i-- {
		if source[i] == '\n' {
			return i + 1 + offset
		}
	}
	return 0 + 1 - 1 + offset
}

// lineEndOf is the index of the next "\n" at or after index - offset, or the source's end, plus
// offset.
func lineEndOf(source []uint16, offset, index int) int {
	from := index - offset
	if from < 0 {
		from = 0
	}
	for i := from; i < len(source); i++ {
		if source[i] == '\n' {
			return i + offset
		}
	}
	return len(source) + offset
}

func isBlank(source []uint16, i int) bool {
	if i < 0 || i >= len(source) {
		return false
	}
	return source[i] == ' ' || source[i] == '\t'
}
