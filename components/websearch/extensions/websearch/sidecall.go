package websearch

import (
	"context"
	"time"
)

// The model calls the extension makes outside the agent loop read four things of Pi's
// ExtensionContext: ctx.model, ctx.modelRegistry, ctx.scopedModels and ctx.sessionManager.
// SideCallContext is that part of the context. Models are the registry's model objects.

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
	// Complete is the registry's `complete(model, context, options)`: ctx is options.signal.
	Complete(ctx context.Context, model, request map[string]any, options CompleteOptions) (map[string]any, error)
}

// CompleteOptions are the options of one completion.
type CompleteOptions struct {
	MaxTokens        int
	Reasoning        string
	ReasoningEffort  string
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
