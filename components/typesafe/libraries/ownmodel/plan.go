package ownmodel

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// The per-request plan of the Python oracle (_schema.py): the answer schema and the prompt
// descriptions derived from the questions, and the strict validation of the output. Where
// the oracle builds Pydantic models per request, this builds the same JSON Schema by hand
// and validates the parsed output against the same rules.

const noDescription = "No additional instructions."

// qplan is one question prepared for prompting, schema generation and decoding.
type qplan struct {
	name  string
	index int
	kind  typesafe.QuestionType
	// question is the prompt text of the instructions.
	instructions string
	// noul
	hasNoulCriteria     bool
	trueDesc, falseDesc string
	// choice and score: one label and one prompt description per option; for a score the
	// labels are "0", "1", ... and legend holds the JSON of each criterion.
	labels []string
	descs  []string
	legend []any
}

type plan struct {
	mode      AnswerMode
	questions []qplan
}

// serialize renders an instruction or description value for a prompt: plain text as is,
// null or omitted as a fixed phrase, JSON compactly (the oracle's
// _serialize_instruction_value_for_prompt).
func serialize(e typesafe.Entry) (string, error) {
	if e.IsOmitted() || e.IsNull() {
		return noDescription, nil
	}
	raw, err := e.MarshalJSON()
	if err != nil {
		return "", err
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return validUTF8(s), nil
	}
	return canonicalJSON(raw)
}

func (p *plan) find(name string) *qplan {
	for i := range p.questions {
		if p.questions[i].name == name {
			return &p.questions[i]
		}
	}
	return nil
}

// newPlan validates the questions for own-model evaluation (non-empty, unique names,
// at least two criteria for score and choice questions, descriptions that encode).
func newPlan(qs typesafe.Questions, mode AnswerMode) (*plan, error) {
	if err := qs.Validate(); err != nil {
		return nil, err
	}
	p := &plan{mode: mode}
	for i, nq := range qs {
		q := qplan{name: nq.Name, index: i, kind: nq.Question.QuestionType()}
		var err error
		fail := func(e error) error {
			return &typesafe.TypeSafeError{Message: fmt.Sprintf("Question %q: %v", nq.Name, e), Cause: e}
		}
		switch v := nq.Question.(type) {
		case typesafe.NoulQuestion:
			if q.instructions, err = serialize(v.Instructions); err != nil {
				return nil, fail(err)
			}
			if c := v.Criteria; c != nil && !c.Null {
				q.hasNoulCriteria = true
				if q.trueDesc, err = serialize(c.True); err != nil {
					return nil, fail(err)
				}
				if q.falseDesc, err = serialize(c.False); err != nil {
					return nil, fail(err)
				}
			}
		case typesafe.ChoiceQuestion:
			if len(v.Criteria) < 2 {
				return nil, &typesafe.TypeSafeError{Message: fmt.Sprintf("Question %q: score and choice questions require at least two criteria.", nq.Name)}
			}
			if q.instructions, err = serialize(v.Instructions); err != nil {
				return nil, fail(err)
			}
			for _, o := range v.Criteria {
				d, err := serialize(o.Description)
				if err != nil {
					return nil, fail(err)
				}
				q.labels = append(q.labels, o.Label)
				q.descs = append(q.descs, d)
			}
		case typesafe.ScoreQuestion:
			if q.instructions, err = serialize(v.Instructions); err != nil {
				return nil, fail(err)
			}
			for j, c := range v.Criteria {
				d, err := serialize(c)
				if err != nil {
					return nil, fail(err)
				}
				q.labels = append(q.labels, strconv.Itoa(j))
				q.descs = append(q.descs, d)
				q.legend = append(q.legend, c.Data())
			}
		default:
			return nil, &typesafe.TypeSafeError{Message: fmt.Sprintf("Question %q has an unsupported type %T.", nq.Name, nq.Question)}
		}
		p.questions = append(p.questions, q)
	}
	return p, nil
}

// questionDescription is _build_llm_output_question_description.
func (p *plan) questionDescription(q *qplan) string {
	if p.mode == Probabilities {
		switch q.kind {
		case typesafe.TypeNoul:
			return "Probability that the answer is yes or the assertion is true. 0 means no or false, 0.5 means uncertain, and 1 means yes or true.\nQuestion: " + q.instructions
		case typesafe.TypeScore:
			return "Each property maps a rubric level to the probability that the document matches it.\nQuestion: " + q.instructions
		case typesafe.TypeChoice:
			return "Each property maps an option to the probability that it is the best answer.\nQuestion: " + q.instructions
		}
	}
	return q.instructions
}

func (p *plan) optionLines(q *qplan) string {
	lines := make([]string, len(q.labels))
	for i := range q.labels {
		lines[i] = q.labels[i] + " = " + q.descs[i]
	}
	return strings.Join(lines, "\n")
}

// fieldDescription is _build_llm_output_field_description.
func (p *plan) fieldDescription(q *qplan) string {
	d := p.questionDescription(q)
	switch q.kind {
	case typesafe.TypeScore:
		if p.mode == Discrete {
			return d + "\nScore levels, answer with the integer:\n" + p.optionLines(q)
		}
		return d + "\nRequired probability keys:\n" + p.optionLines(q)
	case typesafe.TypeChoice:
		if p.mode == Discrete {
			return d + "\nChoice labels, answer with one label:\n" + p.optionLines(q)
		}
		return d + "\nRequired probability keys:\n" + p.optionLines(q)
	}
	if !q.hasNoulCriteria {
		return d
	}
	return d + "\nTrue criteria: " + q.trueDesc + "\nFalse criteria: " + q.falseDesc
}

// build returns the schema as an ordered object, keys sorted like pydantic's output
// (properties keep the question order).
func (p *plan) build() obj {
	defs := map[string]obj{}
	var props obj
	var required []string
	for i := range p.questions {
		q := &p.questions[i]
		required = append(required, q.name)
		switch {
		case q.kind == typesafe.TypeNoul && p.mode == Discrete:
			props = append(props, kv{q.name, obj{{"description", p.fieldDescription(q)}, {"type", "boolean"}}})
		case q.kind == typesafe.TypeNoul:
			props = append(props, kv{q.name, obj{{"description", p.fieldDescription(q)}, {"type", "number"}}})
		case q.kind == typesafe.TypeScore && p.mode == Discrete:
			props = append(props, kv{q.name, obj{{"description", p.fieldDescription(q)}, {"type", "integer"}}})
		case q.kind == typesafe.TypeChoice && p.mode == Discrete:
			props = append(props, kv{q.name, obj{{"description", p.fieldDescription(q)}, {"enum", append([]string(nil), q.labels...)}, {"type", "string"}}})
		default: // a probability map per label
			name := "ProbabilityMap" + strconv.Itoa(q.index)
			var pm obj
			for j, label := range q.labels {
				pm = append(pm, kv{label, obj{{"description", q.descs[j]}, {"type", "number"}}})
			}
			defs[name] = obj{
				{"additionalProperties", false},
				{"description", p.questionDescription(q)},
				{"properties", pm},
				{"required", append([]string(nil), q.labels...)},
				{"type", "object"},
			}
			props = append(props, kv{q.name, obj{{"$ref", "#/$defs/" + name}}})
		}
	}
	defs["TypeSafeAnswers"] = obj{
		{"additionalProperties", false},
		{"description", "Exactly one answer per property below. Use these property names verbatim and do not add, rename, or nest them under any other key."},
		{"properties", props},
		{"required", required},
		{"type", "object"},
	}
	var defList obj
	for _, name := range sortedKeys(defs) {
		defList = append(defList, kv{name, defs[name]})
	}
	return obj{
		{"$defs", defList},
		{"additionalProperties", false},
		{"properties", obj{{"answers", obj{{"$ref", "#/$defs/TypeSafeAnswers"}}}}},
		{"required", []string{"answers"}},
		{"type", "object"},
	}
}

// schema returns the JSON Schema of the model's answer as a generic map.
func (p *plan) schema() map[string]any { return toMap(p.build()).(map[string]any) }

// schemaJSON returns the schema as compact JSON in the key order of the Python oracle.
func (p *plan) schemaJSON() string {
	var b strings.Builder
	writeAny(&b, p.build())
	return b.String()
}

// value is one validated answer.
type value struct {
	prob     float64            // noul, probabilities
	flag     bool               // noul, discrete
	probs    map[string]float64 // choice and score, probabilities
	selected string             // choice, discrete: the label; score, discrete: the level as text
}

// decoded is a validated model output.
type decoded struct {
	answers map[string]value
}

type fieldError struct{ loc, msg string }

// validationError lists why an output does not match the schema.
type validationError struct{ errs []fieldError }

func (e *validationError) Error() string {
	var b strings.Builder
	n := len(e.errs)
	fmt.Fprintf(&b, "%d validation error", n)
	if n != 1 {
		b.WriteByte('s')
	}
	b.WriteString(" for TypeSafeEvaluation")
	for _, f := range e.errs {
		b.WriteString("\n" + f.loc + "\n  " + f.msg)
	}
	return b.String()
}

// extractJSON strips the Markdown fence a prompted model may wrap around the JSON object.
func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = text[3:]
		if len(text) >= 4 && strings.EqualFold(text[:4], "json") {
			text = text[4:]
		}
		text = strings.TrimSpace(text)
		if strings.HasSuffix(text, "```") {
			text = strings.TrimSpace(text[:len(text)-3])
		}
	}
	return text
}

var intLiteral = regexp.MustCompile(`^-?\d+$`)

// decode strips Markdown fences from text and validates it strictly against the schema.
func (p *plan) decode(text string) (*decoded, error) {
	dec := json.NewDecoder(strings.NewReader(extractJSON(text)))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("Invalid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("Invalid JSON: trailing characters after the JSON object")
	}
	var errs []fieldError
	bad := func(loc, msg string) { errs = append(errs, fieldError{loc, msg}) }
	top, ok := root.(map[string]any)
	if !ok {
		return nil, &validationError{[]fieldError{{"", "Input should be a valid dictionary or object"}}}
	}
	for _, k := range sortedKeys(top) {
		if k != "answers" {
			bad(k, "Extra inputs are not permitted")
		}
	}
	rawAnswers, present := top["answers"]
	if !present {
		bad("answers", "Field required")
	}
	out := &decoded{answers: map[string]value{}}
	if present {
		answers, ok := rawAnswers.(map[string]any)
		if !ok {
			bad("answers", "Input should be a valid dictionary or object")
		} else {
			known := map[string]bool{}
			for i := range p.questions {
				q := &p.questions[i]
				known[q.name] = true
				raw, has := answers[q.name]
				loc := "answers." + q.name
				if !has {
					bad(loc, "Field required")
					continue
				}
				v, ferrs := p.validateAnswer(q, raw, loc)
				errs = append(errs, ferrs...)
				out.answers[q.name] = v
			}
			for _, k := range sortedKeys(answers) {
				if !known[k] {
					bad("answers."+k, "Extra inputs are not permitted")
				}
			}
		}
	}
	if len(errs) > 0 {
		return nil, &validationError{errs}
	}
	return out, nil
}

func (p *plan) validateAnswer(q *qplan, raw any, loc string) (value, []fieldError) {
	var errs []fieldError
	bad := func(loc, msg string) { errs = append(errs, fieldError{loc, msg}) }
	var v value
	switch {
	case q.kind == typesafe.TypeNoul && p.mode == Discrete:
		b, ok := raw.(bool)
		if !ok {
			bad(loc, "Input should be a valid boolean")
		}
		v.flag = b
	case q.kind == typesafe.TypeNoul:
		v.prob, errs = probability(raw, loc)
	case q.kind == typesafe.TypeScore && p.mode == Discrete:
		n, ok := raw.(json.Number)
		if !ok || !intLiteral.MatchString(n.String()) {
			bad(loc, "Input should be a valid integer")
			break
		}
		level, err := strconv.Atoi(n.String())
		switch {
		case err != nil || level < 0:
			bad(loc, "Input should be greater than or equal to 0")
		case level >= len(q.labels):
			bad(loc, fmt.Sprintf("Input should be less than %d", len(q.labels)))
		}
		v.selected = strconv.Itoa(level)
	case q.kind == typesafe.TypeChoice && p.mode == Discrete:
		s, ok := raw.(string)
		if !ok {
			bad(loc, "Input should be a valid string")
			break
		}
		found := false
		for _, l := range q.labels {
			if l == s {
				found = true
			}
		}
		if !found {
			bad(loc, "Input should be one of the allowed labels: "+strings.Join(q.labels, ", "))
		}
		v.selected = s
	default: // a probability per label
		m, ok := raw.(map[string]any)
		if !ok {
			bad(loc, "Input should be a valid dictionary or object")
			break
		}
		v.probs = map[string]float64{}
		known := map[string]bool{}
		for _, l := range q.labels {
			known[l] = true
			item, has := m[l]
			if !has {
				bad(loc+"."+l, "Field required")
				continue
			}
			f, ferrs := probability(item, loc+"."+l)
			errs = append(errs, ferrs...)
			v.probs[l] = f
		}
		for _, k := range sortedKeys(m) {
			if !known[k] {
				bad(loc+"."+k, "Extra inputs are not permitted")
			}
		}
	}
	return v, errs
}

// probability validates a strict float in [0, 1]: a JSON number, not a boolean or a string.
func probability(raw any, loc string) (float64, []fieldError) {
	n, ok := raw.(json.Number)
	if !ok {
		return 0, []fieldError{{loc, "Input should be a valid number"}}
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, []fieldError{{loc, "Input should be a finite number"}}
	}
	switch {
	case f < 0:
		return f, []fieldError{{loc, "Input should be greater than or equal to 0"}}
	case f > 1:
		return f, []fieldError{{loc, "Input should be less than or equal to 1"}}
	}
	return f, nil
}
