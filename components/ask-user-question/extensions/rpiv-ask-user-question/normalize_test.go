package ask_user_question

import "testing"

const fNormalizeParams = "tool/normalize-params"

func TestNormalizeParams(t *testing.T) {
	tw(t, fNormalizeParams, "deletes a lone CR without introducing a phantom space (#192 sample 1)", func(t *testing.T) {
		eq(t, normalizeLineTerminators("a\rb"), "ab", "lone CR")
	})
	tw(t, fNormalizeParams, "deletes runs of CRs and keeps the real space that follows them (#192 sample 2)", func(t *testing.T) {
		eq(t, normalizeLineTerminators("a\r\r\r b"), "a b", "CR run")
	})
	tw(t, fNormalizeParams, "maps CRLF to LF so genuine multi-line content survives", func(t *testing.T) {
		eq(t, normalizeLineTerminators("one\r\ntwo\r\nthree"), "one\ntwo\nthree", "CRLF")
	})
	tw(t, fNormalizeParams, "leaves LF-only and CR-free text byte-identical", func(t *testing.T) {
		for _, s := range []string{"", "plain", "a\nb\n", "tabs\tand  spaces"} {
			eq(t, normalizeLineTerminators(s), s, "text")
		}
	})
	tw(t, fNormalizeParams, "normalizes question, header, label, description, and preview", func(t *testing.T) {
		in := params(question{Question: "q\r\n1", Header: "h\r", Options: []option{
			{Label: "l\r\n", Description: "d\rd", Preview: "p\r\np"}, {Label: "x", Description: "y"}}})
		got := normalizeQuestionParams(in)
		q := at(got.Questions, 0)
		eq(t, q.Question, "q\n1", "question")
		eq(t, q.Header, "h", "header")
		eq(t, at(q.Options, 0), option{Label: "l\n", Description: "dd", Preview: "p\np"}, "option")
	})
	tw(t, fNormalizeParams, "does not mutate the input", func(t *testing.T) {
		in := params(question{Question: "q\r\n", Header: "h", Options: []option{{Label: "a\r", Description: "d"}, {Label: "b", Description: "d"}}})
		normalizeQuestionParams(in)
		eq(t, in.Questions[0].Question, "q\r\n", "question")
		eq(t, at(in.Questions[0].Options, 0).Label, "a\r", "label")
	})
	tw(t, fNormalizeParams, "keeps omitted optional fields omitted", func(t *testing.T) {
		got := normalizeQuestionParams(single())
		eq(t, at(at(got.Questions, 0).Options, 0).Preview, "", "preview")
		eq(t, at(got.Questions, 0).MultiSelect, false, "multiSelect")
	})
	tskip(t, fNormalizeParams, "tolerates non-string values in string slots (malformed params reach the validator, not a throw)",
		"a Go struct cannot hold a non-string in a string field; paramsFromArgs reads a wrong-typed slot as empty (TestParamsFromArgsToleratesWrongTypes)")
}

func TestParamsFromArgsToleratesWrongTypes(t *testing.T) {
	got := paramsFromArgs(map[string]any{"questions": []any{
		map[string]any{"question": 5.0, "header": nil, "options": "nope", "multiSelect": "yes"},
		"not an object",
	}})
	eq(t, len(got.Questions), 1, "questions kept")
	eq(t, at(got.Questions, 0), question{}, "wrong-typed slots read as empty")
	eq(t, paramsFromArgs(map[string]any{}), questionParams{}, "no questions")
	eq(t, paramsFromArgs(map[string]any{"questions": "x"}), questionParams{}, "questions not an array")
}

func TestParamsFromArgsReadsAFullQuestion(t *testing.T) {
	got := paramsFromArgs(map[string]any{"questions": []any{map[string]any{
		"question": "Q?", "header": "H", "multiSelect": true,
		"options": []any{map[string]any{"label": "a", "description": "d", "preview": "p"}, map[string]any{"label": "b", "description": "e"}},
	}}})
	eq(t, got, params(question{Question: "Q?", Header: "H", MultiSelect: true,
		Options: []option{{Label: "a", Description: "d", Preview: "p"}, {Label: "b", Description: "e"}}}), "params")
}
