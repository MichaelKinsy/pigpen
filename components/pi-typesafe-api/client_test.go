package pitypesafe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

func testClient(t *testing.T, opts Options, doer typesafe.HTTPDoer) *TypeSafe {
	t.Helper()
	if opts.APIKey == "" && opts.Backend == nil {
		opts.APIKey = "test-key"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = doer
	}
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func failing(status int, header http.Header, calls *atomic.Int32) doerFunc {
	return func(*http.Request) (*http.Response, error) {
		if calls != nil {
			calls.Add(1)
		}
		return jsonResponse(status, map[string]any{"error": map[string]any{"code": "x", "message": "never-print-me"}, "secret": "never-print-me"}, header), nil
	}
}

func TestClient(t *testing.T) {
	tw(t, "client", "official SDK helpers, typed answers, metadata, and one batched network call", func(t *testing.T) {
		isolate(t)
		calls := 0
		questions := typesafe.Questions{
			typesafe.Ask("category", typesafe.Choice("Which?", typesafe.Opt("billing", "Charges"), typesafe.Opt("other", nil))),
			typesafe.Ask("urgent", typesafe.Noul("Urgent?")),
			typesafe.Ask("frustration", typesafe.Score("Frustration?", "Calm", "Angry")),
		}
		client := testClient(t, Options{}, doerFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			body := requestBody(t, r)
			var req struct {
				Model     string          `json:"model"`
				Questions json.RawMessage `json:"questions"`
			}
			_ = json.Unmarshal(body, &req)
			want, _ := json.Marshal(questions)
			if r.URL.String() != "https://api.typesafe.ai/v1/systemone" || r.Method != http.MethodPost || string(req.Questions) != string(want) || req.Model != "jev-latest" || !strings.Contains(r.Header.Get("Authorization"), "test-key") {
				t.Errorf("url=%s method=%s model=%s questions=%s", r.URL, r.Method, req.Model, req.Questions)
			}
			return responseFor(body), nil
		}))
		result, err := client.Evaluate(context.Background(), typesafe.SystemOneRequest{State: typesafe.Value(map[string]any{"message": "Example"}), Questions: questions})
		if err != nil {
			t.Fatal(err)
		}
		category, _ := result.Choice("category")
		urgent, _ := result.Noul("urgent")
		frustration, _ := result.Score("frustration")
		if category.Choice != "billing" || urgent.Noul != 0.9 || frustration.Score != 0 || calls != 1 || result.ElapsedMs < 0 {
			t.Fatalf("answers = %+v %+v %+v calls=%d", category, urgent, frustration, calls)
		}
		if got := client.GetUsage(); got != (UsageSnapshot{RequestsStarted: 1, RequestsSucceeded: 1, InputTokens: 42, EstimatedUSD: 0.000002}) {
			t.Errorf("usage = %+v", got)
		}
		spend := client.GetSpend()
		if spend.Session.RequestsStarted != 1 || spend.Today.InputTokens != 42 || spend.Blocked != nil {
			t.Errorf("spend = %+v", spend)
		}
	})
	tw(t, "client", "invalid questions are rejected before network submission without echoing state", func(t *testing.T) {
		isolate(t)
		var calls atomic.Int32
		client := testClient(t, Options{}, doerFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New(secret)
		}))
		invalid := []any{
			`{"state":"` + secret + `","questions":{}}`,
			`{"state":"` + secret + `","questions":{"q":{"type":"score","criteria":["one"]}}}`,
			`{"state":"` + secret + `","questions":{"q":{"type":"choice","criteria":{}}}}`,
			`{"state":"` + secret + `","questions":{"q":{"type":"unsupported"}}}`,
			`{"state":"` + secret + `","questions":{"q":{"type":"noul","instructions":"yes"}},"apiKey":"` + secret + `"}`,
		}
		var tooMany []string
		for i := 0; i < 33; i++ {
			tooMany = append(tooMany, fmt.Sprintf(`"q%d":{"type":"noul","instructions":"yes"}`, i))
		}
		invalid = append(invalid, `{"state":"`+secret+`","questions":{`+strings.Join(tooMany, ",")+`}}`)
		for _, text := range invalid {
			_, err := client.EvaluateRaw(context.Background(), json.RawMessage(text.(string)))
			if !hasCode(err, CodeValidation) || strings.Contains(err.Error(), secret) {
				t.Errorf("%.60s: %v", text, err)
			}
		}
		if calls.Load() != 0 || client.GetUsage().RequestsStarted != 0 {
			t.Fatalf("calls=%d usage=%+v", calls.Load(), client.GetUsage())
		}
	})
	tw(t, "client", "UTF-8 byte limit applies before submission", func(t *testing.T) {
		isolate(t)
		client := testClient(t, Options{MaxInputBytes: 180}, doerFunc(func(*http.Request) (*http.Response, error) { t.Error("must not run"); return nil, nil }))
		req := sampleRequest()
		req.State = typesafe.Text(strings.Repeat("🙂", 50))
		if _, err := client.Evaluate(context.Background(), req); !hasCode(err, CodeValidation) {
			t.Fatalf("err = %v", err)
		}
		if client.GetUsage().RequestsStarted != 0 {
			t.Fatal("a refused request must not count")
		}
	})
	tw(t, "client", "request budget is shared across concurrent calls and failures", func(t *testing.T) {
		isolate(t)
		var calls atomic.Int32
		client := testClient(t, Options{MaxRequests: 2, Ledger: OpenUsageLedger(LedgerOptions{Path: t.TempDir() + "/u.json"})}, failing(500, nil, &calls))
		var wg sync.WaitGroup
		errs := make([]error, 3)
		for i := range errs {
			wg.Add(1)
			go func() { defer wg.Done(); _, errs[i] = client.Evaluate(context.Background(), sampleRequest()) }()
		}
		wg.Wait()
		for i, err := range errs {
			if err == nil {
				t.Errorf("call %d succeeded", i)
			}
		}
		if calls.Load() != 2 {
			t.Errorf("calls = %d", calls.Load())
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeBudget) {
			t.Errorf("err = %v", err)
		}
		if u := client.GetUsage(); u.RequestsStarted != 2 || u.RequestsSucceeded != 0 {
			t.Errorf("usage = %+v", u)
		}
	})
	tw(t, "client", "HTTP errors are classified, never retried, and do not expose response secrets", func(t *testing.T) {
		for _, status := range []int{400, 401, 403, 422, 429, 500, 503} {
			isolate(t)
			var calls atomic.Int32
			client := testClient(t, Options{}, failing(status, nil, &calls))
			_, err := client.Evaluate(context.Background(), sampleRequest())
			ie, ok := err.(*IntegrationError)
			if !ok || ie.Code != CodeHTTP || ie.Status != status || strings.Contains(ie.Message, "never-print-me") {
				t.Errorf("%d: %v", status, err)
			}
			if data, _ := json.Marshal(err); strings.Contains(string(data), "never-print-me") {
				t.Errorf("%d: secret in JSON", status)
			}
			if calls.Load() != 1 {
				t.Errorf("%d: %d calls", status, calls.Load())
			}
		}
	})
	tw(t, "client", "HTTP advice names the backend's key, covers 402, and quotes a numeric Retry-After", func(t *testing.T) {
		cases := []struct {
			backend    string
			status     int
			retryAfter bool
			usable     bool
			message    string
		}{
			{"typesafe", 401, true, false, "TypeSafe returned HTTP 401. Check TYPESAFE_API_KEY. No automatic retry was made."},
			{"openrouter", 401, true, false, "TypeSafe returned HTTP 401. Check OPENROUTER_API_KEY. No automatic retry was made."},
			{"typesafe", 402, true, true, "TypeSafe returned HTTP 402. Check your account balance. No automatic retry was made."},
			{"openrouter", 402, true, true, "TypeSafe returned HTTP 402. Insufficient credits. Add credits at https://openrouter.ai/credits. No automatic retry was made."},
			{"typesafe", 429, true, true, "TypeSafe returned HTTP 429. Check your account quota and try again later. Retry after 7 seconds. No automatic retry was made."},
			{"openrouter", 429, true, true, "TypeSafe returned HTTP 429. Check your account quota and try again later. Retry after 7 seconds. No automatic retry was made."},
			{"typesafe", 429, false, true, "TypeSafe returned HTTP 429. Check your account quota and try again later. No automatic retry was made."},
		}
		for _, c := range cases {
			isolate(t)
			// authState({ backend }).usable needs a key present for each backend; the client itself takes its key below.
			t.Setenv("TYPESAFE_API_KEY", "offline-env-key-0123456789abcdef")
			t.Setenv("OPENROUTER_API_KEY", "offline-or-key-0123456789abcdef")
			header := http.Header{}
			if c.retryAfter {
				header.Set("Retry-After", "7")
			}
			client, err := New(Options{APIKey: "test-key", Backend: c.backend, HTTPClient: failing(c.status, header, nil)})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Evaluate(context.Background(), sampleRequest())
			ie, ok := err.(*IntegrationError)
			if !ok || ie.Code != CodeHTTP || ie.Status != c.status || ie.Message != c.message || strings.Contains(ie.Message, "never-print-me") {
				t.Errorf("%s %d: %v", c.backend, c.status, err)
			}
			// A 401 rejects the key; a 402 is billing, so it must leave it usable (rejected statuses stay 401 and 403).
			if s := mustAuth(t, c.backend); s.Usable != c.usable {
				t.Errorf("%s %d: usable = %v", c.backend, c.status, s.Usable)
			}
		}
	})
	tw(t, "client", "safeError keeps its one-argument form and defaults the key advice", func(t *testing.T) {
		own := &IntegrationError{Code: CodeHTTP, Message: "kept", Status: 401}
		if SafeError(own, nil) != own {
			t.Error("an IntegrationError must pass through")
		}
		if got := SafeError(apiError(401), nil).Message; got != "TypeSafe returned HTTP 401. Check TYPESAFE_API_KEY. No automatic retry was made." {
			t.Errorf("message = %s", got)
		}
	})
	tw(t, "client", "cancellation before submission does not consume an attempt", func(t *testing.T) {
		isolate(t)
		client := testClient(t, Options{}, doerFunc(func(*http.Request) (*http.Response, error) { t.Error("must not run"); return nil, nil }))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.Evaluate(ctx, sampleRequest()); !hasCode(err, CodeAborted) {
			t.Fatalf("err = %v", err)
		}
		if client.GetUsage().RequestsStarted != 0 {
			t.Fatal("a cancelled request must not count")
		}
	})
	tw(t, "client", "in-flight cancellation and timeout reach the transport", func(t *testing.T) {
		isolate(t)
		pending := doerFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		client := testClient(t, Options{Timeout: time.Second}, pending)
		done := make(chan error, 1)
		go func() { _, err := client.Evaluate(ctx, sampleRequest()); done <- err }()
		time.Sleep(20 * time.Millisecond)
		cancel()
		if err := <-done; !hasCode(err, CodeAborted) {
			t.Fatalf("cancel: %v", err)
		}
		timed := testClient(t, Options{Timeout: 10 * time.Millisecond}, pending)
		if _, err := timed.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeTimeout) {
			t.Fatalf("timeout: %v", err)
		}
	})
	tw(t, "client", "malformed successful responses fail safely", func(t *testing.T) {
		bodies := []*http.Response{
			jsonResponse(200, map[string]any{"secret": "private"}, nil),
			rawResponse(200, "not-json"),
			jsonResponse(200, map[string]any{"model": "test", "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}, "answers": map[string]any{"yes": map[string]any{"type": "noul", "noul": 5}}}, nil),
		}
		for i, response := range bodies {
			isolate(t)
			client := testClient(t, Options{}, doerFunc(func(*http.Request) (*http.Response, error) { return response, nil }))
			if _, err := client.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeResponse) {
				t.Errorf("case %d: %v", i, err)
			}
			if client.GetUsage().RequestsSucceeded != 0 {
				t.Errorf("case %d counted a success", i)
			}
		}
	})
	tw(t, "client", "client ignores SDK endpoint and logging environment overrides", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_BASE_URL", "https://untrusted.invalid")
		t.Setenv("TYPESAFE_LOG_LEVEL", "debug")
		client := testClient(t, Options{}, doerFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.HasPrefix(r.URL.String(), "https://api.typesafe.ai/") {
				t.Errorf("url = %s", r.URL)
			}
			return responseFor(requestBody(t, r)), nil
		}))
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, "client", "configuration errors are early and usage snapshots are detached", func(t *testing.T) {
		isolate(t)
		if _, err := New(Options{APIKey: " "}); !hasCode(err, CodeConfiguration) {
			t.Errorf("blank key: %v", err)
		}
		// Go zero values mean "default", so the original's timeoutMs: 0, NaN and model: "" have no counterpart; negatives and an overlong model remain.
		for name, opts := range map[string]Options{"timeout": {Timeout: -1}, "maxRequests": {MaxRequests: -1}, "maxInputBytes": {MaxInputBytes: -5}, "model": {Model: strings.Repeat("x", 101)}} {
			opts.APIKey = "test-key"
			if _, err := New(opts); !hasCode(err, CodeConfiguration) {
				t.Errorf("%s: %v", name, err)
			}
		}
		client := testClient(t, Options{}, doerFunc(func(r *http.Request) (*http.Response, error) { return responseFor(requestBody(t, r)), nil }))
		snapshot := client.GetUsage()
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil {
			t.Fatal(err)
		}
		if snapshot.RequestsStarted != 0 || client.GetUsage().RequestsStarted != 1 {
			t.Fatal("snapshots must be detached")
		}
	})
	tw(t, "client", "request data is snapshotted before asynchronous work", func(t *testing.T) {
		isolate(t)
		var sent atomic.Value
		release, entered := make(chan struct{}), make(chan struct{})
		client := testClient(t, Options{}, doerFunc(func(r *http.Request) (*http.Response, error) {
			close(entered)
			<-release
			body := requestBody(t, r)
			sent.Store(string(body))
			return responseFor(body), nil
		}))
		req := sampleRequest()
		done := make(chan error, 1)
		go func() { _, err := client.Evaluate(context.Background(), req); done <- err }()
		<-entered // the request is in flight, so it has been snapshotted
		req.State = typesafe.Text("changed")
		req.Questions[0] = typesafe.Ask("other", typesafe.Noul("changed?"))
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if s, _ := sent.Load().(string); !strings.Contains(s, `"state":"synthetic"`) || strings.Contains(s, "changed") {
			t.Fatalf("sent = %s", s)
		}
	})
	tw(t, "client", "evaluate admits the same near-miss aliases as the agent tool", func(t *testing.T) {
		isolate(t)
		var sent string
		client := testClient(t, Options{}, doerFunc(func(r *http.Request) (*http.Response, error) {
			body := requestBody(t, r)
			sent = string(body)
			return responseFor(body), nil
		}))
		near := map[string]any{"state": "synthetic", "questions": map[string]any{"yes": map[string]any{"type": "noul", "instructions": "Is this synthetic?", "criteria": "Is this synthetic data?"}}}
		result, err := client.EvaluateRaw(context.Background(), near)
		if err != nil {
			t.Fatal(err)
		}
		yes, _ := result.Noul("yes")
		if yes.Noul != 0.9 || !strings.Contains(sent, `"criteria":{"true":"Is this synthetic data?"}`) {
			t.Fatalf("yes=%+v sent=%s", yes, sent)
		}
	})
	tw(t, "client", "unknown backend throws configuration error", func(t *testing.T) {
		_, err := New(Options{APIKey: "test-key", Backend: "bogus"})
		if !hasCode(err, CodeConfiguration) || !strings.Contains(err.Error(), "Unknown judgment backend") || !strings.Contains(err.Error(), "bogus") {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "client", "known backend is called at its full request URL", func(t *testing.T) {
		// A host does not identify the endpoint: openrouter.ai answers the SDK's own /v1/systemone with an HTML page and status 200.
		for backend, want := range map[string]string{"typesafe": "https://api.typesafe.ai/v1/systemone", "openrouter": "https://openrouter.ai/api/alpha/decisions"} {
			isolate(t)
			var got string
			client, err := New(Options{APIKey: "test-key", Backend: backend, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
				got = r.URL.String()
				return responseFor(requestBody(t, r)), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || got != want {
				t.Errorf("%s: %v url=%s", backend, err, got)
			}
		}
	})
	tw(t, "client", "listModels asks each backend for its own model list", func(t *testing.T) {
		cases := []struct {
			backend, url string
			wire         any
			want         string
		}{
			{"typesafe", "https://api.typesafe.ai/v1/models", map[string]any{"models": []any{map[string]any{"name": "jev-latest"}}}, "jev-latest"},
			{"openrouter", "https://openrouter.ai/api/v1/models", map[string]any{"data": []any{map[string]any{"id": "vendor/model", "name": "Vendor: Model"}}}, "vendor/model"},
		}
		for _, c := range cases {
			isolate(t)
			var got string
			client, _ := New(Options{APIKey: "test-key", Backend: c.backend, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
				got = r.URL.String()
				return jsonResponse(200, c.wire, nil), nil
			})})
			names, err := client.ListModels(context.Background())
			if err != nil || len(names) != 1 || names[0] != c.want || got != c.url {
				t.Errorf("%s: %v %v url=%s", c.backend, names, err, got)
			}
		}
	})
	tw(t, "client", "a model list without the backend's declared field keeps the SDK's own shape error", func(t *testing.T) {
		isolate(t)
		client, _ := New(Options{APIKey: "test-key", Backend: "openrouter", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"items": []any{map[string]any{"name": "not the declared field"}}}, nil), nil
		})})
		if _, err := client.ListModels(context.Background()); !hasCode(err, CodeResponse) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "client", "the model list is renamed only for the backend that declares another field", func(t *testing.T) {
		isolate(t)
		client, _ := New(Options{APIKey: "test-key", Backend: "typesafe", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"data": []any{map[string]any{"name": "not the SDK's field"}}}, nil), nil
		})})
		if _, err := client.ListModels(context.Background()); !hasCode(err, CodeResponse) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "client", "a public model list leaves the auth state unverified", func(t *testing.T) {
		isolate(t)
		ClearAuthState()
		client, _ := New(Options{APIKey: "garbage-key", Backend: "openrouter", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"data": []any{map[string]any{"id": "vendor/model", "name": "Vendor: Model"}}}, nil), nil
		})})
		names, err := client.ListModels(context.Background())
		// openrouter.ai serves this list to anyone, so a success says nothing about the key.
		if err != nil || len(names) != 1 || names[0] != "vendor/model" || mustAuth(t, "openrouter").Verified {
			t.Fatalf("names=%v err=%v", names, err)
		}
	})
	tw(t, "client", "the model in the request body is the backend's own id form", func(t *testing.T) {
		cases := []struct{ backend, requested, want string }{
			{"openrouter", "", "typesafe/jev-1.13"}, {"openrouter", "jev-latest", "~typesafe/jev-latest"}, {"openrouter", "jev-1.13", "typesafe/jev-1.13"},
			{"openrouter", "jev-1.13.0", "typesafe/jev-1.13"}, {"openrouter", "vendor/other", "vendor/other"}, {"typesafe", "jev-latest", "jev-latest"},
		}
		for _, c := range cases {
			isolate(t)
			var url, model string
			client, err := New(Options{APIKey: "test-key", Backend: c.backend, Model: c.requested, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
				url = r.URL.String()
				body := requestBody(t, r)
				var sent struct{ Model string }
				_ = json.Unmarshal(body, &sent)
				model = sent.Model
				return responseFor(body), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || model != c.want {
				t.Errorf("%+v: model=%s err=%v", c, model, err)
			}
			// OpenRouter judgments travel to its own decisions path, so the mapped id is proven on the wire that uses it.
			wantURL := map[bool]string{true: "https://openrouter.ai/api/alpha/decisions", false: "https://api.typesafe.ai/v1/systemone"}[c.backend == "openrouter"]
			if url != wantURL {
				t.Errorf("%+v: url=%s", c, url)
			}
		}
	})
	tw(t, "client", "a per-request model gets the same mapping as the client default", func(t *testing.T) {
		cases := []struct{ backend, requested, want string }{
			{"openrouter", "jev-latest", "~typesafe/jev-latest"}, {"openrouter", "jev-1.13", "typesafe/jev-1.13"}, {"openrouter", "vendor/other", "vendor/other"}, {"typesafe", "jev-latest", "jev-latest"},
		}
		for _, c := range cases {
			isolate(t)
			var model string
			client, _ := New(Options{APIKey: "test-key", Backend: c.backend, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
				body := requestBody(t, r)
				var sent struct{ Model string }
				_ = json.Unmarshal(body, &sent)
				model = sent.Model
				return responseFor(body), nil
			})})
			req := sampleRequest()
			req.Model = c.requested
			if _, err := client.Evaluate(context.Background(), req); err != nil || model != c.want {
				t.Errorf("%+v: model=%s err=%v", c, model, err)
			}
		}
	})
	tw(t, "client", "a TypeSafe key is never sent to another backend", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", "ts-test-key-1234567890123456")
		_, err := New(Options{Backend: "openrouter"})
		if !hasCode(err, CodeConfiguration) || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") || strings.Contains(err.Error(), "typesafe login") {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "client", "key resolution picks the right env var per backend", func(t *testing.T) {
		isolate(t)
		// With no key set, the openrouter backend should complain about OPENROUTER_API_KEY.
		if _, err := New(Options{Backend: "openrouter"}); !hasCode(err, CodeConfiguration) || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
			t.Fatalf("err = %v", err)
		}
		// Setting the env var for the right backend gets past key resolution; construction sends nothing.
		t.Setenv("OPENROUTER_API_KEY", "or-test-key-1234567890123456")
		called := false
		if _, err := New(Options{Backend: "openrouter", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })}); err != nil || called {
			t.Fatalf("err = %v called=%v", err, called)
		}
	})
}
