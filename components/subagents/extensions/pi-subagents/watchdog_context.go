package pi_subagents

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The part of the session the watchdog reads (ExtensionContext in the original), as an interface so the reviewer, the permission
// arbiter and the model selection run against a scripted registry in tests and the SDK's context in the extension.

// wdAuth is the answer of modelRegistry.getApiKeyAndHeaders: OK false carries Error.
type wdAuth struct {
	OK      bool
	APIKey  string
	Headers map[string]any
	Env     map[string]any
	Error   string
}

type wdToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// wdLLMContext is the transcript context of one model request.
type wdLLMContext struct {
	SystemPrompt string
	Messages     []map[string]any
	Tools        []wdToolSpec
}

// wdStreamOptions are the options of a model request. Signal closes when the request is aborted; a stream function returns an
// assistant message with stopReason "aborted" then.
type wdStreamOptions struct {
	APIKey    string
	Headers   map[string]any
	Env       map[string]any
	Reasoning string
	Signal    <-chan struct{}
}

// wdStreamFn is a model request: it returns the terminal assistant message.
type wdStreamFn func(model map[string]any, ctx wdLLMContext, opts wdStreamOptions) map[string]any

type wdRegistry interface {
	GetAvailable() []map[string]any
	Find(provider, id string) map[string]any
	HasConfiguredAuth(model map[string]any) bool
	// GetAPIKeyAndHeaders returns an error when the registry itself failed (the original's rejection).
	GetAPIKeyAndHeaders(model map[string]any) (wdAuth, error)
	StreamSimple(model map[string]any, ctx wdLLMContext, opts wdStreamOptions) map[string]any
}

type wdContext interface {
	Cwd() string
	// CurrentModel is ctx.model: nil when the session has none.
	CurrentModel() map[string]any
	// Signal is ctx.signal: nil never aborts.
	Signal() <-chan struct{}
	SessionID() string
	// ThinkingLevel is ctx.thinkingLevel: "" when unset.
	ThinkingLevel() string
	Registry() wdRegistry
}

func wdModelProvider(model map[string]any) string { panic("not implemented") }
func wdModelID(model map[string]any) string       { panic("not implemented") }
func wdFullModelID(model map[string]any) string   { panic("not implemented") }

// sdkWatchdogContext adapts an SDK context.
type sdkWatchdogContext struct{ ctx sdk.Context }

func newSDKWatchdogContext(ctx sdk.Context) wdContext { panic("not implemented") }

func (c sdkWatchdogContext) Cwd() string { panic("not implemented") }

func (c sdkWatchdogContext) CurrentModel() map[string]any { panic("not implemented") }

func (c sdkWatchdogContext) Signal() <-chan struct{} { panic("not implemented") }

func (c sdkWatchdogContext) SessionID() string { panic("not implemented") }

func (c sdkWatchdogContext) ThinkingLevel() string { panic("not implemented") }

func (c sdkWatchdogContext) Registry() wdRegistry { panic("not implemented") }

type sdkWatchdogRegistry struct{ ctx sdk.Context }

func (r sdkWatchdogRegistry) GetAvailable() []map[string]any { panic("not implemented") }

func (r sdkWatchdogRegistry) Find(provider, id string) map[string]any { panic("not implemented") }

func (r sdkWatchdogRegistry) HasConfiguredAuth(model map[string]any) bool { panic("not implemented") }

func (r sdkWatchdogRegistry) GetAPIKeyAndHeaders(model map[string]any) (wdAuth, error) {
	panic("not implemented")
}

// StreamSimple asks the host for one completion. The host call itself cannot be cancelled from here, so an abort returns an
// aborted message at once and the call is left to finish on its own goroutine.
func (r sdkWatchdogRegistry) StreamSimple(model map[string]any, llm wdLLMContext, opts wdStreamOptions) map[string]any {
	panic("not implemented")
}

func wdAbortedMessage(model map[string]any) map[string]any { panic("not implemented") }
