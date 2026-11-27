// Package plugins implements Hermes Agent's plugin discovery, manifest parsing,
// portable Agent Plugin support, and out-of-process Python plugin bridge for pig-hermes.
package plugins

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Valid plugin kinds matching hermes_cli/plugins_manifest.py.
const (
	KindStandalone    = "standalone"
	KindBackend       = "backend"
	KindExclusive     = "exclusive"
	KindPlatform      = "platform"
	KindModelProvider = "model-provider"
)

// Discovery sources in increasing precedence order (bundled < user < project).
const (
	SourceBundled = "bundled"
	SourceUser    = "user"
	SourceProject = "project"
)

// Manifest represents a parsed Hermes plugin.yaml/plugin.yml or portable plugin.json.
type Manifest struct {
	Key                    string                       `json:"key"`
	Name                   string                       `json:"name"`
	Version                string                       `json:"version,omitempty"`
	Description            string                       `json:"description,omitempty"`
	Author                 string                       `json:"author,omitempty"`
	Kind                   string                       `json:"kind"`
	Entry                  string                       `json:"entry,omitempty"`
	Label                  string                       `json:"label,omitempty"`
	Tools                  []string                     `json:"tools,omitempty"`
	Hooks                  []string                     `json:"hooks,omitempty"`
	Commands               []string                     `json:"commands,omitempty"`
	ProvidesMemoryProvider bool                         `json:"provides_memory_provider,omitempty"`
	Platforms              []string                     `json:"platforms,omitempty"`
	RequiresEnv            []string                     `json:"requires_env,omitempty"`
	RequiresPlugins        []string                     `json:"requires_plugins,omitempty"`
	Settings               map[string]any               `json:"settings,omitempty"`
	License                string                       `json:"license,omitempty"`
	Homepage               string                       `json:"homepage,omitempty"`
	Tags                   []string                     `json:"tags,omitempty"`
	Source                 string                       `json:"source"`
	Dir                    string                       `json:"dir"`
	ManifestFile           string                       `json:"manifest_file"`
	Portable               bool                         `json:"portable,omitempty"`
	MCPServers             map[string]PortableMCPServer `json:"mcp_servers,omitempty"`
	Skills                 []PortableSkill              `json:"skills,omitempty"`
}

// CanonicalKey returns the registry identifier of the manifest.
func (m Manifest) CanonicalKey() string {
	if strings.TrimSpace(m.Key) != "" {
		return strings.TrimSpace(m.Key)
	}
	return strings.TrimSpace(m.Name)
}

// EntryPath resolves the Python entry file path for this plugin, if one exists.
func (m Manifest) EntryPath() string {
	if m.Dir == "" {
		return ""
	}
	entry := strings.TrimSpace(m.Entry)
	if entry == "" {
		entry = "__init__.py"
	}
	candidate := entry
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(m.Dir, entry)
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}
	return candidate
}

// PortableMCPServer represents an MCP server declared in a portable plugin's mcp.json.
type PortableMCPServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Trust   string            `json:"trust,omitempty"`
}

// PortableSkill represents a skill bundled under skills/<name>/SKILL.md in a portable plugin.
type PortableSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

var (
	validSectionCharRe = regexp.MustCompile(`[^a-zA-Z0-9_]+`)
	placeholderRe      = regexp.MustCompile(`\$\{(PLUGIN_ROOT|PLUGIN_DATA)\}`)
)

// SanitizeSectionName converts an arbitrary plugin or section identifier into
// an underscore-only valid PiG system prompt section name prefixed with hermes_plugin_.
func SanitizeSectionName(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = validSectionCharRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "section"
	}
	return "hermes_plugin_" + s
}

// PortableDataNamespace matches hermes_cli/plugins_manifest.py:_portable_data_namespace.
func PortableDataNamespace(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-_")
	if slug == "" {
		slug = "plugin"
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("agent-plugin-%s-%s", slug, hex.EncodeToString(sum[:])[:8])
}

// PluginDataDir returns the durable plugin-data directory under hermesHome for a plugin.
func PluginDataDir(hermesHome, key string, portable bool) string {
	ns := key
	if portable {
		ns = PortableDataNamespace(key)
	} else {
		ns = strings.ReplaceAll(strings.TrimSpace(key), "/", "__")
		if ns == "" {
			ns = "plugin"
		}
	}
	return filepath.Join(hermesHome, "plugin-data", ns)
}

// ParseManifestFile parses a Hermes plugin.yaml or plugin.yml manifest.
func ParseManifestFile(manifestPath, pluginDir, source, prefix string) (Manifest, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Kind:         KindStandalone,
		Source:       source,
		Dir:          pluginDir,
		ManifestFile: manifestPath,
		Settings:     make(map[string]any),
	}
	parsePluginYAML(string(raw), &m)

	dirName := filepath.Base(pluginDir)
	if strings.TrimSpace(m.Name) == "" {
		m.Name = dirName
	}
	if prefix != "" {
		m.Key = prefix + "/" + dirName
	} else {
		m.Key = m.Name
	}

	m.Kind = normalizeKind(m.Kind, pluginDir, m.ProvidesMemoryProvider)
	if m.Kind == KindExclusive && !m.ProvidesMemoryProvider {
		if detectMemoryProviderSource(pluginDir) {
			m.ProvidesMemoryProvider = true
		}
	}
	if len(m.Settings) == 0 {
		m.Settings = nil
	}
	return m, nil
}

// ParsePortableManifest parses a portable Agent Plugin v1 directory (plugin.json + mcp.json + skills/).
func ParsePortableManifest(pluginDir, source, prefix, hermesHome string) (Manifest, error) {
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Manifest{}, fmt.Errorf("invalid plugin.json: %w", err)
	}
	dirName := filepath.Base(pluginDir)
	name := strings.TrimSpace(stringField(doc["name"]))
	if name == "" {
		name = dirName
	}
	key := name
	if prefix != "" {
		key = prefix + "/" + dirName
	}

	author := ""
	switch a := doc["author"].(type) {
	case string:
		author = strings.TrimSpace(a)
	case map[string]any:
		author = strings.TrimSpace(stringField(a["name"]))
	}

	m := Manifest{
		Key:          key,
		Name:         name,
		Version:      strings.TrimSpace(stringField(doc["version"])),
		Description:  strings.TrimSpace(stringField(doc["description"])),
		Author:       author,
		Kind:         KindStandalone,
		License:      strings.TrimSpace(stringField(doc["license"])),
		Homepage:     strings.TrimSpace(stringField(doc["homepage"])),
		Tags:         stringSliceField(doc["keywords"]),
		Source:       source,
		Dir:          pluginDir,
		ManifestFile: manifestPath,
		Portable:     true,
	}

	dataRoot := PluginDataDir(hermesHome, key, true)

	// Extract optional Hermes extension server trust metadata.
	trustMap := extractHermesServerTrust(doc)

	// Parse mcp.json if present.
	mcpPath := filepath.Join(pluginDir, "mcp.json")
	if mcpRaw, err := os.ReadFile(mcpPath); err == nil {
		servers, err := parsePortableMCPJSON(mcpRaw, pluginDir, dataRoot, trustMap)
		if err == nil && len(servers) > 0 {
			m.MCPServers = servers
		}
	}

	// Discover bundled skills under skills/<name>/SKILL.md.
	m.Skills = discoverPortableSkills(pluginDir)

	return m, nil
}

func extractHermesServerTrust(doc map[string]any) map[string]string {
	out := make(map[string]string)
	exts, ok := doc["extensions"].(map[string]any)
	if !ok {
		return out
	}
	hermesExt, ok := exts["com.nousresearch.hermes"].(map[string]any)
	if !ok {
		return out
	}
	servers, ok := hermesExt["servers"].(map[string]any)
	if !ok {
		return out
	}
	for name, raw := range servers {
		if m, ok := raw.(map[string]any); ok {
			if t := strings.TrimSpace(stringField(m["trust"])); t != "" {
				out[name] = t
			}
		}
	}
	return out
}

func expandPluginPlaceholders(s, pluginRoot, dataRoot string) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		if match == "${PLUGIN_ROOT}" {
			return pluginRoot
		}
		if match == "${PLUGIN_DATA}" {
			return dataRoot
		}
		return match
	})
}

func parsePortableMCPJSON(raw []byte, pluginRoot, dataRoot string, trustMap map[string]string) (map[string]PortableMCPServer, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	serversRaw, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		if alt, ok2 := doc["servers"].(map[string]any); ok2 {
			serversRaw = alt
		} else {
			return nil, nil
		}
	}
	out := make(map[string]PortableMCPServer, len(serversRaw))
	for name, val := range serversRaw {
		obj, ok := val.(map[string]any)
		if !ok {
			continue
		}
		srv := PortableMCPServer{
			Type:    strings.TrimSpace(stringField(obj["type"])),
			Command: expandPluginPlaceholders(strings.TrimSpace(stringField(obj["command"])), pluginRoot, dataRoot),
			Cwd:     expandPluginPlaceholders(strings.TrimSpace(stringField(obj["cwd"])), pluginRoot, dataRoot),
			URL:     expandPluginPlaceholders(strings.TrimSpace(stringField(obj["url"])), pluginRoot, dataRoot),
			Trust:   trustMap[name],
		}
		for _, a := range stringSliceField(obj["args"]) {
			srv.Args = append(srv.Args, expandPluginPlaceholders(a, pluginRoot, dataRoot))
		}
		if envMap, ok := obj["env"].(map[string]any); ok && len(envMap) > 0 {
			srv.Env = make(map[string]string, len(envMap))
			for k, v := range envMap {
				srv.Env[k] = expandPluginPlaceholders(stringField(v), pluginRoot, dataRoot)
			}
		}
		if hdrMap, ok := obj["headers"].(map[string]any); ok && len(hdrMap) > 0 {
			srv.Headers = make(map[string]string, len(hdrMap))
			for k, v := range hdrMap {
				srv.Headers[k] = expandPluginPlaceholders(stringField(v), pluginRoot, dataRoot)
			}
		}
		out[name] = srv
	}
	return out, nil
}

func discoverPortableSkills(pluginDir string) []PortableSkill {
	skillsRoot := filepath.Join(pluginDir, "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return nil
	}
	var out []PortableSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillMD := filepath.Join(skillsRoot, entry.Name(), "SKILL.md")
		raw, err := os.ReadFile(skillMD)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(raw), entry.Name())
		out = append(out, PortableSkill{
			Name:        name,
			Description: desc,
			Path:        skillMD,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseSkillFrontmatter(content, fallbackName string) (string, string) {
	content = strings.TrimPrefix(content, "\ufeff")
	name := fallbackName
	desc := ""
	if !strings.HasPrefix(content, "---") {
		return name, desc
	}
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return name, desc
	}
	fm := rest[:idx]
	scanner := bufio.NewScanner(strings.NewReader(fm))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = unquoteYAML(stripInlineComment(strings.TrimSpace(v)))
		switch k {
		case "name":
			if v != "" {
				name = v
			}
		case "description":
			desc = v
		}
	}
	return name, desc
}

func normalizeKind(kind, pluginDir string, explicitMemory bool) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case KindStandalone, KindBackend, KindExclusive, KindPlatform, KindModelProvider:
		return k
	}
	if explicitMemory {
		return KindExclusive
	}
	if detected := detectKindFromSource(pluginDir); detected != "" {
		return detected
	}
	return KindStandalone
}

func detectKindFromSource(pluginDir string) string {
	src := readInitSourcePrefix(pluginDir, 8192)
	if src == "" {
		return ""
	}
	for _, marker := range []string{
		"register_memory_provider", "MemoryProvider",
		"register_cron_scheduler", "CronScheduler",
		"register_computer_use_provider", "ComputerUseProvider",
	} {
		if strings.Contains(src, marker) {
			return KindExclusive
		}
	}
	if strings.Contains(src, "register_provider") && strings.Contains(src, "ProviderProfile") {
		return KindModelProvider
	}
	return ""
}

func detectMemoryProviderSource(pluginDir string) bool {
	src := readInitSourcePrefix(pluginDir, 8192)
	return strings.Contains(src, "register_memory_provider") || strings.Contains(src, "MemoryProvider")
}

func readInitSourcePrefix(pluginDir string, limit int) string {
	f, err := os.Open(filepath.Join(pluginDir, "__init__.py"))
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// parsePluginYAML parses Hermes plugin.yaml/plugin.yml without external YAML dependencies.
func parsePluginYAML(content string, m *Manifest) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentSection string
	var currentSectionIndent int
	var nestedSettingKey string

	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := countLeadingSpaces(rawLine)

		// Handle list items under a top-level list key.
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if indent > currentSectionIndent && currentSection != "" {
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
				item = stripInlineComment(item)
				item = extractYAMLListItemScalar(item)
				if item != "" {
					appendManifestList(m, currentSection, item)
				}
			}
			continue
		}

		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		val = stripInlineComment(val)

		if indent == 0 {
			currentSection = key
			currentSectionIndent = 0
			nestedSettingKey = ""
			if val == "" {
				continue
			}
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				for _, item := range parseFlowList(val) {
					appendManifestList(m, key, item)
				}
				continue
			}
			applyManifestScalar(m, key, unquoteYAML(val))
			continue
		}

		// Nested keys under author / settings / config_schema.
		switch currentSection {
		case "author":
			if key == "name" && m.Author == "" {
				m.Author = unquoteYAML(val)
			}
		case "settings":
			if indent <= 4 {
				if val != "" {
					m.Settings[key] = parseYAMLScalarValue(val)
					nestedSettingKey = ""
				} else {
					nestedSettingKey = key
				}
			} else if nestedSettingKey != "" && key == "default" && val != "" {
				m.Settings[nestedSettingKey] = parseYAMLScalarValue(val)
			}
		case "config_schema":
			if indent <= 4 && val == "" {
				nestedSettingKey = key
			} else if nestedSettingKey != "" && key == "default" && val != "" {
				if _, exists := m.Settings[nestedSettingKey]; !exists {
					m.Settings[nestedSettingKey] = parseYAMLScalarValue(val)
				}
			}
		}
	}
}

func extractYAMLListItemScalar(item string) string {
	item = strings.TrimSpace(item)
	if item == "" {
		return ""
	}
	// Handle inline mapping items such as `{id: foo, version_range: ">=1.0"}` or `name: MY_KEY`.
	if strings.HasPrefix(item, "{") && strings.HasSuffix(item, "}") {
		inner := strings.TrimSpace(item[1 : len(item)-1])
		for _, part := range strings.Split(inner, ",") {
			k, v, ok := strings.Cut(part, ":")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			if k == "id" || k == "name" {
				return unquoteYAML(strings.TrimSpace(v))
			}
		}
		return ""
	}
	if k, v, ok := strings.Cut(item, ":"); ok {
		k = strings.TrimSpace(k)
		if k == "id" || k == "name" {
			return unquoteYAML(strings.TrimSpace(v))
		}
	}
	return unquoteYAML(item)
}

func applyManifestScalar(m *Manifest, key, val string) {
	switch key {
	case "name":
		m.Name = val
	case "version":
		m.Version = val
	case "description":
		m.Description = val
	case "author":
		m.Author = val
	case "kind":
		m.Kind = val
	case "entry":
		m.Entry = val
	case "label":
		m.Label = val
	case "license":
		m.License = val
	case "homepage":
		m.Homepage = val
	case "provides_memory_provider":
		m.ProvidesMemoryProvider = parseBoolish(val)
	}
}

func appendManifestList(m *Manifest, key, val string) {
	switch key {
	case "tools", "provides_tools":
		m.Tools = appendUnique(m.Tools, val)
	case "hooks", "provides_hooks":
		m.Hooks = appendUnique(m.Hooks, val)
	case "commands", "provides_commands":
		m.Commands = appendUnique(m.Commands, val)
	case "platforms":
		m.Platforms = appendUnique(m.Platforms, val)
	case "requires_env":
		m.RequiresEnv = appendUnique(m.RequiresEnv, val)
	case "requires_plugins":
		m.RequiresPlugins = appendUnique(m.RequiresPlugins, val)
	case "tags":
		m.Tags = appendUnique(m.Tags, val)
	}
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else if r == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}

func stripInlineComment(v string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t') {
				return strings.TrimSpace(v[:i])
			}
		}
	}
	return strings.TrimSpace(v)
}

func unquoteYAML(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}

func parseYAMLScalarValue(v string) any {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	switch strings.ToLower(v) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.Atoi(v); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		return parseFlowList(v)
	}
	return v
}

func parseFlowList(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		v = v[1 : len(v)-1]
	}
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := unquoteYAML(strings.TrimSpace(p))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseBoolish(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func stringField(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringSliceField(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
