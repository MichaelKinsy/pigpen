package pigmodel

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/ownmodel"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// fakeRegistry stands in for the SDK's ModelRegistry: what the host would answer.
type fakeRegistry struct {
	mu       sync.Mutex
	models   map[string]map[string]any // "provider/id" -> model
	auth     map[string]any
	authErr  error
	complete func(model, request, options map[string]any) map[string]any

	finds     []string
	authCalls int
	requests  []map[string]any
	options   []map[string]any
	modelsIn  []map[string]any
}

func (r *fakeRegistry) Find(providerID, modelID string) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finds = append(r.finds, providerID+"/"+modelID)
	return r.models[providerID+"/"+modelID]
}

func (r *fakeRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authCalls++
	return r.auth, r.authErr
}

func (r *fakeRegistry) Complete(model, request, options map[string]any) map[string]any {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.options = append(r.options, options)
	r.modelsIn = append(r.modelsIn, model)
	fn := r.complete
	r.mu.Unlock()
	return fn(model, request, options)
}

func message(text string, usageIn, usageOut float64) map[string]any {
	return map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}},
		"usage":      map[string]any{"input": usageIn, "output": usageOut},
		"stopReason": "stop",
	}
}

func newRegistry(reply func(model, request, options map[string]any) map[string]any) *fakeRegistry {
	return &fakeRegistry{
		models:   map[string]map[string]any{"prov/mod": {"id": "mod", "provider": "prov", "api": "some-api"}},
		auth:     map[string]any{"ok": true, "apiKey": "key-1", "headers": map[string]any{"X-A": "b"}},
		complete: reply,
	}
}

func ref() Ref { return Ref{Provider: "prov", ID: "mod"} }

func mustModel(t *testing.T, r Registry) *Model {
	t.Helper()
	m, err := New(r, ref())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func asErr[T any](t *testing.T, err error) T {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("error %T (%v) is not %T", err, err, target)
	}
	return target
}

func conversation() ownmodel.Request {
	return ownmodel.Request{Messages: []ownmodel.Message{
		{Role: ownmodel.RoleSystem, Content: "be exact"},
		{Role: ownmodel.RoleUser, Content: "<document>x</document>"},
	}, Schema: map[string]any{"type": "object"}}
}

func TestComplete_SendsTheConversationThroughTheRegistry(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message(`{"answers":{}}`, 12, 5) })
	m := mustModel(t, r)
	res, err := m.Complete(context.Background(), conversation())
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != `{"answers":{}}` || res.InputTokens == nil || *res.InputTokens != 12 || res.OutputTokens == nil || *res.OutputTokens != 5 {
		t.Fatalf("result %+v", res)
	}
	if m.Name() != "mod" {
		t.Fatalf("name %q", m.Name())
	}
	req := r.requests[0]
	if req["systemPrompt"] != "be exact" {
		t.Fatalf("system prompt %v", req["systemPrompt"])
	}
	msgs := req["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages %v", msgs)
	}
	um := msgs[0].(map[string]any)
	if um["role"] != "user" {
		t.Fatalf("role %v", um["role"])
	}
	content := um["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "<document>x</document>" {
		t.Fatalf("content %v", content)
	}
	if _, ok := um["timestamp"]; !ok {
		t.Fatal("a Pi user message carries a timestamp")
	}
	if r.options[0]["apiKey"] != "key-1" || !reflect.DeepEqual(r.options[0]["headers"], map[string]any{"X-A": "b"}) {
		t.Fatalf("options %v", r.options[0])
	}
	if !reflect.DeepEqual(r.modelsIn[0], r.models["prov/mod"]) {
		t.Fatal("the model handed to Complete must be the registry's")
	}
}

func TestComplete_ResolvesTheModelAndAuthOnFirstUseAndCachesTheModel(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	m := mustModel(t, r)
	if len(r.finds) != 0 || r.authCalls != 0 {
		t.Fatal("New must not touch the registry")
	}
	for i := 0; i < 2; i++ {
		if _, err := m.Complete(context.Background(), conversation()); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(r.finds, []string{"prov/mod"}) {
		t.Fatalf("finds %v", r.finds)
	}
	if r.authCalls != 2 { // credentials are resolved per request: they can refresh
		t.Fatalf("auth calls %d", r.authCalls)
	}
}

func TestComplete_ReportsAModelTheRegistryDoesNotKnow(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	m, err := New(r, Ref{Provider: "prov", ID: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Complete(context.Background(), conversation())
	asErr[*typesafe.TypeSafeError](t, err)
	if !strings.Contains(err.Error(), "prov/missing") {
		t.Fatalf("got %v", err)
	}
}

func TestComplete_AuthFailuresAreReported(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	r.auth = map[string]any{"ok": false, "error": `No API key found for "prov"`}
	_, err := mustModel(t, r).Complete(context.Background(), conversation())
	asErr[*typesafe.TypeSafeError](t, err)
	if !strings.Contains(err.Error(), `No API key found for "prov"`) {
		t.Fatalf("got %v", err)
	}
	r = newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	r.authErr = errors.New("host down")
	_, err = mustModel(t, r).Complete(context.Background(), conversation())
	if err == nil || !strings.Contains(err.Error(), "host down") {
		t.Fatalf("got %v", err)
	}
}

func TestComplete_AssistantTurnsCarryTheModelIdentity(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	req := conversation()
	req.Messages = append(req.Messages,
		ownmodel.Message{Role: ownmodel.RoleAssistant, Content: `{"answers":`},
		ownmodel.Message{Role: ownmodel.RoleUser, Content: "fix it"})
	if _, err := mustModel(t, r).Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	msgs := r.requests[0]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages %v", msgs)
	}
	a := msgs[1].(map[string]any)
	if a["role"] != "assistant" || a["api"] != "some-api" || a["provider"] != "prov" || a["model"] != "mod" || a["stopReason"] != "stop" {
		t.Fatalf("assistant message %v", a)
	}
	if txt := a["content"].([]any)[0].(map[string]any); txt["type"] != "text" || txt["text"] != `{"answers":` {
		t.Fatalf("assistant content %v", txt)
	}
	if _, ok := a["usage"].(map[string]any); !ok {
		t.Fatal("an assistant message carries usage")
	}
}

func TestComplete_MultipleSystemMessagesAreJoined(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	req := ownmodel.Request{Messages: []ownmodel.Message{{Role: ownmodel.RoleSystem, Content: "a"}, {Role: ownmodel.RoleSystem, Content: "b"}, {Role: ownmodel.RoleUser, Content: "u"}}}
	if _, err := mustModel(t, r).Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if r.requests[0]["systemPrompt"] != "a\n\nb" {
		t.Fatalf("system prompt %v", r.requests[0]["systemPrompt"])
	}
}

func TestComplete_TextBlocksAreConcatenatedAndOtherBlocksIgnored(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any {
		return map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{
			map[string]any{"type": "thinking", "thinking": "hmm"},
			map[string]any{"type": "text", "text": `{"a":`},
			map[string]any{"type": "text", "text": `1}`},
		}}
	})
	res, err := mustModel(t, r).Complete(context.Background(), conversation())
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != `{"a":1}` || res.InputTokens != nil || res.OutputTokens != nil {
		t.Fatalf("result %+v (unreported usage must stay unknown)", res)
	}
}

func TestComplete_StopReasonsError(t *testing.T) {
	cases := []struct {
		name   string
		reply  map[string]any
		check  func(t *testing.T, err error)
		retrys bool
	}{
		{"status in the message", map[string]any{"stopReason": "error", "errorMessage": "429 rate limited"}, func(t *testing.T, err error) {
			asErr[*typesafe.RateLimitError](t, err)
		}, true},
		{"server status", map[string]any{"stopReason": "error", "errorMessage": "503 upstream"}, func(t *testing.T, err error) {
			asErr[*typesafe.InternalServerError](t, err)
		}, true},
		{"client status", map[string]any{"stopReason": "error", "errorMessage": "401 bad key"}, func(t *testing.T, err error) {
			asErr[*typesafe.AuthenticationError](t, err)
		}, false},
		{"no status", map[string]any{"stopReason": "error", "errorMessage": "socket hang up"}, func(t *testing.T, err error) {
			asErr[*typesafe.APIConnectionError](t, err)
			if !strings.Contains(err.Error(), "socket hang up") {
				t.Fatalf("got %v", err)
			}
		}, true},
		{"no message", map[string]any{"stopReason": "error"}, func(t *testing.T, err error) { asErr[*typesafe.APIConnectionError](t, err) }, true},
	}
	for _, tc := range cases {
		r := newRegistry(func(model, request, options map[string]any) map[string]any { return tc.reply })
		_, err := mustModel(t, r).Complete(context.Background(), conversation())
		if err == nil {
			t.Fatalf("%s: want an error", tc.name)
		}
		tc.check(t, err)
		policy := typesafe.DefaultRetryPolicy()
		calls := 0
		_, _ = typesafe.Retry(context.Background(), func() typesafe.RetryPolicy { policy.BackoffInitial = 0; return policy }(), typesafe.RetryHooks{}, func(ctx context.Context, attempt int) (int, error) {
			calls++
			_, err := mustModel(t, r).Complete(ctx, conversation())
			return 0, err
		})
		if (calls > 1) != tc.retrys {
			t.Fatalf("%s: retried=%v, want %v", tc.name, calls > 1, tc.retrys)
		}
	}
}

func TestComplete_StopReasonsNotAnswers(t *testing.T) {
	for reason, want := range map[string]string{
		"length":  "output",
		"toolUse": "tool",
		"weird":   `"weird"`,
	} {
		r := newRegistry(func(model, request, options map[string]any) map[string]any {
			return map[string]any{"stopReason": reason, "content": []any{map[string]any{"type": "text", "text": "partial"}}}
		})
		_, err := mustModel(t, r).Complete(context.Background(), conversation())
		te := asErr[*typesafe.TypeSafeError](t, err)
		if !strings.Contains(te.Message, want) {
			t.Fatalf("%s: %q lacks %q", reason, te.Message, want)
		}
		var api *typesafe.APIError
		var conn *typesafe.APIConnectionError
		if errors.As(err, &api) || errors.As(err, &conn) {
			t.Fatalf("%s: must not be retryable", reason)
		}
	}
	r := newRegistry(func(model, request, options map[string]any) map[string]any {
		return map[string]any{"stopReason": "aborted"}
	})
	_, err := mustModel(t, r).Complete(context.Background(), conversation())
	asErr[*typesafe.APIUserAbortError](t, err)
	r = newRegistry(func(model, request, options map[string]any) map[string]any { return nil })
	_, err = mustModel(t, r).Complete(context.Background(), conversation())
	asErr[*typesafe.TypeSafeError](t, err)
}

func TestComplete_AContextThatEndsReturnsAtOnceWithoutWaitingForTheHost(t *testing.T) {
	release := make(chan struct{})
	r := newRegistry(func(model, request, options map[string]any) map[string]any { <-release; return message("x", 1, 1) })
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := mustModel(t, r).Complete(ctx, conversation()); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		asErr[*typesafe.APIUserAbortError](t, err)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the abort must carry the context error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Complete did not return after the context ended")
	}
}

func TestComplete_NativeStructuredOutputIsNotSupported(t *testing.T) {
	r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
	req := conversation()
	req.Structured = true
	_, err := mustModel(t, r).Complete(context.Background(), req)
	asErr[*typesafe.TypeSafeError](t, err)
	if !strings.Contains(err.Error(), "structured") {
		t.Fatalf("got %v", err)
	}
	if len(r.requests) != 0 {
		t.Fatal("no request may be sent")
	}
}

func TestProviderShapesOfTheModelMap(t *testing.T) {
	// The provider may be a string or an object with an id, the model id "id" or "modelId".
	for _, m := range []map[string]any{
		{"id": "mod", "provider": "prov", "api": "x"},
		{"modelId": "mod", "provider": map[string]any{"id": "prov"}, "api": "x"},
	} {
		r := newRegistry(func(model, request, options map[string]any) map[string]any { return message("x", 1, 1) })
		r.models["prov/mod"] = m
		req := conversation()
		req.Messages = append(req.Messages, ownmodel.Message{Role: ownmodel.RoleAssistant, Content: "a"}, ownmodel.Message{Role: ownmodel.RoleUser, Content: "b"})
		if _, err := mustModel(t, r).Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		a := r.requests[0]["messages"].([]any)[1].(map[string]any)
		if a["provider"] != "prov" || a["model"] != "mod" {
			t.Fatalf("model %v -> assistant %v", m, a)
		}
	}
}

func TestNoProviderOrModelNamesAreBuiltIn(t *testing.T) {
	// Provider routing is data: no source string of the own-model packages names a provider
	// or a model family.
	banned := []string{"openai", "anthropic", "gemini", "google", "copilot", "claude", "gpt", "bedrock", "azure", "mistral", "llama", "deepseek"}
	for _, dir := range []string{".", filepath.Join("..", "ownmodel")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(fset, file, src, 0) // comments are not source strings
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, _ := strconv.Unquote(lit.Value)
				for _, b := range banned {
					if strings.Contains(strings.ToLower(s), b) {
						t.Errorf("%s: string %q names %q", fset.Position(lit.Pos()), s, b)
					}
				}
				return true
			})
		}
	}
}
