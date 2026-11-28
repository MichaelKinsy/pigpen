package urls

import (
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/internal-urls/skill-protocol.ts

//go:embed prompts/skill.md
var skillPromptDoc string

// SkillEntry describes a loaded skill addressable via `skill://<name>`.
// upstream: extensibility/skills.ts Skill.
type SkillEntry struct {
	Name        string
	Description string
	FilePath    string
	BaseDir     string
	// ContainRoot, when non-empty, bounds symlink resolution to that root
	// (e.g. plugin root). Empty leaves non-plugin skills unrestricted.
	ContainRoot string
}

var (
	skillsMu       sync.RWMutex
	activeSkills   []SkillEntry
	skillsProvider func(c Context) []SkillEntry
)

// SetActiveSkills installs a process-wide skill list (for tests or hosts that push loaded skills).
func SetActiveSkills(skills []SkillEntry) {
	skillsMu.Lock()
	activeSkills = append([]SkillEntry(nil), skills...)
	skillsMu.Unlock()
}

// SetSkillsProvider installs a callback that supplies skills for a given Context.
// When nil, SkillHandler uses SetActiveSkills if non-empty, then the host session's
// loaded skills via sdk.Context.GetSystemPromptOptions() (falling back to disk
// discovery only for a zero SDK context).
func SetSkillsProvider(fn func(c Context) []SkillEntry) {
	skillsMu.Lock()
	skillsProvider = fn
	skillsMu.Unlock()
}

// ResetSkillsForTests clears the active skills, session skill cache, and provider.
func ResetSkillsForTests() {
	skillsMu.Lock()
	activeSkills = nil
	skillsProvider = nil
	skillsMu.Unlock()
	sessionStateMu.Lock()
	for _, st := range sessionStates {
		st.skills = nil
		st.hasSkills = false
	}
	sessionStateMu.Unlock()
}

func skillEntriesFromSDK(list []sdk.SystemPromptSkill) []SkillEntry {
	out := make([]SkillEntry, 0, len(list))
	for _, s := range list {
		if s.Name == "" {
			continue
		}
		baseDir := s.BaseDir
		if baseDir == "" && s.FilePath != "" {
			baseDir = filepath.Dir(s.FilePath)
		}
		out = append(out, SkillEntry{
			Name:        s.Name,
			Description: s.Description,
			FilePath:    s.FilePath,
			BaseDir:     baseDir,
		})
	}
	return out
}

func resolveActiveSkills(c Context) []SkillEntry {
	return resolveSkills(c, false)
}

// resolveSkills returns the active skills for c. When preferCache is true
// (autocomplete on every keystroke), the per-session cache populated by
// before_agent_start or an earlier host read is returned without a host call.
func resolveSkills(c Context, preferCache bool) []SkillEntry {
	skillsMu.RLock()
	prov := skillsProvider
	active := activeSkills
	skillsMu.RUnlock()
	if prov != nil {
		return prov(c)
	}
	if len(active) > 0 {
		return active
	}
	if c.SDK != (sdk.Context{}) {
		sid, sidErr := c.SDK.GetSessionID()
		if preferCache && sidErr == nil && sid != "" {
			if cached, ok := getSessionSkills(sid); ok {
				return cached
			}
		}
		// Prefer the host session's loaded skills (from GetSystemPromptOptions,
		// cached per session from before_agent_start if the host call fails).
		if opts, err := c.SDK.GetSystemPromptOptions(); err == nil {
			skills := skillEntriesFromSDK(opts.Skills)
			if sidErr == nil && sid != "" {
				setSessionSkills(sid, skills)
			}
			return skills
		}
		if sidErr == nil && sid != "" {
			if cached, ok := getSessionSkills(sid); ok {
				return cached
			}
		}
		return nil
	}
	// Zero SDK context (unit tests or standalone calls without a host session):
	// discover skills from standard PiG/omp directories on disk.
	return discoverSkills(c.Cwd)
}

// discoverSkills scans standard PiG and omp skill directories on disk for
// standalone/zero-SDK-context callers.
func discoverSkills(cwd string) []SkillEntry {
	var dirs []string
	seenDir := map[string]bool{}
	addDir := func(d string) {
		if d == "" {
			return
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			abs = d
		}
		if seenDir[abs] {
			return
		}
		seenDir[abs] = true
		dirs = append(dirs, abs)
	}

	if cwd != "" {
		addDir(filepath.Join(cwd, ".pig", "skills"))
		addDir(filepath.Join(cwd, ".omp", "skills"))
		// Walk ancestors for .agents/skills (stopping at .git or filesystem root).
		cur, err := filepath.Abs(cwd)
		if err == nil {
			for {
				addDir(filepath.Join(cur, ".agents", "skills"))
				if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
					break
				}
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
			}
		}
	}

	if agentDir := os.Getenv("PIG_CODING_AGENT_DIR"); agentDir != "" {
		addDir(filepath.Join(agentDir, "skills"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		addDir(filepath.Join(home, ".pig", "agent", "skills"))
		addDir(filepath.Join(home, ".omp", "agent", "skills"))
		addDir(filepath.Join(home, ".agents", "skills"))
	}

	seenName := map[string]bool{}
	var out []SkillEntry
	for _, root := range dirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			full := filepath.Join(root, e.Name())
			if e.IsDir() {
				skillFile := filepath.Join(full, "SKILL.md")
				if st, err := os.Stat(skillFile); err == nil && !st.IsDir() {
					name, desc := parseSkillFrontmatter(skillFile, e.Name())
					if !seenName[name] {
						seenName[name] = true
						out = append(out, SkillEntry{
							Name:        name,
							Description: desc,
							FilePath:    skillFile,
							BaseDir:     full,
						})
					}
				}
			} else if strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				name, desc := parseSkillFrontmatter(full, base)
				if !seenName[name] {
					seenName[name] = true
					out = append(out, SkillEntry{
						Name:        name,
						Description: desc,
						FilePath:    full,
						BaseDir:     root,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseSkillFrontmatter extracts `name` and `description` from optional YAML frontmatter.
func parseSkillFrontmatter(filePath, fallbackName string) (name, desc string) {
	name = fallbackName
	data, err := os.ReadFile(filePath)
	if err != nil {
		return name, ""
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return name, ""
	}
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "---" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "name":
			if v != "" {
				name = v
			}
		case "description":
			desc = v
		}
	}
	return name, desc
}

// SkillHandler serves `skill://<name>[/<path>]` URLs.
// upstream: skill-protocol.ts SkillProtocolHandler.
type SkillHandler struct{}

func (SkillHandler) Scheme() string { return "skill" }

func (SkillHandler) Spec() Spec {
	return Spec{
		Backing:   BackingFile,
		Selectors: SelectorsLines,
		Immutable: true,
		Unbounded: true,
		Linkable:  true,
	}
}

func (SkillHandler) PromptDoc(host SchemeHost) string {
	if !host.SkillURIAccess {
		return ""
	}
	return strings.TrimSpace(skillPromptDoc)
}

// normalizeSkillURLPath collapses non-escaping dot segments in a URL path the
// way WHATWG URL.pathname does (e.g. `/a/../b.md` -> `/b.md`), while preserving
// leading traversal attempts (`/../x`) so validateRelativePath still rejects them.
func normalizeSkillURLPath(p string) string {
	if p == "" || p == "/" {
		return p
	}
	// If the raw path already starts with `/..` or contains `%2e%2e`, let
	// validateRelativePath reject it verbatim rather than silently collapsing.
	trimmed := strings.TrimPrefix(p, "/")
	if strings.HasPrefix(trimmed, "..") {
		return p
	}
	// Only collapse internal `a/../b` segments that never pop above root.
	parts := strings.Split(trimmed, "/")
	depth := 0
	for _, seg := range parts {
		switch seg {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return p
			}
		default:
			depth++
		}
	}
	trailingSlash := strings.HasSuffix(p, "/")
	cleaned := path.Clean("/" + trimmed)
	if trailingSlash && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

func findURLSkill(u URL, skills []SkillEntry) (SkillEntry, string, error) {
	skillName := u.Host
	if skillName == "" {
		return SkillEntry{}, "", errors.New("skill:// URL requires a skill name: skill://<name>")
	}
	// Normalize dot-segments in the pathname as WHATWG URL.pathname does
	// (upstream: skill-protocol.ts:75 uses url.pathname), preserving leading slash.
	urlPath := normalizeSkillURLPath(u.Path)
	if len(urlPath) > 1 {
		// Namespaced skill (<plugin>/<name>) spans host + first path segment.
		rest := urlPath[1:]
		firstSeg, afterSlash, hasSlash := strings.Cut(rest, "/")
		decodedSeg, err := url.PathUnescape(firstSeg)
		if err != nil {
			decodedSeg = firstSeg
		}
		namespaced := skillName + "/" + decodedSeg
		for _, s := range skills {
			if s.Name == namespaced {
				if hasSlash {
					return s, "/" + afterSlash, nil
				}
				return s, "", nil
			}
		}
	}
	for _, s := range skills {
		if s.Name == skillName {
			return s, urlPath, nil
		}
	}
	avail := "none"
	if len(skills) > 0 {
		names := make([]string, len(skills))
		for i, s := range skills {
			names[i] = s.Name
		}
		avail = strings.Join(names, ", ")
	}
	return SkillEntry{}, "", fmt.Errorf("Unknown skill: %s\nAvailable: %s", skillName, avail)
}

func skillTargetPath(u URL, skill SkillEntry, urlPath string, directory, create bool) (string, error) {
	var resolvedPath string
	if urlPath == "" || urlPath == "/" {
		if directory {
			resolvedPath = skill.BaseDir
		} else {
			resolvedPath = skill.FilePath
		}
		if abs, err := filepath.Abs(resolvedPath); err == nil {
			resolvedPath = abs
		}
	} else {
		rel, err := url.PathUnescape(strings.TrimPrefix(urlPath, "/"))
		if err != nil {
			return "", fmt.Errorf("Invalid URL encoding in skill:// path: %s", u.Href)
		}
		if err := validateRelativePath(rel, "skill"); err != nil {
			return "", err
		}
		absBase, err := filepath.Abs(skill.BaseDir)
		if err != nil {
			absBase = skill.BaseDir
		}
		resolvedPath = filepath.Join(absBase, filepath.FromSlash(rel))
		if err := ensureWithinRoot(resolvedPath, absBase, "skill", u.Href); err != nil {
			return "", errors.New("Path traversal is not allowed")
		}
	}

	if skill.ContainRoot == "" {
		return resolvedPath, nil
	}
	absContainRoot, err := filepath.Abs(skill.ContainRoot)
	if err != nil {
		absContainRoot = skill.ContainRoot
	}
	realContainRoot, err := filepath.EvalSymlinks(absContainRoot)
	if err != nil {
		return "", err
	}
	escapeMsg := fmt.Sprintf("skill:// path resolves outside the plugin root: %s", u.Href)
	// Rebase resolvedPath onto realContainRoot if absContainRoot itself traverses a symlink
	// (e.g. /var -> /private/var on macOS).
	containmentPath := resolvedPath
	if relToContain, relErr := filepath.Rel(absContainRoot, resolvedPath); relErr == nil && !strings.HasPrefix(relToContain, "..") {
		containmentPath = filepath.Join(realContainRoot, relToContain)
	}
	realTarget, found, err := containedRealPath(containmentPath, realContainRoot, "skill", u.Href)
	if err != nil {
		var ce *UrlContainmentError
		if errors.As(err, &ce) {
			return "", &UrlContainmentError{Msg: escapeMsg}
		}
		return "", err
	}
	if found {
		return realTarget, nil
	}
	if create {
		if err := ensureCreatableWithinRoot(containmentPath, realContainRoot, "skill", u.Href); err != nil {
			var ce *UrlContainmentError
			if errors.As(err, &ce) {
				return "", &UrlContainmentError{Msg: escapeMsg}
			}
			return "", err
		}
	}
	return resolvedPath, nil
}

func (SkillHandler) Resolve(c Context, u URL) (*Resource, error) {
	skills := resolveActiveSkills(c)
	skill, urlPath, err := findURLSkill(u, skills)
	if err != nil {
		return nil, err
	}
	targetPath, err := skillTargetPath(u, skill, urlPath, false, false)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(targetPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("File not found: %s", targetPath)
		}
		return nil, err
	}
	if st.IsDir() {
		return buildDirectoryResource(u.Href, targetPath, nil)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("skill:// URL must resolve to a file or directory: %s", u.Href)
	}
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, err
	}
	return &Resource{
		URL:         u.Href,
		Content:     string(data),
		ContentType: contentTypeForPath(targetPath),
		SourcePath:  targetPath,
	}, nil
}

func (SkillHandler) Locate(c Context, u URL, opts LocateOptions) (string, bool, error) {
	skills := resolveActiveSkills(c)
	skill, urlPath, err := findURLSkill(u, skills)
	if err != nil {
		return "", false, err
	}
	targetPath, err := skillTargetPath(u, skill, urlPath, opts.Directory, opts.Create)
	if err != nil {
		return "", false, err
	}
	if opts.Create {
		return targetPath, true, nil
	}
	if _, err := os.Stat(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return targetPath, true, nil
}

func (SkillHandler) Complete(c Context, _ string) ([]Completion, error) {
	skills := resolveSkills(c, true)
	out := make([]Completion, len(skills))
	for i, s := range skills {
		out[i] = Completion{
			Value:       s.Name,
			Description: s.Description,
		}
	}
	return out, nil
}
