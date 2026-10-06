// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"strings"
)

// The two self-hosted providers' remaining halves: their interactive setup, Ollama's fetch endpoint and its status
// hints, and the connection-refused message. upstream: providers/ollama.ts (configureOllama, promptForBaseUrl,
// promptForOptionalKey, OllamaProvider.fetch, hintForStatus, connectionRefusedError) and providers/searxng.ts
// (configureSearxng).

// Ollama's four endpoints: the cloud build uses the stable paths, the local build the experimental ones. upstream:
// ollama.ts CLOUD_SEARCH_PATH, LOCAL_SEARCH_PATH, CLOUD_FETCH_PATH, LOCAL_FETCH_PATH.
const (
	ollamaCloudFetchPath = "/api/web_fetch"
	ollamaLocalFetchPath = "/api/experimental/web_fetch"
)

// promptForBaseURL is the shared URL prompt of the two self-hosted providers: keep the current value, type a new one,
// or accept the default. A cancel stops the whole flow. upstream: promptForBaseUrl in ollama.ts and searxng.ts.
func promptForBaseURL(ui providerConfigUI, label, defaultURL, current string, hasCurrent bool) (string, bool) {
	existing := strings.TrimSpace(current)
	placeholder := "Press Enter for default (" + defaultURL + "), or type instance URL"
	if hasCurrent && existing != "" {
		placeholder = "Press Enter to keep current (" + existing + "), or type new URL"
	}
	answer := ui.Input(label, placeholder)
	if answer.isCancellation() {
		return "", false
	}
	if typed := strings.TrimSpace(answer.Value); typed != "" {
		return typed, true
	}
	if hasCurrent && existing != "" {
		return existing, true
	}
	return defaultURL, true
}

// promptForOptionalKey is the shared key prompt: keep the current key, type a new one, or leave it unset. A cancel
// stops the whole flow, which is what keeps a half-finished setup from being persisted. upstream: promptForOptionalKey
// in ollama.ts and searxng.ts.
func promptForOptionalKey(ui providerConfigUI, label, current string, hasCurrent bool) (string, bool, bool) {
	existing := strings.TrimSpace(current)
	placeholder := "Press Enter to leave unset, or type a key"
	if hasCurrent && existing != "" {
		placeholder = "Press Enter to keep current (" + maskApiKey(existing) + "), or type new key"
	}
	answer := ui.Input(label, placeholder)
	if answer.isCancellation() {
		return "", false, false
	}
	if typed := strings.TrimSpace(answer.Value); typed != "" {
		return typed, true, true
	}
	if hasCurrent && existing != "" {
		return existing, true, true
	}
	return "", false, true
}

// configureSearxng prompts for the instance URL and then the optional Bearer key. upstream: providers/searxng.ts
// configureSearxng.
func configureSearxng(ui providerConfigUI, current providerConfigCurrent) (providerConfigChange, bool) {
	baseURL, ok := promptForBaseURL(ui, "SearXNG base URL", searxngDefaultURL, current.BaseURL, current.HasBaseURL)
	if !ok {
		return providerConfigChange{}, false
	}
	key, hasKey, ok := promptForOptionalKey(ui, "SearXNG API key (optional — some instances sit behind a reverse proxy that requires it)", current.APIKey, current.HasAPIKey)
	if !ok {
		return providerConfigChange{}, false
	}
	return providerConfigChange{BaseURL: baseURL, HasBaseURL: true, APIKey: key, HasAPIKey: hasKey}, true
}

// configureOllama prompts for the host URL and then the optional key. upstream: providers/ollama.ts configureOllama.
func configureOllama(ui providerConfigUI, current providerConfigCurrent) (providerConfigChange, bool) {
	baseURL, ok := promptForBaseURL(ui, "Ollama base URL", ollamaDefaultURL, current.BaseURL, current.HasBaseURL)
	if !ok {
		return providerConfigChange{}, false
	}
	key, hasKey, ok := promptForOptionalKey(ui,
		"Ollama API key (optional — for direct cloud access; local Ollama authenticates via `ollama signin`)",
		current.APIKey, current.HasAPIKey)
	if !ok {
		return providerConfigChange{}, false
	}
	return providerConfigChange{BaseURL: baseURL, HasBaseURL: true, APIKey: key, HasAPIKey: hasKey}, true
}

// ollamaHintForStatus is the per-status hint that makes an Ollama failure actionable. upstream: ollama.ts
// hintForStatus.
func ollamaHintForStatus(status int) string {
	switch status {
	case 401:
		return " (run `ollama signin` to authenticate)"
	case 404:
		return " (the Ollama instance may not support web search; ensure you are running a recent version)"
	}
	return ""
}

// searchOllamaCloud picks the endpoint the instance was built for. upstream: the `this.local` branch in OllamaProvider.
func ollamaSearchPath(local bool) string {
	if local {
		return ollamaLocalSearchPath
	}
	return ollamaCloudSearchPath
}

func ollamaFetchPath(local bool) string {
	if local {
		return ollamaLocalFetchPath
	}
	return ollamaCloudFetchPath
}

// connectionRefusedError is the actionable message for a refused connection, so a stopped Ollama does not read as a
// protocol failure. upstream: ollama.ts connectionRefusedError.
func connectionRefusedError(host string) error {
	return fmt.Errorf("Could not connect to Ollama at %s. Make sure Ollama is running (ollama serve).", host)
}

// fetchOllama reads a URL through an Ollama instance's fetch endpoint. upstream: providers/ollama.ts
// OllamaProvider.fetch.
func fetchOllama(client httpDoer, baseURL, apiKey, target string, local bool) (fetchResponse, error) {
	baseURL = stripTrailingSlashes(strings.TrimSpace(baseURL))
	if baseURL == "" {
		return fetchResponse{}, missingKeyError(ollamaHostEnvVar)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if strings.TrimSpace(apiKey) != "" {
		headers["Authorization"] = "Bearer " + strings.TrimSpace(apiKey)
	}
	res, err := client.Do(httpRequest{
		Method:  "POST",
		URL:     baseURL + ollamaFetchPath(local),
		Headers: headers,
		Body:    `{"url":` + mustJSONString(target) + `}`,
	})
	if err != nil {
		return fetchResponse{}, err
	}
	if !isOK(res.Status) {
		return fetchResponse{}, fmt.Errorf("Ollama Fetch API error (%d)%s: %s",
			res.Status, ollamaHintForStatus(res.Status), res.Body)
	}
	raw, err := decodeJSONObject(res.Body)
	if err != nil {
		return fetchResponse{}, err
	}
	content := str(raw, "content")
	if content == "" {
		return fetchResponse{}, fmt.Errorf("Ollama Fetch API error: no content returned for %s", target)
	}
	out := fetchResponse{Text: content, ContentType: "text/plain", HasContentType: true}
	if title := str(raw, "title"); title != "" {
		out.Title, out.HasTitle = title, true
	}
	return out, nil
}

// searchOllamaHintError is the Ollama search wrapper with its status hint, so the same 401 and 404 guidance reaches the
// search path too. upstream: the formatError call in OllamaProvider.search.
func searchOllamaHintError(res httpResponse) error {
	return fmt.Errorf("Ollama Search API error (%d)%s: %s", res.Status, ollamaHintForStatus(res.Status), res.Body)
}
