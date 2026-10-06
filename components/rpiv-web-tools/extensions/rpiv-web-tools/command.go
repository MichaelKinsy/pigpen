// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strconv"
	"strings"
)

// The /web-tools command's logic: the provider picker, the two prompt flows and the --show report. It is written
// host-free on purpose, so every branch the upstream twins drive through a mocked pi context is a pure function here
// and the extension's registration stays a thin adapter. upstream: web-tools.ts registerWebSearchConfigCommand,
// formatShowConfigMessage and the hasKey/labelOf closures.

// selectOutcome is what the picker returned. upstream: ctx.ui.select's string | undefined | null.
type selectOutcome struct {
	Label     string
	Cancelled bool
}

// inputOutcome is what the prompt returned. upstream: ctx.ui.input's string | undefined | null.
type inputOutcome struct {
	Value     string
	Cancelled bool
}

// activeProviderMetas lists the providers with the active one first, keeping the declaration order inside each group.
// That is the picker's order, so the active provider is always row 0. upstream: the orderedMetas in
// registerWebSearchConfigCommand.
func activeProviderMetas(activeName string) []providerMeta {
	out := make([]providerMeta, 0, len(providers))
	for _, meta := range providers {
		if meta.Name == activeName {
			out = append(out, meta)
		}
	}
	for _, meta := range providers {
		if meta.Name != activeName {
			out = append(out, meta)
		}
	}
	return out
}

// providerHasCredentials reports whether a provider counts as configured for the picker's marker. A self-hosted
// provider is configured once it has a base URL from env or config; the bare default does not count, because it is only
// a hint that the setting was never touched. upstream: the hasKey closure.
func providerHasCredentials(meta providerMeta, cfg config, env func(string) string) bool {
	if meta.BaseURLEnvVar != "" {
		return strings.TrimSpace(env(meta.BaseURLEnvVar)) != "" || strings.TrimSpace(cfg.BaseURLs[meta.Name]) != ""
	}
	return resolveProviderAPIKey(meta.Name, cfg, env) != ""
}

// providerPickerLabel is one picker row: the label plus its markers, the active provider first. upstream: the labelOf
// closure.
func providerPickerLabel(meta providerMeta, cfg config, env func(string) string, activeName string) string {
	markers := []string{}
	if meta.Name == activeName {
		markers = append(markers, "✓")
	}
	if providerHasCredentials(meta, cfg, env) {
		markers = append(markers, "(configured)")
	}
	if len(markers) == 0 {
		return meta.Label
	}
	return meta.Label + " " + strings.Join(markers, " ")
}

// providerPickerLabels is the row list the picker is given. upstream: orderedMetas.map(labelOf).
func providerPickerLabels(cfg config, env func(string) string) ([]string, string) {
	active := resolveActiveProviderName(cfg, env).Name
	metas := activeProviderMetas(active)
	labels := make([]string, 0, len(metas))
	for _, meta := range metas {
		labels = append(labels, providerPickerLabel(meta, cfg, env, active))
	}
	return labels, active
}

// matchProviderByLabel resolves the picked row back to its provider by matching the original label or any prefix of
// "label …", which is robust to the marker suffix. upstream: the PROVIDERS.find in registerWebSearchConfigCommand.
func matchProviderByLabel(label string) (providerMeta, bool) {
	for _, meta := range providers {
		if label == meta.Label || strings.HasPrefix(label, meta.Label+" ") {
			return meta, true
		}
	}
	return providerMeta{}, false
}

// existingProviderKey is the key a provider would start from: its own entry, or the legacy top-level key for brave.
// upstream: the existingKey computation in registerWebSearchConfigCommand.
func existingProviderKey(cfg config, providerName string) string {
	if key, ok := cfg.APIKeys[providerName]; ok {
		return key
	}
	if providerName == legacyTopLevelKeyProvider {
		return cfg.APIKey
	}
	return ""
}

// saveProviderConfig is the persistence step both prompt flows share: set the active provider, merge the key and/or
// the URL, and delete the legacy top-level key, which is migrated into apiKeys on this write. Every other field,
// including the unknown ones, is carried over untouched. upstream: the toSave construction in
// registerWebSearchConfigCommand.
func saveProviderConfig(current config, providerName string, change providerConfigChange) config {
	next := current
	next.Provider = providerName
	next.APIKey = ""
	if change.HasAPIKey && change.APIKey != "" {
		keys := make(map[string]string, len(current.APIKeys)+1)
		for k, v := range current.APIKeys {
			keys[k] = v
		}
		keys[providerName] = change.APIKey
		next.APIKeys = keys
	}
	if change.HasBaseURL {
		urls := make(map[string]string, len(current.BaseURLs)+1)
		for k, v := range current.BaseURLs {
			urls[k] = v
		}
		urls[providerName] = change.BaseURL
		next.BaseURLs = urls
	}
	return next
}

// keyPromptPlaceholder is the prompt's placeholder: keeping the current key offers a masked preview, a provider with
// no key gets the bare ellipsis. upstream: the ctx.ui.input placeholder in registerWebSearchConfigCommand.
func keyPromptPlaceholder(existingKey string) string {
	if existingKey != "" {
		return "Press Enter to keep current (" + maskApiKey(existingKey) + "), or type new key"
	}
	return "..."
}

// keyPromptOutcome resolves the prompt result to the key to write: a typed key wins, an empty answer keeps the existing
// key, and a cancel changes nothing. It reports whether anything should be saved. upstream: the trimmed/keyToWrite
// computation in registerWebSearchConfigCommand.
func keyPromptOutcome(answer inputOutcome, existingKey string) (string, bool) {
	if answer.Cancelled {
		return "", false
	}
	trimmed := strings.TrimSpace(answer.Value)
	keyToWrite := trimmed
	if keyToWrite == "" {
		keyToWrite = existingKey
	}
	if keyToWrite == "" {
		return "", false
	}
	return keyToWrite, true
}

// keySavedMessage is the confirmation after a key write: a typed key reads as saved, an empty answer as a provider
// switch that kept the key. upstream: the final ctx.ui.notify in registerWebSearchConfigCommand.
func keySavedMessage(label, configPath string, typedKey bool) string {
	if typedKey {
		return "Saved " + label + " API key to " + configPath
	}
	return "Active provider set to " + label + "; existing key kept"
}

// keySaveFailedMessage is the failure text, which names the config file rather than the vendor, because the actual
// cause is the disk write. upstream: the failed ctx.ui.notify in registerWebSearchConfigCommand.
func keySaveFailedMessage(label, configPath string) string {
	return "Failed to save " + label + " API key to " + configPath + " — disk write failed"
}

// showConfigLines is the --show report: the config path, the active provider and its source, one masked line per
// provider naming the env and config sources separately, one URL line per self-hosted provider, and the interceptor
// state. upstream: web-tools.ts formatShowConfigMessage.
func showConfigLines(cfg config, env func(string) string, interceptors interceptorsConfig, githubEnabled bool, githubToken string) []string {
	configPath := ConfigPath()
	lines := []string{"Web search config:", "  config file: " + configPath}

	active := resolveActiveProviderName(cfg, env)
	lines = append(lines, "  active provider: "+active.Name+" (source: "+string(active.Source)+")")

	for _, meta := range providers {
		envKey := ""
		if meta.EnvVar != "" {
			envKey = strings.TrimSpace(env(meta.EnvVar))
		}
		configKey := strings.TrimSpace(cfg.APIKeys[meta.Name])
		legacyKey := ""
		if meta.Name == legacyTopLevelKeyProvider {
			legacyKey = strings.TrimSpace(cfg.APIKey)
		}
		resolved := envKey
		if resolved == "" {
			resolved = configKey
		}
		if resolved == "" {
			resolved = legacyKey
		}
		shownConfigKey := configKey
		if shownConfigKey == "" {
			shownConfigKey = legacyKey
		}
		lines = append(lines, "  "+meta.Name+": "+maskApiKey(resolved)+
			" (env: "+maskApiKey(envKey)+", config: "+maskApiKey(shownConfigKey)+")")
	}

	// One URL line per provider that declares a URL env var, so a second self-hosted provider needs no change here.
	for _, meta := range providers {
		if meta.BaseURLEnvVar == "" {
			continue
		}
		envURL := strings.TrimSpace(env(meta.BaseURLEnvVar))
		configURL := strings.TrimSpace(cfg.BaseURLs[meta.Name])
		resolvedURL, urlSource := envURL, "env"
		if resolvedURL == "" {
			resolvedURL, urlSource = configURL, "config"
		}
		if resolvedURL == "" {
			resolvedURL, urlSource = meta.DefaultBaseURL, "default"
		}
		lines = append(lines, "  "+meta.Name+" url: "+resolvedURL+" (source: "+urlSource+")")
	}

	lines = append(lines, "", "URL interceptors:")
	if githubEnabled {
		opts := ""
		if interceptors.GitHub != nil && interceptors.GitHub.Object != nil && interceptors.GitHub.Object.MaxRepoSizeMB != nil {
			opts += "maxRepoSizeMB: " + numberString(*interceptors.GitHub.Object.MaxRepoSizeMB) + ", "
		}
		if interceptors.GitHub != nil && interceptors.GitHub.Object != nil && interceptors.GitHub.Object.ClonePath != nil {
			opts += "clonePath: " + *interceptors.GitHub.Object.ClonePath
		}
		lines = append(lines, "  github: enabled (GITHUB_TOKEN: "+maskApiKey(strings.TrimSpace(githubToken))+", "+opts+")")
	} else {
		lines = append(lines, "  github: disabled",
			`  ↳ enable:  add  "interceptors": { "github": true }   to config.json`,
			`  ↳ disable: set  "interceptors": { "github": false }  to override a consumer-enabled default`)
	}
	return lines
}

// numberString renders a float the way the host's JSON writer does, so the --show text matches the original's
// interpolation. upstream: the template literal in formatShowConfigMessage.
func numberString(f float64) string {
	if f == float64(int64(f)) {
		return itoa(int(int64(f)))
	}
	return trimTrailingZeros(f)
}

func trimTrailingZeros(f float64) string {
	s := strings.TrimRight(strings.TrimRight(formatFloat(f), "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

// formatFloat is the host's shortest round-trip decimal rendering, the same one Go's strconv uses with -1 precision.
func formatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
