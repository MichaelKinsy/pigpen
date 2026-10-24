package pi_permission_system

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of the global config scope of src/config: config.json under the agent directory, its `permission` map and `yoloMode`.

const extensionID = "pi-permission-system"

// agentDir is the agent directory the config lives in. First the host's own answer: PiG starts every extension
// process with PIG_EXT_HOST_INFO carrying the directory the session runs with (its own tree, or Pi's when it shares
// it), which is the fact the original read through the coding-agent's getAgentDir(). Reading it here is what makes
// the port find the config file the original found — guessing from PI_CODING_AGENT_DIR does not, because in the
// default mode PiG sets no such variable (it is a shared-mode variable only) and the guess falls back to
// ~/.pi/agent, where no config exists: every call then falls to the universal ask and the whole allow list is lost.
// Without a host (tests, a standalone run) the directory is resolved with PiG's own rule (divergence D2, the same
// order pi-typesafe-api's AgentDir uses), then Pi's variable for compatibility with the original's environment.
func agentDir() string {
	if d, err := sdk.AgentDir(); err == nil {
		if d = jsTrim(d); d != "" {
			return d
		}
	}
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if o := jsTrim(os.Getenv("PI_CODING_AGENT_DIR")); o != "" {
			return expandAgentHome(o)
		}
		return filepath.Join(homeDir(), ".pi", "agent")
	}
	for _, name := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		if o := jsTrim(os.Getenv(name)); o != "" {
			return expandAgentHome(o)
		}
	}
	if h := jsTrim(os.Getenv("PIG_HOME")); h != "" {
		return filepath.Join(expandAgentHome(h), "agent")
	}
	if x := jsTrim(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(expandAgentHome(x), "pig", "agent")
	}
	return filepath.Join(homeDir(), ".pig", "agent")
}

// expandAgentHome makes an agent directory absolute: a path stays as it is, ~ and a relative segment resolve
// against the home directory. A leading separator counts as absolute the way Node's path.isAbsolute does on
// Windows, so a POSIX-spelled root is not joined onto the home directory a second time.
func expandAgentHome(v string) string {
	if filepath.IsAbs(v) || strings.HasPrefix(v, "/") || strings.HasPrefix(v, `\`) {
		return filepath.Clean(v)
	}
	if v == "~" {
		return homeDir()
	}
	if strings.HasPrefix(v, "~/") || strings.HasPrefix(v, `~\`) {
		return filepath.Join(homeDir(), v[2:])
	}
	return filepath.Join(homeDir(), v)
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
	Unported   []string // the options set in the file that the port accepts and does not read yet (unported.go)
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
	c.Unported = unportedOptions(root)
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

// simpleCommandChars are the characters a simple command's words may hold beyond letters and digits. On Windows a
// backslash is a path separator rather than (only) an escape, so it joins a word like a forward slash does.
var simpleCommandChars = func() string {
	c := "_./:=@%+,- "
	if runtime.GOOS == "windows" {
		c += `\`
	}
	return c
}()

// isSimpleCommand reports a command the port can treat as one unit: words of plain characters separated by spaces, no quoting,
// expansion, redirection or chaining, and not starting with a variable assignment. Wrappers are classified per unit (bash.go).
func isSimpleCommand(cmd string) bool {
	if cmd == "" || strings.HasPrefix(cmd, " ") || strings.HasSuffix(cmd, " ") {
		return false
	}
	for _, r := range cmd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(simpleCommandChars, r):
		default:
			return false
		}
	}
	first := strings.SplitN(cmd, " ", 2)[0]
	return !strings.Contains(first, "=")
}

// Verdict is the outcome for one tool call: allow is the zero value, a deny carries its reason, and an ask carries
// everything the approval dialog needs. Ask is nil when the policy does not ask, so a verdict is comparable in tests.
type Verdict struct {
	Block  bool
	Reason string
	Ask    *Ask
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

func (p *Policy) constantIn(surface string, session Ruleset) Rule {
	return Evaluate(surface, "*", p.sessionWith(session), PosixPathFlavor, "")
}

// unsupported refuses a call the port cannot decide yet (refusal.go). The call is blocked, never allowed.
func unsupported(feature string) Verdict {
	return Verdict{Block: true, Reason: refusal(feature)}
}

// outcome is the verdict for a decided rule: an allow passes, an ask becomes the ask a was built for, and a deny
// is the original's denial text (agent-renderer.ts renderPolicyDenial, in the port's compact clause style).
func (p *Policy) outcome(r Rule, a Ask) Verdict {
	switch r.Action {
	case "allow":
		return Verdict{}
	case "ask":
		return Verdict{Ask: &a}
	}
	return Verdict{Block: true, Reason: tagged(denySentence(a.Surface, r))}
}

// worstVerdict is the most restrictive of the verdicts (deny, then ask, then allow; the first of equals wins), the
// way the original runs its gates deny-first and takes the first block (descriptor.ts orderDenyFirst).
func worstVerdict(verdicts ...Verdict) Verdict {
	rank := func(v Verdict) int {
		switch {
		case v.Block:
			return 2
		case v.Ask != nil:
			return 1
		}
		return 0
	}
	best, bestRank := Verdict{}, 0
	for _, v := range verdicts {
		if rank(v) > bestRank {
			best, bestRank = v, rank(v)
		}
	}
	return best
}

// Decide gates one tool call, with no session approvals and no path boundary (the session-free entry point the
// probes and unit tests use; the extension calls DecideIn).
func (p *Policy) Decide(tool string, input map[string]any) Verdict {
	return p.DecideIn(tool, input, nil, "")
}

// DecideIn gates one tool call: session approvals are appended after the config's rules (last match wins, as the
// original composes them at check time), and cwd is the working directory the path and external_directory gates
// judge paths against ("" leaves the boundary unevaluated).
func (p *Policy) DecideIn(tool string, input map[string]any, session Ruleset, cwd string) Verdict {
	name := jsTrim(tool)
	switch {
	case name == "bash":
		cmd, _ := input["command"].(string)
		return p.decideBash(cmd, session, cwd)
	case p.shellTools[name]:
		return unsupported("the 'shellTools' option (the '" + name + "' tool routed through the bash surface)")
	case name == "mcp", name == "skill", isPiMcpToolName(name):
		return unsupported("permissions for the '" + name + "' tool (the MCP and skill surfaces)")
	case pathBearingTools[name]:
		if p.valuePatterns(name) {
			return unsupported("path rules with value patterns for the '" + name + "' tool (path matching)")
		}
		return p.decidePathTool(name, input, session, cwd)
	}
	return p.decidePathTool(name, input, session, cwd)
}

// decidePathTool is a tool call's decision: the tool surface's catch-all, then the cross-cutting path and
// external-directory gates over the call's `path` input (every tool carries one by convention), the most
// restrictive verdict winning.
func (p *Policy) decidePathTool(name string, input map[string]any, session Ruleset, cwd string) Verdict {
	r := p.constantIn(name, session)
	toolVerdict := p.outcome(r, toolAsk(name, r))
	path := extractToolPath(name, input)
	if path == "" {
		return toolVerdict
	}
	return worstVerdict(
		p.pathGate(name, path, cwd, session),
		p.externalDirectoryGate(name, path, cwd, session),
		toolVerdict,
	)
}

// IsToolFullyDenied reports whether every value of the tool's surface is denied, so the tool is withheld from the model.
func (p *Policy) IsToolFullyDenied(tool string) bool {
	name := jsTrim(tool)
	if isPiMcpToolName(name) {
		return false // the MCP surface is not ported
	}
	return IsSurfaceFullyDenied(name, p.rules, PosixPathFlavor)
}
