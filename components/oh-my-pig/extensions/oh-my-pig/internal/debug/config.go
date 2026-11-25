package debug

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// upstream: packages/coding-agent/src/dap/config.ts
// upstream: packages/coding-agent/src/lsp/config.ts
// upstream: packages/coding-agent/src/utils/config-file-sources.ts

//go:embed defaults.json
var defaultsJSON []byte

var (
	extensionlessDebuggerOrder = []string{"gdb", "lldb-dap"}
	jsDebugServerEnv           = "JS_DEBUG_DAP_SERVER"
	dapPortArgument            = "$" + "{port}"
	attachProbeTimeout         = 1 * time.Second
)

type runtimeAdapterRule struct {
	pattern *regexp.Regexp
	adapter string
}

var runtimeAdapters = []runtimeAdapterRule{
	{regexp.MustCompile(`(?i)^(node|nodejs|deno|electron)(\.exe)?$`), "js-debug-adapter"},
	{regexp.MustCompile(`(?i)^python(\d+(\.\d+)*)?(\.exe)?$`), "debugpy"},
	{regexp.MustCompile(`(?i)^dotnet(\.exe)?$`), "netcoredbg"},
	{regexp.MustCompile(`(?i)^ruby(\.exe)?$`), "rdbg"},
	{regexp.MustCompile(`(?i)^php(\.exe)?$`), "php-debug-adapter"},
}

type localBinPath struct {
	markers []string
	binDir  string
}

var pythonRootMarkers = []string{
	"pyproject.toml",
	"ty.toml",
	"requirements.txt",
	"setup.py",
	"setup.cfg",
	"Pipfile",
	"pyrightconfig.json",
	"ruff.toml",
	".ruff.toml",
}

var localBinPaths = []localBinPath{
	{markers: []string{"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml"}, binDir: "node_modules/.bin"},
	{markers: pythonRootMarkers, binDir: ".venv/bin"},
	{markers: pythonRootMarkers, binDir: ".venv/Scripts"},
	{markers: pythonRootMarkers, binDir: "venv/bin"},
	{markers: pythonRootMarkers, binDir: "venv/Scripts"},
	{markers: pythonRootMarkers, binDir: ".env/bin"},
	{markers: pythonRootMarkers, binDir: ".env/Scripts"},
	{markers: []string{"Gemfile", "Gemfile.lock"}, binDir: "vendor/bundle/bin"},
	{markers: []string{"Gemfile", "Gemfile.lock"}, binDir: "bin"},
	{markers: []string{"go.mod", "go.sum", "go.work"}, binDir: "bin"},
}

var windowsLocalExecutableExtensions = []string{".exe", ".cmd", ".bat"}

type orderedAdapterConfigs struct {
	order  []string
	byName map[string]DapAdapterConfig
}

func newOrderedAdapterConfigs() orderedAdapterConfigs {
	return orderedAdapterConfigs{byName: make(map[string]DapAdapterConfig)}
}

func (o *orderedAdapterConfigs) clone() orderedAdapterConfigs {
	cp := orderedAdapterConfigs{
		order:  slices.Clone(o.order),
		byName: make(map[string]DapAdapterConfig, len(o.byName)),
	}
	for k, v := range o.byName {
		cp.byName[k] = cloneAdapterConfig(v)
	}
	return cp
}

func (o *orderedAdapterConfigs) set(name string, cfg DapAdapterConfig) {
	if _, exists := o.byName[name]; !exists {
		o.order = append(o.order, name)
	}
	o.byName[name] = cfg
}

var defaultOrderedAdapters = loadDefaultAdapters()

// lookPathFn is a test seam for PATH resolution (mirrors piUtils.$which spying in upstream tests).
var lookPathFn = exec.LookPath

// decodeOrderedJSONObject reads a JSON object's top-level keys in declaration order.
func decodeOrderedJSONObject(data []byte) ([]string, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, nil, fmt.Errorf("expected JSON object")
	}
	var order []string
	fields := make(map[string]json.RawMessage)
	for dec.More() {
		ktok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := ktok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected string key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, err
		}
		if _, exists := fields[key]; !exists {
			order = append(order, key)
		}
		fields[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	return order, fields, nil
}

func loadDefaultAdapters() orderedAdapterConfigs {
	out := newOrderedAdapterConfigs()
	order, fields, err := decodeOrderedJSONObject(defaultsJSON)
	if err != nil {
		return out
	}
	for _, name := range order {
		var val any
		if err := json.Unmarshal(fields[name], &val); err != nil {
			continue
		}
		if cfg, ok := normalizeAdapterConfig(val); ok {
			out.set(name, cfg)
		}
	}
	return out
}

func cloneAdapterConfig(c DapAdapterConfig) DapAdapterConfig {
	out := DapAdapterConfig{
		Command:                 c.Command,
		Args:                    slices.Clone(c.Args),
		Languages:               slices.Clone(c.Languages),
		FileTypes:               slices.Clone(c.FileTypes),
		RootMarkers:             slices.Clone(c.RootMarkers),
		ConnectMode:             c.ConnectMode,
		AcceptsDirectoryProgram: c.AcceptsDirectoryProgram,
	}
	if c.LaunchDefaults != nil {
		out.LaunchDefaults = maps.Clone(c.LaunchDefaults)
	}
	if c.AttachDefaults != nil {
		out.AttachDefaults = maps.Clone(c.AttachDefaults)
	}
	return out
}

func normalizeStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			var out []string
			for _, s := range ss {
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}
	var out []string
	for _, entry := range arr {
		if s, ok := entry.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func normalizeMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return map[string]any{}
	}
	return maps.Clone(m)
}

func normalizeAdapterConfig(v any) (DapAdapterConfig, bool) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return DapAdapterConfig{}, false
	}
	cmd, ok := m["command"].(string)
	if !ok || cmd == "" {
		return DapAdapterConfig{}, false
	}
	var cm ConnectMode
	if rawCM, ok := m["connectMode"].(string); ok {
		if rawCM == string(ConnectModeSocket) || rawCM == string(ConnectModeTCP) {
			cm = ConnectMode(rawCM)
		}
	}
	fileTypes := normalizeStringSlice(m["fileTypes"])
	for i := range fileTypes {
		fileTypes[i] = strings.ToLower(fileTypes[i])
	}
	acceptsDir, _ := m["acceptsDirectoryProgram"].(bool)
	return DapAdapterConfig{
		Command:                 cmd,
		Args:                    normalizeStringSlice(m["args"]),
		Languages:               normalizeStringSlice(m["languages"]),
		FileTypes:               fileTypes,
		RootMarkers:             normalizeStringSlice(m["rootMarkers"]),
		LaunchDefaults:          normalizeMap(m["launchDefaults"]),
		AttachDefaults:          normalizeMap(m["attachDefaults"]),
		ConnectMode:             cm,
		AcceptsDirectoryProgram: acceptsDir,
	}, true
}

type orderedRawOverrides struct {
	order  []string
	byName map[string]any
}

func parseConfigOverrides(content string, filePath string) (*orderedRawOverrides, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".yaml" || ext == ".yml" {
		parsed, err := parseSimpleYAML(content)
		if err != nil {
			return nil, err
		}
		m := extractAdaptersMap(parsed)
		if m == nil {
			return nil, nil
		}
		order := extractYAMLAdapterKeyOrder(content, m)
		return &orderedRawOverrides{order: order, byName: m}, nil
	}
	topOrder, topFields, err := decodeOrderedJSONObject([]byte(content))
	if err != nil {
		return nil, err
	}
	if rawAdapters, hasAdapters := topFields["adapters"]; hasAdapters {
		if subOrder, subFields, err := decodeOrderedJSONObject(rawAdapters); err == nil {
			byName := make(map[string]any, len(subFields))
			for _, name := range subOrder {
				var val any
				if err := json.Unmarshal(subFields[name], &val); err == nil {
					byName[name] = val
				}
			}
			return &orderedRawOverrides{order: subOrder, byName: byName}, nil
		}
	}
	byName := make(map[string]any, len(topFields))
	for _, name := range topOrder {
		var val any
		if err := json.Unmarshal(topFields[name], &val); err == nil {
			byName[name] = val
		}
	}
	return &orderedRawOverrides{order: topOrder, byName: byName}, nil
}

// extractYAMLAdapterKeyOrder recovers adapter declaration order from YAML lines
// so new entries append in source order like JS object keys.
func extractYAMLAdapterKeyOrder(content string, adapters map[string]any) []string {
	var order []string
	seen := make(map[string]bool, len(adapters))
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(stripYAMLComment(raw))
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			continue
		}
		if key, _, ok := splitYAMLKeyValue(trimmed); ok {
			if _, inMap := adapters[key]; inMap && !seen[key] {
				seen[key] = true
				order = append(order, key)
			}
		}
	}
	// Include any remaining keys deterministically if missed.
	var extra []string
	for k := range adapters {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(order, extra...)
}

func extractAdaptersMap(parsed any) map[string]any {
	m, ok := parsed.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	if nested, ok := m["adapters"].(map[string]any); ok {
		return nested
	}
	return m
}

func adapterConfigToMap(c DapAdapterConfig) map[string]any {
	m := map[string]any{
		"command":                 c.Command,
		"args":                    c.Args,
		"languages":               c.Languages,
		"fileTypes":               c.FileTypes,
		"rootMarkers":             c.RootMarkers,
		"launchDefaults":          maps.Clone(c.LaunchDefaults),
		"attachDefaults":          maps.Clone(c.AttachDefaults),
		"acceptsDirectoryProgram": c.AcceptsDirectoryProgram,
	}
	if c.ConnectMode != "" {
		m["connectMode"] = string(c.ConnectMode)
	}
	return m
}

func mergeAdapters(base orderedAdapterConfigs, overrides *orderedRawOverrides) orderedAdapterConfigs {
	merged := base.clone()
	if overrides == nil {
		return merged
	}
	for _, name := range overrides.order {
		rawCfg := overrides.byName[name]
		existing, hasExisting := merged.byName[name]
		var candidate any = rawCfg
		if hasExisting {
			if overrideMap, ok := rawCfg.(map[string]any); ok {
				combined := adapterConfigToMap(existing)
				for k, v := range overrideMap {
					combined[k] = v
				}
				if existing.LaunchDefaults != nil || overrideMap["launchDefaults"] != nil {
					ld := maps.Clone(existing.LaunchDefaults)
					if ld == nil {
						ld = map[string]any{}
					}
					for k, v := range normalizeMap(overrideMap["launchDefaults"]) {
						ld[k] = v
					}
					combined["launchDefaults"] = ld
				}
				if existing.AttachDefaults != nil || overrideMap["attachDefaults"] != nil {
					ad := maps.Clone(existing.AttachDefaults)
					if ad == nil {
						ad = map[string]any{}
					}
					for k, v := range normalizeMap(overrideMap["attachDefaults"]) {
						ad[k] = v
					}
					combined["attachDefaults"] = ad
				}
				candidate = combined
			}
		}
		if normalized, ok := normalizeAdapterConfig(candidate); ok {
			merged.set(name, normalized)
		}
	}
	return merged
}

// configFilePaths returns dap config file paths in priority order (highest first),
// mirroring packages/coding-agent/src/utils/config-file-sources.ts.
// When includeProject is false (untrusted project), cwd and project config dirs
// are skipped so untrusted repositories cannot override adapter executables.
func configFilePaths(cwd string, includeProject bool) []string {
	filenames := []string{"dap.json", ".dap.json", "dap.yaml", ".dap.yaml", "dap.yml", ".dap.yml"}
	var paths []string
	addDir := func(dir string) {
		if dir == "" {
			return
		}
		for _, name := range filenames {
			paths = append(paths, filepath.Join(dir, name))
		}
	}

	if includeProject {
		// 1. cwd
		addDir(cwd)

		// 2. Project config dirs (.omp, .claude, .codex, .gemini)
		for _, sub := range []string{".omp", ".claude", ".codex", ".gemini"} {
			addDir(filepath.Join(cwd, sub))
		}
	}

	// 3. User config dirs
	home, _ := os.UserHomeDir()
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" {
		if cfgDir := os.Getenv("PI_CONFIG_DIR"); cfgDir != "" {
			agentDir = filepath.Join(cfgDir, "agent")
		} else if home != "" {
			agentDir = filepath.Join(home, ".omp", "agent")
		}
	}
	addDir(agentDir)
	if home != "" {
		claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
		if claudeDir == "" {
			claudeDir = filepath.Join(home, ".claude")
		}
		addDir(claudeDir)
		addDir(filepath.Join(home, ".codex"))
		addDir(filepath.Join(home, ".gemini"))
		// 4. Home directory files
		addDir(home)
	}
	return paths
}

// hasProjectConfigOrLocalBin reports whether cwd (or its project config dirs)
// contains any dap config file or a project-local bin directory for any default adapter.
func hasProjectConfigFiles(cwd string) bool {
	if cwd == "" {
		return false
	}
	filenames := []string{"dap.json", ".dap.json", "dap.yaml", ".dap.yaml", "dap.yml", ".dap.yml"}
	dirs := []string{
		cwd,
		filepath.Join(cwd, ".omp"),
		filepath.Join(cwd, ".claude"),
		filepath.Join(cwd, ".codex"),
		filepath.Join(cwd, ".gemini"),
	}
	for _, dir := range dirs {
		for _, name := range filenames {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				return true
			}
		}
	}
	return false
}

// hasProjectLocalBin reports whether any project-local bin dir (node_modules/.bin, .venv, bin)
// exists under root (or cwd) for a root marker.
func hasProjectLocalBin(cwd string) bool {
	if cwd == "" {
		return false
	}
	for _, entry := range localBinPaths {
		if !hasRootMarkers(cwd, entry.markers) {
			continue
		}
		if info, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(entry.binDir))); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func loadAdapterConfigsOrdered(cwd string, includeProject bool) orderedAdapterConfigs {
	adapters := defaultOrderedAdapters.clone()
	if cwd == "" {
		return adapters
	}
	paths := configFilePaths(cwd, includeProject)
	// Merge lowest priority first so higher priority overrides field-by-field.
	for i := len(paths) - 1; i >= 0; i-- {
		data, err := os.ReadFile(paths[i])
		if err != nil {
			continue
		}
		overrides, err := parseConfigOverrides(string(data), paths[i])
		if err != nil || overrides == nil {
			continue
		}
		adapters = mergeAdapters(adapters, overrides)
	}
	return adapters
}

// GetAdapterConfigs returns all merged DAP adapter configs for cwd.
// upstream: packages/coding-agent/src/dap/config.ts:100-112
func GetAdapterConfigs(cwd string) map[string]DapAdapterConfig {
	return loadAdapterConfigsOrdered(cwd, true).byName
}

func getAdapterConfigsWithTrust(cwd string, includeProject bool) map[string]DapAdapterConfig {
	return loadAdapterConfigsOrdered(cwd, includeProject).byName
}

func hasRootMarkers(dir string, markers []string) bool {
	if len(markers) == 0 {
		return false
	}
	var entries []os.DirEntry
	readEntries := false
	for _, marker := range markers {
		if strings.Contains(marker, "*") {
			if !readEntries {
				entries, _ = os.ReadDir(dir)
				readEntries = true
			}
			for _, entry := range entries {
				if matched, _ := filepath.Match(marker, entry.Name()); matched {
					return true
				}
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

func resolveLocalCommand(basePath string) string {
	if info, err := os.Stat(basePath); err == nil && !info.IsDir() {
		return basePath
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, ext := range windowsLocalExecutableExtensions {
		candidate := basePath + ext
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func resolveCommandFromLocalRoot(command string, root string) string {
	for _, entry := range localBinPaths {
		if !hasRootMarkers(root, entry.markers) {
			continue
		}
		if resolved := resolveLocalCommand(filepath.Join(root, filepath.FromSlash(entry.binDir), command)); resolved != "" {
			return resolved
		}
	}
	return ""
}

func normalizeCommandForCwd(command string, cwd string) string {
	if filepath.IsAbs(command) {
		return command
	}
	if strings.HasPrefix(command, "./") || strings.HasPrefix(command, "../") ||
		strings.HasPrefix(command, `.\`) || strings.HasPrefix(command, `..\`) {
		return filepath.Join(cwd, filepath.FromSlash(strings.ReplaceAll(command, `\`, "/")))
	}
	return command
}

func resolveCommand(command string, cwd string, localRoots []string, allowProjectLocal bool) string {
	commandIsBare := !filepath.IsAbs(command) && !strings.Contains(command, "/") && !strings.Contains(command, `\`)
	if commandIsBare && allowProjectLocal {
		if len(localRoots) > 0 {
			for _, root := range localRoots {
				if resolved := resolveCommandFromLocalRoot(command, root); resolved != "" {
					return resolved
				}
			}
		} else if cwd != "" {
			if resolved := resolveCommandFromLocalRoot(command, cwd); resolved != "" {
				return resolved
			}
		}
	}
	if p, err := lookPathFn(command); err == nil && p != "" {
		return p
	}
	return ""
}

func resolveJsDebugServerPath(cwd string) string {
	var candidates []string
	if configured := os.Getenv(jsDebugServerEnv); configured != "" {
		if filepath.IsAbs(configured) {
			candidates = append(candidates, configured)
		} else {
			candidates = append(candidates, filepath.Join(cwd, configured))
		}
	}
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if dataHome != "" {
		candidates = append(candidates, filepath.Join(dataHome, "nvim", "mason", "packages", "js-debug-adapter", "js-debug", "src", "dapDebugServer.js"))
	}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "opt", "js-debug", "src", "dapDebugServer.js"))
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

func resolveDefaultJsDebugAdapter(adapterName string, cfg DapAdapterConfig, cwd string, localRoots []string, allowProjectLocal bool) (*DapResolvedAdapter, bool) {
	if adapterName != "js-debug-adapter" || cfg.Command != "js-debug-adapter" {
		return nil, false
	}
	serverPath := resolveJsDebugServerPath(cwd)
	if serverPath == "" {
		return nil, true
	}
	nodeCmd := resolveCommand("node", cwd, localRoots, allowProjectLocal)
	cmdName := "node"
	resolvedCmd := nodeCmd
	if resolvedCmd == "" {
		bunCmd := resolveCommand("bun", cwd, localRoots, allowProjectLocal)
		if bunCmd == "" {
			return nil, true
		}
		cmdName = "bun"
		resolvedCmd = bunCmd
	}
	return &DapResolvedAdapter{
		Name:                    adapterName,
		Command:                 cmdName,
		Args:                    []string{serverPath, dapPortArgument, "127.0.0.1"},
		ResolvedCommand:         resolvedCmd,
		Languages:               slices.Clone(cfg.Languages),
		FileTypes:               slices.Clone(cfg.FileTypes),
		RootMarkers:             slices.Clone(cfg.RootMarkers),
		LaunchDefaults:          normalizeMap(cfg.LaunchDefaults),
		AttachDefaults:          normalizeMap(cfg.AttachDefaults),
		ConnectMode:             ConnectModeTCP,
		AcceptsDirectoryProgram: cfg.AcceptsDirectoryProgram,
	}, true
}

func resolveAdapterFromConfig(adapterName string, configs map[string]DapAdapterConfig, cwd string, localRoots []string, allowProjectLocal bool) *DapResolvedAdapter {
	cfg, ok := configs[adapterName]
	if !ok {
		return nil
	}
	if resolved, handled := resolveDefaultJsDebugAdapter(adapterName, cfg, cwd, localRoots, allowProjectLocal); handled {
		return resolved
	}
	normalizedCmd := normalizeCommandForCwd(cfg.Command, cwd)
	commandIsBare := !filepath.IsAbs(cfg.Command) && !strings.Contains(cfg.Command, "/") && !strings.Contains(cfg.Command, `\`)
	var roots []string
	if commandIsBare && allowProjectLocal {
		roots = localRoots
	}
	resolvedCmd := resolveCommand(normalizedCmd, cwd, roots, allowProjectLocal)
	if resolvedCmd == "" {
		return nil
	}
	cm := cfg.ConnectMode
	if cm == "" {
		cm = ConnectModeStdio
	}
	return &DapResolvedAdapter{
		Name:                    adapterName,
		Command:                 cfg.Command,
		Args:                    slices.Clone(cfg.Args),
		ResolvedCommand:         resolvedCmd,
		Languages:               slices.Clone(cfg.Languages),
		FileTypes:               slices.Clone(cfg.FileTypes),
		RootMarkers:             slices.Clone(cfg.RootMarkers),
		LaunchDefaults:          normalizeMap(cfg.LaunchDefaults),
		AttachDefaults:          normalizeMap(cfg.AttachDefaults),
		ConnectMode:             cm,
		AcceptsDirectoryProgram: cfg.AcceptsDirectoryProgram,
	}
}

// ResolveAdapter resolves a single named adapter for cwd.
func ResolveAdapter(adapterName string, cwd string) *DapResolvedAdapter {
	return resolveAdapterWithTrust(adapterName, cwd, true)
}

func resolveAdapterWithTrust(adapterName string, cwd string, includeProject bool) *DapResolvedAdapter {
	return resolveAdapterFromConfig(adapterName, loadAdapterConfigsOrdered(cwd, includeProject).byName, cwd, nil, includeProject)
}

// GetAvailableAdapters returns all configured adapters whose executable is available,
// in defaults.json + config-merge insertion order (matching dap/config.ts:211-216).
func GetAvailableAdapters(cwd string) []DapResolvedAdapter {
	return getAvailableAdaptersWithTrust(cwd, true)
}

func getAvailableAdaptersWithTrust(cwd string, includeProject bool) []DapResolvedAdapter {
	ordered := loadAdapterConfigsOrdered(cwd, includeProject)
	var out []DapResolvedAdapter
	for _, name := range ordered.order {
		if a := resolveAdapterFromConfig(name, ordered.byName, cwd, nil, includeProject); a != nil {
			out = append(out, *a)
		}
	}
	return out
}

type launchAdapterCandidate struct {
	name    string
	rootDir string
}

func findRootMarkerInLaunchAncestry(program string, cwd string, markers []string, programKind LaunchProgramKind) string {
	if len(markers) == 0 {
		return ""
	}
	resolved := program
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	dir := resolved
	if programKind != ProgramKindDirectory {
		dir = filepath.Dir(resolved)
	}
	for {
		if hasRootMarkers(dir, markers) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func resolveAdapterForLaunch(adapterName string, configs map[string]DapAdapterConfig, cwd string, rootDir string, allowProjectLocal bool) *DapResolvedAdapter {
	var localRoots []string
	if allowProjectLocal && rootDir != "" && rootDir != cwd {
		localRoots = []string{rootDir, cwd}
	}
	return resolveAdapterFromConfig(adapterName, configs, cwd, localRoots, allowProjectLocal)
}

func sortAdaptersForLaunch(program string, cwd string, programKind LaunchProgramKind, adapters []DapResolvedAdapter) []DapResolvedAdapter {
	ext := strings.ToLower(filepath.Ext(program))
	type scored struct {
		adapter           DapResolvedAdapter
		hasExtensionMatch bool
		hasRootMatch      bool
	}
	items := make([]scored, len(adapters))
	for i, a := range adapters {
		items[i] = scored{
			adapter:           a,
			hasExtensionMatch: ext != "" && slices.Contains(a.FileTypes, ext),
			hasRootMatch:      findRootMarkerInLaunchAncestry(program, cwd, a.RootMarkers, programKind) != "",
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		l, r := items[i], items[j]
		if l.hasExtensionMatch != r.hasExtensionMatch {
			return l.hasExtensionMatch
		}
		if l.hasRootMatch != r.hasRootMatch {
			return l.hasRootMatch
		}
		lr := slices.Index(extensionlessDebuggerOrder, l.adapter.Name)
		rr := slices.Index(extensionlessDebuggerOrder, r.adapter.Name)
		if lr < 0 {
			lr = int(^uint(0) >> 1)
		}
		if rr < 0 {
			rr = int(^uint(0) >> 1)
		}
		if lr != rr {
			return lr < rr
		}
		return l.adapter.Name < r.adapter.Name
	})
	out := make([]DapResolvedAdapter, len(items))
	for i, it := range items {
		out[i] = it.adapter
	}
	return out
}

func selectAutomaticLaunchAdapter(program string, cwd string, programKind LaunchProgramKind, ordered orderedAdapterConfigs, allowProjectLocal bool) LaunchAdapterSelection {
	configs := ordered.byName
	names := ordered.order

	unavailableFor := func(cand launchAdapterCandidate) LaunchAdapterSelection {
		cfg, ok := configs[cand.name]
		if !ok {
			return LaunchAdapterSelection{Kind: "none"}
		}
		return LaunchAdapterSelection{Kind: "unavailable", AdapterName: cand.name, Command: cfg.Command}
	}

	ext := strings.ToLower(filepath.Ext(program))
	if ext != "" {
		var configured []launchAdapterCandidate
		var available []DapResolvedAdapter
		for _, name := range names {
			cfg := configs[name]
			if !slices.Contains(cfg.FileTypes, ext) {
				continue
			}
			rootDir := findRootMarkerInLaunchAncestry(program, cwd, cfg.RootMarkers, programKind)
			configured = append(configured, launchAdapterCandidate{name: name, rootDir: rootDir})
			if a := resolveAdapterForLaunch(name, configs, cwd, rootDir, allowProjectLocal); a != nil {
				available = append(available, *a)
			}
		}
		sorted := sortAdaptersForLaunch(program, cwd, programKind, available)
		if len(sorted) > 0 {
			sel := sorted[0]
			return LaunchAdapterSelection{Kind: "adapter", Adapter: &sel}
		}
		for _, cand := range configured {
			if cand.rootDir != "" {
				return unavailableFor(cand)
			}
		}
		if len(configured) > 0 {
			return unavailableFor(configured[0])
		}
	}

	var available []DapResolvedAdapter
	var rootMatches []launchAdapterCandidate
	var directoryMatches []launchAdapterCandidate
	for _, name := range names {
		cfg := configs[name]
		rootDir := findRootMarkerInLaunchAncestry(program, cwd, cfg.RootMarkers, programKind)
		cand := launchAdapterCandidate{name: name, rootDir: rootDir}
		if rootDir != "" {
			rootMatches = append(rootMatches, cand)
			if cfg.AcceptsDirectoryProgram {
				directoryMatches = append(directoryMatches, cand)
			}
		}
		if !slices.Contains(extensionlessDebuggerOrder, name) && rootDir == "" {
			continue
		}
		if a := resolveAdapterForLaunch(name, configs, cwd, rootDir, allowProjectLocal); a != nil {
			available = append(available, *a)
		}
	}
	if programKind == ProgramKindDirectory && len(directoryMatches) > 0 {
		matchNames := make(map[string]bool, len(directoryMatches))
		for _, c := range directoryMatches {
			matchNames[c.name] = true
		}
		var dirAdapters []DapResolvedAdapter
		for _, a := range available {
			if a.AcceptsDirectoryProgram && matchNames[a.Name] {
				dirAdapters = append(dirAdapters, a)
			}
		}
		sorted := sortAdaptersForLaunch(program, cwd, programKind, dirAdapters)
		if len(sorted) > 0 {
			sel := sorted[0]
			return LaunchAdapterSelection{Kind: "adapter", Adapter: &sel}
		}
		return unavailableFor(directoryMatches[0])
	}

	candidates := available
	if programKind == ProgramKindDirectory {
		var dirAdapters []DapResolvedAdapter
		for _, a := range available {
			if a.AcceptsDirectoryProgram {
				dirAdapters = append(dirAdapters, a)
			}
		}
		if len(dirAdapters) > 0 {
			candidates = dirAdapters
		}
	}
	sorted := sortAdaptersForLaunch(program, cwd, programKind, candidates)
	if len(sorted) > 0 {
		sel := sorted[0]
		return LaunchAdapterSelection{Kind: "adapter", Adapter: &sel}
	}
	if len(rootMatches) > 0 {
		return unavailableFor(rootMatches[0])
	}
	return LaunchAdapterSelection{Kind: "none"}
}

// SelectLaunchAdapter selects a launch adapter or reports why matching config cannot run.
// upstream: packages/coding-agent/src/dap/config.ts:357-372
func SelectLaunchAdapter(program string, cwd string, adapterName string, programKind LaunchProgramKind) LaunchAdapterSelection {
	return selectLaunchAdapterWithTrust(program, cwd, adapterName, programKind, true)
}

func selectLaunchAdapterWithTrust(program string, cwd string, adapterName string, programKind LaunchProgramKind, includeProject bool) LaunchAdapterSelection {
	if programKind == "" {
		programKind = ProgramKindFile
	}
	ordered := loadAdapterConfigsOrdered(cwd, includeProject)
	if adapterName != "" {
		cfg, ok := ordered.byName[adapterName]
		if !ok {
			return LaunchAdapterSelection{Kind: "none"}
		}
		rootDir := findRootMarkerInLaunchAncestry(program, cwd, cfg.RootMarkers, programKind)
		a := resolveAdapterForLaunch(adapterName, ordered.byName, cwd, rootDir, includeProject)
		if a != nil {
			return LaunchAdapterSelection{Kind: "adapter", Adapter: a}
		}
		return LaunchAdapterSelection{Kind: "unavailable", AdapterName: adapterName, Command: cfg.Command}
	}
	return selectAutomaticLaunchAdapter(program, cwd, programKind, ordered, includeProject)
}

func isV8Inspector(ctx context.Context, host string, port int) bool {
	probeCtx, cancel := context.WithTimeout(ctx, attachProbeTimeout)
	defer cancel()
	url := fmt.Sprintf("http://%s/json/version", net.JoinHostPort(host, strconv.Itoa(port)))
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	if _, ok := body["V8-Version"].(string); ok {
		return true
	}
	if _, ok := body["Protocol-Version"].(string); ok {
		return true
	}
	return false
}

// SelectAttachAdapter selects an adapter for attach based on explicit adapter name,
// target PID runtime image, target port V8 inspector probe, or native fallback.
// upstream: packages/coding-agent/src/dap/config.ts:430-457
func SelectAttachAdapter(ctx context.Context, cwd string, adapterName string, target DapAttachTarget) *DapResolvedAdapter {
	return selectAttachAdapterWithTrust(ctx, cwd, adapterName, target, true)
}

func selectAttachAdapterWithTrust(ctx context.Context, cwd string, adapterName string, target DapAttachTarget, includeProject bool) *DapResolvedAdapter {
	if adapterName != "" {
		return resolveAdapterWithTrust(adapterName, cwd, includeProject)
	}
	available := getAvailableAdaptersWithTrust(cwd, includeProject)
	byName := func(name string) *DapResolvedAdapter {
		for i := range available {
			if available[i].Name == name {
				cp := available[i]
				return &cp
			}
		}
		return nil
	}
	if target.PID != nil {
		image := processImageName(ctx, *target.PID)
		if image != "" {
			for _, rule := range runtimeAdapters {
				if rule.pattern.MatchString(image) {
					if m := byName(rule.adapter); m != nil {
						return m
					}
					break
				}
			}
		}
	}
	if target.Port != nil {
		if jsDebug := byName("js-debug-adapter"); jsDebug != nil {
			host := target.Host
			if host == "" {
				host = "127.0.0.1"
			}
			if isV8Inspector(ctx, host, *target.Port) {
				return jsDebug
			}
		}
		if debugpy := byName("debugpy"); debugpy != nil {
			return debugpy
		}
	}
	for _, preferred := range extensionlessDebuggerOrder {
		if m := byName(preferred); m != nil {
			return m
		}
	}
	if len(available) > 0 {
		cp := available[0]
		return &cp
	}
	return nil
}

// ResolveLaunchOverrides computes adapter-specific launch arguments that depend on
// the resolved program (for dlv: directory/.go -> mode=debug, file -> mode=exec,
// plus outputMode=remote so Delve routes debuggee stdout/stderr as DAP output events
// rather than directly to dlv's own stdio which socket-mode discards).
// upstream: packages/coding-agent/src/dap/config.ts:472-487
func ResolveLaunchOverrides(adapter DapResolvedAdapter, program string, programKind LaunchProgramKind) map[string]any {
	if adapter.Name == "dlv" {
		ext := strings.ToLower(filepath.Ext(program))
		if programKind == ProgramKindDirectory || ext == ".go" {
			return map[string]any{"mode": "debug", "outputMode": "remote"}
		}
		if programKind == ProgramKindFile {
			return map[string]any{"mode": "exec", "outputMode": "remote"}
		}
	}
	return map[string]any{}
}
