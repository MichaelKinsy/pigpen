package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Agent discovery (src/agents/agents.ts:1798-2991): definition directories, the source snapshot cache, settings projection.

// DirectoryReport is a definition directory inspected during one discovery (agents.ts AgentDefinitionDirectoryReport).
type DirectoryReport struct {
	Source         AgentSource
	Path           string
	State          string // "absent", "empty", "candidates", "unreadable" or "not-directory"
	CandidateCount int
}

// agentInspection is agents.ts AgentDefinitionInspection with the directories it visited.
type agentInspection struct {
	files       []string
	state       string
	directories []string
}

func shouldPruneDiscoveryDir(root, dir, name string) bool {
	return shouldPrune(root, dir, name)
}

// inspectAgentDefinitionDirectory is agents.ts:1895: a nested unreadable directory makes the whole inspection unavailable.
func inspectAgentDefinitionDirectory(dir string, isExcluded func(string) bool) agentInspection {
	root, _ := filepath.Abs(dir)
	if isExcluded != nil && isExcluded(root) {
		return agentInspection{state: "empty"}
	}
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return agentInspection{state: "absent", directories: []string{root}}
		}
		return agentInspection{state: "unreadable", directories: []string{root}}
	}
	if !st.IsDir() {
		return agentInspection{state: "not-directory", directories: []string{root}}
	}
	var files []string
	unreadable := false
	visited := map[string]bool{}
	var inspected []string
	var visit func(cur string)
	visit = func(cur string) {
		abs, _ := filepath.Abs(cur)
		inspected = append(inspected, abs)
		real, err := filepath.EvalSymlinks(cur)
		if err != nil {
			unreadable = true
			return
		}
		if visited[real] {
			return
		}
		visited[real] = true
		entries, err := os.ReadDir(cur)
		if err != nil {
			unreadable = true
			return
		}
		sort.Slice(entries, func(i, j int) bool { return localeLess(entries[i].Name(), entries[j].Name()) })
		for _, e := range entries {
			p := filepath.Join(cur, e.Name())
			if isExcluded != nil && isExcluded(p) {
				continue
			}
			isD := e.IsDir()
			isFile := e.Type().IsRegular()
			isLink := e.Type()&os.ModeSymlink != 0
			if isLink {
				s, err := os.Stat(p)
				isD = err == nil && s.IsDir()
			}
			if isD {
				if !shouldPruneDiscoveryDir(root, p, e.Name()) {
					visit(p)
				}
				continue
			}
			if (isFile || isLink) && strings.HasSuffix(e.Name(), ".md") && !strings.HasSuffix(e.Name(), ".chain.md") && !isLegacySkillPath(root, p) {
				files = append(files, p)
			}
		}
	}
	visit(root)
	state := "empty"
	if unreadable {
		state = "unreadable"
	} else if len(files) > 0 {
		state = "candidates"
	}
	return agentInspection{files: files, state: state, directories: uniqueStrings(inspected)}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func readAgentDefinitionFiles(dir string, insp agentInspection) []definitionFile {
	var files []definitionFile
	for _, p := range insp.files {
		if isLegacySkillPath(dir, p) {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			files = append(files, definitionFile{p, string(b)})
		}
	}
	return files
}

func reportAgentDefinitionDirectory(source AgentSource, dir string, insp agentInspection) DirectoryReport {
	abs, _ := filepath.Abs(dir)
	r := DirectoryReport{Source: source, Path: abs, State: insp.state}
	if insp.state == "candidates" {
		r.CandidateCount = len(insp.files)
	}
	return r
}

// chainDirLoad is agents.ts loadChainsFromDir's result.
type chainDirLoad struct {
	chains      []ChainConfig
	diagnostics []Diagnostic
	files       []string
	directories []string
}

// loadChainsFromDir is agents.ts:2306.
func loadChainsFromDir(dir string, source AgentSource) chainDirLoad {
	res := chainDirLoad{}
	var order []string
	byName := map[string]ChainConfig{}
	files := listFilesRecursiveInspected(dir, func(n string) bool { return strings.HasSuffix(n, ".chain.md") || strings.HasSuffix(n, ".chain.json") }, &res.directories)
	res.files = files
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c *ChainConfig
		if strings.HasSuffix(p, ".chain.json") {
			c, err = parseJSONChain(string(b), source, p)
		} else {
			c, err = ParseChain(string(b), source, p)
		}
		if err != nil {
			res.diagnostics = append(res.diagnostics, Diagnostic{Source: source, FilePath: p, Error: err.Error()})
			continue
		}
		if ex, ok := byName[c.Name]; ok && strings.HasSuffix(ex.FilePath, ".chain.json") && strings.HasSuffix(p, ".chain.md") {
			continue
		}
		if _, ok := byName[c.Name]; !ok {
			order = append(order, c.Name)
		}
		byName[c.Name] = *c
	}
	for _, n := range order {
		res.chains = append(res.chains, byName[n])
	}
	return res
}

// listFilesRecursiveInspected is agents.ts listFilesRecursive: files under dir with keep(name), recording the directories it visits.
func listFilesRecursiveInspected(dir string, keep func(string) bool, inspected *[]string) []string {
	var out []string
	visited := map[string]bool{}
	var walk func(cur string)
	walk = func(cur string) {
		if _, err := os.Stat(cur); err != nil {
			return
		}
		abs, _ := filepath.Abs(cur)
		if inspected != nil {
			*inspected = append(*inspected, abs)
		}
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
			isD, isFile := e.IsDir(), e.Type().IsRegular()
			if e.Type()&os.ModeSymlink != 0 {
				st, err := os.Stat(p)
				isD = err == nil && st.IsDir()
			}
			if isD {
				if !shouldPruneDiscoveryDir(dir, p, e.Name()) {
					walk(p)
				}
				continue
			}
			if !isFile && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if keep(e.Name()) {
				out = append(out, p)
			}
		}
	}
	walk(dir)
	if inspected != nil {
		*inspected = uniqueStrings(*inspected)
	}
	return out
}

// ---- scan and exclude directories ----

type exclusionRoot struct{ resolved, real string }

func readConfiguredAgentScanDirs(filePath, key string) []string {
	if filePath == "" {
		return nil
	}
	settings, err := readSettingsFileStrict(filePath)
	if err != nil {
		return nil
	}
	sub := settings.obj("subagents")
	if sub == nil {
		return nil
	}
	arr, _ := sub.vals[key].([]any)
	var out []string
	for _, e := range arr {
		if s, ok := e.(string); ok && jsTrim(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func osSep(p string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range p {
		if r == '/' || r == '\\' {
			if !prevSep {
				b.WriteRune(filepath.Separator)
			}
			prevSep = true
			continue
		}
		prevSep = false
		b.WriteRune(r)
	}
	return b.String()
}

func agentExclusionRoots(userSettingsPath, projectSettingsPath string) []exclusionRoot {
	var roots []exclusionRoot
	for _, sp := range []string{userSettingsPath, projectSettingsPath} {
		if sp == "" {
			continue
		}
		for _, entry := range readConfiguredAgentScanDirs(sp, "agentExcludeDirs") {
			resolved, _ := filepath.Abs(filepath.Join(filepath.Dir(sp), osSep(expandHomePath(jsTrim(entry)))))
			if filepath.IsAbs(osSep(expandHomePath(jsTrim(entry)))) {
				resolved, _ = filepath.Abs(osSep(expandHomePath(jsTrim(entry))))
			}
			roots = append(roots, exclusionRoot{resolved, canonicalAgentPath(resolved)})
		}
	}
	return roots
}

func agentExclusions(roots []exclusionRoot) func(string) bool {
	return func(p string) bool {
		if len(roots) == 0 {
			return false
		}
		for _, r := range roots {
			if isPathWithin(r.resolved, p) {
				return true
			}
		}
		real := canonicalAgentPath(p)
		for _, r := range roots {
			if isPathWithin(r.real, real) {
				return true
			}
		}
		return false
	}
}

type agentScanDirs struct{ dirs, watchPaths []string }

func expandAgentScanDirPattern(pattern string, isExcluded func(string) bool) agentScanDirs {
	expanded := osSep(expandHomePath(jsTrim(pattern)))
	if expanded == "" {
		return agentScanDirs{}
	}
	stars := strings.Count(expanded, "*")
	if stars == 0 {
		dir, _ := filepath.Abs(expanded)
		var dirs []string
		if _, err := os.Stat(dir); err == nil && !isExcluded(dir) {
			dirs = []string{dir}
		}
		return agentScanDirs{dirs, []string{dir}}
	}
	parts := strings.Split(expanded, string(filepath.Separator))
	idx := -1
	for i, p := range parts {
		if strings.Contains(p, "*") {
			idx = i
			break
		}
	}
	if stars != 1 || idx == -1 || parts[idx] != "*" {
		return agentScanDirs{}
	}
	baseJoined := strings.Join(parts[:idx], string(filepath.Separator))
	if baseJoined == "" {
		baseJoined = string(filepath.Separator)
	}
	base, _ := filepath.Abs(baseJoined)
	if isExcluded(base) {
		return agentScanDirs{nil, []string{base}}
	}
	rest := parts[idx+1:]
	entries, err := os.ReadDir(base)
	if err != nil {
		return agentScanDirs{nil, []string{base}}
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			candidates = append(candidates, filepath.Join(append([]string{base, e.Name()}, rest...)...))
		}
	}
	out := agentScanDirs{watchPaths: append([]string{base}, candidates...)}
	for _, d := range candidates {
		if _, err := os.Stat(d); err == nil && !isExcluded(d) {
			out.dirs = append(out.dirs, d)
		}
	}
	return out
}

func settingsAgentScanDirs(entries []string, isExcluded func(string) bool) agentScanDirs {
	var out agentScanDirs
	seenD, seenW := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		x := expandAgentScanDirPattern(e, isExcluded)
		for _, d := range x.dirs {
			if !seenD[d] {
				seenD[d] = true
				out.dirs = append(out.dirs, d)
			}
		}
		for _, w := range x.watchPaths {
			if !seenW[w] {
				seenW[w] = true
				out.watchPaths = append(out.watchPaths, w)
			}
		}
	}
	return out
}

// ---- project directories ----

func resolveNearestProjectAgentDirs(cwd string) (readDirs, candidateDirs []string, preferredDir string) {
	root := FindConfiguredProjectRoot(cwd)
	if root == "" {
		return nil, nil, ""
	}
	legacy := filepath.Join(root, ".agents")
	preferred := filepath.Join(getProjectConfigDir(root), "agents")
	candidateDirs = []string{legacy, preferred}
	if isDir(legacy) {
		readDirs = append(readDirs, legacy)
	}
	if isDir(preferred) {
		readDirs = append(readDirs, preferred)
	}
	return readDirs, candidateDirs, preferred
}

func resolveNearestProjectChainDirs(cwd string) (readDirs []string, preferredDir string) {
	root := FindConfiguredProjectRoot(cwd)
	if root == "" {
		return nil, ""
	}
	preferred := filepath.Join(getProjectConfigDir(root), "chains")
	if isDir(preferred) {
		readDirs = []string{preferred}
	}
	return readDirs, preferred
}

// ---- sources ----

type loadedAgentDirectory struct {
	dir          string
	inspection   agentInspection
	agents       []AgentConfig
	diagnostics  []Diagnostic
	packageEntry *packageSubagentPath
}

type discoverySources struct {
	cwd                                           string
	preferredModelProvider                        string
	userDir, userDirOld, userDirNew, userChainDir string
	projectAgentDirs, projectCandidateDirs        []string
	projectAgentsDir                              string
	projectChainDirs                              []string
	projectChainDir                               string
	userSettingsPath, projectSettingsPath         string
	userSettings, projectSettings                 *subagentSettings
	packagePaths                                  packageSubagentPaths
	builtinAgents                                 []AgentConfig
	builtinDiagnostics                            []Diagnostic
	userLoaded, projectLoaded, packageLoaded      []loadedAgentDirectory
	projectInspections                            map[string]agentInspection
	userChains                                    *chainDirLoad
	projectChainLoaded                            []chainDirLoad
	projectChainDirsLoaded                        []string
	packageChainLoaded                            []chainDirLoad
	isExcluded                                    func(string) bool
	exclusionRoots                                []exclusionRoot
	identityWatchPaths                            []string
	chainWatchPaths                               map[string]bool
	hasChainWatch                                 bool
	watchPaths                                    []string
}

type cacheEntry struct {
	sources     *discoverySources
	fingerprint string
}

var (
	discoveryCacheMu sync.Mutex
	discoveryCache   = map[string]*cacheEntry{}
)

// clearAgentDiscoveryCache drops every process-local discovery snapshot (agents.ts:2756).
func clearAgentDiscoveryCache() {
	discoveryCacheMu.Lock()
	discoveryCache = map[string]*cacheEntry{}
	discoveryCacheMu.Unlock()
}

func addDirectoryWatchPaths(paths map[string]bool, root string, files, directories []string) {
	r, _ := filepath.Abs(root)
	paths[r] = true
	for _, d := range directories {
		if isPathWithin(r, d) {
			abs, _ := filepath.Abs(d)
			paths[abs] = true
		}
	}
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		paths[abs] = true
		cur, _ := filepath.Abs(filepath.Dir(f))
		for isPathWithin(r, cur) {
			paths[cur] = true
			if cur == r {
				break
			}
			cur = filepath.Dir(cur)
		}
	}
}

func watchPathSignature(p string, isExcluded func(string) bool) string {
	st, err := os.Stat(p)
	if err != nil {
		return "missing"
	}
	if st.IsDir() {
		entries, err := os.ReadDir(p)
		if err != nil {
			return "missing"
		}
		sort.Slice(entries, func(i, j int) bool { return localeLess(entries[i].Name(), entries[j].Name()) })
		var parts []string
		for _, e := range entries {
			if isExcluded != nil && isExcluded(filepath.Join(p, e.Name())) {
				continue
			}
			kind := "f"
			if e.IsDir() {
				kind = "d"
			} else if e.Type()&os.ModeSymlink != 0 {
				kind = "l"
				if isExcluded != nil {
					kind = "l:" + canonicalAgentPath(filepath.Join(p, e.Name()))
				}
			}
			parts = append(parts, e.Name()+":"+kind)
		}
		return "directory:" + strings.Join(parts, "|")
	}
	return fmt.Sprintf("file:%d:%d", st.Size(), st.ModTime().UnixNano())
}

func exclusionIdentityJSON(roots []exclusionRoot, recompute bool) string {
	type item struct {
		Resolved string `json:"resolved"`
		Real     string `json:"real"`
	}
	items := make([]item, len(roots))
	for i, r := range roots {
		real := r.real
		if recompute {
			real = canonicalAgentPath(r.resolved)
		}
		items[i] = item{r.resolved, real}
	}
	b, _ := json.Marshal(items)
	return string(b)
}

// discoveryFingerprint is agents.ts:2593.
func discoveryFingerprint(s *discoverySources) string {
	identity := exclusionIdentityJSON(s.exclusionRoots, true)
	if identity != exclusionIdentityJSON(s.exclusionRoots, false) {
		return identity
	}
	var ids [][2]string
	for _, r := range s.identityWatchPaths {
		ids = append(ids, [2]string{r, canonicalAgentPath(r)})
	}
	idb, _ := json.Marshal(ids)
	unfiltered := map[string]bool{}
	for _, p := range s.packagePaths.watchPaths {
		unfiltered[p] = true
	}
	for p := range s.chainWatchPaths {
		unfiltered[p] = true
	}
	all := map[string]bool{}
	for _, p := range s.watchPaths {
		all[p] = true
	}
	for p := range unfiltered {
		all[p] = true
	}
	var list []string
	for p := range all {
		if unfiltered[p] || p == s.userSettingsPath || p == s.projectSettingsPath || !s.isExcluded(p) {
			list = append(list, p)
		}
	}
	sort.Slice(list, func(i, j int) bool { return localeLess(list[i], list[j]) })
	lines := make([]string, len(list))
	for i, p := range list {
		var ex func(string) bool
		if len(s.exclusionRoots) > 0 && !unfiltered[p] {
			ex = s.isExcluded
		}
		lines[i] = p + ":" + watchPathSignature(p, ex)
	}
	return identity + "\n" + string(idb) + "\n" + strings.Join(lines, "\n")
}

func discoveryCacheKey(cwd, provider string, globalNpmRoot *string) string {
	abs, _ := filepath.Abs(cwd)
	g := []string{"default"}
	if globalNpmRoot != nil {
		g = []string{"override", *globalNpmRoot}
	}
	b, _ := json.Marshal([]any{abs, provider, g, getProjectConfigDir(abs), agentDir(), homeDir(), os.Getenv("HOME"), os.Getenv("USERPROFILE"),
		os.Getenv("PI_SUBAGENT_EXTRA_AGENT_DIRS"), os.Getenv("PI_OFFLINE"), os.Getenv("APPDATA")})
	return string(b)
}

func projectDiscoveryWatchPaths(cwd string) []string {
	var paths []string
	cur, _ := filepath.Abs(cwd)
	for {
		paths = append(paths, getProjectConfigDir(cur), filepath.Join(cur, ".agents"), filepath.Join(cur, ".git"))
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return paths
}

func loadAgentsFromDir(dir string, source AgentSource, priority int, pkg *packageSource, insp agentInspection) ([]AgentConfig, []Diagnostic) {
	return loadAgentsFromDefinitionFiles(readAgentDefinitionFiles(dir, insp), source, &priority, pkg)
}

func (e *packageSubagentPath) source() *packageSource {
	return &packageSource{e.packageName, e.packageVersion, e.packageRoot}
}

// buildAgentDiscoverySources is agents.ts:2640.
func buildAgentDiscoverySources(cwd, provider string, globalNpmRoot *string) *discoverySources {
	effectiveCwd, _ := filepath.Abs(cwd)
	s := &discoverySources{cwd: effectiveCwd, preferredModelProvider: provider}
	s.userDirOld = filepath.Join(agentDir(), "agents")
	s.userDirNew = filepath.Join(homeDir(), ".agents")
	s.userChainDir = filepath.Join(agentDir(), "chains")
	s.projectAgentDirs, s.projectCandidateDirs, s.projectAgentsDir = resolveNearestProjectAgentDirs(effectiveCwd)
	s.projectChainDirs, s.projectChainDir = resolveNearestProjectChainDirs(effectiveCwd)
	s.userSettingsPath = userAgentSettingsPath()
	s.projectSettingsPath = projectAgentSettingsPath(effectiveCwd)
	s.packagePaths = collectPackageSubagentPaths(effectiveCwd, packageDiscoveryOptions{includeUser: true, includeProject: true, globalNpmRoot: globalNpmRoot})
	s.exclusionRoots = agentExclusionRoots(s.userSettingsPath, s.projectSettingsPath)
	s.isExcluded = agentExclusions(s.exclusionRoots)
	userScan := settingsAgentScanDirs(readConfiguredAgentScanDirs(s.userSettingsPath, "agentScanDirs"), s.isExcluded)
	projectScan := settingsAgentScanDirs(readConfiguredAgentScanDirs(s.projectSettingsPath, "agentScanDirs"), s.isExcluded)

	s.builtinAgents, s.builtinDiagnostics = builtinAgents()

	var userDirs []string
	for _, d := range append(append(extraUserAgentDirs(), userScan.dirs...), s.userDirOld, s.userDirNew) {
		if !s.isExcluded(d) {
			userDirs = append(userDirs, d)
		}
	}
	for i, dir := range userDirs {
		insp := inspectAgentDefinitionDirectory(dir, s.isExcluded)
		a, d := loadAgentsFromDir(dir, SourceUser, i, nil, insp)
		s.userLoaded = append(s.userLoaded, loadedAgentDirectory{dir: dir, inspection: insp, agents: a, diagnostics: d})
	}
	s.projectInspections = map[string]agentInspection{}
	for _, d := range s.projectCandidateDirs {
		if !s.isExcluded(d) {
			s.projectInspections[d] = inspectAgentDefinitionDirectory(d, s.isExcluded)
		}
	}
	var projectDirs []string
	for _, d := range append(append([]string{}, projectScan.dirs...), s.projectAgentDirs...) {
		if !s.isExcluded(d) {
			projectDirs = append(projectDirs, d)
		}
	}
	for i, dir := range projectDirs {
		insp, ok := s.projectInspections[dir]
		if !ok {
			insp = inspectAgentDefinitionDirectory(dir, s.isExcluded)
		}
		prio := i
		if dir == s.projectAgentsDir {
			prio = 1
		}
		a, d := loadAgentsFromDir(dir, SourceProject, prio, nil, insp)
		s.projectLoaded = append(s.projectLoaded, loadedAgentDirectory{dir: dir, inspection: insp, agents: a, diagnostics: d})
	}
	n := len(s.packagePaths.agents)
	idx := 0
	for _, entry := range s.packagePaths.agents {
		if s.isExcluded(entry.dir) {
			continue
		}
		insp := inspectAgentDefinitionDirectory(entry.dir, s.isExcluded)
		// the original numbers the filtered entries by their position among the filtered ones
		a, d := loadAgentsFromDir(entry.dir, SourcePackage, n-idx, entry.source(), insp)
		idx++
		s.packageLoaded = append(s.packageLoaded, loadedAgentDirectory{dir: entry.dir, inspection: insp, agents: a, diagnostics: d, packageEntry: entry})
	}

	watch := map[string]bool{s.userSettingsPath: true}
	if s.projectSettingsPath != "" {
		watch[s.projectSettingsPath] = true
	}
	for _, p := range projectDiscoveryWatchPaths(effectiveCwd) {
		watch[p] = true
	}
	for _, r := range findProjectRootCandidates(effectiveCwd) {
		watch[filepath.Join(getProjectConfigDir(r), "settings.json")] = true
	}
	for _, p := range userScan.watchPaths {
		watch[p] = true
	}
	for _, p := range projectScan.watchPaths {
		watch[p] = true
	}
	for _, p := range s.packagePaths.watchPaths {
		watch[p] = true
	}
	for _, l := range s.userLoaded {
		addDirectoryWatchPaths(watch, l.dir, l.inspection.files, l.inspection.directories)
	}
	for _, d := range s.projectCandidateDirs {
		if insp, ok := s.projectInspections[d]; ok {
			addDirectoryWatchPaths(watch, d, insp.files, insp.directories)
		} else {
			addDirectoryWatchPaths(watch, d, nil, nil)
		}
	}
	for _, l := range s.projectLoaded {
		addDirectoryWatchPaths(watch, l.dir, l.inspection.files, l.inspection.directories)
	}
	for _, l := range s.packageLoaded {
		addDirectoryWatchPaths(watch, l.dir, l.inspection.files, l.inspection.directories)
	}
	s.userDir = s.userDirOld
	if os.Getenv("PI_CODING_AGENT_DIR") == "" {
		if _, err := os.Stat(s.userDirNew); err == nil {
			s.userDir = s.userDirNew
		}
	}
	var kept []string
	for _, c := range s.projectCandidateDirs {
		if !s.isExcluded(c) {
			kept = append(kept, c)
		}
	}
	s.projectCandidateDirs = kept
	identity := map[string]bool{}
	var identityList []string
	add := func(p string) {
		if !identity[p] {
			identity[p] = true
			identityList = append(identityList, p)
		}
	}
	for _, p := range userScan.watchPaths {
		add(p)
	}
	for _, p := range projectScan.watchPaths {
		add(p)
	}
	for _, p := range extraUserAgentDirs() {
		add(p)
	}
	add(s.userDirOld)
	add(s.userDirNew)
	for _, p := range s.projectCandidateDirs {
		add(p)
	}
	for _, e := range s.packagePaths.agents {
		add(e.dir)
	}
	for _, p := range identityList {
		underExcluded := false
		for _, r := range s.exclusionRoots {
			underExcluded = underExcluded || isPathWithin(r.resolved, p)
		}
		if !underExcluded && s.isExcluded(p) {
			s.identityWatchPaths = append(s.identityWatchPaths, p)
		}
	}
	for p := range watch {
		if !s.isExcluded(p) || p == s.userSettingsPath || p == s.projectSettingsPath {
			s.watchPaths = append(s.watchPaths, p)
		}
	}
	return s
}

// ensureDiscoveryChains is agents.ts:2722.
func ensureDiscoveryChains(s *discoverySources) {
	if s.userChains != nil {
		return
	}
	s.packageChainLoaded = nil
	for _, e := range s.packagePaths.chains {
		s.packageChainLoaded = append(s.packageChainLoaded, loadChainsFromDir(e.dir, SourcePackage))
	}
	uc := loadChainsFromDir(s.userChainDir, SourceUser)
	s.userChains = &uc
	s.projectChainLoaded = nil
	s.projectChainDirsLoaded = nil
	for _, d := range s.projectChainDirs {
		s.projectChainLoaded = append(s.projectChainLoaded, loadChainsFromDir(d, SourceProject))
		s.projectChainDirsLoaded = append(s.projectChainDirsLoaded, d)
	}
	watch := map[string]bool{}
	for _, p := range s.packagePaths.watchPaths {
		watch[p] = true
	}
	addDirectoryWatchPaths(watch, s.userChainDir, uc.files, uc.directories)
	for i, c := range s.projectChainLoaded {
		addDirectoryWatchPaths(watch, s.projectChainDirsLoaded[i], c.files, c.directories)
	}
	for i, c := range s.packageChainLoaded {
		addDirectoryWatchPaths(watch, s.packagePaths.chains[i].dir, c.files, c.directories)
	}
	s.chainWatchPaths = watch
	have := map[string]bool{}
	for _, p := range s.watchPaths {
		have[p] = true
	}
	for p := range watch {
		if !have[p] {
			s.watchPaths = append(s.watchPaths, p)
		}
	}
}

func getAgentDiscoverySources(cwd, provider string, includeChains bool, globalNpmRoot *string) *discoverySources {
	key := discoveryCacheKey(cwd, provider, globalNpmRoot)
	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()
	if c, ok := discoveryCache[key]; ok && c.fingerprint == discoveryFingerprint(c.sources) {
		if includeChains {
			ensureDiscoveryChains(c.sources)
			c.fingerprint = discoveryFingerprint(c.sources)
		}
		return c.sources
	}
	s := buildAgentDiscoverySources(cwd, provider, globalNpmRoot)
	e := &cacheEntry{sources: s, fingerprint: discoveryFingerprint(s)}
	discoveryCache[key] = e
	if includeChains {
		ensureDiscoveryChains(s)
		e.fingerprint = discoveryFingerprint(s)
	}
	return s
}

// settingsForScope is agents.ts:2771: only the settings that take part in this projection are parsed.
func settingsForScope(s *discoverySources, scope AgentScope) (user, project subagentSettings, err error) {
	if scope != ScopeProject && s.userSettings == nil {
		if e := s.packagePaths.settingsErrors["user"]; e != nil {
			return user, project, e
		}
		st, e := readSubagentSettings(s.userSettingsPath)
		if e != nil {
			return user, project, e
		}
		st = selectProviderOverrides(st, s.preferredModelProvider)
		s.userSettings = &st
	}
	if scope != ScopeUser && s.projectSettings == nil {
		if e := s.packagePaths.settingsErrors["project"]; e != nil {
			return user, project, e
		}
		st, e := readSubagentSettings(s.projectSettingsPath)
		if e != nil {
			return user, project, e
		}
		st = selectProviderOverrides(st, s.preferredModelProvider)
		s.projectSettings = &st
	}
	user, project = emptySubagentSettings(), emptySubagentSettings()
	if scope != ScopeProject && s.userSettings != nil {
		user = *s.userSettings
	}
	if scope != ScopeUser && s.projectSettings != nil {
		project = *s.projectSettings
	}
	return user, project, nil
}

type configuredAgents struct {
	builtin, pkg, user, project []AgentConfig
	maxThinking                 string
	modelScope                  *ModelScopeConfig
}

func defaultsFor(user, project subagentSettings, userPath, projectPath string) (subagentDefaults, string) {
	provider := resolveSubagentDefaultProvider(user, project, projectPath)
	d := subagentDefaults{
		model:                  resolveSubagentDefaultModel(user, project, userPath, projectPath, provider),
		provider:               provider,
		thinking:               resolveSubagentDefaultThinking(user, project, projectPath),
		extensions:             resolveDefaultList(user.defaultExtensions, project.defaultExtensions, project.defaultExtensions != nil, projectPath),
		subagentOnlyExtensions: resolveDefaultList(user.defaultSubagentOnlyExtensions, project.defaultSubagentOnlyExtensions, project.defaultSubagentOnlyExtensions != nil, projectPath),
	}
	return d, resolveSubagentMaxThinking(user, project, projectPath)
}

// configuredAgentsForScope is agents.ts:2782.
func configuredAgentsForScope(s *discoverySources, scope, settingsScope AgentScope) (configuredAgents, error) {
	user, project, err := settingsForScope(s, settingsScope)
	if err != nil {
		return configuredAgents{}, err
	}
	d, maxThinking := defaultsFor(user, project, s.userSettingsPath, s.projectSettingsPath)
	var c configuredAgents
	c.maxThinking = maxThinking
	c.builtin = applyBuiltinOverrides(d.apply(s.builtinAgents), user, project, s.userSettingsPath, s.projectSettingsPath)
	var userAgents []AgentConfig
	if scope != ScopeProject {
		for _, l := range s.userLoaded {
			userAgents = append(userAgents, l.agents...)
		}
	}
	c.user = applyCustomAgentOverrides(d.apply(userAgents), user, project, s.userSettingsPath, s.projectSettingsPath)
	projectMap, projectOrder := map[string]AgentConfig{}, []string{}
	if scope != ScopeUser {
		for _, l := range s.projectLoaded {
			for _, a := range l.agents {
				if _, ok := projectMap[a.Name]; !ok {
					projectOrder = append(projectOrder, a.Name)
				}
				projectMap[a.Name] = a
			}
		}
	}
	var projectAgents []AgentConfig
	for _, n := range projectOrder {
		projectAgents = append(projectAgents, projectMap[n])
	}
	c.project = applyCustomAgentOverrides(d.apply(projectAgents), user, project, s.userSettingsPath, s.projectSettingsPath)
	pkgMap, pkgOrder := map[string]AgentConfig{}, []string{}
	for _, l := range s.packageLoaded {
		if l.packageEntry == nil || !packageEntryIncluded(scope, l.packageEntry.scope) {
			continue
		}
		for _, a := range l.agents {
			if _, ok := pkgMap[a.Name]; !ok {
				pkgOrder = append(pkgOrder, a.Name)
				pkgMap[a.Name] = a
			}
		}
	}
	var pkgAgents []AgentConfig
	for _, n := range pkgOrder {
		pkgAgents = append(pkgAgents, pkgMap[n])
	}
	c.pkg = applyCustomAgentOverrides(d.apply(pkgAgents), user, project, s.userSettingsPath, s.projectSettingsPath)
	c.modelScope = project.modelScope
	if c.modelScope == nil {
		c.modelScope = user.modelScope
	}
	return c, nil
}

func discoveryDirectories(s *discoverySources, scope AgentScope) []DirectoryReport {
	dirs := []DirectoryReport{{Source: SourceBuiltin, Path: "builtin", State: "candidates", CandidateCount: len(s.builtinAgents) + len(s.builtinDiagnostics)}}
	if scope != ScopeProject {
		for _, d := range s.userLoaded {
			dirs = append(dirs, reportAgentDefinitionDirectory(SourceUser, d.dir, d.inspection))
		}
	}
	if scope != ScopeUser {
		reported := map[string]bool{}
		for _, d := range s.projectCandidateDirs {
			abs, _ := filepath.Abs(d)
			reported[abs] = true
			dirs = append(dirs, reportAgentDefinitionDirectory(SourceProject, d, s.projectInspections[d]))
		}
		for _, d := range s.projectLoaded {
			abs, _ := filepath.Abs(d.dir)
			if !reported[abs] {
				dirs = append(dirs, reportAgentDefinitionDirectory(SourceProject, d.dir, d.inspection))
			}
		}
	}
	for _, d := range s.packageLoaded {
		if d.packageEntry != nil && packageEntryIncluded(scope, d.packageEntry.scope) {
			dirs = append(dirs, reportAgentDefinitionDirectory(SourcePackage, d.dir, d.inspection))
		}
	}
	return dirs
}

func discoveryDiagnostics(s *discoverySources, scope AgentScope) []Diagnostic {
	out := append([]Diagnostic{}, s.builtinDiagnostics...)
	if scope != ScopeProject {
		for _, l := range s.userLoaded {
			out = append(out, l.diagnostics...)
		}
	}
	if scope != ScopeUser {
		for _, l := range s.projectLoaded {
			out = append(out, l.diagnostics...)
		}
	}
	for _, l := range s.packageLoaded {
		if l.packageEntry != nil && packageEntryIncluded(scope, l.packageEntry.scope) {
			out = append(out, l.diagnostics...)
		}
	}
	return out
}

func notDisabled(agents []AgentConfig) []AgentConfig {
	var out []AgentConfig
	for _, a := range agents {
		if a.Disabled == nil || !*a.Disabled {
			out = append(out, a)
		}
	}
	return out
}

func (s *discoverySources) projectAgentsDirOrEmpty() string { return s.projectAgentsDir }

func buildEffectiveDiscovery(s *discoverySources, scope AgentScope) (Discovery, error) {
	c, err := configuredAgentsForScope(s, scope, scope)
	if err != nil {
		return Discovery{}, err
	}
	agents := applySubagentMaxThinking(notDisabled(MergeAgentsForScope(scope, c.user, c.project, c.builtin, c.pkg)), c.maxThinking)
	return Discovery{Agents: agents, Diagnostics: discoveryDiagnostics(s, scope), ProjectAgentsDir: s.projectAgentsDir, UserDir: s.userDir,
		Cwd: s.cwd, Scope: scope, Directories: discoveryDirectories(s, scope), ModelScope: c.modelScope, MaxThinking: c.maxThinking}, nil
}

func buildAllDiscovery(s *discoverySources, includeChains bool, settingsScope AgentScope) (DiscoveryAll, error) {
	if includeChains {
		ensureDiscoveryChains(s)
	}
	c, err := configuredAgentsForScope(s, ScopeBoth, settingsScope)
	if err != nil {
		return DiscoveryAll{}, err
	}
	var chains []ChainConfig
	var chainDiags []Diagnostic
	pkgMap, pkgOrder := map[string]ChainConfig{}, []string{}
	var pkgDiags []Diagnostic
	for _, l := range s.packageChainLoaded {
		pkgDiags = append(pkgDiags, l.diagnostics...)
		for _, ch := range l.chains {
			if _, ok := pkgMap[ch.Name]; !ok {
				pkgMap[ch.Name] = ch
				pkgOrder = append(pkgOrder, ch.Name)
			}
		}
	}
	projMap, projOrder := map[string]ChainConfig{}, []string{}
	var projDiags []Diagnostic
	for _, l := range s.projectChainLoaded {
		projDiags = append(projDiags, l.diagnostics...)
		for _, ch := range l.chains {
			if _, ok := projMap[ch.Name]; !ok {
				projOrder = append(projOrder, ch.Name)
			}
			projMap[ch.Name] = ch
		}
	}
	for _, n := range pkgOrder {
		chains = append(chains, pkgMap[n])
	}
	if s.userChains != nil {
		chains = append(chains, s.userChains.chains...)
	}
	for _, n := range projOrder {
		chains = append(chains, projMap[n])
	}
	chainDiags = append(chainDiags, pkgDiags...)
	if s.userChains != nil {
		chainDiags = append(chainDiags, s.userChains.diagnostics...)
	}
	chainDiags = append(chainDiags, projDiags...)
	var diags []Diagnostic
	diags = append(diags, s.builtinDiagnostics...)
	for _, l := range s.userLoaded {
		diags = append(diags, l.diagnostics...)
	}
	for _, l := range s.packageLoaded {
		diags = append(diags, l.diagnostics...)
	}
	for _, l := range s.projectLoaded {
		diags = append(diags, l.diagnostics...)
	}
	return DiscoveryAll{
		Builtin: applySubagentMaxThinking(c.builtin, c.maxThinking), Package: applySubagentMaxThinking(c.pkg, c.maxThinking),
		User: applySubagentMaxThinking(c.user, c.maxThinking), Project: applySubagentMaxThinking(c.project, c.maxThinking),
		Diagnostics: diags, Chains: chains, ChainDiagnostics: chainDiags, Cwd: s.cwd, UserDir: s.userDir, ProjectDir: s.projectAgentsDir,
		UserChainDir: s.userChainDir, ProjectChainDir: s.projectChainDir, UserSettingsPath: s.userSettingsPath,
		ProjectSettingsPath: s.projectSettingsPath, MaxThinking: c.maxThinking,
	}, nil
}

// DiscoveryOptions is agents.ts AgentDiscoveryOptions: GlobalNpmRoot nil looks the root up, a pointer to "" skips global discovery.
type DiscoveryOptions struct {
	GlobalNpmRoot *string
	// NoChains skips chain loading (agents.ts includeChains: false).
	NoChains bool
}

// discoverAgentSnapshot is agents.ts:2921.
func discoverAgentSnapshot(cwd string, scope AgentScope, provider string, opts DiscoveryOptions) (effective Discovery, all DiscoveryAll, err error) {
	s := getAgentDiscoverySources(cwd, provider, !opts.NoChains, opts.GlobalNpmRoot)
	effective, err = buildEffectiveDiscovery(s, scope)
	if err != nil {
		return
	}
	all, err = buildAllDiscovery(s, !opts.NoChains, scope)
	return
}

// discoverAgentsUncached is agents.ts:2932: a fresh scan of one non-"both" scope.
func discoverAgentsUncached(cwd string, scope AgentScope, provider string, opts DiscoveryOptions) (Discovery, error) {
	effectiveCwd, _ := filepath.Abs(cwd)
	userDirOld := filepath.Join(agentDir(), "agents")
	userDirNew := filepath.Join(homeDir(), ".agents")
	projectAgentDirs, projectCandidateDirs, projectAgentsDir := resolveNearestProjectAgentDirs(effectiveCwd)
	userSettingsPath := userAgentSettingsPath()
	projectSettingsPath := projectAgentSettingsPath(effectiveCwd)
	user, project := emptySubagentSettings(), emptySubagentSettings()
	var err error
	if scope != ScopeProject {
		if user, err = readSubagentSettings(userSettingsPath); err != nil {
			return Discovery{}, err
		}
	}
	user = selectProviderOverrides(user, provider)
	if scope != ScopeUser {
		if project, err = readSubagentSettings(projectSettingsPath); err != nil {
			return Discovery{}, err
		}
	}
	project = selectProviderOverrides(project, provider)
	d, maxThinking := defaultsFor(user, project, userSettingsPath, projectSettingsPath)
	modelScope := project.modelScope
	if modelScope == nil {
		modelScope = user.modelScope
	}
	pkgPaths := collectPackageSubagentPaths(effectiveCwd, packageDiscoveryOptions{includeUser: scope != ScopeProject, includeProject: scope != ScopeUser, globalNpmRoot: opts.GlobalNpmRoot})
	isExcluded := agentExclusions(agentExclusionRoots(userSettingsPath, projectSettingsPath))
	builtin, builtinDiags := builtinAgents()
	dirs := []DirectoryReport{{Source: SourceBuiltin, Path: "builtin", State: "candidates", CandidateCount: len(builtin) + len(builtinDiags)}}
	builtinConfigured := applyBuiltinOverrides(d.apply(builtin), user, project, userSettingsPath, projectSettingsPath)
	userScan := settingsAgentScanDirs(user.agentScanDirs, isExcluded)
	projectScan := settingsAgentScanDirs(project.agentScanDirs, isExcluded)
	var diags []Diagnostic
	diags = append(diags, builtinDiags...)
	var userAgents []AgentConfig
	var userDiags []Diagnostic
	if scope != ScopeProject {
		i := 0
		for _, dir := range append(append(extraUserAgentDirs(), userScan.dirs...), userDirOld, userDirNew) {
			if isExcluded(dir) {
				continue
			}
			insp := inspectAgentDefinitionDirectory(dir, isExcluded)
			dirs = append(dirs, reportAgentDefinitionDirectory(SourceUser, dir, insp))
			a, dg := loadAgentsFromDir(dir, SourceUser, i, nil, insp)
			i++
			userAgents = append(userAgents, a...)
			userDiags = append(userDiags, dg...)
		}
	}
	userConfigured := applyCustomAgentOverrides(d.apply(userAgents), user, project, userSettingsPath, projectSettingsPath)
	projectInspections := map[string]agentInspection{}
	var projectOrderDirs []string
	if scope != ScopeUser {
		for _, dir := range projectCandidateDirs {
			if !isExcluded(dir) {
				projectInspections[dir] = inspectAgentDefinitionDirectory(dir, isExcluded)
				projectOrderDirs = append(projectOrderDirs, dir)
			}
		}
		for _, dir := range projectOrderDirs {
			dirs = append(dirs, reportAgentDefinitionDirectory(SourceProject, dir, projectInspections[dir]))
		}
	}
	var projectAgents []AgentConfig
	var projectDiags []Diagnostic
	if scope != ScopeUser {
		i := 0
		pm, order := map[string]AgentConfig{}, []string{}
		for _, dir := range append(append([]string{}, projectScan.dirs...), projectAgentDirs...) {
			if isExcluded(dir) {
				continue
			}
			insp, ok := projectInspections[dir]
			if !ok {
				insp = inspectAgentDefinitionDirectory(dir, isExcluded)
				dirs = append(dirs, reportAgentDefinitionDirectory(SourceProject, dir, insp))
			}
			prio := i
			if dir == projectAgentsDir {
				prio = 1
			}
			i++
			a, dg := loadAgentsFromDir(dir, SourceProject, prio, nil, insp)
			projectDiags = append(projectDiags, dg...)
			for _, x := range a {
				if _, ok := pm[x.Name]; !ok {
					order = append(order, x.Name)
				}
				pm[x.Name] = x
			}
		}
		for _, n := range order {
			projectAgents = append(projectAgents, pm[n])
		}
	}
	projectConfigured := applyCustomAgentOverrides(d.apply(projectAgents), user, project, userSettingsPath, projectSettingsPath)
	pkgMap, pkgOrder := map[string]AgentConfig{}, []string{}
	var pkgDiags []Diagnostic
	idx := 0
	for _, entry := range pkgPaths.agents {
		if isExcluded(entry.dir) {
			continue
		}
		insp := inspectAgentDefinitionDirectory(entry.dir, isExcluded)
		dirs = append(dirs, reportAgentDefinitionDirectory(SourcePackage, entry.dir, insp))
		a, dg := loadAgentsFromDir(entry.dir, SourcePackage, len(pkgPaths.agents)-idx, entry.source(), insp)
		idx++
		pkgDiags = append(pkgDiags, dg...)
		for _, x := range a {
			if _, ok := pkgMap[x.Name]; !ok {
				pkgMap[x.Name] = x
				pkgOrder = append(pkgOrder, x.Name)
			}
		}
	}
	var pkgAgents []AgentConfig
	for _, n := range pkgOrder {
		pkgAgents = append(pkgAgents, pkgMap[n])
	}
	pkgConfigured := applyCustomAgentOverrides(d.apply(pkgAgents), user, project, userSettingsPath, projectSettingsPath)
	agents := applySubagentMaxThinking(notDisabled(MergeAgentsForScope(scope, userConfigured, projectConfigured, builtinConfigured, pkgConfigured)), maxThinking)
	diags = append(append(append(diags, userDiags...), projectDiags...), pkgDiags...)
	userDir := userDirOld
	if os.Getenv("PI_CODING_AGENT_DIR") == "" {
		if _, e := os.Stat(userDirNew); e == nil {
			userDir = userDirNew
		}
	}
	return Discovery{Agents: agents, Diagnostics: diags, ProjectAgentsDir: projectAgentsDir, UserDir: userDir, Cwd: effectiveCwd, Scope: scope,
		Directories: dirs, ModelScope: modelScope, MaxThinking: maxThinking}, nil
}

// discoverAgents is agents.ts:2983. A malformed settings file is an error, as the original throws.
func discoverAgents(cwd string, scope AgentScope, provider string, opts DiscoveryOptions) (Discovery, error) {
	if scope != ScopeBoth {
		return discoverAgentsUncached(cwd, scope, provider, opts)
	}
	s := getAgentDiscoverySources(cwd, provider, false, opts.GlobalNpmRoot)
	return buildEffectiveDiscovery(s, scope)
}

// discoverAgentsAll is agents.ts:2989.
func discoverAgentsAll(cwd, provider string, opts DiscoveryOptions) (DiscoveryAll, error) {
	_, all, err := discoverAgentSnapshot(cwd, ScopeBoth, provider, opts)
	return all, err
}

// applyRuntimeAgentSettings is agents.ts:1619: runtime-registered agents follow the model-tier settings of every other agent.
func applyRuntimeAgentSettings(agents []AgentConfig, ctx RuntimeAgentSettingsContext) ([]AgentConfig, error) {
	if len(agents) == 0 {
		return agents, nil
	}
	s := getAgentDiscoverySources(ctx.Cwd, ctx.PreferredModelProvider, false, nil)
	user, project, err := settingsForScope(s, ctx.Scope)
	if err != nil {
		return nil, err
	}
	provider := resolveSubagentDefaultProvider(user, project, s.projectSettingsPath)
	dm := resolveSubagentDefaultModel(user, project, s.userSettingsPath, s.projectSettingsPath, provider)
	dt := resolveSubagentDefaultThinking(user, project, s.projectSettingsPath)
	with := applySubagentDefaultThinking(applySubagentDefaultModel(agents, dm, provider), dt)
	return applyCustomAgentOverrides(with, runtimeAgentOverrides(user), runtimeAgentOverrides(project), s.userSettingsPath, s.projectSettingsPath), nil
}

// DiscoverAgentsAll finds every agent and chain definition. It keeps the pre-existing name; the error is the malformed
// settings file the original throws for.
func DiscoverAgentsAll(cwd string) DiscoveryAll {
	all, err := discoverAgentsAll(cwd, "", DiscoveryOptions{})
	all.Err = err
	return all
}

// DiscoverAgents is the effective set of agents for a scope: project over user over builtin.
func DiscoverAgents(cwd string, scope AgentScope) Discovery {
	d, err := discoverAgents(cwd, scope, "", DiscoveryOptions{})
	d.Err = err
	return d
}
