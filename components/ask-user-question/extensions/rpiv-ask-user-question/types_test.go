package ask_user_question

import (
	"encoding/json"
	"testing"
)

const fTypes = "tool/types"

// check is a small JSON Schema checker for the keywords the tool's schema uses (type, required, properties, items,
// minItems, maxItems, maxLength): the same keywords TypeBox emits and PiG's host validates.
func check(schema map[string]any, v any) bool {
	switch schema["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, has := m[r.(string)]; !has {
					return false
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for k, sub := range props {
			if val, has := m[k]; has && !check(sub.(map[string]any), val) {
				return false
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return false
		}
		if n, ok := schema["minItems"].(float64); ok && float64(len(a)) < n {
			return false
		}
		if n, ok := schema["maxItems"].(float64); ok && float64(len(a)) > n {
			return false
		}
		for _, e := range a {
			if !check(schema["items"].(map[string]any), e) {
				return false
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return false
		}
		if n, ok := schema["maxLength"].(float64); ok && float64(len([]rune(s))) > n {
			return false
		}
	case "boolean":
		_, ok := v.(bool)
		return ok
	}
	return true
}

func schema(t *testing.T) map[string]any {
	t.Helper()
	b, err := json.Marshal(toolParameters())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		t.Fatalf("toolParameters() is not an object: %s", b)
	}
	return m
}

func opts(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"label": string(rune('a' + i)), "description": "d"}
	}
	return out
}

func questionOf(options []any) map[string]any {
	return map[string]any{"question": "Q?", "header": "H", "options": options}
}

func args(qs ...any) any { return map[string]any{"questions": qs} }

func TestToolSchema(t *testing.T) {
	s := schema(t)
	ok := func(name string, v any) {
		t.Helper()
		if !check(s, v) {
			t.Fatalf("%s: the schema rejects %v", name, v)
		}
	}
	bad := func(name string, v any) {
		t.Helper()
		if check(s, v) {
			t.Fatalf("%s: the schema accepts %v", name, v)
		}
	}
	tw(t, fTypes, "accepts a single question", func(t *testing.T) { ok("single", args(questionOf(opts(2)))) })
	tw(t, fTypes, "accepts MAX_QUESTIONS (4) questions", func(t *testing.T) {
		ok("four", args(questionOf(opts(2)), questionOf(opts(2)), questionOf(opts(2)), questionOf(opts(2))))
	})
	tw(t, fTypes, "rejects empty array (minItems=1)", func(t *testing.T) { bad("empty", args()) })
	tw(t, fTypes, "rejects > MAX_QUESTIONS items (maxItems=4)", func(t *testing.T) {
		q := questionOf(opts(2))
		bad("five", args(q, q, q, q, q))
	})
	tw(t, fTypes, "accepts options with optional preview field", func(t *testing.T) {
		o := opts(2)
		o[0].(map[string]any)["preview"] = "p"
		ok("preview", args(questionOf(o)))
	})
	tw(t, fTypes, "accepts a question with all optional fields populated", func(t *testing.T) {
		q := questionOf(opts(2))
		q["multiSelect"] = true
		q["options"].([]any)[0].(map[string]any)["preview"] = "p"
		ok("full", args(q))
	})
	tw(t, fTypes, "accepts multiSelect: true", func(t *testing.T) {
		q := questionOf(opts(3))
		q["multiSelect"] = true
		ok("multi", args(q))
	})
	tw(t, fTypes, "accepts a header up to MAX_HEADER_LENGTH chars", func(t *testing.T) {
		q := questionOf(opts(2))
		q["header"] = "1234567890123456"
		ok("16", args(q))
	})
	tw(t, fTypes, "rejects a single-option question (minItems=2)", func(t *testing.T) { bad("one", args(questionOf(opts(1)))) })
	tw(t, fTypes, "rejects empty options array (minItems=2)", func(t *testing.T) { bad("none", args(questionOf(opts(0)))) })
	tw(t, fTypes, "rejects more than MAX_OPTIONS options (maxItems=4)", func(t *testing.T) { bad("five", args(questionOf(opts(5)))) })
	tw(t, fTypes, "rejects an option missing the required description", func(t *testing.T) {
		bad("no description", args(questionOf([]any{map[string]any{"label": "a"}, map[string]any{"label": "b", "description": "d"}})))
	})
	tw(t, fTypes, "rejects a question missing the required header", func(t *testing.T) {
		bad("no header", args(map[string]any{"question": "Q?", "options": opts(2)}))
	})
	tw(t, fTypes, "rejects a header longer than MAX_HEADER_LENGTH chars", func(t *testing.T) {
		q := questionOf(opts(2))
		q["header"] = "12345678901234567"
		bad("17", args(q))
	})
	tw(t, fTypes, "rejects a label longer than MAX_LABEL_LENGTH (60) chars", func(t *testing.T) {
		o := opts(2)
		long := ""
		for i := 0; i < 61; i++ {
			long += "x"
		}
		o[0].(map[string]any)["label"] = long
		bad("61", args(questionOf(o)))
		o[0].(map[string]any)["label"] = long[:60]
		ok("60", args(questionOf(o)))
	})
	tw(t, fTypes, "rejects question with missing 'question' text", func(t *testing.T) {
		bad("no text", args(map[string]any{"header": "H", "options": opts(2)}))
	})
	tw(t, fTypes, "accepts { questions: [...] }", func(t *testing.T) { ok("object", args(questionOf(opts(2)))) })
	tw(t, fTypes, "accepts full valid payload with preview + multiSelect", func(t *testing.T) {
		q := questionOf(opts(2))
		q["multiSelect"] = true
		q["options"].([]any)[1].(map[string]any)["preview"] = "p"
		ok("full", args(q, questionOf(opts(4))))
	})
	tw(t, fTypes, "rejects missing 'questions' field", func(t *testing.T) { bad("missing", map[string]any{}) })
	tw(t, fTypes, "rejects non-array questions field", func(t *testing.T) { bad("string", map[string]any{"questions": "x"}) })
}

func TestSchemaConstantsAndDescriptions(t *testing.T) {
	tw(t, fTypes, "exports the new schema constants with expected values", func(t *testing.T) {
		eq(t, []int{maxQuestions, minOptions, maxOptions, maxHeaderLength, maxLabelLength}, []int{4, 2, 4, 16, 60}, "limits")
		eq(t, labelOther, "Type something.", "other label")
		eq(t, labelNext, "Next", "next label")
	})
	tw(t, fTypes, "RESERVED_LABELS includes the Pi sentinels + CC's 'Other'", func(t *testing.T) {
		eq(t, reservedLabels, []string{"Other", "Type something.", "Next"}, "reserved")
	})
	// The limits are also stated to the model.
	s := schema(t)
	q := s["properties"].(map[string]any)["questions"].(map[string]any)
	eq(t, q["minItems"], 1.0, "minItems")
	eq(t, q["maxItems"], 4.0, "maxItems")
	eq(t, q["description"], "Questions to ask the user (1-4 questions)", "description")
	eq(t, s["required"], []any{"questions"}, "required")
	hdr := q["items"].(map[string]any)["properties"].(map[string]any)["header"].(map[string]any)
	eq(t, hdr["maxLength"], 16.0, "header maxLength")
}

func TestDecodeKeepsAnEmptySelection(t *testing.T) {
	r, ok := decodeResult(map[string]any{"cancelled": false, "answers": []any{
		map[string]any{"questionIndex": 0.0, "question": "Q", "kind": "multi", "answer": nil, "selected": []any{}}}})
	eq(t, ok, true, "ok")
	eq(t, r.Answers[0].HasSelected, true, "an empty selection is still a selection")
	b, _ := json.Marshal(r.Answers[0])
	eq(t, string(b), `{"answer":null,"kind":"multi","question":"Q","questionIndex":0,"selected":[]}`, "wire")
}

func TestDecodeResult(t *testing.T) {
	// dec returns the decoded result by value: a nil result reads as the zero value, so a wrong decode fails the
	// assertion that follows instead of panicking the binary.
	var dec = func(s string) (questionnaireResult, bool) {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		r, ok := decodeResult(v)
		if r == nil {
			return questionnaireResult{}, ok
		}
		return *r, ok
	}
	tw(t, fTypes, "accepts a valid result", func(t *testing.T) {
		r, ok := dec(`{"answers":[],"cancelled":false}`)
		eq(t, ok, true, "ok")
		eq(t, r.Cancelled, false, "cancelled")
	})
	tw(t, fTypes, "accepts a result with error field", func(t *testing.T) {
		r, ok := dec(`{"answers":[],"cancelled":true,"error":"no_ui"}`)
		eq(t, ok, true, "ok")
		eq(t, r.Error, "no_ui", "error")
	})
	tw(t, fTypes, "accepts a result with the new error variants", func(t *testing.T) {
		for _, e := range []string{"no_questions", "empty_options", "too_many_questions", "duplicate_question", "duplicate_option_label", "reserved_label"} {
			r, ok := dec(`{"answers":[],"cancelled":true,"error":"` + e + `"}`)
			eq(t, ok, true, e)
			eq(t, r.Error, e, e)
		}
	})
	tw(t, fTypes, "accepts a result carrying a globalNote (Submit-tab global note)", func(t *testing.T) {
		r, _ := dec(`{"answers":[],"cancelled":false,"globalNote":"note"}`)
		eq(t, r.GlobalNote, "note", "note")
	})
	tw(t, fTypes, "accepts a result with populated answers", func(t *testing.T) {
		r, ok := dec(`{"answers":[{"questionIndex":0,"question":"Q","kind":"option","answer":"A","preview":"P","notes":"N"},
			{"questionIndex":1,"question":"M","kind":"multi","answer":null,"selected":["x","y"]}],"cancelled":false}`)
		eq(t, ok, true, "ok")
		eq(t, r.Answers, []questionAnswer{
			{QuestionIndex: 0, Question: "Q", Kind: "option", Answer: sp("A"), Preview: "P", Notes: "N"},
			{QuestionIndex: 1, Question: "M", Kind: "multi", Selected: []string{"x", "y"}, HasSelected: true},
		}, "answers")
	})
	tw(t, fTypes, "accepts an answer with notes populated", func(t *testing.T) {
		r, _ := dec(`{"answers":[{"questionIndex":0,"question":"Q","kind":"option","answer":"A","notes":"n"}],"cancelled":false}`)
		eq(t, at(r.Answers, 0).Notes, "n", "notes")
	})
	tw(t, fTypes, "accepts an answer with selected[] (multi-select) and no answer scalar", func(t *testing.T) {
		r, _ := dec(`{"answers":[{"questionIndex":0,"question":"Q","kind":"multi","selected":["a"]}],"cancelled":false}`)
		eq(t, at(r.Answers, 0).Selected, []string{"a"}, "selected")
		eq(t, at(r.Answers, 0).Answer == nil, true, "no answer")
	})
	tw(t, fTypes, "accepts an answer with preview populated (single-select with matched preview-bearing option)", func(t *testing.T) {
		r, _ := dec(`{"answers":[{"questionIndex":0,"question":"Q","kind":"option","answer":"A","preview":"P"}],"cancelled":false}`)
		eq(t, at(r.Answers, 0).Preview, "P", "preview")
	})
	tw(t, fTypes, "supports all three variant kinds", func(t *testing.T) {
		for _, k := range []string{"option", "custom", "multi"} {
			r, ok := dec(`{"answers":[{"questionIndex":0,"question":"Q","kind":"` + k + `","answer":"A"}],"cancelled":false}`)
			eq(t, ok, true, k)
			eq(t, at(r.Answers, 0).Kind, k, k)
		}
	})
	tw(t, fTypes, "rejects null / undefined", func(t *testing.T) {
		_, ok := dec(`null`)
		eq(t, ok, false, "null")
		_, ok = decodeResult(nil)
		eq(t, ok, false, "nil")
	})
	tw(t, fTypes, "rejects primitives", func(t *testing.T) {
		for _, s := range []string{`1`, `"x"`, `true`} {
			_, ok := dec(s)
			eq(t, ok, false, s)
		}
	})
	tw(t, fTypes, "rejects an array", func(t *testing.T) {
		_, ok := dec(`[]`)
		eq(t, ok, false, "array")
	})
	tw(t, fTypes, "rejects missing fields", func(t *testing.T) {
		for _, s := range []string{`{}`, `{"answers":[]}`, `{"cancelled":true}`, `{"answers":"x","cancelled":true}`, `{"answers":[],"cancelled":"yes"}`} {
			_, ok := dec(s)
			eq(t, ok, false, s)
		}
	})
}
