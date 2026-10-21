package jsdate

import (
	"math"
	"testing"
)

// V8's ISO parser accepts 24:00 only as the end of a day, with nothing after the hour (Node 24: Date.parse of each string below).
func TestHour24IsOnlyTheEndOfTheDay(t *testing.T) {
	for _, s := range []string{"2020-01-01T24:00:00Z", "2020-01-01T24:00:00.000Z", "2020-01-01T24:00Z", "2020-01-01T24:00:00.0Z"} {
		if got := Parse(s); got != 1577923200000 {
			t.Errorf("Parse(%q) = %v, want 1577923200000", s, got)
		}
	}
	for _, s := range []string{"2020-01-01T24:00:01Z", "2020-01-01T24:01Z", "2020-01-01T24:00:00.5Z", "2020-01-01T25:00Z"} {
		if got := Parse(s); !math.IsNaN(got) {
			t.Errorf("Parse(%q) = %v, want NaN", s, got)
		}
	}
}
