package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Twins of test/handlers/external-directory-symlink-acceptance.test.ts (issue #418): a real symlink, no realpath mock.

func TestExternalDirectorySymlinkAcceptanceTwins(t *testing.T) {
	const f = "handlers/external-directory-symlink-acceptance"
	jstr := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	// fixture builds realDir, a symlink linkDir to it, and a cwd, each in its own temp root.
	fixture := func(t *testing.T) (realDir, linkDir, cwd string) {
		t.Helper()
		realDir = t.TempDir()
		linkDir = filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(realDir, linkDir); err != nil {
			t.Fatal(err)
		}
		cwd = t.TempDir()
		return
	}
	resolverFor := func(t *testing.T, cwd, permissionJSON string) *PermissionResolver {
		t.Helper()
		m := managerWithConfig(t, permissionJSON)
		m.ConfigureForCwd(cwd)
		return NewPermissionResolver(m, &SessionRules{})
	}
	readState := func(t *testing.T, realDir, linkDir, cwd, permissionJSON string) string {
		t.Helper()
		tcc := ToolCallContext{ToolName: "read", Input: obj("path", filepath.Join(linkDir, "file.ts")), ToolCallID: "tc-1", Cwd: cwd}
		got := DescribeExternalDirectoryGate(tcc, InfrastructureReadScope{Dirs: []string{}, ExcludedDirs: []string{}}, resolverFor(t, cwd, permissionJSON),
			NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), cwd), nil)
		d, ok := got.(*GateDescriptor)
		if !ok {
			t.Fatalf("got %T, want a descriptor", got)
		}
		return d.PreCheck.State
	}

	tw(t, f, "allows a path-bearing tool when the allow is keyed on the typed (symlinked) path", func(t *testing.T) {
		realDir, linkDir, cwd := fixture(t)
		eq(t, readState(t, realDir, linkDir, cwd, `{"external_directory":{"*":"ask",`+jstr(linkDir+"/*")+`:"allow"}}`), "allow", "state")
	})
	tw(t, f, "allows a path-bearing tool when the allow is keyed on the resolved path", func(t *testing.T) {
		// Key the allow on the fully symlink-resolved directory (on macOS the tmpdir root itself is a symlink, /var to /private/var).
		realDir, linkDir, cwd := fixture(t)
		resolved, err := filepath.EvalSymlinks(realDir)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, readState(t, realDir, linkDir, cwd, `{"external_directory":{"*":"ask",`+jstr(resolved+"/*")+`:"allow"}}`), "allow", "state")
	})
	tw(t, f, "still prompts (ask) when no external_directory allow matches", func(t *testing.T) {
		realDir, linkDir, cwd := fixture(t)
		eq(t, readState(t, realDir, linkDir, cwd, `{"external_directory":{"*":"ask"}}`), "ask", "state")
	})
	tw(t, f, "allows a bash command referencing the typed (symlinked) path", func(t *testing.T) {
		_, linkDir, cwd := fixture(t)
		resolver := resolverFor(t, cwd, `{"external_directory":{"*":"ask",`+jstr(linkDir+"/*")+`:"allow"}}`)
		command := "cat " + filepath.Join(linkDir, "file.ts")
		tcc := ToolCallContext{ToolName: "bash", Input: obj("command", command), ToolCallID: "tc-2", Cwd: cwd}
		n := NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), cwd)
		program, err := ParseBashProgram(command, n, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Covered by a config allow: no gate, no prompt, no session entry.
		eq(t, DescribeBashExternalDirectoryGate(tcc, program, resolver, n) == nil, true, "null")
	})
}
