package websearch

import (
	"fmt"
	"math"
	"time"
	"unicode/utf16"
)

// The Date of JavaScript as the extractors use it: new Date(string) with V8's date parser (the ES5 ISO format
// first, then the legacy format that accepts "Tue, Mar 4, 2025, 10:15 AM"), new Date(number), toISOString and the
// local calendar getters. Time zone: the local zone of the process, as Node's is (TZ).
//
// Port of V8's src/date/dateparser.{h,cc,inl.h} and the conversion of src/builtins/builtins-date.cc.

const (
	dpNone = math.MaxInt32

	dpTokInvalid  = -6
	dpTokUnknown  = -5
	dpTokSpace    = -4
	dpTokNumber   = -3
	dpTokSymbol   = -2
	dpTokEnd      = -1
	dpKwInvalid   = 0
	dpKwMonth     = 1
	dpKwTZ        = 2
	dpKwSeparator = 3
	dpKwAMPM      = 4
)

type dpToken struct {
	tag    int // a token tag, or a keyword type (>= 0)
	length int
	value  int
}

func (t dpToken) isNumber() bool       { return t.tag == dpTokNumber }
func (t dpToken) isSymbol(c rune) bool { return t.tag == dpTokSymbol && t.value == int(c) }
func (t dpToken) isEnd() bool          { return t.tag == dpTokEnd }
func (t dpToken) isSpace() bool        { return t.tag == dpTokSpace }
func (t dpToken) isKeyword() bool      { return t.tag >= dpKwInvalid }
func (t dpToken) isKeywordType(k int) bool {
	return t.tag == k
}
func (t dpToken) isFixed(n int) bool { return t.isNumber() && t.length == n }
func (t dpToken) isASCIISign() bool {
	return t.tag == dpTokSymbol && (t.value == '-' || t.value == '+')
}
func (t dpToken) asciiSign() int { return 44 - t.value }
func (t dpToken) isKeywordZ() bool {
	return t.isKeywordType(dpKwTZ) && t.length == 1
}

type dpKeyword struct {
	p0, p1, p2 uint16
	typ, value int
}

var dpKeywords = []dpKeyword{
	{'j', 'a', 'n', dpKwMonth, 1}, {'f', 'e', 'b', dpKwMonth, 2}, {'m', 'a', 'r', dpKwMonth, 3},
	{'a', 'p', 'r', dpKwMonth, 4}, {'m', 'a', 'y', dpKwMonth, 5}, {'j', 'u', 'n', dpKwMonth, 6},
	{'j', 'u', 'l', dpKwMonth, 7}, {'a', 'u', 'g', dpKwMonth, 8}, {'s', 'e', 'p', dpKwMonth, 9},
	{'o', 'c', 't', dpKwMonth, 10}, {'n', 'o', 'v', dpKwMonth, 11}, {'d', 'e', 'c', dpKwMonth, 12},
	{'a', 'm', 0, dpKwAMPM, 0}, {'p', 'm', 0, dpKwAMPM, 12},
	{'u', 't', 0, dpKwTZ, 0}, {'u', 't', 'c', dpKwTZ, 0}, {'z', 0, 0, dpKwTZ, 0}, {'g', 'm', 't', dpKwTZ, 0},
	{'c', 'd', 't', dpKwTZ, -5}, {'c', 's', 't', dpKwTZ, -6}, {'e', 'd', 't', dpKwTZ, -4}, {'e', 's', 't', dpKwTZ, -5},
	{'m', 'd', 't', dpKwTZ, -6}, {'m', 's', 't', dpKwTZ, -7}, {'p', 'd', 't', dpKwTZ, -7}, {'p', 's', 't', dpKwTZ, -8},
	{'t', 0, 0, dpKwSeparator, 0},
}

type dpScanner struct {
	in   []uint16
	pos  int
	next dpToken
}

func (s *dpScanner) ch() uint16 {
	if s.pos < len(s.in) {
		return s.in[s.pos]
	}
	return 0
}

func dpIsSpace(c uint16) bool { return jsSpace(rune(c)) }

func (s *dpScanner) scan() dpToken {
	pre := s.pos
	if s.pos >= len(s.in) {
		return dpToken{tag: dpTokEnd}
	}
	c := s.ch()
	if c >= '0' && c <= '9' {
		v, i := 0, 0
		for s.pos < len(s.in) && s.ch() >= '0' && s.ch() <= '9' {
			if i < 9 {
				v = v*10 + int(s.ch()-'0')
			}
			i++
			s.pos++
		}
		return dpToken{tag: dpTokNumber, length: s.pos - pre, value: v}
	}
	switch c {
	case ':', '-', '+', '.', ')':
		s.pos++
		return dpToken{tag: dpTokSymbol, value: int(c)}
	}
	if c >= 'A' && !dpIsSpace(c) {
		var buf [3]uint16
		n := 0
		for s.pos < len(s.in) && s.ch() >= 'A' && !dpIsSpace(s.ch()) {
			if n < 3 {
				ch := s.ch()
				if ch >= 'A' && ch <= 'Z' {
					ch += 'a' - 'A'
				}
				buf[n] = ch
			}
			n++
			s.pos++
		}
		for _, k := range dpKeywords {
			if buf[0] == k.p0 && buf[1] == k.p1 && buf[2] == k.p2 && (n <= 3 || k.typ == dpKwMonth) {
				return dpToken{tag: k.typ, length: n, value: k.value}
			}
		}
		return dpToken{tag: dpKwInvalid, length: n}
	}
	if dpIsSpace(c) {
		s.pos++
		return dpToken{tag: dpTokSpace, length: s.pos - pre}
	}
	if c == '(' {
		balance := 0
		for {
			if s.ch() == ')' {
				balance--
			} else if s.ch() == '(' {
				balance++
			}
			s.pos++
			if !(balance > 0 && s.ch() != 0) {
				break
			}
		}
		return dpToken{tag: dpTokUnknown}
	}
	s.pos++
	return dpToken{tag: dpTokUnknown}
}

func (s *dpScanner) nextToken() dpToken {
	r := s.next
	s.next = s.scan()
	return r
}
func (s *dpScanner) peek() dpToken { return s.next }
func (s *dpScanner) skipSymbol(c rune) bool {
	if s.next.isSymbol(c) {
		s.next = s.scan()
		return true
	}
	return false
}

type dpDay struct {
	comp       [3]int
	index      int
	named      int
	isISO      bool
	namedValue bool
}

func dpBetween(x, lo, hi int) bool { return x >= lo && x <= hi }

func (d *dpDay) add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func (d *dpDay) write(out *[8]float64) bool {
	if d.index < 1 {
		return false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	year, month, day := 0, dpNone, dpNone
	isDay := func(x int) bool { return dpBetween(x, 1, 31) }
	if !d.namedValue {
		if d.isISO || (d.index == 3 && !isDay(d.comp[0])) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day = d.comp[0], d.comp[1]
			if d.index == 3 {
				year = d.comp[2]
			}
		}
	} else {
		month = d.named
		if d.index == 1 {
			day = d.comp[0]
		} else if !isDay(d.comp[0]) {
			year, day = d.comp[0], d.comp[1]
		} else {
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.isISO {
		if dpBetween(year, 0, 49) {
			year += 2000
		} else if dpBetween(year, 50, 99) {
			year += 1900
		}
	}
	if !dpBetween(month, 1, 12) || !isDay(day) || year < -(1<<30) || year > (1<<30)-1 {
		return false
	}
	out[0], out[1], out[2] = float64(year), float64(month-1), float64(day)
	return true
}

type dpTime struct {
	comp       [4]int
	index      int
	hourOffset int
}

func dpIsMinute(x int) bool { return dpBetween(x, 0, 59) }
func dpIsHour(x int) bool   { return dpBetween(x, 0, 23) }

func (t *dpTime) isExpecting(n int) bool {
	return (t.index == 1 && dpIsMinute(n)) || (t.index == 2 && dpIsMinute(n)) || (t.index == 3 && dpBetween(n, 0, 999))
}
func (t *dpTime) add(n int) bool {
	if t.index < 4 {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}
func (t *dpTime) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}
func (t *dpTime) write(out *[8]float64) bool {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	h, m, s, ms := t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dpNone {
		if !dpBetween(h, 0, 12) {
			return false
		}
		h %= 12
		h += t.hourOffset
	}
	if !dpIsHour(h) || !dpIsMinute(m) || !dpIsMinute(s) || !dpBetween(ms, 0, 999) {
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return false
		}
	}
	out[3], out[4], out[5], out[6] = float64(h), float64(m), float64(s), float64(ms)
	return true
}

type dpZone struct{ sign, hour, minute int }

func (z *dpZone) set(hours int) {
	if hours < 0 {
		z.sign = -1
	} else {
		z.sign = 1
	}
	z.hour = hours * z.sign
	z.minute = 0
}
func (z *dpZone) setSign(s int) {
	if s < 0 {
		z.sign = -1
	} else {
		z.sign = 1
	}
}
func (z *dpZone) isExpecting(n int) bool {
	return z.hour != dpNone && z.minute == dpNone && dpIsMinute(n)
}
func (z *dpZone) isUTC() bool   { return z.hour == 0 && z.minute == 0 }
func (z *dpZone) isEmpty() bool { return z.hour == dpNone }
func (z *dpZone) write(out *[8]float64) bool {
	if z.sign != dpNone {
		if z.hour == dpNone {
			z.hour = 0
		}
		if z.minute == dpNone {
			z.minute = 0
		}
		total := uint32(z.hour)*3600 + uint32(z.minute)*60
		if total > (1<<30)-1 {
			return false
		}
		t := int(total)
		if z.sign < 0 {
			t = -t
		}
		out[7] = float64(t)
	} else {
		out[7] = math.NaN()
	}
	return true
}

func dpReadMilliseconds(t dpToken) int {
	number, length := t.value, t.length
	if length < 3 {
		if length == 1 {
			number *= 100
		} else if length == 2 {
			number *= 10
		}
	} else if length > 3 {
		if length > 9 {
			length = 9
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

// dpParseES5 is DateParser::ParseES5DateTime. It returns the first token the legacy parser must still handle,
// ok false for an invalid string.
func dpParseES5(s *dpScanner, day *dpDay, tm *dpTime, tz *dpZone) (tok dpToken, ok bool) {
	invalid := dpToken{tag: dpTokInvalid}
	if s.peek().isASCIISign() {
		sign := s.nextToken()
		if !s.peek().isFixed(6) {
			return sign, true
		}
		year := s.nextToken().value
		if sign.asciiSign() < 0 && year == 0 {
			return sign, true
		}
		day.add(sign.asciiSign() * year)
	} else if s.peek().isFixed(4) {
		day.add(s.nextToken().value)
	} else {
		return s.nextToken(), true
	}
	if s.skipSymbol('-') {
		if !s.peek().isFixed(2) || !dpBetween(s.peek().value, 1, 12) {
			return s.nextToken(), true
		}
		day.add(s.nextToken().value)
		if s.skipSymbol('-') {
			if !s.peek().isFixed(2) || !dpBetween(s.peek().value, 1, 31) {
				return s.nextToken(), true
			}
			day.add(s.nextToken().value)
		}
	}
	if !s.peek().isKeywordType(dpKwSeparator) {
		if !s.peek().isEnd() {
			return s.nextToken(), true
		}
	} else {
		s.nextToken()
		if !s.peek().isFixed(2) || !dpBetween(s.peek().value, 0, 24) {
			return invalid, false
		}
		hour24 := s.peek().value == 24
		tm.add(s.nextToken().value)
		if !s.skipSymbol(':') {
			return invalid, false
		}
		if !s.peek().isFixed(2) || !dpIsMinute(s.peek().value) || (hour24 && s.peek().value > 0) {
			return invalid, false
		}
		tm.add(s.nextToken().value)
		if s.skipSymbol(':') {
			if !s.peek().isFixed(2) || !dpIsMinute(s.peek().value) || (hour24 && s.peek().value > 0) {
				return invalid, false
			}
			tm.add(s.nextToken().value)
			if s.skipSymbol('.') {
				if !s.peek().isNumber() || (hour24 && s.peek().value > 0) {
					return invalid, false
				}
				tm.add(dpReadMilliseconds(s.nextToken()))
			}
		}
		if s.peek().isKeywordZ() {
			s.nextToken()
			tz.set(0)
		} else if s.peek().isSymbol('+') || s.peek().isSymbol('-') {
			if s.nextToken().value == '+' {
				tz.setSign(1)
			} else {
				tz.setSign(-1)
			}
			if s.peek().isFixed(4) {
				hm := s.nextToken().value
				h, m := hm/100, hm%100
				if !dpIsHour(h) || !dpIsMinute(m) {
					return invalid, false
				}
				tz.hour, tz.minute = h, m
			} else {
				if !s.peek().isFixed(2) || !dpIsHour(s.peek().value) {
					return invalid, false
				}
				tz.hour = s.nextToken().value
				if !s.skipSymbol(':') {
					return invalid, false
				}
				if !s.peek().isFixed(2) || !dpIsMinute(s.peek().value) {
					return invalid, false
				}
				tz.minute = s.nextToken().value
			}
		}
		if !s.peek().isEnd() {
			return invalid, false
		}
	}
	if tz.isEmpty() && tm.index == 0 {
		tz.set(0)
	}
	day.isISO = true
	return dpToken{tag: dpTokEnd}, true
}

// dpParse is DateParser::Parse: the fields of the date, year month day hour minute second ms and the UTC offset in
// seconds (NaN for local time).
func dpParse(str string) (out [8]float64, ok bool) {
	s := &dpScanner{in: utf16.Encode([]rune(str))}
	s.next = s.scan()
	var tz = dpZone{sign: dpNone, hour: dpNone, minute: dpNone}
	var tm = dpTime{hourOffset: dpNone}
	var day = dpDay{named: dpNone}
	tok, valid := dpParseES5(s, &day, &tm, &tz)
	if !valid {
		return out, false
	}
	hasReadNumber := day.index != 0
	for ; !tok.isEnd(); tok = s.nextToken() {
		switch {
		case tok.isNumber():
			hasReadNumber = true
			n := tok.value
			if s.skipSymbol(':') {
				if s.skipSymbol(':') {
					if tm.index != 0 {
						return out, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return out, false
					}
					if s.peek().isSymbol('.') {
						s.nextToken()
					}
				}
			} else if s.skipSymbol('.') && tm.isExpecting(n) {
				tm.add(n)
				if !s.peek().isNumber() {
					return out, false
				}
				ms := dpReadMilliseconds(s.nextToken())
				if ms < 0 {
					return out, false
				}
				tm.addFinal(ms)
			} else if tz.isExpecting(n) {
				tz.minute = n
			} else if tm.isExpecting(n) {
				tm.addFinal(n)
				p := s.peek()
				if !p.isEnd() && !p.isSpace() && !p.isKeywordZ() && !p.isASCIISign() {
					return out, false
				}
			} else {
				if !day.add(n) {
					return out, false
				}
				s.skipSymbol('-')
			}
		case tok.isKeyword():
			typ, value := tok.tag, tok.value
			if typ == dpKwAMPM && tm.index != 0 {
				tm.hourOffset = value
			} else if typ == dpKwMonth {
				day.named = value
				day.namedValue = true
				s.skipSymbol('-')
			} else if typ == dpKwTZ && hasReadNumber {
				tz.set(value)
			} else {
				if hasReadNumber {
					return out, false
				}
				if s.peek().isNumber() {
					return out, false
				}
			}
		case tok.isASCIISign() && (tz.isUTC() || tm.index != 0):
			tz.setSign(tok.asciiSign())
			n, length := 0, 0
			if s.peek().isNumber() {
				nt := s.nextToken()
				length, n = nt.length, nt.value
			}
			hasReadNumber = true
			if s.peek().isSymbol(':') {
				tz.hour = n
				tz.minute = dpNone
			} else if length == 2 || length == 1 {
				tz.hour = n
				tz.minute = 0
			} else if length == 4 || length == 3 {
				tz.hour = n / 100
				tz.minute = n % 100
			} else {
				return out, false
			}
		case (tok.isASCIISign() || tok.isSymbol(')')) && hasReadNumber:
			return out, false
		}
	}
	if !(day.write(&out) && tm.write(&out) && tz.write(&out)) {
		return out, false
	}
	return out, true
}

const jsMaxTime = 8.64e15

func jsTimeClip(t float64) float64 {
	if math.IsNaN(t) || math.Abs(t) > jsMaxTime {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

// jsDaysFromCivil is the day number (days since 1970-01-01) of a proleptic Gregorian date.
func jsDaysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func jsMakeDay(year, month, date float64) float64 {
	if math.IsNaN(year) || math.IsNaN(month) || math.IsNaN(date) || math.IsInf(year, 0) || math.IsInf(month, 0) || math.IsInf(date, 0) {
		return math.NaN()
	}
	y, m, dt := math.Trunc(year), math.Trunc(month), math.Trunc(date)
	ym := y + math.Floor(m/12)
	mn := math.Mod(m, 12)
	if mn < 0 {
		mn += 12
	}
	if ym < -1000000 || ym > 1000000 {
		return math.NaN()
	}
	return float64(jsDaysFromCivil(int64(ym), int64(mn)+1, 1)) + dt - 1
}

// jsDateFromString is new Date(string).getTime(): NaN when the string is not a date.
func jsDateFromString(s string) float64 {
	f, ok := dpParse(s)
	if !ok {
		return math.NaN()
	}
	day := jsMakeDay(f[0], f[1], f[2])
	tm := f[3]*3600000 + f[4]*60000 + f[5]*1000 + f[6]
	date := day*86400000 + tm
	if math.IsNaN(f[7]) {
		if math.IsNaN(date) || math.Abs(date) > jsMaxTime+864e5 {
			return math.NaN()
		}
		u := time.UnixMilli(int64(date)).UTC()
		date = float64(time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), u.Minute(), u.Second(), u.Nanosecond(), time.Local).UnixMilli())
	} else {
		date -= f[7] * 1000
	}
	return jsTimeClip(date)
}

// jsDateFromNumber is new Date(n).getTime().
func jsDateFromNumber(n float64) float64 { return jsTimeClip(n) }

// jsISOString is Date.prototype.toISOString; it throws a RangeError for an invalid date.
func jsISOString(ms float64) string {
	if math.IsNaN(ms) {
		panic(&jsThrow{name: "RangeError", msg: "Invalid time value"})
	}
	t := time.UnixMilli(int64(ms)).UTC()
	y := t.Year()
	var year string
	if y >= 0 && y <= 9999 {
		year = fmt.Sprintf("%04d", y)
	} else if y < 0 {
		year = fmt.Sprintf("-%06d", -y)
	} else {
		year = fmt.Sprintf("+%06d", y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", year, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6)
}

// jsISODate is `new Date(ms).toISOString().split('T')[0]`.
func jsISODate(ms float64) string {
	s := jsISOString(ms)
	for i := 0; i < len(s); i++ {
		if s[i] == 'T' {
			return s[:i]
		}
	}
	return s
}

// jsLocalDate is the local getFullYear, getMonth and getDate of a valid time value.
func jsLocalDate(ms float64) (year, month, day int) {
	t := time.UnixMilli(int64(ms)).In(time.Local)
	return t.Year(), int(t.Month()), t.Day()
}
