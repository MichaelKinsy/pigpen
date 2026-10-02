package jev

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// A question, in the shape of the original's JevQuestion. Questions travel as an
// ordered list because the wire order is part of the request.
type question struct {
	ID           string
	Type         string // noul, choice, score
	Instructions string
	// noul
	CriteriaTrue, CriteriaFalse string
	// choice: option names in order, description nil for "no detail".
	Options []option
	// score: ordered rubric levels, lowest first.
	Levels []string
}

type option struct {
	Name        string
	Description *string
}

func desc(s string) *string { return &s }

// answer is one typed answer.
type answer struct {
	Type          string
	Noul          float64
	Choice        string
	Score         float64
	Confidence    float64
	HasConfidence bool
	Probabilities []probability  // choice or score, sorted by name
	Legend        map[string]any // score: rubric text by level, as the judge sent it
}

type probability struct {
	Name  string
	Value float64
}

type usage struct {
	Input, Output float64
}

type response struct {
	Model   string
	Answers map[string]answer
	Usage   *usage
}

// fromResult validates a judge's result against the asked questions and converts
// it to the extension's answer types. A result that skips a question, answers with
// another type or a value outside its range is an error, never a zero (0.2.2 fix,
// and PORT.md C5).
func fromResult(res *typesafe.SystemOneResult, qs []question) (*response, error) {
	if res == nil {
		return nil, fmt.Errorf("response was not an object")
	}
	if res.Answers == nil {
		return nil, fmt.Errorf("response is missing the answers map")
	}
	out := &response{Model: res.Model, Answers: map[string]answer{}}
	if res.Usage.InputTokens != 0 || res.Usage.OutputTokens != 0 {
		out.Usage = &usage{Input: float64(res.Usage.InputTokens), Output: float64(res.Usage.OutputTokens)}
	}
	for _, q := range qs {
		raw, present := res.Answers[q.ID]
		if !present {
			return nil, fmt.Errorf("response did not answer %q", q.ID)
		}
		a, err := convertAnswer(q, raw)
		if err != nil {
			return nil, err
		}
		out.Answers[q.ID] = a
	}
	return out, nil
}

func inRatio(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }

func convertAnswer(q question, raw typesafe.Answer) (answer, error) {
	if raw == nil || string(raw.AnswerType()) != q.Type {
		return answer{}, fmt.Errorf("answer %q is not a %s", q.ID, q.Type)
	}
	a := answer{Type: q.Type}
	switch t := raw.(type) {
	case typesafe.NoulAnswer:
		if !inRatio(t.Noul) {
			return answer{}, fmt.Errorf("answer %q noul %v is outside 0 to 1", q.ID, t.Noul)
		}
		a.Noul = t.Noul
	case typesafe.ChoiceAnswer:
		if !inRatio(t.Confidence) {
			return answer{}, fmt.Errorf("answer %q confidence %v is outside 0 to 1", q.ID, t.Confidence)
		}
		found := false
		for _, o := range q.Options {
			found = found || o.Name == t.Choice
		}
		if !found {
			return answer{}, fmt.Errorf("answer %q chose %q, which is not one of its options", q.ID, t.Choice)
		}
		a.Choice, a.Confidence, a.HasConfidence = t.Choice, t.Confidence, true
		for _, k := range sortedKeys(t.Probabilities) {
			a.Probabilities = append(a.Probabilities, probability{k, t.Probabilities[k]})
		}
	case typesafe.ScoreAnswer:
		if !inRatio(t.Confidence) {
			return answer{}, fmt.Errorf("answer %q confidence %v is outside 0 to 1", q.ID, t.Confidence)
		}
		if math.IsNaN(t.Score) || t.Score < 0 || t.Score > float64(len(q.Levels)-1) {
			return answer{}, fmt.Errorf("answer %q score %v is outside the %d levels", q.ID, t.Score, len(q.Levels))
		}
		a.Score, a.Confidence, a.HasConfidence = t.Score, t.Confidence, true
		a.Legend = map[string]any{}
		for k, v := range t.Legend {
			a.Legend[strconv.Itoa(k)] = v
		}
		for k, v := range t.Probabilities {
			a.Probabilities = append(a.Probabilities, probability{strconv.Itoa(k), v})
		}
		sort.Slice(a.Probabilities, func(i, j int) bool { return a.Probabilities[i].Name < a.Probabilities[j].Name })
	default:
		return answer{}, fmt.Errorf("answer %q has an unknown shape", q.ID)
	}
	return a, nil
}

// toTypeSafe converts the questions to the shared client's, in order.
func toTypeSafe(qs []question) typesafe.Questions {
	out := make(typesafe.Questions, 0, len(qs))
	for _, q := range qs {
		switch q.Type {
		case "noul":
			nq := typesafe.Noul(q.Instructions)
			if q.CriteriaTrue != "" || q.CriteriaFalse != "" {
				nq = nq.Yes(q.CriteriaTrue).No(q.CriteriaFalse)
			}
			out = append(out, typesafe.Ask(q.ID, nq))
		case "choice":
			opts := make([]typesafe.Option, len(q.Options))
			for i, o := range q.Options {
				var d any
				if o.Description != nil {
					d = *o.Description
				}
				opts[i] = typesafe.Opt(o.Name, d)
			}
			out = append(out, typesafe.Ask(q.ID, typesafe.Choice(q.Instructions, opts...)))
		case "score":
			levels := make([]any, len(q.Levels))
			for i, l := range q.Levels {
				levels[i] = l
			}
			out = append(out, typesafe.Ask(q.ID, typesafe.Score(q.Instructions, levels...)))
		}
	}
	return out
}

// describeAnswer is the compact one-line rendering for notifications.
func describeAnswer(a *answer) string {
	if a == nil {
		return "no answer"
	}
	switch a.Type {
	case "noul":
		return "yes " + toFixed2(a.Noul)
	case "choice":
		return fmt.Sprintf("%s (conf %s)", a.Choice, toFixed2(a.Confidence))
	}
	levels := len(a.Legend) - 1
	suffix := ""
	if levels > 0 {
		suffix = fmt.Sprintf("/%d", levels)
	}
	return fmt.Sprintf("%s%s (conf %s)", toFixed2(a.Score), suffix, toFixed2(a.Confidence))
}

// describeAnswers is the one-line dump for /jev last.
func describeAnswers(r *response, qs []question) string {
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		a := r.Answers[q.ID]
		parts = append(parts, q.ID+"="+describeAnswer(&a))
	}
	return strings.Join(parts, " ")
}

func formatChoice(a answer) string {
	ranked := append([]probability(nil), a.Probabilities...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Value > ranked[j].Value })
	parts := make([]string, len(ranked))
	for i, p := range ranked {
		parts[i] = p.Name + " " + toFixed2(p.Value)
	}
	return fmt.Sprintf("%s (conf %s) [%s]", a.Choice, toFixed2(a.Confidence), strings.Join(parts, ", "))
}

// validateQuestions fails early with a useful message instead of paying for a 422.
func validateQuestions(qs []question) error {
	if len(qs) == 0 {
		return fmt.Errorf("no questions provided")
	}
	seen := map[string]bool{}
	for _, q := range qs {
		if strings.TrimSpace(q.Instructions) == "" {
			return fmt.Errorf("question %q: instructions are required", q.ID)
		}
		if seen[q.ID] {
			return fmt.Errorf("duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
	}
	return nil
}
