package ownmodel

import "testing"

// Added by the mutation check (port/mutations.json: tolerance-loosened): the oracle's tolerance
// is 1e-6, so a deviation just above it is normalized and just below it is left alone.
func TestNormalizationToleranceIsOneMillionth(t *testing.T) {
	answers := []string{"a", "b"}
	over := normalizeProbabilities(answers, map[string]float64{"a": 0.500005, "b": 0.500005}, "", Probabilities, true)
	approx(t, over.Probabilities["a"], 0.5)
	if over.Original == nil {
		t.Fatal("a deviation of 1e-5 must be normalized and its original kept")
	}
	under := normalizeProbabilities(answers, map[string]float64{"a": 0.5000004, "b": 0.5000004}, "", Probabilities, true)
	approx(t, under.Probabilities["a"], 0.5000004)
	if under.Original != nil {
		t.Fatal("a deviation of 8e-7 is within the tolerance")
	}
}

// Added by the mutation check (default-transient-retries-2): the oracle's default is no
// transient retries, so a failing provider is called once unless the caller opts in.
func TestNoTransientRetriesByDefault(t *testing.T) {
	model := newScripted(providerError(500))
	b := mustNew(t, Options{Model: model})
	_, err := evaluate(t, b, "state", answerNoul("positive"), nil)
	if err == nil {
		t.Fatal("expected the provider error")
	}
	eq(t, model.callCount(), 1)
}
