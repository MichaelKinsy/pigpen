package typesafe

import (
	"encoding/json"
	"net/http"
	"testing"
)

const allAnswersJSON = `{"model":"m","answers":{
  "a":{"type":"noul","noul":0.25},
  "b":{"type":"choice","choice":"yes","confidence":0.5,"probabilities":{"yes":0.75,"no":0.25}},
  "c":{"type":"score","score":0.5,"confidence":0.4,"legend":{"0":"bad","1":"ok"},"probabilities":{"0":0.5,"1":0.5}}},
  "usage":{"input_tokens":3,"output_tokens":4}}`

func decodeResult(t *testing.T, s string) *SystemOneResult {
	t.Helper()
	var r SystemOneResult
	noErr(t, json.Unmarshal([]byte(s), &r))
	return &r
}

func TestTypes_MapsEachQuestionToItsResponseTypeWithLiteralCriteriaKeys(t *testing.T) {
	// Adapted: TypeScript infers the answer type from the question literals; Go returns
	// concrete answer types from typed accessors, and a wrong name or type is an error.
	twin(t, "types.test-d.ts | test/types.test-d.ts systemOne result inference maps each question to its response type with literal criteria keys")
	r := decodeResult(t, allAnswersJSON)
	var noul NoulAnswer
	var choice ChoiceAnswer
	var score ScoreAnswer
	var err error
	noul, err = r.Noul("a")
	noErr(t, err)
	choice, err = r.Choice("b")
	noErr(t, err)
	score, err = r.Score("c")
	noErr(t, err)
	var _ float64 = noul.Noul
	var _ string = choice.Choice
	var _ map[string]float64 = choice.Probabilities
	var _ float64 = score.Score
	eq(t, choice.Probabilities, map[string]float64{"yes": 0.75, "no": 0.25})
	if _, err = r.Choice("d"); err == nil {
		t.Fatal("an unknown answer name must be an error")
	}
	if _, err = r.Noul("b"); err == nil {
		t.Fatal("a type mismatch must be an error")
	}
	contains(t, err.Error(), `"b"`)
}

func TestTypes_TypesScoreListsByIndexWithTheLegendKeyedByScore(t *testing.T) {
	twin(t, "types.test-d.ts | test/types.test-d.ts systemOne result inference types score lists by index, with the legend keyed by score")
	r := decodeResult(t, `{"model":"m","answers":{"c":{"type":"score","score":1,"confidence":1,"legend":{"0":"bad","1":"ok","2":{"rich":"great"}},"probabilities":{"0":0,"1":0,"2":1}}},"usage":{"input_tokens":0,"output_tokens":0}}`)
	s, err := r.Score("c")
	noErr(t, err)
	eq(t, s.Probabilities, map[int]float64{0: 0, 1: 0, 2: 1})
	eq(t, s.Legend[2], any(map[string]any{"rich": "great"}))
	eq(t, s.Legend[0], any("bad"))
	if _, has := s.Legend[3]; has {
		t.Fatal("score 3 was not defined")
	}
}

func TestTypes_TypeLevelInference(t *testing.T) {
	skipTwin(t, "TypeScript conditional-type inference and readonly modifiers have no Go counterpart: non-literal score lists, readonly results and client settings (the accessors return copies, TestRetryPolicy_IsolatesThe...), and the APIPromise type",
		"types.test-d.ts | test/types.test-d.ts systemOne result inference degrades to numeric indexing for non-literal score lists",
		"types.test-d.ts | test/types.test-d.ts systemOne result inference results are readonly",
		"types.test-d.ts | test/types.test-d.ts systemOne result inference client settings are readonly",
		"types.test-d.ts | test/types.test-d.ts APIPromise client methods return APIPromise")
}

func TestTypes_AcceptsAPartialRetryPolicyOnTheClientAndPerCall(t *testing.T) {
	twin(t, "types.test-d.ts | test/types.test-d.ts systemOne result inference accepts a partial retry policy on the client and per call")
	partial := RetryOverrides{MaxRetries: Ptr(1), HTTPStatuses: []int{503}}
	c := newClient(t, modelsOK(), func(cfg *Config) { cfg.Retry = partial })
	_, err := c.Models().List(ctxBG(), &RequestOptions{Retry: RetryOverrides{HTTPStatuses: []int{503}, BackoffJitter: Ptr(0.0)}})
	noErr(t, err)
}

func TestTypes_CriteriaShapes(t *testing.T) {
	skipTwin(t, "compile-time rejections (a label list for choice, a numeric-keyed map or a short list for score) are properties of the TypeScript types; in Go Choice takes []Option and Score takes a list, and the runtime rules are Questions.Validate and ParseQuestions",
		"types.test-d.ts | test/types.test-d.ts criteria shapes rejects label-list shorthand for choice",
		"types.test-d.ts | test/types.test-d.ts criteria shapes rejects numeric-keyed maps for score")
}

func TestTypes_ConstrainsDescriptionsWhileAllowingNullForEveryQuestionType(t *testing.T) {
	// Adapted: numbers are not valid descriptions; Go reports them when the request is built.
	twin(t, "types.test-d.ts | test/types.test-d.ts question helpers constrains descriptions while allowing null for every question type")
	for _, q := range []Question{
		Score("q", "ok", nil), Noul(nil).Yes(nil).No(nil), Noul(nil).NullCriteria(), Noul(nil), Choice(nil, Opt("yes", nil)), Score(nil, nil, nil),
		Choice("q", Opt("yes", nil), Opt("no", "ok"), Opt("maybe", map[string]any{"detail": "rich"})),
		Score("q", "bad", "ok", map[string]any{"detail": "rich"}), Noul("q").Yes(map[string]any{"detail": "rich"}), Noul("q").No("only one side"),
	} {
		if _, err := json.Marshal(q); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	for _, q := range []Question{Choice("q", Opt("yes", 1)), Score("q", 1, 2), Noul("q").Yes(true)} {
		_, err := json.Marshal(q)
		if err == nil {
			t.Fatalf("%T: a number or boolean description must not marshal", q)
		}
		mustAs[*TypeSafeError](t, unwrapMarshal(err))
	}
}

// unwrapMarshal digs the *TypeSafeError out of a json.MarshalerError.
func unwrapMarshal(err error) error {
	var me *json.MarshalerError
	if asErr(err, &me) {
		return me.Err
	}
	return err
}

func TestTypes_PreservesResultInferenceWithNullsArraysAndOmittedInstructions(t *testing.T) {
	twin(t, "types.test-d.ts | test/types.test-d.ts question helpers preserves result inference with nulls, arrays, and omitted instructions")
	r := decodeResult(t, `{"model":"m","answers":{
	  "noul":{"type":"noul","noul":1},
	  "choice":{"type":"choice","choice":"yes","confidence":1,"probabilities":{"yes":1,"no":0}},
	  "list":{"type":"score","score":1,"confidence":1,"legend":{"0":null,"1":"high"},"probabilities":{"0":0,"1":1}}},"usage":{"input_tokens":0,"output_tokens":0}}`)
	l, err := r.Score("list")
	noErr(t, err)
	eq(t, l.Legend, map[int]any{0: nil, 1: "high"})
	eq(t, l.Probabilities, map[int]float64{0: 0, 1: 1})
	c, err := r.Choice("choice")
	noErr(t, err)
	eq(t, c.Choice, "yes")
	_, err = json.Marshal(Questions{
		Ask("q", Noul([]any{nil, "instructions"}).Yes([]any{nil, "criterion"})),
		Ask("c", Choice([]any{"instructions"}, Opt("yes", []any{nil, "criterion"}))),
		Ask("s", Score([]any{"instructions"}, []any{nil, "criterion"}, nil)),
	})
	noErr(t, err)
}

func TestTypes_AreAllAssignableToQuestion(t *testing.T) {
	twin(t, "types.test-d.ts | test/types.test-d.ts question helpers are all assignable to Question")
	var _ Question = Noul("x")
	var _ Question = Choice("x", Opt("a", nil))
	var _ Question = Choice("x", Opt("a", nil), Opt("b", nil))
	var _ Question = Score("x", "a", "b")
	eq(t, Noul("x").QuestionType(), TypeNoul)
	eq(t, Choice("x").QuestionType(), TypeChoice)
	eq(t, Score("x").QuestionType(), TypeScore)
}

func TestTypes_AwaitingYieldsThePlainResult(t *testing.T) {
	twin(t, "types.test-d.ts | test/types.test-d.ts APIPromise awaiting yields the plain result")
	m := always(func() *http.Response {
		return jsonResp(200, decode(t, allAnswersJSON), "x-typesafe-request-id", "req_1")
	})
	c := newClient(t, m)
	res, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("a", Noul("x"))}}, nil)
	noErr(t, err)
	var _ *SystemOneResult = res
	a, err := res.Noul("a")
	noErr(t, err)
	eq(t, a.Noul, 0.25)
	wr, err := c.SystemOneWithResponse(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("a", Noul("x"))}}, nil)
	noErr(t, err)
	var _ string = wr.RequestID
	eq(t, wr.RequestID, "req_1")
	eq(t, wr.Data.Usage, Usage{InputTokens: 3, OutputTokens: 4})
}
