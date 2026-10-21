package croner

import (
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

// Expected values are croner 10.0.1's under Node 24.19 (`TZ=<zone> node`, new Cron(pattern).nextRun(from)): a cron time that occurs twice
// (the clocks go back) fires at its first occurrence.
func TestAmbiguousLocalTimeFiresFirst(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	for _, c := range []struct{ zone, pattern, from, want string }{
		{"America/New_York", "0 30 1 * * *", "2024-11-02T12:00:00Z", "2024-11-03T05:30:00.000Z"},
		{"America/New_York", "0 30 1 * * *", "2024-11-03T05:30:00Z", "2024-11-04T06:30:00.000Z"},
		{"Europe/London", "0 30 1 * * *", "2024-10-27T00:00:00Z", "2024-10-27T00:30:00.000Z"},
		{"Australia/Lord_Howe", "0 45 1 * * *", "2024-04-06T14:00:00Z", "2024-04-06T14:45:00.000Z"},
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		jsdate.SetLocation(loc)
		cr, err := Parse(c.pattern)
		if err != nil {
			t.Fatal(err)
		}
		got, err := cr.NextRunAfter(jsdate.Parse(c.from))
		if err != nil {
			t.Fatal(err)
		}
		if iso, _ := jsdate.ToISOString(got); iso != c.want {
			t.Errorf("%s %q after %s: %s, croner says %s", c.zone, c.pattern, c.from, iso, c.want)
		}
	}
}

// The constructor of croner runs the first search, so a pattern whose search overflows V8's stack is rejected at validation
// (croner 10.0.1: `new Cron("26 47 7-8 lw * +7-7", fn)` throws RangeError).
func TestValidateRunsTheSearch(t *testing.T) {
	err := Validate("26 47 7-8 lw * +7-7")
	if err == nil || err.Error() != "Maximum call stack size exceeded" {
		t.Fatalf("Validate = %v", err)
	}
}

// A one-shot ISO pattern without a zone is a local wall-clock time: where the clocks went back it is the first of the two instants.
// (croner 10.0.1 under Node 24.19 with TZ set: `new Cron("2024-10-27T01:30:00").nextRun(new Date("2024-01-01T00:00:00Z"))`.)
func TestISOPatternAmbiguousLocalTime(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	for _, c := range []struct{ zone, pattern, want string }{
		{"Europe/London", "2024-10-27T01:30:00", "2024-10-27T00:30:00.000Z"},
		{"Europe/London", "2024-11-03T01:30:00", "2024-11-03T01:30:00.000Z"},
		{"America/New_York", "2024-11-03T01:30:00", "2024-11-03T05:30:00.000Z"},
		{"America/New_York", "2024-03-10T02:30:00", "2024-03-10T07:30:00.000Z"},
		{"America/New_York", "2024-11-03T01:30:00Z", "2024-11-03T01:30:00.000Z"},
	} {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		jsdate.SetLocation(loc)
		cr, err := Parse(c.pattern)
		if err != nil {
			t.Fatalf("%s %q: %v", c.zone, c.pattern, err)
		}
		got, err := cr.NextRunAfter(jsdate.Parse("2024-01-01T00:00:00Z"))
		if err != nil {
			t.Fatal(err)
		}
		if iso, _ := jsdate.ToISOString(got); iso != c.want {
			t.Errorf("%s %q: %s, croner says %s", c.zone, c.pattern, iso, c.want)
		}
	}
}

// fromTZ itself (helpers/timezone.ts): of the two instants that show the same wall-clock time where the clocks went back it answers the
// first (the guess an hour earlier). CronDate keeps only the wall-clock fields, so the pattern tests above cannot tell the two apart.
func TestFromTZPrefersTheFirstOfTwoInstants(t *testing.T) {
	defer jsdate.SetLocation(time.Local)
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	jsdate.SetLocation(loc)
	got, err := fromTZ(timePoint{y: 2024, m: 10, d: 27, h: 1, i: 30})
	if err != nil {
		t.Fatal(err)
	}
	if iso, _ := jsdate.ToISOString(got); iso != "2024-10-27T00:30:00.000Z" {
		t.Fatalf("fromTZ = %s", iso)
	}
}
