package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/access-intent/access-path.test.ts.

func TestAccessPathTwins(t *testing.T) {
	const f = "access-intent/access-path"
	cwd := "/projects/my-app"
	px, wn := PosixPathFlavor, Win32PathFlavor
	setup := func(t *testing.T) {
		mockHome(t, "/mock/home")
		mockRealpath(t, identityRealpath)
	}
	tmpLink := func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if strings.HasPrefix(p, "/tmp") {
				return "/private" + p, nil
			}
			return p, nil
		})
	}
	fp := func(p string, c string, base *string, fl PathFlavor) *AccessPath {
		return AccessPathForPath(p, AccessPathForPathOptions{Cwd: c, ResolveBase: base, Flavor: fl})
	}
	pxp := func(p string) *AccessPath { return fp(p, cwd, nil, px) }

	t.Run("AccessPath.forPath", func(t *testing.T) {
		t.Run("matchValues()", func(t *testing.T) {
			tw(t, f, "adds the symlink-resolved alias alongside the typed path", func(t *testing.T) {
				setup(t)
				tmpLink(t)
				eqs(t, pxp("/tmp/x").MatchValues(), "/tmp/x", "/private/tmp/x")
			})
			tw(t, f, "deduplicates when the canonical form equals the lexical form", func(t *testing.T) {
				setup(t)
				eqs(t, pxp("/etc/hosts").MatchValues(), "/etc/hosts")
			})
			tw(t, f, "keeps the relative aliases for an in-cwd token without duplicating", func(t *testing.T) {
				setup(t)
				eqs(t, pxp("src/foo.ts").MatchValues(), "/projects/my-app/src/foo.ts", "src/foo.ts")
			})
			tw(t, f, "includes only the lexical aliases when canonical is empty", func(t *testing.T) {
				setup(t)
				eqv(t, len(pxp("/etc/hosts").MatchValues()) != 0, true)
			})
			tw(t, f, "resolves a relative token against an explicit resolveBase", func(t *testing.T) {
				setup(t)
				eqs(t, fp("foo.ts", cwd, ptr("/projects/my-app/sub"), px).MatchValues(), "/projects/my-app/sub/foo.ts", "sub/foo.ts", "foo.ts")
			})
			tw(t, f, "adds the canonical alias resolved against resolveBase", func(t *testing.T) {
				setup(t)
				mockRealpath(t, func(p string) (string, error) {
					if p == "/projects/my-app/sub/foo.ts" {
						return "/real/foo.ts", nil
					}
					return p, nil
				})
				eqs(t, fp("foo.ts", cwd, ptr("/projects/my-app/sub"), px).MatchValues(),
					"/projects/my-app/sub/foo.ts", "sub/foo.ts", "foo.ts", "/real/foo.ts")
			})
		})
		t.Run("platform option", func(t *testing.T) {
			tw(t, f, "win32: builds lexical/match/boundary values with win32 rules", func(t *testing.T) {
				setup(t)
				ap := fp(`src\foo.ts`, `C:\Projects\App`, nil, wn)
				eqv(t, ap.Value(), `c:\projects\app\src\foo.ts`)
				eqv(t, ap.BoundaryValue(), `c:\projects\app\src\foo.ts`)
				eqs(t, ap.MatchValues(), `c:\projects\app\src\foo.ts`, `src\foo.ts`)
			})
			tw(t, f, "win32: lowercases the symlink-resolved boundary value", func(t *testing.T) {
				setup(t)
				mockRealpath(t, func(p string) (string, error) {
					if p == `c:\projects\app\link` {
						return `C:\Real\App`, nil
					}
					return p, nil
				})
				eqv(t, fp("link", `C:\Projects\App`, nil, wn).BoundaryValue(), `c:\real\app`)
			})
		})
		t.Run("boundaryValue()", func(t *testing.T) {
			tw(t, f, "returns the canonical (symlink-resolved) form", func(t *testing.T) {
				setup(t)
				tmpLink(t)
				eqv(t, pxp("/tmp/x").BoundaryValue(), "/private/tmp/x")
			})
			tw(t, f, "returns the lexical form when path has no symlinks", func(t *testing.T) {
				setup(t)
				eqv(t, pxp("/etc/hosts").BoundaryValue(), "/etc/hosts")
			})
			tw(t, f, "returns empty string for empty input", func(t *testing.T) { setup(t); eqv(t, pxp("").BoundaryValue(), "") })
		})
		t.Run("value()", func(t *testing.T) {
			tw(t, f, "returns the lexical (as-typed, normalized) form", func(t *testing.T) {
				setup(t)
				tmpLink(t)
				eqv(t, pxp("/tmp/x").Value(), "/tmp/x")
			})
			tw(t, f, "normalizes the path against cwd", func(t *testing.T) {
				setup(t)
				eqv(t, pxp("src/foo.ts").Value(), "/projects/my-app/src/foo.ts")
			})
			tw(t, f, "normalizes a relative path against an explicit resolveBase", func(t *testing.T) {
				setup(t)
				eqv(t, fp("foo.ts", cwd, ptr("/projects/my-app/sub"), px).Value(), "/projects/my-app/sub/foo.ts")
			})
			tw(t, f, "returns empty string for empty input", func(t *testing.T) { setup(t); eqv(t, pxp("").Value(), "") })
		})
	})

	t.Run("resolvedAlias()", func(t *testing.T) {
		alias := func(ap *AccessPath) (string, bool) { return ap.ResolvedAlias() }
		tw(t, f, "returns the canonical form when a symlink resolves elsewhere", func(t *testing.T) {
			setup(t)
			mockRealpath(t, func(p string) (string, error) {
				if p == "/projects/my-app/demo-symlink-passwd" {
					return "/etc/passwd", nil
				}
				return p, nil
			})
			a, ok := alias(pxp("demo-symlink-passwd"))
			eqv(t, ok, true)
			eqv(t, a, "/etc/passwd")
		})
		tw(t, f, "returns undefined when the path has no symlinks (canonical equals lexical)", func(t *testing.T) {
			setup(t)
			_, ok := alias(pxp("/etc/hosts"))
			eqv(t, ok, false)
		})
		tw(t, f, "returns undefined for a literal-only path (no canonical)", func(t *testing.T) {
			_, ok := alias(AccessPathForLiteral("foo.ts"))
			eqv(t, ok, false)
		})
		tw(t, f, "returns undefined for empty input", func(t *testing.T) {
			setup(t)
			_, ok := alias(pxp(""))
			eqv(t, ok, false)
		})
		tw(t, f, "win32: returns the lowercased canonical form for a real symlink target", func(t *testing.T) {
			setup(t)
			mockRealpath(t, func(p string) (string, error) {
				if p == `c:\projects\app\link` {
					return `C:\Real\App`, nil
				}
				return p, nil
			})
			a, ok := alias(fp("link", `C:\Projects\App`, nil, wn))
			eqv(t, ok, true)
			eqv(t, a, `c:\real\app`)
		})
		tw(t, f, "win32: returns undefined for a case-only difference (both forms lowercased)", func(t *testing.T) {
			setup(t)
			_, ok := alias(fp(`src\foo.ts`, `C:\Projects\App`, nil, wn))
			eqv(t, ok, false)
		})
	})

	t.Run("AccessPath.forDevice", func(t *testing.T) {
		tw(t, f, "lexical, boundary, and match values are all the device path", func(t *testing.T) {
			ap := AccessPathForDevice("/dev/null")
			eqv(t, ap.Value(), "/dev/null")
			eqv(t, ap.BoundaryValue(), "/dev/null")
			eqs(t, ap.MatchValues(), "/dev/null")
		})
		tw(t, f, "resolvedAlias is undefined (canonical equals lexical)", func(t *testing.T) {
			_, ok := AccessPathForDevice("/dev/null").ResolvedAlias()
			eqv(t, ok, false)
		})
	})

	t.Run("AccessPath.forLiteral", func(t *testing.T) {
		tw(t, f, "matchValues() carries only the literal — no canonical, no absolute", func(t *testing.T) {
			eqs(t, AccessPathForLiteral("foo.ts").MatchValues(), "foo.ts")
		})
		tw(t, f, "boundaryValue() is empty (no outside-cwd notion for an unknown base)", func(t *testing.T) {
			eqv(t, AccessPathForLiteral("foo.ts").BoundaryValue(), "")
		})
		tw(t, f, "value() returns the literal", func(t *testing.T) { eqv(t, AccessPathForLiteral("foo.ts").Value(), "foo.ts") })
		tw(t, f, "an empty literal yields no match values", func(t *testing.T) {
			eqs(t, AccessPathForLiteral("").MatchValues())
			eqv(t, AccessPathForLiteral("").Value(), "")
		})
	})

	t.Run("AccessPath.forNativeTarget", func(t *testing.T) {
		opts := AccessPathForNativeTargetOptions{Cwd: cwd, Flavor: px}
		nat := func(target string, rewritten bool, rel *string) *AccessPath {
			return AccessPathForNativeTarget(NativeToolTarget{Target: target, Rewritten: rewritten, RelativeSpelling: rel}, opts)
		}
		t.Run("matchValues()", func(t *testing.T) {
			tw(t, f, "equals forPath's for an unrewritten relative spelling", func(t *testing.T) {
				setup(t)
				native := nat("/projects/my-app/.env", false, ptr(".env"))
				eq(t, native, pxp(".env"), "native vs forPath")
				eqs(t, native.MatchValues(), "/projects/my-app/.env", ".env")
			})
			tw(t, f, "keeps a relative spelling that escapes cwd", func(t *testing.T) {
				setup(t)
				eqs(t, nat("/projects/other/x", false, ptr("../other/x")).MatchValues(), "/projects/other/x", "../other/x")
			})
			tw(t, f, "adds the symlink-resolved alias", func(t *testing.T) {
				setup(t)
				tmpLink(t)
				eqs(t, nat("/tmp/x", false, nil).MatchValues(), "/tmp/x", "/private/tmp/x")
			})
		})
		t.Run("value()", func(t *testing.T) {
			setup(t)
			for _, target := range []string{"/projects/my-app/x'", "/projects/my-app/x ", "/projects/my-app/$HOME/x"} {
				eqv(t, nat(target, false, nil).Value(), target)
			}
			tw(t, f, "folds case under the win32 flavor", func(t *testing.T) {
				ap := AccessPathForNativeTarget(NativeToolTarget{Target: `C:\Proj\A.txt`}, AccessPathForNativeTargetOptions{Cwd: `C:\Proj`, Flavor: wn})
				eqv(t, ap.Value(), `c:\proj\a.txt`)
			})
		})
		t.Run("resolvedAlias()", func(t *testing.T) {
			tw(t, f, "discloses a rewritten target", func(t *testing.T) {
				setup(t)
				a, ok := nat("/x/d\u2019x.txt", true, nil).ResolvedAlias()
				eqv(t, ok, true)
				eqv(t, a, "/x/d\u2019x.txt")
			})
			tw(t, f, "discloses the canonical form when it differs", func(t *testing.T) {
				setup(t)
				tmpLink(t)
				a, ok := nat("/tmp/x", true, nil).ResolvedAlias()
				eqv(t, ok, true)
				eqv(t, a, "/private/tmp/x")
			})
			tw(t, f, "discloses nothing for an unrewritten target", func(t *testing.T) {
				setup(t)
				_, ok := nat("/x/a.txt", false, nil).ResolvedAlias()
				eqv(t, ok, false)
			})
		})
	})
}
