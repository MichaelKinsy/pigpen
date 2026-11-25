package hashline

import (
	"regexp"
	"strconv"
	"strings"
)

// Normalization of line prefixes copied from read/search output.
// upstream: crates/edit/src/modes/hashline/prefixes.rs

var (
	hlPrefixRe          = regexp.MustCompile(`^\s*(?:(?:>>>|>>)\s*)?(?:[+*-]\s*)?(\d+)[:|]`)
	hlPrefixPlusRe      = regexp.MustCompile(`^\s*(?:(?:>>>|>>)\s*)?\+\s*\d+:`)
	hlHeaderRe          = regexp.MustCompile(`^\s*\[[^#\r\n]+#[0-9a-fA-F]{4}\]\s*$`)
	readRangeElisionRe  = regexp.MustCompile(`^\s*[1-9]\d*\s*-\s*[1-9]\d*:.*(?:…|\.\.\.).*$`)
	readSingleElisionRe = regexp.MustCompile(`^\s*(?:…|\.\.\.)\s*$`)
)

// IsReadTruncationNotice reports whether a row is a truncation notice emitted by read.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:21
func IsReadTruncationNotice(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return false
	}
	body := trimmed[1 : len(trimmed)-1]
	showingNotice := strings.HasPrefix(body, "Showing ") &&
		(strings.Contains(body, " line") || strings.Contains(body, "lines ") || strings.Contains(body, "bytes ")) &&
		(strings.Contains(body, " of ") || strings.Contains(body, " elided"))
	morePrefix := strings.HasPrefix(body, "More lines in ")
	if !morePrefix {
		if countStr, _, ok := strings.Cut(body, " more line"); ok {
			if _, err := strconv.ParseUint(countStr, 10, 64); err == nil {
				morePrefix = true
			}
		}
	}
	moreNotice := morePrefix &&
		strings.Contains(body, " in ") &&
		strings.Contains(body, ". Use ") &&
		strings.HasSuffix(body, " to continue")
	elidedNotice := (strings.HasPrefix(body, "…") || strings.HasPrefix(body, "...")) &&
		strings.Contains(body, "ln elided;") &&
		strings.Contains(body, "re-read needed ranges")
	oversizedLineNotice := strings.HasPrefix(body, "Line ") &&
		strings.Contains(body, " exceeds ") &&
		strings.Contains(body, " limit.")
	return showingNotice || moreNotice || elidedNotice || oversizedLineNotice
}

// IsReadMetadataLine reports whether a row is display-only metadata emitted by read.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:45
func IsReadMetadataLine(line string) bool {
	return IsReadTruncationNotice(line) ||
		readRangeElisionRe.MatchString(line) ||
		readSingleElisionRe.MatchString(line)
}

func stripLeadingHashlinePrefixes(line string) string {
	result := line
	for {
		next := hlPrefixRe.ReplaceAllString(result, "")
		if next == result {
			return result
		}
		result = next
	}
}

// StripOneLeadingHashlinePrefix strips at most one leading read/search line-number prefix.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:63
func StripOneLeadingHashlinePrefix(line string) string {
	loc := hlPrefixRe.FindStringIndex(line)
	if loc == nil {
		return line
	}
	return line[loc[1]:]
}

type linePrefixStats struct {
	nonEmpty                int
	headerCount             int
	hashPrefixCount         int
	diffPlusHashPrefixCount int
	diffPlusCount           int
}

func collectLinePrefixStats(lines []string) linePrefixStats {
	var stats linePrefixStats
	for _, line := range lines {
		if line == "" || IsReadMetadataLine(line) {
			continue
		}
		stats.nonEmpty++
		if hlHeaderRe.MatchString(line) {
			stats.headerCount++
			continue
		}
		if hlPrefixRe.MatchString(line) {
			stats.hashPrefixCount++
		}
		if hlPrefixPlusRe.MatchString(line) {
			stats.diffPlusHashPrefixCount++
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "++") {
			stats.diffPlusCount++
		}
	}
	return stats
}

// StripNewLinePrefixes opportunistically strips a consistent numbered or diff prefix scheme.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:101
func StripNewLinePrefixes(lines []string) []string {
	stats := collectLinePrefixStats(lines)
	if stats.nonEmpty == 0 {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	contentLineCount := stats.nonEmpty - stats.headerCount
	stripHash := contentLineCount > 0 && stats.hashPrefixCount == contentLineCount
	stripPlus := !stripHash &&
		stats.diffPlusHashPrefixCount == 0 &&
		stats.diffPlusCount > 0 &&
		float64(stats.diffPlusCount) >= float64(stats.nonEmpty)*0.5
	if !stripHash && !stripPlus && stats.diffPlusHashPrefixCount == 0 {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if IsReadMetadataLine(line) || (stripHash && hlHeaderRe.MatchString(line)) {
			continue
		}
		if stripHash {
			out = append(out, stripLeadingHashlinePrefixes(line))
			continue
		}
		if stripPlus && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "++") {
			out = append(out, line[1:])
			continue
		}
		if stats.diffPlusHashPrefixCount > 0 && hlPrefixPlusRe.MatchString(line) {
			out = append(out, StripOneLeadingHashlinePrefix(line))
			continue
		}
		out = append(out, line)
	}
	return out
}

// numbersCountUpByOne checks whether each numbered row increments by one across contiguous runs.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:140
func numbersCountUpByOne(lines []string) bool {
	var prev uint64
	hasPrev := false
	for _, line := range lines {
		if line == "" || IsReadMetadataLine(line) || hlHeaderRe.MatchString(line) {
			hasPrev = false
			continue
		}
		m := hlPrefixRe.FindStringSubmatch(line)
		if m == nil {
			return false
		}
		num, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return false
		}
		if hasPrev && (prev == ^uint64(0) || prev+1 != num) {
			return false
		}
		prev = num
		hasPrev = true
	}
	return true
}

// StripHashlinePrefixes strips numbered prefixes only when every content row carries one
// and the numbers count up by one.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:163
func StripHashlinePrefixes(lines []string) []string {
	stats := collectLinePrefixStats(lines)
	if stats.nonEmpty == 0 {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	contentLineCount := stats.nonEmpty - stats.headerCount
	if contentLineCount == 0 || stats.hashPrefixCount != contentLineCount || !numbersCountUpByOne(lines) {
		out := make([]string, len(lines))
		copy(out, lines)
		return out
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if IsReadMetadataLine(line) || hlHeaderRe.MatchString(line) {
			continue
		}
		out = append(out, stripLeadingHashlinePrefixes(line))
	}
	return out
}

// HashlineParseText normalizes a multiline text payload into unprefixed rows.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:183
func HashlineParseText(edit string) []string {
	trimmed := strings.TrimSuffix(edit, "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "")
	return StripNewLinePrefixes(strings.Split(trimmed, "\n"))
}
