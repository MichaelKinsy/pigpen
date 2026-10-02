package typesafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// QuestionType names a kind of question on the wire (the "type" field).
type QuestionType string

// The three question kinds.
const (
	TypeNoul   QuestionType = "noul"
	TypeChoice QuestionType = "choice"
	TypeScore  QuestionType = "score"
)

// Question is a yes/no ([NoulQuestion]), named-alternatives ([ChoiceQuestion]) or
// ordered-rubric ([ScoreQuestion]) question. The interface is sealed to this package.
type Question interface {
	// QuestionType returns the wire type of the question.
	QuestionType() QuestionType
	json.Marshaler
	isQuestion()
}

// NoulCriteria describes the yes ("true") and no ("false") outcomes. Each side is
// omitted, null or a description independently. Null makes the whole criteria value
// null on the wire (the TypeScript `noul(x, null)`).
type NoulCriteria struct {
	True  Entry
	False Entry
	Null  bool
}

// MarshalJSON writes null when Null is set, else an object with the sides that are not omitted.
func (c NoulCriteria) MarshalJSON() ([]byte, error) {
	if c.Null {
		return []byte("null"), nil
	}
	o := newObject()
	o.entry("true", c.True)
	o.entry("false", c.False)
	return o.done()
}

// NoulQuestion is a yes/no question. Instructions nil-valued (zero) is omitted from
// the wire; the [Noul] builder sets null, like the TypeScript builder.
type NoulQuestion struct {
	Instructions Entry
	// Criteria is nil when omitted from the request.
	Criteria *NoulCriteria
}

// Noul builds a yes/no question; instructions is text, JSON or nil (null).
func Noul(instructions any) NoulQuestion { return NoulQuestion{Instructions: EntryOf(instructions)} }

func (q NoulQuestion) withCriteria(mutate func(*NoulCriteria)) NoulQuestion {
	c := NoulCriteria{}
	if q.Criteria != nil {
		c = *q.Criteria
	}
	c.Null = false
	mutate(&c)
	q.Criteria = &c
	return q
}

// Yes returns a copy that describes the yes outcome; nil describes it as null.
func (q NoulQuestion) Yes(description any) NoulQuestion {
	return q.withCriteria(func(c *NoulCriteria) { c.True = EntryOf(description) })
}

// No returns a copy that describes the no outcome; nil describes it as null.
func (q NoulQuestion) No(description any) NoulQuestion {
	return q.withCriteria(func(c *NoulCriteria) { c.False = EntryOf(description) })
}

// NullCriteria returns a copy whose criteria are sent as null.
func (q NoulQuestion) NullCriteria() NoulQuestion {
	q.Criteria = &NoulCriteria{Null: true}
	return q
}

// QuestionType implements [Question].
func (NoulQuestion) QuestionType() QuestionType { return TypeNoul }
func (NoulQuestion) isQuestion()                {}

// jsonObject writes a JSON object with its keys in insertion order.
type jsonObject struct {
	buf bytes.Buffer
	n   int
	err error
}

func newObject() *jsonObject {
	o := &jsonObject{}
	o.buf.WriteByte('{')
	return o
}

func (o *jsonObject) key(name string) {
	if o.n > 0 {
		o.buf.WriteByte(',')
	}
	o.n++
	k, _ := marshalPlain(name)
	o.buf.Write(k)
	o.buf.WriteByte(':')
}

func (o *jsonObject) raw(name string, raw []byte) {
	o.key(name)
	o.buf.Write(raw)
}

func (o *jsonObject) value(name string, v any) {
	raw, err := marshalPlain(v)
	if err != nil {
		if o.err == nil {
			o.err = unwrapMarshalError(err)
		}
		raw = []byte("null")
	}
	o.raw(name, raw)
}

func (o *jsonObject) entry(name string, e Entry) {
	if e.IsOmitted() {
		return
	}
	o.value(name, e)
}

func (o *jsonObject) done() ([]byte, error) {
	if o.err != nil {
		return nil, o.err
	}
	o.buf.WriteByte('}')
	return o.buf.Bytes(), nil
}

// MarshalJSON implements [Question].
func (q NoulQuestion) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.value("type", TypeNoul)
	o.entry("instructions", q.Instructions)
	if q.Criteria != nil {
		raw, err := q.Criteria.MarshalJSON()
		if err != nil {
			return nil, err
		}
		o.raw("criteria", raw)
	}
	return o.done()
}

// Option is one choice label with its description (omitted or nil is sent as null).
type Option struct {
	Label       string
	Description Entry
}

// Opt builds an Option; description is text, JSON or nil (null).
func Opt(label string, description any) Option {
	return Option{Label: label, Description: EntryOf(description)}
}

// ChoiceQuestion selects between named alternatives. Options keep their order on the wire.
type ChoiceQuestion struct {
	Instructions Entry
	Criteria     []Option
}

// Choice builds a question that selects between named alternatives.
func Choice(instructions any, options ...Option) ChoiceQuestion {
	return ChoiceQuestion{Instructions: EntryOf(instructions), Criteria: append([]Option(nil), options...)}
}

// QuestionType implements [Question].
func (ChoiceQuestion) QuestionType() QuestionType { return TypeChoice }
func (ChoiceQuestion) isQuestion()                {}

// MarshalJSON implements [Question]; Criteria is written as a JSON object in order.
func (q ChoiceQuestion) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.value("type", TypeChoice)
	o.entry("instructions", q.Instructions)
	sub := newObject()
	for _, opt := range q.Criteria {
		sub.value(opt.Label, opt.Description)
	}
	raw, err := sub.done()
	if err != nil {
		return nil, err
	}
	o.raw("criteria", raw)
	return o.done()
}

// ScoreQuestion assigns a score from an ordered rubric: Criteria[i] describes score i.
// At least two entries are required ([Questions.Validate]).
type ScoreQuestion struct {
	Instructions Entry
	Criteria     []Entry
}

// Score builds a score question; each criterion is text, JSON or nil (null).
func Score(instructions any, criteria ...any) ScoreQuestion {
	q := ScoreQuestion{Instructions: EntryOf(instructions), Criteria: make([]Entry, len(criteria))}
	for i, c := range criteria {
		q.Criteria[i] = EntryOf(c)
	}
	return q
}

// QuestionType implements [Question].
func (ScoreQuestion) QuestionType() QuestionType { return TypeScore }
func (ScoreQuestion) isQuestion()                {}

// MarshalJSON implements [Question].
func (q ScoreQuestion) MarshalJSON() ([]byte, error) {
	o := newObject()
	o.value("type", TypeScore)
	o.entry("instructions", q.Instructions)
	criteria := q.Criteria
	if criteria == nil {
		criteria = []Entry{}
	}
	o.value("criteria", criteria)
	return o.done()
}

// NamedQuestion is a question with the name that identifies its answer.
type NamedQuestion struct {
	Name     string
	Question Question
}

// Ask names a question.
func Ask(name string, q Question) NamedQuestion { return NamedQuestion{Name: name, Question: q} }

// Questions is an ordered set of named questions; the order is kept on the wire and in
// prompts. Names must be unique.
type Questions []NamedQuestion

// Validate rejects an empty set, duplicate names, and score questions with fewer than
// two criteria, before anything is sent (the TypeScript validateQuestions).
func (qs Questions) Validate() error {
	if len(qs) == 0 {
		return errorf("At least one question is required.")
	}
	seen := make(map[string]bool, len(qs))
	for _, nq := range qs {
		if seen[nq.Name] {
			return errorf("Duplicate question name %q.", nq.Name)
		}
		seen[nq.Name] = true
		if nq.Question == nil {
			return errorf("Question %q is nil.", nq.Name)
		}
		if s, ok := nq.Question.(ScoreQuestion); ok && len(s.Criteria) < 2 {
			return errorf("Score question %q has %d criteria; at least two scores are required.", nq.Name, len(s.Criteria))
		}
		if c, ok := nq.Question.(ChoiceQuestion); ok {
			labels := make(map[string]bool, len(c.Criteria))
			for _, opt := range c.Criteria {
				if labels[opt.Label] {
					return errorf("Choice question %q has the label %q twice.", nq.Name, opt.Label)
				}
				labels[opt.Label] = true
			}
		}
	}
	return nil
}

// MarshalJSON writes the set as a JSON object keyed by name, in order.
func (qs Questions) MarshalJSON() ([]byte, error) {
	o := newObject()
	for _, nq := range qs {
		if nq.Question == nil {
			return nil, errorf("Question %q is nil.", nq.Name)
		}
		o.value(nq.Name, nq.Question)
	}
	return o.done()
}

// ParseQuestions decodes the JSON object form (as written by MarshalJSON, or by hand
// or by a tool caller) into an ordered set, keeping key order and the omitted/null
// distinctions. Score criteria must be a list and choice criteria a map, as in the
// TypeScript builders.
func ParseQuestions(data []byte) (Questions, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, &TypeSafeError{Message: "Questions must be a JSON object: " + err.Error(), Cause: err}
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errorf("Questions must be a JSON object keyed by question name.")
	}
	var out Questions
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, &TypeSafeError{Message: "Questions must be a JSON object: " + err.Error(), Cause: err}
		}
		name := keyTok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, &TypeSafeError{Message: fmt.Sprintf("Question %q: %v", name, err), Cause: err}
		}
		if seen[name] {
			return nil, errorf("Duplicate question name %q.", name)
		}
		seen[name] = true
		q, err := parseQuestion(name, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, NamedQuestion{Name: name, Question: q})
	}
	if _, err := dec.Token(); err != nil {
		return nil, &TypeSafeError{Message: "Questions must be a JSON object: " + err.Error(), Cause: err}
	}
	if rest, err := dec.Token(); err != io.EOF {
		return nil, errorf("Unexpected data after the questions object: %v", rest)
	}
	return out, nil
}

func parseQuestion(name string, raw json.RawMessage) (Question, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errorf("Question %q must be a JSON object with a \"type\".", name)
	}
	var qtype string
	if t, ok := fields["type"]; ok {
		if err := json.Unmarshal(t, &qtype); err != nil {
			return nil, errorf("Question %q: \"type\" must be text.", name)
		}
	}
	entry := func(field string) (Entry, error) {
		v, ok := fields[field]
		if !ok {
			return Entry{}, nil
		}
		var e Entry
		if err := json.Unmarshal(v, &e); err != nil {
			return Entry{}, errorf("Question %q: %s must be text, a JSON object, a JSON array or null.", name, field)
		}
		return e, nil
	}
	instructions, err := entry("instructions")
	if err != nil {
		return nil, err
	}
	switch QuestionType(qtype) {
	case TypeNoul:
		q := NoulQuestion{Instructions: instructions}
		if c, ok := fields["criteria"]; ok {
			crit, err := parseNoulCriteria(name, c)
			if err != nil {
				return nil, err
			}
			q.Criteria = crit
		}
		return q, nil
	case TypeChoice:
		opts, err := parseChoiceCriteria(name, fields["criteria"])
		if err != nil {
			return nil, err
		}
		return ChoiceQuestion{Instructions: instructions, Criteria: opts}, nil
	case TypeScore:
		var list []json.RawMessage
		c := bytes.TrimSpace(fields["criteria"])
		if len(c) == 0 || c[0] != '[' {
			if len(c) > 0 && c[0] == '{' {
				return nil, errorf("Question %q: Score criteria must be a list of descriptions indexed by score from zero, not a map.", name)
			}
			return nil, errorf("Question %q: score criteria must be a list of descriptions indexed by score from zero.", name)
		}
		if err := json.Unmarshal(c, &list); err != nil {
			return nil, errorf("Question %q: %v", name, err)
		}
		q := ScoreQuestion{Instructions: instructions, Criteria: make([]Entry, len(list))}
		for i, item := range list {
			if err := json.Unmarshal(item, &q.Criteria[i]); err != nil {
				return nil, errorf("Question %q: criteria[%d] must be text, a JSON object, a JSON array or null.", name, i)
			}
		}
		return q, nil
	case "":
		return nil, errorf("Question %q needs a \"type\": noul, choice or score.", name)
	}
	return nil, errorf("Question %q has unknown type %q.", name, qtype)
}

func parseNoulCriteria(name string, raw json.RawMessage) (*NoulCriteria, error) {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return &NoulCriteria{Null: true}, nil
	}
	var sides map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &sides) != nil {
		return nil, errorf("Question %q: noul criteria must be an object with \"true\" and \"false\", or null.", name)
	}
	c := &NoulCriteria{}
	for side, dst := range map[string]*Entry{"true": &c.True, "false": &c.False} {
		if v, ok := sides[side]; ok {
			if err := json.Unmarshal(v, dst); err != nil {
				return nil, errorf("Question %q: criteria.%s must be text, a JSON object, a JSON array or null.", name, side)
			}
		}
	}
	return c, nil
}

func parseChoiceCriteria(name string, raw json.RawMessage) ([]Option, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		return nil, errorf("Question %q: Choice criteria must be a map of labels to descriptions, not a list.", name)
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errorf("Question %q: choice criteria must be a map of labels to descriptions.", name)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, errorf("Question %q: %v", name, err)
	}
	var opts []Option
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, errorf("Question %q: %v", name, err)
		}
		label := keyTok.(string)
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return nil, errorf("Question %q: %v", name, err)
		}
		if seen[label] {
			return nil, errorf("Question %q has the label %q twice.", name, label)
		}
		seen[label] = true
		var e Entry
		if err := json.Unmarshal(item, &e); err != nil {
			return nil, errorf("Question %q: the description of %q must be text, a JSON object, a JSON array or null.", name, label)
		}
		opts = append(opts, Option{Label: label, Description: e})
	}
	return opts, nil
}

// unwrapMarshalError returns the error a MarshalJSON method returned, without the
// json.MarshalerError layers that encoding/json adds around it.
func unwrapMarshalError(err error) error {
	for {
		var me *json.MarshalerError
		if !errors.As(err, &me) {
			return err
		}
		err = me.Err
	}
}
