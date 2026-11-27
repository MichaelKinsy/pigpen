package plugins

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

// InBothListsReason matches hermes_cli/plugins_discovery.py:IN_BOTH_LISTS.
const InBothListsReason = "listed in both plugins.enabled and plugins.disabled — disabled wins"

var (
	foreignHarnessDirs = map[string]bool{
		".claude-plugin": true,
		".codex-plugin":  true,
		".cursor-plugin": true,
		".devin-plugin":  true,
		".kimi-plugin":   true,
	}
	bundledSkipDirs = map[string]bool{
		"memory":          true,
		"context_engine":  true,
		"model-providers": true,
		"cron_providers":  true,
		"computer_use":    true,
		"config_backends": true,
	}
)

// PluginConfig holds the parsed plugins: section from config.yaml (and optional plugins.state.json).
type PluginConfig struct {
	Enabled  []string                  `json:"enabled"`
	Disabled []string                  `json:"disabled"`
	Entries  map[string]map[string]any `json:"entries,omitempty"`
}

// GateResult is the routing verdict for one discovered plugin.
type GateResult struct {
	Action  string `json:"action"` // "load", "load_now", "defer", "placeholder"
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// PluginInfo combines a winning Manifest with its gate verdict, effective settings, and runtime diagnostics.
type PluginInfo struct {
	Manifest         Manifest       `json:"manifest"`
	Gate             GateResult     `json:"gate"`
	Settings         map[string]any `json:"settings,omitempty"`
	Loaded           bool           `json:"loaded,omitempty"`
	LoadError        string         `json:"load_error,omitempty"`
	RegisteredTools  []string       `json:"registered_tools,omitempty"`
	RegisteredHooks  []string       `json:"registered_hooks,omitempty"`
	RegisteredCmds   []string       `json:"registered_commands,omitempty"`
	RegisteredPrompt []string       `json:"registered_prompt_sections,omitempty"`
}

// IsSkippedPluginDir reports whether a directory name is never treated as a plugin.
func IsSkippedPluginDir(name string) bool {
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") && len(name) >= 4 {
		return true
	}
	return foreignHarnessDirs[name]
}

// ScanDirectory scans root for Hermes plugin manifests up to depth 2.
func ScanDirectory(root, source, hermesHome string, skipNames map[string]bool) []Manifest {
	return scanDirDepth(root, source, hermesHome, skipNames, "", 0)
}

func scanDirDepth(root, source, hermesHome string, skipNames map[string]bool, prefix string, depth int) []Manifest {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var out []Manifest
	for _, entry := range entries {
		name := entry.Name()
		if IsSkippedPluginDir(name) {
			continue
		}
		childPath := filepath.Join(root, name)
		info, err := os.Stat(childPath)
		if err != nil || !info.IsDir() {
			continue
		}
		if depth == 0 && skipNames != nil && skipNames[name] {
			continue
		}

		yamlPath := ""
		for _, candidate := range []string{"plugin.yaml", "plugin.yml"} {
			p := filepath.Join(childPath, candidate)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				yamlPath = p
				break
			}
		}
		portablePath := filepath.Join(childPath, "plugin.json")
		hasPortable := false
		if st, err := os.Stat(portablePath); err == nil && !st.IsDir() {
			hasPortable = true
		}

		if yamlPath != "" {
			if m, err := ParseManifestFile(yamlPath, childPath, source, prefix); err == nil {
				out = append(out, m)
			}
		} else if hasPortable {
			if m, err := ParsePortableManifest(childPath, source, prefix, hermesHome); err == nil {
				out = append(out, m)
			}
		} else if depth < 1 {
			subPrefix := name
			if prefix != "" {
				subPrefix = prefix + "/" + name
			}
			out = append(out, scanDirDepth(childPath, source, hermesHome, nil, subPrefix, depth+1)...)
		}
	}
	return out
}

// CollectDirectoryManifests collects manifests in Hermes discovery order:
// bundled ($HERMES_BUNDLED_PLUGINS) -> user (<p.Home>/plugins) -> project (<cwd>/.hermes/plugins, opt-in).
func CollectDirectoryManifests(p paths.ProfilePaths, cwd string) []Manifest {
	var all []Manifest

	if bundled := strings.TrimSpace(os.Getenv("HERMES_BUNDLED_PLUGINS")); bundled != "" {
		all = append(all, ScanDirectory(bundled, SourceBundled, p.Home, bundledSkipDirs)...)
	}

	if p.Home != "" {
		userDir := filepath.Join(p.Home, "plugins")
		all = append(all, ScanDirectory(userDir, SourceUser, p.Home, nil)...)
	}

	if parseBoolish(os.Getenv("HERMES_ENABLE_PROJECT_PLUGINS")) {
		base := strings.TrimSpace(cwd)
		if base == "" {
			base, _ = os.Getwd()
		}
		if base != "" {
			projDir := filepath.Join(base, ".hermes", "plugins")
			all = append(all, ScanDirectory(projDir, SourceProject, p.Home, nil)...)
		}
	}

	return all
}

// ResolveManifestWinners applies precedence (project > user > bundled) and the
// Hermes impostor guard: a user/project plugin claiming a bundled key from a
// differently named directory is rejected.
func ResolveManifestWinners(manifests []Manifest) (map[string]Manifest, []string) {
	winners := make(map[string]Manifest, len(manifests))
	var warnings []string
	for _, m := range manifests {
		key := m.CanonicalKey()
		if key == "" {
			continue
		}
		if shadowed, exists := winners[key]; exists && shadowed.Source == SourceBundled && (m.Source == SourceUser || m.Source == SourceProject) {
			ownDir := filepath.Base(m.Dir)
			bundledDir := filepath.Base(shadowed.Dir)
			if ownDir != "" && bundledDir != "" && ownDir != bundledDir {
				warnings = append(warnings, fmt.Sprintf(
					"ignoring %s plugin at %s: manifest key %q matches bundled plugin but directory is %q (expected %q)",
					m.Source, m.Dir, key, ownDir, bundledDir,
				))
				continue
			}
		}
		winners[key] = m
	}
	return winners, warnings
}

// GateManifest evaluates Hermes's enable/disable gate for one winning manifest.
func GateManifest(m Manifest, pcfg PluginConfig) GateResult {
	disabled := make(map[string]bool, len(pcfg.Disabled))
	for _, d := range pcfg.Disabled {
		disabled[strings.TrimSpace(d)] = true
	}
	enabled := make(map[string]bool, len(pcfg.Enabled))
	for _, e := range pcfg.Enabled {
		enabled[strings.TrimSpace(e)] = true
	}

	key := m.CanonicalKey()
	matches := func(set map[string]bool) bool {
		return set[key] || (m.Name != "" && set[m.Name])
	}

	if matches(disabled) {
		if matches(enabled) {
			return GateResult{Action: "placeholder", Enabled: false, Reason: InBothListsReason}
		}
		return GateResult{Action: "placeholder", Enabled: false, Reason: "disabled via config"}
	}
	if m.Kind == KindExclusive {
		return GateResult{Action: "placeholder", Enabled: false, Reason: "exclusive plugin — activate via <category>.provider config"}
	}
	if m.Kind == KindModelProvider {
		return GateResult{Action: "placeholder", Enabled: true, Reason: "model-provider — handled by provider discovery"}
	}
	if m.Source == SourceBundled {
		if m.Kind == KindBackend {
			return GateResult{Action: "load_now", Enabled: true}
		}
		if m.Kind == KindPlatform {
			return GateResult{Action: "defer", Enabled: true}
		}
	}
	if !matches(enabled) {
		return GateResult{
			Action:  "placeholder",
			Enabled: false,
			Reason:  fmt.Sprintf("not enabled in config (run `plugins enable %s` to activate)", key),
		}
	}
	return GateResult{Action: "load", Enabled: true}
}

// LoadPluginConfig reads plugins.enabled, plugins.disabled, and plugins.entries.<id>.settings
// from <p.Home>/config.yaml (and merges <p.Home>/plugins.state.json if present).
func LoadPluginConfig(p paths.ProfilePaths) PluginConfig {
	pcfg := PluginConfig{
		Entries: make(map[string]map[string]any),
	}
	if p.ConfigFile != "" {
		if raw, err := os.ReadFile(p.ConfigFile); err == nil {
			parsePluginsFromConfigYAML(string(raw), &pcfg)
		}
	}
	if p.Home != "" {
		stateFile := filepath.Join(p.Home, "plugins.state.json")
		if raw, err := os.ReadFile(stateFile); err == nil {
			var st PluginConfig
			if json.Unmarshal(raw, &st) == nil {
				for _, e := range st.Enabled {
					pcfg.Enabled = appendUnique(pcfg.Enabled, e)
				}
				for _, d := range st.Disabled {
					pcfg.Disabled = appendUnique(pcfg.Disabled, d)
				}
				for id, settings := range st.Entries {
					if pcfg.Entries[id] == nil {
						pcfg.Entries[id] = make(map[string]any)
					}
					for k, v := range settings {
						pcfg.Entries[id][k] = v
					}
				}
			}
		}
	}
	return pcfg
}

func parsePluginsFromConfigYAML(content string, out *PluginConfig) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	inPlugins := false
	pluginsIndent := 0
	subSection := "" // "enabled", "disabled", "entries"
	subIndent := 0
	entryID := ""
	entryIndent := 0
	inEntrySettings := false
	settingsIndent := 0

	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := countLeadingSpaces(rawLine)

		if indent == 0 {
			if strings.HasPrefix(trimmed, "plugins:") {
				inPlugins = true
				pluginsIndent = 0
				subSection = ""
				entryID = ""
				inEntrySettings = false
				continue
			}
			inPlugins = false
			continue
		}
		if !inPlugins {
			continue
		}

		// List item under plugins.enabled or plugins.disabled.
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if indent > subIndent {
				item := unquoteYAML(stripInlineComment(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))))
				if item != "" {
					switch subSection {
					case "enabled":
						out.Enabled = appendUnique(out.Enabled, item)
					case "disabled":
						out.Disabled = appendUnique(out.Disabled, item)
					}
				}
			}
			continue
		}

		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := stripInlineComment(strings.TrimSpace(trimmed[colon+1:]))

		if indent > pluginsIndent && (subSection == "" || indent <= subIndent) {
			subSection = key
			subIndent = indent
			entryID = ""
			inEntrySettings = false
			if val != "" && (key == "enabled" || key == "disabled") {
				for _, item := range parseFlowList(val) {
					if key == "enabled" {
						out.Enabled = appendUnique(out.Enabled, item)
					} else {
						out.Disabled = appendUnique(out.Disabled, item)
					}
				}
			}
			continue
		}

		if subSection == "entries" {
			if entryID == "" || indent <= entryIndent {
				entryID = unquoteYAML(key)
				entryIndent = indent
				inEntrySettings = false
				if out.Entries[entryID] == nil {
					out.Entries[entryID] = make(map[string]any)
				}
				continue
			}
			if !inEntrySettings || indent <= settingsIndent {
				if key == "settings" || key == "config" {
					inEntrySettings = true
					settingsIndent = indent
				} else {
					inEntrySettings = false
				}
				continue
			}
			if inEntrySettings && indent > settingsIndent && val != "" {
				out.Entries[entryID][key] = parseYAMLScalarValue(val)
			}
		}
	}
}

// SavePluginLists atomically updates plugins.enabled and plugins.disabled in <p.Home>/config.yaml,
// preserving all unrelated configuration lines.
func SavePluginLists(p paths.ProfilePaths, enabled, disabled []string) error {
	if p.ConfigFile == "" {
		return fmt.Errorf("config file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o755); err != nil {
		return err
	}
	var existing string
	if raw, err := os.ReadFile(p.ConfigFile); err == nil {
		existing = string(raw)
	}

	sort.Strings(enabled)
	sort.Strings(disabled)

	updated := rewriteConfigYAMLPluginLists(existing, enabled, disabled)

	tmp, err := os.CreateTemp(filepath.Dir(p.ConfigFile), ".config-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, p.ConfigFile); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Keep plugins.state.json synchronized if it already exists.
	if p.Home != "" {
		stateFile := filepath.Join(p.Home, "plugins.state.json")
		if _, err := os.Stat(stateFile); err == nil {
			pcfg := LoadPluginConfig(p)
			pcfg.Enabled = enabled
			pcfg.Disabled = disabled
			if b, err := json.MarshalIndent(pcfg, "", "  "); err == nil {
				_ = os.WriteFile(stateFile, append(b, '\n'), 0o644)
			}
		}
	}
	return nil
}

func formatPluginListYAML(name string, items []string) []string {
	if len(items) == 0 {
		return []string{fmt.Sprintf("  %s: []", name)}
	}
	lines := []string{fmt.Sprintf("  %s:", name)}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("    - %s", item))
	}
	return lines
}

// rewriteConfigYAMLPluginLists replaces plugins.enabled and plugins.disabled inside config.yaml
// while preserving other top-level keys and other keys under plugins: (e.g. entries:).
func rewriteConfigYAMLPluginLists(existing string, enabled, disabled []string) string {
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	// Remove trailing empty element produced by final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	pluginsIdx := -1
	pluginsEnd := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := countLeadingSpaces(line)
		if indent == 0 {
			if pluginsIdx >= 0 {
				pluginsEnd = i
				break
			}
			if trimmed == "plugins:" || strings.HasPrefix(trimmed, "plugins:") {
				pluginsIdx = i
			}
		}
	}

	if pluginsIdx < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "plugins:")
		lines = append(lines, formatPluginListYAML("enabled", enabled)...)
		lines = append(lines, formatPluginListYAML("disabled", disabled)...)
		return strings.Join(lines, "\n") + "\n"
	}

	// Keep any sub-blocks inside plugins: other than enabled: and disabled:.
	var preserved []string
	skipSub := false
	skipIndent := 0
	for i := pluginsIdx + 1; i < pluginsEnd; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !skipSub {
				preserved = append(preserved, line)
			}
			continue
		}
		indent := countLeadingSpaces(line)
		if skipSub && indent > skipIndent {
			continue
		}
		skipSub = false
		if strings.HasPrefix(trimmed, "enabled:") || strings.HasPrefix(trimmed, "disabled:") {
			skipSub = true
			skipIndent = indent
			continue
		}
		preserved = append(preserved, line)
	}

	var out []string
	out = append(out, lines[:pluginsIdx+1]...)
	out = append(out, formatPluginListYAML("enabled", enabled)...)
	out = append(out, formatPluginListYAML("disabled", disabled)...)
	out = append(out, preserved...)
	out = append(out, lines[pluginsEnd:]...)
	return strings.Join(out, "\n") + "\n"
}

// Discover discovers all plugins, resolves precedence and impostor guard, and evaluates gating.
func Discover(p paths.ProfilePaths, cwd string) []PluginInfo {
	manifests := CollectDirectoryManifests(p, cwd)
	winners, _ := ResolveManifestWinners(manifests)
	pcfg := LoadPluginConfig(p)

	keys := make([]string, 0, len(winners))
	for k := range winners {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]PluginInfo, 0, len(keys))
	for _, k := range keys {
		m := winners[k]
		gate := GateManifest(m, pcfg)
		mergedSettings := make(map[string]any)
		for sk, sv := range m.Settings {
			mergedSettings[sk] = sv
		}
		if entrySettings, ok := pcfg.Entries[k]; ok {
			for sk, sv := range entrySettings {
				mergedSettings[sk] = sv
			}
		} else if m.Name != "" {
			if entrySettings, ok := pcfg.Entries[m.Name]; ok {
				for sk, sv := range entrySettings {
					mergedSettings[sk] = sv
				}
			}
		}
		if len(mergedSettings) == 0 {
			mergedSettings = nil
		}
		out = append(out, PluginInfo{
			Manifest: m,
			Gate:     gate,
			Settings: mergedSettings,
		})
	}
	return out
}

// ListPlugins returns all discovered plugins sorted by canonical key.
func ListPlugins(p paths.ProfilePaths, cwd string) ([]PluginInfo, error) {
	return Discover(p, cwd), nil
}

// InspectPlugin finds a plugin by canonical key or manifest name.
func InspectPlugin(p paths.ProfilePaths, cwd, id string) (*PluginInfo, error) {
	target := strings.TrimSpace(id)
	if target == "" {
		return nil, fmt.Errorf("plugin id is required")
	}
	all := Discover(p, cwd)
	for i := range all {
		if all[i].Manifest.CanonicalKey() == target || all[i].Manifest.Name == target {
			cp := all[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("plugin %q not found", target)
}

// EnablePlugin enables a plugin by canonical key (removing it from plugins.disabled if present).
func EnablePlugin(p paths.ProfilePaths, cwd, id string) (*PluginInfo, error) {
	target := strings.TrimSpace(id)
	if target == "" {
		return nil, fmt.Errorf("plugin id is required")
	}
	key := target
	if info, err := InspectPlugin(p, cwd, target); err == nil {
		key = info.Manifest.CanonicalKey()
	}

	pcfg := LoadPluginConfig(p)
	enabled := appendUnique(pcfg.Enabled, key)
	disabled := removeStrings(pcfg.Disabled, key, target)
	if err := SavePluginLists(p, enabled, disabled); err != nil {
		return nil, err
	}
	if info, err := InspectPlugin(p, cwd, key); err == nil {
		return info, nil
	}
	return &PluginInfo{
		Manifest: Manifest{Key: key, Name: key},
		Gate:     GateResult{Action: "load", Enabled: true},
	}, nil
}

// DisablePlugin disables a plugin by canonical key (removing it from plugins.enabled if present).
func DisablePlugin(p paths.ProfilePaths, cwd, id string) (*PluginInfo, error) {
	target := strings.TrimSpace(id)
	if target == "" {
		return nil, fmt.Errorf("plugin id is required")
	}
	key := target
	if info, err := InspectPlugin(p, cwd, target); err == nil {
		key = info.Manifest.CanonicalKey()
	}

	pcfg := LoadPluginConfig(p)
	disabled := appendUnique(pcfg.Disabled, key)
	enabled := removeStrings(pcfg.Enabled, key, target)
	if err := SavePluginLists(p, enabled, disabled); err != nil {
		return nil, err
	}
	if info, err := InspectPlugin(p, cwd, key); err == nil {
		return info, nil
	}
	return &PluginInfo{
		Manifest: Manifest{Key: key, Name: key},
		Gate:     GateResult{Action: "placeholder", Enabled: false, Reason: "disabled via config"},
	}, nil
}

func removeStrings(list []string, targets ...string) []string {
	drop := make(map[string]bool, len(targets))
	for _, t := range targets {
		if t != "" {
			drop[t] = true
		}
	}
	var out []string
	for _, item := range list {
		if !drop[item] {
			out = append(out, item)
		}
	}
	return out
}
