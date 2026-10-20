package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type logEntry struct {
	event   string
	details *jsObject
}

type fakeStoreLogger struct{ debug, review []logEntry }

func (l *fakeStoreLogger) Debug(e string, d *jsObject)  { l.debug = append(l.debug, logEntry{e, d}) }
func (l *fakeStoreLogger) Review(e string, d *jsObject) { l.review = append(l.review, logEntry{e, d}) }
func (l *fakeStoreLogger) debugOf(event string) []*jsObject {
	var out []*jsObject
	for _, e := range l.debug {
		if e.event == event {
			out = append(out, e.details)
		}
	}
	return out
}

type fakePolicyPaths struct{ calls int }

func (p *fakePolicyPaths) GetResolvedPolicyPaths() ResolvedPolicyPaths {
	p.calls++
	return ResolvedPolicyPaths{GlobalConfigPath: "/agent/config.json", AgentsDir: "/agent/agents"}
}

type storeRig struct {
	store            *ConfigStore
	logger           *fakeStoreLogger
	paths            *fakePolicyPaths
	merge            MergedConfigResult
	mergeCalls       []mergeCall
	unified          UnifiedConfigLoadResult
	statusSyncs      int
	mkdirErr         error
	writes           []writeCall
	renames, removes int
	existing         func(string) bool
	existsCalls      []string
	builtWith        []BuildResolvedConfigLogEntryOptions
}
type mergeCall struct {
	agentDir, cwd string
	opts          MergeOptions
}
type writeCall struct{ path, data string }

type fakeUI struct{ notes [][]string }

func (u *fakeUI) Notify(m, l string)       { u.notes = append(u.notes, []string{m, l}) }
func (u *fakeUI) SetStatus(string, string) {}

func newStoreRig() *storeRig {
	r := &storeRig{logger: &fakeStoreLogger{}, paths: &fakePolicyPaths{}, existing: func(string) bool { return false }}
	r.store = NewConfigStore(ConfigStoreDeps{AgentDir: "/test/agent", PolicyPaths: r.paths, Logger: r.logger})
	r.store.hooks = configStoreHooks{
		loadAndMerge: func(agentDir, cwd, _ string, opts MergeOptions) MergedConfigResult {
			r.mergeCalls = append(r.mergeCalls, mergeCall{agentDir, cwd, opts})
			return r.merge
		},
		loadUnified: func(string) UnifiedConfigLoadResult { return r.unified },
		syncStatus:  func(ConfigUI, PermissionSystemExtensionConfig) { r.statusSyncs++ },
		buildEntry: func(o BuildResolvedConfigLogEntryOptions) ResolvedConfigLogEntry {
			r.builtWith = append(r.builtWith, o)
			return buildResolvedConfigLogEntry(o)
		},
		exists:    func(p string) bool { r.existsCalls = append(r.existsCalls, p); return r.existing(p) },
		mkdirAll:  func(string) error { return r.mkdirErr },
		writeFile: func(p, d string) error { r.writes = append(r.writes, writeCall{p, d}); return nil },
		rename:    func(string, string) error { r.renames++; return nil },
		remove:    func(string) error { r.removes++; return nil },
	}
	return r
}

func TestConfigStoreTwins(t *testing.T) {
	const f = "config/config-store"
	boolsOf := func(debug, review, yolo bool) UnifiedConfig {
		return UnifiedConfig{DebugLog: &debug, PermissionReviewLog: &review, YoloMode: &yolo}
	}
	tw(t, f, "returns DEFAULT_EXTENSION_CONFIG before any refresh", func(t *testing.T) {
		eq(t, newStoreRig().store.Current(), defaultExtensionConfig(), "current")
	})
	tw(t, f, "answers empty before any refresh", func(t *testing.T) { eq(t, newStoreRig().store.GetConfigIssues(), []string{}, "issues") })
	tw(t, f, "answers the issues the last load produced", func(t *testing.T) {
		r := newStoreRig()
		r.merge = MergedConfigResult{Issues: []string{"first issue", "second issue"}}
		r.store.Refresh("/test/project", true)
		eq(t, r.store.GetConfigIssues(), []string{"first issue", "second issue"}, "issues")
	})
	tw(t, f, "answers empty again once a reload finds the config clean", func(t *testing.T) {
		r := newStoreRig()
		r.merge = MergedConfigResult{Issues: []string{"transient issue"}}
		r.store.Refresh("/test/project", true)
		r.merge = MergedConfigResult{Issues: []string{}}
		r.store.Refresh("/test/project", true)
		eq(t, len(r.store.GetConfigIssues()), 0, "issues")
	})
	tw(t, f, "uses the passed cwd for loadAndMergeConfigs and includes the project scope when trusted", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("/my/project", true)
		eq(t, len(r.mergeCalls), 1, "calls")
		eq(t, r.mergeCalls[0].agentDir+"|"+r.mergeCalls[0].cwd, "/test/agent|/my/project", "args")
		eq(t, *r.mergeCalls[0].opts.IncludeProjectScope, true, "includeProjectScope")
	})
	tw(t, f, "withholds the project scope when the project is untrusted", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("/my/project", false)
		eq(t, *r.mergeCalls[0].opts.IncludeProjectScope, false, "includeProjectScope")
	})
	tw(t, f, "uses empty string cwd when no cwd is provided", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("", true)
		eq(t, r.mergeCalls[0].cwd, "", "cwd")
		eq(t, *r.mergeCalls[0].opts.IncludeProjectScope, true, "includeProjectScope")
	})
	tw(t, f, "updates current() with normalized merged result", func(t *testing.T) {
		r := newStoreRig()
		r.merge = MergedConfigResult{Merged: boolsOf(true, false, false)}
		r.store.Refresh("", true)
		eq(t, r.store.Current().DebugLog, true, "debugLog")
		eq(t, r.store.Current().PermissionReviewLog, false, "review log")
	})
	tw(t, f, "writes config.loaded debug log", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("", true)
		got := r.logger.debugOf("config.loaded")
		eq(t, len(got), 1, "entries")
		eq(t, got[0].vals["debugLog"], false, "debugLog")
	})
	// `config.loaded` is the durable record of what the load found; the operator-facing notification is ConfigIssueReporter's job
	tw(t, f, "records the issues on config.loaded as one joined string", func(t *testing.T) {
		r := newStoreRig()
		r.merge = MergedConfigResult{Issues: []string{"first issue", "second issue"}}
		r.store.Refresh("", true)
		eq(t, r.logger.debugOf("config.loaded")[0].vals["warning"], "first issue\nsecond issue", "warning")
	})
	tw(t, f, "records a null warning on config.loaded when the config is clean", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("", true)
		w, present := r.logger.debugOf("config.loaded")[0].vals["warning"]
		eq(t, present && w == nil, true, "null warning")
	})
	tw(t, f, "does not sync the status bar", func(t *testing.T) {
		r := newStoreRig()
		r.store.Refresh("/my/project", true)
		eq(t, r.statusSyncs, 0, "status syncs")
	})
	tw(t, f, "carries piInfrastructureReadPaths from merged config into current()", func(t *testing.T) {
		r := newStoreRig()
		r.merge = MergedConfigResult{Merged: UnifiedConfig{PiInfrastructureReadPaths: []string{"/extra/path"}}}
		r.store.Refresh("", true)
		eq(t, r.store.Current().PiInfrastructureReadPaths, []string{"/extra/path"}, "paths")
	})

	tw(t, f, "writes merged config to the global path", func(t *testing.T) {
		r := newStoreRig()
		r.unified = UnifiedConfigLoadResult{Config: cfg(t, `{"permission":{"*":"ask"}}`)}
		next := defaultExtensionConfig()
		next.DebugLog = true
		r.store.Save(next, &fakeUI{})
		eq(t, len(r.writes), 1, "writes")
		eq(t, strings.Contains(r.writes[0].path, ".tmp"), true, "tmp path")
		eq(t, strings.Contains(r.writes[0].data, `"debugLog": true`), true, "data")
		eq(t, r.renames > 0, true, "renamed")
	})
	tw(t, f, "updates current() after a successful save", func(t *testing.T) {
		r := newStoreRig()
		next := defaultExtensionConfig()
		next.DebugLog = true
		r.store.Save(next, &fakeUI{})
		eq(t, r.store.Current().DebugLog, true, "debugLog")
	})
	tw(t, f, "calls syncPermissionSystemStatus after a successful save", func(t *testing.T) {
		r := newStoreRig()
		r.store.Save(defaultExtensionConfig(), &fakeUI{})
		eq(t, r.statusSyncs, 1, "status syncs")
	})
	tw(t, f, "writes config.saved debug log after a successful save", func(t *testing.T) {
		r := newStoreRig()
		r.store.Save(defaultExtensionConfig(), &fakeUI{})
		got := r.logger.debugOf("config.saved")
		eq(t, len(got), 1, "entries")
		eq(t, got[0].vals["debugLog"], false, "debugLog")
	})
	tw(t, f, "notifies with error and returns early when write fails", func(t *testing.T) {
		r := newStoreRig()
		ui := &fakeUI{}
		r.mkdirErr = errors.New("disk full")
		r.store.Save(defaultExtensionConfig(), ui)
		eq(t, len(ui.notes), 1, "notifications")
		eq(t, strings.Contains(ui.notes[0][0], "Failed to save"), true, "message")
		eq(t, ui.notes[0][1], "error", "level")
		// current() is not updated on failure, and nothing is logged as saved
		eq(t, r.store.Current(), defaultExtensionConfig(), "current")
		eq(t, len(r.logger.debugOf("config.saved")), 0, "saved logs")
	})
	tw(t, f, "attempts cleanup of tmp file when write fails and tmp exists", func(t *testing.T) {
		r := newStoreRig()
		r.mkdirErr = errors.New("disk full")
		r.existing = func(string) bool { return true }
		r.store.Save(defaultExtensionConfig(), &fakeUI{})
		eq(t, r.removes > 0, true, "unlinked")
	})
	tw(t, f, "preserves an existing global toolInputPreviewMaxLength on save", func(t *testing.T) {
		r := newStoreRig()
		r.unified = UnifiedConfigLoadResult{Config: cfg(t, `{"toolInputPreviewMaxLength":800}`)}
		r.store.Save(defaultExtensionConfig(), &fakeUI{})
		eq(t, strings.Contains(r.writes[0].data, `"toolInputPreviewMaxLength": 800`), true, "data")
	})
	tw(t, f, "preserves an existing global piInfrastructureReadPaths on save", func(t *testing.T) {
		r := newStoreRig()
		r.unified = UnifiedConfigLoadResult{Config: cfg(t, `{"piInfrastructureReadPaths":["/extra/path"]}`)}
		r.store.Save(defaultExtensionConfig(), &fakeUI{})
		eq(t, strings.Contains(r.writes[0].data, `"piInfrastructureReadPaths"`), true, "data")
	})

	tw(t, f, "writes config.resolved to both review and debug logs", func(t *testing.T) {
		r := newStoreRig()
		r.store.LogResolvedPaths("")
		eq(t, len(r.logger.review), 1, "review")
		eq(t, r.logger.review[0].event, "config.resolved", "review event")
		eq(t, len(r.logger.debugOf("config.resolved")), 1, "debug")
	})
	tw(t, f, "calls getResolvedPolicyPaths from the provider", func(t *testing.T) {
		r := newStoreRig()
		r.store.LogResolvedPaths("")
		eq(t, r.paths.calls, 1, "calls")
	})
	tw(t, f, "passes legacy detection results to buildResolvedConfigLogEntry", func(t *testing.T) {
		r := newStoreRig()
		r.existing = func(p string) bool { return strings.Contains(p, "policies.json") }
		r.store.LogResolvedPaths("/some/project")
		eq(t, len(r.builtWith), 1, "builds")
		eq(t, r.builtWith[0].LegacyGlobalPolicyDetected || r.builtWith[0].LegacyProjectPolicyDetected || r.builtWith[0].LegacyExtensionConfigDetected, false, "flags are booleans, all false here")
	})
	tw(t, f, "does not check project legacy path when no cwd is provided", func(t *testing.T) {
		r := newStoreRig()
		r.store.LogResolvedPaths("")
		for _, p := range r.existsCalls {
			eq(t, strings.Contains(p, "null"), false, "project legacy path "+p)
			eq(t, strings.Contains(p, filepath.Join(".pi", "agent", "pi-permissions.jsonc")), false, "project legacy path "+p)
		}
	})
}

func TestStoreTouchesNoFileWithoutAnExtensionRoot(t *testing.T) {
	r := newStoreRig()
	r.store.LogResolvedPaths("/some/project")
	for _, p := range r.existsCalls {
		eq(t, strings.HasSuffix(p, "config.json") && !strings.Contains(p, "extensions"), false, "legacy extension config looked for: "+p)
	}
}

func TestPolicyLoaderTwins(t *testing.T) {
	const f = "config/policy-loader"
	mk := func(t *testing.T, global string) (string, *FilePolicyLoader) {
		base := t.TempDir()
		agents := filepath.Join(base, "agents")
		if err := os.MkdirAll(agents, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(base, "config.json"), global)
		return base, NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: filepath.Join(base, "config.json"), AgentsDir: agents})
	}
	missing := PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents"}
	bump := func(t *testing.T, p string) {
		future := time.Now().Add(5 * time.Second)
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
	}
	tw(t, f, "returns ScopeConfig with permission from a valid config file", func(t *testing.T) {
		_, l := mk(t, `{"permission":{"*":"allow","read":"ask"}}`)
		eq(t, permJSON(l.LoadGlobalConfig().Permission), `{"*":"allow","read":"ask"}`, "permission")
	})
	tw(t, f, "returns empty ScopeConfig when config file is missing", func(t *testing.T) {
		eq(t, NewFilePolicyLoader(missing).LoadGlobalConfig().Permission == nil, true, "permission")
	})
	tw(t, f, "returns empty ScopeConfig when config file has no permission key", func(t *testing.T) {
		_, l := mk(t, `{"debugLog":true}`)
		eq(t, l.LoadGlobalConfig().Permission == nil, true, "permission")
	})
	tw(t, f, "returns empty ScopeConfig when no project path is configured", func(t *testing.T) {
		eq(t, *NewFilePolicyLoader(missing).LoadProjectConfig(), ScopeConfig{}, "scope")
	})
	tw(t, f, "returns ScopeConfig from a project config file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "project-config.json")
		writeTestFile(t, p, `{"permission":{"bash":"allow"}}`)
		l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", ProjectGlobalConfigPath: p})
		c := l.LoadProjectConfig()
		eq(t, permJSON(c.Permission), `{"bash":"allow"}`, "permission")
		eq(t, c.Invalid, false, "invalid")
	})
	tw(t, f, "marks the scope invalid when the project config file is rejected", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "project-config.json")
		// an unrecognized key: schema rejection, the issue's "typo" scenario
		writeTestFile(t, p, `{"debugLo":true}`)
		l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", ProjectGlobalConfigPath: p})
		eq(t, l.LoadProjectConfig().Invalid, true, "invalid")
	})
	tw(t, f, "leaves invalid unset when no project file is present", func(t *testing.T) {
		l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: "/nonexistent/config.json", AgentsDir: "/nonexistent/agents", ProjectGlobalConfigPath: "/nonexistent/project-config.json"})
		eq(t, l.LoadProjectConfig().Invalid, false, "invalid")
	})
	tw(t, f, "returns empty ScopeConfig when agentName is undefined", func(t *testing.T) {
		_, l := mk(t, `{}`)
		eq(t, *l.LoadAgentConfig(""), ScopeConfig{}, "scope")
	})
	tw(t, f, "returns ScopeConfig from agent frontmatter", func(t *testing.T) {
		base, l := mk(t, `{}`)
		writeTestFile(t, filepath.Join(base, "agents", "coder.md"), "---\npermission:\n  bash: allow\n---\n# Coder agent\n")
		eq(t, permJSON(l.LoadAgentConfig("coder").Permission), `{"bash":"allow"}`, "permission")
	})
	tw(t, f, "returns empty ScopeConfig when agent file does not exist", func(t *testing.T) {
		_, l := mk(t, `{}`)
		c := l.LoadAgentConfig("nonexistent")
		eq(t, *c, ScopeConfig{}, "scope")
		eq(t, c.Invalid, false, "invalid")
	})
	tw(t, f, "marks the scope invalid when an existing agent file cannot be read", func(t *testing.T) {
		base, l := mk(t, `{}`)
		// a directory at the agent's file path exists (stat succeeds) but reading it fails: a present-but-unreadable file
		if err := os.Mkdir(filepath.Join(base, "agents", "coder.md"), 0o755); err != nil {
			t.Fatal(err)
		}
		eq(t, l.LoadAgentConfig("coder").Invalid, true, "invalid")
	})
	tw(t, f, "returns empty ScopeConfig when no projectAgentsDir is configured", func(t *testing.T) {
		_, l := mk(t, `{}`)
		eq(t, *l.LoadProjectAgentConfig("coder"), ScopeConfig{}, "scope")
	})
	tw(t, f, "returns empty array before any loads", func(t *testing.T) { eq(t, NewFilePolicyLoader(missing).GetConfigIssues(), []string{}, "issues") })
	tw(t, f, "returns empty array for valid config", func(t *testing.T) {
		_, l := mk(t, `{"permission":{"*":"ask"}}`)
		l.LoadGlobalConfig()
		eq(t, l.GetConfigIssues(), []string{}, "issues")
	})
	tw(t, f, "returns correct paths and existence when files exist", func(t *testing.T) {
		base := t.TempDir()
		global, agents := filepath.Join(base, "config.json"), filepath.Join(base, "agents")
		writeTestFile(t, global, `{}`)
		if err := os.MkdirAll(agents, 0o755); err != nil {
			t.Fatal(err)
		}
		p := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: global, AgentsDir: agents}).GetResolvedPolicyPaths()
		eq(t, p.GlobalConfigPath, global, "global")
		eq(t, p.GlobalConfigExists, true, "global exists")
		eq(t, p.AgentsDir, agents, "agents")
		eq(t, p.AgentsDirExists, true, "agents exists")
		eq(t, p.ProjectConfigPath == nil, true, "project null")
		eq(t, p.ProjectConfigExists, false, "project exists")
		eq(t, p.ProjectAgentsDir == nil, true, "project agents null")
		eq(t, p.ProjectAgentsDirExists, false, "project agents exists")
	})
	tw(t, f, "returns a string stamp", func(t *testing.T) { eq(t, len(NewFilePolicyLoader(missing).GetCacheStamp("")) > 0, true, "stamp") })
	tw(t, f, "changes when the global config file changes", func(t *testing.T) {
		base, l := mk(t, `{}`)
		stamp1 := l.GetCacheStamp("")
		writeTestFile(t, filepath.Join(base, "config.json"), `{"permission": {}}`)
		bump(t, filepath.Join(base, "config.json"))
		eq(t, stamp1 != l.GetCacheStamp(""), true, "stamp changed")
	})
	tw(t, f, "includes agent stamp when agentName is provided and agent file exists", func(t *testing.T) {
		base, l := mk(t, `{}`)
		writeTestFile(t, filepath.Join(base, "agents", "coder.md"), "---\npermission:\n  read: allow\n---\n")
		eq(t, l.GetCacheStamp("") != l.GetCacheStamp("coder"), true, "stamps differ")
	})
	tw(t, f, "returns cached value on second call with unchanged file", func(t *testing.T) {
		_, l := mk(t, `{"permission":{"*":"allow"}}`)
		first, second := l.LoadGlobalConfig(), l.LoadGlobalConfig()
		eq(t, first == second, true, "same reference")
	})
	tw(t, f, "reloads when file mtime changes", func(t *testing.T) {
		base, l := mk(t, `{"permission":{"*":"allow"}}`)
		first := l.LoadGlobalConfig()
		eq(t, first.Permission.vals["*"], "allow", "first")
		p := filepath.Join(base, "config.json")
		writeTestFile(t, p, `{"permission":{"*":"deny"}}`)
		bump(t, p)
		second := l.LoadGlobalConfig()
		eq(t, second.Permission.vals["*"], "deny", "second")
		eq(t, second != first, true, "new reference")
	})
	tw(t, f, "loads permission from agent frontmatter with pattern map", func(t *testing.T) {
		base, l := mk(t, `{}`)
		writeTestFile(t, filepath.Join(base, "agents", "coder.md"), strings.Join([]string{"---", "permission:", "  bash:", `    "git *": allow`, `    "rm *": deny`, "---", "# Coder"}, "\n"))
		c := l.LoadAgentConfig("coder")
		eq(t, c.Permission != nil, true, "defined")
		eq(t, marshalJSON(c.Permission.vals["bash"], ""), `{"git *":"allow","rm *":"deny"}`, "bash")
	})
	tw(t, f, "returns empty config for agent file without frontmatter", func(t *testing.T) {
		base, l := mk(t, `{}`)
		writeTestFile(t, filepath.Join(base, "agents", "plain.md"), "# No frontmatter\n")
		eq(t, *l.LoadAgentConfig("plain"), ScopeConfig{}, "scope")
	})
	tw(t, f, "project agent config loads from projectAgentsDir", func(t *testing.T) {
		base := t.TempDir()
		pa := filepath.Join(base, "project-agents")
		writeTestFile(t, filepath.Join(pa, "coder.md"), "---\npermission:\n  write: allow\n---\n# Coder\n")
		writeTestFile(t, filepath.Join(base, "config.json"), `{}`)
		l := NewFilePolicyLoader(PolicyLoaderOptions{GlobalConfigPath: filepath.Join(base, "config.json"), AgentsDir: filepath.Join(base, "agents"), ProjectAgentsDir: pa})
		eq(t, permJSON(l.LoadProjectAgentConfig("coder").Permission), `{"write":"allow"}`, "permission")
	})

	tw(t, f, "returns override names when provided", func(t *testing.T) {
		o := missing
		o.McpServerNames = []string{"exa", "research"}
		eq(t, sortedCopy(NewFilePolicyLoader(o).GetConfiguredMcpServerNames()), []string{"exa", "research"}, "names")
	})
	tw(t, f, "deduplicates and trims override names", func(t *testing.T) {
		o := missing
		o.McpServerNames = []string{" exa ", "exa", "research"}
		n := 0
		for _, name := range NewFilePolicyLoader(o).GetConfiguredMcpServerNames() {
			if name == "exa" {
				n++
			}
		}
		eq(t, n, 1, "exa entries")
	})
	tw(t, f, "reads server names from mcp.json on disk", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp.json")
		writeTestFile(t, p, `{"mcpServers":{"exa":{},"research":{}}}`)
		o := missing
		o.GlobalMcpConfigPath = p
		eq(t, sortedCopy(NewFilePolicyLoader(o).GetConfiguredMcpServerNames()), []string{"exa", "research"}, "names")
	})
	tw(t, f, "returns empty array when mcp.json is missing", func(t *testing.T) {
		o := missing
		o.GlobalMcpConfigPath = "/nonexistent/mcp.json"
		eq(t, NewFilePolicyLoader(o).GetConfiguredMcpServerNames(), []string{}, "names")
	})
	tw(t, f, "merges server names from the global and project mcp.json, longest first", func(t *testing.T) {
		base := t.TempDir()
		g, p := filepath.Join(base, "global-mcp.json"), filepath.Join(base, "project-mcp.json")
		writeTestFile(t, g, `{"mcpServers":{"exa":{},"shared":{}}}`)
		writeTestFile(t, p, `{"mcpServers":{"danger-srv":{},"shared":{}}}`)
		o := missing
		o.GlobalMcpConfigPath, o.ProjectMcpConfigPath = g, p
		eq(t, NewFilePolicyLoader(o).GetConfiguredMcpServerNames(), []string{"danger-srv", "shared", "exa"}, "names")
	})
	tw(t, f, "re-reads the project mcp.json after it changes", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "project-mcp.json")
		writeTestFile(t, p, `{"mcpServers":{"exa":{}}}`)
		o := missing
		o.GlobalMcpConfigPath, o.ProjectMcpConfigPath = "/nonexistent/mcp.json", p
		l := NewFilePolicyLoader(o)
		eq(t, l.GetConfiguredMcpServerNames(), []string{"exa"}, "first")
		writeTestFile(t, p, `{"mcpServers":{"exa":{},"research":{}}}`)
		bump(t, p)
		eq(t, l.GetConfiguredMcpServerNames(), []string{"research", "exa"}, "second")
	})
	tw(t, f, "caches MCP server names across calls", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp.json")
		writeTestFile(t, p, `{"mcpServers":{"exa":{}}}`)
		o := missing
		o.GlobalMcpConfigPath = p
		l := NewFilePolicyLoader(o)
		first, second := l.GetConfiguredMcpServerNames(), l.GetConfiguredMcpServerNames()
		eq(t, unsafe.SliceData(first) == unsafe.SliceData(second), true, "same reference")
	})
	tw(t, f, "accumulates issues from malformed config files", func(t *testing.T) {
		_, l := mk(t, "{ INVALID JSON")
		l.LoadGlobalConfig()
		issues := l.GetConfigIssues()
		eq(t, len(issues) >= 1, true, "issues")
		eq(t, strings.Contains(issues[0], "Failed to read config"), true, "message")
	})
	tw(t, f, "does not duplicate issues on repeated loads", func(t *testing.T) {
		base, l := mk(t, "{ INVALID JSON")
		l.LoadGlobalConfig()
		before := l.GetConfigIssues()
		p := filepath.Join(base, "config.json")
		writeTestFile(t, p, "{ INVALID JSON")
		bump(t, p) // bust the cache
		l.LoadGlobalConfig()
		eq(t, len(l.GetConfigIssues()), len(before), "issues")
	})
}

// stubCommandContext is a command context with no host behind it.
type stubCommandContext struct {
	notes       [][]string
	hasUI       bool
	custom      int
	lastFactory any
	lastOptions any
}

func (c *stubCommandContext) Notify(m, l string)       { c.notes = append(c.notes, []string{m, l}) }
func (c *stubCommandContext) SetStatus(string, string) {}
func (c *stubCommandContext) HasUI() bool              { return c.hasUI }
func (c *stubCommandContext) Custom(factory any, options any) (any, error) {
	c.custom++
	c.lastFactory, c.lastOptions = factory, options
	return nil, nil
}

type fileBackedStore struct {
	path   string
	config PermissionSystemExtensionConfig
	t      *testing.T
}

func (s *fileBackedStore) Current() PermissionSystemExtensionConfig { return s.config }
func (s *fileBackedStore) Save(next PermissionSystemExtensionConfig, _ ConfigUI) {
	before := s.config
	writeTestFile(s.t, s.path, marshalJSON(newRuntimeObject(next), "  ")+"\n")
	s.config = normalizePermissionSystemConfig(loadUnifiedConfig(s.path).Config)
	if runtimeJSON(s.config) == runtimeJSON(before) {
		s.t.Error("the saved config does not differ from the current one")
	}
}

// newRuntimeObject is the object JSON.stringify writes for a normalized config.
func newRuntimeObject(c PermissionSystemExtensionConfig) *jsObject {
	v, _ := parseJSON([]byte(runtimeJSON(c)))
	return v.(*jsObject)
}

func TestConfigModalTwins(t *testing.T) {
	const f = "config/config-modal"
	tw(t, f, "permission-system command completions expose top-level config actions", func(t *testing.T) {
		top := getArgumentCompletions("")
		values := func(items []sdk.AutocompleteItem) []string {
			var v []string
			for _, i := range items {
				v = append(v, i.Value)
			}
			return v
		}
		eq(t, len(top) > 0, true, "top level")
		eq(t, strings.Contains(strings.Join(values(top), ","), "show"), true, "show")
		eq(t, strings.Contains(strings.Join(values(top), ","), "reset"), true, "reset")
		eq(t, values(getArgumentCompletions("pa")), []string{"path"}, "filtered")
		eq(t, getArgumentCompletions("path extra") == nil, true, "with a space")
		eq(t, getArgumentCompletions("zzz") == nil, true, "no match")
	})
	tw(t, f, "permission-system command handlers manage config summary, persistence, and modal routing", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.json")
		cur := PermissionSystemExtensionConfig{DebugLog: true, PermissionReviewLog: false, YoloMode: true, DoublePressToConfirm: true}
		writeTestFile(t, configPath, marshalJSON(newRuntimeObject(cur), "  ")+"\n")
		store := &fileBackedStore{path: configPath, config: cur, t: t}
		controller := PermissionSystemConfigController{Config: store, ConfigPath: configPath, GetActiveAgentConfigRules: func() Ruleset { return Ruleset{} }}

		e := sdk.New("t")
		registerPermissionSystemCommand(e, controller)

		last := func(c *stubCommandContext) []string { return c.notes[len(c.notes)-1] }
		info := &stubCommandContext{hasUI: true}
		run := func(c *stubCommandContext, args string) {
			if err := runPermissionSystemCommand(c, args, controller); err != nil {
				t.Fatal(err)
			}
		}
		run(info, "show")
		containsAll(t, last(info)[0], "yoloMode=on", "debugLog=on")
		run(info, "path")
		eq(t, last(info)[0], "permission-system config: "+configPath, "path")
		run(info, "help")
		containsAll(t, last(info)[0], "Usage: /permission-system")
		run(info, "reset")
		eq(t, store.config, defaultExtensionConfig(), "reset config")
		eq(t, last(info)[0], "Permission system settings reset to defaults.", "reset message")
		persisted, _ := parseJSON(readTestdataFile(t, configPath))
		eq(t, marshalJSON(persisted, ""), `{"debugLog":false,"permissionReviewLog":true,"yoloMode":false,"doublePressToConfirm":true}`, "persisted")
		run(info, "unknown")
		eq(t, last(info)[1], "warning", "level")
		containsAll(t, last(info)[0], "Usage: /permission-system")
		headless := &stubCommandContext{}
		run(headless, "")
		eq(t, last(headless)[0], "/permission-system requires interactive TUI mode.", "headless")
		modal := &stubCommandContext{hasUI: true}
		run(modal, "")
		eq(t, modal.custom, 1, "ui.custom calls")
	})
	tw(t, f, "show output includes rule origins when getComposedRules is provided", func(t *testing.T) {
		rules := Ruleset{rule("read", "*", "allow", "config", "global"), rule("bash", "rm *", "deny", "config", "project")}
		store := &fileBackedStore{config: defaultExtensionConfig(), t: t}
		ctx := &stubCommandContext{hasUI: true}
		_ = runPermissionSystemCommand(ctx, "show", PermissionSystemConfigController{Config: store, ConfigPath: "/fake/config.json", GetActiveAgentConfigRules: func() Ruleset { return rules }})
		containsAll(t, ctx.notes[len(ctx.notes)-1][0], "global", "project", "read", "bash")
	})
	tw(t, f, "show output omits rule summary when getComposedRules is not provided", func(t *testing.T) {
		c := defaultExtensionConfig()
		c.YoloMode = true
		store := &fileBackedStore{config: c, t: t}
		ctx := &stubCommandContext{hasUI: true}
		_ = runPermissionSystemCommand(ctx, "show", PermissionSystemConfigController{Config: store, ConfigPath: "/fake/config.json", GetActiveAgentConfigRules: func() Ruleset { return Ruleset{} }})
		msg := ctx.notes[len(ctx.notes)-1][0]
		containsAll(t, msg, "yoloMode=on") // config knobs still present
		eq(t, strings.Contains(msg, "(global)"), false, "rule annotation lines")
	})
}
