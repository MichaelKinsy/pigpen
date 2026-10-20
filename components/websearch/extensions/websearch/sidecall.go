package websearch

import (
	"context"
	"errors"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The model calls the extension makes outside the agent loop (a page answer, a query rewrite, a
// search summary) read four things of Pi's ExtensionContext: ctx.model, ctx.modelRegistry,
// ctx.scopedModels and ctx.sessionManager (page-query.ts:56-67, query-rewrite.ts:6-27,
// summary-review.ts:33). SideCallContext is that part of the context. Models are the registry's
// model objects, decoded JSON: provider, id, input, contextWindow, baseUrl, reasoning.

// SideCallContext is the session state a model side call reads.
type SideCallContext interface {
	// Model is ctx.model: the selected model, or nil when none is selected.
	Model() map[string]any
	// Registry is ctx.modelRegistry.
	Registry() SideCallRegistry
	// ScopedModels is ctx.scopedModels, each entry's model; empty means every available model is allowed.
	ScopedModels() ([]map[string]any, error)
	// SessionID is sessionManager.getSessionId(), or "" when unknown.
	SessionID() string
}

// SideCallRegistry is the slice of Pi's ModelRegistry a side call uses.
type SideCallRegistry interface {
	Find(provider, id string) map[string]any
	GetAvailable() ([]map[string]any, error)
	// GetApiKeyAndHeaders is getApiKeyAndHeaders: a map with ok, apiKey, headers, error.
	GetApiKeyAndHeaders(model map[string]any) (map[string]any, error)
	// Complete is the registry's `complete(model, context, options)`: ctx is options.signal. A
	// cancelled ctx ends the call with a message whose stopReason is "aborted".
	Complete(ctx context.Context, model, request map[string]any, options CompleteOptions) (map[string]any, error)
}

// CompleteOptions are the options of one completion. The registry path sends MaxTokens, the
// reasoning level and TransformHeaders; the direct path (a CompleteFunc supplied by the caller)
// receives APIKey and Headers instead, as pi-ai's `complete` does.
type CompleteOptions struct {
	MaxTokens       int
	Reasoning       string
	ReasoningEffort string
	// TransformHeaders returns the request headers with the attribution headers added; nil installs none.
	TransformHeaders func(headers map[string]any) map[string]any
	APIKey           string
	Headers          map[string]any
}

// CompleteFunc is a completion call: ctx.modelRegistry.complete or a caller-supplied function.
type CompleteFunc func(ctx context.Context, model, request map[string]any, options CompleteOptions) (map[string]any, error)

type sideCallKey struct{}

// WithSideCallContext attaches the session's context to a call so tools can make model side calls.
func WithSideCallContext(ctx context.Context, sc SideCallContext) context.Context {
	if sc == nil {
		return ctx
	}
	return context.WithValue(ctx, sideCallKey{}, sc)
}

// SideCallContextFrom returns the context attached by WithSideCallContext, or nil.
func SideCallContextFrom(ctx context.Context) SideCallContext {
	sc, _ := ctx.Value(sideCallKey{}).(SideCallContext)
	return sc
}

// userMessage is `{ role: "user", content: [{ type: "text", text }], timestamp: Date.now() }`.
func userMessage(text string) map[string]any {
	return map[string]any{
		"role":      "user",
		"content":   []any{map[string]any{"type": "text", "text": text}},
		"timestamp": time.Now().UnixMilli(),
	}
}

// sdkSideCall adapts an SDK context.
type sdkSideCall struct{ ctx sdk.Context }

func (s sdkSideCall) Model() map[string]any {
	id, provider := s.ctx.Model(), s.ctx.ModelProvider()
	if id == "" || provider == "" {
		return nil
	}
	return s.ctx.ModelRegistry().Find(provider, id)
}

func (s sdkSideCall) Registry() SideCallRegistry { return sdkSideRegistry{s.ctx.ModelRegistry()} }

func (s sdkSideCall) ScopedModels() ([]map[string]any, error) {
	scoped, err := s.ctx.ScopedModels()
	if err != nil {
		return nil, err
	}
	models := make([]map[string]any, 0, len(scoped))
	for _, entry := range scoped {
		models = append(models, entry.Model)
	}
	return models, nil
}

func (s sdkSideCall) SessionID() string {
	id, _ := s.ctx.SessionManager().GetSessionID()
	return id
}

// sdkSideRegistry adapts the SDK's model registry.
type sdkSideRegistry struct{ reg sdk.ModelRegistry }

func (r sdkSideRegistry) Find(provider, id string) map[string]any { return r.reg.Find(provider, id) }

func (r sdkSideRegistry) GetAvailable() ([]map[string]any, error) { return r.reg.GetAvailable() }

func (r sdkSideRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	return r.reg.GetApiKeyAndHeaders(model)
}

// Complete streams the request through the host and waits for the terminal message. SDK gap: the
// SDK's Stream takes no abort signal (pi-ai's `options.signal`; the host serves `cancelModelStream`
// but only provider streams reach it), so a cancelled ctx ends the wait with an aborted message
// while the host's request runs to its end.
func (r sdkSideRegistry) Complete(ctx context.Context, model, request map[string]any, o CompleteOptions) (map[string]any, error) {
	stream := r.reg.Stream(model, request, streamOptions(o))
	done := make(chan map[string]any, 1)
	go func() { done <- stream.Result() }()
	select {
	case message := <-done:
		if message == nil {
			return nil, errors.New("model stream returned no message")
		}
		return message, nil
	case <-ctx.Done():
		message := abortedMessage(model)
		stream.End(message)
		return message, nil
	}
}

func streamOptions(o CompleteOptions) map[string]any {
	options := map[string]any{}
	if o.MaxTokens > 0 {
		options["maxTokens"] = o.MaxTokens
	}
	if o.Reasoning != "" {
		options["reasoning"] = o.Reasoning
	}
	if o.ReasoningEffort != "" {
		options["reasoningEffort"] = o.ReasoningEffort
	}
	if o.APIKey != "" {
		options["apiKey"] = o.APIKey
	}
	if o.Headers != nil {
		options["headers"] = o.Headers
	}
	if transform := o.TransformHeaders; transform != nil {
		options["transformHeaders"] = sdk.TransformHeadersFunc(func(headers map[string]any, _ map[string]any) (map[string]any, error) {
			return transform(headers), nil
		})
	}
	return options
}

// abortedMessage is the message a cancelled provider request ends with.
func abortedMessage(model map[string]any) map[string]any {
	return map[string]any{
		"role":       "assistant",
		"content":    []any{},
		"provider":   model["provider"],
		"model":      model["id"],
		"stopReason": "aborted",
		"timestamp":  time.Now().UnixMilli(),
	}
}
