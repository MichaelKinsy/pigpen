package pi_subagents

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Ports of src/runs/shared/mcp-direct-tool-allowlist.ts (which tools the agent's `mcp:` selectors grant, read from the MCP
// adapter's configuration and its metadata cache) and src/runs/shared/mcp-config-sources.ts (servers of packages and of
// agent plugins). A server definition stays the decoded object (*jsObject), as the original keeps it, so the config hash reads
// exactly the fields the adapter writes it from.
//
// Not ported: the runtime snapshot of pi-mcp-adapter (`pi-mcp-adapter:runtime-snapshot:v1`), which the original reads by
// emitting a request object on the in-process event bus and reading the `result` the adapter's listener sets on it. The SDK's
// event bus passes payloads by value, so a listener cannot answer; a selector of a server that exists only in that snapshot
// stays unresolved (the preflight reports it, as the original does without a snapshot host).

const mcpCacheMaxAgeMs = 7 * 24 * 60 * 60 * 1000

var mcpImportKinds = []string{"cursor", "claude-code", "claude-desktop", "codex", "windsurf", "vscode"}

func mcpImportPaths(kind string) []string {
	h := homeDir()
	switch kind {
	case "cursor":
		return []string{filepath.Join(h, ".cursor", "mcp.json")}
	case "claude-code":
		return []string{filepath.Join(h, ".claude", "mcp.json"), filepath.Join(h, ".claude.json"), filepath.Join(h, ".claude", "claude_desktop_config.json")}
	case "claude-desktop":
		return []string{filepath.Join(h, "Library", "Application Support", "Claude", "claude_desktop_config.json")}
	case "codex":
		return []string{filepath.Join(h, ".codex", "config.json")}
	case "windsurf":
		return []string{filepath.Join(h, ".windsurf", "mcp.json")}
	case "vscode":
		return []string{".vscode/mcp.json"}
	}
	return nil
}

func isMcpImportKind(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, k := range mcpImportKinds {
		if k == s {
			return true
		}
	}
	return false
}

func isStringList(v any) bool {
	a, ok := asArray(v)
	if !ok {
		return false
	}
	for _, e := range a {
		if _, ok := e.(string); !ok {
			return false
		}
	}
	return true
}

func isStringRecord(v any) bool {
	o, ok := asObject(v)
	if !ok {
		return false
	}
	for _, k := range o.keys {
		if _, ok := o.vals[k].(string); !ok {
			return false
		}
	}
	return true
}

func isRequestHeadersCommand(v any) bool {
	o, ok := asObject(v)
	if !ok {
		return false
	}
	if _, ok := o.vals["command"].(string); !ok {
		return false
	}
	if x, has := o.vals["args"]; has && !isUndefined(x) && !isStringList(x) {
		return false
	}
	if x, has := o.vals["env"]; has && !isUndefined(x) && !isStringRecord(x) {
		return false
	}
	if x, has := o.vals["timeoutMs"]; has && !isUndefined(x) {
		f, isNum := asNumber(x)
		return isNum && !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	return true
}

// isMcpServerDefinition is mcp-config-sources.ts:isMcpServerDefinition.
func isMcpServerDefinition(v any) bool {
	o, ok := asObject(v)
	if !ok {
		return false
	}
	present := func(k string) (any, bool) {
		x, has := o.vals[k]
		return x, has && !isUndefined(x)
	}
	for _, f := range []string{"command", "socket", "cwd", "url", "bearerToken", "bearerTokenEnv", "protocolVersion", "httpTransport", "pluginDataDir"} {
		if x, has := present(f); has {
			if _, isStr := x.(string); !isStr {
				return false
			}
		}
	}
	for _, f := range []string{"args", "includeTools", "excludeTools"} {
		if x, has := present(f); has && !isStringList(x) {
			return false
		}
	}
	for _, f := range []string{"env", "headers"} {
		if x, has := present(f); has && !isStringRecord(x) {
			return false
		}
	}
	for _, f := range []string{"exposeResources", "literalEnv"} {
		if x, has := present(f); has {
			if _, isBool := x.(bool); !isBool {
				return false
			}
		}
	}
	if x, has := present("requestHeadersCommand"); has && !isRequestHeadersCommand(x) {
		return false
	}
	if x, has := present("auth"); has {
		if s, isStr := x.(string); !(isStr && (s == "oauth" || s == "bearer")) && x != false {
			return false
		}
	}
	x, has := present("directTools")
	if !has {
		return true
	}
	if _, isBool := x.(bool); isBool {
		return true
	}
	return isStringList(x)
}

// readJSONStrict is mcp-config-sources.ts readJson: nil for a missing file, an error for an unreadable or malformed one.
func readJSONStrict(filePath string) (any, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read JSON file '%s': %s", filePath, err.Error())
	}
	v, msg := jsJSONParse(string(b))
	if msg != "" {
		return nil, fmt.Errorf("Invalid JSON in '%s': %s", filePath, msg)
	}
	return v, nil
}

// resolveContainedPath is path.resolve(root, value) when it stays inside root ("" otherwise).
func resolveContainedPath(root, value string) string {
	resolved := nodeResolve(root, value)
	rel, err := filepath.Rel(nodeResolve(root), resolved)
	if err != nil {
		return ""
	}
	if rel == "." || rel == "" || (!strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, string(filepath.Separator)) && !filepath.IsAbs(rel)) {
		return resolved
	}
	return ""
}

var (
	mcpFormatNameRe  = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
	mcpEdgeSepRe     = regexp.MustCompile(`^[_-]+|[_-]+$`)
	mcpNpmSpecRe     = regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@(.+))?$`)
	mcpHTTPLikeRe    = regexp.MustCompile(`^(?:https?://|ssh://|git@[^:]+:)`)
	mcpScpLikeRe     = regexp.MustCompile(`^git@([^:]+):(.+)$`)
	mcpSchemeURLRe   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	mcpPluginNameRe  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	mcpLoopbackRe    = regexp.MustCompile(`^127(?:\.\d{1,3}){3}$`)
	mcpHeaderNameRe  = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
	mcpGitPartSplit  = regexp.MustCompile(`[\\/]`)
	mcpEnvVarRe1     = regexp.MustCompile(`\$\{(\w+)\}`)
	mcpEnvVarRe2     = regexp.MustCompile(`\$env:(\w+)`)
	mcpEnvVarRe3     = regexp.MustCompile(`\{env:(\w+)\}`)
	mcpEnvVarMissing = regexp.MustCompile(`\$\{(\w+)\}|\$env:(\w+)|\{env:(\w+)\}`)
)

func mcpFormatName(value, fallback string) string {
	s := mcpFormatNameRe.ReplaceAllString(value, "_")
	s = mcpEdgeSepRe.ReplaceAllString(s, "")
	if s == "" {
		return fallback
	}
	return s
}

func readMcpManifest(p string) (*jsObject, error) {
	v, err := readJSONStrict(p)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsObject)
	return o, nil
}

func findMcpProjectConfigDir(cwd string) string {
	abs := nodeResolve(cwd)
	if root := FindConfiguredProjectRoot(abs); root != "" {
		return getProjectConfigDir(root)
	}
	return filepath.Join(abs, getConfigDirName())
}

func parseMcpNpmPackageName(source string) string {
	spec := jsTrim(source[4:])
	if spec == "" {
		return ""
	}
	name := spec
	if m := mcpNpmSpecRe.FindStringSubmatch(spec); m != nil && m[1] != "" {
		name = m[1]
	}
	if resolveContainedPath(string(filepath.Separator), name) == "" {
		return ""
	}
	return name
}

func isSafeMcpPackagePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range mcpGitPartSplit.Split(value, -1) {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func stripMcpGitRef(repoPath string) string {
	at, hash := strings.Index(repoPath, "@"), strings.Index(repoPath, "#")
	ref := -1
	for _, i := range []int{at, hash} {
		if i >= 0 && (ref < 0 || i < ref) {
			ref = i
		}
	}
	if ref < 0 {
		return repoPath
	}
	return repoPath[:ref]
}

func parseMcpGitPackagePath(source string) (host, repoPath string, ok bool) {
	spec := jsTrim(source[4:])
	if spec == "" {
		return "", "", false
	}
	if m := mcpScpLikeRe.FindStringSubmatch(spec); m != nil {
		host, repoPath = m[1], m[2]
	} else if mcpSchemeURLRe.MatchString(spec) {
		u, err := url.Parse(spec)
		if err != nil {
			return "", "", false
		}
		host, repoPath = strings.ToLower(u.Hostname()), strings.TrimLeft(u.EscapedPath(), "/")
	} else {
		slash := strings.Index(spec, "/")
		if slash < 0 {
			return "", "", false
		}
		host, repoPath = spec[:slash], spec[slash+1:]
	}
	normalized := strings.TrimLeft(strings.TrimSuffix(stripMcpGitRef(repoPath), ".git"), "/")
	if host == "" || !isSafeMcpPackagePath(host) || !isSafeMcpPackagePath(normalized) || len(mcpGitPartSplit.Split(normalized, -1)) < 2 {
		return "", "", false
	}
	return host, normalized, true
}

func resolveMcpPackageRoot(source, baseDir string) string {
	trimmed := jsTrim(source)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "npm:") {
		name := parseMcpNpmPackageName(trimmed)
		if name == "" {
			return ""
		}
		return resolveContainedPath(filepath.Join(baseDir, "npm", "node_modules"), name)
	}
	if strings.HasPrefix(trimmed, "git:") || mcpHTTPLikeRe.MatchString(trimmed) {
		spec := trimmed
		if !strings.HasPrefix(trimmed, "git:") {
			spec = "git:" + trimmed
		}
		host, repo, ok := parseMcpGitPackagePath(spec)
		if !ok {
			return ""
		}
		return filepath.Join(baseDir, "git", host, repo)
	}
	local := strings.TrimPrefix(trimmed, "file:")
	if local == "~" {
		return homeDir()
	}
	if strings.HasPrefix(local, "~/") {
		return filepath.Join(homeDir(), local[2:])
	}
	if filepath.IsAbs(local) {
		return nodeResolve(local)
	}
	return nodeResolve(baseDir, local)
}

func configuredMcpPackageRoots(cwd string) ([]string, error) {
	roots := []string{}
	projectConfigDir := findMcpProjectConfigDir(cwd)
	for _, src := range []struct{ path, baseDir string }{
		{filepath.Join(projectConfigDir, "settings.json"), projectConfigDir},
		{filepath.Join(agentDir(), "settings.json"), agentDir()},
	} {
		v, err := readJSONStrict(src.path)
		if err != nil {
			return nil, err
		}
		settings, ok := v.(*jsObject)
		if !ok {
			continue
		}
		packages, ok := asArray(settings.vals["packages"])
		if !ok {
			continue
		}
		for _, entry := range packages {
			source := ""
			if s, isStr := entry.(string); isStr {
				source = s
			} else if o, isObj := asObject(entry); isObj {
				source, _ = o.vals["source"].(string)
			}
			if source == "" {
				continue
			}
			if root := resolveMcpPackageRoot(source, src.baseDir); root != "" && !contains(roots, root) {
				roots = append(roots, root)
			}
		}
	}
	return roots, nil
}

func resolveMcpPackageConfigPath(packageRoot, configured string) string {
	lexical := resolveContainedPath(packageRoot, configured)
	if lexical == "" {
		return ""
	}
	st, err := os.Stat(lexical)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	pr, err1 := filepath.EvalSymlinks(packageRoot)
	cr, err2 := filepath.EvalSymlinks(lexical)
	if err1 != nil || err2 != nil {
		return ""
	}
	return resolveContainedPath(pr, cr)
}

// loadPackageMcpServers reads the servers the configured packages declare under `pi.mcp`.
func loadPackageMcpServers(cwd string) (*jsObject, error) {
	servers := newObject()
	seen := map[string]bool{}
	roots, err := configuredMcpPackageRoots(cwd)
	if err != nil {
		return nil, err
	}
	for _, packageRoot := range roots {
		manifest, err := readMcpManifest(filepath.Join(packageRoot, "package.json"))
		if err != nil {
			return nil, err
		}
		if manifest == nil {
			continue
		}
		packageName, _ := manifest.vals["name"].(string)
		if packageName == "" {
			continue
		}
		var configPaths []string
		if pi := manifest.obj("pi"); pi != nil {
			switch m := pi.vals["mcp"].(type) {
			case string:
				configPaths = []string{m}
			case []any:
				if isStringList(m) {
					for _, e := range m {
						configPaths = append(configPaths, e.(string))
					}
				}
			}
		}
		prefix := mcpFormatName(packageName, "package")
		for _, configPath := range configPaths {
			resolved := resolveMcpPackageConfigPath(packageRoot, configPath)
			if resolved == "" {
				continue
			}
			config, err := readMcpManifest(resolved)
			if err != nil {
				return nil, err
			}
			if config == nil {
				continue
			}
			raw, ok := config.vals["mcpServers"].(*jsObject)
			if !ok {
				continue
			}
			for _, name := range raw.keys {
				def := raw.vals[name]
				if !isMcpServerDefinition(def) {
					continue
				}
				normalized := prefix + "__" + mcpFormatName(name, "server")
				if seen[normalized] {
					continue
				}
				seen[normalized] = true
				servers.set(normalized, def)
			}
		}
	}
	return servers, nil
}

const (
	mcpPluginSchema    = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	mcpPluginMcpSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
)

func resolveMcpPluginPath(configured, cwd string) string {
	home := os.Getenv("HOME")
	if configured == "~" {
		return nodeResolve(home, ".")
	}
	if strings.HasPrefix(configured, "~/") {
		return nodeResolve(home, configured[2:])
	}
	if filepath.IsAbs(configured) {
		return nodeResolve(configured)
	}
	return nodeResolve(cwd, configured)
}

func isValidMcpPluginManifest(v any) (string, bool) {
	o, ok := v.(*jsObject)
	if !ok {
		return "", false
	}
	name, isStr := o.vals["name"].(string)
	if s, _ := o.vals["$schema"].(string); s != mcpPluginSchema || !isStr {
		return "", false
	}
	n := jsLen(name)
	if n < 1 || n > 64 || strings.Contains(name, "--") || strings.Contains(name, "..") || !mcpPluginNameRe.MatchString(name) {
		return "", false
	}
	return name, true
}

func isValidMcpPluginConfig(v any) (*jsObject, bool) {
	o, ok := v.(*jsObject)
	if !ok {
		return nil, false
	}
	if s, _ := o.vals["$schema"].(string); s != mcpPluginMcpSchema {
		return nil, false
	}
	for _, k := range o.keys {
		if k != "$schema" && k != "mcpServers" {
			return nil, false
		}
	}
	servers, ok := o.vals["mcpServers"].(*jsObject)
	return servers, ok
}

func stringListOf(v any) ([]string, bool) {
	a, ok := asArray(v)
	if !ok {
		return nil, false
	}
	out := make([]string, len(a))
	for i, e := range a {
		s, isStr := e.(string)
		if !isStr {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func isBareMcpCommand(c string) bool {
	return !strings.Contains(c, "/") && !strings.Contains(c, `\`) && !strings.Contains(c, "${PLUGIN_ROOT}") && !strings.Contains(c, "${PLUGIN_DATA}")
}

func expandMcpPluginPlaceholders(v, pluginRoot, dataDir string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "${PLUGIN_ROOT}", pluginRoot), "${PLUGIN_DATA}", dataDir)
}

func resolveMcpPluginCwd(v any, pluginRoot, dataDir string) string {
	if isUndefined(v) {
		return pluginRoot
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "./"):
		return resolveContainedPath(pluginRoot, s)
	case s == "${PLUGIN_ROOT}" || strings.HasPrefix(s, "${PLUGIN_ROOT}/"):
		return resolveContainedPath(pluginRoot, strings.Replace(s, "${PLUGIN_ROOT}", ".", 1))
	case s == "${PLUGIN_DATA}" || strings.HasPrefix(s, "${PLUGIN_DATA}/"):
		return resolveContainedPath(dataDir, strings.Replace(s, "${PLUGIN_DATA}", ".", 1))
	}
	return ""
}

func translateMcpPluginStdio(pluginName, pluginRoot string, raw *jsObject) any {
	for _, k := range raw.keys {
		if k != "type" && k != "command" && k != "args" && k != "env" && k != "cwd" {
			return nil
		}
	}
	command, _ := raw.vals["command"].(string)
	if command == "" || !isBareMcpCommand(command) && !strings.HasPrefix(command, "./") {
		return nil
	}
	var args []string
	if v, has := raw.vals["args"]; has && !isUndefined(v) {
		var ok bool
		if args, ok = stringListOf(v); !ok {
			return nil
		}
	}
	var env *jsObject
	if v, has := raw.vals["env"]; has && !isUndefined(v) {
		o, ok := asObject(v)
		if !ok || !isStringRecord(o) {
			return nil
		}
		env = o
		if _, bad := o.vals["PLUGIN_ROOT"]; bad {
			return nil
		}
		if _, bad := o.vals["PLUGIN_DATA"]; bad {
			return nil
		}
	}
	dataDir := filepath.Join(agentDir(), "agent-plugin-data", pluginName)
	if strings.HasPrefix(command, "./") {
		if command = resolveContainedPath(pluginRoot, command); command == "" {
			return nil
		}
	}
	cwd := resolveMcpPluginCwd(raw.vals["cwd"], pluginRoot, dataDir)
	if cwd == "" {
		return nil
	}
	outArgs := []any{}
	for _, a := range args {
		outArgs = append(outArgs, expandMcpPluginPlaceholders(a, pluginRoot, dataDir))
	}
	outEnv := newObject()
	if env != nil {
		for _, k := range env.keys {
			outEnv.set(k, expandMcpPluginPlaceholders(env.vals[k].(string), pluginRoot, dataDir))
		}
	}
	outEnv.set("PLUGIN_ROOT", pluginRoot)
	outEnv.set("PLUGIN_DATA", dataDir)
	def := newObject()
	def.set("command", command)
	def.set("args", outArgs)
	def.set("env", outEnv)
	def.set("cwd", cwd)
	def.set("pluginDataDir", dataDir)
	def.set("literalEnv", true)
	return def
}

func isValidMcpPluginURL(value string) bool {
	if strings.Contains(value, "${") || strings.Contains(value, "$env:") || strings.Contains(value, "{env:") {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]" || mcpLoopbackRe.MatchString(host)
}

func translateMcpPluginHTTP(raw *jsObject) any {
	for _, k := range raw.keys {
		if k != "type" && k != "url" && k != "headers" {
			return nil
		}
	}
	u, ok := raw.vals["url"].(string)
	if !ok || !isValidMcpPluginURL(u) {
		return nil
	}
	def := newObject()
	def.set("url", u)
	if v, has := raw.vals["headers"]; has && !isUndefined(v) {
		h, ok := asObject(v)
		if !ok || !isStringRecord(h) {
			return nil
		}
		lower := map[string]bool{}
		for _, k := range h.keys {
			l := strings.ToLower(k)
			if lower[l] {
				return nil
			}
			lower[l] = true
			val := h.vals[k].(string)
			if !mcpHeaderNameRe.MatchString(k) || strings.ContainsAny(val, "\r\n\x00") {
				return nil
			}
		}
		def.set("headers", h)
	}
	def.set("httpTransport", raw.vals["type"])
	return def
}

func translateMcpPluginServer(pluginName, pluginRoot string, value any) any {
	raw, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	switch raw.vals["type"] {
	case "stdio":
		return translateMcpPluginStdio(pluginName, pluginRoot, raw)
	case "streamable-http", "sse":
		return translateMcpPluginHTTP(raw)
	}
	return nil
}

// loadAgentPluginMcpServers reads the servers of the agent plugins named by `settings.agentPluginPaths`.
func loadAgentPluginMcpServers(paths any, cwd string) (*jsObject, error) {
	servers := newObject()
	list, ok := asArray(paths)
	if !ok {
		return servers, nil
	}
	for _, p := range list {
		configured, ok := p.(string)
		if !ok {
			continue
		}
		root := resolveMcpPluginPath(configured, cwd)
		manifestRaw, err := readJSONStrict(filepath.Join(root, "plugin.json"))
		if err != nil {
			return nil, err
		}
		name, ok := isValidMcpPluginManifest(manifestRaw)
		if !ok {
			continue
		}
		configRaw, err := readJSONStrict(filepath.Join(root, "mcp.json"))
		if err != nil {
			return nil, err
		}
		raw, ok := isValidMcpPluginConfig(configRaw)
		if !ok {
			continue
		}
		for _, serverName := range raw.keys {
			def := translateMcpPluginServer(name, root, raw.vals[serverName])
			if def == nil {
				continue
			}
			normalized := mcpFormatName(name, "plugin") + "__" + mcpFormatName(serverName, "server")
			if _, has := servers.vals[normalized]; has {
				continue
			}
			servers.set(normalized, def)
		}
	}
	return servers, nil
}

// ---- mcp-direct-tool-allowlist.ts ------------------------------------------------------------------------------------------

// mcpConfig is McpConfig: the servers, the import kinds and the settings the adapter reads.
type mcpConfig struct {
	Servers  *jsObject
	Imports  []string
	Settings *jsObject // nil: unset
}

func parseMcpServerEntries(v any) *jsObject {
	out := newObject()
	o, ok := asObject(v)
	if !ok {
		return out
	}
	for _, k := range o.keys {
		if isMcpServerDefinition(o.vals[k]) {
			out.set(k, o.vals[k])
		}
	}
	return out
}

func parseMcpSettings(v any) *jsObject {
	o, ok := asObject(v)
	if !ok {
		return nil
	}
	out := newObject()
	if s, isStr := o.vals["toolPrefix"].(string); isStr && (s == "server" || s == "short" || s == "none" || s == "mcp") {
		out.set("toolPrefix", s)
	}
	if b, isBool := o.vals["directTools"].(bool); isBool {
		out.set("directTools", b)
	}
	if x, has := o.vals["agentPluginPaths"]; has && !isUndefined(x) {
		out.set("agentPluginPaths", x)
	}
	if len(out.keys) == 0 {
		return nil
	}
	return out
}

func validateMcpConfig(raw any) mcpConfig {
	o, ok := asObject(raw)
	if !ok {
		return mcpConfig{Servers: newObject()}
	}
	servers := o.vals["mcpServers"]
	if servers == nil || isUndefined(servers) {
		servers = o.vals["mcp-servers"]
	}
	cfg := mcpConfig{Servers: parseMcpServerEntries(servers), Settings: parseMcpSettings(o.vals["settings"])}
	if arr, isArr := asArray(o.vals["imports"]); isArr {
		for _, e := range arr {
			if isMcpImportKind(e) {
				cfg.Imports = append(cfg.Imports, e.(string))
			}
		}
	}
	return cfg
}

func mergeMcpConfigs(base, next mcpConfig) mcpConfig {
	servers := base.Servers.clone()
	for _, k := range next.Servers.keys {
		servers.set(k, next.Servers.vals[k])
	}
	var imports []string
	for _, i := range append(append([]string{}, base.Imports...), next.Imports...) {
		if !contains(imports, i) {
			imports = append(imports, i)
		}
	}
	settings := base.Settings
	if next.Settings != nil {
		merged := newObject()
		if base.Settings != nil {
			for _, k := range base.Settings.keys {
				merged.set(k, base.Settings.vals[k])
			}
		}
		for _, k := range next.Settings.keys {
			merged.set(k, next.Settings.vals[k])
		}
		settings = merged
	}
	return mcpConfig{Servers: servers, Imports: imports, Settings: settings}
}

func readMcpConfigFile(p string) (mcpConfig, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return mcpConfig{}, false
	}
	v, msg := jsJSONParse(string(b))
	if msg != "" {
		return mcpConfig{}, false
	}
	return validateMcpConfig(v), true
}

func resolveMcpImportPath(kind, cwd string) string {
	for _, c := range mcpImportPaths(kind) {
		full := c
		if strings.HasPrefix(c, ".") {
			full = nodeResolve(cwd, c)
		}
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	return ""
}

func expandMcpImports(cfg mcpConfig, cwd string) mcpConfig {
	if len(cfg.Imports) == 0 {
		return cfg
	}
	imported := newObject()
	for _, kind := range cfg.Imports {
		p := resolveMcpImportPath(kind, cwd)
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, msg := jsJSONParse(string(b))
		if msg != "" {
			continue
		}
		o, ok := asObject(v)
		if !ok {
			continue
		}
		servers := o.vals["mcpServers"]
		if kind == "cursor" || kind == "windsurf" || kind == "vscode" {
			if servers == nil || isUndefined(servers) {
				servers = o.vals["mcp-servers"]
			}
		}
		entries := parseMcpServerEntries(servers)
		for _, name := range entries.keys {
			if _, has := imported.vals[name]; !has {
				imported.set(name, entries.vals[name])
			}
		}
	}
	servers := imported.clone()
	for _, k := range cfg.Servers.keys {
		servers.set(k, cfg.Servers.vals[k])
	}
	return mcpConfig{Imports: cfg.Imports, Settings: cfg.Settings, Servers: servers}
}

// loadMcpConfig reads the adapter's configuration files, packages and agent plugins for a working directory.
func loadMcpConfig(cwd string) (mcpConfig, error) {
	resolved := nodeResolve(cwd)
	projectRoot := FindConfiguredProjectRoot(resolved)
	if projectRoot == "" {
		projectRoot = resolved
	}
	cfg := mcpConfig{Servers: newObject()}
	var paths []string
	for _, p := range []string{
		filepath.Join(homeDir(), ".config", "mcp", "mcp.json"),
		filepath.Join(agentDir(), "mcp-adapter.json"),
		nodeResolve(projectRoot, ".mcp.json"),
		filepath.Join(getProjectConfigDir(projectRoot), "mcp-adapter.json"),
	} {
		if !contains(paths, p) {
			paths = append(paths, p)
		}
	}
	for _, p := range paths {
		if loaded, ok := readMcpConfigFile(p); ok {
			cfg = mergeMcpConfigs(cfg, expandMcpImports(loaded, projectRoot))
		}
	}
	packageServers, err := loadPackageMcpServers(projectRoot)
	if err != nil {
		return mcpConfig{}, err
	}
	var pluginPaths any
	if cfg.Settings != nil {
		pluginPaths = cfg.Settings.vals["agentPluginPaths"]
	}
	pluginServers, err := loadAgentPluginMcpServers(pluginPaths, projectRoot)
	if err != nil {
		return mcpConfig{}, err
	}
	packageOnly := newObject()
	for _, k := range packageServers.keys {
		if _, has := pluginServers.vals[k]; !has {
			packageOnly.set(k, packageServers.vals[k])
		}
	}
	return mergeMcpConfigs(mcpConfig{Servers: packageOnly}, mergeMcpConfigs(mcpConfig{Servers: pluginServers}, cfg)), nil
}

// mcpCacheEntry is a validated entry of the adapter's metadata cache.
type mcpCacheEntry struct {
	ConfigHash *string
	CachedAt   *float64
	meta       mcpGrantMetadata
}

func loadMcpMetadataCache() map[string]mcpCacheEntry {
	b, err := os.ReadFile(filepath.Join(agentDir(), "mcp-cache.json"))
	if err != nil {
		return nil
	}
	v, msg := jsJSONParse(string(b))
	if msg != "" {
		return nil
	}
	o, ok := asObject(v)
	if !ok {
		return nil
	}
	if ver, isNum := o.vals["version"].(float64); !isNum || ver != 1 {
		return nil
	}
	servers, ok := asObject(o.vals["servers"])
	if !ok {
		return nil
	}
	out := map[string]mcpCacheEntry{}
	for _, name := range servers.keys {
		if e, ok := parseMcpCacheEntry(servers.vals[name]); ok {
			out[name] = e
		}
	}
	return out
}

func parseMcpCacheEntry(v any) (mcpCacheEntry, bool) {
	o, ok := asObject(v)
	if !ok {
		return mcpCacheEntry{}, false
	}
	e := mcpCacheEntry{}
	if x, has := o.vals["configHash"]; has && !isUndefined(x) {
		s, isStr := x.(string)
		if !isStr {
			return e, false
		}
		e.ConfigHash = &s
	}
	if x, has := o.vals["cachedAt"]; has && !isUndefined(x) {
		f, isNum := x.(float64)
		if !isNum || math.IsInf(f, 0) || math.IsNaN(f) {
			return e, false
		}
		e.CachedAt = &f
	}
	if x, has := o.vals["tools"]; has && !isUndefined(x) {
		arr, isArr := asArray(x)
		if !isArr {
			return e, false
		}
		e.meta.Tools = []mcpGrantTool{}
		for _, t := range arr {
			to, ok := asObject(t)
			if !ok {
				return e, false
			}
			tool := mcpGrantTool{}
			if n, has := to.vals["name"]; has && !isUndefined(n) {
				s, isStr := n.(string)
				if !isStr {
					return e, false
				}
				tool.Name = &s
			}
			e.meta.Tools = append(e.meta.Tools, tool)
		}
	}
	if x, has := o.vals["resources"]; has && !isUndefined(x) {
		arr, isArr := asArray(x)
		if !isArr {
			return e, false
		}
		e.meta.Resources = []mcpGrantResource{}
		for _, r := range arr {
			ro, ok := asObject(r)
			if !ok {
				return e, false
			}
			res := mcpGrantResource{}
			for _, f := range []struct {
				key string
				dst **string
			}{{"uri", &res.URI}, {"name", &res.Name}} {
				if n, has := ro.vals[f.key]; has && !isUndefined(n) {
					s, isStr := n.(string)
					if !isStr {
						return e, false
					}
					*f.dst = &s
				}
			}
			e.meta.Resources = append(e.meta.Resources, res)
		}
	}
	return e, true
}

func isMcpServerCacheValid(e mcpCacheEntry, hash string) bool {
	if e.ConfigHash == nil || *e.ConfigHash != hash {
		return false
	}
	if e.CachedAt == nil || *e.CachedAt == 0 {
		return false
	}
	return float64(time.Now().UnixMilli())-*e.CachedAt <= mcpCacheMaxAgeMs
}

// mcpDirectToolResolution is McpDirectToolResolution (without the runtime snapshot).
type mcpDirectToolResolution struct {
	Selections          []resolvedMcpSelection
	UnresolvedSelectors []string
}

// resolveMcpDirectToolResolution resolves an agent's `mcp:` selectors against the configured servers and the fresh part of the
// adapter's metadata cache. An error is a server definition that cannot be hashed or a malformed configuration file.
func resolveMcpDirectToolResolution(mcpDirectTools []string, cwd string) (mcpDirectToolResolution, error) {
	selectors := normalizeMcpDirectToolSelectors(mcpDirectTools)
	if len(selectors) == 0 {
		return mcpDirectToolResolution{Selections: []resolvedMcpSelection{}, UnresolvedSelectors: []string{}}, nil
	}
	cfg, err := loadMcpConfig(cwd)
	if err != nil {
		return mcpDirectToolResolution{}, err
	}
	sel := parseMcpDirectToolSelectors(selectors)
	selected := append([]string{}, sel.ServerOrder...)
	for _, s := range sel.ToolServers {
		if !contains(selected, s) {
			selected = append(selected, s)
		}
	}
	for _, name := range selected {
		if def, ok := cfg.Servers.vals[name].(*jsObject); ok {
			if _, err := computeMcpServerHash(def); err != nil {
				return mcpDirectToolResolution{}, err
			}
		}
	}
	cache := loadMcpMetadataCache()
	if cache == nil {
		return mcpDirectToolResolution{Selections: []resolvedMcpSelection{}, UnresolvedSelectors: selectors}, nil
	}
	valid := map[string]mcpGrantMetadata{}
	var entries []mcpGrantServerEntry
	for _, name := range cfg.Servers.keys {
		def := cfg.Servers.vals[name].(*jsObject)
		if e, ok := cache[name]; ok {
			hash, err := computeMcpServerHash(def)
			if err != nil {
				return mcpDirectToolResolution{}, err
			}
			if isMcpServerCacheValid(e, hash) {
				valid[name] = e.meta
			}
		}
		entries = append(entries, mcpGrantServerEntry{Name: name, Server: mcpGrantServerOf(def)})
	}
	var prefix any
	if cfg.Settings != nil {
		prefix = cfg.Settings.vals["toolPrefix"]
	}
	grant := planMcpDirectToolGrant(mcpDirectToolGrantInput{Selectors: selectors, Servers: entries, Metadata: valid, ToolPrefix: prefix})
	return mcpDirectToolResolution{Selections: grant.Selections, UnresolvedSelectors: grant.UnresolvedSelectors}, nil
}

func mcpGrantServerOf(def *jsObject) mcpGrantServer {
	s := mcpGrantServer{ToolPrefix: def.vals["toolPrefix"]}
	if isUndefined(s.ToolPrefix) {
		s.ToolPrefix = nil
	}
	if b, ok := def.vals["exposeResources"].(bool); ok {
		s.ExposeResources = &b
	}
	s.IncludeTools, _ = asArray(def.vals["includeTools"])
	s.ExcludeTools, _ = asArray(def.vals["excludeTools"])
	return s
}

// ---- the config hash --------------------------------------------------------------------------------------------------------

func interpolateMcpEnvVars(value string) string {
	value = mcpEnvVarRe1.ReplaceAllStringFunc(value, func(m string) string { return os.Getenv(mcpEnvVarRe1.FindStringSubmatch(m)[1]) })
	value = mcpEnvVarRe2.ReplaceAllStringFunc(value, func(m string) string { return os.Getenv(mcpEnvVarRe2.FindStringSubmatch(m)[1]) })
	return mcpEnvVarRe3.ReplaceAllStringFunc(value, func(m string) string { return os.Getenv(mcpEnvVarRe3.FindStringSubmatch(m)[1]) })
}

func interpolateMcpSecretExpression(value string) string {
	if strings.HasPrefix(value, "!!") {
		return interpolateMcpEnvVars(value[1:])
	}
	if strings.HasPrefix(value, "!") {
		return value
	}
	return interpolateMcpEnvVars(value)
}

// mcpUndefined is the value stableStringify prints as `undefined`.
type mcpUndefined struct{}

func interpolateMcpEnvRecord(v any) any {
	o, ok := asObject(v)
	if !ok {
		return mcpUndefined{}
	}
	out := newObject()
	for _, k := range o.keys {
		out.set(k, interpolateMcpSecretExpression(o.vals[k].(string)))
	}
	return out
}

func resolveMcpConfigPath(v any) any {
	s, ok := v.(string)
	if !ok {
		return mcpUndefined{}
	}
	resolved := interpolateMcpEnvVars(s)
	if resolved == "~" {
		return homeDir()
	}
	if strings.HasPrefix(resolved, "~/") || strings.HasPrefix(resolved, `~\`) {
		return filepath.Join(homeDir(), resolved[2:])
	}
	return resolved
}

func resolveMcpServerURL(def *jsObject) (any, error) {
	v, has := def.vals["url"]
	if !has || v == nil || isUndefined(v) {
		return mcpUndefined{}, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("MCP server URL must be a string")
	}
	var missing []string
	for _, m := range mcpEnvVarMissing.FindAllStringSubmatch(s, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" {
			name = m[3]
		}
		if _, set := os.LookupEnv(name); name != "" && !set && !contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		plural := "s"
		if len(missing) == 1 {
			plural = ""
		}
		return nil, fmt.Errorf("Missing environment variable%s in MCP server URL: %s", plural, strings.Join(missing, ", "))
	}
	resolved := interpolateMcpEnvVars(s)
	if u, err := url.Parse(resolved); err != nil || u.Scheme == "" {
		return nil, fmt.Errorf("Invalid MCP server URL after environment interpolation: %s", resolved)
	}
	return resolved, nil
}

func resolveMcpBearerToken(def *jsObject) any {
	if s, ok := def.vals["bearerToken"].(string); ok {
		return interpolateMcpSecretExpression(s)
	}
	if env, ok := def.vals["bearerTokenEnv"].(string); ok && env != "" {
		if v, set := os.LookupEnv(env); set {
			return v
		}
	}
	return mcpUndefined{}
}

func mcpStableStringify(v any) string {
	switch x := v.(type) {
	case mcpUndefined:
		return "undefined"
	case nil:
		return "null"
	case *jsObject:
		keys := append([]string{}, x.keys...)
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			val := x.vals[k]
			if _, u := val.(undef); u {
				val = mcpUndefined{}
			}
			parts[i] = marshalJSON(k, "") + ":" + mcpStableStringify(val)
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = mcpStableStringify(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case []string:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = marshalJSON(e, "")
		}
		return "[" + strings.Join(parts, ",") + "]"
	case undef:
		return "undefined"
	}
	return marshalJSON(v, "")
}

func mcpOpt(def *jsObject, key string) any {
	v, has := def.vals[key]
	if !has || isUndefined(v) && v != nil {
		return mcpUndefined{}
	}
	return v
}

// computeMcpServerHash is the adapter's identity hash of a server definition: what its metadata cache entry is keyed by.
func computeMcpServerHash(def *jsObject) (string, error) {
	url, err := resolveMcpServerURL(def)
	if err != nil {
		return "", err
	}
	identity := newObject()
	identity.set("command", mcpOpt(def, "command"))
	identity.set("args", mcpOpt(def, "args"))
	identity.set("socket", resolveMcpConfigPath(def.vals["socket"]))
	identity.set("env", interpolateMcpEnvRecord(def.vals["env"]))
	identity.set("cwd", resolveMcpConfigPath(def.vals["cwd"]))
	identity.set("url", url)
	identity.set("headers", interpolateMcpEnvRecord(def.vals["headers"]))
	var rhc any = mcpUndefined{}
	if o, ok := def.vals["requestHeadersCommand"].(*jsObject); ok {
		c := newObject()
		cmd, _ := o.vals["command"].(string)
		c.set("command", interpolateMcpEnvVars(cmd))
		args := any(mcpUndefined{})
		if list, ok := stringListOf(o.vals["args"]); ok {
			conv := make([]any, len(list))
			for i, a := range list {
				conv[i] = interpolateMcpEnvVars(a)
			}
			args = conv
		}
		c.set("args", args)
		c.set("env", interpolateMcpEnvRecord(o.vals["env"]))
		c.set("timeoutMs", mcpOpt(o, "timeoutMs"))
		rhc = c
	}
	identity.set("requestHeadersCommand", rhc)
	identity.set("auth", mcpOpt(def, "auth"))
	identity.set("bearerToken", resolveMcpBearerToken(def))
	identity.set("bearerTokenEnv", mcpOpt(def, "bearerTokenEnv"))
	identity.set("exposeResources", mcpOpt(def, "exposeResources"))
	identity.set("includeTools", mcpOpt(def, "includeTools"))
	identity.set("excludeTools", mcpOpt(def, "excludeTools"))
	identity.set("protocolVersion", mcpOpt(def, "protocolVersion"))
	sum := sha256.Sum256([]byte(mcpStableStringify(identity)))
	return hex.EncodeToString(sum[:]), nil
}
