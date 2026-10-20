package pi_subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/unit/skills-fallback, skills-disable-model-invocation and proactive-skills.

func writeSkillFile(t *testing.T, skillDir, body, description string) {
	t.Helper()
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "---\ndescription: "+description+"\n---\n\n"+body+"\n")
}

func makeProjectSkill(t *testing.T, cwd, name, body, description string) {
	t.Helper()
	writeSkillFile(t, filepath.Join(cwd, ".pi", "skills", name), body, description)
}

func makePackageSkill(t *testing.T, packageRoot, name, body, packageName string) {
	t.Helper()
	if packageName == "" {
		packageName = name + "-pkg"
	}
	writeFile(t, filepath.Join(packageRoot, "skills", name, "SKILL.md"), body+"\n")
	wjc(t, filepath.Join(packageRoot, "package.json"), m{"name": packageName, "version": "1.0.0", "pi": m{"skills": l{"./skills"}}})
}

// skillEnv isolates HOME, stays offline and clears the skill cache.
func skillEnv(t *testing.T) string {
	t.Helper()
	withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	clearSkillCache()
	t.Cleanup(clearSkillCache)
	return tmp(t)
}

func skillByName(skills []AvailableSkill, name string) *AvailableSkill {
	for i := range skills {
		if skills[i].Name == name {
			return &skills[i]
		}
	}
	return nil
}

func mustSkills(t *testing.T, cwd string) []AvailableSkill {
	t.Helper()
	s, err := discoverAvailableSkills(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustResolve(t *testing.T, names []string, cwd string, local []string, base string) ([]ResolvedSkill, []string) {
	t.Helper()
	r, miss, err := resolveSkills(names, cwd, local, base)
	if err != nil {
		t.Fatal(err)
	}
	return r, miss
}

func descOf(s *AvailableSkill) string {
	if s == nil || s.Description == nil {
		return "<none>"
	}
	return *s.Description
}

func TestSkillsFallback(t *testing.T) {
	const f = "skills-fallback"
	const td = "Test description"
	tw(t, f, "discovers project skills from filesystem paths", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "fallback-skill", "Use fallback mode.", td)
		s := skillByName(mustSkills(t, dir), "fallback-skill")
		if s == nil {
			t.Fatal("expected fallback-skill to be discovered")
		}
		eq(t, s.Source, skillProject, "source")
		eq(t, descOf(s), td, "description")
	})
	tw(t, f, "reads block scalar descriptions for discovery and child injection", func(t *testing.T) {
		dir := skillEnv(t)
		cases := [][2]string{{"|", "first line\nsecond line"}, {"|-", "first line\nsecond line"}, {">", "first line second line"}, {">-", "first line second line"}}
		for i, c := range cases {
			makeProjectSkill(t, dir, "block-scalar-"+strconvItoa(i), "Use block scalar metadata.", c[0]+"\n  first line\n  second line")
		}
		for i, c := range cases {
			name := "block-scalar-" + strconvItoa(i)
			eq(t, descOf(skillByName(mustSkills(t, dir), name)), c[1], "description "+name)
			resolved, missing := mustResolve(t, []string{name}, dir, nil, "")
			eq(t, len(missing), 0, "missing")
			if !strings.Contains(buildSkillInjection(resolved), "<description>"+c[1]+"</description>") {
				t.Errorf("injection lacks the description of %s: %s", name, buildSkillInjection(resolved))
			}
		}
	})
	tw(t, f, "discovers project skills nested below grouping directories", func(t *testing.T) {
		dir := skillEnv(t)
		writeSkillFile(t, filepath.Join(dir, ".pi", "skills", "shell", "issue-262-nested-skill"), "Use nested project skill.", "Nested issue 262 skill")
		s := skillByName(mustSkills(t, dir), "issue-262-nested-skill")
		if s == nil {
			t.Fatal("expected grouped nested skill to be discovered")
		}
		eq(t, s.Source, skillProject, "source")
		eq(t, descOf(s), "Nested issue 262 skill", "description")
		resolved, missing := mustResolve(t, []string{"issue-262-nested-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		hasRe(t, resolved[0].Content, `Use nested project skill\.`)
	})
	tw(t, f, "stops recursive project skill discovery at the first SKILL.md anchor", func(t *testing.T) {
		dir := skillEnv(t)
		root := filepath.Join(dir, ".pi", "skills", "group")
		writeSkillFile(t, filepath.Join(root, "issue-262-anchor"), "Use anchor skill.", td)
		writeSkillFile(t, filepath.Join(root, "issue-262-anchor", "nested", "issue-262-leaked-skill"), "Should not leak.", td)
		writeSkillFile(t, filepath.Join(root, "issue-262-sibling"), "Use sibling skill.", td)
		s := mustSkills(t, dir)
		eq(t, skillByName(s, "issue-262-anchor") != nil, true, "anchor")
		eq(t, skillByName(s, "issue-262-sibling") != nil, true, "sibling")
		eq(t, skillByName(s, "issue-262-leaked-skill") != nil, false, "leaked")
	})
	tw(t, f, "skips hidden directories and node_modules while recursing for project skills", func(t *testing.T) {
		dir := skillEnv(t)
		root := filepath.Join(dir, ".pi", "skills", "group")
		writeSkillFile(t, filepath.Join(root, ".hidden", "issue-262-hidden-skill"), "Should stay hidden.", td)
		writeSkillFile(t, filepath.Join(root, "node_modules", "issue-262-node-skill"), "Should stay ignored.", td)
		writeSkillFile(t, filepath.Join(root, "visible", "issue-262-visible-skill"), "Use visible nested skill.", td)
		s := mustSkills(t, dir)
		eq(t, skillByName(s, "issue-262-visible-skill") != nil, true, "visible")
		eq(t, skillByName(s, "issue-262-hidden-skill") != nil, false, "hidden")
		eq(t, skillByName(s, "issue-262-node-skill") != nil, false, "node_modules")
	})
	tw(t, f, "keeps direct markdown skills from explicit settings roots after parent recursion", func(t *testing.T) {
		dir := skillEnv(t)
		writeFile(t, filepath.Join(dir, ".pi", "skills", "group", "issue-262-direct.md"), "Use direct markdown skill.\n")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"skills": l{"./skills/group"}})
		resolved, missing := mustResolve(t, []string{"issue-262-direct"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, resolved[0].Source, skillProjectSettings, "source")
		hasRe(t, resolved[0].Content, `Use direct markdown skill\.`)
	})
	tw(t, f, "keeps nested skills from higher-priority explicit settings roots after parent recursion", func(t *testing.T) {
		dir := skillEnv(t)
		writeSkillFile(t, filepath.Join(dir, "skills", "group", "issue-262-settings-nested"), "Use settings nested skill.", td)
		wjc(t, filepath.Join(dir, "package.json"), m{"name": "fixture", "version": "1.0.0", "pi": m{"skills": l{"./skills"}}})
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"skills": l{"../skills/group"}})
		resolved, missing := mustResolve(t, []string{"issue-262-settings-nested"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, resolved[0].Source, skillProjectSettings, "source")
		hasRe(t, resolved[0].Content, `Use settings nested skill\.`)
	})
	tw(t, f, "keeps nested skills from higher-priority explicit settings roots when the root path is duplicated", func(t *testing.T) {
		dir := skillEnv(t)
		writeSkillFile(t, filepath.Join(dir, "skills", "group", "issue-262-settings-same-root"), "Use settings same root skill.", td)
		wjc(t, filepath.Join(dir, "package.json"), m{"name": "fixture", "version": "1.0.0", "pi": m{"skills": l{"./skills"}}})
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"skills": l{"../skills"}})
		resolved, missing := mustResolve(t, []string{"issue-262-settings-same-root"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, resolved[0].Source, skillProjectSettings, "source")
		hasRe(t, resolved[0].Content, `Use settings same root skill\.`)
	})
	tw(t, f, "resolves and reads skill content via filesystem fallback", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "resolve-skill", "Run local fallback checks.", td)
		resolved, missing := mustResolve(t, []string{"resolve-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, []any{resolved[0].Name, resolved[0].Source}, []any{"resolve-skill", skillProject}, "skill")
		hasRe(t, resolved[0].Content, `Run local fallback checks\.`)
	})
	tw(t, f, "builds lazy skill references instead of inlining full skill bodies", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "lazy-skill", "This body should stay out of the system prompt.", td)
		resolved, missing := mustResolve(t, []string{"lazy-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		inj := buildSkillInjection(resolved)
		hasRe(t, inj, "The following configured skills are available to this subagent")
		hasRe(t, inj, "Use the read tool to load a skill's file")
		hasRe(t, inj, "<available_skills>")
		hasRe(t, inj, "<name>lazy-skill</name>")
		hasRe(t, inj, "<description>Test description</description>")
		hasRe(t, inj, `<location>.*lazy-skill.*SKILL\.md</location>`)
		noRe(t, inj, "This body should stay out")
		noRe(t, inj, "<skill name=")
	})
	tw(t, f, "escapes XML-sensitive skill metadata in lazy references", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "amp&skill", "Body", "Use A & B <carefully>")
		resolved, _ := mustResolve(t, []string{"amp&skill"}, dir, nil, "")
		inj := buildSkillInjection(resolved)
		hasRe(t, inj, "<name>amp&amp;skill</name>")
		hasRe(t, inj, "<description>Use A &amp; B &lt;carefully&gt;</description>")
		hasRe(t, inj, `amp&amp;skill[\\/]SKILL\.md`)
	})
	tw(t, f, "does not expose pi-subagents as a child-injectable skill", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "pi-subagents", "Parent orchestration only.", td)
		makeProjectSkill(t, dir, "safe-bash", "Use safe bash.", td)
		avail := mustSkills(t, dir)
		eq(t, skillByName(avail, "pi-subagents") != nil, false, "orchestration")
		eq(t, skillByName(avail, "safe-bash") != nil, true, "safe-bash")
		resolved, missing := mustResolve(t, []string{"pi-subagents", "safe-bash"}, dir, nil, "")
		eq(t, missing, []string{"pi-subagents"}, "missing")
		eq(t, len(resolved) == 1 && resolved[0].Name == "safe-bash", true, "resolved")
		agentDirP := filepath.Join(dir, "agent")
		writeSkillFile(t, filepath.Join(agentDirP, "skills", "pi-subagents"), "Still parent-only.", td)
		lr, lm := mustResolve(t, []string{"pi-subagents"}, dir, []string{"./skills"}, agentDirP)
		eq(t, len(lr), 0, "local resolved")
		eq(t, lm, []string{"pi-subagents"}, "local missing")
	})
	tw(t, f, "classifies package-provided skills as project-package", func(t *testing.T) {
		dir := skillEnv(t)
		makePackageSkill(t, filepath.Join(dir, ".pi", "npm", "node_modules", "test-skill-package"), "pkg-skill", "Use package skill.", "test-skill-package")
		s := skillByName(mustSkills(t, dir), "pkg-skill")
		if s == nil {
			t.Fatal("expected pkg-skill to be discovered")
		}
		eq(t, s.Source, skillProjectPackage, "source")
	})
	tw(t, f, "prefers project skills over project-package skills with the same name", func(t *testing.T) {
		dir := skillEnv(t)
		makePackageSkill(t, filepath.Join(dir, ".pi", "npm", "node_modules", "test-skill-package"), "shared-skill", "Package version", "test-skill-package")
		makeProjectSkill(t, dir, "shared-skill", "Project version", td)
		resolved, missing := mustResolve(t, []string{"shared-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, resolved[0].Source, skillProject, "source")
		hasRe(t, resolved[0].Content, "Project version")
	})
	tw(t, f, "discovers skills from project settings packages", func(t *testing.T) {
		dir := skillEnv(t)
		makePackageSkill(t, filepath.Join(dir, ".pi", "packages", "local-skill-pkg"), "settings-package-skill", "Settings package skill.", "")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"packages": l{"./packages/local-skill-pkg"}})
		resolved, missing := mustResolve(t, []string{"settings-package-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved) == 1 && resolved[0].Source == skillProjectPackage, true, "resolved")
	})
	tw(t, f, "discovers skills from project settings npm package sources", func(t *testing.T) {
		dir := skillEnv(t)
		makePackageSkill(t, filepath.Join(dir, ".pi", "npm", "node_modules", "@scope", "skill-package"), "project-settings-scoped-npm-package-skill", "Project settings scoped npm package skill.", "@scope/skill-package")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"packages": l{"npm:@scope/skill-package@1.2.3"}})
		resolved, missing := mustResolve(t, []string{"project-settings-scoped-npm-package-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved) == 1 && resolved[0].Source == skillProjectPackage, true, "resolved")
	})
	tw(t, f, "discovers skills from the current cwd package", func(t *testing.T) {
		dir := skillEnv(t)
		makePackageSkill(t, dir, "cwd-package-skill", "Cwd package skill.", "")
		resolved, missing := mustResolve(t, []string{"cwd-package-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved) == 1 && resolved[0].Source == skillProjectPackage, true, "resolved")
	})
	tw(t, f, "skips optional global npm discovery in offline mode", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake npm is a POSIX shell script")
		}
		dir := tmp(t)
		withTempHome(t)
		bin := filepath.Join(dir, "bin")
		marker := filepath.Join(dir, "npm-calls.txt")
		mkdirAll(t, bin)
		if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\nprintf 'npm-root-called\\n' >> \"$PI_DISCOVERY_MARKER\"\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PI_DISCOVERY_MARKER", marker)
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("PI_OFFLINE", "1")
		clearSkillCache()
		clearAgentDiscoveryCache()
		agentsNpmRoot, skillsNpmRoot = npmRootLookup{}, npmRootLookup{}
		t.Cleanup(func() {
			agentsNpmRoot, skillsNpmRoot = npmRootLookup{}, npmRootLookup{}
			clearSkillCache()
			clearAgentDiscoveryCache()
		})
		mustSkills(t, dir)
		dAgents(t, dir, ScopeBoth, "")
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("npm was invoked while PI_OFFLINE was enabled")
		}
		unsetenv(t, "PI_OFFLINE")
		clearSkillCache()
		clearAgentDiscoveryCache()
		mustSkills(t, dir)
		dAgents(t, dir, ScopeBoth, "")
		b, _ := os.ReadFile(marker)
		eq(t, strings.Fields(string(b)), []string{"npm-root-called", "npm-root-called"}, "npm calls")
	})
	tw(t, f, "keeps optional global discovery silent when no package manager is available", func(t *testing.T) {
		dir := tmp(t)
		withTempHome(t)
		empty := filepath.Join(dir, "empty-bin")
		mkdirAll(t, empty)
		t.Setenv("PATH", empty)
		t.Setenv("PI_OFFLINE", "")
		clearSkillCache()
		clearAgentDiscoveryCache()
		agentsNpmRoot, skillsNpmRoot = npmRootLookup{}, npmRootLookup{}
		t.Cleanup(func() {
			agentsNpmRoot, skillsNpmRoot = npmRootLookup{}, npmRootLookup{}
			clearSkillCache()
			clearAgentDiscoveryCache()
		})
		mustSkills(t, dir)
		dAgents(t, dir, ScopeBoth, "")
	})
	tskip(t, f, "uses the Windows APPDATA npm root without invoking npm", "redefines process.platform as win32 on the running Node process (Object.defineProperty) to take the original's Windows branch; Go selects it with runtime.GOOS at compile time and has no runtime override, so this branch is exercised only on a Windows host")
	tskip(t, f, "falls back to npm when the Windows APPDATA npm root is invalid", "redefines process.platform as win32 and fakes cmd.exe through ComSpec to reach the Windows exec path; the Go port selects the Windows branch with runtime.GOOS and runs npm directly, with no shell involved")
	tw(t, f, "falls back to the runtime cwd when the execution cwd lacks the skill", func(t *testing.T) {
		dir := skillEnv(t)
		nested := filepath.Join(dir, "nested")
		mkdirAll(t, nested)
		makePackageSkill(t, dir, "runtime-fallback-skill", "Runtime fallback skill.", "")
		resolved, missing, err := resolveSkillsWithFallback([]string{"runtime-fallback-skill"}, nested, dir, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved) == 1 && resolved[0].Source == skillProjectPackage, true, "resolved")
	})
	userSettingsTest := func(t *testing.T, name, skillName string, setup func(userAgentDir string) string, wantSource skillSource) {
		dir := tmp(t)
		fake := filepath.Join(dir, "fake-home")
		userAgentDir := filepath.Join(fake, ".pi", "agent")
		t.Setenv("HOME", fake)
		t.Setenv("USERPROFILE", fake)
		unsetenv(t, "PI_CODING_AGENT_DIR")
		t.Setenv("PI_OFFLINE", "true")
		clearSkillCache()
		t.Cleanup(clearSkillCache)
		root := setup(userAgentDir)
		_ = root
		clearSkillCache()
		s := skillByName(mustSkills(t, dir), skillName)
		if s == nil {
			t.Fatalf("%s: skill missing", name)
		}
		eq(t, s.Source, wantSource, name)
	}
	tw(t, f, "discovers skills from user settings packages", func(t *testing.T) {
		userSettingsTest(t, "user package", "user-settings-package-skill", func(ua string) string {
			root := filepath.Join(ua, "user-pkg")
			makePackageSkill(t, root, "user-settings-package-skill", "User settings package skill.", "")
			wjc(t, filepath.Join(ua, "settings.json"), m{"packages": l{m{"source": "./user-pkg"}}})
			return root
		}, skillUserPackage)
	})
	tw(t, f, "discovers skills from user settings git package sources", func(t *testing.T) {
		userSettingsTest(t, "git package", "user-settings-git-package-skill", func(ua string) string {
			root := filepath.Join(ua, "git", "github.com", "user", "repo")
			makePackageSkill(t, root, "user-settings-git-package-skill", "User settings git package skill.", "")
			wjc(t, filepath.Join(ua, "settings.json"), m{"packages": l{"git:github.com/user/repo.git@main"}})
			return root
		}, skillUserPackage)
	})
	tw(t, f, "discovers skills from user settings scoped npm package sources", func(t *testing.T) {
		userSettingsTest(t, "scoped npm", "user-settings-scoped-npm-package-skill", func(ua string) string {
			root := filepath.Join(ua, "npm", "node_modules", "@scope", "skill-package")
			makePackageSkill(t, root, "user-settings-scoped-npm-package-skill", "User settings scoped npm package skill.", "@scope/skill-package")
			wjc(t, filepath.Join(ua, "settings.json"), m{"packages": l{m{"source": "npm:@scope/skill-package@latest"}}})
			return root
		}, skillUserPackage)
	})
	tw(t, f, "resolves agent-local files and directories before global skills without publishing them", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "shared", "global body", td)
		agentDirP := filepath.Join(dir, "agents", "nested")
		writeSkillFile(t, filepath.Join(agentDirP, "skills", "shared"), "local shared body", td)
		writeSkillFile(t, filepath.Join(agentDirP, "direct"), "local direct body", td)
		local, missing := mustResolve(t, []string{"shared", "direct", "missing"}, dir, []string{"./skills", "./direct/SKILL.md"}, agentDirP)
		var got [][2]string
		for _, s := range local {
			got = append(got, [2]string{s.Name, s.Content})
		}
		eq(t, got, [][2]string{{"shared", "local shared body"}, {"direct", "local direct body"}}, "resolved")
		eq(t, missing, []string{"missing"}, "missing")
		g, _ := mustResolve(t, []string{"shared"}, dir, nil, "")
		eq(t, g[0].Content, "global body", "global")
		eq(t, skillByName(mustSkills(t, dir), "direct") != nil, false, "not published")
	})
	tw(t, f, "does not read malformed global settings when every selected local skill resolves", func(t *testing.T) {
		dir := skillEnv(t)
		agentDirP := filepath.Join(dir, "agents", "nested")
		writeSkillFile(t, filepath.Join(agentDirP, "skills", "local"), "local body", td)
		writeFile(t, filepath.Join(dir, ".pi", "settings.json"), "{bad-json")
		r, missing := mustResolve(t, []string{"local"}, dir, []string{"./skills"}, agentDirP)
		eq(t, len(missing), 0, "missing")
		eq(t, r[0].Content, "local body", "content")
	})
	tw(t, f, "falls back globally when an agent-local skill candidate cannot be read", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "shared", "global body", td)
		agentDirP := filepath.Join(dir, "agents", "nested")
		mkdirAll(t, filepath.Join(agentDirP, "skills", "shared", "SKILL.md"))
		r, missing := mustResolve(t, []string{"shared"}, dir, []string{"./skills"}, agentDirP)
		eq(t, len(missing), 0, "missing")
		eq(t, r[0].Content, "global body", "content")
	})
	tw(t, f, "keeps same-named agent-local skills isolated between invocations", func(t *testing.T) {
		dir := skillEnv(t)
		makeProjectSkill(t, dir, "global-only", "global fallback", td)
		one, two := filepath.Join(dir, "one"), filepath.Join(dir, "two")
		writeSkillFile(t, filepath.Join(one, "skills", "private"), "one private", td)
		writeSkillFile(t, filepath.Join(two, "skills", "private"), "two private", td)
		a, _ := mustResolve(t, []string{"private", "global-only"}, dir, []string{"./skills"}, one)
		b, _ := mustResolve(t, []string{"private", "global-only"}, dir, []string{"./skills"}, two)
		c, _ := mustResolve(t, []string{"global-only"}, dir, []string{"./skills"}, one)
		eq(t, []string{a[0].Content, b[0].Content, c[0].Content}, []string{"one private", "two private", "global fallback"}, "isolated")
	})
	tw(t, f, "surfaces malformed project settings files instead of silently ignoring them", func(t *testing.T) {
		dir := skillEnv(t)
		writeFile(t, filepath.Join(dir, ".pi", "settings.json"), "{bad-json")
		_, _, err := resolveSkills([]string{"missing-skill"}, dir, nil, "")
		eq(t, err != nil && regexpMatch(`Failed to read skills settings file .+\.pi[\\/]settings\.json`, err.Error()), true, "error")
	})
	tw(t, f, "surfaces malformed explicit settings package manifests instead of silently ignoring them", func(t *testing.T) {
		dir := skillEnv(t)
		root := filepath.Join(dir, ".pi", "packages", "broken-package")
		writeFile(t, filepath.Join(root, "package.json"), "{bad-json")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"packages": l{"./packages/broken-package"}})
		_, err := discoverAvailableSkills(dir)
		eq(t, err != nil && regexpMatch(`Failed to read package manifest .+broken-package[\\/]package\.json`, err.Error()), true, "error")
	})
}

func TestSkillsDisableModelInvocation(t *testing.T) {
	const f = "skills-disable-model-invocation"
	write := func(t *testing.T, cwd, name, body string, dmi string) {
		t.Helper()
		lines := "---\ndescription: Test description\n"
		if dmi != "" {
			lines += "disable-model-invocation: " + dmi + "\n"
		}
		writeFile(t, filepath.Join(cwd, ".pi", "skills", name, "SKILL.md"), lines+"---\n\n"+body+"\n")
	}
	tw(t, f, "exposes disableModelInvocation metadata when a skill opts out of model invocation", func(t *testing.T) {
		dir := skillEnv(t)
		write(t, dir, "hidden-skill", "User-only skill body.", "true")
		write(t, dir, "visible-skill", "Model-invocable body.", "")
		skills := mustSkills(t, dir)
		hidden, visible := skillByName(skills, "hidden-skill"), skillByName(skills, "visible-skill")
		if hidden == nil {
			t.Fatal("expected hidden-skill to be discovered for user tooling")
		}
		ptrEq(t, hidden.DisableModelInvocation, new(true), "hidden")
		eq(t, visible.DisableModelInvocation == nil, true, "visible")
	})
	tw(t, f, "still resolves hidden skills when they are named explicitly", func(t *testing.T) {
		dir := skillEnv(t)
		write(t, dir, "hidden-skill", "User-only skill body.", "true")
		resolved, missing := mustResolve(t, []string{"hidden-skill"}, dir, nil, "")
		eq(t, len(missing), 0, "missing")
		eq(t, len(resolved), 1, "resolved")
		eq(t, resolved[0].Name, "hidden-skill", "name")
		hasRe(t, resolved[0].Content, `User-only skill body\.`)
		ptrEq(t, resolved[0].DisableModelInvocation, new(true), "flag")
	})
	tw(t, f, "buildSkillInjection filters hidden skills even when the caller resolves them by name", func(t *testing.T) {
		dir := skillEnv(t)
		write(t, dir, "hidden-skill", "User-only skill body.", "true")
		write(t, dir, "visible-skill", "Model-invocable body.", "")
		var names []string
		for _, s := range mustSkills(t, dir) {
			names = append(names, s.Name)
		}
		resolved, _ := mustResolve(t, names, dir, nil, "")
		found := false
		for _, s := range resolved {
			found = found || s.Name == "hidden-skill"
		}
		eq(t, found, true, "hidden resolvable")
		inj := buildSkillInjection(resolved)
		hasRe(t, inj, "<name>visible-skill</name>")
		noRe(t, inj, "<name>hidden-skill</name>")
		noRe(t, inj, "User-only skill body")
	})
	tw(t, f, "buildSkillInjection returns empty when every resolved skill is hidden", func(t *testing.T) {
		dir := skillEnv(t)
		write(t, dir, "only-hidden", "User-only skill body.", "true")
		resolved, _ := mustResolve(t, []string{"only-hidden"}, dir, nil, "")
		eq(t, buildSkillInjection(resolved), "", "empty")
	})
	tw(t, f, "carries disableModelInvocation through name-level source precedence", func(t *testing.T) {
		dir := skillEnv(t)
		writeSkillFile(t, filepath.Join(dir, "skills", "upgrade-skill"), "Settings body.", "Test description")
		wjc(t, filepath.Join(dir, ".pi", "settings.json"), m{"skills": l{"../skills"}})
		write(t, dir, "upgrade-skill", "Project body.", "true")
		clearSkillCache()
		w := skillByName(mustSkills(t, dir), "upgrade-skill")
		eq(t, w.Source, skillProject, "source")
		ptrEq(t, w.DisableModelInvocation, new(true), "flag")
	})
	tw(t, f, "matches host YAML boolean semantics in injection and proactive recommendations", func(t *testing.T) {
		dir := skillEnv(t)
		cases := []struct {
			name, value string
			hidden      bool
		}{{"plain-true", "true", true}, {"quoted-true", `"true"`, false}, {"title-true", "True", true}, {"upper-true", "TRUE", true}, {"plain-false", "false", false}, {"plain-yes", "yes", false}}
		var names []string
		for _, c := range cases {
			write(t, dir, c.name, c.name+" body.", c.value)
			names = append(names, c.name)
		}
		available := mustSkills(t, dir)
		for _, c := range cases {
			s := skillByName(available, c.name)
			eq(t, s != nil && (s.DisableModelInvocation != nil) == c.hidden, true, c.name)
		}
		resolved, _ := mustResolve(t, names, dir, nil, "")
		inj := buildSkillInjection(resolved)
		for _, c := range cases {
			if c.hidden {
				noRe(t, inj, "<name>"+c.name+"</name>")
			} else {
				hasRe(t, inj, "<name>"+c.name+"</name>")
			}
		}
		var agents []AgentConfig
		for _, n := range []string{"one", "two"} {
			agents = append(agents, AgentConfig{Name: n, Description: n + " agent", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/" + n + ".md", Skills: names})
		}
		var meta []ProactiveAvailableSkill
		for _, s := range available {
			meta = append(meta, ProactiveAvailableSkill{s.Name, s.Description, s.DisableModelInvocation})
		}
		recs := recommendProactiveSkillSubagents(agents, nil, meta, nil)
		var got []string
		for _, r := range recs {
			got = append(got, r.Skill)
		}
		eq(t, got, []string{"plain-false", "plain-yes", "quoted-true"}, "recommendations")
	})
}

func TestProactiveSkills(t *testing.T) {
	const f = "proactive-skills"
	agent := func(name string, skills []string, disabled bool) AgentConfig {
		a := AgentConfig{Name: name, Description: name + " agent", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/tmp/" + name + ".md", Skills: skills}
		if disabled {
			a.Disabled = new(true)
		}
		return a
	}
	chain := func(name string, skills []string) ChainConfig {
		return ChainConfig{Name: name, Description: name + " chain", Source: SourceProject, FilePath: "/tmp/" + name + ".chain.md",
			Steps: []ChainStep{{Agent: "worker", Skills: TriList{Set: true, Items: skills}}}}
	}
	sk := func(name, desc string) ProactiveAvailableSkill {
		s := ProactiveAvailableSkill{Name: name}
		if desc != "" {
			s.Description = &desc
		}
		return s
	}
	tw(t, f, "recommends available skills referenced by multiple enabled configs", func(t *testing.T) {
		recs := recommendProactiveSkillSubagents(
			[]AgentConfig{agent("reviewer", nil, false), agent("ui-reviewer", []string{"accessibility"}, false), agent("disabled-reviewer", []string{"accessibility"}, true)},
			[]ChainConfig{chain("ui-check", []string{"accessibility"}), chain("cleanup", []string{"deslop"})},
			[]ProactiveAvailableSkill{sk("accessibility", "Accessibility review."), sk("deslop", "Cleanup review.")}, nil)
		eq(t, len(recs), 1, "count")
		eq(t, []any{recs[0].Skill, recs[0].Agent, recs[0].References, recs[0].Sources}, []any{"accessibility", "reviewer", 2, []string{"agent:ui-reviewer", "chain:ui-check"}}, "recommendation")
	})
	tw(t, f, "does not recommend hidden skills (disable-model-invocation: true)", func(t *testing.T) {
		hidden := sk("hidden-skill", "User-only.")
		hidden.DisableModelInvocation = new(true)
		recs := recommendProactiveSkillSubagents(
			[]AgentConfig{agent("one", []string{"hidden-skill", "visible-skill"}, false), agent("two", []string{"hidden-skill", "visible-skill"}, false)},
			nil, []ProactiveAvailableSkill{hidden, sk("visible-skill", "Model-invocable.")}, nil)
		eq(t, len(recs) == 1 && recs[0].Skill == "visible-skill", true, "visible only")
	})
	tw(t, f, "filters unavailable orchestration skills and honors config bounds", func(t *testing.T) {
		pref := "delegate"
		recs := recommendProactiveSkillSubagents(
			[]AgentConfig{agent("delegate", []string{"pi-subagents", "alpha", "beta"}, false), agent("one", []string{"alpha", "beta"}, false), agent("two", []string{"gamma"}, false), agent("three", []string{"gamma"}, false)},
			nil, []ProactiveAvailableSkill{sk("alpha", ""), sk("beta", ""), sk("gamma", "")}, &ProactiveSkillSubagentsConfig{PreferredAgent: &pref, MaxRecommendations: new(float64(2))})
		var got []string
		for _, r := range recs {
			got = append(got, r.Skill)
			eq(t, r.Agent, "delegate", "agent")
		}
		eq(t, got, []string{"alpha", "beta"}, "skills")
	})
	tw(t, f, "can be disabled and formats guardrails for visible suggestions", func(t *testing.T) {
		eq(t, resolveProactiveSkillSubagentsConfig(false).enabled, false, "disabled")
		eq(t, len(recommendProactiveSkillSubagents([]AgentConfig{agent("reviewer", []string{"deslop"}, false), agent("cleanup", []string{"deslop"}, false)}, nil, []ProactiveAvailableSkill{sk("deslop", "")}, false)), 0, "none")
		lines := strings.Join(formatProactiveSkillSubagentRecommendations([]ProactiveSkillSubagentRecommendation{{Skill: "deslop", Agent: "reviewer", References: 2, Sources: []string{"agent:a", "chain:b"}, Reason: "referenced by 2 configured agents/chains"}}), "\n")
		hasRe(t, lines, "Proactive skill subagent suggestions:")
		hasRe(t, lines, "fresh context")
	})
	tw(t, f, "does not discover skills when disabled and treats discovery failures as no suggestions", func(t *testing.T) {
		calls := 0
		agents := []AgentConfig{agent("reviewer", []string{"deslop"}, false), agent("cleanup", []string{"deslop"}, false)}
		fail := func() ([]ProactiveAvailableSkill, error) { calls++; return nil, os.ErrInvalid }
		eq(t, len(buildProactiveSkillSubagentRecommendationLines(agents, nil, false, fail)), 0, "disabled")
		eq(t, calls, 0, "no discovery when disabled")
		eq(t, len(buildProactiveSkillSubagentRecommendationLines(agents, nil, nil, fail)), 0, "failed")
		eq(t, calls, 1, "one discovery")
	})
}
