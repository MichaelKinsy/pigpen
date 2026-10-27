// SPDX-License-Identifier: GPL-3.0-or-later
//
// Discovery of the free OpenCode Zen models. This is a Go port of
// port/oracle/src/discovery.ts at 0f4c16bade33106847ae4abd8d2be2d1b0058e36.
package piopencodefree

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// ZenModelsURL advertises the models OpenCode Zen accepts.
	ZenModelsURL = "https://opencode.ai/zen/v1/models"
	// ModelsDevURL is the models.dev catalog used to enrich what Zen advertises.
	ModelsDevURL = "https://models.dev/api.json"
	// UserAgent is the OpenCode client identity Zen's free tier expects.
	UserAgent = "opencode/0.0.0-dev"
)

var freeModelID = regexp.MustCompile(`(?i)^(opencode/)?.*-free$|^(opencode/)?big-pickle$`)

// thinkingLevels is the whole Pi thinking-level vocabulary, in upstream order.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// modelInfo is one discovered free model, before it becomes a provider config.
type modelInfo struct {
	ID               string
	Name             string
	Reasoning        bool
	ContextWindow    int
	MaxTokens        int
	ThinkingLevelMap map[string]*string
	Input            []string
	// API is empty for the chat/completions path and "openai-responses" when
	// models.dev routes the model through @ai-sdk/openai.
	API string
}

type modelMeta struct {
	Name             string
	Reasoning        bool
	ReasoningOptions []reasoningOption
	Limit            modelLimit
	Input            []string
	ProviderNPM      string
}

type reasoningOption struct {
	Type   string
	Values []string
}

type modelLimit struct {
	Context int
	Output  int
}

type zenModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type zenResponse struct {
	Data []zenModel `json:"data"`
}

type modelsDevCatalog struct {
	OpenCode struct {
		Models map[string]modelsDevModel `json:"models"`
	} `json:"opencode"`
}

type modelsDevModel struct {
	Name             string            `json:"name"`
	Reasoning        bool              `json:"reasoning"`
	ReasoningOptions []reasoningOption `json:"reasoning_options"`
	Limit            struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
	Modalities struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Provider struct {
		NPM string `json:"npm"`
	} `json:"provider"`
}

type discoverOptions struct {
	// Fetch is the HTTP client used for both catalogs. Nil means http.DefaultClient.
	Fetch *http.Client
	// TimeoutMs is the boot budget for both requests. Zero means 3000 ms.
	TimeoutMs int
	// Signal cancels discovery.
	Signal context.Context
}

// discoverModels fetches the models Zen advertises and enriches them from
// models.dev. Any failure returns an empty list, as upstream does: there is no
// offline catalog.
func discoverModels(opts discoverOptions) []modelInfo {
	fetcher := opts.Fetch
	if fetcher == nil {
		fetcher = http.DefaultClient
	}
	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = 3000
	}
	parent := opts.Signal
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	var zen zenResponse
	var zenOK bool
	var catalog map[string]modelMeta
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ZenModelsURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("x-opencode-client", "cli")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := fetcher.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return
		}
		if err := json.NewDecoder(resp.Body).Decode(&zen); err != nil {
			return
		}
		zenOK = true
	}()
	go func() {
		defer wg.Done()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ModelsDevURL, nil)
		if err != nil {
			return
		}
		resp, err := fetcher.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return
		}
		var raw modelsDevCatalog
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return
		}
		if len(raw.OpenCode.Models) == 0 {
			return
		}
		out := make(map[string]modelMeta, len(raw.OpenCode.Models))
		for id, model := range raw.OpenCode.Models {
			meta := modelMeta{
				Name:             model.Name,
				Reasoning:        model.Reasoning,
				ReasoningOptions: model.ReasoningOptions,
				Input:            model.Modalities.Input,
				ProviderNPM:      model.Provider.NPM,
			}
			meta.Limit = modelLimit{Context: model.Limit.Context, Output: model.Limit.Output}
			out[id] = meta
		}
		catalog = out
	}()
	wg.Wait()
	if !zenOK {
		return nil
	}
	return filterFreeModels(zen.Data, catalog)
}

// filterFreeModels keeps the free ids and applies conservative defaults when a
// model has no models.dev entry.
func filterFreeModels(models []zenModel, catalog map[string]modelMeta) []modelInfo {
	out := make([]modelInfo, 0)
	for _, model := range models {
		if !freeModelID.MatchString(model.ID) {
			continue
		}
		base := baseModelID(model.ID)
		bare := strings.TrimPrefix(model.ID, "opencode/")
		meta, ok := catalog[bare]
		if !ok {
			meta, ok = catalog[base]
		}
		name := model.Name
		if name == "" && ok {
			name = meta.Name
		}
		if name == "" {
			name = humanizeName(model.ID)
		}
		info := modelInfo{
			ID:            prefixedModelID(model.ID),
			Name:          name,
			Reasoning:     ok && meta.Reasoning,
			ContextWindow: 128_000,
			MaxTokens:     16_384,
			Input:         meta.Input,
		}
		if ok && meta.Limit.Context > 0 {
			info.ContextWindow = meta.Limit.Context
		}
		if ok && meta.Limit.Output > 0 {
			info.MaxTokens = meta.Limit.Output
		}
		if ok {
			info.ThinkingLevelMap = buildThinkingLevelMap(meta)
			if meta.ProviderNPM == "@ai-sdk/openai" {
				info.API = "openai-responses"
			}
		}
		out = append(out, info)
	}
	return out
}

func baseModelID(id string) string {
	id = strings.TrimPrefix(id, "opencode/")
	return strings.TrimSuffix(id, "-free")
}

func prefixedModelID(id string) string {
	if strings.HasPrefix(id, "opencode/") {
		return id
	}
	return "opencode/" + id
}

func humanizeName(id string) string {
	base := strings.ReplaceAll(strings.TrimSuffix(id, "-free"), "_", " ")
	base = strings.ReplaceAll(base, "-", " ")
	words := strings.Fields(base)
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ") + " (Free)"
}

// buildThinkingLevelMap converts models.dev reasoning_options into Pi's
// thinkingLevelMap. A model without explicit effort values gets no map and
// defers to the provider default.
func buildThinkingLevelMap(meta modelMeta) map[string]*string {
	if !meta.Reasoning {
		return nil
	}
	var effort []string
	hasToggle := false
	for _, option := range meta.ReasoningOptions {
		if option.Type == "effort" {
			effort = option.Values
		}
		if option.Type == "toggle" {
			hasToggle = true
		}
	}
	if len(effort) == 0 {
		return nil
	}
	out := make(map[string]*string, len(thinkingLevels))
	for _, level := range thinkingLevels {
		out[level] = nil
	}
	sawNone := false
	for _, value := range effort {
		if value == "none" {
			off := "off"
			out["off"] = &off
			sawNone = true
			continue
		}
		if _, exists := out[value]; exists {
			mapped := value
			out[value] = &mapped
		}
	}
	if hasToggle && !sawNone {
		off := "off"
		out["off"] = &off
	}
	return out
}
