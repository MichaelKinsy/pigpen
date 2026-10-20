package pi_subagents

import "sync"

// A scripted session for the watchdog twins: the registry and context of the original's createCtx helpers.

type wdFakeRegistry struct {
	mu        sync.Mutex
	models    []map[string]any
	available []map[string]any // what getAvailable lists
	authed    func(model map[string]any) bool
	auth      func(model map[string]any) (wdAuth, error)
	stream    wdStreamFn
}

func (r *wdFakeRegistry) GetAvailable() []map[string]any { return r.available }

func (r *wdFakeRegistry) Find(provider, id string) map[string]any {
	for _, m := range r.models {
		if wdModelProvider(m) == provider && wdModelID(m) == id {
			return m
		}
	}
	return nil
}

func (r *wdFakeRegistry) HasConfiguredAuth(model map[string]any) bool {
	return r.authed == nil || r.authed(model)
}

func (r *wdFakeRegistry) GetAPIKeyAndHeaders(model map[string]any) (wdAuth, error) {
	if r.auth != nil {
		return r.auth(model)
	}
	return wdAuth{OK: true, APIKey: "test-key"}, nil
}

func (r *wdFakeRegistry) StreamSimple(model map[string]any, ctx wdLLMContext, opts wdStreamOptions) map[string]any {
	if r.stream == nil {
		panic("Unexpected model registry stream call")
	}
	return r.stream(model, ctx, opts)
}

type wdFakeCtx struct {
	cwd       string
	model     map[string]any
	signal    <-chan struct{}
	sessionID string
	thinking  string
	reg       *wdFakeRegistry
}

func (c *wdFakeCtx) Cwd() string                  { return c.cwd }
func (c *wdFakeCtx) CurrentModel() map[string]any { return c.model }
func (c *wdFakeCtx) Signal() <-chan struct{}      { return c.signal }
func (c *wdFakeCtx) SessionID() string            { return c.sessionID }
func (c *wdFakeCtx) ThinkingLevel() string        { return c.thinking }
func (c *wdFakeCtx) Registry() wdRegistry         { return c.reg }

// wdModel is a registry model with the fields the review test's model() sets.
func wdModel(provider, id string, extra ...any) map[string]any {
	m := map[string]any{"id": id, "name": id, "api": "faux", "provider": provider, "baseUrl": "https://example.invalid", "reasoning": true,
		"input": []any{"text"}, "cost": map[string]any{"input": float64(0), "output": float64(0), "cacheRead": float64(0), "cacheWrite": float64(0)},
		"contextWindow": float64(100000), "maxTokens": float64(4096)}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// wdAssistant is fauxAssistantMessage: content blocks (a string is one text block), a stop reason and an optional error text.
func wdAssistant(stopReason, errorMessage string, blocks ...any) map[string]any {
	content := []any{}
	for _, b := range blocks {
		if s, ok := b.(string); ok {
			content = append(content, map[string]any{"type": "text", "text": s})
		} else {
			content = append(content, b)
		}
	}
	m := map[string]any{"role": "assistant", "content": content, "stopReason": stopReason}
	if errorMessage != "" {
		m["errorMessage"] = errorMessage
	}
	return m
}

var wdCallSeq int

// wdToolCall is fauxToolCall.
func wdToolCall(name string, args map[string]any) map[string]any {
	wdCallSeq++
	return map[string]any{"type": "toolCall", "id": "tool-" + itoa(wdCallSeq), "name": name, "arguments": args}
}

// wdStreamCall is one request a scripted stream function saw.
type wdStreamCall struct {
	Model   map[string]any
	Context wdLLMContext
	Options wdStreamOptions
}

// wdScriptedStream answers each request with the next scripted message (then a plain "done") and records the calls.
type wdScriptedStream struct {
	mu        sync.Mutex
	responses []map[string]any
	calls     []wdStreamCall
}

func newWdScriptedStream(responses ...map[string]any) *wdScriptedStream {
	return &wdScriptedStream{responses: responses}
}

func (s *wdScriptedStream) fn() wdStreamFn {
	return func(model map[string]any, ctx wdLLMContext, opts wdStreamOptions) map[string]any {
		s.mu.Lock()
		defer s.mu.Unlock()
		messages := append([]map[string]any{}, ctx.Messages...)
		ctx.Messages = messages
		s.calls = append(s.calls, wdStreamCall{Model: model, Context: ctx, Options: opts})
		if len(s.responses) == 0 {
			return wdAssistant("stop", "", "done")
		}
		next := s.responses[0]
		s.responses = s.responses[1:]
		return next
	}
}

func (s *wdScriptedStream) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *wdScriptedStream) call(i int) wdStreamCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

func wdToolNames(tools []wdToolSpec) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}
