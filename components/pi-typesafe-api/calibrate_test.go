package pitypesafe

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sample(score float64, label bool, id string) ScoredSample {
	return ScoredSample{Score: score, Label: label, ID: id}
}

// Eight observations: two clear positives, two clear negatives, and a muddy middle.
func muddy() []ScoredSample {
	return []ScoredSample{
		sample(0.95, true, "p1"), sample(0.8, true, "p2"), sample(0.6, true, "p3"), sample(0.4, true, "p4"),
		sample(0.7, false, "n1"), sample(0.5, false, "n2"), sample(0.2, false, "n3"), sample(0.05, false, "n4"),
	}
}

func f(v float64) *float64 { return &v }

func TestCalibrate(t *testing.T) {
	tw(t, "calibrate", "AUC is the rank-based probability that a positive outranks a negative", func(t *testing.T) {
		eq := func(got *float64, want float64) {
			t.Helper()
			if got == nil || *got != want {
				t.Errorf("auc = %v, want %v", got, want)
			}
		}
		eq(AUC([]ScoredSample{sample(1, true, ""), sample(0, false, "")}), 1)
		eq(AUC([]ScoredSample{sample(0, true, ""), sample(1, false, "")}), 0)
		eq(AUC([]ScoredSample{sample(0.5, true, ""), sample(0.5, false, "")}), 0.5)
		eq(AUC([]ScoredSample{sample(0.5, true, ""), sample(0.5, true, ""), sample(0.5, false, ""), sample(0.5, false, "")}), 0.5)
		// One class only: nothing to rank against.
		if AUC([]ScoredSample{sample(0.5, true, ""), sample(0.9, true, "")}) != nil || AUC(nil) != nil {
			t.Error("a single class has no AUC")
		}
		// Three positives above one negative and one below: 3 wins of 4 pairs.
		eq(AUC([]ScoredSample{sample(0.9, true, ""), sample(0.8, true, ""), sample(0.7, true, ""), sample(0.6, false, ""), sample(0.1, true, "")}), 0.75)
	})
	tw(t, "calibrate", "a threshold row counts every case once", func(t *testing.T) {
		row := MetricsAt(muddy(), 0.6)
		if row.Threshold != 0.6 || row.Flagged != 4 || row.TP != 3 || row.FP != 1 || row.FN != 1 || row.TN != 3 || *row.Precision != 0.75 || *row.Recall != 0.75 || row.FlagRate != 0.5 {
			t.Fatalf("row = %+v", row)
		}
		// At the top of the range nothing is flagged, so precision has no denominator.
		strict := MetricsAt(muddy(), 1)
		if strict.Flagged != 0 || strict.Precision != nil || *strict.Recall != 0 {
			t.Fatalf("strict = %+v", strict)
		}
		if got := Sweep(muddy(), []float64{0.6}); got[0].TP != 3 {
			t.Fatalf("sweep = %+v", got)
		}
	})
	tw(t, "calibrate", "the default threshold grid is every distinct score, ascending", func(t *testing.T) {
		if got := DefaultThresholds(muddy(), 0); !reflect.DeepEqual(got, []float64{0.05, 0.2, 0.4, 0.5, 0.6, 0.7, 0.8, 0.95}) {
			t.Fatalf("grid = %v", got)
		}
		if len(DefaultThresholds(muddy(), 3)) != 3 {
			t.Error("limit 3 must give 3 rows")
		}
		if got := DefaultThresholds([]ScoredSample{sample(0.5, true, ""), sample(0.5, false, "")}, 0); !reflect.DeepEqual(got, []float64{0.5}) {
			t.Errorf("grid = %v", got)
		}
	})
	tw(t, "calibrate", "a recommendation honours the floors, and the report names what it misses and flags", func(t *testing.T) {
		c := Calibrate("muddy", muddy(), CalibrateOptions{Thresholds: []float64{0.5, 0.6, 0.7, 0.8}, MinPrecision: f(0.75), MinRecall: f(0.5)})
		if c.Recommended == nil || c.Recommended.Threshold != 0.6 || c.Scored != 8 || c.Positives != 4 || c.Negatives != 4 || c.Errors != 0 || c.AUC == nil || *c.AUC <= 0.7 {
			t.Fatalf("calibration = %+v", c)
		}
		if len(c.Missed) != 1 || c.Missed[0].ID != "p4" || len(c.Flagged) != 1 || c.Flagged[0].ID != "n1" {
			t.Fatalf("missed=%v flagged=%v", c.Missed, c.Flagged)
		}
		text := FormatCalibration(c)
		for _, want := range []string{"muddy: 8 scored, 4 positives, 4 negatives", "recommended 0.60: precision 75%, recall 75%", "missed positives (1): p4", "flagged negatives (1): n1"} {
			if !strings.Contains(text, want) {
				t.Errorf("report lacks %q:\n%s", want, text)
			}
		}
		// Floors nothing can meet leave no recommendation, and the report says so.
		impossible := Calibrate("strict", muddy(), CalibrateOptions{Thresholds: []float64{0.5}, MinPrecision: f(0.99), MinRecall: f(0.99)})
		if impossible.Recommended != nil || !strings.Contains(FormatCalibration(impossible), "recommended: none") {
			t.Fatalf("impossible = %+v", impossible)
		}
	})
	tw(t, "calibrate", "with no floors, the recommendation is the best F1 among the rows", func(t *testing.T) {
		rows := Sweep(muddy(), []float64{0.4, 0.5, 0.6, 0.7})
		var tps []int
		for _, r := range rows {
			tps = append(tps, r.TP)
		}
		// At 0.4 every positive is caught for two false alarms: F1 0.8 beats the tighter rows.
		if !reflect.DeepEqual(tps, []int{4, 3, 3, 2}) {
			t.Fatalf("tp = %v", tps)
		}
		if got := PickThreshold(rows, nil, nil); got == nil || got.Threshold != 0.4 {
			t.Fatalf("pick = %+v", got)
		}
		if PickThreshold(nil, nil, nil) != nil {
			t.Error("no rows, no pick")
		}
		// No positives: precision has no denominator, so no row can be ranked.
		if PickThreshold(Sweep([]ScoredSample{sample(0.5, false, "")}, []float64{0.5}), nil, nil) != nil {
			t.Error("no positives, no pick")
		}
	})
	tw(t, "calibrate", "replay scores labelled cases with bounded concurrency and keeps per-case order", func(t *testing.T) {
		var inFlight, peak atomic.Int32
		cases := []ReplayCase[int]{{"a", true, 1}, {"b", false, 2}, {"c", true, 3}, {"d", false, 4}}
		results := Replay(context.Background(), cases, func(_ context.Context, data int, _ int) (float64, error) {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
			return float64(data), nil
		}, ReplayOptions{Concurrency: 2})
		if peak.Load() != 2 {
			t.Errorf("peak = %d", peak.Load())
		}
		for i, r := range results {
			if r.ID != cases[i].ID || r.Score != float64(i+1) || r.Skipped || r.Error != "" {
				t.Errorf("result %d = %+v", i, r)
			}
		}
		if samples, errs := SamplesOf(results); errs != 0 || len(samples) != 4 {
			t.Errorf("samples = %v, %d", samples, errs)
		}
	})
	tw(t, "calibrate", "a budget failure stops the replay and leaves the rest unsubmitted", func(t *testing.T) {
		var started []int
		cases := []ReplayCase[int]{{"a", true, 1}, {"b", false, 2}, {"c", true, 3}, {"d", false, 4}}
		msg := "TypeSafe request limit reached (1 attempts per client instance)."
		results := Replay(context.Background(), cases, func(_ context.Context, data int, _ int) (float64, error) {
			started = append(started, data)
			if data == 2 {
				return 0, newError(CodeBudget, msg)
			}
			return 0.1, nil
		}, ReplayOptions{Concurrency: 1, StopOn: func(err error) bool { return hasCode(err, CodeBudget) }})
		if !reflect.DeepEqual(started, []int{1, 2}) || results[1].Error != msg || results[1].Skipped {
			t.Fatalf("started=%v r1=%+v", started, results[1])
		}
		// The budget stop kept every later case from being submitted at all.
		if !results[2].Skipped || !results[3].Skipped || results[3].Scored {
			t.Fatalf("r2=%+v r3=%+v", results[2], results[3])
		}
		samples, errs := SamplesOf(results)
		if errs != 3 || !reflect.DeepEqual(samples, []ScoredSample{{Label: true, Score: 0.1, ID: "a"}}) {
			t.Fatalf("samples = %v, %d", samples, errs)
		}
		// A replay that lost most of its cases still yields honest numbers.
		c := Calibrate("replay", samples, CalibrateOptions{Errors: errs})
		if c.Errors != 3 || c.Scored != 1 || c.AUC != nil {
			t.Fatalf("calibration = %+v", c)
		}
	})
	tw(t, "calibrate", "replay reports scorer failures with the caller's own message", func(t *testing.T) {
		thrown := errors.New("upstream body with a key: sk-secret")
		one := []ReplayCase[string]{{"boom", true, "case"}}
		fail := func(context.Context, string, int) (float64, error) { return 0, thrown }
		// The default is the scorer's own message, which the caller controls; a supplied describer replaces it.
		plain := Replay(context.Background(), one, fail, ReplayOptions{})
		if plain[0].Error != "upstream body with a key: sk-secret" || plain[0].Scored {
			t.Fatalf("plain = %+v", plain[0])
		}
		described := Replay(context.Background(), one, fail, ReplayOptions{DescribeError: func(error) string { return "The scorer failed." }})
		if described[0].Error != "The scorer failed." {
			t.Fatalf("described = %+v", described[0])
		}
		// An error with no message reads as the generic failure (a thrown non-Error in the original).
		blank := Replay(context.Background(), one, func(context.Context, string, int) (float64, error) { return 0, errors.New("") }, ReplayOptions{})
		if blank[0].Error != "The scorer failed." {
			t.Fatalf("blank = %+v", blank[0])
		}
	})
}
