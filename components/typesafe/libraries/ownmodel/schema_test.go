package ownmodel

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

const schemaFile = "tests/test_schema.py::"

var schemaKeywords = []string{"title", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"}
var fieldNames = append(append([]string{}, schemaKeywords...), "model_dump", "model_config", "_private", "", "with spaces", "answer_0", "probability_0")

func planFor(t testing.TB, qs typesafe.Questions, mode AnswerMode) *plan {
	t.Helper()
	p, err := newPlan(qs, mode)
	noErr(t, err)
	return p
}

func asMap(t testing.TB, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%T is not an object", v)
	}
	return m
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func stringsOf(t testing.TB, v any) []string {
	t.Helper()
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestInvalidDictionaryQuestionsAreRejected(t *testing.T) {
	twin(t,
		schemaFile+"test_invalid_dictionary_questions_are_rejected[question0]",
		schemaFile+"test_invalid_dictionary_questions_are_rejected[question1]",
		schemaFile+"test_invalid_dictionary_questions_are_rejected[question2]",
		schemaFile+"test_invalid_dictionary_questions_are_rejected[question3]")
	for _, q := range []string{
		`{"type":"unknown"}`,
		`{"type":"noul","instructions":42}`,
		`{"type":"choice","criteria":["yes","no"]}`,
		`{"type":"score","criteria":{"0":"Bad.","1":"Good."}}`,
	} {
		if _, err := typesafe.ParseQuestions([]byte(`{"answer":` + q + `}`)); err == nil {
			t.Fatalf("%s must be rejected", q)
		}
	}
}

func TestSDKQuestionFieldsAreRevalidated(t *testing.T) {
	// Adapted: Python mutates a model field to an invalid value; the Go equivalent is a
	// description that is not text, JSON or null (a number), which must fail before any model call.
	twin(t, schemaFile+"test_sdk_question_fields_are_revalidated")
	q := typesafe.Noul(nil).Yes(42)
	if _, err := newPlan(typesafe.Questions{typesafe.Ask("answer", q)}, Probabilities); err == nil {
		t.Fatal("a numeric criterion must be rejected")
	}
	model := newScripted(map[string]any{"answers": map[string]any{"answer": 0.5}})
	_, err := evaluate(t, mustNew(t, Options{Model: model}), "s", typesafe.Questions{typesafe.Ask("answer", q)}, nil)
	if err == nil || model.callCount() != 0 {
		t.Fatalf("err=%v calls=%d", err, model.callCount())
	}
}

func TestQuestionIDsPreserveArbitraryNames(t *testing.T) {
	twin(t, schemaFile+"test_question_ids_preserve_arbitrary_names[probabilities]", schemaFile+"test_question_ids_preserve_arbitrary_names[discrete]")
	for _, mode := range []AnswerMode{Probabilities, Discrete} {
		var qs typesafe.Questions
		instructions := map[string]string{}
		for _, key := range fieldNames {
			instructions[key] = "Evaluate " + key + "."
			qs = append(qs, typesafe.Ask(key, typesafe.Noul(instructions[key])))
		}
		p := planFor(t, qs, mode)
		schema := p.schema()
		eq(t, schema["properties"].(map[string]any)["answers"], any(map[string]any{"$ref": "#/$defs/TypeSafeAnswers"}))
		answers := asMap(t, asMap(t, schema["$defs"])["TypeSafeAnswers"])
		contains(t, answers["description"].(string), "Use these property names verbatim")
		props := asMap(t, answers["properties"])
		eq(t, keysOf(props), sortedCopy(fieldNames))
		eq(t, sortedCopy(stringsOf(t, answers["required"])), sortedCopy(fieldNames))
		for key, a := range props {
			am := asMap(t, a)
			contains(t, am["description"].(string), instructions[key])
			for _, kw := range schemaKeywords {
				if _, has := am[kw]; has {
					t.Fatalf("%q carries the keyword %q", key, kw)
				}
			}
		}
		value := any(0.8)
		if mode == Discrete {
			value = true
		}
		payload := map[string]any{}
		for _, key := range fieldNames {
			payload[key] = value
		}
		raw, err := json.Marshal(map[string]any{"answers": payload})
		noErr(t, err)
		_, err = p.decode(string(raw))
		noErr(t, err)
	}
}

func TestProbabilityLabelsPreserveArbitraryNames(t *testing.T) {
	twin(t, schemaFile+"test_probability_labels_preserve_arbitrary_names")
	var opts []typesafe.Option
	criteria := map[string]string{}
	for _, key := range fieldNames {
		criteria[key] = "The " + key + " option."
		opts = append(opts, typesafe.Opt(key, criteria[key]))
	}
	p := planFor(t, typesafe.Questions{typesafe.Ask("level", typesafe.Choice(nil, opts...))}, Probabilities)
	pm := asMap(t, asMap(t, p.schema()["$defs"])["ProbabilityMap0"])
	props := asMap(t, pm["properties"])
	eq(t, keysOf(props), sortedCopy(fieldNames))
	eq(t, sortedCopy(stringsOf(t, pm["required"])), sortedCopy(fieldNames))
	if _, has := pm["title"]; has {
		t.Fatal("title must be dropped")
	}
	for key, pr := range props {
		prm := asMap(t, pr)
		eq(t, prm["description"], any(criteria[key]))
		for _, kw := range schemaKeywords {
			if _, has := prm[kw]; has {
				t.Fatalf("%q carries the keyword %q", key, kw)
			}
		}
	}
	probs := map[string]any{}
	for _, key := range fieldNames {
		probs[key] = 1 / float64(len(fieldNames))
	}
	raw, _ := json.Marshal(map[string]any{"answers": map[string]any{"level": probs}})
	_, err := p.decode(string(raw))
	noErr(t, err)
	for k := range probs {
		probs[k] = 2
	}
	raw, _ = json.Marshal(map[string]any{"answers": map[string]any{"level": probs}})
	if _, err := p.decode(string(raw)); err == nil {
		t.Fatal("probabilities above 1 must be rejected")
	}
}

func TestOutputValidationPreservesTypesBoundsAndAllowedValues(t *testing.T) {
	twin(t,
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question0-discrete-true]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question1-discrete-1]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question2-probabilities-0.5]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question3-probabilities-True]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question4-probabilities--0.1]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question5-probabilities-1.1]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question6-probabilities-nan]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question7-discrete-1.0]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question8-discrete-True]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question9-discrete-2]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question10-discrete-maybe]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question11-probabilities-answer11]",
		schemaFile+"test_output_validation_preserves_types_bounds_and_allowed_values[question12-probabilities-answer12]")
	noul := typesafe.Noul(nil)
	score := typesafe.Score(nil, "Bad.", "Good.")
	choice := typesafe.Choice(nil, typesafe.Opt("yes", nil), typesafe.Opt("no", nil))
	for i, tc := range []struct {
		q      typesafe.Question
		mode   AnswerMode
		answer string // JSON text of the model's answer
	}{
		{noul, Discrete, `"true"`}, {noul, Discrete, `1`}, {noul, Probabilities, `"0.5"`}, {noul, Probabilities, `true`},
		{noul, Probabilities, `-0.1`}, {noul, Probabilities, `1.1`}, {noul, Probabilities, `null`}, // NaN encodes as null
		{score, Discrete, `1.0`}, {score, Discrete, `true`}, {score, Discrete, `2`},
		{choice, Discrete, `"maybe"`}, {choice, Probabilities, `{"yes":0.5}`}, {choice, Probabilities, `{"yes":0.5,"no":0.5,"maybe":0}`},
	} {
		p := planFor(t, typesafe.Questions{typesafe.Ask("answer", tc.q)}, tc.mode)
		if _, err := p.decode(`{"answers":{"answer":` + tc.answer + `}}`); err == nil {
			t.Fatalf("case %d (%s %s): must be rejected", i, tc.mode, tc.answer)
		}
	}
	// Valid answers of the same shapes pass.
	for _, tc := range []struct {
		q      typesafe.Question
		mode   AnswerMode
		answer string
	}{
		{noul, Discrete, `true`}, {noul, Probabilities, `0`}, {noul, Probabilities, `1`}, {noul, Probabilities, `0.5`},
		{score, Discrete, `0`}, {score, Discrete, `1`}, {choice, Discrete, `"no"`}, {choice, Probabilities, `{"yes":0.5,"no":0.5}`},
	} {
		p := planFor(t, typesafe.Questions{typesafe.Ask("answer", tc.q)}, tc.mode)
		if _, err := p.decode(`{"answers":{"answer":` + tc.answer + `}}`); err != nil {
			t.Fatalf("%s %s: %v", tc.mode, tc.answer, err)
		}
	}
	_ = math.NaN
}

func TestOutputRejectsExtraFieldsAndInternalFieldNames(t *testing.T) {
	twin(t,
		schemaFile+"test_output_rejects_extra_fields_and_internal_field_names[payload0]",
		schemaFile+"test_output_rejects_extra_fields_and_internal_field_names[payload1]",
		schemaFile+"test_output_rejects_extra_fields_and_internal_field_names[payload2]")
	p := planFor(t, typesafe.Questions{typesafe.Ask("answer", typesafe.Noul(nil))}, Probabilities)
	for _, payload := range []string{`{"answers":{"answer":0.5},"extra":1}`, `{"answers":{"answer":0.5,"extra":1}}`, `{"answers":{"answer_0":0.5}}`} {
		if _, err := p.decode(payload); err == nil {
			t.Fatalf("%s must be rejected", payload)
		}
	}
}

func TestDecodeRejectsTrailingDataAndAcceptsFences(t *testing.T) {
	p := planFor(t, typesafe.Questions{typesafe.Ask("q", typesafe.Noul(nil))}, Probabilities)
	if _, err := p.decode(`{"answers":{"q":0.5}} trailing`); err == nil {
		t.Fatal("trailing text must be rejected")
	}
	for _, text := range []string{"```json\n{\"answers\":{\"q\":0.5}}\n```", "```\n{\"answers\":{\"q\":0.5}}```", "  ```JSON {\"answers\":{\"q\":0.5}}```  "} {
		if _, err := p.decode(text); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
	}
}

func TestSchemaJSONKeyOrderAndDefs(t *testing.T) {
	qs := typesafe.Questions{typesafe.Ask("a", typesafe.Choice("x", typesafe.Opt("p", nil), typesafe.Opt("q", nil)))}
	s := planFor(t, qs, Probabilities).schemaJSON()
	if !strings.HasPrefix(s, `{"$defs":{"ProbabilityMap0":{"additionalProperties":false,"description":`) || !strings.HasSuffix(s, `"required":["answers"],"type":"object"}`) {
		t.Fatalf("schema key order: %s", s)
	}
}
