package pi_permission_system

import "testing"

// Tests the port adds: the wildcard and rule extras and the permissive-bash warning.

const allowPaths = `"path":"allow","external_directory":"allow"`

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
