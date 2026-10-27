// SPDX-License-Identifier: GPL-3.0-or-later
package piopencodefree

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFilterFreeModelsUsesConservativeDefaults(t *testing.T) {
	out := filterFreeModels([]zenModel{
		{ID: "hy3-free", Name: "Hy3"},
		{ID: "gpt-5.6-sol", Name: "Paid Model"},
		{ID: "big-pickle", Name: "Big Pickle"},
	}, nil)
	if len(out) != 2 || out[0].ID != "opencode/hy3-free" || out[1].ID != "opencode/big-pickle" {
		t.Fatalf("models = %#v", out)
	}
	if out[0].Reasoning || out[0].ContextWindow != 128_000 || out[0].MaxTokens != 16_384 {
		t.Errorf("defaults = %#v", out[0])
	}
	if out[1].Reasoning || out[1].ContextWindow != 128_000 {
		t.Errorf("defaults = %#v", out[1])
	}
}

func TestDiscoverModelsEmptyWhenZenFails(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, http.ErrHandlerTimeout
	})}
	out := discoverModels(discoverOptions{Fetch: client})
	if len(out) != 0 {
		t.Fatalf("models = %#v", out)
	}
}

func TestDiscoverModelsServesEveryFreeModel(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "models.dev") {
			return jsonResponse(200, `{}`), nil
		}
		return jsonResponse(200, `{"data":[{"id":"hy3-free"},{"id":"deepseek-v4-flash-free"},{"id":"gpt-5.6-sol"}]}`), nil
	})}
	out := discoverModels(discoverOptions{Fetch: client})
	if len(out) != 2 || out[0].ID != "opencode/hy3-free" || out[1].ID != "opencode/deepseek-v4-flash-free" {
		t.Fatalf("models = %#v", out)
	}
}

func TestDiscoverModelsEnrichesFromModelsDev(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "models.dev") {
			return jsonResponse(200, `{"opencode":{"models":{"x-preview-f-free":{"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","high","max"]}],"limit":{"context":1000000,"output":131072},"modalities":{"input":["text","image"]},"provider":{"npm":"@ai-sdk/openai"}}}}}`), nil
		}
		return jsonResponse(200, `{"data":[{"id":"x-preview-f-free"}]}`), nil
	})}
	out := discoverModels(discoverOptions{Fetch: client})
	if len(out) != 1 {
		t.Fatalf("models = %#v", out)
	}
	model := out[0]
	if model.ContextWindow != 1_000_000 || model.MaxTokens != 131_072 || !model.Reasoning {
		t.Errorf("metadata = %#v", model)
	}
	if model.API != "openai-responses" {
		t.Errorf("api = %q", model.API)
	}
	if len(model.Input) != 2 || model.Input[1] != "image" {
		t.Errorf("input = %#v", model.Input)
	}
	if model.ThinkingLevelMap["high"] == nil || *model.ThinkingLevelMap["high"] != "high" {
		t.Errorf("thinking = %#v", model.ThinkingLevelMap)
	}
	if model.ThinkingLevelMap["off"] != nil {
		t.Errorf("off should stay hidden: %#v", model.ThinkingLevelMap["off"])
	}
}

func TestDiscoverModelsDefaultsWhenModelsDevUnreachable(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.String(), "models.dev") {
			return nil, http.ErrHandlerTimeout
		}
		return jsonResponse(200, `{"data":[{"id":"hy3-free"}]}`), nil
	})}
	out := discoverModels(discoverOptions{Fetch: client})
	if len(out) != 1 || out[0].ContextWindow != 128_000 {
		t.Fatalf("models = %#v", out)
	}
}

func TestDiscoverModelsReturnsEmptyOnTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	start := time.Now()
	out := discoverModels(discoverOptions{Fetch: client, TimeoutMs: 10})
	if len(out) != 0 {
		t.Fatalf("models = %#v", out)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("discovery did not honor its timeout")
	}
}

func TestHumanizeName(t *testing.T) {
	if got := humanizeName("deepseek-v4_flash-free"); got != "Deepseek V4 Flash (Free)" {
		t.Fatalf("name = %q", got)
	}
}
