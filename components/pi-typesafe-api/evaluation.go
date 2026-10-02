package pitypesafe

import (
	"encoding/json"
	"sort"
)

// MarshalJSON encodes the evaluation the way the original's JSON.stringify(result) does: model, answers
// (in the order the questions were asked), usage, then elapsedMs.
func (e *Evaluation) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(e.SystemOneResult)
	if err != nil {
		return nil, err
	}
	tree, err := ParseJSON(raw)
	if err != nil {
		return nil, err
	}
	source, ok := tree.(*Object)
	if !ok {
		return nil, newError(CodeResponse, "TypeSafe returned an unreadable or unexpected response.")
	}
	out := NewObject()
	if v, ok := source.Get("model"); ok {
		out.Set("model", v)
	}
	if v, ok := source.Get("answers"); ok {
		out.Set("answers", e.orderAnswers(v))
	}
	if v, ok := source.Get("usage"); ok {
		out.Set("usage", v)
	}
	for _, k := range source.Keys() {
		if !out.Has(k) {
			out.Set(k, source.vals[k])
		}
	}
	out.Set("elapsedMs", float64(e.ElapsedMs))
	return EncodeJSON(out)
}

// orderAnswers reorders an answers object to the question order; unknown ids follow, sorted.
func (e *Evaluation) orderAnswers(v any) any {
	answers, ok := v.(*Object)
	if !ok {
		return v
	}
	ordered := NewObject()
	for _, id := range e.Order {
		if a, ok := answers.Get(id); ok {
			ordered.Set(id, a)
		}
	}
	rest := []string{}
	for _, id := range answers.Keys() {
		if !ordered.Has(id) {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	for _, id := range rest {
		a, _ := answers.Get(id)
		ordered.Set(id, a)
	}
	return ordered
}

// DetailsJSON is MarshalJSON plus an "order" array naming the answer ids in question order. A host that decodes
// tool details into maps loses key order, so a renderer reads the order from here.
func (e *Evaluation) DetailsJSON() ([]byte, error) {
	raw, err := e.MarshalJSON()
	if err != nil {
		return nil, err
	}
	tree, err := ParseJSON(raw)
	if err != nil {
		return nil, err
	}
	obj := tree.(*Object)
	order := make([]any, len(e.Order))
	for i, id := range e.Order {
		order[i] = id
	}
	obj.Set("order", order)
	return EncodeJSON(obj)
}
