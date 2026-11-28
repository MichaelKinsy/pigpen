package hashline

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Token-driven parser for hashline section bodies.
// upstream: crates/edit/src/modes/hashline/parser.rs

const maxExpandedRangeLines uint32 = 100_000

var (
	unifiedHunkRe  = regexp.MustCompile(`^@@\s+[-+]?\d+,\d+\s+[-+]?\d+,\d+\s+@@`)
	headerShapedRe = regexp.MustCompile(`^\s*(?:(?:PUT|CUT|REM|MV)(?:\s|$)|\[[^\]]+#[0-9A-Fa-f]{4}\]\s*$)`)
)

// InvalidAbsoluteRange records an inverted concrete range for source-aware enrichment.
// upstream: crates/edit/src/modes/hashline/parser.rs:41
type InvalidAbsoluteRange struct {
	PatchLine   uint32
	StartLine   uint32
	EndLine     uint32
	Op          AbsoluteRangeOp
	Register    string
	HasRegister bool
	Block       *BlockSpan
}

// Message formats the model-facing absolute-range guidance.
// upstream: crates/edit/src/modes/hashline/parser.rs:51
func (e *InvalidAbsoluteRange) Message() string {
	return InvalidAbsoluteRangeMessage(e.PatchLine, e.StartLine, e.EndLine, e.Op, e.Block, e.Register, e.HasRegister)
}

// WithBlock returns a copy enriched with a proven syntactic block.
// upstream: crates/edit/src/modes/hashline/parser.rs:64
func (e *InvalidAbsoluteRange) WithBlock(span BlockSpan) *InvalidAbsoluteRange {
	cp := *e
	b := span
	cp.Block = &b
	return &cp
}

// ParseFailure is raised while lowering hashline tokens.
// upstream: crates/edit/src/modes/hashline/parser.rs:71
type ParseFailure struct {
	InvalidRange *InvalidAbsoluteRange
	Other        string
}

func (f *ParseFailure) Error() string {
	if f.InvalidRange != nil {
		return f.InvalidRange.Message()
	}
	return f.Other
}

func (f *ParseFailure) ToEditError() *EditError {
	return ParseError(f.Error())
}

// Parsed holds edits, optional file operation, and parser warnings for one section.
// upstream: crates/edit/src/modes/hashline/input.rs:43
type Parsed struct {
	Edits    []Edit
	FileOp   *FileOp
	Warnings []string
}

type payloadRow struct {
	text    string
	lineNum uint32
	bare    bool
	minus   bool
}

type pendingOp struct {
	target         BlockTarget
	lineNum        uint32
	payloads       []payloadRow
	hadColon       bool
	deferredBlanks []payloadRow
}

type skippableComment struct {
	lineNum uint32
	text    string
}

// Executor is the incremental token-to-edit executor.
// upstream: crates/edit/src/modes/hashline/parser.rs:106
type Executor struct {
	edits                  []Edit
	warnings               []string
	editIndex              uint32
	pending                *pendingOp
	fileOp                 *FileOp
	terminated             bool
	skippableComments      []skippableComment
	recoveredSnapshotLines map[uint32]struct{}
}

// NewExecutor constructs an empty Executor.
// upstream: crates/edit/src/modes/hashline/parser.rs:125
func NewExecutor() *Executor {
	return &Executor{
		recoveredSnapshotLines: make(map[uint32]struct{}),
	}
}

func (ex *Executor) warnOnce(warning string) {
	if !slices.Contains(ex.warnings, warning) {
		ex.warnings = append(ex.warnings, warning)
	}
}

func (ex *Executor) consumeComments() *ParseFailure {
	if len(ex.skippableComments) == 0 {
		return nil
	}
	comments := ex.skippableComments
	ex.skippableComments = nil
	for _, c := range comments {
		if err := ex.handleRaw(c.text, c.lineNum); err != nil {
			return err
		}
	}
	return nil
}

// Push consumes one classified token.
// upstream: crates/edit/src/modes/hashline/parser.rs:139
func (ex *Executor) Push(token Token) *ParseFailure {
	return ex.feed(token)
}

func (ex *Executor) feed(token Token) *ParseFailure {
	if ex.terminated {
		return nil
	}
	switch token.Kind {
	case TokenEnvelopeBegin:
		return ex.consumeComments()
	case TokenEnvelopeEnd:
		if err := ex.consumeComments(); err != nil {
			return err
		}
		ex.terminated = true
		return nil
	case TokenAbort:
		ex.terminated = true
		return nil
	case TokenHeader:
		if err := ex.consumeComments(); err != nil {
			return err
		}
		return ex.flushPending()
	case TokenBlank:
		if err := ex.consumeComments(); err != nil {
			return err
		}
		ex.handleBlank("", token.LineNum)
		return nil
	case TokenPayloadLiteral:
		if err := ex.consumeComments(); err != nil {
			return err
		}
		return ex.handleLiteral(token.Text, token.LineNum)
	case TokenRaw:
		if ex.pending == nil && strings.HasPrefix(strings.TrimLeftFunc(token.Text, unicode.IsSpace), "#") {
			ex.skippableComments = append(ex.skippableComments, skippableComment{lineNum: token.LineNum, text: token.Text})
			return nil
		}
		if err := ex.consumeComments(); err != nil {
			return err
		}
		return ex.handleRaw(token.Text, token.LineNum)
	case TokenOpBlock:
		ex.skippableComments = nil
		target := token.Target
		lineNum := token.LineNum
		hadColon := token.HadColon
		if target.Kind == TargetReplace {
			if err := validateRange(target.Range, lineNum, AbsoluteRangeReplace, target.Register, target.HasRegister); err != nil {
				return err
			}
		}
		if target.Kind == TargetCut {
			if err := validateRange(target.Range, lineNum, AbsoluteRangeCut, target.Register, target.HasRegister); err != nil {
				return err
			}
		}
		if hadColon && (target.Kind == TargetCut || target.Kind == TargetCutBlock) {
			ex.warnOnce(CutColonIgnoredWarning)
		}
		if hadColon && target.Kind != TargetRem && target.Kind != TargetMove && target.HasRegister {
			return failAt(lineNum, ColonOnRegisterPut)
		}
		switch target.Kind {
		case TargetRem:
			if err := ex.flushPending(); err != nil {
				return err
			}
			return ex.setFileOp(FileOp{Kind: FileOpDelete}, lineNum)
		case TargetMove:
			if err := ex.flushPending(); err != nil {
				return err
			}
			return ex.setFileOp(FileOp{Kind: FileOpMove, Dest: target.Dest}, lineNum)
		default:
			if err := ex.flushPending(); err != nil {
				return err
			}
			ex.pending = &pendingOp{
				target:   target,
				lineNum:  lineNum,
				hadColon: hadColon,
			}
			return nil
		}
	}
	return nil
}

func (ex *Executor) finish(streaming bool) (Parsed, *ParseFailure) {
	if err := ex.consumeComments(); err != nil {
		return Parsed{}, err
	}
	if streaming {
		if ex.pending != nil && (len(ex.pending.payloads) > 0 || completeBodyless(ex.pending)) {
			if err := ex.flushPending(); err != nil {
				return Parsed{}, err
			}
		} else {
			ex.pending = nil
		}
	} else {
		if err := ex.flushPending(); err != nil {
			return Parsed{}, err
		}
	}
	if ex.fileOp != nil && ex.fileOp.Kind == FileOpDelete && !onlyCuts(ex.edits) {
		return Parsed{}, &ParseFailure{Other: RemTakesNoBody}
	}
	if err := ex.normalizeOverlaps(); err != nil {
		return Parsed{}, err
	}
	return Parsed{Edits: ex.edits, FileOp: ex.fileOp, Warnings: ex.warnings}, nil
}

func (ex *Executor) setFileOp(op FileOp, lineNum uint32) *ParseFailure {
	if ex.fileOp != nil {
		return failAt(lineNum, "only one file-level op (`REM` or `MV`) per section. Merge them under one header.")
	}
	if op.Kind == FileOpDelete && !onlyCuts(ex.edits) {
		return failAt(lineNum, RemTakesNoBody)
	}
	cp := op
	ex.fileOp = &cp
	return nil
}

func (ex *Executor) handleLiteral(text string, lineNum uint32) *ParseFailure {
	if ex.pending == nil {
		if ex.fileOp != nil {
			return failAt(lineNum, MoveTakesNoBody)
		}
		return failAt(lineNum, fmt.Sprintf("payload line has no preceding hunk header. Got %s.", JSONQuote("+"+text)))
	}
	if msg := bodylessMessage(ex.pending.target, ex.pending.hadColon); msg != "" {
		return failAt(lineNum, msg)
	}
	ex.pending.payloads = append(ex.pending.payloads, ex.pending.deferredBlanks...)
	ex.pending.deferredBlanks = nil
	if IsHunkHeaderText(text) {
		ex.warnings = append(ex.warnings, LiteralOpRowWarning(lineNum, text))
	}
	ex.pending.payloads = append(ex.pending.payloads, payloadRow{
		text:    text,
		lineNum: lineNum,
		bare:    false,
		minus:   false,
	})
	return nil
}

func (ex *Executor) handleRaw(text string, lineNum uint32) *ParseFailure {
	if ex.pending == nil && IsReadMetadataLine(text) {
		ex.warnOnce(ReadMetadataIgnoredWarning)
		return nil
	}
	if msg := contaminationMessage(text); msg != "" {
		return failAt(lineNum, msg)
	}
	if ex.fileOp != nil {
		return failAt(lineNum, MoveTakesNoBody)
	}
	if ex.pending != nil {
		if strings.TrimSpace(text) == "" {
			ex.handleBlank(text, lineNum)
			return nil
		}
		if msg := bodylessMessage(ex.pending.target, ex.pending.hadColon); msg != "" {
			return failAt(lineNum, msg)
		}
		if headerShapedRe.MatchString(text) {
			return failAt(lineNum, MalformedHeaderRowMessage(text))
		}
		minus := strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "-")
		if !minus {
			ex.warnOnce(BareBodyAutoPipedWarning)
		}
		ex.pending.payloads = append(ex.pending.payloads, ex.pending.deferredBlanks...)
		ex.pending.deferredBlanks = nil
		ex.pending.payloads = append(ex.pending.payloads, payloadRow{
			text:    text,
			lineNum: lineNum,
			bare:    true,
			minus:   minus,
		})
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if rng, ok := parseBareRange(text); ok {
		if err := validateRange(rng, lineNum, AbsoluteRangeReplace, "", false); err != nil {
			return err
		}
		ex.pending = &pendingOp{
			target:   BlockTarget{Kind: TargetReplace, Range: rng},
			lineNum:  lineNum,
			hadColon: true,
		}
		ex.warnOnce(BareRangeAutoPutWarning)
		return nil
	}
	if ln, val, ok := parseSnapshotRow(text); ok {
		if _, dup := ex.recoveredSnapshotLines[ln]; dup {
			return failAt(lineNum, RepeatedSnapshotRowMessage(ln))
		}
		ex.recoveredSnapshotLines[ln] = struct{}{}
		rng := ParsedRange{Start: Anchor{Line: ln}, End: Anchor{Line: ln}}
		ex.pushInsert(Cursor{Kind: CursorBeforeAnchor, Anchor: Anchor{Line: ln}}, val, lineNum, true)
		ex.pushDeleteRange(rng, lineNum)
		ex.warnOnce(SnapshotRowsAutoPutWarning)
		return nil
	}
	return failAt(
		lineNum,
		fmt.Sprintf(
			"payload line has no preceding hunk header. Use `PUT N.=M:`, `CUT N.=M`, or `PUT <N:`/`PUT >N:` above the body. Got %s.",
			JSONQuote(text),
		),
	)
}

func (ex *Executor) handleBlank(text string, lineNum uint32) {
	if ex.pending == nil {
		return
	}
	if bodylessMessage(ex.pending.target, ex.pending.hadColon) != "" || len(ex.pending.payloads) == 0 {
		return
	}
	ex.pending.deferredBlanks = append(ex.pending.deferredBlanks, payloadRow{
		text:    text,
		lineNum: lineNum,
		bare:    true,
		minus:   false,
	})
}

func (ex *Executor) flushPending() *ParseFailure {
	if ex.pending == nil {
		return nil
	}
	pending := ex.pending
	ex.pending = nil
	if err := ex.resolveMinusRows(&pending.payloads); err != nil {
		return err
	}
	stripUniformBarePrefixes(pending.payloads)
	line := pending.lineNum
	target := pending.target
	switch target.Kind {
	case TargetRem, TargetMove:
	case TargetCut:
		ex.pushCut(target.Range, target.Register, target.HasRegister, line)
	case TargetCutBlock:
		ex.pushBlock(target.Anchor, nil, BlockModeCut, target.Register, target.HasRegister, line)
	case TargetReplace:
		if target.HasRegister {
			ex.pushPaste(PasteTarget{Kind: PasteTargetSpan, Range: target.Range}, target.Register, target.HasRegister, line)
		} else if len(pending.payloads) == 0 {
			if !pending.hadColon {
				return failAt(line, ColonlessSpanPut)
			}
			ex.pushDeleteRange(target.Range, line)
			ex.warnOnce(EmptyPutAutoCutWarning)
		} else {
			for _, row := range pending.payloads {
				ex.pushInsert(Cursor{Kind: CursorBeforeAnchor, Anchor: target.Range.Start}, row.text, line, true)
			}
			ex.pushDeleteRange(target.Range, line)
		}
	case TargetBlock:
		if target.HasRegister {
			ex.pushBlock(target.Anchor, nil, BlockModeReplace, target.Register, target.HasRegister, line)
		} else if len(pending.payloads) == 0 {
			if !pending.hadColon {
				return failAt(line, ColonlessSpanPut)
			}
			ex.pushBlock(target.Anchor, nil, BlockModeReplace, "", false, line)
			ex.warnOnce(EmptyPutAutoCutWarning)
		} else {
			texts := make([]string, len(pending.payloads))
			for i, r := range pending.payloads {
				texts[i] = r.text
			}
			ex.pushBlock(target.Anchor, texts, BlockModeReplace, "", false, line)
		}
	case TargetInsertAfterBlock:
		if target.HasRegister || (!pending.hadColon && len(pending.payloads) == 0) {
			ex.pushBlock(target.Anchor, nil, BlockModePasteAfter, target.Register, target.HasRegister, line)
		} else if len(pending.payloads) == 0 {
			return failAt(line, EmptyInsertMessage)
		} else {
			texts := make([]string, len(pending.payloads))
			for i, r := range pending.payloads {
				texts[i] = r.text
			}
			ex.pushBlock(target.Anchor, texts, BlockModeInsertAfter, "", false, line)
		}
	default:
		var cursor Cursor
		switch target.Kind {
		case TargetInsertBefore:
			cursor = Cursor{Kind: CursorBeforeAnchor, Anchor: target.Anchor}
		case TargetInsertAfter:
			cursor = Cursor{Kind: CursorAfterAnchor, Anchor: target.Anchor}
		case TargetBof:
			cursor = Cursor{Kind: CursorBof}
		case TargetEof:
			cursor = Cursor{Kind: CursorEof}
		}
		if target.HasRegister || (!pending.hadColon && len(pending.payloads) == 0) {
			ex.pushPaste(PasteTarget{Kind: PasteTargetGap, Cursor: cursor}, target.Register, target.HasRegister, line)
		} else if len(pending.payloads) == 0 {
			return failAt(line, EmptyInsertMessage)
		} else {
			for _, row := range pending.payloads {
				ex.pushInsert(cursor, row.text, line, false)
			}
		}
	}
	return nil
}

func (ex *Executor) resolveMinusRows(rows *[]payloadRow) *ParseFailure {
	var minus []payloadRow
	var explicit []payloadRow
	for _, row := range *rows {
		if row.minus {
			minus = append(minus, row)
		}
		if !row.bare {
			explicit = append(explicit, row)
		}
	}
	if len(minus) == 0 {
		return nil
	}
	allBullets := true
	for _, r := range minus {
		if !markdownBullet(r.text) {
			allBullets = false
			break
		}
	}
	anyExplicitBullet := false
	for _, r := range explicit {
		if markdownBullet(r.text) {
			anyExplicitBullet = true
			break
		}
	}
	if allBullets && (len(explicit) == 0 || anyExplicitBullet) {
		ex.warnOnce(MinusBulletAutoPipedWarning)
		return nil
	}
	if len(explicit) > 0 && !allBullets {
		filtered := (*rows)[:0]
		for _, row := range *rows {
			if !row.minus {
				filtered = append(filtered, row)
			}
		}
		*rows = filtered
		ex.warnOnce(DiffOldRowsIgnoredWarning)
		return nil
	}
	return failAt(minus[0].lineNum, MinusRowRejected)
}

func (ex *Executor) nextIndex() uint32 {
	idx := ex.editIndex
	if ex.editIndex < math.MaxUint32 {
		ex.editIndex++
	}
	return idx
}

func (ex *Executor) pushInsert(cursor Cursor, text string, lineNum uint32, replacement bool) {
	ex.edits = append(ex.edits, Edit{
		Kind:        EditInsert,
		Cursor:      cursor,
		Text:        text,
		LineNum:     lineNum,
		Index:       ex.nextIndex(),
		Replacement: replacement,
	})
}

func (ex *Executor) pushDelete(anchor Anchor, lineNum uint32) {
	ex.edits = append(ex.edits, Edit{
		Kind:    EditDelete,
		Anchor:  anchor,
		LineNum: lineNum,
		Index:   ex.nextIndex(),
	})
}

func (ex *Executor) pushDeleteRange(rng ParsedRange, lineNum uint32) {
	for ln := rng.Start.Line; ; ln++ {
		ex.pushDelete(Anchor{Line: ln}, lineNum)
		if ln == rng.End.Line {
			break
		}
	}
}

func (ex *Executor) pushCut(rng ParsedRange, reg string, hasReg bool, lineNum uint32) {
	ex.edits = append(ex.edits, Edit{
		Kind:        EditCut,
		Range:       rng,
		Register:    reg,
		HasRegister: hasReg,
		LineNum:     lineNum,
		Index:       ex.nextIndex(),
	})
	ex.pushDeleteRange(rng, lineNum)
}

func (ex *Executor) pushPaste(at PasteTarget, reg string, hasReg bool, lineNum uint32) {
	ex.edits = append(ex.edits, Edit{
		Kind:        EditPaste,
		PasteAt:     at,
		Register:    reg,
		HasRegister: hasReg,
		LineNum:     lineNum,
		Index:       ex.nextIndex(),
	})
}

func (ex *Executor) pushBlock(anchor Anchor, payloads []string, mode BlockMode, reg string, hasReg bool, lineNum uint32) {
	ex.edits = append(ex.edits, Edit{
		Kind:        EditBlock,
		Anchor:      anchor,
		Payloads:    payloads,
		BlockMode:   mode,
		Register:    reg,
		HasRegister: hasReg,
		LineNum:     lineNum,
		Index:       ex.nextIndex(),
	})
}

// normalizeOverlaps matches crates/edit/src/modes/hashline/parser.rs:574.
func (ex *Executor) normalizeOverlaps() *ParseFailure {
	type hunkInfo struct {
		lines     map[uint32]struct{}
		clipboard bool
	}
	hunks := make(map[uint32]*hunkInfo)
	var hunkOrder []uint32
	getHunk := func(ln uint32) *hunkInfo {
		h, ok := hunks[ln]
		if !ok {
			h = &hunkInfo{lines: make(map[uint32]struct{})}
			hunks[ln] = h
			hunkOrder = append(hunkOrder, ln)
		}
		return h
	}
	for _, edit := range ex.edits {
		switch edit.Kind {
		case EditCut:
			getHunk(edit.LineNum).clipboard = true
		case EditPaste:
			if edit.PasteAt.Kind == PasteTargetSpan {
				h := getHunk(edit.LineNum)
				h.clipboard = true
				for ln := edit.PasteAt.Range.Start.Line; ; ln++ {
					h.lines[ln] = struct{}{}
					if ln == edit.PasteAt.Range.End.Line {
						break
					}
				}
			}
		case EditDelete:
			getHunk(edit.LineNum).lines[edit.Anchor.Line] = struct{}{}
		}
	}
	slices.Sort(hunkOrder)

	owner := make(map[uint32]uint32)
	dropped := make(map[uint32]struct{})
	for _, lineNum := range hunkOrder {
		hunk := hunks[lineNum]
		if len(hunk.lines) == 0 {
			continue
		}
		overlaps := make(map[uint32]struct{})
		for ln := range hunk.lines {
			if prev, ok := owner[ln]; ok {
				overlaps[prev] = struct{}{}
			}
		}
		if len(overlaps) == 0 {
			for ln := range hunk.lines {
				owner[ln] = lineNum
			}
			continue
		}
		var prev uint32
		hasSinglePrev := false
		if len(overlaps) == 1 {
			for p := range overlaps {
				prev = p
				hasSinglePrev = true
			}
		}
		exact := false
		if hasSinglePrev {
			old := hunks[prev]
			if !old.clipboard && sameLineSet(old.lines, hunk.lines) {
				exact = true
			}
		}
		if exact {
			dropped[prev] = struct{}{}
			for ln, o := range owner {
				if o == prev {
					delete(owner, ln)
				}
			}
			for ln := range hunk.lines {
				owner[ln] = lineNum
			}
			ex.warnOnce(ReplacePairCoalescedWarning)
			continue
		}
		// Find smallest overlapping line.
		sortedLines := make([]uint32, 0, len(hunk.lines))
		for ln := range hunk.lines {
			sortedLines = append(sortedLines, ln)
		}
		slices.Sort(sortedLines)
		var first uint32
		for _, ln := range sortedLines {
			if _, ok := owner[ln]; ok {
				first = ln
				break
			}
		}
		priorStr := "an earlier line"
		if hasSinglePrev {
			priorStr = strconv.FormatUint(uint64(prev), 10)
		}
		return failAt(
			lineNum,
			fmt.Sprintf(
				"anchor line %d is already targeted by another hunk on line %s. Issue ONE hunk per range; payload is only the final desired content, never a before/after pair.",
				first, priorStr,
			),
		)
	}
	if len(dropped) > 0 {
		kept := ex.edits[:0]
		for _, edit := range ex.edits {
			if _, ok := dropped[edit.LineNum]; !ok {
				kept = append(kept, edit)
			}
		}
		ex.edits = kept
	}
	return nil
}

func sameLineSet(a, b map[uint32]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func validateRange(rng ParsedRange, lineNum uint32, op AbsoluteRangeOp, reg string, hasReg bool) *ParseFailure {
	if rng.End.Line < rng.Start.Line {
		return &ParseFailure{
			InvalidRange: &InvalidAbsoluteRange{
				PatchLine:   lineNum,
				StartLine:   rng.Start.Line,
				EndLine:     rng.End.Line,
				Op:          op,
				Register:    reg,
				HasRegister: hasReg,
			},
		}
	}
	span := rng.End.Line - rng.Start.Line + 1
	if span > maxExpandedRangeLines {
		return failAt(
			lineNum,
			fmt.Sprintf(
				"%s range spans %d lines; the maximum is %d. Split it into smaller hunks.",
				op.String(), span, maxExpandedRangeLines,
			),
		)
	}
	return nil
}

func failAt(line uint32, message string) *ParseFailure {
	return &ParseFailure{Other: fmt.Sprintf("line %d: %s", line, message)}
}

func bodylessMessage(target BlockTarget, hadColon bool) string {
	if target.Kind == TargetCut || target.Kind == TargetCutBlock {
		return CutTakesNoBody
	}
	if target.Kind == TargetRem || target.Kind == TargetMove {
		return ""
	}
	if target.HasRegister {
		return RegisterPutTakesNoBody
	}
	if !hadColon {
		return ColonlessPutTakesNoBody
	}
	return ""
}

func onlyCuts(edits []Edit) bool {
	for _, edit := range edits {
		switch edit.Kind {
		case EditCut, EditDelete:
		case EditBlock:
			if edit.BlockMode != BlockModeCut {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func completeBodyless(pending *pendingOp) bool {
	k := pending.target.Kind
	if k == TargetCut || k == TargetCutBlock || pending.target.HasRegister {
		return true
	}
	if !pending.hadColon {
		switch k {
		case TargetInsertBefore, TargetInsertAfter, TargetInsertAfterBlock, TargetBof, TargetEof:
			return true
		}
	}
	return false
}

func markdownBullet(text string) bool {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	rest, ok := strings.CutPrefix(trimmed, "- ")
	if !ok || rest == "" {
		return false
	}
	r, _ := decodeRuneAt(rest, 0)
	return !unicode.IsSpace(r)
}

func parseSnapshotRow(text string) (uint32, string, bool) {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	split := strings.IndexAny(trimmed, ":|")
	if split <= 0 {
		return 0, "", false
	}
	numStr := trimmed[:split]
	if numStr[0] == '0' {
		return 0, "", false
	}
	for i := range len(numStr) {
		if numStr[i] < '0' || numStr[i] > '9' {
			return 0, "", false
		}
	}
	n, err := strconv.ParseUint(numStr, 10, 32)
	if err != nil {
		return 0, "", false
	}
	return uint32(n), trimmed[split+1:], true
}

func parseBareRange(text string) (ParsedRange, bool) {
	trimmed := strings.TrimSpace(text)
	before, ok := strings.CutSuffix(trimmed, ":")
	if !ok {
		return ParsedRange{}, false
	}
	before = strings.TrimSpace(before)
	parts := strings.FieldsFunc(before, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '.' || r == '=' || r == '…'
	})
	if len(parts) != 2 {
		return ParsedRange{}, false
	}
	start, err1 := strconv.ParseUint(parts[0], 10, 32)
	end, err2 := strconv.ParseUint(parts[1], 10, 32)
	if err1 != nil || err2 != nil || start == 0 || end == 0 {
		return ParsedRange{}, false
	}
	return ParsedRange{Start: Anchor{Line: uint32(start)}, End: Anchor{Line: uint32(end)}}, true
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return s
}

func contaminationMessage(text string) string {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, prefix := range []string{"*** Update File:", "*** Add File:", "*** Delete File:", "*** Move to:"} {
		if strings.HasPrefix(trimmed, prefix) {
			preview := truncateRunes(trimmed, 48)
			return fmt.Sprintf(
				"apply_patch sentinel %s is not valid in hashline. File sections start with `[path#HASH]` (no `Update File:` / `Add File:` keyword). Use `PUT N.=M:`, `CUT N.=M`, or `PUT <N:`/`PUT >N:` ops.",
				JSONQuote(preview),
			)
		}
	}
	if strings.HasPrefix(trimmed, "@@") {
		if unifiedHunkRe.MatchString(trimmed) {
			return "unified-diff hunk header (`@@ -N,M +N,M @@`) is not valid in hashline. Use `PUT N.=M:`, `CUT N.=M`, or `PUT <N:`/`PUT >N:` ops."
		}
		preview := truncateRunes(trimmed, 48)
		return fmt.Sprintf(
			"`@@`-bracketed hunk header %s is not valid in hashline. Drop the `@@ ... @@` brackets and write a header such as `PUT N.=M:`.",
			JSONQuote(preview),
		)
	}
	if trimmed != "" {
		allDigitOrWS := true
		for i := range len(trimmed) {
			b := trimmed[i]
			if !((b >= '0' && b <= '9') || isASCIIWhitespace(b)) {
				allDigitOrWS = false
				break
			}
		}
		if allDigitOrWS && len(strings.Fields(trimmed)) == 1 {
			n := strings.TrimSpace(trimmed)
			return fmt.Sprintf(
				"hunk headers need a verb and both endpoints. Use `PUT %s.=%s:` to replace, or `CUT %s.=%s` to delete.",
				n, n, n, n,
			)
		}
	}
	pieces := strings.Fields(strings.TrimRight(trimmed, ":"))
	if len(pieces) == 2 {
		allDigits := true
		for _, p := range pieces {
			if p == "" {
				allDigits = false
				break
			}
			for i := range len(p) {
				if p[i] < '0' || p[i] > '9' {
					allDigits = false
					break
				}
			}
		}
		if allDigits {
			return fmt.Sprintf(
				"bare range hunk header %s is not valid. Hunk headers need a verb: use `PUT N.=M:` or `CUT N.=M`.",
				JSONQuote(trimmed),
			)
		}
	}
	return ""
}

func stripUniformBarePrefixes(rows []payloadRow) {
	saw := false
	allLiteral := true
	for _, row := range rows {
		if !row.bare || strings.TrimSpace(row.text) == "" {
			continue
		}
		saw = true
		stripped := StripOneLeadingHashlinePrefix(row.text)
		if stripped == row.text {
			return
		}
		if !literalValue(stripped) {
			allLiteral = false
		}
	}
	if !saw || allLiteral {
		return
	}
	for i := range rows {
		if !rows[i].bare || strings.TrimSpace(rows[i].text) == "" {
			continue
		}
		rows[i].text = StripOneLeadingHashlinePrefix(rows[i].text)
	}
}

func literalValue(text string) bool {
	value := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), ","))
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return true
	}
	if value == "" {
		return false
	}
	// Rust f64::parse rejects hex floats (0x...) and underscores that Go's strconv.ParseFloat accepts.
	if strings.ContainsAny(value, "xXpP_") {
		return false
	}
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

// ParsePatch parses a complete hashline section body.
// upstream: crates/edit/src/modes/hashline/parser.rs:852
func ParsePatch(diff string) (Parsed, *ParseFailure) {
	return parseImpl(diff, false)
}

// ParsePatchStreaming parses a partial body while dropping the trailing incomplete operation.
// upstream: crates/edit/src/modes/hashline/parser.rs:856
func ParsePatchStreaming(diff string) (Parsed, *ParseFailure) {
	return parseImpl(diff, true)
}

func parseImpl(diff string, streaming bool) (Parsed, *ParseFailure) {
	tok := NewTokenizer()
	ex := NewExecutor()
	tokens, err := tok.Feed(diff)
	if err != nil {
		return Parsed{}, &ParseFailure{Other: err.Error()}
	}
	for _, token := range tokens {
		if pErr := ex.feed(token); pErr != nil {
			return Parsed{}, pErr
		}
	}
	for _, token := range tok.End() {
		if pErr := ex.feed(token); pErr != nil {
			return Parsed{}, pErr
		}
	}
	return ex.finish(streaming)
}
