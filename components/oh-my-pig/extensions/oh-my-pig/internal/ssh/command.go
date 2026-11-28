package ssh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// HostConfigEntry is one entry in an omp `ssh.json` file.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:10-17
type HostConfigEntry struct {
	Host        string `json:"host"`
	Username    string `json:"username,omitempty"`
	Port        int    `json:"port,omitempty"`
	KeyPath     string `json:"keyPath,omitempty"`
	Description string `json:"description,omitempty"`
	Compat      bool   `json:"compat,omitempty"`
}

// ConfigFile is a lossless representation of an omp `ssh.json` file.
// Top-level unknown fields and untouched host entries are preserved verbatim
// via raw JSON maps when reading and writing back.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:19-21
type ConfigFile struct {
	Hosts    map[string]HostConfigEntry
	rawTop   map[string]json.RawMessage
	rawHosts map[string]json.RawMessage
}

var validHostNameRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// ValidateHostName validates an SSH host alias name.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:66-78
func ValidateHostName(name string) error {
	if name == "" {
		return errors.New("Host name cannot be empty")
	}
	if len(name) > 100 {
		return errors.New("Host name is too long (max 100 characters)")
	}
	if !validHostNameRE.MatchString(name) {
		return errors.New("Host name can only contain letters, numbers, dash, underscore, and dot")
	}
	return nil
}

func parseDisplayHostEntry(raw json.RawMessage) HostConfigEntry {
	var parsed rawSSHJSONHost
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return HostConfigEntry{}
	}
	entry := HostConfigEntry{
		Host:        strings.TrimSpace(parsed.Host),
		Username:    strings.TrimSpace(parsed.Username),
		KeyPath:     strings.TrimSpace(parsed.KeyPath),
		Description: strings.TrimSpace(parsed.Description),
	}
	if entry.KeyPath == "" {
		entry.KeyPath = strings.TrimSpace(parsed.Key)
	}
	switch p := parsed.Port.(type) {
	case float64:
		if p >= 1 && p <= 65535 && p == float64(int(p)) {
			entry.Port = int(p)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 1 && n <= 65535 {
			entry.Port = n
		}
	}
	if b, ok := parsed.Compat.(bool); ok {
		entry.Compat = b
	}
	return entry
}

// ReadConfigFile reads an omp `ssh.json` file losslessly; returns an empty ConfigFile if missing.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:27-42
func ReadConfigFile(filePath string) (*ConfigFile, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConfigFile{
				Hosts:    map[string]HostConfigEntry{},
				rawTop:   map[string]json.RawMessage{},
				rawHosts: map[string]json.RawMessage{},
			}, nil
		}
		return nil, err
	}
	var rawTop map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawTop); err != nil {
		return nil, fmt.Errorf("Failed to parse SSH config file %s: %w", filePath, err)
	}
	if rawTop == nil {
		rawTop = map[string]json.RawMessage{}
	}
	rawHosts := map[string]json.RawMessage{}
	if hostsBlob, ok := rawTop["hosts"]; ok && len(hostsBlob) > 0 && string(hostsBlob) != "null" {
		if err := json.Unmarshal(hostsBlob, &rawHosts); err != nil {
			return nil, fmt.Errorf("Failed to parse SSH config file %s: %w", filePath, err)
		}
		if rawHosts == nil {
			rawHosts = map[string]json.RawMessage{}
		}
	}
	hosts := make(map[string]HostConfigEntry, len(rawHosts))
	for name, blob := range rawHosts {
		hosts[name] = parseDisplayHostEntry(blob)
	}
	return &ConfigFile{
		Hosts:    hosts,
		rawTop:   rawTop,
		rawHosts: rawHosts,
	}, nil
}

// WriteConfigFile writes an omp `ssh.json` file atomically with 0600 permissions,
// preserving top-level unknown keys and untouched raw host entries verbatim.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:48-60
func WriteConfigFile(filePath string, cfg *ConfigFile) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	outTop := make(map[string]json.RawMessage, len(cfg.rawTop)+1)
	for k, v := range cfg.rawTop {
		outTop[k] = v
	}
	outHosts := make(map[string]json.RawMessage, len(cfg.Hosts))
	for k, entry := range cfg.Hosts {
		if raw, ok := cfg.rawHosts[k]; ok {
			outHosts[k] = raw
			continue
		}
		b, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		outHosts[k] = b
	}
	hostsRaw, err := json.Marshal(outHosts)
	if err != nil {
		return err
	}
	outTop["hosts"] = hostsRaw

	data, err := json.MarshalIndent(outTop, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, filePath)
}

// AddHost adds a new SSH host entry to filePath without mutating any other
// top-level keys or existing host entries.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:85-116
func AddHost(filePath, name string, entry HostConfigEntry) error {
	if err := ValidateHostName(name); err != nil {
		return err
	}
	if strings.TrimSpace(entry.Host) == "" {
		return errors.New("Host address cannot be empty")
	}
	cfg, err := ReadConfigFile(filePath)
	if err != nil {
		return err
	}
	if _, exists := cfg.Hosts[name]; exists {
		return fmt.Errorf("Host %q already exists in %s", name, filePath)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	cfg.Hosts[name] = entry
	cfg.rawHosts[name] = b
	return WriteConfigFile(filePath, cfg)
}

// RemoveHost removes an SSH host entry from filePath without mutating any other
// top-level keys or existing host entries.
// upstream: packages/coding-agent/src/ssh/config-writer.ts:157-175
func RemoveHost(filePath, name string) error {
	cfg, err := ReadConfigFile(filePath)
	if err != nil {
		return err
	}
	if _, exists := cfg.Hosts[name]; !exists {
		return fmt.Errorf("Host %q not found in %s", name, filePath)
	}
	delete(cfg.Hosts, name)
	delete(cfg.rawHosts, name)
	return WriteConfigFile(filePath, cfg)
}

// sshConfigPath returns the managed ssh.json path for the given scope ("project" or "user"),
// using the exact same user-agent directory resolver (`sshJSONUserAgentDir`) as DiscoverHosts.
func sshConfigPath(scope, cwd, home string) string {
	if scope == "user" {
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		userAgentDir := sshJSONUserAgentDir(home)
		if userAgentDir != "" {
			return filepath.Join(userAgentDir, "ssh.json")
		}
		return filepath.Join(home, ".omp", "agent", "ssh.json")
	}
	return filepath.Join(cwd, ".omp", "ssh.json")
}

func registerSSHCommand(e *sdk.Extension) {
	if e == nil {
		return
	}
	e.RegisterCommand("ssh", sdk.CommandOptions{
		Description: "Manage SSH hosts (add, list, remove)",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			if strings.Contains(prefix, " ") {
				return nil, nil
			}
			items := []sdk.AutocompleteItem{
				{Value: "add ", Label: "add", Description: "Add an SSH host"},
				{Value: "list ", Label: "list", Description: "List all configured SSH hosts"},
				{Value: "remove ", Label: "remove", Description: "Remove an SSH host"},
				{Value: "help ", Label: "help", Description: "Show help message"},
			}
			lower := strings.ToLower(prefix)
			var out []sdk.AutocompleteItem
			for _, item := range items {
				if strings.HasPrefix(item.Label, lower) {
					out = append(out, item)
				}
			}
			return out, nil
		},
		Handler: handleSSHCommand,
	})
}

const sshAddUsage = "Usage: /ssh add <name> --host <host> [--user <user>] [--port <port>] [--key <keyPath>] [--desc <description>] [--compat] [--scope project|user]"

// handleSSHCommand implements `/ssh <add|list|remove|help>`.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:227-248,
//
//	packages/coding-agent/src/modes/controllers/ssh-command-controller.ts
func handleSSHCommand(ctx sdk.Context, args string) error {
	tokens := parseSSHCommandArgs(args)
	if len(tokens) == 0 || strings.EqualFold(tokens[0], "help") {
		showSSHHelp(ctx)
		return nil
	}
	sub := strings.ToLower(tokens[0])
	switch sub {
	case "list":
		handleSSHList(ctx)
	case "add":
		handleSSHAdd(ctx, tokens[1:])
	case "remove", "rm":
		handleSSHRemove(ctx, tokens[1:])
	default:
		ctx.Notify(fmt.Sprintf("Unknown subcommand: %s. Type /ssh help for usage.", sub), "error")
	}
	return nil
}

func showSSHHelp(ctx sdk.Context) {
	help := strings.Join([]string{
		"Manage SSH host configurations for remote command execution.",
		"",
		"Commands:",
		"  /ssh add <name> --host <host> [--user <user>] [--port <port>] [--key <keyPath>] [--desc <description>] [--compat] [--scope project|user]",
		"  /ssh list             List all configured SSH hosts",
		"  /ssh remove <name> [--scope project|user]    Remove an SSH host (default: project)",
		"  /ssh help             Show this help message",
	}, "\n")
	ctx.Notify(help, "info")
}

func formatSSHHostDetails(host, username string, port int) string {
	var parts []string
	if host != "" {
		parts = append(parts, host)
	}
	if username != "" {
		parts = append(parts, "user="+username)
	}
	if port > 0 && port != 22 {
		parts = append(parts, fmt.Sprintf("port=%d", port))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func resolveSSHDirs(ctx sdk.Context) (cwd, home string) {
	cwd = ctx.Cwd()
	home, _ = os.UserHomeDir()
	if defaultManager != nil && defaultManager.ConfigOpts != nil && defaultManager.ConfigOpts.Home != "" {
		home = defaultManager.ConfigOpts.Home
	}
	return cwd, home
}

func handleSSHList(ctx sdk.Context) {
	cwd, home := resolveSSHDirs(ctx)

	userPath := sshConfigPath("user", cwd, home)
	projectPath := sshConfigPath("project", cwd, home)

	userCfg, err := ReadConfigFile(userPath)
	if err != nil {
		ctx.Notify(fmt.Sprintf("Failed to list hosts: %v", err), "error")
		return
	}
	projectCfg, err := ReadConfigFile(projectPath)
	if err != nil {
		ctx.Notify(fmt.Sprintf("Failed to list hosts: %v", err), "error")
		return
	}

	seen := map[string]bool{}
	userNames := sortedKeys(userCfg.Hosts)
	for _, n := range userNames {
		seen[n] = true
	}
	projectNames := sortedKeys(projectCfg.Hosts)
	for _, n := range projectNames {
		seen[n] = true
	}

	opts := ConfigOptions{
		Cwd:  cwd,
		Home: home,
	}
	if defaultManager != nil && defaultManager.ConfigOpts != nil && defaultManager.ConfigOpts.DisableSystemConfig {
		opts.DisableSystemConfig = true
	}
	allDiscovered, _ := DiscoverHosts(opts)
	var discovered []Host
	for _, h := range allDiscovered {
		if !seen[h.Name] {
			discovered = append(discovered, h)
		}
	}

	if len(userNames) == 0 && len(projectNames) == 0 && len(discovered) == 0 {
		ctx.Notify("No SSH hosts configured.\n\nUse /ssh add to add a host.", "info")
		return
	}

	var lines []string
	if len(userNames) > 0 {
		lines = append(lines, fmt.Sprintf("User level (%s):", userPath))
		for _, name := range userNames {
			c := userCfg.Hosts[name]
			lines = append(lines, fmt.Sprintf("  %s %s", name, formatSSHHostDetails(c.Host, c.Username, c.Port)))
		}
		lines = append(lines, "")
	}
	if len(projectNames) > 0 {
		lines = append(lines, fmt.Sprintf("Project level (%s):", projectPath))
		for _, name := range projectNames {
			c := projectCfg.Hosts[name]
			lines = append(lines, fmt.Sprintf("  %s %s", name, formatSSHHostDetails(c.Host, c.Username, c.Port)))
		}
		lines = append(lines, "")
	}
	if len(discovered) > 0 {
		bySource := map[string][]Host{}
		var sources []string
		for _, h := range discovered {
			src := h.Source.Path
			if src == "" {
				src = h.Source.Provider
			}
			if _, ok := bySource[src]; !ok {
				sources = append(sources, src)
			}
			bySource[src] = append(bySource[src], h)
		}
		for _, src := range sources {
			hosts := bySource[src]
			provider := hosts[0].Source.Provider
			if provider == "" {
				provider = "ssh"
			}
			lines = append(lines, fmt.Sprintf("Discovered (%s: %s): read-only", provider, src))
			for _, h := range hosts {
				lines = append(lines, fmt.Sprintf("  %s %s", h.Name, formatSSHHostDetails(h.Host, h.Username, h.Port)))
			}
			lines = append(lines, "")
		}
	}

	ctx.Notify(strings.TrimSpace(strings.Join(lines, "\n")), "info")
}

func handleSSHAdd(ctx sdk.Context, tokens []string) {
	if len(tokens) == 0 {
		ctx.Notify(sshAddUsage, "error")
		return
	}
	var name, host, username, keyPath, desc string
	var port int
	var compat bool
	scope := "project"

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok {
		case "--host":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --host. "+sshAddUsage, "error")
				return
			}
			i++
			host = tokens[i]
		case "--user", "--username":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --user. "+sshAddUsage, "error")
				return
			}
			i++
			username = tokens[i]
		case "--port":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --port. "+sshAddUsage, "error")
				return
			}
			i++
			p, err := strconv.Atoi(tokens[i])
			if err != nil || p < 1 || p > 65535 {
				ctx.Notify(fmt.Sprintf("Invalid port: %q (must be 1-65535)", tokens[i]), "error")
				return
			}
			port = p
		case "--key", "--key-path":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --key. "+sshAddUsage, "error")
				return
			}
			i++
			keyPath = tokens[i]
		case "--desc", "--description":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --desc. "+sshAddUsage, "error")
				return
			}
			i++
			desc = tokens[i]
		case "--compat":
			compat = true
		case "--scope":
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --scope (expected project or user).", "error")
				return
			}
			i++
			s := strings.ToLower(tokens[i])
			if s != "project" && s != "user" {
				ctx.Notify(fmt.Sprintf("Invalid scope %q: expected project or user.", tokens[i]), "error")
				return
			}
			scope = s
		default:
			if strings.HasPrefix(tok, "-") {
				ctx.Notify(fmt.Sprintf("Unknown flag: %s. %s", tok, sshAddUsage), "error")
				return
			}
			if name == "" {
				name = tok
			} else {
				ctx.Notify(fmt.Sprintf("Unexpected argument: %s. %s", tok, sshAddUsage), "error")
				return
			}
		}
	}

	if name == "" || host == "" {
		ctx.Notify(sshAddUsage, "error")
		return
	}

	cwd, home := resolveSSHDirs(ctx)
	filePath := sshConfigPath(scope, cwd, home)
	entry := HostConfigEntry{
		Host:        host,
		Username:    username,
		Port:        port,
		KeyPath:     keyPath,
		Description: desc,
		Compat:      compat,
	}
	if err := AddHost(filePath, name, entry); err != nil {
		ctx.Notify(fmt.Sprintf("Failed to add host: %v", err), "error")
		return
	}
	ctx.Notify(fmt.Sprintf("+ Added SSH host %q to %s config (%s)", name, scope, filePath), "info")
}

func handleSSHRemove(ctx sdk.Context, tokens []string) {
	var name string
	scope := "project"
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok == "--scope" {
			if i+1 >= len(tokens) {
				ctx.Notify("Missing value for --scope (expected project or user).", "error")
				return
			}
			i++
			s := strings.ToLower(tokens[i])
			if s != "project" && s != "user" {
				ctx.Notify(fmt.Sprintf("Invalid scope %q: expected project or user.", tokens[i]), "error")
				return
			}
			scope = s
			continue
		}
		if strings.HasPrefix(tok, "-") {
			ctx.Notify(fmt.Sprintf("Unknown flag: %s. Usage: /ssh remove <name> [--scope project|user]", tok), "error")
			return
		}
		if name == "" {
			name = tok
		}
	}
	if name == "" {
		ctx.Notify("Host name required. Usage: /ssh remove <name> [--scope project|user]", "error")
		return
	}

	cwd, home := resolveSSHDirs(ctx)
	filePath := sshConfigPath(scope, cwd, home)
	cfg, err := ReadConfigFile(filePath)
	if err != nil {
		ctx.Notify(fmt.Sprintf("Failed to remove host: %v", err), "error")
		return
	}
	if _, ok := cfg.Hosts[name]; !ok {
		ctx.Notify(fmt.Sprintf("Host %q not found in %s config.", name, scope), "error")
		return
	}
	if err := RemoveHost(filePath, name); err != nil {
		ctx.Notify(fmt.Sprintf("Failed to remove host: %v", err), "error")
		return
	}
	ctx.Notify(fmt.Sprintf("- Removed SSH host %q from %s config", name, scope), "info")
}

func sortedKeys(m map[string]HostConfigEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findClosingQuote returns the rune index of the matching quote that terminates
// an argument (end of string or followed by space/tab), or -1 if none exists.
// upstream: packages/coding-agent/src/utils/command-args.ts:62-69
func findClosingQuote(runes []rune, open int) int {
	quote := runes[open]
	for i := open + 1; i < len(runes); i++ {
		if runes[i] == quote && (i+1 == len(runes) || runes[i+1] == ' ' || runes[i+1] == '\t') {
			return i
		}
	}
	return -1
}

// parseSSHCommandArgs mirrors omp's default-mode `parseCommandArgs`:
// backslashes are always literal (preserving Windows paths like `C:\Users\me`),
// and `'`/`"` only delimit when starting a token or immediately after `=`
// (so prose apostrophes like `don't` stay literal).
// upstream: packages/coding-agent/src/utils/command-args.ts:9-59
func parseSSHCommandArgs(argsString string) []string {
	runes := []rune(argsString)
	var out []string
	var cur strings.Builder
	started := false

	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if (ch == '"' || ch == '\'') && (!started || strings.HasSuffix(cur.String(), "=")) {
			closeIdx := findClosingQuote(runes, i)
			if closeIdx == -1 {
				cur.WriteRune(ch)
			} else {
				cur.WriteString(string(runes[i+1 : closeIdx]))
				i = closeIdx
			}
			started = true
		} else if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
			started = false
		} else {
			cur.WriteRune(ch)
			started = true
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
