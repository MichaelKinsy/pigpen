package typesafe

import (
	"encoding/json"
	"testing"
)

// Go-specific cases beyond the upstream suite: entries, ordered questions, JSON parsing.

func TestEntry_ZeroIsOmittedAndNullIsExplicit(t *testing.T) {
	var zero Entry
	if !zero.IsOmitted() || zero.IsNull() {
		t.Fatal("the zero Entry is omitted, not null")
	}
	if Null.IsOmitted() || !Null.IsNull() {
		t.Fatal("Null is explicit")
	}
	if !EntryOf(nil).IsNull() || Text("x").IsOmitted() || Value(nil).IsOmitted() || !Value(nil).IsNull() {
		t.Fatal("nil converts to null")
	}
	eq(t, EntryOf(Text("t")), Text("t"))
	eq(t, Text("x").Data(), any("x"))
	eq(t, Value(map[string]any{"a": 1}).Data(), any(map[string]any{"a": float64(1)}))
	eq(t, Null.Data(), nil)
}

func TestEntry_MarshalsJSONValuesAndRejectsNumbersAndBooleans(t *testing.T) {
	for _, tc := range []struct {
		e    Entry
		want string
	}{{Text("a\"b"), `"a\"b"`}, {Null, `null`}, {Entry{}, `null`}, {Value([]any{nil, 1}), `[null,1]`}, {Value(map[string]any{"k": true}), `{"k":true}`}, {Value(struct {
		A int `json:"a"`
	}{2}), `{"a":2}`}} {
		raw, err := json.Marshal(tc.e)
		noErr(t, err)
		eq(t, string(raw), tc.want)
	}
	for _, e := range []Entry{Value(1), Value(2.5), Value(true), Value(json.RawMessage(`3`)), Value(make(chan int))} {
		_, err := json.Marshal(e)
		if err == nil {
			t.Fatalf("%v: must not marshal", e)
		}
	}
}

func TestEntry_ValueKeepsRawMessageKeyOrder(t *testing.T) {
	raw, err := json.Marshal(Noul(json.RawMessage(`{"z":1,"a":{"y":2,"b":3}}`)))
	noErr(t, err)
	eq(t, string(raw), `{"type":"noul","instructions":{"z":1,"a":{"y":2,"b":3}}}`)
}

func TestEntry_UnmarshalJSON(t *testing.T) {
	var e Entry
	noErr(t, json.Unmarshal([]byte(`null`), &e))
	if !e.IsNull() {
		t.Fatal("null decodes to Null")
	}
	noErr(t, json.Unmarshal([]byte(` "s" `), &e))
	eq(t, e.Data(), any("s"))
	noErr(t, json.Unmarshal([]byte(`{"a":[1]}`), &e))
	eq(t, e.Data(), any(map[string]any{"a": []any{float64(1)}}))
	if err := json.Unmarshal([]byte(`5`), &e); err == nil {
		t.Fatal("a number is not an Entry")
	}
}

func TestQuestions_MarshalKeepsOrderAndNames(t *testing.T) {
	raw, err := json.Marshal(Questions{Ask("z", Noul("1")), Ask("a", Noul("2")), Ask("m", Noul("3"))})
	noErr(t, err)
	eq(t, string(raw), `{"z":{"type":"noul","instructions":"1"},"a":{"type":"noul","instructions":"2"},"m":{"type":"noul","instructions":"3"}}`)
	raw, err = json.Marshal(Choice("q", Opt("b", nil), Opt("a", "x")))
	noErr(t, err)
	eq(t, string(raw), `{"type":"choice","instructions":"q","criteria":{"b":null,"a":"x"}}`)
}

func TestQuestions_ValidateRejectsDuplicatesEmptyAndShortScores(t *testing.T) {
	noErr(t, Questions{Ask("a", Noul("x")), Ask("", Noul("y"))}.Validate())
	err := Questions{Ask("a", Noul("x")), Ask("a", Noul("y"))}.Validate()
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), `Duplicate question name "a"`)
	err = Questions{}.Validate()
	contains(t, err.Error(), "At least one question is required.")
	err = Questions{Ask("s", Score("x", "only"))}.Validate()
	contains(t, err.Error(), `Score question "s" has 1 criteria; at least two scores are required.`)
	err = Questions{Ask("n", nil)}.Validate()
	if err == nil {
		t.Fatal("a nil question must be rejected")
	}
}

func TestParseQuestions_RoundTripsInOrderKeepingOmittedAndNull(t *testing.T) {
	in := `{"zeta":{"type":"noul","criteria":null},"alpha":{"type":"noul","instructions":null,"criteria":{"false":"no"}},` +
		`"c":{"type":"choice","instructions":["a",null],"criteria":{"b":null,"a":{"k":"v"}}},"s":{"type":"score","instructions":"s","criteria":[null,"hi",["x"]]},` +
		`"plain":{"type":"noul"}}`
	qs, err := ParseQuestions([]byte(in))
	noErr(t, err)
	eq(t, len(qs), 5)
	eq(t, qs[0].Name, "zeta")
	eq(t, qs[1].Name, "alpha")
	raw, err := json.Marshal(qs)
	noErr(t, err)
	eq(t, string(raw), in)
	n := qs[4].Question.(NoulQuestion)
	if !n.Instructions.IsOmitted() || n.Criteria != nil {
		t.Fatal("omitted stays omitted")
	}
	z := qs[0].Question.(NoulQuestion)
	if z.Criteria == nil || !z.Criteria.Null {
		t.Fatal("null criteria stays null")
	}
}

func TestParseQuestions_RejectsAChoiceLabelList(t *testing.T) {
	_, err := ParseQuestions([]byte(`{"q":{"type":"choice","instructions":"q","criteria":["a","b"]}}`))
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Choice criteria must be a map of labels to descriptions, not a list.")
}

func TestParseQuestions_RejectsAScoreMap(t *testing.T) {
	_, err := ParseQuestions([]byte(`{"q":{"type":"score","criteria":{"0":"bad","1":"good"}}}`))
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Score criteria must be a list of descriptions indexed by score from zero, not a map.")
}

func TestParseQuestions_RejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`[]`, "object"},
		{`{"q":{"type":"unknown"}}`, `unknown type "unknown"`},
		{`{"q":{"instructions":"x"}}`, `"q"`},
		{`{"q":5}`, `"q"`},
		{`{"q":{"type":"noul"},"q":{"type":"noul"}}`, `Duplicate question name "q"`},
		{`{"q":{"type":"noul","instructions":5}}`, `"q"`},
		{`{"q":{"type":"choice","criteria":null}}`, `"q"`},
		{`not json`, ""},
	} {
		_, err := ParseQuestions([]byte(tc.in))
		if err == nil {
			t.Fatalf("%s: want an error", tc.in)
		}
		contains(t, err.Error(), tc.want)
	}
}

func TestResult_DecodingIsLenientAndAnswersByType(t *testing.T) {
	r := decodeResult(t, `{}`)
	eq(t, len(r.Answers), 0)
	r = decodeResult(t, `{"model":"m","answers":{"x":{"type":"future","v":1}},"usage":{"input_tokens":1,"output_tokens":2},"extra":true}`)
	raw, ok := r.Answers["x"].(RawAnswer)
	if !ok || raw.Type != "future" || string(raw.Raw) != `{"type":"future","v":1}` {
		t.Fatalf("unknown answer types keep their JSON: %#v", r.Answers["x"])
	}
	if _, err := r.Noul("x"); err == nil {
		t.Fatal("a raw answer is not a Noul")
	}
	var bad SystemOneResult
	if err := json.Unmarshal([]byte(`{"answers":{"a":{"type":"noul","noul":"high"}}}`), &bad); err == nil {
		t.Fatal("a mistyped field must fail")
	}
}

func TestResult_MarshalsTheWireForm(t *testing.T) {
	r := decodeResult(t, allAnswersJSON)
	raw, err := json.Marshal(r)
	noErr(t, err)
	jsonEq(t, json.RawMessage(raw), allAnswersJSON)
}
