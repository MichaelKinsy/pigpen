package bashanalysis

import "testing"

const tcf = "access-intent/bash/token-classification"

func pathC(t *testing.T, token string) *string {
	t.Helper()
	if s, ok := ClassifyTokenAsPathCandidate(token); ok {
		return &s
	}
	return nil
}

func ruleC(t *testing.T, token string, f PathFlavor) *string {
	t.Helper()
	if s, ok := ClassifyTokenAsRuleCandidate(token, f); ok {
		return &s
	}
	return nil
}

func bareC(t *testing.T, token string) *string {
	t.Helper()
	if s, ok := ClassifyBareTokenCandidate(token); ok {
		return &s
	}
	return nil
}

func some(s string) *string { return &s }

// pathIs asserts that the strict classifier returns each token unchanged (want true) or null.
func pathIs(t *testing.T, want bool, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if want {
			eq(t, pathC(t, tok), some(tok), tok)
		} else {
			eq(t, pathC(t, tok), (*string)(nil), tok)
		}
	}
}

func ruleIs(t *testing.T, f PathFlavor, want bool, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if want {
			eq(t, ruleC(t, tok, f), some(tok), tok)
		} else {
			eq(t, ruleC(t, tok, f), (*string)(nil), tok)
		}
	}
}

func bareIs(t *testing.T, want bool, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if want {
			eq(t, bareC(t, tok), some(tok), tok)
		} else {
			eq(t, bareC(t, tok), (*string)(nil), tok)
		}
	}
}

func TestClassifyTokenAsPathCandidate(t *testing.T) {
	// shared rejection: rejectNonPathToken
	tw(t, tcf, "empty string → null", func(t *testing.T) { pathIs(t, false, "") })
	tw(t, tcf, "flag (leading dash) → null", func(t *testing.T) { pathIs(t, false, "-r", "--recursive") })
	tw(t, tcf, "env assignment (= before any /) → null", func(t *testing.T) { pathIs(t, false, "FOO=/bar", "HOME=/home/user") })
	tw(t, tcf, "env-like token where = comes after / is NOT rejected as assignment", func(t *testing.T) { pathIs(t, true, "/foo=bar") })
	tw(t, tcf, "URL → null", func(t *testing.T) {
		pathIs(t, false, "https://example.com", "http://localhost:3000", "file:///tmp/foo", "git+ssh://github.com/a/b")
	})
	tw(t, tcf, "@scope/package → null", func(t *testing.T) { pathIs(t, false, "@foo/bar", "@scope/pkg") })
	tw(t, tcf, "@/ prefix is NOT rejected (it looks like an absolute-rooted scoped path)", func(t *testing.T) { pathIs(t, false, "@/foo/bar") })
	tw(t, tcf, "shapeless regex-like token → null (acceptance gate, not the prelude)", func(t *testing.T) {
		pathIs(t, false, "foo.*", "bar.+", `a\|b`, `\(group\)`, "[abc]", "^/start")
	})
	// path-candidate acceptance gate
	tw(t, tcf, "absolute path (starts with /) → returned as-is", func(t *testing.T) { pathIs(t, true, "/etc/hosts", "/tmp", "/home/user/file.txt") })
	tw(t, tcf, "bare-slash token (filesystem root) → returned as-is", func(t *testing.T) { pathIs(t, true, "/", "//", "///") })
	tw(t, tcf, "home-relative path (starts with ~/) → returned as-is", func(t *testing.T) { pathIs(t, true, "~/Documents", "~/.ssh/config") })
	tw(t, tcf, "parent-traversal (a whole .. segment) → returned as-is", func(t *testing.T) {
		pathIs(t, true, "../../etc/passwd", "../foo", "..", "foo/..", "a/../b", `..\foo`, `foo\..`)
	})
	tw(t, tcf, ".. inside a segment (git revision ranges) → null", func(t *testing.T) {
		pathIs(t, false, "HEAD..origin/master", "cc83d7b48..origin/master", "v1..v2", "main...feature")
	})
	tw(t, tcf, "plain word with no path shape → null", func(t *testing.T) { pathIs(t, false, "hello", "myfile.txt") })
	tw(t, tcf, "dot-file (starts with .) → null (strict path gate)", func(t *testing.T) { pathIs(t, false, ".env", ".gitignore") })
	tw(t, tcf, "relative path with / but no leading / or ~/ → null (strict path gate)", func(t *testing.T) { pathIs(t, false, "src/foo.ts", "./build") })
	// Windows drive-letter
	tw(t, tcf, "forward-slash drive path → returned as-is", func(t *testing.T) { pathIs(t, true, "C:/Windows/win.ini", "D:/secrets/password.txt") })
	tw(t, tcf, "backslash drive path → returned as-is", func(t *testing.T) { pathIs(t, true, `C:\Windows\win.ini`, `D:\secrets\password.txt`) })
	tw(t, tcf, "lowercase drive letter → returned as-is", func(t *testing.T) { pathIs(t, true, "c:/foo") })
	tw(t, tcf, "single-letter scheme with double-slash (c://x) → null (URL_PATTERN fires first)", func(t *testing.T) { pathIs(t, false, "c://x") })
	tw(t, tcf, "drive-relative path without separator (C:foo) → null", func(t *testing.T) { pathIs(t, false, "C:foo") })
	// glob-bearing
	tw(t, tcf, "bracket glob in an absolute path → returned as-is", func(t *testing.T) { pathIs(t, true, "/etc/[p]asswd", "/et[c]/pa*") })
	tw(t, tcf, "dot-star glob in an absolute path → returned as-is", func(t *testing.T) { pathIs(t, true, "/tmp/tmp.*", "/var/log/sys.+") })
	tw(t, tcf, "bracket glob in a home-relative path → returned as-is", func(t *testing.T) { pathIs(t, true, "~/.ssh/[i]d_rsa") })
	tw(t, tcf, "bracket glob in a parent-traversal path → returned as-is", func(t *testing.T) { pathIs(t, true, "../[e]tc/passwd") })
}

func TestClassifyTokenAsRuleCandidate(t *testing.T) {
	px, wn := PosixPathFlavor, Win32PathFlavor
	tw(t, tcf, "empty string → null", func(t *testing.T) { ruleIs(t, px, false, "") })
	tw(t, tcf, "flag (leading dash) → null", func(t *testing.T) { ruleIs(t, px, false, "-r", "--recursive") })
	tw(t, tcf, "env assignment (= before any /) → null", func(t *testing.T) { ruleIs(t, px, false, "FOO=/bar", "HOME=/home/user") })
	tw(t, tcf, "env-like token where = comes after / is NOT rejected as assignment", func(t *testing.T) { ruleIs(t, px, true, "/foo=bar") })
	tw(t, tcf, "URL → null", func(t *testing.T) {
		ruleIs(t, px, false, "https://example.com", "http://localhost:3000", "file:///tmp/foo")
	})
	tw(t, tcf, "@scope/package → null", func(t *testing.T) { ruleIs(t, px, false, "@foo/bar", "@scope/pkg") })
	tw(t, tcf, "shapeless regex-like token → null (acceptance gate, not the prelude)", func(t *testing.T) {
		ruleIs(t, px, false, "foo.*", "bar.+", `a\|b`, "[abc]")
	})
	tw(t, tcf, "separator-bearing regex-like token → accepted for the rules to judge", func(t *testing.T) { ruleIs(t, px, true, "^/start") })
	tw(t, tcf, "absolute path (starts with /) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "/etc/hosts") })
	tw(t, tcf, "bare-slash token (filesystem root) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "/", "//") })
	tw(t, tcf, "home-relative path (starts with ~/) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "~/Documents") })
	tw(t, tcf, "parent-traversal (a whole .. segment) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "../foo", "..", `foo\..`) })
	tw(t, tcf, ".. inside a segment (git revision ranges) → not by the .. rule", func(t *testing.T) {
		ruleIs(t, px, false, "v1..v2", "main...feature")
		ruleIs(t, px, true, "HEAD..origin/master")
	})
	tw(t, tcf, "dot-file (starts with .) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, ".env", ".gitignore") })
	tw(t, tcf, "current-dir relative (starts with ./) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "./src", "./build/output.js") })
	tw(t, tcf, "relative path containing / → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "src/foo.ts", "packages/pi-foo/index.ts") })
	tw(t, tcf, "plain word with no path shape → null", func(t *testing.T) { ruleIs(t, px, false, "hello", "myfile.txt") })
	tw(t, tcf, "forward-slash drive path → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "C:/Windows/win.ini") })
	tw(t, tcf, "backslash drive path → returned as-is (new: no forward slash)", func(t *testing.T) {
		ruleIs(t, px, true, `D:\secrets\password.txt`, `C:\Windows\win.ini`)
	})
	tw(t, tcf, "lowercase drive letter (backslash) → returned as-is", func(t *testing.T) { ruleIs(t, px, true, `c:\foo`) })
	tw(t, tcf, "drive-relative path without separator (C:foo) → null", func(t *testing.T) { ruleIs(t, px, false, "C:foo") })
	tw(t, tcf, "bracket glob in a relative path → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "src/[s]ecret.env") })
	tw(t, tcf, "bracket glob in a dot-file → returned as-is", func(t *testing.T) { ruleIs(t, px, true, ".[e]nv") })
	tw(t, tcf, "dot-star glob in a relative path → returned as-is", func(t *testing.T) { ruleIs(t, px, true, "logs/app.*") })
	tw(t, tcf, "backslash-relative token accepted under the win32 flavor", func(t *testing.T) { ruleIs(t, wn, true, `dir\file`) })
	tw(t, tcf, "backslash-relative token rejected under the posix flavor", func(t *testing.T) { ruleIs(t, px, false, `dir\file`) })
	tw(t, tcf, "backslash-bearing token accepted under the win32 flavor, whatever else it contains", func(t *testing.T) {
		ruleIs(t, wn, true, `a\|b`, `\(group\)`)
	})
	tw(t, tcf, "backslash traversal accepted regardless of flavor (already via ..)", func(t *testing.T) {
		ruleIs(t, px, true, `..\secret`)
		ruleIs(t, wn, true, `..\secret`)
	})
	// The upstream titles interpolate the token; each loop runs under its template title.
	tw(t, tcf, `dot-file "${tok}": rule accepts, path rejects`, func(t *testing.T) {
		for _, tok := range []string{".env", ".gitignore", ".eslintrc"} {
			ruleIs(t, px, true, tok)
			pathIs(t, false, tok)
		}
	})
	tw(t, tcf, `relative path "${tok}": rule accepts, path rejects`, func(t *testing.T) {
		for _, tok := range []string{"src/index.ts", "lib/utils.js", "config/settings.json"} {
			ruleIs(t, px, true, tok)
			pathIs(t, false, tok)
		}
	})
	tw(t, tcf, `"${tok}": both classifiers accept`, func(t *testing.T) {
		for _, tok := range []string{"/etc/hosts", "~/docs", "../sibling"} {
			ruleIs(t, px, true, tok)
			pathIs(t, true, tok)
		}
	})
	tw(t, tcf, `Windows drive path "${tok}": both classifiers accept`, func(t *testing.T) {
		for _, tok := range []string{"C:/Windows/win.ini", `D:\secrets\password.txt`, "c:/foo"} {
			ruleIs(t, px, true, tok)
			pathIs(t, true, tok)
		}
	})
	tw(t, tcf, `"${tok}": both classifiers reject`, func(t *testing.T) {
		for _, tok := range []string{"hello", "--flag", "FOO=/bar", "https://x.com"} {
			ruleIs(t, px, false, tok)
			pathIs(t, false, tok)
		}
	})
}

func TestClassifyBareTokenCandidate(t *testing.T) {
	tw(t, tcf, "bare word → returned unchanged", func(t *testing.T) { bareIs(t, true, "id_rsa", "key.pem", "outside-link") })
	tw(t, tcf, "bare word that names no file is still returned — existence is not its question", func(t *testing.T) {
		bareIs(t, true, "status", "build")
	})
	tw(t, tcf, "consults no policy — identical result for every token of the same shape", func(t *testing.T) { bareIs(t, true, "anything") })
	tw(t, tcf, "glob-bearing bare token → returned for the probe to settle (#821)", func(t *testing.T) { bareIs(t, true, "[a]bc", "tmp.*") })
	tw(t, tcf, "flag (leading dash) → null", func(t *testing.T) { bareIs(t, false, "-r", "--recursive") })
	tw(t, tcf, "env assignment → null", func(t *testing.T) { bareIs(t, false, "FOO=/bar") })
	tw(t, tcf, "URL → null", func(t *testing.T) { bareIs(t, false, "https://example.com") })
	tw(t, tcf, "@scope/package → null", func(t *testing.T) { bareIs(t, false, "@foo/bar") })
	tw(t, tcf, "metacharacters are not a rejection — the probe settles it (#821)", func(t *testing.T) { bareIs(t, true, "foo.*") })
	tw(t, tcf, "empty string → null", func(t *testing.T) { bareIs(t, false, "") })
}
