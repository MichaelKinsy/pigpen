package ansi

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// ═══════════════════════════════════════════════════════════════════════════
// Go Bash Shell Visualizer
// Port of omp v19.1.1:
//   - crates/shell/src/bash/trace/viz/{model.rs,printer.rs,words.rs,embedded.rs,analysis.rs}
//   - packages/tui/src/tools/shell-viz-render.ts
// ═══════════════════════════════════════════════════════════════════════════

// BuildShellVizPresentation parses a bash command string and produces omp's
// canonical ShellVizPresentation JSON-compatible map consumed by both Tern's
// native `shell` node and plain-terminal RenderShellVizTerminal.
func BuildShellVizPresentation(command string, running bool, exitCode *int, output string) map[string]any {
	rows := buildShellVizRows(command, running, exitCode)
	return map[string]any{
		"preferences": map[string]any{
			"view": "pseudo",
			"layers": map[string]any{
				"color":   true,
				"inline":  true,
				"idioms":  true,
				"effects": true,
				"unwrap":  true,
			},
			"split":         false,
			"playing":       false,
			"speed":         1,
			"expandedNodes": []any{},
		},
		"rows":      rows,
		"original":  []any{},
		"output":    output,
		"running":   false,
		"executing": running,
		"watches":   []any{},
		"timeline": map[string]any{
			"length":     0,
			"cursor":     0,
			"elapsedNs":  0,
			"durationNs": 0,
		},
	}
}

// ── UTF-16 offset table ─────────────────────────────────────────────────────

func buildUTF16Map(src string) []int {
	m := make([]int, len(src)+1)
	var u16 int
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRuneInString(src[i:])
		for j := range size {
			m[i+j] = u16
		}
		if r >= 0x10000 {
			u16 += 2
		} else {
			u16++
		}
		i += size
	}
	m[len(src)] = u16
	return m
}

func utf16Range(u16 []int, lo, hi int) []int {
	if lo < 0 {
		lo = 0
	}
	if hi < lo {
		hi = lo
	}
	if lo >= len(u16) {
		lo = len(u16) - 1
	}
	if hi >= len(u16) {
		hi = len(u16) - 1
	}
	return []int{u16[lo], u16[hi]}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// ── AST definitions ─────────────────────────────────────────────────────────

type svWordPartKind int

const (
	svPartLit svWordPartKind = iota
	svPartEscaped
	svPartSQuote
	svPartAnsiC
	svPartDQuote
	svPartParam
	svPartComsub
	svPartArith
)

type svWordPart struct {
	kind  svWordPartKind
	text  string
	parts []svWordPart // for svPartDQuote
	raw   string       // original raw slice for param/comsub/arith
}

type svWord struct {
	parts []svWordPart
	lo    int
	hi    int
}

type svAssign struct {
	name   string
	append bool
	value  svWord
	lo     int
	hi     int
}

type svRedir struct {
	fd      string
	op      string
	target  *svWord
	heredoc *svHereDoc
	lo      int
	hi      int
}

type svHereDoc struct {
	delim    string
	delimRaw string
	quoted   bool
	stripTab bool
	body     string
	lo       int
	hi       int
}

type svCmdKind int

const (
	svCmdSimple svCmdKind = iota
	svCmdIf
	svCmdFor
	svCmdWhile
	svCmdUntil
	svCmdCase
	svCmdSubshell
	svCmdBrace
	svCmdCond
	svCmdArith
	svCmdFuncDef
)

type svIfArm struct {
	cond []*svListItem
	body []*svListItem
}

type svCaseArm struct {
	patterns []svWord
	body     []*svListItem
	term     string // ";;", ";&", ";;&"
}

type svCommand struct {
	kind       svCmdKind
	assigns    []svAssign
	words      []svWord
	redirs     []svRedir
	pipeStderr bool
	lo         int
	hi         int

	// compound fields
	ifArms   []svIfArm
	elseBody []*svListItem
	forVar   string
	forWords []svWord
	hasIn    bool
	body     []*svListItem
	cond     []*svListItem
	caseWord svWord
	caseArms []svCaseArm
	condText string // for [[ ... ]]
	arithSrc string // for (( ... ))
	funcName string
	funcBody *svCommand
}

type svPipeline struct {
	bang   bool
	timed  bool
	stages []*svCommand
	lo     int
	hi     int
}

type svAndOr struct {
	first *svPipeline
	ops   []string // "&&" or "||"
	rest  []*svPipeline
	lo    int
	hi    int
}

type svListItem struct {
	andOr *svAndOr
	sep   string // "", ";", "&", "\n"
	lo    int
	hi    int
}

// ── Bash Lexer ──────────────────────────────────────────────────────────────

type svTokKind int

const (
	svTokEOF svTokKind = iota
	svTokWord
	svTokNewline
	svTokSemi     // ;
	svTokDSemi    // ;;
	svTokSemiAmp  // ;&
	svTokDSemiAmp // ;;&
	svTokAmp      // &
	svTokAnd      // &&
	svTokPipe     // |
	svTokPipeAmp  // |&
	svTokOr       // ||
	svTokLParen   // (
	svTokRParen   // )
	svTokRedir    // <, >, >>, >|, <>, <&, >&, &>, &>>, <<, <<-, <<<
	svTokCond     // [[ ... ]]
	svTokArith    // (( ... ))
)

type svToken struct {
	kind    svTokKind
	text    string
	word    svWord
	fd      string // optional leading fd on svTokRedir
	lo      int
	hi      int
	heredoc *svHereDoc
}

type svPendingHereDoc struct {
	doc *svHereDoc
}

const (
	maxShellVizBytes = 256 * 1024
	maxShellVizDepth = 128
)

type svLexer struct {
	src             string
	pos             int
	heredocs        []*svPendingHereDoc
	arithFailedFrom int
	err             error
}

func newSVLexer(src string) *svLexer {
	return &svLexer{src: src, arithFailedFrom: -1}
}

func (l *svLexer) peekByte(offset int) byte {
	idx := l.pos + offset
	if idx < 0 || idx >= len(l.src) {
		return 0
	}
	return l.src[idx]
}

func (l *svLexer) skipSpaces() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ' ' || c == '\t' || c == '\r' {
			l.pos++
			continue
		}
		if c == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n' {
			l.pos += 2
			continue
		}
		break
	}
}

func (l *svLexer) consumeHereDocs() {
	if len(l.heredocs) == 0 {
		return
	}
	pending := l.heredocs
	l.heredocs = nil
	for _, ph := range pending {
		doc := ph.doc
		bodyStart := l.pos
		var lines []string
		found := false
		for l.pos <= len(l.src) {
			lineStart := l.pos
			nl := strings.IndexByte(l.src[l.pos:], '\n')
			var line string
			if nl < 0 {
				line = l.src[l.pos:]
				l.pos = len(l.src)
			} else {
				line = l.src[l.pos : l.pos+nl]
				l.pos = l.pos + nl + 1
			}
			lineClean := strings.TrimSuffix(line, "\r")
			check := lineClean
			if doc.stripTab {
				check = strings.TrimLeft(check, "\t")
			}
			if check == doc.delim {
				doc.lo = bodyStart
				doc.hi = lineStart
				found = true
				break
			}
			if doc.stripTab {
				lines = append(lines, strings.TrimLeft(lineClean, "\t"))
			} else {
				lines = append(lines, lineClean)
			}
			if nl < 0 {
				break
			}
		}
		if len(lines) > 0 {
			doc.body = strings.Join(lines, "\n") + "\n"
		}
		if !found {
			doc.lo = bodyStart
			doc.hi = l.pos
			l.err = fmt.Errorf("unterminated heredoc %s", doc.delim)
		}
	}
}

func (l *svLexer) nextToken() svToken {
	l.skipSpaces()
	if l.pos >= len(l.src) {
		return svToken{kind: svTokEOF, lo: l.pos, hi: l.pos}
	}

	// Comment: # to end of line
	if l.src[l.pos] == '#' {
		for l.pos < len(l.src) && l.src[l.pos] != '\n' {
			l.pos++
		}
		if l.pos >= len(l.src) {
			return svToken{kind: svTokEOF, lo: l.pos, hi: l.pos}
		}
	}

	start := l.pos
	c := l.src[l.pos]

	if c == '\n' {
		l.pos++
		l.consumeHereDocs()
		return svToken{kind: svTokNewline, text: "\n", lo: start, hi: l.pos}
	}

	// Check [[ ... ]] conditional expression
	if c == '[' && l.peekByte(1) == '[' && (l.pos+2 >= len(l.src) || isSVMetaOrSpace(l.src[l.pos+2])) {
		tok, ok := l.scanCond()
		if ok {
			return tok
		}
	}

	// Check (( ... )) arithmetic command
	if c == '(' && l.peekByte(1) == '(' {
		tok, ok := l.scanArithCmd()
		if ok {
			return tok
		}
	}

	// Multi-char operators
	switch c {
	case ';':
		if l.peekByte(1) == ';' {
			if l.peekByte(2) == '&' {
				l.pos += 3
				return svToken{kind: svTokDSemiAmp, text: ";;&", lo: start, hi: l.pos}
			}
			l.pos += 2
			return svToken{kind: svTokDSemi, text: ";;", lo: start, hi: l.pos}
		}
		if l.peekByte(1) == '&' {
			l.pos += 2
			return svToken{kind: svTokSemiAmp, text: ";&", lo: start, hi: l.pos}
		}
		l.pos++
		return svToken{kind: svTokSemi, text: ";", lo: start, hi: l.pos}
	case '&':
		if l.peekByte(1) == '&' {
			l.pos += 2
			return svToken{kind: svTokAnd, text: "&&", lo: start, hi: l.pos}
		}
		if l.peekByte(1) == '>' {
			if l.peekByte(2) == '>' {
				l.pos += 3
				return svToken{kind: svTokRedir, text: "&>>", lo: start, hi: l.pos}
			}
			l.pos += 2
			return svToken{kind: svTokRedir, text: "&>", lo: start, hi: l.pos}
		}
		l.pos++
		return svToken{kind: svTokAmp, text: "&", lo: start, hi: l.pos}
	case '|':
		if l.peekByte(1) == '|' {
			l.pos += 2
			return svToken{kind: svTokOr, text: "||", lo: start, hi: l.pos}
		}
		if l.peekByte(1) == '&' {
			l.pos += 2
			return svToken{kind: svTokPipeAmp, text: "|&", lo: start, hi: l.pos}
		}
		l.pos++
		return svToken{kind: svTokPipe, text: "|", lo: start, hi: l.pos}
	case '(':
		l.pos++
		return svToken{kind: svTokLParen, text: "(", lo: start, hi: l.pos}
	case ')':
		l.pos++
		return svToken{kind: svTokRParen, text: ")", lo: start, hi: l.pos}
	case '<', '>':
		return l.scanRedir("", start)
	}

	// Leading digits immediately followed by < or > form a redirection fd (e.g. 2>/dev/null, 2>&1)
	if c >= '0' && c <= '9' {
		j := l.pos
		for j < len(l.src) && l.src[j] >= '0' && l.src[j] <= '9' {
			j++
		}
		if j < len(l.src) && (l.src[j] == '<' || l.src[j] == '>') {
			fd := l.src[start:j]
			l.pos = j
			return l.scanRedir(fd, start)
		}
	}

	return l.scanWord()
}

func (l *svLexer) scanRedir(fd string, start int) svToken {
	c := l.src[l.pos]
	var op string
	if c == '>' {
		switch l.peekByte(1) {
		case '>':
			op = ">>"
			l.pos += 2
		case '|':
			op = ">|"
			l.pos += 2
		case '&':
			op = ">&"
			l.pos += 2
		default:
			op = ">"
			l.pos++
		}
	} else {
		switch l.peekByte(1) {
		case '<':
			if l.peekByte(2) == '<' {
				op = "<<<"
				l.pos += 3
			} else if l.peekByte(2) == '-' {
				op = "<<-"
				l.pos += 3
			} else {
				op = "<<"
				l.pos += 2
			}
		case '&':
			op = "<&"
			l.pos += 2
		case '>':
			op = "<>"
			l.pos += 2
		default:
			op = "<"
			l.pos++
		}
	}
	return svToken{kind: svTokRedir, fd: fd, text: op, lo: start, hi: l.pos}
}

func (l *svLexer) scanCond() (svToken, bool) {
	start := l.pos
	i := l.pos + 2
	inSingle := false
	inDouble := false
	for i < len(l.src) {
		c := l.src[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			i++
			continue
		}
		if c == '\\' && i+1 < len(l.src) {
			i += 2
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			i++
			continue
		}
		if c == '\'' {
			inSingle = true
			i++
			continue
		}
		if c == '"' {
			inDouble = true
			i++
			continue
		}
		if c == ']' && i+1 < len(l.src) && l.src[i+1] == ']' {
			inner := strings.TrimSpace(l.src[start+2 : i])
			l.pos = i + 2
			return svToken{kind: svTokCond, text: inner, lo: start, hi: l.pos}, true
		}
		i++
	}
	l.err = fmt.Errorf("unterminated [[")
	return svToken{}, false
}

func (l *svLexer) scanArithCmd() (svToken, bool) {
	start := l.pos
	if l.arithFailedFrom >= 0 && start >= l.arithFailedFrom {
		return svToken{}, false
	}
	i := l.pos + 2
	depth := 1
	for i < len(l.src) {
		if l.src[i] == '(' {
			depth++
			i++
			continue
		}
		if l.src[i] == ')' {
			if depth == 1 && i+1 < len(l.src) && l.src[i+1] == ')' {
				inner := strings.TrimSpace(l.src[start+2 : i])
				l.pos = i + 2
				return svToken{kind: svTokArith, text: inner, lo: start, hi: l.pos}, true
			}
			depth--
			i++
			continue
		}
		i++
	}
	if l.arithFailedFrom < 0 || start < l.arithFailedFrom {
		l.arithFailedFrom = start
	}
	return svToken{}, false
}

func isSVMetaOrSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

func (l *svLexer) scanWord() svToken {
	start := l.pos
	var parts []svWordPart
	flushLit := func(buf *strings.Builder) {
		if buf.Len() > 0 {
			parts = append(parts, svWordPart{kind: svPartLit, text: buf.String()})
			buf.Reset()
		}
	}
	var lit strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '(' && len(parts) == 0 && isArrayAssignPrefix(lit.String()) {
			lit.WriteByte(c)
			l.pos++
			depth := 1
			for l.pos < len(l.src) && depth > 0 {
				ch := l.src[l.pos]
				if ch == '\'' {
					lit.WriteByte(ch)
					l.pos++
					for l.pos < len(l.src) && l.src[l.pos] != '\'' {
						lit.WriteByte(l.src[l.pos])
						l.pos++
					}
					if l.pos < len(l.src) {
						lit.WriteByte(l.src[l.pos])
						l.pos++
					}
					continue
				}
				if ch == '"' {
					lit.WriteByte(ch)
					l.pos++
					for l.pos < len(l.src) && l.src[l.pos] != '"' {
						if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
							lit.WriteByte(l.src[l.pos])
							l.pos++
						}
						lit.WriteByte(l.src[l.pos])
						l.pos++
					}
					if l.pos < len(l.src) {
						lit.WriteByte(l.src[l.pos])
						l.pos++
					}
					continue
				}
				if ch == '\\' && l.pos+1 < len(l.src) {
					lit.WriteByte(ch)
					l.pos++
					lit.WriteByte(l.src[l.pos])
					l.pos++
					continue
				}
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
				}
				lit.WriteByte(ch)
				l.pos++
			}
			continue
		}
		if isSVMetaOrSpace(c) {
			break
		}
		switch c {
		case '\\':
			flushLit(&lit)
			l.pos++
			if l.pos < len(l.src) {
				if l.src[l.pos] == '\n' {
					l.pos++
					continue
				}
				r, sz := utf8.DecodeRuneInString(l.src[l.pos:])
				parts = append(parts, svWordPart{kind: svPartEscaped, text: string(r)})
				l.pos += sz
			} else {
				parts = append(parts, svWordPart{kind: svPartEscaped, text: "\\"})
			}
		case '\'':
			flushLit(&lit)
			l.pos++
			sStart := l.pos
			for l.pos < len(l.src) && l.src[l.pos] != '\'' {
				l.pos++
			}
			if l.pos >= len(l.src) {
				l.err = fmt.Errorf("unterminated single quote")
				parts = append(parts, svWordPart{kind: svPartSQuote, text: l.src[sStart:]})
			} else {
				parts = append(parts, svWordPart{kind: svPartSQuote, text: l.src[sStart:l.pos]})
				l.pos++
			}
		case '"':
			flushLit(&lit)
			parts = append(parts, l.scanDQuote())
		case '$':
			if l.peekByte(1) == '\'' {
				flushLit(&lit)
				parts = append(parts, l.scanAnsiC())
			} else if l.peekByte(1) == '(' && l.peekByte(2) == '(' {
				flushLit(&lit)
				parts = append(parts, l.scanArithSub())
			} else if l.peekByte(1) == '(' {
				flushLit(&lit)
				parts = append(parts, l.scanComsub())
			} else if l.peekByte(1) == '{' {
				flushLit(&lit)
				parts = append(parts, l.scanBracedParam())
			} else if isParamStart(l.peekByte(1)) {
				flushLit(&lit)
				parts = append(parts, l.scanShortParam())
			} else {
				lit.WriteByte(c)
				l.pos++
			}
		case '`':
			flushLit(&lit)
			parts = append(parts, l.scanBacktick())
		default:
			lit.WriteByte(c)
			l.pos++
		}
	}
	flushLit(&lit)
	w := svWord{parts: parts, lo: start, hi: l.pos}
	return svToken{kind: svTokWord, text: l.src[start:l.pos], word: w, lo: start, hi: l.pos}
}

func isArrayAssignPrefix(s string) bool {
	if strings.HasSuffix(s, "+=") {
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "=") {
		s = s[:len(s)-1]
	} else {
		return false
	}
	return isValidShellIdent(s)
}

func isValidShellIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

func (l *svLexer) scanDQuote() svWordPart {
	l.pos++ // skip opening "
	var inner []svWordPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			inner = append(inner, svWordPart{kind: svPartLit, text: lit.String()})
			lit.Reset()
		}
	}
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.pos++
			flush()
			return svWordPart{kind: svPartDQuote, parts: inner}
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			next := l.src[l.pos+1]
			if next == '\n' {
				l.pos += 2
				continue
			}
			if next == '$' || next == '`' || next == '"' || next == '\\' {
				flush()
				inner = append(inner, svWordPart{kind: svPartEscaped, text: string(next)})
				l.pos += 2
				continue
			}
			lit.WriteByte(c)
			l.pos++
			continue
		}
		if c == '$' {
			if l.peekByte(1) == '(' && l.peekByte(2) == '(' {
				flush()
				inner = append(inner, l.scanArithSub())
				continue
			}
			if l.peekByte(1) == '(' {
				flush()
				inner = append(inner, l.scanComsub())
				continue
			}
			if l.peekByte(1) == '{' {
				flush()
				inner = append(inner, l.scanBracedParam())
				continue
			}
			if isParamStart(l.peekByte(1)) {
				flush()
				inner = append(inner, l.scanShortParam())
				continue
			}
		}
		if c == '`' {
			flush()
			inner = append(inner, l.scanBacktick())
			continue
		}
		lit.WriteByte(c)
		l.pos++
	}
	l.err = fmt.Errorf("unterminated double quote")
	flush()
	return svWordPart{kind: svPartDQuote, parts: inner}
}

func (l *svLexer) scanAnsiC() svWordPart {
	l.pos += 2 // skip $'
	var out strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\'' {
			l.pos++
			return svWordPart{kind: svPartAnsiC, text: out.String()}
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos++
			esc := l.src[l.pos]
			l.pos++
			switch esc {
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case '\\':
				out.WriteByte('\\')
			case '\'':
				out.WriteByte('\'')
			case '"':
				out.WriteByte('"')
			case 'a':
				out.WriteByte('\a')
			case 'b':
				out.WriteByte('\b')
			case 'e', 'E':
				out.WriteByte(0x1b)
			case 'f':
				out.WriteByte('\f')
			case 'v':
				out.WriteByte('\v')
			default:
				out.WriteByte('\\')
				out.WriteByte(esc)
			}
			continue
		}
		out.WriteByte(c)
		l.pos++
	}
	l.err = fmt.Errorf("unterminated $'...'")
	return svWordPart{kind: svPartAnsiC, text: out.String()}
}

func isParamStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' ||
		(c >= '0' && c <= '9') || c == '@' || c == '*' || c == '#' || c == '?' || c == '-' || c == '$' || c == '!'
}

func (l *svLexer) scanShortParam() svWordPart {
	start := l.pos
	l.pos++ // skip $
	c := l.src[l.pos]
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' {
		nameStart := l.pos
		for l.pos < len(l.src) && isIdentChar(l.src[l.pos]) {
			l.pos++
		}
		return svWordPart{kind: svPartParam, text: l.src[nameStart:l.pos], raw: l.src[start:l.pos]}
	}
	l.pos++
	return svWordPart{kind: svPartParam, text: string(c), raw: l.src[start:l.pos]}
}

func (l *svLexer) scanBracedParam() svWordPart {
	start := l.pos
	l.pos += 2 // skip ${
	depth := 1
	inSingle := false
	inDouble := false
	for l.pos < len(l.src) && depth > 0 {
		c := l.src[l.pos]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			l.pos++
			continue
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos += 2
			continue
		}
		if c == '"' {
			inDouble = !inDouble
			l.pos++
			continue
		}
		if !inDouble && c == '\'' {
			inSingle = true
			l.pos++
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				body := l.src[start+2 : l.pos]
				l.pos++
				return svWordPart{kind: svPartParam, text: body, raw: l.src[start:l.pos]}
			}
		}
		l.pos++
	}
	l.err = fmt.Errorf("unterminated ${")
	return svWordPart{kind: svPartParam, text: l.src[start+2:], raw: l.src[start:]}
}

func (l *svLexer) scanComsub() svWordPart {
	start := l.pos
	l.pos += 2 // skip $(
	depth := 1
	inSingle := false
	inDouble := false
	for l.pos < len(l.src) && depth > 0 {
		c := l.src[l.pos]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			l.pos++
			continue
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos += 2
			continue
		}
		if c == '"' {
			inDouble = !inDouble
			l.pos++
			continue
		}
		if !inDouble && c == '\'' {
			inSingle = true
			l.pos++
			continue
		}
		if !inDouble && c == '(' {
			depth++
		} else if !inDouble && c == ')' {
			depth--
			if depth == 0 {
				body := l.src[start+2 : l.pos]
				l.pos++
				return svWordPart{kind: svPartComsub, text: body, raw: l.src[start:l.pos]}
			}
		}
		l.pos++
	}
	l.err = fmt.Errorf("unterminated $(")
	return svWordPart{kind: svPartComsub, text: l.src[start+2:], raw: l.src[start:]}
}

func (l *svLexer) scanBacktick() svWordPart {
	start := l.pos
	l.pos++ // skip `
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '\\' && l.pos+1 < len(l.src) {
			l.pos += 2
			continue
		}
		if c == '`' {
			body := l.src[start+1 : l.pos]
			l.pos++
			return svWordPart{kind: svPartComsub, text: body, raw: l.src[start:l.pos]}
		}
		l.pos++
	}
	l.err = fmt.Errorf("unterminated backtick")
	return svWordPart{kind: svPartComsub, text: l.src[start+1:], raw: l.src[start:]}
}

func (l *svLexer) scanArithSub() svWordPart {
	start := l.pos
	l.pos += 3 // skip $((
	depth := 1
	for l.pos < len(l.src) {
		if l.src[l.pos] == '(' {
			depth++
			l.pos++
			continue
		}
		if l.src[l.pos] == ')' {
			if depth == 1 && l.peekByte(1) == ')' {
				body := l.src[start+3 : l.pos]
				l.pos += 2
				return svWordPart{kind: svPartArith, text: body, raw: l.src[start:l.pos]}
			}
			depth--
			l.pos++
			continue
		}
		l.pos++
	}
	l.err = fmt.Errorf("unterminated $((")
	return svWordPart{kind: svPartArith, text: l.src[start+3:], raw: l.src[start:]}
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// ── Bash Parser ─────────────────────────────────────────────────────────────

type svParser struct {
	lex   *svLexer
	cur   svToken
	depth int
	err   error
}

func parseBashScript(src string) ([]*svListItem, error) {
	lex := newSVLexer(src)
	p := &svParser{lex: lex}
	p.advance()
	items := p.parseList(nil)
	if p.err != nil {
		return nil, p.err
	}
	if lex.err != nil {
		return nil, lex.err
	}
	if p.cur.kind != svTokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.cur.text)
	}
	return items, nil
}

func (p *svParser) advance() {
	p.cur = p.lex.nextToken()
}

func (p *svParser) skipNewlines() {
	for p.cur.kind == svTokNewline {
		p.advance()
	}
}

func (p *svParser) isStopWord(stops map[string]bool) bool {
	if p.cur.kind == svTokEOF {
		return true
	}
	if stops == nil {
		return false
	}
	if p.cur.kind == svTokRParen && stops[")"] {
		return true
	}
	if (p.cur.kind == svTokDSemi || p.cur.kind == svTokSemiAmp || p.cur.kind == svTokDSemiAmp) && stops[";;"] {
		return true
	}
	if p.cur.kind == svTokWord && isBareWord(p.cur.word) && stops[p.cur.text] {
		return true
	}
	return false
}

func isBareWord(w svWord) bool {
	return len(w.parts) == 1 && w.parts[0].kind == svPartLit
}

func (p *svParser) parseList(stops map[string]bool) []*svListItem {
	p.depth++
	if p.depth > maxShellVizDepth {
		p.err = fmt.Errorf("nesting depth exceeded")
		p.depth--
		return nil
	}
	defer func() { p.depth-- }()
	var items []*svListItem
	for {
		p.skipNewlines()
		if p.cur.kind == svTokEOF || p.isStopWord(stops) || p.err != nil {
			break
		}
		if p.cur.kind == svTokSemi {
			p.advance()
			continue
		}
		ao := p.parseAndOr()
		if ao == nil {
			if p.err == nil {
				p.err = fmt.Errorf("unexpected token %q", p.cur.text)
			}
			break
		}
		item := &svListItem{andOr: ao, lo: ao.lo, hi: ao.hi}
		switch p.cur.kind {
		case svTokSemi:
			item.sep = ";"
			p.advance()
		case svTokAmp:
			item.sep = "&"
			item.hi = p.cur.hi
			p.advance()
		case svTokNewline:
			item.sep = "\n"
			p.advance()
		default:
			if p.cur.kind != svTokEOF && !p.isStopWord(stops) && p.err == nil {
				p.err = fmt.Errorf("unexpected token %q", p.cur.text)
			}
		}
		items = append(items, item)
	}
	return items
}

func (p *svParser) parseAndOr() *svAndOr {
	first := p.parsePipeline()
	if first == nil {
		return nil
	}
	ao := &svAndOr{first: first, lo: first.lo, hi: first.hi}
	for p.cur.kind == svTokAnd || p.cur.kind == svTokOr {
		op := p.cur.text
		p.advance()
		p.skipNewlines()
		next := p.parsePipeline()
		if next == nil {
			p.err = fmt.Errorf("expected command after %s", op)
			return ao
		}
		ao.ops = append(ao.ops, op)
		ao.rest = append(ao.rest, next)
		ao.hi = next.hi
	}
	return ao
}

func (p *svParser) parsePipeline() *svPipeline {
	pl := &svPipeline{lo: p.cur.lo}
	for p.cur.kind == svTokWord && isBareWord(p.cur.word) {
		if p.cur.text == "!" {
			pl.bang = !pl.bang
			p.advance()
			continue
		}
		if p.cur.text == "time" {
			pl.timed = true
			p.advance()
			if p.cur.kind == svTokWord && isBareWord(p.cur.word) && p.cur.text == "-p" {
				p.advance()
			}
			continue
		}
		break
	}
	first := p.parseCommand()
	if first == nil {
		if pl.bang || pl.timed {
			p.err = fmt.Errorf("expected command in pipeline")
		}
		return nil
	}
	pl.stages = append(pl.stages, first)
	pl.hi = first.hi
	for p.cur.kind == svTokPipe || p.cur.kind == svTokPipeAmp {
		stderr := p.cur.kind == svTokPipeAmp
		pl.stages[len(pl.stages)-1].pipeStderr = stderr
		p.advance()
		p.skipNewlines()
		next := p.parseCommand()
		if next == nil {
			p.err = fmt.Errorf("expected command after pipe")
			return pl
		}
		pl.stages = append(pl.stages, next)
		pl.hi = next.hi
	}
	return pl
}

func (p *svParser) parseCommand() *svCommand {
	switch p.cur.kind {
	case svTokCond:
		cmd := &svCommand{kind: svCmdCond, condText: p.cur.text, lo: p.cur.lo, hi: p.cur.hi}
		p.advance()
		p.parseTrailingRedirs(cmd)
		return cmd
	case svTokArith:
		cmd := &svCommand{kind: svCmdArith, arithSrc: p.cur.text, lo: p.cur.lo, hi: p.cur.hi}
		p.advance()
		p.parseTrailingRedirs(cmd)
		return cmd
	case svTokLParen:
		return p.parseSubshell()
	case svTokWord:
		if isBareWord(p.cur.word) {
			switch p.cur.text {
			case "if":
				return p.parseIf()
			case "for":
				return p.parseFor()
			case "while":
				return p.parseWhile(false)
			case "until":
				return p.parseWhile(true)
			case "case":
				return p.parseCase()
			case "{":
				return p.parseBrace()
			case "function":
				return p.parseFuncDefKeyword()
			}
		}
		return p.parseSimpleOrFunc()
	case svTokRedir:
		return p.parseSimpleOrFunc()
	default:
		return nil
	}
}

func (p *svParser) parseTrailingRedirs(cmd *svCommand) {
	for p.cur.kind == svTokRedir {
		r, ok := p.parseOneRedir()
		if !ok {
			return
		}
		cmd.redirs = append(cmd.redirs, r)
		cmd.hi = r.hi
	}
}

func (p *svParser) parseOneRedir() (svRedir, bool) {
	tok := p.cur
	p.advance()
	if p.cur.kind != svTokWord {
		p.err = fmt.Errorf("expected redirection target after %s", tok.text)
		return svRedir{}, false
	}
	targetTok := p.cur
	if tok.text == "<<" || tok.text == "<<-" {
		delim := svWordLiteral(targetTok.word)
		quoted := svWordHasQuote(targetTok.word)
		doc := &svHereDoc{
			delim:    delim,
			delimRaw: targetTok.text,
			quoted:   quoted,
			stripTab: tok.text == "<<-",
		}
		p.lex.heredocs = append(p.lex.heredocs, &svPendingHereDoc{doc: doc})
		p.advance()
		return svRedir{
			fd:      tok.fd,
			op:      tok.text,
			heredoc: doc,
			lo:      tok.lo,
			hi:      targetTok.hi,
		}, true
	}
	p.advance()
	w := targetTok.word
	return svRedir{
		fd:     tok.fd,
		op:     tok.text,
		target: &w,
		lo:     tok.lo,
		hi:     targetTok.hi,
	}, true
}

func (p *svParser) parseSubshell() *svCommand {
	start := p.cur.lo
	p.advance() // skip (
	body := p.parseList(map[string]bool{")": true})
	if p.cur.kind != svTokRParen {
		p.err = fmt.Errorf("expected )")
		return nil
	}
	hi := p.cur.hi
	p.advance()
	cmd := &svCommand{kind: svCmdSubshell, body: body, lo: start, hi: hi}
	p.parseTrailingRedirs(cmd)
	return cmd
}

func (p *svParser) parseBrace() *svCommand {
	start := p.cur.lo
	p.advance() // skip {
	body := p.parseList(map[string]bool{"}": true})
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "}" {
		p.err = fmt.Errorf("expected }")
		return nil
	}
	hi := p.cur.hi
	p.advance()
	cmd := &svCommand{kind: svCmdBrace, body: body, lo: start, hi: hi}
	p.parseTrailingRedirs(cmd)
	return cmd
}

func (p *svParser) parseIf() *svCommand {
	start := p.cur.lo
	p.advance() // skip if
	cmd := &svCommand{kind: svCmdIf, lo: start}
	for {
		cond := p.parseList(map[string]bool{"then": true})
		if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "then" {
			p.err = fmt.Errorf("expected then")
			return nil
		}
		p.advance()
		body := p.parseList(map[string]bool{"elif": true, "else": true, "fi": true})
		cmd.ifArms = append(cmd.ifArms, svIfArm{cond: cond, body: body})
		if p.cur.kind == svTokWord && isBareWord(p.cur.word) && p.cur.text == "elif" {
			p.advance()
			continue
		}
		if p.cur.kind == svTokWord && isBareWord(p.cur.word) && p.cur.text == "else" {
			p.advance()
			cmd.elseBody = p.parseList(map[string]bool{"fi": true})
		}
		if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "fi" {
			p.err = fmt.Errorf("expected fi")
			return nil
		}
		cmd.hi = p.cur.hi
		p.advance()
		break
	}
	p.parseTrailingRedirs(cmd)
	return cmd
}

func (p *svParser) parseFor() *svCommand {
	start := p.cur.lo
	p.advance() // skip for
	p.skipNewlines()
	if p.cur.kind != svTokWord {
		p.err = fmt.Errorf("expected variable after for")
		return nil
	}
	varName := p.cur.text
	p.advance()
	p.skipNewlines()
	var words []svWord
	hasIn := false
	if p.cur.kind == svTokWord && isBareWord(p.cur.word) && p.cur.text == "in" {
		hasIn = true
		p.advance()
		for p.cur.kind == svTokWord {
			words = append(words, p.cur.word)
			p.advance()
		}
		if p.cur.kind == svTokSemi || p.cur.kind == svTokNewline {
			p.advance()
		}
	} else if p.cur.kind == svTokSemi || p.cur.kind == svTokNewline {
		p.advance()
	}
	p.skipNewlines()
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "do" {
		p.err = fmt.Errorf("expected do")
		return nil
	}
	p.advance()
	body := p.parseList(map[string]bool{"done": true})
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "done" {
		p.err = fmt.Errorf("expected done")
		return nil
	}
	hi := p.cur.hi
	p.advance()
	cmd := &svCommand{
		kind:     svCmdFor,
		forVar:   varName,
		forWords: words,
		hasIn:    hasIn,
		body:     body,
		lo:       start,
		hi:       hi,
	}
	p.parseTrailingRedirs(cmd)
	return cmd
}

func (p *svParser) parseWhile(until bool) *svCommand {
	start := p.cur.lo
	p.advance() // skip while/until
	cond := p.parseList(map[string]bool{"do": true})
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "do" {
		p.err = fmt.Errorf("expected do")
		return nil
	}
	p.advance()
	body := p.parseList(map[string]bool{"done": true})
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "done" {
		p.err = fmt.Errorf("expected done")
		return nil
	}
	hi := p.cur.hi
	p.advance()
	kind := svCmdWhile
	if until {
		kind = svCmdUntil
	}
	cmd := &svCommand{kind: kind, cond: cond, body: body, lo: start, hi: hi}
	p.parseTrailingRedirs(cmd)
	return cmd
}

func (p *svParser) parseCase() *svCommand {
	start := p.cur.lo
	p.advance() // skip case
	p.skipNewlines()
	if p.cur.kind != svTokWord {
		p.err = fmt.Errorf("expected word after case")
		return nil
	}
	word := p.cur.word
	p.advance()
	p.skipNewlines()
	if p.cur.kind != svTokWord || !isBareWord(p.cur.word) || p.cur.text != "in" {
		p.err = fmt.Errorf("expected in")
		return nil
	}
	p.advance()
	var arms []svCaseArm
	for {
		p.skipNewlines()
		if p.cur.kind == svTokWord && isBareWord(p.cur.word) && p.cur.text == "esac" {
			hi := p.cur.hi
			p.advance()
			cmd := &svCommand{kind: svCmdCase, caseWord: word, caseArms: arms, lo: start, hi: hi}
			p.parseTrailingRedirs(cmd)
			return cmd
		}
		if p.cur.kind == svTokEOF {
			p.err = fmt.Errorf("expected esac")
			return nil
		}
		if p.cur.kind == svTokLParen {
			p.advance()
		}
		var pats []svWord
		for p.cur.kind == svTokWord {
			pats = append(pats, p.cur.word)
			p.advance()
			if p.cur.kind == svTokPipe {
				p.advance()
				continue
			}
			break
		}
		if p.cur.kind != svTokRParen {
			p.err = fmt.Errorf("expected ) in case pattern")
			return nil
		}
		p.advance()
		body := p.parseList(map[string]bool{";;": true, "esac": true})
		term := ";;"
		if p.cur.kind == svTokDSemi || p.cur.kind == svTokSemiAmp || p.cur.kind == svTokDSemiAmp {
			term = p.cur.text
			p.advance()
		}
		arms = append(arms, svCaseArm{patterns: pats, body: body, term: term})
	}
}

func (p *svParser) parseFuncDefKeyword() *svCommand {
	start := p.cur.lo
	p.advance() // skip function
	if p.cur.kind != svTokWord {
		p.err = fmt.Errorf("expected function name")
		return nil
	}
	name := p.cur.text
	p.advance()
	if p.cur.kind == svTokLParen {
		p.advance()
		if p.cur.kind != svTokRParen {
			p.err = fmt.Errorf("expected )")
			return nil
		}
		p.advance()
	}
	p.skipNewlines()
	body := p.parseCommand()
	if body == nil {
		p.err = fmt.Errorf("expected function body")
		return nil
	}
	return &svCommand{kind: svCmdFuncDef, funcName: name, funcBody: body, lo: start, hi: body.hi}
}

func (p *svParser) parseSimpleOrFunc() *svCommand {
	start := p.cur.lo
	cmd := &svCommand{kind: svCmdSimple, lo: start, hi: start}
	for {
		if p.cur.kind == svTokRedir {
			r, ok := p.parseOneRedir()
			if !ok {
				return nil
			}
			cmd.redirs = append(cmd.redirs, r)
			cmd.hi = r.hi
			continue
		}
		if p.cur.kind == svTokWord {
			// Check for `name() { ... }` function definition
			if len(cmd.words) == 0 && len(cmd.assigns) == 0 && len(cmd.redirs) == 0 && isBareWord(p.cur.word) && isValidIdent(p.cur.text) {
				savedWord := p.cur
				p.advance()
				if p.cur.kind == svTokLParen {
					p.advance()
					if p.cur.kind != svTokRParen {
						p.err = fmt.Errorf("expected )")
						return nil
					}
					p.advance()
					p.skipNewlines()
					body := p.parseCommand()
					if body == nil {
						p.err = fmt.Errorf("expected function body")
						return nil
					}
					return &svCommand{kind: svCmdFuncDef, funcName: savedWord.text, funcBody: body, lo: start, hi: body.hi}
				}
				// Not a function; process savedWord as assignment or word
				if a, ok := splitAssignment(savedWord.word); ok {
					cmd.assigns = append(cmd.assigns, a)
				} else {
					cmd.words = append(cmd.words, savedWord.word)
				}
				cmd.hi = savedWord.hi
				continue
			}

			if len(cmd.words) == 0 {
				if a, ok := splitAssignment(p.cur.word); ok {
					cmd.assigns = append(cmd.assigns, a)
					cmd.hi = p.cur.hi
					p.advance()
					continue
				}
			}
			cmd.words = append(cmd.words, p.cur.word)
			cmd.hi = p.cur.hi
			p.advance()
			continue
		}
		break
	}
	if len(cmd.words) == 0 && len(cmd.assigns) == 0 && len(cmd.redirs) == 0 {
		return nil
	}
	return cmd
}

func isValidIdent(s string) bool {
	if len(s) == 0 || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentChar(s[i]) {
			return false
		}
	}
	return true
}

func splitAssignment(w svWord) (svAssign, bool) {
	if len(w.parts) == 0 || w.parts[0].kind != svPartLit {
		return svAssign{}, false
	}
	first := w.parts[0].text
	eq := strings.IndexByte(first, '=')
	if eq <= 0 {
		return svAssign{}, false
	}
	name := first[:eq]
	appendOp := false
	if strings.HasSuffix(name, "+") {
		name = name[:len(name)-1]
		appendOp = true
	}
	if !isValidIdent(name) {
		return svAssign{}, false
	}
	var valParts []svWordPart
	if eq+1 < len(first) {
		valParts = append(valParts, svWordPart{kind: svPartLit, text: first[eq+1:]})
	}
	valParts = append(valParts, w.parts[1:]...)
	return svAssign{
		name:   name,
		append: appendOp,
		value:  svWord{parts: valParts, lo: w.lo + eq + 1, hi: w.hi},
		lo:     w.lo,
		hi:     w.hi,
	}, true
}

// ── Word literal & Python-like WordPrinter (words.rs) ───────────────────────

func svWordHasQuote(w svWord) bool {
	for _, pt := range w.parts {
		switch pt.kind {
		case svPartSQuote, svPartDQuote, svPartAnsiC, svPartEscaped:
			return true
		}
	}
	return false
}

// svWordLiteral mirrors analysis::literal in analysis.rs:81-101:
// Quote removal only; expansions and unquoted tilde return ("", false).
func svWordLiteralOpt(w svWord) (string, bool) {
	var out strings.Builder
	if !appendSVLiteral(w.parts, false, &out) {
		return "", false
	}
	return out.String(), true
}

func svWordLiteral(w svWord) string {
	s, ok := svWordLiteralOpt(w)
	if ok {
		return s
	}
	return ""
}

func appendSVLiteral(parts []svWordPart, quoted bool, out *strings.Builder) bool {
	for _, pt := range parts {
		switch pt.kind {
		case svPartLit:
			if !quoted && strings.ContainsRune(pt.text, '~') {
				return false
			}
			out.WriteString(pt.text)
		case svPartEscaped, svPartSQuote:
			out.WriteString(pt.text)
		case svPartDQuote:
			if !appendSVLiteral(pt.parts, true, out) {
				return false
			}
		case svPartAnsiC:
			if quoted {
				return false
			}
			if idx := strings.IndexByte(pt.text, 0); idx >= 0 {
				out.WriteString(pt.text[:idx])
			} else {
				out.WriteString(pt.text)
			}
		default:
			return false
		}
	}
	return true
}

// svQuote mirrors words::quote in words.rs:33-51.
func svQuote(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
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
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func svPartsExpand(parts []svWordPart) bool {
	for _, pt := range parts {
		switch pt.kind {
		case svPartDQuote:
			if svPartsExpand(pt.parts) {
				return true
			}
		case svPartLit, svPartEscaped, svPartSQuote, svPartAnsiC:
		default:
			return true
		}
	}
	return false
}

func svPrintWord(w svWord, inlineVars map[string]string) string {
	if len(w.parts) == 0 {
		return `""`
	}
	if len(w.parts) == 1 {
		return svPrintPart(w.parts[0], inlineVars)
	}
	return svPrintInterpolation(w.parts, inlineVars)
}

func svPrintPart(pt svWordPart, inlineVars map[string]string) string {
	switch pt.kind {
	case svPartLit, svPartEscaped, svPartSQuote, svPartAnsiC:
		return svQuote(pt.text)
	case svPartDQuote:
		return svPrintInterpolation(pt.parts, inlineVars)
	case svPartParam:
		return svPrintParam(pt.text, inlineVars)
	case svPartComsub:
		return "⟨" + strings.TrimSpace(pt.text) + "⟩"
	case svPartArith:
		return "$((" + strings.TrimSpace(pt.text) + "))"
	default:
		return pt.raw
	}
}

func svPrintInterpolation(parts []svWordPart, inlineVars map[string]string) string {
	dynamic := svPartsExpand(parts)
	var content strings.Builder
	svInterpolate(parts, dynamic, inlineVars, &content)
	if dynamic {
		return `f"` + content.String() + `"`
	}
	return `"` + content.String() + `"`
}

func svInterpolate(parts []svWordPart, dynamic bool, inlineVars map[string]string, content *strings.Builder) {
	for _, pt := range parts {
		var literal string
		switch pt.kind {
		case svPartDQuote:
			svInterpolate(pt.parts, dynamic, inlineVars, content)
			continue
		case svPartLit, svPartEscaped, svPartSQuote, svPartAnsiC:
			literal = pt.text
		default:
			content.WriteByte('{')
			content.WriteString(svPrintPart(pt, inlineVars))
			content.WriteByte('}')
			continue
		}
		escaped := svQuote(literal)
		escaped = escaped[1 : len(escaped)-1]
		if dynamic {
			for _, r := range escaped {
				content.WriteRune(r)
				if r == '{' || r == '}' {
					content.WriteRune(r)
				}
			}
		} else {
			content.WriteString(escaped)
		}
	}
}

func svPrintParam(body string, inlineVars map[string]string) string {
	if val, ok := inlineVars[body]; ok {
		return "‹" + val + "›"
	}
	switch body {
	case "@":
		return "args"
	case "*":
		return "joined_args"
	case "#":
		return "argc"
	case "?":
		return "status"
	case "-":
		return "shell_flags"
	case "$":
		return "shell_pid"
	case "!":
		return "last_pid"
	}
	if len(body) > 0 && isAllDigits(body) {
		return "arg" + body
	}
	if strings.HasPrefix(body, "#") && len(body) > 1 {
		return "len(" + body[1:] + ")"
	}
	for _, op := range []string{":-", "-", ":=", "=", ":+", "+", ":?", "?"} {
		if idx := strings.Index(body, op); idx > 0 {
			name := body[:idx]
			if !isValidShellIdent(name) && !isAllDigits(name) {
				continue
			}
			arg := body[idx+len(op):]
			colon := op[0] == ':'
			pres := "is_set(" + name + ")"
			if colon {
				pres = "nonempty(" + name + ")"
			}
			qArg := svQuote(arg)
			switch op[len(op)-1] {
			case '-':
				return fmt.Sprintf("(%s if %s else %s)", name, pres, qArg)
			case '=':
				return fmt.Sprintf("default_assign(%s, %s, %s)", name, qArg, pres)
			case '+':
				return fmt.Sprintf("(%s if %s else \"\")", qArg, pres)
			case '?':
				if arg == "" {
					qArg = svQuote("parameter null or not set")
				}
				return fmt.Sprintf("require(%s, %s, %s)", name, qArg, pres)
			}
		}
	}
	return body
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func svPrintAssign(a svAssign, inlineVars map[string]string) string {
	op := "="
	if a.append {
		op = "+="
	}
	return a.name + " " + op + " " + svPrintWord(a.value, inlineVars)
}

// ── Embedded language detection & highlighting (embedded.rs) ────────────────

var svShellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
}

func svBasename(s string) string {
	if idx := strings.LastIndexByte(s, '/'); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

func svSetsFlag(word string, flag byte) bool {
	if !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") || len(word) < 2 {
		return false
	}
	return strings.IndexByte(word[1:], flag) >= 0
}

func svIsAssignWord(word string) bool {
	eq := strings.IndexByte(word, '=')
	return eq > 0 && isValidIdent(word[:eq])
}

func svOperand(words []string, from int, takesValue func(string) bool) int {
	i := from
	for i < len(words) {
		w := words[i]
		if w == "--" {
			return i + 1
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			break
		}
		if takesValue(w) {
			i += 2
		} else {
			i++
		}
	}
	return i
}

func svLauncher(words []string) (skip int, remote bool, ok bool) {
	if len(words) == 0 {
		return 0, false, false
	}
	switch svBasename(words[0]) {
	case "env":
		at := svOperand(words, 1, func(w string) bool {
			switch w {
			case "-u", "--unset", "-C", "--chdir", "-S", "--split-string", "-P":
				return true
			}
			return false
		})
		for at < len(words) && svIsAssignWord(words[at]) {
			at++
		}
		return at, false, true
	case "sudo", "doas":
		at := svOperand(words, 1, func(w string) bool {
			switch w {
			case "-u", "-g", "-C", "-D", "-h", "-p", "-R", "-r", "-T", "-t", "-U",
				"--user", "--group", "--close-from", "--chdir", "--host", "--prompt",
				"--chroot", "--role", "--command-timeout", "--type", "--other-user":
				return true
			}
			return false
		})
		for at < len(words) && svIsAssignWord(words[at]) {
			at++
		}
		return at, false, true
	case "timeout", "gtimeout":
		at := svOperand(words, 1, func(w string) bool {
			return w == "-s" || w == "--signal" || w == "-k" || w == "--kill-after"
		})
		return at + 1, false, true
	case "nice":
		return svOperand(words, 1, func(w string) bool { return w == "-n" || w == "--adjustment" }), false, true
	case "nohup", "time":
		return svOperand(words, 1, func(w string) bool { return w == "-o" || w == "--output" || w == "-f" || w == "--format" }), false, true
	case "stdbuf":
		return svOperand(words, 1, func(w string) bool {
			switch w {
			case "-i", "-o", "-e", "--input", "--output", "--error":
				return true
			}
			return false
		}), false, true
	case "exec":
		return svOperand(words, 1, func(w string) bool { return w == "-a" }), false, true
	case "ssh":
		host := svOperand(words, 1, func(w string) bool {
			return len(w) == 2 && w[0] == '-' && strings.ContainsRune("pilojFSLRDWbcEemOQw", rune(w[1]))
		})
		if host < len(words) {
			return host + 1, true, true
		}
		return 0, false, false
	}
	return 0, false, false
}

func svLaunch(words []string) (at int, remote bool) {
	for {
		skip, rem, ok := svLauncher(words[at:])
		if !ok || skip == 0 {
			break
		}
		at = min(at+skip, len(words))
		remote = remote || rem
	}
	return at, remote
}

type svArgLangCtx struct {
	words          []string
	at             int
	remote         bool
	shellCArgIdx   int
	positionalLang string
	positionalIdx  int
}

func newSVArgLangCtx(words []string) svArgLangCtx {
	at, remote := svLaunch(words)
	ctx := svArgLangCtx{
		words:         words,
		at:            at,
		remote:        remote,
		shellCArgIdx:  -1,
		positionalIdx: -1,
	}
	for i, w := range words {
		if svShellNames[svBasename(w)] {
			for j := i + 1; j < len(words) && strings.HasPrefix(words[j], "-"); j++ {
				if svSetsFlag(words[j], 'c') {
					if j+1 < len(words) {
						ctx.shellCArgIdx = j + 1
					}
					break
				}
			}
			if ctx.shellCArgIdx >= 0 {
				break
			}
		}
	}
	if at < len(words) {
		args := words[at:]
		command := svBasename(args[0])
		var lang string
		switch command {
		case "jq":
			lang = "jq"
		case "awk", "gawk":
			lang = "awk"
		case "grep", "egrep", "rg":
			lang = "regex"
		}
		if lang != "" {
			ctx.positionalLang = lang
			for i := 1; i < len(args); {
				arg := args[i]
				if arg == "--arg" || arg == "--argjson" {
					i += 3
					continue
				}
				if (arg == "-e" || arg == "--regexp") && lang == "regex" {
					ctx.positionalIdx = i + 1
					break
				}
				switch arg {
				case "--argfile", "--slurpfile", "--rawfile":
					i += 3
					continue
				case "-F", "-f", "-m", "-A", "-B", "-C", "--file":
					i += 2
					continue
				}
				if arg == "--" {
					ctx.positionalIdx = i + 1
					break
				}
				if strings.HasPrefix(arg, "-") {
					i++
					continue
				}
				ctx.positionalIdx = i
				break
			}
		}
	}
	return ctx
}

func (c svArgLangCtx) languageAt(index int) string {
	if c.remote && c.at+1 == len(c.words) && index == c.at {
		return "bash"
	}
	if c.shellCArgIdx >= 0 && index == c.shellCArgIdx {
		return "bash"
	}
	if index < c.at || c.at >= len(c.words) {
		return ""
	}
	args := c.words[c.at:]
	idx := index - c.at
	command := svBasename(args[0])
	prev := ""
	if idx > 0 && idx-1 < len(args) {
		prev = args[idx-1]
	}
	if strings.HasPrefix(command, "python") && prev == "-c" {
		return "py"
	}
	if (command == "node" || command == "bun" || command == "deno") && (prev == "-e" || prev == "--eval") {
		return "js"
	}
	if command == "gh" && (prev == "--jq" || prev == "-q") {
		return "jq"
	}
	if command == "curl" && (prev == "-d" || prev == "--data" || prev == "--data-raw" || prev == "--data-binary" || prev == "--json") {
		return "json"
	}
	if (command == "docker" || command == "podman") && prev == "--format" {
		return "gotmpl"
	}
	if c.positionalLang != "" && idx == c.positionalIdx {
		return c.positionalLang
	}
	return ""
}

func svArgumentLanguage(words []string, index int) string {
	return newSVArgLangCtx(words).languageAt(index)
}

func svHeredocLanguage(words []string, targets []string, delim, body string) string {
	at, remote := svLaunch(words)
	rem := words[at:]
	if len(rem) == 0 && remote {
		return "bash"
	}
	if len(rem) > 0 {
		if l := svInterpreter(rem[0]); l != "" {
			return l
		}
	}
	if strings.HasPrefix(body, "#!") {
		firstLine := body[2:]
		if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
			firstLine = firstLine[:nl]
		}
		fields := strings.Fields(firstLine)
		if len(fields) > 0 {
			prog := fields[0]
			if svBasename(prog) == "env" {
				for _, f := range fields[1:] {
					if !strings.HasPrefix(f, "-") && !svIsAssignWord(f) {
						prog = f
						break
					}
				}
			}
			if l := svInterpreter(prog); l != "" {
				return l
			}
		}
	}
	for _, w := range append(append([]string(nil), words...), targets...) {
		if l := svExtLanguage(w); l != "" {
			return l
		}
	}
	switch strings.ToUpper(delim) {
	case "PY", "PYTHON":
		return "py"
	case "JS", "JAVASCRIPT", "TS":
		return "js"
	case "JSON":
		return "json"
	case "TOML":
		return "toml"
	case "SH", "BASH", "SHELL":
		return "bash"
	case "SQL":
		return "sql"
	case "YAML", "YML":
		return "yaml"
	case "AWK":
		return "awk"
	case "JQ":
		return "jq"
	case "HTML":
		return "html"
	case "CSS":
		return "css"
	default:
		return "text"
	}
}

func svInterpreter(prog string) string {
	name := svBasename(prog)
	switch {
	case strings.HasPrefix(name, "python"):
		return "py"
	case name == "node" || name == "bun" || name == "deno":
		return "js"
	case name == "git":
		return "commit"
	case name == "psql" || name == "mysql" || name == "sqlite3" || name == "duckdb":
		return "sql"
	case svShellNames[name]:
		return "bash"
	default:
		return ""
	}
}

func svExtLanguage(p string) string {
	dot := strings.LastIndexByte(p, '.')
	if dot < 0 || dot == len(p)-1 {
		return ""
	}
	switch p[dot+1:] {
	case "py", "pyi":
		return "py"
	case "js", "mjs", "cjs", "jsx", "ts", "mts", "cts", "tsx":
		return "js"
	case "json":
		return "json"
	case "toml":
		return "toml"
	case "sh", "bash", "zsh":
		return "bash"
	case "sql":
		return "sql"
	case "yml", "yaml":
		return "yaml"
	case "rs":
		return "rust"
	case "go":
		return "go"
	case "c", "h", "cc", "cpp", "cxx", "hpp":
		return "c"
	case "swift":
		return "swift"
	case "css":
		return "css"
	case "html", "htm":
		return "html"
	default:
		return ""
	}
}

func svEmbeddedKeyword(lang, s string) bool {
	var list string
	switch lang {
	case "py":
		list = "import from as for in if elif else while def return with break continue pass not and or is None True False lambda class try except finally raise yield async await"
	case "js", "ts":
		list = "import from export const let var function return if else for of in new await async true false null undefined typeof class throw try catch"
	case "jq":
		list = "if then elif else end select map keys length join and or not as def reduce empty test split tostring tonumber"
	case "awk":
		list = "BEGIN END print printf if else for while in int length split substr NR NF"
	case "json", "toml", "yaml":
		list = "true false null"
	case "gotmpl":
		list = "range end if else with json join printf define template block"
	case "bash", "sh":
		list = "if then elif else fi for in do done while until case esac function select local export return"
	case "sql":
		list = "select from where insert into values update set delete create table join on as and or not null true false group by order having limit union distinct"
	case "rust":
		list = "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while"
	case "go":
		list = "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false"
	case "c":
		list = "auto break case char const continue default do double else enum extern float for goto if inline int long register return short signed sizeof static struct switch typedef union unsigned void volatile while bool true false NULL nullptr class namespace template typename public private protected virtual new delete this using"
	case "swift":
		list = "import func let var if else guard return struct class enum protocol extension for in while repeat switch case default break continue true false nil self Self init throws throw try catch async await public private internal fileprivate static override final mutating weak defer where is as"
	default:
		return false
	}
	for _, w := range strings.Fields(list) {
		if lang == "sql" {
			if strings.EqualFold(w, s) {
				return true
			}
		} else if w == s {
			return true
		}
	}
	return false
}

// svHighlightEmbedded mirrors embedded::highlight in embedded.rs:408-551.
func svHighlightEmbedded(text, language, node string, rng []int) []map[string]any {
	var result []map[string]any
	i := 0
	hashComment := language == "py" || language == "toml" || language == "yaml" || language == "bash" || language == "sh" || language == "awk" || language == "commit"
	cComments := language == "js" || language == "ts" || language == "rust" || language == "go" || language == "c" || language == "swift"
	runStart := -1
	runStyle := ""
	flushRun := func(end int) {
		if runStart < 0 || runStart >= end {
			return
		}
		seg := map[string]any{
			"text":     text[runStart:end],
			"node":     node,
			"language": language,
		}
		if runStyle != "" {
			seg["style"] = runStyle
		}
		if rng != nil {
			seg["range"] = rng
		}
		result = append(result, seg)
		runStart = -1
	}
	for i < len(text) {
		start := i
		style := ""
		if (hashComment && text[i] == '#') ||
			((cComments || language == "jsonc") && strings.HasPrefix(text[i:], "//")) ||
			(language == "sql" && strings.HasPrefix(text[i:], "--")) {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			style = "comment"
		} else if (cComments || language == "css") && strings.HasPrefix(text[i:], "/*") {
			i += 2
			for i < len(text) && !strings.HasPrefix(text[i:], "*/") {
				_, sz := utf8.DecodeRuneInString(text[i:])
				i += sz
			}
			if i < len(text) {
				i += 2
			}
			style = "comment"
		} else if language == "rust" && text[i] == '\'' && i+1 < len(text) && isIdentStart(text[i+1]) && !(i+2 < len(text) && text[i+2] == '\'') {
			i++
			for i < len(text) && isIdentChar(text[i]) {
				i++
			}
			style = "var"
		} else if (text[i] == '\'' || text[i] == '"' || text[i] == '`') && language != "regex" {
			q := text[i]
			triple := language == "py" && i+2 < len(text) && text[i+1] == q && text[i+2] == q
			if triple {
				i += 3
			} else {
				i++
			}
			for i < len(text) {
				if text[i] == '\\' {
					i++
					if i < len(text) {
						_, sz := utf8.DecodeRuneInString(text[i:])
						i += sz
					}
					continue
				}
				if text[i] == q && (!triple || (i+2 < len(text) && text[i+1] == q && text[i+2] == q)) {
					if triple {
						i += 3
					} else {
						i++
					}
					break
				}
				_, sz := utf8.DecodeRuneInString(text[i:])
				i += sz
			}
			if language == "json" && strings.HasPrefix(strings.TrimLeft(text[i:], " \t"), ":") {
				style = "key"
			} else {
				style = "str"
			}
		} else if language == "regex" && text[i] == '\\' {
			i++
			if i < len(text) {
				_, sz := utf8.DecodeRuneInString(text[i:])
				i += sz
			}
			style = "field"
		} else if language == "regex" && text[i] == '[' {
			i++
			for i < len(text) {
				if text[i] == '\\' {
					i++
					if i < len(text) {
						_, sz := utf8.DecodeRuneInString(text[i:])
						i += sz
					}
					continue
				}
				if text[i] == ']' {
					i++
					break
				}
				_, sz := utf8.DecodeRuneInString(text[i:])
				i += sz
			}
			style = "section"
		} else if text[i] >= '0' && text[i] <= '9' {
			i++
			for i < len(text) && (isIdentChar(text[i]) || text[i] == '.') {
				i++
			}
			style = "num"
		} else if isIdentStart(text[i]) || text[i] == '$' {
			i++
			for i < len(text) && (isIdentChar(text[i]) || text[i] == '$') {
				i++
			}
			word := text[start:i]
			trimmedRest := strings.TrimLeft(text[i:], " \t")
			if svEmbeddedKeyword(language, word) {
				style = "kw"
			} else if strings.HasPrefix(trimmedRest, "(") {
				style = "fn"
			} else if strings.HasPrefix(word, "$") {
				style = "var"
			} else if (language == "toml" || language == "yaml") && (strings.HasPrefix(trimmedRest, "=") || strings.HasPrefix(trimmedRest, ":")) {
				style = "key"
			}
		} else {
			r, sz := utf8.DecodeRuneInString(text[i:])
			i += sz
			if language == "regex" && (r == '^' || r == '$') {
				style = "kw"
			} else if language == "regex" && strings.ContainsRune("*+?{}", r) {
				style = "num"
			} else if strings.ContainsRune("{}[]()|=+*/<>!:,;.", r) {
				style = "op"
			}
			if r == '.' && (language == "jq" || language == "gotmpl") {
				for i < len(text) && isIdentChar(text[i]) {
					i++
				}
				style = "field"
			}
		}
		if runStart < 0 {
			runStart = start
			runStyle = style
		} else if runStyle != style {
			flushRun(start)
			runStart = start
			runStyle = style
		}
	}
	flushRun(len(text))
	return result
}

// ── Heuristic Effects (analysis.rs:1012-1168) ───────────────────────────────

func svComputeEffects(cmd *svCommand, background bool) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	add := func(kind, glyph, label, why string) {
		k := kind + "|" + why
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, map[string]any{
			"kind":  kind,
			"glyph": glyph,
			"label": label,
			"why":   why + " (heuristic, not a safety guarantee)",
		})
	}
	var name string
	var args []string
	if cmd.kind == svCmdSimple && len(cmd.words) > 0 {
		for _, w := range cmd.words {
			if lit, ok := svWordLiteralOpt(w); ok {
				args = append(args, lit)
			}
		}
		if len(args) > 0 {
			name = args[0]
		}
	}
	has := func(vals ...string) bool {
		for _, a := range args[min(1, len(args)):] {
			for _, v := range vals {
				if a == v {
					return true
				}
			}
		}
		return false
	}
	joined := strings.Join(args, " ")

	packages := (name == "bun" || name == "npm" || name == "pnpm" || name == "yarn") && has("install", "add", "i")
	network := (name == "curl" || name == "wget" || name == "gh" || name == "ssh" || name == "scp" || name == "rsync") ||
		(name == "git" && has("fetch", "push", "pull", "clone"))
	if network {
		host := "remote"
		if name == "gh" {
			host = "api.github.com"
		}
		for _, a := range args {
			if rest, ok := strings.CutPrefix(a, "https://"); ok {
				host = strings.SplitN(rest, "/", 2)[0]
				break
			}
			if rest, ok := strings.CutPrefix(a, "http://"); ok {
				host = strings.SplitN(rest, "/", 2)[0]
				break
			}
		}
		add("network", "⇅", "network", name+" → "+host)
	}
	if name == "rm" || name == "kill" || name == "pkill" ||
		(name == "git" && ((has("reset") && has("--hard")) || (has("branch") && has("-D", "-d", "--delete")) || (has("worktree") && has("remove")))) {
		add("destructive", "×", "destructive", joined)
	}
	isSedInPlace := name == "sed" && func() bool {
		for _, a := range args {
			if strings.HasPrefix(a, "-i") {
				return true
			}
		}
		return false
	}()
	if name == "mkdir" || name == "touch" || name == "cp" || name == "mv" || name == "ln" || name == "tee" || name == "install" ||
		isSedInPlace ||
		(name == "git" && has("add", "commit", "rebase", "checkout", "switch", "merge", "cherry-pick", "stash", "tag", "branch", "worktree")) {
		add("write", "✎", "write", joined)
	}
	if packages {
		add("network", "⇅", "network", "package installation fetches packages")
		add("write", "✎", "write", "package installation writes dependencies")
	}
	if name == "sudo" || name == "su" || name == "runuser" || name == "chmod" || name == "chown" {
		add("privilege", "⇪", "privilege", joined)
	}
	if background {
		add("background", "⇉", "background", "runs in background")
	}
	if name == "docker" && has("exec", "run", "compose", "start", "stop", "restart") {
		add("background", "⇉", "process", joined)
	}
	if (name == "source" || name == ".") && func() bool {
		for _, a := range args {
			if strings.Contains(a, ".env") {
				return true
			}
		}
		return false
	}() {
		add("secret", "◆", "secret", "loads environment file")
	}
	for _, r := range cmd.redirs {
		if r.target != nil {
			switch r.op {
			case ">", ">>", ">|", "<>", "&>", "&>>":
				target, ok := svWordLiteralOpt(*r.target)
				if !ok {
					target = "dynamic target"
				}
				if !strings.HasPrefix(target, "/dev/") {
					add("write", "✎", "write", "writes "+target)
				}
			}
		}
		if r.heredoc != nil && (strings.Contains(r.heredoc.body, ".env") || strings.Contains(r.heredoc.body, "API_KEY")) {
			add("secret", "◆", "secret", "embedded program reads an environment secret")
		}
	}
	return out
}

// ── Pseudo Printer (printer.rs) ─────────────────────────────────────────────

type svPrinter struct {
	src      string
	u16      []int
	rows     []map[string]any
	serial   int
	nodeSeq  int
	running  bool
	exitCode *int
	funcs    map[string]bool
}

func buildShellVizRows(command string, running bool, exitCode *int) []any {
	if len(command) > maxShellVizBytes {
		u16 := buildUTF16Map(command)
		p := &svPrinter{src: command, u16: u16, running: running, exitCode: exitCode, funcs: map[string]bool{}}
		p.emitFallback(command)
		out := make([]any, len(p.rows))
		for i, r := range p.rows {
			out[i] = r
		}
		return out
	}
	u16 := buildUTF16Map(command)
	p := &svPrinter{
		src:      command,
		u16:      u16,
		running:  running,
		exitCode: exitCode,
		funcs:    map[string]bool{},
	}
	items, err := parseBashScript(command)
	if err != nil || len(items) == 0 {
		// Invalid syntax or comment-only/whitespace fallback: emit literal rows
		// matching printer.rs `pending`.
		p.emitFallback(command)
	} else {
		p.collectFuncNames(items)
		p.printList(items, 0)
	}
	// Mark final statement state if exitCode or running is known.
	if len(p.rows) > 0 {
		if running {
			p.rows[len(p.rows)-1]["state"] = "running"
		} else if exitCode != nil {
			st := "ok"
			if *exitCode != 0 {
				st = "fail"
			}
			for i := len(p.rows) - 1; i >= 0; i-- {
				if p.rows[i]["role"] == "stmt" {
					p.rows[i]["state"] = st
					break
				}
			}
		}
	}

	out := make([]any, len(p.rows))
	for i, r := range p.rows {
		out[i] = r
	}
	return out
}

func (p *svPrinter) nextNode() string {
	p.nodeSeq++
	return fmt.Sprintf("0:0:%d", p.nodeSeq)
}

func (p *svPrinter) emitFallback(src string) {
	if strings.TrimSpace(src) == "" {
		return
	}
	lines := strings.Split(src, "\n")
	offset := 0
	for i, rawLine := range lines {
		if i == len(lines)-1 && rawLine == "" {
			break
		}
		dispLine := strings.TrimSuffix(rawLine, "\r")
		lo := p.u16[min(offset, len(p.src))]
		hi := p.u16[min(offset+len(dispLine), len(p.src))]
		p.rows = append(p.rows, map[string]any{
			"id":    fmt.Sprintf("pending-%d", offset),
			"depth": 0,
			"range": []int{lo, hi},
			"role":  "raw",
			"segments": []any{
				map[string]any{
					"text":  dispLine,
					"style": "str uncompiled",
					"range": []int{lo, hi},
				},
			},
			"effects": []any{},
			"detail":  "Literal shell source not yet announced as a compiled unit.",
		})
		offset += len(rawLine) + 1
	}
}

func (p *svPrinter) collectFuncNames(items []*svListItem) {
	for _, it := range items {
		if it.andOr == nil {
			continue
		}
		for _, pl := range append([]*svPipeline{it.andOr.first}, it.andOr.rest...) {
			for _, st := range pl.stages {
				if st.kind == svCmdFuncDef && st.funcName != "" {
					p.funcs[st.funcName] = true
				}
			}
		}
	}
}

func (p *svPrinter) seg(node string, lo, hi int, text, style string) map[string]any {
	s := map[string]any{
		"text":  text,
		"node":  node,
		"range": utf16Range(p.u16, lo, hi),
	}
	if style != "" {
		s["style"] = style
	}
	return s
}

func (p *svPrinter) emitRow(node string, lo, hi, depth int, role string, segments []map[string]any, effects []map[string]any) {
	p.serial++
	segsAny := make([]any, len(segments))
	for i, s := range segments {
		segsAny[i] = s
	}
	effAny := make([]any, len(effects))
	for i, e := range effects {
		effAny[i] = e
	}
	detail := ""
	if lo >= 0 && hi <= len(p.src) && lo <= hi {
		detail = p.src[lo:hi]
	}
	row := map[string]any{
		"id":       fmt.Sprintf("row-%s-%d", node, p.serial),
		"depth":    depth,
		"node":     node,
		"range":    utf16Range(p.u16, lo, hi),
		"role":     role,
		"segments": segsAny,
		"effects":  effAny,
	}
	if detail != "" {
		row["detail"] = detail
	}
	p.rows = append(p.rows, row)
}

func (p *svPrinter) printList(items []*svListItem, depth int) {
	if depth > maxShellVizDepth {
		return
	}
	for i := 0; i < len(items); i++ {
		// set -a ; source/. file ; set +a idiom
		if i+2 < len(items) {
			c0 := singleSimple(items[i].andOr)
			c1 := singleSimple(items[i+1].andOr)
			c2 := singleSimple(items[i+2].andOr)
			if isSimpleWithArg(c0, "set", "-a") &&
				(isSimpleCmd(c1, ".") || isSimpleCmd(c1, "source")) &&
				isSimpleWithArg(c2, "set", "+a") {
				node := p.nextNode()
				var argParts []string
				for _, w := range c1.words[1:] {
					argParts = append(argParts, svPrintWord(w, nil))
				}
				seg := p.seg(node, c1.lo, c1.hi, fmt.Sprintf("load_env(%s) exports all", strings.Join(argParts, ", ")), "fn badge")
				seg["expanded"] = p.src[c0.lo:c2.hi]
				p.emitRow(node, c1.lo, c1.hi, depth, "stmt", []map[string]any{seg}, svComputeEffects(c1, false))
				i += 2
				continue
			}
		}
		bg := items[i].sep == "&"
		p.printAndOr(items[i].andOr, depth, bg)
	}
}

func singleSimple(ao *svAndOr) *svCommand {
	if ao == nil || len(ao.rest) > 0 || ao.first == nil || ao.first.bang || ao.first.timed || len(ao.first.stages) != 1 {
		return nil
	}
	st := ao.first.stages[0]
	if st.kind != svCmdSimple {
		return nil
	}
	return st
}

func isSimpleCmd(c *svCommand, name string) bool {
	if c == nil || c.kind != svCmdSimple || len(c.redirs) > 0 || len(c.assigns) > 0 || len(c.words) < 2 {
		return false
	}
	n, ok := svWordLiteralOpt(c.words[0])
	return ok && n == name
}

func isSimpleWithArg(c *svCommand, name, arg string) bool {
	if !isSimpleCmd(c, name) || len(c.words) != 2 {
		return false
	}
	a, ok := svWordLiteralOpt(c.words[1])
	return ok && a == arg
}

func (p *svPrinter) printAndOr(ao *svAndOr, depth int, bg bool) {
	if ao == nil || ao.first == nil {
		return
	}
	if len(ao.rest) == 0 && len(ao.first.stages) == 1 && !ao.first.bang && !ao.first.timed {
		p.printCommand(ao.first.stages[0], depth, bg)
		return
	}
	allSimple := true
	for _, pl := range append([]*svPipeline{ao.first}, ao.rest...) {
		for _, st := range pl.stages {
			if st.kind != svCmdSimple && st.kind != svCmdCond && st.kind != svCmdArith {
				allSimple = false
				break
			}
		}
	}
	if allSimple {
		type rowSpec struct {
			pl   *svPipeline
			d    int
			segs []map[string]any
			effs []map[string]any
		}
		var lead []map[string]any
		aoNode := p.nextNode()
		if bg {
			lead = append(lead, p.seg(aoNode, ao.lo, ao.hi, "spawn ", "kw"))
		}
		firstSegs, firstEffs := p.printPipeline(ao.first, bg)
		lead = append(lead, firstSegs...)
		specs := []rowSpec{{pl: ao.first, d: depth, segs: lead, effs: firstEffs}}
		for i, pl := range ao.rest {
			op := ao.ops[i]
			// `cmd || true` / `cmd || :` idiom -> ` may fail` badge
			if i == len(ao.rest)-1 && op == "||" && !pl.bang && !pl.timed && len(pl.stages) == 1 {
				st := pl.stages[0]
				if st.kind == svCmdSimple && len(st.assigns) == 0 && len(st.redirs) == 0 && len(st.words) == 1 {
					if name, ok := svWordLiteralOpt(st.words[0]); ok && (name == "true" || name == ":") {
						stNode := p.nextNode()
						badge := p.seg(stNode, st.lo, st.hi, " may fail", "badge")
						badge["expanded"] = "|| " + p.src[st.lo:st.hi]
						specs[len(specs)-1].segs = append(specs[len(specs)-1].segs, badge)
						continue
					}
				}
			}
			conn := "and "
			if op == "||" {
				conn = "or "
			}
			segs := []map[string]any{p.seg(aoNode, ao.lo, ao.hi, conn, "op")}
			pSegs, pEffs := p.printPipeline(pl, false)
			segs = append(segs, pSegs...)
			specs = append(specs, rowSpec{pl: pl, d: depth + 1, segs: segs, effs: pEffs})
		}
		for _, sp := range specs {
			head := sp.pl.stages[0]
			node := p.nextNode()
			p.emitRow(node, head.lo, sp.pl.hi, sp.d, "stmt", sp.segs, sp.effs)
			for _, st := range sp.pl.stages {
				p.emitCommandDocuments(st, sp.d)
			}
		}
		return
	}
	// Compound in and-or/pipeline
	allPls := append([]*svPipeline{ao.first}, ao.rest...)
	for pi, pl := range allPls {
		prevStderr := false
		for si, st := range pl.stages {
			startRow := len(p.rows)
			d := depth
			if pi > 0 || si > 0 {
				d = depth + 1
			}
			p.printCommand(st, d, bg)
			var prefix string
			if si > 0 {
				if prevStderr {
					prefix = "→& "
				} else {
					prefix = "→ "
				}
			} else if pi > 0 {
				if ao.ops[pi-1] == "&&" {
					prefix = "and "
				} else {
					prefix = "or "
				}
			}
			prevStderr = st.pipeStderr
			if si == 0 && pl.bang {
				prefix += "not "
			}
			if si == 0 && pl.timed {
				prefix += "timed "
			}
			if prefix != "" && startRow < len(p.rows) {
				pNode := p.nextNode()
				prefSeg := p.seg(pNode, pl.lo, pl.hi, prefix, "op")
				oldSegs := p.rows[startRow]["segments"].([]any)
				p.rows[startRow]["segments"] = append([]any{prefSeg}, oldSegs...)
			}
		}
	}
}

func (p *svPrinter) printPipeline(pl *svPipeline, bg bool) ([]map[string]any, []map[string]any) {
	var out []map[string]any
	var effs []map[string]any
	plNode := p.nextNode()
	if pl.bang {
		out = append(out, p.seg(plNode, pl.lo, pl.hi, "not ", "kw"))
	}
	if pl.timed {
		out = append(out, p.seg(plNode, pl.lo, pl.hi, "timed ", "badge"))
	}
	prevStderr := false
	for i, cmd := range pl.stages {
		pipeStderr := prevStderr
		prevStderr = cmd.pipeStderr
		if i > 0 && i == len(pl.stages)-1 && len(cmd.redirs) == 0 && cmd.kind == svCmdSimple {
			if kind, count, ok := tailCount(cmd); ok {
				cmdNode := p.nextNode()
				label := fmt.Sprintf(" ⇡ first %d", count)
				if kind == "tail" {
					label = fmt.Sprintf(" ⇣ last %d", count)
				}
				seg := p.seg(cmdNode, cmd.lo, cmd.hi, label, "badge")
				seg["expanded"] = "| " + p.src[cmd.lo:cmd.hi]
				out = append(out, seg)
				continue
			}
		}
		if i > 0 {
			arrow := " → "
			if pipeStderr {
				arrow = " →& "
			}
			out = append(out, p.seg(plNode, pl.lo, pl.hi, arrow, "op"))
		}
		out = append(out, p.inlineCommand(cmd)...)
		effs = append(effs, svComputeEffects(cmd, bg && i == 0)...)
	}
	return out, effs
}

func tailCount(cmd *svCommand) (string, int, bool) {
	if cmd.kind != svCmdSimple || len(cmd.assigns) > 0 || len(cmd.words) == 0 {
		return "", 0, false
	}
	name, ok := svWordLiteralOpt(cmd.words[0])
	if !ok || (name != "head" && name != "tail") {
		return "", 0, false
	}
	args := make([]string, 0, len(cmd.words)-1)
	for _, w := range cmd.words[1:] {
		v, ok := svWordLiteralOpt(w)
		if !ok {
			return "", 0, false
		}
		args = append(args, v)
	}
	parseNum := func(s string) (int, bool) {
		if !isAllDigits(s) {
			return 0, false
		}
		n := 0
		for i := range len(s) {
			n = n*10 + int(s[i]-'0')
		}
		return n, true
	}
	switch len(args) {
	case 0:
		return name, 10, true
	case 2:
		if args[0] == "-n" {
			if n, ok := parseNum(args[1]); ok {
				return name, n, true
			}
		}
	case 1:
		if rest, ok := strings.CutPrefix(args[0], "-n"); ok {
			if n, ok := parseNum(rest); ok {
				return name, n, true
			}
		} else if rest, ok := strings.CutPrefix(args[0], "-"); ok {
			if n, ok := parseNum(rest); ok {
				return name, n, true
			}
		}
	}
	return "", 0, false
}

func (p *svPrinter) inlineCommand(cmd *svCommand) []map[string]any {
	node := p.nextNode()
	switch cmd.kind {
	case svCmdSimple:
		return p.printSimple(cmd, node)
	case svCmdCond:
		return []map[string]any{p.seg(node, cmd.lo, cmd.hi, svPrintCond(cmd.condText), "var")}
	case svCmdArith:
		return []map[string]any{p.seg(node, cmd.lo, cmd.hi, "(( "+cmd.arithSrc+" ))", "var")}
	default:
		return []map[string]any{p.seg(node, cmd.lo, cmd.hi, p.src[cmd.lo:cmd.hi], "kw")}
	}
}

func svPrintCond(inner string) string {
	fields := strings.Fields(inner)
	if len(fields) == 2 {
		op, arg := fields[0], fields[1]
		switch op {
		case "-f":
			return "is_file(" + svCondArg(arg) + ")"
		case "-d":
			return "is_dir(" + svCondArg(arg) + ")"
		case "-e":
			return "exists(" + svCondArg(arg) + ")"
		case "-n":
			return "nonempty(" + svCondArg(arg) + ")"
		case "-z":
			return "empty(" + svCondArg(arg) + ")"
		case "-r":
			return "readable(" + svCondArg(arg) + ")"
		case "-w":
			return "writable(" + svCondArg(arg) + ")"
		case "-x":
			return "executable(" + svCondArg(arg) + ")"
		case "-s":
			return "has_size(" + svCondArg(arg) + ")"
		case "-L", "-h":
			return "is_symlink(" + svCondArg(arg) + ")"
		case "-v":
			return "is_set(" + svCondArg(arg) + ")"
		}
	}
	if len(fields) == 3 {
		lhs, op, rhs := svCondArg(fields[0]), fields[1], svCondArg(fields[2])
		switch op {
		case "=~":
			return fmt.Sprintf("regex(%s, %s)", lhs, rhs)
		case "=", "==":
			return fmt.Sprintf("matches(%s, %s)", lhs, rhs)
		case "!=":
			return fmt.Sprintf("not matches(%s, %s)", lhs, rhs)
		case "-eq":
			return fmt.Sprintf("int(%s) == int(%s)", lhs, rhs)
		case "-ne":
			return fmt.Sprintf("int(%s) != int(%s)", lhs, rhs)
		case "-lt":
			return fmt.Sprintf("int(%s) < int(%s)", lhs, rhs)
		case "-le":
			return fmt.Sprintf("int(%s) <= int(%s)", lhs, rhs)
		case "-gt":
			return fmt.Sprintf("int(%s) > int(%s)", lhs, rhs)
		case "-ge":
			return fmt.Sprintf("int(%s) >= int(%s)", lhs, rhs)
		}
	}
	return inner
}

func svCondArg(raw string) string {
	lex := newSVLexer(raw)
	tok := lex.nextToken()
	if tok.kind == svTokWord {
		return svPrintWord(tok.word, nil)
	}
	return raw
}

func (p *svPrinter) printSimple(cmd *svCommand, node string) []map[string]any {
	var out []map[string]any
	values := make([]string, len(cmd.words))
	for i, w := range cmd.words {
		if lit, ok := svWordLiteralOpt(w); ok {
			values[i] = lit
		} else {
			values[i] = svPrintWord(w, nil)
		}
	}
	if len(cmd.words) == 0 {
		for i, a := range cmd.assigns {
			if i > 0 {
				out = append(out, p.seg(node, cmd.lo, cmd.hi, ", ", "op"))
			}
			aNode := p.nextNode()
			out = append(out, p.seg(aNode, a.lo, a.hi, svPrintAssign(a, nil), "var def"))
		}
		out = append(out, p.printRedirs(cmd, node)...)
		return out
	}
	if len(cmd.assigns) > 0 {
		parts := make([]string, len(cmd.assigns))
		for i, a := range cmd.assigns {
			parts[i] = svPrintAssign(a, nil)
		}
		out = append(out, p.seg(node, cmd.lo, cmd.hi, "env("+strings.Join(parts, ", ")+") ", "chip env"))
	}
	name := ""
	if n, ok := svWordLiteralOpt(cmd.words[0]); ok {
		name = n
	}

	// cd <dir> -> cwd <dir>
	if name == "cd" && len(cmd.words) == 2 {
		seg := p.seg(node, cmd.lo, cmd.hi, "cwd "+values[1], "badge")
		seg["expanded"] = p.src[cmd.lo:cmd.hi]
		out = append(out, seg)
		return out
	}

	// set -e / -u / -euo / etc.
	if name == "set" && len(cmd.words) == 2 {
		meaning := ""
		switch values[1] {
		case "-e":
			meaning = "stop on first error"
		case "+e":
			meaning = "keep going on errors"
		case "-u":
			meaning = "error on unset vars"
		case "-a":
			meaning = "auto-export assignments"
		case "+a":
			meaning = "stop auto-export"
		case "-m":
			meaning = "job control on"
		case "+m":
			meaning = "job control off"
		case "-x":
			meaning = "trace commands"
		case "-euo":
			meaning = "strict mode"
		}
		if meaning != "" {
			seg := p.seg(node, cmd.lo, cmd.hi, "# "+meaning, "comment badge")
			seg["expanded"] = p.src[cmd.lo:cmd.hi]
			out = append(out, seg)
			return out
		}
	}

	// curl ... %{http_code} ... /dev/null ... url -> http_status(url)
	if name == "curl" {
		hasCode, hasNull := false, false
		for _, v := range values {
			if v == "%{http_code}" {
				hasCode = true
			}
			if v == "/dev/null" {
				hasNull = true
			}
		}
		if hasCode && hasNull {
			for _, w := range cmd.words {
				if v, ok := svWordLiteralOpt(w); ok && (strings.HasPrefix(v, "http:") || strings.HasPrefix(v, "https:") || strings.HasPrefix(v, "localhost")) {
					out = append(out, p.seg(node, cmd.lo, cmd.hi, "http_status(", "fn badge"))
					out = append(out, p.wordSegment(w, "str"))
					out = append(out, p.seg(node, cmd.lo, cmd.hi, ")", "op"))
					return out
				}
			}
		}
	}

	switch {
	case name == "echo" || name == "printf":
		out = append(out, p.seg(node, cmd.lo, cmd.hi, "print(", "fn"))
		first := true
		for i := 1; i < len(cmd.words); i++ {
			w := cmd.words[i]
			if i == 1 {
				if v, ok := svWordLiteralOpt(w); ok && isEchoFlag(v) {
					continue
				}
			}
			if !first {
				out = append(out, p.seg(node, cmd.lo, cmd.hi, ", ", "op"))
			}
			first = false
			out = append(out, p.wordSegment(w, "str"))
		}
		out = append(out, p.seg(node, cmd.lo, cmd.hi, ")", "op"))

	case name == "[" || name == "test":
		end := len(cmd.words)
		if name == "[" && end > 1 {
			if last, ok := svWordLiteralOpt(cmd.words[end-1]); ok && last == "]" {
				end--
			}
		}
		out = append(out, p.seg(node, cmd.lo, cmd.hi, svTestWords(cmd.words[1:end]), "var"))

	case p.funcs[name] || name == "source" || name == "." || name == "exit" || name == "return" || name == "break" || name == "continue":
		fnName := name
		if fnName == "." {
			fnName = "source"
		}
		out = append(out, p.seg(node, cmd.lo, cmd.hi, fnName+"(", "fn"))
		for i := 1; i < len(cmd.words); i++ {
			if i > 1 {
				out = append(out, p.seg(node, cmd.lo, cmd.hi, ", ", "op"))
			}
			out = append(out, p.wordSegment(cmd.words[i], "str"))
		}
		out = append(out, p.seg(node, cmd.lo, cmd.hi, ")", "op"))

	default:
		start := 0
		if name == "timeout" && len(values) > 2 && isTimeoutDuration(values[1]) {
			dur := values[1]
			if dur[len(dur)-1] >= '0' && dur[len(dur)-1] <= '9' {
				dur += "s"
			}
			out = append(out, p.seg(node, cmd.lo, cmd.hi, "⏱ "+dur+" ", "badge"))
			start = 2
		}
		argLangCtx := newSVArgLangCtx(values)
		for i := start; i < len(cmd.words); i++ {
			w := cmd.words[i]
			if i > start {
				out = append(out, p.seg(node, cmd.lo, cmd.hi, " ", ""))
			}
			lang := argLangCtx.languageAt(i)
			if lang != "" {
				if val, ok := svWordLiteralOpt(w); ok {
					wNode := p.nextNode()
					if strings.ContainsRune(val, '\n') || len(val) > 64 {
						seg := p.seg(wNode, w.lo, w.hi, fmt.Sprintf("⟦%s ↓⟧", lang), "chip emb")
						seg["expanded"] = p.src[w.lo:w.hi]
						out = append(out, seg)
					} else {
						out = append(out, p.seg(wNode, w.lo, w.hi, "‹", "emb"))
						out = append(out, svHighlightEmbedded(val, lang, wNode, utf16Range(p.u16, w.lo, w.hi))...)
						out = append(out, p.seg(wNode, w.lo, w.hi, "›", "emb"))
					}
					continue
				}
			}
			st := "arg"
			if i == start {
				st = "cmd"
			}
			out = append(out, p.wordSegment(w, st))
		}
	}

	out = append(out, p.printRedirs(cmd, node)...)
	return out
}

func isEchoFlag(v string) bool {
	if len(v) <= 1 || v[0] != '-' {
		return false
	}
	for i := 1; i < len(v); i++ {
		if v[i] != 'n' && v[i] != 'e' && v[i] != 'E' {
			return false
		}
	}
	return true
}

func isTimeoutDuration(v string) bool {
	if v == "" {
		return false
	}
	digits := 0
	for digits < len(v) && v[digits] >= '0' && v[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return false
	}
	if digits == len(v) {
		return true
	}
	return digits+1 == len(v) && (v[digits] == 's' || v[digits] == 'm' || v[digits] == 'h')
}

func svTestWords(words []svWord) string {
	vals := make([]string, len(words))
	lits := make([]string, len(words))
	for i, w := range words {
		vals[i] = svPrintWord(w, nil)
		lits[i] = svWordLiteral(w)
	}
	if len(words) == 2 {
		op := lits[0]
		switch op {
		case "-f":
			return "is_file(" + vals[1] + ")"
		case "-d":
			return "is_dir(" + vals[1] + ")"
		case "-e":
			return "exists(" + vals[1] + ")"
		case "-s":
			return "has_content(" + vals[1] + ")"
		case "-x":
			return "is_executable(" + vals[1] + ")"
		case "-L":
			return "is_symlink(" + vals[1] + ")"
		case "-n":
			return vals[1] + ` != ""`
		case "-z":
			return vals[1] + ` == ""`
		case "!":
			return "not " + vals[1]
		}
	}
	if len(words) == 3 {
		op := lits[1]
		switch op {
		case "=", "==", "-eq":
			op = "=="
		case "!=", "-ne":
			op = "!="
		case "-lt":
			op = "<"
		case "-le":
			op = "<="
		case "-gt":
			op = ">"
		case "-ge":
			op = ">="
		}
		return vals[0] + " " + op + " " + vals[2]
	}
	return strings.Join(vals, " ")
}

func (p *svPrinter) wordSegment(w svWord, style string) map[string]any {
	wNode := p.nextNode()
	var text string
	if style == "cmd" || style == "arg" {
		if lit, ok := svWordLiteralOpt(w); ok {
			text = lit
		} else {
			text = svPrintWord(w, nil)
		}
	} else {
		text = svPrintWord(w, nil)
	}
	return p.seg(wNode, w.lo, w.hi, text, style)
}

func (p *svPrinter) printRedirs(cmd *svCommand, node string) []map[string]any {
	var out []map[string]any
	for _, r := range cmd.redirs {
		style := "redir badge"
		var text string
		if r.heredoc != nil {
			lang := p.heredocLang(cmd, r.heredoc)
			text = fmt.Sprintf(" ⟵ %s ↓", lang)
		} else if r.target != nil {
			rawTarget, isLit := svWordLiteralOpt(*r.target)
			if isLit && rawTarget == "/dev/null" {
				label := "out"
				style = "redir badge badge-discard-out"
				if r.op == "&>" || r.op == "&>>" {
					label = "silent"
					style = "redir badge badge-discard-all"
				} else if r.fd == "2" {
					label = "err"
					style = "redir badge badge-discard-err"
				}
				text = " 🔇 " + label
			} else if r.fd == "2" && r.op == ">&" && isLit && rawTarget == "1" {
				text = " +err"
			} else {
				targetStr := rawTarget
				if !isLit {
					targetStr = svPrintWord(*r.target, nil)
				}
				text = " " + r.fd + r.op + targetStr
			}
		}
		out = append(out, p.seg(node, r.lo, r.hi, text, style))
	}
	return out
}

func (p *svPrinter) heredocLang(cmd *svCommand, doc *svHereDoc) string {
	var words []string
	for _, w := range cmd.words {
		words = append(words, svWordLiteral(w))
	}
	var targets []string
	for _, r := range cmd.redirs {
		if r.target != nil && (r.op == ">" || r.op == ">>" || r.op == ">|" || r.op == "&>" || r.op == "&>>") {
			targets = append(targets, svWordLiteral(*r.target))
		}
	}
	return svHeredocLanguage(words, targets, doc.delim, doc.body)
}

func (p *svPrinter) emitCommandDocuments(cmd *svCommand, depth int) {
	if cmd.kind == svCmdSimple {
		values := make([]string, len(cmd.words))
		for i, w := range cmd.words {
			values[i] = svWordLiteral(w)
		}
		argLangCtx := newSVArgLangCtx(values)
		for i, w := range cmd.words {
			lang := argLangCtx.languageAt(i)
			if lang != "" {
				if val, ok := svWordLiteralOpt(w); ok && (strings.ContainsRune(val, '\n') || len(val) > 64) {
					p.emitDocument(val, lang, w.lo, w.hi, depth+1, "inline argument")
				}
			}
		}
	}
	for _, r := range cmd.redirs {
		if r.heredoc != nil {
			doc := r.heredoc
			body := strings.TrimSuffix(doc.body, "\n")
			lang := p.heredocLang(cmd, doc)
			title := "heredoc «" + doc.delim + "»"
			if !doc.quoted && strings.ContainsRune(doc.body, '$') {
				title += " · $vars expand"
			}
			p.emitDocument(body, lang, doc.lo, doc.hi, depth+1, title)
		}
	}
}

func (p *svPrinter) emitDocument(text, language string, lo, hi, depth int, title string) {
	node := p.nextNode()
	rng := utf16Range(p.u16, lo, hi)
	if title != "" {
		p.serial++
		p.rows = append(p.rows, map[string]any{
			"id":    fmt.Sprintf("doc-head-%s-%d", node, p.serial),
			"depth": depth,
			"node":  node,
			"range": rng,
			"role":  "document",
			"segments": []any{
				map[string]any{
					"text":  language + " · " + title,
					"node":  node,
					"range": rng,
					"style": "emb",
				},
			},
			"effects": []any{},
		})
	}
	for _, line := range strings.Split(text, "\n") {
		p.serial++
		segs := svHighlightEmbedded(line, language, node, rng)
		segsAny := make([]any, len(segs))
		for i, s := range segs {
			segsAny[i] = s
		}
		p.rows = append(p.rows, map[string]any{
			"id":       fmt.Sprintf("doc-%s-%d", node, p.serial),
			"depth":    depth,
			"node":     node,
			"range":    rng,
			"role":     "document",
			"segments": segsAny,
			"effects":  []any{},
		})
	}
}

func (p *svPrinter) printCommand(cmd *svCommand, depth int, bg bool) {
	node := p.nextNode()
	if cmd.kind == svCmdSimple {
		segs := p.printSimple(cmd, node)
		if bg {
			segs = append([]map[string]any{p.seg(node, cmd.lo, cmd.hi, "spawn ", "kw")}, segs...)
		}
		p.emitRow(node, cmd.lo, cmd.hi, depth, "stmt", segs, svComputeEffects(cmd, bg))
		p.emitCommandDocuments(cmd, depth)
		return
	}

	rowStart := len(p.rows)
	switch cmd.kind {
	case svCmdIf:
		for i, arm := range cmd.ifArms {
			kw := "if "
			if i > 0 {
				kw = "elif "
			}
			segs := []map[string]any{p.seg(node, cmd.lo, cmd.hi, kw, "kw")}
			segs = append(segs, p.inlineList(arm.cond)...)
			segs = append(segs, p.seg(node, cmd.lo, cmd.hi, ":", "op"))
			p.emitRow(node, cmd.lo, cmd.hi, depth, "head", segs, nil)
			p.printList(arm.body, depth+1)
		}
		if len(cmd.elseBody) > 0 {
			p.emitRow(node, cmd.lo, cmd.hi, depth, "mid", []map[string]any{p.seg(node, cmd.lo, cmd.hi, "else:", "kw")}, nil)
			p.printList(cmd.elseBody, depth+1)
		}

	case svCmdWhile, svCmdUntil:
		kw := "while "
		if cmd.kind == svCmdUntil {
			kw = "while not "
		}
		// Special case: while read ...
		var segs []map[string]any
		if len(cmd.cond) == 1 {
			if rc := singleSimple(cmd.cond[0].andOr); rc != nil && len(rc.words) > 0 {
				if n, ok := svWordLiteralOpt(rc.words[0]); ok && n == "read" {
					var vars []string
					for _, w := range rc.words[1:] {
						if lit, ok := svWordLiteralOpt(w); ok && !strings.HasPrefix(lit, "-") {
							vars = append(vars, lit)
						}
					}
					segs = []map[string]any{p.seg(node, rc.lo, rc.hi, fmt.Sprintf("for %s in lines:", strings.Join(vars, ", ")), "kw")}
				}
			}
		}
		if len(segs) == 0 {
			segs = []map[string]any{p.seg(node, cmd.lo, cmd.hi, kw, "kw")}
			segs = append(segs, p.inlineList(cmd.cond)...)
			segs = append(segs, p.seg(node, cmd.lo, cmd.hi, ":", "op"))
		}
		p.emitRow(node, cmd.lo, cmd.hi, depth, "head", segs, nil)
		p.printList(cmd.body, depth+1)

	case svCmdFor:
		segs := []map[string]any{
			p.seg(node, cmd.lo, cmd.hi, "for ", "kw"),
			p.seg(node, cmd.lo, cmd.hi, cmd.forVar, "var def"),
			p.seg(node, cmd.lo, cmd.hi, " in ", "kw"),
		}
		segs = append(segs, p.loopItems(cmd, node)...)
		segs = append(segs, p.seg(node, cmd.lo, cmd.hi, ":", "op"))
		p.emitRow(node, cmd.lo, cmd.hi, depth, "head", segs, nil)
		p.printList(cmd.body, depth+1)

	case svCmdCase:
		segs := []map[string]any{p.seg(node, cmd.lo, cmd.hi, "match "+svPrintWord(cmd.caseWord, nil)+":", "kw")}
		p.emitRow(node, cmd.lo, cmd.hi, depth, "head", segs, nil)
		for _, arm := range cmd.caseArms {
			pats := make([]string, len(arm.patterns))
			for i, w := range arm.patterns {
				pats[i] = svPrintWord(w, nil)
			}
			p.emitRow(node, cmd.lo, cmd.hi, depth+1, "head", []map[string]any{p.seg(node, cmd.lo, cmd.hi, "case "+strings.Join(pats, " | ")+":", "kw")}, nil)
			p.printList(arm.body, depth+2)
			if arm.term == ";&" || arm.term == ";;&" {
				t := "fallthrough"
				if arm.term == ";;&" {
					t = "continue matching"
				}
				p.emitRow(node, cmd.lo, cmd.hi, depth+2, "tail", []map[string]any{p.seg(node, cmd.lo, cmd.hi, t, "kw")}, nil)
			}
		}

	case svCmdFuncDef:
		p.emitRow(node, cmd.lo, cmd.hi, depth, "head", []map[string]any{p.seg(node, cmd.lo, cmd.hi, "def "+cmd.funcName+"():", "fn")}, nil)
		if cmd.funcBody != nil {
			if cmd.funcBody.kind == svCmdBrace {
				p.printList(cmd.funcBody.body, depth+1)
			} else {
				p.printCommand(cmd.funcBody, depth+1, false)
			}
		}

	case svCmdBrace:
		if bg || len(cmd.redirs) > 0 {
			lbl := "redirected block:"
			if bg {
				lbl = "spawn:"
			}
			segs := []map[string]any{p.seg(node, cmd.lo, cmd.hi, lbl, "kw")}
			segs = append(segs, p.printRedirs(cmd, node)...)
			p.emitRow(node, cmd.lo, cmd.hi, depth, "head", segs, svComputeEffects(cmd, bg))
			p.printList(cmd.body, depth+1)
		} else {
			p.printList(cmd.body, depth)
		}

	case svCmdSubshell:
		p.emitRow(node, cmd.lo, cmd.hi, depth, "head", []map[string]any{p.seg(node, cmd.lo, cmd.hi, "subshell:", "kw")}, nil)
		p.printList(cmd.body, depth+1)

	case svCmdCond, svCmdArith:
		segs := p.inlineCommand(cmd)
		p.emitRow(node, cmd.lo, cmd.hi, depth, "stmt", segs, svComputeEffects(cmd, bg))
	}

	if bg && cmd.kind != svCmdBrace && rowStart < len(p.rows) {
		pref := p.seg(node, cmd.lo, cmd.hi, "spawn ", "kw")
		oldSegs := p.rows[rowStart]["segments"].([]any)
		p.rows[rowStart]["segments"] = append([]any{pref}, oldSegs...)
	}
	if len(cmd.redirs) > 0 && cmd.kind != svCmdBrace {
		redirs := p.printRedirs(cmd, node)
		if rowStart < len(p.rows) {
			oldSegs := p.rows[rowStart]["segments"].([]any)
			for _, rs := range redirs {
				oldSegs = append(oldSegs, rs)
			}
			p.rows[rowStart]["segments"] = oldSegs
		}
		p.emitCommandDocuments(cmd, depth)
	}
}

func (p *svPrinter) loopItems(cmd *svCommand, node string) []map[string]any {
	if !cmd.hasIn {
		return []map[string]any{p.seg(node, cmd.lo, cmd.hi, "args", "var")}
	}
	if len(cmd.forWords) == 1 {
		w := cmd.forWords[0]
		if len(w.parts) == 1 && w.parts[0].kind == svPartLit && strings.ContainsAny(w.parts[0].text, "*?[") {
			wNode := p.nextNode()
			return []map[string]any{p.seg(wNode, w.lo, w.hi, "glob("+svQuote(w.parts[0].text)+")", "fn")}
		}
	}
	count := min(len(cmd.forWords), 2)
	segs := []map[string]any{p.seg(node, cmd.lo, cmd.hi, "[", "op")}
	for i := range count {
		if i > 0 {
			segs = append(segs, p.seg(node, cmd.lo, cmd.hi, ", ", "op"))
		}
		segs = append(segs, p.wordSegment(cmd.forWords[i], "str"))
	}
	if count < len(cmd.forWords) {
		segs = append(segs, p.seg(node, cmd.lo, cmd.hi, ", ", "op"))
		more := p.seg(node, cmd.lo, cmd.hi, fmt.Sprintf("+%d more", len(cmd.forWords)-count), "chip")
		segs = append(segs, more)
	}
	segs = append(segs, p.seg(node, cmd.lo, cmd.hi, "]", "op"))
	return segs
}

func (p *svPrinter) inlineList(items []*svListItem) []map[string]any {
	var out []map[string]any
	for i, item := range items {
		if item.andOr == nil {
			continue
		}
		if i > 0 {
			n := p.nextNode()
			out = append(out, p.seg(n, item.lo, item.hi, "; ", "op"))
		}
		segs, _ := p.printPipeline(item.andOr.first, false)
		out = append(out, segs...)
		for j, pl := range item.andOr.rest {
			n := p.nextNode()
			conn := " and "
			if item.andOr.ops[j] == "||" {
				conn = " or "
			}
			out = append(out, p.seg(n, item.andOr.lo, item.andOr.hi, conn, "op"))
			pSegs, _ := p.printPipeline(pl, false)
			out = append(out, pSegs...)
		}
	}
	return out
}

// ── Plain-Terminal Renderer (packages/tui/src/tools/shell-viz-render.ts) ────

func shellVizText(text string) string {
	s := replaceTabs(sanitizeDisplayText(text))
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func shellVizColorToken(style string) string {
	for _, tok := range strings.Fields(style) {
		switch tok {
		case "comment":
			return "syntaxComment"
		case "kw":
			return "syntaxKeyword"
		case "var", "secret":
			return "syntaxVariable"
		case "str", "path":
			return "syntaxString"
		case "num":
			return "syntaxNumber"
		case "op":
			return "syntaxOperator"
		case "cmd", "fn":
			return "syntaxFunction"
		case "chip", "badge":
			return "accent"
		}
	}
	return "text"
}

func (p palette) faint(text string) string {
	if text == "" {
		return ""
	}
	return "\x1b[2m" + text + "\x1b[22m"
}

func (p palette) shellVizSegmentText(seg, row map[string]any, colorOn, running bool) string {
	style := strField(seg, "style")
	text := shellVizText(strField(seg, "text"))
	if text == "" {
		return ""
	}
	segActive, hasSegActive := boolField(seg, "active")
	rowActive, _ := boolField(row, "active")
	active := rowActive
	if hasSegActive {
		active = segActive
	}
	state := strField(seg, "state")
	if state == "" {
		state = strField(row, "state")
	}
	inactive := (running && !active) || state == "skipped"
	semantic := false
	for _, tok := range strings.Fields(style) {
		if tok == "chip" || tok == "badge" || tok == "secret" {
			semantic = true
			break
		}
	}
	styled := text
	if colorOn || semantic {
		styled = p.fg(shellVizColorToken(style), text)
	}
	if inactive {
		return p.faint(styled)
	}
	if segActive && running {
		return p.bold(styled)
	}
	return styled
}

// RenderShellVizTerminal renders a ShellVizPresentation map to plain-terminal
// ANSI lines matching packages/tui/src/tools/shell-viz-render.ts (with
// includeOutput = false, as bash.ts:581 passes for command panes).
func RenderShellVizTerminal(p palette, pres map[string]any, width int) []string {
	if pres == nil {
		return nil
	}
	if width < 1 {
		width = 1
	}
	prefs := asMap(pres["preferences"])
	layers := asMap(prefs["layers"])
	colorOn := true
	if v, ok := boolField(layers, "color"); ok {
		colorOn = v
	}
	effectsOn := true
	if v, ok := boolField(layers, "effects"); ok {
		effectsOn = v
	}
	running, _ := boolField(pres, "running")
	selectedNode := strField(prefs, "selectedNode")

	var lines []string
	if msg := strField(pres, "message"); msg != "" {
		lines = append(lines, p.fg("dim", shellVizText(msg)))
	}

	for _, rawRow := range asSlice(pres["rows"]) {
		row := asMap(rawRow)
		if row == nil {
			continue
		}
		segs := asSlice(row["segments"])
		rowNode := strField(row, "node")
		rowActive, _ := boolField(row, "active")
		rowState := strField(row, "state")
		rowRole := strField(row, "role")
		rowDepth, _ := intField(row, "depth")

		selected := selectedNode != "" && rowNode == selectedNode
		rowFailedInSegs := false
		anySegActive := false
		for _, rs := range segs {
			sm := asMap(rs)
			if sm == nil {
				continue
			}
			if selectedNode != "" && strField(sm, "node") == selectedNode {
				selected = true
			}
			if strField(sm, "state") == "fail" {
				rowFailedInSegs = true
			}
			if a, _ := boolField(sm, "active"); a {
				anySegActive = true
			}
		}

		muted := running && !rowActive
		failedStmt := rowState == "fail" && !rowFailedInSegs && rowRole != "head" && rowRole != "mid" && rowRole != "tail"
		gutter := " "
		switch {
		case failedStmt:
			gutter = p.fg("error", "×")
		case running && anySegActive:
			gutter = p.fg("accent", "▶")
		case selected:
			gutter = p.fg("accent", "›")
		}

		var effBuf strings.Builder
		if effectsOn {
			for _, re := range asSlice(row["effects"]) {
				if em := asMap(re); em != nil {
					effBuf.WriteString(shellVizText(strField(em, "glyph")))
				}
			}
		}
		effStr := effBuf.String()
		if muted && effStr != "" {
			effStr = p.faint(effStr)
		}

		maxIndent := max(0, width/4)
		indent := strings.Repeat("  ", min(max(0, rowDepth), maxIndent))
		lead := gutter + " " + indent
		if effStr != "" {
			lead += effStr + " "
		}

		failures := map[string]bool{}
		var bodyBuf strings.Builder
		for _, rs := range segs {
			sm := asMap(rs)
			if sm == nil {
				continue
			}
			execNode := strField(sm, "executionNode")
			if execNode == "" {
				execNode = strField(sm, "node")
			}
			if execNode == "" {
				execNode = rowNode
			}
			if strField(sm, "state") == "fail" && execNode != "" && !failures[execNode] {
				failures[execNode] = true
				mark := p.fg("error", "× ")
				if muted {
					mark = p.faint(mark)
				}
				bodyBuf.WriteString(mark)
			}
			bodyBuf.WriteString(p.shellVizSegmentText(sm, row, colorOn, running))
		}
		if meta := strField(row, "meta"); meta != "" {
			tok := "dim"
			if rowState == "fail" {
				tok = "error"
			}
			bodyBuf.WriteString("  ")
			bodyBuf.WriteString(p.fg(tok, shellVizText(meta)))
		}

		body := bodyBuf.String()
		leadW := visibleWidth(lead)
		if leadW >= width {
			lines = append(lines, wrapAnsi(lead+body, max(1, width))...)
		} else {
			hanging := strings.Repeat(" ", leadW)
			wrapped := wrapAnsi(body, width-leadW)
			for idx, wl := range wrapped {
				if idx == 0 {
					lines = append(lines, clampRow(lead+wl, width))
				} else {
					lines = append(lines, clampRow(hanging+wl, width))
				}
			}
		}
	}

	return clampRows(lines, width)
}
