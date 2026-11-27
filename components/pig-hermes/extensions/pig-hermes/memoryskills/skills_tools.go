package memoryskills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const (
	maxSkillNameLen    = 64
	maxSkillDescLen    = 1024
	maxSkillContentLen = 100000
	maxSkillFileBytes  = 1048576 // 1 MiB
	maxSkillWalkDepth  = 6
	defaultDescLimit   = 60
)

var (
	validSkillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	allowedSubdirs   = map[string]bool{
		"references": true,
		"templates":  true,
		"scripts":    true,
		"assets":     true,
	}
	excludedSkillDirs = map[string]bool{
		".git":         true,
		".locks":       true,
		".archive":     true,
		".hub":         true,
		"node_modules": true,
		"__pycache__":  true,
		".venv":        true,
		"venv":         true,
	}
)

// SkillMeta holds parsed SKILL.md frontmatter and location metadata.
type SkillMeta struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    string            `json:"category,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Platforms   []string          `json:"platforms,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Source      string            `json:"source"` // "project", "local", "extra"
	Tier        int               `json:"-"`      // 0=project, 1=local, 2=extra
	Dir         string            `json:"-"`
	FilePath    string            `json:"-"`
	RelPath     string            `json:"-"`
}

// SkillFrontmatter holds parsed YAML frontmatter from SKILL.md.
type SkillFrontmatter struct {
	Name        string
	Description string
	Category    string
	Tags        []string
	Platforms   []string
	Metadata    map[string]string
	HasName     bool
	HasDesc     bool
}

type skillRoot struct {
	Tier   int
	Source string
	Dir    string
}

// skillMutex serializes mutations across skill_manage calls.
var skillMu sync.Mutex

func registerSkillsTools(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config) {
	if !isToolDisabled(cfg, "skills_list") {
		ext.RegisterTool(sdk.ToolDefinition{
			Name:        "skills_list",
			Label:       "Skills List",
			Description: "List available skills (name, category, description, source). Use skill_view(name) to load full content.",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"category": map[string]any{
						"type":        "string",
						"description": "Optional category filter to narrow results.",
					},
					"query": map[string]any{
						"type":        "string",
						"description": "Optional substring query to filter skills by name, description, category, or tags.",
					},
				},
				"required": []string{},
			},
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				return handleSkillsList(ctx, p, cfg, params), nil
			},
		})
	}

	if !isToolDisabled(cfg, "skill_view") {
		ext.RegisterTool(sdk.ToolDefinition{
			Name:        "skill_view",
			Label:       "Skill View",
			Description: "Load a skill's SKILL.md, or one of its linked references, templates, scripts, or assets. The first call returns SKILL.md plus a 'linked_files' dict; call again with file_path to read one.",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "The skill name (use skills_list to see available skills).",
					},
					"file_path": map[string]any{
						"type":        "string",
						"description": "OPTIONAL: Path to a linked file within the skill (e.g., 'references/api.md', 'templates/config.yaml', 'scripts/validate.py'). Omit to get the main SKILL.md content.",
					},
				},
				"required": []string{"name"},
			},
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				return handleSkillView(ctx, p, cfg, params), nil
			},
		})
	}

	if !isToolDisabled(cfg, "skill_manage") {
		ext.RegisterTool(sdk.ToolDefinition{
			Name:  "skill_manage",
			Label: "Skill Manage",
			Description: "Create, update, patch, or delete skills and their supporting files (references/, templates/, scripts/, assets/). " +
				"Actions: create, edit, patch, delete, write_file, remove_file.",
			Parameters: sdk.Schema{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "edit", "patch", "delete", "write_file", "remove_file"},
						"description": "Action to perform on the skill.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name (lowercase letters, numbers, hyphens, dots, underscores; max 64 chars).",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full SKILL.md content (for create/edit) or file content (for write_file).",
					},
					"file_content": map[string]any{
						"type":        "string",
						"description": "Supporting file content for write_file (alias for content).",
					},
					"old_string": map[string]any{
						"type":        "string",
						"description": "Text to find when action='patch'.",
					},
					"new_string": map[string]any{
						"type":        "string",
						"description": "Replacement text when action='patch'.",
					},
					"replace_all": map[string]any{
						"type":        "boolean",
						"description": "Replace all occurrences when action='patch' (default false).",
					},
					"category": map[string]any{
						"type":        "string",
						"description": "Optional single-segment category directory when action='create'.",
					},
					"file_path": map[string]any{
						"type":        "string",
						"description": "Relative supporting file path under references/, templates/, scripts/, or assets/.",
					},
				},
				"required": []string{"action", "name"},
			},
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				return handleSkillManage(ctx, p, cfg, params), nil
			},
		})
	}
}

func resolveSkillRoots(cwd string, p paths.ProfilePaths, cfg *config.Config) []skillRoot {
	var roots []skillRoot
	seen := make(map[string]bool)
	addRoot := func(tier int, source, dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		expanded := expandHomePath(strings.TrimSpace(dir))
		abs, err := filepath.Abs(expanded)
		if err != nil {
			abs = filepath.Clean(expanded)
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		roots = append(roots, skillRoot{Tier: tier, Source: source, Dir: abs})
	}

	// Tier 0: project (.hermes/skills, .agents/skills) under git root or cwd.
	if cwd != "" {
		projectBase := findGitRoot(cwd)
		if projectBase == "" {
			projectBase = cwd
		}
		addRoot(0, "project", filepath.Join(projectBase, ".hermes", "skills"))
		addRoot(0, "project", filepath.Join(projectBase, ".agents", "skills"))
		if projectBase != cwd {
			addRoot(0, "project", filepath.Join(cwd, ".hermes", "skills"))
			addRoot(0, "project", filepath.Join(cwd, ".agents", "skills"))
		}
	}

	// Tier 1: local profile skills dir.
	if p.SkillsDir != "" {
		addRoot(1, "local", p.SkillsDir)
	}

	// Tier 2: extra configured dirs (skills.create_dir, skills.external_dirs).
	if cfg != nil && cfg.Raw != nil {
		if createDir := strings.TrimSpace(cfg.Raw["skills.create_dir"]); createDir != "" {
			addRoot(2, "extra", createDir)
		}
		if extDirs := strings.TrimSpace(cfg.Raw["skills.external_dirs"]); extDirs != "" {
			for _, part := range splitListValue(extDirs) {
				addRoot(2, "extra", part)
			}
		}
	}
	return roots
}

// DiscoverSkills scans skill roots in precedence order: project > local > extra.
// Higher-precedence tiers shadow lower-precedence skills with the same name.
func DiscoverSkills(cwd string, p paths.ProfilePaths, cfg *config.Config) []SkillMeta {
	roots := resolveSkillRoots(cwd, p, cfg)
	byName := make(map[string]SkillMeta)
	var order []string

	for _, r := range roots {
		info, err := os.Stat(r.Dir)
		if err != nil || !info.IsDir() {
			continue
		}
		found := scanSkillRoot(r)
		for _, sm := range found {
			if !skillMatchesCurrentPlatform(sm.Platforms) {
				continue
			}
			key := strings.ToLower(sm.Name)
			if _, exists := byName[key]; !exists {
				byName[key] = sm
				order = append(order, key)
			}
		}
	}

	out := make([]SkillMeta, 0, len(order))
	for _, k := range order {
		out = append(out, byName[k])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func scanSkillRoot(r skillRoot) []SkillMeta {
	var results []SkillMeta
	rootClean := filepath.Clean(r.Dir)

	_ = filepath.WalkDir(rootClean, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != rootClean {
				base := d.Name()
				if excludedSkillDirs[base] || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				rel, relErr := filepath.Rel(rootClean, path)
				if relErr == nil {
					depth := len(strings.Split(rel, string(filepath.Separator)))
					if depth > maxSkillWalkDepth {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		fm, _ := ParseSkillFrontmatter(string(raw))
		skillDir := filepath.Dir(path)
		relDir, _ := filepath.Rel(rootClean, skillDir)
		relDir = filepath.ToSlash(relDir)

		name := fm.Name
		if name == "" {
			name = filepath.Base(skillDir)
		}
		category := fm.Category
		if category == "" {
			if idx := strings.Index(relDir, "/"); idx > 0 {
				category = relDir[:idx]
			}
		}
		results = append(results, SkillMeta{
			Name:        name,
			Description: fm.Description,
			Category:    category,
			Tags:        fm.Tags,
			Platforms:   fm.Platforms,
			Metadata:    fm.Metadata,
			Source:      r.Source,
			Tier:        r.Tier,
			Dir:         skillDir,
			FilePath:    path,
			RelPath:     relDir,
		})
		return nil
	})
	return results
}

// ParseSkillFrontmatter parses YAML frontmatter delimited by --- lines at the top of SKILL.md.
// Returns the parsed frontmatter and the markdown body.
func ParseSkillFrontmatter(content string) (SkillFrontmatter, string) {
	var fm SkillFrontmatter
	clean := strings.TrimPrefix(content, "\ufeff")
	clean = strings.ReplaceAll(clean, "\r\n", "\n")

	if !strings.HasPrefix(clean, "---") {
		return fm, clean
	}
	rest := clean[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else {
		// First line must only have whitespace after ---.
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 || strings.TrimSpace(rest[:nl]) != "" {
			return fm, clean
		}
		rest = rest[nl+1:]
	}

	lines := strings.Split(rest, "\n")
	endIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return fm, clean
	}

	fmLines := lines[:endIdx]
	body := strings.Join(lines[endIdx+1:], "\n")
	fm.Metadata = make(map[string]string)

	var currentListKey string
	inMetadata := false

	for _, rawLine := range fmLines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := countLeadingSpaces(rawLine)

		// YAML sequence item under tags / platforms.
		if strings.HasPrefix(trimmed, "- ") && currentListKey != "" {
			val := unquoteSimple(strings.TrimSpace(trimmed[2:]))
			if val != "" {
				switch currentListKey {
				case "tags":
					fm.Tags = append(fm.Tags, val)
				case "platforms":
					fm.Platforms = append(fm.Platforms, strings.ToLower(val))
				}
			}
			continue
		}

		if indent == 0 {
			currentListKey = ""
			inMetadata = false
		}

		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])

		if inMetadata && indent > 0 {
			if val != "" {
				fm.Metadata[key] = unquoteSimple(val)
			}
			continue
		}

		switch strings.ToLower(key) {
		case "name":
			fm.HasName = true
			fm.Name = unquoteSimple(val)
		case "description":
			fm.HasDesc = true
			fm.Description = unquoteSimple(val)
		case "category":
			fm.Category = unquoteSimple(val)
		case "tags":
			if val == "" {
				currentListKey = "tags"
			} else {
				fm.Tags = append(fm.Tags, parseInlineList(val)...)
			}
		case "platforms":
			if val == "" {
				currentListKey = "platforms"
			} else {
				for _, p := range parseInlineList(val) {
					fm.Platforms = append(fm.Platforms, strings.ToLower(p))
				}
			}
		case "metadata":
			if val == "" {
				inMetadata = true
			}
		}
	}
	if len(fm.Metadata) == 0 {
		fm.Metadata = nil
	}
	return fm, body
}

func validateSkillFrontmatter(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("Content cannot be empty.")
	}
	clean := strings.TrimPrefix(content, "\ufeff")
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	if !strings.HasPrefix(clean, "---") {
		return errors.New("SKILL.md must start with YAML frontmatter (---). See existing skills for format.")
	}
	// Verify closing delimiter.
	rest := clean[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 || strings.TrimSpace(rest[:nl]) != "" {
		return errors.New("SKILL.md must start with YAML frontmatter (---). See existing skills for format.")
	}
	lines := strings.Split(rest[nl+1:], "\n")
	closed := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "---" {
			closed = true
			break
		}
	}
	if !closed {
		return errors.New("SKILL.md frontmatter is not closed. Ensure you have a closing '---' line.")
	}

	fm, body := ParseSkillFrontmatter(content)
	if !fm.HasName || strings.TrimSpace(fm.Name) == "" {
		return errors.New("Frontmatter must include 'name' field.")
	}
	if !fm.HasDesc || strings.TrimSpace(fm.Description) == "" {
		return errors.New("Frontmatter must include 'description' field.")
	}
	if len(fm.Description) > maxSkillDescLen {
		return fmt.Errorf("Description exceeds %d characters.", maxSkillDescLen)
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("SKILL.md must have content after the frontmatter (instructions, procedures, etc.).")
	}
	if len(content) > maxSkillContentLen {
		return fmt.Errorf("SKILL.md exceeds %d characters.", maxSkillContentLen)
	}
	return nil
}

func validateSkillName(name string) error {
	if name == "" {
		return errors.New("Skill name is required.")
	}
	if len(name) > maxSkillNameLen {
		return fmt.Errorf("Skill name exceeds %d characters.", maxSkillNameLen)
	}
	if !validSkillNameRe.MatchString(name) {
		return fmt.Errorf("Invalid skill name '%s'. Use lowercase letters, numbers, hyphens, dots, and underscores. Must start with a letter or digit.", name)
	}
	return nil
}

func validateCategory(cat string) error {
	cat = strings.TrimSpace(cat)
	if cat == "" {
		return nil
	}
	if strings.ContainsAny(cat, "/\\") || len(cat) > maxSkillNameLen || !validSkillNameRe.MatchString(cat) {
		return fmt.Errorf("Invalid category '%s'. Use lowercase letters, numbers, hyphens, dots, and underscores. Categories must be a single directory name.", cat)
	}
	return nil
}

// resolveSafeSkillFile validates that relPath is a safe relative path inside skillDir
// (no traversal, no absolute paths, no symlink escape). When requireSubdir is true, the
// first path component must be one of references/, templates/, scripts/, assets/.
func resolveSafeSkillFile(skillDir, relPath string, requireSubdir bool) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", errors.New("file_path is required.")
	}
	if strings.ContainsRune(relPath, '\x00') {
		return "", errors.New("Invalid file_path: null byte not allowed.")
	}
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") || strings.HasPrefix(relPath, "\\") {
		return "", fmt.Errorf("Invalid file_path '%s': must be a relative path within the skill directory.", relPath)
	}
	// Reject Windows drive prefixes like C:foo on all platforms.
	if len(relPath) >= 2 && relPath[1] == ':' {
		return "", fmt.Errorf("Invalid file_path '%s': must be a relative path within the skill directory.", relPath)
	}
	slashed := strings.ReplaceAll(relPath, "\\", "/")
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return "", fmt.Errorf("Path traversal not allowed in file_path '%s'.", relPath)
		}
	}
	cleaned := filepath.Clean(filepath.FromSlash(slashed))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Path traversal not allowed in file_path '%s'.", relPath)
	}

	if requireSubdir {
		parts := strings.Split(filepath.ToSlash(cleaned), "/")
		if len(parts) < 2 || !allowedSubdirs[parts[0]] {
			return "", fmt.Errorf("Invalid file_path '%s': must be inside references/, templates/, scripts/, or assets/.", relPath)
		}
	}

	absSkillDir, err := filepath.Abs(skillDir)
	if err != nil {
		return "", err
	}
	resolvedRoot := absSkillDir
	if evalRoot, err := filepath.EvalSymlinks(absSkillDir); err == nil {
		resolvedRoot = evalRoot
	}

	target := filepath.Join(absSkillDir, cleaned)
	relCheck, err := filepath.Rel(absSkillDir, target)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("Path traversal not allowed in file_path '%s'.", relPath)
	}

	// Check symlink containment for existing target or its nearest existing ancestor.
	checkPath := target
	for {
		if _, statErr := os.Lstat(checkPath); statErr == nil {
			if evalTarget, evalErr := filepath.EvalSymlinks(checkPath); evalErr == nil {
				relEval, relErr := filepath.Rel(resolvedRoot, evalTarget)
				if relErr != nil || strings.HasPrefix(relEval, "..") {
					return "", fmt.Errorf("Path '%s' resolves outside the skill directory.", relPath)
				}
			}
			break
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath || parent == absSkillDir {
			break
		}
		checkPath = parent
	}

	return target, nil
}

func handleSkillsList(ctx sdk.Context, p paths.ProfilePaths, cfg *config.Config, params map[string]any) sdk.ToolResult {
	cwd := safeCwd(ctx)
	skills := DiscoverSkills(cwd, p, cfg)

	catFilter := strings.ToLower(strings.TrimSpace(stringParam(params, "category")))
	queryFilter := strings.ToLower(strings.TrimSpace(stringParam(params, "query")))

	filtered := make([]SkillMeta, 0, len(skills))
	catSet := make(map[string]bool)
	for _, s := range skills {
		if catFilter != "" && strings.ToLower(s.Category) != catFilter {
			continue
		}
		if queryFilter != "" && !matchesSkillQuery(s, queryFilter) {
			continue
		}
		filtered = append(filtered, s)
		if s.Category != "" {
			catSet[s.Category] = true
		}
	}

	categories := make([]string, 0, len(catSet))
	for c := range catSet {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	type compactSkill struct {
		Name        string `json:"name"`
		Category    string `json:"category,omitempty"`
		Description string `json:"description"`
		Source      string `json:"source"`
	}
	compact := make([]compactSkill, 0, len(filtered))
	for _, s := range filtered {
		compact = append(compact, compactSkill{
			Name:        s.Name,
			Category:    s.Category,
			Description: s.Description,
			Source:      s.Source,
		})
	}

	return jsonToolOK(map[string]any{
		"success":    true,
		"skills":     compact,
		"categories": categories,
		"count":      len(compact),
		"hint":       "Use skill_view(name) to see full content, tags, and linked files",
	})
}

func handleSkillView(ctx sdk.Context, p paths.ProfilePaths, cfg *config.Config, params map[string]any) sdk.ToolResult {
	name := strings.TrimSpace(stringParam(params, "name"))
	if name == "" {
		return jsonToolError("Skill name is required.")
	}
	if strings.Contains(name, "..") || filepath.IsAbs(name) {
		return jsonToolError(fmt.Sprintf("Invalid skill name '%s'.", name))
	}

	cwd := safeCwd(ctx)
	skills := DiscoverSkills(cwd, p, cfg)
	skill, found := findSkillByName(skills, name)
	if !found {
		return jsonToolError(fmt.Sprintf("Skill '%s' not found. Use skills_list() to see available skills.", name))
	}

	filePath := strings.TrimSpace(stringParam(params, "file_path"))
	if filePath != "" {
		target, err := resolveSafeSkillFile(skill.Dir, filePath, false)
		if err != nil {
			return jsonToolError(err.Error())
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			return jsonToolError(fmt.Sprintf("Failed to read '%s' in skill '%s': %v", filePath, skill.Name, err))
		}
		if isLikelyBinary(raw) {
			return jsonToolOK(map[string]any{
				"success":   true,
				"name":      skill.Name,
				"file_path": filepath.ToSlash(filePath),
				"is_binary": true,
				"size":      len(raw),
				"message":   fmt.Sprintf("Binary file (%d bytes).", len(raw)),
			})
		}
		return jsonToolOK(map[string]any{
			"success":   true,
			"name":      skill.Name,
			"file_path": filepath.ToSlash(filePath),
			"content":   string(raw),
		})
	}

	raw, err := os.ReadFile(skill.FilePath)
	if err != nil {
		return jsonToolError(fmt.Sprintf("Failed to read skill '%s': %v", skill.Name, err))
	}
	linked := listLinkedSkillFiles(skill.Dir)
	res := map[string]any{
		"success":     true,
		"name":        skill.Name,
		"description": skill.Description,
		"category":    skill.Category,
		"source":      skill.Source,
		"tags":        skill.Tags,
		"content":     string(raw),
		"path":        skill.RelPath,
		"skill_dir":   skill.Dir,
	}
	if len(skill.Metadata) > 0 {
		res["metadata"] = skill.Metadata
	}
	if len(linked) > 0 {
		res["linked_files"] = linked
		res["usage_hint"] = "To view linked files, call skill_view(name, file_path) where file_path is e.g. 'references/api.md' or 'assets/config.yaml'"
	}
	return jsonToolOK(res)
}

func handleSkillManage(ctx sdk.Context, p paths.ProfilePaths, cfg *config.Config, params map[string]any) sdk.ToolResult {
	action := strings.ToLower(strings.TrimSpace(stringParam(params, "action")))
	name := strings.TrimSpace(stringParam(params, "name"))
	content := stringParam(params, "content")
	fileContent := stringParam(params, "file_content")
	if fileContent == "" {
		fileContent = content
	}
	oldString := stringParam(params, "old_string")
	newString := stringParam(params, "new_string")
	replaceAll := boolParam(params, "replace_all")
	category := strings.TrimSpace(stringParam(params, "category"))
	filePath := strings.TrimSpace(stringParam(params, "file_path"))

	if action == "" {
		return jsonToolError("action is required. Use: create, edit, patch, delete, write_file, remove_file")
	}

	// Validate skill name (for create, must be bare kebab/valid name; for others allow category/name).
	baseName := name
	if action != "create" && strings.Contains(name, "/") {
		parts := strings.Split(name, "/")
		baseName = parts[len(parts)-1]
	}
	if err := validateSkillName(baseName); err != nil {
		return jsonToolError(err.Error())
	}

	skillMu.Lock()
	defer skillMu.Unlock()

	cwd := safeCwd(ctx)

	switch action {
	case "create":
		if err := validateCategory(category); err != nil {
			return jsonToolError(err.Error())
		}
		if err := validateSkillFrontmatter(content); err != nil {
			return jsonToolError(err.Error())
		}
		existing := DiscoverSkills(cwd, p, cfg)
		if found, ok := findSkillByName(existing, name); ok {
			return jsonToolError(fmt.Sprintf("A skill named '%s' already exists at %s.", name, found.FilePath))
		}
		baseDir := p.SkillsDir
		if cfg != nil && cfg.Raw != nil {
			if cd := strings.TrimSpace(cfg.Raw["skills.create_dir"]); cd != "" {
				baseDir = expandHomePath(cd)
			}
		}
		if baseDir == "" {
			return jsonToolError("Skills directory is not configured.")
		}
		skillDir := filepath.Join(baseDir, name)
		if category != "" {
			skillDir = filepath.Join(baseDir, category, name)
		}
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to create skill directory: %v", err))
		}
		skillMD := filepath.Join(skillDir, "SKILL.md")
		if err := atomicWriteFile(skillMD, []byte(content), 0o644); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to write SKILL.md: %v", err))
		}
		res := map[string]any{
			"success":  true,
			"message":  fmt.Sprintf("Skill '%s' created.", name),
			"path":     skillDir,
			"skill_md": skillMD,
		}
		if category != "" {
			res["category"] = category
		}
		return jsonToolOK(res)

	case "edit":
		if err := validateSkillFrontmatter(content); err != nil {
			return jsonToolError(err.Error())
		}
		skills := DiscoverSkills(cwd, p, cfg)
		skill, ok := findSkillByName(skills, name)
		if !ok {
			return jsonToolError(fmt.Sprintf("Skill '%s' not found.", name))
		}
		if err := atomicWriteFile(skill.FilePath, []byte(content), 0o644); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to update SKILL.md: %v", err))
		}
		return jsonToolOK(map[string]any{
			"success": true,
			"message": fmt.Sprintf("Skill '%s' updated (full rewrite).", skill.Name),
			"path":    skill.Dir,
		})

	case "patch":
		if oldString == "" {
			return jsonToolError("old_string is required for action='patch'.")
		}
		skills := DiscoverSkills(cwd, p, cfg)
		skill, ok := findSkillByName(skills, name)
		if !ok {
			return jsonToolError(fmt.Sprintf("Skill '%s' not found.", name))
		}
		target := skill.FilePath
		targetLabel := "SKILL.md"
		if filePath != "" {
			var err error
			target, err = resolveSafeSkillFile(skill.Dir, filePath, true)
			if err != nil {
				return jsonToolError(err.Error())
			}
			targetLabel = filepath.ToSlash(filePath)
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			return jsonToolError(fmt.Sprintf("File not found: %s", targetLabel))
		}
		current := string(raw)
		count := strings.Count(current, oldString)
		if count == 0 {
			return jsonToolError(fmt.Sprintf("old_string not found in %s of skill '%s'.", targetLabel, skill.Name))
		}
		if count > 1 && !replaceAll {
			return jsonToolError(fmt.Sprintf("old_string matched %d times in %s; provide more context or set replace_all=true.", count, targetLabel))
		}
		var updated string
		replacements := 1
		if replaceAll {
			updated = strings.ReplaceAll(current, oldString, newString)
			replacements = count
		} else {
			updated = strings.Replace(current, oldString, newString, 1)
		}
		if filePath == "" {
			if err := validateSkillFrontmatter(updated); err != nil {
				return jsonToolError(fmt.Sprintf("Patch would break SKILL.md structure: %v", err))
			}
		} else if len(updated) > maxSkillFileBytes {
			return jsonToolError(fmt.Sprintf("Patched file exceeds %d bytes.", maxSkillFileBytes))
		}
		if err := atomicWriteFile(target, []byte(updated), 0o644); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to patch %s: %v", targetLabel, err))
		}
		return jsonToolOK(map[string]any{
			"success": true,
			"message": fmt.Sprintf("Patched %s in skill '%s' (%d replacement(s)).", targetLabel, skill.Name, replacements),
		})

	case "delete":
		skills := DiscoverSkills(cwd, p, cfg)
		skill, ok := findSkillByName(skills, name)
		if !ok {
			return jsonToolError(fmt.Sprintf("Skill '%s' not found.", name))
		}
		if err := validateDeletableSkillDir(skill.Dir, cwd, p, cfg); err != nil {
			return jsonToolError(fmt.Sprintf("Refusing to delete skill '%s': %v", skill.Name, err))
		}
		if err := os.RemoveAll(skill.Dir); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to delete skill '%s': %v", skill.Name, err))
		}
		return jsonToolOK(map[string]any{
			"success": true,
			"message": fmt.Sprintf("Skill '%s' deleted.", skill.Name),
		})

	case "write_file":
		if filePath == "" {
			return jsonToolError("file_path is required for action='write_file'.")
		}
		if len(fileContent) > maxSkillFileBytes {
			return jsonToolError(fmt.Sprintf("File content exceeds %d bytes (1 MiB).", maxSkillFileBytes))
		}
		skills := DiscoverSkills(cwd, p, cfg)
		skill, ok := findSkillByName(skills, name)
		if !ok {
			return jsonToolError(fmt.Sprintf("Skill '%s' not found. Create it first with action='create'.", name))
		}
		target, err := resolveSafeSkillFile(skill.Dir, filePath, true)
		if err != nil {
			return jsonToolError(err.Error())
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to create directory: %v", err))
		}
		if err := atomicWriteFile(target, []byte(fileContent), 0o644); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to write '%s': %v", filePath, err))
		}
		return jsonToolOK(map[string]any{
			"success": true,
			"message": fmt.Sprintf("File '%s' written to skill '%s'.", filepath.ToSlash(filePath), skill.Name),
			"path":    target,
		})

	case "remove_file":
		if filePath == "" {
			return jsonToolError("file_path is required for action='remove_file'.")
		}
		skills := DiscoverSkills(cwd, p, cfg)
		skill, ok := findSkillByName(skills, name)
		if !ok {
			return jsonToolError(fmt.Sprintf("Skill '%s' not found.", name))
		}
		target, err := resolveSafeSkillFile(skill.Dir, filePath, true)
		if err != nil {
			return jsonToolError(err.Error())
		}
		if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
			return jsonToolError(fmt.Sprintf("File '%s' not found in skill '%s'.", filepath.ToSlash(filePath), skill.Name))
		}
		if err := os.Remove(target); err != nil {
			return jsonToolError(fmt.Sprintf("Failed to remove '%s': %v", filePath, err))
		}
		return jsonToolOK(map[string]any{
			"success": true,
			"message": fmt.Sprintf("File '%s' removed from skill '%s'.", filepath.ToSlash(filePath), skill.Name),
		})

	default:
		return jsonToolError(fmt.Sprintf("Unknown action '%s'. Use: create, edit, patch, delete, write_file, remove_file", action))
	}
}

func validateDeletableSkillDir(skillDir, cwd string, p paths.ProfilePaths, cfg *config.Config) error {
	if strings.TrimSpace(skillDir) == "" {
		return errors.New("empty skill directory")
	}
	linfo, lerr := os.Lstat(skillDir)
	if lerr != nil {
		return fmt.Errorf("failed to inspect skill directory: %v", lerr)
	}
	if linfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill directory %q is a symlink", skillDir)
	}
	skillDirAbs, err := filepath.Abs(skillDir)
	if err != nil {
		skillDirAbs = filepath.Clean(skillDir)
	}
	skillDirReal, err := filepath.EvalSymlinks(skillDirAbs)
	if err != nil {
		skillDirReal = skillDirAbs
	}
	for _, r := range resolveSkillRoots(cwd, p, cfg) {
		rootAbs, err := filepath.Abs(r.Dir)
		if err != nil {
			rootAbs = filepath.Clean(r.Dir)
		}
		rootReal, err := filepath.EvalSymlinks(rootAbs)
		if err != nil {
			rootReal = rootAbs
		}
		if skillDirAbs == rootAbs || skillDirReal == rootReal {
			return fmt.Errorf("skill directory %q is a skills root", skillDir)
		}
	}
	return nil
}

func findSkillByName(skills []SkillMeta, name string) (SkillMeta, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, s := range skills {
		if strings.ToLower(s.Name) == lower || strings.ToLower(s.RelPath) == lower {
			return s, true
		}
	}
	return SkillMeta{}, false
}

func matchesSkillQuery(s SkillMeta, query string) bool {
	if strings.Contains(strings.ToLower(s.Name), query) ||
		strings.Contains(strings.ToLower(s.Description), query) ||
		strings.Contains(strings.ToLower(s.Category), query) {
		return true
	}
	for _, t := range s.Tags {
		if strings.Contains(strings.ToLower(t), query) {
			return true
		}
	}
	return false
}

func listLinkedSkillFiles(skillDir string) map[string][]string {
	linked := make(map[string][]string)
	for subdir := range allowedSubdirs {
		subPath := filepath.Join(skillDir, subdir)
		info, err := os.Stat(subPath)
		if err != nil || !info.IsDir() {
			continue
		}
		var files []string
		_ = filepath.WalkDir(subPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(skillDir, path)
			if relErr == nil {
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if len(files) > 0 {
			sort.Strings(files)
			linked[subdir] = files
		}
	}
	return linked
}

func skillMatchesCurrentPlatform(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	cur := runtime.GOOS
	for _, p := range platforms {
		norm := strings.ToLower(strings.TrimSpace(p))
		switch norm {
		case "macos", "osx", "darwin":
			if cur == "darwin" {
				return true
			}
		case "linux":
			if cur == "linux" {
				return true
			}
		case "windows", "win32", "win":
			if cur == "windows" {
				return true
			}
		default:
			if norm == cur {
				return true
			}
		}
	}
	return false
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".skill_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func isLikelyBinary(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	return bytes.IndexByte(data[:limit], 0) >= 0
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
		} else if r == '\t' {
			n += 2
		} else {
			break
		}
	}
	return n
}

func unquoteSimple(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseInlineList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := unquoteSimple(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func splitListValue(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return parseInlineList(s)
	}
	// Support comma or path-list separated strings.
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := unquoteSimple(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func expandHomePath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return os.ExpandEnv(p)
}

func safeCwd(ctx sdk.Context) (out string) {
	defer func() {
		if recover() != nil || strings.TrimSpace(out) == "" {
			if cwd, err := os.Getwd(); err == nil {
				out = cwd
			}
		}
	}()
	if cwd := strings.TrimSpace(ctx.Cwd()); cwd != "" {
		return cwd
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// findGitRoot walks up from dir looking for a .git directory or file.
func findGitRoot(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
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

// Keep json import used even if refactored later.
var _ = json.Marshal
