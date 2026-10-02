package pitypesafe

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// A judge-tuning kit: label a set of cases, score them with Jev, and read off AUC and threshold behaviour. It
// carries no domain knowledge: a case is anything a scorer can turn into a number, so the same toolkit fits an
// action guard, a triage rule, or a prose check.

// ScoredSample is one labelled observation: the truth about the case, and the number the judge assigned to it.
type ScoredSample struct {
	Label bool
	Score float64
	// ID is an optional name; it is used in the missed and flagged listings.
	ID string
}

// ThresholdRow holds outcome counts for one threshold. Precision and Recall are nil when their denominator is empty.
type ThresholdRow struct {
	Threshold float64
	Flagged   int
	TP, FP    int
	FN, TN    int
	Precision *float64
	Recall    *float64
	// FlagRate is the share of all cases the threshold selects.
	FlagRate float64
}

// Calibration is the result of Calibrate.
type Calibration struct {
	Name      string
	Scored    int
	Positives int
	Negatives int
	Errors    int
	// AUC is the rank-based AUC (Mann–Whitney, ties count half); nil when one class is empty.
	AUC  *float64
	Rows []ThresholdRow
	// Recommended is the lowest threshold meeting the requested floors, when one exists.
	Recommended *ThresholdRow
	// Missed are positive cases the recommended threshold misses.
	Missed []ScoredSample
	// Flagged are negative cases the recommended threshold flags.
	Flagged []ScoredSample
}

// CalibrateOptions configure Calibrate.
type CalibrateOptions struct {
	// Thresholds to evaluate. Default: every distinct score, ascending (at most 64 rows).
	Thresholds []float64
	// MinPrecision and MinRecall are floors for the recommendation; nil means no floor.
	MinPrecision *float64
	MinRecall    *float64
	// Errors counts cases that could not be scored; reported and excluded from the metrics.
	Errors int
}

// AUC is the rank-based probability that a positive outranks a negative; nil when a class is empty.
func AUC(samples []ScoredSample) *float64 {
	var pos, neg []float64
	for _, s := range samples {
		if s.Label {
			pos = append(pos, s.Score)
		} else {
			neg = append(neg, s.Score)
		}
	}
	if len(pos) == 0 || len(neg) == 0 {
		return nil
	}
	wins := 0.0
	for _, p := range pos {
		for _, n := range neg {
			switch {
			case p > n:
				wins++
			case p == n:
				wins += 0.5
			}
		}
	}
	v := wins / float64(len(pos)*len(neg))
	return &v
}

func ratio(num, den int) *float64 {
	if den == 0 {
		return nil
	}
	v := float64(num) / float64(den)
	return &v
}

// MetricsAt counts the outcomes at one threshold: a case is flagged when its score is at least the threshold.
func MetricsAt(samples []ScoredSample, threshold float64) ThresholdRow {
	row := ThresholdRow{Threshold: threshold}
	for _, s := range samples {
		flagged := s.Score >= threshold
		switch {
		case flagged && s.Label:
			row.TP++
		case flagged:
			row.FP++
		case s.Label:
			row.FN++
		default:
			row.TN++
		}
	}
	row.Flagged = row.TP + row.FP
	row.Precision = ratio(row.TP, row.TP+row.FP)
	row.Recall = ratio(row.TP, row.TP+row.FN)
	if len(samples) > 0 {
		row.FlagRate = float64(row.Flagged) / float64(len(samples))
	}
	return row
}

// Sweep evaluates every threshold.
func Sweep(samples []ScoredSample, thresholds []float64) []ThresholdRow {
	rows := make([]ThresholdRow, len(thresholds))
	for i, t := range thresholds {
		rows[i] = MetricsAt(samples, t)
	}
	return rows
}

// DefaultThresholds are the distinct scores, ascending, as a threshold grid: every point where the counts can
// change. limit (64 when not positive) caps the rows by sampling evenly.
func DefaultThresholds(samples []ScoredSample, limit int) []float64 {
	if limit <= 0 {
		limit = 64
	}
	seen := map[float64]bool{}
	var distinct []float64
	for _, s := range samples {
		if !seen[s.Score] {
			seen[s.Score] = true
			distinct = append(distinct, s.Score)
		}
	}
	sort.Float64s(distinct)
	if len(distinct) <= limit {
		return distinct
	}
	step := float64(len(distinct)-1) / float64(limit-1)
	out := make([]float64, limit)
	for i := range out {
		out[i] = distinct[int(math.Floor(float64(i)*step+0.5))]
	}
	return out
}

// PickThreshold returns the lowest threshold that clears the precision and recall floors. With no floors, it
// returns the best F1 among the rows; nil when nothing clears them.
func PickThreshold(rows []ThresholdRow, minPrecision, minRecall *float64) *ThresholdRow {
	if minPrecision == nil && minRecall == nil {
		var best *ThresholdRow
		bestF1 := -1.0
		for i := range rows {
			row := rows[i]
			if row.Precision == nil || row.Recall == nil {
				continue
			}
			f1 := 0.0
			if *row.Precision+*row.Recall != 0 {
				f1 = 2 * *row.Precision * *row.Recall / (*row.Precision + *row.Recall)
			}
			if f1 > bestF1 {
				bestF1 = f1
				best = &rows[i]
			}
		}
		return best
	}
	var candidates []ThresholdRow
	for _, row := range rows {
		p, r := 0.0, 0.0
		if row.Precision != nil {
			p = *row.Precision
		}
		if row.Recall != nil {
			r = *row.Recall
		}
		if (minPrecision == nil || p >= *minPrecision) && (minRecall == nil || r >= *minRecall) {
			candidates = append(candidates, row)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Threshold < candidates[j].Threshold })
	return &candidates[0]
}

// Calibrate labels, scores, and reads the numbers: AUC, the threshold sweep, and one recommendation.
func Calibrate(name string, samples []ScoredSample, opts CalibrateOptions) Calibration {
	thresholds := opts.Thresholds
	if thresholds == nil {
		thresholds = DefaultThresholds(samples, 0)
	}
	rows := Sweep(samples, thresholds)
	recommendation := PickThreshold(rows, opts.MinPrecision, opts.MinRecall)
	c := Calibration{Name: name, Scored: len(samples), Errors: opts.Errors, AUC: AUC(samples), Rows: rows, Recommended: recommendation}
	for _, s := range samples {
		if s.Label {
			c.Positives++
		} else {
			c.Negatives++
		}
	}
	if recommendation != nil {
		t := recommendation.Threshold
		for _, s := range samples {
			if s.Label && s.Score < t {
				c.Missed = append(c.Missed, s)
			}
			if !s.Label && s.Score >= t {
				c.Flagged = append(c.Flagged, s)
			}
		}
	}
	return c
}

func percent(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", math.Floor(*v*100+0.5))
}

func listing(samples []ScoredSample) string {
	parts := make([]string, len(samples))
	for i, s := range samples {
		if s.ID != "" {
			parts[i] = s.ID
		} else {
			parts[i] = fmt.Sprintf("%.2f", s.Score)
		}
	}
	text := strings.Join(parts, ", ")
	return truncateUTF16(text, 300)
}

// FormatCalibration renders plain text, no colour: safe to write to a report file or a log.
func FormatCalibration(c Calibration) string {
	errs := ""
	if c.Errors > 0 {
		errs = fmt.Sprintf(", %d errors", c.Errors)
	}
	auc := "-"
	if c.AUC != nil {
		auc = fmt.Sprintf("%.3f", *c.AUC)
	}
	lines := []string{
		fmt.Sprintf("%s: %d scored, %d positives, %d negatives%s", c.Name, c.Scored, c.Positives, c.Negatives, errs),
		"AUC " + auc,
		"threshold  flagged  TP  FP  FN  TN  precision  recall",
	}
	for _, r := range c.Rows {
		lines = append(lines, fmt.Sprintf("%9.2f  %7d  %2d  %2d  %2d  %2d  %9s  %6s", r.Threshold, r.Flagged, r.TP, r.FP, r.FN, r.TN, percent(r.Precision), percent(r.Recall)))
	}
	if c.Recommended == nil {
		lines = append(lines, "recommended: none (no threshold clears the floors)")
	} else {
		r := c.Recommended
		lines = append(lines, fmt.Sprintf("recommended %.2f: precision %s, recall %s, flags %s", r.Threshold, percent(r.Precision), percent(r.Recall), percent(&r.FlagRate)))
	}
	if len(c.Missed) > 0 {
		lines = append(lines, fmt.Sprintf("missed positives (%d): %s", len(c.Missed), listing(c.Missed)))
	}
	if len(c.Flagged) > 0 {
		lines = append(lines, fmt.Sprintf("flagged negatives (%d): %s", len(c.Flagged), listing(c.Flagged)))
	}
	return strings.Join(lines, "\n")
}

// ReplayCase is one labelled replay case: the truth, plus whatever the scorer needs to judge it.
type ReplayCase[T any] struct {
	ID    string
	Label bool
	Data  T
}

// ReplayResult is the outcome of one replayed case.
type ReplayResult[T any] struct {
	ID    string
	Label bool
	Data  T
	// Score is the judge's number; valid only when Scored.
	Score  float64
	Scored bool
	// Error is the failure message, empty on success. It carries no upstream body.
	Error string
	// Skipped is true when the case was never submitted (cancellation or a stopped batch).
	Skipped bool
}

// ReplayOptions configure Replay.
type ReplayOptions struct {
	// Concurrency is the number of cases in flight at once. Default: DefaultConcurrency.
	Concurrency int
	// StopOn stops launching new cases once it returns true for a failure, for example a budget error.
	StopOn func(error) bool
	// DescribeError turns a scorer error into the reported message. Default: the error's own message.
	DescribeError func(error) string
}

// Replay runs labelled cases through a scorer with bounded concurrency, keeping order and capturing per-case
// failures. The scorer is usually one Jev question; an error is recorded rather than aborting the run, so one
// bad case cannot destroy a long calibration. Results feed straight into SamplesOf and Calibrate.
func Replay[T any](ctx context.Context, cases []ReplayCase[T], score func(ctx context.Context, data T, index int) (float64, error), opts ReplayOptions) []ReplayResult[T] {
	settled := FanOut(ctx, cases, func(ctx context.Context, c ReplayCase[T], index int) (float64, error) {
		return score(ctx, c.Data, index)
	}, FanOutOptions{Concurrency: opts.Concurrency, StopOn: opts.StopOn})
	out := make([]ReplayResult[T], len(settled))
	for i, r := range settled {
		c := cases[i]
		res := ReplayResult[T]{ID: c.ID, Label: c.Label, Data: c.Data}
		if r.OK {
			res.Score, res.Scored = r.Value, true
		} else {
			res.Skipped = r.Skipped
			if opts.DescribeError != nil {
				res.Error = opts.DescribeError(r.Err)
			} else if r.Err != nil && r.Err.Error() != "" {
				res.Error = r.Err.Error()
			} else {
				res.Error = "The scorer failed."
			}
		}
		out[i] = res
	}
	return out
}

// SamplesOf returns the scored cases of a replay, in replay order. Unscored cases are excluded and counted as errors.
func SamplesOf[T any](results []ReplayResult[T]) (samples []ScoredSample, errors int) {
	for _, r := range results {
		if !r.Scored || math.IsNaN(r.Score) || math.IsInf(r.Score, 0) {
			errors++
			continue
		}
		samples = append(samples, ScoredSample{Label: r.Label, Score: r.Score, ID: r.ID})
	}
	return samples, errors
}
