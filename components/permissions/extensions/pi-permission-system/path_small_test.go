package pi_permission_system

import (
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// Twins of test/access-intent/bash/msys-bash-tokens, test/path/{safe-system-paths,path-containment,canonicalize-path,
// expand-home,node-modules-discovery,approval-pattern}, test/types, test/value-guards and test/permission-request-id.

func TestMsysBashTokensTwins(t *testing.T) {
	const f = "access-intent/bash/msys-bash-tokens"
	for _, tok := range []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"} {
		eqv(t, ClassifyWin32BashToken(tok), BashTokenShape{Kind: BashTokenDevice})
	}
	dm := func(p string) BashTokenShape { return BashTokenShape{Kind: BashTokenDriveMount, WindowsPath: p} }
	tw(t, f, `translates /c/Users/x to C:\Users\x`, func(t *testing.T) { eqv(t, ClassifyWin32BashToken("/c/Users/x"), dm(`C:\Users\x`)) })
	tw(t, f, "uppercases the drive letter", func(t *testing.T) { eqv(t, ClassifyWin32BashToken("/d/secrets/pw.txt"), dm(`D:\secrets\pw.txt`)) })
	tw(t, f, "accepts an already-uppercase mount letter", func(t *testing.T) { eqv(t, ClassifyWin32BashToken("/C/x"), dm(`C:\x`)) })
	tw(t, f, `bare /c translates to the drive root C:\`, func(t *testing.T) { eqv(t, ClassifyWin32BashToken("/c"), dm(`C:\`)) })
	tw(t, f, `trailing-slash /c/ translates to the drive root C:\`, func(t *testing.T) { eqv(t, ClassifyWin32BashToken("/c/"), dm(`C:\`)) })
	for _, tok := range []string{"/tmp/foo", "/usr/bin", "/etc/hosts", "/mingw64/bin"} {
		eqv(t, ClassifyWin32BashToken(tok), BashTokenShape{Kind: BashTokenPosixAbsolute})
	}
	tw(t, f, "a two-letter first segment is not a drive mount", func(t *testing.T) {
		eqv(t, ClassifyWin32BashToken("/cc/x"), BashTokenShape{Kind: BashTokenPosixAbsolute})
	})
	for _, tok := range []string{"src/foo.ts", "foo.ts", `C:\Users\x`, "C:/Users/x", "../up"} {
		eqv(t, ClassifyWin32BashToken(tok), BashTokenShape{Kind: BashTokenPlain})
	}
	// A line terminator stops the regex's `.`, and `$` matches only the end: not a mount.
	eqv(t, ClassifyWin32BashToken("/c/a\nb"), BashTokenShape{Kind: BashTokenPosixAbsolute})
}

func TestSafeSystemPathsTwins(t *testing.T) {
	const f = "path/safe-system-paths"
	tw(t, f, "contains /dev/null, /dev/stdin, /dev/stdout, /dev/stderr", func(t *testing.T) {
		for _, p := range []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"} {
			eqv(t, SafeSystemPaths[p], true)
		}
	})
	tw(t, f, "returns true for /dev/null", func(t *testing.T) { eqv(t, IsSafeSystemPath("/dev/null"), true) })
	tw(t, f, "returns true for /dev/stdin", func(t *testing.T) { eqv(t, IsSafeSystemPath("/dev/stdin"), true) })
	tw(t, f, "returns true for /dev/stdout", func(t *testing.T) { eqv(t, IsSafeSystemPath("/dev/stdout"), true) })
	tw(t, f, "returns true for /dev/stderr", func(t *testing.T) { eqv(t, IsSafeSystemPath("/dev/stderr"), true) })
	tw(t, f, "returns false for an arbitrary absolute path", func(t *testing.T) { eqv(t, IsSafeSystemPath("/etc/passwd"), false) })
	tw(t, f, "returns false for a path prefixed with a safe system path", func(t *testing.T) {
		eqv(t, IsSafeSystemPath("/dev/null/subdir"), false)
	})
	tw(t, f, "returns false for an empty string", func(t *testing.T) { eqv(t, IsSafeSystemPath(""), false) })
	tw(t, f, "returns false for a relative path", func(t *testing.T) { eqv(t, IsSafeSystemPath("dev/null"), false) })
}

func TestPathContainmentTwins(t *testing.T) {
	const f = "path/path-containment"
	cwd := "/projects/my-app"
	out := func(p, c string) bool { return IsPathOutsideWorkingDirectory(p, c, PosixPathFlavor) }
	tw(t, f, "does not canonicalize its operands (no filesystem access)", func(t *testing.T) {
		calls := 0
		mockRealpath(t, func(p string) (string, error) { calls++; return p, nil })
		out("/projects/my-app/src", cwd)
		eqv(t, calls, 0)
	})
	tw(t, f, "returns false when path is inside cwd", func(t *testing.T) { eqv(t, out("/projects/my-app/src", cwd), false) })
	tw(t, f, "returns false when path equals cwd", func(t *testing.T) { eqv(t, out("/projects/my-app", cwd), false) })
	tw(t, f, "returns true when path is outside cwd", func(t *testing.T) { eqv(t, out("/etc/passwd", cwd), true) })
	tw(t, f, "returns false for an empty canonical path", func(t *testing.T) { eqv(t, out("", cwd), false) })
	tw(t, f, "returns false for an empty canonical cwd", func(t *testing.T) { eqv(t, out("/etc/passwd", ""), false) })
	tw(t, f, "returns false for /dev/null (safe system path)", func(t *testing.T) { eqv(t, out("/dev/null", cwd), false) })
	tw(t, f, "returns false for /dev/stdin (safe system path)", func(t *testing.T) { eqv(t, out("/dev/stdin", cwd), false) })
	tw(t, f, "returns false for /dev/stdout (safe system path)", func(t *testing.T) { eqv(t, out("/dev/stdout", cwd), false) })
	tw(t, f, "returns false for /dev/stderr (safe system path)", func(t *testing.T) { eqv(t, out("/dev/stderr", cwd), false) })
	tw(t, f, "returns true for /dev/null/subdir (not a safe path)", func(t *testing.T) { eqv(t, out("/dev/null/subdir", cwd), true) })
}

func TestCanonicalizePathTwins(t *testing.T) {
	const f = "path/canonicalize-path"
	px, wn := PosixPathFlavor, Win32PathFlavor
	// seq mocks realpathSync with a queue of results (mockImplementationOnce / mockReturnValueOnce), then the fallback.
	type step struct {
		path string
		err  error
	}
	seq := func(t *testing.T, steps []step, fallback func(string) (string, error)) {
		i := 0
		mockRealpath(t, func(p string) (string, error) {
			if i < len(steps) {
				s := steps[i]
				i++
				return s.path, s.err
			}
			if fallback != nil {
				return fallback(p)
			}
			return "", nil
		})
	}
	tw(t, f, "returns empty string for empty input", func(t *testing.T) { eqv(t, CanonicalizePath("", px), "") })
	tw(t, f, "returns realpathSync result when path exists", func(t *testing.T) {
		seq(t, []step{{"/real/projects/app", nil}}, nil)
		eqv(t, CanonicalizePath("/projects/link", px), "/real/projects/app")
	})
	tw(t, f, "re-appends a non-existent leaf to the canonical parent", func(t *testing.T) {
		seq(t, []step{{"", errno(syscall.ENOENT, "/projects/app/new-file.ts")}, {"/canonical/app", nil}}, nil)
		eqv(t, CanonicalizePath("/projects/app/new-file.ts", px), "/canonical/app/new-file.ts")
	})
	tw(t, f, "walks up multiple levels for a deeply non-existent path", func(t *testing.T) {
		seq(t, []step{{"", errno(syscall.ENOENT, "a")}, {"", errno(syscall.ENOENT, "b")}, {"", errno(syscall.ENOENT, "c")}, {"/canonical/projects", nil}}, nil)
		eqv(t, CanonicalizePath("/projects/app/src/new-file.ts", px), "/canonical/projects/app/src/new-file.ts")
	})
	tw(t, f, "returns input unchanged when walk reaches filesystem root (all ENOENT)", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) { return "", errno(syscall.ENOENT, "") })
		eqv(t, CanonicalizePath("/nonexistent/path/file.ts", px), "/nonexistent/path/file.ts")
	})
	tw(t, f, "returns input unchanged on ELOOP (symlink loop)", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) { return "", errno(syscall.ELOOP, p) })
		eqv(t, CanonicalizePath("/some/looping/path", px), "/some/looping/path")
	})
	tw(t, f, "returns input unchanged on EACCES (permission denied)", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) { return "", errno(syscall.EACCES, p) })
		eqv(t, CanonicalizePath("/restricted/path", px), "/restricted/path")
	})
	tw(t, f, "handles ENOTDIR by walking up (like ENOENT)", func(t *testing.T) {
		seq(t, []step{{"", errno(syscall.ENOTDIR, "x")}, {"/real/parent", nil}}, nil)
		eqv(t, CanonicalizePath("/real/parent/not-a-dir", px), "/real/parent/not-a-dir")
	})
	tw(t, f, "win32: splits and rejoins on the backslash separator", func(t *testing.T) {
		seq(t, []step{{"", errno(syscall.ENOENT, "x")}, {`C:\real\app`, nil}}, nil)
		eqv(t, CanonicalizePath(`C:\projects\link\file.ts`, wn), `C:\real\app\file.ts`)
	})
	tw(t, f, "win32: resolves an existing path via realpathSync", func(t *testing.T) {
		seq(t, []step{{`C:\real\app`, nil}}, nil)
		eqv(t, CanonicalizePath(`C:\projects\link`, wn), `C:\real\app`)
	})
}

func TestExpandHomeTwins(t *testing.T) {
	const f = "path/expand-home"
	mockHome(t, fakeHome)
	j := func(rest string) string { return HostPath.Join(fakeHome, rest) }
	tw(t, f, "bare ~ expands to homedir()", func(t *testing.T) { eqv(t, expandHomePath("~"), fakeHome) })
	tw(t, f, "~/path expands to homedir()/path", func(t *testing.T) { eqv(t, expandHomePath("~/dev/project"), j("dev/project")) })
	tw(t, f, "~/path/* expands to homedir()/path/*", func(t *testing.T) { eqv(t, expandHomePath("~/dev/*"), j("dev/*")) })
	tw(t, f, `~\ (Windows separator) expands to homedir() + rest`, func(t *testing.T) { eqv(t, expandHomePath(`~\dev\project`), j(`dev\project`)) })
	tw(t, f, "~username (no separator) is not expanded (no-op)", func(t *testing.T) { eqv(t, expandHomePath("~username"), "~username") })
	tw(t, f, "bare $HOME expands to homedir()", func(t *testing.T) { eqv(t, expandHomePath("$HOME"), fakeHome) })
	tw(t, f, "$HOME/path expands to homedir()/path", func(t *testing.T) { eqv(t, expandHomePath("$HOME/dev/project"), j("dev/project")) })
	tw(t, f, "$HOME/path/* expands to homedir()/path/*", func(t *testing.T) { eqv(t, expandHomePath("$HOME/dev/*"), j("dev/*")) })
	tw(t, f, `$HOME\ (Windows separator) expands to homedir() + rest`, func(t *testing.T) { eqv(t, expandHomePath(`$HOME\dev\project`), j(`dev\project`)) })
	tw(t, f, "$HOMEDIR (no separator) is not expanded (no-op)", func(t *testing.T) { eqv(t, expandHomePath("$HOMEDIR"), "$HOMEDIR") })
	tw(t, f, "bare ${HOME} expands to homedir()", func(t *testing.T) { eqv(t, expandHomePath("${HOME}"), fakeHome) })
	tw(t, f, "${HOME}/path expands to homedir()/path", func(t *testing.T) { eqv(t, expandHomePath("${HOME}/dev/project"), j("dev/project")) })
	tw(t, f, "${HOME}/path/* expands to homedir()/path/*", func(t *testing.T) { eqv(t, expandHomePath("${HOME}/dev/*"), j("dev/*")) })
	tw(t, f, `${HOME}\ (Windows separator) expands to homedir() + rest`, func(t *testing.T) { eqv(t, expandHomePath(`${HOME}\dev\project`), j(`dev\project`)) })
	tw(t, f, "${HOMEDIR} is not expanded (no-op)", func(t *testing.T) { eqv(t, expandHomePath("${HOMEDIR}"), "${HOMEDIR}") })
	tw(t, f, "${HOME:-/tmp} (parameter expansion with an operator) is not expanded", func(t *testing.T) { eqv(t, expandHomePath("${HOME:-/tmp}/x"), "${HOME:-/tmp}/x") })
	tw(t, f, "${HOME (unterminated brace) is not expanded", func(t *testing.T) { eqv(t, expandHomePath("${HOME/dev"), "${HOME/dev") })
	tw(t, f, "absolute path is unchanged", func(t *testing.T) { eqv(t, expandHomePath("/usr/local/bin"), "/usr/local/bin") })
	tw(t, f, "relative path is unchanged", func(t *testing.T) { eqv(t, expandHomePath("dev/project"), "dev/project") })
	tw(t, f, "glob-only pattern is unchanged", func(t *testing.T) { eqv(t, expandHomePath("*"), "*") })
	tw(t, f, "empty string is unchanged", func(t *testing.T) { eqv(t, expandHomePath(""), "") })
	tw(t, f, "bash command pattern starting with a word is unchanged", func(t *testing.T) { eqv(t, expandHomePath("git push *"), "git push *") })
	eqv(t, hasHomePrefix("~/x"), true)
	eqv(t, hasHomePrefix("~x"), false)
}

func TestNodeModulesDiscoveryTwins(t *testing.T) {
	const f = "path/node-modules-discovery"
	mockSpawn := func(t *testing.T, status int, stdout string, ok bool, calls *int) {
		old, oldExists := spawnNpmRootGlobal, existsSync
		spawnNpmRootGlobal = func() (int, string, bool) {
			if calls != nil {
				*calls++
			}
			return status, stdout, ok
		}
		t.Cleanup(func() { spawnNpmRootGlobal, existsSync = old, oldExists })
	}
	mockExists := func(v bool) { existsSync = func(string) bool { return v } }
	if HostPath == Win32Path {
		t.Skip("the URLs below are POSIX paths")
	}
	tw(t, f, "returns node_modules root when URL is inside a node_modules tree", func(t *testing.T) {
		calls := 0
		mockSpawn(t, 0, "", true, &calls)
		got, ok := DiscoverGlobalNodeModulesRoot("file:///opt/homebrew/lib/node_modules/@gotgenes/pi-permission-system/dist/external-directory.js")
		eqv(t, ok, true)
		eqv(t, got, "/opt/homebrew/lib/node_modules")
		eqv(t, calls, 0)
	})
	dev := "file:///Users/dev/my-project/src/external-directory.ts"
	tw(t, f, "calls npm root -g as fallback when walk-up finds no node_modules ancestor", func(t *testing.T) {
		calls := 0
		mockSpawn(t, 0, "/opt/homebrew/lib/node_modules\n", true, &calls)
		mockExists(true)
		got, ok := DiscoverGlobalNodeModulesRoot(dev)
		eqv(t, calls, 1)
		eqv(t, ok, true)
		eqv(t, got, "/opt/homebrew/lib/node_modules")
	})
	tw(t, f, "returns null when walk-up fails and npm root -g returns non-zero exit", func(t *testing.T) {
		mockSpawn(t, 1, "", true, nil)
		_, ok := DiscoverGlobalNodeModulesRoot(dev)
		eqv(t, ok, false)
	})
	tw(t, f, "returns null when walk-up fails and spawnSync throws", func(t *testing.T) {
		mockSpawn(t, 0, "", false, nil)
		_, ok := DiscoverGlobalNodeModulesRoot(dev)
		eqv(t, ok, false)
	})
	tw(t, f, "returns null when walk-up fails and npm root -g returns non-existent path", func(t *testing.T) {
		mockSpawn(t, 0, "/some/nonexistent/node_modules\n", true, nil)
		mockExists(false)
		_, ok := DiscoverGlobalNodeModulesRoot(dev)
		eqv(t, ok, false)
	})
	tw(t, f, "returns null when walk-up fails and npm root -g returns empty stdout", func(t *testing.T) {
		mockSpawn(t, 0, "   ", true, nil)
		_, ok := DiscoverGlobalNodeModulesRoot(dev)
		eqv(t, ok, false)
	})
}

func TestApprovalPatternTwins(t *testing.T) {
	const f = "path/approval-pattern"
	px, wn := PosixPathFlavor, Win32PathFlavor
	// grantFor records the derived patterns as session grants, as the gates do.
	grantFor := func(surface, pathValue string, flavor PathFlavor, isDir bool) Ruleset {
		var rs Ruleset
		for _, p := range DeriveApprovalPatterns(pathValue, flavor, isDir) {
			rs = append(rs, Rule{Surface: surface, Pattern: p, Action: "allow", Origin: "session"})
		}
		return rs
	}
	action := func(surface, value string, rs Ruleset, flavor PathFlavor) string {
		return Evaluate(surface, value, rs, flavor, "ask").Action
	}
	for _, c := range [][2]string{{"/other/project/src/foo.ts", "/other/project/src/*"}, {"/other/project/src/", "/other/project/src/*"},
		{"/other/project/src", "/other/project/*"}, {"/", "/*"}, {"/foo", "/*"}, {"/dev/null", "/dev/*"}, {"C:/foo/bar.ts", "C:/foo/*"}, {"src/.env", "src/*"}} {
		eqs(t, DeriveApprovalPatterns(c[0], px, false), c[1])
	}
	tw(t, f, "treats a backslash as an ordinary filename character", func(t *testing.T) {
		eqs(t, DeriveApprovalPatterns(`C:\foo\bar.ts`, px, false), "./*")
	})
	tw(t, f, "falls back to the current directory for a separator-free value", func(t *testing.T) {
		eqs(t, DeriveApprovalPatterns("index.html", px, false), "./*")
		eqs(t, DeriveApprovalPatterns("", px, false), "./*")
	})
	for _, c := range [][2]string{{`C:\foo\bar.ts`, `C:\foo\*`}, {`C:\`, `C:\*`}, {"C:/foo/bar.ts", "C:/foo/*"},
		{"/dev/null", "/dev/*"}, {"/tmp/logs/", "/tmp/logs/*"}, {"/tmp/logs", "/tmp/*"}, {"/foo", "/*"}, {"/", "/*"}} {
		eqs(t, DeriveApprovalPatterns(c[0], wn, false), c[1])
	}
	tw(t, f, "falls back to the windows current directory for a separator-free value", func(t *testing.T) {
		eqs(t, DeriveApprovalPatterns("index.html", wn, false), `.\*`)
	})
	eqs(t, DeriveApprovalPatterns("/r/projects/project-a", px, true), "/r/projects/project-a", "/r/projects/project-a/*")
	eqs(t, DeriveApprovalPatterns("/", px, true), "/", "/*")
	eqs(t, DeriveApprovalPatterns(`C:\r\a`, wn, true), `C:\r\a`, `C:\r\a\*`)
	eqs(t, DeriveApprovalPatterns(`C:\`, wn, true), `C:\`, `C:\*`)
	eqs(t, DeriveApprovalPatterns("C:/r/a", wn, true), "C:/r/a", "C:/r/a/*")

	tw(t, f, "grants an approved directory and its contents, not its siblings", func(t *testing.T) {
		s := grantFor("external_directory_read", "/r/projects/project-a", px, true)
		a := func(v string) string { return action("external_directory_read", v, s, px) }
		eqv(t, a("/r/projects/project-a"), "allow")
		eqv(t, a("/r/projects/project-a/src/x.ts"), "allow")
		eqv(t, a("/r/projects/project-b/sample.txt"), "ask")
		eqv(t, a("/r/projects/project-a-evil/x.ts"), "ask")
	})
	tw(t, f, "grants siblings of the approved file", func(t *testing.T) {
		s := grantFor("external_directory_read", "/other/project/src/foo.ts", px, false)
		eqv(t, action("external_directory_read", "/other/project/src/bar.ts", s, px), "allow")
	})
	tw(t, f, "does not grant sibling directories", func(t *testing.T) {
		s := grantFor("external_directory_read", "/other/project/src/foo.ts", px, false)
		eqv(t, action("external_directory_read", "/other/project/lib/bar.ts", s, px), "ask")
	})
	tw(t, f, "binds a current-directory file to the cwd subtree once resolved", func(t *testing.T) {
		s := grantFor("edit", "/test/project/index.html", px, false)
		eqv(t, action("edit", "/test/project/index.html", s, px), "allow")
		eqv(t, action("edit", "/etc/passwd", s, px), "ask")
	})
	tw(t, f, "bounds a win32 directory token to itself, not its parent", func(t *testing.T) {
		s := grantFor("external_directory_read", "/tmp/logs/", wn, false)
		eqv(t, action("external_directory_read", "/tmp/logs/app.log", s, wn), "allow")
		eqv(t, action("external_directory_read", "/tmp/other/secrets.env", s, wn), "ask")
	})
}

func TestTypesTwins(t *testing.T) {
	const f = "types"
	tw(t, f, "returns true for 'allow'", func(t *testing.T) { eqv(t, IsPermissionState("allow"), true) })
	tw(t, f, "returns true for 'deny'", func(t *testing.T) { eqv(t, IsPermissionState("deny"), true) })
	tw(t, f, "returns true for 'ask'", func(t *testing.T) { eqv(t, IsPermissionState("ask"), true) })
	tw(t, f, "returns false for unrecognized strings", func(t *testing.T) {
		for _, s := range []string{"ALLOW", "permit", "", "block"} {
			eqv(t, IsPermissionState(s), false)
		}
	})
	tw(t, f, "returns false for non-string types", func(t *testing.T) {
		for _, v := range []any{nil, 1.0, newObject()} {
			eqv(t, IsPermissionState(v), false)
		}
	})
	tw(t, f, "returns true for { action: 'deny' } without a reason", func(t *testing.T) { eqv(t, IsDenyWithReason(jo(t, `{"action":"deny"}`)), true) })
	tw(t, f, "returns true for { action: 'deny', reason: '...' }", func(t *testing.T) {
		eqv(t, IsDenyWithReason(jo(t, `{"action":"deny","reason":"Use pnpm"}`)), true)
	})
	tw(t, f, "returns false for non-deny actions", func(t *testing.T) {
		eqv(t, IsDenyWithReason(jo(t, `{"action":"allow"}`)), false)
		eqv(t, IsDenyWithReason(jo(t, `{"action":"ask"}`)), false)
	})
	tw(t, f, "returns false for a non-string reason", func(t *testing.T) {
		eqv(t, IsDenyWithReason(jo(t, `{"action":"deny","reason":42}`)), false)
		eqv(t, IsDenyWithReason(jo(t, `{"action":"deny","reason":null}`)), false)
	})
	tw(t, f, "returns false for non-object types", func(t *testing.T) {
		for _, v := range []any{nil, "deny", []any{"deny"}} {
			eqv(t, IsDenyWithReason(v), false)
		}
	})
}

func TestValueGuardsTwins(t *testing.T) {
	const f = "value-guards"
	empty := func(v any) bool { r := ToRecord(v); return len(r.keys) == 0 }
	tw(t, f, "returns empty object for null", func(t *testing.T) { eqv(t, empty(nil), true) })
	tw(t, f, "returns empty object for undefined", func(t *testing.T) { eqv(t, empty(undef{}), true) })
	tw(t, f, "returns empty object for a string", func(t *testing.T) { eqv(t, empty("hello"), true) })
	tw(t, f, "returns empty object for a number", func(t *testing.T) { eqv(t, empty(42.0), true) })
	tw(t, f, "returns empty object for an array", func(t *testing.T) { eqv(t, empty([]any{"a", "b"}), true) })
	tw(t, f, "returns the object itself for a plain object", func(t *testing.T) {
		in := jo(t, `{"a":1,"b":"two"}`)
		eqv(t, ToRecord(in), in)
	})
	tw(t, f, "returns the object for a nested object", func(t *testing.T) {
		in := jo(t, `{"x":{"y":3}}`)
		eqv(t, ToRecord(in), in)
	})
	tw(t, f, "returns null for non-string values", func(t *testing.T) {
		for _, v := range []any{nil, undef{}, 42.0, newObject(), []any{}} {
			_, ok := GetNonEmptyString(v)
			eqv(t, ok, false)
		}
	})
	tw(t, f, "returns null for empty string", func(t *testing.T) { _, ok := GetNonEmptyString(""); eqv(t, ok, false) })
	tw(t, f, "returns null for whitespace-only string", func(t *testing.T) {
		for _, s := range []string{"   ", "\t\n"} {
			_, ok := GetNonEmptyString(s)
			eqv(t, ok, false)
		}
	})
	tw(t, f, "returns trimmed string for valid string", func(t *testing.T) {
		s, _ := GetNonEmptyString("hello")
		eqv(t, s, "hello")
		s, _ = GetNonEmptyString("  hello  ")
		eqv(t, s, "hello")
	})
	tw(t, f, "returns single non-whitespace character", func(t *testing.T) { s, ok := GetNonEmptyString("a"); eqv(t, s, "a"); eqv(t, ok, true) })
	eqv(t, IsPlainRecord(newObject()), true)
	eqv(t, IsPlainRecord([]any{}), false)
}

func TestPermissionRequestIdTwins(t *testing.T) {
	const f = "permission-request-id"
	tw(t, f, "prefixes the id so it is distinguishable from an SDK tool call id", func(t *testing.T) {
		if !regexp.MustCompile(`^perm-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(CreatePermissionRequestId()) {
			t.Error("bad id")
		}
	})
	tw(t, f, "mints a distinct id on every call", func(t *testing.T) {
		ids := map[string]bool{}
		for range 100 {
			ids[CreatePermissionRequestId()] = true
		}
		eqv(t, len(ids), 100)
		eqv(t, strings.HasPrefix(CreatePermissionRequestId(), "perm-"), true)
	})
}
