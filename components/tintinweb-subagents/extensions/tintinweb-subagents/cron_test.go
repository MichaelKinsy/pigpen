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
	vectors, _ := loadCronFixture(t)
	return vectors
}

func loadCronFixture(t *testing.T) ([]cronVector, []dateVector) {
	t.Helper()
	raw, err := os.ReadFile("testdata/croner-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Croner  string       `json:"croner"`
		Vectors []cronVector `json:"vectors"`
		Dates   []dateVector `json:"dates"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	eq(t, f.Croner, "10.0.1")
	return f.Vectors, f.Dates
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
	vectors := loadCronVectors(t)
	eq(t, len(vectors) > 2500, true)
	byZone := map[string][]cronVector{}
	for _, v := range vectors {
		byZone[v.Zone] = append(byZone[v.Zone], v)
	}
	for zone, list := range byZone {
		t.Run(zone, func(t *testing.T) {
			withLocal(t, zone)
			for _, v := range list {
				c, err := newCron(v.Pattern)
				if len(jsFields(v.Pattern)) != 6 {
					// croner also reads five and seven fields; the scheduler's own gate (validateCronExpression) allows six only.
					eq(t, validateCronExpression(v.Pattern).Valid, false)
					continue
				}
				if !v.Valid {
					if err == nil {
						t.Errorf("%q: croner rejects it, cron.go accepts it", v.Pattern)
					}
					continue
				}
				if err != nil {
					t.Errorf("%q: croner accepts it, cron.go says %v", v.Pattern, err)
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
