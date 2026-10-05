package pi_permission_system

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Port of the global config scope of src/config: config.json under the agent directory, its `permission` map and `yoloMode`.

const extensionID = "pi-permission-system"

func agentDir() string {
	if o := jsTrim(os.Getenv("PI_CODING_AGENT_DIR")); o != "" {
		if filepath.IsAbs(o) {
			return filepath.Clean(o)
		}
		return filepath.Join(homeDir(), o)
	}
	return filepath.Join(homeDir(), ".pi", "agent")
}

func globalConfigPath() string {
	return filepath.Join(agentDir(), "extensions", extensionID, "config.json")
}

// Config is what the port reads from the global config file.
type Config struct {
	Permission *jsObject // the `permission` map, nil when absent or not an object
	Yolo       bool
	ShellTools []string // the tools `shellTools` routes through the bash surface
	Issue      string   // the reason the file could not be used, "" when it could
}

// loadConfig reads the global config. A missing file is an empty config; a file that cannot be read, parsed or validated is an
// empty config with an Issue (config-loader.ts loadUnifiedConfig).
func loadConfig() Config {
	path := globalConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}
		}
		return Config{Issue: "Failed to read config at '" + path + "': " + err.Error()}
	}
	return parseConfig(data, path)
}

// parseConfig reads the contents of the config file at path: comments are stripped, and a file the schema rejects is not used.
func parseConfig(data []byte, path string) Config {
	v, err := parseJSON([]byte(stripJSONComments(string(data))))
	if err != nil {
		return Config{Issue: "Failed to read config at '" + path + "': " + err.Error()}
	}
	if issues := validateUnifiedConfig(v); len(issues) > 0 {
		return Config{Issue: strings.Join(issues, "\n")}
	}
	root := v.(*jsObject)
	c := Config{}
	c.Yolo, _ = root.vals["yoloMode"].(bool)
	if perm := root.obj("permission"); perm != nil {
		c.Permission = schemaPermission(perm)
	}
	if tools := root.obj("shellTools"); tools != nil {
		c.ShellTools = tools.order()
	}
	return c
}

// detectPermissiveBashFallback warns when a top-level `*: allow` leaves bash without a policy of its own.
func detectPermissiveBashFallback(permission *jsObject) string {
	if permission == nil {
		return ""
	}
	if s, _ := permission.vals["*"].(string); s != "allow" {
		return ""
	}
	switch bash := permission.vals["bash"].(type) {
	case string:
		return ""
	case *jsObject:
		if _, has := bash.vals["*"]; has {
			return ""
		}
	}
	return "Permission config sets a permissive top-level '*': 'allow' with no 'bash' '*' policy, " +
		"so bash commands silently inherit 'allow'. Set an explicit 'bash' policy " +
		`(e.g. "bash": { "*": "ask" }) to gate bash commands.`
}

// Policy is the composed ruleset of the global scope: the universal fallback first, then the config rules.
type Policy struct {
	rules      Ruleset
	yolo       bool
	shellTools map[string]bool
}

// NewPolicy composes the ruleset the way PermissionManager does for one scope: the `*` key is the default rule only, the other
// surfaces become config rules, and yolo mode rewrites every ask to an allow.
func NewPolicy(c Config) *Policy {
	permission := newObject()
	if c.Permission != nil {
		permission = MergeScopesWithOrigins([]Scope{{Name: "global", Permission: c.Permission}}).Permission
	}
	universal := "ask"
	if s, ok := isPermissionState(permission.vals["*"]); ok {
		universal = s
	}
	without := newObject()
	for _, k := range permission.order() {
		if k != "*" {
			without.set(k, permission.vals[k])
		}
	}
	rules := Ruleset{{Surface: "*", Pattern: "*", Action: universal, Layer: "default", Origin: originOf(permission)}}
	for _, r := range NormalizeFlatConfig(without) {
		r.Layer, r.Origin = "config", "global"
		rules = append(rules, r)
	}
	rules = RelocateMcpToolKeyRules(rules).Rules
	if c.Yolo {
		rules = RewriteAsksToYolo(rules)
	}
	p := &Policy{rules: rules, yolo: c.Yolo, shellTools: map[string]bool{}}
	for _, t := range c.ShellTools {
		p.shellTools[t] = true
	}
	return p
}

func originOf(permission *jsObject) string {
	if _, ok := permission.vals["*"].(string); ok {
		return "global"
	}
	return "builtin"
}

// ---- the tool_call gate ----

var pathBearingTools = map[string]bool{"read": true, "write": true, "edit": true, "find": true, "grep": true, "ls": true}

// isSimpleCommand reports a command the port can treat as one unit: words of plain characters separated by spaces, no quoting,
// expansion, redirection or chaining, and not starting with a variable assignment. Wrappers are classified per unit (bash.go).
func isSimpleCommand(cmd string) bool {
	if cmd == "" || strings.HasPrefix(cmd, " ") || strings.HasSuffix(cmd, " ") {
		return false
	}
	for _, r := range cmd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_./:=@%+,- ", r):
		default:
			return false
		}
	}
	first := strings.SplitN(cmd, " ", 2)[0]
	return !strings.Contains(first, "=")
}

// Verdict is the outcome for one tool call.
type Verdict struct {
	Block  bool
	Reason string
}

func tagged(sentence string) string { return "[" + extensionID + "] " + sentence }

func (p *Policy) valuePatterns(surface string) bool {
	for _, r := range p.rules {
		if r.Layer == "config" && r.Pattern != "*" && WildcardMatch(r.Surface, surface, nil) {
			return true
		}
	}
	return false
}

func (p *Policy) constant(surface string) Rule {
	return Evaluate(surface, "*", p.rules, PosixPathFlavor, "")
}

func unsupported(tool, why string) Verdict {
	return Verdict{Block: true, Reason: tagged("(Go port) cannot evaluate this '" + tool + "' call: " + why + ". It was blocked (fail-closed).")}
}

func asked(tool string) Verdict {
	return Verdict{Block: true, Reason: tagged("(Go port) this '" + tool + "' call requires approval, but the approval dialog is not ported. It was blocked.")}
}

func (p *Policy) outcome(surface string, r Rule) Verdict {
	switch r.Action {
	case "allow":
		return Verdict{}
	case "ask":
		return asked(surface)
	}
	var b strings.Builder
	b.WriteString("Denied by policy: '" + surface + "'")
	if r.Layer == "config" || r.Layer == "session" {
		b.WriteString(" (rule '" + r.Pattern + "')")
	}
	b.WriteString(".")
	if r.Reason != nil && *r.Reason != "" {
		b.WriteString(" Reason: " + *r.Reason + ".")
	}
	return Verdict{Block: true, Reason: tagged(b.String())}
}

// pathFamilyAllows reports whether the path and external_directory surfaces allow every value without a value pattern, which is
// what lets a call skip the path gates the port does not have.
func (p *Policy) pathFamilyAllows(tool string) (bool, string) {
	for _, s := range []string{"path_read", "path_write", "external_directory_read", "external_directory_write"} {
		if p.valuePatterns(s) {
			return false, "the path rules have value patterns, and path matching is not ported"
		}
		if p.constant(s).Action != "allow" {
			return false, "the " + s + " surface does not allow every path, and path gating is not ported"
		}
	}
	return true, ""
}

// Decide gates one tool call.
func (p *Policy) Decide(tool string, input map[string]any) Verdict {
	name := jsTrim(tool)
	switch {
	case name == "bash":
		cmd, _ := input["command"].(string)
		if ok, why := p.pathFamilyAllows(name); !ok {
			return unsupported(name, why)
		}
		return p.decideBash(cmd)
	case p.shellTools[name]:
		return unsupported(name, "the config routes it through the bash surface (shellTools), and shell-tool aliases are not ported")
	case name == "mcp", name == "skill", isPiMcpToolName(name):
		return unsupported(name, "MCP and skill surfaces are not ported")
	case pathBearingTools[name]:
		if p.valuePatterns(name) {
			return unsupported(name, "the rules for it have value patterns, and path matching is not ported")
		}
		if ok, why := p.pathFamilyAllows(name); !ok {
			return unsupported(name, why)
		}
		return p.outcome(name, p.constant(name))
	}
	return p.outcome(name, p.constant(name))
}

// IsToolFullyDenied reports whether every value of the tool's surface is denied, so the tool is withheld from the model.
func (p *Policy) IsToolFullyDenied(tool string) bool {
	name := jsTrim(tool)
	if isPiMcpToolName(name) {
		return false // the MCP surface is not ported
	}
	return IsSurfaceFullyDenied(name, p.rules, PosixPathFlavor)
}
