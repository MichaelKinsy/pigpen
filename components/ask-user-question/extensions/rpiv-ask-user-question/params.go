package ask_user_question

// paramsFromArgs reads the model's arguments, which the host has already validated against the schema. A slot of
// the wrong type reads as empty, so malformed input reaches the validator and not a panic; an element of
// questions that is not an object is dropped.
func paramsFromArgs(args map[string]any) questionParams {
	var out questionParams
	list, _ := args["questions"].([]any)
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		q := question{}
		q.Question, _ = m["question"].(string)
		q.Header, _ = m["header"].(string)
		q.MultiSelect, _ = m["multiSelect"].(bool)
		opts, _ := m["options"].([]any)
		for _, rawOpt := range opts {
			om, ok := rawOpt.(map[string]any)
			if !ok {
				continue
			}
			o := option{}
			o.Label, _ = om["label"].(string)
			o.Description, _ = om["description"].(string)
			o.Preview, _ = om["preview"].(string)
			q.Options = append(q.Options, o)
		}
		out.Questions = append(out.Questions, q)
	}
	return out
}

// decodeResult reads a questionnaire result the custom component resolved with. upstream:
// tool/types.ts isQuestionnaireResult: an object with an answers array and a boolean cancelled.
func decodeResult(v any) (*questionnaireResult, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	answers, ok := m["answers"].([]any)
	if !ok {
		return nil, false
	}
	cancelled, ok := m["cancelled"].(bool)
	if !ok {
		return nil, false
	}
	r := &questionnaireResult{Cancelled: cancelled}
	r.GlobalNote, _ = m["globalNote"].(string)
	r.Error, _ = m["error"].(string)
	for _, raw := range answers {
		am, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		a := questionAnswer{}
		if n, ok := am["questionIndex"].(float64); ok {
			a.QuestionIndex = int(n)
		}
		a.Question, _ = am["question"].(string)
		a.Kind, _ = am["kind"].(string)
		if s, ok := am["answer"].(string); ok {
			a.Answer = &s
		}
		if sel, ok := am["selected"].([]any); ok {
			a.HasSelected = true
			for _, e := range sel {
				if s, ok := e.(string); ok {
					a.Selected = append(a.Selected, s)
				}
			}
		}
		a.Notes, _ = am["notes"].(string)
		a.Preview, _ = am["preview"].(string)
		r.Answers = append(r.Answers, a)
	}
	return r, true
}
