package jsdate

import (
	"compress/gzip"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
	_ "time/tzdata"
)

type dateGolden struct {
	V8      string       `json:"v8"`
	Zones   []string     `json:"zones"`
	Strings []string     `json:"strings"`
	Parse   [][]*float64 `json:"parse"`
	Local   [][][]any    `json:"local"`
	ISO     [][]any      `json:"iso"`
}

func loadGolden(t *testing.T) dateGolden {
	t.Helper()
	f, err := os.Open("../../../../port/diff/date-golden.json.gz")
	if err != nil {
		f, err = os.Open("../../../../../port/diff/date-golden.json.gz")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var g dateGolden
	if err := json.NewDecoder(zr).Decode(&g); err != nil {
		t.Fatal(err)
	}
	return g
}

func num(v any) float64 {
	if v == nil {
		return math.NaN()
	}
	return v.(float64)
}

func same(a, b float64) bool { return (math.IsNaN(a) && math.IsNaN(b)) || a == b }

// TestDateGolden compares Date.parse, the local constructor and the local getters with what Node's V8 answers (gen-date-golden.mjs).
func TestDateGolden(t *testing.T) {
	g := loadGolden(t)
	defer SetLocation(time.Local)
	for zi, zone := range g.Zones {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		SetLocation(loc)
		t.Run(zone, func(t *testing.T) {
			bad := 0
			for i, s := range g.Strings {
				want := math.NaN()
				if g.Parse[zi][i] != nil {
					want = *g.Parse[zi][i]
				}
				if got := Parse(s); !same(got, want) {
					bad++
					if bad <= 15 {
						t.Errorf("Parse(%q) = %v, V8 says %v", s, got, want)
					}
				}
			}
			if bad > 15 {
				t.Errorf("%d strings differ in %s", bad, zone)
			}
			lbad := 0
			for _, r := range g.Local[zi] {
				inst := r[0].(float64)
				f := LocalFields(inst)
				got := []int64{f.Year, f.Month, f.Day, f.Hour, f.Minute, f.Second, f.Weekday}
				for k := range got {
					if float64(got[k]) != r[1+k].(float64) {
						lbad++
						if lbad <= 15 {
							t.Errorf("LocalFields(%v)[%d] = %v, V8 says %v", inst, k, got[k], r[1+k])
						}
					}
				}
				if w := LocalDate(f.Year, f.Month, f.Day, f.Hour, f.Minute, f.Second, 0); !same(w, num(r[8])) {
					lbad++
					if lbad <= 15 {
						t.Errorf("LocalDate(%v wall clock) = %v, V8 says %v", inst, w, num(r[8]))
					}
				}
				if len(r) > 9 {
					for k, dd := range [][2]int64{{1, 0}, {-1, 0}, {0, 1}} {
						w := LocalDate(f.Year, f.Month, f.Day+dd[1], f.Hour+dd[0], f.Minute, f.Second, 0)
						if want := num(r[9+k]); !same(w, want) {
							lbad++
							if lbad <= 15 {
								t.Errorf("LocalDate shifted %d at %v = %v, V8 says %v", k, inst, w, want)
							}
						}
					}
				}
			}
			if lbad > 15 {
				t.Errorf("%d local rows differ in %s", lbad, zone)
			}
		})
	}
	for _, r := range g.ISO {
		want := r[1].(string)
		got, ok := ToISOString(r[0].(float64))
		if !ok || got != want {
			t.Errorf("ToISOString(%v) = %q, want %q", r[0], got, want)
		}
	}
}
