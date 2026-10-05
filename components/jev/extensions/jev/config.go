package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	configFile = "pi-jev.json"
	apiKeyEnv  = "TYPESAFE_API_KEY"

	backendModel    = "model"
	backendTypeSafe = "typesafe"
)

type gateThresholds struct {
	Destructive, Exfiltration, BeyondScope, Impact float64
}

type gateConfig struct {
	Enabled        bool
	Mode           string // shadow or enforce
	Tools          []string
	ArgumentChars  int
	CacheSeconds   int
	BlockOn        gateThresholds
	MinConfidence  float64
	BlockWithoutUI bool
}

type outputConfig struct {
	Enabled       bool
	Tools         []string
	OutputChars   int
	LeakThreshold float64
	MinConfidence float64
}

type config struct {
	// Consent and destination. Only the user's own config can set these.
	Enabled      bool
	Acknowledged bool
	Backend      string
	Endpoint     string
	Model        string
	APIKey       string
	APIKeyFile   string
	Display      string // rich or plain
	TimeoutMs    int
	Retries      int

	MaxStateChars int
	Gate          gateConfig
	Output        outputConfig

	// KeySource says where the key came from, for /jev.
	KeySource string
	Key       string
}

func defaultConfig() config {
	return config{
		Backend:       backendModel,
		Display:       "rich",
		TimeoutMs:     20000,
		Retries:       2,
		MaxStateChars: 8000,
		Gate: gateConfig{
			Enabled: true, Mode: "shadow", Tools: []string{"bash", "write", "edit"},
			ArgumentChars: 400, CacheSeconds: 120, MinConfidence: 0.5,
			BlockOn: gateThresholds{Destructive: 0.9, Exfiltration: 0.7, BeyondScope: 0.85, Impact: 2.5},
		},
		Output: outputConfig{Enabled: true, Tools: []string{"bash"}, OutputChars: 2000, LeakThreshold: 0.9, MinConfidence: 0.6},
	}
}

// parsed is a config file with only the keys it declared.
type parsed struct {
	m map[string]any
}

func (p parsed) has(k string) bool { _, ok := p.m[k]; return ok }

func asString(v any) (string, bool) {
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && s != ""
}

func isInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.IsInf(f, 0) || f > 1<<53 {
		return 0, false
	}
	return int(f), true
}
func asPositiveInt(v any) (int, bool)    { n, ok := isInt(v); return n, ok && n > 0 }
func asNonNegativeInt(v any) (int, bool) { n, ok := isInt(v); return n, ok && n >= 0 }
func asRatio(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && f >= 0 && f <= 1
}
func asToolNames(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out, len(out) > 0
}

// agentDir is PiG's agent directory: PIG_CODING_AGENT_DIR, else <config home>/agent.
func agentDir(configHome string) string {
	if d := os.Getenv("PIG_CODING_AGENT_DIR"); d != "" {
		return d
	}
	return filepath.Join(configHome, "agent")
}

func readConfigFile(path string, warnings *[]string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		// A missing file is the normal case; anything else is worth reporting.
		if !errors.Is(err, os.ErrNotExist) {
			*warnings = append(*warnings, fmt.Sprintf("%s: %v", path, err))
		}
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: invalid JSON (%v)", path, err))
		return nil, false
	}
	obj, ok := v.(map[string]any)
	if !ok {
		*warnings = append(*warnings, fmt.Sprintf("%s: expected a JSON object", path))
		return nil, false
	}
	return obj, true
}

// applyFile lays the keys a file declares over c. project files are held to the
// trust rules: they cannot consent, redirect, or send more than the user allowed.
func applyFile(c *config, m map[string]any, project bool) (ignored []string) {
	key := func(k string) bool { // returns true when the key may be applied
		if _, ok := m[k]; !ok {
			return false
		}
		if project {
			ignored = append(ignored, k)
			return false
		}
		return true
	}
	if key("enabled") {
		if b, ok := m["enabled"].(bool); ok {
			c.Enabled = b
		}
	}
	if key("acknowledged") {
		if b, ok := m["acknowledged"].(bool); ok {
			c.Acknowledged = b
		}
	}
	if key("backend") {
		if s, ok := asString(m["backend"]); ok && (s == backendModel || s == backendTypeSafe) {
			c.Backend = s
		}
	}
	if key("display") {
		if s, ok := asString(m["display"]); ok && (s == "rich" || s == "plain") {
			c.Display = s
		}
	}
	if key("endpoint") {
		if s, ok := asString(m["endpoint"]); ok {
			c.Endpoint = s
		}
	}
	if key("model") {
		if s, ok := asString(m["model"]); ok {
			c.Model = s
		}
	}
	if key("apiKey") {
		if s, ok := asString(m["apiKey"]); ok {
			c.APIKey = s
		}
	}
	if key("apiKeyFile") {
		if s, ok := asString(m["apiKeyFile"]); ok {
			c.APIKeyFile = s
		}
	}
	if key("timeoutMs") {
		if n, ok := asPositiveInt(m["timeoutMs"]); ok {
			c.TimeoutMs = n
		}
	}
	if key("retries") {
		if n, ok := asNonNegativeInt(m["retries"]); ok {
			c.Retries = n
		}
	}
	if v, ok := m["maxStateChars"]; ok {
		if n, ok := asPositiveInt(v); ok && (!project || n < c.MaxStateChars) {
			c.MaxStateChars = n
		} else if ok {
			ignored = append(ignored, "maxStateChars (a project can only lower it)")
		}
	}
	if g, ok := m["gate"].(map[string]any); ok {
		if b, ok := g["enabled"].(bool); ok {
			c.Gate.Enabled = b
		}
		if s, ok := g["mode"].(string); ok && (s == "shadow" || s == "enforce") {
			c.Gate.Mode = s
		}
		if names, ok := asToolNames(g["tools"]); ok {
			c.Gate.Tools = applyTools(c.Gate.Tools, names, project, "gate.tools", &ignored)
		}
		if n, ok := asNonNegativeInt(g["cacheSeconds"]); ok {
			c.Gate.CacheSeconds = n
		}
		if n, ok := asPositiveInt(g["argumentChars"]); ok && (!project || n < c.Gate.ArgumentChars) {
			c.Gate.ArgumentChars = n
		} else if ok && project {
			ignored = append(ignored, "gate.argumentChars (a project can only lower it)")
		}
		if b, ok := g["blockWithoutUI"].(bool); ok {
			c.Gate.BlockWithoutUI = b
		}
		if f, ok := asRatio(g["minConfidence"]); ok {
			c.Gate.MinConfidence = f
		}
		if bo, ok := g["blockOn"].(map[string]any); ok {
			if f, ok := asRatio(bo["destructive"]); ok {
				c.Gate.BlockOn.Destructive = f
			}
			if f, ok := asRatio(bo["exfiltration"]); ok {
				c.Gate.BlockOn.Exfiltration = f
			}
			if f, ok := asRatio(bo["beyondScope"]); ok {
				c.Gate.BlockOn.BeyondScope = f
			}
			if f, ok := bo["impact"].(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 {
				c.Gate.BlockOn.Impact = f
			}
		}
	}
	if o, ok := m["output"].(map[string]any); ok {
		if b, ok := o["enabled"].(bool); ok {
			c.Output.Enabled = b
		}
		if names, ok := asToolNames(o["tools"]); ok {
			c.Output.Tools = applyTools(c.Output.Tools, names, project, "output.tools", &ignored)
		}
		if n, ok := asPositiveInt(o["outputChars"]); ok && (!project || n < c.Output.OutputChars) {
			c.Output.OutputChars = n
		} else if ok && project {
			ignored = append(ignored, "output.outputChars (a project can only lower it)")
		}
		if f, ok := asRatio(o["leakThreshold"]); ok {
			c.Output.LeakThreshold = f
		}
		if f, ok := asRatio(o["minConfidence"]); ok {
			c.Output.MinConfidence = f
		}
	}
	return ignored
}

// relaxations lists the project settings that weaken the protection the user's own
// config set up: a gate or output judge turned off, enforce turned into shadow, a
// higher threshold, fewer judged tools. They are applied (a project may send less),
// but never silently: a repository can otherwise switch off the gate a user relies on.
func relaxations(user, merged config) []string {
	var out []string
	num := func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
	if user.Gate.Enabled && !merged.Gate.Enabled {
		out = append(out, "gate.enabled false")
	}
	if user.Gate.Mode == "enforce" && merged.Gate.Mode != "enforce" {
		out = append(out, "gate.mode "+merged.Gate.Mode)
	}
	if user.Gate.BlockWithoutUI && !merged.Gate.BlockWithoutUI {
		out = append(out, "gate.blockWithoutUI false")
	}
	for _, t := range []struct {
		name    string
		was, is float64
	}{
		{"gate.blockOn.destructive", user.Gate.BlockOn.Destructive, merged.Gate.BlockOn.Destructive},
		{"gate.blockOn.exfiltration", user.Gate.BlockOn.Exfiltration, merged.Gate.BlockOn.Exfiltration},
		{"gate.blockOn.beyondScope", user.Gate.BlockOn.BeyondScope, merged.Gate.BlockOn.BeyondScope},
		{"gate.blockOn.impact", user.Gate.BlockOn.Impact, merged.Gate.BlockOn.Impact},
		{"gate.minConfidence", user.Gate.MinConfidence, merged.Gate.MinConfidence},
	} {
		if t.is > t.was {
			out = append(out, t.name+" "+num(t.is))
		}
	}
	if dropped := droppedTools(user.Gate.Tools, merged.Gate.Tools); len(dropped) > 0 {
		out = append(out, "gate.tools without "+strings.Join(dropped, "/"))
	}
	if user.Output.Enabled && !merged.Output.Enabled {
		out = append(out, "output.enabled false")
	}
	if merged.Output.LeakThreshold > user.Output.LeakThreshold {
		out = append(out, "output.leakThreshold "+num(merged.Output.LeakThreshold))
	}
	if dropped := droppedTools(user.Output.Tools, merged.Output.Tools); len(dropped) > 0 {
		out = append(out, "output.tools without "+strings.Join(dropped, "/"))
	}
	return out
}

func droppedTools(was, is []string) []string {
	var out []string
	for _, t := range was {
		if !slices.Contains(is, t) {
			out = append(out, t)
		}
	}
	return out
}

// applyTools replaces the tool list; a project can only choose among tools the
// user's own config already judges.
func applyTools(cur, names []string, project bool, what string, ignored *[]string) []string {
	if !project {
		return names
	}
	var out []string
	for _, n := range names {
		if slices.Contains(cur, n) {
			out = append(out, n)
		} else {
			*ignored = append(*ignored, what+" "+n)
		}
	}
	return out
}

type loaded struct {
	Config   config
	Warnings []string
}

// loadConfig layers built-in defaults, <agent dir>/pi-jev.json and
// <cwd>/.pig/pi-jev.json. Project values win only where they narrow: they cannot
// opt in, name a backend, endpoint, model or key, or send more than the user
// configured. See PORT.md C2.
func loadConfig(configHome, cwd string) loaded {
	c := defaultConfig()
	var warnings []string
	if g, ok := readConfigFile(filepath.Join(agentDir(configHome), configFile), &warnings); ok {
		applyFile(&c, g, false)
	}
	projectPath := filepath.Join(cwd, ".pig", configFile)
	if p, ok := readConfigFile(projectPath, &warnings); ok {
		before := c
		if ignored := applyFile(&c, p, true); len(ignored) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: ignored project setting %s (only your own %s can set these)",
				projectPath, strings.Join(ignored, ", "), filepath.Join(agentDir(configHome), configFile)))
		}
		if relaxed := relaxations(before, c); len(relaxed) > 0 {
			warnings = append(warnings, fmt.Sprintf("%s relaxes the gate your own %s sets: %s",
				projectPath, filepath.Join(agentDir(configHome), configFile), strings.Join(relaxed, ", ")))
		}
	}
	c.Key, c.KeySource = resolveKey(c, &warnings)
	return loaded{Config: c, Warnings: warnings}
}

// resolveKey: the environment wins over config, so a host can inject the key
// without touching files. apiKeyFile supports "~/".
func resolveKey(c config, warnings *[]string) (key, source string) {
	if v := strings.TrimSpace(os.Getenv(apiKeyEnv)); v != "" {
		return v, "env " + apiKeyEnv
	}
	if v := strings.TrimSpace(c.APIKey); v != "" {
		return v, "apiKey in pi-jev.json"
	}
	if c.APIKeyFile == "" {
		return "", ""
	}
	path := expandHome(c.APIKeyFile)
	b, err := os.ReadFile(path)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: %v", path, err))
		return "", ""
	}
	if v := strings.TrimSpace(string(b)); v != "" {
		return v, "apiKeyFile " + c.APIKeyFile
	}
	return "", ""
}

func expandHome(p string) string {
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// loopback reports whether host is this machine.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkEndpoint accepts https, or http to this machine only: the API key travels
// in the Authorization header.
func checkEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("endpoint %q is not a URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("endpoint %q must use https (http is accepted only for localhost)", raw)
}
