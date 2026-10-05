package pitypesafe

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// DefaultMaxInputBytes is the default UTF-8 JSON byte budget for one evaluation request; the tool and the client share it.
const DefaultMaxInputBytes = 65_536

// DefaultMaxQuestions is the number of questions one request may ask. More than this needs ChunkRequest, which splits and fans out.
const DefaultMaxQuestions = 32

// Request is an admitted evaluation request: state, questions in order, and an optional model id.
type Request struct {
	State     any
	Questions *Object
	// Model is empty when the request names none.
	Model string
}

// PrepareOptions configure admission.
type PrepareOptions struct {
	// MaxInputBytes is the UTF-8 JSON bytes of the serialized request. Default: DefaultMaxInputBytes.
	MaxInputBytes int
}

var usageText = fmt.Sprintf(`Expected { state, questions: { <id>: { type: "choice", instructions, criteria: { label: description|null } } | { type: "score", instructions, criteria: [level0, level1, ...] } | { type: "noul", instructions } } }; 1–%d questions, Choice 1–64 options, Score 2–32 levels.`, DefaultMaxQuestions)

const jsonSafetyMessage = "state and questions must be plain JSON."

// tree returns the request as an *Object tree with the model that was asked for.
func (r *Request) tree() *Object {
	o := NewObject()
	o.Set("state", r.State)
	o.Set("questions", r.Questions)
	if r.Model != "" {
		o.Set("model", r.Model)
	}
	return o
}

// MarshalJSON serializes state, questions and model in that order, questions in their order.
func (r *Request) MarshalJSON() ([]byte, error) { return EncodeJSON(r.tree()) }

// WithModel returns a copy carrying model.
func (r *Request) WithModel(model string) *Request {
	c := *r
	c.Model = model
	return &c
}

// Typed converts the admitted request to the shared client's request type, keeping order.
func (r *Request) Typed() (typesafe.SystemOneRequest, error) {
	var state typesafe.Entry
	switch s := r.State.(type) {
	case nil:
		state = typesafe.Null
	case string:
		state = typesafe.Text(s)
	default:
		raw, err := EncodeJSON(s)
		if err != nil {
			return typesafe.SystemOneRequest{}, err
		}
		state = typesafe.Value(json.RawMessage(raw))
	}
	rawQuestions, err := EncodeJSON(r.Questions)
	if err != nil {
		return typesafe.SystemOneRequest{}, err
	}
	questions, err := typesafe.ParseQuestions(rawQuestions)
	if err != nil {
		return typesafe.SystemOneRequest{}, err
	}
	return typesafe.SystemOneRequest{State: state, Questions: questions, Model: r.Model}, nil
}

// depth limit of the JSON-safety walk.
const maxDepth = 64

func jsonSafe(v any, depth int) bool {
	if depth > maxDepth {
		return false
	}
	switch t := v.(type) {
	case nil, bool, string, float64:
		return true
	case *Object:
		for _, k := range t.keys {
			if !jsonSafe(t.vals[k], depth+1) {
				return false
			}
		}
		return true
	case []any:
		for _, e := range t {
			if !jsonSafe(e, depth+1) {
				return false
			}
		}
		return true
	}
	return false
}

// asTree converts input to a tree, or reports that it is not plain JSON.
func asTree(value any) (any, error) {
	tree, err := FromGo(value)
	if err != nil {
		return nil, errorf(CodeValidation, "Invalid evaluation request: %s %s", jsonSafetyMessage, usageText)
	}
	if obj, ok := tree.(*Object); ok && !jsonSafe(obj, 0) {
		return nil, errorf(CodeValidation, "Invalid evaluation request: %s %s", jsonSafetyMessage, usageText)
	}
	return tree, nil
}

func isEntry(v any) bool {
	switch v.(type) {
	case nil, string, []any, *Object:
		return true
	}
	return false
}

type issues struct{ list []string }

func (i *issues) add(path, message string) {
	if len(i.list) >= 3 {
		return
	}
	if path == "" {
		path = "request"
	}
	if len(path) > 120 {
		path = path[:120]
	}
	i.list = append(i.list, path+": "+message)
}

func (i *issues) full() bool { return len(i.list) >= 3 }

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func validateEntry(is *issues, path string, v any) {
	if !isEntry(v) {
		// TypeBox lists the first three branches of the union it failed.
		is.add(path, "must be string")
		is.add(path, "must be null")
		is.add(path, "must be array")
	}
}

func validateQuestion(is *issues, path string, v any) {
	q, ok := v.(*Object)
	if !ok {
		// TypeBox reports each of the three branches.
		is.add(path, "must be object")
		is.add(path, "must be object")
		is.add(path, "must be object")
		return
	}
	typeValue, _ := q.Get("type")
	kind, _ := typeValue.(string)
	var allowed map[string]bool
	switch kind {
	case "noul":
		allowed = map[string]bool{"type": true, "instructions": true, "criteria": true}
	case "choice", "score":
		allowed = map[string]bool{"type": true, "instructions": true, "criteria": true}
	default:
		is.add(join(path, "type"), "must be one of noul, choice, score")
		return
	}
	for _, k := range q.keys {
		if !allowed[k] {
			is.add(join(path, k), "must not have additional properties")
		}
	}
	if ins, ok := q.Get("instructions"); ok {
		validateEntry(is, join(path, "instructions"), ins)
	}
	criteria, hasCriteria := q.Get("criteria")
	cpath := join(path, "criteria")
	switch kind {
	case "noul":
		if !hasCriteria || criteria == nil {
			return
		}
		obj, ok := criteria.(*Object)
		if !ok {
			is.add(cpath, "must be null or object")
			return
		}
		for _, k := range obj.keys {
			if k != "true" && k != "false" {
				is.add(join(cpath, k), "must not have additional properties")
				continue
			}
			validateEntry(is, join(cpath, k), obj.vals[k])
		}
	case "choice":
		if !hasCriteria {
			is.add(path, "must have required property 'criteria'")
			return
		}
		obj, ok := criteria.(*Object)
		if !ok {
			is.add(cpath, "must be object")
			return
		}
		if obj.Len() < 1 || obj.Len() > 64 {
			is.add(cpath, countMessage(obj.Len(), 1, 64, "properties"))
			return
		}
		for _, k := range obj.keys {
			if n := utf16Len(k); n < 1 || n > 200 {
				is.add(join(cpath, k), countMessage(n, 1, 200, "characters"))
				continue
			}
			validateEntry(is, join(cpath, k), obj.vals[k])
		}
	case "score":
		if !hasCriteria {
			is.add(path, "must have required property 'criteria'")
			return
		}
		arr, ok := criteria.([]any)
		if !ok {
			is.add(cpath, "must be array")
			return
		}
		if len(arr) < 2 || len(arr) > 32 {
			is.add(cpath, countMessage(len(arr), 2, 32, "items"))
			return
		}
		for i, e := range arr {
			validateEntry(is, fmt.Sprintf("%s.%d", cpath, i), e)
		}
	}
}

// validateRequest returns up to three "path: message" issues; paths and messages only, never submitted values.
func validateRequest(v any) []string {
	is := &issues{}
	req, ok := v.(*Object)
	if !ok {
		is.add("", "must be object")
		return is.list
	}
	extra := false
	for _, k := range req.keys {
		if k != "state" && k != "questions" && k != "model" {
			is.add(k, "schema is false")
			extra = true
		}
	}
	if extra {
		is.add("", "must not have additional properties")
	}
	state, hasState := req.Get("state")
	qv, hasQuestions := req.Get("questions")
	var missing []string
	if !hasState {
		missing = append(missing, "state")
	}
	if !hasQuestions {
		missing = append(missing, "questions")
	}
	if len(missing) > 0 {
		is.add("", "must have required properties "+strings.Join(missing, ", "))
	}
	if hasState {
		validateEntry(is, "state", state)
	}
	if hasQuestions {
		validateQuestions(is, qv)
	}
	if m, ok := req.Get("model"); ok {
		s, isStr := m.(string)
		if !isStr {
			is.add("model", "must be string")
		} else if n := utf16Len(s); n < 1 || n > 100 {
			is.add("model", countMessage(n, 1, 100, "characters"))
		}
	}
	return is.list
}

func validateQuestions(is *issues, qv any) {
	if qs, ok := qv.(*Object); !ok {
		is.add("questions", "must be object")
	} else {
		if qs.Len() < 1 || qs.Len() > DefaultMaxQuestions {
			is.add("questions", countMessage(qs.Len(), 1, DefaultMaxQuestions, "properties"))
		}
		for _, id := range qs.keys {
			if n := utf16Len(id); n < 1 || n > 100 {
				is.add("questions", countMessage(n, 1, 100, "characters"))
				continue
			}
			validateQuestion(is, join("questions", id), qs.vals[id])
			if is.full() {
				break
			}
		}
	}
}

// ParseEvaluationRequest validates without including submitted content in validation errors. Prefer
// PrepareEvaluationRequest, which also accepts near-misses and enforces the byte budget.
func ParseEvaluationRequest(value any) (*Request, error) {
	tree, err := asTree(value)
	if err != nil {
		return nil, err
	}
	if problems := validateRequest(tree); len(problems) > 0 {
		return nil, errorf(CodeValidation, "Invalid evaluation request at %s. %s", strings.Join(problems, "; "), usageText)
	}
	req := tree.(*Object)
	state, _ := req.Get("state")
	questions, _ := req.Get("questions")
	out := &Request{State: state, Questions: questions.(*Object)}
	if m, ok := req.Get("model"); ok {
		out.Model = m.(string)
	}
	return out, nil
}

// NormalizeEvaluationRequest accepts common near-misses from language models without loosening the schema
// itself. Prefer PrepareEvaluationRequest, which applies this before validating. Input that is not a request
// object with a questions object is returned unchanged.
func NormalizeEvaluationRequest(value any) any {
	tree, err := FromGo(value)
	if err != nil {
		return value
	}
	request, ok := tree.(*Object)
	if !ok {
		return tree
	}
	qv, _ := request.Get("questions")
	questions, ok := qv.(*Object)
	if !ok {
		return tree
	}
	normalized := NewObject()
	for _, id := range questions.keys {
		question, ok := questions.vals[id].(*Object)
		if !ok {
			normalized.Set(id, questions.vals[id])
			continue
		}
		item := NewObject()
		for _, k := range question.keys {
			if k == "options" || k == "levels" || k == "choices" {
				continue
			}
			item.Set(k, question.vals[k])
		}
		if !item.Has("criteria") {
			options, _ := question.Get("options")
			levels, _ := question.Get("levels")
			choices, hasChoices := question.Get("choices")
			var alias any
			present := false
			switch {
			case options != nil:
				alias, present = options, true
			case levels != nil:
				alias, present = levels, true
			case hasChoices:
				alias, present = choices, true
			}
			if present {
				item.Set("criteria", alias)
			}
		}
		kind, _ := item.vals["type"].(string)
		criteria, _ := item.Get("criteria")
		if arr, ok := criteria.([]any); ok && kind == "choice" {
			labels := NewObject()
			all := true
			for _, l := range arr {
				if s, ok := l.(string); !ok || s == "" {
					all = false
					break
				}
			}
			if all {
				for _, l := range arr {
					labels.Set(l.(string), nil)
				}
				item.Set("criteria", labels)
			}
		}
		if s, ok := criteria.(string); ok && kind == "noul" {
			c := NewObject()
			c.Set("true", s)
			item.Set("criteria", c)
		}
		normalized.Set(id, item)
	}
	out := request.Clone()
	out.Set("questions", normalized)
	return out
}

// AssertWithinByteLimit measures the serialized request and names the configured limit: one byte rule for every limit check.
func AssertWithinByteLimit(text []byte, maxInputBytes int) error {
	if len(text) > maxInputBytes {
		return errorf(CodeValidation, "Evaluation exceeds the %d-byte input limit.", maxInputBytes)
	}
	return nil
}

// PrepareEvaluationRequest is the one admission rule: normalize known near-miss aliases, validate the schema
// and JSON-safety, then enforce the byte budget, always in that order. The tool, the playground, and Evaluate
// all pass through here, so what one accepts the others accept.
func PrepareEvaluationRequest(value any, opts PrepareOptions) (*Request, error) {
	tree, err := asTree(value)
	if err != nil {
		return nil, err
	}
	req, err := ParseEvaluationRequest(NormalizeEvaluationRequest(tree))
	if err != nil {
		return nil, err
	}
	limit := opts.MaxInputBytes
	if limit == 0 {
		limit = DefaultMaxInputBytes
	}
	body, err := req.MarshalJSON()
	if err != nil {
		return nil, errorf(CodeValidation, "Invalid evaluation request: %s", jsonSafetyMessage)
	}
	if err := AssertWithinByteLimit(body, limit); err != nil {
		return nil, err
	}
	return req, nil
}

//go:embed evaluation_schema.json
var evaluationSchemaJSON []byte

// EvaluationSchema is the JSON Schema used by the tool and the programmatic interface: exactly what the
// original's TypeBox schema serializes to (generated from port/oracle/src/schema.ts, see port/PORT.md). Every field the agent authors says what it means, because
// the schema is the only shape guidance the model gets before its first call. Each call returns a fresh copy.
func EvaluationSchema() map[string]any {
	var schema map[string]any
	if err := json.Unmarshal(evaluationSchemaJSON, &schema); err != nil {
		panic("pitypesafe: embedded evaluation schema is invalid: " + err.Error())
	}
	return schema
}

// countMessage is TypeBox's wording for a size outside its bounds.
func countMessage(n, minimum, maximum int, unit string) string {
	if n < minimum {
		return fmt.Sprintf("must not have fewer than %d %s", minimum, unit)
	}
	return fmt.Sprintf("must not have more than %d %s", maximum, unit)
}
