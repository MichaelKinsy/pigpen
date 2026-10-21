package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"testing"
	"time"
	_ "time/tzdata" // the recorded zones must exist wherever the tests run
)

// The croner differential: testdata/croner-vectors.json holds croner 10.0.1's own answers (port/record-croner.mjs),
// recorded under six time zones. Each vector is a pattern and, if croner accepted it, the runs croner computed one
// after the other from `from`. cron.go must accept and reject the same patterns and compute the same instants.
type cronVector struct {
	Zone    string  `json:"zone"`
	Pattern string  `json:"pattern"`
	Valid   bool    `json:"valid"`
	From    int64   `json:"from"`
	Chain   []int64 `json:"chain"`
	Throws  string  `json:"throws"`
}

type dateVector struct {
	Zone  string `json:"zone"`
	Input string `json:"input"`
	Ms    *int64 `json:"ms"`
}

func loadCronVectors(t *testing.T) []cronVector {
	return loadCronFile(t).Vectors
}

func loadCronFixture(t *testing.T) ([]cronVector, []dateVector) {
	f := loadCronFile(t)
	return f.Vectors, f.Dates
}

type cronFixture struct {
	Croner  string           `json:"croner"`
	At      map[string]int64 `json:"at"` // per zone: the instant croner built its Crons at (a Cron searches for its first run from then)
	Vectors []cronVector     `json:"vectors"`
	Dates   []dateVector     `json:"dates"`
}

func loadCronFile(t *testing.T) cronFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/croner-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f cronFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	eq(t, f.Croner, "10.0.1")
	return f
}

func withLocal(t *testing.T, zone string) {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Skipf("no zone database for %s: %v", zone, err)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

func TestCronMatchesCroner(t *testing.T) {
	fixture := loadCronFile(t)
	vectors := fixture.Vectors
	eq(t, len(vectors) > 2500, true)
	byZone := map[string][]cronVector{}
	for _, v := range vectors {
		byZone[v.Zone] = append(byZone[v.Zone], v)
	}
	for zone, list := range byZone {
		t.Run(zone, func(t *testing.T) {
			withLocal(t, zone)
			for _, v := range list {
				c, err := newCronAt(v.Pattern, time.UnixMilli(fixture.At[zone]))
				if len(jsFields(v.Pattern)) != 6 {
					// croner also reads five and seven fields; the scheduler's own gate (validateCronExpression) allows six only.
					eq(t, validateCronExpression(v.Pattern).Valid, false)
					continue
				}
				if !v.Valid {
					if err == nil && !inStackBand(t, v.Pattern, fixture.At[zone]) {
						t.Errorf("%q: croner rejects it, cron.go accepts it", v.Pattern)
					}
					continue
				}
				if err != nil {
					if !(err.Error() == "Maximum call stack size exceeded" && searchWithinVariance(t, v.Pattern, fixture.At[zone])) {
						t.Errorf("%q: croner accepts it, cron.go says %v", v.Pattern, err)
					}
					continue
				}
				prev := time.UnixMilli(v.From)
				for i, want := range v.Chain {
					got, ok := c.next(prev)
					if !ok || got.UnixMilli() != want {
						t.Errorf("%s %q after %s, run %d: croner %s, cron.go %v (ok=%v)", zone, v.Pattern, prev.UTC().Format(time.RFC3339Nano), i,
							time.UnixMilli(want).UTC().Format(time.RFC3339Nano), got.UTC().Format(time.RFC3339Nano), ok)
						break
					}
					prev = got
				}
				if len(v.Chain) < 3 && v.Throws == "" {
					// croner stopped early: no further run before its year limit.
					if got, ok := c.next(prev); ok {
						t.Errorf("%s %q after %s: croner has no further run, cron.go finds %s", zone, v.Pattern, prev.UTC().Format(time.RFC3339Nano), got.UTC().Format(time.RFC3339Nano))
					}
				}
			}
		})
	}
}

// parseJSDate (the one-shot timestamps of detectSchedule) answers as `new Date(string)` does, under UTC and a zone
// with daylight saving, for the shapes the original can be handed.
func TestParseJSDateMatchesNode(t *testing.T) {
	_, dates := loadCronFixture(t)
	eq(t, len(dates) > 100, true)
	for _, d := range dates {
		withLocal(t, d.Zone)
		got, ok := parseJSDate(d.Input)
		switch {
		case d.Ms == nil && ok:
			t.Errorf("%s %q: Date.parse is NaN, parseJSDate says %v", d.Zone, d.Input, got)
		case d.Ms != nil && (!ok || int64(got) != *d.Ms):
			t.Errorf("%s %q: Date.parse says %d, parseJSDate says %v (ok=%v)", d.Zone, d.Input, *d.Ms, got, ok)
		}
	}
}

// The search croner makes for a run is a recursion, one call per field it settles or carries, and V8 throws from
// `new Cron` when it gets too deep. testdata/croner-stack.json holds, per pattern and instant, the calls croner's
// `recurse` made (read by a counting copy of croner, in a fresh process) and whether it overflowed. cron.go makes the
// same calls, one for one, and gives up at cronStackLimit, between the most a completed search made and the fewest at
// which one overflowed.
func TestCronSearchMakesCronersCallsAndOverflowsWhereItDoes(t *testing.T) {
	withLocal(t, "UTC")
	raw, err := os.ReadFile("testdata/croner-stack.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Croner  string `json:"croner"`
		Entries []struct {
			Pattern string `json:"pattern"`
			From    int64  `json:"from"`
			Calls   int    `json:"calls"`
			Outcome string `json:"outcome"` // "ok", "none" (no run before the year 3000) or "overflow"
		} `json:"entries"`
	}
	eq(t, json.Unmarshal(raw, &f), nil)
	eq(t, f.Croner, "10.0.1")
	var overflowed, completed, deepest int
	for _, e := range f.Entries {
		p, err := parseCronPattern(e.Pattern)
		if err != nil {
			t.Errorf("%q: %v", e.Pattern, err)
			continue
		}
		d := fromTime(time.UnixMilli(e.From))
		found := d.increment(p)
		if e.Outcome == "overflow" {
			overflowed++
			if !d.overflow && e.Calls > cronStackLimit { // at or below the limit is V8's variance (inStackBand)
				t.Errorf("%q from %d: croner overflowed its stack (at %d calls), cron.go made %d calls and did not", e.Pattern, e.From, e.Calls, d.calls)
			}
			continue
		}
		completed++
		deepest = max(deepest, e.Calls)
		if e.Calls > cronStackLimit {
			continue // croner completed a search deeper than the median overflow: V8's variance (inStackBand)
		}
		if d.overflow || d.calls != e.Calls {
			t.Errorf("%q from %d: croner made %d calls, cron.go %d (overflow %v)", e.Pattern, e.From, e.Calls, d.calls, d.overflow)
		}
		if found != (e.Outcome == "ok") {
			t.Errorf("%q from %d: croner says %s, cron.go found a run: %v", e.Pattern, e.From, e.Outcome, found)
		}
	}
	eq(t, overflowed > 0, true)
	eq(t, completed > 50, true)
	eq(t, deepest > 4000, true)
}

// stackBand is the range of depths at which croner overflowed in testdata/croner-stack.json, from the shallowest to the
// deepest: V8's limit is somewhere in it, depending on how warm the code is, so a search that lands in it (and under
// cronStackLimit) could go either way in the original.
func stackBand(t *testing.T) (low, high int) {
	t.Helper()
	var f struct {
		Entries []struct {
			Calls   int    `json:"calls"`
			Outcome string `json:"outcome"`
		} `json:"entries"`
	}
	raw, err := os.ReadFile("testdata/croner-stack.json")
	eq(t, err, nil)
	eq(t, json.Unmarshal(raw, &f), nil)
	low = 1 << 40
	for _, e := range f.Entries {
		if e.Outcome == "overflow" {
			low, high = min(low, e.Calls), max(high, e.Calls)
		}
	}
	return low, high
}

// cronTotalCalls is the calls a search makes from the instant from, without any limit.
func cronTotalCalls(t *testing.T, p *cronPattern, from int64) int {
	t.Helper()
	saved := cronStackLimit
	cronStackLimit = 1 << 40
	defer func() { cronStackLimit = saved }()
	d := fromTime(time.UnixMilli(from))
	d.increment(p)
	return d.calls
}

// inStackBand reports whether a pattern's first-run search, from at, makes as many calls as croner overflowed at in
// some probe: croner refusing it while cron.go accepts it is then V8's variance, not a difference in the search.
func inStackBand(t *testing.T, pattern string, at int64) bool {
	t.Helper()
	low, high := stackBand(t)
	p, err := parseCronPattern(pattern)
	if err != nil {
		return false
	}
	total := cronTotalCalls(t, p, at)
	return total >= low && total <= high
}

// searchWithinVariance reports whether a pattern's first-run search, from at, is deep enough to be near V8's limit and
// no more than twice cronStackLimit: croner completed it, so its stack was deeper than cron.go's limit, which is the
// median depth at which it overflowed (the depth V8 allows varies from probe to probe, and some completed at 7,000).
func searchWithinVariance(t *testing.T, pattern string, at int64) bool {
	t.Helper()
	p, err := parseCronPattern(pattern)
	if err != nil {
		return false
	}
	total := cronTotalCalls(t, p, at)
	return total > cronStackLimit && total <= 2*cronStackLimit
}
