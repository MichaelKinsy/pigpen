package pi_subagents

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Package-provided agents and chains (src/agents/agents.ts:364-689, src/agents/global-npm-root.ts).

type packageScope string

const (
	pkgScopeRoot    packageScope = "root"
	pkgScopeUser    packageScope = "user"
	pkgScopeProject packageScope = "project"
)

type scopeSet map[packageScope]bool

// packageSubagentPath is one agents directory a package declares.
type packageSubagentPath struct {
	dir            string
	scope          scopeSet
	packageName    string
	packageVersion string
	packageRoot    string
}

type packageChainPath struct {
	dir         string
	scope       scopeSet
	packageRoot string
}

type packageSubagentPaths struct {
	agents         []*packageSubagentPath
	chains         []*packageChainPath
	watchPaths     []string
	settingsErrors map[string]error
}

// npmRootLookup caches one `npm root -g` result for the process; the agent discovery and the skill discovery of the original
// each keep their own.
type npmRootLookup struct {
	mu   sync.Mutex
	set  bool
	root string
}

var (
	agentsNpmRoot npmRootLookup
	skillsNpmRoot npmRootLookup
)

func readJSONFileBestEffort(p string) any {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil
	}
	return v
}

func isSafePackagePath(v string) bool {
	if v == "" || filepath.IsAbs(v) {
		return false
	}
	for _, part := range regexp.MustCompile(`[\\/]`).Split(v, -1) {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

var npmSpecRe = regexp.MustCompile(`^(@?[^@]+(?:/[^@]+)?)(?:@(.+))?$`)

func parseNpmPackageName(source string) string {
	spec := jsTrim(source[4:])
	if spec == "" {
		return ""
	}
	name := spec
	if m := npmSpecRe.FindStringSubmatch(spec); m != nil {
		name = m[1]
	}
	if isSafePackagePath(name) {
		return name
	}
	return ""
}

func stripGitRef(repoPath string) string {
	at, hash := strings.Index(repoPath, "@"), strings.Index(repoPath, "#")
	idx := -1
	for _, i := range []int{at, hash} {
		if i >= 0 && (idx == -1 || i < idx) {
			idx = i
		}
	}
	if idx == -1 {
		return repoPath
	}
	return repoPath[:idx]
}

var (
	scpLikeRe   = regexp.MustCompile(`^git@([^:]+):(.+)$`)
	urlSchemeRe = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)
	httpRe      = regexp.MustCompile(`(?i)^https?://`)
	leadSlash   = regexp.MustCompile(`^/+`)
)

func parseGitPackagePath(source string) (host, repoPath string, ok bool) {
	spec := jsTrim(source[4:])
	if spec == "" {
		return "", "", false
	}
	if m := scpLikeRe.FindStringSubmatch(spec); m != nil {
		host, repoPath = m[1], m[2]
	} else if urlSchemeRe.MatchString(spec) {
		u, err := url.Parse(spec)
		if err != nil {
			return "", "", false
		}
		host = strings.ToLower(u.Hostname())
		repoPath = leadSlash.ReplaceAllString(u.EscapedPath(), "")
	} else {
		i := strings.Index(spec, "/")
		if i < 0 {
			return "", "", false
		}
		host, repoPath = spec[:i], spec[i+1:]
	}
	normalized := strings.TrimSuffix(stripGitRef(repoPath), ".git")
	normalized = leadSlash.ReplaceAllString(normalized, "")
	if host == "" || !isSafePackagePath(host) || !isSafePackagePath(normalized) || len(regexp.MustCompile(`[\\/]`).Split(normalized, -1)) < 2 {
		return "", "", false
	}
	return host, normalized, true
}

// resolveSettingsPackageRoot is agents.ts:455.
func resolveSettingsPackageRoot(source, baseDir string) string {
	trimmed := jsTrim(source)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "git:") {
		if h, r, ok := parseGitPackagePath(trimmed); ok {
			return filepath.Join(baseDir, "git", h, r)
		}
		return ""
	}
	if strings.HasPrefix(trimmed, "npm:") {
		if n := parseNpmPackageName(trimmed); n != "" {
			return filepath.Join(baseDir, "npm", "node_modules", n)
		}
		return ""
	}
	normalized := strings.TrimPrefix(trimmed, "file:")
	if strings.HasPrefix(trimmed, "file:") {
		normalized = trimmed[5:]
	}
	if normalized == "~" {
		return homeDir()
	}
	if strings.HasPrefix(normalized, "~/") {
		return filepath.Join(homeDir(), normalized[2:])
	}
	if filepath.IsAbs(normalized) {
		return normalized
	}
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, "../") {
		return filepath.Join(baseDir, normalized)
	}
	if httpRe.MatchString(trimmed) {
		if h, r, ok := parseGitPackagePath("git:" + trimmed); ok {
			return filepath.Join(baseDir, "git", h, r)
		}
	}
	return ""
}

func offlineEnv() bool {
	switch strings.ToLower(os.Getenv("PI_OFFLINE")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// getGlobalNpmRoot is agents.ts:480: the global node_modules, "" when offline or npm is unavailable. The result is cached
// for the process.
func getGlobalNpmRoot() string { return agentsNpmRoot.get() }

func (l *npmRootLookup) get() string {
	if offlineEnv() {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.set {
		return l.root
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			root := filepath.Join(appData, "npm", "node_modules")
			if isDir(root) {
				if real, err := filepath.EvalSymlinks(root); err == nil {
					l.root, l.set = real, true
					return real
				}
			}
		}
	}
	l.set = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "root", "-g").Output()
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(jsTrim(string(out)))
	if err != nil {
		return ""
	}
	l.root = real
	return real
}

func nonEmptyStringList(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, e := range arr {
		if s, ok := e.(string); ok && jsTrim(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimmedStringField(o *jsObject, k string) string {
	if s, ok := o.str(k); ok && jsTrim(s) != "" {
		return jsTrim(s)
	}
	return ""
}

// extractSubagentPathsFromPackageRoot is agents.ts:523.
func extractSubagentPathsFromPackageRoot(root string, scope scopeSet) packageSubagentPaths {
	pj := filepath.Join(root, "package.json")
	res := packageSubagentPaths{watchPaths: []string{pj}, settingsErrors: map[string]error{}}
	pkg, ok := readJSONFileBestEffort(pj).(*jsObject)
	if !ok {
		return res
	}
	name, version := trimmedStringField(pkg, "name"), trimmedStringField(pkg, "version")
	var roots []*jsObject
	if o := pkg.obj("pi-subagents"); o != nil {
		roots = append(roots, o)
	}
	if pi := pkg.obj("pi"); pi != nil {
		if o := pi.obj("subagents"); o != nil {
			roots = append(roots, o)
		}
	}
	for _, r := range roots {
		v, _ := r.get("agents")
		for _, e := range nonEmptyStringList(v) {
			abs, _ := filepath.Abs(filepath.Join(root, e))
			res.agents = append(res.agents, &packageSubagentPath{dir: abs, scope: scope, packageName: name, packageVersion: version, packageRoot: root})
		}
		c, _ := r.get("chains")
		for _, e := range nonEmptyStringList(c) {
			abs, _ := filepath.Abs(filepath.Join(root, e))
			res.chains = append(res.chains, &packageChainPath{dir: abs, scope: scope, packageRoot: root})
		}
	}
	return res
}

// collectPackageRootsFromNodeModules is agents.ts:553.
func collectPackageRootsFromNodeModules(nodeModulesDir string, watch *[]string) []string {
	var roots []string
	if watch != nil {
		*watch = append(*watch, nodeModulesDir)
	}
	if _, err := os.Stat(nodeModulesDir); err != nil {
		return roots
	}
	entries, err := os.ReadDir(nodeModulesDir)
	if err != nil {
		return roots
	}
	isDirLike := func(e os.DirEntry) bool { return e.IsDir() || e.Type()&os.ModeSymlink != 0 }
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !isDirLike(e) {
			continue
		}
		if strings.HasPrefix(e.Name(), "@") {
			scopeDir := filepath.Join(nodeModulesDir, e.Name())
			if watch != nil {
				*watch = append(*watch, scopeDir)
			}
			sub, err := os.ReadDir(scopeDir)
			if err != nil {
				continue
			}
			for _, s := range sub {
				if strings.HasPrefix(s.Name(), ".") || !isDirLike(s) {
					continue
				}
				roots = append(roots, filepath.Join(scopeDir, s.Name()))
			}
			continue
		}
		roots = append(roots, filepath.Join(nodeModulesDir, e.Name()))
	}
	return roots
}

// collectSettingsPackageRoots is agents.ts:591.
func collectSettingsPackageRoots(settingsFile, baseDir string) ([]string, error) {
	settings, err := readSettingsFileStrict(settingsFile)
	if err != nil {
		return nil, err
	}
	pkgs, _ := settings.vals["packages"].([]any)
	var roots []string
	for _, e := range pkgs {
		src := ""
		switch x := e.(type) {
		case string:
			src = x
		case *jsObject:
			src, _ = x.str("source")
		}
		if src == "" {
			continue
		}
		if r := resolveSettingsPackageRoot(src, baseDir); r != "" {
			roots = append(roots, r)
		}
	}
	return roots, nil
}

type packageDiscoveryOptions struct {
	includeUser, includeProject bool
	globalNpmRoot               *string // nil: look it up; pointer to "": skip global discovery
}

// collectPackageSubagentPaths is agents.ts:610.
func collectPackageSubagentPaths(cwd string, opts packageDiscoveryOptions) packageSubagentPaths {
	agentDirectory := agentDir()
	projectRoot := FindConfiguredProjectRoot(cwd)
	if projectRoot == "" {
		projectRoot = cwd
	}
	type rootScope struct {
		root  string
		scope packageScope
	}
	roots := []rootScope{{projectRoot, pkgScopeRoot}}
	watch := []string{filepath.Join(projectRoot, "package.json")}
	settingsErrors := map[string]error{}
	scoped := func(scope, file, base string) []string {
		r, err := collectSettingsPackageRoots(file, base)
		if err != nil {
			settingsErrors[scope] = err
			return nil
		}
		return r
	}
	if opts.includeProject {
		pcd := getProjectConfigDir(projectRoot)
		for _, r := range collectPackageRootsFromNodeModules(filepath.Join(pcd, "npm", "node_modules"), &watch) {
			roots = append(roots, rootScope{r, pkgScopeProject})
		}
		for _, r := range scoped("project", filepath.Join(pcd, "settings.json"), pcd) {
			roots = append(roots, rootScope{r, pkgScopeProject})
		}
	}
	if opts.includeUser {
		for _, r := range collectPackageRootsFromNodeModules(filepath.Join(agentDirectory, "npm", "node_modules"), &watch) {
			roots = append(roots, rootScope{r, pkgScopeUser})
		}
		for _, r := range scoped("user", filepath.Join(agentDirectory, "settings.json"), agentDirectory) {
			roots = append(roots, rootScope{r, pkgScopeUser})
		}
		global := ""
		if opts.globalNpmRoot == nil {
			global = getGlobalNpmRoot()
		} else {
			global = *opts.globalNpmRoot
		}
		if global != "" {
			for _, r := range collectPackageRootsFromNodeModules(global, &watch) {
				roots = append(roots, rootScope{r, pkgScopeUser})
			}
		}
	}
	seenRoots := map[string]scopeSet{}
	seenAgents := map[string]*packageSubagentPath{}
	seenChains := map[string]*packageChainPath{}
	res := packageSubagentPaths{settingsErrors: settingsErrors}
	for _, rs := range roots {
		resolved, _ := filepath.Abs(rs.root)
		if scopes, ok := seenRoots[resolved]; ok {
			scopes[rs.scope] = true
			continue
		}
		scopes := scopeSet{rs.scope: true}
		seenRoots[resolved] = scopes
		paths := extractSubagentPathsFromPackageRoot(resolved, scopes)
		watch = append(watch, paths.watchPaths...)
		for _, ap := range paths.agents {
			key := ap.dir + "\x00" + ap.packageRoot
			if ex, ok := seenAgents[key]; ok {
				for s := range ap.scope {
					ex.scope[s] = true
				}
				continue
			}
			seenAgents[key] = ap
			res.agents = append(res.agents, ap)
		}
		for _, cp := range paths.chains {
			key := cp.dir + "\x00" + cp.packageRoot
			if ex, ok := seenChains[key]; ok {
				for s := range cp.scope {
					ex.scope[s] = true
				}
				continue
			}
			seenChains[key] = cp
			res.chains = append(res.chains, cp)
		}
	}
	res.watchPaths = watch
	return res
}

func packageEntryIncluded(scope AgentScope, scopes scopeSet) bool {
	if scope == ScopeBoth || scopes[pkgScopeRoot] {
		return true
	}
	return scopes[packageScope(scope)]
}

func sortedScopeNames(s scopeSet) []string {
	var out []string
	for k := range s {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}
