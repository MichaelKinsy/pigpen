package pi_subagents

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

var scheduleNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const scheduleMax = 9007199254740991

func TestScheduleAtValidation(t *testing.T) {
	maxSeconds := (scheduleMax - scheduleNow.UnixMilli()) / 1000
	for _, c := range []struct{ at, want string }{
		{"+10m", ""},
		{" +10m ", ""},
		{"+1s", ""},
		{"+0m", `Invalid at value "+0m". Relative delays must be positive, such as "+10m".`},
		{"+9007199254740992s", `Invalid at value "+9007199254740992s". Relative delays must be positive, such as "+10m".`},
		{"+" + itoa(maxSeconds) + "s", ""},
		{"+" + itoa(maxSeconds+1) + "s", `Invalid at value "+` + itoa(maxSeconds+1) + `s". Relative delay is too large.`},
		{"+9007199254740s", `Invalid at value "+9007199254740s". Relative delay is too large.`},
		{"tomorrow", `Invalid at value "tomorrow". Use a one-shot delay such as "+10m" or an ISO timestamp with timezone.`},
		{"2030-01-01T10:00Z", ""},
		{"2030-01-01T10:00:30Z", ""},
		{"2030-01-01T10:00:30.5+02:00", ""},
		{"2030-01-01T10:00:30+23:59", ""},
		{"2030-01-01T10:00+24:00", `Invalid at value "2030-01-01T10:00+24:00". Use a valid ISO timestamp.`},
		{"2030-01-01T10:00+00:60", `Invalid at value "2030-01-01T10:00+00:60". Use a valid ISO timestamp.`},
		{"2030-13-01T10:00Z", `Invalid at value "2030-13-01T10:00Z". Use a valid ISO timestamp.`},
		{"2030-02-30T10:00Z", `Invalid at value "2030-02-30T10:00Z". Use a valid ISO timestamp.`},
		{"2030-01-01T24:00Z", `Invalid at value "2030-01-01T24:00Z". Use a valid ISO timestamp.`},
		{"2030-01-01T10:60Z", `Invalid at value "2030-01-01T10:60Z". Use a valid ISO timestamp.`},
		{"2030-01-01T10:00:60Z", `Invalid at value "2030-01-01T10:00:60Z". Use a valid ISO timestamp.`},
		{"2025-12-31T23:59:59Z", "Scheduled time 2025-12-31T23:59:59.000Z is in the past."},
		{"2026-01-01T00:00:00Z", "Scheduled time 2026-01-01T00:00:00.000Z is in the past."},
		{"2026-01-01T00:00:01Z", ""},
	} {
		eq(t, parseScheduledRunTime(c.at, scheduleNow), c.want, "at "+c.at)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestScheduleIntervalValidation(t *testing.T) {
	for _, c := range []struct{ every, want string }{
		{"30m", ""}, {" 6h ", ""}, {"2d", ""}, {"2w", ""},
		{"0m", `Invalid every value "0m". Interval must be positive.`},
		{"5s", `Invalid every value "5s". This first recurring slice supports fixed intervals such as "30m", "6h", "2d", or "2w".`},
		{"14892855w", ""},
		{"14892856w", `Invalid every value "14892856w". Interval is too large.`},
		{"9007199254740991m", `Invalid every value "9007199254740991m". Interval is too large.`},
		{"9007199254740992m", `Invalid every value "9007199254740992m". Interval must be positive.`},
	} {
		eq(t, parseScheduleInterval(c.every), c.want, "every "+c.every)
	}
}

func TestScheduleArgumentErrors(t *testing.T) {
	sixtyFive := strings.Repeat("a", 65)
	idMsg := "Schedule id must be 1-64 characters and contain only letters, numbers, '.', '_', or '-'."
	for _, c := range []struct {
		action string
		params map[string]any
		want   string
	}{
		{"schedule.create", map[string]any{"at": "+10m"}, ""},
		{"schedule.create", map[string]any{"every": "30m"}, ""},
		{"schedule.create", map[string]any{}, "schedule.create requires exactly one trigger: at or every."},
		{"schedule.create", map[string]any{"at": "+10m", "every": "30m"}, "schedule.create requires exactly one trigger: at or every."},
		{"schedule.create", map[string]any{"every": "30m", "overlap": "queue"}, "This first recurring slice supports overlap='skip' only."},
		{"schedule.create", map[string]any{"every": "30m", "overlap": "skip", "catchUp": "latest"}, ""},
		{"schedule.create", map[string]any{"every": "30m", "catchUp": "none"}, ""},
		{"schedule.create", map[string]any{"every": "30m", "catchUp": "all"}, "catchUp must be 'none' or 'latest'."},
		{"schedule.create", map[string]any{"every": "30m", "missionScope": "x"}, "Mission attachment is deferred from this first schedule slice."},
		{"schedule.create", map[string]any{"every": "30m", "missionId": "x"}, "Mission attachment is deferred from this first schedule slice."},
		{"schedule.create", map[string]any{"every": "30m", "on": "mon"}, "Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."},
		{"schedule.create", map[string]any{"every": "30m", "timezone": "UTC"}, "Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."},
		{"schedule.create", map[string]any{"every": "day"}, "Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."},
		{"schedule.create", map[string]any{"every": "year"}, "Calendar schedules are deferred from this first safe slice. Use a fixed interval such as every:'24h' or every:'7d'."},
		{"schedule.create", map[string]any{"every": "30m", "quiet": "yes"}, "quiet must be a boolean."},
		{"schedule.create", map[string]any{"every": "30m", "quiet": true}, ""},
		{"schedule.create", map[string]any{"at": "+10m", "quiet": true}, "quiet is only supported for recurring schedules."},
		{"schedule.create", map[string]any{"at": "+10m", "quiet": false}, ""},
		{"schedule.create", map[string]any{"every": "30m", "id": "bad id"}, idMsg},
		{"schedule.create", map[string]any{"every": "30m", "id": sixtyFive}, idMsg},
		{"schedule.create", map[string]any{"every": "30m", "id": strings.Repeat("a", 64)}, ""},
		{"schedule.create", map[string]any{"at": "later"}, `Invalid at value "later". Use a one-shot delay such as "+10m" or an ISO timestamp with timezone.`},
		{"schedule.create", map[string]any{"every": "soon"}, `Invalid every value "soon". This first recurring slice supports fixed intervals such as "30m", "6h", "2d", or "2w".`},
		{"schedule.list", map[string]any{}, ""},
	} {
		eq(t, scheduleArgumentError(c.action, c.params, scheduleNow), c.want, fmt.Sprint(c.action, " ", c.params))
	}
	for _, action := range []string{"schedule.show", "schedule.history", "schedule.pause", "schedule.resume", "schedule.run", "schedule.delete"} {
		eq(t, scheduleArgumentError(action, map[string]any{}, scheduleNow), action+" requires id.", action+" without id")
		eq(t, scheduleArgumentError(action, map[string]any{"id": " "}, scheduleNow), action+" requires id.", action+" blank id")
		eq(t, scheduleArgumentError(action, map[string]any{"id": "bad id"}, scheduleNow), idMsg, action+" bad id")
		eq(t, scheduleArgumentError(action, map[string]any{"id": "nightly"}, scheduleNow), "", action+" id")
	}
}
