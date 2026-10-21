package jsdate

import (
	"testing"
	"time"
)

// Assertions that pin the mutation run's survivors (V8's Date.UTC and `new Date(y, m, ...)`).

func TestTwoDigitYearsAreNineteenHundreds(t *testing.T) {
	old := Location()
	SetLocation(time.UTC)
	defer SetLocation(old)
	for _, y := range []int64{0, 1, 50, 51, 75, 99} {
		if got, want := LocalDate(y, 0, 1, 0, 0, 0, 0), LocalDate(1900+y, 0, 1, 0, 0, 0, 0); got != want {
			t.Errorf("new Date(%d, 0, 1) = %v, want the year %d: %v", y, got, 1900+y, want)
		}
	}
}

// A negative month counts back into the previous year: Date.UTC(2020, -1, 1) is 2019-12-01.
func TestNegativeMonthsCarryIntoThePreviousYear(t *testing.T) {
	for _, c := range []struct{ y, m, wy, wm int64 }{{2020, -1, 2019, 11}, {2020, -12, 2019, 0}, {2020, -13, 2018, 11}, {2020, 12, 2021, 0}, {2020, 25, 2022, 1}} {
		if got, want := UTCDate(c.y, c.m, 1, 0, 0, 0, 0), UTCDate(c.wy, c.wm, 1, 0, 0, 0, 0); got != want {
			t.Errorf("Date.UTC(%d, %d, 1) = %v, want Date.UTC(%d, %d, 1) = %v", c.y, c.m, got, c.wy, c.wm, want)
		}
	}
}
