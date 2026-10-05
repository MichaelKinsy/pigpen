package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The port's own tests: behavior the original's tests reach through machinery that is not ported (the serializers, the
// management actions, the settings), or that they leave to the TypeScript types. Each says what it pins.

func TestFrontmatterEdges(t *testing.T) {
	fm, body := ParseFrontmatter("---\r\nname: a\r\ndescription: b\r\n---\r\nbody\r\n")
	eq(t, []string{fm.Get("name"), fm.Get("description"), body}, []string{"a", "b", "body"}, "CRLF input")
	fm, body = ParseFrontmatter("---\nname: a\n--dash: y\n---\nbody")
	eq(t, []string{fm.Get("name"), body}, []string{"a", "body"}, "a line that starts with -- does not end the block")
	_, body = ParseFrontmatter("no header here")
	eq(t, body, "no header here", "no frontmatter")
	_, body = ParseFrontmatter("---\nname: a\nnever closed")
	eq(t, body, "---\nname: a\nnever closed", "unclosed frontmatter is body")
	fm, _ = ParseFrontmatter("---\ndescription: >\n  one\n  two\n\n  three\nname: w\n---\nx")
	eq(t, fm.Get("description"), "one two\nthree", "a blank line inside a folded block")
	fm, _ = ParseFrontmatter("---\n10: ten\n2: two\nname: n\n---\nx")
	eq(t, fm.Keys(), []string{"2", "10", "name"}, "integer-like keys come first, as in a JavaScript object")
	s := func(v string) *string { return &v }
	eq(t, ParseFrontmatterList(s("- a\n- b")), []string{"a", "b"}, "block list")
	eq(t, ParseFrontmatterList(s("foo-bar, - baz")), []string{"foo-bar", "- baz"}, "a hyphen inside a value stays")
	eq(t, ParseFrontmatterList(s("")), []string{}, "empty list")
	eq(t, ParseFrontmatterList(nil), []string(nil), "absent list")
}

func TestChainEdges(t *testing.T) {
	_, err := ParseChain("---\nname: only-name\n---\n\n## a\n\nx\n", SourceProject, "/tmp/c.chain.md")
	hasMatch(t, err, "Chain frontmatter must include name and description")
	_, err = ParseChain(chainText("## a\ntoolBudget: {\"hard\":0}\n\nx\n"), SourceProject, "/tmp/c")
	hasMatch(t, err, "toolBudget for step 'a'.hard must be an integer >= 1.")
	_, err = ParseChain(chainText("## a\ntoolBudget: {\"hard\":5,\"soft\":5}\n\nx\n"), SourceProject, "/tmp/c")
	if err != nil {
		t.Errorf("soft equal to hard is allowed: %v", err)
	}
	_, err = ParseChain(chainText("## a\ntoolBudget: {\"hard\":2,\"block\":[]}\n\nx\n"), SourceProject, "/tmp/c")
	hasMatch(t, err, "block must contain at least one tool name")
	_, err = ParseChain(chainText("## a\ntoolBudget: nope\n\nx\n"), SourceProject, "/tmp/c")
	hasMatch(t, err, "Invalid toolBudget in .chain.md step 'a'")
	_, err = ParseChain(chainText("## a\noutputSchema: [1]\n\nx\n"), SourceProject, "/tmp/c")
	hasMatch(t, err, "Inline outputSchema values are not supported")

	c := mustChain(t, chainText("## a\noutput: false\nreads: x,y\nskills: false\nprogress: true\nmodel: m1\nmachine: box\n\n\n\nThe task\n\n## b   \noutput: out.md\nreads: false\n\nNext\n"))
	eq(t, c.Steps[0].Output, Tri[string]{Set: true, False: true}, "output: false")
	eq(t, c.Steps[0].Reads, TriList{Set: true, Items: []string{"x", "y"}}, "reads list")
	eq(t, c.Steps[0].Skills, TriList{Set: true, False: true}, "skills: false")
	eq(t, *c.Steps[0].Progress, true, "progress")
	eq(t, []string{c.Steps[0].Model, c.Steps[0].Machine, c.Steps[0].Task}, []string{"m1", "box", "The task"}, "model, machine, and a task after several blank lines")
	eq(t, []string{c.Steps[1].Agent, c.Steps[1].Output.Value}, []string{"b", "out.md"}, "a heading with trailing spaces")
	eq(t, c.Steps[1].Reads, TriList{Set: true, False: true}, "reads: false")
	out := SerializeChain(c)
	for _, want := range []string{"output: false", "reads: x, y", "skills: false", "progress: true", "model: m1", "machine: box", "## b\n", "output: out.md", "reads: false"} {
		if !strings.Contains(out, want) {
			t.Errorf("serialized chain lacks %q:\n%s", want, out)
		}
	}

	last := mustChain(t, chainText("## a\n\nx\n\n## z  "))
	eq(t, []string{last.Steps[1].Agent, last.Steps[1].Task}, []string{"z", ""}, "a last heading with trailing spaces and no newline")

	p := mustChain(t, "---\nname: flow\npackage: code-analysis\ndescription: D\nowner: team\n---\n\n## a\n\nx\n")
	eq(t, marshalJSON(p.ExtraFields, ""), `{"owner":"team"}`, "extra fields keep other keys, not package")
	s := SerializeChain(p)
	for _, want := range []string{"name: flow\n", "package: code-analysis\n", "owner: team\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("serialized chain lacks %q:\n%s", want, s)
		}
	}
}

func TestPackageNames(t *testing.T) {
	n, e := parsePackageName("--Code  Analysis--", true, "p")
	eq(t, []string{n, e}, []string{"code-analysis", ""}, "edges and spaces")
	_, e = parsePackageName("a.-b", true, "label")
	eq(t, e, "label is invalid after sanitization.", "a name the identifier pattern refuses")
	n, e = parsePackageName("", true, "p")
	eq(t, []string{n, e}, []string{"", ""}, "empty means no package")
	eq(t, frontmatterName("pkg.scout", "", "pkg"), "scout", "the package prefix comes off")
	eq(t, frontmatterName("scout", "", ""), "scout", "no package")
	eq(t, buildRuntimeName("scout", " pkg "), "pkg.scout", "runtime name")
}

func TestResolveEdges(t *testing.T) {
	agents := []AgentConfig{mk("scout", SourceBuiltin, "b"), mk("scout", SourceProject, "p"), mk("scout", SourceUser, "u")}
	a, msg := ResolveAgentName("scout", agents)
	if a == nil || a.Source != SourceProject || msg != "" {
		t.Errorf("same name in several sources picks the highest: %#v %q", a, msg)
	}
	a, _ = ResolveAgentName("", []AgentConfig{mk("x", SourceProject, "p")})
	eq(t, a, (*AgentConfig)(nil), "an empty name resolves to nothing")
	al := mk("worker", SourceProject, "p")
	al.Aliases = []string{"dev"}
	a, _ = ResolveAgentName(" dev ", []AgentConfig{al})
	if a == nil || a.Name != "worker" {
		t.Errorf("alias: %#v", a)
	}
}

func TestDiscoveryEdges(t *testing.T) {
	withTempHome(t)
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, ".pi", "agents", "ignored.chain.md"), "---\nname: ignored-chain\ndescription: x\n---\n\n## a\n\nx\n")
	writeFile(t, filepath.Join(dir, ".pi", "agents", "sub", ".git", "HEAD"), "ref")
	writeFile(t, filepath.Join(dir, ".pi", "agents", "sub", "inner.md"), agentMD("inner", "Inner", "x"))
	writeFile(t, filepath.Join(dir, ".pi", "agents", "nodesc.md"), "---\nname: nodesc\n---\nBody")
	agents := DiscoverAgents(dir, ScopeProject).Agents
	eq(t, findAgent(agents, "ignored-chain"), (*AgentConfig)(nil), "a .chain.md is not an agent")
	eq(t, findAgent(agents, "inner"), (*AgentConfig)(nil), "a directory with its own .git is pruned")
	eq(t, findAgent(agents, "nodesc"), (*AgentConfig)(nil), "a file without a description is not an agent")
	eq(t, len(DiscoverAgents(dir, ScopeProject).Diagnostics), 0, "and is not an error either")

	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".agents", "uagent.md"), agentMD("uagent", "User", "x"))
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "oagent.md"), agentMD("oagent", "Old dir", "x"))
	u := DiscoverAgents(dir, ScopeUser).Agents
	if findAgent(u, "uagent") == nil || findAgent(u, "oagent") == nil {
		t.Errorf("both user directories are read: %#v", u)
	}
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "custom"))
	writeFile(t, filepath.Join(home, "custom", "chains", "c.chain.md"), "---\nname: cc\ndescription: x\n---\n\n## a\n\nx\n")
	found := false
	for _, c := range DiscoverAgentsAll(dir).Chains {
		found = found || c.Name == "cc"
	}
	eq(t, found, true, "chains come from the agent directory")
}

func TestGitRootResolution(t *testing.T) {
	withTempHome(t)
	repo := tmp(t)
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref")
	writeFile(t, filepath.Join(repo, ".pi", "settings.json"), `{"subagents":{"projectRootResolution":"git-root"}}`)
	nested := filepath.Join(repo, "pkg", "app")
	writeFile(t, filepath.Join(nested, ".pi", "keep"), "")
	eq(t, FindConfiguredProjectRoot(nested), repo, "git-root policy picks the repository root")
	writeFile(t, filepath.Join(repo, ".pi", "settings.json"), `{"subagents":{"projectRootResolution":"nearest"}}`)
	eq(t, FindConfiguredProjectRoot(nested), nested, "nearest policy picks the nearest")
}

func TestAgentFields(t *testing.T) {
	withTempHome(t)
	dir := tmp(t)
	w := func(name, fm string) {
		writeFile(t, filepath.Join(dir, ".pi", "agents", name+".md"), "---\nname: "+name+"\ndescription: D\n"+fm+"---\n\nBody\n")
	}
	w("w", "aliases: developer, coder, w\ntools: read, mcp:search, grep\nthinking: false\nasync: false\nskill: lint, style\nacceptanceRole: writer\ndefaultReads: a.md, b.md\ndefaultProgress: true\n")
	w("bad", "async: maybe\n")
	d := DiscoverAgents(dir, ScopeProject)
	a := findAgent(d.Agents, "w")
	if a == nil {
		t.Fatal("w missing")
	}
	eq(t, a.Aliases, []string{"developer", "coder"}, "an alias equal to the name is dropped")
	eq(t, a.Tools, []string{"read", "grep"}, "tools")
	eq(t, a.McpDirectTools, []string{"search"}, "mcp tools split off")
	eq(t, a.Thinking, any(false), "thinking: false")
	eq(t, *a.DefaultAsync, false, "async false")
	eq(t, a.Skills, []string{"lint", "style"}, "skill")
	eq(t, []any{a.AcceptanceRole, a.DefaultReads, a.DefaultProgress}, []any{"writer", []string{"a.md", "b.md"}, true}, "acceptance role, reads, progress")
	eq(t, findAgent(d.Agents, "bad"), (*AgentConfig)(nil), "an invalid async value drops the agent")
	hasDiag := false
	for _, x := range d.Diagnostics {
		hasDiag = hasDiag || (x.Name == "bad" && strings.Contains(x.Error, "invalid async frontmatter"))
	}
	eq(t, hasDiag, true, "and says why")
	if c, err := parseRunner("type: external-cli\ncommand: ", "x"); err == nil || c != nil {
		t.Errorf("an external-cli runner needs a command: %v", err)
	}
}

func TestManagementEdges(t *testing.T) {
	home := withTempHome(t)
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	writeFile(t, filepath.Join(dir, ".pi", "agents", "dup.md"), agentMD("dup", "Project dup", "p"))
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "dup.md"), "---\nname: dup\ndescription: Broken user dup\nrunner:\n  type: nope\n---\nx")
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "only-user.md"), "---\nname: only-user\ndescription: Broken\nrunner:\n  type: nope\n---\nx")
	writeFile(t, filepath.Join(dir, ".pi", "agents", "pbroken.md"), "---\nname: pbroken\ndescription: Broken\nrunner:\n  type: nope\n---\nx")
	writeFile(t, filepath.Join(home, ".pi", "agent", "agents", "pbroken.md"), agentMD("pbroken", "User valid", "u"))

	r := getAgent(dir, map[string]any{"agent": "  HELPER "})
	if r.isError || !strings.HasPrefix(r.text, "Agent: helper (project)") {
		t.Errorf("a sanitized name finds the agent: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "dup"})
	if r.isError || !strings.Contains(r.text, "Project dup") {
		t.Errorf("a valid project agent shadows a broken user one: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "pbroken"})
	if !r.isError || !strings.Contains(r.text, "has invalid configuration") {
		t.Errorf("a broken project agent blocks a valid user one: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "pbroken", "agentScope": "user"})
	if r.isError || !strings.Contains(r.text, "User valid") {
		t.Errorf("a project diagnostic is out of scope for user: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "only-user", "agentScope": "project"})
	if !r.isError || !strings.Contains(r.text, "not found") {
		t.Errorf("a user diagnostic is out of scope for project: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "only-user"})
	if !r.isError || !strings.Contains(r.text, "has invalid configuration") {
		t.Errorf("a user diagnostic is in scope for both: %+v", r)
	}
	r = getAgent(dir, map[string]any{"agent": "helper", "agentScope": "nowhere"})
	eq(t, r.text, "agentScope must be 'user', 'project', or 'both' for get.", "bad scope")

	l := listAgents(dir, map[string]any{}).text
	if !(strings.Index(l, "User agents") < strings.Index(l, "Project agents")) {
		t.Errorf("sections come user, project, builtin:\n%s", l)
	}
	if strings.Index(l, "- delegate (") > strings.Index(l, "- evidence-auditor (") || strings.Index(l, "- helper (project)") > strings.Index(l, "- pbroken (project") {
		t.Errorf("agents are sorted by name:\n%s", l)
	}
	if !strings.Contains(l, "Invalid agent definitions:") || !strings.Contains(l, "- only-user (user): ") {
		t.Errorf("diagnostics are listed:\n%s", l)
	}
	if !strings.Contains(listAgents(dir, map[string]any{"agentScope": "project"}).text, "- helper (project): Helps") {
		t.Error("project list")
	}
	w := mk("worker", SourceProject, "p")
	w.Aliases = []string{"dev", "coder"}
	eq(t, listLine(w), "- worker (project, aliases: dev, coder): worker agent", "aliases in a list line")
}

func TestToolSelection(t *testing.T) {
	eq(t, nextSelection([]string{"read", "bash"}, true), []string{"read", "bash", "subagent", "subagents_enable"}, "enable adds the tool and the loader")
	eq(t, nextSelection([]string{"read", "subagent", "subagents_enable"}, true), []string{"read", "subagent", "subagents_enable"}, "enabling twice does not duplicate")
	eq(t, nextSelection([]string{"read", "subagent"}, false), []string{"read", "subagents_enable"}, "start removes the tool and keeps the loader")
	eq(t, nextSelection([]string{"read", "subagents_enable", "read"}, false), []string{"read", "subagents_enable"}, "duplicates collapse")
}

func TestRunRequestAndChainLookup(t *testing.T) {
	_, _, ok := runRequest(map[string]any{"agent": "a"})
	eq(t, ok, false, "a task is required")
	n, tk, ok := runRequest(map[string]any{"agent": "a", "task": "t"})
	eq(t, []any{n, tk, ok}, []any{"a", "t", true}, "agent and task")
	chains := []ChainConfig{{Name: "x", Source: SourceUser}, {Name: "x", Source: SourceProject}, {Name: "pkg.y", LocalName: "y"}}
	eq(t, findChain(chains, "x").Source, SourceProject, "project over user")
	eq(t, findChain(chains, "y").Name, "pkg.y", "by local name")
	eq(t, findChain(chains, "z"), (*ChainConfig)(nil), "unknown")
}

func TestChildEnvironment(t *testing.T) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	var env []string
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		env = req.Env
		return childResult{Stdout: "ok"}, nil
	}
	if _, err := runAgent(nil, ".", &AgentConfig{Name: "scout", SystemPromptMode: "replace"}, "t", ""); err != nil {
		t.Fatal(err)
	}
	// the whole environment is given (childEnv: this process's, less the host's internals), ending with who the child is
	eq(t, env[len(env)-3:], []string{"PIG_SUBAGENT=1", "PIG_SUBAGENT_DEPTH=1", "PIG_AGENT_ROLE=scout"}, "the child knows it is a subagent, how deep and which")
}
