package pi_permission_system

import "testing"

func TestWildcardMatcher(t *testing.T) {
	const f = "policy/wildcard-matcher"
	useFakeHome(t)
	allow := func(p string) Entry[string] { return Entry[string]{p, "allow"} }

	tw(t, f, "returns empty array for empty iterable", func(t *testing.T) {
		eq(t, len(CompilePatternEntries[string](nil)), 0, "empty")
	})
	tw(t, f, "compiles a single exact pattern", func(t *testing.T) {
		r := CompilePatternEntries([]Entry[string]{{"read", "allow"}})
		eq(t, len(r), 1, "length")
		eq(t, r[0].Pattern, "read", "pattern")
		eq(t, r[0].State, "allow", "state")
	})
	tw(t, f, "compiles multiple patterns in order", func(t *testing.T) {
		r := CompilePatternEntries([]Entry[string]{{"read", "allow"}, {"write", "deny"}, {"bash *", "ask"}})
		eq(t, len(r), 3, "length")
		var got []string
		for _, c := range r {
			got = append(got, c.Pattern)
		}
		eq(t, got, []string{"read", "write", "bash *"}, "order")
	})

	tw(t, f, "returns null for empty patterns array", func(t *testing.T) {
		if FindCompiledMatch[string](nil, "read") != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "matches exact pattern", func(t *testing.T) {
		m := FindCompiledMatch(CompilePatternEntries([]Entry[string]{allow("read")}), "read")
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.State, "allow", "state")
		eq(t, m.MatchedPattern, "read", "pattern")
		eq(t, m.MatchedName, "read", "name")
	})
	tw(t, f, "returns null when no pattern matches", func(t *testing.T) {
		if FindCompiledMatch(CompilePatternEntries([]Entry[string]{allow("read")}), "write") != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "matches glob * pattern", func(t *testing.T) {
		m := FindCompiledMatch(CompilePatternEntries([]Entry[string]{allow("git *")}), "git status")
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.State, "allow", "state")
		eq(t, m.MatchedPattern, "git *", "pattern")
	})
	tw(t, f, "glob * matches zero or more characters", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{allow("git*")})
		eq(t, FindCompiledMatch(p, "git") != nil, true, "git")
		eq(t, FindCompiledMatch(p, "git status") != nil, true, "git status")
		eq(t, FindCompiledMatch(p, "npm install") == nil, true, "npm install")
	})
	tw(t, f, "last-match-wins precedence: later pattern overrides earlier", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{{"git *", "allow"}, {"git push *", "deny"}})
		m := FindCompiledMatch(p, "git push origin main")
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.State, "deny", "state")
		eq(t, m.MatchedPattern, "git push *", "pattern")
	})
	tw(t, f, "last-match-wins: specific deny before broad allow matches the later one", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{{"*", "deny"}, {"git status", "allow"}})
		m := FindCompiledMatch(p, "git status")
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.State, "allow", "state")
	})
	tw(t, f, "exact pattern does not match partial name", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{allow("read")})
		eq(t, FindCompiledMatch(p, "read ") == nil, true, "trailing space")
		eq(t, FindCompiledMatch(p, "readonly") == nil, true, "readonly")
	})
	tw(t, f, "regex special characters in pattern are escaped", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{{"tool.name", "allow"}, {"tool+extra", "deny"}})
		eq(t, FindCompiledMatch(p, "toolXname") == nil, true, "dot is literal")
		eq(t, FindCompiledMatch(p, "tool.name") != nil, true, "tool.name")
		eq(t, FindCompiledMatch(p, "tool+extra") != nil, true, "tool+extra")
	})

	tw(t, f, "returns null for empty names array", func(t *testing.T) {
		if FindCompiledMatchForNames(CompilePatternEntries([]Entry[string]{allow("read")}), nil) != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "returns null when all names are whitespace", func(t *testing.T) {
		if FindCompiledMatchForNames(CompilePatternEntries([]Entry[string]{allow("  ")}), []string{"  ", "\t"}) != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "matches first name that has a pattern match", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{{"read", "allow"}, {"write", "deny"}})
		m := FindCompiledMatchForNames(p, []string{"grep", "write"})
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.MatchedName, "write", "name")
		eq(t, m.State, "deny", "state")
	})
	tw(t, f, "trims whitespace from names before matching", func(t *testing.T) {
		m := FindCompiledMatchForNames(CompilePatternEntries([]Entry[string]{allow("read")}), []string{"  read  "})
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.State, "allow", "state")
	})
	tw(t, f, "returns null when no name matches any pattern", func(t *testing.T) {
		if FindCompiledMatchForNames(CompilePatternEntries([]Entry[string]{allow("read")}), []string{"write", "grep"}) != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "multi-name lookup: returns match for first matching name in order", func(t *testing.T) {
		p := CompilePatternEntries([]Entry[string]{{"read", "allow"}, {"write", "deny"}})
		m := FindCompiledMatchForNames(p, []string{"read", "write"})
		if m == nil {
			t.Fatal("nil")
		}
		eq(t, m.MatchedName, "read", "name")
		eq(t, m.State, "allow", "state")
	})
	tw(t, f, "compileWildcardPattern produces correct pattern metadata", func(t *testing.T) {
		c := CompilePattern("bash *", "ask", nil)
		eq(t, c.Pattern, "bash *", "pattern")
		eq(t, c.State, "ask", "state")
		eq(t, c.Matches("bash ls -la"), true, "match")
		eq(t, c.Matches("echo hello"), false, "no match")
	})
	tw(t, f, "a compiled pattern folds the value it is handed (#653)", func(t *testing.T) {
		c := CompilePattern("/dev/*", "allow", &MatchOptions{WindowsSeparators: true})
		eq(t, c.Matches("/dev/null"), true, "forward")
		eq(t, c.Matches("\\dev\\null"), true, "backward")
	})

	w := func(t *testing.T, pattern, value string, want bool, o ...*MatchOptions) {
		t.Helper()
		var opt *MatchOptions
		if len(o) > 0 {
			opt = o[0]
		}
		if got := WildcardMatch(pattern, value, opt); got != want {
			t.Errorf("WildcardMatch(%q, %q) = %v, want %v", pattern, value, got, want)
		}
	}
	tw(t, f, "'*' pattern matches any value", func(t *testing.T) { w(t, "*", "anything", true); w(t, "*", "", true); w(t, "*", "bash", true) })
	tw(t, f, "'*' pattern matches values containing newlines", func(t *testing.T) { w(t, "*", "line1\nline2", true); w(t, "*", "a\nb\nc", true) })
	tw(t, f, "prefix-wildcard pattern matches value with embedded newlines", func(t *testing.T) {
		w(t, "node *", "node -e \"\nimport('x').then(() => {\n  console.log('done');\n});\n\"", true)
	})
	tw(t, f, "compileWildcardPattern matches a multiline string", func(t *testing.T) {
		eq(t, CompilePattern("*", "allow", nil).Matches("a\nb"), true, "multiline")
	})
	tw(t, f, "exact pattern matches identical value", func(t *testing.T) {
		w(t, "read", "read", true)
		w(t, "external_directory", "external_directory", true)
	})
	tw(t, f, "exact pattern does not match a different value", func(t *testing.T) {
		w(t, "read", "write", false)
		w(t, "read", "readonly", false)
		w(t, "read", "read ", false)
	})
	tw(t, f, "glob pattern matches with wildcard", func(t *testing.T) {
		w(t, "git *", "git status", true)
		w(t, "git *", "git push origin main", true)
		w(t, "git *", "npm install", false)
	})
	tw(t, f, "glob with no trailing space matches longer string", func(t *testing.T) {
		w(t, "git*", "git", true)
		w(t, "git*", "git status", true)
		w(t, "git*", "npm", false)
	})
	tw(t, f, "regex special characters in pattern are treated as literals", func(t *testing.T) {
		w(t, "tool.name", "tool.name", true)
		w(t, "tool.name", "toolXname", false)
	})
	tw(t, f, "'git *' matches bare 'git' (trailing space+wildcard is optional)", func(t *testing.T) { w(t, "git *", "git", true) })
	tw(t, f, "'git *' still matches 'git status' (existing behaviour preserved)", func(t *testing.T) { w(t, "git *", "git status", true) })
	tw(t, f, "'git *' still matches 'git status --short'", func(t *testing.T) { w(t, "git *", "git status --short", true) })
	tw(t, f, "'git *' does not match an unrelated command", func(t *testing.T) { w(t, "git *", "npm install", false) })
	tw(t, f, "'git status *' matches bare 'git status'", func(t *testing.T) { w(t, "git status *", "git status", true) })
	tw(t, f, "'git status *' matches 'git status --short'", func(t *testing.T) { w(t, "git status *", "git status --short", true) })
	tw(t, f, "non-trailing '*' is unaffected: 'g*t' does not match 'g' or 't'", func(t *testing.T) { w(t, "g*t", "g", false); w(t, "g*t", "t", false) })
	tw(t, f, "non-trailing '*' still matches when content is present: 'g*t' matches 'git'", func(t *testing.T) { w(t, "g*t", "git", true) })
	tw(t, f, "'git*' (no space) still matches bare 'git' — unchanged behaviour", func(t *testing.T) { w(t, "git*", "git", true) })
	tw(t, f, "'*' alone still matches everything", func(t *testing.T) { w(t, "*", "git", true); w(t, "*", "", true) })

	tw(t, f, "caseInsensitive matches a value differing only in case", func(t *testing.T) {
		w(t, `C:\Users\Foo\*`, `c:\users\foo\bar.md`, true, &MatchOptions{CaseInsensitive: true})
	})
	tw(t, f, "case folding is off by default", func(t *testing.T) { w(t, `C:\Users\Foo\*`, `c:\users\foo\bar.md`, false) })
	tw(t, f, "windowsSeparators matches a backslash value against a forward-slash pattern", func(t *testing.T) {
		w(t, "C:/Users/Foo/*", `C:\Users\Foo\bar.md`, true, &MatchOptions{WindowsSeparators: true})
	})
	tw(t, f, "separator normalization is off by default", func(t *testing.T) { w(t, "C:/Users/Foo/*", `C:\Users\Foo\bar.md`, false) })
	tw(t, f, "both options fold a mixed-case forward-slash pattern onto a lowercased backslash value", func(t *testing.T) {
		w(t, "C:/Users/Foo/AppData/Roaming/*", `c:\users\foo\appdata\roaming\npm\x.md`, true, win)
	})
	tw(t, f, "a forward-slash pattern matches a forward-slash value", func(t *testing.T) {
		w(t, "/dev/null", "/dev/null", true, &MatchOptions{WindowsSeparators: true})
	})
	tw(t, f, "a forward-slash glob matches a forward-slash device value", func(t *testing.T) { w(t, "/dev/*", "/dev/null", true, win) })
	tw(t, f, "a forward-slash relative pattern matches a forward-slash value", func(t *testing.T) {
		w(t, "src/*", "src/foo.ts", true, &MatchOptions{WindowsSeparators: true})
	})
	tw(t, f, "a backslash pattern matches a forward-slash value", func(t *testing.T) {
		w(t, `src\*`, "src/foo.ts", true, &MatchOptions{WindowsSeparators: true})
	})
	tw(t, f, "the value fold is off by default", func(t *testing.T) {
		w(t, `src\*`, "src/foo.ts", false)
		w(t, "/dev/null", `\dev\null`, false)
	})
	tw(t, f, "folding separators does not make unrelated values match", func(t *testing.T) {
		w(t, "/dev/null", "/dev/stdout", false, &MatchOptions{WindowsSeparators: true})
	})

	tw(t, f, "'?' matches exactly one character", func(t *testing.T) { w(t, "?", "a", true); w(t, "?", "Z", true); w(t, "?", "5", true) })
	tw(t, f, "'?' does not match zero characters", func(t *testing.T) { w(t, "?", "", false); w(t, "a?", "a", false) })
	tw(t, f, "'?' does not match two or more characters", func(t *testing.T) { w(t, "?", "ab", false); w(t, "?", "abc", false) })
	tw(t, f, "multiple '?' match exactly that many characters", func(t *testing.T) {
		w(t, "f??", "foo", true)
		w(t, "f??", "fo", false)
		w(t, "f??", "fooo", false)
	})
	tw(t, f, "'?' combined with '*'", func(t *testing.T) { w(t, "git?*", "git status", true); w(t, "git?*", "git", false) })
	tw(t, f, "'?' matches path separators and special characters", func(t *testing.T) {
		w(t, "a?b", "a/b", true)
		w(t, "a?b", "a.b", true)
		w(t, "a?b", "a\nb", true)
	})
	tw(t, f, "'?' in pattern matches literal '?' in value", func(t *testing.T) { w(t, "a?c", "a?c", true) })
	tw(t, f, "'?' in bash-style patterns", func(t *testing.T) {
		w(t, "git statu?", "git status", true)
		w(t, "git statu?", "git statux", true)
		w(t, "git statu?", "git statu", false)
	})

	tw(t, f, "wildcardMatch expands ~ prefix in pattern before matching", func(t *testing.T) {
		w(t, "~/dev/project", fakeHome+"/dev/project", true)
	})
	tw(t, f, "wildcardMatch expands ~/glob in pattern", func(t *testing.T) { w(t, "~/dev/*", fakeHome+"/dev/project/file.ts", true) })
	tw(t, f, "wildcardMatch ~/glob does not match a different home directory", func(t *testing.T) {
		w(t, "~/dev/*", "/other/user/dev/file.ts", false)
	})
	tw(t, f, "wildcardMatch expands $HOME prefix in pattern before matching", func(t *testing.T) {
		w(t, "$HOME/dev/project", fakeHome+"/dev/project", true)
	})
	tw(t, f, "wildcardMatch expands $HOME/glob in pattern", func(t *testing.T) { w(t, "$HOME/work/*", fakeHome+"/work/file.ts", true) })
	tw(t, f, "compileWildcardPattern retains original ~ pattern in .pattern field", func(t *testing.T) {
		eq(t, CompilePattern("~/dev/*", "allow", nil).Pattern, "~/dev/*", "pattern")
	})
	tw(t, f, "compileWildcardPattern retains original $HOME pattern in .pattern field", func(t *testing.T) {
		eq(t, CompilePattern("$HOME/dev/*", "allow", nil).Pattern, "$HOME/dev/*", "pattern")
	})
	tw(t, f, "compileWildcardPattern expanded pattern matches the expanded path", func(t *testing.T) {
		eq(t, CompilePattern("~/dev/*", "allow", nil).Matches(fakeHome+"/dev/file.ts"), true, "match")
	})
	tw(t, f, "non-home pattern is unaffected", func(t *testing.T) {
		w(t, "/absolute/path/*", "/absolute/path/file", true)
		w(t, "/absolute/path/*", "/other/file", false)
	})
}
