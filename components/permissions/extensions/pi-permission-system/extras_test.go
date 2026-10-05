package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests the port adds: the gate's own decisions, the config reader and the guards for what is not ported.

func policyOf(t *testing.T, config string) *Policy {
	t.Helper()
	c := Config{}
	root := jo(t, config)
	c.Permission = root.obj("permission")
	c.Yolo, _ = root.vals["yoloMode"].(bool)
	return NewPolicy(c)
}

const allowPaths = `"path":"allow","external_directory":"allow"`

func TestIsSimpleCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"git status": true, "echo  hi": true, "npm i --save-dev x": true, "./run.sh a=b": true, "make test": true,
		"": false, " ls": false, "ls ": false, "ls | wc": false, "ls; rm x": false, "echo $HOME": false, "echo `x`": false,
		"echo 'a'": false, "echo \"a\"": false, "a && b": false, "cat < f": false, "cat > f": false, "ls\nrm": false, "ls\trm": false,
		// a wrapper is a simple command too: it is classified per unit (classifyWrapperWords), not refused here
		"sudo rm x": true, "env A=1 ls": true, "A=1 ls": false, "bash x.sh": true, "xargs rm": true, "find . -name x": true,
		"echo (x)": false, "echo {a,b}": false, "echo *": false, "echo ~": false, "echo é": false, "git status\\": false,
	} {
		eq(t, isSimpleCommand(cmd), want, cmd)
	}
}

func TestDecideBash(t *testing.T) {
	p := policyOf(t, `{"permission":{`+allowPaths+`,"bash":{"*":"deny","git status":"allow","rm *":{"action":"deny","reason":"no deletes"}}}}`)
	eq(t, p.Decide("bash", map[string]any{"command": "git status"}), Verdict{}, "allowed")
	eq(t, p.Decide("bash", map[string]any{"command": "rm x"}).Reason, "[pi-permission-system] Denied by policy: 'bash' (rule 'rm *'). Reason: no deletes.", "reason")
	eq(t, p.Decide("bash", map[string]any{"command": "ls"}).Reason, "[pi-permission-system] Denied by policy: 'bash' (rule '*').", "catch-all")
	// A chain of simple commands is decided unit by unit, the most restrictive unit winning (bash-command.ts).
	eq(t, p.Decide("bash", map[string]any{"command": "git status && rm x"}).Reason, "[pi-permission-system] Denied by policy: 'bash' (rule 'rm *'). Reason: no deletes.", "chain")
	eq(t, p.Decide("bash", map[string]any{"command": "git status; ls"}).Reason, "[pi-permission-system] Denied by policy: 'bash' (rule '*').", "chain catch-all")
	eq(t, p.Decide("bash", map[string]any{"command": "git status || git status"}), Verdict{}, "chain allowed")
	eq(t, p.Decide("bash", map[string]any{"command": "  git status\n"}), Verdict{}, "the command is trimmed")
	for _, cmd := range []string{"echo $(ls)", "", "ls &", "ls;", "git status > x"} {
		v := p.Decide("bash", map[string]any{"command": cmd})
		eq(t, v.Block, true, "blocked: "+cmd)
		eq(t, strings.Contains(v.Reason, "cannot evaluate this 'bash' call"), true, "says why: "+cmd)
	}
	eq(t, p.Decide(" bash ", map[string]any{"command": "git status"}), Verdict{}, "the tool name is trimmed")
	eq(t, p.Decide("bash", nil).Block, true, "no input")

	// Without value patterns, every unit of a chain of simple commands gets the catch-all; a wrapper unit is floored (below).
	q := policyOf(t, `{"permission":{`+allowPaths+`,"bash":"allow"}}`)
	eq(t, q.Decide("bash", map[string]any{"command": "ls | wc -l; rm x"}), Verdict{}, "constant allow")
	d := policyOf(t, `{"permission":{`+allowPaths+`,"bash":"deny"}}`)
	eq(t, d.Decide("bash", map[string]any{"command": "ls | wc"}).Reason, "[pi-permission-system] Denied by policy: 'bash' (rule '*').", "constant deny")
	// a wrapper unit's allow is floored to ask (the original's <indirection-bash-wrapper> and <opaque-bash-wrapper>)
	for _, cmd := range []string{"nice touch a", "echo x && sudo rm x", "/bin/sh -c ls", "fd -x rm", "eval x"} {
		eq(t, q.Decide("bash", map[string]any{"command": cmd}), asked("bash"), "floored: "+cmd)
	}
	// a command the port cannot read is refused while the catch-all allows: it could hide a wrapper
	eq(t, strings.Contains(q.Decide("bash", map[string]any{"command": "echo $(sudo rm x)"}).Reason, "cannot evaluate"), true, "unreadable")
	// yoloMode grants the floored ask, and anything the catch-all allows
	y := policyOf(t, `{"yoloMode":true,"permission":{`+allowPaths+`,"bash":"allow"}}`)
	eq(t, y.Decide("bash", map[string]any{"command": "nice touch a"}), Verdict{}, "yolo floor")
	eq(t, y.Decide("bash", map[string]any{"command": "echo $(sudo rm x)"}), Verdict{}, "yolo unreadable")
	// a command the port guards (it runs another command the original reads by its own rule) is refused when allowed
	eq(t, strings.Contains(q.Decide("bash", map[string]any{"command": "ssh host rm x"}).Reason, "'ssh' runs another command"), true, "guard")
}

func TestDecideAsk(t *testing.T) {
	p := policyOf(t, `{"permission":{`+allowPaths+`,"bash":{"*":"ask","git *":"allow"},"foo":"ask"}}`)
	eq(t, p.Decide("bash", map[string]any{"command": "ls"}).Block, true, "ask blocks")
	eq(t, strings.Contains(p.Decide("bash", map[string]any{"command": "ls"}).Reason, "approval dialog is not ported"), true, "ask says so")
	eq(t, p.Decide("bash", map[string]any{"command": "git log"}), Verdict{}, "allow")
	eq(t, p.Decide("foo", nil).Block, true, "extension tool ask")
	// The universal fallback is ask without any config.
	eq(t, NewPolicy(Config{}).Decide("anything", nil).Block, true, "default ask")
	// yoloMode turns asks into allows and leaves denies.
	y := policyOf(t, `{"yoloMode":true,"permission":{`+allowPaths+`,"bash":{"*":"ask","rm *":"deny"},"foo":"deny"}}`)
	eq(t, y.Decide("bash", map[string]any{"command": "ls"}), Verdict{}, "yolo allows an ask")
	eq(t, y.Decide("bash", map[string]any{"command": "rm x"}).Block, true, "yolo keeps a deny")
	eq(t, y.Decide("foo", nil).Block, true, "yolo keeps a tool deny")
	eq(t, y.yolo, true, "yolo flag")
}

func TestDecideExtensionTools(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"deny","foo":"allow","bar":{"*":{"action":"deny","reason":"nope"}},"baz":{"qux":"allow"}}}`)
	eq(t, p.Decide("foo", nil), Verdict{}, "allow")
	eq(t, p.Decide("bar", nil).Reason, "[pi-permission-system] Denied by policy: 'bar' (rule '*'). Reason: nope.", "deny with reason")
	eq(t, p.Decide("other", nil).Reason, "[pi-permission-system] Denied by policy: 'other'.", "universal deny names no rule")
	eq(t, p.Decide("baz", nil).Reason, "[pi-permission-system] Denied by policy: 'baz'.", "a tool's value patterns do not match its '*' value")
	// A wildcard surface key matches several tools.
	w := policyOf(t, `{"permission":{"*":"ask","my_*":"allow"}}`)
	eq(t, w.Decide("my_tool", nil), Verdict{}, "wildcard surface")
}

func TestDecideGuards(t *testing.T) {
	has := func(v Verdict, s string) bool { return v.Block && strings.Contains(v.Reason, s) }
	// A path-bearing tool with value patterns is not evaluated.
	p := policyOf(t, `{"permission":{`+allowPaths+`,"read":{"*":"deny","*.md":"allow"}}}`)
	eq(t, has(p.Decide("read", map[string]any{"path": "a.md"}), "value patterns, and path matching is not ported"), true, "read with patterns")
	// Constant rules are decided.
	q := policyOf(t, `{"permission":{`+allowPaths+`,"read":"allow","write":"deny"}}`)
	eq(t, q.Decide("read", map[string]any{"path": "a"}), Verdict{}, "read allowed")
	eq(t, q.Decide("write", map[string]any{"path": "a"}).Reason, "[pi-permission-system] Denied by policy: 'write' (rule '*').", "write denied")
	// The path surfaces must allow everything, or the port cannot stand in for the path gates.
	n := policyOf(t, `{"permission":{"read":"allow","bash":"allow"}}`)
	eq(t, has(n.Decide("read", map[string]any{"path": "a"}), "path_read surface does not allow every path"), true, "default path ask")
	eq(t, has(n.Decide("bash", map[string]any{"command": "ls"}), "path_read surface does not allow every path"), true, "bash needs it too")
	o := policyOf(t, `{"permission":{"path":{"*":"allow","*.env":"deny"},"external_directory":"allow","read":"allow"}}`)
	eq(t, has(o.Decide("read", map[string]any{"path": "a"}), "path rules have value patterns"), true, "path patterns")
	e := policyOf(t, `{"permission":{"path":"allow","external_directory":"ask","read":"allow"}}`)
	eq(t, has(e.Decide("read", map[string]any{"path": "a"}), "external_directory_read surface does not allow"), true, "external ask")
	w := policyOf(t, `{"permission":{"path":"allow","external_directory":{"*":"allow"},"external_directory_write":"ask","read":"allow"}}`)
	eq(t, has(w.Decide("read", map[string]any{"path": "a"}), "external_directory_write surface does not allow"), true, "external write ask")
	// MCP and skill surfaces.
	eq(t, has(q.Decide("mcp", nil), "MCP and skill surfaces are not ported"), true, "mcp")
	eq(t, has(q.Decide("skill", nil), "MCP and skill surfaces are not ported"), true, "skill")
	eq(t, has(q.Decide("mcp__srv__tool", nil), "MCP and skill surfaces are not ported"), true, "pi mcp tool")
	eq(t, q.Decide("mcp__srv", nil).Block, true, "not an MCP name: an extension tool under the default ask")
}

func TestPolicyComposition(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"deny","read":"allow","bash":{"a":"ask"}}}`)
	eq(t, p.rules[0], Rule{Surface: "*", Pattern: "*", Action: "deny", Layer: "default", Origin: "global"}, "the universal fallback is the default rule")
	eq(t, len(p.rules), 3, "the '*' key is not a config rule")
	eq(t, p.rules[1], Rule{Surface: "read", Pattern: "*", Action: "allow", Layer: "config", Origin: "global"}, "config rule")
	eq(t, NewPolicy(Config{}).rules[0], Rule{Surface: "*", Pattern: "*", Action: "ask", Layer: "default", Origin: "builtin"}, "no config: ask, builtin")
	m := policyOf(t, `{"permission":{"*":"bogus"}}`)
	eq(t, m.rules[0].Action, "ask", "an invalid universal value is ignored")
	// path sugar expands to its directions
	s := policyOf(t, `{"permission":{"path":"allow"}}`)
	var surfaces []string
	for _, r := range s.rules[1:] {
		surfaces = append(surfaces, r.Surface)
	}
	eq(t, surfaces, []string{"path_read", "path_write"}, "sugar")
}

func TestIsToolFullyDenied(t *testing.T) {
	p := policyOf(t, `{"permission":{"*":"ask","read":{"*":"deny","a":"allow"},"write":"deny","edit":{"a":"deny"},"bash":{"*":"deny","echo":"allow"}}}`)
	for tool, want := range map[string]bool{"read": false, "write": true, " write ": true, "edit": false, "bash": false, "grep": false, "mcp__a__b": false} {
		eq(t, p.IsToolFullyDenied(tool), want, tool)
	}
	eq(t, policyOf(t, `{"permission":{"*":"deny"}}`).IsToolFullyDenied("anything"), true, "universal deny")
	eq(t, policyOf(t, `{"permission":{"*":"deny"}}`).IsToolFullyDenied("mcp__srv__tool"), false, "an MCP tool is never withheld: its surface is not ported")
}

func TestDetectPermissiveBashFallback(t *testing.T) {
	for cfg, want := range map[string]bool{
		`{"*":"allow"}`: true, `{"*":"allow","bash":{"git *":"ask"}}`: true, `{"*":"allow","bash":"ask"}`: false,
		`{"*":"allow","bash":{"*":"ask"}}`: false, `{"*":"ask"}`: false, `{"*":"deny"}`: false, `{}`: false, `{"bash":"allow"}`: false,
	} {
		got := detectPermissiveBashFallback(jo(t, cfg)) != ""
		eq(t, got, want, cfg)
	}
	eq(t, detectPermissiveBashFallback(nil), "", "nil")
	eq(t, detectPermissiveBashFallback(jo(t, `{"*":"allow"}`)),
		`Permission config sets a permissive top-level '*': 'allow' with no 'bash' '*' policy, so bash commands silently inherit 'allow'. Set an explicit 'bash' policy (e.g. "bash": { "*": "ask" }) to gate bash commands.`, "text")
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	eq(t, globalConfigPath(), filepath.Join(dir, "extensions", "pi-permission-system", "config.json"), "path")
	eq(t, loadConfig(), Config{}, "missing")
	write := func(s string) {
		p := globalConfigPath()
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write(`{"permission":{"bash":"allow"},"yoloMode":true}`)
	c := loadConfig()
	eq(t, c.Yolo, true, "yolo")
	eq(t, marshalJSON(c.Permission, ""), `{"bash":"allow"}`, "permission")
	write(`{"yoloMode":"yes","permission":[1]}`)
	c = loadConfig()
	eq(t, c.Yolo, false, "yoloMode must be the boolean true")
	eq(t, c.Permission == nil, true, "permission must be an object")
	write(`{"permission":`)
	eq(t, loadConfig().Issue != "", true, "invalid JSON is an issue")
	write(`[1]`)
	eq(t, loadConfig(), Config{Issue: "Invalid config value at '(root)': Invalid input: expected object, received array"}, "not an object")
	// a relative agent directory is under the home directory
	useFakeHome(t)
	t.Setenv("PI_CODING_AGENT_DIR", "rel/agent")
	eq(t, agentDir(), filepath.Join(fakeHome, "rel/agent"), "relative")
	t.Setenv("PI_CODING_AGENT_DIR", "  ")
	eq(t, agentDir(), filepath.Join(fakeHome, ".pi", "agent"), "default")
}

func TestWildcardExtras(t *testing.T) {
	useFakeHome(t)
	eq(t, WildcardMatch(`~\dev`, fakeHome+"/dev", nil), true, "a backslash after ~ is a separator")
	eq(t, WildcardMatch("~", fakeHome, nil), true, "bare ~ is the home directory")
	eq(t, WildcardMatch("$HOME", fakeHome, nil), true, "bare $HOME")
	eq(t, WildcardMatch("${HOME}/x", fakeHome+"/x", nil), true, "${HOME}")
	eq(t, WildcardMatch("~x", fakeHome+"x", nil), false, "~x is not a home prefix")
	eq(t, WildcardMatch("~x", "~x", nil), true, "~x is literal")
	ci := &MatchOptions{CaseInsensitive: true}
	eq(t, WildcardMatch("I", "\u0131", ci), false, "dotless i does not fold onto ASCII (JavaScript's canonicalization)")
	eq(t, WildcardMatch("S", "\u017f", ci), false, "long s does not fold onto ASCII")
	eq(t, WildcardMatch("\u00e9", "\u00c9", ci), true, "non-ASCII letters fold")
	eq(t, WildcardMatch("i", "I", ci), true, "ASCII folds")
	// ? is one UTF-16 unit: an astral character is two
	eq(t, WildcardMatch("?", "\U0001F600", nil), false, "one unit does not match an astral character")
	eq(t, WildcardMatch("??", "\U0001F600", nil), true, "two units do")
	eq(t, WildcardMatch("*", "\U0001F600", nil), true, "star")
	// names: a name that is only white space is skipped even when a pattern would match it
	p := CompilePatternEntries([]Entry[string]{{"*", "allow"}})
	eq(t, FindCompiledMatchForNames(p, []string{" ", "\t"}) == nil, true, "blank names")
	eq(t, FindCompiledMatchForNames(p, []string{" ", "x"}).MatchedName, "x", "the trimmed non-blank name")
}

func TestRuleExtras(t *testing.T) {
	px := PosixPathFlavor
	// the path surface folds case and separators on Windows
	r := Ruleset{rule("path", `C:\Foo\*`, "allow", "", "global"), rule("path", "*", "deny", "", "global")}
	eq(t, Evaluate("path", `c:\foo\x`, Ruleset{r[1], r[0]}, Win32PathFlavor, "").Action, "allow", "path folds on win32")
	eq(t, Evaluate("path", `c:\foo\x`, Ruleset{r[1], r[0]}, px, "").Action, "deny", "path is exact on posix")
	// two asks: the first one is reported
	asks := Ruleset{rule("path", "a", "ask", "", "global"), rule("path", "b", "ask", "", "global")}
	got := EvaluateMostRestrictive("path", []string{"a", "b"}, asks, px)
	eq(t, got.Value, "a", "first ask")
	// Pi MCP tool names
	for name, want := range map[string]bool{"mcp__srv__tool": true, "mcp____tool": false, "mcp__srv__": false, "mcp__srv": false, "mcp_srv__tool": false, "xmcp__a__b": false, "mcp__a__b__c": true} {
		eq(t, isPiMcpToolName(name), want, name)
	}
	for key, want := range map[string]bool{"mcp__a?": true, "mcp__a*": true, "mcp__a": false, "mcp?": false, "mcp__*": true} {
		eq(t, canNamePiMcpTool(key), want, key)
	}
	// an array in place of a pattern map: its elements are patterns named by index
	arr := NormalizeFlatConfig(jo(t, `{"bash":["allow","bogus","deny"]}`))
	eq(t, arr, Ruleset{rule("bash", "0", "allow", "", "builtin"), rule("bash", "2", "deny", "", "builtin")}, "array")
}
