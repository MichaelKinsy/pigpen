package ssh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/capability/ssh.ts,
//           packages/coding-agent/src/discovery/ssh-config.ts,
//           packages/coding-agent/src/discovery/ssh.ts

// HostSource describes where an SSH host entry was discovered.
type HostSource struct {
	Provider string // "ssh-json" or "ssh-config"
	Path     string
	Level    string // "project" or "user"
}

// Host is a canonical discovered SSH host entry.
// upstream: packages/coding-agent/src/capability/ssh.ts:11-28
type Host struct {
	Name        string
	Host        string
	Username    string
	Port        int
	KeyPath     string
	Description string
	Compat      *bool
	Source      HostSource
}

// ConfigOptions controls where DiscoverHosts looks for SSH config files.
type ConfigOptions struct {
	Cwd string
	// Home overrides the user home directory (defaults to effectiveHome()).
	Home string
	// SystemConfig overrides `/etc/ssh/ssh_config` (empty uses default unless
	// DisableSystemConfig is true).
	SystemConfig string
	// DisableSystemConfig skips reading `/etc/ssh/ssh_config`.
	DisableSystemConfig bool
	// Providers restricts discovery to named providers ("ssh-json", "ssh-config").
	// Empty loads both with ssh-json taking priority over ssh-config.
	Providers []string
}

const (
	defaultSystemSSHConfig = "/etc/ssh/ssh_config"
	// OpenSSH's READCONF_MAX_DEPTH: how deep Include may nest.
	maxIncludeDepth = 16
)

var (
	sshConfigDirectiveRE = regexp.MustCompile(`^\s*([A-Za-z]+)\s*=?\s*(.*)$`)
	sshConfigArgumentRE  = regexp.MustCompile(`"([^"]*)"|'([^']*)'|([^\s"']+)`)
	sshConfigGlobCharsRE = regexp.MustCompile(`[*?\[]`)
	envVarExpandRE       = regexp.MustCompile(`\$\{([^}:]+)(?::-([^}]*))?\}`)
)

// effectiveHome resolves the user's home directory, preferring an explicit
// override, then HOME (so tests overriding HOME never touch real ~/.ssh), then
// os.UserHomeDir(), then os/user.Current().HomeDir (matching Node os.homedir()).
func effectiveHome(override string) string {
	if override != "" {
		return override
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	return ""
}
func expandTilde(p, home string) string {
	if home == "" || p == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}

// expandEnvVars expands `${VAR}` and `${VAR:-default}` matching omp's
// discovery/helpers.ts:563-572.
func expandEnvVars(value string) string {
	return envVarExpandRE.ReplaceAllStringFunc(value, func(match string) string {
		m := envVarExpandRE.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		varName := m[1]
		hasDefault := strings.Contains(match, ":-")
		defaultVal := ""
		if len(m) >= 3 {
			defaultVal = m[2]
		}
		envVal, ok := os.LookupEnv(varName)
		if ok && (!hasDefault || envVal != "") {
			return envVal
		}
		if hasDefault {
			return defaultVal
		}
		return "${" + varName + "}"
	})
}

// DiscoverHosts loads SSH hosts from omp `ssh.json` config files (priority 5)
// and OpenSSH `~/.ssh/config` + `/etc/ssh/ssh_config` (priority 3).
// When the same host name appears in both, higher-priority `ssh-json` wins.
// Strictly read-only on `~/.ssh/config`.
// upstream: discovery/ssh.ts + discovery/ssh-config.ts + capability/index.ts
func DiscoverHosts(opts ConfigOptions) ([]Host, []string) {
	home := effectiveHome(opts.Home)
	wantJSON := len(opts.Providers) == 0
	wantConfig := len(opts.Providers) == 0
	for _, p := range opts.Providers {
		switch p {
		case "ssh-json":
			wantJSON = true
		case "ssh-config":
			wantConfig = true
		}
	}

	seen := make(map[string]bool)
	var items []Host
	var warnings []string

	// Priority 5: ssh-json (project managed, user managed, cwd ssh.json, cwd .ssh.json).
	if wantJSON {
		jsonHosts, jsonWarns := discoverSSHJSON(home, opts.Cwd)
		warnings = append(warnings, jsonWarns...)
		for _, h := range jsonHosts {
			if seen[h.Name] {
				continue
			}
			seen[h.Name] = true
			items = append(items, h)
		}
	}

	// Priority 3: OpenSSH config (~/.ssh/config, then /etc/ssh/ssh_config).
	if wantConfig {
		sysCfg := opts.SystemConfig
		if sysCfg == "" && !opts.DisableSystemConfig {
			sysCfg = defaultSystemSSHConfig
		}
		cfgHosts, cfgWarns := discoverOpenSSHConfig(home, sysCfg)
		warnings = append(warnings, cfgWarns...)
		for _, h := range cfgHosts {
			if seen[h.Name] {
				continue
			}
			seen[h.Name] = true
			items = append(items, h)
		}
	}

	return items, warnings
}

type rawSSHJSONHost struct {
	Host        string `json:"host"`
	Username    string `json:"username"`
	Port        any    `json:"port"`
	Compat      any    `json:"compat"`
	Key         string `json:"key"`
	KeyPath     string `json:"keyPath"`
	Description string `json:"description"`
}

func sshJSONConfigDirName() string {
	if v := strings.TrimSpace(os.Getenv("PI_CONFIG_DIR")); v != "" {
		return v
	}
	return ".omp"
}

func sshJSONUserAgentDir(home string) string {
	if v := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); v != "" {
		return v
	}
	if home == "" {
		return ""
	}
	cfgDir := sshJSONConfigDirName()
	if filepath.IsAbs(cfgDir) {
		return filepath.Join(cfgDir, "agent")
	}
	return filepath.Join(home, cfgDir, "agent")
}

func discoverSSHJSON(home, cwd string) ([]Host, []string) {
	type source struct {
		path  string
		level string
	}
	var candidates []source
	if cwd != "" {
		candidates = append(candidates, source{path: filepath.Join(cwd, ".omp", "ssh.json"), level: "project"})
	}
	if userAgentDir := sshJSONUserAgentDir(home); userAgentDir != "" {
		candidates = append(candidates, source{path: filepath.Join(userAgentDir, "ssh.json"), level: "user"})
	}
	if cwd != "" {
		candidates = append(candidates,
			source{path: filepath.Join(cwd, "ssh.json"), level: "project"},
			source{path: filepath.Join(cwd, ".ssh.json"), level: "project"},
		)
	}

	seenPaths := make(map[string]bool, len(candidates))
	var out []Host
	var warnings []string
	for _, cand := range candidates {
		clean := filepath.Clean(cand.path)
		if seenPaths[clean] {
			continue
		}
		seenPaths[clean] = true
		hosts, warns := loadSSHJSONFile(clean, cand.level, home)
		out = append(out, hosts...)
		warnings = append(warnings, warns...)
	}
	return out, warnings
}

type orderedRawEntry struct {
	name string
	raw  json.RawMessage
}

// decodeOrderedJSONObject decodes a JSON object into key/raw-value pairs while
// preserving the source file's key order.
func decodeOrderedJSONObject(data []byte) ([]orderedRawEntry, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, false
	}
	var entries []orderedRawEntry
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := kTok.(string)
		if !ok {
			return nil, false
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, false
		}
		entries = append(entries, orderedRawEntry{name: key, raw: val})
	}
	return entries, true
}

func loadSSHJSONFile(filePath, level, home string) ([]Host, []string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, []string{fmt.Sprintf("Failed to parse JSON in %s", filePath)}
	}
	hostsRaw, hasHosts := top["hosts"]
	if !hasHosts {
		return nil, []string{fmt.Sprintf("Missing hosts in %s", filePath)}
	}
	orderedEntries, ok := decodeOrderedJSONObject(hostsRaw)
	if !ok {
		return nil, []string{fmt.Sprintf("Missing hosts in %s", filePath)}
	}

	src := HostSource{Provider: "ssh-json", Path: filePath, Level: level}
	var hosts []Host
	var warnings []string
	for _, entry := range orderedEntries {
		name := entry.name
		if strings.TrimSpace(name) == "" {
			warnings = append(warnings, fmt.Sprintf("Invalid SSH host name in %s", filePath))
			continue
		}
		// Decode each entry individually so a malformed non-object entry is skipped
		// with a warning rather than failing the whole file (discovery/ssh.ts:100-108).
		var objCheck map[string]json.RawMessage
		if err := json.Unmarshal(entry.raw, &objCheck); err != nil || objCheck == nil {
			warnings = append(warnings, fmt.Sprintf("Invalid host entry in %s: %s", filePath, name))
			continue
		}
		var raw rawSSHJSONHost
		if err := json.Unmarshal(entry.raw, &raw); err != nil {
			warnings = append(warnings, fmt.Sprintf("Invalid host entry in %s: %s", filePath, name))
			continue
		}
		hostAddr := expandEnvVars(raw.Host)
		if hostAddr == "" {
			warnings = append(warnings, fmt.Sprintf("Missing host for SSH entry: %s", name))
			continue
		}
		port, portOK := parseJSONPort(raw.Port)
		if raw.Port != nil && !portOK {
			warnings = append(warnings, fmt.Sprintf("Invalid port for SSH entry %s: %v", name, raw.Port))
		}
		compat, compatOK := parseJSONCompat(raw.Compat)
		if raw.Compat != nil && !compatOK {
			warnings = append(warnings, fmt.Sprintf("Invalid compat flag for SSH entry %s: %v", name, raw.Compat))
		}
		keyVal := expandEnvVars(raw.KeyPath)
		if keyVal == "" {
			keyVal = expandEnvVars(raw.Key)
		}
		keyPath := ""
		if keyVal != "" {
			keyPath = expandTilde(keyVal, home)
		}
		hosts = append(hosts, Host{
			Name:        name,
			Host:        hostAddr,
			Username:    expandEnvVars(raw.Username),
			Port:        port,
			KeyPath:     keyPath,
			Description: expandEnvVars(raw.Description),
			Compat:      compat,
			Source:      src,
		})
	}
	return hosts, warnings
}

func parseJSONPort(v any) (int, bool) {
	if v == nil {
		return 0, true
	}
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(expandEnvVars(x)))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func parseJSONCompat(v any) (*bool, bool) {
	if v == nil {
		return nil, true
	}
	switch x := v.(type) {
	case bool:
		b := x
		return &b, true
	case string:
		switch strings.ToLower(strings.TrimSpace(expandEnvVars(x))) {
		case "true", "1", "yes":
			b := true
			return &b, true
		case "false", "0", "no":
			b := false
			return &b, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

// ── OpenSSH ~/.ssh/config parser (read-only) ─────────────────────────────────
// upstream: packages/coding-agent/src/discovery/ssh-config.ts

type sshConfigScan struct {
	home     string
	baseDir  string
	order    []string
	hosts    map[string]Host
	visited  map[string]bool
	warnings []string
}

// splitSSHConfigArgs extracts directive arguments, unquoting "..." and '...'
// and stopping at an unquoted `#` token.
// upstream: discovery/ssh-config.ts:40-47
func splitSSHConfigArgs(rest string) []string {
	matches := sshConfigArgumentRE.FindAllStringSubmatch(rest, -1)
	args := make([]string, 0, len(matches))
	for _, m := range matches {
		if m[3] != "" && strings.HasPrefix(m[3], "#") {
			break
		}
		switch {
		case m[1] != "":
			args = append(args, m[1])
		case m[2] != "":
			args = append(args, m[2])
		case m[3] != "":
			args = append(args, m[3])
		default:
			// Empty quoted string "" or ''
			args = append(args, "")
		}
	}
	return args
}

// resolveSSHInclude expands an Include argument against scan.baseDir (with
// tilde expansion), returning sorted matches like OpenSSH's glob(3) — skipping
// dotfiles unless the matching glob pattern segment explicitly starts with `.`.
// upstream: discovery/ssh-config.ts:50-63
func resolveSSHInclude(arg string, scan *sshConfigScan) []string {
	expanded := expandTilde(arg, scan.home)
	var file string
	if filepath.IsAbs(expanded) {
		file = filepath.Clean(expanded)
	} else {
		file = filepath.Clean(filepath.Join(scan.baseDir, expanded))
	}
	if !sshConfigGlobCharsRE.MatchString(file) {
		return []string{file}
	}
	matches, err := filepath.Glob(file)
	if err != nil {
		return nil
	}
	patSegs := strings.Split(filepath.ToSlash(file), "/")
	// OpenSSH Include follows symlinks to regular files; filter out directories
	// and dotfiles whose corresponding glob segment does not start with `.`.
	var files []string
	for _, m := range matches {
		if globMatchedHiddenSegment(patSegs, strings.Split(filepath.ToSlash(m), "/")) {
			continue
		}
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue
		}
		files = append(files, m)
	}
	sort.Strings(files)
	return files
}

func globMatchedHiddenSegment(patSegs, matchSegs []string) bool {
	if len(patSegs) != len(matchSegs) {
		return false
	}
	for i := range patSegs {
		if sshConfigGlobCharsRE.MatchString(patSegs[i]) &&
			!strings.HasPrefix(patSegs[i], ".") &&
			strings.HasPrefix(matchSegs[i], ".") {
			return true
		}
	}
	return false
}

func scanOpenSSHConfigFile(file string, depth int, scan *sshConfigScan) {
	clean := filepath.Clean(file)
	if scan.visited[clean] {
		return
	}
	scan.visited[clean] = true
	data, err := os.ReadFile(clean)
	if err != nil {
		return
	}
	src := HostSource{Provider: "ssh-config", Path: clean, Level: "user"}
	lines := strings.Split(string(data), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		m := sshConfigDirectiveRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		keyword := strings.ToLower(m[1])
		switch keyword {
		case "host":
			for _, name := range splitSSHConfigArgs(m[2]) {
				// Skip negated (`!bastion`), wildcard (`*.corp`, `db?`), empty, or already-seen names.
				if name == "" || strings.HasPrefix(name, "!") || strings.ContainsAny(name, "*?") {
					continue
				}
				if _, exists := scan.hosts[name]; exists {
					continue
				}
				scan.order = append(scan.order, name)
				scan.hosts[name] = Host{
					Name:   name,
					Host:   name,
					Source: src,
				}
			}
		case "include":
			if depth >= maxIncludeDepth {
				scan.warnings = append(scan.warnings, fmt.Sprintf("Include nested deeper than %d levels in %s", maxIncludeDepth, clean))
				continue
			}
			for _, arg := range splitSSHConfigArgs(m[2]) {
				for _, inc := range resolveSSHInclude(arg, scan) {
					scanOpenSSHConfigFile(inc, depth+1, scan)
				}
			}
		}
	}
}

func discoverOpenSSHConfig(home, systemConfig string) ([]Host, []string) {
	scan := &sshConfigScan{
		home:    home,
		hosts:   make(map[string]Host),
		visited: make(map[string]bool),
	}
	type rootCfg struct {
		file    string
		baseDir string
	}
	var roots []rootCfg
	if home != "" {
		userDir := filepath.Join(home, ".ssh")
		roots = append(roots, rootCfg{file: filepath.Join(userDir, "config"), baseDir: userDir})
	}
	if systemConfig != "" {
		roots = append(roots, rootCfg{file: systemConfig, baseDir: filepath.Dir(systemConfig)})
	}
	for _, r := range roots {
		scan.baseDir = r.baseDir
		scanOpenSSHConfigFile(r.file, 0, scan)
	}
	out := make([]Host, 0, len(scan.order))
	for _, name := range scan.order {
		out = append(out, scan.hosts[name])
	}
	return out, scan.warnings
}
