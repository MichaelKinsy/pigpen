package rpiv_web_tools

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The /web-tools command: pick a provider and save its key or URL, or print the resolved config. upstream:
// web-tools.ts:471-683 and the providers' configure flows.

// configUI is what the providers' prompts need of the UI.
type configUI interface {
	// input asks for a line; ok is false when the dialog is cancelled.
	input(label, placeholder string) (value string, ok bool, err error)
}

type providerConfigCurrent struct {
	BaseURL, APIKey       string
	HasBaseURL, HasAPIKey bool
}

// providerConfigChange is what a configure flow returns: a base URL, and an API key (nil Key = leave unset).
type providerConfigChange struct {
	BaseURL string
	APIKey  *string
}

func maskKey(key string) string {
	u := []rune(key)
	return string(u[:min(4, len(u))]) + "..." + string(u[max(0, len(u)-4):])
}

// promptForBaseURL: cancelled → ok false; otherwise the typed URL, the existing one, or the default.
func promptForBaseURL(ui configUI, label, def, current string) (string, bool, error) {
	existing := jsTrim(current)
	placeholder := fmt.Sprintf("Press Enter for default (%s), or type instance URL", def)
	if existing != "" {
		placeholder = fmt.Sprintf("Press Enter to keep current (%s), or type new URL", existing)
	}
	v, ok, err := ui.input(label, placeholder)
	if err != nil || !ok {
		return "", false, err
	}
	switch {
	case jsTrim(v) != "":
		return jsTrim(v), true, nil
	case existing != "":
		return existing, true, nil
	}
	return def, true, nil
}

// promptForOptionalKey: cancelled → ok false; otherwise the typed key, the existing one, or none.
func promptForOptionalKey(ui configUI, label, current string) (key *string, ok bool, err error) {
	existing := jsTrim(current)
	placeholder := "Press Enter to leave unset, or type a key"
	if existing != "" {
		placeholder = fmt.Sprintf("Press Enter to keep current (%s), or type new key", maskKey(existing))
	}
	v, got, err := ui.input(label, placeholder)
	if err != nil || !got {
		return nil, false, err
	}
	switch {
	case jsTrim(v) != "":
		k := jsTrim(v)
		return &k, true, nil
	case existing != "":
		return &existing, true, nil
	}
	return nil, true, nil
}

func configureSearxng(ui configUI, current providerConfigCurrent) (*providerConfigChange, error) {
	baseURL, ok, err := promptForBaseURL(ui, "SearXNG base URL", searxngDefault, current.BaseURL)
	if err != nil || !ok {
		return nil, err
	}
	key, ok, err := promptForOptionalKey(ui, "SearXNG API key (optional — for instances behind a Bearer-auth proxy)", current.APIKey)
	if err != nil || !ok {
		return nil, err
	}
	return &providerConfigChange{BaseURL: baseURL, APIKey: key}, nil
}

func configureOllama(ui configUI, current providerConfigCurrent) (*providerConfigChange, error) {
	baseURL, ok, err := promptForBaseURL(ui, "Ollama base URL", ollamaDefault, current.BaseURL)
	if err != nil || !ok {
		return nil, err
	}
	key, ok, err := promptForOptionalKey(ui, "Ollama API key (optional — for direct cloud access; local Ollama authenticates via `ollama signin`)", current.APIKey)
	if err != nil || !ok {
		return nil, err
	}
	return &providerConfigChange{BaseURL: baseURL, APIKey: key}, nil
}

type ctxConfigUI struct{ ctx sdk.Context }

func (u ctxConfigUI) input(label, placeholder string) (string, bool, error) {
	return u.ctx.Input(label, placeholder)
}

// formatShowConfigMessage is the text of `/web-tools --show`, with every key masked. upstream: web-tools.ts:471-510.
func formatShowConfigMessage(cfg webToolsConfig) string {
	lines := []string{"Web search config:", "  config file: " + configPath()}
	name, source := activeProvider(cfg)
	lines = append(lines, fmt.Sprintf("  active provider: %s (source: %s)", name, source))
	for _, meta := range providers {
		envKey := ""
		if meta.EnvVar != "" {
			envKey = envTrim(meta.EnvVar)
		}
		configKey, hasConfig := cfg.apiKey(meta.Name)
		configKey = jsTrim(configKey)
		legacyKey := ""
		if meta.Name == legacyKeyProvider {
			legacyKey, _ = cfg.str("apiKey")
			legacyKey = jsTrim(legacyKey)
		}
		// `envKey ?? configKey ?? legacyKey`: the first defined value wins, even when it is empty.
		resolved, resolvedSet := "", false
		switch {
		case meta.EnvVar != "" && os.Getenv(meta.EnvVar) != "" || false:
			resolved, resolvedSet = envKey, true
		case hasConfig:
			resolved, resolvedSet = configKey, true
		case meta.Name == legacyKeyProvider && hasString(cfg, "apiKey"):
			resolved, resolvedSet = legacyKey, true
		}
		cfgShown, cfgSet := configKey, hasConfig
		if !cfgSet && meta.Name == legacyKeyProvider && hasString(cfg, "apiKey") {
			cfgShown, cfgSet = legacyKey, true
		}
		lines = append(lines, fmt.Sprintf("  %s: %s (env: %s, config: %s)", meta.Name,
			maskAPIKey(resolved, resolvedSet && resolved != ""), maskAPIKey(envKey, envKey != ""), maskAPIKey(cfgShown, cfgSet && cfgShown != "")))
	}
	for _, meta := range providers {
		if meta.BaseURLEnvVar == "" {
			continue
		}
		envURL := envTrim(meta.BaseURLEnvVar)
		cfgURL, _ := cfg.baseURL(meta.Name)
		cfgURL = jsTrim(cfgURL)
		resolved := envURL
		src := "env"
		switch {
		case envURL != "":
		case cfgURL != "":
			resolved, src = cfgURL, "config"
		default:
			resolved, src = meta.DefaultBaseURL, "default"
		}
		lines = append(lines, fmt.Sprintf("  %s url: %s (source: %s)", meta.Name, resolved, src))
	}
	lines = append(lines, "", "URL interceptors:")
	lines = append(lines, githubInterceptorLines(cfg)...)
	return strings.Join(lines, "\n")
}

func hasString(cfg webToolsConfig, key string) bool { _, ok := cfg[key].(string); return ok }

// githubInterceptorLines reports the interceptor: this port does not implement it (PORT.md, slice 2).
func githubInterceptorLines(cfg webToolsConfig) []string {
	if githubInterceptorEnabled(cfg) {
		return []string{"  github: enabled in config, but this Go port does not implement the interceptor: github.com URLs are fetched like any other page"}
	}
	return []string{
		"  github: disabled",
		`  ↳ enable:  add  "interceptors": { "github": true }   to config.json`,
		`  ↳ disable: set  "interceptors": { "github": false }  to override a consumer-enabled default`,
	}
}

// githubInterceptorEnabled reads the opt-in the way the original does: true, or an object not disabled.
func githubInterceptorEnabled(cfg webToolsConfig) bool {
	im, _ := cfg["interceptors"].(map[string]any)
	switch g := im["github"].(type) {
	case bool:
		return g
	case map[string]any:
		if e, ok := g["enabled"].(bool); ok {
			return e
		}
		return true
	}
	return false
}

// webToolsCommand is /web-tools.
func webToolsCommand(ctx sdk.Context, args string) error {
	if !ctx.HasUI() {
		ctx.Notify("/web-tools requires interactive mode", "error")
		return nil
	}
	current := readConfig()
	if strings.Contains(args, "--show") {
		ctx.Notify(formatShowConfigMessage(current), "info")
		return nil
	}
	active, _ := activeProvider(current)
	var ordered []providerMeta
	for _, p := range providers {
		if p.Name == active {
			ordered = append(ordered, p)
		}
	}
	for _, p := range providers {
		if p.Name != active {
			ordered = append(ordered, p)
		}
	}
	hasKey := func(p providerMeta) bool {
		if p.BaseURLEnvVar != "" {
			u, _ := current.baseURL(p.Name)
			return envTrim(p.BaseURLEnvVar) != "" || jsTrim(u) != ""
		}
		_, ok := resolveProviderAPIKey(p.Name, current)
		return ok
	}
	labelOf := func(p providerMeta) string {
		var markers []string
		if p.Name == active {
			markers = append(markers, "✓")
		}
		if hasKey(p) {
			markers = append(markers, "(configured)")
		}
		if len(markers) > 0 {
			return p.Label + " " + strings.Join(markers, " ")
		}
		return p.Label
	}
	labels := make([]string, len(ordered))
	for i, p := range ordered {
		labels[i] = labelOf(p)
	}
	selected, ok, err := ctx.SelectWithOptions("Search provider", labels, sdk.DialogOptions{})
	if err != nil {
		return err
	}
	unchanged := func() error { ctx.Notify("Web search config unchanged", "info"); return nil }
	if !ok {
		return unchanged()
	}
	var meta *providerMeta
	for i := range providers {
		if selected == providers[i].Label || strings.HasPrefix(selected, providers[i].Label+" ") {
			meta = &providers[i]
			break
		}
	}
	if meta == nil {
		return unchanged()
	}
	selectedProvider := meta.Name
	if meta.configure != nil {
		cur := providerConfigCurrent{}
		cur.BaseURL, cur.HasBaseURL = current.baseURL(selectedProvider)
		cur.APIKey, cur.HasAPIKey = current.apiKey(selectedProvider)
		result, err := meta.configure(ctxConfigUI{ctx}, cur)
		if err != nil {
			return err
		}
		if result == nil {
			return unchanged()
		}
		toSave := cloneConfig(current)
		toSave["provider"] = selectedProvider
		urls := cloneMap(current["baseUrls"])
		urls[selectedProvider] = result.BaseURL
		toSave["baseUrls"] = urls
		if result.APIKey != nil && *result.APIKey != "" {
			keys := cloneMap(current["apiKeys"])
			keys[selectedProvider] = *result.APIKey
			toSave["apiKeys"] = keys
		}
		delete(toSave, "apiKey")
		if !writeConfig(toSave) {
			ctx.Notify(fmt.Sprintf("Failed to save %s config to %s — disk write failed", meta.Label, configPath()), "error")
			return nil
		}
		if result.BaseURL != "" {
			ctx.Notify(fmt.Sprintf("Saved %s config (url: %s) to %s", meta.Label, result.BaseURL, configPath()), "info")
		} else {
			ctx.Notify(fmt.Sprintf("Saved %s config to %s", meta.Label, configPath()), "info")
		}
		return nil
	}
	existing, hasExisting := current.apiKey(selectedProvider)
	if !hasExisting && selectedProvider == legacyKeyProvider {
		existing, hasExisting = current.str("apiKey")
	}
	placeholder := "..."
	if hasExisting && existing != "" {
		placeholder = fmt.Sprintf("Press Enter to keep current (%s), or type new key", maskAPIKey(existing, true))
	}
	input, got, err := ctx.Input(meta.Label+" API key", placeholder)
	if err != nil {
		return err
	}
	if !got {
		return unchanged()
	}
	trimmed := jsTrim(input)
	keyToWrite := trimmed
	if keyToWrite == "" && hasExisting {
		keyToWrite = existing
	}
	if keyToWrite == "" {
		return unchanged()
	}
	toSave := cloneConfig(current)
	toSave["provider"] = selectedProvider
	keys := cloneMap(current["apiKeys"])
	keys[selectedProvider] = keyToWrite
	toSave["apiKeys"] = keys
	delete(toSave, "apiKey")
	if !writeConfig(toSave) {
		ctx.Notify(fmt.Sprintf("Failed to save %s API key to %s — disk write failed", meta.Label, configPath()), "error")
		return nil
	}
	if trimmed != "" {
		ctx.Notify(fmt.Sprintf("Saved %s API key to %s", meta.Label, configPath()), "info")
	} else {
		ctx.Notify(fmt.Sprintf("Active provider set to %s; existing key kept", meta.Label), "info")
	}
	return nil
}

func cloneConfig(c webToolsConfig) webToolsConfig {
	out := webToolsConfig{}
	for k, v := range c {
		out[k] = v
	}
	return out
}

func cloneMap(v any) map[string]any {
	out := map[string]any{}
	if m, ok := v.(map[string]any); ok {
		for k, x := range m {
			out[k] = x
		}
	}
	return out
}
