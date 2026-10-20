package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of test/unit/agent-scan-dirs, agent-exclude-dirs, agent-discovery-cache, agent-global-root-override,
// extra-agent-dirs and the discovery parts of agent-disabled.

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func wjc(t *testing.T, p string, v any) { writeFile(t, p, jsonString(v)) }

func TestAgentScanDirs(t *testing.T) {
	const f = "agent-scan-dirs"
	setup := func(t *testing.T) (agentDirP, projectDir, scanRoot string) {
		t.Helper()
		withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		agentDirP, projectDir, scanRoot = tmp(t), tmp(t), tmp(t)
		t.Setenv("PI_CODING_AGENT_DIR", agentDirP)
		mkdirAll(t, filepath.Join(agentDirP, "agents"))
		mkdirAll(t, filepath.Join(projectDir, ".pi"))
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		return
	}
	body := func(name, d string) string { return "---\nname: " + name + "\ndescription: " + d + "\n---\nbody\n" }
	write := func(t *testing.T, dir, name, d string) { writeFile(t, filepath.Join(dir, name+".md"), body(name, d)) }
	tw(t, f, "discovers agents from configured user scan directories", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		write(t, scanRoot, "scan-writer", "scan dirs test")
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{scanRoot}}))
		a := findAgent(dAgents(t, projectDir, ScopeUser, "").Agents, "scan-writer")
		eq(t, a.Source, SourceUser, "source")
		eq(t, a.FilePath, filepath.Join(scanRoot, "scan-writer.md"), "path")
	})
	tw(t, f, "expands one wildcard directory segment and invalidates when children are added", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{filepath.Join(scanRoot, "*", "agents")}}))
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "late-writer"), false, "before")
		write(t, filepath.Join(scanRoot, "template-a", "agents"), "late-writer", "scan dirs test")
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "late-writer"), true, "after")
	})
	tw(t, f, "invalidates when an existing wildcard child receives its agents directory", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		mkdirAll(t, filepath.Join(scanRoot, "template-a"))
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{filepath.Join(scanRoot, "*", "agents")}}))
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "late-nested-writer"), false, "before")
		write(t, filepath.Join(scanRoot, "template-a", "agents"), "late-nested-writer", "scan dirs test")
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "late-nested-writer"), true, "after")
	})
	tw(t, f, "keeps user and project scan directories scoped", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		userScan, projectScan := filepath.Join(scanRoot, "user-agents"), filepath.Join(scanRoot, "project-agents")
		write(t, userScan, "user-scan", "scan dirs test")
		write(t, projectScan, "project-scan", "scan dirs test")
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{userScan}}))
		wjc(t, filepath.Join(projectDir, ".pi", "settings.json"), sub(m{"agentScanDirs": l{projectScan}}))
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "project-scan"), false, "user sees project")
		eq(t, hasName(dAgents(t, projectDir, ScopeProject, "").Agents, "user-scan"), false, "project sees user")
		eq(t, hasName(dAgents(t, projectDir, ScopeBoth, "").Agents, "user-scan"), true, "both user")
		eq(t, hasName(dAgents(t, projectDir, ScopeBoth, "").Agents, "project-scan"), true, "both project")
	})
	tw(t, f, "lets fixed user agents override same-name scan-dir agents", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		write(t, filepath.Join(scanRoot, "template", "agents"), "writer", "from scan dir")
		write(t, filepath.Join(agentDirP, "agents"), "writer", "from fixed user dir")
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{filepath.Join(scanRoot, "*", "agents")}}))
		eq(t, findAgent(dAgents(t, projectDir, ScopeBoth, "").Agents, "writer").FilePath, filepath.Join(agentDirP, "agents", "writer.md"), "path")
	})
	tw(t, f, "accepts backslash-separated wildcard patterns", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		write(t, filepath.Join(scanRoot, "template", "agents"), "backslash-scan", "scan dirs test")
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{scanRoot + `\*\agents`}}))
		if filepath.Separator == '\\' {
			eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "backslash-scan"), true, "found")
			return
		}
		// On a POSIX host the original normalises every separator to the platform one as well.
		eq(t, hasName(dAgents(t, projectDir, ScopeUser, "").Agents, "backslash-scan"), true, "found")
	})
	tw(t, f, "does not treat constrained wildcard segments as broad directory scans", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		write(t, filepath.Join(scanRoot, "flow-a", "agents"), "flow-scan", "scan dirs test")
		write(t, filepath.Join(scanRoot, "other", "agents"), "other-scan", "scan dirs test")
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{filepath.Join(scanRoot, "flow-*", "agents")}}))
		a := dAgents(t, projectDir, ScopeUser, "").Agents
		eq(t, hasName(a, "flow-scan"), false, "flow")
		eq(t, hasName(a, "other-scan"), false, "other")
	})
	tw(t, f, "rejects malformed scan directory settings", func(t *testing.T) {
		agentDirP, projectDir, scanRoot := setup(t)
		wjc(t, filepath.Join(agentDirP, "settings.json"), sub(m{"agentScanDirs": l{filepath.Join(scanRoot, "agents"), 42.0}}))
		err := discErr(projectDir, ScopeUser)
		if err == nil || !strings.Contains(err.Error(), "invalid 'agentScanDirs'") || !strings.Contains(err.Error(), "Subagent settings") {
			t.Errorf("error %v", err)
		}
	})
}

func TestExtraAgentDirs(t *testing.T) {
	const f = "extra-agent-dirs"
	setup := func(t *testing.T) (tempDir, agentDirP, cwd string) {
		t.Helper()
		tempDir = tmp(t)
		agentDirP = filepath.Join(tempDir, "agent")
		home := filepath.Join(tempDir, "home")
		cwd = filepath.Join(tempDir, "workspace")
		mkdirAll(t, cwd)
		mkdirAll(t, home)
		t.Setenv("PI_CODING_AGENT_DIR", agentDirP)
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("PI_OFFLINE", "true")
		unsetenv(t, "PI_SUBAGENT_EXTRA_AGENT_DIRS")
		t.Cleanup(clearAgentDiscoveryCache)
		clearAgentDiscoveryCache()
		return
	}
	tw(t, f, "discovers agents from the env-provided dirs as a 'user' source", func(t *testing.T) {
		tempDir, _, cwd := setup(t)
		bundled := filepath.Join(tempDir, "store", "agents")
		bundledAgent := writeAgentFile(t, bundled, "bundled-reviewer", "bundled-reviewer agent")
		t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", bundled)
		found := findAgent(dAgents(t, cwd, ScopeUser, "").Agents, "bundled-reviewer")
		if found == nil {
			t.Fatal("expected bundled agent under 'user' scope")
		}
		eq(t, found.FilePath, bundledAgent, "path")
		found2 := findAgent(dAll(t, cwd).User, "bundled-reviewer")
		eq(t, found2 != nil && found2.FilePath == bundledAgent, true, "all.user")
	})
	tw(t, f, "scans every directory listed (PATH-style delimiter)", func(t *testing.T) {
		tempDir, _, cwd := setup(t)
		a, b := filepath.Join(tempDir, "store-a"), filepath.Join(tempDir, "store-b")
		writeAgentFile(t, a, "agent-a", "agent-a agent")
		writeAgentFile(t, b, "agent-b", "agent-b agent")
		t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", a+string(os.PathListSeparator)+b)
		all := dAll(t, cwd)
		eq(t, hasName(all.User, "agent-a") && hasName(all.User, "agent-b"), true, "both")
	})
	tw(t, f, "lets a local user agent override a bundled one of the same name", func(t *testing.T) {
		tempDir, agentDirP, cwd := setup(t)
		bundled := filepath.Join(tempDir, "store", "agents")
		writeAgentFile(t, bundled, "shared", "shared agent")
		local := writeAgentFile(t, filepath.Join(agentDirP, "agents"), "shared", "shared agent")
		t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", bundled)
		var matches []AgentConfig
		for _, a := range dAgents(t, cwd, ScopeUser, "").Agents {
			if a.Name == "shared" {
				matches = append(matches, a)
			}
		}
		eq(t, len(matches), 1, "name collisions must dedupe")
		eq(t, matches[0].FilePath, local, "local user agent should win over bundled")
	})
	tw(t, f, "ignores the env var when unset or empty", func(t *testing.T) {
		_, _, cwd := setup(t)
		t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", "")
		eq(t, len(dAll(t, cwd).User), 0, "user")
	})
}

func TestAgentDisabledDiscovery(t *testing.T) {
	const f = "agent-disabled"
	tw(t, f, "filters a per-agent disabled builtin from runtime discovery while keeping it in discoverAgentsAll", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"disabled": true}}}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer") == nil, true, "runtime")
		r := findAgent(dAll(t, e.project).Builtin, "reviewer")
		ptrEq(t, r.Disabled, new(true), "disabled")
		eq(t, r.Override.Scope, "user", "scope")
	})
	tw(t, f, "surfaces malformed disabled overrides instead of silently ignoring them", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"agentOverrides": m{"reviewer": m{"disabled": "true"}}}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "reviewer", "disabled")
	})
	tw(t, f, "bulk disableBuiltins hides builtins at runtime and marks them disabled in discoverAgentsAll", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": true}))
		n := 0
		for _, a := range dAgents(t, e.project, ScopeBoth, "").Agents {
			if a.Source == SourceBuiltin {
				n++
			}
		}
		eq(t, n, 0, "runtime builtins")
		all := dAll(t, e.project).Builtin
		if len(all) == 0 {
			t.Fatal("no builtins")
		}
		for _, a := range all {
			if a.Disabled == nil || !*a.Disabled || a.Override == nil || a.Override.Scope != "user" {
				t.Errorf("%s: %+v", a.Name, a.Override)
			}
		}
	})
	tw(t, f, "an explicit user override opts a builtin out of user-scope bulk disable", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": true, "agentOverrides": m{"reviewer": m{"model": "openai/gpt-5.4"}}}))
		r := findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer")
		eq(t, r.Disabled == nil, true, "disabled")
		eq(t, r.Model, "openai/gpt-5.4", "model")
		eq(t, r.Override.Scope, "user", "scope")
	})
	tw(t, f, "project disableBuiltins false re-enables builtins hidden by user bulk disable", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": true}))
		wj(t, e.projectSettings(), sub(m{"disableBuiltins": false}))
		any := false
		for _, a := range dAgents(t, e.project, ScopeBoth, "").Agents {
			any = any || a.Source == SourceBuiltin
		}
		eq(t, any, true, "builtins")
	})
	tw(t, f, "project bulk disable beats user per-agent re-enable overrides", func(t *testing.T) {
		e := newAmEnv(t)
		e.mkProjectPi(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": true, "agentOverrides": m{"reviewer": m{"disabled": false, "model": "openai/gpt-5.4"}}}))
		wj(t, e.projectSettings(), sub(m{"disableBuiltins": true}))
		eq(t, findAgent(dAgents(t, e.project, ScopeBoth, "").Agents, "reviewer") == nil, true, "runtime")
		r := findAgent(dAll(t, e.project).Builtin, "reviewer")
		ptrEq(t, r.Disabled, new(true), "disabled")
		eq(t, r.Override.Scope, "project", "scope")
	})
	tw(t, f, "surfaces malformed disableBuiltins values instead of silently ignoring them", func(t *testing.T) {
		e := newAmEnv(t)
		wj(t, e.userSettings(), sub(m{"disableBuiltins": "true"}))
		errHas(t, discErr(e.project, ScopeBoth), e.userSettings(), "disableBuiltins")
	})
	tw(t, f, "buildBuiltinOverrideConfig emits disabled false when re-enabling a builtin", func(t *testing.T) {
		o := buildBuiltinOverrideConfig(OverrideBase{SystemPromptMode: "replace", SystemPrompt: "Base prompt"},
			AgentConfig{SystemPromptMode: "replace", SystemPrompt: "Base prompt", Disabled: new(false)})
		eq(t, marshalJSON(o, ""), `{"disabled":false}`, "override")
	})
}

func TestAgentExcludeDirs(t *testing.T) {
	const f = "agent-exclude-dirs"
	type env struct{ root, user, project string }
	setup := func(t *testing.T) env {
		t.Helper()
		withTempHome(t)
		t.Setenv("PI_OFFLINE", "true")
		root := tmp(t)
		e := env{root, filepath.Join(root, "user"), filepath.Join(root, "project")}
		mkdirAll(t, e.user)
		mkdirAll(t, filepath.Join(e.project, ".pi"))
		t.Setenv("PI_CODING_AGENT_DIR", e.user)
		clearAgentDiscoveryCache()
		t.Cleanup(clearAgentDiscoveryCache)
		return e
	}
	settings := func(t *testing.T, dir string, subagents any, extra m) {
		t.Helper()
		v := m{"subagents": subagents}
		for k, x := range extra {
			v[k] = x
		}
		wjc(t, filepath.Join(dir, "settings.json"), v)
	}
	wa := func(t *testing.T, dir, name string) {
		t.Helper()
		writeFile(t, filepath.Join(dir, name+".md"), "---\nname: "+name+"\ndescription: exclusion test\n---\nbody\n")
	}
	underPlugins := func(p, plugins string) bool {
		return p == plugins || strings.HasPrefix(p, plugins+string(filepath.Separator))
	}
	tw(t, f, "prunes nested sources, diagnostics and explicit roots in every projection while retaining legacy agents", func(t *testing.T) {
		e := setup(t)
		userPlugins := filepath.Join(e.user, "agents", "plugins")
		projectPlugins := filepath.Join(e.project, ".agents", "plugins")
		fixedProjectRoot := filepath.Join(e.project, ".pi", "agents")
		wa(t, filepath.Join(e.user, "agents"), "user-legacy")
		wa(t, filepath.Join(e.project, ".agents"), "project-legacy")
		mkdirAll(t, fixedProjectRoot)
		writeFile(t, filepath.Join(fixedProjectRoot, "fixed-invalid.md"), "---\nname: fixed-invalid\n---\nbody")
		for _, dir := range []string{userPlugins, projectPlugins} {
			wa(t, filepath.Join(dir, "nested", "agents"), "plugin-only")
			wa(t, dir, "user-legacy")
			writeFile(t, filepath.Join(dir, "invalid.md"), "---\nname: invalid\n---\nbody")
		}
		settings(t, e.user, m{"agentExcludeDirs": l{"agents/plugins"}, "agentScanDirs": l{userPlugins, projectPlugins}}, nil)
		settings(t, filepath.Join(e.project, ".pi"), m{"agentExcludeDirs": l{"../.agents/plugins", "agents"}}, nil)
		for _, scope := range []AgentScope{ScopeUser, ScopeProject, ScopeBoth} {
			direct := dAgents(t, e.project, scope, "")
			eff, _, err := discoverAgentSnapshot(e.project, scope, "", DiscoveryOptions{NoChains: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range []Discovery{direct, eff} {
				eq(t, hasName(r.Agents, "plugin-only"), false, string(scope)+" plugin-only")
				want := "user-legacy"
				if scope == ScopeProject {
					want = "project-legacy"
				}
				eq(t, hasName(r.Agents, want), true, string(scope)+" "+want)
				for _, d := range r.Diagnostics {
					eq(t, strings.Contains(d.FilePath, "plugins"), false, "diagnostic "+d.FilePath)
				}
				for _, d := range r.Directories {
					eq(t, strings.Contains(d.Path, "plugins"), false, "directory "+d.Path)
				}
			}
		}
		all := dAll(t, e.project)
		for _, list := range [][]AgentConfig{all.User, all.Project, all.Package} {
			for _, a := range list {
				eq(t, a.Name == "plugin-only" || strings.Contains(a.FilePath, "plugins"), false, "collision inputs must not retain excluded definitions: "+a.FilePath)
			}
		}
		for _, d := range all.Diagnostics {
			eq(t, strings.Contains(d.FilePath, "plugins"), false, "all diagnostic")
		}
		for _, d := range dAgents(t, e.project, ScopeProject, "").Directories {
			eq(t, d.Path == fixedProjectRoot, false, "fixed root")
		}
		_, snapAll, _ := discoverAgentSnapshot(e.project, ScopeProject, "", DiscoveryOptions{NoChains: true})
		for _, d := range snapAll.Diagnostics {
			eq(t, strings.HasPrefix(d.FilePath, fixedProjectRoot), false, "fixed diagnostic")
		}
	})
	tw(t, f, "invalidates cached exclusions when settings change and discovers later additions after removal", func(t *testing.T) {
		e := setup(t)
		plugins := filepath.Join(e.user, "agents", "plugins")
		wa(t, plugins, "plugin-cache")
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "plugin-cache"), true, "initial")
		settings(t, e.user, m{"agentExcludeDirs": l{"agents/plugins"}}, nil)
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "plugin-cache"), false, "excluded")
		wa(t, plugins, "late-plugin")
		settings(t, e.user, m{"agentExcludeDirs": l{}}, nil)
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "late-plugin"), true, "late")
	})
	tw(t, f, "matches real paths and symlink aliases without matching sibling prefixes", func(t *testing.T) {
		e := setup(t)
		plugins := filepath.Join(e.user, "agents", "plugins")
		wa(t, plugins, "hidden-alias")
		wa(t, plugins+"-kept", "sibling-kept")
		wa(t, filepath.Join(plugins, "..kept"), "hidden-descendant")
		symlink(t, plugins, filepath.Join(e.user, "agents", "alias"))
		symlink(t, filepath.Join(plugins, "hidden-alias.md"), filepath.Join(e.user, "agents", "alias.md"))
		symlink(t, plugins, filepath.Join(e.root, "excluded-alias"))
		settings(t, e.user, m{"agentExcludeDirs": l{filepath.Join(e.root, "excluded-alias", ".")}, "agentScanDirs": l{plugins}}, nil)
		names := agentNames(dAgents(t, e.project, ScopeBoth, "").Agents)
		in := func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
		eq(t, in("hidden-alias"), false, "hidden-alias")
		eq(t, in("hidden-descendant"), false, "hidden-descendant")
		eq(t, in("sibling-kept"), true, "sibling-kept")
		os.Remove(filepath.Join(e.root, "excluded-alias"))
		symlink(t, plugins+"-kept", filepath.Join(e.root, "excluded-alias"))
		names = agentNames(dAgents(t, e.project, ScopeBoth, "").Agents)
		eq(t, in("hidden-alias"), true, "retargeted hidden-alias")
		eq(t, in("sibling-kept"), false, "retargeted sibling-kept")
	})
	tw(t, f, "invalidates excluded explicit scan-root aliases when retargeted to allowed storage", func(t *testing.T) {
		e := setup(t)
		hidden, allowed := filepath.Join(e.root, "hidden"), filepath.Join(e.root, "allowed")
		wa(t, hidden, "hidden-retarget")
		wa(t, allowed, "allowed-retarget")
		for _, scope := range []string{"user", "project"} {
			clearAgentDiscoveryCache()
			alias := filepath.Join(e.root, scope+"-scan-alias")
			symlink(t, hidden, alias)
			us := m{"agentExcludeDirs": l{hidden}}
			ps := m{}
			if scope == "user" {
				us["agentScanDirs"] = l{alias}
			} else {
				ps["agentScanDirs"] = l{alias}
			}
			settings(t, e.user, us, nil)
			settings(t, filepath.Join(e.project, ".pi"), ps, nil)
			for attempt := 0; attempt < 2; attempt++ {
				initial, _, err := discoverAgentSnapshot(e.project, ScopeBoth, "", DiscoveryOptions{NoChains: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range initial.Agents {
					eq(t, strings.HasSuffix(a.Name, "-retarget"), false, "excluded alias read: "+a.Name)
				}
			}
			os.Remove(alias)
			symlink(t, allowed, alias)
			cached, cachedAll, err := discoverAgentSnapshot(e.project, ScopeBoth, "", DiscoveryOptions{NoChains: true})
			if err != nil {
				t.Fatal(err)
			}
			eq(t, hasName(cached.Agents, "allowed-retarget"), true, scope+" effective")
			list := cachedAll.User
			if scope == "project" {
				list = cachedAll.Project
			}
			eq(t, hasName(list, "allowed-retarget"), true, scope+" all")
			clearAgentDiscoveryCache()
			var a, b []string
			for _, x := range cached.Agents {
				a = append(a, x.FilePath)
			}
			for _, x := range dAgents(t, e.project, ScopeBoth, "").Agents {
				b = append(b, x.FilePath)
			}
			eq(t, a, b, scope+" cached equals fresh")
		}
	})
	tw(t, f, "invalidates excluded ${source} root aliases on reverse retarget", func(t *testing.T) {
		for _, source := range []string{"wildcard", "environment", "package", "fixed"} {
			source := source
			t.Run(source, func(t *testing.T) {
				e := setup(t)
				hidden, allowed := filepath.Join(e.root, "hidden"), filepath.Join(e.root, "allowed")
				container := filepath.Join(e.root, "sources", "child")
				mkdirAll(t, container)
				base := container
				if source == "fixed" {
					base = e.user
				}
				alias := filepath.Join(base, "agents")
				wa(t, hidden, "hidden-source")
				wa(t, allowed, "allowed-source")
				symlink(t, hidden, alias)
				us := m{"agentExcludeDirs": l{hidden}}
				var extra m
				if source == "environment" {
					t.Setenv("PI_SUBAGENT_EXTRA_AGENT_DIRS", alias)
				}
				if source == "package" {
					wjc(t, filepath.Join(container, "package.json"), m{"name": "alias-fixture", "pi-subagents": m{"agents": l{"./agents"}}})
					extra = m{"packages": l{container}}
				}
				if source == "wildcard" {
					us["agentScanDirs"] = l{filepath.Join(e.root, "sources", "*", "agents")}
				}
				settings(t, e.user, us, extra)
				eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "allowed-source"), false, "before")
				os.Remove(alias)
				symlink(t, allowed, alias)
				eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "allowed-source"), true, "after")
			})
		}
	})
	tw(t, f, "resolves home paths and missing descendants through symlinked ancestors", func(t *testing.T) {
		e := setup(t)
		t.Setenv("HOME", e.root)
		t.Setenv("USERPROFILE", e.root)
		plugins := filepath.Join(e.user, "agents", "plugins")
		mkdirAll(t, plugins)
		symlink(t, plugins, filepath.Join(e.root, "plugin-alias"))
		settings(t, e.user, m{"agentExcludeDirs": l{"~/plugin-alias/later"}, "agentScanDirs": l{filepath.Join(plugins, "*", "agents")}}, nil)
		dAgents(t, e.project, ScopeBoth, "")
		wa(t, filepath.Join(plugins, "later", "agents"), "missing-hidden")
		wa(t, filepath.Join(plugins, "later-kept", "agents"), "missing-kept")
		a := dAgents(t, e.project, ScopeBoth, "").Agents
		eq(t, hasName(a, "missing-hidden"), false, "hidden")
		eq(t, hasName(a, "missing-kept"), true, "kept")
	})
	tskip(t, f, "does not read or traverse excluded trees on fresh scans or cache fingerprints", "asserts which paths Node's fs.readdirSync and fs.readFileSync received through mock.method(fs, ...); Go has no seam to record filesystem reads, and the same pruning is covered by the exclusion twins above")
	tw(t, f, "does not re-include excluded package roots or change chain discovery", func(t *testing.T) {
		e := setup(t)
		pkg := filepath.Join(e.root, "pkg")
		wa(t, filepath.Join(pkg, "agents"), "package-hidden")
		wjc(t, filepath.Join(pkg, "package.json"), m{"name": "exclude-fixture", "pi-subagents": m{"agents": l{"./agents"}, "chains": l{"./agents"}}})
		wjc(t, filepath.Join(pkg, "agents", "kept.chain.json"), m{"name": "kept-chain", "description": "Retained chain", "chain": l{m{"agent": "scout", "task": "Inspect"}}})
		settings(t, e.user, m{}, m{"packages": l{pkg}})
		eq(t, hasName(dAll(t, e.project).Package, "package-hidden"), true, "included")
		settings(t, e.user, m{"agentExcludeDirs": l{filepath.Join(pkg, "agents")}}, m{"packages": l{pkg}})
		all := dAll(t, e.project)
		eq(t, hasName(all.Package, "package-hidden"), false, "excluded")
		hasChain := func(a DiscoveryAll, n string) bool {
			for _, c := range a.Chains {
				if c.Name == n {
					return true
				}
			}
			return false
		}
		eq(t, hasChain(all, "kept-chain"), true, "kept chain")
		wjc(t, filepath.Join(pkg, "agents", "late.chain.json"), m{"name": "late-chain", "description": "New chain", "chain": l{m{"agent": "scout", "task": "Inspect"}}})
		eq(t, hasChain(dAll(t, e.project), "late-chain"), true, "late chain")
	})
	tw(t, f, "invalidates agent-only discovery when an excluded package declares an allowed external root", func(t *testing.T) {
		e := setup(t)
		pkg := filepath.Join(e.root, "pkg")
		wa(t, filepath.Join(pkg, "agents"), "package-hidden")
		wa(t, filepath.Join(e.root, "allowed"), "outside")
		manifest := filepath.Join(pkg, "package.json")
		wjc(t, manifest, m{"name": "fixture", "pi-subagents": m{"agents": l{"./agents"}}})
		settings(t, e.user, m{"agentExcludeDirs": l{pkg}}, m{"packages": l{pkg}})
		eq(t, hasName(dAgents(t, e.project, ScopeBoth, "").Agents, "outside"), false, "before")
		wjc(t, manifest, m{"name": "fixture", "pi-subagents": m{"agents": l{"../allowed"}}})
		cached := dAgents(t, e.project, ScopeBoth, "")
		eq(t, hasName(cached.Agents, "outside"), true, "outside")
		eq(t, hasName(cached.Agents, "package-hidden"), false, "package-hidden")
		_, snapAll, _ := discoverAgentSnapshot(e.project, ScopeBoth, "", DiscoveryOptions{NoChains: true})
		eq(t, hasName(snapAll.Package, "outside"), true, "snapshot")
		_ = underPlugins
	})
	tw(t, f, "rejects malformed exclusions in participating settings", func(t *testing.T) {
		e := setup(t)
		settings(t, e.user, m{"agentExcludeDirs": l{42.0}}, nil)
		errHas(t, discErr(e.project, ScopeUser), "invalid 'agentExcludeDirs'")
		errHas(t, discErr(e.project, ScopeBoth), "invalid 'agentExcludeDirs'")
	})
}
