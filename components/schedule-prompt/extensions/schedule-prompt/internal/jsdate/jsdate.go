// Package jsdate is the part of JavaScript's Date that pi-schedule-prompt and croner use, with V8's behavior: `new Date(string)`
// (V8's date parser, ES5 ISO strings and the legacy formats), the local-time constructor and getters with the ES
// disambiguation of skipped and repeated local times, and toISOString. A Date is a float64 of milliseconds since the epoch, NaN
// for an Invalid Date, as in JavaScript.
//
// upstream: V8 src/date/dateparser.cc, dateparser-inl.h, dateparser.h (V8 13.6, the Node 24 engine), BSD-3-Clause, Copyright 2011 the
// V8 project authors; the ECMAScript Date algorithms (MakeDay, MakeTime, TimeClip, UTC(t)).
package jsdate

import (
	"math"
	"time"
)

const (
	msPerDay    = 86400000
	maxTimeMs   = 8.64e15
	minYear     = -1000000 // V8 MakeDay's kMinYear / kMaxYear
	maxYear     = 1000000
	minMonth    = -10000000
	maxMonth    = 10000000
	nanDay      = math.MaxInt64
	secondsInMs = 1000
)

var loc = time.Local

// SetLocation sets the local time zone (tests; the default is the process's, as Node's).
func SetLocation(l *time.Location) { loc = l }

// Location is the local time zone.
func Location() *time.Location { return loc }

// Now is Date.now().
func Now() float64 { return float64(time.Now().UnixMilli()) }

func nan() float64 { return math.NaN() }

// IsNaN reports an Invalid Date.
func IsNaN(t float64) bool { return math.IsNaN(t) }

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int64) int64 { return a - floorDiv(a, b)*b }

// daysFromCivil is the day number (0 = 1970-01-01) of a proleptic Gregorian date, month 1..12.
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the inverse of daysFromCivil.
func civilFromDays(z int64) (y, m, d int64) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	m = mp + 3
	if m > 12 {
		m -= 12
	}
	if m <= 2 {
		y++
	}
	return
}

// MakeDay is the ES abstract operation (month 0-based); the result is a day number or ok=false for NaN (V8's bounds).
func MakeDay(year, month, date int64) (days int64, ok bool) {
	if year < minYear || year > maxYear || month < minMonth || month > maxMonth {
		return 0, false
	}
	ym := year + floorDiv(month, 12)
	mn := floorMod(month, 12)
	return daysFromCivil(ym, mn+1, 1) + date - 1, true
}

// MakeTime is the ES abstract operation over integers.
func MakeTime(h, m, s, ms int64) int64 { return h*3600000 + m*60000 + s*1000 + ms }

// TimeClip is the ES abstract operation.
func TimeClip(t float64) float64 {
	if math.IsNaN(t) || math.Abs(t) > maxTimeMs {
		return nan()
	}
	return math.Trunc(t) + 0 // +0 turns -0 into +0
}

// UTCDate is Date.UTC(year, month, day, hour, minute, second, ms) with integer arguments: years 0..99 mean 1900..1999.
func UTCDate(year, month, day, hour, minute, second, ms int64) float64 {
	if year >= 0 && year <= 99 {
		year += 1900
	}
	days, ok := MakeDay(year, month, day)
	if !ok {
		return nan()
	}
	return TimeClip(float64(days*msPerDay + MakeTime(hour, minute, second, ms)))
}

// offsetAt is the local offset from UTC in ms at the UTC instant t.
func offsetAt(t int64) int64 {
	_, off := time.UnixMilli(t).In(loc).Zone()
	return int64(off) * 1000
}

// localToUTC is ES UTC(t) with V8's (ICU's "former") disambiguation: a skipped local time is read with the offset before the
// transition, a repeated one is the first occurrence.
func localToUTC(local int64) int64 {
	offBefore := offsetAt(local - msPerDay)
	offAfter := offsetAt(local + msPerDay)
	candBefore := local - offBefore
	candAfter := local - offAfter
	validBefore := offsetAt(candBefore) == offBefore
	validAfter := offsetAt(candAfter) == offAfter
	switch {
	case validBefore && validAfter:
		if candBefore < candAfter {
			return candBefore
		}
		return candAfter
	case validBefore:
		return candBefore
	case validAfter:
		return candAfter
	}
	return candBefore
}

// LocalDate is `new Date(year, month, day, hour, minute, second, ms)` with integer arguments: years 0..99 mean 1900..1999.
func LocalDate(year, month, day, hour, minute, second, ms int64) float64 {
	if year >= 0 && year <= 99 {
		year += 1900
	}
	days, ok := MakeDay(year, month, day)
	if !ok {
		return nan()
	}
	local := days*msPerDay + MakeTime(hour, minute, second, ms)
	if math.Abs(float64(local)) > maxTimeMs+msPerDay {
		return nan()
	}
	return TimeClip(float64(localToUTC(local)))
}

// Fields are the local-time components of a Date.
type Fields struct {
	Year, Month, Day, Hour, Minute, Second, Ms, Weekday int64 // Month is 0-based, Weekday 0 = Sunday
}

// LocalFields are getFullYear() ... getMilliseconds() and getDay() of a valid date.
func LocalFields(t float64) Fields {
	ms := int64(t)
	local := ms + offsetAt(ms)
	days := floorDiv(local, msPerDay)
	rem := local - days*msPerDay
	y, m, d := civilFromDays(days)
	return Fields{
		Year: y, Month: m - 1, Day: d,
		Hour: rem / 3600000, Minute: rem / 60000 % 60, Second: rem / 1000 % 60, Ms: rem % 1000,
		Weekday: floorMod(days+4, 7),
	}
}

// UTCFields are the getUTC* components of a valid date.
func UTCFields(t float64) Fields {
	ms := int64(t)
	days := floorDiv(ms, msPerDay)
	rem := ms - days*msPerDay
	y, m, d := civilFromDays(days)
	return Fields{
		Year: y, Month: m - 1, Day: d,
		Hour: rem / 3600000, Minute: rem / 60000 % 60, Second: rem / 1000 % 60, Ms: rem % 1000,
		Weekday: floorMod(days+4, 7),
	}
}

// ToISOString is Date.prototype.toISOString; ok=false is the RangeError an Invalid Date throws.
func ToISOString(t float64) (string, bool) {
	if math.IsNaN(t) {
		return "", false
	}
	f := UTCFields(t)
	var year string
	switch {
	case f.Year >= 0 && f.Year <= 9999:
		year = pad(f.Year, 4)
	case f.Year < 0:
		year = "-" + pad(-f.Year, 6)
	default:
		year = "+" + pad(f.Year, 6)
	}
	return year + "-" + pad(f.Month+1, 2) + "-" + pad(f.Day, 2) + "T" + pad(f.Hour, 2) + ":" + pad(f.Minute, 2) + ":" + pad(f.Second, 2) + "." + pad(f.Ms, 3) + "Z", true
}

func pad(n int64, width int) string {
	s := itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
