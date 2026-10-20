package pi_permission_system

import "testing"

// Twins of test/access-intent/path-normalization.test.ts and test/access-intent/path-surfaces.test.ts.

func TestPathNormalizationTwins(t *testing.T) {
	const f = "access-intent/path-normalization"
	cwd := "/projects/my-app"
	px, wn := PosixPathFlavor, Win32PathFlavor
	setup := func(t *testing.T) { mockHome(t, "/mock/home"); mockRealpath(t, identityRealpath) }
	t.Run("normalizePathForComparison", func(t *testing.T) {
		setup(t)
		n := func(p, base string, fl PathFlavor) string { return NormalizePathForComparison(p, base, fl) }
		tw(t, f, "resolves absolute path unchanged", func(t *testing.T) { eqv(t, n("/usr/local/bin", cwd, px), "/usr/local/bin") })
		tw(t, f, "resolves relative path against cwd", func(t *testing.T) { eqv(t, n("src/foo.ts", cwd, px), "/projects/my-app/src/foo.ts") })
		tw(t, f, "expands bare ~ to homedir", func(t *testing.T) { eqv(t, n("~", cwd, px), "/mock/home") })
		tw(t, f, "expands ~/... to homedir-relative path", func(t *testing.T) {
			eqv(t, n("~/docs/readme.md", cwd, px), HostPath.Join("/mock/home", "docs/readme.md"))
		})
		tw(t, f, "expands bare $HOME to homedir", func(t *testing.T) { eqv(t, n("$HOME", cwd, px), "/mock/home") })
		tw(t, f, "expands $HOME/... to homedir-relative path", func(t *testing.T) {
			eqv(t, n("$HOME/.ssh/config", cwd, px), HostPath.Join("/mock/home", ".ssh/config"))
		})
		tw(t, f, "strips leading @ before resolving", func(t *testing.T) { eqv(t, n("@/usr/local/bin", cwd, px), "/usr/local/bin") })
		tw(t, f, "strips surrounding quotes", func(t *testing.T) {
			eqv(t, n("'/usr/local/bin'", cwd, px), "/usr/local/bin")
			eqv(t, n(`"/usr/local/bin"`, cwd, px), "/usr/local/bin")
		})
		tw(t, f, "returns empty string for blank/whitespace-only path", func(t *testing.T) {
			eqv(t, n("", cwd, px), "")
			eqv(t, n("   ", cwd, px), "")
		})
		tw(t, f, "win32: lowercases the resolved absolute path", func(t *testing.T) {
			eqv(t, n(`C:\Users\Foo\Bar.txt`, `C:\Projects`, wn), `c:\users\foo\bar.txt`)
		})
		tw(t, f, "win32: resolves a relative path against cwd with win32 rules", func(t *testing.T) {
			eqv(t, n(`src\foo.ts`, `C:\Projects\App`, wn), `c:\projects\app\src\foo.ts`)
		})
		tw(t, f, "posix platform leaves case untouched", func(t *testing.T) { eqv(t, n("/Projects/App/Src.ts", cwd, px), "/Projects/App/Src.ts") })
	})
	t.Run("canonicalNormalizePathForComparison", func(t *testing.T) {
		c := func(p, base string, fl PathFlavor) string { return CanonicalNormalizePathForComparison(p, base, fl) }
		tw(t, f, "returns canonical form of an existing path", func(t *testing.T) {
			setup(t)
			mockRealpath(t, func(p string) (string, error) {
				if p == "/projects/link" {
					return "/real/projects/app", nil
				}
				return p, nil
			})
			eqv(t, c("/projects/link", cwd, px), "/real/projects/app")
		})
		tw(t, f, "returns empty string for empty input", func(t *testing.T) { setup(t); eqv(t, c("", cwd, px), "") })
		tw(t, f, "returns lexical form when no symlinks (identity realpathSync)", func(t *testing.T) {
			setup(t)
			eqv(t, c("/projects/my-app/src/index.ts", cwd, px), "/projects/my-app/src/index.ts")
		})
		tw(t, f, "win32: lowercases the canonical form", func(t *testing.T) {
			setup(t)
			eqv(t, c(`C:\Projects\App\Src`, `C:\Projects\App`, wn), `c:\projects\app\src`)
		})
	})
	t.Run("normalizePathPolicyLiteral", func(t *testing.T) {
		setup(t)
		l := NormalizePathPolicyLiteral
		tw(t, f, "returns a relative token unchanged", func(t *testing.T) { eqv(t, l("src/foo.ts"), "src/foo.ts") })
		tw(t, f, "trims and strips simple wrapping quotes", func(t *testing.T) {
			eqv(t, l("  'src/foo.ts'  "), "src/foo.ts")
			eqv(t, l(`"a/b"`), "a/b")
		})
		tw(t, f, "strips a leading @ prefix", func(t *testing.T) { eqv(t, l("@src/foo.ts"), "src/foo.ts") })
		tw(t, f, "expands ~ to the home directory", func(t *testing.T) {
			eqv(t, l("~/docs/readme.md"), HostPath.Join("/mock/home", "docs/readme.md"))
		})
		tw(t, f, "does not resolve a relative value against any cwd", func(t *testing.T) { eqv(t, l("foo.ts"), "foo.ts") })
		tw(t, f, "returns empty string for blank input", func(t *testing.T) { eqv(t, l("   "), "") })
		tw(t, f, "preserves the surface catch-all", func(t *testing.T) { eqv(t, l("*"), "*") })
		// A lone quote is stripped at the start and nothing is left for the end; two quotes strip to nothing.
		eqv(t, l(`"`), "")
		eqv(t, l(`''`), "")
		eqv(t, l(`'a"`), "a")
		eqv(t, l("@@a"), "@a")
	})
	t.Run("getPathPolicyValues", func(t *testing.T) {
		setup(t)
		g := func(p string, o PathPolicyValueOptions) []string { return GetPathPolicyValues(p, o, px) }
		c := cwd
		tw(t, f, "returns only the literal when no base is available", func(t *testing.T) {
			eqs(t, g("src/foo.ts", PathPolicyValueOptions{}), "src/foo.ts")
		})
		tw(t, f, "adds absolute and project-relative aliases for a relative token", func(t *testing.T) {
			eqs(t, g("src/foo.ts", PathPolicyValueOptions{Cwd: &c}), "/projects/my-app/src/foo.ts", "src/foo.ts")
		})
		tw(t, f, "omits the relative alias for a token outside cwd", func(t *testing.T) {
			eqs(t, g("/etc/hosts", PathPolicyValueOptions{Cwd: &c}), "/etc/hosts")
		})
		tw(t, f, "resolves against resolveBase while aliasing relative to cwd", func(t *testing.T) {
			eqs(t, g("foo.txt", PathPolicyValueOptions{Cwd: &c, ResolveBase: ptr("/projects/my-app/nested")}),
				"/projects/my-app/nested/foo.txt", "nested/foo.txt", "foo.txt")
		})
		tw(t, f, "preserves the surface catch-all", func(t *testing.T) { eqs(t, g("*", PathPolicyValueOptions{Cwd: &c}), "*") })
		tw(t, f, "returns empty for blank input", func(t *testing.T) { eqs(t, g("   ", PathPolicyValueOptions{Cwd: &c})) })
	})
	t.Run("cwdRelativePolicyValues", func(t *testing.T) {
		setup(t)
		tw(t, f, "aliases an absolute path inside cwd relative to it", func(t *testing.T) {
			eqs(t, CwdRelativePolicyValues("/projects/my-app/src/a.ts", cwd, px), "src/a.ts")
		})
		tw(t, f, "has no alias for cwd itself", func(t *testing.T) { eqs(t, CwdRelativePolicyValues(cwd, cwd, px)) })
		tw(t, f, "has no alias for a path outside cwd", func(t *testing.T) { eqs(t, CwdRelativePolicyValues("/etc/hosts", cwd, px)) })
		tw(t, f, "folds the cwd under the win32 flavor", func(t *testing.T) {
			eqs(t, CwdRelativePolicyValues(`c:\proj\src\a.ts`, `C:\Proj`, wn), `src\a.ts`)
		})
	})
}

func TestPathSurfacesTwins(t *testing.T) {
	const f = "access-intent/path-surfaces"
	has := func(m map[string]bool, names ...string) bool {
		for _, n := range names {
			if !m[n] {
				return false
			}
		}
		return true
	}
	tw(t, f, "contains the expected tool names", func(t *testing.T) {
		eqv(t, has(PathBearingTools, "read", "write", "edit", "find", "grep", "ls"), true)
	})
	tw(t, f, "does not contain bash or mcp", func(t *testing.T) {
		eqv(t, PathBearingTools["bash"] || PathBearingTools["mcp"], false)
	})
	tw(t, f, "contains read, find, grep, ls", func(t *testing.T) { eqv(t, has(ReadOnlyPathBearingTools, "read", "find", "grep", "ls"), true) })
	tw(t, f, "does not contain write or edit", func(t *testing.T) {
		eqv(t, ReadOnlyPathBearingTools["write"] || ReadOnlyPathBearingTools["edit"], false)
	})
	tw(t, f, "contains the path-bearing tools plus the cross-cutting gates", func(t *testing.T) {
		eqv(t, has(PathSurfaces, "read", "write", "edit", "find", "grep", "ls", "external_directory", "path"), true)
	})
	tw(t, f, "does not contain bash or mcp", func(t *testing.T) {
		eqv(t, PathSurfaces["bash"] || PathSurfaces["mcp"], false)
	})
	tw(t, f, "contains the four directional surfaces", func(t *testing.T) {
		eqv(t, has(PathSurfaces, "path_read", "path_write", "external_directory_read", "external_directory_write"), true)
	})
	for _, c := range [][2]string{{"path_read", "path"}, {"path_write", "path"}, {"external_directory_read", "external_directory"}, {"external_directory_write", "external_directory"}} {
		eqv(t, SurfaceFamilyOf(c[0]), c[1])
	}
	for _, s := range []string{"path", "external_directory", "read", "write", "bash", "mcp"} {
		eqv(t, SurfaceFamilyOf(s), s)
		_, ok := CapabilityDirectionOf(s)
		eqv(t, ok, false)
	}
	tw(t, f, "leaves a suffixed surface outside the directional families alone", func(t *testing.T) {
		eqv(t, SurfaceFamilyOf("my_tool_read"), "my_tool_read")
		eqv(t, SurfaceFamilyOf("_read"), "_read")
	})
	for _, c := range [][2]string{{"path_read", "read"}, {"path_write", "write"}, {"external_directory_read", "read"}, {"external_directory_write", "write"}} {
		d, ok := CapabilityDirectionOf(c[0])
		eqv(t, ok, true)
		eqv(t, string(d), c[1])
	}
	tw(t, f, "answers null for a suffixed surface outside the directional families", func(t *testing.T) {
		_, ok := CapabilityDirectionOf("my_tool_read")
		eqv(t, ok, false)
		_, ok = CapabilityDirectionOf("_read")
		eqv(t, ok, false)
	})
	tw(t, f, "answers a family name with its members in read-then-write order", func(t *testing.T) {
		m, ok := SurfaceFamilyMembers("path")
		eqv(t, ok, true)
		eqv(t, m, [2]string{"path_read", "path_write"})
		m, _ = SurfaceFamilyMembers("external_directory")
		eqv(t, m, [2]string{"external_directory_read", "external_directory_write"})
	})
	for _, s := range []string{"path_read", "path_write", "external_directory_read", "read", "write", "edit", "bash"} {
		_, ok := SurfaceFamilyMembers(s)
		eqv(t, ok, false)
	}
	for _, tool := range []string{"read", "grep", "find", "ls"} {
		eqv(t, CapabilitySurfaceForTool("path", tool), "path_read")
		eqv(t, CapabilitySurfaceForTool("external_directory", tool), "external_directory_read")
	}
	tw(t, f, "names the write surface for write", func(t *testing.T) {
		eqv(t, CapabilitySurfaceForTool("path", "write"), "path_write")
		eqv(t, CapabilitySurfaceForTool("external_directory", "write"), "external_directory_write")
	})
	for _, tool := range []string{"edit", "mcp__server__tool", "some_extension_tool", "bash"} {
		eqv(t, CapabilitySurfaceForTool("path", tool), "path")
		eqv(t, CapabilitySurfaceForTool("external_directory", tool), "external_directory")
	}
	tw(t, f, "names the read surface for a proven read", func(t *testing.T) {
		eqv(t, CapabilitySurfaceForEffect("path", EffectRead), "path_read")
		eqv(t, CapabilitySurfaceForEffect("external_directory", EffectRead), "external_directory_read")
	})
	tw(t, f, "names the write surface for a proven write", func(t *testing.T) {
		eqv(t, CapabilitySurfaceForEffect("path", EffectWrite), "path_write")
		eqv(t, CapabilitySurfaceForEffect("external_directory", EffectWrite), "external_directory_write")
	})
	tw(t, f, "names the bare family for an unproven effect, which folds both", func(t *testing.T) {
		eqv(t, CapabilitySurfaceForEffect("path", EffectUnproven), "path")
		eqv(t, CapabilitySurfaceForEffect("external_directory", EffectUnproven), "external_directory")
	})
	tw(t, f, "agrees with the tool-keyed selector, which routes through it", func(t *testing.T) {
		eqv(t, CapabilitySurfaceForEffect("path", EffectRead), CapabilitySurfaceForTool("path", "read"))
		eqv(t, CapabilitySurfaceForEffect("path", EffectWrite), CapabilitySurfaceForTool("path", "write"))
		eqv(t, CapabilitySurfaceForEffect("path", EffectUnproven), CapabilitySurfaceForTool("path", "edit"))
	})
}
