// Package croner is the part of croner 10.0.1 (https://github.com/Hexagon/croner, MIT, Copyright (c) 2015-2024 Hexagon) that
// pi-schedule-prompt reaches: `new Cron(pattern, fn)` and `nextRun()` with no options, in the process's local time zone. The
// original rejects every pattern that does not have six fields before it calls croner, so the year field is always `*`; the
// timezone, utcOffset, startAt, stopAt, maxRuns, interval, dayOffset, protect, mode, alternativeWeekdays and sloppyRanges options
// are never set and are not ported. Error messages are croner's, character for character.
//
// upstream: src/pattern.ts (CronPattern), src/date.ts (CronDate), src/croner.ts (Cron), src/helpers/timezone.ts (fromTZISO).
package croner

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	lastOccurrence = 0b100000
	anyOccurrence  = 0b00001 | 0b00010 | 0b00100 | 0b01000 | 0b10000 | lastOccurrence
)

var occurrenceBitmasks = [5]int{0b00001, 0b00010, 0b00100, 0b01000, 0b10000}

// Pattern is a parsed cron pattern (CronPattern with the default options).
type Pattern struct {
	pattern         string
	second          [60]int
	minute          [60]int
	hour            [24]int
	day             [31]int
	month           [12]int
	dayOfWeek       [7]int
	lastDayOfMonth  bool
	lastWeekday     bool
	nearestWeekdays [31]int
	starDOM         bool
	starDOW         bool
	useAndLogic     bool
}

func perr(format string, a ...string) error { return errors.New(sprintf(format, a...)) }

func sprintf(format string, a ...string) string {
	var b strings.Builder
	i := 0
	for _, r := range format {
		if r == '%' {
			b.WriteString(a[i])
			i++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// jsNum formats a number as JavaScript prints it (integers only matter here).
func jsNum(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case math.Abs(f) < 1e21:
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	if i := strings.IndexByte(s, 'e'); i >= 0 && s[i+1] != '-' && s[i+1] != '+' {
		s = s[:i+1] + "+" + s[i+1:]
	}
	// JS prints 1e+21, Go 1e+21 (two-digit exponent minimum); strip a leading zero of the exponent.
	if i := strings.IndexAny(s, "+-"); i > 0 && i+2 < len(s) && s[i+1] == '0' {
		s = s[:i+1] + s[i+2:]
	}
	return s
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// JSFields is `s.trim().split(/\s+/)` for a non-empty trimmed string and `s.match(/\S+/g)` otherwise: the runs of non-whitespace.
func JSFields(s string) []string {
	return strings.FieldsFunc(s, isJSSpace)
}

// JSTrim is String.prototype.trim.
func JSTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsParseInt is parseInt(s, 10): ok=false is NaN.
func jsParseInt(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n == 0 {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s[:n], 64)
	if neg {
		f = -f
	}
	return f, true
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// replaceFold replaces every ASCII-case-insensitive occurrence of the lower-case ASCII word (JavaScript's /word/gi without the u flag
// folds no non-ASCII character onto an ASCII one).
func replaceFold(s, word, repl string) string {
	if word == "" {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if i+len(word) <= len(s) && matchFold(s[i:i+len(word)], word) {
			b.WriteString(repl)
			i += len(word)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func matchFold(s, word string) bool {
	for i := 0; i < len(word); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c |= 0x20
		}
		if c != word[i] {
			return false
		}
	}
	return true
}

// nth is the value `result[1] || defaultValue` of the original: a string after `#` or the `L` suffix, or the numeric default.
type nthValue struct {
	str   string
	isStr bool
	num   int
}

func (n nthValue) numeric() (int, bool) { return n.num, !n.isStr }

// NewPattern parses a cron pattern as `new CronPattern(pattern, undefined, {mode: "auto"})` does.
func NewPattern(pattern string) (*Pattern, error) {
	p := &Pattern{pattern: pattern}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Pattern) parse() error {
	if strings.Contains(p.pattern, "@") {
		cleaned, err := handleNicknames(p.pattern)
		if err != nil {
			return err
		}
		p.pattern = JSTrim(cleaned)
	}
	parts := JSFields(p.pattern)
	if len(parts) == 0 {
		parts = []string{""}
	}
	if len(parts) < 5 || len(parts) > 7 {
		return perr("CronPattern: invalid configuration format ('%'), exactly five, six, or seven space separated parts are required.", p.pattern)
	}
	if len(parts) == 5 {
		parts = append([]string{"0"}, parts...)
	}
	if len(parts) == 6 {
		parts = append(parts, "*")
	}
	if strings.ToUpper(parts[3]) == "LW" {
		p.lastWeekday = true
		parts[3] = ""
	} else if strings.Contains(strings.ToUpper(parts[3]), "L") {
		parts[3] = strings.NewReplacer("L", "", "l", "").Replace(parts[3])
		p.lastDayOfMonth = true
	}
	if parts[3] == "*" {
		p.starDOM = true
	}
	starYear := parts[6] == "*"
	_ = starYear
	if utf16Len(parts[4]) >= 3 {
		parts[4] = replaceAlphaMonths(parts[4])
	}
	if utf16Len(parts[5]) >= 3 {
		parts[5] = replaceAlphaDays(parts[5])
	}
	if strings.HasPrefix(parts[5], "+") {
		p.useAndLogic = true
		parts[5] = parts[5][1:]
		if parts[5] == "" {
			return perr("CronPattern: Day-of-week field cannot be empty after '+' modifier.")
		}
	}
	if parts[5] == "*" {
		p.starDOW = true
	}
	if strings.Contains(p.pattern, "?") {
		for i := 0; i < 6; i++ {
			parts[i] = strings.ReplaceAll(parts[i], "?", "*")
		}
		if parts[6] != "" {
			parts[6] = strings.ReplaceAll(parts[6], "?", "*")
		}
	}
	if err := throwAtIllegalCharacters(parts); err != nil {
		return err
	}
	if err := p.partToArray("second", parts[0], 0, nthValue{num: 1}); err != nil {
		return err
	}
	if err := p.partToArray("minute", parts[1], 0, nthValue{num: 1}); err != nil {
		return err
	}
	if err := p.partToArray("hour", parts[2], 0, nthValue{num: 1}); err != nil {
		return err
	}
	if err := p.partToArray("day", parts[3], -1, nthValue{num: 1}); err != nil {
		return err
	}
	if err := p.partToArray("month", parts[4], -1, nthValue{num: 1}); err != nil {
		return err
	}
	if err := p.partToArray("dayOfWeek", parts[5], 0, nthValue{num: anyOccurrence}); err != nil {
		return err
	}
	// The year field is parts[6] == "*" for a six-field pattern: every year matches. A seven-field pattern never gets here
	// (the original's six-field check comes first); its year is not parsed.
	if parts[6] != "*" {
		return perr("CronPattern: a year field is not supported by this port")
	}
	return nil
}

func handleNicknames(pattern string) (string, error) {
	clean := strings.ToLower(JSTrim(pattern))
	switch clean {
	case "@yearly", "@annually":
		return "0 0 1 1 *", nil
	case "@monthly":
		return "0 0 1 * *", nil
	case "@weekly":
		return "0 0 * * 0", nil
	case "@daily", "@midnight":
		return "0 0 * * *", nil
	case "@hourly":
		return "0 * * * *", nil
	case "@reboot":
		return "", perr("CronPattern: @reboot is not supported in this environment. This is an event-based trigger that requires system startup detection.")
	}
	return pattern, nil
}

func replaceAlphaDays(conf string) string {
	conf = replaceFold(conf, "-sun", "-7")
	for i, d := range []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"} {
		conf = replaceFold(conf, d, strconv.Itoa(i))
	}
	return conf
}

func replaceAlphaMonths(conf string) string {
	for i, m := range []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"} {
		conf = replaceFold(conf, m, strconv.Itoa(i+1))
	}
	return conf
}

func throwAtIllegalCharacters(parts []string) error {
	for i, part := range parts {
		allowed := "/*0123456789,-"
		switch i {
		case 3:
			allowed += "WwLl"
		case 5:
			allowed += "#Ll"
		}
		for _, r := range part {
			if r > 0x7f || !strings.ContainsRune(allowed, r) {
				return perr("CronPattern: configuration entry % (%) contains illegal characters.", strconv.Itoa(i), part)
			}
		}
	}
	return nil
}

var partLength = map[string]int{"second": 60, "minute": 60, "hour": 24, "day": 31, "month": 12, "dayOfWeek": 7, "nearestWeekdays": 31}

func (p *Pattern) slot(part string) []int {
	switch part {
	case "second":
		return p.second[:]
	case "minute":
		return p.minute[:]
	case "hour":
		return p.hour[:]
	case "day":
		return p.day[:]
	case "month":
		return p.month[:]
	case "dayOfWeek":
		return p.dayOfWeek[:]
	case "nearestWeekdays":
		return p.nearestWeekdays[:]
	}
	return nil
}

func (p *Pattern) partToArray(typ, conf string, offset int, def nthValue) error {
	arr := p.slot(typ)
	lastDay := typ == "day" && p.lastDayOfMonth
	lastWeekday := typ == "day" && p.lastWeekday
	if conf == "" && !lastDay && !lastWeekday {
		return perr("CronPattern: configuration entry % (%) is empty, check for trailing spaces.", typ, conf)
	}
	if conf == "*" {
		for i := range arr {
			arr[i] = def.num
		}
		return nil
	}
	split := strings.Split(conf, ",")
	switch {
	case len(split) > 1:
		for _, s := range split {
			if err := p.partToArray(typ, s, offset, def); err != nil {
				return err
			}
		}
	case strings.Contains(conf, "-") && strings.Contains(conf, "/"):
		return p.handleRangeWithStepping(conf, typ, offset, def)
	case strings.Contains(conf, "-"):
		return p.handleRange(conf, typ, offset, def)
	case strings.Contains(conf, "/"):
		return p.handleStepping(conf, typ, offset, def)
	case conf != "":
		return p.handleNumber(conf, typ, offset, def)
	}
	return nil
}

func upperHas(s, sub string) bool { return strings.Contains(strings.ToUpper(s), sub) }

func (p *Pattern) handleNumber(conf, typ string, offset int, def nthValue) error {
	rest, nth, err := extractNth(conf, typ)
	if err != nil {
		return err
	}
	nearest := upperHas(conf, "W")
	if typ != "day" && nearest {
		return perr("CronPattern: Nearest weekday modifier (W) only allowed in day-of-month.")
	}
	if nearest {
		typ = "nearestWeekdays"
	}
	f, ok := jsParseInt(rest)
	if !ok {
		return perr("CronPattern: % is not a number: '%'", typ, conf)
	}
	return p.setPart(typ, f+float64(offset), pickNth(nth, def))
}

func pickNth(nth *string, def nthValue) nthValue {
	if nth != nil && *nth != "" {
		return nthValue{str: *nth, isStr: true}
	}
	return def
}

func (p *Pattern) setPart(part string, index float64, value nthValue) error {
	if part == "dayOfWeek" {
		if index == 7 {
			index = 0
		}
		if index < 0 || index > 6 {
			return perr("CronPattern: Invalid value for dayOfWeek: %", jsNum(index))
		}
		return p.setNthWeekdayOfMonth(int(index), value)
	}
	bound := map[string]float64{"second": 60, "minute": 60, "hour": 24, "day": 31, "nearestWeekdays": 31, "month": 12}[part]
	if index < 0 || index >= bound {
		return perr("CronPattern: Invalid value for %: %", part, jsNum(index))
	}
	v, _ := value.numeric()
	p.slot(part)[int(index)] = v
	return nil
}

func (p *Pattern) validateRange(lower, upper float64, steps *float64, typ, conf string) error {
	if lower > upper {
		return perr("CronPattern: From value is larger than to value: '%'", conf)
	}
	if steps != nil {
		if *steps == 0 {
			return perr("CronPattern: Syntax error, illegal stepping: 0")
		}
		if *steps > float64(partLength[typ]) {
			return perr("CronPattern: Syntax error, steps cannot be greater than maximum value of part (%)", strconv.Itoa(partLength[typ]))
		}
	}
	return nil
}

func (p *Pattern) handleRangeWithStepping(conf, typ string, offset int, def nthValue) error {
	if upperHas(conf, "W") {
		return perr("CronPattern: Syntax error, W is not allowed in ranges with stepping.")
	}
	rest, nth, err := extractNth(conf, typ)
	if err != nil {
		return err
	}
	lo, hi, st, ok := matchRangeStep(rest)
	if !ok {
		return perr("CronPattern: Syntax error, illegal range with stepping: '%'", conf)
	}
	lower, _ := jsParseInt(lo)
	upper, _ := jsParseInt(hi)
	steps, _ := jsParseInt(st)
	lower += float64(offset)
	upper += float64(offset)
	if err := p.validateRange(lower, upper, &steps, typ, conf); err != nil {
		return err
	}
	v := pickNth(nth, def)
	for i := lower; i <= upper; i += steps {
		if err := p.setPart(typ, i, v); err != nil {
			return err
		}
	}
	return nil
}

// matchRangeStep is /^(\d+)-(\d+)\/(\d+)$/.
func matchRangeStep(s string) (lo, hi, st string, ok bool) {
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		return
	}
	slash := strings.IndexByte(s, '/')
	if slash < dash {
		return
	}
	lo, hi, st = s[:dash], s[dash+1:slash], s[slash+1:]
	for _, d := range []string{lo, hi, st} {
		if d == "" {
			return "", "", "", false
		}
		for i := 0; i < len(d); i++ {
			if d[i] < '0' || d[i] > '9' {
				return "", "", "", false
			}
		}
	}
	return lo, hi, st, true
}

func extractNth(conf, typ string) (rest string, nth *string, err error) {
	rest = conf
	if strings.Contains(rest, "#") {
		if typ != "dayOfWeek" {
			return "", nil, perr("CronPattern: nth (#) only allowed in day-of-week field")
		}
		parts := strings.Split(rest, "#")
		s := ""
		if len(parts) > 1 {
			s = parts[1]
		}
		nth = &s
		rest = parts[0]
	} else if strings.HasSuffix(strings.ToUpper(rest), "L") {
		if typ != "dayOfWeek" {
			return "", nil, perr("CronPattern: L modifier only allowed in day-of-week field (use L alone for day-of-month)")
		}
		s := "L"
		nth = &s
		rest = rest[:len(rest)-1]
	}
	return rest, nth, nil
}

func (p *Pattern) handleRange(conf, typ string, offset int, def nthValue) error {
	if upperHas(conf, "W") {
		return perr("CronPattern: Syntax error, W is not allowed in a range.")
	}
	rest, nth, err := extractNth(conf, typ)
	if err != nil {
		return err
	}
	split := strings.Split(rest, "-")
	if len(split) != 2 {
		return perr("CronPattern: Syntax error, illegal range: '%'", conf)
	}
	lower, ok1 := jsParseInt(split[0])
	upper, ok2 := jsParseInt(split[1])
	lower += float64(offset)
	upper += float64(offset)
	if !ok1 {
		return perr("CronPattern: Syntax error, illegal lower range (NaN)")
	}
	if !ok2 {
		return perr("CronPattern: Syntax error, illegal upper range (NaN)")
	}
	if err := p.validateRange(lower, upper, nil, typ, conf); err != nil {
		return err
	}
	v := pickNth(nth, def)
	for i := lower; i <= upper; i++ {
		if err := p.setPart(typ, i, v); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pattern) handleStepping(conf, typ string, offset int, def nthValue) error {
	if upperHas(conf, "W") {
		return perr("CronPattern: Syntax error, W is not allowed in parts with stepping.")
	}
	rest, nth, err := extractNth(conf, typ)
	if err != nil {
		return err
	}
	split := strings.Split(rest, "/")
	if len(split) != 2 {
		return perr("CronPattern: Syntax error, illegal stepping: '%'", conf)
	}
	if split[0] == "" {
		return perr("CronPattern: Syntax error, stepping with missing prefix ('%') is not allowed. Use wildcard (*/step) or range (min-max/step) instead.", conf)
	}
	if split[0] != "*" {
		return perr("CronPattern: Syntax error, stepping with numeric prefix ('%') is not allowed. Use wildcard (*/step) or range (min-max/step) instead.", conf)
	}
	steps, ok := jsParseInt(split[1])
	if !ok {
		return perr("CronPattern: Syntax error, illegal stepping: (NaN)")
	}
	if err := p.validateRange(0, float64(partLength[typ]-1), &steps, typ, conf); err != nil {
		return err
	}
	v := pickNth(nth, def)
	for i := 0.0; i < float64(partLength[typ]); i += steps {
		if err := p.setPart(typ, i, v); err != nil {
			return err
		}
	}
	return nil
}

// setNthWeekdayOfMonth: nth is the numeric default, or a string (JavaScript coerces it with < and >).
func (p *Pattern) setNthWeekdayOfMonth(index int, nth nthValue) error {
	if nth.isStr {
		if strings.ToUpper(nth.str) == "L" {
			p.dayOfWeek[index] |= lastOccurrence
			return nil
		}
		f, ok := jsToNumber(nth.str)
		if ok && f < 6 && f > 0 {
			// OCCURRENCE_BITMASKS[f - 1] is undefined for a fractional index: `x | undefined` leaves x as it is.
			if f == math.Trunc(f) {
				p.dayOfWeek[index] |= occurrenceBitmasks[int(f)-1]
			}
			return nil
		}
		return perr("CronPattern: nth weekday out of range, should be 1-5 or L. Value: %, Type: string", nth.str)
	}
	if nth.num == anyOccurrence {
		p.dayOfWeek[index] = anyOccurrence
		return nil
	}
	if nth.num < 6 && nth.num > 0 {
		p.dayOfWeek[index] |= occurrenceBitmasks[nth.num-1]
		return nil
	}
	return perr("CronPattern: nth weekday out of range, should be 1-5 or L. Value: %, Type: number", strconv.Itoa(nth.num))
}

// jsToNumber is Number(s) for the strings that can reach it (the field allows only digits, `-`, `*`, `/`, `,`, `#` and `L`).
func jsToNumber(s string) (float64, bool) {
	if s == "" {
		return 0, true
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f, true
}
