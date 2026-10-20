package ollamanative

import (
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// optionDisposition says what the provider does with each key the host puts in
// ProviderStreamOptions.Values (providerOptionsWire in PiG, Pi's StreamOptions
// and SimpleStreamOptions).
//
//	mapped:  sent to Ollama, or changes how the request is made.
//	ignored: a hint Pi documents as ignorable by providers without the feature
//	         (session caching, cache retention, transport, token budgets,
//	         request metadata, Bedrock region and profile); Ollama has no
//	         equivalent, so it neither changes the request nor fails it.
//	refused: a value that asks for something Ollama cannot do is an error
//	         (see refuseOptions); the request is not sent.
//
// An unknown key is ignored: Pi's ProviderStreamOptions is open
// (StreamOptions & Record<string, unknown>).
var optionDisposition = map[string]string{
	"apiKey": "mapped", "reasoning": "mapped", "reasoningEffort": "mapped", "effort": "mapped",
	"maxTokens": "mapped", "temperature": "mapped", "thinkingEnabled": "mapped", "samplingParams": "mapped",
	"headers": "mapped", "env": "mapped", "timeoutMs": "mapped", "maxRetries": "mapped", "maxRetryDelayMs": "mapped",
	"toolChoice": "mapped",
	"deferred":   "refused",

	"cacheRetention": "ignored", "sessionId": "ignored", "transport": "ignored", "websocketConnectTimeoutMs": "ignored",
	"thinkingBudgetTokens": "ignored", "interleavedThinking": "ignored", "thinking": "ignored", "thinkingBudgets": "ignored",
	"requestMetadata": "ignored", "metadata": "ignored", "region": "ignored", "profile": "ignored", "hasOnPayload": "ignored",
}

// refuseOptions rejects a request that asks for something Ollama cannot do,
// before anything is sent. There is no original extension to install, so the
// message names only the missing feature.
func refuseOptions(opts sdk.ProviderStreamOptions) error {
	refuse := func(feature string) error {
		return fmt.Errorf("@pi-in-go/pigpen-ollama-native does not support %s yet. The request was not sent.", feature)
	}
	switch d := opts.Values["deferred"].(type) {
	case nil:
	case bool:
		if d {
			return refuse("deferred generation")
		}
	default:
		return refuse("deferred generation")
	}
	switch c := opts.Values["toolChoice"].(type) {
	case nil:
	case string:
		if c != "auto" && c != "none" {
			return refuse(fmt.Sprintf("toolChoice %q", c))
		}
	default:
		return refuse("a specific toolChoice")
	}
	return nil
}

// toolChoice is "auto" (the default) or "none"; refuseOptions has rejected the rest.
func toolChoice(opts sdk.ProviderStreamOptions) string {
	if c, _ := opts.Values["toolChoice"].(string); c == "none" {
		return "none"
	}
	return "auto"
}

// number reads a JSON number from an option map.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// chatOptions maps the sampling options onto /api/chat's "options". Pi's
// samplingParams are merged over the named fields per key, so they win;
// Ollama's own option names (top_p, top_k, min_p, seed, stop, num_ctx,
// repeat_penalty, ...) pass through.
func chatOptions(values map[string]any) map[string]any {
	options := map[string]any{}
	if v, ok := number(values["temperature"]); ok {
		options["temperature"] = v
	}
	if v, ok := number(values["maxTokens"]); ok && v > 0 {
		options["num_predict"] = int(v)
	}
	if sp, ok := values["samplingParams"].(map[string]any); ok {
		for k, v := range sp {
			options[k] = v
		}
	}
	return options
}

// thinkParam is the `think` value for a model that thinks. Ollama takes a bool,
// except gpt-oss, which takes "low", "medium" or "high" and ignores a bool.
// An explicit thinkingEnabled wins over a level; of the levels, reasoning wins
// over reasoningEffort, which wins over effort. ok is false when nothing asked.
func thinkParam(model, values map[string]any) (think any, ok bool) {
	level := ""
	for _, key := range []string{"reasoning", "reasoningEffort", "effort"} {
		if s, _ := values[key].(string); s != "" {
			level = s
			break
		}
	}
	on, explicit := values["thinkingEnabled"].(bool)
	if !explicit {
		if level == "" {
			return nil, false
		}
		on = level != "off" && level != "none"
	}
	if !on {
		return false, true
	}
	if id, _ := model["id"].(string); strings.HasPrefix(id, "gpt-oss") {
		switch level {
		case "minimal", "low":
			return "low", true
		case "high", "xhigh", "max":
			return "high", true
		default:
			return "medium", true
		}
	}
	return true, true
}

// envValue reads a provider-scoped environment value (Pi's options.env), which
// takes precedence over the process environment.
func envValue(values map[string]any, key string) string {
	env, _ := values["env"].(map[string]any)
	s, _ := env[key].(string)
	return strings.TrimSpace(s)
}

// applyHeaders merges the caller's headers over the defaults; a null value
// suppresses a default header of that name (Pi's ProviderHeaders).
func applyHeaders(h http.Header, values map[string]any) {
	headers, _ := values["headers"].(map[string]any)
	for name, v := range headers {
		switch s := v.(type) {
		case nil:
			h.Del(name)
		case string:
			h.Set(name, s)
		}
	}
}
