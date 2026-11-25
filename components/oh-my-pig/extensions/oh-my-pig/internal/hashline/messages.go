package hashline

import (
	"fmt"
	"slices"
	"strings"
)

// Centralized error/warning text for the hashline parser, applier, and patcher.
// Every message reproduces crates/edit/src/modes/hashline/messages.rs verbatim.
// upstream: crates/edit/src/modes/hashline/messages.rs

// JSONQuote produces JS-compatible JSON.stringify(str) escaping ", \, and control chars.
// upstream: crates/edit/src/modes/hashline/messages.rs:22
func JSONQuote(s string) string {
	var out strings.Builder
	out.Grow(len(s) + 2)
	out.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, c)
			} else {
				out.WriteRune(c)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// MismatchContext is the number of lines shown either side of a hash mismatch.
// upstream: crates/edit/src/modes/hashline/messages.rs:46
const MismatchContext uint32 = 2

// AnchoredContextLines returns the sorted 1-indexed line numbers displayed by FormatAnchoredContext.
// upstream: crates/edit/src/modes/hashline/messages.rs:50
func AnchoredContextLines(anchorLines []uint32, lineCount int) []uint32 {
	if lineCount <= 0 || len(anchorLines) == 0 {
		return nil
	}
	set := make(map[uint32]struct{})
	maxLine := uint32(lineCount)
	for _, line := range anchorLines {
		if line < 1 || line > maxLine {
			continue
		}
		lo := uint32(1)
		if line > MismatchContext {
			lo = line - MismatchContext
		}
		hi := maxLine
		if maxLine > MismatchContext && line <= maxLine-MismatchContext {
			hi = line + MismatchContext
		}
		for n := lo; ; n++ {
			set[n] = struct{}{}
			if n == hi {
				break
			}
		}
	}
	out := make([]uint32, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// FormatAnchoredContext renders numbered LINE:TEXT rows around anchorLines (±MismatchContext),
// *-marking anchors, ... between non-adjacent runs.
// upstream: crates/edit/src/modes/hashline/messages.rs:66
func FormatAnchoredContext(anchorLines []uint32, fileLines []string) []string {
	displayLines := AnchoredContextLines(anchorLines, len(fileLines))
	if len(displayLines) == 0 {
		return nil
	}
	anchorSet := make(map[uint32]struct{}, len(anchorLines))
	for _, a := range anchorLines {
		anchorSet[a] = struct{}{}
	}
	var rows []string
	var prev uint32
	hasPrev := false
	for _, lineNum := range displayLines {
		if hasPrev && lineNum > prev+1 {
			rows = append(rows, "...")
		}
		prev = lineNum
		hasPrev = true
		marker := " "
		if _, ok := anchorSet[lineNum]; ok {
			marker = "*"
		}
		text := ""
		if int(lineNum-1) < len(fileLines) {
			text = fileLines[lineNum-1]
		}
		rows = append(rows, marker+formatNumberedLineU32(lineNum, text))
	}
	return rows
}

// AbsoluteRangeOp names the concrete range operation rejected because end < start.
// upstream: crates/edit/src/modes/hashline/messages.rs:97
type AbsoluteRangeOp int

const (
	AbsoluteRangeReplace AbsoluteRangeOp = iota
	AbsoluteRangeCut
)

func (op AbsoluteRangeOp) String() string {
	if op == AbsoluteRangeCut {
		return "cut"
	}
	return "replace"
}

func regSuffix(register string, hasReg bool) string {
	if !hasReg {
		return ""
	}
	return " @" + register
}

func rangeOpSingle(op AbsoluteRangeOp, line uint32, register string, hasReg bool) string {
	suffix := regSuffix(register, hasReg)
	if op == AbsoluteRangeCut {
		return fmt.Sprintf("%s %d%s", HLCutKeyword, line, suffix)
	}
	if hasReg {
		return fmt.Sprintf("%s %d%s", HLPutKeyword, line, suffix)
	}
	return fmt.Sprintf("%s %d:", HLPutKeyword, line)
}

func rangeOpRange(op AbsoluteRangeOp, start, end uint32, register string, hasReg bool) string {
	suffix := regSuffix(register, hasReg)
	if op == AbsoluteRangeCut {
		return fmt.Sprintf("%s %d%s%d%s", HLCutKeyword, start, HLRangeSep, end, suffix)
	}
	if hasReg {
		return fmt.Sprintf("%s %d%s%d%s", HLPutKeyword, start, HLRangeSep, end, suffix)
	}
	return fmt.Sprintf("%s %d%s%d:", HLPutKeyword, start, HLRangeSep, end)
}

func blockFormAt(op AbsoluteRangeOp, line uint32, register string, hasReg bool) string {
	suffix := regSuffix(register, hasReg)
	if op == AbsoluteRangeCut {
		return fmt.Sprintf("%s %d*%s", HLCutKeyword, line, suffix)
	}
	if hasReg {
		return fmt.Sprintf("%s %d*%s", HLPutKeyword, line, suffix)
	}
	return fmt.Sprintf("%s %d*:", HLPutKeyword, line)
}

// InvalidAbsoluteRangeMessage explains absolute range endpoints and retry forms.
// upstream: crates/edit/src/modes/hashline/messages.rs:158
func InvalidAbsoluteRangeMessage(
	patchLine, start, end uint32,
	op AbsoluteRangeOp,
	block *BlockSpan,
	register string,
	hasReg bool,
) string {
	single := rangeOpSingle(op, start, register, hasReg)
	var counted string
	hasCounted := false
	if end >= 1 {
		sum := uint64(start) + uint64(end) - 1
		if sum <= uint64(^uint32(0)) && uint32(sum) >= start {
			counted = rangeOpRange(op, start, uint32(sum), register, hasReg)
			hasCounted = true
		}
	}
	blockForm := blockFormAt(op, start, register, hasReg)
	var b strings.Builder
	fmt.Fprintf(&b,
		"line %d: Invalid absolute range: start %d, end %d. The value after `%s` is an absolute source line, not a line count or replacement length. For one line use `%s`.",
		patchLine, start, end, HLRangeSep, single,
	)
	if hasCounted {
		fmt.Fprintf(&b, " For %d lines starting at %d, use `%s`.", end, start, counted)
	}
	if block != nil && block.Start == start && block.End > start {
		fmt.Fprintf(&b, " The syntactic block beginning at %d ends at %d, so `%s` is also valid.", start, block.End, blockForm)
	}
	return b.String()
}

// Envelope & abort markers and static warning/error messages.
// upstream: crates/edit/src/modes/hashline/messages.rs:197-296
const (
	BeginPatchMarker = "*** Begin Patch"
	EndPatchMarker   = "*** End Patch"
	AbortMarker      = "*** Abort"

	ReplacePairCoalescedWarning = "Multiple hunks targeted the same exact range; kept only the last. Issue one `PUT` or `CUT` hunk per range."
	BareBodyAutoPipedWarning    = "Auto-prefixed bare body row(s) with `+`. Body rows must be `+TEXT` literal lines."
	SnapshotRowsAutoPutWarning  = "Recovered top-level `N:TEXT` snapshot row(s) as single-line `PUT N.=N:` replacements. Use explicit `PUT` headers for reliable edits."
	BareRangeAutoPutWarning     = "Recovered a bare `N.=M:` header as `PUT N.=M:`. Prefix replacement ranges with `PUT`."
	ReadMetadataIgnoredWarning  = "Ignored copied read-output elision row(s). Re-read elided ranges before editing them."
	EmptyPutAutoCutWarning      = "Interpreted an empty `PUT` body as deletion. Use `CUT N.=M` or `CUT N*` for bodyless deletes."
	CutColonIgnoredWarning      = "Ignored a trailing `:` on bodyless `CUT`. Prefer `CUT N.=M` / `CUT N*` without a colon."
	MinusBulletAutoPipedWarning = "Auto-prefixed bare `- ` bullet row(s) as literal content. `-` rows never remove lines — the range does that; always prefix literal body rows with `+`: `+- item`."
	DiffOldRowsIgnoredWarning   = "Ignored unified-diff `-old` row(s); the range already removes old content, so only `+new` rows were kept."
	MinusRowRejected            = "`-` rows are not valid; the range already names the lines being changed. For Markdown bullets or other literal `-` lines, prefix the literal row with `+`: `+- item`."

	BlockResolverUnavailable = "Block locators (`N*` in `PUT N*:`, `PUT >N*`, `CUT N*`) are not available here (no block resolver configured). Use a concrete line range."

	UnresolvedBlockInternal     = "internal error: unresolved block edit reached the applier (resolveBlockEdits was not run)."
	UnresolvedClipboardInternal = "internal error: unresolved clipboard edit reached the applier (resolveClipboardEdits was not run)."

	RemTakesNoBody          = "`REM` deletes the whole file and takes no body rows or line ops other than bodyless `CUT`s. Issue it alone under the header, or after the `CUT`s that capture lines to move."
	MoveTakesNoBody         = "`MV DEST` does not take body rows. Put line edits above the `MV` row; the destination path follows `MV` on the same line."
	CutTakesNoBody          = "`CUT` deletes (and captures) the named lines and takes no body rows. To write new content, use `PUT N.=M:` with `+TEXT` rows."
	ColonOnRegisterPut      = "`PUT … @name` pastes the register and never takes `:` — the colon promises body rows. Drop the colon (`PUT >40 @name`), or drop `@name` and write `+TEXT` body rows."
	RegisterPutTakesNoBody  = "A register `PUT` pastes captured lines and takes no `+` body rows. To write literal text, drop the `@name` and use `PUT …:` with body rows."
	ColonlessPutTakesNoBody = "`PUT` without `:` is clipboard-backed and takes no body rows. Add `:` after the locator to write literal content (`PUT >40:` then `+TEXT` rows)."
	ColonlessSpanPut        = "Colonless `PUT` is clipboard-backed, and span targets need a named register (`PUT 5.=9 @name`); the anonymous register pastes only at gaps (`PUT >40`). To write literal content, add `:` and `+TEXT` body rows."
	EmptyPasteMessage       = "Nothing to paste: no unlabeled `CUT` precedes this `PUT` in this call, and the anonymous register never carries across calls. Put `CUT N.=M` / `CUT N*` above it, or use named registers (`CUT … @name` → `PUT … @name`) for cross-call moves."

	ClipboardInterleavedSections = "`CUT`/register-`PUT` ops cannot be used in a file whose sections are interleaved with another file's: same-path sections merge into the first occurrence, which would reorder the register sequence. Keep each file's ops under ONE `[path#TAG]` header."
	EmptyInsertMessage           = "`PUT <N:` / `PUT >N:` promises body rows and got none. Write `+TEXT` rows, or drop the `:` to paste a register (`PUT >N` = anonymous, `PUT >N @name` = named)."

	RecoveryExternalWarning     = "Recovered from a stale file hash using a previous read snapshot (file changed externally between read and edit)."
	RecoverySessionChainWarning = "Recovered from a stale file hash using an earlier in-session snapshot (a prior edit in this session advanced the hash)."
	RecoveryLineRemapWarning    = "Recovered by remapping stale line anchors to unchanged current lines (file changed since the tagged read). Verify the diff matches your intent."
	HeadTailDriftWarning        = "Applied the `PUT <1:`/`PUT >$:` edit despite a stale snapshot tag (file changed since your read) — head/tail position is content-independent. Re-read if the drift was unexpected."
)

// MalformedHeaderRowMessage formats the refusal for a bare row shaped like a hunk/file header.
// upstream: crates/edit/src/modes/hashline/messages.rs:219
func MalformedHeaderRowMessage(text string) string {
	return fmt.Sprintf(
		"%s looks like a hunk or file header but does not parse as one, so it was neither run nor written. Ops: `PUT N.=M:`, `PUT N*:`, `PUT <N:`, `PUT >N:`, `CUT N.=M`, `CUT N*`, `REM`, `MV DEST`; a file header is `[PATH#TAG]` alone at the start of a line. If the row is literal file content, prefix it with `+`.",
		JSONQuote(text),
	)
}

// RepeatedSnapshotRowMessage formats the refusal for repeated top-level N:TEXT rows.
// upstream: crates/edit/src/modes/hashline/messages.rs:239
func RepeatedSnapshotRowMessage(line uint32) string {
	return fmt.Sprintf(
		"two or more pasted `%d:TEXT` read-output rows name line %d. Such rows are recovered as single-line `PUT %d%s%d:` replacements, so repeating a number would keep only the last row and drop the rest. Write the hunk explicitly: one `PUT %d%sM:` header covering exactly the lines that change, followed by `+TEXT` body rows holding their complete final content.",
		line, line, line, HLRangeSep, line, line, HLRangeSep,
	)
}

// LiteralOpRowWarning warns when a + body row is itself a valid hunk header.
// upstream: crates/edit/src/modes/hashline/messages.rs:254
func LiteralOpRowWarning(line uint32, text string) string {
	return fmt.Sprintf(
		"line %d: body row `%s%s` is itself a valid hunk header, so it was inserted into the file as literal text rather than executed. Ops are never `%s`-prefixed — drop the `%s` to run it, and re-issue if this line landed in the file by mistake.",
		line, HLPayloadReplace, text, HLPayloadReplace, HLPayloadReplace,
	)
}

// BlockDiagnosticSuggestions holds optional source-aware suggestions for block-anchor diagnostics.
// upstream: crates/edit/src/modes/hashline/messages.rs:300
type BlockDiagnosticSuggestions struct {
	NextBlock      *BlockSpan
	EnclosingBlock *BlockSpan
}

// BlockCoverage indicates how far block locators reach in the target file's language.
// upstream: crates/edit/src/modes/hashline/messages.rs:310
type BlockCoverage int

const (
	BlockCoverageFull BlockCoverage = iota
	BlockCoverageNoGrammar
	BlockCoverageNoEmbedded
)

// BlockUnresolvedMessage formats the refusal when a block-anchored replace/cut cannot resolve.
// upstream: crates/edit/src/modes/hashline/messages.rs:323
func BlockUnresolvedMessage(
	line uint32,
	op AbsoluteRangeOp,
	fileLines []string,
	suggestions BlockDiagnosticSuggestions,
	register string,
	hasReg bool,
	coverage BlockCoverage,
) string {
	phrase := blockFormAt(op, line, register, hasReg)
	var fallback string
	if op == AbsoluteRangeCut {
		if hasReg {
			fallback = fmt.Sprintf("%s %d%sM @%s", HLCutKeyword, line, HLRangeSep, register)
		} else {
			fallback = fmt.Sprintf("%s %d%sM", HLCutKeyword, line, HLRangeSep)
		}
	} else {
		if hasReg {
			fallback = fmt.Sprintf("%s %d%sM @%s", HLPutKeyword, line, HLRangeSep, register)
		} else {
			fallback = fmt.Sprintf("%s %d%sM:", HLPutKeyword, line, HLRangeSep)
		}
	}
	blankAnchor := false
	if line >= 1 && int(line-1) < len(fileLines) {
		blankAnchor = strings.TrimSpace(fileLines[line-1]) == ""
	}
	var b strings.Builder
	if blankAnchor && suggestions.NextBlock != nil {
		retry := blockFormAt(op, suggestions.NextBlock.Start, register, hasReg)
		fmt.Fprintf(&b,
			"Line %d is blank; no syntactic block can begin there. The next multi-line block begins at line %d and ends at line %d. Retry `%s`.",
			line, suggestions.NextBlock.Start, suggestions.NextBlock.End, retry,
		)
	} else {
		var reason string
		switch coverage {
		case BlockCoverageNoGrammar:
			reason = "(block locators need a tree-sitter grammar, and this file type has none)"
		case BlockCoverageNoEmbedded:
			reason = "(blank/closer line, parse error, or a line inside an embedded `<script>`/`<style>` section, which block locators do not reach)"
		default:
			reason = "(blank/closer line, or parse error)"
		}
		fmt.Fprintf(&b,
			"`%s` could not resolve a syntactic block beginning on line %d %s. Use `%s` with explicit lines.",
			phrase, line, reason, fallback,
		)
	}
	if suggestions.EnclosingBlock != nil {
		retry := blockFormAt(op, suggestions.EnclosingBlock.Start, register, hasReg)
		fmt.Fprintf(&b,
			" The nearest enclosing multi-line block begins at line %d and ends at line %d; use `%s` to target it.",
			suggestions.EnclosingBlock.Start, suggestions.EnclosingBlock.End, retry,
		)
	}
	if len(fileLines) > 0 {
		ctx := FormatAnchoredContext([]uint32{line}, fileLines)
		if len(ctx) > 0 {
			b.WriteString("\n\n")
			b.WriteString(strings.Join(ctx, "\n"))
		}
	}
	return b.String()
}

func closerLoweredWarning(blockForm, plainForm string) string {
	return fmt.Sprintf(
		"`%s` anchors on a closing delimiter, so it was applied as plain `%s`. Anchor on the line that OPENS the construct.",
		blockForm, plainForm,
	)
}

func unresolvedLoweredWarning(blockForm string, line uint32, plainForm string) string {
	return fmt.Sprintf(
		"`%s` could not resolve a syntactic block on line %d, so it was applied as plain `%s`. Verify the landing line; anchor on a line that OPENS a construct.",
		blockForm, line, plainForm,
	)
}

// InsertAfterBlockCloserLoweredWarning formats the warning for PUT >N*: on a closer.
// upstream: crates/edit/src/modes/hashline/messages.rs:414
func InsertAfterBlockCloserLoweredWarning(line uint32) string {
	return closerLoweredWarning(fmt.Sprintf("PUT >%d*:", line), fmt.Sprintf("PUT >%d:", line))
}

// InsertAfterBlockUnresolvedLoweredWarning formats the warning for unresolved PUT >N*:.
// upstream: crates/edit/src/modes/hashline/messages.rs:419
func InsertAfterBlockUnresolvedLoweredWarning(line uint32) string {
	return unresolvedLoweredWarning(fmt.Sprintf("PUT >%d*:", line), line, fmt.Sprintf("PUT >%d:", line))
}

// PasteAfterBlockCloserLoweredWarning formats the warning for PUT >N* on a closer.
// upstream: crates/edit/src/modes/hashline/messages.rs:425
func PasteAfterBlockCloserLoweredWarning(line uint32) string {
	return closerLoweredWarning(fmt.Sprintf("PUT >%d*", line), fmt.Sprintf("PUT >%d", line))
}

// PasteAfterBlockUnresolvedLoweredWarning formats the warning for unresolved PUT >N*.
// upstream: crates/edit/src/modes/hashline/messages.rs:430
func PasteAfterBlockUnresolvedLoweredWarning(line uint32) string {
	return unresolvedLoweredWarning(fmt.Sprintf("PUT >%d*", line), line, fmt.Sprintf("PUT >%d", line))
}

// BoundarySide names which side an ambiguous echo appeared on.
// upstream: crates/edit/src/modes/hashline/messages.rs:436
type BoundarySide int

const (
	BoundaryLeading BoundarySide = iota
	BoundaryTrailing
)

func formatLineSpanPair(first, last uint32) string {
	if first == last {
		return fmt.Sprintf("line %d", first)
	}
	return fmt.Sprintf("lines %d-%d", first, last)
}

// AmbiguousBoundaryEchoMessage formats the one-sided too-short echo refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:456
func AmbiguousBoundaryEchoMessage(startLine, endLine uint32, side BoundarySide, count uint32, leftStart, leftEnd uint32) string {
	var whereClause string
	if side == BoundaryLeading {
		whereClause = fmt.Sprintf("opens by restating the %d line(s) just above the range", count)
	} else {
		whereClause = fmt.Sprintf("ends by restating the %d line(s) just below the range", count)
	}
	leftOut := formatLineSpanPair(leftStart, leftEnd)
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: the body %s, but is too short to be the full final content of the selected range. If the range is off by those line(s), the body leaves out %s, which this edit would delete. Re-issue with the range covering exactly the lines that change and the body as their complete final content.",
		startLine, HLRangeSep, endLine, whereClause, leftOut,
	)
}

// RepeatedBoundaryEchoMessage formats the repeated boundary echo refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:486
func RepeatedBoundaryEchoMessage(startLine, endLine uint32, side BoundarySide, count uint32, leftStart, leftEnd uint32) string {
	var whereClause string
	if side == BoundaryLeading {
		whereClause = fmt.Sprintf("opens by restating the %d line(s) just above the range more than once", count)
	} else {
		whereClause = fmt.Sprintf("ends by restating the %d line(s) just below the range more than once", count)
	}
	leftOut := formatLineSpanPair(leftStart, leftEnd)
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: the body %s. With the rows that restate lines just outside the range read as those lines, the body is too short to be the full final content of the selected range: it leaves out %s, which this edit would delete. Re-issue with the range covering exactly the lines that change and the body as their complete final content.",
		startLine, HLRangeSep, endLine, whereClause, leftOut,
	)
}

// TwoSidedBoundaryEchoMessage formats the two-sided too-short boundary echo refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:518
func TwoSidedBoundaryEchoMessage(startLine, endLine, above, below uint32, repeated bool, leftStart, leftEnd uint32) string {
	leftOut := formatLineSpanPair(leftStart, leftEnd)
	rep := ""
	if repeated {
		rep = " more than once"
	}
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: the body restates both the %d line(s) just above and the %d line(s) just below the range%s, and once the rows that restate lines outside the range are set aside, the rows left are too few to be the full final content of the selected range: they leave out %s, which this edit would delete. Re-issue with the range covering exactly the lines that change and the body as their complete final content.",
		startLine, HLRangeSep, endLine, above, below, rep, leftOut,
	)
}

// BoundaryEchoOutcomeMessage formats the final echo check refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:543
func BoundaryEchoOutcomeMessage(startLine, endLine uint32, above, below, leftOut *[2]uint32) string {
	var effects []string
	if above != nil {
		effects = append(effects, fmt.Sprintf("write %s (just above the range) a second time", formatLineSpanPair(above[0], above[1])))
	}
	if below != nil {
		effects = append(effects, fmt.Sprintf("write %s (just below the range) a second time", formatLineSpanPair(below[0], below[1])))
	}
	if leftOut != nil {
		effects = append(effects, fmt.Sprintf("leave out %s of the range, deleting it", formatLineSpanPair(leftOut[0], leftOut[1])))
	}
	extendStart := startLine
	extendEnd := endLine
	if above != nil {
		extendStart = above[0]
	}
	if below != nil {
		extendEnd = below[1]
	}
	extend := fmt.Sprintf("%d%s%d", extendStart, HLRangeSep, extendEnd)
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: the body restates lines next to the range, and as written the result would %s. Nothing was applied. Re-issue with the range covering exactly the lines that change and the body as their complete final content; if the repeated line is meant to appear twice, use `PUT %s:` and write both copies.",
		startLine, HLRangeSep, endLine, strings.Join(effects, " and "), extend,
	)
}

// AmbiguousBoundaryPlacementMessage formats the undecided placement refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:588
func AmbiguousBoundaryPlacementMessage(startLine, endLine uint32, errorLine uint32) string {
	at := ""
	if errorLine > 0 {
		at = fmt.Sprintf(" (first syntax error near line %d)", errorLine)
	}
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: as written the file would no longer parse%s. A selected boundary row is required for the file to parse, but the body indentation does not establish whether it belongs before or after that row. Re-read the region and re-issue with a range that excludes every unchanged boundary row.",
		startLine, HLRangeSep, endLine, at,
	)
}

// TrailingCommaReplacementMessage formats the JSON trailing-comma refusal.
// upstream: crates/edit/src/modes/hashline/messages.rs:604
func TrailingCommaReplacementMessage(startLine, endLine uint32) string {
	return fmt.Sprintf(
		"`PUT %d%s%d:` rejected: the body's last non-blank row ends with a comma that does not parse in place (e.g. a trailing comma before a closing `]` or `}`); without that comma this replacement parses. Drop the comma, or include the rows that should follow it in the same body.",
		startLine, HLRangeSep, endLine,
	)
}

// BoundaryFix describes one replacement's parse-restoring change.
// upstream: crates/edit/src/modes/hashline/messages.rs:615
type BoundaryFix struct {
	Start        uint32
	End          uint32
	Keep         uint32 // 0 = none
	DropLeading  uint32
	DropTrailing uint32
}

// BoundaryFixMessage formats the boundary-fix rejection.
// upstream: crates/edit/src/modes/hashline/messages.rs:631
func BoundaryFixMessage(fixes []BoundaryFix, errorLine uint32) string {
	at := ""
	if errorLine > 0 {
		at = fmt.Sprintf(" (first syntax error near line %d)", errorLine)
	}
	clauses := make([]string, 0, len(fixes))
	for _, fix := range fixes {
		var parts []string
		if fix.Keep > 0 {
			var form string
			switch {
			case fix.Start == fix.End:
				form = fmt.Sprintf("PUT <%d:", fix.Start)
			case fix.Keep == fix.Start:
				form = fmt.Sprintf("PUT %d%s%d:", fix.Start+1, HLRangeSep, fix.End)
			case fix.Keep == fix.End:
				form = fmt.Sprintf("PUT %d%s%d:", fix.Start, HLRangeSep, fix.End-1)
			}
			if form != "" {
				parts = append(parts, fmt.Sprintf("line %d stays (use `%s`)", fix.Keep, form))
			} else {
				parts = append(parts, fmt.Sprintf("line %d stays after the body", fix.Keep))
			}
		}
		if fix.DropLeading > 0 {
			parts = append(parts, fmt.Sprintf("its first %d body row(s), which repeat the line(s) just above the range, are dropped", fix.DropLeading))
		}
		if fix.DropTrailing > 0 {
			parts = append(parts, fmt.Sprintf("its last %d body row(s), which repeat the line(s) just below the range, are dropped", fix.DropTrailing))
		}
		clauses = append(clauses, fmt.Sprintf("for `PUT %d%s%d:` %s", fix.Start, HLRangeSep, fix.End, strings.Join(parts, " and ")))
	}
	return fmt.Sprintf(
		"Edit rejected: the file parses now but would not after this edit as written%s. It parses if, %s. Nothing was applied. If that is what you meant, re-issue it that way; otherwise correct the body so the result parses.",
		at, strings.Join(clauses, "; "),
	)
}

// EditBrokeParseWarning formats the advisory parse regression warning.
// upstream: crates/edit/src/modes/hashline/messages.rs:683
func EditBrokeParseWarning(errorLine uint32) string {
	return fmt.Sprintf(
		"Tree-sitter parsed this file before the edit but reports a syntax error near line %d after it, within the edited lines. Re-read that region and fix it if the edit broke it; if the code is valid, this is a parser limitation and needs no action.",
		errorLine,
	)
}

// EmptyRegisterPasteWarning formats the warning for an empty named gap paste.
// upstream: crates/edit/src/modes/hashline/messages.rs:747
func EmptyRegisterPasteWarning(name string, known []string) string {
	base := fmt.Sprintf(
		"`@%s` was empty — no `CUT … @%s` precedes this op in this call and no persisted register has that name — so nothing was pasted.",
		name, name,
	)
	if len(known) == 0 {
		return base
	}
	avail := make([]string, len(known))
	for i, k := range known {
		avail[i] = "`@" + k + "`"
	}
	return fmt.Sprintf("%s Available registers: %s.", base, strings.Join(avail, ", "))
}

// EmptyRegisterSpanPasteMessage formats the error for an empty named span paste.
// upstream: crates/edit/src/modes/hashline/messages.rs:769
func EmptyRegisterSpanPasteMessage(name string, known []string) string {
	base := fmt.Sprintf(
		"`@%s` is empty — no `CUT … @%s` precedes this op in this call and no persisted register has that name — so pasting it over a range would delete those lines and write nothing back. Capture the register first (`CUT … @%s`), or use `CUT` if deleting the range is what you meant.",
		name, name, name,
	)
	if len(known) == 0 {
		return base
	}
	avail := make([]string, len(known))
	for i, k := range known {
		avail[i] = "`@" + k + "`"
	}
	return fmt.Sprintf("%s Available registers: %s.", base, strings.Join(avail, ", "))
}

// AmbiguousAnonymousPasteMessage formats the error when multiple unlabeled cuts are pending.
// upstream: crates/edit/src/modes/hashline/messages.rs:789
func AmbiguousAnonymousPasteMessage(pending []string) string {
	return fmt.Sprintf(
		"%d unlabeled `CUT`s are pending (%s) — an unlabeled paste cannot tell which one you meant. Label the moves (`CUT … @name` → `PUT … @name`), or keep at most one unlabeled `CUT` before each unlabeled paste.",
		len(pending), strings.Join(pending, ", "),
	)
}

// AfterInsertLandingMessage formats the landing rejection for PUT >N:.
// upstream: crates/edit/src/modes/hashline/messages.rs:818
func AfterInsertLandingMessage(anchorLine, landingLine, crossed uint32) string {
	s := "s"
	if crossed == 1 {
		s = ""
	}
	return fmt.Sprintf(
		"`PUT >%d:` rejected: with the body after line %d the file would no longer parse. The body is indented shallower than line %d and parses after line %d, past %d closing line%s. Nothing was applied. If it belongs there, re-issue as `PUT >%d:`; for the deeper position inside the block, indent the body to match.",
		anchorLine, anchorLine, anchorLine, landingLine, crossed, s, landingLine,
	)
}

// BlockInsertLandingMessage formats the landing rejection for PUT >N*:.
// upstream: crates/edit/src/modes/hashline/messages.rs:832
func BlockInsertLandingMessage(blockStart, closerLine, landingLine uint32) string {
	return fmt.Sprintf(
		"`PUT >%d*:` rejected: it lands after the block's closing line %d, where the file would no longer parse. The body is indented deeper than that closer and parses inside the block, after line %d. Nothing was applied. If inside was intended, re-issue as `PUT >%d:`; otherwise indent the body to sibling depth.",
		blockStart, closerLine, landingLine, landingLine,
	)
}

// AfterInsertOpenerMessage formats the landing rejection for PUT >N: on an opener.
// upstream: crates/edit/src/modes/hashline/messages.rs:848
func AfterInsertOpenerMessage(anchorLine, landingLine uint32) string {
	return fmt.Sprintf(
		"`PUT >%d:` rejected: line %d opens a block, so the body would land inside it, where the file would no longer parse. The body's indentation points outside the block, and it parses after line %d, past the whole construct. Nothing was applied. If it belongs there, re-issue as `PUT >%d:` or `PUT >%d*:`.",
		anchorLine, anchorLine, landingLine, landingLine, anchorLine,
	)
}

// PathRecoveredFromTagMessage formats the warning when a missing path rebounds via tag.
// upstream: crates/edit/src/modes/hashline/messages.rs:883
func PathRecoveredFromTagMessage(authoredPath, resolvedPath, tag string) string {
	return fmt.Sprintf(
		"Path \"%s\" does not exist; matched its filename and snapshot tag %s%s to %s (read earlier this session). Anchor future edits on %s%s%sTAG%s.",
		authoredPath, HLFileHashSep, tag, resolvedPath, HLFilePrefix, resolvedPath, HLFileHashSep, HLFileSuffix,
	)
}

// formatLineRangesU32 compresses a line list into sorted "1-4, 7, 10-12".
// upstream: crates/edit/src/modes/hashline/messages.rs:896
func formatLineRangesU32(lines []uint32) string {
	if len(lines) == 0 {
		return ""
	}
	sorted := slices.Clone(lines)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	var parts []string
	start := sorted[0]
	prev := sorted[0]
	for _, cur := range sorted[1:] {
		if cur == prev+1 {
			prev = cur
			continue
		}
		if start == prev {
			parts = append(parts, fmt.Sprintf("%d", start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
		start = cur
		prev = cur
	}
	if start == prev {
		parts = append(parts, fmt.Sprintf("%d", start))
	} else {
		parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
	}
	return strings.Join(parts, ", ")
}

// RevealedLine is one anchored line surfaced in an unseen-lines error.
// upstream: crates/edit/src/modes/hashline/messages.rs:929
type RevealedLine struct {
	Line uint32
	Text string
}

// UnseenLinesReveal is the content preview handed to UnseenLinesMessage.
// upstream: crates/edit/src/modes/hashline/messages.rs:965
type UnseenLinesReveal struct {
	Lines     []RevealedLine
	Truncated bool
}

// WideUnseenLinesMessage formats the refusal for a wide unseen anchor.
// upstream: crates/edit/src/modes/hashline/messages.rs:935
func WideUnseenLinesMessage(sectionPath string, unseenLines []uint32) string {
	ranges := formatLineRangesU32(unseenLines)
	selector := strings.ReplaceAll(ranges, ", ", ",")
	return fmt.Sprintf(
		"This edit anchors to lines %s of %s that were not fully displayed. Read `%s:%s:raw` for their full content, then re-issue the edit.",
		ranges, sectionPath, sectionPath, selector,
	)
}

// UnverifiedWideLinesMessage formats the refusal when no retained read verifies wide lines.
// upstream: crates/edit/src/modes/hashline/messages.rs:947
func UnverifiedWideLinesMessage(sectionPath string, wideLines []uint32) string {
	ranges := formatLineRangesU32(wideLines)
	selector := strings.ReplaceAll(ranges, ", ", ",")
	return fmt.Sprintf(
		"This edit anchors to lines %s of %s, which exceed the display column cap, and no retained read shows them in full. Read `%s:%s:raw` for their full content, then re-issue the edit.",
		ranges, sectionPath, sectionPath, selector,
	)
}

// UnseenLinesMessage formats the unseen-lines refusal with optional inline reveal.
// upstream: crates/edit/src/modes/hashline/messages.rs:978
func UnseenLinesMessage(sectionPath string, unseenLines []uint32, tag string, reveal UnseenLinesReveal) string {
	ranges := formatLineRangesU32(unseenLines)
	selector := strings.ReplaceAll(ranges, ", ", ",")
	header := fmt.Sprintf(
		"This edit anchors to lines %s of %s that %s%s%s%s%s never displayed (it showed a partial range, a search hit, or a folded summary).",
		ranges, sectionPath, HLFilePrefix, sectionPath, HLFileHashSep, tag, HLFileSuffix,
	)
	if len(reveal.Lines) == 0 {
		return fmt.Sprintf(
			"%s Re-read them in full first with a ranged read like `%s:%s` — it skips summarization and mints a fresh tag (a plain re-read just re-folds them) — then re-issue the edit.",
			header, sectionPath, selector,
		)
	}
	rows := make([]string, len(reveal.Lines))
	for i, r := range reveal.Lines {
		rows[i] = "  " + formatNumberedLineU32(r.Line, r.Text)
	}
	preview := strings.Join(rows, "\n")
	if reveal.Truncated {
		return fmt.Sprintf(
			"%s Preview of the actual file content at the first %d unseen line(s):\n%s\nThe range exceeds the inline preview cap — re-read the remainder with `%s:%s` before re-issuing the edit.",
			header, len(reveal.Lines), preview, sectionPath, selector,
		)
	}
	return fmt.Sprintf(
		"%s Actual file content at those lines:\n%s\nVerify the content matches what you intend to touch, then re-issue the edit with the same %spath%stag%s header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers.",
		header, preview, HLFilePrefix, HLFileHashSep, HLFileSuffix,
	)
}

// StaleUnseenLinesMessage formats the read-first fallback for a stale tag.
// upstream: crates/edit/src/modes/hashline/messages.rs:1027
func StaleUnseenLinesMessage(sectionPath string, unseenLines []uint32, tag string, liveLines []uint32, liveTag string, raw bool) string {
	ranges := formatLineRangesU32(unseenLines)
	liveRanges := formatLineRangesU32(liveLines)
	selector := strings.ReplaceAll(liveRanges, ", ", ",")
	rawSuffix := ""
	if raw {
		rawSuffix = ":raw"
	}
	liveHeader := fmt.Sprintf("%s%s%s%s%s", HLFilePrefix, sectionPath, HLFileHashSep, liveTag, HLFileSuffix)
	return fmt.Sprintf(
		"This edit anchors to lines %s of %s that %s%s%s%s%s never displayed in full. The file has changed since that tag: those lines are now lines %s, under %s. Read `%s:%s%s` for their full content, then re-issue the edit under %s with current line numbers.",
		ranges, sectionPath, HLFilePrefix, sectionPath, HLFileHashSep, tag, HLFileSuffix, liveRanges, liveHeader, sectionPath, selector, rawSuffix, liveHeader,
	)
}

func blockOpForm(op BlockOpKind, line uint32) string {
	switch op {
	case BlockOpReplace:
		return fmt.Sprintf("%s %d*:", HLPutKeyword, line)
	case BlockOpInsertAfter:
		return fmt.Sprintf("%s >%d*:", HLPutKeyword, line)
	case BlockOpCut:
		return fmt.Sprintf("%s %d*", HLCutKeyword, line)
	case BlockOpPasteAfter:
		return fmt.Sprintf("%s >%d*", HLPutKeyword, line)
	default:
		return ""
	}
}

func blockOpPlain(op BlockOpKind, line uint32) string {
	switch op {
	case BlockOpReplace:
		return fmt.Sprintf("%s %d:", HLPutKeyword, line)
	case BlockOpInsertAfter:
		return fmt.Sprintf("%s >%d:", HLPutKeyword, line)
	case BlockOpCut:
		return fmt.Sprintf("%s %d", HLCutKeyword, line)
	case BlockOpPasteAfter:
		return fmt.Sprintf("%s >%d", HLPutKeyword, line)
	default:
		return ""
	}
}

// BlockSingleLineMessage formats the refusal when a block anchor resolves to a single line.
// upstream: crates/edit/src/modes/hashline/messages.rs:1080
func BlockSingleLineMessage(line uint32, op BlockOpKind, enclosing *BlockSpan) string {
	form := blockOpForm(op, line)
	plainForm := blockOpPlain(op, line)
	var b strings.Builder
	fmt.Fprintf(&b,
		"`%s` resolved a single-line block — line %d is a bare statement, not the opening line of a multi-line construct. For only this statement use `%s`.",
		form, line, plainForm,
	)
	if enclosing != nil {
		enclosingForm := blockOpForm(op, enclosing.Start)
		fmt.Fprintf(&b,
			" The nearest enclosing multi-line block begins at line %d and ends at line %d; use `%s` to target it.",
			enclosing.Start, enclosing.End, enclosingForm,
		)
	}
	return b.String()
}
