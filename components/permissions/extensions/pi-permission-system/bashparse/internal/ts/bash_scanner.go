package ts

import "encoding/binary"

// Port of tree-sitter-bash 0.25.1 src/scanner.c (the external scanner), statement for statement.
// C `char` is a signed 8-bit type in the wasm build, so delimiter bytes are int8 and a lookahead
// pushed into one is truncated to its low byte, exactly as the C does.

const (
	tokHeredocStart = iota
	tokSimpleHeredocBody
	tokHeredocBodyBeginning
	tokHeredocContent
	tokHeredocEnd
	tokFileDescriptor
	tokEmptyValue
	tokConcat
	tokVariableName
	tokTestOperator
	tokRegex
	tokRegexNoSlash
	tokRegexNoSpace
	tokExpansionWord
	tokExtglobPattern
	tokBareDollar
	tokBraceStart
	tokImmediateDoubleHash
	tokExternalExpansionSymHash
	tokExternalExpansionSymBang
	tokExternalExpansionSymEqual
	tokClosingBrace
	tokClosingBracket
	tokHeredocArrow
	tokHeredocArrowDash
	tokNewline
	tokOpeningParen
	tokEsac
	tokErrorRecovery
)

type heredoc struct {
	isRaw, started, allowsIndent bool
	delimiter                    []int8
	currentLeadingWord           []int8
}

type bashScanner struct {
	lastGlobParenDepth  uint8
	extWasInDoubleQuote bool
	extSawOutsideQuote  bool
	heredocs            []*heredoc
}

const serializationBufferSize = 1024

func resetString(s *[]int8) {
	for i := range *s {
		(*s)[i] = 0
	}
	*s = (*s)[:0]
}

func resetHeredoc(h *heredoc) {
	h.isRaw = false
	h.started = false
	h.allowsIndent = false
	resetString(&h.delimiter)
}

// reset is scanner.c reset: it clears every heredoc in place and keeps the list's length.
func (s *bashScanner) reset() {
	for _, h := range s.heredocs {
		resetHeredoc(h)
	}
}

func (s *bashScanner) serialize(buffer []byte) uint32 {
	size := uint32(0)
	buffer[size] = s.lastGlobParenDepth
	size++
	buffer[size] = b2u(s.extWasInDoubleQuote)
	size++
	buffer[size] = b2u(s.extSawOutsideQuote)
	size++
	buffer[size] = byte(len(s.heredocs))
	size++
	for _, h := range s.heredocs {
		if size+3+4+uint32(len(h.delimiter)) >= serializationBufferSize {
			return 0
		}
		buffer[size] = b2u(h.isRaw)
		size++
		buffer[size] = b2u(h.started)
		size++
		buffer[size] = b2u(h.allowsIndent)
		size++
		binary.LittleEndian.PutUint32(buffer[size:], uint32(len(h.delimiter)))
		size += 4
		for _, c := range h.delimiter {
			buffer[size] = byte(c)
			size++
		}
	}
	return size
}

func b2u(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// deserialize is scanner.c deserialize. Entries past the serialized count are left as they are,
// as in the C (the list never shrinks here).
func (s *bashScanner) deserialize(buffer []byte) {
	if len(buffer) == 0 {
		s.reset()
		return
	}
	size := 0
	s.lastGlobParenDepth = buffer[size]
	size++
	s.extWasInDoubleQuote = buffer[size] != 0
	size++
	s.extSawOutsideQuote = buffer[size] != 0
	size++
	count := int(buffer[size])
	size++
	for i := 0; i < count; i++ {
		var h *heredoc
		if i < len(s.heredocs) {
			h = s.heredocs[i]
		} else {
			h = &heredoc{}
			s.heredocs = append(s.heredocs, h)
		}
		h.isRaw = buffer[size] != 0
		size++
		h.started = buffer[size] != 0
		size++
		h.allowsIndent = buffer[size] != 0
		size++
		n := int(binary.LittleEndian.Uint32(buffer[size:]))
		size += 4
		h.delimiter = h.delimiter[:0]
		for j := 0; j < n; j++ {
			h.delimiter = append(h.delimiter, int8(buffer[size]))
			size++
		}
	}
}

// ---- libc character classes as the language's wasm links them (wasi-libc, musl): wctype.go

func advance(l *lexer) { l.advance(false) }
func skip(l *lexer)    { l.advance(true) }

// advanceWord consumes a "word" in POSIX parlance and returns it unquoted (advance_word).
func advanceWord(l *lexer, unquoted *[]int8) bool {
	empty := true
	quote := int32(0)
	if l.data.lookahead == '\'' || l.data.lookahead == '"' {
		quote = l.data.lookahead
		advance(l)
	}
	for l.data.lookahead != 0 {
		var stop bool
		if quote != 0 {
			stop = l.data.lookahead == quote || l.data.lookahead == '\r' || l.data.lookahead == '\n'
		} else {
			stop = iswspace(l.data.lookahead)
		}
		if stop {
			break
		}
		if l.data.lookahead == '\\' {
			advance(l)
			if l.data.lookahead == 0 {
				return false
			}
		}
		empty = false
		*unquoted = append(*unquoted, int8(l.data.lookahead))
		advance(l)
	}
	*unquoted = append(*unquoted, 0)
	if quote != 0 && l.data.lookahead == quote {
		advance(l)
	}
	return !empty
}

func scanBareDollar(l *lexer) bool {
	for iswspace(l.data.lookahead) && l.data.lookahead != '\n' && !l.eof() {
		skip(l)
	}
	if l.data.lookahead == '$' {
		advance(l)
		l.data.resultSymbol = tokBareDollar
		l.markEnd()
		return iswspace(l.data.lookahead) || l.eof() || l.data.lookahead == '"'
	}
	return false
}

func scanHeredocStart(h *heredoc, l *lexer) bool {
	for iswspace(l.data.lookahead) {
		skip(l)
	}
	l.data.resultSymbol = tokHeredocStart
	h.isRaw = l.data.lookahead == '\'' || l.data.lookahead == '"' || l.data.lookahead == '\\'
	found := advanceWord(l, &h.delimiter)
	if !found {
		resetString(&h.delimiter)
		return false
	}
	return found
}

// cstrEqual is strcmp(a, b) == 0 over NUL-terminated int8 arrays.
func cstrEqual(a, b []int8) bool {
	for i := 0; ; i++ {
		var x, y int8
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return false
		}
		if x == 0 {
			return true
		}
	}
}

func scanHeredocEndIdentifier(h *heredoc, l *lexer) bool {
	resetString(&h.currentLeadingWord)
	size := 0
	if len(h.delimiter) > 0 {
		for {
			var d int32
			if size < len(h.delimiter) {
				d = int32(h.delimiter[size])
			}
			if !(l.data.lookahead != 0 && l.data.lookahead != '\n' && d == l.data.lookahead && len(h.currentLeadingWord) < len(h.delimiter)) {
				break
			}
			h.currentLeadingWord = append(h.currentLeadingWord, int8(l.data.lookahead))
			advance(l)
			size++
		}
	}
	h.currentLeadingWord = append(h.currentLeadingWord, 0)
	if len(h.delimiter) == 0 {
		return false
	}
	return cstrEqual(h.currentLeadingWord, h.delimiter)
}

func (s *bashScanner) back() *heredoc { return s.heredocs[len(s.heredocs)-1] }
func (s *bashScanner) pop()           { s.heredocs = s.heredocs[:len(s.heredocs)-1] }

func (s *bashScanner) scanHeredocContent(l *lexer, middleType, endType uint16) bool {
	didAdvance := false
	h := s.back()
	for {
		switch l.data.lookahead {
		case 0:
			if l.eof() && didAdvance {
				resetHeredoc(h)
				l.data.resultSymbol = endType
				return true
			}
			return false
		case '\\':
			didAdvance = true
			advance(l)
			advance(l)
		case '$':
			if h.isRaw {
				didAdvance = true
				advance(l)
				break
			}
			if didAdvance {
				l.markEnd()
				l.data.resultSymbol = middleType
				h.started = true
				advance(l)
				if iswalpha(l.data.lookahead) || l.data.lookahead == '{' || l.data.lookahead == '(' {
					return true
				}
				break
			}
			if middleType == tokHeredocBodyBeginning && l.getColumn() == 0 {
				l.data.resultSymbol = middleType
				h.started = true
				return true
			}
			return false
		case '\n':
			if !didAdvance {
				skip(l)
			} else {
				advance(l)
			}
			didAdvance = true
			if h.allowsIndent {
				for iswspace(l.data.lookahead) {
					advance(l)
				}
			}
			if h.started {
				l.data.resultSymbol = middleType
			} else {
				l.data.resultSymbol = endType
			}
			l.markEnd()
			if scanHeredocEndIdentifier(h, l) {
				if l.data.resultSymbol == tokHeredocEnd {
					s.pop()
				}
				return true
			}
		default:
			if l.getColumn() == 0 {
				// an alternative is to check the starting column of the heredoc body and track
				// that statefully
				for iswspace(l.data.lookahead) {
					if didAdvance {
						advance(l)
					} else {
						skip(l)
					}
				}
				if endType != tokSimpleHeredocBody {
					l.data.resultSymbol = middleType
					if scanHeredocEndIdentifier(h, l) {
						return true
					}
				}
				if endType == tokSimpleHeredocBody {
					l.data.resultSymbol = endType
					l.markEnd()
					if scanHeredocEndIdentifier(h, l) {
						return true
					}
				}
			}
			didAdvance = true
			advance(l)
		}
	}
}

func inErrorRecovery(valid []bool) bool { return valid[tokErrorRecovery] }

func (s *bashScanner) scan(l *lexer, valid []bool) bool {
	la := func() int32 { return l.data.lookahead }

	if valid[tokConcat] && !inErrorRecovery(valid) {
		if !(la() == 0 || iswspace(la()) || la() == '>' || la() == '<' || la() == ')' || la() == '(' ||
			la() == ';' || la() == '&' || la() == '|' ||
			(la() == '}' && valid[tokClosingBrace]) ||
			(la() == ']' && valid[tokClosingBracket])) {
			l.data.resultSymbol = tokConcat
			// So for a`b`, we want to return a concat. We check if the 2nd backtick has whitespace
			// after it, and if it does we return concat.
			if la() == '`' {
				l.markEnd()
				advance(l)
				for la() != '`' && !l.eof() {
					advance(l)
				}
				if l.eof() {
					return false
				}
				if la() == '`' {
					advance(l)
				}
				return iswspace(la()) || l.eof()
			}
			// strings w/ expansions that contains escaped quotes or backslashes need this to
			// return a concat
			if la() == '\\' {
				l.markEnd()
				advance(l)
				if la() == '"' || la() == '\'' || la() == '\\' {
					return true
				}
				if l.eof() {
					return false
				}
			} else {
				return true
			}
		}
		if iswspace(la()) && valid[tokClosingBrace] && !valid[tokExpansionWord] {
			l.data.resultSymbol = tokConcat
			return true
		}
	}

	if valid[tokImmediateDoubleHash] && !inErrorRecovery(valid) {
		// advance two # and ensure not } after
		if la() == '#' {
			l.markEnd()
			advance(l)
			if la() == '#' {
				advance(l)
				if la() != '}' {
					l.data.resultSymbol = tokImmediateDoubleHash
					l.markEnd()
					return true
				}
			}
		}
	}

	if valid[tokExternalExpansionSymHash] && !inErrorRecovery(valid) {
		if la() == '#' || la() == '=' || la() == '!' {
			switch la() {
			case '#':
				l.data.resultSymbol = tokExternalExpansionSymHash
			case '!':
				l.data.resultSymbol = tokExternalExpansionSymBang
			default:
				l.data.resultSymbol = tokExternalExpansionSymEqual
			}
			advance(l)
			l.markEnd()
			for la() == '#' || la() == '=' || la() == '!' {
				advance(l)
			}
			for iswspace(la()) {
				skip(l)
			}
			if la() == '}' {
				return true
			}
			return false
		}
	}

	if valid[tokEmptyValue] {
		if iswspace(la()) || l.eof() || la() == ';' || la() == '&' {
			l.data.resultSymbol = tokEmptyValue
			return true
		}
	}

	if (valid[tokHeredocBodyBeginning] || valid[tokSimpleHeredocBody]) && len(s.heredocs) > 0 &&
		!s.back().started && !inErrorRecovery(valid) {
		return s.scanHeredocContent(l, tokHeredocBodyBeginning, tokSimpleHeredocBody)
	}

	if valid[tokHeredocEnd] && len(s.heredocs) > 0 {
		h := s.back()
		if scanHeredocEndIdentifier(h, l) {
			h.currentLeadingWord = nil
			h.delimiter = nil
			s.pop()
			l.data.resultSymbol = tokHeredocEnd
			return true
		}
	}

	if valid[tokHeredocContent] && len(s.heredocs) > 0 && s.back().started && !inErrorRecovery(valid) {
		return s.scanHeredocContent(l, tokHeredocContent, tokHeredocEnd)
	}

	if valid[tokHeredocStart] && !inErrorRecovery(valid) && len(s.heredocs) > 0 {
		return scanHeredocStart(s.back(), l)
	}

	if valid[tokTestOperator] && !valid[tokExpansionWord] {
		for iswspace(la()) && la() != '\n' {
			skip(l)
		}

		if la() == '\\' {
			if valid[tokExtglobPattern] {
				goto extglobPattern
			}
			if valid[tokRegexNoSpace] {
				goto regex
			}
			skip(l)

			if l.eof() {
				return false
			}

			if la() == '\r' {
				skip(l)
				if la() == '\n' {
					skip(l)
				}
			} else if la() == '\n' {
				skip(l)
			} else {
				return false
			}

			for iswspace(la()) {
				skip(l)
			}
		}

		if la() == '\n' && !valid[tokNewline] {
			skip(l)
			for iswspace(la()) {
				skip(l)
			}
		}

		if la() == '-' {
			advance(l)

			advancedOnce := false
			for iswalpha(la()) {
				advancedOnce = true
				advance(l)
			}

			if iswspace(la()) && advancedOnce {
				l.markEnd()
				advance(l)
				if la() == '}' && valid[tokClosingBrace] {
					if valid[tokExpansionWord] {
						l.markEnd()
						l.data.resultSymbol = tokExpansionWord
						return true
					}
					return false
				}
				l.data.resultSymbol = tokTestOperator
				return true
			}
			if iswspace(la()) && valid[tokExtglobPattern] {
				l.data.resultSymbol = tokExtglobPattern
				return true
			}
		}

		if valid[tokBareDollar] && !inErrorRecovery(valid) && scanBareDollar(l) {
			return true
		}
	}

	if (valid[tokVariableName] || valid[tokFileDescriptor] || valid[tokHeredocArrow]) &&
		!valid[tokRegexNoSlash] && !inErrorRecovery(valid) {
		for {
			if (la() == ' ' || la() == '\t' || la() == '\r' || (la() == '\n' && !valid[tokNewline])) &&
				!valid[tokExpansionWord] {
				skip(l)
			} else if la() == '\\' {
				skip(l)

				if l.eof() {
					l.markEnd()
					l.data.resultSymbol = tokVariableName
					return true
				}

				if la() == '\r' {
					skip(l)
				}
				if la() == '\n' {
					skip(l)
				} else {
					if la() == '\\' && valid[tokExpansionWord] {
						goto expansionWord
					}
					return false
				}
			} else {
				break
			}
		}

		// no '*', '@', '?', '-', '$', '0', '_'
		if !valid[tokExpansionWord] &&
			(la() == '*' || la() == '@' || la() == '?' || la() == '-' || la() == '0' || la() == '_') {
			l.markEnd()
			advance(l)
			if la() == '=' || la() == '[' || la() == ':' || la() == '-' || la() == '%' || la() == '#' || la() == '/' {
				return false
			}
			if valid[tokExtglobPattern] && iswspace(la()) {
				l.markEnd()
				l.data.resultSymbol = tokExtglobPattern
				return true
			}
		}

		if valid[tokHeredocArrow] && la() == '<' {
			advance(l)
			if la() == '<' {
				advance(l)
				if la() == '-' {
					advance(l)
					s.heredocs = append(s.heredocs, &heredoc{allowsIndent: true})
					l.data.resultSymbol = tokHeredocArrowDash
				} else if la() == '<' || la() == '=' {
					return false
				} else {
					s.heredocs = append(s.heredocs, &heredoc{})
					l.data.resultSymbol = tokHeredocArrow
				}
				return true
			}
			return false
		}

		isNumber := true
		if iswdigit(la()) {
			advance(l)
		} else if iswalpha(la()) || la() == '_' {
			isNumber = false
			advance(l)
		} else {
			if la() == '{' {
				goto braceStart
			}
			if valid[tokExpansionWord] {
				goto expansionWord
			}
			if valid[tokExtglobPattern] {
				goto extglobPattern
			}
			return false
		}

		for {
			if iswdigit(la()) {
				advance(l)
			} else if iswalpha(la()) || la() == '_' {
				isNumber = false
				advance(l)
			} else {
				break
			}
		}

		if isNumber && valid[tokFileDescriptor] && (la() == '>' || la() == '<') {
			l.data.resultSymbol = tokFileDescriptor
			return true
		}

		if valid[tokVariableName] {
			if la() == '+' {
				l.markEnd()
				advance(l)
				if la() == '=' || la() == ':' || valid[tokClosingBrace] {
					l.data.resultSymbol = tokVariableName
					return true
				}
				return false
			}
			if la() == '/' {
				return false
			}
			if la() == '=' || la() == '[' ||
				(la() == ':' && !valid[tokClosingBrace] && !valid[tokOpeningParen]) ||
				la() == '%' ||
				(la() == '#' && !isNumber) || la() == '@' ||
				(la() == '-' && valid[tokClosingBrace]) {
				l.markEnd()
				l.data.resultSymbol = tokVariableName
				return true
			}

			if la() == '?' {
				l.markEnd()
				advance(l)
				l.data.resultSymbol = tokVariableName
				return iswalpha(la())
			}
		}

		return false
	}

	if valid[tokBareDollar] && !inErrorRecovery(valid) && scanBareDollar(l) {
		return true
	}

regex:
	if (valid[tokRegex] || valid[tokRegexNoSlash] || valid[tokRegexNoSpace]) && !inErrorRecovery(valid) {
		if valid[tokRegex] || valid[tokRegexNoSpace] {
			for iswspace(la()) {
				skip(l)
			}
		}

		if (la() != '"' && la() != '\'') ||
			((la() == '$' || la() == '\'') && valid[tokRegexNoSlash]) ||
			(la() == '\'' && valid[tokRegexNoSpace]) {
			type regexState struct {
				done, advancedOnce, foundNonAlnumDollarUnderDash, lastWasEscape, inSingleQuote bool
				parenDepth, bracketDepth, braceDepth                                           uint32
			}

			if la() == '$' && valid[tokRegexNoSlash] {
				l.markEnd()
				advance(l)
				if la() == '(' {
					return false
				}
			}

			l.markEnd()

			var state regexState
			for !state.done {
				if state.inSingleQuote {
					if la() == '\'' {
						state.inSingleQuote = false
						advance(l)
						l.markEnd()
					}
				}
				switch la() {
				case '\\':
					state.lastWasEscape = true
				case 0:
					return false
				case '(':
					state.parenDepth++
					state.lastWasEscape = false
				case '[':
					state.bracketDepth++
					state.lastWasEscape = false
				case '{':
					if !state.lastWasEscape {
						state.braceDepth++
					}
					state.lastWasEscape = false
				case ')':
					if state.parenDepth == 0 {
						state.done = true
					}
					state.parenDepth--
					state.lastWasEscape = false
				case ']':
					if state.bracketDepth == 0 {
						state.done = true
					}
					state.bracketDepth--
					state.lastWasEscape = false
				case '}':
					if state.braceDepth == 0 {
						state.done = true
					}
					state.braceDepth--
					state.lastWasEscape = false
				case '\'':
					// Enter or exit a single-quoted string.
					state.inSingleQuote = !state.inSingleQuote
					advance(l)
					state.advancedOnce = true
					state.lastWasEscape = false
					continue
				default:
					state.lastWasEscape = false
				}

				if !state.done {
					if valid[tokRegex] {
						wasSpace := !state.inSingleQuote && iswspace(la())
						advance(l)
						state.advancedOnce = true
						if !wasSpace || state.parenDepth > 0 {
							l.markEnd()
						}
					} else if valid[tokRegexNoSlash] {
						if la() == '/' {
							l.markEnd()
							l.data.resultSymbol = tokRegexNoSlash
							return state.advancedOnce
						}
						if la() == '\\' {
							advance(l)
							state.advancedOnce = true
							if !l.eof() && la() != '[' && la() != '/' {
								advance(l)
								l.markEnd()
							}
						} else {
							wasSpace := !state.inSingleQuote && iswspace(la())
							advance(l)
							state.advancedOnce = true
							if !wasSpace {
								l.markEnd()
							}
						}
					} else if valid[tokRegexNoSpace] {
						if la() == '\\' {
							state.foundNonAlnumDollarUnderDash = true
							advance(l)
							if !l.eof() {
								advance(l)
							}
						} else if la() == '$' {
							l.markEnd()
							advance(l)
							// do not parse a command substitution
							if la() == '(' {
								return false
							}
							// end $ always means regex, e.g. 99999999$
							if iswspace(la()) {
								l.data.resultSymbol = tokRegexNoSpace
								l.markEnd()
								return true
							}
						} else {
							wasSpace := !state.inSingleQuote && iswspace(la())
							if wasSpace && state.parenDepth == 0 {
								l.markEnd()
								l.data.resultSymbol = tokRegexNoSpace
								return state.foundNonAlnumDollarUnderDash
							}
							if !iswalnum(la()) && la() != '$' && la() != '-' && la() != '_' {
								state.foundNonAlnumDollarUnderDash = true
							}
							advance(l)
						}
					}
				}
			}

			if valid[tokRegexNoSlash] {
				l.data.resultSymbol = tokRegexNoSlash
			} else if valid[tokRegexNoSpace] {
				l.data.resultSymbol = tokRegexNoSpace
			} else {
				l.data.resultSymbol = tokRegex
			}
			if valid[tokRegex] && !state.advancedOnce {
				return false
			}
			return true
		}
	}

extglobPattern:
	if valid[tokExtglobPattern] && !inErrorRecovery(valid) {
		// first skip ws, then check for ? * + @ !
		for iswspace(la()) {
			skip(l)
		}

		if la() == '?' || la() == '*' || la() == '+' || la() == '@' || la() == '!' || la() == '-' || la() == ')' ||
			la() == '\\' || la() == '.' || la() == '[' || iswalpha(la()) {
			if la() == '\\' {
				advance(l)
				if (iswspace(la()) || la() == '"') && la() != '\r' && la() != '\n' {
					advance(l)
				} else {
					return false
				}
			}

			if la() == ')' && s.lastGlobParenDepth == 0 {
				l.markEnd()
				advance(l)

				if iswspace(la()) {
					return false
				}
			}

			l.markEnd()
			wasNonAlpha := !iswalpha(la())
			if la() != '[' {
				// no esac
				if la() == 'e' {
					l.markEnd()
					advance(l)
					if la() == 's' {
						advance(l)
						if la() == 'a' {
							advance(l)
							if la() == 'c' {
								advance(l)
								if iswspace(la()) {
									return false
								}
							}
						}
					}
				} else {
					advance(l)
				}
			}

			// -\w is just a word, find something else special
			if la() == '-' {
				l.markEnd()
				advance(l)
				for iswalnum(la()) {
					advance(l)
				}

				if la() == ')' || la() == '\\' || la() == '.' {
					return false
				}
				l.markEnd()
			}

			// case item -) or *)
			if la() == ')' && s.lastGlobParenDepth == 0 {
				l.markEnd()
				advance(l)
				if iswspace(la()) {
					l.data.resultSymbol = tokExtglobPattern
					return wasNonAlpha
				}
			}

			if iswspace(la()) {
				l.markEnd()
				l.data.resultSymbol = tokExtglobPattern
				s.lastGlobParenDepth = 0
				return true
			}

			if la() == '$' {
				l.markEnd()
				advance(l)
				if la() == '{' || la() == '(' {
					l.data.resultSymbol = tokExtglobPattern
					return true
				}
			}

			if la() == '|' {
				l.markEnd()
				advance(l)
				l.data.resultSymbol = tokExtglobPattern
				return true
			}

			if !iswalnum(la()) && la() != '(' && la() != '"' && la() != '[' && la() != '?' && la() != '/' &&
				la() != '\\' && la() != '_' && la() != '*' {
				return false
			}

			type globState struct {
				done, sawNonAlphaDot                 bool
				parenDepth, bracketDepth, braceDepth uint32
			}

			state := globState{false, wasNonAlpha, uint32(s.lastGlobParenDepth), 0, 0}
			for !state.done {
				switch la() {
				case 0:
					return false
				case '(':
					state.parenDepth++
				case '[':
					state.bracketDepth++
				case '{':
					state.braceDepth++
				case ')':
					if state.parenDepth == 0 {
						state.done = true
					}
					state.parenDepth--
				case ']':
					if state.bracketDepth == 0 {
						state.done = true
					}
					state.bracketDepth--
				case '}':
					if state.braceDepth == 0 {
						state.done = true
					}
					state.braceDepth--
				}

				if la() == '|' {
					l.markEnd()
					advance(l)
					if state.parenDepth == 0 && state.bracketDepth == 0 && state.braceDepth == 0 {
						l.data.resultSymbol = tokExtglobPattern
						return true
					}
				}

				if !state.done {
					wasSpace := iswspace(la())
					if la() == '$' {
						l.markEnd()
						if !iswalpha(la()) && la() != '.' && la() != '\\' {
							state.sawNonAlphaDot = true
						}
						advance(l)
						if la() == '(' || la() == '{' {
							l.data.resultSymbol = tokExtglobPattern
							s.lastGlobParenDepth = uint8(state.parenDepth)
							return state.sawNonAlphaDot
						}
					}
					if wasSpace {
						l.markEnd()
						l.data.resultSymbol = tokExtglobPattern
						s.lastGlobParenDepth = 0
						return state.sawNonAlphaDot
					}
					if la() == '"' {
						l.markEnd()
						l.data.resultSymbol = tokExtglobPattern
						s.lastGlobParenDepth = 0
						return state.sawNonAlphaDot
					}
					if la() == '\\' {
						if !iswalpha(la()) && la() != '.' && la() != '\\' {
							state.sawNonAlphaDot = true
						}
						advance(l)
						if iswspace(la()) || la() == '"' {
							advance(l)
						}
					} else {
						if !iswalpha(la()) && la() != '.' && la() != '\\' {
							state.sawNonAlphaDot = true
						}
						advance(l)
					}
					if !wasSpace {
						l.markEnd()
					}
				}
			}

			l.data.resultSymbol = tokExtglobPattern
			s.lastGlobParenDepth = 0
			return state.sawNonAlphaDot
		}
		s.lastGlobParenDepth = 0

		return false
	}

expansionWord:
	if valid[tokExpansionWord] {
		advancedOnce := false
		advanceOnceSpace := false
		for {
			if la() == '"' {
				return false
			}
			if la() == '$' {
				l.markEnd()
				advance(l)
				if la() == '{' || la() == '(' || la() == '\'' || iswalnum(la()) {
					l.data.resultSymbol = tokExpansionWord
					return advancedOnce
				}
				advancedOnce = true
			}

			if la() == '}' {
				l.markEnd()
				l.data.resultSymbol = tokExpansionWord
				return advancedOnce || advanceOnceSpace
			}

			if la() == '(' && !(advancedOnce || advanceOnceSpace) {
				l.markEnd()
				advance(l)
				for la() != ')' && !l.eof() {
					// if we find a $( or ${ assume this is valid and is a garbage concatenation of
					// some weird word + an expansion
					if la() == '$' {
						l.markEnd()
						advance(l)
						if la() == '{' || la() == '(' || la() == '\'' || iswalnum(la()) {
							l.data.resultSymbol = tokExpansionWord
							return advancedOnce
						}
						advancedOnce = true
					} else {
						advancedOnce = advancedOnce || !iswspace(la())
						advanceOnceSpace = advanceOnceSpace || iswspace(la())
						advance(l)
					}
				}
				l.markEnd()
				if la() == ')' {
					advancedOnce = true
					advance(l)
					l.markEnd()
					if la() == '}' {
						return false
					}
				} else {
					return false
				}
			}

			if la() == '\'' {
				return false
			}

			if l.eof() {
				return false
			}
			advancedOnce = advancedOnce || !iswspace(la())
			advanceOnceSpace = advanceOnceSpace || iswspace(la())
			advance(l)
		}
	}

braceStart:
	if valid[tokBraceStart] && !inErrorRecovery(valid) {
		for iswspace(la()) {
			skip(l)
		}

		if la() != '{' {
			return false
		}

		advance(l)
		l.markEnd()

		for isdigit(la()) {
			advance(l)
		}

		if la() != '.' {
			return false
		}
		advance(l)

		if la() != '.' {
			return false
		}
		advance(l)

		for isdigit(la()) {
			advance(l)
		}

		if la() != '}' {
			return false
		}

		l.data.resultSymbol = tokBraceStart
		return true
	}

	return false
}
