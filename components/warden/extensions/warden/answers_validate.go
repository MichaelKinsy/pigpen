package warden

import (
	"math"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// The answer check pi-typesafe's client makes before it hands a response to its caller (client.js `validResult`): a
// response whose answers are not exactly the questions asked, in the types asked, with the probabilities the types
// carry, is not a judgment. The shared Go client accepts such a response (missing answers are not an error there), so
// the TypeSafe judge applies the original's check itself and fails the request as the original does: code "response",
// the original's message.

// unexpectedAnswerMessage is the original's text for a response that fails the check.
const unexpectedAnswerMessage = "TypeSafe returned an unexpected answer or usage format."

func probability(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }

// validResult reports whether res answers exactly the asked questions.
func validResult(res *typesafe.SystemOneResult, questions Questions) bool {
	if res == nil || res.Model == "" || res.Answers == nil {
		return false
	}
	if res.Usage.InputTokens < 0 || res.Usage.OutputTokens < 0 {
		return false
	}
	if len(res.Answers) != len(questions) {
		return false
	}
	for id, q := range questions {
		answer, ok := res.Answers[id]
		if !ok || answer == nil {
			return false
		}
		switch a := answer.(type) {
		case typesafe.NoulAnswer:
			if q.Type != "noul" || !probability(a.Noul) {
				return false
			}
		case *typesafe.NoulAnswer:
			if a == nil || q.Type != "noul" || !probability(a.Noul) {
				return false
			}
		case typesafe.ChoiceAnswer:
			if !validChoice(a, q) {
				return false
			}
		case *typesafe.ChoiceAnswer:
			if a == nil || !validChoice(*a, q) {
				return false
			}
		case typesafe.ScoreAnswer:
			if !validScore(a, q) {
				return false
			}
		case *typesafe.ScoreAnswer:
			if a == nil || !validScore(*a, q) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validChoice(a typesafe.ChoiceAnswer, q Question) bool {
	if q.Type != "choice" || !probability(a.Confidence) || a.Probabilities == nil || len(a.Probabilities) != len(q.Criteria) {
		return false
	}
	for label := range q.Criteria {
		p, ok := a.Probabilities[label]
		if !ok || !probability(p) {
			return false
		}
	}
	_, ok := q.Criteria[a.Choice]
	return ok
}

func validScore(a typesafe.ScoreAnswer, q Question) bool {
	levels := len(q.Levels)
	if q.Type != "score" || !probability(a.Confidence) || a.Probabilities == nil || len(a.Probabilities) != levels {
		return false
	}
	for i := range levels {
		p, ok := a.Probabilities[i]
		if !ok || !probability(p) {
			return false
		}
	}
	return !math.IsNaN(a.Score) && !math.IsInf(a.Score, 0) && a.Score >= 0 && a.Score <= float64(levels-1) && a.Legend != nil
}
