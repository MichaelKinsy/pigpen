package hashline

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// SeenLineRevealCap is the maximum number of unseen lines revealed inline in a seen-line error.
// upstream: crates/pi-edit/src/modes/hashline/patcher.rs
const SeenLineRevealCap = 20

// CheckSeenLines enforces that every concrete anchor line in ops was previously displayed
// for the cited (absPath, tag) snapshot. When unseen lines are found, it reveals up to
// SeenLineRevealCap lines inline, records them as seen so a straight retry succeeds without
// a re-read, and returns omp's exact diagnostic error.
// upstream: crates/pi-edit/src/modes/hashline/patcher.rs:349, messages.rs:820
func CheckSeenLines(store *Store, absPath, displayPath, tag, normalizedText string, ops []Op) error {
	if store == nil {
		return nil
	}
	snap, ok := store.ByHash(absPath, tag)
	if !ok || len(snap.SeenLines) == 0 {
		return nil
	}

	anchors := CollectAnchorLines(ops)
	var unseen []int
	for _, ln := range anchors {
		if !snap.SeenLines[ln] {
			unseen = append(unseen, ln)
		}
	}
	if len(unseen) == 0 {
		return nil
	}

	sourceLines := SplitAddressableFileLines(normalizedText)
	type revealedLine struct {
		line int
		text string
	}
	var revealed []revealedLine
	for i, ln := range unseen {
		if i >= SeenLineRevealCap {
			break
		}
		if ln >= 1 && ln <= len(sourceLines) {
			revealed = append(revealed, revealedLine{line: ln, text: sourceLines[ln-1]})
		}
	}

	rangesStr := formatLineRanges(unseen)
	selector := strings.ReplaceAll(rangesStr, ", ", ",")
	header := fmt.Sprintf(
		"This edit anchors to lines %s of %s that %s never displayed (it showed a partial range, a search hit, or a folded summary).",
		rangesStr, displayPath, FormatHeader(displayPath, tag),
	)

	if len(revealed) == 0 {
		return fmt.Errorf(
			"%s Re-read them in full first with a ranged read like `%s:%s` — it skips summarization and mints a fresh tag (a plain re-read just re-folds them) — then re-issue the edit.",
			header, displayPath, selector,
		)
	}

	previewRows := make([]string, len(revealed))
	revealedNums := make([]int, len(revealed))
	for i, r := range revealed {
		previewRows[i] = "  " + FormatNumberedLine(r.line, r.text)
		revealedNums[i] = r.line
	}
	preview := strings.Join(previewRows, "\n")

	truncated := len(unseen) > len(revealed)
	if truncated {
		return fmt.Errorf(
			"%s Preview of the actual file content at the first %d unseen line(s):\n%s\nThe range exceeds the inline preview cap — re-read the remainder with `%s:%s` before re-issuing the edit.",
			header, len(revealed), preview, displayPath, selector,
		)
	}

	store.RecordSeenLines(absPath, tag, revealedNums)
	return fmt.Errorf(
		"%s Actual file content at those lines:\n%s\nVerify the content matches what you intend to touch, then re-issue the edit with the same [path#tag] header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers.",
		header, preview,
	)
}

// SeenLinesFromBody parses 1-indexed line numbers from a hashline-formatted body
// (rows like `12:text` or ` 12:text` or `*12:text`).
// upstream: crates/pi-edit/src/store.rs:106
func SeenLinesFromBody(body string) []int {
	var seen []int
	for _, row := range strings.Split(body, "\n") {
		s := row
		if strings.HasPrefix(s, " ") || strings.HasPrefix(s, "*") {
			s = s[1:]
		}
		colon := strings.IndexByte(s, ':')
		if colon <= 0 {
			continue
		}
		prefix := s[:colon]
		if dash := strings.IndexByte(prefix, '-'); dash > 0 {
			if a, err := strconv.Atoi(prefix[:dash]); err == nil && a > 0 {
				seen = append(seen, a)
			}
			if b, err := strconv.Atoi(prefix[dash+1:]); err == nil && b > 0 {
				seen = append(seen, b)
			}
			continue
		}
		if n, err := strconv.Atoi(prefix); err == nil && n > 0 {
			seen = append(seen, n)
		}
	}
	return seen
}

func formatLineRanges(lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	sorted := slices.Clone(lines)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	var parts []string
	start := sorted[0]
	end := sorted[0]
	for _, n := range sorted[1:] {
		if n == end+1 {
			end = n
			continue
		}
		parts = append(parts, formatOneRange(start, end))
		start, end = n, n
	}
	parts = append(parts, formatOneRange(start, end))
	return strings.Join(parts, ", ")
}

func formatOneRange(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}
