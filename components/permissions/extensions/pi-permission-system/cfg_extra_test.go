package pi_permission_system

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Behaviour the upstream tests do not state but the original has, found by mutating the port.

func TestLegacyExtensionConfigNeedsARoot(t *testing.T) {
	// with no install directory the legacy path would be the relative `config.json`: a file in the process's own directory
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "config.json"), `{"yoloMode":true}`)
	t.Chdir(cwd)
	r := loadAndMergeConfigs(filepath.Join(cwd, "agent"), filepath.Join(cwd, "project"), "", MergeOptions{})
	eq(t, r.Merged.YoloMode == nil, true, "the process directory's config.json is not read")
	eq(t, r.Issues, []string{}, "issues")
}

func TestFileStampHasSubSecondResolution(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	writeTestFile(t, p, "x")
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, m := range []time.Duration{0, time.Millisecond} {
		if err := os.Chtimes(p, base.Add(m), base.Add(m)); err != nil {
			t.Fatal(err)
		}
	}
	a := fileStamp(p)
	if err := os.Chtimes(p, base.Add(2*time.Millisecond), base.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	eq(t, a != fileStamp(p), true, "a change within one second moves the stamp (String(mtimeMs) keeps the fraction)")
	eq(t, fileStamp(filepath.Join(t.TempDir(), "absent")), "missing", "missing")
}

func TestProjectAgentScopeNeedsADirectory(t *testing.T) {
	cwd := t.TempDir()
	writeTestFile(t, filepath.Join(cwd, "coder.md"), "---\npermission:\n  bash: allow\n---\n")
	t.Chdir(cwd)
	l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents"})
	eq(t, *l.LoadProjectAgentConfig("coder"), ScopeConfig{}, "no project agents directory: no scope, whatever the process directory holds")
}

func TestPolicyLoaderCachesEveryScope(t *testing.T) {
	base := t.TempDir()
	proj := filepath.Join(base, "project.json")
	agents, pagents := filepath.Join(base, "agents"), filepath.Join(base, "pagents")
	writeTestFile(t, proj, `{"permission":{"bash":"allow"}}`)
	writeTestFile(t, filepath.Join(agents, "a.md"), "---\npermission:\n  read: allow\n---\n")
	writeTestFile(t, filepath.Join(pagents, "a.md"), "---\npermission:\n  write: allow\n---\n")
	l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: filepath.Join(base, "none.json"), AgentsDir: agents, ProjectGlobalConfigPath: proj, ProjectAgentsDir: pagents})
	eq(t, l.LoadProjectConfig() == l.LoadProjectConfig(), true, "project scope cached")
	eq(t, l.LoadAgentConfig("a") == l.LoadAgentConfig("a"), true, "agent scope cached")
	eq(t, l.LoadProjectAgentConfig("a") == l.LoadProjectAgentConfig("a"), true, "project agent scope cached")
	first := l.LoadProjectConfig()
	future := time.Now().Add(5 * time.Second)
	writeTestFile(t, proj, `{"permission":{"bash":"deny"}}`)
	_ = os.Chtimes(proj, future, future)
	eq(t, permJSON(l.LoadProjectConfig().Permission), `{"bash":"deny"}`, "reloaded when the file changed")
	eq(t, l.LoadProjectConfig() != first, true, "a new value")
	// a scope that was missing is loaded once the file appears
	eq(t, *l.LoadAgentConfig("b"), ScopeConfig{}, "absent")
	writeTestFile(t, filepath.Join(agents, "b.md"), "---\npermission:\n  edit: ask\n---\n")
	eq(t, permJSON(l.LoadAgentConfig("b").Permission), `{"edit":"ask"}`, "appeared")
}

func TestPolicyLoaderMcpOverrideAndStamps(t *testing.T) {
	base := t.TempDir()
	mcp := filepath.Join(base, "mcp.json")
	writeTestFile(t, mcp, `{"mcpServers":{"exa":{}}}`)
	o := PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", GlobalMcpConfigPath: mcp}
	eq(t, NewFilePolicyLoader(o).GetConfiguredMcpServerNames(), []string{"exa"}, "read from disk")
	o.McpServerNames = []string{}
	eq(t, NewFilePolicyLoader(o).GetConfiguredMcpServerNames(), []string{}, "an empty override still overrides")
	o.McpServerNames = []string{" exa ", "exa", "", "  ", "research"}
	eq(t, NewFilePolicyLoader(o).GetConfiguredMcpServerNames(), []string{"exa", "research"}, "trimmed, deduplicated, blanks dropped")

	l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents"})
	eq(t, l.GetCacheStamp(""), "missing|none|missing|none", "no project, no agent")
	l = NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents",
		ProjectGlobalConfigPath: "/nonexistent/p.json", ProjectAgentsDir: "/nonexistent/pa"})
	eq(t, l.GetCacheStamp("x"), "missing|missing|missing|missing", "everything configured, nothing there")
	eq(t, l.GetCacheStamp(""), "missing|missing|missing|none", "a project agents directory without an agent")
}

func TestDerivePolicyLoaderOptions(t *testing.T) {
	g := derivePolicyLoaderOptions("/a", "")
	eq(t, g, PolicyLoaderOptions{GlobalConfigPath: getGlobalConfigPath("/a"), AgentsDir: filepath.Join("/a", "agents"), GlobalMcpConfigPath: filepath.Join("/a", "mcp.json")}, "no cwd: global only")
	p := derivePolicyLoaderOptions("/a", "/proj")
	eq(t, p.ProjectGlobalConfigPath, filepath.Join("/proj", ".pi", "extensions", "pi-permission-system", "config.json"), "project config")
	eq(t, p.ProjectAgentsDir, filepath.Join("/proj", ".pi", "agents"), "project agents")
	eq(t, p.ProjectMcpConfigPath, filepath.Join("/proj", ".pi", "mcp.json"), "project mcp")
	eq(t, p.GlobalMcpConfigPath, filepath.Join("/a", "mcp.json"), "global mcp")
	eq(t, p.AgentsDir, filepath.Join("/a", "agents"), "agents")
}

func TestConfigLoadedLogCarriesTheTrust(t *testing.T) {
	for _, trusted := range []bool{true, false} {
		r := newStoreRig()
		r.store.Refresh("/p", trusted)
		eq(t, r.logger.debugOf("config.loaded")[0].vals["projectTrusted"], trusted, "projectTrusted")
	}
}

func TestLogsDirectoryIsOwnerOnlyAllTheWayDown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	base := t.TempDir()
	logs := filepath.Join(base, "extensions", "pi-permission-system", "logs")
	if m := ensurePermissionSystemLogsDirectory(logs); m != "" {
		t.Fatal(m)
	}
	// mkdirSync({ recursive: true, mode }) gives every directory it creates the mode (the umask only removes bits)
	st, _ := os.Stat(filepath.Join(base, "extensions"))
	eq(t, st.Mode().Perm()&0o077, os.FileMode(0), "the parent directories grant nothing to group or others")

	blocker := filepath.Join(base, "file")
	writeTestFile(t, blocker, "x")
	msg := ensurePermissionSystemLogsDirectory(filepath.Join(blocker, "logs"))
	eq(t, strings.HasPrefix(msg, "Failed to create permission-system log directory '"+filepath.Join(blocker, "logs")+"': "), true, "message: "+msg)
	containsAll(t, msg, "ENOTDIR")
}

// memStore is a CommandConfigStore that may refuse a save.
type memStore struct {
	config PermissionSystemExtensionConfig
	refuse bool
}

func (s *memStore) Current() PermissionSystemExtensionConfig { return s.config }
func (s *memStore) Save(next PermissionSystemExtensionConfig, _ ConfigUI) {
	if !s.refuse {
		s.config = next
	}
}

func TestSettingsModalAppliesEachSetting(t *testing.T) {
	store := &memStore{config: defaultExtensionConfig()}
	modal := &settingsModal{ctx: &stubCommandContext{}, controller: PermissionSystemConfigController{Config: store}, current: store.config}
	change := func(id, v string) {
		if _, err := modal.HandleViewEvent(kit.Event{Type: kit.EventChange, ID: id, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	change("yoloMode", "on")
	change("permissionReviewLog", "off")
	change("debugLog", "on")
	change("doublePressToConfirm", "off")
	eq(t, store.config, PermissionSystemExtensionConfig{YoloMode: true, PermissionReviewLog: false, DebugLog: true, DoublePressToConfirm: false}, "all four saved")
	change("debugLog", "off")
	change("doublePressToConfirm", "on")
	change("permissionReviewLog", "on")
	change("yoloMode", "off")
	eq(t, store.config, defaultExtensionConfig(), "and back")
	change("unknown", "on")
	eq(t, store.config, defaultExtensionConfig(), "an unknown id changes nothing")
}

func TestSettingsModalShowsWhatTheStoreKept(t *testing.T) {
	store := &memStore{config: defaultExtensionConfig(), refuse: true}
	modal := &settingsModal{ctx: &stubCommandContext{}, controller: PermissionSystemConfigController{Config: store}, current: store.config}
	_, _ = modal.HandleViewEvent(kit.Event{Type: kit.EventChange, ID: "yoloMode", Value: "on"})
	eq(t, modal.View(80).Root.(*kit.SettingsList).Items[0].CurrentValue, "off", "a refused save leaves the row at the stored value in the next frame")
}
