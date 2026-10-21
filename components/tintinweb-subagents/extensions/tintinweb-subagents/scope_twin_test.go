package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Twins of the extension and tool scope tests of test/agent-runner.test.ts. The original runs runAgent against a mocked
// Pi session and reads the options it built; the Go form reads the same decisions from planScope (which extensions
// load, which tools the child can call, which warnings it raises) and from the pig flags they become.

var builtins7 = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

type testExt struct {
	path  string
	tools []string
}

// scopeOf plans a child the way runAgent does: the agent file's fields, the built-ins it asked for and the extensions
// the loader would find.
func scopeOf(cfg func(*agentConfig), toolNames []string, isolated bool, exts ...testExt) scopePlan {
	c := &agentConfig{Name: "test-agent", BuiltinToolNames: builtins7, Extensions: true, Skills: false, SystemPrompt: "Test.", PromptMode: "replace", Enabled: true}
	if cfg != nil {
		cfg(c)
	}
	in := scopeInput{Type: "Explore", Config: c, Isolated: isolated, ConfigCwd: "/tmp", ToolNames: toolNames}
	for _, e := range exts {
		in.Discovered = append(in.Discovered, loadedExtension{Path: e.path, Tools: e.tools})
	}
	return planScope(in)
}

func sameSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	eq(t, g, w)
}

func hasTool(p scopePlan, name string) bool { return slices.Contains(p.Callable(), name) }

func warnings(p scopePlan, prefix string) []string {
	var out []string
	for _, w := range p.Warnings {
		if strings.HasPrefix(w, prefix) {
			out = append(out, w)
		}
	}
	return out
}

func TestExtensionNamesTwins(t *testing.T) {
	const f = "agent-runner"
	tw(t, f, "strips .ts/.js from a single-file extension basename", func(t *testing.T) {
		eq(t, extensionCanonicalName("/x/foo.ts"), "foo")
		eq(t, extensionCanonicalName("/x/foo.js"), "foo")
	})
	tw(t, f, "uses the parent directory name for index.{ts,js} extensions", func(t *testing.T) {
		eq(t, extensionCanonicalName("/x/foo/index.ts"), "foo")
		eq(t, extensionCanonicalName("/x/foo/index.js"), "foo")
	})
	tw(t, f, "lowercases the result for case-insensitive matching", func(t *testing.T) {
		eq(t, extensionCanonicalName("/x/MCP.ts"), "mcp")
		eq(t, extensionCanonicalName("/x/MyExt.js"), "myext")
		eq(t, extensionCanonicalName("/x/Foo/index.ts"), "foo")
	})

	pkgDir := func(t *testing.T, name string, piExtensions string) string {
		t.Helper()
		dir := t.TempDir()
		manifest := `{"name": "` + name + `"`
		if piExtensions != "" {
			manifest += `, "pi": {"extensions": ` + piExtensions + `}`
		}
		write(t, filepath.Join(dir, "package.json"), manifest+"}")
		write(t, filepath.Join(dir, "src", "index.ts"), "export default () => {};")
		return dir
	}
	tw(t, f, "aliases a package-declared index.ts entry to the unscoped, lowercased package name", func(t *testing.T) {
		// Without this, `pi.extensions: ["./src/index.ts"]` only ever matches as "src".
		dir := pkgDir(t, "@tintinweb/Pi-Subagents", `["./src/index.ts"]`)
		eq(t, extensionCanonicalNames(filepath.Join(dir, "src", "index.ts")), []string{"src", "pi-subagents"})
	})
	tw(t, f, "adds no alias for a loose file with no enclosing package.json", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "foo.ts"), "export default () => {};")
		eq(t, extensionCanonicalNames(filepath.Join(dir, "foo.ts")), []string{"foo"})
	})
	tw(t, f, "adds no alias when the nearest manifest does not declare this entry", func(t *testing.T) {
		dir := pkgDir(t, "@scope/other-ext", `["./src/other.ts"]`)
		eq(t, extensionCanonicalNames(filepath.Join(dir, "src", "index.ts")), []string{"src"})
	})
	tw(t, f, "adds no alias when the nearest package.json has no pi manifest", func(t *testing.T) {
		dir := pkgDir(t, "just-a-project", "")
		eq(t, extensionCanonicalNames(filepath.Join(dir, "src", "index.ts")), []string{"src"})
	})
	tw(t, f, "does not climb past a node_modules boundary into a consumer's manifest", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "package.json"), `{"name": "consumer", "pi": {"extensions": ["./node_modules/inner-ext/index.ts"]}}`)
		inner := filepath.Join(root, "node_modules", "inner-ext")
		write(t, filepath.Join(inner, "index.ts"), "export default () => {};")
		// Only the path-derived name, never "consumer".
		eq(t, extensionCanonicalNames(filepath.Join(inner, "index.ts")), []string{"inner-ext"})
	})
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setOf(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestParseExtensionsSpecTwins(t *testing.T) {
	const f = "agent-runner"
	home, _ := os.UserHomeDir()
	tw(t, f, "classifies bare entries as names", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"mcp", "logger"}, "/work")
		eq(t, s.Names, setOf("mcp", "logger"))
		eq(t, len(s.Paths), 0)
		eq(t, s.Wildcard, false)
	})
	tw(t, f, "treats '*' as the wildcard", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"*"}, "/work")
		eq(t, s.Wildcard, true)
		eq(t, len(s.Names), 0)
		eq(t, len(s.Paths), 0)
	})
	tw(t, f, "resolves a relative path against cwd and adds its canonical name", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"./rel/foo.ts"}, "/work")
		eq(t, s.Paths, []string{filepath.Join("/work", "rel", "foo.ts")})
		eq(t, s.Names, setOf("foo"))
	})
	tw(t, f, "keeps an absolute path as-is", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"/abs/bar.ts"}, "/work")
		eq(t, s.Paths, []string{"/abs/bar.ts"})
		eq(t, s.Names, setOf("bar"))
	})
	tw(t, f, "expands a leading ~ to the home directory", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"~/ext/baz.ts"}, "/work")
		eq(t, s.Paths[0], home+"/ext/baz.ts")
		eq(t, s.Names, setOf("baz"))
	})
	tw(t, f, "composes wildcard, names, and paths", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"*", "mcp", "/abs/foo.ts"}, "/work")
		eq(t, s.Wildcard, true)
		eq(t, s.Names, setOf("mcp", "foo"))
		eq(t, s.Paths, []string{"/abs/foo.ts"})
	})
	tw(t, f, "lowercases bare-name entries — extension names match case-insensitively", func(t *testing.T) {
		eq(t, parseExtensionsSpec([]string{"Mcp", "LOGGER"}, "/work").Names, setOf("mcp", "logger"))
	})
	tw(t, f, "ignores empty entries (defensive — upstream parsers already strip them)", func(t *testing.T) {
		s := parseExtensionsSpec([]string{"", "mcp", ""}, "/work")
		eq(t, s.Names, setOf("mcp"))
		eq(t, s.Wildcard, false)
	})
}

func TestParseExtSelectorsTwins(t *testing.T) {
	const f = "agent-runner"
	tw(t, f, "bare ext:foo → name only, no narrowing", func(t *testing.T) {
		s := parseExtSelectors([]string{"ext:foo"})
		eq(t, s.ExtNames, setOf("foo"))
		eq(t, len(s.Narrowing), 0)
	})
	tw(t, f, "ext:foo/bar → name plus a narrowing entry", func(t *testing.T) {
		s := parseExtSelectors([]string{"ext:foo/bar"})
		eq(t, s.ExtNames, setOf("foo"))
		eq(t, s.Narrowing["foo"], setOf("bar"))
	})
	tw(t, f, "multiple ext:foo/* entries union", func(t *testing.T) {
		eq(t, parseExtSelectors([]string{"ext:foo/a", "ext:foo/b"}).Narrowing["foo"], setOf("a", "b"))
	})
	tw(t, f, "ext:foo + ext:foo/bar → narrowing wins", func(t *testing.T) {
		eq(t, parseExtSelectors([]string{"ext:foo", "ext:foo/bar"}).Narrowing["foo"], setOf("bar"))
	})
	tw(t, f, "splits on the first / so tool names may contain /", func(t *testing.T) {
		eq(t, parseExtSelectors([]string{"ext:foo/bar/baz"}).Narrowing["foo"], setOf("bar/baz"))
	})
	tw(t, f, "skips empty name and empty tool halves", func(t *testing.T) {
		s := parseExtSelectors([]string{"ext:", "ext:foo/"})
		eq(t, s.ExtNames, setOf("foo"))
		eq(t, len(s.Narrowing), 0)
	})
	tw(t, f, "lowercases the extension name but preserves tool-name case", func(t *testing.T) {
		s := parseExtSelectors([]string{"ext:Mcp/SomeTool", "ext:FOO"})
		eq(t, s.ExtNames, setOf("mcp", "foo"))
		eq(t, s.Narrowing["mcp"], setOf("SomeTool"))
	})
}

func TestToolAllowlistTwins(t *testing.T) {
	const f = "agent-runner"
	tw(t, f, "extensions: true with extension tools — all 7 built-ins plus extension tools land in the allowlist", func(t *testing.T) {
		p := scopeOf(nil, builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp", "mcp_call"}})
		sameSet(t, p.Callable(), append(slices.Clone(builtins7), "mcp", "mcp_call")...)
	})
	tw(t, f, "enumerates tools across multiple loaded extensions", func(t *testing.T) {
		p := scopeOf(nil, builtins7, false, testExt{"/ext/a.ts", []string{"tool_a"}}, testExt{"/ext/b.ts", []string{"tool_b"}})
		eq(t, hasTool(p, "tool_a") && hasTool(p, "tool_b"), true)
	})
	tw(t, f, "disallowedTools removes both built-ins and extension tools", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) { c.DisallowedTools = []string{"bash", "mcp"} }, builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp", "mcp_call"}})
		eq(t, hasTool(p, "bash"), false)
		eq(t, hasTool(p, "mcp"), false)
		eq(t, hasTool(p, "mcp_call") && hasTool(p, "read"), true)
	})
	tw(t, f, "EXCLUDED_TOOL_NAMES never reach the allowlist even if an extension registers them", func(t *testing.T) {
		p := scopeOf(nil, builtins7, false, testExt{"/ext/evil.ts", []string{"Agent", "get_subagent_result", "steer_subagent", "ok_ext"}})
		for _, n := range []string{"Agent", "get_subagent_result", "steer_subagent"} {
			eq(t, hasTool(p, n), false)
		}
		eq(t, hasTool(p, "ok_ext"), true)
	})
	tw(t, f, "extensions: false with disallowedTools — denylist applies to built-ins", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) { c.Extensions = false; c.DisallowedTools = []string{"bash"} }, builtins7, false)
		eq(t, hasTool(p, "bash"), false)
		eq(t, p.Callable(), []string{"read", "edit", "write", "grep", "find", "ls"})
	})
	tw(t, f, "dynamic mode: leaves the allowlist unset, denies via excludeTools, activates post-bind", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) { c.DisallowedTools = []string{"bash"} }, builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp"}})
		// The allowlist is unset so tools an extension registers later (MCP on session_start) are admitted; the
		// scope is a denylist of this extension's own tools plus disallowed_tools.
		eq(t, p.Tools == nil, true)
		sameSet(t, p.ExcludeTools, "Agent", "SubagentWorkflow", "get_subagent_result", "steer_subagent", "bash")
		// The active set, derived from the registry: the extension tool in, the denied built-in out.
		eq(t, hasTool(p, "mcp") && hasTool(p, "read"), true)
		eq(t, hasTool(p, "bash"), false)
	})
	tw(t, f, "isolated keeps the static allowlist — no live scoping installed", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) { c.Extensions = false }, []string{"read"}, true, testExt{"/ext/foo.ts", []string{"foo_tool"}})
		// A hard registry gate is the right boundary here: nothing can register asynchronously.
		eq(t, p.Tools, []string{"read"})
		eq(t, p.NoExtensions, true)
		eq(t, len(p.ExcludeTools), 0)
	})
}

func TestExtensionAllowlistTwins(t *testing.T) {
	const f = "agent-runner"
	list := func(entries ...string) func(*agentConfig) { return func(c *agentConfig) { c.Extensions = entries } }
	tw(t, f, "['*'] short-circuits — no extensionsOverride, behaves like extensions: true", func(t *testing.T) {
		p := scopeOf(list("*"), builtins7, false, testExt{"/ext/a.ts", []string{"tool_a"}})
		eq(t, p.LoadAll, true) // no loader override
		eq(t, len(p.AdditionalPaths), 0)
		eq(t, hasTool(p, "tool_a"), true)
	})
	tw(t, f, "['mcp'] keeps only the mcp-named extension, drops others", func(t *testing.T) {
		p := scopeOf(list("mcp"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp", "mcp_call"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, hasTool(p, "mcp") && hasTool(p, "mcp_call"), true)
		eq(t, hasTool(p, "other_tool"), false)
	})
	tw(t, f, "matches a package-installed extension by its package short name, not just its src dir (#143)", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"), `{"name": "@tintinweb/pi-subagents", "pi": {"extensions": ["./src/index.ts"]}}`)
		write(t, filepath.Join(dir, "src", "index.ts"), "export default () => {};")
		entry := filepath.Join(dir, "src", "index.ts")
		p := scopeOf(list("pi-subagents"), builtins7, false, testExt{entry, []string{"pkg_tool"}})
		eq(t, hasTool(p, "pkg_tool"), true)
	})
	tw(t, f, "an absolute path is added to additionalExtensionPaths and its extension survives", func(t *testing.T) {
		p := scopeOf(list("/abs/foo.ts"), builtins7, false, testExt{"/abs/foo.ts", []string{"foo_tool"}})
		eq(t, p.AdditionalPaths, []string{"/abs/foo.ts"})
		eq(t, hasTool(p, "foo_tool"), true)
	})
	tw(t, f, "['*', path] keeps all defaults plus the extra path", func(t *testing.T) {
		p := scopeOf(list("*", "/abs/foo.ts"), builtins7, false, testExt{"/ext/default.ts", []string{"default_tool"}}, testExt{"/abs/foo.ts", []string{"foo_tool"}})
		eq(t, hasTool(p, "default_tool") && hasTool(p, "foo_tool"), true)
		// The loader is given the path whatever else it keeps: the flags are what the child is started with.
		sp := p.spec([]loadedExtension{{Path: "/ext/default.ts", Tools: []string{"default_tool"}}, {Path: "/abs/foo.ts", Tools: []string{"foo_tool"}}})
		args := childArgs(childSpec{NoExtensions: sp.NoExtensions, ExtensionPaths: sp.ExtensionPaths})
		eq(t, slices.Contains(args, "--no-extensions"), false) // every default stays
		i := slices.Index(args, "--extension")
		eq(t, i >= 0 && args[i+1] == "/abs/foo.ts", true)
		eq(t, sp.ExtensionPaths, []string{"/abs/foo.ts"})
	})
	tw(t, f, "['mcp', path] keeps exactly those two, drops other defaults (no wildcard)", func(t *testing.T) {
		p := scopeOf(list("mcp", "/abs/foo.ts"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}}, testExt{"/abs/foo.ts", []string{"foo_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, p.AdditionalPaths, []string{"/abs/foo.ts"})
		eq(t, p.LoadAll, false) // the loader override is in force: narrowing, not load-all
		eq(t, hasTool(p, "mcp_tool") && hasTool(p, "foo_tool"), true)
		eq(t, hasTool(p, "other_tool"), false)
	})
	tw(t, f, "disallowedTools still applies to tools from an allowlisted extension", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) { c.Extensions = []string{"mcp"}; c.DisallowedTools = []string{"mcp"} }, builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp", "mcp_call"}})
		eq(t, hasTool(p, "mcp"), false)
		eq(t, hasTool(p, "mcp_call"), true)
	})
	tw(t, f, "warns but proceeds when a bare name matches no loaded extension", func(t *testing.T) {
		p := scopeOf(list("mcp", "typo"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, warnings(p, "extension-error:"), []string{`extension-error:extension "typo" requested by agent "Explore" was not loaded`})
	})
	tw(t, f, "warns but proceeds when a path entry fails to load", func(t *testing.T) {
		// Not among the loadable extensions: the loader records a load error and the path's canonical name is what the
		// unmatched-name check reports.
		p := scopeOf(list("/abs/missing.ts"), builtins7, false)
		eq(t, warnings(p, "extension-error:"), []string{`extension-error:extension "missing" requested by agent "Explore" was not loaded`})
	})
	tw(t, f, "matches `extensions: [Mcp]` against `mcp.ts` (case-insensitive)", func(t *testing.T) {
		p := scopeOf(list("Mcp"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, len(warnings(p, "extension-error:")), 0) // the name resolved
		eq(t, hasTool(p, "mcp_tool"), true)
	})
}

func TestExcludeExtensionsTwins(t *testing.T) {
	const f = "agent-runner"
	agent := func(extensions any, exclude ...string) func(*agentConfig) {
		return func(c *agentConfig) { c.Extensions = extensions; c.ExcludeExtensions = exclude }
	}
	tw(t, f, "extensions: true + exclude — override installed, excluded tools dropped, others kept", func(t *testing.T) {
		p := scopeOf(agent(true, "notify"), builtins7, false, testExt{"/ext/notify.ts", []string{"notify_send"}}, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, p.LoadAll, false)
		eq(t, hasTool(p, "notify_send"), false)
		eq(t, hasTool(p, "mcp_tool"), true)
		eq(t, len(warnings(p, "extension-error:")), 0)
	})
	tw(t, f, "['*'] + exclude — wildcard no longer short-circuits, exclusion applies", func(t *testing.T) {
		p := scopeOf(agent([]string{"*"}, "notify"), builtins7, false, testExt{"/ext/notify.ts", []string{"notify_send"}}, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, p.LoadAll, false)
		eq(t, hasTool(p, "notify_send"), false)
		eq(t, hasTool(p, "mcp_tool"), true)
	})
	tw(t, f, "allowlist + exclude of a listed name — subtracted, 'in both' warning fires", func(t *testing.T) {
		p := scopeOf(agent([]string{"mcp", "other"}, "other"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, hasTool(p, "mcp_tool"), true)
		eq(t, hasTool(p, "other_tool"), false)
		w := warnings(p, "extension-error:")
		eq(t, len(w), 1)
		eq(t, strings.Contains(w[0], "in both extensions: and exclude_extensions:"), true)
	})
	tw(t, f, "exclude typo — warning fires, all extensions still load", func(t *testing.T) {
		p := scopeOf(agent(true, "nope"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, hasTool(p, "mcp_tool"), true)
		w := warnings(p, "extension-error:")
		eq(t, len(w), 1)
		eq(t, strings.Contains(w[0], `exclude_extensions: "nope"`), true)
	})
	tw(t, f, "extensions: false + exclude — orphan warning, no override", func(t *testing.T) {
		p := scopeOf(agent(false, "notify"), builtins7, false)
		eq(t, p.NoExtensions, true)
		w := warnings(p, "extension-error:")
		eq(t, len(w), 1)
		eq(t, strings.Contains(w[0], "exclude_extensions has no effect"), true)
	})
	tw(t, f, "isolated: true + exclude — excludes nulled, no warnings", func(t *testing.T) {
		p := scopeOf(agent(true, "notify"), builtins7, true, testExt{"/ext/notify.ts", []string{"notify_send"}})
		eq(t, hasTool(p, "notify_send"), false)
		eq(t, len(warnings(p, "extension-error:")), 0)
	})
	tw(t, f, "tools: ext:foo referencing an excluded extension — existing orphan warning fires", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) {
			c.Extensions = true
			c.ExcludeExtensions = []string{"beta"}
			c.ExtSelectors = []string{"ext:beta"}
		}, builtins7, false,
			testExt{"/ext/beta.ts", []string{"beta_tool"}}, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, hasTool(p, "beta_tool"), false)
		w := warnings(p, "extension-error:")
		eq(t, len(w), 1)
		eq(t, strings.Contains(w[0], "extension-error:ext:beta"), true)
	})
	tw(t, f, "exclude matches case-insensitively", func(t *testing.T) {
		p := scopeOf(agent(true, "MCP"), builtins7, false, testExt{"/ext/mcp.ts", []string{"mcp_tool"}})
		eq(t, hasTool(p, "mcp_tool"), false)
		eq(t, len(warnings(p, "extension-error:")), 0)
	})
}

func TestUnknownBuiltinToolTwins(t *testing.T) {
	const f = "agent-runner"
	tw(t, f, "emits a tools-error warning for each plain entry not in BUILTIN_TOOL_NAMES", func(t *testing.T) {
		names := []string{"read", "reed", "grep", "edt"}
		p := scopeOf(func(c *agentConfig) { c.Extensions = false; c.BuiltinToolNames = names }, names, false)
		w := warnings(p, "tools-error:")
		eq(t, len(w), 2)
		eq(t, strings.Contains(w[0], `"reed"`) || strings.Contains(w[1], `"reed"`), true)
		eq(t, strings.Contains(w[0], `"edt"`) || strings.Contains(w[1], `"edt"`), true)
	})
	tw(t, f, "stays quiet when all plain tool names are valid built-ins", func(t *testing.T) {
		names := []string{"read", "grep"}
		p := scopeOf(func(c *agentConfig) { c.Extensions = false; c.BuiltinToolNames = names }, names, false)
		eq(t, len(warnings(p, "tools-error:")), 0)
	})
}

func TestExtToolSelectorTwins(t *testing.T) {
	const f = "agent-runner"
	sel := func(ext any, selectors ...string) func(*agentConfig) {
		return func(c *agentConfig) { c.Extensions = ext; c.ExtSelectors = selectors }
	}
	tw(t, f, "any ext: entry flips extension tools to an allowlist — non-selected extensions muted", func(t *testing.T) {
		// `tools: ext:foo` → zero built-ins, opt-in flip active.
		p := scopeOf(sel(true, "ext:foo"), []string{}, false, testExt{"/ext/foo.ts", []string{"foo_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, hasTool(p, "foo_tool"), true)
		eq(t, hasTool(p, "other_tool"), false) // loaded but muted
		eq(t, hasTool(p, "read"), false)       // tools: ext:foo → no built-ins
		eq(t, p.LoadAll, true)                 // both extensions still load: no loader filter under extensions: true
	})
	tw(t, f, "'*' alongside ext: keeps all built-ins while the flip still applies", func(t *testing.T) {
		p := scopeOf(sel(true, "ext:foo"), builtins7, false, testExt{"/ext/foo.ts", []string{"foo_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		for _, b := range builtins7 {
			eq(t, hasTool(p, b), true)
		}
		eq(t, hasTool(p, "foo_tool"), true)
		eq(t, hasTool(p, "other_tool"), false)
	})
	tw(t, f, "ext:foo/bar narrows foo to a single tool", func(t *testing.T) {
		p := scopeOf(sel(true, "ext:foo/bar"), []string{"read"}, false, testExt{"/ext/foo.ts", []string{"bar", "baz"}})
		eq(t, hasTool(p, "read") && hasTool(p, "bar"), true)
		eq(t, hasTool(p, "baz"), false)
	})
	tw(t, f, "ext:foo is orphaned when extensions: false — no extension loads, warning fires", func(t *testing.T) {
		p := scopeOf(sel(false, "ext:foo"), []string{"read"}, false, testExt{"/ext/foo.ts", []string{"foo_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, p.NoExtensions, true)
		eq(t, p.Callable(), []string{"read"})
		w := warnings(p, "extension-error:")
		eq(t, len(w) == 1 && strings.Contains(w[0], "extension-error:ext:foo"), true)
	})
	tw(t, f, "ext: cannot pull an extension that `extensions: [...]` excludes — warns, no surfacing", func(t *testing.T) {
		// extensions: [a] loads only a. ext:foo references foo, which is not loaded; the flip still mutes a (it is not
		// named in ext:), so the agent gets no extension tools and a warning fires.
		p := scopeOf(sel([]string{"a"}, "ext:foo"), []string{}, false, testExt{"/ext/a.ts", []string{"a_tool"}}, testExt{"/ext/foo.ts", []string{"foo_tool"}})
		eq(t, hasTool(p, "foo_tool"), false) // foo never loaded
		eq(t, hasTool(p, "a_tool"), false)   // a loaded but muted by the flip
		w := warnings(p, "extension-error:")
		eq(t, len(w) == 1 && strings.Contains(w[0], "extension-error:ext:foo"), true)
	})
	tw(t, f, "['*'] short-circuit survives ext: narrowing", func(t *testing.T) {
		p := scopeOf(sel([]string{"*"}, "ext:foo/bar"), []string{"read"}, false, testExt{"/ext/foo.ts", []string{"bar", "baz"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, p.LoadAll, true) // the pure-["*"] short-circuit holds
		eq(t, hasTool(p, "bar"), true)
		eq(t, hasTool(p, "baz"), false)
		eq(t, hasTool(p, "other_tool"), false) // the flip mutes the unselected extension
	})
	tw(t, f, "warns but proceeds when an ext: name doesn't match any loaded extension", func(t *testing.T) {
		p := scopeOf(sel(true, "ext:ghost"), []string{"read"}, false, testExt{"/ext/real.ts", []string{"real_tool"}})
		w := warnings(p, "extension-error:")
		eq(t, len(w) == 1 && strings.Contains(w[0], "extension-error:ext:ghost"), true)
	})
	tw(t, f, "isolated: true ignores extSelectors — no extension tools", func(t *testing.T) {
		p := scopeOf(sel(true, "ext:foo"), []string{"read"}, true, testExt{"/ext/foo.ts", []string{"foo_tool"}})
		eq(t, hasTool(p, "read"), true)
		eq(t, hasTool(p, "foo_tool"), false)
		eq(t, p.NoExtensions, true)
	})
	tw(t, f, "ext: composes with a path-loaded extension via its canonical name", func(t *testing.T) {
		// ext: is name-only (matched by canonical name), so it composes with extensions loaded by path through
		// extensions:. The path "/abs/foo.ts" has canonical name "foo", which ext:foo then surfaces: no orphan warning.
		p := scopeOf(sel([]string{"/abs/foo.ts"}, "ext:foo"), []string{"read"}, false, testExt{"/abs/foo.ts", []string{"foo_tool"}}, testExt{"/ext/other.ts", []string{"other_tool"}})
		eq(t, p.AdditionalPaths, []string{"/abs/foo.ts"})
		eq(t, hasTool(p, "foo_tool") && hasTool(p, "read"), true)
		eq(t, hasTool(p, "other_tool"), false) // dropped at the loader: not in the names it keeps
		eq(t, len(warnings(p, "extension-error:")), 0)
	})
	tw(t, f, "ext:foo/Bar narrowing is case-sensitive on the tool half", func(t *testing.T) {
		p := scopeOf(sel(true, "ext:foo/Bar"), []string{"read"}, false, testExt{"/ext/foo.ts", []string{"Bar", "bar"}})
		eq(t, hasTool(p, "Bar"), true)
		eq(t, hasTool(p, "bar"), false)
	})
	tw(t, f, "disallowedTools removes a tool reached via an ext: selector", func(t *testing.T) {
		p := scopeOf(func(c *agentConfig) {
			c.Extensions = true
			c.ExtSelectors = []string{"ext:foo"}
			c.DisallowedTools = []string{"foo_tool"}
		}, []string{"read"}, false,
			testExt{"/ext/foo.ts", []string{"foo_tool", "foo_other"}})
		eq(t, hasTool(p, "read") && hasTool(p, "foo_other"), true)
		eq(t, hasTool(p, "foo_tool"), false)
	})
}
