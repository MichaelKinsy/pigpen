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
	return filepath.Join(home, getConfigDirName(), "agent")
}

func projectConfigDir(root string) string { return getProjectConfigDir(root) }

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

func extraUserAgentDirs() []string {
	var out []string
	for _, d := range filepath.SplitList(os.Getenv("PI_SUBAGENT_EXTRA_AGENT_DIRS")) {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}
