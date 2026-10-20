package pi_subagents

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// The argument validation of the schedule actions (src/runs/background/scheduled-runs.ts), which the original answers with its own
// messages before it stores or runs anything. The port does not run schedules: a call that passes this is refused by refuse.go.

var (
	scheduleIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	scheduleRelativeAt = regexp.MustCompile(`^\+(\d+)(s|m|h|d)$`)
	scheduleISOAt      = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})$`)
	scheduleInterval   = regexp.MustCompile(`^(\d+)(m|h|d|w)$`)
)

const scheduleIDMessage = "Schedule id must be 1-64 characters and contain only letters, numbers, '.', '_', or '-'."

// parseScheduledRunTime is parseScheduledRunTime: the error message of an invalid or past `at` (empty if it is valid).
func parseScheduledRunTime(at string, now time.Time) string {
	trimmed := jsTrim(at)
	if m := scheduleRelativeAt.FindStringSubmatch(trimmed); m != nil {
		amount, err := strconv.ParseFloat(m[1], 64)
		if err != nil || amount != math.Trunc(amount) || amount > 9007199254740991 || amount < 1 {
			return fmt.Sprintf("Invalid at value \"%s\". Relative delays must be positive, such as \"+10m\".", at)
		}
		unit := map[string]float64{"s": 1_000, "m": 60_000, "h": 3_600_000, "d": 86_400_000}[m[2]]
		if float64(now.UnixMilli())+amount*unit > 9007199254740991 {
			return fmt.Sprintf("Invalid at value \"%s\". Relative delay is too large.", at)
		}
		return ""
	}
	m := scheduleISOAt.FindStringSubmatch(trimmed)
	if m == nil {
		return fmt.Sprintf("Invalid at value \"%s\". Use a one-shot delay such as \"+10m\" or an ISO timestamp with timezone.", at)
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	year, month, day, hour, minute := num(m[1]), num(m[2]), num(m[3]), num(m[4]), num(m[5])
	second := 0
	if m[6] != "" {
		second = num(m[6])
	}
	offsetHour, offsetMinute := 0, 0
	if m[7] != "Z" {
		offsetHour, offsetMinute = num(m[7][1:3]), num(m[7][4:6])
	}
	daysInMonth := 0
	if month >= 1 && month <= 12 {
		daysInMonth = time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	if month < 1 || month > 12 || day < 1 || day > daysInMonth || hour > 23 || minute > 59 || second > 59 || offsetHour > 23 || offsetMinute > 59 {
		return fmt.Sprintf("Invalid at value \"%s\". Use a valid ISO timestamp.", at)
	}
	layout := "2006-01-02T15:04:05.999999999Z07:00"
	if m[6] == "" {
		layout = "2006-01-02T15:04Z07:00"
	}
	parsed, err := time.Parse(layout, trimmed)
	if err != nil {
		return fmt.Sprintf("Invalid at value \"%s\". Use a valid ISO timestamp.", at)
	}
	if !parsed.After(now) {
		return fmt.Sprintf("Scheduled time %s is in the past.", parsed.UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	return ""
}

// parseScheduleInterval is parseScheduleInterval: the error message of an invalid `every` (empty if it is valid).
func parseScheduleInterval(every string) string {
	m := scheduleInterval.FindStringSubmatch(jsTrim(every))
	if m == nil {
		return fmt.Sprintf("Invalid every value \"%s\". This first recurring slice supports fixed intervals such as \"30m\", \"6h\", \"2d\", or \"2w\".", every)
	}
	amount, err := strconv.ParseFloat(m[1], 64)
	if err != nil || amount != math.Trunc(amount) || amount > 9007199254740991 || amount < 1 {
		return fmt.Sprintf("Invalid every value \"%s\". Interval must be positive.", every)
	}
	unit := map[string]float64{"m": 60_000, "h": 3_600_000, "d": 86_400_000, "w": 604_800_000}[m[2]]
	if amount*unit > 9007199254740991 {
		return fmt.Sprintf("Invalid every value \"%s\". Interval is too large.", every)
	}
	return ""
}

// scheduleArgumentError is the message the original's schedule action answers for a malformed call, in its order of checks ("" when
// the arguments are valid).
func scheduleArgumentError(action string, params map[string]any, now time.Time) string {
	str := func(k string) string { s, _ := params[k].(string); return jsTrim(s) }
	switch action {
	case "schedule.create":
		at, every := str("at"), str("every")
		if (at != "") == (every != "") {
			return "schedule.create requires exactly one trigger: at or every."
		}
		if v, ok := params["overlap"]; ok && v != nil && v != "skip" {
			return "This first recurring slice supports overlap='skip' only."
		}
		if v, ok := params["catchUp"]; ok && v != nil && v != "none" && v != "latest" {
			return "catchUp must be 'none' or 'latest'."
		}
		for _, k := range []string{"missionId", "mission", "missionUpdate", "missionStatus", "missionScope"} {
			if defined(params, k) {
				return "Mission attachment is deferred from this first schedule slice."
			}
		}
		if defined(params, "on") || defined(params, "timezone") || every == "day" || every == "week" || every == "month" || every == "year" {
			return "Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."
		}
		if v, ok := params["quiet"]; ok && v != nil {
			quiet, isBool := v.(bool)
			if !isBool {
				return "quiet must be a boolean."
			}
			if at != "" && quiet {
				return "quiet is only supported for recurring schedules."
			}
		}
		if id := str("id"); id != "" && !scheduleIDPattern.MatchString(id) {
			return scheduleIDMessage
		}
		if at != "" {
			return parseScheduledRunTime(at, now)
		}
		return parseScheduleInterval(every)
	case "schedule.show", "schedule.history", "schedule.pause", "schedule.resume", "schedule.run", "schedule.delete":
		id := str("id")
		if id == "" {
			return action + " requires id."
		}
		if !scheduleIDPattern.MatchString(id) {
			return scheduleIDMessage
		}
	}
	return ""
}
