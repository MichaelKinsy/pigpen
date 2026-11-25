package hashline

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Stateful line tokenizer for hashline patches.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs

// BlockTargetKind identifies the locator variant of an operation header.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:16
type BlockTargetKind int

const (
	TargetReplace BlockTargetKind = iota
	TargetBlock
	TargetInsertBefore
	TargetInsertAfter
	TargetInsertAfterBlock
	TargetCut
	TargetCutBlock
	TargetBof
	TargetEof
	TargetRem
	TargetMove
)

// BlockTarget is the locator and optional register parsed from one operation header.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:16
type BlockTarget struct {
	Kind        BlockTargetKind
	Range       ParsedRange
	Anchor      Anchor
	Register    string
	HasRegister bool
	Dest        string
}

// TokenKind identifies one classified hashline input row.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:50
type TokenKind int

const (
	TokenBlank TokenKind = iota
	TokenEnvelopeBegin
	TokenEnvelopeEnd
	TokenAbort
	TokenHeader
	TokenOpBlock
	TokenPayloadLiteral
	TokenRaw
)

// Token is one classified hashline input row.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:50
type Token struct {
	Kind     TokenKind
	LineNum  uint32
	Path     string
	FileHash string
	HasHash  bool
	Target   BlockTarget
	HadColon bool
	Text     string
}

// Tokenizer is a stateful line-oriented hashline tokenizer.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:79
type Tokenizer struct {
	buffer      string
	nextLineNum uint32
	closed      bool
}

// NewTokenizer constructs an empty Tokenizer.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:93
func NewTokenizer() *Tokenizer {
	return &Tokenizer{nextLineNum: 1}
}

// Feed feeds a text chunk and returns tokens for complete rows.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:98
func (t *Tokenizer) Feed(chunk string) ([]Token, *EditError) {
	if t.closed {
		return nil, ParseError("Tokenizer is closed; call reset() before reusing.")
	}
	if chunk == "" {
		return nil, nil
	}
	t.buffer += chunk
	var tokens []Token
	start := 0
	for {
		offset := strings.IndexByte(t.buffer[start:], '\n')
		if offset < 0 {
			break
		}
		end := start + offset
		line := strings.TrimSuffix(t.buffer[start:end], "\r")
		tokens = append(tokens, classifyLine(line, t.nextLineNum))
		if t.nextLineNum < math.MaxUint32 {
			t.nextLineNum++
		}
		start = end + 1
	}
	t.buffer = t.buffer[start:]
	return tokens, nil
}

// End closes the stream and classifies its final unterminated row.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:124
func (t *Tokenizer) End() []Token {
	if t.closed {
		return nil
	}
	t.closed = true
	if t.buffer == "" {
		return nil
	}
	line := strings.TrimSuffix(t.buffer, "\r")
	t.buffer = ""
	tok := classifyLine(line, t.nextLineNum)
	if t.nextLineNum < math.MaxUint32 {
		t.nextLineNum++
	}
	return []Token{tok}
}

// Reset resets this tokenizer for reuse.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:142
func (t *Tokenizer) Reset() {
	t.buffer = ""
	t.nextLineNum = 1
	t.closed = false
}

// TokenizeAll tokenizes one complete string.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:149
func (t *Tokenizer) TokenizeAll(text string) ([]Token, *EditError) {
	t.Reset()
	tokens, err := t.Feed(text)
	if err != nil {
		return nil, err
	}
	tokens = append(tokens, t.End()...)
	return tokens, nil
}

// Tokenize classifies one already-split row.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:157
func (t *Tokenizer) Tokenize(line string, lineNum uint32) Token {
	return classifyLine(line, lineNum)
}

// IsOp reports whether a row is a complete operation header.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:162
func (t *Tokenizer) IsOp(line string) bool {
	_, _, ok := parseHunkHeader(line)
	return ok
}

// IsHeader reports whether a row is a valid file header.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:167
func (t *Tokenizer) IsHeader(line string) bool {
	_, _, _, ok := parseHeader(line)
	return ok
}

// IsEnvelopeMarker reports whether a row is a patch envelope or abort marker.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:172
func (t *Tokenizer) IsEnvelopeMarker(line string) bool {
	return markerLineEquals(line, BeginPatchMarker) ||
		markerLineEquals(line, EndPatchMarker) ||
		markerLineEquals(line, AbortMarker)
}

// SplitHashlineLines splits LF/CRLF text without retaining a terminal empty sentinel.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:180
func SplitHashlineLines(text string) []string {
	if text == "" {
		return []string{""}
	}
	var lines []string
	start := 0
	for i := range len(text) {
		if text[i] == '\n' {
			line := strings.TrimSuffix(text[start:i], "\r")
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, strings.TrimSuffix(text[start:], "\r"))
	}
	return lines
}

// ParseLid parses a positive bare line-number anchor.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:203
func ParseLid(raw string, lineNum uint32) (Anchor, *EditError) {
	value := strings.TrimSpace(raw)
	if !validLineNumber(value) {
		return Anchor{}, ParseErrorAt(
			fmt.Sprintf(
				"line %d: expected a line number such as %s; got %s. Use [PATH#hash] from your latest read for file-version binding.",
				lineNum, DescribeAnchorExamples("119"), JSONQuote(raw),
			),
			lineNum,
		)
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return Anchor{}, ParseError("line number is too large")
	}
	return Anchor{Line: uint32(n)}, nil
}

// IsHunkHeaderText reports whether text is a complete operation header.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:224
func IsHunkHeaderText(text string) bool {
	lead := strings.TrimLeftFunc(text, unicode.IsSpace)
	if !strings.HasPrefix(lead, HLPutKeyword) &&
		!strings.HasPrefix(lead, HLCutKeyword) &&
		!strings.HasPrefix(lead, HLRemKeyword) &&
		!strings.HasPrefix(lead, HLMoveKeyword) {
		return false
	}
	_, _, ok := parseHunkHeader(text)
	return ok
}

// OpLabels returns canonical metaharness labels for operation headers in payload order.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:233
func OpLabels(input string) []string {
	tok := NewTokenizer()
	tokens, err := tok.TokenizeAll(input)
	if err != nil {
		return nil
	}
	var out []string
	for _, token := range tokens {
		if token.Kind == TokenOpBlock {
			out = append(out, opLabel(token.Target, token.HadColon))
		}
	}
	return out
}

func putSuffix(hasReg, hadColon, allowsAnonPaste bool) string {
	if hasReg {
		if hadColon {
			return " @reg: (invalid)"
		}
		return " @reg"
	}
	if hadColon {
		return ":"
	}
	if allowsAnonPaste {
		return ""
	}
	return " (invalid)"
}

func cutSuffix(hasReg, hadColon bool) string {
	if hasReg {
		if hadColon {
			return " @reg: (invalid)"
		}
		return " @reg"
	}
	if hadColon {
		return ": (invalid)"
	}
	return ""
}

func opLabel(target BlockTarget, hadColon bool) string {
	switch target.Kind {
	case TargetReplace:
		span := "N.=M"
		if target.Range.Start.Line == target.Range.End.Line {
			span = "N.=N"
		}
		return "PUT " + span + putSuffix(target.HasRegister, hadColon, false)
	case TargetBlock:
		return "PUT N*" + putSuffix(target.HasRegister, hadColon, false)
	case TargetInsertBefore:
		return "PUT <N" + putSuffix(target.HasRegister, hadColon, true)
	case TargetInsertAfter:
		return "PUT >N" + putSuffix(target.HasRegister, hadColon, true)
	case TargetBof:
		return "PUT <1" + putSuffix(target.HasRegister, hadColon, true)
	case TargetEof:
		return "PUT >$" + putSuffix(target.HasRegister, hadColon, true)
	case TargetInsertAfterBlock:
		return "PUT >N*" + putSuffix(target.HasRegister, hadColon, true)
	case TargetCut:
		span := "N.=M"
		if target.Range.Start.Line == target.Range.End.Line {
			span = "N.=N"
		}
		return "CUT " + span + cutSuffix(target.HasRegister, hadColon)
	case TargetCutBlock:
		return "CUT N*" + cutSuffix(target.HasRegister, hadColon)
	case TargetRem:
		return "REM"
	case TargetMove:
		return "MV"
	default:
		return ""
	}
}

func markerLineEquals(line, marker string) bool {
	return strings.TrimRightFunc(line, unicode.IsSpace) == marker
}

func validLineNumber(raw string) bool {
	if raw == "" || raw[0] == '0' {
		return false
	}
	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(raw, 10, 32)
	return err == nil
}

func parseNumberPrefix(raw string) (uint32, int, bool) {
	if len(raw) == 0 || raw[0] < '1' || raw[0] > '9' {
		return 0, 0, false
	}
	end := 1
	for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
		end++
	}
	n, err := strconv.ParseUint(raw[:end], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	return uint32(n), end, true
}

// parseRange matches crates/edit/src/modes/hashline/tokenizer.rs:352.
func parseRange(raw string, allowSingle bool) (ParsedRange, int, bool, bool) {
	trimmed := strings.TrimLeftFunc(raw, unicode.IsSpace)
	leading := len(raw) - len(trimmed)
	start, used, ok := parseNumberPrefix(raw[leading:])
	if !ok {
		return ParsedRange{}, 0, false, false
	}
	cursor := leading + used
	sawNonWS := false
	for cursor < len(raw) {
		r, size := decodeRuneAt(raw, cursor)
		if unicode.IsSpace(r) || r == '-' || r == '.' || r == '=' || r == '…' {
			if !unicode.IsSpace(r) {
				sawNonWS = true
			}
			cursor += size
		} else {
			break
		}
	}
	if end, count, ok := parseNumberPrefix(raw[cursor:]); ok {
		cursor += count
		for cursor < len(raw) && isASCIIWhitespace(raw[cursor]) {
			cursor++
		}
		return ParsedRange{Start: Anchor{Line: start}, End: Anchor{Line: end}}, cursor, true, true
	}
	if !allowSingle {
		return ParsedRange{}, 0, false, false
	}
	if sawNonWS && (cursor == len(raw) || raw[cursor] == ':' || raw[cursor] == '@') {
		return ParsedRange{Start: Anchor{Line: start}, End: Anchor{Line: start}}, cursor, true, true
	}
	return ParsedRange{Start: Anchor{Line: start}, End: Anchor{Line: start}}, cursor, false, true
}

func decodeRuneAt(s string, pos int) (rune, int) {
	for _, r := range s[pos:] {
		return r, len(string(r))
	}
	return 0, 0
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func isASCIIAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isASCIIHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// parseRegisterAndColon matches crates/edit/src/modes/hashline/tokenizer.rs:391.
func parseRegisterAndColon(raw string, target BlockTarget) (BlockTarget, bool, bool) {
	rest := strings.TrimLeftFunc(raw, unicode.IsSpace)
	if regRaw, hasAt := strings.CutPrefix(rest, "@"); hasAt {
		nameLen := 0
		for nameLen < len(regRaw) && (isASCIIAlphaNum(regRaw[nameLen]) || regRaw[nameLen] == '_' || regRaw[nameLen] == '-') {
			nameLen++
		}
		if nameLen == 0 || nameLen > 64 {
			return BlockTarget{}, false, false
		}
		target.Register = regRaw[:nameLen]
		target.HasRegister = true
		rest = strings.TrimLeftFunc(regRaw[nameLen:], unicode.IsSpace)
	}
	hadColon := strings.HasPrefix(rest, ":")
	if hadColon {
		rest = strings.TrimLeftFunc(rest[1:], unicode.IsSpace)
	}
	if rest == "" {
		return target, hadColon, true
	}
	return BlockTarget{}, false, false
}

// parsePutTarget matches crates/edit/src/modes/hashline/tokenizer.rs:433.
func parsePutTarget(raw string) (BlockTarget, bool, bool) {
	rest := strings.TrimLeftFunc(raw, unicode.IsSpace)
	if after, ok := strings.CutPrefix(rest, ">"); ok {
		after = strings.TrimLeftFunc(after, unicode.IsSpace)
		if tail, isEOF := strings.CutPrefix(after, "$"); isEOF {
			return parseRegisterAndColon(tail, BlockTarget{Kind: TargetEof})
		}
		line, used, ok := parseNumberPrefix(after)
		if !ok {
			return BlockTarget{}, false, false
		}
		tail := after[used:]
		block := strings.HasPrefix(tail, "*")
		if block {
			tail = tail[1:]
		}
		kind := TargetInsertAfter
		if block {
			kind = TargetInsertAfterBlock
		}
		return parseRegisterAndColon(tail, BlockTarget{Kind: kind, Anchor: Anchor{Line: line}})
	}
	if after, ok := strings.CutPrefix(rest, "<"); ok {
		after = strings.TrimLeftFunc(after, unicode.IsSpace)
		line, used, ok := parseNumberPrefix(after)
		if !ok {
			return BlockTarget{}, false, false
		}
		tail := after[used:]
		if strings.HasPrefix(tail, "*") {
			tail = tail[1:]
		}
		if line == 1 {
			return parseRegisterAndColon(tail, BlockTarget{Kind: TargetBof})
		}
		return parseRegisterAndColon(tail, BlockTarget{Kind: TargetInsertBefore, Anchor: Anchor{Line: line}})
	}
	rng, used, hadSep, ok := parseRange(rest, true)
	if !ok {
		return BlockTarget{}, false, false
	}
	tail := rest[used:]
	if strings.HasPrefix(tail, "*") {
		if hadSep {
			return BlockTarget{}, false, false
		}
		tail = tail[1:]
		return parseRegisterAndColon(tail, BlockTarget{Kind: TargetBlock, Anchor: rng.Start})
	}
	return parseRegisterAndColon(tail, BlockTarget{Kind: TargetReplace, Range: rng})
}

// parseCutTarget matches crates/edit/src/modes/hashline/tokenizer.rs:482.
func parseCutTarget(raw string) (BlockTarget, bool, bool) {
	rest := strings.TrimLeftFunc(raw, unicode.IsSpace)
	rng, used, hadSep, ok := parseRange(rest, true)
	if !ok {
		return BlockTarget{}, false, false
	}
	tail := rest[used:]
	if strings.HasPrefix(tail, "*") {
		if hadSep {
			return BlockTarget{}, false, false
		}
		tail = tail[1:]
		return parseRegisterAndColon(tail, BlockTarget{Kind: TargetCutBlock, Anchor: rng.Start})
	}
	return parseRegisterAndColon(tail, BlockTarget{Kind: TargetCut, Range: rng})
}

func keywordTail(line, keyword string) (string, bool) {
	rest, ok := strings.CutPrefix(line, keyword)
	if !ok {
		return "", false
	}
	if rest == "" || strings.HasPrefix(rest, ":") {
		return rest, true
	}
	r, _ := decodeRuneAt(rest, 0)
	if unicode.IsSpace(r) {
		return rest, true
	}
	return "", false
}

// parseHunkHeader matches crates/edit/src/modes/hashline/tokenizer.rs:511.
func parseHunkHeader(line string) (BlockTarget, bool, bool) {
	line = strings.TrimSpace(line)
	if rest, ok := keywordTail(line, HLRemKeyword); ok {
		if strings.TrimSpace(rest) == "" {
			return BlockTarget{Kind: TargetRem}, false, true
		}
		return BlockTarget{}, false, false
	}
	if rest, ok := keywordTail(line, HLMoveKeyword); ok {
		dest, ok := unquotePath(strings.TrimSpace(rest))
		if ok && dest != "" {
			return BlockTarget{Kind: TargetMove, Dest: dest}, false, true
		}
		return BlockTarget{}, false, false
	}
	if rest, ok := keywordTail(line, HLPutKeyword); ok {
		return parsePutTarget(rest)
	}
	if rest, ok := keywordTail(line, HLCutKeyword); ok {
		return parseCutTarget(rest)
	}
	return BlockTarget{}, false, false
}

// unquotePath matches crates/edit/src/modes/hashline/tokenizer.rs:529.
func unquotePath(raw string) (string, bool) {
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		return raw[1 : len(raw)-1], true
	}
	if strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "'") {
		return "", false
	}
	return raw, true
}

// HeaderPathHasOrphanBracket reports whether a header path closes a bracket it never opened.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:550
func HeaderPathHasOrphanBracket(path string) bool {
	depth := 0
	for i := range len(path) {
		switch path[i] {
		case '[':
			depth++
		case ']':
			if depth == 0 {
				return true
			}
			depth--
		}
	}
	return false
}

// parseHeader splits a [PATH] / [PATH#TAG] header row.
// upstream: crates/edit/src/modes/hashline/tokenizer.rs:568
func parseHeader(line string) (path string, fileHash string, hasHash bool, ok bool) {
	line = strings.TrimRightFunc(line, unicode.IsSpace)
	if len(line) < 2 || line[0] != '[' || line[len(line)-1] != ']' {
		return "", "", false, false
	}
	body := line[1 : len(line)-1]
	if body == "" {
		return "", "", false, false
	}
	if idx := strings.LastIndexByte(body, '#'); idx >= 0 {
		p := body[:idx]
		h := body[idx+1:]
		if p == "" || HeaderPathHasOrphanBracket(p) || len(h) != HLFileHashLength {
			return "", "", false, false
		}
		for i := range len(h) {
			if !isASCIIHex(h[i]) {
				return "", "", false, false
			}
		}
		return p, strings.ToUpper(h), true, true
	}
	if HeaderPathHasOrphanBracket(body) {
		return "", "", false, false
	}
	return body, "", false, true
}

// classifyLine matches crates/edit/src/modes/hashline/tokenizer.rs:591.
func classifyLine(line string, lineNum uint32) Token {
	if line == "" {
		return Token{Kind: TokenBlank, LineNum: lineNum}
	}
	if markerLineEquals(line, BeginPatchMarker) {
		return Token{Kind: TokenEnvelopeBegin, LineNum: lineNum}
	}
	if markerLineEquals(line, EndPatchMarker) {
		return Token{Kind: TokenEnvelopeEnd, LineNum: lineNum}
	}
	if markerLineEquals(line, AbortMarker) {
		return Token{Kind: TokenAbort, LineNum: lineNum}
	}
	if path, hash, hasHash, ok := parseHeader(line); ok {
		return Token{Kind: TokenHeader, LineNum: lineNum, Path: path, FileHash: hash, HasHash: hasHash}
	}
	if target, hadColon, ok := parseHunkHeader(line); ok {
		return Token{Kind: TokenOpBlock, LineNum: lineNum, Target: target, HadColon: hadColon}
	}
	if text, ok := strings.CutPrefix(line, "+"); ok {
		return Token{Kind: TokenPayloadLiteral, LineNum: lineNum, Text: text}
	}
	return Token{Kind: TokenRaw, LineNum: lineNum, Text: line}
}
