package schedule_prompt

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/croner"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

// Schedule parsing and wording (upstream: scheduler.ts, the static methods and the helpers at the end of the file).

var (
	relativeRe = regexp.MustCompile(`^\+([0-9]+)(s|m|h|d)$`)
	intervalRe = regexp.MustCompile(`^([0-9]+)(s|m|h|d)$`)
)

var unitMs = map[string]float64{"s": 1000, "m": 60 * 1000, "h": 60 * 60 * 1000, "d": 24 * 60 * 60 * 1000}

// validateCronExpression: a cron expression must have six fields; croner then validates (its message is returned as it is).
func validateCronExpression(expression string) (valid bool, errText string) {
	trimmed := croner.JSTrim(expression)
	n := len(croner.JSFields(trimmed))
	if trimmed == "" {
		n = 1 // "".split(/\s+/) is [""]
	}
	if n != 6 {
		return false, "Cron expression must have 6 fields (second minute hour dom month dow), got " + strconv.Itoa(n) + `. Example: "0 * * * * *" for every minute`
	}
	if err := croner.Validate(expression); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// parseRelativeTime resolves "+10s" to an ISO timestamp. ok is false when the text is not a relative time; err is the RangeError
// toISOString throws for a time beyond JavaScript's range.
func parseRelativeTime(delta string, now float64) (iso string, ok bool, err error) {
	m := relativeRe.FindStringSubmatch(delta)
	if m == nil {
		return "", false, nil
	}
	value, _ := strconv.ParseFloat(m[1], 64) // out of range: +Inf, as parseInt gives Infinity
	t := jsdate.TimeClip(now + value*unitMs[m[2]])
	s, valid := isoString(t)
	if !valid {
		return "", true, errors.New("Invalid time value")
	}
	return s, true, nil
}

// parseInterval resolves "5m" to milliseconds; ok is false for anything else.
func parseInterval(interval string) (ms float64, ok bool) {
	m := intervalRe.FindStringSubmatch(interval)
	if m == nil {
		return 0, false
	}
	value, _ := strconv.ParseFloat(m[1], 64)
	return value * unitMs[m[2]], true
}

// validated is ValidateScheduleResult.
type validated struct {
	ok         bool
	schedule   string
	intervalMs *float64
	err        string
}

func fail(format string, parts ...string) validated {
	var b strings.Builder
	i := 0
	for _, r := range format {
		if r == '%' {
			b.WriteString(parts[i])
			i++
			continue
		}
		b.WriteRune(r)
	}
	return validated{err: b.String()}
}

// validateSchedule validates and resolves a schedule string for the job type, the single source of truth of the tool's add and update
// and of the add flow. The error return is a thrown exception (not a validation failure).
func validateSchedule(typ, schedule string) (validated, error) {
	return validateScheduleAt(typ, schedule, jsdate.Now())
}

func validateScheduleAt(typ, schedule string, now float64) (validated, error) {
	if typ == "interval" {
		ms, ok := parseInterval(schedule)
		if !ok || ms == 0 {
			return fail("Invalid interval format: %. Use format like '5m', '1h', '30s'", schedule), nil
		}
		return validated{ok: true, schedule: schedule, intervalMs: &ms}, nil
	}
	if typ == "once" {
		rel, ok, err := parseRelativeTime(schedule, now)
		if err != nil {
			return validated{}, err
		}
		if ok {
			return validated{ok: true, schedule: rel}, nil
		}
		date := jsdate.Parse(schedule)
		if math.IsNaN(date) {
			return fail("Invalid timestamp: %. Use ISO format or relative time like '+10s', '+5m'", schedule), nil
		}
		delay := date - now
		iso, _ := isoString(date)
		if delay < 0 {
			nowISO, _ := isoString(now)
			return fail("Timestamp is in the past: %. Current time: %", iso, nowISO), nil
		}
		if delay < 5000 {
			return validated{err: "Timestamp is too soon (" + jsNumber(math.Floor(delay/1000+0.5)) + "s). For delays under 5s, use relative time like '+" +
				jsNumber(math.Ceil(delay/1000)) + "s' instead, or schedule at least 5s in the future."}, nil
		}
		return validated{ok: true, schedule: iso}, nil
	}
	valid, text := validateCronExpression(schedule)
	if !valid {
		return fail("Invalid cron expression: %", text), nil
	}
	return validated{ok: true, schedule: schedule}, nil
}

// describeSchedule renders a resolved schedule as a short human-readable phrase.
func describeSchedule(typ, schedule string) string {
	if typ == "interval" {
		return "every " + schedule
	}
	if typ == "once" {
		d := jsdate.Parse(schedule)
		if math.IsNaN(d) {
			return schedule
		}
		return formatISOShortMs(d)
	}
	return humanizeCron(schedule)
}

var humanizedCron = map[string]string{
	"* * * * * *":    "every second",
	"0 * * * * *":    "every minute",
	"0 */5 * * * *":  "every 5 min",
	"0 */10 * * * *": "every 10 min",
	"0 */15 * * * *": "every 15 min",
	"0 */30 * * * *": "every 30 min",
	"0 0 * * * *":    "every hour",
	"0 0 */2 * * *":  "every 2 hours",
	"0 0 */3 * * *":  "every 3 hours",
	"0 0 */6 * * *":  "every 6 hours",
	"0 0 0 * * *":    "daily",
	"0 0 0 * * 0":    "weekly",
	"0 0 0 1 * *":    "monthly",
	"0 0 9 * * 1-5":  "9am weekdays",
	"0 0 0 * * 1-5":  "weekdays",
	"0 0 0 * * 0,6":  "weekends",
}

var (
	everyMinRe  = regexp.MustCompile(`^0 \*/([0-9]+) \* \* \* \*$`)
	everyHourRe = regexp.MustCompile(`^0 0 \*/([0-9]+) \* \* \*$`)
	dailyAtRe   = regexp.MustCompile(`^0 0 ([0-9]+) \* \* \*$`)
)

// humanizeCron is the human-readable form of a 6-field cron expression for common patterns; anything else comes back as it is.
func humanizeCron(expression string) string {
	normalized := croner.JSTrim(expression)
	if s, ok := humanizedCron[normalized]; ok {
		return s
	}
	if m := everyMinRe.FindStringSubmatch(normalized); m != nil {
		return "every " + m[1] + " min"
	}
	if m := everyHourRe.FindStringSubmatch(normalized); m != nil {
		return "every " + m[1] + "h"
	}
	if m := dailyAtRe.FindStringSubmatch(normalized); m != nil {
		f, _ := strconv.ParseFloat(m[1], 64)
		return "daily at " + jsNumber(f) + ":00"
	}
	return normalized
}

var monthNames = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func formatISOShortMs(ms float64) string {
	f := jsdate.LocalFields(ms)
	return monthNames[f.Month] + " " + strconv.FormatInt(f.Day, 10) + " " + pad2(f.Hour) + ":" + pad2(f.Minute)
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// formatISOShort renders a timestamp as "Feb 13 15:30"; text that does not parse as a date comes back as it is.
func formatISOShort(input string) string {
	d := jsdate.Parse(input)
	if math.IsNaN(d) {
		return input
	}
	return formatISOShortMs(d)
}
