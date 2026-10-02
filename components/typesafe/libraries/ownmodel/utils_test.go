package ownmodel

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

func approx(t testing.TB, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestConfidenceMetrics(t *testing.T) {
	twin(t,
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[score_confidence-probabilities0-0.0]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[score_confidence-probabilities1-0.0]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[score_confidence-probabilities2-0.55]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[choice_confidence-probabilities3-0.0]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[choice_confidence-probabilities4-0.0]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[choice_confidence-probabilities5-0.64]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[score_confidence-probabilities6-1.0]",
		"tests/utils/test_confidence_metrics.py::test_confidence_metrics[choice_confidence-probabilities7-1.0]")
	for _, tc := range []struct {
		metric func([]float64) float64
		probs  []float64
		want   float64
	}{
		{scoreConfidence, []float64{0.2, 0.2, 0.2, 0.2, 0.2}, 0.0},
		{scoreConfidence, []float64{0.04, 0.04, 0.04, 0.04, 0.04}, 0.0},
		{scoreConfidence, []float64{0.01, 0.02, 0.07, 0.3, 0.6}, 0.55},
		{choiceConfidence, []float64{0.5, 0.5}, 0.0},
		{choiceConfidence, []float64{0.2, 0.2}, 0.0},
		{choiceConfidence, []float64{0.82, 0.18}, 0.64},
		{scoreConfidence, []float64{1.0}, 1.0},
		{choiceConfidence, []float64{1.0}, 1.0},
	} {
		approx(t, tc.metric(tc.probs), tc.want)
	}
	// A zero total falls back to a uniform distribution.
	approx(t, choiceConfidence([]float64{0, 0}), 0)
	approx(t, scoreConfidence([]float64{0, 0, 0}), 0)
}

func TestProbabilityNormalizationAndDebugData(t *testing.T) {
	twin(t,
		"tests/utils/test_probability_normalization.py::test_probability_normalization_and_debug_data[False-0.2-0.2-None-0.6-expected_probability_errors0]",
		"tests/utils/test_probability_normalization.py::test_probability_normalization_and_debug_data[True-0.2-0.5-expected_originals1-0.6-expected_probability_errors1]",
		"tests/utils/test_probability_normalization.py::test_probability_normalization_and_debug_data[False-0.50000025-0.50000025-None-5e-07-expected_probability_errors2]")
	for _, tc := range []struct {
		enabled       bool
		raw, want     float64
		originals     bool
		maxError      float64
		invalidCounts int
	}{{false, 0.2, 0.2, false, 0.6, 2}, {true, 0.2, 0.5, true, 0.6, 2}, {false, 0.50000025, 0.50000025, false, 5e-7, 0}} {
		score := normalizeProbabilities([]string{"0", "1"}, map[string]float64{"0": tc.raw, "1": tc.raw}, "", Probabilities, tc.enabled)
		genre := normalizeProbabilities([]string{"fiction", "nonfiction"}, map[string]float64{"fiction": tc.raw, "nonfiction": tc.raw}, "", Probabilities, tc.enabled)
		approx(t, score.Probabilities["0"], tc.want)
		approx(t, score.Probabilities["1"], tc.want)
		approx(t, genre.Probabilities["fiction"], tc.want)
		d := probabilityDebugData(map[string]*probNorm{"positive": nil, "stars": &score, "genre": &genre})
		approx(t, d.MaxError, tc.maxError)
		eq(t, d.InvalidProbs, tc.invalidCounts)
		eq(t, len(d.ProbabilityErrors), tc.invalidCounts)
		for _, e := range d.ProbabilityErrors {
			approx(t, e, tc.maxError)
		}
		if tc.originals {
			eq(t, d.Original, map[string]map[string]float64{"stars": {"0": 0.2, "1": 0.2}, "genre": {"fiction": 0.2, "nonfiction": 0.2}})
		} else if len(d.Original) != 0 {
			t.Fatalf("no originals expected, got %v", d.Original)
		}
	}
}

func TestDiscreteNormalizationSelectsOneAnswer(t *testing.T) {
	n := normalizeProbabilities([]string{"a", "b", "c"}, nil, "b", Discrete, false)
	eq(t, n.Probabilities, map[string]float64{"a": 0, "b": 1, "c": 0})
	eq(t, n.Error, 0.0)
}

func TestRetriesSucceedAfterTransientError(t *testing.T) {
	twin(t, "tests/utils/test_error_handling.py::test_retries_succeed_after_transient_error")
	calls := 0
	res, n, err := retryTransient(t, typesafe.DefaultRetryPolicy(), func() (string, error) {
		calls++
		if calls == 1 {
			return "", providerError(503)
		}
		return "success", nil
	})
	noErr(t, err)
	eq(t, res, "success")
	eq(t, calls, 2)
	eq(t, n, 1)
}

func TestNonRetryableErrorIsNotRetried(t *testing.T) {
	twin(t, "tests/utils/test_error_handling.py::test_non_retryable_error_is_not_retried")
	calls := 0
	_, _, err := retryTransient(t, typesafe.DefaultRetryPolicy(), func() (string, error) { calls++; return "", providerError(400) })
	mustAs[*typesafe.BadRequestError](t, err)
	eq(t, calls, 1)
}

func TestRetriesAreExhaustedAndReasonsRecorded(t *testing.T) {
	twin(t, "tests/utils/test_error_handling.py::test_retries_are_exhausted_and_reasons_recorded")
	model := newScripted(providerError(503))
	b := mustNew(t, Options{Model: model, Retry: fastRetry(2)})
	_, err := evaluate(t, b, "s", answerNoul("q"), nil)
	mustAs[*typesafe.InternalServerError](t, err)
	de := mustAs[*DebugError](t, err)
	eq(t, categories(de.Debug.RetryReasons), []string{"provider_error", "provider_error"})
}

// retryTransient runs fn through a Backend-like transient retry (a helper on the Backend's loop).
func retryTransient(t testing.TB, p typesafe.RetryPolicy, fn func() (string, error)) (string, int, error) {
	t.Helper()
	p.BackoffInitial, p.BackoffJitter = durMS(1), 0
	return runTransient(p, fn)
}
