package files

import (
	"fmt"
	"io"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

const (
	// conflictScanMaxBytes matches omp's SCAN_FILE_DEFAULT_MAX_BYTES (10 MB).
	// upstream: packages/coding-agent/src/tools/conflict-detect.ts:126
	conflictScanMaxBytes = 10 * 1024 * 1024
	// conflictPreviewSideLines matches omp's PREVIEW_SIDE_LINES (6).
	// upstream: packages/coding-agent/src/tools/conflict-detect.ts:757
	conflictPreviewSideLines = 6
)

// conflictFileScan is the result of scanning a file on disk for conflict blocks.
// upstream: packages/coding-agent/src/tools/conflict-detect.ts:129-133
type conflictFileScan struct {
	blocks          []urls.ConflictBlock
	truncatedAtLine int // 0 when the whole file was scanned
}

// scanFileForConflicts scans up to conflictScanMaxBytes of absPath for completed
// git merge-conflict blocks.
// upstream: packages/coding-agent/src/tools/conflict-detect.ts:143-158
func scanFileForConflicts(absPath string) (conflictFileScan, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return conflictFileScan{}, err
	}
	truncated := info.Size() > conflictScanMaxBytes
	f, err := os.Open(absPath)
	if err != nil {
		return conflictFileScan{}, err
	}
	defer f.Close()

	var data []byte
	if truncated {
		data, err = io.ReadAll(io.LimitReader(f, conflictScanMaxBytes))
	} else {
		data, err = io.ReadAll(f)
	}
	if err != nil {
		return conflictFileScan{}, err
	}
	text := string(data)
	// Drop leading UTF-8 BOM to match TextDecoder("utf-8") in upstream.
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	blocks := urls.ScanConflictLines(lines, 1)
	if truncated {
		return conflictFileScan{blocks: blocks, truncatedAtLine: len(lines)}, nil
	}
	return conflictFileScan{blocks: blocks}, nil
}

// formatConflictScanCapNote formats the byte-cap note when conflict scanning stops early.
// upstream: packages/coding-agent/src/tools/conflict-detect.ts:164-166
func formatConflictScanCapNote(displayPath string, line int) string {
	return fmt.Sprintf("- note: the conflict scan stopped at the byte cap at line %d; blocks from there on are not counted or registered here — range-read `%s:%d-` to show and register them.", line, displayPath, line)
}

// registerAndFormatReadConflicts scans collected lines (starting at 1-indexed startLineDisplay)
// for completed git merge-conflict blocks, registers each block in the session's ConflictHistory,
// and returns (warningFooter, count). Blocks containing any line in cutSet are excluded because
// their lines were column-truncated and would register a cut side.
// upstream: packages/coding-agent/src/tools/read.ts:3378-3419
func registerAndFormatReadConflicts(
	ctx sdk.Context,
	cwd, absPath string,
	collected []string,
	startLineDisplay int,
	cutSet map[int]bool,
) (string, int) {
	if len(collected) == 0 {
		return "", 0
	}
	rawBlocks := urls.ScanConflictLines(collected, startLineDisplay)
	if len(rawBlocks) == 0 {
		return "", 0
	}
	blocks := rawBlocks[:0]
	for _, b := range rawBlocks {
		if blockHasCutLine(b, cutSet) {
			continue
		}
		blocks = append(blocks, b)
	}
	if len(blocks) == 0 {
		return "", 0
	}

	history := urls.SessionConflictHistory(urls.Context{SDK: ctx, Cwd: cwd})
	displayPath := formatPathRelativeToCwd(absPath, cwd)
	entries := make([]urls.ConflictEntry, 0, len(blocks))
	for _, b := range blocks {
		entries = append(entries, history.Register(absPath, displayPath, b))
	}

	totalInFile := len(entries)
	truncatedAtLine := 0
	if fileScan, err := scanFileForConflicts(absPath); err == nil {
		if len(fileScan.blocks) > totalInFile {
			totalInFile = len(fileScan.blocks)
		}
		truncatedAtLine = fileScan.truncatedAtLine
	}

	warning := formatConflictWarning(entries, totalInFile, displayPath, truncatedAtLine)
	return warning, len(entries)
}

func blockHasCutLine(b urls.ConflictBlock, cutSet map[int]bool) bool {
	if len(cutSet) == 0 {
		return false
	}
	for ln := range cutSet {
		if b.StartLine <= ln && ln <= b.EndLine {
			return true
		}
	}
	return false
}

// readFileConflicts implements the `<path>:conflicts` read selector: scans the file,
// reconciles and registers every conflict block in the session's ConflictHistory, and
// returns omp's compact `#N  L_a-L_b` index.
// upstream: packages/coding-agent/src/tools/read.ts:3506-3539
func readFileConflicts(ctx sdk.Context, cwd, absPath string, suffixRes *SuffixResolution) (sdk.ToolResult, error) {
	scan, err := scanFileForConflicts(absPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	displayPath := formatPathRelativeToCwd(absPath, cwd)
	history := urls.SessionConflictHistory(urls.Context{SDK: ctx, Cwd: cwd})
	history.ReconcilePath(absPath, scan.blocks)
	entries := make([]urls.ConflictEntry, 0, len(scan.blocks))
	for _, b := range scan.blocks {
		entries = append(entries, history.Register(absPath, displayPath, b))
	}

	var summary string
	switch {
	case len(entries) > 0:
		summary = formatConflictSummary(entries, displayPath, scan.truncatedAtLine)
	case scan.truncatedAtLine > 0:
		summary = fmt.Sprintf("No unresolved git merge conflicts before line %d of %s.\n%s",
			scan.truncatedAtLine, displayPath, formatConflictScanCapNote(displayPath, scan.truncatedAtLine))
	default:
		summary = fmt.Sprintf("No unresolved git merge conflicts in %s.", displayPath)
	}

	details := map[string]any{
		"resolvedPath":  dirs.StandardizeMacOSPath(absPath),
		"conflictCount": len(entries),
	}
	if suffixRes != nil {
		details["suffixResolution"] = suffixRes.toMap()
	}
	setMetaSource(details, "path", absPath)
	return sdk.ToolResult{Content: summary, Details: details}, nil
}

// formatConflictWarning formats omp's diff-style conflict footer appended to `read` output.
// upstream: packages/coding-agent/src/tools/conflict-detect.ts:794-867
func formatConflictWarning(entries []urls.ConflictEntry, totalInFile int, displayPath string, truncatedAtLine int) string {
	if len(entries) == 0 {
		return ""
	}
	total := totalInFile
	if total <= 0 {
		total = len(entries)
	}
	partial := total > len(entries)
	out := []string{""}
	word := "conflicts"
	if total == 1 {
		word = "conflict"
	}
	hintPath := displayPath
	if hintPath == "" {
		hintPath = "<file>"
	}
	if partial {
		out = append(out, fmt.Sprintf(
			"⚠ %d of %d unresolved %s visible in this window; this read registered only the blocks below, so don't infer ids for the others — read `%s:conflicts` to list and register the file's blocks.",
			len(entries), total, word, hintPath,
		))
	} else {
		out = append(out, fmt.Sprintf("⚠ %d unresolved %s detected", total, word))
	}
	if truncatedAtLine > 0 {
		out = append(out, formatConflictScanCapNote(hintPath, truncatedAtLine))
	}

	oursLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string { return e.OursLabel })
	theirsLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string { return e.TheirsLabel })
	baseLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string {
		if e.BaseLines != nil {
			return e.BaseLabel
		}
		return ""
	})
	anyBase := false
	for _, e := range entries {
		if e.BaseLines != nil {
			anyBase = true
			break
		}
	}
	if oursLabel != "" {
		out = append(out, "- ours = "+oursLabel)
	}
	if theirsLabel != "" {
		out = append(out, "- theirs = "+theirsLabel)
	}
	if anyBase {
		if baseLabel == "" {
			baseLabel = "(no label)"
		}
		out = append(out, "- base = "+baseLabel)
	}
	out = append(out,
		`NOTICE: Inspect a block by reading `+"`conflict://<N>`"+` (add `+"`/ours`"+` / `+"`/theirs`"+` / `+"`/base`"+` to render a single side). Resolve with `+"`write({ path: \"conflict://<N>\", content })`"+`, or bulk-resolve every registered conflict with `+"`write({ path: \"conflict://*\", content })`"+`. Writes replace ONLY the marker block (markers + all sides) — never repeat the lines before/after it; they stay in place.`,
		"`content` shorthand: a line that is exactly `@ours` / `@theirs` / `@base` / `@both` expands to that recorded section. `@both` is ours-then-theirs with no separator — only for additive conflicts where each side adds something different; NEVER for competing edits of the same lines (pick a side or write the combined text). Lines that are not a token pass through verbatim, so `\"// keep both\\n@ours\\n@theirs\"` literally writes the comment, then ours, then theirs.",
		`Per-id bulk: `+"`write({ path: \"conflict://*\", content: \"1: @ours\\n2: @theirs\\n…\" })`"+` resolves each listed id with that side in ONE call — the cheapest way through many pick-one conflicts; unlisted ids stay registered.`,
		"Resolve each block faithfully: keep one side (`@ours`/`@theirs`), or combine them when both intents apply — never invent content beyond the recorded sides, and never stack both sides of competing edits. Resolve several conflicts in a single turn by issuing multiple `write` calls at once; ids stay valid as earlier blocks are resolved.",
	)

	for _, entry := range entries {
		rng := fmt.Sprintf("L%d-%d", entry.StartLine, entry.EndLine)
		if entry.StartLine == entry.EndLine {
			rng = fmt.Sprintf("L%d", entry.StartLine)
		}
		out = append(out, "", fmt.Sprintf("──── #%d  %s ────", entry.ID, rng))

		baseEqualsOurs := entry.BaseLines != nil && conflictSectionsEqual(entry.BaseLines, entry.OursLines)
		baseEqualsTheirs := entry.BaseLines != nil && conflictSectionsEqual(entry.BaseLines, entry.TheirsLines)
		theirsEqualsOurs := conflictSectionsEqual(entry.TheirsLines, entry.OursLines)

		out = append(out, "<<< ours")
		out = appendConflictBody(out, entry.OursLines)

		if entry.BaseLines != nil {
			switch {
			case baseEqualsOurs:
				out = append(out, "=== base ≡ ours")
			case baseEqualsTheirs:
				out = append(out, "=== base ≡ theirs")
			default:
				out = append(out, "=== base")
				out = appendConflictBody(out, entry.BaseLines)
			}
		}

		if theirsEqualsOurs {
			out = append(out, ">>> theirs ≡ ours")
		} else {
			out = append(out, ">>> theirs")
			out = appendConflictBody(out, entry.TheirsLines)
		}
	}
	return strings.Join(out, "\n")
}

// formatConflictSummary formats omp's `<path>:conflicts` compact index.
// upstream: packages/coding-agent/src/tools/conflict-detect.ts:874-907
func formatConflictSummary(entries []urls.ConflictEntry, displayPath string, truncatedAtLine int) string {
	lines := make([]string, 0, len(entries)+12)
	total := len(entries)
	word := "conflicts"
	if total == 1 {
		word = "conflict"
	}
	pathLabel := displayPath
	if pathLabel == "" {
		pathLabel = "<file>"
	}
	lines = append(lines, fmt.Sprintf("⚠ %d unresolved %s in %s", total, word, pathLabel))
	if truncatedAtLine > 0 {
		lines = append(lines, formatConflictScanCapNote(pathLabel, truncatedAtLine))
	}
	oursLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string { return e.OursLabel })
	theirsLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string { return e.TheirsLabel })
	baseLabel := pickConflictLabel(entries, func(e urls.ConflictEntry) string {
		if e.BaseLines != nil {
			return e.BaseLabel
		}
		return ""
	})
	anyBase := false
	for _, e := range entries {
		if e.BaseLines != nil {
			anyBase = true
			break
		}
	}
	if oursLabel != "" {
		lines = append(lines, "- ours = "+oursLabel)
	}
	if theirsLabel != "" {
		lines = append(lines, "- theirs = "+theirsLabel)
	}
	if anyBase {
		if baseLabel == "" {
			baseLabel = "(no label)"
		}
		lines = append(lines, "- base = "+baseLabel)
	}
	lines = append(lines,
		`NOTICE: Bulk-resolve with `+"`write({ path: \"conflict://*\", content })`"+`, or address a single block with `+"`write({ path: \"conflict://<N>\", content })`"+`. Inspect a block by reading `+"`conflict://<N>`"+` (add `+"`/ours`"+` / `+"`/theirs`"+` / `+"`/base`"+` for a single side).`,
		"`content` shorthand: `@ours` / `@theirs` / `@base` / `@both` lines expand to the recorded sections; `@both` = ours-then-theirs (additive conflicts only — never for competing edits of the same lines). Per-id bulk: content of `<id>: @side` lines (e.g. \"1: @ours\\n2: @theirs\") resolves each listed id in one call. Non-token lines pass through verbatim. Writes replace ONLY the marker block — never repeat the surrounding lines. Keep one side or combine faithfully; never invent content beyond the recorded sides.",
		"",
	)
	idWidth := 1
	if len(entries) > 0 {
		idWidth = len(fmt.Sprintf("%d", entries[len(entries)-1].ID))
	}
	for _, entry := range entries {
		rng := fmt.Sprintf("L%d-%d", entry.StartLine, entry.EndLine)
		if entry.StartLine == entry.EndLine {
			rng = fmt.Sprintf("L%d", entry.StartLine)
		}
		idCell := fmt.Sprintf("#%*d", idWidth, entry.ID)
		kind := ""
		if entry.BaseLines != nil {
			kind = "  (3-way)"
		}
		lines = append(lines, fmt.Sprintf("%s  %s%s", idCell, rng, kind))
	}
	return strings.Join(lines, "\n")
}

func pickConflictLabel(entries []urls.ConflictEntry, get func(urls.ConflictEntry) string) string {
	for _, e := range entries {
		if lbl := get(e); strings.TrimSpace(lbl) != "" {
			return lbl
		}
	}
	return ""
}

func conflictSectionsEqual(a, b []string) bool {
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

func appendConflictBody(out []string, section []string) []string {
	if len(section) == 0 {
		return append(out, "(empty)")
	}
	shown := section
	if len(shown) > conflictPreviewSideLines {
		shown = section[:conflictPreviewSideLines]
	}
	out = append(out, shown...)
	if hidden := len(section) - len(shown); hidden > 0 {
		plural := "s"
		if hidden == 1 {
			plural = ""
		}
		out = append(out, fmt.Sprintf("… (%d more line%s)", hidden, plural))
	}
	return out
}
