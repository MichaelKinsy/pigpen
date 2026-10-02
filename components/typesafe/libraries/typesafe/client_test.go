package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sentAPIKey returns the API key the client puts on an outgoing request.
func sentAPIKey(t *testing.T, cfg Config) string {
	t.Helper()
	m := always(func() *http.Response { return jsonResp(200, map[string]any{"models": []any{}}) })
	cfg.HTTPClient = m
	c, err := NewClient(cfg)
	noErr(t, err)
	_, err = c.Models().List(ctxBG(), nil)
	noErr(t, err)
	return strings.TrimPrefix(m.req(0).Header.Get("Authorization"), "Bearer ")
}

// twin: client.test.ts | TypeSafeClient configuration falls back to defaults when neither config nor env is set
func TestClient_FallsBackToDefaultsWhenNeitherConfigNorEnvIsSet(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration falls back to defaults when neither config nor env is set")
	c, err := NewClient(Config{APIKey: "k", Getenv: noEnv})
	noErr(t, err)
	eq(t, c.BaseURL(), DefaultBaseURL)
	eq(t, c.DefaultModel(), DefaultModel)
	eq(t, c.LogLevel(), DefaultLogLevel)
	eq(t, c.RetryPolicy(), DefaultRetryPolicy())
	eq(t, c.Timeout(), DefaultTimeout)
}

func TestClient_ReadsEverySettingFromTheEnvironment(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration reads every setting from the environment")
	env := envOf(map[string]string{EnvAPIKey: "env-key", EnvBaseURL: "https://env.test", EnvDefaultModel: "env-model", EnvLogLevel: "debug"})
	c, err := NewClient(Config{Getenv: env, Logger: &recordingLogger{}})
	noErr(t, err)
	eq(t, sentAPIKey(t, Config{Getenv: env, Logger: &recordingLogger{}}), "env-key")
	eq(t, c.BaseURL(), "https://env.test")
	eq(t, c.DefaultModel(), "env-model")
	eq(t, c.LogLevel(), LogDebug)
}

func TestClient_PrefersConfigOverTheEnvironment(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration prefers config over the environment")
	env := envOf(map[string]string{EnvAPIKey: "env-key", EnvBaseURL: "https://env.test", EnvDefaultModel: "env-model", EnvLogLevel: "debug"})
	c, err := NewClient(Config{APIKey: "code-key", BaseURL: "https://code.test", DefaultModel: "code-model", LogLevel: LogError, Getenv: env})
	noErr(t, err)
	eq(t, sentAPIKey(t, Config{APIKey: "code-key", Getenv: env}), "code-key")
	eq(t, c.BaseURL(), "https://code.test")
	eq(t, c.DefaultModel(), "code-model")
	eq(t, c.LogLevel(), LogError)
}

func TestClient_KeepsTheAPIKeyOffTheInstanceSoLoggingTheClientCannotLeakIt(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration keeps the API key off the instance so logging the client cannot leak it")
	c, err := NewClient(Config{APIKey: "super-secret", Getenv: noEnv})
	noErr(t, err)
	for _, s := range []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprint(c), fmt.Sprintf("%s", c)} {
		if strings.Contains(s, "super-secret") {
			t.Fatalf("formatted client leaks the key: %s", s)
		}
	}
	raw, err := json.Marshal(c)
	noErr(t, err)
	if strings.Contains(string(raw), "super-secret") {
		t.Fatalf("JSON leaks the key: %s", raw)
	}
	typ := reflect.TypeOf(c).Elem()
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() && strings.Contains(strings.ToLower(f.Name), "key") {
			t.Fatalf("exported key field %s", f.Name)
		}
	}
}

func TestClient_TreatsEmptyAndWhitespaceOnlyEnvValuesAsUnset(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration treats empty and whitespace-only env values as unset")
	env := envOf(map[string]string{EnvAPIKey: "k", EnvBaseURL: "   ", EnvDefaultModel: "", EnvLogLevel: ""})
	c, err := NewClient(Config{Getenv: env})
	noErr(t, err)
	eq(t, c.BaseURL(), DefaultBaseURL)
	eq(t, c.DefaultModel(), DefaultModel)
	eq(t, c.LogLevel(), DefaultLogLevel)
	// A whitespace-only key is also unset.
	_, err = NewClient(Config{Getenv: envOf(map[string]string{EnvAPIKey: "  \t "})})
	if err == nil {
		t.Fatal("a whitespace-only key must not be accepted")
	}
}

func TestClient_ThrowsATypeSafeErrorNamingTheEnvVarWhenNoAPIKeyIsAvailable(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration throws a TypeSafeError naming the env var when no API key is available")
	_, err := NewClient(Config{Getenv: noEnv})
	if err == nil {
		t.Fatal("want an error")
	}
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), EnvAPIKey)
}

func TestClient_StripsTrailingSlashesFromBaseURLFromEitherSource(t *testing.T) {
	twin(t, "client.test.ts | TypeSafeClient configuration strips trailing slashes from baseURL from either source")
	c, err := NewClient(Config{APIKey: "k", Getenv: envOf(map[string]string{EnvBaseURL: "https://example.test///"})})
	noErr(t, err)
	eq(t, c.BaseURL(), "https://example.test")
	c, err = NewClient(Config{APIKey: "k", BaseURL: "https://x.test/", Getenv: noEnv})
	noErr(t, err)
	eq(t, c.BaseURL(), "https://x.test")
}

func TestClient_AcceptsEveryLogLevel(t *testing.T) {
	twin(t,
		"client.test.ts | TypeSafeClient configuration accepts log level debug",
		"client.test.ts | TypeSafeClient configuration accepts log level info",
		"client.test.ts | TypeSafeClient configuration accepts log level warn",
		"client.test.ts | TypeSafeClient configuration accepts log level error",
		"client.test.ts | TypeSafeClient configuration accepts log level off")
	for _, level := range LogLevels {
		c, err := NewClient(Config{APIKey: "k", LogLevel: level, Getenv: noEnv})
		noErr(t, err)
		eq(t, c.LogLevel(), level)
		c, err = NewClient(Config{APIKey: "k", Getenv: envOf(map[string]string{EnvLogLevel: string(level)})})
		noErr(t, err)
		eq(t, c.LogLevel(), level)
	}
}

func TestClient_RejectsAnInvalidLogLevelAndNamesWhereItCameFrom(t *testing.T) {
	// Adapted: the option is named `LogLevel` in Go, so the message names "the `LogLevel` option".
	twin(t, "client.test.ts | TypeSafeClient configuration rejects an invalid log level and names where it came from")
	_, err := NewClient(Config{APIKey: "k", Getenv: envOf(map[string]string{EnvLogLevel: "loud"})})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), `"loud" from `+EnvLogLevel)
	contains(t, err.Error(), "debug, info, warn, error, off")
	_, err = NewClient(Config{APIKey: "k", LogLevel: "loud", Getenv: noEnv})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "the `LogLevel` option")
}

func TestRequests_SendsAuthAndIdentifyingHeaders(t *testing.T) {
	twin(t, "client.test.ts | requests sends auth and identifying headers")
	m := always(func() *http.Response { return jsonResp(200, map[string]any{"models": []any{}}) })
	c, err := NewClient(Config{APIKey: "secret", BaseURL: "https://x.test", HTTPClient: m, Getenv: noEnv})
	noErr(t, err)
	_, err = c.Models().List(ctxBG(), nil)
	noErr(t, err)
	eq(t, m.count(), 1)
	r := m.req(0)
	eq(t, r.URL, "https://x.test/v1/models")
	eq(t, r.Method, "GET")
	eq(t, r.Header.Get("Authorization"), "Bearer secret")
	eq(t, r.Header.Get("User-Agent"), "typesafe-sdk-go/"+Version)
	eq(t, r.Header.Get("X-TypeSafe-SDK"), "typesafe-sdk-go/"+Version)
	runtimeHeader := r.Header.Get("X-TypeSafe-Runtime")
	eq(t, runtimeHeader, "go/"+strings.TrimPrefix(runtime.Version(), "go")+" ("+runtime.GOOS+"; "+runtime.GOARCH+")")
	if !regexp.MustCompile(`^go/\d+\.\d+(\.\d+)? \(\w+; \w+\)$`).MatchString(runtimeHeader) {
		t.Fatalf("runtime header %q", runtimeHeader)
	}
	eq(t, r.Header.Get("Accept"), "application/json")
	eq(t, r.Header.Get("Content-Type"), "")
}

func TestRequests_MergesDefaultHeadersAndPerCallHeadersPerCallWinningNeverClobberingAuth(t *testing.T) {
	twin(t, "client.test.ts | requests merges defaultHeaders and per-call headers, per-call winning, never clobbering auth")
	m := always(func() *http.Response { return jsonResp(200, map[string]any{"models": []any{}}) })
	c := newClient(t, m, func(cfg *Config) {
		cfg.APIKey = "secret"
		cfg.DefaultHeaders = map[string]string{"X-Trace": "client", "X-Only-Default": "yes", "Authorization": "nope"}
	})
	_, err := c.Models().List(ctxBG(), &RequestOptions{Headers: map[string]string{"X-Trace": "call", "X-Only-Call": "yes"}})
	noErr(t, err)
	h := m.req(0).Header
	eq(t, h.Get("X-Trace"), "call")
	eq(t, h.Get("X-Only-Default"), "yes")
	eq(t, h.Get("X-Only-Call"), "yes")
	eq(t, h.Get("Authorization"), "Bearer secret")
}

func TestRequests_ModelsListUnwrapsTheDocumentedResponse(t *testing.T) {
	twin(t,
		`client.test.ts | requests models.list() unwraps the documented response: {"cards":[]}`,
		`client.test.ts | requests models.list() unwraps the documented response: {"cards":[{"name":"m","description":"d","release_date":"2026"}]}`)
	for _, cards := range [][]ModelCard{{}, modelCards} {
		wire := map[string]any{"models": cards}
		c := newClient(t, always(func() *http.Response { return jsonResp(200, wire) }))
		got, err := c.Models().List(ctxBG(), nil)
		noErr(t, err)
		eq(t, got, cards)
		res, err := c.Models().ListWithResponse(ctxBG(), nil)
		noErr(t, err)
		eq(t, res.Data, cards)
		eq(t, res.Status, 200)
	}
}

func TestRequests_ModelsListFailsClearlyOnAnUnrecognizedShape(t *testing.T) {
	twin(t,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":null}`,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":[]}`,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":{"models":{"models":[]}}}`,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":{"models":null}}`,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":{"models":"bad"}}`,
		`client.test.ts | requests models.list() fails clearly on an unrecognized shape: {"wire":{"ok":true}}`)
	for _, wire := range []string{`null`, `[]`, `{"models":{"models":[]}}`, `{"models":null}`, `{"models":"bad"}`, `{"ok":true}`} {
		c := newClient(t, always(func() *http.Response { return jsonResp(200, decode(t, wire)) }))
		_, err := c.Models().List(ctxBG(), nil)
		if err == nil {
			t.Fatalf("%s: want an error", wire)
		}
		mustAs[*TypeSafeError](t, err)
		contains(t, err.Error(), "Unexpected response shape from GET /v1/models")
	}
}

func TestRequests_PreservesEmployeeOnlyModelFieldsThroughRawAccess(t *testing.T) {
	// Adapted: Go has no unconsumed Response body; the raw response carries the buffered body.
	twin(t, "client.test.ts | requests preserves employee-only model fields through raw access")
	wire := map[string]any{"models": []any{map[string]any{"name": "m", "description": "d", "release_date": "2026", "tags": []any{"internal"}}}}
	c := newClient(t, always(func() *http.Response { return jsonResp(200, wire) }))
	raw, err := c.Models().ListRaw(ctxBG(), nil)
	noErr(t, err)
	jsonEq(t, json.RawMessage(raw.Body), string(mustJSON(t, wire)))
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	noErr(t, err)
	return b
}

func TestRequests_PostsTheSystemOnePayloadWithTheDefaultModel(t *testing.T) {
	twin(t, "client.test.ts | requests posts the systemOne payload with the default model")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c, err := NewClient(Config{APIKey: "k", BaseURL: "https://x.test", HTTPClient: m, Getenv: noEnv})
	noErr(t, err)
	res, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Value(map[string]any{"a": 1}), Questions: Questions{Ask("q1", Noul("x"))}}, nil)
	noErr(t, err)
	r := m.req(0)
	eq(t, r.URL, "https://x.test/v1/systemone")
	eq(t, r.Method, "POST")
	eq(t, r.Body, decode(t, `{"state":{"a":1},"model":"jev-latest","questions":{"q1":{"type":"noul","instructions":"x"}}}`))
	eq(t, r.Header.Get("Content-Type"), "application/json")
	a, err := res.Noul("q1")
	noErr(t, err)
	eq(t, a.Noul, 0.5)
}

func TestRequests_SendsTheRequestObjectAsThePayloadFillingInTheDefaultModel(t *testing.T) {
	twin(t, "client.test.ts | requests sends the request object as the payload, filling in the default model")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m)
	req := SystemOneRequest{State: Value(map[string]any{"a": 1}), Questions: Questions{Ask("q1", Noul("x"))}}
	_, err := c.SystemOne(ctxBG(), req, nil)
	noErr(t, err)
	eq(t, m.req(0).Body, decode(t, `{"state":{"a":1},"model":"jev-latest","questions":{"q1":{"type":"noul","instructions":"x"}}}`))
	req.Model = "explicit"
	_, err = c.SystemOne(ctxBG(), req, nil)
	noErr(t, err)
	eq(t, m.req(1).Body, decode(t, `{"state":{"a":1},"model":"explicit","questions":{"q1":{"type":"noul","instructions":"x"}}}`))
}

func TestRequests_HonorsPerCallModelAndClientDefaultModel(t *testing.T) {
	twin(t, "client.test.ts | requests honors per-call model and client defaultModel")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m, func(cfg *Config) { cfg.DefaultModel = "client-default" })
	qs := Questions{Ask("q", Choice("c", Opt("a", nil)))}
	_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: qs}, nil)
	noErr(t, err)
	_, err = c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: qs, Model: "per-call"}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body.(map[string]any)["model"], any("client-default"))
	eq(t, m.req(1).Body.(map[string]any)["model"], any("per-call"))
}

func TestRequests_AbortingTheCallersSignalAbortsTheSignalHandedToFetchAndWrapsTheError(t *testing.T) {
	// Adapted: the AbortSignal is the call's context.
	twin(t, "client.test.ts | requests aborting the caller's signal aborts the signal handed to fetch, and wraps the error")
	ctx, cancel := context.WithCancel(context.Background())
	m := newMock(func(r *recordedRequest) (*http.Response, error) {
		cancel()
		return nil, context.Canceled
	})
	c := newClient(t, m)
	_, err := c.Models().List(ctx, nil)
	mustAs[*APIUserAbortError](t, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the abort must carry the context error: %v", err)
	}
	if m.req(0).Ctx.Err() == nil {
		t.Fatal("the context handed to the transport must be cancelled")
	}
	contains(t, err.Error(), "Request was aborted.")
}

func TestRequests_WrapsNetworkFailuresInAPIConnectionErrorWithTheCause(t *testing.T) {
	twin(t, "client.test.ts | requests wraps network failures in APIConnectionError with the cause")
	boom := errors.New("fetch failed")
	c := newClient(t, newMock(func(*recordedRequest) (*http.Response, error) { return nil, boom }), func(cfg *Config) { cfg.Retry = RetryOverrides{MaxRetries: Ptr(0)} })
	_, err := c.Models().List(ctxBG(), nil)
	conn := mustAs[*APIConnectionError](t, err)
	if !errors.Is(conn, boom) || !errors.Is(err, boom) {
		t.Fatal("the cause must be reachable")
	}
	contains(t, err.Error(), "fetch failed")
	contains(t, err.Error(), "Connection error:")
}

func TestQuestionBuilders_ChoiceAcceptsACriteriaObjectAsIs(t *testing.T) {
	twin(t, "client.test.ts | question builders choice accepts a criteria object as-is")
	jsonEq(t, Choice("q", Opt("a", "desc"), Opt("b", nil)), `{"type":"choice","instructions":"q","criteria":{"a":"desc","b":null}}`)
}

func TestQuestionBuilders_ChoiceRejectsTheRemovedLabelListShorthandInJavaScriptToo(t *testing.T) {
	skipTwin(t, "JavaScript-caller check: Go's Choice takes []Option, so a bare label list cannot be expressed; the JSON-side rejection is TestParseQuestions_RejectsAChoiceLabelList",
		"client.test.ts | question builders choice rejects the removed label-list shorthand in JavaScript too")
}

func TestQuestionBuilders_NoulAllowsDescribingOneSideBothOrNeither(t *testing.T) {
	twin(t, "client.test.ts | question builders noul allows describing one side, both, or neither")
	jsonEq(t, Noul("q"), `{"type":"noul","instructions":"q"}`)
	jsonEq(t, Noul("q").Yes("yes means this"), `{"type":"noul","instructions":"q","criteria":{"true":"yes means this"}}`)
	jsonEq(t, Noul("q").No("no means this").Criteria, `{"false":"no means this"}`)
	jsonEq(t, Noul("q").Yes("a").No("b").Criteria, `{"true":"a","false":"b"}`)
}

func TestQuestionBuilders_DescriptionsCanBeJSONObjectsNotOnlyStrings(t *testing.T) {
	twin(t, "client.test.ts | question builders descriptions can be JSON objects, not only strings")
	rich := map[string]any{"summary": "warm", "examples": []any{"hi!", "welcome"}}
	q := Choice("q", Opt("friendly", rich), Opt("hostile", nil))
	jsonEq(t, q.Criteria[0].Description, `{"summary":"warm","examples":["hi!","welcome"]}`)
	jsonEq(t, Score("q", rich, "meh").Criteria[0], `{"summary":"warm","examples":["hi!","welcome"]}`)
	jsonEq(t, Noul("q").Yes(rich).Criteria.True, `{"summary":"warm","examples":["hi!","welcome"]}`)
}

func TestQuestionBuilders_ChoiceAnswersCarryTheWinningLabel(t *testing.T) {
	twin(t, "client.test.ts | question builders choice answers carry the winning label")
	wire := map[string]any{"model": "m", "answers": map[string]any{"q": map[string]any{"type": "choice", "choice": "b", "confidence": 0.9, "probabilities": map[string]any{"a": 0.1, "b": 0.9}}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}
	c := newClient(t, always(func() *http.Response { return jsonResp(200, wire) }))
	r, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Choice("q", Opt("a", nil), Opt("b", nil)))}}, nil)
	noErr(t, err)
	a, err := r.Choice("q")
	noErr(t, err)
	eq(t, a.Choice, "b")
	eq(t, a.Probabilities["b"], 0.9)
}

func TestQuestionBuilders_ScoreKeepsTheListItWasGivenAndRejectsMaps(t *testing.T) {
	twin(t, "client.test.ts | question builders score keeps the list it was given and rejects maps")
	eq(t, Score("q", "bad", "good").Criteria, []Entry{Text("bad"), Text("good")})
	// The "rejects maps" half is a JavaScript-caller check: Go's Score takes a list. The JSON-side
	// rejection is TestParseQuestions_RejectsAScoreMap.
}

func TestWireFormat_PreservesNullStateInstructionsAndCriteriaValues(t *testing.T) {
	twin(t, "client.test.ts | wire format preserves null state, instructions, and criteria values")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m)
	qs := Questions{
		Ask("noul", Noul(nil).Yes(nil).No(nil)),
		Ask("noCriteria", Noul(nil).NullCriteria()),
		Ask("choice", Choice(nil, Opt("yes", nil), Opt("no", nil))),
		Ask("score", Score(nil, nil, "high")),
	}
	_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Null, Questions: qs}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body, decode(t, `{"model":"jev-latest","state":null,"questions":{
		"noul":{"type":"noul","instructions":null,"criteria":{"true":null,"false":null}},
		"noCriteria":{"type":"noul","instructions":null,"criteria":null},
		"choice":{"type":"choice","instructions":null,"criteria":{"yes":null,"no":null}},
		"score":{"type":"score","instructions":null,"criteria":[null,"high"]}}}`))
}

func TestWireFormat_AllowsOmittedInstructionsAndPreservesJSONArrays(t *testing.T) {
	twin(t, "client.test.ts | wire format allows omitted instructions and preserves JSON arrays")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m)
	qs := Questions{
		Ask("noul", NoulQuestion{Criteria: &NoulCriteria{Null: true}}),
		Ask("choice", ChoiceQuestion{Criteria: []Option{{Label: "yes", Description: Value([]any{nil, map[string]any{"example": true}})}}}),
		Ask("score", ScoreQuestion{Criteria: []Entry{Value([]any{nil, "low"}), Null}}),
		Ask("arrayInstructions", Noul([]any{nil, map[string]any{"examples": []any{1, false}}}).Yes([]any{"yes", nil})),
		Ask("defaultInstructions", Noul(nil)),
	}
	state := Value([]any{nil, map[string]any{"messages": []any{"hello"}}})
	_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: state, Questions: qs}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body, decode(t, `{"model":"jev-latest","state":[null,{"messages":["hello"]}],"questions":{
		"noul":{"type":"noul","criteria":null},
		"choice":{"type":"choice","criteria":{"yes":[null,{"example":true}]}},
		"score":{"type":"score","criteria":[[null,"low"],null]},
		"arrayInstructions":{"type":"noul","instructions":[null,{"examples":[1,false]}],"criteria":{"true":["yes",null]}},
		"defaultInstructions":{"type":"noul","instructions":null}}}`))
}

func TestWireFormat_SendsChoiceCriteriaWithoutRewriting(t *testing.T) {
	twin(t, "client.test.ts | wire format sends choice criteria without rewriting")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	_, err := newClient(t, m).SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Choice("which?", Opt("a", nil), Opt("b", nil)))}}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body.(map[string]any)["questions"].(map[string]any)["q"].(map[string]any)["criteria"], decode(t, `{"a":null,"b":null}`))
}

func TestWireFormat_SendsAScoreListUntouched(t *testing.T) {
	twin(t, "client.test.ts | wire format sends a score list untouched")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	_, err := newClient(t, m).SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Score("q", "bad", "ok", "great"))}}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body.(map[string]any)["questions"].(map[string]any)["q"].(map[string]any)["criteria"], decode(t, `["bad","ok","great"]`))
}

func TestWireFormat_RejectsNonListScoreCriteriaFewerThanTwoCriteriaAndEmptyQuestionSetsBeforeSending(t *testing.T) {
	// Adapted: the non-list (map) criteria case is a JavaScript-caller input; Go's ScoreQuestion.Criteria is a slice.
	twin(t, "client.test.ts | wire format rejects non-list score criteria, fewer than two criteria, and empty question sets before sending")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m)
	bad := func(criteria ...Entry) error {
		_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", ScoreQuestion{Criteria: criteria})}}, nil)
		return err
	}
	err := bad()
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), `Score question "q" has 0 criteria; at least two scores are required.`)
	err = bad(Text("only"))
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "at least two scores")
	_, err = c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{}}, nil)
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "At least one question is required")
	eq(t, m.count(), 0)
}

func TestPrimitiveContract_UsesATenSecondDefaultAndKeepsRawMetadataAccess(t *testing.T) {
	twin(t, "client.test.ts | 1.0 primitive contract uses a ten-second default and keeps raw metadata access")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse, "x-typesafe-request-id", "req_1") })
	c := newClient(t, m)
	eq(t, c.Timeout(), 10*time.Second)
	res, err := c.SystemOneWithResponse(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q1", Noul("?"))}}, nil)
	noErr(t, err)
	jsonEq(t, res.Data, string(mustJSON(t, systemOneResponse)))
	eq(t, res.RequestID, "req_1")
	eq(t, m.count(), 1)
}

func TestPrimitiveContract_ForwardsExtraFieldsAndNull(t *testing.T) {
	twin(t, "client.test.ts | 1.0 primitive contract forwards extra fields and null")
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	c := newClient(t, m)
	_, err := c.SystemOne(ctxBG(), SystemOneRequest{
		State: Text("s"), Questions: Questions{Ask("q", Score("?", "low", "high"))},
		Extra: map[string]any{"future_option": nil, "nested": map[string]any{"enabled": true}},
	}, nil)
	noErr(t, err)
	eq(t, m.req(0).Body, decode(t, `{"state":"s","questions":{"q":{"type":"score","instructions":"?","criteria":["low","high"]}},"future_option":null,"nested":{"enabled":true},"model":"jev-latest"}`))
	_, err = c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Noul("?"))}}, nil)
	noErr(t, err)
	if _, has := m.req(1).Body.(map[string]any)["future_option"]; has {
		t.Fatal("extra fields must not leak between requests")
	}
}

// Go-specific cases beyond the upstream suite.

func TestRequests_ExtraFieldsMayNotShadowTheRequestFields(t *testing.T) {
	c := newClient(t, always(func() *http.Response { return jsonResp(200, systemOneResponse) }))
	for _, name := range []string{"state", "questions", "model"} {
		_, err := c.SystemOne(ctxBG(), SystemOneRequest{State: Text("s"), Questions: Questions{Ask("q", Noul("?"))}, Extra: map[string]any{name: 1}}, nil)
		mustAs[*TypeSafeError](t, err)
		contains(t, err.Error(), name)
	}
}

func TestRequests_ARequestWithoutStateOmitsIt(t *testing.T) {
	m := always(func() *http.Response { return jsonResp(200, systemOneResponse) })
	_, err := newClient(t, m).SystemOne(ctxBG(), SystemOneRequest{Questions: Questions{Ask("q", Noul("?"))}}, nil)
	noErr(t, err)
	if _, has := m.req(0).Body.(map[string]any)["state"]; has {
		t.Fatal("an omitted state must not be sent")
	}
}

func TestRequests_NegativeTimeoutIsRejected(t *testing.T) {
	_, err := NewClient(Config{APIKey: "k", Timeout: -time.Second, Getenv: noEnv})
	mustAs[*TypeSafeError](t, err)
	contains(t, err.Error(), "Timeout")
}
