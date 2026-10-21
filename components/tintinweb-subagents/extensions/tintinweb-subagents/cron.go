package tintinweb_subagents

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A cron expression parser and next-run calculator with the semantics of croner 10.0.1, the library the original
// schedules with (src/schedule.ts `new Cron(expr, fn)`, no options). croner's `Cron` and `CronPattern` read
// "second minute hour day-of-month month day-of-week" (the scheduler requires exactly those six fields, so the
// five- and seven-field forms of croner are not reachable and not ported). The algorithm is croner's own, step for
// step: it is full of quirks (the day-of-week and day-of-month fields combine with OR unless the day-of-week field
// starts with `+`, a search stops at the year 3000, a day that does not exist in a month is normalized and searched
// again), and a clean re-derivation would disagree with it on some date. port/record-croner.mjs records croner's own
// answers and cron_test.go replays them. upstream: croner src/pattern.ts (CronPattern), src/date.ts (CronDate),
// src/croner.ts (Cron.nextRun), as used by src/schedule.ts.

// The day-of-week occurrence bitmasks. upstream: croner pattern.ts LAST_OCCURRENCE, ANY_OCCURRENCE, OCCURRENCE_BITMASKS.
const (
	lastOccurrence = 0b100000
	anyOccurrence  = 0b00001 | 0b00010 | 0b00100 | 0b01000 | 0b10000 | lastOccurrence
)

var occurrenceBitmasks = [5]int{0b00001, 0b00010, 0b00100, 0b01000, 0b10000}

// cronYearLimit: a search that reaches this year gives up (croner date.ts recurse, `this.year >= 3000` with a `*` year).
const cronYearLimit = 3000

// cronStackLimit is how many nested `recurse` calls croner's search can make before V8 throws "Maximum call stack size
// exceeded" (the search recurses once per field it settles or carries, and nothing unwinds until it is done). A search
// for a run that never comes, whose failure is decided at the day level, makes about a dozen calls per year to the year
// 3000 and overflows: croner's constructor throws, so the original refuses such a pattern ("Invalid schedule"), where a
// search with fewer calls ends quietly with no next run. The value sits between the most calls a completed search made
// and the fewest at which one overflowed, both measured by port/record-croner.mjs under Node 24.19.0 and kept in
// testdata/croner-stack.json, which cron_test.go replays call for call.
const cronStackLimit = 7000

// jsSpace reports whether r is a JavaScript whitespace character (what `\s`, `\S` and String.prototype.trim use).
// Go's unicode.IsSpace differs (it has U+0085 and lacks U+FEFF).
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsFields splits on runs of JavaScript whitespace: `s.match(/\S+/g) || []`.
func jsFields(s string) []string { return strings.FieldsFunc(s, jsSpace) }

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, jsSpace) }

// cronPattern is croner's CronPattern for the six-field form. upstream: pattern.ts.
type cronPattern struct {
	second, minute, hour, day, month, dayOfWeek, nearestWeekdays []int

	lastDayOfMonth, lastWeekday bool
	starDOM, starDOW            bool
	useAndLogic                 bool // a `+` before the day-of-week field: day-of-month AND day-of-week
}

const (
	partSecond = iota
	partMinute
	partHour
	partDay
	partMonth
	partDayOfWeek
	partNearestWeekdays
)

var partNames = [...]string{"second", "minute", "hour", "day", "month", "dayOfWeek", "nearestWeekdays"}

func (p *cronPattern) arr(part int) []int {
	switch part {
	case partSecond:
		return p.second
	case partMinute:
		return p.minute
	case partHour:
		return p.hour
	case partDay:
		return p.day
	case partMonth:
		return p.month
	case partDayOfWeek:
		return p.dayOfWeek
	}
	return p.nearestWeekdays
}

var (
	illegalDOM   = regexp.MustCompile(`[^/*0-9,\-WwLl]+`)
	illegalDOW   = regexp.MustCompile(`[^/*0-9,\-#Ll]+`)
	illegalOther = regexp.MustCompile(`[^/*0-9,\-]+`)
	rangeStepRe  = regexp.MustCompile(`^(\d+)-(\d+)/(\d+)$`)
)

// replaceFold is a global, case-insensitive replace of an ASCII word (JavaScript `.replace(/word/gi, with)`).
func replaceFold(s, word, with string) string {
	return regexp.MustCompile("(?i)"+regexp.QuoteMeta(word)).ReplaceAllLiteralString(s, with)
}

var monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// parseCronPattern parses a six-field expression. upstream: pattern.ts CronPattern.parse.
func parseCronPattern(pattern string) (*cronPattern, error) {
	parts := jsFields(pattern)
	if len(parts) != 6 {
		return nil, fmt.Errorf("CronPattern: invalid configuration format ('%s'), exactly five, six, or seven space separated parts are required.", pattern)
	}
	// croner appends `*` for an omitted year; the year field is not ported, so there is no seventh part.
	p := &cronPattern{
		second: make([]int, 60), minute: make([]int, 60), hour: make([]int, 24), day: make([]int, 31), month: make([]int, 12),
		dayOfWeek: make([]int, 7), nearestWeekdays: make([]int, 31),
	}
	// `L` and `LW` in the day-of-month field are flags, not values.
	if strings.EqualFold(parts[3], "LW") {
		p.lastWeekday = true
		parts[3] = ""
	} else if strings.ContainsAny(parts[3], "Ll") {
		parts[3] = strings.NewReplacer("L", "", "l", "").Replace(parts[3])
		p.lastDayOfMonth = true
	}
	if parts[3] == "*" {
		p.starDOM = true
	}
	if len(parts[4]) >= 3 {
		for i, n := range monthNames {
			parts[4] = replaceFold(parts[4], n, fmt.Sprint(i+1))
		}
	}
	if len(parts[5]) >= 3 {
		// -sun is 7 so that a range ending on Sunday is not backwards; then sun is 0.
		parts[5] = replaceFold(parts[5], "-sun", "-7")
		for i, n := range dayNames {
			parts[5] = replaceFold(parts[5], n, fmt.Sprint(i))
		}
	}
	if strings.HasPrefix(parts[5], "+") {
		p.useAndLogic = true
		parts[5] = parts[5][1:]
		if parts[5] == "" {
			return nil, errors.New("CronPattern: Day-of-week field cannot be empty after '+' modifier.")
		}
	}
	if parts[5] == "*" {
		p.starDOW = true
	}
	// `?` is an alias of `*`; it is replaced after starDOM and starDOW were decided, as croner does.
	if strings.Contains(pattern, "?") {
		for i := range parts {
			parts[i] = strings.ReplaceAll(parts[i], "?", "*")
		}
	}
	for i, part := range parts {
		re := illegalOther
		switch i {
		case 3:
			re = illegalDOM
		case 5:
			re = illegalDOW
		}
		if re.MatchString(part) {
			return nil, fmt.Errorf("CronPattern: configuration entry %d (%s) contains illegal characters.", i, part)
		}
	}
	for _, step := range []struct {
		part   int
		conf   string
		offset int
		def    int
	}{
		{partSecond, parts[0], 0, 1}, {partMinute, parts[1], 0, 1}, {partHour, parts[2], 0, 1},
		{partDay, parts[3], -1, 1}, {partMonth, parts[4], -1, 1}, {partDayOfWeek, parts[5], 0, anyOccurrence},
	} {
		if err := p.partToArray(step.part, step.conf, step.offset, step.def); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// partToArray marks the values a field selects. upstream: pattern.ts partToArray.
func (p *cronPattern) partToArray(part int, conf string, offset, def int) error {
	arr := p.arr(part)
	if conf == "" && !(part == partDay && p.lastDayOfMonth) && !(part == partDay && p.lastWeekday) {
		return fmt.Errorf("CronPattern: configuration entry %s (%s) is empty, check for trailing spaces.", partNames[part], conf)
	}
	if conf == "*" {
		for i := range arr {
			arr[i] = def
		}
		return nil
	}
	if list := strings.Split(conf, ","); len(list) > 1 {
		for _, item := range list {
			if err := p.partToArray(part, item, offset, def); err != nil {
				return err
			}
		}
		return nil
	}
	hasDash, hasSlash := strings.Contains(conf, "-"), strings.Contains(conf, "/")
	switch {
	case hasDash && hasSlash:
		return p.handleRangeWithStepping(conf, part, offset, def)
	case hasDash:
		return p.handleRange(conf, part, offset, def)
	case hasSlash:
		return p.handleStepping(conf, part, offset, def)
	case conf != "":
		return p.handleNumber(conf, part, offset, def)
	}
	return nil
}

// jsParseInt is parseInt(s, 10) for a string without a sign or whitespace: its leading digits, or false (NaN).
func jsParseInt(s string) (float64, bool) {
	var n float64
	seen := false
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + float64(c-'0')
		seen = true
	}
	return n, seen
}

// extractNth splits `#n` (the nth weekday) or a trailing `L` (the last one) off a field. upstream: pattern.ts extractNth.
func extractNth(conf string, part int) (rest, nth string, err error) {
	rest = conf
	if strings.Contains(rest, "#") {
		if part != partDayOfWeek {
			return "", "", errors.New("CronPattern: nth (#) only allowed in day-of-week field")
		}
		pieces := strings.Split(rest, "#")
		return pieces[0], pieces[1], nil
	}
	if strings.HasSuffix(strings.ToUpper(rest), "L") {
		if part != partDayOfWeek {
			return "", "", errors.New("CronPattern: L modifier only allowed in day-of-week field (use L alone for day-of-month)")
		}
		return rest[:len(rest)-1], "L", nil
	}
	return rest, "", nil
}

func (p *cronPattern) handleNumber(conf string, part, offset, def int) error {
	rest, nth, err := extractNth(conf, part)
	if err != nil {
		return err
	}
	nearest := strings.Contains(strings.ToUpper(conf), "W")
	if part != partDay && nearest {
		return errors.New("CronPattern: Nearest weekday modifier (W) only allowed in day-of-month.")
	}
	if nearest {
		part = partNearestWeekdays
	}
	n, ok := jsParseInt(rest)
	if !ok {
		return fmt.Errorf("CronPattern: %s is not a number: '%s'", partNames[part], conf)
	}
	return p.setPart(part, n+float64(offset), nth, def)
}

func (p *cronPattern) setPart(part int, index float64, nth string, def int) error {
	if part == partDayOfWeek {
		if index == 7 {
			index = 0
		}
		if index < 0 || index > 6 {
			return fmt.Errorf("CronPattern: Invalid value for dayOfWeek: %v", index)
		}
		return p.setNthWeekdayOfMonth(int(index), nth, def)
	}
	limit := float64(len(p.arr(part)))
	if index < 0 || index >= limit {
		return fmt.Errorf("CronPattern: Invalid value for %s: %v", partNames[part], index)
	}
	p.arr(part)[int(index)] = def
	return nil
}

// setNthWeekdayOfMonth: nth is "" (every occurrence), "L", or a digit string 1-5. upstream: pattern.ts setNthWeekdayOfMonth.
func (p *cronPattern) setNthWeekdayOfMonth(index int, nth string, def int) error {
	switch {
	case nth != "" && strings.EqualFold(nth, "L"):
		p.dayOfWeek[index] |= lastOccurrence
	case nth == "" && def == anyOccurrence:
		p.dayOfWeek[index] = anyOccurrence
	default:
		// croner compares the string as a number (`"2" < 6`): digits are a number, anything else is NaN.
		n, ok := 0.0, nth != ""
		for _, c := range nth {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + float64(c-'0')
		}
		if ok && n < 6 && n > 0 {
			p.dayOfWeek[index] |= occurrenceBitmasks[int(n)-1]
			return nil
		}
		return fmt.Errorf("CronPattern: nth weekday out of range, should be 1-5 or L. Value: %s, Type: string", nth)
	}
	return nil
}

func validateRange(lower, upper float64, steps *float64, part int, conf string, size int) error {
	if lower > upper {
		return fmt.Errorf("CronPattern: From value is larger than to value: '%s'", conf)
	}
	if steps != nil {
		if *steps == 0 {
			return errors.New("CronPattern: Syntax error, illegal stepping: 0")
		}
		if *steps > float64(size) {
			return fmt.Errorf("CronPattern: Syntax error, steps cannot be greater than maximum value of part (%d)", size)
		}
	}
	return nil
}

func (p *cronPattern) handleRangeWithStepping(conf string, part, offset, def int) error {
	if strings.Contains(strings.ToUpper(conf), "W") {
		return errors.New("CronPattern: Syntax error, W is not allowed in ranges with stepping.")
	}
	rest, nth, err := extractNth(conf, part)
	if err != nil {
		return err
	}
	m := rangeStepRe.FindStringSubmatch(rest)
	if m == nil {
		return fmt.Errorf("CronPattern: Syntax error, illegal range with stepping: '%s'", conf)
	}
	lo, _ := jsParseInt(m[1])
	hi, _ := jsParseInt(m[2])
	steps, _ := jsParseInt(m[3])
	lo, hi = lo+float64(offset), hi+float64(offset)
	if err := validateRange(lo, hi, &steps, part, conf, len(p.arr(part))); err != nil {
		return err
	}
	for i := lo; i <= hi; i += steps {
		if err := p.setPart(part, i, nth, def); err != nil {
			return err
		}
	}
	return nil
}

func (p *cronPattern) handleRange(conf string, part, offset, def int) error {
	if strings.Contains(strings.ToUpper(conf), "W") {
		return errors.New("CronPattern: Syntax error, W is not allowed in a range.")
	}
	rest, nth, err := extractNth(conf, part)
	if err != nil {
		return err
	}
	split := strings.Split(rest, "-")
	if len(split) != 2 {
		return fmt.Errorf("CronPattern: Syntax error, illegal range: '%s'", conf)
	}
	lo, ok1 := jsParseInt(split[0])
	hi, ok2 := jsParseInt(split[1])
	if !ok1 {
		return errors.New("CronPattern: Syntax error, illegal lower range (NaN)")
	}
	if !ok2 {
		return errors.New("CronPattern: Syntax error, illegal upper range (NaN)")
	}
	lo, hi = lo+float64(offset), hi+float64(offset)
	if err := validateRange(lo, hi, nil, part, conf, len(p.arr(part))); err != nil {
		return err
	}
	for i := lo; i <= hi; i++ {
		if err := p.setPart(part, i, nth, def); err != nil {
			return err
		}
	}
	return nil
}

func (p *cronPattern) handleStepping(conf string, part, _, def int) error {
	if strings.Contains(strings.ToUpper(conf), "W") {
		return errors.New("CronPattern: Syntax error, W is not allowed in parts with stepping.")
	}
	rest, nth, err := extractNth(conf, part)
	if err != nil {
		return err
	}
	split := strings.Split(rest, "/")
	if len(split) != 2 {
		return fmt.Errorf("CronPattern: Syntax error, illegal stepping: '%s'", conf)
	}
	// Only `*/n` (or a range) may step: `0/10` and `/10` are errors unless croner's sloppyRanges is on, and it is off.
	if split[0] == "" {
		return fmt.Errorf("CronPattern: Syntax error, stepping with missing prefix ('%s') is not allowed. Use wildcard (*/step) or range (min-max/step) instead.", conf)
	}
	if split[0] != "*" {
		return fmt.Errorf("CronPattern: Syntax error, stepping with numeric prefix ('%s') is not allowed. Use wildcard (*/step) or range (min-max/step) instead.", conf)
	}
	steps, ok := jsParseInt(split[1])
	if !ok {
		return errors.New("CronPattern: Syntax error, illegal stepping: (NaN)")
	}
	size := len(p.arr(part))
	if err := validateRange(0, float64(size-1), &steps, part, conf, size); err != nil {
		return err
	}
	for i := 0.0; i < float64(size); i += steps {
		if err := p.setPart(part, i, nth, def); err != nil {
			return err
		}
	}
	return nil
}

// ---- CronDate: croner's calendar walker. upstream: date.ts ----

// daysOfMonth is the minimum days of each month (February pinned to 28: "could be out of bounds").
var daysOfMonth = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

const (
	tMonth = iota
	tDay
	tHour
	tMinute
	tSecond
	tYear
)

// recursionSteps: the field to settle, the field to carry into when it cannot be settled, and the pattern index offset.
var recursionSteps = [5]struct{ target, parent, offset int }{
	{tMonth, tYear, 0}, {tDay, tMonth, -1}, {tHour, tDay, 0}, {tMinute, tHour, 0}, {tSecond, tMinute, 0},
}

// cronDate is a calendar time in the process's local zone, field by field, as croner's CronDate holds it.
type cronDate struct {
	ms, second, minute, hour, day, month, year int
	calls                                      int  // nested calls of croner's `recurse` so far
	overflow                                   bool // the search made more than cronStackLimit of them
}

func (d *cronDate) get(t int) int {
	switch t {
	case tMonth:
		return d.month
	case tDay:
		return d.day
	case tHour:
		return d.hour
	case tMinute:
		return d.minute
	case tSecond:
		return d.second
	}
	return d.year
}

func (d *cronDate) set(t, v int) {
	switch t {
	case tMonth:
		d.month = v
	case tDay:
		d.day = v
	case tHour:
		d.hour = v
	case tMinute:
		d.minute = v
	case tSecond:
		d.second = v
	default:
		d.year = v
	}
}

func fromTime(t time.Time) *cronDate {
	t = t.In(time.Local)
	return &cronDate{ms: t.Nanosecond() / 1e6, second: t.Second(), minute: t.Minute(), hour: t.Hour(), day: t.Day(), month: int(t.Month()) - 1, year: t.Year()}
}

func utcDate(y, mo, d, h, mi, s, ms int) time.Time {
	return time.Date(y, time.Month(mo+1), d, h, mi, s, ms*1e6, time.UTC)
}

// apply normalizes the fields when one is, or could be, out of range (date.ts apply, including its quirks: the hour
// is compared with 59, and February is pinned to 28 days so a 29th always goes through the normalization).
func (d *cronDate) apply() bool {
	if d.month > 11 || d.month < 0 || d.day > daysOfMonth[d.month] || d.day < 1 || d.hour > 59 || d.minute > 59 || d.second > 59 ||
		d.hour < 0 || d.minute < 0 || d.second < 0 {
		u := utcDate(d.year, d.month, d.day, d.hour, d.minute, d.second, d.ms)
		d.ms, d.second, d.minute, d.hour, d.day, d.month, d.year = u.Nanosecond()/1e6, u.Second(), u.Minute(), u.Hour(), u.Day(), int(u.Month())-1, u.Year()
		return true
	}
	return false
}

func lastDayOfMonth(year, month int) int {
	if month != 1 {
		return daysOfMonth[month]
	}
	return utcDate(year, 2, 0, 0, 0, 0, 0).Day()
}

func utcWeekday(year, month, day int) int {
	return int(utcDate(year, month, day, 0, 0, 0, 0).Weekday())
}

func lastWeekdayOfMonth(year, month int) int {
	last := lastDayOfMonth(year, month)
	switch utcWeekday(year, month, last) {
	case 0:
		return last - 2
	case 6:
		return last - 1
	}
	return last
}

// nearestWeekday is the weekday (Mon-Fri) nearest to day without leaving the month, or -1 when the month has no such day.
func nearestWeekday(year, month, day int) int {
	in := lastDayOfMonth(year, month)
	if day > in {
		return -1
	}
	switch utcWeekday(year, month, day) {
	case 0:
		if day == in {
			return day - 2
		}
		return day + 1
	case 6:
		if day == 1 {
			return day + 2
		}
		return day - 1
	}
	return day
}

func isNthWeekdayOfMonth(year, month, day, nth int) bool {
	weekday := utcWeekday(year, month, day)
	count := 0
	for d := 1; d <= day; d++ {
		if utcWeekday(year, month, d) == weekday {
			count++
		}
	}
	if nth&anyOccurrence != 0 && count >= 1 && count <= 5 && occurrenceBitmasks[count-1]&nth != 0 {
		return true
	}
	if nth&lastOccurrence != 0 {
		in := lastDayOfMonth(year, month)
		for d := day + 1; d <= in; d++ {
			if utcWeekday(year, month, d) == weekday {
				return false
			}
		}
		return true
	}
	return false
}

// findNext settles one field at the first matching value at or after the current one: 1 the same value, 2 a later one,
// 3 none. upstream: date.ts _findMatch (forward).
func (d *cronDate) findNext(p *cronPattern, target, offset int) int {
	original := d.get(target)
	last := 0
	if p.lastDayOfMonth {
		last = lastDayOfMonth(d.year, d.month)
	}
	firstWeekday := 0
	if !p.starDOW && target == tDay {
		firstWeekday = utcWeekday(d.year, d.month, 1)
	}
	var arr []int
	switch target {
	case tMonth:
		arr = p.month
	case tDay:
		arr = p.day
	case tHour:
		arr = p.hour
	case tMinute:
		arr = p.minute
	default:
		arr = p.second
	}
	for i := d.get(target) + offset; i < len(arr); i++ {
		match := i >= 0 && arr[i] != 0
		if target == tDay && !match {
			for w := range p.nearestWeekdays {
				if p.nearestWeekdays[w] == 0 {
					continue
				}
				exec := nearestWeekday(d.year, d.month, w-offset)
				if exec == -1 {
					continue
				}
				if exec == i-offset {
					match = true
					break
				}
			}
		}
		if target == tDay && p.lastWeekday && i-offset == lastWeekdayOfMonth(d.year, d.month) {
			match = true
		}
		if target == tDay && p.lastDayOfMonth && i-offset == last {
			match = true
		}
		if target == tDay && !p.starDOW {
			dow := 0 // an index below zero is `undefined` in croner: no weekday matches
			if at := firstWeekday + ((i - offset) - 1); at >= 0 {
				dow = p.dayOfWeek[at%7]
			}
			if dow != 0 && dow&anyOccurrence != 0 {
				if !isNthWeekdayOfMonth(d.year, d.month, i-offset, dow) {
					dow = 0
				} else {
					dow = 1
				}
			}
			switch {
			case p.useAndLogic:
				match = match && dow != 0
			case !p.starDOM:
				match = match || dow != 0 // croner's default (domAndDow off): day-of-month OR day-of-week
			default:
				match = match && dow != 0
			}
		}
		if match {
			d.set(target, i-offset)
			if original != d.get(target) {
				return 2
			}
			return 1
		}
	}
	return 3
}

// recurse settles month, day, hour, minute and second in turn, carrying into the parent when a field has no match and
// searching again. It reports false when the search runs past the year limit. upstream: date.ts recurse.
func (d *cronDate) recurse(p *cronPattern) bool {
	doing := 0
	for {
		d.calls++ // each pass is one call of croner's recursive `recurse`
		if d.calls > cronStackLimit {
			d.overflow = true
			return false
		}
		step := recursionSteps[doing]
		res := d.findNext(p, step.target, step.offset)
		if res > 1 {
			for reset := doing + 1; reset < len(recursionSteps); reset++ {
				d.set(recursionSteps[reset].target, -recursionSteps[reset].offset)
			}
			if res == 3 {
				d.set(step.parent, d.get(step.parent)+1)
				d.set(step.target, -step.offset)
				d.apply()
				doing = 0
				continue
			}
			if d.apply() && doing > 0 {
				doing--
				continue
			}
		}
		doing++
		if doing >= len(recursionSteps) {
			return true
		}
		if d.year >= cronYearLimit {
			return false
		}
	}
}

// increment moves to the next matching second after the current one. upstream: date.ts increment (no interval option).
func (d *cronDate) increment(p *cronPattern) bool {
	d.second++
	d.ms = 0
	d.apply()
	return d.recurse(p)
}

// jsLocalTime is `new Date(y, m, d, h, mi, s, ms)` in the local zone: fields out of range carry, a wall-clock time that
// does not exist (a spring-forward gap) is read with the offset before the transition, and one that happens twice
// (a fall-back overlap) is its first occurrence, as ECMAScript says.
func jsLocalTime(y, mo, d, h, mi, s, ms int) time.Time {
	u := utcDate(y, mo, d, h, mi, s, ms)
	wall := u.Unix() // the wall-clock time read as if it were UTC
	offsetAt := func(unix int64) int {
		_, off := time.Unix(unix, 0).In(time.Local).Zone()
		return off
	}
	var candidates []int
	for _, shift := range []int64{-86400, -43200, 0, 43200, 86400} {
		off := offsetAt(wall + shift)
		seen := false
		for _, c := range candidates {
			seen = seen || c == off
		}
		if !seen {
			candidates = append(candidates, off)
		}
	}
	chosen, found := 0, false
	for _, off := range candidates {
		if offsetAt(wall-int64(off)) == off && (!found || off > chosen) {
			chosen, found = off, true
		}
	}
	if !found { // a gap: no offset is consistent with the wall time; the one before the transition is the smaller
		chosen = slices.Min(candidates)
	}
	return time.Unix(wall-int64(chosen), int64(u.Nanosecond()))
}

func (d *cronDate) time() time.Time {
	return jsLocalTime(d.year, d.month, d.day, d.hour, d.minute, d.second, d.ms)
}

// cron is a parsed expression. upstream: croner Cron (the part src/schedule.ts uses).
type cron struct{ pattern *cronPattern }

// newCron is `new Cron(expr, fn)` as it stands now: see newCronAt.
func newCron(expr string) (*cron, error) { return newCronAt(expr, time.Now()) }

// newCronAt is `new Cron(expr, fn)` at the instant now. croner's constructor arms the job at once, which searches for
// the first run, so a pattern whose search overflows croner's stack is refused here as it is there.
func newCronAt(expr string, now time.Time) (*cron, error) {
	// croner reads a pattern with a `:` after its first character as a date, parsed by V8's Date.parse. The
	// original's own check (six fields) lets a few such strings through ("Tue Jan 01 2030 10:00:00 GMT+0100" is six
	// fields), and Go has no Date.parse: they are refused here, like croner refuses the ones Date.parse rejects.
	if strings.Index(expr, ":") > 0 {
		return nil, errors.New("Invalid ISO8601 passed to timezone parser.")
	}
	p, err := parseCronPattern(expr)
	if err != nil {
		return nil, err
	}
	c := &cron{pattern: p}
	if _, _, overflow := c.nextRun(now); overflow {
		return nil, errors.New("Maximum call stack size exceeded")
	}
	return c, nil
}

// next is the first run after `after` (strictly: the next whole second), or false when there is none before the year
// 3000. upstream: croner Cron.nextRun(prev).
func (c *cron) next(after time.Time) (time.Time, bool) {
	t, ok, _ := c.nextRun(after)
	return t, ok
}

// nextRun is next, also reporting that croner's search would have overflowed the stack (and so found nothing).
func (c *cron) nextRun(after time.Time) (t time.Time, ok, overflow bool) {
	d := fromTime(after)
	if !d.increment(c.pattern) {
		return time.Time{}, false, d.overflow
	}
	return d.time(), true, false
}
