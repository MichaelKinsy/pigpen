package jsdate

import (
	"math"
	"unicode/utf16"
)

// Parse is Date.parse(s) / new Date(s): V8's DateParser, port of src/date/dateparser.cc and dateparser-inl.h.

const kNone = math.MaxInt32

const maxSignificantDigits = 9

type keywordType int

const (
	kwInvalid keywordType = iota
	kwMonthName
	kwTimeZoneName
	kwTimeSeparator
	kwAmPm
)

type tokenTag int

const (
	tagInvalid    tokenTag = -6
	tagUnknown    tokenTag = -5
	tagWhiteSpace tokenTag = -4
	tagNumber     tokenTag = -3
	tagSymbol     tokenTag = -2
	tagEnd        tokenTag = -1
	// keyword tags are the keyword types (>= 0)
)

type token struct {
	tag    tokenTag
	length int
	value  int
}

func (t token) isInvalid() bool    { return t.tag == tagInvalid }
func (t token) isNumber() bool     { return t.tag == tagNumber }
func (t token) isSymbolTok() bool  { return t.tag == tagSymbol }
func (t token) isWhiteSpace() bool { return t.tag == tagWhiteSpace }
func (t token) isEnd() bool        { return t.tag == tagEnd }
func (t token) isKeyword() bool    { return t.tag >= 0 }
func (t token) isSymbol(c byte) bool {
	return t.tag == tagSymbol && t.value == int(c)
}
func (t token) isKeywordType(k keywordType) bool { return t.tag == tokenTag(k) }
func (t token) isFixedLengthNumber(n int) bool   { return t.isNumber() && t.length == n }
func (t token) isAsciiSign() bool {
	return t.tag == tagSymbol && (t.value == '-' || t.value == '+')
}
func (t token) asciiSign() int { return 44 - t.value }
func (t token) isKeywordZ() bool {
	return t.isKeywordType(kwTimeZoneName) && t.length == 1 && t.value == 0
}

// inputReader reads UTF-16 code units; the end (and an embedded NUL) is 0.
type inputReader struct {
	buf   []uint16
	index int
	ch    uint32
}

func newInputReader(s []uint16) *inputReader {
	r := &inputReader{buf: s}
	r.next()
	return r
}

func (r *inputReader) position() int { return r.index }

func (r *inputReader) next() {
	if r.index < len(r.buf) {
		r.ch = uint32(r.buf[r.index])
	} else {
		r.ch = 0
	}
	r.index++
}

func (r *inputReader) isDigit() bool { return r.ch >= '0' && r.ch <= '9' }
func (r *inputReader) isEnd() bool   { return r.ch == 0 }

// isWhiteSpace is V8's IsWhiteSpace (WhiteSpace without line terminators).
func isWhiteSpace(c uint32) bool {
	switch c {
	case 0x09, 0x0B, 0x0C, 0x20, 0xA0, 0xFEFF, 0x1680, 0x202F, 0x205F, 0x3000:
		return true
	}
	return c >= 0x2000 && c <= 0x200A
}

func isWhiteSpaceOrLineTerminator(c uint32) bool {
	return isWhiteSpace(c) || c == 0x0A || c == 0x0D || c == 0x2028 || c == 0x2029
}

func (r *inputReader) isAlphaOrAbove() bool   { return r.ch >= 'A' }
func (r *inputReader) isWhiteSpaceChar() bool { return isWhiteSpace(r.ch) }

func (r *inputReader) readUnsignedNumeral() int {
	n, i := 0, 0
	for r.ch == '0' {
		r.next()
	}
	for r.isDigit() {
		if i < maxSignificantDigits {
			n = n*10 + int(r.ch) - '0'
		}
		i++
		r.next()
	}
	return n
}

func (r *inputReader) readWord(prefix *[3]uint32) int {
	length := 0
	for r.isAlphaOrAbove() && !r.isWhiteSpaceChar() {
		if length < 3 {
			prefix[length] = r.ch | 0x20
		}
		r.next()
		length++
	}
	for i := length; i < 3; i++ {
		prefix[i] = 0
	}
	return length
}

func (r *inputReader) skip(c uint32) bool {
	if r.ch == c {
		r.next()
		return true
	}
	return false
}

func (r *inputReader) skipWhiteSpace() bool {
	if isWhiteSpaceOrLineTerminator(r.ch) {
		r.next()
		return true
	}
	return false
}

func (r *inputReader) skipParentheses() bool {
	if r.ch != '(' {
		return false
	}
	balance := 0
	for {
		if r.ch == ')' {
			balance--
		} else if r.ch == '(' {
			balance++
		}
		r.next()
		if !(balance > 0 && r.ch != 0) {
			break
		}
	}
	return true
}

type keyword struct {
	pre   [3]uint32
	typ   keywordType
	value int
}

var keywordTable = []keyword{
	{[3]uint32{'j', 'a', 'n'}, kwMonthName, 1},
	{[3]uint32{'f', 'e', 'b'}, kwMonthName, 2},
	{[3]uint32{'m', 'a', 'r'}, kwMonthName, 3},
	{[3]uint32{'a', 'p', 'r'}, kwMonthName, 4},
	{[3]uint32{'m', 'a', 'y'}, kwMonthName, 5},
	{[3]uint32{'j', 'u', 'n'}, kwMonthName, 6},
	{[3]uint32{'j', 'u', 'l'}, kwMonthName, 7},
	{[3]uint32{'a', 'u', 'g'}, kwMonthName, 8},
	{[3]uint32{'s', 'e', 'p'}, kwMonthName, 9},
	{[3]uint32{'o', 'c', 't'}, kwMonthName, 10},
	{[3]uint32{'n', 'o', 'v'}, kwMonthName, 11},
	{[3]uint32{'d', 'e', 'c'}, kwMonthName, 12},
	{[3]uint32{'a', 'm', 0}, kwAmPm, 0},
	{[3]uint32{'p', 'm', 0}, kwAmPm, 12},
	{[3]uint32{'u', 't', 0}, kwTimeZoneName, 0},
	{[3]uint32{'u', 't', 'c'}, kwTimeZoneName, 0},
	{[3]uint32{'z', 0, 0}, kwTimeZoneName, 0},
	{[3]uint32{'g', 'm', 't'}, kwTimeZoneName, 0},
	{[3]uint32{'c', 'd', 't'}, kwTimeZoneName, -5},
	{[3]uint32{'c', 's', 't'}, kwTimeZoneName, -6},
	{[3]uint32{'e', 'd', 't'}, kwTimeZoneName, -4},
	{[3]uint32{'e', 's', 't'}, kwTimeZoneName, -5},
	{[3]uint32{'m', 'd', 't'}, kwTimeZoneName, -6},
	{[3]uint32{'m', 's', 't'}, kwTimeZoneName, -7},
	{[3]uint32{'p', 'd', 't'}, kwTimeZoneName, -7},
	{[3]uint32{'p', 's', 't'}, kwTimeZoneName, -8},
	{[3]uint32{'t', 0, 0}, kwTimeSeparator, 0},
}

func keywordLookup(pre *[3]uint32, length int) keyword {
	for _, k := range keywordTable {
		if k.pre == *pre && (length <= 3 || k.typ == kwMonthName) {
			return k
		}
	}
	return keyword{typ: kwInvalid}
}

type tokenizer struct {
	in  *inputReader
	nxt token
}

func newTokenizer(in *inputReader) *tokenizer {
	t := &tokenizer{in: in}
	t.nxt = t.scan()
	return t
}

func (t *tokenizer) next() token {
	r := t.nxt
	t.nxt = t.scan()
	return r
}

func (t *tokenizer) peek() token { return t.nxt }

func (t *tokenizer) skipSymbol(c byte) bool {
	if t.nxt.isSymbol(c) {
		t.nxt = t.scan()
		return true
	}
	return false
}

func symbolToken(c byte) token { return token{tagSymbol, 1, int(c)} }

func (t *tokenizer) scan() token {
	in := t.in
	prePos := in.position()
	if in.isEnd() {
		return token{tagEnd, 0, -1}
	}
	if in.isDigit() {
		n := in.readUnsignedNumeral()
		return token{tagNumber, in.position() - prePos, n}
	}
	for _, c := range []byte{':', '-', '+', '.', ')'} {
		if in.skip(uint32(c)) {
			return symbolToken(c)
		}
	}
	if in.isAlphaOrAbove() && !in.isWhiteSpaceChar() {
		var buf [3]uint32
		length := in.readWord(&buf)
		k := keywordLookup(&buf, length)
		return token{tokenTag(k.typ), length, k.value}
	}
	if in.skipWhiteSpace() {
		return token{tagWhiteSpace, in.position() - prePos, -1}
	}
	if in.skipParentheses() {
		return token{tagUnknown, 1, -1}
	}
	in.next()
	return token{tagUnknown, 1, -1}
}

func between(x, lo, hi int) bool { return uint32(x-lo) <= uint32(hi-lo) }

type tzComposer struct{ sign, hour, minute int }

func newTZ() tzComposer { return tzComposer{kNone, kNone, kNone} }

func (c *tzComposer) set(offsetHours int) {
	if offsetHours < 0 {
		c.sign = -1
	} else {
		c.sign = 1
	}
	c.hour = offsetHours * c.sign
	c.minute = 0
}
func (c *tzComposer) setSign(s int) {
	if s < 0 {
		c.sign = -1
	} else {
		c.sign = 1
	}
}
func (c *tzComposer) isExpecting(n int) bool {
	return c.hour != kNone && c.minute == kNone && between(n, 0, 59)
}
func (c *tzComposer) isUTC() bool   { return c.hour == 0 && c.minute == 0 }
func (c *tzComposer) isEmpty() bool { return c.hour == kNone }

// write returns the UTC offset in seconds, or NaN when no zone was given.
func (c *tzComposer) write() (float64, bool) {
	if c.sign != kNone {
		if c.hour == kNone {
			c.hour = 0
		}
		if c.minute == kNone {
			c.minute = 0
		}
		total := uint32(c.hour)*3600 + uint32(c.minute)*60 // unsigned arithmetic as V8
		if total > 1<<30-1 {
			return 0, false
		}
		sec := int(total)
		if c.sign < 0 {
			sec = -sec
		}
		return float64(sec), true
	}
	return math.NaN(), true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func newTime() timeComposer { return timeComposer{hourOffset: kNone} }

func (c *timeComposer) isEmpty() bool { return c.index == 0 }
func (c *timeComposer) isExpecting(n int) bool {
	return (c.index == 1 && between(n, 0, 59)) || (c.index == 2 && between(n, 0, 59)) || (c.index == 3 && between(n, 0, 999))
}
func (c *timeComposer) add(n int) bool {
	if c.index < 4 {
		c.comp[c.index] = n
		c.index++
		return true
	}
	return false
}
func (c *timeComposer) addFinal(n int) bool {
	if !c.add(n) {
		return false
	}
	for c.index < 4 {
		c.comp[c.index] = 0
		c.index++
	}
	return true
}

func (c *timeComposer) write() (h, m, s, ms int, ok bool) {
	for c.index < 4 {
		c.comp[c.index] = 0
		c.index++
	}
	hour, minute, second, milli := c.comp[0], c.comp[1], c.comp[2], c.comp[3]
	if c.hourOffset != kNone {
		if !between(hour, 0, 12) {
			return 0, 0, 0, 0, false
		}
		hour %= 12
		hour += c.hourOffset
	}
	if !between(hour, 0, 23) || !between(minute, 0, 59) || !between(second, 0, 59) || !between(milli, 0, 999) {
		if hour != 24 || minute != 0 || second != 0 || milli != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return hour, minute, second, milli, true
}

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	isISO      bool
}

func newDay() dayComposer { return dayComposer{namedMonth: kNone} }

func (c *dayComposer) isEmpty() bool { return c.index == 0 }
func (c *dayComposer) add(n int) bool {
	if c.index < 3 {
		c.comp[c.index] = n
		c.index++
		return true
	}
	return false
}
func isMonth(x int) bool { return between(x, 1, 12) }
func isDay(x int) bool   { return between(x, 1, 31) }

func (c *dayComposer) write() (year, month, day int, ok bool) {
	if c.index < 1 {
		return 0, 0, 0, false
	}
	for c.index < 3 {
		c.comp[c.index] = 1
		c.index++
	}
	month, day = kNone, kNone
	if c.namedMonth == kNone {
		if c.isISO || (c.index == 3 && !isDay(c.comp[0])) {
			year, month, day = c.comp[0], c.comp[1], c.comp[2]
		} else {
			month, day = c.comp[0], c.comp[1]
			if c.index == 3 {
				year = c.comp[2]
			}
		}
	} else {
		month = c.namedMonth
		if c.index == 1 {
			day = c.comp[0]
		} else if !isDay(c.comp[0]) {
			year, day = c.comp[0], c.comp[1]
		} else {
			day, year = c.comp[0], c.comp[1]
		}
	}
	if !c.isISO {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	// Smi::IsValid on a 64-bit build: 31-bit ints in the pointer-compressed build.
	if year < -(1<<30) || year > 1<<30-1 || !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month - 1, day, true
}

func readMilliseconds(t token) int {
	number, length := t.value, t.length
	if length < 3 {
		if length == 1 {
			number *= 100
		} else if length == 2 {
			number *= 10
		}
	} else if length > 3 {
		if length > maxSignificantDigits {
			length = maxSignificantDigits
		}
		factor := 1
		for {
			factor *= 10
			length--
			if length <= 3 {
				break
			}
		}
		number /= factor
	}
	return number
}

// parseES5DateTime tries the ES5 ISO format first and returns the token to continue with in the legacy parser.
func parseES5DateTime(sc *tokenizer, day *dayComposer, tm *timeComposer, tz *tzComposer) token {
	if sc.peek().isAsciiSign() {
		signTok := sc.next()
		if !sc.peek().isFixedLengthNumber(6) {
			return signTok
		}
		sign := signTok.asciiSign()
		year := sc.next().value
		if sign < 0 && year == 0 {
			return signTok
		}
		day.add(sign * year)
	} else if sc.peek().isFixedLengthNumber(4) {
		day.add(sc.next().value)
	} else {
		return sc.next()
	}
	if sc.skipSymbol('-') {
		if !sc.peek().isFixedLengthNumber(2) || !isMonth(sc.peek().value) {
			return sc.next()
		}
		day.add(sc.next().value)
		if sc.skipSymbol('-') {
			if !sc.peek().isFixedLengthNumber(2) || !isDay(sc.peek().value) {
				return sc.next()
			}
			day.add(sc.next().value)
		}
	}
	if !sc.peek().isKeywordType(kwTimeSeparator) {
		if !sc.peek().isEnd() {
			return sc.next()
		}
	} else {
		sc.next()
		invalid := token{tagInvalid, 0, -1}
		if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().value, 0, 24) {
			return invalid
		}
		hourIs24 := sc.peek().value == 24
		tm.add(sc.next().value)
		if !sc.skipSymbol(':') {
			return invalid
		}
		if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().value, 0, 59) || (hourIs24 && sc.peek().value > 0) {
			return invalid
		}
		tm.add(sc.next().value)
		if sc.skipSymbol(':') {
			if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().value, 0, 59) || (hourIs24 && sc.peek().value > 0) {
				return invalid
			}
			tm.add(sc.next().value)
			if sc.skipSymbol('.') {
				if !sc.peek().isNumber() || (hourIs24 && sc.peek().value > 0) {
					return invalid
				}
				tm.add(readMilliseconds(sc.next()))
			}
		}
		if sc.peek().isKeywordZ() {
			sc.next()
			tz.set(0)
		} else if sc.peek().isSymbol('+') || sc.peek().isSymbol('-') {
			if sc.next().value == '+' {
				tz.setSign(1)
			} else {
				tz.setSign(-1)
			}
			if sc.peek().isFixedLengthNumber(4) {
				hourmin := sc.next().value
				hour, min := hourmin/100, hourmin%100
				if !between(hour, 0, 23) || !between(min, 0, 59) {
					return invalid
				}
				tz.hour = hour
				tz.minute = min
			} else {
				if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().value, 0, 23) {
					return invalid
				}
				tz.hour = sc.next().value
				if !sc.skipSymbol(':') {
					return invalid
				}
				if !sc.peek().isFixedLengthNumber(2) || !between(sc.peek().value, 0, 59) {
					return invalid
				}
				tz.minute = sc.next().value
			}
		}
		if !sc.peek().isEnd() {
			return invalid
		}
	}
	if tz.isEmpty() && tm.isEmpty() {
		tz.set(0)
	}
	day.isISO = true
	return token{tagEnd, 0, -1}
}

// Parse is Date.parse: milliseconds since the epoch, or NaN.
func Parse(s string) float64 {
	units := utf16.Encode([]rune(s))
	sc := newTokenizer(newInputReader(units))
	tz := newTZ()
	tm := newTime()
	day := newDay()

	nextUnhandled := parseES5DateTime(sc, &day, &tm, &tz)
	if nextUnhandled.isInvalid() {
		return nan()
	}
	hasReadNumber := !day.isEmpty()
	for tok := nextUnhandled; !tok.isEnd(); tok = sc.next() {
		switch {
		case tok.isNumber():
			hasReadNumber = true
			n := tok.value
			if sc.skipSymbol(':') {
				if sc.skipSymbol(':') {
					if !tm.isEmpty() {
						return nan()
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return nan()
					}
					if sc.peek().isSymbol('.') {
						sc.next()
					}
				}
			} else if sc.skipSymbol('.') && tm.isExpecting(n) {
				tm.add(n)
				if !sc.peek().isNumber() {
					return nan()
				}
				ms := readMilliseconds(sc.next())
				if ms < 0 {
					return nan()
				}
				tm.addFinal(ms)
			} else if tz.isExpecting(n) {
				tz.minute = n
			} else if tm.isExpecting(n) {
				tm.addFinal(n)
				peek := sc.peek()
				if !peek.isEnd() && !peek.isWhiteSpace() && !peek.isKeywordZ() && !peek.isAsciiSign() {
					return nan()
				}
			} else {
				if !day.add(n) {
					return nan()
				}
				sc.skipSymbol('-')
			}
		case tok.isKeyword():
			typ := keywordType(tok.tag)
			value := tok.value
			if typ == kwAmPm && !tm.isEmpty() {
				tm.hourOffset = value
			} else if typ == kwMonthName {
				day.namedMonth = value
				sc.skipSymbol('-')
			} else if typ == kwTimeZoneName && hasReadNumber {
				tz.set(value)
			} else {
				if hasReadNumber {
					return nan()
				}
				if sc.peek().isNumber() {
					return nan()
				}
			}
		case tok.isAsciiSign() && (tz.isUTC() || !tm.isEmpty()):
			tz.setSign(tok.asciiSign())
			n, length := 0, 0
			if sc.peek().isNumber() {
				nt := sc.next()
				length = nt.length
				n = nt.value
			}
			hasReadNumber = true
			if sc.peek().isSymbol(':') {
				tz.hour = n
				tz.minute = kNone
			} else if length == 2 || length == 1 {
				tz.hour = n
				tz.minute = 0
			} else if length == 4 || length == 3 {
				tz.hour = n / 100
				tz.minute = n % 100
			} else {
				return nan()
			}
		case (tok.isAsciiSign() || tok.isSymbol(')')) && hasReadNumber:
			return nan()
		default:
			// Ignore other characters and whitespace.
		}
	}

	year, month, d, ok := day.write()
	if !ok {
		return nan()
	}
	h, mi, sec, ms, ok := tm.write()
	if !ok {
		return nan()
	}
	utcOffset, ok := tz.write()
	if !ok {
		return nan()
	}
	days, ok := MakeDay(int64(year), int64(month), int64(d))
	if !ok {
		return nan()
	}
	date := float64(days*msPerDay + MakeTime(int64(h), int64(mi), int64(sec), int64(ms)))
	if math.IsNaN(utcOffset) {
		const maxTimeBeforeUTC = 8.64e15 + 864e5 // DateCache::kMaxTimeBeforeUTCInMs
		if date < -maxTimeBeforeUTC || date > maxTimeBeforeUTC {
			return nan()
		}
		date = float64(localToUTC(int64(date)))
	} else {
		date -= utcOffset * 1000
	}
	return TimeClip(date)
}
