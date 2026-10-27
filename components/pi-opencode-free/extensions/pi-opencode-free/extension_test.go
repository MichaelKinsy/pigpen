// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func testClient(discover func(context.Context) []modelInfo) *client {
	return &client{
		baseURL:  BaseURL,
		discover: discover,
		newID:    func() string { return "call_test" },
	}
}

func TestProviderStartsWithEmptyCatalog(t *testing.T) {
	p := newProvider(testClient(func(context.Context) []modelInfo {
		t.Fatal("must not discover while constructing the provider")
		return nil
	}))
	models, err := p.GetModels()
	if err != nil || len(models) != 0 {
		t.Fatalf("models = %#v, %v", models, err)
	}
	if p.Name != ProviderName || p.BaseURL == nil || *p.BaseURL != BaseURL {
		t.Fatalf("provider = %#v", p)
	}
	if p.Headers["x-opencode-client"] != "cli" || p.Headers["x-opencode-project"] != "global" {
		t.Fatalf("headers = %#v", p.Headers)
	}
}

func TestProviderAuthIsKeylessAndCarriesHeaders(t *testing.T) {
	p := newProvider(testClient(nil))
	result, err := p.Auth.APIKey.Resolve(sdk.APIKeyAuthInput{})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth["apiKey"] != "none" {
		t.Fatalf("auth = %#v", result)
	}
	headers, ok := result.Auth["headers"].(map[string]string)
	if !ok || headers["x-opencode-client"] != "cli" || headers["User-Agent"] != UserAgent {
		t.Fatalf("headers = %#v", result.Auth["headers"])
	}
	check, err := p.Auth.APIKey.Check(sdk.APIKeyAuthInput{})
	if err != nil || check == nil || check.Type != "api_key" {
		t.Fatalf("check = %#v, %v", check, err)
	}
}

func TestCacheOnlyRefreshRestoresSnapshot(t *testing.T) {
	p := newProvider(testClient(func(context.Context) []modelInfo {
		t.Fatal("cache-only refresh must not discover")
		return nil
	}))
	stored := map[string]any{"models": []any{
		map[string]any{
			"id": "hy3-free", "name": "Hy3 (Free)", "api": "openai-completions",
			"provider": ProviderID, "baseUrl": BaseURL,
			"reasoning": true, "input": []any{"text"},
			"contextWindow": float64(256_000), "maxTokens": float64(64_000),
		},
		map[string]any{
			"id": "muse-spark-1.2-contributor-free", "name": "Muse Spark 1.2 Free",
			"api": "openai-responses", "provider": ProviderID, "baseUrl": BaseURL,
		},
	}}
	var updated []map[string]any
	err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: false,
		Stored:       stored,
		Signal:       context.Background(),
		Publish: func(publication sdk.ModelsPublication) (bool, error) {
			if publication.Persist != nil {
				t.Fatalf("cache-only refresh must not persist")
			}
			if publication.Update != nil {
				if err := publication.Update(); err != nil {
					return false, err
				}
			}
			updated, _ = p.GetModels()
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 || updated[0]["id"] != "hy3-free" || updated[1]["api"] != "openai-responses" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, ok := updated[0]["provider"]; ok {
		t.Fatalf("provider must be stripped from the config shape: %#v", updated[0])
	}
}

func TestCacheOnlyRefreshWithoutSnapshotIsEmpty(t *testing.T) {
	p := newProvider(testClient(func(context.Context) []modelInfo {
		t.Fatal("cache-only refresh must not discover")
		return nil
	}))
	called := false
	err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: false,
		Signal:       context.Background(),
		Publish: func(sdk.ModelsPublication) (bool, error) {
			called = true
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("nothing should be published when there is no snapshot")
	}
	models, _ := p.GetModels()
	if len(models) != 0 {
		t.Fatalf("models = %#v", models)
	}
}

func TestNetworkRefreshDiscoversAndPersistsSnapshot(t *testing.T) {
	p := newProvider(testClient(func(context.Context) []modelInfo {
		return []modelInfo{{
			ID: "opencode/hy3-free", Name: "Hy3 (Free)", Reasoning: true,
			ContextWindow: 256_000, MaxTokens: 64_000, Input: []string{"text"},
		}}
	}))
	var persisted map[string]any
	err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: true,
		Signal:       context.Background(),
		Publish: func(publication sdk.ModelsPublication) (bool, error) {
			if err := json.Unmarshal(publication.Persist, &persisted); err != nil {
				t.Fatalf("persist: %v", err)
			}
			if publication.Update != nil {
				if err := publication.Update(); err != nil {
					return false, err
				}
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	models, _ := p.GetModels()
	if len(models) != 1 || models[0]["id"] != "hy3-free" || models[0]["reasoning"] != true {
		t.Fatalf("models = %#v", models)
	}
	if persisted["checkedAt"] == nil {
		t.Fatalf("checkedAt missing: %#v", persisted)
	}
	items, _ := persisted["models"].([]any)
	if len(items) != 1 {
		t.Fatalf("persisted models = %#v", persisted)
	}
	first, _ := items[0].(map[string]any)
	if first["provider"] != ProviderID || first["baseUrl"] != BaseURL || first["api"] != "openai-completions" {
		t.Fatalf("stored model = %#v", first)
	}
}

func TestNetworkRefreshNeverWipesGoodSnapshot(t *testing.T) {
	initial := []modelInfo{{ID: "opencode/old-free", Name: "Old", ContextWindow: 128_000, MaxTokens: 16_384}}
	c := testClient(func(context.Context) []modelInfo { return initial })
	p := newProvider(c)
	if err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: true, Signal: context.Background(),
		Publish: func(publication sdk.ModelsPublication) (bool, error) {
			if publication.Update != nil {
				if err := publication.Update(); err != nil {
					return false, err
				}
			}
			return true, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := p.GetModels()
	c.discover = func(context.Context) []modelInfo { return nil }
	published := false
	err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: true, Signal: context.Background(),
		Publish: func(sdk.ModelsPublication) (bool, error) {
			published = true
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := p.GetModels()
	if published {
		t.Fatal("an empty discovery must not publish")
	}
	if len(after) != len(before) || after[0]["id"] != "old-free" {
		t.Fatalf("catalog was wiped: before %#v after %#v", before, after)
	}
}

func TestNetworkRefreshRespectsAbortBeforeFetching(t *testing.T) {
	called := false
	p := newProvider(testClient(func(context.Context) []modelInfo {
		called = true
		return nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.RefreshModels(sdk.RefreshModelsContext{
		AllowNetwork: true, Signal: ctx,
		Publish: func(sdk.ModelsPublication) (bool, error) {
			t.Fatal("aborted refresh must not publish")
			return false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("aborted refresh must not discover")
	}
}

func TestToProviderModelShape(t *testing.T) {
	level := "high"
	model := toProviderModel(modelInfo{
		ID: "opencode/muse-spark-free", Name: "Muse Spark (Free)", Reasoning: true,
		ContextWindow: 131_072, MaxTokens: 16_384, Input: []string{"text", "image"},
		ThinkingLevelMap: map[string]*string{"high": &level, "off": nil},
		API:              "openai-responses",
	})
	if model["id"] != "muse-spark-free" || model["api"] != "openai-responses" {
		t.Fatalf("model = %#v", model)
	}
	input, _ := model["input"].([]string)
	if len(input) != 2 || input[1] != "image" {
		t.Fatalf("input = %#v", model["input"])
	}
	compat, _ := model["compat"].(map[string]any)
	if compat["maxTokensField"] != "max_tokens" || compat["supportsStore"] != false {
		t.Fatalf("compat = %#v", compat)
	}
	levels, _ := model["thinkingLevelMap"].(map[string]any)
	if levels["high"] != "high" || levels["off"] != nil {
		t.Fatalf("thinking = %#v", levels)
	}
}

func TestRestoreDropsForeignAndBrokenModels(t *testing.T) {
	got := restore(map[string]any{"models": []any{
		map[string]any{"id": "good-free", "provider": ProviderID, "api": "openai-completions"},
		map[string]any{"id": "foreign-free", "provider": "other", "api": "openai-completions"},
		map[string]any{"id": "bad-api-free", "provider": ProviderID, "api": "anthropic-messages"},
		map[string]any{"provider": ProviderID},
	}})
	if len(got) != 1 || got[0]["id"] != "good-free" {
		t.Fatalf("restored = %#v", got)
	}
}
