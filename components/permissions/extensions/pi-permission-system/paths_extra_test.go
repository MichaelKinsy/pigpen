package pi_permission_system

import (
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
)

// Cases the upstream tests and the recorded corpus do not reach: they pin the port's own behavior where the original's is
// decided by process state (cwd, drive environment), by an error class or by a call's aliasing. Each value follows from the
// original's source (Node's path.js for the cwd cases, canonicalize-path.ts, path-normalizer.ts).

func TestNodePathProcessState(t *testing.T) {
	oldCwd, oldEnv := processCwd, processEnv
	t.Cleanup(func() { processCwd, processEnv = oldCwd, oldEnv })
	t.Run("a relative cwd that normalizes to nothing resolves to a dot", func(t *testing.T) {
		processCwd = func() string { return "" }
		processEnv = func(string) string { return "" }
		eqv(t, PosixPath.Resolve("a/.."), ".")
		eqv(t, Win32Path.Resolve(`a\..`), ".")
	})
	t.Run("a drive-relative path takes the drive's cwd from the environment", func(t *testing.T) {
		processCwd = func() string { return `C:\proj` }
		processEnv = func(k string) string {
			if k == "=d:" {
				return `d:\work`
			}
			return ""
		}
		eqv(t, Win32Path.Resolve("d:x"), `d:\work\x`)
	})
	t.Run("a drive-relative path falls back to the drive root when the cwd is on another drive", func(t *testing.T) {
		processCwd = func() string { return `C:\proj` }
		processEnv = func(string) string { return "" }
		eqv(t, Win32Path.Resolve("d:x"), `d:\x`)
	})
}

func TestCanonicalizePathErrorClasses(t *testing.T) {
	px := PosixPathFlavor
	t.Run("an error other than ENOENT or ENOTDIR stops the walk even when an ancestor resolves", func(t *testing.T) {
		calls := 0
		mockRealpath(t, func(p string) (string, error) {
			calls++
			if calls == 1 {
				return "", errno(syscall.ELOOP, p)
			}
			return "/real", nil
		})
		eqv(t, CanonicalizePath("/a/b", px), "/a/b")
		eqv(t, calls, 1)
	})
	t.Run("ENOTDIR walks up to the ancestor that resolves", func(t *testing.T) {
		calls := 0
		mockRealpath(t, func(p string) (string, error) {
			calls++
			if calls == 1 {
				return "", errno(syscall.ENOTDIR, p)
			}
			return "/canon", nil
		})
		eqv(t, CanonicalizePath("/a/b", px), "/canon/b")
	})
	t.Run("the root itself is tried last", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if p == "/" {
				return "/real", nil
			}
			return "", errno(syscall.ENOENT, p)
		})
		eqv(t, CanonicalizePath("/a/b", px), "/real/a/b")
	})
}

func TestRealpathHook(t *testing.T) {
	t.Run("an empty path is ENOENT", func(t *testing.T) {
		_, err := realpathSync("")
		eqv(t, errors.Is(err, fs.ErrNotExist), true)
	})
	t.Run("a relative path resolves against the working directory", func(t *testing.T) {
		dir := realTmp(t)
		writeFile(t, dir, "f", "x")
		t.Chdir(dir)
		got, err := realpathSync("f")
		eqv(t, err, nil)
		eqv(t, got, filepath.Join(dir, "f"))
	})
}

func TestPathLayerEdges(t *testing.T) {
	px, wn := PosixPathFlavor, Win32PathFlavor
	t.Run("a native target's boundary value is folded under win32", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if p == `c:\proj\a.txt` {
				return `C:\Real\A.txt`, nil
			}
			return p, nil
		})
		ap := AccessPathForNativeTarget(NativeToolTarget{Target: `C:\Proj\A.txt`}, AccessPathForNativeTargetOptions{Cwd: `C:\Proj`, Flavor: wn})
		eqv(t, ap.BoundaryValue(), `c:\real\a.txt`)
	})
	t.Run("an empty resolveBase is not the default", func(t *testing.T) {
		c := "/c"
		eqs(t, GetPathPolicyValues("src/a", PathPolicyValueOptions{Cwd: &c, ResolveBase: ptr("")}, px), "src/a")
	})
	t.Run("forToolPath trims the tool name before choosing read's variants", func(t *testing.T) {
		root := realTmp(t)
		n := NewPathNormalizer(px, root)
		curly := writeFile(t, root, "d\u2019x.txt", "")
		eqv(t, n.ForToolPath(" read ", "d'x.txt").Value(), curly)
	})
	t.Run("the cwd is compared in its canonical form", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if len(p) >= 4 && p[:4] == "/tmp" {
				return "/private" + p, nil
			}
			return p, nil
		})
		n := NewPathNormalizer(px, "/tmp/ws")
		eqv(t, n.IsBoundaryOutsideWorkingDirectory(n.ForPath("x", nil).BoundaryValue()), false)
	})
	t.Run("infrastructure reads follow the canonical path, not the typed one", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if p == "/infra/link/x" {
				return "/elsewhere/x", nil
			}
			return p, nil
		})
		n := NewPathNormalizer(px, "/p")
		ap := n.ForPath("/infra/link/x", nil)
		eqv(t, ap.Value(), "/infra/link/x")
		eqv(t, n.IsInfrastructureRead("read", ap, InfrastructureReadScope{Dirs: []string{"/infra"}}), false)
	})
	t.Run("a resolved intent owns its match values", func(t *testing.T) {
		vals := []string{"a", "b"}
		r := BuildResolvedIntentFromMatchValues("path", vals, "x").(PathValuesAccessIntent)
		vals[0] = "z"
		eqv(t, r.Values[0], "a")
	})
	t.Run("an extractor's path is trimmed", func(t *testing.T) {
		ex := fakeLookup{"t", NewToolAccessExtractor(func(*jsObject) (string, bool) { return "  /x  ", true }), RegistrationLocal}
		got := GetToolInputPath("t", newObject(), ex)
		eqv(t, got.Path, "/x")
	})
	t.Run("a configured name is sanitized character by character, upper case kept", func(t *testing.T) {
		eqs(t, CreatePiMcpToolTargets("mcp__My_Srv__x", []string{"My.Srv"}), "My.Srv_x", "My.Srv:x", "My.Srv", "My_Srv_x", "My_Srv:x", "My_Srv", "x", "mcp__My_Srv__x", "mcp_call")
	})
	t.Run("a non-BMP character in a configured name sanitizes to two underscores, one per UTF-16 unit", func(t *testing.T) {
		eqs(t, CreatePiMcpToolTargets("mcp__a____x", []string{"a\U0001F600"}), "a\U0001F600_x", "a\U0001F600:x", "a\U0001F600", "a___x", "a__:x", "a__", "x", "mcp__a____x", "mcp_call")
	})
	t.Run("every configured name that sanitizes to the server is kept, in either order", func(t *testing.T) {
		eqs(t, CreatePiMcpToolTargets("mcp__a_b__x", []string{"a_b", "a.b"}), "a_b_x", "a_b:x", "a_b", "a.b_x", "a.b:x", "a.b", "x", "mcp__a_b__x", "mcp_call")
	})
}
