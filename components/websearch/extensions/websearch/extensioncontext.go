package websearch

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ExtensionContext is the part of Pi's ExtensionContext that provider side calls read (the
// original passes it as options.extensionContext): the selected model, the model registry that
// resolves credentials, and the session id.
type ExtensionContext interface {
	// Model is the selected model object (ctx.model), or nil when none is selected.
	Model() map[string]any
	ModelRegistry() ModelRegistry
	// SessionID is sessionManager.getSessionId(), or "" when unknown.
	SessionID() string
}

// ModelRegistry is the slice of Pi's ModelRegistry the providers use. sdk.ModelRegistry
// implements it.
type ModelRegistry interface {
	GetAll() ([]map[string]any, error)
	Find(providerID, modelID string) map[string]any
	// GetApiKeyAndHeaders is getApiKeyAndHeaders: a map with ok, apiKey, headers, baseUrl, error.
	GetApiKeyAndHeaders(model map[string]any) (map[string]any, error)
}

type extensionContextKey struct{}

// WithExtensionContext attaches the session's context to a call so providers can reach the model
// registry. A call without one behaves like the original's call without options.extensionContext.
func WithExtensionContext(ctx context.Context, ec ExtensionContext) context.Context {
	if ec == nil {
		return ctx
	}
	return context.WithValue(ctx, extensionContextKey{}, ec)
}

// ExtensionContextFrom returns the context attached by WithExtensionContext, or nil.
func ExtensionContextFrom(ctx context.Context) ExtensionContext {
	ec, _ := ctx.Value(extensionContextKey{}).(ExtensionContext)
	return ec
}

// sdkExtensionContext adapts an SDK context.
type sdkExtensionContext struct{ ctx sdk.Context }

func (s sdkExtensionContext) Model() map[string]any {
	return currentModelOf(s.ctx.Model(), s.ctx.ModelProvider(), s.ctx.ModelRegistry())
}

// currentModelOf is ctx.model: the registry's object for the selected model, or nil when the host
// reports none.
func currentModelOf(id, provider string, reg ModelRegistry) map[string]any {
	if id == "" || provider == "" {
		return nil
	}
	return reg.Find(provider, id)
}

func (s sdkExtensionContext) ModelRegistry() ModelRegistry { return s.ctx.ModelRegistry() }

func (s sdkExtensionContext) SessionID() string {
	id, _ := s.ctx.SessionManager().GetSessionID()
	return id
}
