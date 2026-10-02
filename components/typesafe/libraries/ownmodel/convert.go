package ownmodel

import (
	"math"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// probabilityTolerance is the largest deviation of a probability sum from 1 that is not
// reported as invalid (the oracle's PROBABILITY_TOLERANCE).
const probabilityTolerance = 1e-6

// normalizeConfidence returns probs as a distribution; a zero total gives a uniform one.
func normalizeConfidence(probs []float64) []float64 {
	total := 0.0
	for _, p := range probs {
		total += p
	}
	out := make([]float64, len(probs))
	for i, p := range probs {
		if total == 0 {
			out[i] = 1 / float64(len(probs))
		} else {
			out[i] = p / total
		}
	}
	return out
}

// scoreConfidence measures score concentration around its modal score.
func scoreConfidence(probs []float64) float64 {
	if len(probs) == 1 {
		return 1
	}
	n := normalizeConfidence(probs)
	mode := 0
	for i := range n {
		if n[i] > n[mode] { // the first maximum, like Python's max
			mode = i
		}
	}
	distance := 0.0
	for i, p := range n {
		distance += p * math.Abs(float64(i-mode))
	}
	center := float64(len(n)-1) / 2
	deviation := 0.0
	for i := range n {
		deviation += math.Abs(float64(i) - center)
	}
	deviation /= float64(len(n))
	return math.Max(0, 1-distance/deviation)
}

// choiceConfidence scales peak choice probability from uniform to certainty.
func choiceConfidence(probs []float64) float64 {
	if len(probs) == 1 {
		return 1
	}
	n := normalizeConfidence(probs)
	peak := n[0]
	for _, p := range n {
		peak = math.Max(peak, p)
	}
	uniform := 1 / float64(len(n))
	return (peak - uniform) / (1 - uniform)
}

// probNorm is the probability distribution of one answer, its error against a sum of 1,
// and the original distribution when normalization changed it.
type probNorm struct {
	Order         []string
	Probabilities map[string]float64
	Error         float64
	Original      map[string]float64
}

// rescale rescales probabilities to sum to 1, falling back to uniform for a zero total.
func rescale(order []string, probs map[string]float64) map[string]float64 {
	total := 0.0
	for _, a := range order {
		total += probs[a]
	}
	out := make(map[string]float64, len(order))
	for _, a := range order {
		if total == 0 {
			out[a] = 1 / float64(len(order))
		} else {
			out[a] = probs[a] / total
		}
	}
	return out
}

// normalizeProbabilities builds and optionally rescales a distribution. In discrete mode
// selected is the chosen answer; in probabilities mode probs is the model's distribution.
func normalizeProbabilities(answers []string, probs map[string]float64, selected string, mode AnswerMode, enabled bool) probNorm {
	if mode == Discrete {
		out := make(map[string]float64, len(answers))
		for _, a := range answers {
			out[a] = 0
			if a == selected {
				out[a] = 1
			}
		}
		return probNorm{Order: answers, Probabilities: out}
	}
	original := make(map[string]float64, len(answers))
	total := 0.0
	for _, a := range answers {
		original[a] = probs[a]
		total += probs[a]
	}
	errAmount := math.Abs(total - 1)
	if !enabled || errAmount <= probabilityTolerance {
		return probNorm{Order: answers, Probabilities: original, Error: errAmount}
	}
	return probNorm{Order: answers, Probabilities: rescale(answers, original), Error: errAmount, Original: original}
}

// probDebug is the probability diagnostics of an evaluation.
type probDebug struct {
	MaxError          float64
	InvalidProbs      int
	ProbabilityErrors map[string]float64
	Original          map[string]map[string]float64
}

// probabilityDebugData summarizes normalizations keyed by question name (nil for a Noul).
func probabilityDebugData(norms map[string]*probNorm) probDebug {
	d := probDebug{ProbabilityErrors: map[string]float64{}, Original: map[string]map[string]float64{}}
	for name, n := range norms {
		if n == nil {
			continue
		}
		d.MaxError = math.Max(d.MaxError, n.Error)
		if n.Error > probabilityTolerance {
			d.ProbabilityErrors[name] = n.Error
		}
		if n.Original != nil {
			d.Original[name] = n.Original
		}
	}
	d.InvalidProbs = len(d.ProbabilityErrors)
	return d
}

// convert turns a validated output into typed answers and the normalizations of the
// Score and Choice answers.
func (p *plan) convert(d *decoded, normalize bool) (map[string]typesafe.Answer, map[string]*probNorm, error) {
	answers := make(map[string]typesafe.Answer, len(p.questions))
	norms := make(map[string]*probNorm, len(p.questions))
	for i := range p.questions {
		q := &p.questions[i]
		v := d.answers[q.name]
		norms[q.name] = nil
		switch q.kind {
		case typesafe.TypeNoul:
			prob := v.prob
			if p.mode == Discrete {
				prob = 0
				if v.flag {
					prob = 1
				}
			}
			answers[q.name] = typesafe.NoulAnswer{Noul: prob}
		case typesafe.TypeScore:
			n := normalizeProbabilities(q.labels, v.probs, v.selected, p.mode, normalize)
			norms[q.name] = &n
			// The score is an expected value, meaningful only over a distribution summing to 1:
			// rescale here, whatever normalize says about the reported probabilities.
			dist := rescale(q.labels, n.Probabilities)
			score := 0.0
			ordered := make([]float64, len(q.labels))
			probs := make(map[int]float64, len(q.labels))
			legend := make(map[int]any, len(q.labels))
			for j, label := range q.labels {
				score += float64(j) * dist[label]
				ordered[j] = n.Probabilities[label]
				probs[j] = n.Probabilities[label]
				legend[j] = q.legend[j]
			}
			answers[q.name] = typesafe.ScoreAnswer{Score: score, Confidence: scoreConfidence(ordered), Probabilities: probs, Legend: legend}
		case typesafe.TypeChoice:
			n := normalizeProbabilities(q.labels, v.probs, v.selected, p.mode, normalize)
			norms[q.name] = &n
			best := 0
			ordered := make([]float64, len(q.labels))
			for j, label := range q.labels {
				ordered[j] = n.Probabilities[label]
				if n.Probabilities[label] > n.Probabilities[q.labels[best]] {
					best = j
				}
			}
			answers[q.name] = typesafe.ChoiceAnswer{Choice: q.labels[best], Confidence: choiceConfidence(ordered), Probabilities: n.Probabilities}
		}
	}
	return answers, norms, nil
}
