package pitypesafe

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

const secret = "private-user-content"

func nearMiss(t *testing.T) any {
	return mustTree(t, `{"state":"synthetic","questions":{
	  "team":{"type":"choice","instructions":"Which team?","options":{"billing":"Charges and payments","other":"None of these"}},
	  "refund":{"type":"noul","instructions":"Is a refund requested?","criteria":"The sender asks for money back"},
	  "severity":{"type":"score","instructions":"How severe?","levels":["Cosmetic","Blocking"]},
	  "picks":{"type":"choice","instructions":"Pick one","choices":["a","b"]}}}`)
}

func criteriaOf(t *testing.T, r *Request, id string) string {
	t.Helper()
	q, _ := r.Questions.Get(id)
	c, _ := q.(*Object).Get("criteria")
	out, err := EncodeJSON(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestSchema(t *testing.T) {
	tw(t, "schema", "admission accepts the near-miss aliases the agent tool has always accepted", func(t *testing.T) {
		p, err := PrepareEvaluationRequest(nearMiss(t), PrepareOptions{})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"team": `{"billing":"Charges and payments","other":"None of these"}`, "refund": `{"true":"The sender asks for money back"}`, "severity": `["Cosmetic","Blocking"]`, "picks": `{"a":null,"b":null}`}
		for id, w := range want {
			if got := criteriaOf(t, p, id); got != w {
				t.Errorf("%s criteria = %s, want %s", id, got, w)
			}
		}
	})
	tw(t, "schema", "admission normalizes before it validates", func(t *testing.T) {
		v := mustTree(t, `{"state":"s","questions":{"q":{"type":"choice","criteria":["a","b"]}}}`)
		if _, err := ParseEvaluationRequest(v); !hasCode(err, CodeValidation) {
			t.Fatalf("parse alone must reject: %v", err)
		}
		if _, err := PrepareEvaluationRequest(v, PrepareOptions{}); err != nil {
			t.Fatalf("prepare must accept: %v", err)
		}
	})
	tw(t, "schema", "normalization is idempotent", func(t *testing.T) {
		once := NormalizeEvaluationRequest(nearMiss(t))
		twice := NormalizeEvaluationRequest(once)
		a, _ := EncodeJSON(once)
		b, _ := EncodeJSON(twice)
		if string(a) != string(b) {
			t.Fatalf("%s != %s", a, b)
		}
		if _, err := PrepareEvaluationRequest(once, PrepareOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, "schema", "admission enforces the byte budget on the serialized request", func(t *testing.T) {
		v := mustTree(t, `{"state":"`+strings.Repeat("🙂", 50)+`","questions":{"yes":{"type":"noul","instructions":"Is this synthetic?"}}}`)
		body, _ := EncodeJSON(v)
		n := len(body)
		if n >= DefaultMaxInputBytes {
			t.Fatal("fixture too large")
		}
		if _, err := PrepareEvaluationRequest(v, PrepareOptions{MaxInputBytes: n - 1}); !hasCode(err, CodeValidation) {
			t.Fatalf("one byte under the limit must fail: %v", err)
		}
		if _, err := PrepareEvaluationRequest(v, PrepareOptions{MaxInputBytes: n}); err != nil {
			t.Fatalf("exactly the limit must pass: %v", err)
		}
	})
	tw(t, "schema", "the default byte budget is 64 KiB", func(t *testing.T) {
		v := mustTree(t, `{"state":"`+strings.Repeat("x", DefaultMaxInputBytes)+`","questions":{"yes":{"type":"noul","instructions":"?"}}}`)
		if DefaultMaxInputBytes != 65536 {
			t.Fatal("default is not 64 KiB")
		}
		if _, err := PrepareEvaluationRequest(v, PrepareOptions{}); !hasCode(err, CodeValidation) {
			t.Fatalf("oversized request must fail: %v", err)
		}
		if _, err := PrepareEvaluationRequest(v, PrepareOptions{MaxInputBytes: DefaultMaxInputBytes * 2}); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, "schema", "admission still rejects non-JSON state", func(t *testing.T) {
		cyc := map[string]any{}
		cyc["self"] = cyc
		for name, state := range map[string]any{"cycle": cyc, "nan": map[string]any{"n": math.NaN()}} {
			_, err := PrepareEvaluationRequest(map[string]any{"state": state, "questions": map[string]any{"yes": map[string]any{"type": "noul", "instructions": "?"}}}, PrepareOptions{})
			if !hasCode(err, CodeValidation) {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
	tw(t, "schema", "every field the agent authors carries a description, so a bare union is not its only guidance", func(t *testing.T) {
		data, _ := json.Marshal(EvaluationSchema())
		var schema any
		_ = json.Unmarshal(data, &schema)
		authored := map[string]int{}
		var undescribed []string
		var walk func(any)
		walk = func(node any) {
			switch n := node.(type) {
			case []any:
				for _, e := range n {
					walk(e)
				}
			case map[string]any:
				if props, ok := n["properties"].(map[string]any); ok {
					for name, f := range props {
						authored[name]++
						if d, _ := f.(map[string]any)["description"].(string); d == "" {
							undescribed = append(undescribed, name)
						}
					}
				}
				for _, v := range n {
					walk(v)
				}
			}
		}
		walk(schema)
		if authored["type"] != 3 {
			t.Errorf("the schema must still offer noul, choice, and score: %d", authored["type"])
		}
		for _, name := range []string{"state", "questions", "model", "instructions", "criteria"} {
			if authored[name] == 0 {
				t.Errorf("%s is no longer authored", name)
			}
		}
		for _, name := range undescribed {
			// `true` and `false` inside noul criteria are the only fields whose parent already explains them.
			if name != "true" && name != "false" {
				t.Errorf("field %q has no description", name)
			}
		}
	})
}

// The cases below are the client.test.ts cases that exercise admission and need no network.
func TestClientAdmission(t *testing.T) {
	tw(t, "client", "structured state, descriptions, and null values are supported", func(t *testing.T) {
		v := typesafe.SystemOneRequest{State: typesafe.Null, Questions: typesafe.Questions{
			typesafe.Ask("yes", typesafe.Noul(map[string]any{"goal": "Check"}).Yes([]any{"a"}).No(nil)),
			typesafe.Ask("pick", typesafe.Choice([]any{"Choose"}, typesafe.Opt("a", map[string]any{"detail": "a"}), typesafe.Opt("b", nil))),
			typesafe.Ask("rating", typesafe.Score(nil, nil, map[string]any{"level": "high"})),
		}}
		if _, err := ParseEvaluationRequest(v); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, "client", "validation errors name the offending path, never the submitted value", func(t *testing.T) {
		cases := []struct {
			request string
			path    string
		}{
			{`{"state":"` + secret + `","questions":{"q":{"type":"choice","instructions":"` + secret + `","criteria":["a",1]}}}`, `questions\.q`},
			{`{"state":"` + secret + `","questions":{"q":{"type":"score","criteria":["` + secret + `"]}}}`, `questions\.q`},
			{`{"state":"` + secret + `","questions":{}}`, `questions`},
			{`{"state":"` + secret + `"}`, `questions|request`},
			{`{"state":"` + secret + `","questions":{"q":{"type":"noul","instructions":"ok"}},"extra":"` + secret + `"}`, `extra`},
		}
		for _, c := range cases {
			_, err := ParseEvaluationRequest(mustTree(t, c.request))
			if !hasCode(err, CodeValidation) {
				t.Fatalf("%s: %v", c.request, err)
			}
			msg := err.Error()
			if !regexpMatch(c.path, msg) || !strings.Contains(msg, "Expected { state, questions") || strings.Contains(msg, secret) {
				t.Errorf("message %q does not name %s, carry the usage, or leaks the value", msg, c.path)
			}
		}
	})
	tw(t, "client", "normalization accepts common model near-misses without loosening the schema", func(t *testing.T) {
		v := mustTree(t, `{"state":"s","questions":{
		  "team":{"type":"choice","instructions":"Which team?","options":["frontend","backend"]},
		  "level":{"type":"score","instructions":"How bad?","levels":["fine","bad"]},
		  "flag":{"type":"noul","instructions":"Is it?","criteria":"Yes when stated"},
		  "keep":{"type":"choice","instructions":"Already valid","criteria":{"a":"A","b":null}}}}`)
		r, err := ParseEvaluationRequest(NormalizeEvaluationRequest(v))
		if err != nil {
			t.Fatal(err)
		}
		q := func(id string) string { x, _ := r.Questions.Get(id); b, _ := EncodeJSON(x); return string(b) }
		want := map[string]string{
			"team":  `{"type":"choice","instructions":"Which team?","criteria":{"frontend":null,"backend":null}}`,
			"level": `{"type":"score","instructions":"How bad?","criteria":["fine","bad"]}`,
			"flag":  `{"type":"noul","instructions":"Is it?","criteria":{"true":"Yes when stated"}}`,
			"keep":  `{"type":"choice","instructions":"Already valid","criteria":{"a":"A","b":null}}`,
		}
		for id, w := range want {
			if q(id) != w {
				t.Errorf("%s = %s, want %s", id, q(id), w)
			}
		}
		for _, in := range []any{nil, "text", []any{}, mustTree(t, `{"questions":[]}`), mustTree(t, `{"questions":{"q":"text"}}`)} {
			out := NormalizeEvaluationRequest(in)
			a, _ := EncodeJSON(in)
			b, _ := EncodeJSON(out)
			if string(a) != string(b) {
				t.Errorf("normalize(%s) = %s", a, b)
			}
		}
		bad := mustTree(t, `{"state":"s","questions":{"q":{"type":"choice","options":["a",2]}}}`)
		if _, err := ParseEvaluationRequest(NormalizeEvaluationRequest(bad)); !hasCode(err, CodeValidation) {
			t.Fatalf("a non-string label must stay invalid: %v", err)
		}
	})
	tw(t, "client", "non-JSON state, getters, cycles, and excessive nesting are rejected", func(t *testing.T) {
		// Getters, Date and BigInt have no Go counterpart: a Go value either marshals to JSON or it does not.
		cycle := map[string]any{}
		cycle["self"] = cycle
		var nested any
		for i := 0; i < 70; i++ {
			nested = map[string]any{"nested": nested}
		}
		invalid := []any{cycle, map[string]any{"n": math.NaN()}, map[string]any{"n": math.Inf(1)}, map[string]any{"fn": func() {}}, map[string]any{"c": make(chan int)}, nested}
		questions := map[string]any{"yes": map[string]any{"type": "noul", "instructions": "?"}}
		for i, state := range invalid {
			if _, err := ParseEvaluationRequest(map[string]any{"state": state, "questions": questions}); !hasCode(err, CodeValidation) {
				t.Errorf("case %d: %v", i, err)
			}
		}
	})
}

func TestRequestTypedKeepsOrder(t *testing.T) {
	r, err := ParseEvaluationRequest(mustTree(t, `{"state":{"b":1,"a":2},"questions":{"z":{"type":"noul"},"a":{"type":"choice","criteria":{"y":null,"x":"d"}}},"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	typed, err := r.Typed()
	if err != nil {
		t.Fatal(err)
	}
	if len(typed.Questions) != 2 || typed.Questions[0].Name != "z" || typed.Questions[1].Name != "a" || typed.Model != "m" {
		t.Fatalf("typed = %+v", typed)
	}
	body, _ := json.Marshal(typed)
	if !strings.Contains(string(body), `"b":1,"a":2`) || !strings.Contains(string(body), `"y":null,"x":"d"`) {
		t.Fatalf("order lost: %s", body)
	}
}

func TestTreeQuestionOrderSurvivesAdmission(t *testing.T) {
	r, err := PrepareEvaluationRequest(mustTree(t, `{"state":"s","questions":{"z":{"type":"noul"},"a":{"type":"noul"}}}`), PrepareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Questions.Keys(); !reflect.DeepEqual(got, []string{"z", "a"}) {
		t.Fatalf("keys = %v", got)
	}
}
