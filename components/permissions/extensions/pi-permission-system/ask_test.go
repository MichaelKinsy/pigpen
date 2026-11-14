package pi_permission_system

import (
	"path/filepath"
	"strings"
	"testing"
)

// The ask model, the dialog's texts, the session grants and the bash word scan (ask.go, paths.go).

func TestAskSessionSuggestions(t *testing.T) {
	// suggestBashPattern: a one-token command is exact, anything else is the first token plus a wildcard (the
	// original's arity table can suggest a longer exact prefix; that table is not ported).
	for cmd, want := range map[string]string{
		"ls":             "ls",
		"rm -rf /":       "rm *",
		"git  status":    "git *",
		"":               "*",
		"   ":            "*",
		"git status -sb": "git *",
	} {
		eq(t, suggestBashPattern(cmd), want, cmd)
	}
	// The bash ask carries the command as its value and the suggestion as its grant.
	r := rule("bash", "*", "ask", "config", "global")
	a := bashAsk(" rm -rf / ", r)
	eq(t, a.Surface, "bash", "surface")
	eq(t, a.Value, "rm -rf /", "the trimmed command")
	eq(t, a.Pattern, "*", "the deciding rule's pattern")
	eq(t, a.Session.Label, `Yes, allow bash "rm *" for this session`, "label")
	// A config rule that asked by its own pattern names it in the ask.
	r2 := rule("bash", "git push *", "ask", "config", "global")
	eq(t, bashAsk("git push origin", r2).Pattern, "git push *", "the matched pattern")
	// A default-rule ask names no pattern (the original's matchedPattern is undefined there).
	eq(t, bashAsk("ls", rule("*", "*", "ask", "default", "builtin")).Pattern, "", "the universal default")
	// A tool ask offers the catch-all grant on its own surface.
	ta := toolAsk("foo", rule("foo", "*", "ask", "config", "global"))
	eq(t, ta.Session.Surface, "foo", "grant surface")
	eq(t, ta.Session.Pattern, "*", "grant pattern")
	// The skill asks carry no session option.
	sa := skillAsk("demo", "read")
	eq(t, sa.Session.Surface, "", "no session option")
	eq(t, sa.Tool, "read", "the invoked tool")
}

func TestAskDialogTexts(t *testing.T) {
	a := Ask{Surface: "bash", Value: "rm -rf /"}
	eq(t, askTitle(a), "pi-permission-system: bash: rm -rf /", "title")
	long := Ask{Surface: "bash", Value: strings.Repeat("x", 300)}
	eq(t, len([]rune(askTitle(long))), len("pi-permission-system: bash: ")+160+3, "the title is truncated")
	// The denial names the surface, the value (never a command) and the rule, and carries a typed reason.
	eq(t, askDenial(Ask{Surface: "external_directory_write", Pattern: "*", Value: `C:\x\y.txt`}, ""),
		"[pi-permission-system] The user denied this 'external_directory_write' call 'C:\\x\\y.txt' (rule '*').", "a path")
	eq(t, askDenial(Ask{Surface: "skill", Value: "demo", Tool: "read"}, "no skills"),
		"[pi-permission-system] The user denied this 'skill' call 'demo' for tool 'read'. Reason: no skills.", "a skill, with a reason")
	eq(t, strings.Contains(askDenial(Ask{Surface: "bash", Value: "rm -rf /"}, ""), "rm -rf"), false, "the command is never echoed to the model")
	eq(t, askUnavailable(Ask{Surface: "foo"}),
		"[pi-permission-system] This 'foo' call requires approval, but no interactive UI is available.", "unavailable")
}

func TestSessionGrants(t *testing.T) {
	// A grant on a bare family expands to both directional members (SessionRules.approve).
	fold := Ask{Session: AskSession{Surface: "external_directory", Pattern: `C:\out`}}
	eq(t, fold.sessionGrants(), Ruleset{
		rule("external_directory_read", `C:\out`, "allow", "session", "session"),
		rule("external_directory_write", `C:\out`, "allow", "session", "session"),
	}, "family expansion")
	// A directional grant stays one rule.
	narrow := Ask{Session: AskSession{Surface: "external_directory_write", Pattern: `C:\out`}}
	eq(t, len(narrow.sessionGrants()), 1, "one rule")
	// An ask with no session option records nothing.
	eq(t, skillAsk("demo", "").sessionGrants() == nil, true, "no grants")
	// A session grant answers the next check through the session ruleset.
	p := policyOf(t, `{"permission":{"path":"allow","external_directory_write":"ask","read":"allow","write":"allow"}}`)
	cwd := t.TempDir()
	outside := filepath.Join(cwd, "..", "outside.txt")
	v := p.DecideIn("write", map[string]any{"path": outside}, nil, cwd)
	eq(t, v.Ask != nil, true, "asks first")
	eq(t, p.DecideIn("write", map[string]any{"path": outside}, v.Ask.sessionGrants(), cwd), Verdict{}, "the typed-path grant answers it")
}

func TestPathTokens(t *testing.T) {
	for word, want := range map[string]bool{
		"/etc/hosts": true, "~/notes": true, "~": true, `C:\Windows\note`: true, "c:/windows": true,
		"../up.txt": true, "..": true, "..\\up.txt": true,
		"relative.txt": false, "./in.txt": false, "-rf": false, "http://x": false, "a:b": false, "C:x": false, "": false,
	} {
		eq(t, isPathToken(word), want, word)
	}
	for cmd, want := range map[string]string{"cat f": "read", "ls -la /": "read", "rm x": "write", "cp a b": "write", "git s": "unproven", "": "unproven"} {
		eq(t, tokenDirection(cmd), want, cmd)
	}
}

// The word scan resolves the words a simple command carries: a read command's external path on the read member, a
// writing command's on the write member, an unknown command's on the folded family, and nothing for a path inside
// the working directory.
func TestBashExternalGate(t *testing.T) {
	cwd := t.TempDir()
	p := policyOf(t, `{"permission":{"path":"allow","external_directory_read":"allow","external_directory_write":"ask","bash":"allow"}}`)
	outside := filepath.Join(cwd, "..", "out.txt")
	eq(t, p.bashExternalGate("cat in.txt", cwd, nil), Verdict{}, "inside: no boundary")
	eq(t, p.bashExternalGate("cat "+outside, cwd, nil), Verdict{}, "an external read is allowed")
	w := p.bashExternalGate("touch "+outside, cwd, nil)
	eq(t, w.Ask != nil && w.Ask.Surface == "external_directory_write", true, "an external write asks")
	u := p.bashExternalGate("mystery "+outside, cwd, nil)
	eq(t, u.Ask != nil && u.Ask.Surface == "external_directory", true, "an unknown command proves no direction")
	eq(t, p.bashExternalGate("mystery "+outside, cwd, u.Ask.sessionGrants()), Verdict{}, "the session grant answers it")
	// A deny on the write member denies the writing command.
	d := policyOf(t, `{"permission":{"path":"allow","external_directory_write":{"*":{"action":"deny","reason":"no writes"}},"bash":"allow"}}`)
	dv := d.DecideIn("bash", map[string]any{"command": "touch " + outside}, nil, cwd)
	eq(t, dv.Block && strings.Contains(dv.Reason, "Denied by policy: 'external_directory_write'"), true, "external deny")
}
