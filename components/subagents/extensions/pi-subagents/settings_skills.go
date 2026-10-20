package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Skill resolution and caching (src/agents/skills.ts).

type skillSource string

const (
	skillProject         skillSource = "project"
	skillUser            skillSource = "user"
	skillProjectPackage  skillSource = "project-package"
	skillUserPackage     skillSource = "user-package"
	skillProjectSettings skillSource = "project-settings"
	skillUserSettings    skillSource = "user-settings"
	skillExtension       skillSource = "extension"
	skillBuiltin         skillSource = "builtin"
	skillUnknown         skillSource = "unknown"
)

// ResolvedSkill is skills.ts ResolvedSkill. DisableModelInvocation is nil when unset.
type ResolvedSkill struct {
	Name, Path, Content    string
	Description            *string
	Source                 skillSource
	DisableModelInvocation *bool
}

type cachedSkillEntry struct {
	name, filePath         string
	source                 skillSource
	description            *string
	disableModelInvocation *bool
	order                  int
}

type skillSearchPath struct {
	path   string
	source skillSource
}

const (
	maxSkillCacheSize          = 50
	loadSkillsCacheTTL         = 5 * time.Second
	subagentOrchestrationSkill = "pi-subagents"
)

var skillSourcePriority = map[skillSource]int{
	skillProject: 700, skillProjectSettings: 650, skillProjectPackage: 600, skillUser: 300, skillUserSettings: 250,
	skillUserPackage: 200, skillExtension: 150, skillBuiltin: 100, skillUnknown: 0,
}

type skillCacheEntry struct {
	mtime int64
	skill ResolvedSkill
}

var (
	skillMu        sync.Mutex
	skillCache     = map[string]skillCacheEntry{}
	skillCacheKeys []string
	loadSkills     struct {
		valid     bool
		cwd, dir  string
		skills    []cachedSkillEntry
		timestamp time.Time
	}
)

// clearSkillCache is skills.ts:clearSkillCache.
func clearSkillCache() {
	skillMu.Lock()
	skillCache, skillCacheKeys = map[string]skillCacheEntry{}, nil
	loadSkills.valid = false
	skillMu.Unlock()
}

func stripSkillFrontmatter(content string) string {
	n := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(n, "---") {
		return n
	}
	i := strings.Index(n[3:], "\n---")
	if i == -1 {
		return n
	}
	return jsTrim(n[3+i+4:])
}

func isWithinPath(filePath, dir string) bool {
	rel, err := filepath.Rel(dir, filePath)
	if err != nil {
		return false
	}
	return rel == "." || rel == "" || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// readOptionalJSONFile is skills.ts:87: nil for a missing file, an error naming the file for anything else.
func readOptionalJSONFile(filePath, label string) (any, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read %s '%s': %s", label, filePath, err.Error())
	}
	v, msg := jsJSONParse(string(b))
	if msg != "" {
		return nil, fmt.Errorf("Failed to read %s '%s': %s", label, filePath, msg)
	}
	return v, nil
}

func extractSkillPathsFromPackageRoot(root string, source skillSource, bestEffort bool) ([]skillSearchPath, error) {
	pj := filepath.Join(root, "package.json")
	var pkg any
	if bestEffort {
		pkg = readJSONFileBestEffort(pj)
	} else {
		var err error
		if pkg, err = readOptionalJSONFile(pj, "package manifest"); err != nil {
			return nil, err
		}
	}
	o, _ := pkg.(*jsObject)
	if o == nil {
		return nil, nil
	}
	pi := o.obj("pi")
	if pi == nil {
		return nil, nil
	}
	skills, _ := pi.vals["skills"].([]any)
	var out []skillSearchPath
	for _, e := range skills {
		if s, ok := e.(string); ok {
			abs, _ := filepath.Abs(filepath.Join(root, s))
			out = append(out, skillSearchPath{abs, source})
		}
	}
	return out, nil
}

func collectInstalledPackageSkillPaths(cwd, agentDirectory string) []skillSearchPath {
	pcd := getProjectConfigDir(cwd)
	dirs := []skillSearchPath{{filepath.Join(pcd, "npm", "node_modules"), skillProjectPackage}, {filepath.Join(agentDirectory, "npm", "node_modules"), skillUserPackage}}
	if g := skillsNpmRoot.get(); g != "" {
		dirs = append(dirs, skillSearchPath{g, skillUserPackage})
	}
	var results []skillSearchPath
	for _, d := range dirs {
		for _, root := range collectPackageRootsFromNodeModules(d.path, nil) {
			p, _ := extractSkillPathsFromPackageRoot(root, d.source, true)
			results = append(results, p...)
		}
	}
	return results
}

func collectSettingsSkillPaths(cwd, agentDirectory string) ([]skillSearchPath, error) {
	pcd := getProjectConfigDir(cwd)
	var results []skillSearchPath
	for _, f := range []struct {
		file, base string
		source     skillSource
	}{{filepath.Join(pcd, "settings.json"), pcd, skillProjectSettings}, {filepath.Join(agentDirectory, "settings.json"), agentDirectory, skillUserSettings}} {
		settings, err := readOptionalJSONFile(f.file, "skills settings file")
		if err != nil {
			return nil, err
		}
		o, _ := settings.(*jsObject)
		if o == nil {
			continue
		}
		skills, _ := o.vals["skills"].([]any)
		for _, e := range skills {
			s, ok := e.(string)
			if !ok {
				continue
			}
			resolved := s
			if strings.HasPrefix(s, "~/") {
				resolved = filepath.Join(homeDir(), s[2:])
			} else if !filepath.IsAbs(s) {
				resolved, _ = filepath.Abs(filepath.Join(f.base, s))
			}
			results = append(results, skillSearchPath{resolved, f.source})
		}
	}
	return results, nil
}

// skillsSettingsPackageRoot is skills.ts:290 (it has no bare-https branch, unlike the agent discovery's).
func skillsSettingsPackageRoot(source, baseDir string) string {
	trimmed := jsTrim(source)
	if trimmed == "" {
		return ""
	}
	if httpRe.MatchString(trimmed) && !strings.HasPrefix(trimmed, "git:") && !strings.HasPrefix(trimmed, "npm:") {
		return ""
	}
	return resolveSettingsPackageRoot(source, baseDir)
}

func collectSettingsPackageSkillPaths(cwd, agentDirectory string) ([]skillSearchPath, error) {
	pcd := getProjectConfigDir(cwd)
	var results []skillSearchPath
	for _, f := range []struct {
		file, base string
		source     skillSource
	}{{filepath.Join(pcd, "settings.json"), pcd, skillProjectPackage}, {filepath.Join(agentDirectory, "settings.json"), agentDirectory, skillUserPackage}} {
		settings, err := readOptionalJSONFile(f.file, "skills settings file")
		if err != nil {
			return nil, err
		}
		o, _ := settings.(*jsObject)
		if o == nil {
			continue
		}
		pkgs, _ := o.vals["packages"].([]any)
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
			root := skillsSettingsPackageRoot(src, f.base)
			if root == "" {
				continue
			}
			p, err := extractSkillPathsFromPackageRoot(root, f.source, false)
			if err != nil {
				return nil, err
			}
			results = append(results, p...)
		}
	}
	return results, nil
}

func buildSkillPaths(cwd, agentDirectory string) ([]skillSearchPath, error) {
	pcd := getProjectConfigDir(cwd)
	paths := []skillSearchPath{
		{filepath.Join(pcd, "skills"), skillProject},
		{filepath.Join(cwd, ".agents", "skills"), skillProject},
		{filepath.Join(agentDirectory, "skills"), skillUser},
		{filepath.Join(homeDir(), ".agents", "skills"), skillUser},
	}
	paths = append(paths, collectInstalledPackageSkillPaths(cwd, agentDirectory)...)
	sp, err := collectSettingsPackageSkillPaths(cwd, agentDirectory)
	if err != nil {
		return nil, err
	}
	paths = append(paths, sp...)
	cp, err := extractSkillPathsFromPackageRoot(cwd, skillProjectPackage, false)
	if err != nil {
		return nil, err
	}
	paths = append(paths, cp...)
	ss, err := collectSettingsSkillPaths(cwd, agentDirectory)
	if err != nil {
		return nil, err
	}
	paths = append(paths, ss...)
	byPath := map[string]int{}
	var out []skillSearchPath
	for _, e := range paths {
		abs, _ := filepath.Abs(e.path)
		if i, ok := byPath[abs]; ok {
			if skillSourcePriority[e.source] > skillSourcePriority[out[i].source] {
				out[i] = skillSearchPath{abs, e.source}
			}
			continue
		}
		byPath[abs] = len(out)
		out = append(out, skillSearchPath{abs, e.source})
	}
	return out, nil
}

func inferSkillSource(filePath, cwd, agentDirectory string, hint skillSource) skillSource {
	if hint != "" {
		return hint
	}
	abs := func(p ...string) string { a, _ := filepath.Abs(filepath.Join(p...)); return a }
	projectConfigRoot := abs(getProjectConfigDir(cwd))
	projectSkillsRoot := abs(projectConfigRoot, "skills")
	projectPackagesRoot := abs(projectConfigRoot, "npm", "node_modules")
	projectAgentsRoot := abs(cwd, ".agents")
	userSkillsRoot := abs(agentDirectory, "skills")
	userPackagesRoot := abs(agentDirectory, "npm", "node_modules")
	userAgentRoot := abs(agentDirectory)
	userAgentsRoot := abs(homeDir(), ".agents")
	switch {
	case isWithinPath(filePath, projectPackagesRoot):
		return skillProjectPackage
	case isWithinPath(filePath, projectSkillsRoot) || isWithinPath(filePath, projectAgentsRoot):
		return skillProject
	case isWithinPath(filePath, projectConfigRoot):
		return skillProjectSettings
	case isWithinPath(filePath, userPackagesRoot):
		return skillUserPackage
	case isWithinPath(filePath, userSkillsRoot) || isWithinPath(filePath, userAgentsRoot):
		return skillUser
	case isWithinPath(filePath, userAgentRoot):
		return skillUserSettings
	}
	if g := skillsNpmRoot.get(); g != "" && isWithinPath(filePath, g) {
		return skillUserPackage
	}
	return skillUnknown
}

func chooseHigherPrioritySkill(existing *cachedSkillEntry, c cachedSkillEntry) cachedSkillEntry {
	if existing == nil {
		return c
	}
	ep, cp := skillSourcePriority[existing.source], skillSourcePriority[c.source]
	if cp > ep {
		return c
	}
	if cp < ep {
		return *existing
	}
	if c.order < existing.order {
		return c
	}
	return *existing
}

func parseSkillDescription(content string) *string {
	fm, _ := ParseFrontmatter(content)
	if fm.Has("description") {
		d := fm.Get("description")
		return &d
	}
	return nil
}

// maybeReadSkillDisableModelInvocation matches the host's skill loader: only YAML boolean true disables model invocation.
func maybeReadSkillDisableModelInvocation(content string) *bool {
	n := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(n, "---") {
		return nil
	}
	i := strings.Index(n[3:], "\n---")
	if i == -1 {
		return nil
	}
	end := 3 + i
	if len(n) < 4 || end < 4 {
		return nil
	}
	parsed, err := parseYAML(n[4:end])
	if err != nil {
		return nil
	}
	if o, ok := parsed.(*jsObject); ok {
		if v, ok := o.vals["disable-model-invocation"].(bool); ok && v {
			return new(true)
		}
	}
	return nil
}

func maybeReadSkillMetadata(filePath string) (desc *string, dmi *bool) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil
	}
	return parseSkillDescription(string(b)), maybeReadSkillDisableModelInvocation(string(b))
}

// collectFilesystemSkills is skills.ts:433.
func collectFilesystemSkills(cwd, agentDirectory string, skillPaths []skillSearchPath) []cachedSkillEntry {
	var entries []cachedSkillEntry
	seen := map[string]int{}
	visited := map[string]int{}
	order := 0
	pushEntry := func(name, filePath string, hint skillSource) {
		resolved, _ := filepath.Abs(filePath)
		if _, err := os.Stat(resolved); err != nil {
			return
		}
		source := inferSkillSource(resolved, cwd, agentDirectory, hint)
		desc, dmi := maybeReadSkillMetadata(resolved)
		if idx, ok := seen[resolved]; ok {
			if skillSourcePriority[source] > skillSourcePriority[entries[idx].source] {
				e := entries[idx]
				e.name, e.source, e.description, e.disableModelInvocation = name, source, desc, dmi
				entries[idx] = e
			}
			return
		}
		seen[resolved] = len(entries)
		entries = append(entries, cachedSkillEntry{name: name, filePath: resolved, source: source, description: desc, disableModelInvocation: dmi, order: order})
		order++
	}
	skipDir := func(n string) bool { return strings.HasPrefix(n, ".") || n == "node_modules" }
	markVisited := func(dirPath string, hint skillSource) bool {
		real, err := filepath.EvalSymlinks(dirPath)
		if err != nil {
			real, _ = filepath.Abs(dirPath)
		}
		prio := skillSourcePriority[skillUnknown]
		if hint != "" {
			prio = skillSourcePriority[hint]
		}
		if prev, ok := visited[real]; ok && prev >= prio {
			return false
		}
		visited[real] = prio
		return true
	}
	var walk func(dirPath string, hint skillSource)
	walk = func(dirPath string, hint skillSource) {
		if !markVisited(dirPath, hint) {
			return
		}
		skillFile := filepath.Join(dirPath, "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			pushEntry(filepath.Base(dirPath), skillFile, hint)
			return
		}
		children, err := os.ReadDir(dirPath)
		if err != nil {
			return
		}
		for _, e := range children {
			if skipDir(e.Name()) || (!e.IsDir() && e.Type()&os.ModeSymlink == 0) {
				continue
			}
			p := filepath.Join(dirPath, e.Name())
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				walk(p, hint)
			}
		}
	}
	for _, sp := range skillPaths {
		st, err := os.Stat(sp.path)
		if err != nil {
			continue
		}
		if st.Mode().IsRegular() {
			fileName := filepath.Base(sp.path)
			if !strings.HasSuffix(strings.ToLower(fileName), ".md") {
				continue
			}
			name := strings.TrimSuffix(fileName, filepath.Ext(fileName))
			if strings.ToLower(fileName) == "skill.md" {
				name = filepath.Base(filepath.Dir(sp.path))
			}
			pushEntry(name, sp.path, sp.source)
			continue
		}
		if !st.IsDir() {
			continue
		}
		rootSkill := filepath.Join(sp.path, "SKILL.md")
		if _, err := os.Stat(rootSkill); err == nil {
			pushEntry(filepath.Base(sp.path), rootSkill, sp.source)
			continue
		}
		markVisited(sp.path, sp.source)
		children, err := os.ReadDir(sp.path)
		if err != nil {
			continue
		}
		for _, c := range children {
			if strings.HasPrefix(c.Name(), ".") {
				continue
			}
			childPath := filepath.Join(sp.path, c.Name())
			if c.IsDir() || c.Type()&os.ModeSymlink != 0 {
				if skipDir(c.Name()) {
					continue
				}
				if cs, err := os.Stat(childPath); err == nil && cs.IsDir() {
					walk(childPath, sp.source)
				}
				continue
			}
			if c.Type().IsRegular() && strings.HasSuffix(strings.ToLower(c.Name()), ".md") {
				pushEntry(strings.TrimSuffix(c.Name(), filepath.Ext(c.Name())), childPath, sp.source)
			}
		}
	}
	return entries
}

// getCachedSkills is skills.ts:579. A malformed skills settings file is an error, as the original throws.
func getCachedSkills(cwd string) ([]cachedSkillEntry, error) {
	now := time.Now()
	dir := agentDir()
	skillMu.Lock()
	if loadSkills.valid && loadSkills.cwd == cwd && loadSkills.dir == dir && now.Sub(loadSkills.timestamp) < loadSkillsCacheTTL {
		s := loadSkills.skills
		skillMu.Unlock()
		return s, nil
	}
	skillMu.Unlock()
	paths, err := buildSkillPaths(cwd, dir)
	if err != nil {
		return nil, err
	}
	loaded := collectFilesystemSkills(cwd, dir, paths)
	byName := map[string]cachedSkillEntry{}
	for _, e := range loaded {
		cur, ok := byName[e.name]
		var p *cachedSkillEntry
		if ok {
			p = &cur
		}
		byName[e.name] = chooseHigherPrioritySkill(p, e)
	}
	skills := make([]cachedSkillEntry, 0, len(byName))
	for _, e := range byName {
		skills = append(skills, e)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].order < skills[j].order })
	skillMu.Lock()
	loadSkills.valid, loadSkills.cwd, loadSkills.dir, loadSkills.skills, loadSkills.timestamp = true, cwd, dir, skills, now
	skillMu.Unlock()
	return skills, nil
}

// resolveSkillPath is skills.ts:600.
func resolveSkillPath(skillName, cwd string) (path string, source skillSource, ok bool, err error) {
	skills, err := getCachedSkills(cwd)
	if err != nil {
		return "", "", false, err
	}
	for _, s := range skills {
		if s.name == skillName {
			return s.filePath, s.source, true, nil
		}
	}
	return "", "", false, nil
}

func readSkill(name, skillPath string, source skillSource) *ResolvedSkill {
	st, err := os.Stat(skillPath)
	if err != nil {
		return nil
	}
	mtime := st.ModTime().UnixNano()
	skillMu.Lock()
	if c, ok := skillCache[skillPath]; ok && c.mtime == mtime {
		s := c.skill
		skillMu.Unlock()
		return &s
	}
	skillMu.Unlock()
	b, err := os.ReadFile(skillPath)
	if err != nil {
		return nil
	}
	raw := string(b)
	skill := ResolvedSkill{Name: name, Path: skillPath, Content: stripSkillFrontmatter(raw), Description: parseSkillDescription(raw), DisableModelInvocation: maybeReadSkillDisableModelInvocation(raw), Source: source}
	skillMu.Lock()
	if _, exists := skillCache[skillPath]; !exists {
		skillCacheKeys = append(skillCacheKeys, skillPath)
	}
	skillCache[skillPath] = skillCacheEntry{mtime, skill}
	if len(skillCache) > maxSkillCacheSize && len(skillCacheKeys) > 0 {
		first := skillCacheKeys[0]
		skillCacheKeys = skillCacheKeys[1:]
		delete(skillCache, first)
	}
	skillMu.Unlock()
	return &skill
}

// resolveSkills is skills.ts:648.
func resolveSkills(skillNames []string, cwd string, localSkillPaths []string, localBaseDir string) (resolved []ResolvedSkill, missing []string, err error) {
	localByName := map[string]cachedSkillEntry{}
	if len(localSkillPaths) > 0 {
		base := localBaseDir
		if base == "" {
			base = cwd
		}
		var sps []skillSearchPath
		for _, e := range localSkillPaths {
			abs, _ := filepath.Abs(filepath.Join(base, e))
			if filepath.IsAbs(e) {
				abs = filepath.Clean(e)
			}
			sps = append(sps, skillSearchPath{abs, skillUnknown})
		}
		for _, e := range collectFilesystemSkills(cwd, agentDir(), sps) {
			if _, ok := localByName[e.name]; !ok {
				localByName[e.name] = e
			}
		}
	}
	for _, name := range skillNames {
		t := jsTrim(name)
		if t == "" {
			continue
		}
		if t == subagentOrchestrationSkill {
			missing = append(missing, t)
			continue
		}
		var skill *ResolvedSkill
		if local, ok := localByName[t]; ok {
			skill = readSkill(t, local.filePath, local.source)
		}
		if skill == nil {
			p, src, ok, err := resolveSkillPath(t, cwd)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				skill = readSkill(t, p, src)
			}
		}
		if skill != nil {
			resolved = append(resolved, *skill)
		} else {
			missing = append(missing, t)
		}
	}
	return resolved, missing, nil
}

// resolveSkillsWithFallback is skills.ts:689.
func resolveSkillsWithFallback(skillNames []string, primaryCwd, fallbackCwd string, localSkillPaths []string, localBaseDir string) ([]ResolvedSkill, []string, error) {
	resolved, missing, err := resolveSkills(skillNames, primaryCwd, localSkillPaths, localBaseDir)
	if err != nil || fallbackCwd == "" || len(missing) == 0 {
		return resolved, missing, err
	}
	a, _ := filepath.Abs(primaryCwd)
	b, _ := filepath.Abs(fallbackCwd)
	if a == b {
		return resolved, missing, nil
	}
	fr, fm, err := resolveSkills(missing, fallbackCwd, nil, "")
	if err != nil {
		return nil, nil, err
	}
	return append(resolved, fr...), fm, nil
}

func escapeXMLText(v string) string {
	v = strings.ReplaceAll(v, "&", "&amp;")
	v = strings.ReplaceAll(v, "<", "&lt;")
	return strings.ReplaceAll(v, ">", "&gt;")
}

// buildSkillInjection is skills.ts:707: hidden skills (disable-model-invocation: true) never reach a child prompt.
func buildSkillInjection(skills []ResolvedSkill) string {
	var visible []ResolvedSkill
	for _, s := range skills {
		if s.DisableModelInvocation == nil || !*s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	lines := []string{
		"The following configured skills are available to this subagent.",
		"Use the read tool to load a skill's file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, s := range visible {
		desc := ""
		if s.Description != nil {
			desc = *s.Description
		}
		lines = append(lines, "  <skill>", "    <name>"+escapeXMLText(s.Name)+"</name>", "    <description>"+escapeXMLText(desc)+"</description>", "    <location>"+escapeXMLText(s.Path)+"</location>", "  </skill>")
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func dedupeTrimmed(parts []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range parts {
		if t := jsTrim(p); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

var _ = regexp.MustCompile

// SkillInput is a normalized skill parameter: unset, false (none), or a list.
type SkillInput struct {
	Set, False bool
	Items      []string
}

// normalizeSkillInput is skills.ts:740. input is nil, a bool, a string or a list of strings.
func normalizeSkillInput(input any) SkillInput {
	switch x := input.(type) {
	case bool:
		if !x {
			return SkillInput{Set: true, False: true}
		}
		return SkillInput{}
	case nil:
		return SkillInput{}
	case []string:
		return SkillInput{Set: true, Items: dedupeTrimmed(x)}
	case []any:
		var parts []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return SkillInput{Set: true, Items: dedupeTrimmed(parts)}
	case string:
		t := jsTrim(x)
		if strings.HasPrefix(t, "[") {
			if v, msg := jsJSONParse(t); msg == "" {
				if arr, ok := v.([]any); ok {
					return normalizeSkillInput(arr)
				}
			}
		}
		return SkillInput{Set: true, Items: dedupeTrimmed(strings.Split(x, ","))}
	}
	return SkillInput{}
}

// AvailableSkill is one entry of discoverAvailableSkills.
type AvailableSkill struct {
	Name                   string
	Source                 skillSource
	Description            *string
	DisableModelInvocation *bool
}

// discoverAvailableSkills is skills.ts:766.
func discoverAvailableSkills(cwd string) ([]AvailableSkill, error) {
	skills, err := getCachedSkills(cwd)
	if err != nil {
		return nil, err
	}
	var out []AvailableSkill
	for _, s := range skills {
		if s.name != subagentOrchestrationSkill {
			out = append(out, AvailableSkill{s.name, s.source, s.description, s.disableModelInvocation})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return localeLess(out[i].Name, out[j].Name) })
	return out, nil
}
