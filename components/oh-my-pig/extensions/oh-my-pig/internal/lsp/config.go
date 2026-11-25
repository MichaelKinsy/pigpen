package lsp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/lsp/config.ts, packages/coding-agent/src/utils/config-file-sources.ts

//go:embed defaults.json
var defaultsJSON []byte

// NamedServer pairs a server name with its ServerConfig, preserving deterministic order.
type NamedServer struct {
	Name   string
	Config ServerConfig
}

// LspConfig holds the resolved server configuration for one workspace cwd and
// project-trust mode. All mutations and reads on cached instances are guarded
// by configCacheMu.
type LspConfig struct {
	Servers              map[string]ServerConfig
	ServerOrder          []string // preserves defaults.json + override order for deterministic ranking
	IdleTimeoutMs        *int
	Nested               *nestedServers
	Unresolved           map[string]ServerConfig
	UnresolvedOrder      []string
	ProjectConfigSkipped bool // true when untrusted project had project-level lsp config or local bin skipped
	trusted              bool
}

type nestedServers struct {
	cwd     string
	servers map[string]ServerConfig
	order   []string
}

type rawServerConfig struct {
	Command               *string
	Args                  []string
	HasArgs               bool
	FileTypes             []string
	ExtensionToLanguage   map[string]any
	LanguageID            *string
	RootMarkers           []string
	InitOptions           map[string]any
	InitializationOptions map[string]any
	Settings              map[string]any
	Disabled              *bool
	WarmupTimeoutMs       *int
	Capabilities          *ServerCapabilitiesHints
	IsLinter              *bool
	WatchFiles            *bool
}

// decodeRawServerConfig filters fields per-key (matching omp's normalizeServerConfig
// in lsp/config.ts:86-117) so a single mistyped optional field does not discard
// the entire config file.
func decodeRawServerConfig(data json.RawMessage) (rawServerConfig, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return rawServerConfig{}, false
	}
	var out rawServerConfig
	if raw, ok := obj["command"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			out.Command = &s
		}
	}
	if raw, ok := obj["args"]; ok {
		var arr []any
		if err := json.Unmarshal(raw, &arr); err == nil {
			out.HasArgs = true
			for _, item := range arr {
				if s, ok := item.(string); ok {
					out.Args = append(out.Args, s)
				}
			}
		}
	}
	if raw, ok := obj["fileTypes"]; ok {
		var arr []any
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, item := range arr {
				if s, ok := item.(string); ok && s != "" {
					out.FileTypes = append(out.FileTypes, s)
				}
			}
		}
	}
	if raw, ok := obj["extensionToLanguage"]; ok {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil && m != nil {
			out.ExtensionToLanguage = m
		}
	}
	if raw, ok := obj["languageId"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			out.LanguageID = &s
		}
	}
	if raw, ok := obj["rootMarkers"]; ok {
		var arr []any
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, item := range arr {
				if s, ok := item.(string); ok && s != "" {
					out.RootMarkers = append(out.RootMarkers, s)
				}
			}
		}
	}
	if raw, ok := obj["initOptions"]; ok {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil && m != nil {
			out.InitOptions = m
		}
	}
	if raw, ok := obj["initializationOptions"]; ok {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil && m != nil {
			out.InitializationOptions = m
		}
	}
	if raw, ok := obj["settings"]; ok {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil && m != nil {
			out.Settings = m
		}
	}
	if raw, ok := obj["disabled"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil {
			out.Disabled = &b
		}
	}
	if raw, ok := obj["warmupTimeoutMs"]; ok {
		var n int
		if err := json.Unmarshal(raw, &n); err == nil {
			out.WarmupTimeoutMs = &n
		}
	}
	if raw, ok := obj["capabilities"]; ok {
		var caps ServerCapabilitiesHints
		if err := json.Unmarshal(raw, &caps); err == nil {
			out.Capabilities = &caps
		}
	}
	if raw, ok := obj["isLinter"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil {
			out.IsLinter = &b
		}
	}
	if raw, ok := obj["watchFiles"]; ok {
		var b bool
		if err := json.Unmarshal(raw, &b); err == nil {
			out.WatchFiles = &b
		}
	}
	return out, true
}

type normalizedConfigFile struct {
	servers       map[string]rawServerConfig
	order         []string
	idleTimeoutMs *int
}

// loadDefaultsOrdered decodes embedded defaults.json while preserving key order
// (so python primaries pyright/basedpyright/pylsp/ty and elixirls/expert keep
// omp's exact priority order).
func loadDefaultsOrdered() (map[string]ServerConfig, []string) {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(defaultsJSON, &rawMap); err != nil {
		return map[string]ServerConfig{}, nil
	}
	order := extractTopLevelJSONKeys(defaultsJSON)
	out := make(map[string]ServerConfig, len(rawMap))
	var validOrder []string
	for _, name := range order {
		rawBytes, ok := rawMap[name]
		if !ok {
			continue
		}
		raw, ok := decodeRawServerConfig(rawBytes)
		if !ok {
			continue
		}
		if cfg, valid := normalizeServerConfig(raw); valid {
			out[name] = cfg
			validOrder = append(validOrder, name)
		}
	}
	return out, validOrder
}

// extractTopLevelJSONKeys returns the top-level object keys in wire order.
func extractTopLevelJSONKeys(data []byte) []string {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			break
		}
		key, ok := kTok.(string)
		if !ok {
			break
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			break
		}
	}
	return keys
}

func nonEmptyStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func normalizeServerConfig(raw rawServerConfig) (ServerConfig, bool) {
	if raw.Command == nil || *raw.Command == "" {
		return ServerConfig{}, false
	}
	fileTypes := nonEmptyStrings(raw.FileTypes)
	if len(fileTypes) == 0 && len(raw.ExtensionToLanguage) > 0 {
		for ext := range raw.ExtensionToLanguage {
			if ext != "" {
				fileTypes = append(fileTypes, ext)
			}
		}
		sort.Strings(fileTypes)
	}
	rootMarkers := nonEmptyStrings(raw.RootMarkers)
	if len(rootMarkers) == 0 && len(raw.ExtensionToLanguage) > 0 {
		rootMarkers = []string{"."}
	}
	if len(fileTypes) == 0 || len(rootMarkers) == 0 {
		return ServerConfig{}, false
	}
	cfg := ServerConfig{
		Command:      *raw.Command,
		Args:         raw.Args,
		FileTypes:    fileTypes,
		RootMarkers:  rootMarkers,
		InitOptions:  raw.InitOptions,
		Settings:     raw.Settings,
		Capabilities: raw.Capabilities,
		WatchFiles:   raw.WatchFiles,
	}
	if cfg.InitOptions == nil && raw.InitializationOptions != nil {
		cfg.InitOptions = raw.InitializationOptions
	}
	if raw.LanguageID != nil && *raw.LanguageID != "" {
		cfg.LanguageID = *raw.LanguageID
	}
	if raw.Disabled != nil {
		cfg.Disabled = *raw.Disabled
	}
	if raw.WarmupTimeoutMs != nil {
		cfg.WarmupTimeoutMs = *raw.WarmupTimeoutMs
	}
	if raw.IsLinter != nil {
		cfg.IsLinter = *raw.IsLinter
	}
	return cfg, true
}

func mergeServerOverride(base ServerConfig, ov rawServerConfig) (ServerConfig, bool) {
	merged := base
	if ov.Command != nil && *ov.Command != "" {
		merged.Command = *ov.Command
	}
	if ov.HasArgs {
		merged.Args = ov.Args
	}
	if ft := nonEmptyStrings(ov.FileTypes); len(ft) > 0 {
		merged.FileTypes = ft
	} else if len(ov.ExtensionToLanguage) > 0 {
		var exts []string
		for ext := range ov.ExtensionToLanguage {
			if ext != "" {
				exts = append(exts, ext)
			}
		}
		sort.Strings(exts)
		if len(exts) > 0 {
			merged.FileTypes = exts
		}
	}
	if rm := nonEmptyStrings(ov.RootMarkers); len(rm) > 0 {
		merged.RootMarkers = rm
	}
	if ov.LanguageID != nil && *ov.LanguageID != "" {
		merged.LanguageID = *ov.LanguageID
	}
	if ov.InitOptions != nil {
		merged.InitOptions = ov.InitOptions
	} else if ov.InitializationOptions != nil {
		merged.InitOptions = ov.InitializationOptions
	}
	if ov.Settings != nil {
		merged.Settings = ov.Settings
	}
	if ov.Disabled != nil {
		merged.Disabled = *ov.Disabled
	}
	if ov.WarmupTimeoutMs != nil {
		merged.WarmupTimeoutMs = *ov.WarmupTimeoutMs
	}
	if ov.Capabilities != nil {
		merged.Capabilities = ov.Capabilities
	}
	if ov.IsLinter != nil {
		merged.IsLinter = *ov.IsLinter
	}
	if ov.WatchFiles != nil {
		merged.WatchFiles = ov.WatchFiles
	}
	if merged.Command == "" || len(merged.FileTypes) == 0 || len(merged.RootMarkers) == 0 {
		return ServerConfig{}, false
	}
	return merged, true
}

// parseConfigFile parses JSON (and YAML config files) per-server so one bad
// server entry or mistyped optional field does not discard the whole file.
func parseConfigFile(filePath string) *normalizedConfigFile {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	var top map[string]json.RawMessage
	var keys []string
	if ext == ".yaml" || ext == ".yml" {
		converted, ok := parseSimpleYAMLToJSON(string(data))
		if !ok {
			return nil
		}
		if err := json.Unmarshal(converted, &top); err != nil {
			return nil
		}
		keys = extractTopLevelJSONKeys(converted)
	} else {
		if err := json.Unmarshal(data, &top); err != nil {
			return nil
		}
		keys = extractTopLevelJSONKeys(data)
	}
	if len(top) == 0 {
		return nil
	}
	out := &normalizedConfigFile{
		servers: make(map[string]rawServerConfig),
	}
	if rawIdle, ok := top["idleTimeoutMs"]; ok {
		var ms int
		if err := json.Unmarshal(rawIdle, &ms); err == nil {
			out.idleTimeoutMs = &ms
		}
	}
	if rawServers, ok := top["servers"]; ok {
		var srvMap map[string]json.RawMessage
		if err := json.Unmarshal(rawServers, &srvMap); err == nil && srvMap != nil {
			for _, k := range extractTopLevelJSONKeys(rawServers) {
				if rawSrv, valid := decodeRawServerConfig(srvMap[k]); valid {
					out.servers[k] = rawSrv
					out.order = append(out.order, k)
				}
			}
			return out
		}
	}
	for _, k := range keys {
		if k == "idleTimeoutMs" {
			continue
		}
		if rawSrv, valid := decodeRawServerConfig(top[k]); valid {
			out.servers[k] = rawSrv
			out.order = append(out.order, k)
		}
	}
	return out
}

type configSourceEntry struct {
	path      string
	isProject bool
}

// configFilePaths returns candidate LSP config files ordered from lowest to
// highest priority, matching omp v19.1.1's config.ts:17-22 (.omp, .claude,
// .codex, .gemini — no .pi directories).
// upstream: packages/coding-agent/src/utils/config-file-sources.ts,
//
//	packages/coding-agent/src/config.ts:17-22
func configFilePaths(cwd string) []configSourceEntry {
	filenames := []string{"lsp.json", ".lsp.json", "lsp.yaml", ".lsp.yaml", "lsp.yml", ".lsp.yml"}
	var highToLow []configSourceEntry
	addDir := func(dir string, isProject bool) {
		if dir == "" {
			return
		}
		for _, fn := range filenames {
			highToLow = append(highToLow, configSourceEntry{
				path:      filepath.Join(dir, fn),
				isProject: isProject,
			})
		}
	}

	// 1. Project root
	addDir(cwd, true)

	// 2. Project config dirs (.omp, .claude, .codex, .gemini)
	for _, d := range []string{".omp", ".claude", ".codex", ".gemini"} {
		addDir(filepath.Join(cwd, d), true)
	}

	// 3. User config dirs (~/.omp/agent, ~/.claude, ~/.codex, ~/.gemini)
	home, _ := os.UserHomeDir()
	agentDir := os.Getenv("PI_CODING_AGENT_DIR")
	if agentDir == "" && home != "" {
		agentDir = filepath.Join(home, ".omp", "agent")
	}
	addDir(agentDir, false)
	if home != "" {
		addDir(filepath.Join(home, ".claude"), false)
		addDir(filepath.Join(home, ".codex"), false)
		addDir(filepath.Join(home, ".gemini"), false)
		// 4. User home root
		addDir(home, false)
	}

	// Reverse so later entries override earlier ones (lowest -> highest).
	for i, j := 0, len(highToLow)-1; i < j; i, j = i+1, j-1 {
		highToLow[i], highToLow[j] = highToLow[j], highToLow[i]
	}
	return highToLow
}

func applyRuntimeDefaults(servers map[string]ServerConfig) {
	if biome, ok := servers["biome"]; ok {
		biome.CreateClient = newBiomeClient
		servers["biome"] = biome
	}
	if sw, ok := servers["swiftlint"]; ok {
		sw.CreateClient = newSwiftLintClient
		servers["swiftlint"] = sw
	}
	if omni, ok := servers["omnisharp"]; ok && len(omni.Args) > 0 {
		pidStr := strconv.Itoa(os.Getpid())
		args := make([]string, len(omni.Args))
		for i, a := range omni.Args {
			if a == "$PID" {
				args[i] = pidStr
			} else {
				args[i] = a
			}
		}
		omni.Args = args
		servers["omnisharp"] = omni
	}
}

// =============================================================================
// Root Markers & Binary Resolution
// =============================================================================

func hasRootMarkers(dir string, markers []string) bool {
	var entries []os.DirEntry
	entriesLoaded := false
	for _, marker := range markers {
		if strings.Contains(marker, "*") {
			if !entriesLoaded {
				entries, _ = os.ReadDir(dir)
				entriesLoaded = true
			}
			for _, e := range entries {
				if matchGlob(marker, e.Name()) {
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

func hasRootMarkerAncestor(filePath string, markers []string) bool {
	if len(markers) == 0 {
		return false
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	dir := filepath.Dir(abs)
	for {
		if hasRootMarkers(dir, markers) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

var pythonRootMarkers = []string{
	"pyproject.toml", "ty.toml", "requirements.txt", "setup.py",
	"setup.cfg", "Pipfile", "pyrightconfig.json", "ruff.toml", ".ruff.toml",
}

type localBinEntry struct {
	markers []string
	binDir  string
}

var localBinPaths = []localBinEntry{
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

func resolveLocalCommand(basePath string) string {
	if info, err := os.Stat(basePath); err == nil && !info.IsDir() {
		return basePath
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		cand := basePath + ext
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	return ""
}

func resolveCommandFromLocalRoot(command, cwd string) string {
	for _, entry := range localBinPaths {
		if !hasRootMarkers(cwd, entry.markers) {
			continue
		}
		if resolved := resolveLocalCommand(filepath.Join(cwd, filepath.FromSlash(entry.binDir), command)); resolved != "" {
			return resolved
		}
	}
	return ""
}

// resolveCommand checks project-local bin directories first (only when trusted),
// then falls back to $PATH. Returns (resolvedPath, skippedLocalBin).
// upstream: packages/coding-agent/src/lsp/config.ts:resolveCommand (trust-gated for PiG)
var lookPathFunc = exec.LookPath

func resolveCommandWithTrust(command, cwd string, trusted bool, localRoots ...string) (string, bool) {
	if filepath.IsAbs(command) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() {
			return command, false
		}
		return "", false
	}
	roots := localRoots
	if len(roots) == 0 {
		roots = []string{cwd}
	}
	skippedLocal := false
	for _, root := range roots {
		if resolved := resolveCommandFromLocalRoot(command, root); resolved != "" {
			if trusted {
				return resolved, false
			}
			skippedLocal = true
			break
		}
	}
	if resolved, err := lookPathFunc(command); err == nil && resolved != "" {
		if abs, absErr := filepath.Abs(resolved); absErr == nil {
			return abs, skippedLocal
		}
		return resolved, skippedLocal
	}
	return "", skippedLocal
}

func resolveCommand(command, cwd string, localRoots ...string) string {
	p, _ := resolveCommandWithTrust(command, cwd, true, localRoots...)
	return p
}

// =============================================================================
// TypeScript 7 vs typescript-language-server selection
// upstream: packages/coding-agent/src/lsp/config.ts:selectTypescriptServer
// =============================================================================

var tscVersionRe = regexp.MustCompile(`^Version (\d+)\.`)

func typescriptPackageDir(tscPath string) string {
	realPath, err := filepath.EvalSymlinks(tscPath)
	if err != nil {
		realPath = tscPath
	}
	binDir := filepath.Dir(tscPath)
	var candidates []string
	if filepath.Base(filepath.Dir(realPath)) == "bin" {
		candidates = append(candidates, filepath.Dir(filepath.Dir(realPath)))
	}
	candidates = append(candidates,
		filepath.Join(binDir, "..", "typescript"),
		filepath.Join(binDir, "node_modules", "typescript"),
	)
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
	}
	return ""
}

func typescriptSpeaksLsp(tscPath, cwd string) bool {
	if pkgDir := typescriptPackageDir(tscPath); pkgDir != "" {
		_, err := os.Stat(filepath.Join(pkgDir, "lib", "tsserver.js"))
		return os.IsNotExist(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tscPath, "--version")
	cmd.Dir = cwd
	configureCmdCancel(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	m := tscVersionRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if len(m) < 2 {
		return false
	}
	major, convErr := strconv.Atoi(m[1])
	return convErr == nil && major >= 7
}

func selectTypescriptServer(servers map[string]ServerConfig, cwd string) {
	native, ok := servers["typescript-native"]
	if !ok || native.ResolvedCommand == "" {
		return
	}
	if typescriptSpeaksLsp(native.ResolvedCommand, cwd) {
		delete(servers, "typescript-language-server")
	} else {
		delete(servers, "typescript-native")
	}
}

// =============================================================================
// Config Loading & Caching (keyed by cwd + trust; locked for concurrent safety)
// =============================================================================

// LoadConfig loads LSP configuration assuming a trusted project (convenience wrapper).
func LoadConfig(cwd string) *LspConfig {
	return LoadConfigWithTrust(cwd, true)
}

// LoadConfigWithTrust loads LSP configuration from defaults.json + config files.
// When trusted is false, project-directory config files and project-local bin
// directories are skipped (PiG project-trust policy), and ProjectConfigSkipped
// is set if any project config file or project-local binary existed.
// upstream: packages/coding-agent/src/lsp/config.ts:loadConfig (trust-gated for PiG)
func LoadConfigWithTrust(cwd string, trusted bool) *LspConfig {
	merged, order := loadDefaultsOrdered()
	seenOrder := make(map[string]struct{}, len(order))
	for _, name := range order {
		seenOrder[name] = struct{}{}
	}

	var idleTimeoutMs *int
	projectConfigSkipped := false
	for _, src := range configFilePaths(cwd) {
		if src.isProject && !trusted {
			if _, statErr := os.Stat(src.path); statErr == nil {
				projectConfigSkipped = true
			}
			continue
		}
		parsed := parseConfigFile(src.path)
		if parsed == nil {
			continue
		}
		if parsed.idleTimeoutMs != nil {
			v := *parsed.idleTimeoutMs
			idleTimeoutMs = &v
		}
		for _, name := range parsed.order {
			ov, ok := parsed.servers[name]
			if !ok {
				continue
			}
			if base, exists := merged[name]; exists {
				if next, valid := mergeServerOverride(base, ov); valid {
					merged[name] = next
				}
			} else if cfg, valid := normalizeServerConfig(ov); valid {
				merged[name] = cfg
				if _, seen := seenOrder[name]; !seen {
					seenOrder[name] = struct{}{}
					order = append(order, name)
				}
			}
		}
	}

	applyRuntimeDefaults(merged)

	servers := make(map[string]ServerConfig)
	var serverOrder []string
	nestedMap := make(map[string]ServerConfig)
	var nestedOrder []string
	unresolved := make(map[string]ServerConfig)
	var unresolvedOrder []string

	for _, name := range order {
		cfg, ok := merged[name]
		if !ok || cfg.Disabled {
			continue
		}
		if !hasRootMarkers(cwd, cfg.RootMarkers) {
			if len(cfg.RootMarkers) > 0 {
				nestedMap[name] = cfg
				nestedOrder = append(nestedOrder, name)
			}
			continue
		}
		resolved, skippedLocal := resolveCommandWithTrust(cfg.Command, cwd, trusted)
		if skippedLocal {
			projectConfigSkipped = true
		}
		if resolved == "" {
			unresolved[name] = cfg
			unresolvedOrder = append(unresolvedOrder, name)
			continue
		}
		cfg.ResolvedCommand = resolved
		servers[name] = cfg
		serverOrder = append(serverOrder, name)
	}
	selectTypescriptServer(servers, cwd)

	filteredOrder := make([]string, 0, len(serverOrder))
	for _, name := range serverOrder {
		if _, ok := servers[name]; ok {
			filteredOrder = append(filteredOrder, name)
		}
	}

	return &LspConfig{
		Servers:              servers,
		ServerOrder:          filteredOrder,
		IdleTimeoutMs:        idleTimeoutMs,
		Nested:               &nestedServers{cwd: cwd, servers: nestedMap, order: nestedOrder},
		Unresolved:           unresolved,
		UnresolvedOrder:      unresolvedOrder,
		ProjectConfigSkipped: projectConfigSkipped,
		trusted:              trusted,
	}
}

type configCacheKey struct {
	cwd     string
	trusted bool
}

var (
	configCacheMu sync.Mutex
	configCache   = make(map[configCacheKey]*LspConfig)
)

// GetConfig returns the cached trusted LspConfig for cwd.
func GetConfig(cwd string) *LspConfig {
	return GetConfigWithTrust(cwd, true)
}

// GetConfigWithTrust returns the cached LspConfig for (cwd, trusted).
func GetConfigWithTrust(cwd string, trusted bool) *LspConfig {
	key := configCacheKey{cwd: filepath.Clean(cwd), trusted: trusted}
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	if cfg, ok := configCache[key]; ok {
		return cfg
	}
	cfg := LoadConfigWithTrust(cwd, trusted)
	configCache[key] = cfg
	return cfg
}

// InvalidateConfig drops cached configs for cwd (both trusted and untrusted).
func InvalidateConfig(cwd string) {
	clean := filepath.Clean(cwd)
	configCacheMu.Lock()
	delete(configCache, configCacheKey{cwd: clean, trusted: true})
	delete(configCache, configCacheKey{cwd: clean, trusted: false})
	configCacheMu.Unlock()
}

// snapshotAllServers returns a copy of all configured servers in order under configCacheMu.
func snapshotAllServers(cfg *LspConfig) []NamedServer {
	if cfg == nil {
		return nil
	}
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	order := cfg.ServerOrder
	if len(order) == 0 && len(cfg.Servers) > 0 {
		order = make([]string, 0, len(cfg.Servers))
		for k := range cfg.Servers {
			order = append(order, k)
		}
		sort.Strings(order)
	}
	out := make([]NamedServer, 0, len(order))
	for _, name := range order {
		if srvCfg, ok := cfg.Servers[name]; ok {
			out = append(out, NamedServer{Name: name, Config: srvCfg})
		}
	}
	return out
}

func configProjectSkipped(cfg *LspConfig) bool {
	if cfg == nil {
		return false
	}
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	return cfg.ProjectConfigSkipped
}

func configIdleTimeoutMs(cfg *LspConfig) int {
	if cfg == nil {
		return 0
	}
	configCacheMu.Lock()
	defer configCacheMu.Unlock()
	if cfg.IdleTimeoutMs == nil {
		return 0
	}
	return *cfg.IdleTimeoutMs
}

func serverHandlesFile(cfg ServerConfig, filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	extNoDot := strings.TrimPrefix(ext, ".")
	base := strings.ToLower(filepath.Base(filePath))
	for _, ft := range cfg.FileTypes {
		norm := strings.ToLower(ft)
		normNoDot := strings.TrimPrefix(norm, ".")
		if norm == ext || norm == base || (extNoDot != "" && normNoDot == extNoDot) || normNoDot == base {
			return true
		}
	}
	return false
}

func nestedRootMarkerDir(filePath, cwd string, markers []string) string {
	root, err := filepath.Abs(cwd)
	if err != nil {
		root = filepath.Clean(cwd)
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		absFile = filepath.Clean(filePath)
	}
	dir := filepath.Dir(absFile)
	prefix := root + string(filepath.Separator)
	for dir != root && strings.HasPrefix(dir, prefix) {
		if hasRootMarkers(dir, markers) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// activateNestedServersLocked must be called with configCacheMu held.
func activateNestedServersLocked(cfg *LspConfig, filePath string) {
	if cfg == nil || cfg.Nested == nil || len(cfg.Nested.servers) == 0 {
		return
	}
	activated := make(map[string]ServerConfig)
	var activatedOrder []string
	var projectDir string
	for _, name := range cfg.Nested.order {
		srvCfg, ok := cfg.Nested.servers[name]
		if !ok {
			continue
		}
		if !serverHandlesFile(srvCfg, filePath) {
			continue
		}
		markerDir := nestedRootMarkerDir(filePath, cfg.Nested.cwd, srvCfg.RootMarkers)
		if markerDir == "" {
			continue
		}
		delete(cfg.Nested.servers, name)
		resolved, skippedLocal := resolveCommandWithTrust(srvCfg.Command, cfg.Nested.cwd, cfg.trusted, markerDir, cfg.Nested.cwd)
		if skippedLocal {
			cfg.ProjectConfigSkipped = true
		}
		if resolved == "" {
			if cfg.Unresolved == nil {
				cfg.Unresolved = make(map[string]ServerConfig)
			}
			if _, exists := cfg.Unresolved[name]; !exists {
				cfg.UnresolvedOrder = append(cfg.UnresolvedOrder, name)
			}
			cfg.Unresolved[name] = srvCfg
			continue
		}
		srvCfg.ResolvedCommand = resolved
		activated[name] = srvCfg
		activatedOrder = append(activatedOrder, name)
		if projectDir == "" {
			projectDir = markerDir
		}
	}
	if projectDir == "" {
		return
	}
	selectTypescriptServer(activated, projectDir)
	for _, name := range activatedOrder {
		if srvCfg, ok := activated[name]; ok {
			cfg.Servers[name] = srvCfg
			cfg.ServerOrder = append(cfg.ServerOrder, name)
		}
	}
}

// GetServersForFile returns all servers matching filePath, sorted with primary
// (non-linter) servers before linters while preserving configuration order.
// upstream: packages/coding-agent/src/lsp/config.ts:getServersForFile
func GetServersForFile(cfg *LspConfig, filePath string) []NamedServer {
	if cfg == nil {
		return nil
	}
	configCacheMu.Lock()
	activateNestedServersLocked(cfg, filePath)
	order := cfg.ServerOrder
	if len(order) == 0 && len(cfg.Servers) > 0 {
		order = make([]string, 0, len(cfg.Servers))
		for k := range cfg.Servers {
			order = append(order, k)
		}
		sort.Strings(order)
	}
	var matches []NamedServer
	for _, name := range order {
		srvCfg, ok := cfg.Servers[name]
		if !ok {
			continue
		}
		if serverHandlesFile(srvCfg, filePath) {
			matches = append(matches, NamedServer{Name: name, Config: srvCfg})
		}
	}
	configCacheMu.Unlock()

	sort.SliceStable(matches, func(i, j int) bool {
		aLinter := matches[i].Config.IsLinter
		bLinter := matches[j].Config.IsLinter
		if aLinter == bLinter {
			return false
		}
		return !aLinter && bLinter
	})
	return matches
}

// describeMissingServers explains why no server handles filePath, preserving
// configuration order (matching lsp/config.ts:591-606).
func describeMissingServers(cfg *LspConfig, filePath string) string {
	ext := filepath.Ext(filePath)
	if ext == "" {
		ext = filepath.Base(filePath)
	}
	var reasons []string
	if cfg != nil {
		configCacheMu.Lock()
		unresolvedOrder := cfg.UnresolvedOrder
		if len(unresolvedOrder) == 0 && len(cfg.Unresolved) > 0 {
			for name := range cfg.Unresolved {
				unresolvedOrder = append(unresolvedOrder, name)
			}
			sort.Strings(unresolvedOrder)
		}
		for _, name := range unresolvedOrder {
			srvCfg, ok := cfg.Unresolved[name]
			if !ok {
				continue
			}
			if serverHandlesFile(srvCfg, filePath) {
				reasons = append(reasons, fmt.Sprintf("%s: `%s` not found on PATH", name, srvCfg.Command))
			}
		}
		if cfg.Nested != nil {
			for _, name := range cfg.Nested.order {
				srvCfg, ok := cfg.Nested.servers[name]
				if !ok {
					continue
				}
				if serverHandlesFile(srvCfg, filePath) {
					reasons = append(reasons, fmt.Sprintf("%s: no %s in the file's directory or its parents",
						name, strings.Join(srvCfg.RootMarkers, ", ")))
				}
			}
		}
		configCacheMu.Unlock()
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("No language server handles %s files", ext)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("No language server for %s:", ext))
	for _, r := range reasons {
		lines = append(lines, "  "+r)
	}
	return strings.Join(lines, "\n")
}

// =============================================================================
// YAML-to-JSON parser for lsp.yaml / .lsp.yml (stdlib only)
// Supports block mappings, indented and flush block sequences (`- item`),
// string/bool/number/null scalars, and inline JSON arrays/objects.
// =============================================================================

type yamlLine struct {
	indent int
	text   string
}

func parseSimpleYAMLToJSON(src string) ([]byte, bool) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return nil, false
	}
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), true
	}

	var lines []yamlLine
	for _, rawLine := range strings.Split(src, "\n") {
		clean := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(clean) == "" || strings.HasPrefix(strings.TrimSpace(clean), "#") {
			continue
		}
		indent := 0
		for indent < len(clean) && clean[indent] == ' ' {
			indent++
		}
		lines = append(lines, yamlLine{indent: indent, text: strings.TrimSpace(clean[indent:])})
	}
	if len(lines) == 0 {
		return nil, false
	}
	val, _ := parseYAMLBlock(lines, 0, lines[0].indent)
	data, err := json.Marshal(val)
	if err != nil {
		return nil, false
	}
	return data, true
}

func isYAMLSeqLine(s string) bool {
	return s == "-" || strings.HasPrefix(s, "- ")
}

func parseYAMLBlock(lines []yamlLine, start, minIndent int) (any, int) {
	if start >= len(lines) {
		return map[string]any{}, start
	}
	if isYAMLSeqLine(lines[start].text) {
		return parseYAMLSequence(lines, start, lines[start].indent)
	}
	return parseYAMLMapping(lines, start, lines[start].indent)
}

func parseYAMLSequence(lines []yamlLine, start, seqIndent int) ([]any, int) {
	var list []any
	i := start
	for i < len(lines) {
		ln := lines[i]
		if ln.indent < seqIndent || (ln.indent == seqIndent && !isYAMLSeqLine(ln.text)) {
			break
		}
		if ln.indent != seqIndent {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
		i++
		if rest == "" {
			if i < len(lines) && lines[i].indent > seqIndent {
				var child any
				child, i = parseYAMLBlock(lines, i, lines[i].indent)
				list = append(list, child)
			} else {
				list = append(list, nil)
			}
			continue
		}
		list = append(list, parseYAMLScalar(stripYAMLInlineComment(rest)))
	}
	return list, i
}

func parseYAMLMapping(lines []yamlLine, start, mapIndent int) (map[string]any, int) {
	obj := make(map[string]any)
	i := start
	for i < len(lines) {
		ln := lines[i]
		if ln.indent < mapIndent || (ln.indent == mapIndent && isYAMLSeqLine(ln.text)) {
			break
		}
		if ln.indent > mapIndent {
			// Skip stray over-indented line safely.
			i++
			continue
		}
		colon := findYAMLKeyColon(ln.text)
		if colon == -1 {
			i++
			continue
		}
		key := strings.Trim(strings.TrimSpace(ln.text[:colon]), `"'`)
		valStr := strings.TrimSpace(ln.text[colon+1:])
		i++
		if valStr == "" || strings.HasPrefix(valStr, "#") {
			if i < len(lines) && (lines[i].indent > mapIndent || (lines[i].indent == mapIndent && isYAMLSeqLine(lines[i].text))) {
				var child any
				child, i = parseYAMLBlock(lines, i, lines[i].indent)
				obj[key] = child
			} else {
				obj[key] = map[string]any{}
			}
		} else {
			obj[key] = parseYAMLScalar(stripYAMLInlineComment(valStr))
		}
	}
	return obj, i
}

func findYAMLKeyColon(s string) int {
	inQuote := byte(0)
	for i := range len(s) {
		ch := s[i]
		if inQuote != 0 {
			if ch == inQuote && (i == 0 || s[i-1] != '\\') {
				inQuote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inQuote = ch
			continue
		}
		if ch == ':' && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
			return i
		}
	}
	return -1
}

func stripYAMLInlineComment(s string) string {
	inQuote := byte(0)
	bracketDepth := 0
	for i := range len(s) {
		ch := s[i]
		if inQuote != 0 {
			if ch == inQuote && (i == 0 || s[i-1] != '\\') {
				inQuote = 0
			}
			continue
		}
		switch ch {
		case '"', '\'':
			inQuote = ch
		case '[', '{':
			bracketDepth++
		case ']', '}':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '#':
			if bracketDepth == 0 && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

func parseYAMLScalar(s string) any {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if s == "null" || s == "~" {
		return nil
	}
	if (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) ||
		(strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err == nil {
			return v
		}
	}
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
		(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		if len(s) >= 2 {
			return s[1 : len(s)-1]
		}
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}
