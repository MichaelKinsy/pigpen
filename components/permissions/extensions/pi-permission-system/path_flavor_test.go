package pi_permission_system

import "testing"

// Twins of test/path/path-flavor.test.ts.

func TestPathFlavorTwins(t *testing.T) {
	const f = "path/path-flavor"
	w, p := Win32PathFlavor, PosixPathFlavor
	t.Run("win32PathFlavor", func(t *testing.T) {
		tw(t, f, "exposes the win32 path implementation", func(t *testing.T) {
			if w.Impl != Win32Path {
				t.Error("impl is not win32")
			}
		})
		tw(t, f, "carries the win32 case/separator match options", func(t *testing.T) {
			if w.MatchOptions == nil || *w.MatchOptions != (MatchOptions{CaseInsensitive: true, WindowsSeparators: true}) {
				t.Errorf("%+v", w.MatchOptions)
			}
		})
		tw(t, f, "folds to lower case", func(t *testing.T) { eqv(t, w.Fold(`C:\Foo\Bar`), `c:\foo\bar`) })
		tw(t, f, "resolves, normalizes, and folds a comparable value against a base", func(t *testing.T) {
			eqv(t, w.Comparable("Foo/Bar", `C:\base`), `c:\base\foo\bar`)
		})
		tw(t, f, "decides containment with win32 (case-folding) geometry", func(t *testing.T) {
			eqv(t, w.IsWithin(`C:\base\sub`, `C:\base`), true)
			eqv(t, w.IsWithin(`C:\base`, `C:\base`), true)
			eqv(t, w.IsWithin(`C:\other`, `C:\base`), false)
		})
		tw(t, f, "folds case for a case-different descendant", func(t *testing.T) {
			eqv(t, w.IsWithin(`c:\users\foo\dir\sub\x.md`, `C:\Users\Foo\dir`), true)
		})
		tw(t, f, "folds case when path equals directory in a different case", func(t *testing.T) {
			eqv(t, w.IsWithin(`c:\users\foo\dir\sub`, `C:\USERS\foo\DIR`), true)
		})
		tw(t, f, "rejects a win32 sibling directory", func(t *testing.T) {
			eqv(t, w.IsWithin(`C:\Users\Foo\other`, `C:\Users\Foo\dir`), false)
		})
		tw(t, f, "recognizes either separator as a path separator", func(t *testing.T) {
			eqv(t, w.HasPathSeparator("dir/file"), true)
			eqv(t, w.HasPathSeparator(`dir\file`), true)
			eqv(t, w.HasPathSeparator("plain"), false)
		})
		tw(t, f, "locates the last separator of either spelling", func(t *testing.T) {
			eqv(t, w.LastSeparatorIndex(`C:\a\b`), 4)
			eqv(t, w.LastSeparatorIndex("/a/b"), 2)
			eqv(t, w.LastSeparatorIndex(`C:\a/b`), 4)
			eqv(t, w.LastSeparatorIndex(`dir\file`), 3)
			eqv(t, w.LastSeparatorIndex("/"), 0)
			eqv(t, w.LastSeparatorIndex("plain"), -1)
			eqv(t, w.LastSeparatorIndex(""), -1)
		})
		tw(t, f, "answers hasPathSeparator consistently with lastSeparatorIndex", func(t *testing.T) {
			for _, tok := range []string{"dir/file", `dir\file`, "plain", "", "/"} {
				eqv(t, w.HasPathSeparator(tok), w.LastSeparatorIndex(tok) >= 0)
			}
		})
		tw(t, f, "classifies bash tokens with MSYS semantics", func(t *testing.T) {
			eqv(t, w.BashTokenShape("/dev/null"), BashTokenShape{Kind: BashTokenDevice})
			eqv(t, w.BashTokenShape("/c/Users/x"), BashTokenShape{Kind: BashTokenDriveMount, WindowsPath: `C:\Users\x`})
			eqv(t, w.BashTokenShape("/tmp/x"), BashTokenShape{Kind: BashTokenPosixAbsolute})
			eqv(t, w.BashTokenShape("relative/x"), BashTokenShape{Kind: BashTokenPlain})
		})
	})
	t.Run("posixPathFlavor", func(t *testing.T) {
		tw(t, f, "exposes the posix path implementation", func(t *testing.T) {
			if p.Impl != PosixPath {
				t.Error("impl is not posix")
			}
		})
		tw(t, f, "carries no win32 match options", func(t *testing.T) {
			if p.MatchOptions != nil {
				t.Error("match options set")
			}
		})
		tw(t, f, "does not fold case", func(t *testing.T) { eqv(t, p.Fold("/Foo/Bar"), "/Foo/Bar") })
		tw(t, f, "resolves and normalizes a comparable value without folding", func(t *testing.T) {
			eqv(t, p.Comparable("Foo/Bar", "/base"), "/base/Foo/Bar")
		})
		tw(t, f, "decides containment with posix geometry", func(t *testing.T) {
			eqv(t, p.IsWithin("/base/sub", "/base"), true)
			eqv(t, p.IsWithin("/base", "/base"), true)
			eqv(t, p.IsWithin("/a/b/c/d/e", "/a/b"), true)
			eqv(t, p.IsWithin("/other", "/base"), false)
		})
		tw(t, f, "rejects a sibling directory sharing a name prefix", func(t *testing.T) { eqv(t, p.IsWithin("/a/bc", "/a/b"), false) })
		tw(t, f, "stays case-sensitive", func(t *testing.T) { eqv(t, p.IsWithin("/a/B/c", "/a/b"), false) })
		tw(t, f, "returns false for empty operands", func(t *testing.T) {
			eqv(t, p.IsWithin("", "/a/b"), false)
			eqv(t, p.IsWithin("/a/b", ""), false)
		})
		tw(t, f, "recognizes only the forward slash as a path separator", func(t *testing.T) {
			eqv(t, p.HasPathSeparator("dir/file"), true)
			eqv(t, p.HasPathSeparator(`dir\file`), false)
			eqv(t, p.HasPathSeparator("plain"), false)
		})
		tw(t, f, "locates the last forward slash only", func(t *testing.T) {
			eqv(t, p.LastSeparatorIndex("/a/b"), 2)
			eqv(t, p.LastSeparatorIndex(`dir\file`), -1)
			eqv(t, p.LastSeparatorIndex("/"), 0)
			eqv(t, p.LastSeparatorIndex("plain"), -1)
			eqv(t, p.LastSeparatorIndex(""), -1)
		})
		tw(t, f, "answers hasPathSeparator consistently with lastSeparatorIndex", func(t *testing.T) {
			for _, tok := range []string{"dir/file", `dir\file`, "plain", "", "/"} {
				eqv(t, p.HasPathSeparator(tok), p.LastSeparatorIndex(tok) >= 0)
			}
		})
		tw(t, f, "treats every bash token as an ordinary path", func(t *testing.T) {
			for _, tok := range []string{"/dev/null", "/c/Users/x", "/tmp/x"} {
				eqv(t, p.BashTokenShape(tok), BashTokenShape{Kind: BashTokenPlain})
			}
		})
	})
	t.Run("pathFlavorForPlatform", func(t *testing.T) {
		tw(t, f, "selects the win32 flavor for win32", func(t *testing.T) {
			eqv(t, PathFlavorForPlatform("win32").Impl, Win32Path)
		})
		tw(t, f, "selects the posix flavor for every other platform", func(t *testing.T) {
			eqv(t, PathFlavorForPlatform("linux").Impl, PosixPath)
			eqv(t, PathFlavorForPlatform("darwin").Impl, PosixPath)
		})
	})
	t.Run("Pi built-in tool path conversions", func(t *testing.T) {
		shellPaths := []string{"/c/x/y", "/C", "/c/", "/mnt/d/a", "/cygdrive/e/b/c", "//server/share", `/c\x`, "/tmp/x", "/cc/x", `c:\x`, "relative/x"}
		// Pi's normalizeWindowsShellPath on win32, recorded from the pinned dependency (port/probe, testdata/pathprobe.json).
		pi := map[string]string{
			"/c/x/y": `C:\x\y`, "/C": `C:\`, "/c/": `C:\`, "/mnt/d/a": `D:\a`, "/cygdrive/e/b/c": `E:\b\c`, "//server/share": "//server/share",
			`/c\x`: `/c\x`, "/tmp/x": "/tmp/x", "/cc/x": "/cc/x", `c:\x`: `c:\x`, "relative/x": "relative/x",
		}
		t.Run("toolShellPath", func(t *testing.T) {
			for _, v := range shellPaths {
				eqv(t, w.ToolShellPath(v), pi[v])
			}
			tw(t, f, "translates the WSL and Cygwin mounts", func(t *testing.T) {
				eqv(t, w.ToolShellPath("/mnt/d/a"), `D:\a`)
				eqv(t, w.ToolShellPath("/cygdrive/e/b/c"), `E:\b\c`)
			})
			for _, v := range shellPaths {
				eqv(t, p.ToolShellPath(v), v)
			}
		})
		t.Run("fileUrlToPath", func(t *testing.T) {
			tw(t, f, "decodes a POSIX file URL", func(t *testing.T) {
				got, err := p.FileURLToPath("file:///tmp/s%65cret")
				eqv(t, err, nil)
				eqv(t, got, "/tmp/secret")
			})
			tw(t, f, "decodes a win32 drive file URL", func(t *testing.T) {
				got, err := w.FileURLToPath("file:///C:/x/y%20z")
				eqv(t, err, nil)
				eqv(t, got, `C:\x\y z`)
			})
			tw(t, f, "rejects a POSIX file URL naming a host", func(t *testing.T) {
				if _, err := p.FileURLToPath("file://host/x"); err == nil {
					t.Error("expected an error")
				}
			})
		})
	})
}
