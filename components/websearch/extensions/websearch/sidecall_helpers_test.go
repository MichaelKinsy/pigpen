package websearch

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Fakes of the upstream tests' `ctx` object: modelRegistry (find, getAvailable,
// getApiKeyAndHeaders, complete), scopedModels, model and sessionManager.

// completeCall is what the registry's complete received.
type completeCall struct {
	ctx     context.Context
	model   map[string]any
	request map[string]any
	options CompleteOptions
}

type fakeSideRegistry struct {
	mu          sync.Mutex
	models      []map[string]any
	findFn      func(provider, id string) map[string]any
	availableFn func() []map[string]any
	authFn      func(model map[string]any) (map[string]any, error)
	completeFn  func(ctx context.Context, model, request map[string]any, o CompleteOptions) (map[string]any, error)
	calls       []completeCall
}

func (r *fakeSideRegistry) Find(provider, id string) map[string]any {
	if r.findFn != nil {
		return r.findFn(provider, id)
	}
	for _, m := range r.models {
		if modelIs(m, provider, id) {
			return m
		}
	}
	return nil
}

func (r *fakeSideRegistry) GetAvailable() ([]map[string]any, error) {
	if r.availableFn != nil {
		return r.availableFn(), nil
	}
	return r.models, nil
}

func (r *fakeSideRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	if r.authFn != nil {
		return r.authFn(model)
	}
	return map[string]any{"ok": true, "apiKey": "test-key"}, nil
}

func (r *fakeSideRegistry) Complete(ctx context.Context, model, request map[string]any, o CompleteOptions) (map[string]any, error) {
	r.mu.Lock()
	r.calls = append(r.calls, completeCall{ctx, model, request, o})
	r.mu.Unlock()
	if r.completeFn == nil {
		return stopText("unscripted"), nil
	}
	return r.completeFn(ctx, model, request, o)
}

func (r *fakeSideRegistry) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *fakeSideRegistry) lastCall() completeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

// fakeSide is the session context.
type fakeSide struct {
	model   map[string]any
	reg     *fakeSideRegistry
	scoped  []map[string]any
	session string
}

func (s *fakeSide) Model() map[string]any                   { return s.model }
func (s *fakeSide) Registry() SideCallRegistry              { return s.reg }
func (s *fakeSide) ScopedModels() ([]map[string]any, error) { return s.scoped, nil }
func (s *fakeSide) SessionID() string                       { return s.session }

// stopText is a finished completion with one text part.
func stopText(text string) map[string]any {
	return map[string]any{"stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": text}}}
}

// scModel builds a registry model.
func scModel(provider, id string, input ...string) map[string]any {
	if len(input) == 0 {
		input = []string{"text"}
	}
	in := make([]any, len(input))
	for i, kind := range input {
		in[i] = kind
	}
	return map[string]any{"api": "custom-page-api", "provider": provider, "id": id, "input": in, "contextWindow": 10000.0}
}

// requestPrompt is request.messages[0].content[0].text.
func requestPrompt(request map[string]any) string {
	messages, _ := request["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	content, _ := message["content"].([]any)
	part, _ := content[0].(map[string]any)
	text, _ := part["text"].(string)
	return text
}

// await waits for a signal of the fixture (a gated host call was entered) and fails instead of hanging.
func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
