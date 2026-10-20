package pi_permission_system

import (
	"os"
	"strings"
	"testing"
)

// Twins of test/path/pi-infrastructure-read.test.ts.

func TestPiInfrastructureReadTwins(t *testing.T) {
	const f = "path/pi-infrastructure-read"
	px, wn := PosixPathFlavor, Win32PathFlavor

	t.Run("discoverGlobalNodeModulesRoot", func(t *testing.T) {
		if HostPath == Win32Path {
			t.Skip("the URLs below are POSIX paths")
		}
		setup := func(t *testing.T) {
			old := spawnNpmRootGlobal
			spawnNpmRootGlobal = func() (int, string, bool) { return 1, "", true }
			t.Cleanup(func() { spawnNpmRootGlobal = old })
		}
		d := func(t *testing.T, url string) (string, bool) { setup(t); return DiscoverGlobalNodeModulesRoot(url) }
		tw(t, f, "returns the node_modules dir when the file is inside one", func(t *testing.T) {
			r, ok := d(t, "file:///opt/homebrew/lib/node_modules/pi-permission-system/dist/external-directory.js")
			eqv(t, ok, true)
			eqv(t, r, "/opt/homebrew/lib/node_modules")
		})
		tw(t, f, "returns node_modules for a deeply nested file", func(t *testing.T) {
			r, _ := d(t, "file:///home/user/.nvm/versions/node/v20/lib/node_modules/pi-permission-system/src/external-directory.js")
			eqv(t, r, "/home/user/.nvm/versions/node/v20/lib/node_modules")
		})
		tw(t, f, "returns node_modules for a bun global install path", func(t *testing.T) {
			r, _ := d(t, "file:///home/user/.bun/install/global/node_modules/pi-permission-system/dist/external-directory.js")
			eqv(t, r, "/home/user/.bun/install/global/node_modules")
		})
		tw(t, f, "returns the innermost (closest-to-file) node_modules ancestor", func(t *testing.T) {
			r, _ := d(t, "file:///opt/lib/node_modules/some-pkg/node_modules/pi-permission-system/dist/index.js")
			eqv(t, r, "/opt/lib/node_modules/some-pkg/node_modules")
		})
		tw(t, f, "returns null when the file is not inside any node_modules directory", func(t *testing.T) {
			_, ok := d(t, "file:///home/user/development/pi-permission-system/dist/external-directory.js")
			eqv(t, ok, false)
		})
		tw(t, f, "returns null for a root-level file", func(t *testing.T) {
			_, ok := d(t, "file:///external-directory.js")
			eqv(t, ok, false)
		})
		tw(t, f, "returns null for an invalid URL", func(t *testing.T) {
			_, ok := d(t, "not-a-url")
			eqv(t, ok, false)
		})
		tw(t, f, "works with the real import.meta.url of this extension (smoke test)", func(t *testing.T) {
			// The Go port has no import.meta.url: the module URL is the running executable's.
			exe, err := os.Executable()
			if err != nil {
				t.Skip(err)
			}
			r, ok := d(t, "file://"+exe)
			eqv(t, !ok || strings.HasSuffix(r, "node_modules"), true)
		})
		tw(t, f, "the discovered path includes the pi-permission-system package directory", func(t *testing.T) {
			r, ok := d(t, "file:///opt/homebrew/lib/node_modules/pi-permission-system/dist/external-directory.js")
			eqv(t, ok, true)
			eqv(t, HostPath.Join(r, "pi-permission-system"), "/opt/homebrew/lib/node_modules/pi-permission-system")
		})
	})

	infraDirs := []string{"/home/user/.pi/agent", "/home/user/.pi/agent/git", "/opt/homebrew/lib/node_modules"}
	const cwd = "/home/user/project"
	read := func(tool, path string, dirs []string, c string, fl PathFlavor, excluded ...string) bool {
		return IsPiInfrastructureRead(tool, path, dirs, c, fl, excluded)
	}
	t.Run("isPiInfrastructureRead", func(t *testing.T) {
		tw(t, f, "allows 'read' tool for a file inside agentDir", func(t *testing.T) {
			eqv(t, read("read", "/home/user/.pi/agent/extensions/pi-permission-system/config.json", infraDirs, cwd, px), true)
		})
		tw(t, f, "allows 'find' tool for a path inside node_modules infra dir", func(t *testing.T) {
			eqv(t, read("find", "/opt/homebrew/lib/node_modules/pi-ask-user/skills", infraDirs, cwd, px), true)
		})
		tw(t, f, "allows 'grep' tool for a path inside agentDir/git", func(t *testing.T) {
			eqv(t, read("grep", "/home/user/.pi/agent/git/some-package/README.md", infraDirs, cwd, px), true)
		})
		tw(t, f, "allows 'ls' tool for a path inside node_modules infra dir", func(t *testing.T) {
			eqv(t, read("ls", "/opt/homebrew/lib/node_modules/pi-permission-system", infraDirs, cwd, px), true)
		})
		tw(t, f, "blocks 'write' tool for a file inside agentDir", func(t *testing.T) {
			eqv(t, read("write", "/home/user/.pi/agent/extensions/pi-permission-system/config.json", infraDirs, cwd, px), false)
		})
		tw(t, f, "blocks 'edit' tool for a file inside node_modules", func(t *testing.T) {
			eqv(t, read("edit", "/opt/homebrew/lib/node_modules/pi-ask-user/skills/ask-user/SKILL.md", infraDirs, cwd, px), false)
		})
		tw(t, f, "blocks 'bash' tool regardless of path", func(t *testing.T) {
			eqv(t, read("bash", "/opt/homebrew/lib/node_modules/pi-ask-user/SKILL.md", infraDirs, cwd, px), false)
		})
		tw(t, f, "does not allow 'read' for a path outside all infra dirs", func(t *testing.T) {
			eqv(t, read("read", "/etc/passwd", infraDirs, cwd, px), false)
		})
		tw(t, f, "does not allow 'read' for a path only partially matching an infra dir prefix", func(t *testing.T) {
			eqv(t, read("read", "/home/user/.pi/agent-other/config.json", infraDirs, cwd, px), false)
		})
		tw(t, f, "allows 'read' for a path inside project-local .pi/npm/", func(t *testing.T) {
			eqv(t, read("read", cwd+"/.pi/npm/node_modules/some-skill/SKILL.md", infraDirs, cwd, px), true)
		})
		tw(t, f, "allows 'read' for a path inside project-local .pi/git/", func(t *testing.T) {
			eqv(t, read("read", cwd+"/.pi/git/github.com/org/skill-repo/SKILL.md", infraDirs, cwd, px), true)
		})
		tw(t, f, "blocks 'write' for a path inside project-local .pi/npm/", func(t *testing.T) {
			eqv(t, read("write", cwd+"/.pi/npm/node_modules/some-skill/SKILL.md", infraDirs, cwd, px), false)
		})
		tw(t, f, "returns false when infrastructureDirs is empty and path is not project-local", func(t *testing.T) {
			eqv(t, read("read", "/etc/passwd", nil, cwd, px), false)
		})
		tw(t, f, "returns false when infrastructureDirs is empty but path IS project-local .pi/npm", func(t *testing.T) {
			eqv(t, read("read", cwd+"/.pi/npm/node_modules/x/SKILL.md", nil, cwd, px), true)
		})
	})
	t.Run("isPiInfrastructureRead with glob patterns", func(t *testing.T) {
		const versioned = "/opt/homebrew/Cellar/pi-coding-agent/0.74.0/libexec/lib/node_modules/@earendil-works/pi-coding-agent/SKILL.md"
		tw(t, f, "glob entry matches a versioned nested path", func(t *testing.T) {
			eqv(t, read("read", versioned, []string{"/opt/homebrew/*/@earendil-works/pi-coding-agent/*"}, cwd, px), true)
		})
		tw(t, f, "** behaves the same as * (matches across path separators)", func(t *testing.T) {
			eqv(t, read("read", versioned, []string{"/opt/homebrew/**/@earendil-works/pi-coding-agent/**"}, cwd, px), true)
		})
		tw(t, f, "glob entry does not match an unrelated path", func(t *testing.T) {
			eqv(t, read("read", "/etc/passwd", []string{"/opt/homebrew/*/@earendil-works/pi-coding-agent/*"}, cwd, px), false)
		})
		tw(t, f, "? matches exactly one character", func(t *testing.T) {
			eqv(t, read("read", "/opt/homebrew/X/file.md", []string{"/opt/homebrew/?/file.md"}, cwd, px), true)
		})
		tw(t, f, "? does not match multiple characters", func(t *testing.T) {
			eqv(t, read("read", "/opt/homebrew/abc/file.md", []string{"/opt/homebrew/?/file.md"}, cwd, px), false)
		})
		tw(t, f, "mixed array of plain dirs and glob patterns — both branches work", func(t *testing.T) {
			dirs := []string{"/home/user/.pi/agent", "/opt/homebrew/*/@earendil-works/pi-coding-agent/*"}
			eqv(t, read("read", "/home/user/.pi/agent/config.json", dirs, cwd, px), true)
			eqv(t, read("read", versioned, dirs, cwd, px), true)
		})
		tw(t, f, "plain entry with ~ prefix matches after home expansion", func(t *testing.T) {
			home := homeDir()
			eqv(t, read("read", home+"/.pi/agent/config.json", []string{"~/.pi/agent"}, cwd, px), true)
		})
		tw(t, f, "write tool with a glob-matching path is still rejected", func(t *testing.T) {
			eqv(t, read("write", versioned, []string{"/opt/homebrew/**/@earendil-works/pi-coding-agent/**"}, cwd, px), false)
		})
	})
	t.Run("isPiInfrastructureRead on win32", func(t *testing.T) {
		tw(t, f, "plain infra dir matches a case-different path", func(t *testing.T) {
			eqv(t, read("read", `c:\users\foo\.pi\agent\config.json`, []string{`C:\Users\Foo\.pi\agent`}, `C:\proj`, wn), true)
		})
		tw(t, f, "glob infra dir matches case-insensitively", func(t *testing.T) {
			eqv(t, read("read", `c:\users\foo\npm\node_modules\@earendil-works\pi-coding-agent\skill.md`, []string{`C:\Users\Foo\**\pi-coding-agent\**`}, `C:\proj`, wn), true)
		})
		tw(t, f, "rejects a path outside every infra dir", func(t *testing.T) {
			eqv(t, read("read", `c:\windows\system32\drivers\etc\hosts`, []string{`C:\Users\Foo\.pi\agent`}, `C:\proj`, wn), false)
		})
		tw(t, f, "an excluded dir excludes a case-different path", func(t *testing.T) {
			eqv(t, read("read", `c:\users\foo\.pi\agent\logs\review.jsonl`, []string{`C:\Users\Foo\.pi\agent`}, `C:\proj`, wn, `C:\Users\Foo\.pi\agent\Logs`), false)
		})
	})
	t.Run("isPiInfrastructureRead with excluded directories", func(t *testing.T) {
		const c = "/projects/my-app"
		dirs, excluded := []string{"/a"}, []string{"/a/x/logs"}
		tw(t, f, "does not admit a file inside an excluded dir within a root", func(t *testing.T) {
			eqv(t, read("read", "/a/x/logs/review.jsonl", dirs, c, px, excluded...), false)
		})
		tw(t, f, "does not admit the excluded dir itself", func(t *testing.T) { eqv(t, read("ls", "/a/x/logs", dirs, c, px, excluded...), false) })
		tw(t, f, "still admits a sibling of the excluded dir", func(t *testing.T) {
			eqv(t, read("read", "/a/x/config.json", dirs, c, px, excluded...), true)
		})
		tw(t, f, "wins over a root that names the excluded dir explicitly", func(t *testing.T) {
			eqv(t, read("read", "/a/x/logs/review.jsonl", []string{"/a", "/a/x/logs"}, c, px, excluded...), false)
		})
		tw(t, f, "expands a ~-prefixed excluded dir", func(t *testing.T) {
			home := homeDir()
			eqv(t, read("read", HostPath.Join(home, ".pi", "agent", "logs", "review.jsonl"), []string{HostPath.Join(home, ".pi", "agent")}, c, px, "~/.pi/agent/logs"), false)
		})
	})
}
