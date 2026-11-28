package hashline

import (
	"fmt"
	"strings"
	"unicode"
)

// Conservative pure-Go block-op resolution.
// Because oh-my-pig has no tree-sitter/cgo, a wrong span would corrupt code whereas
// a refusal never does. Therefore this resolver ONLY returns a multi-line span when
// it can prove the full construct extent without a parser, and refuses everything
// else using upstream's exact refusal messages.
// upstream: crates/edit/src/modes/hashline/block.rs, crates/ast/src/block.rs

const blockSuggestionScanLimit uint32 = 64

// UnresolvedBehavior controls what ResolveBlockEdits does when a block anchor cannot resolve.
// upstream: crates/edit/src/modes/hashline/block.rs:41
type UnresolvedBehavior int

const (
	UnresolvedThrow UnresolvedBehavior = iota
	UnresolvedDrop
)

// HasBlockEdit reports whether an edit stream contains a deferred block operation.
// upstream: crates/edit/src/modes/hashline/block.rs:49
func HasBlockEdit(edits []Edit) bool {
	for _, edit := range edits {
		if edit.Kind == EditBlock {
			return true
		}
	}
	return false
}

func blockModeToKind(mode BlockMode) BlockOpKind {
	switch mode {
	case BlockModeInsertAfter:
		return BlockOpInsertAfter
	case BlockModeCut:
		return BlockOpCut
	case BlockModePasteAfter:
		return BlockOpPasteAfter
	default:
		return BlockOpReplace
	}
}

func findNextBlock(anchorLine uint32, lines []string, path string) *BlockSpan {
	last := uint32(len(lines))
	if limit := anchorLine + blockSuggestionScanLimit; limit < last {
		last = limit
	}
	for ln := anchorLine + 1; ln <= last; ln++ {
		if int(ln-1) >= len(lines) || strings.TrimSpace(lines[ln-1]) == "" {
			continue
		}
		if span := ResolveBlockAt(path, lines, ln); span != nil && span.Start == ln && span.End > ln {
			return span
		}
	}
	return nil
}

func findEnclosingBlock(anchorLine uint32, lines []string, path string) *BlockSpan {
	first := uint32(1)
	if anchorLine > blockSuggestionScanLimit {
		first = anchorLine - blockSuggestionScanLimit
	}
	for ln := anchorLine - 1; ln >= first; ln-- {
		if ln < 1 || int(ln-1) >= len(lines) || strings.TrimSpace(lines[ln-1]) == "" {
			if ln == first {
				break
			}
			continue
		}
		if span := ResolveBlockAt(path, lines, ln); span != nil && span.Start == ln && span.End >= anchorLine && span.End > ln {
			return span
		}
		if ln == first {
			break
		}
	}
	return nil
}

// ResolveBlockEdits resolves deferred block operations to concrete edits.
// upstream: crates/edit/src/modes/hashline/block.rs:109
func ResolveBlockEdits(
	edits []Edit,
	text string,
	path string,
	onUnresolved UnresolvedBehavior,
	onResolved func(BlockResolution),
	onWarning func(string),
) ([]Edit, *EditError) {
	if !HasBlockEdit(edits) {
		return edits, nil
	}
	lines := strings.Split(text, "\n")
	var resolved []Edit
	var synthIndex uint32

	for _, edit := range edits {
		if edit.Kind != EditBlock {
			resolved = append(resolved, edit)
			continue
		}
		opKind := blockModeToKind(edit.BlockMode)
		span := ResolveBlockAt(path, lines, edit.Anchor.Line)
		if span == nil {
			if edit.BlockMode == BlockModeInsertAfter || edit.BlockMode == BlockModePasteAfter {
				isCloser := false
				if edit.Anchor.Line >= 1 && int(edit.Anchor.Line-1) < len(lines) {
					isCloser = structuralCloserRe.MatchString(lines[edit.Anchor.Line-1])
				}
				if edit.BlockMode == BlockModePasteAfter {
					if onWarning != nil {
						if isCloser {
							onWarning(PasteAfterBlockCloserLoweredWarning(edit.Anchor.Line))
						} else {
							onWarning(PasteAfterBlockUnresolvedLoweredWarning(edit.Anchor.Line))
						}
					}
					resolved = append(resolved, Edit{
						Kind:        EditPaste,
						PasteAt:     PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorAfterAnchor, Anchor: edit.Anchor}},
						Register:    edit.Register,
						HasRegister: edit.HasRegister,
						LineNum:     edit.LineNum,
						Index:       synthIndex,
					})
					synthIndex++
				} else {
					if onWarning != nil {
						if isCloser {
							onWarning(InsertAfterBlockCloserLoweredWarning(edit.Anchor.Line))
						} else {
							onWarning(InsertAfterBlockUnresolvedLoweredWarning(edit.Anchor.Line))
						}
					}
					for _, payload := range edit.Payloads {
						resolved = append(resolved, Edit{
							Kind:        EditInsert,
							Cursor:      Cursor{Kind: CursorAfterAnchor, Anchor: edit.Anchor},
							Text:        payload,
							LineNum:     edit.LineNum,
							Index:       synthIndex,
							Replacement: false,
						})
						synthIndex++
					}
				}
				continue
			}
			if onUnresolved == UnresolvedDrop {
				continue
			}
			var next *BlockSpan
			if edit.Anchor.Line >= 1 && int(edit.Anchor.Line-1) < len(lines) && strings.TrimSpace(lines[edit.Anchor.Line-1]) == "" {
				next = findNextBlock(edit.Anchor.Line, lines, path)
			}
			var enclosing *BlockSpan
			if next == nil {
				enclosing = findEnclosingBlock(edit.Anchor.Line, lines, path)
			}
			suggestions := BlockDiagnosticSuggestions{NextBlock: next, EnclosingBlock: enclosing}
			rangeOp := AbsoluteRangeReplace
			if opKind == BlockOpCut {
				rangeOp = AbsoluteRangeCut
			}
			return nil, ApplyError(fmt.Sprintf(
				"line %d: %s",
				edit.LineNum,
				BlockUnresolvedMessage(edit.Anchor.Line, rangeOp, lines, suggestions, edit.Register, edit.HasRegister, BlockCoverageForPath(path)),
			))
		}
		if span.Start == span.End {
			if onUnresolved == UnresolvedDrop {
				continue
			}
			enclosing := findEnclosingBlock(edit.Anchor.Line, lines, path)
			return nil, ApplyError(fmt.Sprintf(
				"line %d: %s",
				edit.LineNum,
				BlockSingleLineMessage(edit.Anchor.Line, opKind, enclosing),
			))
		}
		if onResolved != nil {
			onResolved(BlockResolution{
				AnchorLine: edit.Anchor.Line,
				Start:      span.Start,
				End:        span.End,
				Op:         opKind,
			})
		}
		switch edit.BlockMode {
		case BlockModePasteAfter:
			resolved = append(resolved, Edit{
				Kind:        EditPaste,
				PasteAt:     PasteTarget{Kind: PasteTargetGap, Cursor: Cursor{Kind: CursorAfterAnchor, Anchor: Anchor{Line: span.End}}},
				Register:    edit.Register,
				HasRegister: edit.HasRegister,
				LineNum:     edit.LineNum,
				Index:       synthIndex,
				BlockStart:  span.Start,
			})
			synthIndex++
		case BlockModeCut:
			resolved = append(resolved, Edit{
				Kind:        EditCut,
				Range:       ParsedRange{Start: Anchor{Line: span.Start}, End: Anchor{Line: span.End}},
				Register:    edit.Register,
				HasRegister: edit.HasRegister,
				LineNum:     edit.LineNum,
				Index:       synthIndex,
			})
			synthIndex++
			for ln := span.Start; ; ln++ {
				resolved = append(resolved, Edit{
					Kind:    EditDelete,
					Anchor:  Anchor{Line: ln},
					LineNum: edit.LineNum,
					Index:   synthIndex,
				})
				synthIndex++
				if ln == span.End {
					break
				}
			}
		case BlockModeInsertAfter:
			for _, payload := range edit.Payloads {
				resolved = append(resolved, Edit{
					Kind:        EditInsert,
					Cursor:      Cursor{Kind: CursorAfterAnchor, Anchor: Anchor{Line: span.End}},
					Text:        payload,
					LineNum:     edit.LineNum,
					Index:       synthIndex,
					Replacement: false,
					BlockStart:  span.Start,
				})
				synthIndex++
			}
		case BlockModeReplace:
			if edit.HasRegister {
				resolved = append(resolved, Edit{
					Kind:        EditPaste,
					PasteAt:     PasteTarget{Kind: PasteTargetSpan, Range: ParsedRange{Start: Anchor{Line: span.Start}, End: Anchor{Line: span.End}}},
					Register:    edit.Register,
					HasRegister: edit.HasRegister,
					LineNum:     edit.LineNum,
					Index:       synthIndex,
				})
				synthIndex++
			} else {
				for _, payload := range edit.Payloads {
					resolved = append(resolved, Edit{
						Kind:        EditInsert,
						Cursor:      Cursor{Kind: CursorBeforeAnchor, Anchor: Anchor{Line: span.Start}},
						Text:        payload,
						LineNum:     edit.LineNum,
						Index:       synthIndex,
						Replacement: true,
					})
					synthIndex++
				}
				for ln := span.Start; ; ln++ {
					resolved = append(resolved, Edit{
						Kind:    EditDelete,
						Anchor:  Anchor{Line: ln},
						LineNum: edit.LineNum,
						Index:   synthIndex,
					})
					synthIndex++
					if ln == span.End {
						break
					}
				}
			}
		}
	}
	return resolved, nil
}

// ResolveBlockAt resolves the provable block beginning on 1-indexed line.
// Returns:
//   - nil when no block begins on line (unknown grammar, blank, closer, comment, unproven construct)
//   - &BlockSpan{Start: line, End: line} when line is a proven single-line construct (triggers single-line refusal)
//   - &BlockSpan{Start: line, End: end} with end > line when the full multi-line extent is proven
func ResolveBlockAt(path string, lines []string, line uint32) *BlockSpan {
	if line < 1 || int(line) > len(lines) {
		return nil
	}
	lang := detectLang(path)
	if lang == langUnknown || lang == langEmbedded || lang == langOtherKnown {
		return nil
	}
	anchor := lines[line-1]
	trimmed := strings.TrimSpace(anchor)
	if trimmed == "" {
		return nil
	}
	switch lang {
	case langMarkdown:
		return resolveMarkdownBlock(lines, line)
	case langPython:
		return resolvePythonBlock(lines, line)
	case langYAML:
		return resolveYAMLBlock(lines, line)
	case langRust:
		return resolveRustBlock(lines, line)
	case langJS, langJSX, langGo, langJSON:
		return resolveBraceBlock(lang, lines, line)
	default:
		return nil
	}
}

// --- Markdown heading block resolver ---

func resolveMarkdownBlock(lines []string, line uint32) *BlockSpan {
	// Compute addressable line count (excluding trailing-LF phantom empty row).
	contentLen := len(lines)
	if contentLen > 1 && lines[contentLen-1] == "" {
		contentLen--
	}
	if int(line) > contentLen {
		return nil
	}
	// Refuse if document starts with YAML/TOML front-matter (`---` or `+++` on line 1).
	if contentLen > 0 && isMDFrontMatterFence(lines[0]) {
		return nil
	}
	// Ensure anchor line is not inside a fenced code block or HTML block/comment.
	inFence := false
	var fenceChar byte
	fenceLen := 0
	for i := range int(line - 1) {
		t := strings.TrimLeft(lines[i], " ")
		if !inFence {
			if ch, n, ok := mdFenceOpen(t); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
			} else if isMDHTMLBlockOrCommentStart(lines[i]) {
				return nil
			}
		} else if mdFenceClose(t, fenceChar, fenceLen) {
			inFence = false
		}
	}
	if inFence {
		return nil
	}
	// Refuse indented headings (e.g. `  ## Heading` nested under a list item or blockquote).
	anchorLine := lines[line-1]
	if len(anchorLine) > 0 && (anchorLine[0] == ' ' || anchorLine[0] == '\t') {
		return nil
	}
	level := mdHeadingLevel(anchorLine)
	if level == 0 {
		// Non-ATX-heading Markdown blocks (paragraphs, lists, blockquotes, setext)
		// cannot be proven without a CommonMark parser; refuse rather than mis-resolve.
		return nil
	}
	// Real omp (tree-sitter-markdown) ends a heading section on the line immediately
	// BEFORE the next ATX heading of level <= current (outside fenced code blocks),
	// INCLUDING blank lines between sections and trailing blank lines at EOF (not the
	// trailing-LF phantom row). Setext headings stay inside the section.
	end := uint32(contentLen)
	for i := int(line); i < contentLen; i++ {
		t := strings.TrimLeft(lines[i], " ")
		if !inFence {
			if ch, n, ok := mdFenceOpen(t); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
				continue
			}
			if isMDHTMLBlockOrCommentStart(lines[i]) {
				return nil
			}
			// Refuse if a heading-shaped line in the span is indented under a list or blockquote
			// (e.g. `  ## Nested` or `> ## Quoted`), where tree-sitter does not terminate the outer section.
			if isMDIndentedOrQuotedHeading(lines[i]) {
				return nil
			}
			if h := mdHeadingLevel(lines[i]); h > 0 && h <= level {
				end = uint32(i)
				break
			}
		} else {
			if mdFenceClose(t, fenceChar, fenceLen) {
				inFence = false
			}
		}
	}
	if inFence {
		return nil
	}
	return &BlockSpan{Start: line, End: end}
}

func isMDFrontMatterFence(line string) bool {
	t := strings.TrimRight(line, " \t")
	return t == "---" || t == "+++"
}

func isMDIndentedOrQuotedHeading(line string) bool {
	if line == "" {
		return false
	}
	if line[0] == ' ' || line[0] == '\t' {
		t := strings.TrimLeft(line, " \t")
		return mdHeadingLevel(t) > 0
	}
	if line[0] == '>' {
		t := strings.TrimLeft(line, "> \t")
		return mdHeadingLevel(t) > 0
	}
	return false
}
func isMDHTMLBlockOrCommentStart(line string) bool {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 2 || t[0] != '<' {
		return false
	}
	if strings.HasPrefix(t, "<!--") || strings.HasPrefix(t, "<?") || strings.HasPrefix(t, "<!") {
		return true
	}
	// Opening or closing HTML tag: `<tag`, `</tag`.
	rest := t[1:]
	if strings.HasPrefix(rest, "/") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return false
	}
	b := rest[0]
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func mdHeadingLevel(line string) int {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return 0
	}
	level := 0
	for level < len(t) && level < 6 && t[level] == '#' {
		level++
	}
	if level == 0 {
		return 0
	}
	if level < len(t) && t[level] != ' ' && t[level] != '\t' {
		return 0
	}
	return level
}

func mdFenceOpen(trimmedLeft string) (byte, int, bool) {
	if len(trimmedLeft) < 3 {
		return 0, 0, false
	}
	ch := trimmedLeft[0]
	if ch != '`' && ch != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(trimmedLeft) && trimmedLeft[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	if ch == '`' && strings.Contains(trimmedLeft[n:], "`") {
		return 0, 0, false
	}
	return ch, n, true
}

func mdFenceClose(trimmedLeft string, ch byte, minLen int) bool {
	n := 0
	for n < len(trimmedLeft) && trimmedLeft[n] == ch {
		n++
	}
	return n >= minLen && strings.TrimSpace(trimmedLeft[n:]) == ""
}

// --- Rust block resolver (handles outer attributes #[...], ///, /** */ + brace matching) ---

func isRustOuterDocLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "///") && !strings.HasPrefix(trimmed, "////") {
		return true
	}
	if strings.HasPrefix(trimmed, "/**") && !strings.HasPrefix(trimmed, "/***") && !strings.HasPrefix(trimmed, "/**/") {
		return true
	}
	return false
}

func isRustInnerAttrOrDoc(trimmed string) bool {
	return strings.HasPrefix(trimmed, "#![") || strings.HasPrefix(trimmed, "//!") || strings.HasPrefix(trimmed, "/*!")
}

func isRustOuterAttrStart(trimmed string) bool {
	return strings.HasPrefix(trimmed, "#[")
}

func isRustPlainComment(trimmed string) bool {
	if strings.HasPrefix(trimmed, "//") && !isRustOuterDocLine(trimmed) && !strings.HasPrefix(trimmed, "//!") {
		return true
	}
	if strings.HasPrefix(trimmed, "/*") && !isRustOuterDocLine(trimmed) && !strings.HasPrefix(trimmed, "/*!") {
		return true
	}
	return false
}

func resolveRustBlock(lines []string, line uint32) *BlockSpan {
	trimmed := strings.TrimSpace(lines[line-1])
	if trimmed == "" || startsWithCloser(trimmed) {
		return nil
	}
	// Pass 3 W3: a macro invocation or macro_rules! body is a token tree in tree-sitter-rust,
	// so no item node begins inside it. Refuse any anchor lying inside an unclosed
	// `name! {`, `name!(`, `name![`, or `macro_rules! ... {` body.
	if isInsideRustMacroBody(lines, line) {
		return nil
	}
	// Inner attributes, inner doc comments, plain comments never open an item.
	if isRustInnerAttrOrDoc(trimmed) || isRustPlainComment(trimmed) {
		return &BlockSpan{Start: line, End: line}
	}
	// Outer attribute `#[...]` or outer doc comment `///` / `/** ... */`.
	if isRustOuterAttrStart(trimmed) || isRustOuterDocLine(trimmed) {
		indent := leadingIndentCols(lines[line-1])
		// Check if preceded by another outer attribute or outer doc comment at the same indent.
		if hasPrecedingRustOuterAttr(lines, line, indent) {
			// Later outer attribute / doc comment of the same item: refused as single-line.
			return &BlockSpan{Start: line, End: line}
		}
		// Advance past this attribute/doc and any subsequent attributes/comments to the item line.
		itemLine := skipRustOuterAttrsAndComments(lines, line, indent)
		if itemLine == 0 || int(itemLine) > len(lines) {
			// Dangling attribute/doc with no following item -> single-line refusal.
			return &BlockSpan{Start: line, End: line}
		}
		itemSpan := resolveBraceBlock(langRust, lines, itemLine)
		if itemSpan == nil {
			return nil
		}
		if itemSpan.Start == itemSpan.End {
			if !isRustCompleteSingleLineItem(lines, itemLine) {
				return nil
			}
		}
		return &BlockSpan{Start: line, End: itemSpan.End}
	}
	return resolveBraceBlock(langRust, lines, line)
}

// isInsideRustMacroBody reports whether 1-based `line` lies inside an unclosed Rust
// macro invocation (`name! { ... }`, `name!(...)`, `name![...]`) or `macro_rules!` body.
// Pass 3 W3.
func isInsideRustMacroBody(lines []string, line uint32) bool {
	type openFrame struct {
		ch      byte
		isMacro bool
	}
	var st lexState
	st.lang = langRust
	var stack []openFrame
	for i := range int(line - 1) {
		raw := lines[i]
		events := st.scanLine(raw)
		if st.unsupportedLexed {
			return true
		}
		for _, ev := range events {
			switch ev.ch {
			case '{', '(', '[':
				isMacro := rustOpenerStartsMacroBody(raw, ev.bytePos, ev.ch)
				stack = append(stack, openFrame{ch: ev.ch, isMacro: isMacro})
			case '}', ')', ']':
				want := matchingOpener(ev.ch)
				if len(stack) == 0 || stack[len(stack)-1].ch != want {
					// Unbalanced prior brackets -> refuse conservatively.
					return true
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	for _, f := range stack {
		if f.isMacro {
			return true
		}
	}
	return false
}

// rustOpenerStartsMacroBody checks if an opener `{`, `(`, `[` at bytePos on raw opens a
// `name! { ... }` / `name!(...)` / `name![...]` macro invocation or `macro_rules! name {`.
func rustOpenerStartsMacroBody(raw string, bytePos int, ch byte) bool {
	before := strings.TrimRight(raw[:bytePos], " \t")
	if before == "" {
		return false
	}
	// Direct `name! {`, `name!(`, `name![` (ignoring `!=` which cannot precede opener directly without ident,
	// and `!` alone at end of prefix means macro bang).
	if before[len(before)-1] == '!' {
		// Check that there is an identifier immediately before `!` (or path `foo::bar!`).
		if len(before) >= 2 && isIdentByte(before[len(before)-2]) {
			return true
		}
	}
	// `macro_rules! foo {` (ident between `!` and `{`).
	if ch == '{' {
		end := len(before)
		for end > 0 && isIdentByte(before[end-1]) {
			end--
		}
		prefix := strings.TrimRight(before[:end], " \t")
		if strings.HasSuffix(prefix, "macro_rules!") {
			return true
		}
	}
	return false
}

func hasPrecedingRustOuterAttr(lines []string, line uint32, indent int) bool {
	// Walk backwards over comments and blank lines; stop at any non-comment code line.
	inBlockComment := false
	for i := int(line) - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if inBlockComment {
			if strings.Contains(t, "/*") {
				inBlockComment = false
				if isRustOuterDocLine(t) && leadingIndentCols(lines[i]) == indent {
					return true
				}
			}
			continue
		}
		if leadingIndentCols(lines[i]) != indent {
			// Could be interior of a multi-line attribute like `)]` or `feature = ...`.
			// If we hit a closing `]` of a multi-line outer attr, check above.
			if strings.HasSuffix(t, "]") || strings.HasSuffix(t, "*/") {
				// Scan up for matching `#[` or `/**`.
				if strings.HasSuffix(t, "*/") && !strings.Contains(t, "/*") {
					inBlockComment = true
					continue
				}
			}
			return false
		}
		if isRustOuterAttrStart(t) || isRustOuterDocLine(t) {
			return true
		}
		if isRustPlainComment(t) {
			continue
		}
		if strings.HasSuffix(t, "*/") && !strings.Contains(t, "/*") {
			inBlockComment = true
			continue
		}
		if t == ")]" || t == "]" {
			// Walk back to see if this closes a multi-line `#[` at `indent`.
			for j := i - 1; j >= 0; j-- {
				tj := strings.TrimSpace(lines[j])
				if leadingIndentCols(lines[j]) == indent && isRustOuterAttrStart(tj) {
					return true
				}
				if leadingIndentCols(lines[j]) < indent {
					return false
				}
			}
			return false
		}
		return false
	}
	return false
}

func skipRustOuterAttrsAndComments(lines []string, startLine uint32, indent int) uint32 {
	i := int(startLine - 1)
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			i++
			continue
		}
		if leadingIndentCols(lines[i]) != indent {
			return 0
		}
		if isRustOuterAttrStart(t) {
			// Advance past potentially multi-line #[...]
			endAttr := findBracketBalanceEnd(langRust, lines, uint32(i+1))
			if endAttr == 0 {
				return 0
			}
			i = int(endAttr)
			continue
		}
		if strings.HasPrefix(t, "///") && !strings.HasPrefix(t, "////") {
			i++
			continue
		}
		if strings.HasPrefix(t, "/**") && !strings.HasPrefix(t, "/***") && !strings.HasPrefix(t, "/**/") {
			if strings.Contains(t[3:], "*/") {
				i++
				continue
			}
			i++
			for i < len(lines) && !strings.Contains(lines[i], "*/") {
				i++
			}
			if i >= len(lines) {
				return 0
			}
			i++
			continue
		}
		if isRustPlainComment(t) {
			if strings.HasPrefix(t, "/*") && !strings.Contains(t[2:], "*/") {
				i++
				for i < len(lines) && !strings.Contains(lines[i], "*/") {
					i++
				}
				if i >= len(lines) {
					return 0
				}
			}
			i++
			continue
		}
		if startsWithCloser(t) || isRustInnerAttrOrDoc(t) {
			return 0
		}
		return uint32(i + 1)
	}
	return 0
}

// --- Brace-language block resolver (Go, JS, TS, JSX, Rust, C-family, etc.) ---

func startsWithCloser(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	return trimmed[0] == '}' || trimmed[0] == ')' || trimmed[0] == ']'
}

func isLineComment(lang langKind, trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*")
}

type lexState struct {
	lang             langKind
	inBlockComment   int // nesting depth (>0 means inside /* ... */)
	inDoubleQuote    bool
	inSingleQuote    bool
	inBacktick       bool
	inRustRawString  bool
	rustRawHashes    int
	unsupportedLexed bool // set on JS/C backslash-continued strings, etc.
}

func (s *lexState) inStringOrComment() bool {
	return s.inBlockComment > 0 || s.inDoubleQuote || s.inSingleQuote || s.inBacktick || s.inRustRawString || s.unsupportedLexed
}

type bracketEvent struct {
	ch      byte // '{', '}', '(', ')', '[', ']'
	bytePos int
}

// scanLineBrackets scans one line using language-aware lexing and returns structural bracket events.
func (s *lexState) scanLine(line string) []bracketEvent {
	var events []bracketEvent
	n := len(line)
	// prevToken tracks whether a JS/TS `/` starts a regex literal vs division.
	// At start of line/expression, `/` can start a regex literal.
	canStartRegex := true

	for i := 0; i < n; i++ {
		ch := line[i]
		var next byte
		if i+1 < n {
			next = line[i+1]
		}

		// Inside block comment.
		if s.inBlockComment > 0 {
			if s.lang == langRust && ch == '/' && next == '*' {
				s.inBlockComment++
				i++
				continue
			}
			if ch == '*' && next == '/' {
				s.inBlockComment--
				i++
			}
			continue
		}

		// Inside Rust raw string r#"..."#.
		if s.inRustRawString {
			if ch == '"' {
				hashes := 0
				for i+1+hashes < n && line[i+1+hashes] == '#' && hashes < s.rustRawHashes {
					hashes++
				}
				if hashes == s.rustRawHashes {
					s.inRustRawString = false
					i += hashes
				}
			}
			continue
		}

		// Inside double-quoted string.
		if s.inDoubleQuote {
			if ch == '\\' {
				if i+1 >= n {
					// Backslash line continuation inside string literal (JS/C): refuse conservatively.
					s.unsupportedLexed = true
					return events
				}
				i++
				continue
			}
			if ch == '"' {
				s.inDoubleQuote = false
				canStartRegex = false
			}
			continue
		}

		// Inside single-quoted string/char.
		if s.inSingleQuote {
			if ch == '\\' {
				if i+1 >= n {
					s.unsupportedLexed = true
					return events
				}
				i++
				continue
			}
			if ch == '\'' {
				s.inSingleQuote = false
				canStartRegex = false
			}
			continue
		}

		// Inside backtick string/template literal.
		if s.inBacktick {
			if s.lang != langGo && ch == '\\' {
				i++
				continue
			}
			if ch == '`' {
				s.inBacktick = false
				canStartRegex = false
			}
			continue
		}

		// Code mode: check comments first.
		if ch == '/' && next == '/' {
			break
		}
		if ch == '/' && next == '*' {
			s.inBlockComment = 1
			i++
			continue
		}

		// Rust raw string: r"...", r#"..."#, br#"..."#
		if s.lang == langRust && (ch == 'r' || (ch == 'b' && next == 'r')) {
			start := i
			if ch == 'b' {
				start++
			}
			if start < n && line[start] == 'r' && (i == 0 || !isIdentByte(line[i-1])) {
				j := start + 1
				hashes := 0
				for j < n && line[j] == '#' {
					hashes++
					j++
				}
				if j < n && line[j] == '"' {
					s.inRustRawString = true
					s.rustRawHashes = hashes
					i = j
					continue
				}
			}
		}

		// Double-quoted string.
		if ch == '"' {
			s.inDoubleQuote = true
			continue
		}

		// Backtick string (Go raw string, JS/TS template literal).
		if ch == '`' && (s.lang == langGo || s.lang == langJS || s.lang == langJSX) {
			s.inBacktick = true
			continue
		}

		// Single quote: in Rust, distinguish char literals ('a', '\n', '\x7f', '\u{...}') from lifetimes ('a, 'static).
		if ch == '\'' {
			if s.lang == langRust {
				if endIdx, isChar := scanRustCharLiteral(line, i); isChar {
					i = endIdx
					canStartRegex = false
					continue
				}
				// Lifetime ('a, 'static, '_): skip the apostrophe, do NOT enter string mode.
				continue
			}
			s.inSingleQuote = true
			continue
		}

		// JS/TS regex literal `/pattern/flags`.
		if (s.lang == langJS || s.lang == langJSX) && ch == '/' && canStartRegex {
			if endIdx, ok := scanJSRegexLiteral(line, i); ok {
				i = endIdx
				canStartRegex = false
				continue
			}
		}

		// Brackets.
		switch ch {
		case '{', '(', '[':
			events = append(events, bracketEvent{ch: ch, bytePos: i})
			canStartRegex = true
		case '}', ')', ']':
			events = append(events, bracketEvent{ch: ch, bytePos: i})
			canStartRegex = false
		case ' ', '\t':
			// preserve canStartRegex
		case '=', ',', ':', ';', '!', '&', '|', '?', '+', '-', '*', '%', '^', '~', '<', '>':
			canStartRegex = true
		default:
			if isIdentByte(ch) {
				// Read whole identifier to check regex-preceding keywords (`return`, `typeof`, etc.).
				j := i + 1
				for j < n && isIdentByte(line[j]) {
					j++
				}
				word := line[i:j]
				switch word {
				case "return", "case", "throw", "in", "of", "typeof", "void", "delete", "new", "yield", "await", "else", "do":
					canStartRegex = true
				default:
					canStartRegex = false
				}
				i = j - 1
			} else {
				canStartRegex = false
			}
		}
	}

	// Single-line quotes that remained unclosed at EOL (without backslash continuation)
	// are syntax errors or unmodeled multi-line strings (e.g. C++ raw strings): mark unsupported.
	if s.inDoubleQuote || s.inSingleQuote {
		s.unsupportedLexed = true
		s.inDoubleQuote = false
		s.inSingleQuote = false
	}
	return events
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '$'
}

// scanRustCharLiteral checks if line[pos] == '\” starts a Rust char literal.
func scanRustCharLiteral(line string, pos int) (int, bool) {
	n := len(line)
	if pos+2 >= n {
		return 0, false
	}
	if line[pos+1] == '\\' {
		// Escaped char: '\n', '\'', '\\', '\x7f', '\u{1F600}'
		j := pos + 2
		for j < n && j <= pos+14 {
			if line[j] == '\'' {
				return j, true
			}
			if line[j] == ' ' || line[j] == '\t' {
				return 0, false
			}
			j++
		}
		return 0, false
	}
	// Single UTF-8 rune followed immediately by `'`.
	r, size := decodeRuneAt(line, pos+1)
	if size > 0 && r != '\'' && pos+1+size < n && line[pos+1+size] == '\'' {
		return pos + 1 + size, true
	}
	return 0, false
}

// scanJSRegexLiteral checks if line[pos] == '/' is a valid single-line JS regex literal.
func scanJSRegexLiteral(line string, pos int) (int, bool) {
	n := len(line)
	if pos+1 >= n || line[pos+1] == '/' || line[pos+1] == '*' {
		return 0, false
	}
	inClass := false
	for i := pos + 1; i < n; i++ {
		ch := line[i]
		if ch == '\\' {
			i++
			continue
		}
		if inClass {
			if ch == ']' {
				inClass = false
			}
			continue
		}
		if ch == '[' {
			inClass = true
			continue
		}
		if ch == '/' {
			// Consume regex flags (g, i, m, s, u, v, y, d).
			end := i
			for end+1 < n && (line[end+1] >= 'a' && line[end+1] <= 'z') {
				end++
			}
			return end, true
		}
	}
	return 0, false
}

func matchingOpener(closer byte) byte {
	switch closer {
	case '}':
		return '{'
	case ')':
		return '('
	case ']':
		return '['
	default:
		return 0
	}
}

func findBracketBalanceEnd(lang langKind, lines []string, startLine uint32) uint32 {
	var state lexState
	state.lang = lang
	var stack []byte
	for i := int(startLine - 1); i < len(lines); i++ {
		events := state.scanLine(lines[i])
		for _, ev := range events {
			switch ev.ch {
			case '{', '(', '[':
				stack = append(stack, ev.ch)
			case '}', ')', ']':
				want := matchingOpener(ev.ch)
				if len(stack) == 0 || stack[len(stack)-1] != want {
					return 0
				}
				stack = stack[:len(stack)-1]
			}
		}
		if len(stack) == 0 && !state.inStringOrComment() {
			return uint32(i + 1)
		}
	}
	return 0
}

// isRustCompleteSingleLineItem reports whether itemLine ends a complete Rust item
// on that line (ends with `;` or `}` at depth 0) and is not followed by `where`, `{`,
// or a deeper-indented continuation.
// upstream: crates/ast/src/block.rs:179-197
func isRustCompleteSingleLineItem(lines []string, itemLine uint32) bool {
	var st lexState
	st.lang = langRust
	lastCodeByte, ok := st.lastCodeByteAtDepthZero(lines[itemLine-1])
	if !ok || (lastCodeByte != ';' && lastCodeByte != '}') {
		return false
	}
	if hasNextLineBlockContinuation(langRust, lines, itemLine, leadingIndentCols(lines[itemLine-1]), true, lines[itemLine-1]) {
		return false
	}
	// If itemLine ended with `;`, it cannot be followed by a deeper-indented continuation of the same item,
	// unless a `where` clause precedes it (already covered above). If it ended with `}` check `else`/etc.
	return true
}

// lastCodeByteAtDepthZero scans a single line in lang and returns the last non-whitespace
// code byte (outside comments/strings) at bracket depth 0, or ok=false if unbalanced/in-string.
func (s *lexState) lastCodeByteAtDepthZero(line string) (byte, bool) {
	n := len(line)
	var last byte
	depth := 0
	canStartRegex := true
	for i := 0; i < n; i++ {
		ch := line[i]
		var next byte
		if i+1 < n {
			next = line[i+1]
		}
		if s.inBlockComment > 0 {
			if s.lang == langRust && ch == '/' && next == '*' {
				s.inBlockComment++
				i++
				continue
			}
			if ch == '*' && next == '/' {
				s.inBlockComment--
				i++
			}
			continue
		}
		if s.inRustRawString {
			if ch == '"' {
				hashes := 0
				for i+1+hashes < n && line[i+1+hashes] == '#' && hashes < s.rustRawHashes {
					hashes++
				}
				if hashes == s.rustRawHashes {
					s.inRustRawString = false
					i += hashes
					if depth == 0 {
						last = '"'
					}
				}
			}
			continue
		}
		if s.inDoubleQuote {
			if ch == '\\' {
				if i+1 >= n {
					return 0, false
				}
				i++
				continue
			}
			if ch == '"' {
				s.inDoubleQuote = false
				canStartRegex = false
				if depth == 0 {
					last = '"'
				}
			}
			continue
		}
		if s.inSingleQuote {
			if ch == '\\' {
				if i+1 >= n {
					return 0, false
				}
				i++
				continue
			}
			if ch == '\'' {
				s.inSingleQuote = false
				canStartRegex = false
				if depth == 0 {
					last = '\''
				}
			}
			continue
		}
		if s.inBacktick {
			if s.lang != langGo && ch == '\\' {
				if i+1 >= n {
					return 0, false
				}
				i++
				continue
			}
			if ch == '`' {
				s.inBacktick = false
				canStartRegex = false
				if depth == 0 {
					last = '`'
				}
			}
			continue
		}
		if ch == '/' && next == '/' {
			break
		}
		if ch == '/' && next == '*' {
			s.inBlockComment = 1
			i++
			continue
		}
		if s.lang == langRust && (ch == 'r' || (ch == 'b' && next == 'r')) {
			start := i
			if ch == 'b' {
				start++
			}
			if start < n && line[start] == 'r' && (i == 0 || !isIdentByte(line[i-1])) {
				j := start + 1
				hashes := 0
				for j < n && line[j] == '#' {
					hashes++
					j++
				}
				if j < n && line[j] == '"' {
					s.inRustRawString = true
					s.rustRawHashes = hashes
					i = j
					continue
				}
			}
		}
		if ch == '"' {
			s.inDoubleQuote = true
			continue
		}
		if ch == '`' && (s.lang == langGo || s.lang == langJS || s.lang == langJSX) {
			s.inBacktick = true
			continue
		}
		if ch == '\'' {
			if s.lang == langRust {
				if endIdx, isChar := scanRustCharLiteral(line, i); isChar {
					i = endIdx
					if depth == 0 {
						last = '\''
					}
					continue
				}
				if depth == 0 {
					last = '\''
				}
				continue
			}
			s.inSingleQuote = true
			continue
		}
		if (s.lang == langJS || s.lang == langJSX) && ch == '/' && canStartRegex {
			if endIdx, ok := scanJSRegexLiteral(line, i); ok {
				i = endIdx
				canStartRegex = false
				if depth == 0 {
					last = '/'
				}
				continue
			}
		}
		switch ch {
		case '{', '(', '[':
			depth++
			canStartRegex = true
		case '}', ')', ']':
			if depth == 0 {
				return 0, false
			}
			depth--
			canStartRegex = false
			if depth == 0 {
				last = ch
			}
		case ' ', '\t':
		case '=', ',', ':', ';', '!', '&', '|', '?', '+', '-', '*', '%', '^', '~', '<', '>':
			canStartRegex = true
			if depth == 0 {
				last = ch
			}
		default:
			if isIdentByte(ch) {
				j := i + 1
				for j < n && isIdentByte(line[j]) {
					j++
				}
				word := line[i:j]
				switch word {
				case "return", "case", "throw", "in", "of", "typeof", "void", "delete", "new", "yield", "await", "else", "do":
					canStartRegex = true
				default:
					canStartRegex = false
				}
				if depth == 0 {
					last = line[j-1]
				}
				i = j - 1
			} else {
				canStartRegex = false
				if depth == 0 {
					last = ch
				}
			}
		}
	}
	if depth != 0 || s.inStringOrComment() {
		return 0, false
	}
	return last, true
}

// startsWithContinuationToken reports whether trimmed begins with a leading token
// that means this line is a continuation of a previous expression/statement rather
// than the start of a new syntax node (`crates/ast/src/block.rs:116`).
func startsWithContinuationToken(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	switch trimmed[0] {
	case '.', ',', ';', ':', '?', '+', '-', '*', '/', '%', '&', '|', '^', '=', '<', '>', '!':
		return true
	}
	// Note: leading `while` is NOT unconditionally a continuation token (cheap win: standalone
	// `while cond {` in Rust/JS/TS is a valid loop opener; `do { ... }\nwhile (...)` is handled
	// by the `do` block's next-line continuation check, and `} while` starts with `}`).
	for _, kw := range []string{"else", "catch", "finally", "where", "implements", "extends", "throws"} {
		if hasKeywordPrefix(trimmed, kw) {
			return true
		}
	}
	return false
}

func hasKeywordPrefix(trimmed, kw string) bool {
	if !strings.HasPrefix(trimmed, kw) {
		return false
	}
	return len(trimmed) == len(kw) || !isIdentByte(trimmed[len(kw)])
}

// isOnlyTerminatorTail checks that rest (after a matching closer on the closing line)
// contains only optional whitespace, optional single terminator (`;` or `,`), and an optional comment.
func isOnlyTerminatorTail(rest string) bool {
	t := strings.TrimSpace(rest)
	if t == "" {
		return true
	}
	if t[0] == ';' || t[0] == ',' {
		t = strings.TrimSpace(t[1:])
		if t == "" {
			return true
		}
	}
	if strings.HasPrefix(t, "//") {
		return true
	}
	if strings.HasPrefix(t, "/*") {
		if end := strings.Index(t[2:], "*/"); end >= 0 {
			return strings.TrimSpace(t[2+end+2:]) == ""
		}
	}
	return false
}

func resolveBraceBlock(lang langKind, lines []string, line uint32) *BlockSpan {
	trimmed := strings.TrimSpace(lines[line-1])
	if trimmed == "" || startsWithCloser(trimmed) {
		return nil
	}
	if isLineComment(lang, trimmed) {
		return nil
	}
	// Pass 3 W8: refuse CSS files/spans containing template or preprocessor syntax
	// (`{{`, `<%`, `@apply`) that tree-sitter-css parses as ERROR nodes.
	if lang == langCSS && cssHasTemplateOrPreprocessor(lines) {
		return nil
	}
	// Condition (3): anchor line itself must not start with `.` / operator / continuation keyword.
	// Exception: in CSS, `.class`, `:pseudo`, `*` universal selector, `&` nesting are valid rule selectors,
	// while Pass 3 W7 refuses a leading `,` (continuation of a selector list) and leading combinators
	// `>`, `+`, `~` when the previous non-blank/non-comment line does not end a rule (`}` or `;`).
	if lang == langCSS {
		if trimmed[0] == ',' {
			return nil
		}
		if (trimmed[0] == '>' || trimmed[0] == '+' || trimmed[0] == '~') && !cssPrevLineEndsRule(lines, line) {
			return nil
		}
	} else if startsWithContinuationToken(trimmed) {
		return nil
	}
	// JSX opening tag (e.g. `<div className={x}>` or `<ul>`): cannot prove matching
	// closing tag without a JSX parser -> refuse rather than mis-resolve.
	if lang == langJSX && strings.HasPrefix(trimmed, "<") {
		return nil
	}
	// Decorator / annotation line in JS/TS/etc. (`@Component`): refuse without parser.
	// In CSS, `@media`/`@keyframes`/`@supports`/`@layer` are valid at-rule blocks!
	if lang != langCSS && strings.HasPrefix(trimmed, "@") {
		return nil
	}
	// Finding N1(c): refuse `case ...` / `default ...` anchors (a `case 1: { ... }\n break;`
	// tree-sitter switch_case node continues past `}` to `break;`).
	if lang != langCSS && lang != langJSON && (hasKeywordPrefix(trimmed, "case") || hasKeywordPrefix(trimmed, "default")) {
		return nil
	}
	// Finding N1(a) & N1(e) & N5(CSS): refuse continuation anchors whose previous non-blank,
	// non-comment line ends in an operator, `,`, `(`, `[`, `=`, `where`, a JS/TS `@decorator`,
	// or a Rust outer attribute `#[...]` / doc comment before a non-item line (e.g. match arm).
	if hasPrecedingContinuationLine(lang, lines, line) {
		return nil
	}

	// Ensure the anchor line itself is not inside a multi-line comment or raw/template string
	// opened on an earlier line.
	var pre lexState
	pre.lang = lang
	for i := range int(line - 1) {
		pre.scanLine(lines[i])
		if pre.unsupportedLexed {
			return nil
		}
	}
	if pre.inStringOrComment() {
		return nil
	}
	var state lexState
	state.lang = lang
	anchorEvents := state.scanLine(lines[line-1])
	if state.unsupportedLexed {
		return nil
	}

	var stack []byte
	for _, ev := range anchorEvents {
		switch ev.ch {
		case '{', '(', '[':
			stack = append(stack, ev.ch)
		case '}', ')', ']':
			want := matchingOpener(ev.ch)
			if len(stack) == 0 || stack[len(stack)-1] != want {
				// Unmatched closer on anchor line (e.g. continuation line) -> refuse.
				return nil
			}
			stack = stack[:len(stack)-1]
		}
	}

	// If anchor line leaves no unclosed bracket, do NOT forward-scan.
	if len(stack) == 0 {
		if state.inStringOrComment() {
			// Anchor opened a multi-line string/comment with no structural bracket -> refuse.
			return nil
		}
		// Only report single-line if the line does NOT continue onto the next line
		// (e.g. Allman `{`, `where`, chained `.foo`, deeper-indented continuation).
		if hasNextLineBlockContinuation(lang, lines, line, leadingIndentCols(lines[line-1]), false, lines[line-1]) {
			return nil
		}
		// Also check if anchor line ends with a trailing operator / `=` / `=>` / `\`
		// indicating a multi-line statement without brackets.
		if lineEndsWithContinuation(lang, lines[line-1]) {
			return nil
		}
		return &BlockSpan{Start: line, End: line}
	}

	// Condition (1): the anchor line's last unclosed bracket MUST be `{` (the body opener),
	// and it must be the ONLY unclosed bracket on the anchor line (so `promise.then(() => {`
	// where `(` is also open, or `if foo(a,` where `(` is open, or `const f = (` where `(`
	// is open are refused rather than returning a partial span!).
	// Also allow JSON/Go/JS/TS/Rust composite literals whose sole unclosed bracket is `{`
	// (or `[` only for JSON arrays where nothing can chain after `]`).
	anchorOpener := stack[len(stack)-1]
	if len(stack) != 1 || anchorOpener != '{' {
		return nil
	}
	// Finding N1(b): refuse when two statements/members share the anchor line — i.e., a `;`,
	// `}`, or (in JSON) `,` appears at bracket depth 0 before the unclosed `{` on the anchor line.
	if anchorHasPriorStmtBeforeOpenBrace(lang, lines[line-1], anchorEvents) {
		return nil
	}

	// Pass 3 W2: an anchor whose first token is `{` (e.g. `{` alone on its line as the body
	// of a multi-line `if` in Rust) represents ONLY that block node, NOT a following `else` chain.
	// Refuse when anything other than a terminator follows its matching `}` on the closing line.
	bareOpenBrace := trimmed[0] == '{'

	// Scan forward until the anchor's `{` closes.
	for i := int(line); i < len(lines); i++ {
		// Pass 3 W8: in CSS, a custom property whose value contains a brace block
		// (e.g. `--a: { b };`) causes tree-sitter-css to report a parse error on the rule.
		if lang == langCSS && cssLineHasBracePropertyValue(lines[i]) {
			return nil
		}
		events := state.scanLine(lines[i])
		if state.unsupportedLexed {
			return nil
		}
		closedPrimaryBrace := false
		for idx, ev := range events {
			switch ev.ch {
			case '{', '(', '[':
				if closedPrimaryBrace {
					closedPrimaryBrace = false
				}
				stack = append(stack, ev.ch)
			case '}', ')', ']':
				want := matchingOpener(ev.ch)
				if len(stack) == 0 || stack[len(stack)-1] != want {
					return nil
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					if ev.ch != '}' {
						return nil
					}
					rest := strings.TrimSpace(lines[i][ev.bytePos+1:])
					if idx+1 < len(events) {
						if !bareOpenBrace && isContinuationClause(rest) {
							closedPrimaryBrace = true
						} else {
							// Something else opened after our closing brace on the same line
							// (or bare `{` followed by `} else {` — W2).
							return nil
						}
					} else {
						// Condition (1): nothing but an optional terminator (`;`/`,`) and comment may follow `}`.
						if !isOnlyTerminatorTail(rest) {
							return nil
						}
					}
				}
			}
		}
		if len(stack) == 0 && !state.inStringOrComment() {
			endLine := uint32(i + 1)
			// Condition (2): refuse if the next non-blank line is a continuation
			// (leading `.`, `?.`, operator, `else`/`catch`/`finally`/`while`, `where`, `{`, `implements`, `throws`, `:`,
			// or JS/TS ASI continuation `;`, `(`, `[`, `` ` ``).
			if hasNextLineBlockContinuation(lang, lines, endLine, leadingIndentCols(lines[line-1]), false, lines[i]) {
				return nil
			}
			return &BlockSpan{Start: line, End: endLine}
		}
	}
	return nil
}

// cssHasTemplateOrPreprocessor reports whether a CSS file contains template/preprocessor
// markers (`{{`, `<%`, `@apply`) outside comments/strings that tree-sitter-css rejects (W8).
func cssHasTemplateOrPreprocessor(lines []string) bool {
	inComment := false
	for _, raw := range lines {
		n := len(raw)
		inSingle := false
		inDouble := false
		for i := 0; i < n; i++ {
			ch := raw[i]
			var next byte
			if i+1 < n {
				next = raw[i+1]
			}
			if inComment {
				if ch == '*' && next == '/' {
					inComment = false
					i++
				}
				continue
			}
			if inSingle {
				if ch == '\\' {
					i++
					continue
				}
				if ch == '\'' {
					inSingle = false
				}
				continue
			}
			if inDouble {
				if ch == '\\' {
					i++
					continue
				}
				if ch == '"' {
					inDouble = false
				}
				continue
			}
			if ch == '/' && next == '*' {
				inComment = true
				i++
				continue
			}
			if ch == '\'' {
				inSingle = true
				continue
			}
			if ch == '"' {
				inDouble = true
				continue
			}
			if (ch == '{' && next == '{') || (ch == '<' && next == '%') {
				return true
			}
			if ch == '@' && strings.HasPrefix(raw[i:], "@apply") {
				end := i + len("@apply")
				if end == n || raw[end] == ' ' || raw[end] == '\t' {
					return true
				}
			}
		}
	}
	return false
}

// cssLineHasBracePropertyValue detects CSS custom property declarations carrying a brace
// block in the value (e.g. `--a: { b };`), which tree-sitter-css parses as an ERROR node (W8).
func cssLineHasBracePropertyValue(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "--") {
		return false
	}
	colon := strings.IndexByte(t, ':')
	if colon < 0 {
		return false
	}
	val := t[colon+1:]
	return strings.Contains(val, "{")
}

// cssPrevLineEndsRule reports whether the previous non-blank, non-comment line in CSS
// ends a rule or statement (`}` or `;`) so a leading combinator (`>`, `+`, `~`) cannot be
// continuing a multi-line selector (W7).
func cssPrevLineEndsRule(lines []string, line uint32) bool {
	if line <= 1 {
		return true
	}
	inBlockComment := false
	for i := int(line) - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if inBlockComment {
			if strings.Contains(t, "/*") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(t, "/*") && strings.HasSuffix(t, "*/") {
			continue
		}
		if strings.HasSuffix(t, "*/") && !strings.Contains(t, "/*") {
			inBlockComment = true
			continue
		}
		last, ok := lastNonCommentCodeByte(langCSS, lines[i])
		return ok && (last == '}' || last == ';')
	}
	return true
}

// anchorHasPriorStmtBeforeOpenBrace reports whether a statement/member separator (`;`, `}`,
// or `,` in JSON) occurs at bracket depth 0 on the anchor line before the final unclosed `{`.
// Finding N1(b): catches `g(); if c {`, `foo(); if (x) {`, `const re = /[{]/; const obj = {`,
// `let t = S { a: 1 }; let u = S {`, and JSON `"g": 1, "h": {`.
func anchorHasPriorStmtBeforeOpenBrace(lang langKind, line string, events []bracketEvent) bool {
	openPos := -1
	depth := 0
	for _, ev := range events {
		switch ev.ch {
		case '{', '(', '[':
			if depth == 0 && ev.ch == '{' {
				openPos = ev.bytePos
			}
			depth++
		case '}', ')', ']':
			depth--
		}
	}
	if openPos <= 0 {
		return false
	}
	var st lexState
	st.lang = lang
	n := openPos
	canStartRegex := true
	d := 0
	for i := 0; i < n; i++ {
		ch := line[i]
		var next byte
		if i+1 < n {
			next = line[i+1]
		}
		if st.inBlockComment > 0 {
			if lang == langRust && ch == '/' && next == '*' {
				st.inBlockComment++
				i++
				continue
			}
			if ch == '*' && next == '/' {
				st.inBlockComment--
				i++
			}
			continue
		}
		if st.inRustRawString {
			if ch == '"' {
				hashes := 0
				for i+1+hashes < n && line[i+1+hashes] == '#' && hashes < st.rustRawHashes {
					hashes++
				}
				if hashes == st.rustRawHashes {
					st.inRustRawString = false
					i += hashes
				}
			}
			continue
		}
		if st.inDoubleQuote {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				st.inDoubleQuote = false
				canStartRegex = false
			}
			continue
		}
		if st.inSingleQuote {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '\'' {
				st.inSingleQuote = false
				canStartRegex = false
			}
			continue
		}
		if st.inBacktick {
			if lang != langGo && ch == '\\' {
				i++
				continue
			}
			if ch == '`' {
				st.inBacktick = false
				canStartRegex = false
			}
			continue
		}
		if ch == '/' && next == '/' {
			break
		}
		if ch == '/' && next == '*' {
			st.inBlockComment = 1
			i++
			continue
		}
		if lang == langRust && (ch == 'r' || (ch == 'b' && next == 'r')) {
			start := i
			if ch == 'b' {
				start++
			}
			if start < n && line[start] == 'r' && (i == 0 || !isIdentByte(line[i-1])) {
				j := start + 1
				hashes := 0
				for j < n && line[j] == '#' {
					hashes++
					j++
				}
				if j < n && line[j] == '"' {
					st.inRustRawString = true
					st.rustRawHashes = hashes
					i = j
					continue
				}
			}
		}
		if ch == '"' {
			st.inDoubleQuote = true
			continue
		}
		if ch == '`' && (lang == langGo || lang == langJS || lang == langJSX) {
			st.inBacktick = true
			continue
		}
		if ch == '\'' {
			if lang == langRust {
				if endIdx, isChar := scanRustCharLiteral(line, i); isChar {
					i = endIdx
					canStartRegex = false
					continue
				}
				continue
			}
			st.inSingleQuote = true
			continue
		}
		if (lang == langJS || lang == langJSX) && ch == '/' && canStartRegex {
			if endIdx, ok := scanJSRegexLiteral(line, i); ok {
				i = endIdx
				canStartRegex = false
				continue
			}
		}
		switch ch {
		case '{', '(', '[':
			d++
			canStartRegex = true
		case '}', ')', ']':
			if d > 0 {
				d--
			}
			if d == 0 && ch == '}' {
				// Pass 3 W5: after any balanced `{...}` at depth 0 on the anchor line
				// (e.g. `for i := 0; i < 3; i++ { b() }; for {`), a following `;` or further
				// code before the final unclosed `{` means two statements on one line: refuse.
				if !isControlHeaderWithInit(lang, strings.TrimSpace(line[:openPos])) {
					return true
				}
				// Even in Go control headers, a balanced `{...}` followed by `;` or more code
				// is only valid if `{...}` is a composite literal/type inside the header, never
				// a completed block statement followed by `;` + another statement.
				rest := strings.TrimSpace(line[i+1 : openPos])
				if strings.HasPrefix(rest, ";") || strings.Contains(rest, ";") {
					return true
				}
			}
			canStartRegex = false
		case ';':
			if d == 0 {
				if !(lang == langGo && isControlHeaderWithInit(lang, strings.TrimSpace(line[:openPos]))) {
					return true
				}
			}
			canStartRegex = true
		case ',':
			if d == 0 && lang == langJSON {
				return true
			}
			canStartRegex = true
		case ' ', '\t':
		case '=', ':', '!', '&', '|', '?', '+', '-', '*', '%', '^', '~', '<', '>':
			canStartRegex = true
		default:
			if isIdentByte(ch) {
				j := i + 1
				for j < n && isIdentByte(line[j]) {
					j++
				}
				word := line[i:j]
				switch word {
				case "return", "case", "throw", "in", "of", "typeof", "void", "delete", "new", "yield", "await", "else", "do":
					canStartRegex = true
				default:
					canStartRegex = false
				}
				i = j - 1
			} else {
				canStartRegex = false
			}
		}
	}
	return false
}

func isControlHeaderWithInit(lang langKind, trimmedPrefix string) bool {
	if lang != langGo {
		return false
	}
	return hasKeywordPrefix(trimmedPrefix, "if") ||
		hasKeywordPrefix(trimmedPrefix, "for") ||
		hasKeywordPrefix(trimmedPrefix, "switch") ||
		hasKeywordPrefix(trimmedPrefix, "select")
}

// hasPrecedingContinuationLine reports whether the previous non-blank, non-comment line
// leaves a construct open so `line` is a continuation/tail rather than the start of a
// standalone block (findings N1(a), N1(e), N5(CSS), and JS/TS decorated classes).
func hasPrecedingContinuationLine(lang langKind, lines []string, line uint32) bool {
	if line <= 1 {
		return false
	}
	prevIdx := -1
	inBlockComment := false
	for i := int(line) - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if inBlockComment {
			if strings.Contains(t, "/*") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.HasPrefix(t, "/*") && strings.HasSuffix(t, "*/") {
			continue
		}
		if strings.HasSuffix(t, "*/") && !strings.Contains(t, "/*") {
			inBlockComment = true
			continue
		}
		prevIdx = i
		break
	}
	if prevIdx < 0 {
		return false
	}
	prevLine := lines[prevIdx]
	prevTrim := strings.TrimSpace(prevLine)

	if (lang == langJS || lang == langJSX) && strings.HasPrefix(prevTrim, "@") {
		return true
	}
	if lang == langRust && (isRustOuterAttrStart(prevTrim) || prevTrim == ")]" || prevTrim == "]") {
		curTrim := strings.TrimSpace(lines[line-1])
		if strings.Contains(curTrim, "=>") && !isRustItemStart(curTrim) {
			return true
		}
	}
	if lang == langRust && (prevTrim == "where" || strings.HasSuffix(prevTrim, " where")) {
		return true
	}

	var st lexState
	st.lang = lang
	last, ok := st.lastCodeByteAtDepthZero(prevLine)
	if !ok {
		// prevLine had unbalanced brackets (e.g. `b) &&` closing an earlier `(` while
		// ending with `&&`!). Still inspect the last non-comment code byte of prevLine.
		if tail, hasTail := lastNonCommentCodeByte(lang, prevLine); hasTail {
			switch tail {
			case '=', '+', '-', '*', '/', '%', '&', '|', '^', '?', '\\', '<', '>', '.':
				return true
			case ',':
				if lang == langCSS {
					return true
				}
			}
		}
		return false
	}
	switch last {
	case '=', '+', '-', '*', '/', '%', '&', '|', '^', '?', '\\', '<', '>', '.':
		return true
	case ',':
		if lang == langCSS {
			return true
		}
	}
	return false
}

// lastNonCommentCodeByte returns the last non-whitespace byte outside comments/strings
// on line, regardless of whether brackets balance on this single line.
func lastNonCommentCodeByte(lang langKind, line string) (byte, bool) {
	var st lexState
	st.lang = lang
	var last byte
	has := false
	n := len(line)
	for i := 0; i < n; i++ {
		ch := line[i]
		var next byte
		if i+1 < n {
			next = line[i+1]
		}
		if st.inBlockComment > 0 {
			if lang == langRust && ch == '/' && next == '*' {
				st.inBlockComment++
				i++
				continue
			}
			if ch == '*' && next == '/' {
				st.inBlockComment--
				i++
			}
			continue
		}
		if st.inDoubleQuote {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				st.inDoubleQuote = false
				last, has = '"', true
			}
			continue
		}
		if st.inSingleQuote {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '\'' {
				st.inSingleQuote = false
				last, has = '\'', true
			}
			continue
		}
		if ch == '/' && next == '/' {
			break
		}
		if ch == '/' && next == '*' {
			st.inBlockComment = 1
			i++
			continue
		}
		if ch == '"' {
			st.inDoubleQuote = true
			continue
		}
		if ch == '\'' && lang != langRust {
			st.inSingleQuote = true
			continue
		}
		if ch != ' ' && ch != '\t' && ch != '\r' {
			last, has = ch, true
		}
	}
	return last, has
}

func isRustItemStart(trimmed string) bool {
	for _, kw := range []string{"pub", "fn", "async", "unsafe", "const", "static", "extern", "struct", "enum", "union", "trait", "impl", "mod", "type", "use", "macro_rules"} {
		if hasKeywordPrefix(trimmed, kw) {
			return true
		}
	}
	return false
}

func lineEndsWithContinuation(lang langKind, line string) bool {
	var st lexState
	st.lang = lang
	last, ok := st.lastCodeByteAtDepthZero(line)
	if !ok {
		return true
	}
	switch last {
	case '=', '+', '-', '*', '/', '%', '&', '|', '^', '?', ':', ',', '.', '\\', '<', '>':
		return true
	}
	return false
}

func isContinuationClause(rest string) bool {
	for _, kw := range []string{"else", "catch", "finally", "while"} {
		if hasKeywordPrefix(rest, kw) {
			return true
		}
	}
	return false
}

// hasNextLineBlockContinuation checks condition (2): whether the next non-blank, non-comment
// line after endLine looks like a continuation of the construct ending at endLine.
// Cheap wins:
//   - Skips whole multi-line `/* ... */` / `/** ... */` block comments instead of reading
//     interior `* text` lines as operators.
//   - In CSS, does not treat selector starter characters (`.`, `*`, `>`, `+`, `&`, `:`) on
//     the next rule as expression operators.
//
// Pass 3 W1: in JS/TS/JSX, when endLine ends in `}` without `;`, refuse when the next
// non-blank, non-comment line starts with `;`, `(`, `[`, or a backtick.
func hasNextLineBlockContinuation(lang langKind, lines []string, endLine uint32, anchorIndent int, checkDeeperIndent bool, endLineRaw string) bool {
	inBlockComment := 0
	for i := int(endLine); i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if inBlockComment > 0 {
			inBlockComment = advanceBlockCommentDepth(lang, lines[i], inBlockComment)
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.HasPrefix(t, "/*") {
			depth := advanceBlockCommentDepth(lang, t, 0)
			if depth > 0 {
				inBlockComment = depth
				continue
			}
			// Single-line block comment: check if any code follows `*/` on the same line.
			rest := stripLeadingBlockComments(lang, t)
			if rest == "" || strings.HasPrefix(rest, "//") {
				continue
			}
			t = rest
		}
		col := leadingIndentCols(lines[i])
		if checkDeeperIndent && col > anchorIndent {
			return true
		}
		// Cheap win: in CSS, the next rule regularly starts with `.class`, `*`, `:pseudo`,
		// `&`, `>`, `+`, `~`, `[attr]`; those are selectors, not expression continuations.
		if lang == langCSS {
			return t[0] == ',' || t[0] == '{'
		}
		// Pass 3 W1: in JS/TS/JSX, ASI does not end a statement ending in `}` without `;`
		// when the next non-blank, non-comment line starts with `;`, `(`, `[`, or backtick.
		if (lang == langJS || lang == langJSX) && (t[0] == ';' || t[0] == '(' || t[0] == '[' || t[0] == '`') {
			if last, ok := lastNonCommentCodeByte(lang, endLineRaw); ok && last == '}' {
				return true
			}
		}
		// Leading `.`, `?.`, operator, `{`, `:` on next line.
		if t[0] == '.' || t[0] == '{' || t[0] == ':' ||
			strings.HasPrefix(t, "?.") ||
			strings.HasPrefix(t, "&&") || strings.HasPrefix(t, "||") ||
			strings.HasPrefix(t, "??") || strings.HasPrefix(t, "=>") ||
			strings.HasPrefix(t, "->") ||
			t[0] == '+' || t[0] == '-' || t[0] == '*' || t[0] == '/' ||
			t[0] == '?' || t[0] == '&' || t[0] == '|' || t[0] == '^' || t[0] == '%' {
			if t[0] == '*' && col <= anchorIndent {
				return false
			}
			return true
		}
		// Continuation keywords (`else`, `catch`, `finally`, `while`, `where`, `implements`, `throws`).
		if col == anchorIndent && isContinuationClause(t) {
			return true
		}
		for _, kw := range []string{"where", "implements", "throws", "extends"} {
			if hasKeywordPrefix(t, kw) {
				return true
			}
		}
		return false
	}
	return false
}

// advanceBlockCommentDepth updates block-comment nesting depth across `line`
// (supporting nested `/* /* */ */` in Rust).
func advanceBlockCommentDepth(lang langKind, line string, depth int) int {
	n := len(line)
	for i := 0; i+1 < n; i++ {
		if line[i] == '/' && line[i+1] == '*' {
			if depth == 0 || lang == langRust {
				depth++
			}
			i++
		} else if line[i] == '*' && line[i+1] == '/' {
			if depth > 0 {
				depth--
			}
			i++
		}
	}
	return depth
}

// stripLeadingBlockComments strips one or more closed leading `/* ... */` comments
// from trimmed line `t` and returns the remaining trimmed code on that line.
func stripLeadingBlockComments(lang langKind, t string) string {
	for strings.HasPrefix(t, "/*") {
		depth := 1
		end := -1
		for i := 2; i+1 < len(t); i++ {
			if lang == langRust && t[i] == '/' && t[i+1] == '*' {
				depth++
				i++
			} else if t[i] == '*' && t[i+1] == '/' {
				depth--
				i++
				if depth == 0 {
					end = i + 1
					break
				}
			}
		}
		if end < 0 {
			return ""
		}
		t = strings.TrimSpace(t[end:])
	}
	return t
}

// --- Python block resolver ---

func leadingIndentCols(line string) int {
	cols := 0
	for i := range len(line) {
		switch line[i] {
		case ' ':
			cols++
		case '\t':
			cols += 4 - cols%4
		default:
			return cols
		}
	}
	return cols
}

type pyState struct {
	tripleQuote byte // 0, '\'', or '"'
	bracketDep  int
}

// scanPythonLine advances pyState over one line and returns the stripped code portion
// (with strings/comments masked) to check for trailing `:`.
func (ps *pyState) scanLine(line string) (endsWithColon bool, codeAfterColon bool, ok bool) {
	n := len(line)
	lastNonSpace := byte(0)
	sawColonAtDepth0 := false
	hasCodeAfterColon := false

	for i := 0; i < n; i++ {
		ch := line[i]
		if ps.tripleQuote != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == ps.tripleQuote && i+2 < n && line[i+1] == ps.tripleQuote && line[i+2] == ps.tripleQuote {
				ps.tripleQuote = 0
				i += 2
				lastNonSpace = 's'
			}
			continue
		}
		if ch == '#' {
			break
		}
		if ch == '\'' || ch == '"' {
			if i+2 < n && line[i+1] == ch && line[i+2] == ch {
				ps.tripleQuote = ch
				i += 2
				lastNonSpace = 's'
				if sawColonAtDepth0 && ps.bracketDep == 0 {
					hasCodeAfterColon = true
				}
				continue
			}
			// Single-line string.
			quote := ch
			i++
			closed := false
			for i < n {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == quote {
					closed = true
					break
				}
				i++
			}
			if !closed {
				return false, false, false
			}
			lastNonSpace = 's'
			if sawColonAtDepth0 && ps.bracketDep == 0 {
				hasCodeAfterColon = true
			}
			continue
		}
		switch ch {
		case '(', '[', '{':
			ps.bracketDep++
			lastNonSpace = ch
			if sawColonAtDepth0 {
				hasCodeAfterColon = true
			}
		case ')', ']', '}':
			if ps.bracketDep == 0 {
				return false, false, false
			}
			ps.bracketDep--
			lastNonSpace = ch
			if sawColonAtDepth0 && ps.bracketDep == 0 {
				hasCodeAfterColon = true
			}
		case ' ', '\t':
		case ':':
			lastNonSpace = ':'
			if ps.bracketDep == 0 {
				sawColonAtDepth0 = true
				hasCodeAfterColon = false
			}
		default:
			lastNonSpace = ch
			if sawColonAtDepth0 && ps.bracketDep == 0 {
				hasCodeAfterColon = true
			}
		}
	}
	return lastNonSpace == ':' && ps.bracketDep == 0 && ps.tripleQuote == 0, hasCodeAfterColon, true
}

func pyKeyword(trimmed string) string {
	for _, kw := range []string{"async def", "async for", "async with", "def", "class", "if", "for", "while", "with", "try", "match"} {
		if strings.HasPrefix(trimmed, kw) {
			rest := trimmed[len(kw):]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == ':' || rest[0] == '(' {
				return kw
			}
		}
	}
	return ""
}

func pyContinuationKeyword(headerKw, trimmed string) bool {
	var allowed []string
	switch headerKw {
	case "if":
		allowed = []string{"elif", "else"}
	case "for", "async for", "while":
		allowed = []string{"else"}
	case "try":
		allowed = []string{"except", "else", "finally"}
	default:
		return false
	}
	for _, kw := range allowed {
		if strings.HasPrefix(trimmed, kw) {
			rest := trimmed[len(kw):]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == ':' || rest[0] == '*' {
				return true
			}
		}
	}
	return false
}

func resolvePythonBlock(lines []string, line uint32) *BlockSpan {
	trimmed := strings.TrimSpace(lines[line-1])
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || startsWithCloser(trimmed) {
		return nil
	}
	// Ensure anchor line is not inside a prior triple-quoted string or open bracket.
	var pre pyState
	for i := range int(line - 1) {
		if _, _, ok := pre.scanLine(lines[i]); !ok {
			return nil
		}
	}
	if pre.tripleQuote != 0 || pre.bracketDep != 0 {
		return nil
	}

	baseIndent := leadingIndentCols(lines[line-1])

	// Handle decorators (`@...`): only the FIRST decorator opens the item.
	headerLine := line
	if strings.HasPrefix(trimmed, "@") {
		// Check if preceded by another decorator at baseIndent.
		if hasPrecedingPyDecorator(lines, line, baseIndent) {
			return &BlockSpan{Start: line, End: line}
		}
		// Walk past this decorator (and any multi-line args) and any subsequent decorators to def/class.
		i := int(line - 1)
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if t == "" || strings.HasPrefix(t, "#") {
				i++
				continue
			}
			if leadingIndentCols(lines[i]) != baseIndent {
				return nil
			}
			if strings.HasPrefix(t, "@") {
				var ds pyState
				for i < len(lines) {
					if _, _, ok := ds.scanLine(lines[i]); !ok {
						return nil
					}
					i++
					if ds.bracketDep == 0 && ds.tripleQuote == 0 {
						break
					}
				}
				continue
			}
			kw := pyKeyword(t)
			if kw == "def" || kw == "async def" || kw == "class" {
				headerLine = uint32(i + 1)
				break
			}
			return nil
		}
		if headerLine == line {
			return nil
		}
	}

	headerTrimmed := strings.TrimSpace(lines[headerLine-1])
	kw := pyKeyword(headerTrimmed)
	if kw == "" {
		// Not a compound statement header. Refuse multi-line bracketed expressions
		// conservatively because the closing bracket may be followed by chained calls
		// (`.method()`), operators, or backslash continuation (`crates/ast/src/block.rs:94-141`).
		if headerLine != line {
			return nil
		}
		var ps pyState
		_, _, ok := ps.scanLine(lines[line-1])
		if !ok {
			return nil
		}
		if ps.bracketDep == 0 && ps.tripleQuote == 0 {
			// Single-line statement (e.g. `x = 1`): refuse as single-line, unless it has backslash continuation.
			if strings.HasSuffix(strings.TrimRightFunc(lines[line-1], unicode.IsSpace), "\\") {
				return nil
			}
			return &BlockSpan{Start: line, End: line}
		}
		return nil
	}

	// Scan compound statement header (possibly multi-line signature) until trailing `:` at depth 0.
	var ps pyState
	curIdx := int(headerLine - 1)
	headerEndIdx := -1
	for curIdx < len(lines) {
		endsColon, afterColon, ok := ps.scanLine(lines[curIdx])
		if !ok {
			return nil
		}
		if ps.bracketDep == 0 && ps.tripleQuote == 0 {
			if endsColon && !afterColon {
				headerEndIdx = curIdx
				break
			}
			if afterColon {
				// One-line suite (e.g. `def f(): pass`): single-line if headerLine == line.
				if curIdx == int(line-1) {
					return &BlockSpan{Start: line, End: line}
				}
				return &BlockSpan{Start: line, End: uint32(curIdx + 1)}
			}
			// Backslash line continuation in header — refuse.
			return nil
		}
		curIdx++
	}
	if headerEndIdx < 0 {
		return nil
	}

	// Scan suite body and any continuation clauses (elif/else/except/finally) at baseIndent.
	lastBodyLine := uint32(0)
	scanSuite := func(startIdx int) (nextIdx int, foundBody bool, ok bool) {
		var bodyState pyState
		idx := startIdx
		for idx < len(lines) {
			raw := lines[idx]
			t := strings.TrimSpace(raw)
			if bodyState.tripleQuote != 0 || bodyState.bracketDep != 0 {
				// Inside a multi-line string/docstring or bracketed expr: even column-0 lines belong to the suite!
				if _, _, scanOK := bodyState.scanLine(raw); !scanOK {
					return idx, foundBody, false
				}
				lastBodyLine = uint32(idx + 1)
				foundBody = true
				idx++
				continue
			}
			if t == "" {
				idx++
				continue
			}
			if strings.HasPrefix(t, "#") {
				// Finding N4: tree-sitter-python includes trailing comments indented deeper
				// than the compound-statement header inside the suite/block node.
				if leadingIndentCols(raw) > baseIndent {
					lastBodyLine = uint32(idx + 1)
				}
				idx++
				continue
			}
			col := leadingIndentCols(raw)
			if col <= baseIndent {
				return idx, foundBody, true
			}
			if _, _, scanOK := bodyState.scanLine(raw); !scanOK {
				return idx, foundBody, false
			}
			lastBodyLine = uint32(idx + 1)
			foundBody = true
			idx++
		}
		if bodyState.tripleQuote != 0 || bodyState.bracketDep != 0 {
			return idx, foundBody, false
		}
		return idx, foundBody, true
	}

	idx, foundBody, ok := scanSuite(headerEndIdx + 1)
	if !ok || !foundBody {
		return nil
	}

	// Check for continuation clauses (`elif`, `else`, `except`, `finally`) at baseIndent.
	for idx < len(lines) {
		t := strings.TrimSpace(lines[idx])
		if leadingIndentCols(lines[idx]) != baseIndent || !pyContinuationKeyword(kw, t) {
			break
		}
		// Scan continuation clause header (possibly multi-line).
		var cs pyState
		clauseHeaderEnd := -1
		for idx < len(lines) {
			endsColon, afterColon, scanOK := cs.scanLine(lines[idx])
			if !scanOK {
				return nil
			}
			if cs.bracketDep == 0 && cs.tripleQuote == 0 {
				if endsColon && !afterColon {
					clauseHeaderEnd = idx
					break
				}
				if afterColon {
					lastBodyLine = uint32(idx + 1)
					clauseHeaderEnd = -2
					idx++
					break
				}
				return nil
			}
			idx++
		}
		if clauseHeaderEnd == -1 {
			return nil
		}
		if clauseHeaderEnd == -2 {
			continue
		}
		var clauseBody bool
		idx, clauseBody, ok = scanSuite(clauseHeaderEnd + 1)
		if !ok || !clauseBody {
			return nil
		}
	}

	if lastBodyLine <= line {
		return nil
	}
	return &BlockSpan{Start: line, End: lastBodyLine}
}

// hasPrecedingPyDecorator reports whether the preceding statement at baseIndent is a
// decorator (including multi-line decorators whose closing `])` sits at baseIndent).
// Pass 3 W4: walks back over the whole preceding statement using bracket balance.
func hasPrecedingPyDecorator(lines []string, line uint32, baseIndent int) bool {
	// Find the last non-blank, non-comment line before `line`.
	endIdx := -1
	for i := int(line) - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		endIdx = i
		break
	}
	if endIdx < 0 {
		return false
	}
	// Walk backwards from endIdx while brackets are open (scanning from the right) or
	// indentation is deeper than baseIndent, until we reach the first line of that statement.
	bal := 0
	stmtStart := endIdx
	for i := endIdx; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			if bal == 0 {
				break
			}
			continue
		}
		// Update reverse bracket balance (ignoring `#` comments and single-line strings).
		delta := pyLineBracketDelta(lines[i])
		bal -= delta
		stmtStart = i
		if bal <= 0 {
			col := leadingIndentCols(lines[i])
			if col <= baseIndent {
				break
			}
		}
	}
	return leadingIndentCols(lines[stmtStart]) == baseIndent && strings.HasPrefix(strings.TrimSpace(lines[stmtStart]), "@")
}

// pyLineBracketDelta returns (#openers - #closers) outside strings and `#` comments on a line.
func pyLineBracketDelta(line string) int {
	delta := 0
	n := len(line)
	for i := 0; i < n; i++ {
		ch := line[i]
		if ch == '#' {
			break
		}
		if ch == '\'' || ch == '"' {
			q := ch
			if i+2 < n && line[i+1] == q && line[i+2] == q {
				i += 3
				for i+2 < n && !(line[i] == q && line[i+1] == q && line[i+2] == q) {
					if line[i] == '\\' {
						i++
					}
					i++
				}
				i += 2
				continue
			}
			i++
			for i < n {
				if line[i] == '\\' {
					i += 2
					continue
				}
				if line[i] == q {
					break
				}
				i++
			}
			continue
		}
		switch ch {
		case '(', '[', '{':
			delta++
		case ')', ']', '}':
			delta--
		}
	}
	return delta
}

// --- YAML block resolver ---

func resolveYAMLBlock(lines []string, line uint32) *BlockSpan {
	// Pass 3 W8: refuse YAML files containing template syntax (`{{`, `{%`) outside strings/comments
	// (e.g. Go/Helm/Jinja templates), which tree-sitter-yaml parses as ERROR nodes.
	// Note: GitHub Actions `${{ ... }}` is valid inside YAML plain scalars, whereas standalone
	// `{{` / `{%` (not preceded by `$`) breaks YAML's flow-mapping grammar.
	if yamlHasTemplateSyntax(lines) {
		return nil
	}
	raw := lines[line-1]
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || trimmed == "..." {
		return nil
	}
	baseIndent := leadingIndentCols(raw)
	// Finding N3(b): refuse any anchor lying inside a less-indented block scalar (`|` / `>`).
	if isInsideYAMLBlockScalar(lines, line, baseIndent) {
		return nil
	}
	// Sequence item: `-` or `- ...`
	if trimmed == "-" || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "-\t") {
		itemRest := ""
		if len(trimmed) > 1 {
			itemRest = stripYAMLInlineComment(strings.TrimSpace(trimmed[1:]))
		}
		// Pass 3 W6: a sequence item whose value is a flow mapping or flow sequence
		// (`- { ... }` or `- [ ... ]`) is NOT extended over trailing comments by tree-sitter-yaml.
		isFlowItem := strings.HasPrefix(itemRest, "{") || strings.HasPrefix(itemRest, "[")
		lastLine := line
		for i := int(line); i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if leadingIndentCols(lines[i]) <= baseIndent {
				break
			}
			lastLine = uint32(i + 1)
		}
		return finalizeYAMLBlockSpan(lines, line, lastLine, !isFlowItem)
	}

	// Mapping entry: `key: ...`
	colonIdx := findYAMLMappingColon(trimmed)
	if colonIdx < 0 {
		return nil
	}
	val := stripYAMLInlineComment(strings.TrimSpace(trimmed[colonIdx+1:]))

	// Block scalar: `|`, `>`, `|-`, `|+`, `>-`, `>+`, `|2`, etc.
	if isYAMLBlockScalarHeader(val) {
		lastLine := line
		for i := int(line); i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" {
				continue
			}
			if leadingIndentCols(lines[i]) <= baseIndent {
				break
			}
			lastLine = uint32(i + 1)
		}
		// tree-sitter-yaml ends a block_scalar pair at its last indented content line
		// (no trailing comments), while still absorbing trailing blank lines at EOF (Y1).
		return finalizeYAMLBlockSpan(lines, line, lastLine, false)
	}

	// Empty value after `key:`: nested mapping or sequence (YAML allows `- ` at same indent as key!).
	if val == "" {
		firstIdx := -1
		for i := int(line); i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			firstIdx = i
			break
		}
		if firstIdx < 0 {
			return &BlockSpan{Start: line, End: line}
		}
		firstTrim := strings.TrimSpace(lines[firstIdx])
		firstIndent := leadingIndentCols(lines[firstIdx])
		if firstIndent > baseIndent {
			lastLine := line
			for i := firstIdx; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				if leadingIndentCols(lines[i]) <= baseIndent {
					break
				}
				lastLine = uint32(i + 1)
			}
			return finalizeYAMLBlockSpan(lines, line, lastLine, true)
		}
		if firstIndent == baseIndent && (firstTrim == "-" || strings.HasPrefix(firstTrim, "- ") || strings.HasPrefix(firstTrim, "-\t")) {
			lastLine := line
			for i := firstIdx; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if t == "" || strings.HasPrefix(t, "#") {
					continue
				}
				col := leadingIndentCols(lines[i])
				if col < baseIndent {
					break
				}
				if col == baseIndent && !(t == "-" || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "-\t")) {
					break
				}
				lastLine = uint32(i + 1)
			}
			return finalizeYAMLBlockSpan(lines, line, lastLine, true)
		}
		return &BlockSpan{Start: line, End: line}
	}

	// Flow collection opened on this line (`key: [` or `key: {`): refuse multi-line flow
	// collections conservatively because YAML uses `#` comments and `''`-escaped single quotes.
	if strings.HasPrefix(val, "[") || strings.HasPrefix(val, "{") {
		if isClosedSingleLineYAMLFlow(val) {
			return &BlockSpan{Start: line, End: line}
		}
		return nil
	}

	// Single-line scalar `key: value`: if next non-blank line is not indented deeper, it's definitely single-line.
	for i := int(line); i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if leadingIndentCols(lines[i]) > baseIndent {
			// Possible folded multi-line plain scalar — refuse rather than guess.
			return nil
		}
		break
	}
	return &BlockSpan{Start: line, End: line}
}

func findYAMLMappingColon(trimmed string) int {
	inSingle := false
	inDouble := false
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == ':' && (i+1 == len(trimmed) || trimmed[i+1] == ' ' || trimmed[i+1] == '\t') {
			return i
		}
	}
	return -1
}

func stripYAMLInlineComment(val string) string {
	inSingle := false
	inDouble := false
	for i := 0; i < len(val); i++ {
		ch := val[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
		} else if ch == '"' {
			inDouble = true
		} else if ch == '#' && (i == 0 || val[i-1] == ' ' || val[i-1] == '\t') {
			return strings.TrimSpace(val[:i])
		}
	}
	return val
}

func isYAMLBlockScalarHeader(val string) bool {
	if val == "" || (val[0] != '|' && val[0] != '>') {
		return false
	}
	rest := val[1:]
	for i := range len(rest) {
		ch := rest[i]
		if ch != '-' && ch != '+' && (ch < '1' || ch > '9') {
			return false
		}
	}
	return true
}

// isClosedSingleLineYAMLFlow checks if val (starting with `[` or `{`) is a complete
// balanced single-line YAML flow collection.
func isClosedSingleLineYAMLFlow(val string) bool {
	depth := 0
	inSingle := false
	inDouble := false
	for i := 0; i < len(val); i++ {
		ch := val[i]
		if inSingle {
			if ch == '\'' {
				if i+1 < len(val) && val[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !inSingle && !inDouble
}

// isInsideYAMLBlockScalar reports whether 1-based `line` (at `baseIndent`) lies inside
// the indented body of an earlier less-indented YAML block scalar (`key: |` or `key: >`).
// Finding N3(b).
func isInsideYAMLBlockScalar(lines []string, line uint32, baseIndent int) bool {
	minIndent := baseIndent
	for i := int(line) - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		col := leadingIndentCols(lines[i])
		if col < minIndent {
			minIndent = col
			if strings.HasPrefix(t, "#") {
				continue
			}
			cand := t
			if rest, ok := strings.CutPrefix(cand, "- "); ok {
				cand = strings.TrimSpace(rest)
			} else if rest, ok := strings.CutPrefix(cand, "-\t"); ok {
				cand = strings.TrimSpace(rest)
			}
			if cand == "|" || cand == ">" || isYAMLBlockScalarHeader(cand) {
				return true
			}
			if colonIdx := findYAMLMappingColon(cand); colonIdx >= 0 {
				val := stripYAMLInlineComment(strings.TrimSpace(cand[colonIdx+1:]))
				if isYAMLBlockScalarHeader(val) {
					return true
				}
			}
			if col == 0 {
				return false
			}
		}
	}
	return false
}

// yamlHasTemplateSyntax reports whether a YAML file contains template directives
// (`{{` not preceded by `$`, or `{%`) outside comments, quoted strings, and block scalars (W8).
func yamlHasTemplateSyntax(lines []string) bool {
	blockScalarIndent := -1
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		col := leadingIndentCols(raw)
		if blockScalarIndent >= 0 {
			if col > blockScalarIndent {
				continue
			}
			blockScalarIndent = -1
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		// Check for template markers outside quoted strings and `#` comments.
		inSingle := false
		inDouble := false
		for i := 0; i < len(t); i++ {
			ch := t[i]
			if inSingle {
				if ch == '\'' {
					if i+1 < len(t) && t[i+1] == '\'' {
						i++
						continue
					}
					inSingle = false
				}
				continue
			}
			if inDouble {
				if ch == '\\' {
					i++
					continue
				}
				if ch == '"' {
					inDouble = false
				}
				continue
			}
			if ch == '\'' {
				inSingle = true
				continue
			}
			if ch == '"' {
				inDouble = true
				continue
			}
			if ch == '#' && (i == 0 || t[i-1] == ' ' || t[i-1] == '\t') {
				break
			}
			if i+1 < len(t) {
				if ch == '{' && t[i+1] == '{' {
					// GitHub Actions `${{ ... }}` is a valid YAML plain scalar; standalone `{{`
					// (Go/Helm/Jinja templates) breaks YAML's flow-mapping parser.
					if i == 0 || t[i-1] != '$' {
						return true
					}
				}
				if ch == '{' && t[i+1] == '%' {
					return true
				}
			}
		}
		// Enter block scalar body if this line starts one (`key: |` or `- |`).
		cand := t
		if rest, ok := strings.CutPrefix(cand, "- "); ok {
			cand = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(cand, "-\t"); ok {
			cand = strings.TrimSpace(rest)
		}
		if isYAMLBlockScalarHeader(cand) {
			blockScalarIndent = col
		} else if colonIdx := findYAMLMappingColon(cand); colonIdx >= 0 {
			val := stripYAMLInlineComment(strings.TrimSpace(cand[colonIdx+1:]))
			if isYAMLBlockScalarHeader(val) {
				blockScalarIndent = col
			}
		}
	}
	return false
}

// finalizeYAMLBlockSpan applies N3(a) (trailing comments on block-style nodes) and
// Pass 4 Y1 (when nothing but blank lines follows the resolved YAML block up to EOF,
// tree-sitter-yaml extends the node through those trailing blank lines, excluding the
// phantom empty row after the final newline; if a `---` or `...` document marker
// follows instead, refuse conservatively).
func finalizeYAMLBlockSpan(lines []string, start, end uint32, includeComments bool) *BlockSpan {
	if end <= start {
		return &BlockSpan{Start: start, End: end}
	}
	contentLen := len(lines)
	if contentLen > 1 && lines[contentLen-1] == "" {
		contentLen--
	}
	last := end
	if includeComments {
		for i := int(end); i < contentLen; i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" {
				continue
			}
			if strings.HasPrefix(t, "#") {
				last = uint32(i + 1)
				continue
			}
			break
		}
	}
	// Inspect what immediately follows `last` (skipping blank lines).
	nextIdx := -1
	for i := int(last); i < contentLen; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			nextIdx = i
			break
		}
	}
	if nextIdx < 0 {
		// Nothing but blank lines up to EOF: extend through the last real blank line.
		if uint32(contentLen) > last {
			last = uint32(contentLen)
		}
		return &BlockSpan{Start: start, End: last}
	}
	nextTrim := strings.TrimSpace(lines[nextIdx])
	if nextTrim == "---" || nextTrim == "..." ||
		strings.HasPrefix(nextTrim, "--- ") || strings.HasPrefix(nextTrim, "---\t") ||
		strings.HasPrefix(nextTrim, "... ") || strings.HasPrefix(nextTrim, "...\t") {
		return nil
	}
	return &BlockSpan{Start: start, End: last}
}
