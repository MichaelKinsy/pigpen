package croner

import (
	"errors"
	"math"
	"unicode/utf16"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

// CronDate is croner's CronDate in the process's local time zone (the only one the original uses): the wall-clock fields of a date,
// moved to the next matching second by Increment.
type CronDate struct {
	f [7]int // year, month (0-11), day (1-31), hour, minute, second, ms
}

const (
	fYear = iota
	fMonth
	fDay
	fHour
	fMinute
	fSecond
	fMs
)

var daysOfMonth = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// MaxRecurseDepth stands for V8's stack limit: croner's recursion is one JavaScript frame per step, and a pattern that needs more
// than the stack holds (one that matches no date, in every month of every year up to 3000) throws a RangeError. Measured against Node 24
// (V8 13.6) over 46000 generated patterns: every pattern needing 6824 steps or fewer was answered, every one needing 7773 or more
// overflowed; nothing was seen in between. The limit is not exact (it depends on the frames below it and on JIT tiering).
const MaxRecurseDepth = 7300

// DepthHook (tests) receives the number of nested recurse calls croner's recursion makes for one Increment.
var DepthHook func(depth int)

type recursionStep struct {
	target, parent int
	offset         int
}

var recursionSteps = [5]recursionStep{
	{fMonth, fYear, 0},
	{fDay, fMonth, -1},
	{fHour, fDay, 0},
	{fMinute, fHour, 0},
	{fSecond, fMinute, 0},
}

func fromMs(t float64) (*CronDate, error) {
	if math.IsNaN(t) {
		return nil, errors.New("CronDate: Invalid date passed to CronDate constructor")
	}
	fl := jsdate.LocalFields(t)
	return &CronDate{f: [7]int{int(fl.Year), int(fl.Month), int(fl.Day), int(fl.Hour), int(fl.Minute), int(fl.Second), int(fl.Ms)}}, nil
}

// NowDate is `new CronDate()`.
func NowDate() *CronDate {
	d, _ := fromMs(jsdate.Now())
	return d
}

func (d *CronDate) clone() *CronDate { c := *d; return &c }

// timePoint is the helpers/timezone.ts TimePoint of a zone that is either the local one or Etc/UTC.
type timePoint struct {
	y, m, d, h, i, s int64
	utc              bool
}

func (tp timePoint) ms() float64 { return jsdate.UTCDate(tp.y, tp.m-1, tp.d, tp.h, tp.i, tp.s, 0) }

func (tp timePoint) same(o timePoint) bool {
	return tp.y == o.y && tp.m == o.m && tp.d == o.d && tp.h == o.h && tp.i == o.i && tp.s == o.s
}

func tzName(utc bool) string {
	if utc {
		return "Etc/UTC"
	}
	return "undefined"
}

func toTZ(t float64, utc bool) (timePoint, error) {
	if math.IsNaN(t) {
		return timePoint{}, errors.New("toTZ: Invalid timezone '" + tzName(utc) + "' or date. Please provide a valid IANA timezone (e.g., 'America/New_York', 'Europe/Stockholm'). Original error: Invalid time value")
	}
	var f jsdate.Fields
	if utc {
		f = jsdate.UTCFields(t)
	} else {
		f = jsdate.LocalFields(t)
	}
	return timePoint{y: f.Year, m: f.Month + 1, d: f.Day, h: f.Hour, i: f.Minute, s: f.Second, utc: utc}, nil
}

// fromTZ is helpers/timezone.ts fromTZ with throwOnInvalid false.
func fromTZ(tp timePoint) (float64, error) {
	inDate := tp.ms()
	check0, err := toTZ(inDate, tp.utc)
	if err != nil {
		return 0, err
	}
	diff := tp.ms() - check0.ms()
	guess := inDate + diff
	check1, err := toTZ(guess, tp.utc)
	if err != nil {
		return 0, err
	}
	if check1.same(tp) {
		alt := guess - 3600000
		altCheck, err := toTZ(alt, tp.utc)
		if err != nil {
			return 0, err
		}
		if altCheck.same(tp) {
			return alt, nil
		}
		return guess, nil
	}
	guess2 := guess + tp.ms() - check1.ms()
	check2, err := toTZ(guess2, tp.utc)
	if err != nil {
		return 0, err
	}
	if check2.same(tp) {
		return guess2, nil
	}
	if guess > guess2 {
		return guess, nil
	}
	return guess2, nil
}

// fromString is CronDate.fromString with no timezone: fromTZISO(str) through `Date.parse`.
func fromString(s string) (*CronDate, error) {
	pd := jsdate.Parse(s)
	if math.IsNaN(pd) {
		return nil, errors.New("Invalid ISO8601 passed to timezone parser.")
	}
	units := utf16.Encode([]rune(s))
	end := ""
	if len(units) > 9 {
		end = string(utf16.Decode(units[9:]))
	}
	utc := false
	for _, r := range end {
		if r == 'Z' || r == '+' || r == '-' {
			utc = true
		}
	}
	var fl jsdate.Fields
	if utc {
		fl = jsdate.UTCFields(pd)
	} else {
		fl = jsdate.LocalFields(pd)
	}
	t, err := fromTZ(timePoint{y: fl.Year, m: fl.Month + 1, d: fl.Day, h: fl.Hour, i: fl.Minute, s: fl.Second, utc: utc})
	if err != nil {
		return nil, err
	}
	return fromMs(t)
}

// apply normalizes fields that left their range (or could have: February's 29th) through Date.UTC; it reports whether it did.
func (d *CronDate) apply() bool {
	f := &d.f
	if f[fMonth] > 11 || f[fMonth] < 0 || f[fDay] > daysOfMonth[clampMonth(f[fMonth])] || f[fDay] < 1 ||
		f[fHour] > 59 || f[fMinute] > 59 || f[fSecond] > 59 || f[fHour] < 0 || f[fMinute] < 0 || f[fSecond] < 0 {
		t := jsdate.UTCDate(int64(f[fYear]), int64(f[fMonth]), int64(f[fDay]), int64(f[fHour]), int64(f[fMinute]), int64(f[fSecond]), int64(f[fMs]))
		u := jsdate.UTCFields(t)
		*f = [7]int{int(u.Year), int(u.Month), int(u.Day), int(u.Hour), int(u.Minute), int(u.Second), int(u.Ms)}
		return true
	}
	return false
}

// clampMonth: DaysOfMonth[this.month] is undefined for a month outside 0..11, and `day > undefined` is false.
func clampMonth(m int) int {
	if m < 0 || m > 11 {
		return 1 // unreachable in the condition: the month test already decided
	}
	return m
}

func lastDayOfMonth(year, month int) int {
	if month != 1 {
		return daysOfMonth[month]
	}
	return int(jsdate.UTCFields(jsdate.UTCDate(int64(year), int64(month)+1, 0, 0, 0, 0, 0)).Day)
}

func utcWeekday(year, month, day int) int {
	return int(jsdate.UTCFields(jsdate.UTCDate(int64(year), int64(month), int64(day), 0, 0, 0, 0)).Weekday)
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

func nearestWeekday(year, month, day int) int {
	dim := lastDayOfMonth(year, month)
	if day > dim {
		return -1
	}
	switch utcWeekday(year, month, day) {
	case 0:
		if day == dim {
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
	for dd := 1; dd <= day; dd++ {
		if utcWeekday(year, month, dd) == weekday {
			count++
		}
	}
	if nth&anyOccurrence != 0 && count-1 >= 0 && count-1 < len(occurrenceBitmasks) && occurrenceBitmasks[count-1]&nth != 0 {
		return true
	}
	if nth&lastOccurrence != 0 {
		dim := lastDayOfMonth(year, month)
		for dd := day + 1; dd <= dim; dd++ {
			if utcWeekday(year, month, dd) == weekday {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Pattern) slotFor(target int) []int {
	switch target {
	case fMonth:
		return p.month[:]
	case fDay:
		return p.day[:]
	case fHour:
		return p.hour[:]
	case fMinute:
		return p.minute[:]
	}
	return p.second[:]
}

// findNext is _findMatch forward: 1 the value matches as it is, 2 it changed, 3 no match.
func (d *CronDate) findNext(p *Pattern, target int, offset int) (int, error) {
	original := d.f[target]
	lastDay := 0
	if p.lastDayOfMonth {
		lastDay = lastDayOfMonth(d.f[fYear], d.f[fMonth])
	}
	fDomWeekDay := 0
	if !p.starDOW && target == fDay {
		fDomWeekDay = utcWeekday(d.f[fYear], d.f[fMonth], 1)
	}
	arr := p.slotFor(target)
	for i := d.f[target] + offset; i < len(arr); i++ {
		match := arr[i] != 0
		if target == fDay && !match {
			for dayWithW := 0; dayWithW < len(p.nearestWeekdays); dayWithW++ {
				if p.nearestWeekdays[dayWithW] != 0 {
					exec := nearestWeekday(d.f[fYear], d.f[fMonth], dayWithW-offset)
					if exec == -1 {
						continue
					}
					if exec == i-offset {
						match = true
						break
					}
				}
			}
		}
		if target == fDay && p.lastWeekday {
			if i-offset == lastWeekdayOfMonth(d.f[fYear], d.f[fMonth]) {
				match = true
			}
		}
		if target == fDay && p.lastDayOfMonth && i-offset == lastDay {
			match = true
		}
		if target == fDay && !p.starDOW {
			dowMatch := p.dayOfWeek[mod7(fDomWeekDay+((i-offset)-1))]
			if dowMatch != 0 && dowMatch&anyOccurrence != 0 {
				if isNthWeekdayOfMonth(d.f[fYear], d.f[fMonth], i-offset, dowMatch) {
					dowMatch = 1
				} else {
					dowMatch = 0
				}
			} else if dowMatch != 0 {
				return 0, errors.New("CronDate: Invalid value for dayOfWeek encountered. " + jsNum(float64(dowMatch)))
			}
			switch {
			case p.useAndLogic:
				match = match && dowMatch != 0
			case !p.starDOM:
				match = match || dowMatch != 0
			default:
				match = match && dowMatch != 0
			}
		}
		if match {
			d.f[target] = i - offset
			if original != d.f[target] {
				return 2, nil
			}
			return 1, nil
		}
	}
	return 3, nil
}

func mod7(n int) int {
	m := n % 7
	if m < 0 {
		m += 7
	}
	return m
}

// recurse moves the date to the next match (RecursionSteps), nil when none is found before the year 3000.
func (d *CronDate) recurse(p *Pattern, doing int) (*CronDate, error) {
	depth := 0
	if DepthHook != nil {
		defer func() { DepthHook(depth) }()
	}
	for {
		depth++
		if depth > MaxRecurseDepth {
			return nil, errors.New("Maximum call stack size exceeded")
		}
		step := recursionSteps[doing]
		res, err := d.findNext(p, step.target, step.offset)
		if err != nil {
			return nil, err
		}
		if res > 1 {
			for reset := doing + 1; reset < len(recursionSteps); reset++ {
				d.f[recursionSteps[reset].target] = -recursionSteps[reset].offset
			}
			if res == 3 {
				d.f[step.parent]++
				d.f[step.target] = -step.offset
				d.apply()
				doing = 0
				continue
			} else if d.apply() {
				if doing == 0 {
					return nil, errors.New("Cannot read properties of undefined (reading '0')")
				}
				doing--
				continue
			}
		}
		doing++
		if doing >= len(recursionSteps) {
			return d, nil
		} else if d.f[fYear] >= 3000 {
			return nil, nil
		}
	}
}

// Increment moves to the next second and then to the next match; nil when there is none.
func (d *CronDate) Increment(p *Pattern) (*CronDate, error) {
	d.f[fSecond]++
	d.f[fMs] = 0
	d.apply()
	return d.recurse(p, 0)
}

// Date is getDate(): the instant of the wall-clock fields.
func (d *CronDate) Date() float64 {
	f := d.f
	return jsdate.LocalDate(int64(f[fYear]), int64(f[fMonth]), int64(f[fDay]), int64(f[fHour]), int64(f[fMinute]), int64(f[fSecond]), int64(f[fMs]))
}
