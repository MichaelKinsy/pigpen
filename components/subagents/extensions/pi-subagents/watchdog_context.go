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

func wdModelProvider(model map[string]any) string { p, _ := model["provider"].(string); return p }
func wdModelID(model map[string]any) string       { p, _ := model["id"].(string); return p }
func wdFullModelID(model map[string]any) string {
	return wdModelProvider(model) + "/" + wdModelID(model)
}

// sdkWatchdogContext adapts an SDK context.
type sdkWatchdogContext struct{ ctx sdk.Context }

func newSDKWatchdogContext(ctx sdk.Context) wdContext { return sdkWatchdogContext{ctx} }

func (c sdkWatchdogContext) Cwd() string { return c.ctx.Cwd() }

func (c sdkWatchdogContext) CurrentModel() map[string]any {
	id := c.ctx.Model()
	if id == "" {
		return nil
	}
	if m := c.ctx.ModelRegistry().Find(c.ctx.ModelProvider(), id); m != nil {
		return m
	}
	return map[string]any{"provider": c.ctx.ModelProvider(), "id": id}
}

func (c sdkWatchdogContext) Signal() <-chan struct{} {
	if sig := c.ctx.Signal(); sig != nil {
		return sig.Done()
	}
	return nil
}

func (c sdkWatchdogContext) SessionID() string {
	id, _ := c.ctx.GetSessionID()
	return id
}

func (c sdkWatchdogContext) ThinkingLevel() string {
	level, _ := c.ctx.GetThinkingLevel()
	return level
}

func (c sdkWatchdogContext) Registry() wdRegistry { return sdkWatchdogRegistry{c.ctx} }

type sdkWatchdogRegistry struct{ ctx sdk.Context }

func (r sdkWatchdogRegistry) GetAvailable() []map[string]any {
	models, err := r.ctx.ModelRegistry().GetAvailableOfType(sdk.ModelTypeChat)
	if err != nil {
		return nil
	}
	return models
}

func (r sdkWatchdogRegistry) Find(provider, id string) map[string]any {
	return r.ctx.ModelRegistry().Find(provider, id)
}

func (r sdkWatchdogRegistry) HasConfiguredAuth(model map[string]any) bool {
	ok, _ := r.ctx.ModelRegistry().HasConfiguredAuth(model)
	return ok
}

func (r sdkWatchdogRegistry) GetAPIKeyAndHeaders(model map[string]any) (wdAuth, error) {
	auth, err := r.ctx.ModelRegistry().GetApiKeyAndHeaders(model)
	if err != nil {
		return wdAuth{}, err
	}
	out := wdAuth{OK: true}
	if ok, has := auth["ok"].(bool); has && !ok {
		out.OK = false
		out.Error, _ = auth["error"].(string)
		return out, nil
	}
	out.APIKey, _ = auth["apiKey"].(string)
	out.Headers, _ = auth["headers"].(map[string]any)
	out.Env, _ = auth["env"].(map[string]any)
	return out, nil
}

// StreamSimple asks the host for one completion. The host call itself cannot be cancelled from here, so an abort returns an
// aborted message at once and the call is left to finish on its own goroutine.
func (r sdkWatchdogRegistry) StreamSimple(model map[string]any, llm wdLLMContext, opts wdStreamOptions) map[string]any {
	tools := make([]any, len(llm.Tools))
	for i, t := range llm.Tools {
		tools[i] = map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}
	}
	messages := make([]any, len(llm.Messages))
	for i, m := range llm.Messages {
		messages[i] = m
	}
	request := map[string]any{"systemPrompt": llm.SystemPrompt, "messages": messages}
	if len(tools) > 0 {
		request["tools"] = tools
	}
	options := map[string]any{}
	if opts.APIKey != "" {
		options["apiKey"] = opts.APIKey
	}
	if opts.Headers != nil {
		options["headers"] = opts.Headers
	}
	if opts.Env != nil {
		options["env"] = opts.Env
	}
	if opts.Reasoning != "" {
		options["reasoning"] = opts.Reasoning
	}
	result := make(chan map[string]any, 1)
	go func() { result <- r.ctx.ModelRegistry().StreamSimple(model, request, options).Result() }()
	select {
	case message := <-result:
		return message
	case <-opts.Signal:
		return wdAbortedMessage(model)
	}
}

func wdAbortedMessage(model map[string]any) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{}, "provider": wdModelProvider(model), "model": wdModelID(model),
		"stopReason": "aborted", "errorMessage": "Request was aborted"}
}
