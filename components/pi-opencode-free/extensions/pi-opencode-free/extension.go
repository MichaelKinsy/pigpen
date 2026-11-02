// SPDX-License-Identifier: GPL-3.0-or-later
//
// PiG model provider for OpenCode Zen's free models. This is a Go port of
// port/oracle/src/index.ts and port/oracle/src/discovery.ts at
// 0f4c16bade33106847ae4abd8d2be2d1b0058e36.
package piopencodefree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// ExtensionID is the extension's identity; it matches this directory.
	ExtensionID = "pi-opencode-free"
	// ProviderID is the provider name: free models are selected as
	// opencode-free/<model>.
	ProviderID = "opencode-free"
	// BaseURL is the OpenCode Zen API base.
	BaseURL = "https://opencode.ai/zen/v1"
	// ProviderName is the display name in model pickers.
	ProviderName = "OpenCode Direct (Free)"
)

// providerHeaders are the OpenCode client headers Zen's free tier expects on
// every request. No Authorization header is sent: free usage is keyless.
func providerHeaders() map[string]string {
	return map[string]string{
		"x-opencode-client":  "cli",
		"x-opencode-project": "global",
		"User-Agent":         UserAgent,
	}
}

// client is the provider's I/O boundary, injectable for tests.
type client struct {
	baseURL  string
	http     *http.Client
	discover func(context.Context) []modelInfo
	newID    func() string
}

func productionClient() *client {
	c := &client{
		baseURL: BaseURL,
		http:    &http.Client{},
		newID:   randomID,
	}
	c.discover = func(ctx context.Context) []modelInfo {
		return discoverModels(discoverOptions{Fetch: c.http, Signal: ctx})
	}
	return c
}

// Extension returns the extension that registers the opencode-free provider.
// It does no I/O: the model catalog starts empty and is populated only when the
// host restores a stored snapshot or refreshes catalogs with the network on.
func Extension() *sdk.Extension {
	ext := sdk.New(ExtensionID)
	if err := ext.RegisterNativeProvider(newProvider(productionClient())); err != nil {
		panic(err) // a static declaration: this is a programming error, caught by the tests
	}
	return ext
}

// newProvider describes the provider. Its catalog starts empty and only changes
// when the host calls RefreshModels.
func newProvider(c *client) *sdk.Provider {
	var mu sync.Mutex
	models := []map[string]any{}
	base := c.baseURL
	headers := providerHeaders()
	p := &sdk.Provider{
		ID:      ProviderID,
		Name:    ProviderName,
		BaseURL: &base,
		Headers: headers,
		Auth: sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{
			Name: "OpenCode Zen (free models need no key)",
			Check: func(sdk.APIKeyAuthInput) (*sdk.AuthCheck, error) {
				source := "opencode-free"
				return &sdk.AuthCheck{Type: "api_key", Source: &source}, nil
			},
			Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
				source := "opencode-free"
				return &sdk.AuthResult{
					Auth: map[string]any{
						"apiKey":  "none",
						"headers": headers,
					},
					Source: &source,
				}, nil
			},
		}},
		GetModels: func() ([]map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			return cloneModels(models), nil
		},
		RefreshModels: func(in sdk.RefreshModelsContext) error {
			apply := func(next []map[string]any) func() error {
				return func() error {
					mu.Lock()
					models = next
					mu.Unlock()
					return nil
				}
			}
			if !in.AllowNetwork {
				// The host's start-up pass restores the snapshot stored by the
				// last successful refresh, so a one-shot run finds its model.
				restored := restore(in.Stored)
				if len(restored) == 0 {
					return nil
				}
				_, err := in.Publish(sdk.ModelsPublication{Update: apply(restored)})
				return err
			}
			if in.Signal != nil && in.Signal.Err() != nil {
				return nil
			}
			found := c.discover(in.Signal)
			if len(found) == 0 {
				// Keep the previous snapshot and retry on the next refresh.
				return nil
			}
			configs := make([]map[string]any, 0, len(found))
			stored := make([]map[string]any, 0, len(found))
			for _, info := range found {
				config := toProviderModel(info)
				configs = append(configs, config)
				stored = append(stored, toStoredModel(config))
			}
			persist, err := json.Marshal(map[string]any{
				"models":    stored,
				"checkedAt": time.Now().UnixMilli(),
			})
			if err != nil {
				return err
			}
			_, err = in.Publish(sdk.ModelsPublication{Persist: persist, Update: apply(configs)})
			return err
		},
		Stream:       c.stream,
		StreamSimple: c.stream,
	}
	return p
}

// toProviderModel maps a discovered model to the provider config shape.
func toProviderModel(m modelInfo) map[string]any {
	id := strings.TrimPrefix(m.ID, "opencode/")
	api := m.API
	if api == "" {
		api = "openai-completions"
	}
	input := []string{"text"}
	for _, modality := range m.Input {
		if modality == "image" {
			input = append(input, "image")
			break
		}
	}
	thinking := map[string]any{}
	for level, value := range m.ThinkingLevelMap {
		if value == nil {
			thinking[level] = nil
		} else {
			thinking[level] = *value
		}
	}
	var thinkingLevelMap any
	if m.ThinkingLevelMap != nil {
		thinkingLevelMap = thinking
	}
	return map[string]any{
		"id":               id,
		"name":             m.Name,
		"api":              api,
		"reasoning":        m.Reasoning,
		"thinkingLevelMap": thinkingLevelMap,
		"input":            input,
		"contextWindow":    m.ContextWindow,
		"maxTokens":        m.MaxTokens,
		"cost": map[string]any{
			"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0,
		},
		"compat": map[string]any{
			"supportsStore":                               false,
			"supportsDeveloperRole":                       false,
			"supportsFinishReason":                        false,
			"maxTokensField":                              "max_tokens",
			"requiresReasoningContentOnAssistantMessages": true,
		},
	}
}

func toStoredModel(config map[string]any) map[string]any {
	stored := make(map[string]any, len(config)+2)
	for key, value := range config {
		stored[key] = value
	}
	stored["provider"] = ProviderID
	stored["baseUrl"] = BaseURL
	return stored
}

// restore reads back the catalog a refresh stored. Entries without an id, from
// another provider, or with an API this provider does not serve are dropped.
func restore(stored map[string]any) []map[string]any {
	items, _ := stored["models"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		model, _ := item.(map[string]any)
		if model == nil {
			continue
		}
		if id, _ := model["id"].(string); id == "" {
			continue
		}
		if provider, _ := model["provider"].(string); provider != ProviderID {
			continue
		}
		api, _ := model["api"].(string)
		if api != "" && api != "openai-completions" && api != "openai-responses" {
			continue
		}
		restored := make(map[string]any, len(model))
		for key, value := range model {
			if key == "provider" || key == "baseUrl" {
				continue
			}
			restored[key] = value
		}
		out = append(out, restored)
	}
	return out
}

func cloneModels(models []map[string]any) []map[string]any {
	out := make([]map[string]any, len(models))
	for i, model := range models {
		out[i] = cloneMap(model)
	}
	return out
}

func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = cloneValue(item)
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return value
	}
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}
