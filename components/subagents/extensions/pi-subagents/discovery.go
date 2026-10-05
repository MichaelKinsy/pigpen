package pi_subagents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// agentDir is the user's agent directory: PI_CODING_AGENT_DIR (with a leading ~), else ~/.pi/agent.
func agentDir() string {
	configured := os.Getenv("PI_CODING_AGENT_DIR")
	home := homeDir()
	switch {
	case configured == "~":
		return home
	case strings.HasPrefix(configured, "~/") || strings.HasPrefix(configured, `~\`):
		return filepath.Join(home, configured[2:])
	case configured != "":
		return configured
	}
	return filepath.Join(home, ".pi", "agent")
}

func projectConfigDir(root string) string { return filepath.Join(root, ".pi") }

func realDir(p string) string {
	if !isDir(p) {
		return ""
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}

func findProjectRootCandidates(cwd string) []string {
	homes := map[string]bool{}
	candidates := []string{homeDir(), os.Getenv("HOME"), os.Getenv("USERPROFILE")}
	if d, p := os.Getenv("HOMEDRIVE"), os.Getenv("HOMEPATH"); d != "" && p != "" {
		candidates = append(candidates, d+p)
	}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if r := realDir(c); r != "" {
			homes[r] = true
		}
	}
	var roots []string
	cur := cwd
	for {
		// ~/.pi and ~/.agents are user configuration, never an implicit project
		if r := realDir(cur); r != "" && homes[r] {
			return roots
		}
		if isDir(projectConfigDir(cur)) || isDir(filepath.Join(cur, ".agents")) {
			roots = append(roots, cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return roots
		}
		cur = parent
	}
}

func findNearestGitRoot(cwd string) string {
	cur := cwd
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// projectRootResolution reads `subagents.projectRootResolution` from the project's settings.json: "", "nearest" or "git-root".
func projectRootResolution(root string) string {
	b, err := os.ReadFile(filepath.Join(projectConfigDir(root), "settings.json"))
	if err != nil {
		return ""
	}
	v, err := parseJSON(b)
	o, _ := v.(*jsObject)
	if err != nil || o == nil {
		return ""
	}
	if s := o.obj("subagents"); s != nil {
		if m, _ := s.str("projectRootResolution"); m == "nearest" || m == "git-root" {
			return m
		}
	}
	return ""
}

// FindConfiguredProjectRoot is the project root for cwd ("" for none): the nearest directory with a .pi or .agents,
// unless a settings file asks for the git root.
func FindConfiguredProjectRoot(cwd string) string {
	candidates := findProjectRootCandidates(cwd)
	if len(candidates) == 0 {
		return ""
	}
	nearest := candidates[0]
	policyIdx := -1
	for i, c := range candidates {
		switch projectRootResolution(c) {
		case "nearest":
			return nearest
		case "git-root":
			policyIdx = i
		}
		if policyIdx >= 0 {
			break
		}
	}
	if policyIdx < 0 {
		return nearest
	}
	policy := candidates[policyIdx]
	if git := findNearestGitRoot(cwd); git != "" {
		for _, c := range candidates[policyIdx:] {
			if filepath.Clean(c) == filepath.Clean(git) {
				return c
			}
		}
	}
	if _, err := os.Stat(filepath.Join(policy, ".git")); err == nil {
		return policy
	}
	return nearest
}

var prunedDirNames = map[string]bool{".git": true, "node_modules": true, ".pi": true, "sync-backups": true}

func shouldPrune(root, dir, name string) bool {
	if prunedDirNames[name] {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return filepath.Clean(dir) != filepath.Clean(root) && (isDir(projectConfigDir(dir)) || isDir(filepath.Join(dir, ".agents")))
}

// isLegacySkillPath: files under a .agents/skills directory are skills, never agents.
func isLegacySkillPath(root, file string) bool {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return false
	}
	parts := strings.Split(strings.ToLower(rel), string(filepath.Separator))
	if strings.EqualFold(filepath.Base(root), ".agents") {
		parts = append([]string{".agents"}, parts...)
	}
	for i, p := range parts {
		if p == ".agents" && i+1 < len(parts) && parts[i+1] == "skills" {
			return true
		}
	}
	return false
}

// listFiles lists the files under dir for which keep is true, depth first, directories in name order, following
// symlinked directories once.
func listFiles(dir string, keep func(name string) bool) []string {
	var out []string
	visited := map[string]bool{}
	var walk func(cur string)
	walk = func(cur string) {
		real, err := filepath.EvalSymlinks(cur)
		if err != nil || visited[real] {
			return
		}
		visited[real] = true
		entries, err := os.ReadDir(cur)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return localeLess(entries[i].Name(), entries[j].Name()) })
		for _, e := range entries {
			p := filepath.Join(cur, e.Name())
			isD := e.IsDir()
			isFile := e.Type().IsRegular()
			if e.Type()&os.ModeSymlink != 0 {
				st, err := os.Stat(p)
				isD = err == nil && st.IsDir()
				isFile = err == nil && st.Mode().IsRegular()
			}
			if isD {
				if !shouldPrune(dir, p, e.Name()) {
					walk(p)
				}
				continue
			}
			if isFile && keep(e.Name()) {
				out = append(out, p)
			}
		}
	}
	if isDir(dir) {
		walk(dir)
	}
	return out
}

func readDefinitions(dir string) []definitionFile {
	var files []definitionFile
	for _, p := range listFiles(dir, func(n string) bool { return strings.HasSuffix(n, ".md") && !strings.HasSuffix(n, ".chain.md") }) {
		if isLegacySkillPath(dir, p) {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			files = append(files, definitionFile{p, string(b)})
		}
	}
	return files
}

func loadChains(dir string, source AgentSource) ([]ChainConfig, []Diagnostic) {
	var order []string
	byName := map[string]ChainConfig{}
	var diags []Diagnostic
	for _, p := range listFiles(dir, func(n string) bool { return strings.HasSuffix(n, ".chain.md") }) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		c, err := ParseChain(string(b), source, p)
		if err != nil {
			diags = append(diags, Diagnostic{Source: source, FilePath: p, Error: err.Error()})
			continue
		}
		if _, ok := byName[c.Name]; !ok {
			order = append(order, c.Name)
		}
		byName[c.Name] = *c
	}
	chains := make([]ChainConfig, 0, len(order))
	for _, n := range order {
		chains = append(chains, byName[n])
	}
	return chains, diags
}

func extraUserAgentDirs() []string {
	var out []string
	for _, d := range filepath.SplitList(os.Getenv("PI_SUBAGENT_EXTRA_AGENT_DIRS")) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// DiscoverAgentsAll finds every agent and chain definition: the builtin ones, the user's (the agent directory's
// agents and ~/.agents) and the project's (.agents and .pi/agents of the project root).
func DiscoverAgentsAll(cwd string) DiscoveryAll {
	cwd, _ = filepath.Abs(cwd)
	r := DiscoveryAll{}
	var d []Diagnostic
	r.Builtin, d = builtinAgents()
	r.Diagnostics = append(r.Diagnostics, d...)

	userOld, userNew := filepath.Join(agentDir(), "agents"), filepath.Join(homeDir(), ".agents")
	for _, dir := range append(extraUserAgentDirs(), userOld, userNew) {
		agents, d := loadAgents(readDefinitions(dir), SourceUser, 0)
		r.User = append(r.User, agents...)
		r.Diagnostics = append(r.Diagnostics, d...)
	}
	r.UserDir = userOld
	if os.Getenv("PI_CODING_AGENT_DIR") == "" {
		if _, err := os.Stat(userNew); err == nil {
			r.UserDir = userNew
		}
	}
	r.UserChainDir = filepath.Join(agentDir(), "chains")
	chains, cd := loadChains(r.UserChainDir, SourceUser)
	r.Chains = append(r.Chains, chains...)
	r.ChainDiagnostics = append(r.ChainDiagnostics, cd...)

	if root := FindConfiguredProjectRoot(cwd); root != "" {
		legacy, preferred := filepath.Join(root, ".agents"), filepath.Join(projectConfigDir(root), "agents")
		r.ProjectDir = preferred
		var order []string
		byName := map[string]AgentConfig{}
		for _, dir := range []string{legacy, preferred} {
			if !isDir(dir) {
				continue
			}
			agents, d := loadAgents(readDefinitions(dir), SourceProject, 0)
			r.Diagnostics = append(r.Diagnostics, d...)
			for _, a := range agents {
				if _, ok := byName[a.Name]; !ok {
					order = append(order, a.Name)
				}
				byName[a.Name] = a
			}
		}
		for _, n := range order {
			r.Project = append(r.Project, byName[n])
		}
		r.ProjectChainDir = filepath.Join(projectConfigDir(root), "chains")
		if isDir(r.ProjectChainDir) {
			chains, cd := loadChains(r.ProjectChainDir, SourceProject)
			r.Chains = append(r.Chains, chains...)
			r.ChainDiagnostics = append(r.ChainDiagnostics, cd...)
		}
	}
	return r
}

// DiscoverAgents is the effective set of agents for a scope: project over user over builtin.
func DiscoverAgents(cwd string, scope AgentScope) Discovery {
	all := DiscoverAgentsAll(cwd)
	d := Discovery{Agents: MergeAgentsForScope(scope, all.User, all.Project, all.Builtin, all.Package), Diagnostics: all.Diagnostics, ProjectAgentsDir: all.ProjectDir, UserDir: all.UserDir}
	return d
}
