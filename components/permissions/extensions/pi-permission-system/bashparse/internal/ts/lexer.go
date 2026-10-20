package ts

// Port of lib/src/lexer.c for the one configuration web-tree-sitter uses on a string: a single
// included range covering the whole text, input read in chunks, UTF-16LE decoding.

// chunkUnits is how much of the string the web binding hands the lexer per read: its input buffer
// holds 10*1024 bytes, less a terminator, so 5119 UTF-16 code units
// (lib/binding_web/lib/tree-sitter.c INPUT_BUFFER_SIZE, call_parse_callback). A surrogate pair split
// by a chunk boundary decodes as two lone surrogates, so the boundary is reproduced.
const chunkUnits = 5119

const byteOrderMark = 0xFEFF

type charRange struct{ start, end int32 }

// setContains is parser.h set_contains: binary search over sorted ranges.
func setContains(ranges []charRange, lookahead int32) bool {
	index := 0
	size := len(ranges) - index
	for size > 1 {
		half := size / 2
		mid := index + half
		r := ranges[mid]
		if lookahead >= r.start && lookahead <= r.end {
			return true
		} else if lookahead > r.end {
			index = mid
		}
		size -= half
	}
	r := ranges[index]
	return lookahead >= r.start && lookahead <= r.end
}

// lexerData is the part of TSLexer the generated lexer and the scanner read.
type lexerData struct {
	lookahead    int32
	resultSymbol uint16
}

type lexer struct {
	data               lexerData
	currentPosition    Length
	tokenStartPosition Length
	tokenEndPosition   Length
	input              []uint16
	chunkStart         uint32 // bytes
	chunkSize          uint32 // bytes
	hasChunk           bool
	atEOF              bool // current_included_range_index == included_range_count
	lookaheadSize      uint32
	didGetColumn       bool
	columnValid        bool
	columnValue        uint32
}

func newLexer(input []uint16) *lexer {
	l := &lexer{input: input}
	l.gotoPosition(Length{})
	return l
}

func (l *lexer) eof() bool { return l.atEOF }

// advance is ts_lexer__advance.
func (l *lexer) advance(skip bool) {
	if !l.hasChunk {
		return
	}
	l.doAdvance(skip)
}

func (l *lexer) setColumn(v uint32) { l.columnValid = true; l.columnValue = v }

func (l *lexer) clearChunk() {
	l.hasChunk = false
	l.chunkSize = 0
	l.chunkStart = 0
}

// getChunk is ts_lexer__get_chunk with the web binding's reader.
func (l *lexer) getChunk() {
	l.chunkStart = l.currentPosition.bytes
	unit := int(l.currentPosition.bytes / 2)
	remaining := len(l.input) - unit
	if remaining < 0 {
		remaining = 0
	}
	n := remaining
	if n > chunkUnits {
		n = chunkUnits
	}
	l.chunkSize = uint32(n) * 2
	l.hasChunk = n > 0
	if l.chunkSize == 0 {
		l.atEOF = true
		l.hasChunk = false
	}
}

// getLookahead is ts_lexer__get_lookahead with ts_decode_utf16_le.
func (l *lexer) getLookahead() {
	positionInChunk := l.currentPosition.bytes - l.chunkStart
	size := l.chunkSize - positionInChunk // unsigned in C
	if l.chunkSize < positionInChunk {
		size = 0
	}
	if size == 0 {
		l.lookaheadSize = 1
		l.data.lookahead = 0
		return
	}
	if size < 2 {
		l.data.lookahead = -1 // TS_DECODE_ERROR
		l.lookaheadSize = 1
		return
	}
	u := int(l.chunkStart/2) + int(positionInChunk/2)
	end := int(l.chunkStart/2) + int(l.chunkSize/2)
	c := int32(l.input[u])
	i := u + 1
	if c&^0x3ff == 0xd800 && i != end {
		if c2 := int32(l.input[i]); c2&^0x3ff == 0xdc00 {
			i++
			c = (c << 10) + c2 - ((0xd800 << 10) + 0xdc00 - 0x10000)
		}
	}
	l.data.lookahead = c
	l.lookaheadSize = uint32(i-u) * 2
}

// gotoPosition is ts_lexer_goto for a single included range [0, UINT32_MAX).
func (l *lexer) gotoPosition(position Length) {
	if position.bytes != l.currentPosition.bytes {
		l.columnValid = false
		l.columnValue = 0
	}
	l.currentPosition = position
	l.atEOF = false
	if l.hasChunk && (l.currentPosition.bytes < l.chunkStart || l.currentPosition.bytes >= l.chunkStart+l.chunkSize) {
		l.clearChunk()
	}
	l.lookaheadSize = 0
	l.data.lookahead = 0
}

// doAdvance is ts_lexer__do_advance.
func (l *lexer) doAdvance(skip bool) {
	if l.lookaheadSize != 0 {
		if l.data.lookahead == '\n' {
			l.currentPosition.row++
			l.currentPosition.col = 0
			l.setColumn(0)
		} else {
			isBOM := l.currentPosition.bytes == 0 && l.data.lookahead == byteOrderMark
			if !isBOM && l.columnValid {
				l.columnValue++
			}
			l.currentPosition.col += l.lookaheadSize
		}
		l.currentPosition.bytes += l.lookaheadSize
	}
	if skip {
		l.tokenStartPosition = l.currentPosition
	}
	if l.currentPosition.bytes < l.chunkStart || l.currentPosition.bytes >= l.chunkStart+l.chunkSize {
		l.getChunk()
	}
	l.getLookahead()
}

// markEnd is ts_lexer__mark_end.
func (l *lexer) markEnd() { l.tokenEndPosition = l.currentPosition }

// getColumn is ts_lexer__get_column.
func (l *lexer) getColumn() uint32 {
	l.didGetColumn = true
	if !l.columnValid {
		goal := l.currentPosition.bytes
		startOfCol := Length{l.currentPosition.bytes - l.currentPosition.col, l.currentPosition.row, 0}
		l.gotoPosition(startOfCol)
		l.setColumn(0)
		l.getChunk()
		if !l.eof() {
			l.getLookahead()
			for l.currentPosition.bytes < goal && !l.eof() && l.hasChunk {
				l.doAdvance(false)
				if l.eof() {
					break
				}
			}
		}
	}
	return l.columnValue
}

// reset is ts_lexer_reset.
func (l *lexer) reset(position Length) {
	if position.bytes != l.currentPosition.bytes {
		l.gotoPosition(position)
	}
}

// start is ts_lexer_start.
func (l *lexer) start() {
	l.tokenStartPosition = l.currentPosition
	l.tokenEndPosition = lengthUndefined
	l.data.resultSymbol = 0
	l.didGetColumn = false
	if !l.eof() {
		if l.chunkSize == 0 {
			l.getChunk()
		}
		if l.lookaheadSize == 0 {
			l.getLookahead()
		}
		if l.currentPosition.bytes == 0 {
			if l.data.lookahead == byteOrderMark {
				l.advance(true)
			}
			l.setColumn(0)
		}
	}
}

// finish is ts_lexer_finish without the lookahead-byte bookkeeping.
func (l *lexer) finish() {
	if l.tokenEndPosition.isUndefined() {
		l.markEnd()
	}
	if l.tokenEndPosition.bytes < l.tokenStartPosition.bytes {
		l.tokenStartPosition = l.tokenEndPosition
	}
}

func (l *lexer) setResult(symbol uint16) { l.data.resultSymbol = symbol }
