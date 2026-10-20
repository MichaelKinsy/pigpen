package pi_permission_system

import (
	"path/filepath"
	"testing"
)

// Twins of test/path/path-normalizer.test.ts.

func TestPathNormalizerTwins(t *testing.T) {
	const f = "path/path-normalizer"
	setup := func(t *testing.T) {
		mockHome(t, "/mock/home")
		mockRealpath(t, identityRealpath)
	}
	t.Run("posix flavor", func(t *testing.T) {
		setup(t)
		n := NewPathNormalizer(PosixPathFlavor, "/projects/my-app")
		tw(t, f, "forPath builds an AccessPath resolved against the baked cwd", func(t *testing.T) {
			ap := n.ForPath("src/foo.ts", nil)
			eqv(t, ap.Value(), "/projects/my-app/src/foo.ts")
			eqs(t, ap.MatchValues(), "/projects/my-app/src/foo.ts", "src/foo.ts")
		})
		tw(t, f, "forPath honors an explicit resolveBase", func(t *testing.T) {
			eqv(t, n.ForPath("foo.ts", ptr("/projects/my-app/sub")).Value(), "/projects/my-app/sub/foo.ts")
		})
		tw(t, f, "forLiteral builds a literal-only AccessPath", func(t *testing.T) {
			ap := n.ForLiteral("foo.ts")
			eqs(t, ap.MatchValues(), "foo.ts")
			eqv(t, ap.BoundaryValue(), "")
		})
		tw(t, f, "isAbsolute uses posix rules", func(t *testing.T) {
			eqv(t, n.IsAbsolute("/etc/hosts"), true)
			eqv(t, n.IsAbsolute("rel/path"), false)
		})
		tw(t, f, "resolveBase resolves an offset against the baked cwd", func(t *testing.T) {
			eqv(t, n.ResolveBase("sub"), "/projects/my-app/sub")
			eqv(t, n.ResolveBase("/abs"), "/abs")
		})
		tw(t, f, "joinBase joins an offset with a relative target", func(t *testing.T) { eqv(t, n.JoinBase("sub", "nested"), "sub/nested") })
		tw(t, f, "isWithinDirectory decides containment", func(t *testing.T) {
			eqv(t, n.IsWithinDirectory("/a/b/c", "/a/b"), true)
			eqv(t, n.IsWithinDirectory("/a/x", "/a/b"), false)
		})
		t.Run("outside-cwd boundary through an AccessPath's boundary value", func(t *testing.T) {
			outside := func(ap *AccessPath) bool { return n.IsBoundaryOutsideWorkingDirectory(ap.BoundaryValue()) }
			tw(t, f, "keeps a path inside the baked cwd", func(t *testing.T) { eqv(t, outside(n.ForPath("/projects/my-app/src", nil)), false) })
			tw(t, f, "flags an absolute path outside cwd", func(t *testing.T) { eqv(t, outside(n.ForPath("/etc/hosts", nil)), true) })
			tw(t, f, "expands a home-relative token", func(t *testing.T) { eqv(t, outside(n.ForPath("~/secrets", nil)), true) })
			tw(t, f, "resolves a relative token inside cwd", func(t *testing.T) { eqv(t, outside(n.ForPath("src/index.ts", nil)), false) })
			tw(t, f, "follows an in-cwd symlink to an external target", func(t *testing.T) {
				mockRealpath(t, func(p string) (string, error) {
					if p == "/projects/my-app/link/hosts" {
						return "/etc/hosts", nil
					}
					return p, nil
				})
				eqv(t, outside(n.ForPath("./link/hosts", nil)), true)
			})
			tw(t, f, "keeps a path inside a symlinked cwd", func(t *testing.T) {
				mockRealpath(t, func(p string) (string, error) {
					if len(p) >= 5 && p[:5] == "/tmp/" {
						return "/private/tmp" + p[4:], nil
					}
					if p == "/tmp" {
						return "/private/tmp", nil
					}
					return p, nil
				})
				sn := NewPathNormalizer(PosixPathFlavor, "/private/tmp")
				ap := sn.ForPath("/tmp/workspace/file.ts", nil)
				eqv(t, sn.IsBoundaryOutsideWorkingDirectory(ap.BoundaryValue()), false)
			})
		})
		tw(t, f, "comparableValue returns the lexical absolute form (no FS)", func(t *testing.T) {
			eqv(t, n.ComparableValue("src/foo.ts"), "/projects/my-app/src/foo.ts")
			eqv(t, n.ComparableValue("/etc/hosts"), "/etc/hosts")
		})
		tw(t, f, "forBashToken delegates to forPath on posix", func(t *testing.T) {
			eqv(t, n.ForBashToken("src/foo.ts", nil).Value(), "/projects/my-app/src/foo.ts")
		})
		tw(t, f, "isBoundaryOutsideWorkingDirectory tests a canonical path against cwd", func(t *testing.T) {
			eqv(t, n.IsBoundaryOutsideWorkingDirectory("/projects/my-app/src"), false)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory("/etc/hosts"), true)
		})
		tw(t, f, "isBoundaryOutsideWorkingDirectory excludes a safe device path", func(t *testing.T) {
			eqv(t, n.IsBoundaryOutsideWorkingDirectory("/dev/null"), false)
		})
		tw(t, f, "interpretBashCdTarget maps an absolute target to absolute (posix)", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget("/etc"), BashCdTarget{Kind: "absolute", Value: "/etc"})
		})
		tw(t, f, "interpretBashCdTarget maps a relative target to relative (posix)", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget("sub"), BashCdTarget{Kind: "relative"})
		})
		tw(t, f, "approvalPatternsFor scopes a resolved path to its directory", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForPath("src/foo.ts", nil)), "/projects/my-app/src/*")
		})
		tw(t, f, "approvalPatternsFor scopes an absolute path outside the cwd", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForPath("/other/pkg/x.ts", nil)), "/other/pkg/*")
		})
	})

	t.Run("win32 flavor", func(t *testing.T) {
		setup(t)
		n := NewPathNormalizer(Win32PathFlavor, `C:\Projects\App`)
		tw(t, f, "forPath builds a case-folded AccessPath with win32 rules", func(t *testing.T) {
			ap := n.ForPath(`src\foo.ts`, nil)
			eqv(t, ap.Value(), `c:\projects\app\src\foo.ts`)
			eqs(t, ap.MatchValues(), `c:\projects\app\src\foo.ts`, `src\foo.ts`)
		})
		tw(t, f, "isAbsolute uses win32 rules", func(t *testing.T) {
			eqv(t, n.IsAbsolute(`C:\Users\foo`), true)
			eqv(t, n.IsAbsolute(`rel\path`), false)
		})
		tw(t, f, "resolveBase resolves an offset against the baked cwd", func(t *testing.T) { eqv(t, n.ResolveBase("sub"), `C:\Projects\App\sub`) })
		tw(t, f, "joinBase joins an offset with a relative target", func(t *testing.T) { eqv(t, n.JoinBase("sub", "nested"), `sub\nested`) })
		tw(t, f, "isWithinDirectory folds case", func(t *testing.T) {
			eqv(t, n.IsWithinDirectory(`c:\users\foo\dir\sub`, `C:\Users\Foo\dir`), true)
		})
		tw(t, f, "the boundary value case-folds against the baked cwd", func(t *testing.T) {
			inside := n.ForPath(`c:\projects\app\src`, nil)
			outside := n.ForPath(`C:\Other\dir`, nil)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(inside.BoundaryValue()), false)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(outside.BoundaryValue()), true)
		})
		tw(t, f, "comparableValue case-folds the lexical absolute form", func(t *testing.T) {
			eqv(t, n.ComparableValue(`src\foo.ts`), `c:\projects\app\src\foo.ts`)
		})
		tw(t, f, "forBashToken preserves a POSIX device path as a safe device", func(t *testing.T) {
			ap := n.ForBashToken("/dev/null", nil)
			eqv(t, ap.Value(), "/dev/null")
			eqv(t, ap.BoundaryValue(), "/dev/null")
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(ap.BoundaryValue()), false)
		})
		tw(t, f, "forBashToken preserves all four safe device paths", func(t *testing.T) {
			for _, d := range []string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"} {
				eqv(t, n.ForBashToken(d, nil).BoundaryValue(), d)
			}
		})
		tw(t, f, "forBashToken translates an MSYS drive mount to a Windows path", func(t *testing.T) {
			ap := n.ForBashToken("/c/Other/x", nil)
			eqv(t, ap.Value(), `c:\other\x`)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(ap.BoundaryValue()), true)
		})
		tw(t, f, "forBashToken translates an in-cwd drive mount (not external)", func(t *testing.T) {
			ap := n.ForBashToken("/c/projects/app/inside.txt", nil)
			eqv(t, ap.Value(), `c:\projects\app\inside.txt`)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(ap.BoundaryValue()), false)
		})
		tw(t, f, "forBashToken keeps a non-mount POSIX absolute as a literal", func(t *testing.T) {
			ap := n.ForBashToken("/tmp/foo", nil)
			eqv(t, ap.Value(), "/tmp/foo")
			eqv(t, ap.BoundaryValue(), "")
			eqs(t, ap.MatchValues(), "/tmp/foo")
		})
		tw(t, f, "interpretBashCdTarget translates a drive-mount target to absolute", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget("/c/Other"), BashCdTarget{Kind: "absolute", Value: `C:\Other`})
		})
		tw(t, f, "interpretBashCdTarget maps a non-mount POSIX absolute to unknown", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget("/tmp"), BashCdTarget{Kind: "unknown"})
		})
		tw(t, f, "interpretBashCdTarget maps a native drive path to absolute", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget(`C:\Other`), BashCdTarget{Kind: "absolute", Value: `C:\Other`})
		})
		tw(t, f, "interpretBashCdTarget maps a relative target to relative (win32)", func(t *testing.T) {
			eqv(t, n.InterpretBashCdTarget("sub"), BashCdTarget{Kind: "relative"})
		})
		tw(t, f, "isBoundaryOutsideWorkingDirectory case-folds against the baked cwd", func(t *testing.T) {
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(`c:\projects\app\src`), false)
			eqv(t, n.IsBoundaryOutsideWorkingDirectory(`c:\other\dir`), true)
		})
		tw(t, f, "approvalPatternsFor scopes a native windows path with backslashes", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForPath(`src\foo.ts`, nil)), `c:\projects\app\src\*`)
		})
		tw(t, f, "approvalPatternsFor keeps a Git Bash device token POSIX-shaped", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForBashToken("/dev/null", nil)), "/dev/*")
		})
		tw(t, f, "approvalPatternsFor scopes a POSIX-absolute directory token to itself", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForBashToken("/tmp/logs/", nil)), "/tmp/logs/*")
		})
		tw(t, f, "approvalPatternsFor scopes a POSIX-absolute file token to its parent", func(t *testing.T) {
			eqs(t, n.ApprovalPatternsFor(n.ForBashToken("/tmp/logs", nil)), "/tmp/*")
		})
	})

	t.Run("isInfrastructureRead", func(t *testing.T) {
		setup(t)
		n := NewPathNormalizer(PosixPathFlavor, "/projects/my-app")
		infra := InfrastructureReadScope{Dirs: []string{"/infra"}}
		empty := InfrastructureReadScope{}
		tw(t, f, "allows a read-only tool targeting a configured infra dir", func(t *testing.T) {
			eqv(t, n.IsInfrastructureRead("read", n.ForPath("/infra/git/pkg/SKILL.md", nil), infra), true)
		})
		tw(t, f, "does not allow a write tool targeting an infra dir", func(t *testing.T) {
			eqv(t, n.IsInfrastructureRead("write", n.ForPath("/infra/git/pkg/file.ts", nil), infra), false)
		})
		tw(t, f, "does not allow a read-only tool outside any infra dir", func(t *testing.T) {
			eqv(t, n.IsInfrastructureRead("read", n.ForPath("/elsewhere/file.ts", nil), infra), false)
		})
		tw(t, f, "allows a read targeting the project-local .pi/npm dir (from baked cwd)", func(t *testing.T) {
			eqv(t, n.IsInfrastructureRead("read", n.ForPath("/projects/my-app/.pi/npm/dep/index.js", nil), empty), true)
		})
		tw(t, f, "does not allow a read inside one of the scope's excluded dirs", func(t *testing.T) {
			eqv(t, n.IsInfrastructureRead("read", n.ForPath("/infra/logs/review.jsonl", nil),
				InfrastructureReadScope{Dirs: []string{"/infra"}, ExcludedDirs: []string{"/infra/logs"}}), false)
		})
	})

	t.Run("entryExists", func(t *testing.T) {
		fixture := func(t *testing.T) (string, *PathNormalizer) {
			root := realTmp(t)
			return root, NewPathNormalizer(PosixPathFlavor, root)
		}
		tw(t, f, "existing regular file → true", func(t *testing.T) {
			root, n := fixture(t)
			eqv(t, n.EntryExists(writeFile(t, root, "secret.txt", "contents")), true)
		})
		tw(t, f, "existing directory → true", func(t *testing.T) {
			root, n := fixture(t)
			eqv(t, n.EntryExists(mkSubdir(t, root, "nested")), true)
		})
		tw(t, f, "symlink to an existing target → true", func(t *testing.T) {
			root, n := fixture(t)
			target := writeFile(t, root, "target.txt", "")
			eqv(t, n.EntryExists(symlink(t, root, "link", target)), true)
		})
		tw(t, f, "dangling symlink → true (lstat: the link itself is an entry)", func(t *testing.T) {
			root, n := fixture(t)
			eqv(t, n.EntryExists(symlink(t, root, "dangling", filepath.Join(root, "gone.txt"))), true)
		})
		tw(t, f, "nonexistent path → false", func(t *testing.T) {
			root, n := fixture(t)
			eqv(t, n.EntryExists(filepath.Join(root, "missing.txt")), false)
		})
		tw(t, f, "path under a nonexistent parent → false (ENOTDIR/ENOENT)", func(t *testing.T) {
			root, n := fixture(t)
			eqv(t, n.EntryExists(filepath.Join(root, "no-dir", "file.txt")), false)
		})
		tw(t, f, "empty path → false", func(t *testing.T) {
			_, n := fixture(t)
			eqv(t, n.EntryExists(""), false)
		})
		tw(t, f, "answers from the filesystem, not the flavor — win32 normalizer agrees", func(t *testing.T) {
			root, _ := fixture(t)
			file := writeFile(t, root, "flavor-independent.txt", "")
			w := NewPathNormalizer(Win32PathFlavor, root)
			eqv(t, w.EntryExists(file), true)
			eqv(t, w.EntryExists(filepath.Join(root, "nope.txt")), false)
		})
	})

	t.Run("approvalPatternsFor over the filesystem", func(t *testing.T) {
		fixture := func(t *testing.T) (string, *PathNormalizer) {
			root := realTmp(t)
			return mkSubdir(t, root, "projects"), NewPathNormalizer(PosixPathFlavor, mkSubdir(t, root, "workspace"))
		}
		tw(t, f, "an existing directory → the directory and its contents", func(t *testing.T) {
			projects, n := fixture(t)
			dir := mkSubdir(t, projects, "project-a")
			eqs(t, n.ApprovalPatternsFor(n.ForPath(dir, nil)), dir, dir+"/*")
		})
		tw(t, f, "an existing file → its parent directory", func(t *testing.T) {
			projects, n := fixture(t)
			file := writeFile(t, projects, "notes.txt", "")
			eqs(t, n.ApprovalPatternsFor(n.ForPath(file, nil)), projects+"/*")
		})
		tw(t, f, "a missing path → its parent directory", func(t *testing.T) {
			projects, n := fixture(t)
			eqs(t, n.ApprovalPatternsFor(n.ForPath(filepath.Join(projects, "missing"), nil)), projects+"/*")
		})
		tw(t, f, "a literal-only path is never probed → its parent directory", func(t *testing.T) {
			projects, n := fixture(t)
			dir := mkSubdir(t, projects, "project-a")
			eqs(t, n.ApprovalPatternsFor(n.ForLiteral(dir)), projects+"/*")
		})
	})

	t.Run("forToolPath", func(t *testing.T) {
		fixture := func(t *testing.T) (string, *PathNormalizer) {
			root := realTmp(t)
			return root, NewPathNormalizer(PosixPathFlavor, root)
		}
		tw(t, f, "read opens the curly-quote file a straight quote names", func(t *testing.T) {
			root, n := fixture(t)
			curly := writeFile(t, root, "d\u2019x.txt", "")
			eqv(t, n.ForToolPath("read", "d'x.txt").Value(), curly)
		})
		tw(t, f, "write tries no variant spelling", func(t *testing.T) {
			root, n := fixture(t)
			writeFile(t, root, "d\u2019x.txt", "")
			eqv(t, n.ForToolPath("write", "d'x.txt").Value(), filepath.Join(root, "d'x.txt"))
		})
		tw(t, f, "read follows a dangling symlink to the variant Pi opens", func(t *testing.T) {
			root, n := fixture(t)
			symlink(t, root, "dang'x.txt", filepath.Join(root, "gone.txt"))
			curly := writeFile(t, root, "dang\u2019x.txt", "")
			eqv(t, n.ForToolPath("read", "dang'x.txt").Value(), curly)
		})
		tw(t, f, "every built-in tool reads a file URL as the file it names", func(t *testing.T) {
			root, n := fixture(t)
			file := writeFile(t, root, "secret.txt", "")
			for _, tool := range []string{"read", "write", "edit", "ls", "find", "grep"} {
				eqv(t, n.ForToolPath(tool, "file://"+file).Value(), file)
			}
		})
		tw(t, f, "an extension tool keeps forPath's resolution", func(t *testing.T) {
			_, n := fixture(t)
			mockHome(t, "/mock/home")
			a, b := n.ForToolPath("my-ext", "$HOME/x"), n.ForPath("$HOME/x", nil)
			eq(t, a, b, "AccessPath")
		})
	})
}
