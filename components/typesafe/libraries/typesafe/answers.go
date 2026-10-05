package typesafe

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Answer is the typed answer to one question: [NoulAnswer], [ChoiceAnswer] or
// [ScoreAnswer]. An answer with an unknown type decodes as [RawAnswer].
type Answer interface {
	// AnswerType returns the wire type of the answer.
	AnswerType() QuestionType
	isAnswer()
}

// NoulAnswer is a yes/no answer.
type NoulAnswer struct {
	// Noul is the probability of a yes answer, from zero to one.
	Noul float64 `json:"noul"`
}

// ChoiceAnswer is a selected label and its probabilities.
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// ScoreAnswer is an expected score with its rubric and probabilities.
type ScoreAnswer struct {
	// Score is the expected score, which may fall between integer rubric levels.
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	// Legend maps a score to the rubric description that was sent for it.
	Legend map[int]any `json:"legend"`
	// Probabilities maps a score to its probability.
	Probabilities map[int]float64 `json:"probabilities"`
}

// RawAnswer is an answer whose type this package does not know; Raw is the JSON as sent.
type RawAnswer struct {
	Type QuestionType
	Raw  json.RawMessage
}

// AnswerType implements [Answer].
func (NoulAnswer) AnswerType() QuestionType { return TypeNoul }

// AnswerType implements [Answer].
func (ChoiceAnswer) AnswerType() QuestionType { return TypeChoice }

// AnswerType implements [Answer].
func (ScoreAnswer) AnswerType() QuestionType { return TypeScore }

// AnswerType implements [Answer].
func (a RawAnswer) AnswerType() QuestionType { return a.Type }
func (NoulAnswer) isAnswer()                 {}
func (ChoiceAnswer) isAnswer()               {}
func (ScoreAnswer) isAnswer()                {}
func (RawAnswer) isAnswer()                  {}

// MarshalJSON writes the wire form with its "type".
func (a NoulAnswer) MarshalJSON() ([]byte, error) {
	type plain NoulAnswer
	return json.Marshal(struct {
		Type QuestionType `json:"type"`
		plain
	}{TypeNoul, plain(a)})
}

// MarshalJSON writes the wire form with its "type".
func (a ChoiceAnswer) MarshalJSON() ([]byte, error) {
	type plain ChoiceAnswer
	return json.Marshal(struct {
		Type QuestionType `json:"type"`
		plain
	}{TypeChoice, plain(a)})
}

// MarshalJSON writes the wire form with its "type".
func (a ScoreAnswer) MarshalJSON() ([]byte, error) {
	type plain ScoreAnswer
	return json.Marshal(struct {
		Type QuestionType `json:"type"`
		plain
	}{TypeScore, plain(a)})
}

// MarshalJSON writes the JSON as it was received.
func (a RawAnswer) MarshalJSON() ([]byte, error) { return a.Raw, nil }

// Usage is the token usage of a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// SystemOneResult is the answers, keyed by question name, with model and usage metadata.
type SystemOneResult struct {
	// Model is the model that answered the request.
	Model   string
	Answers map[string]Answer
	Usage   Usage
}

func (r *SystemOneResult) answer(name string) (Answer, error) {
	a, ok := r.Answers[name]
	if !ok {
		return nil, errorf("No answer named %q in the result.", name)
	}
	return a, nil
}

func wrongType(name string, got Answer, want QuestionType) error {
	return errorf("Answer %q is a %s answer, not a %s answer.", name, got.AnswerType(), want)
}

// Noul returns the yes/no answer named name, or an error when it is absent or of
// another type.
func (r *SystemOneResult) Noul(name string) (NoulAnswer, error) {
	a, err := r.answer(name)
	if err != nil {
		return NoulAnswer{}, err
	}
	v, ok := a.(NoulAnswer)
	if !ok {
		return NoulAnswer{}, wrongType(name, a, TypeNoul)
	}
	return v, nil
}

// Choice returns the choice answer named name, or an error when it is absent or of
// another type.
func (r *SystemOneResult) Choice(name string) (ChoiceAnswer, error) {
	a, err := r.answer(name)
	if err != nil {
		return ChoiceAnswer{}, err
	}
	v, ok := a.(ChoiceAnswer)
	if !ok {
		return ChoiceAnswer{}, wrongType(name, a, TypeChoice)
	}
	return v, nil
}

// Score returns the score answer named name, or an error when it is absent or of
// another type.
func (r *SystemOneResult) Score(name string) (ScoreAnswer, error) {
	a, err := r.answer(name)
	if err != nil {
		return ScoreAnswer{}, err
	}
	v, ok := a.(ScoreAnswer)
	if !ok {
		return ScoreAnswer{}, wrongType(name, a, TypeScore)
	}
	return v, nil
}

// UnmarshalJSON decodes the wire form ({model, answers, usage}); each answer is
// decoded by its "type". Unknown fields are ignored and missing answers are not an error.
func (r *SystemOneResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   Usage                      `json:"usage"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return &TypeSafeError{Message: "Unexpected response shape; expected { model, answers, usage }: " + err.Error(), Cause: err}
	}
	out := SystemOneResult{Model: wire.Model, Usage: wire.Usage, Answers: make(map[string]Answer, len(wire.Answers))}
	for name, raw := range wire.Answers {
		a, err := decodeAnswer(raw)
		if err != nil {
			return &TypeSafeError{Message: fmt.Sprintf("Unexpected shape of the answer %q: %v", name, err), Cause: err}
		}
		out.Answers[name] = a
	}
	*r = out
	return nil
}

func decodeAnswer(raw json.RawMessage) (Answer, error) {
	var head struct {
		Type QuestionType `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case TypeNoul:
		var a NoulAnswer
		return a, json.Unmarshal(raw, &a)
	case TypeChoice:
		var a ChoiceAnswer
		return a, json.Unmarshal(raw, &a)
	case TypeScore:
		var a ScoreAnswer
		return a, json.Unmarshal(raw, &a)
	}
	return RawAnswer{Type: head.Type, Raw: append(json.RawMessage(nil), raw...)}, nil
}

// MarshalJSON encodes the wire form.
func (r SystemOneResult) MarshalJSON() ([]byte, error) {
	names := make([]string, 0, len(r.Answers))
	for n := range r.Answers {
		names = append(names, n)
	}
	sort.Strings(names)
	answers := newObject()
	for _, n := range names {
		answers.value(n, r.Answers[n])
	}
	raw, err := answers.done()
	if err != nil {
		return nil, err
	}
	o := newObject()
	o.value("model", r.Model)
	o.raw("answers", raw)
	o.value("usage", r.Usage)
	return o.done()
}

// ModelCard is the metadata of an available model.
type ModelCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// SystemOneRequest is the state and named questions for SystemOne.
type SystemOneRequest struct {
	// State is text, a JSON object or array, or null.
	State Entry
	// Questions must be non-empty.
	Questions Questions
	// Model overrides the client's default model when non-empty.
	Model string
	// Extra fields are forwarded at the top level of the request body, including nulls.
	// The names state, questions and model are reserved.
	Extra map[string]any
}

// MarshalJSON writes the request body: state, questions, model, then the extra fields.
func (r SystemOneRequest) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.entry("state", r.State)
	o.value("questions", r.Questions)
	if r.Model != "" {
		o.value("model", r.Model)
	}
	names := make([]string, 0, len(r.Extra))
	for n := range r.Extra {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		switch n {
		case "state", "questions", "model":
			return nil, errorf("The extra request field %q collides with a request field of the same name.", n)
		}
		o.value(n, r.Extra[n])
	}
	return o.done()
}
