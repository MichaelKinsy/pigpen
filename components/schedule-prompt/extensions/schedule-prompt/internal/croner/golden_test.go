package croner

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/jsdate"
)

type cronGolden struct {
	Croner  string              `json:"croner"`
	Zones   []string            `json:"zones"`
	Exprs   []string            `json:"exprs"`
	Refs    [][]float64         `json:"refs"`
	Results [][]json.RawMessage `json:"results"`
}

func readGolden(t *testing.T, path string) cronGolden {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g cronGolden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func checkGolden(t *testing.T, g cronGolden) {
	defer jsdate.SetLocation(time.Local)
	for zi, zone := range g.Zones {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		jsdate.SetLocation(loc)
		bad := 0
		report := func(format string, args ...any) {
			bad++
			if bad <= 12 {
				t.Errorf(zone+": "+format, args...)
			}
		}
		for ei, expr := range g.Exprs {
			raw := g.Results[zi][ei]
			var rejected struct {
				E string `json:"e"`
			}
			isErr := len(raw) > 0 && raw[0] == '{'
			if isErr {
				_ = json.Unmarshal(raw, &rejected)
			}
			c, err := Parse(expr)
			if err == nil {
				err = Validate(expr)
			}
			if isErr {
				if err == nil {
					report("%q: croner rejects with %q, the port accepts", expr, rejected.E)
				} else if err.Error() != rejected.E {
					report("%q: error %q, croner says %q", expr, err.Error(), rejected.E)
				}
				continue
			}
			if err != nil {
				report("%q: the port rejects with %q, croner accepts", expr, err.Error())
				continue
			}
			c, _ = Parse(expr)
			var chains [][]any
			if err := json.Unmarshal(raw, &chains); err != nil {
				t.Fatal(err)
			}
			for ri, ref := range g.Refs[zi] {
				prev := ref
				for step, want := range chains[ri] {
					depth := 0
					DepthHook = func(n int) { depth = max(depth, n) }
					got, gerr := c.NextRunAfter(prev)
					DepthHook = nil
					if gerr != nil && depth > 6800 && depth < 7800 {
						break // inside the band where V8's stack limit is not exactly reproducible
					}
					if gerr != nil {
						if w, ok := want.(string); ok && w == gerr.Error() {
							break
						}
						report("%q after %v: error %q, croner says %v", expr, prev, gerr.Error(), want)
						break
					}
					switch w := want.(type) {
					case nil:
						if !math.IsNaN(got) {
							report("%q after %v (step %d): got %v, croner has no next run", expr, prev, step, got)
						}
					case string:
						if depth > 6800 && depth < 7800 {
							break
						}
						report("%q after %v: croner answers %q, the port %v", expr, prev, w, got)
					case float64:
						if got-prev != w {
							report("%q after %v (step %d): +%v ms, croner says +%v", expr, prev, step, got-prev, w)
						}
					}
					if math.IsNaN(got) {
						break
					}
					prev = got
				}
			}
		}
		if bad > 12 {
			t.Errorf("%s: %d differences", zone, bad)
		}
	}
}

// TestCronGolden compares validation errors and next-run times with croner 10.0.1 under Node (gen-cron-golden.mjs).
func TestCronGolden(t *testing.T) {
	checkGolden(t, readGolden(t, "../../../../port/diff/cron-golden.json.gz"))
}

// TestCronGoldenLarge runs a bigger corpus (CRONER_GOLDEN=<file made with gen-cron-golden.mjs --exprs 100000>), for the evidence run.
func TestCronGoldenLarge(t *testing.T) {
	path := os.Getenv("CRONER_GOLDEN")
	if path == "" {
		t.Skip("CRONER_GOLDEN is not set")
	}
	checkGolden(t, readGolden(t, path))
}
