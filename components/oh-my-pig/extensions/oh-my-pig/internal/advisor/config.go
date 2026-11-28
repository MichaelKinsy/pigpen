package advisor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// upstream: packages/coding-agent/src/advisor/config.ts
// upstream: packages/coding-agent/src/advisor/watchdog.ts

// ReviewMode controls when an advisor runs its review pass.
type ReviewMode string

const (
	ReviewModeTurn     ReviewMode = "turn"
	ReviewModeAgentEnd ReviewMode = "agent-end"
)

// RuntimeStatus mirrors omp's AdvisorRuntimeStatus (config.ts:40).
type RuntimeStatus string

const (
	StatusRunning        RuntimeStatus = "running"
	StatusPaused         RuntimeStatus = "paused"
	StatusQuotaExhausted RuntimeStatus = "quota_exhausted"
	StatusError          RuntimeStatus = "error"
	StatusNoModel        RuntimeStatus = "no_model"
)

// AdvisorConfig describes a single configured advisor from WATCHDOG.yml/yaml.
type AdvisorConfig struct {
	Name              string
	Slug              string
	Model             string
	Tools             []string // nil => default read-only subset (read, grep, glob)
	ToolsExplicit     bool     // distinguishes explicit `tools: []` from omitted
	ReviewMode        ReviewMode
	ReviewInterval    int // 0 => use default setting
	SyncBacklog       string
	MaxNotesPerUpdate int // 0 => use shared/default setting
	Enabled           *bool
	Scope             string // "" | "primary" | "all"
	IncludeReasoning  *bool
	Instructions      string
}

// DiscoveredAdvisors is the merged result of walking the WATCHDOG.yml/yaml
// search path (user agent dir -> project ancestors -> cwd).
type DiscoveredAdvisors struct {
	Advisors                []AdvisorConfig
	SharedInstructions      string
	SharedMaxNotesPerUpdate int
	SourcePaths             []string
	Warnings                []string
}

// ConfigCandidate represents one readable WATCHDOG config file on disk.
type ConfigCandidate struct {
	Path    string
	Content string
	Level   string // "user" | "project"
	Depth   int
}

// DefaultAdvisorToolNames is the read-only investigative tool set granted to
// advisors when a config omits `tools` (upstream: advise-tool.ts:165).
var DefaultAdvisorToolNames = []string{"read", "grep", "glob"}

// knownAdvisorTools mirrors omp's BUILTIN_TOOL_NAMES validation set plus aliases.
var knownAdvisorTools = map[string]string{
	"read":       "read",
	"grep":       "grep",
	"search":     "grep",
	"glob":       "glob",
	"find":       "glob",
	"edit":       "edit",
	"write":      "write",
	"bash":       "bash",
	"eval":       "eval",
	"web_search": "web_search",
	"fetch":      "fetch",
	"lsp":        "lsp",
	"ast_grep":   "ast_grep",
	"ast_edit":   "ast_edit",
	"recall":     "recall",
	"task":       "task",
	"todo":       "todo",
}

// SlugifyAdvisorName normalizes an advisor display name to a filesystem-safe
// slug (upstream: advisor/config.ts:138-140).
func SlugifyAdvisorName(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "advisor"
	}
	return slug
}

// DefaultAgentDir returns the user-level PiG agent configuration directory
// (`$PIG_CODING_AGENT_DIR`, `$PIG_HOME/agent`, or `~/.pig/agent`).
func DefaultAgentDir() string {
	if v := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("PIG_HOME")); v != "" {
		return filepath.Join(v, "agent")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".pig", "agent")
}

// CollectConfigCandidates walks the watchdog/advisor search path — user agent
// dir plus every directory from cwd up to git repo root (or home), probing both
// `<F>` and `.omp/<F>` — returning readable candidates sorted user-first then
// project ancestor -> leaf (depth descending).
// upstream: packages/coding-agent/src/advisor/watchdog.ts:66-134
func CollectConfigCandidates(cwd, agentDir string, filenames []string) []ConfigCandidate {
	return CollectConfigCandidatesWithTrust(cwd, agentDir, filenames, true)
}

// CollectConfigCandidatesWithTrust walks the watchdog/advisor search path,
// omitting project-level candidates when includeProject is false (untrusted
// workspace).
func CollectConfigCandidatesWithTrust(cwd, agentDir string, filenames []string, includeProject bool) []ConfigCandidate {
	home, _ := os.UserHomeDir()
	if agentDir == "" {
		agentDir = DefaultAgentDir()
	}
	absCwd := ""
	stopDir := ""
	if includeProject && cwd != "" {
		var err error
		absCwd, err = filepath.Abs(cwd)
		if err != nil {
			absCwd = cwd
		}
		stopDir = findGitRoot(absCwd)
		if stopDir == "" {
			stopDir = home
		}
	}
	seen := map[string]bool{}
	userPaths := map[string]bool{}
	var paths []string

	if agentDir != "" {
		for _, fn := range filenames {
			p := filepath.Join(agentDir, fn)
			if !seen[p] {
				seen[p] = true
				userPaths[p] = true
				paths = append(paths, p)
			}
		}
	}

	if includeProject && absCwd != "" {
		cur := absCwd
		for cur != "" {
			for _, fn := range filenames {
				for _, p := range []string{filepath.Join(cur, ".omp", fn), filepath.Join(cur, fn)} {
					if !seen[p] {
						seen[p] = true
						paths = append(paths, p)
					}
				}
			}
			if stopDir != "" && samePath(cur, stopDir) {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}

	var items []ConfigCandidate
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		parent := filepath.Dir(p)
		baseName := filepath.Base(parent)
		isUser := userPaths[p]
		ownerDir := parent
		if baseName == ".omp" {
			ownerDir = filepath.Dir(parent)
		}
		ownerBase := filepath.Base(ownerDir)
		if isUser || !strings.HasPrefix(ownerBase, ".") || baseName == ".omp" {
			rel, err := filepath.Rel(absCwd, ownerDir)
			depth := 0
			if err == nil && rel != "." && rel != "" {
				depth = len(strings.Split(filepath.ToSlash(rel), "/"))
			}
			level := "project"
			if isUser {
				level = "user"
			}
			items = append(items, ConfigCandidate{
				Path:    p,
				Content: string(raw),
				Level:   level,
				Depth:   depth,
			})
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Level != items[j].Level {
			return items[i].Level == "user"
		}
		return items[i].Depth > items[j].Depth
	})
	return items
}

func findGitRoot(dir string) string {
	cur := dir
	for cur != "" {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// DiscoverWatchdogFiles discovers WATCHDOG.md files along the config search
// path and formats them as `<attention>` blocks for the advisor system prompt.
// upstream: packages/coding-agent/src/advisor/watchdog.ts:140-148
func DiscoverWatchdogFiles(cwd, agentDir string) []string {
	return DiscoverWatchdogFilesWithTrust(cwd, agentDir, true)
}

// DiscoverWatchdogFilesWithTrust discovers WATCHDOG.md files along the config
// search path, skipping project-level files when includeProject is false.
func DiscoverWatchdogFilesWithTrust(cwd, agentDir string, includeProject bool) []string {
	items := CollectConfigCandidatesWithTrust(cwd, agentDir, []string{"WATCHDOG.md"}, includeProject)
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		body := strings.TrimSpace(item.Content)
		if body == "" {
			continue
		}
		out = append(out, "Especially pay attention to:\n<attention>\n"+body+"\n</attention>")
	}
	return out
}

// DiscoverAdvisorConfigs walks WATCHDOG.yml and WATCHDOG.yaml along the config
// search path, merging advisors by slug (project leaf overrides ancestor/user)
// and concatenating top-level `instructions`.
// upstream: packages/coding-agent/src/advisor/config.ts:200-270
func DiscoverAdvisorConfigs(cwd, agentDir string) DiscoveredAdvisors {
	return DiscoverAdvisorConfigsWithTrust(cwd, agentDir, true)
}

// DiscoverAdvisorConfigsWithTrust discovers WATCHDOG.yml/yaml files, skipping
// project-level files when includeProject is false.
func DiscoverAdvisorConfigsWithTrust(cwd, agentDir string, includeProject bool) DiscoveredAdvisors {
	items := CollectConfigCandidatesWithTrust(cwd, agentDir, []string{"WATCHDOG.yml", "WATCHDOG.yaml"}, includeProject)
	res := DiscoveredAdvisors{}
	bySlug := map[string]AdvisorConfig{}
	var order []string
	var sharedParts []string

	for _, item := range items {
		res.SourcePaths = append(res.SourcePaths, item.Path)
		doc, warnings := ParseWatchdogYAML(item.Content, item.Path)
		res.Warnings = append(res.Warnings, warnings...)
		if strings.TrimSpace(doc.Instructions) != "" {
			sharedParts = append(sharedParts, strings.TrimSpace(doc.Instructions))
		}
		if doc.MaxNotesPerUpdate > 0 {
			res.SharedMaxNotesPerUpdate = doc.MaxNotesPerUpdate
		}
		for _, entry := range doc.Advisors {
			slug := SlugifyAdvisorName(entry.Name)
			entry.Slug = slug
			if _, exists := bySlug[slug]; !exists {
				order = append(order, slug)
			}
			bySlug[slug] = entry
		}
	}

	for _, slug := range order {
		res.Advisors = append(res.Advisors, bySlug[slug])
	}
	if len(sharedParts) > 0 {
		res.SharedInstructions = strings.Join(sharedParts, "\n\n")
	}
	return res
}

// WatchdogConfigDoc is the parsed representation of one WATCHDOG.yml file.
type WatchdogConfigDoc struct {
	Instructions      string
	MaxNotesPerUpdate int
	Advisors          []AdvisorConfig
}

// ParseWatchdogYAML parses omp's WATCHDOG.yml subset using stdlib only (no
// third-party YAML dependency). Supports top-level `instructions` (inline or
// block scalar `|`/`>`), `maxNotesPerUpdate`, and `advisors:` sequence entries
// with `name`, `model`, `tools` (inline `[a, b]` or dashed list), `reviewMode`,
// `reviewInterval`, `syncBacklog`, `maxNotesPerUpdate`, `enabled`, `scope`,
// `includeReasoning`, and `instructions` (inline or block scalar).
func ParseWatchdogYAML(content, sourcePath string) (WatchdogConfigDoc, []string) {
	var doc WatchdogConfigDoc
	var warnings []string

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	n := len(lines)
	i := 0

	inAdvisors := false
	var current *AdvisorConfig
	flushCurrent := func() {
		if current == nil {
			return
		}
		if strings.TrimSpace(current.Name) == "" {
			warnings = append(warnings, fmt.Sprintf("%s: advisor entry missing name — dropped", sourcePath))
		} else {
			current.Slug = SlugifyAdvisorName(current.Name)
			doc.Advisors = append(doc.Advisors, *current)
		}
		current = nil
	}

	readBlockScalar := func(startIdx, parentIndent int, folded bool) (string, int) {
		var blockLines []string
		baseIndent := -1
		j := startIdx
		for j < n {
			raw := lines[j]
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				blockLines = append(blockLines, "")
				j++
				continue
			}
			ind := leadingSpaces(raw)
			if ind <= parentIndent {
				break
			}
			if baseIndent < 0 || ind < baseIndent {
				baseIndent = ind
			}
			blockLines = append(blockLines, raw)
			j++
		}
		for len(blockLines) > 0 && strings.TrimSpace(blockLines[len(blockLines)-1]) == "" {
			blockLines = blockLines[:len(blockLines)-1]
		}
		if baseIndent < 0 {
			baseIndent = parentIndent + 2
		}
		var out []string
		for _, bl := range blockLines {
			if bl == "" {
				out = append(out, "")
				continue
			}
			if len(bl) >= baseIndent {
				out = append(out, bl[baseIndent:])
			} else {
				out = append(out, strings.TrimLeft(bl, " "))
			}
		}
		if folded {
			return strings.TrimSpace(strings.Join(out, " ")), j
		}
		return strings.TrimSpace(strings.Join(out, "\n")), j
	}

	for i < n {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}
		ind := leadingSpaces(raw)

		// Top-level key (note: indent-0 `- ...` while inside `advisors:` is a
		// valid YAML sequence item, not a new top-level mapping key).
		if ind == 0 && !(inAdvisors && (strings.HasPrefix(trimmed, "- ") || trimmed == "-")) {
			flushCurrent()
			inAdvisors = false
			key, val, ok := splitYAMLKeyValue(trimmed)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("%s:%d: expected mapping key — skipped", sourcePath, i+1))
				i++
				continue
			}
			switch key {
			case "instructions":
				if isBlockScalarHeader(val) {
					body, next := readBlockScalar(i+1, 0, strings.HasPrefix(val, ">"))
					doc.Instructions = body
					i = next
					continue
				}
				doc.Instructions = unquoteYAMLScalar(val)
			case "maxNotesPerUpdate":
				if num, err := strconv.Atoi(unquoteYAMLScalar(val)); err == nil && num >= 1 {
					doc.MaxNotesPerUpdate = num
				} else {
					warnings = append(warnings, fmt.Sprintf("%s:%d: invalid maxNotesPerUpdate %q", sourcePath, i+1, val))
				}
			case "advisors":
				if val == "[]" {
					inAdvisors = false
				} else if val != "" {
					warnings = append(warnings, fmt.Sprintf("%s: advisors must be a list — ignored", sourcePath))
				} else {
					inAdvisors = true
				}
			}
			i++
			continue
		}

		if !inAdvisors {
			i++
			continue
		}

		// Start of a new advisor item: `  - name: ...` or `  -`
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			flushCurrent()
			current = &AdvisorConfig{}
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if rest != "" {
				if key, val, ok := splitYAMLKeyValue(rest); ok {
					nextIdx, warn := applyAdvisorField(current, key, val, i, ind+2, readBlockScalar, lines, sourcePath)
					if warn != "" {
						warnings = append(warnings, warn)
					}
					i = nextIdx
					continue
				}
			}
			i++
			continue
		}

		if current == nil {
			i++
			continue
		}

		key, val, ok := splitYAMLKeyValue(trimmed)
		if !ok {
			i++
			continue
		}
		nextIdx, warn := applyAdvisorField(current, key, val, i, ind, readBlockScalar, lines, sourcePath)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		i = nextIdx
	}

	flushCurrent()
	return doc, warnings
}

func applyAdvisorField(
	cfg *AdvisorConfig,
	key, val string,
	lineIdx, keyIndent int,
	readBlockScalar func(startIdx, parentIndent int, folded bool) (string, int),
	lines []string,
	sourcePath string,
) (int, string) {
	switch key {
	case "name":
		cfg.Name = unquoteYAMLScalar(val)
	case "model":
		cfg.Model = unquoteYAMLScalar(val)
	case "instructions":
		if isBlockScalarHeader(val) {
			body, next := readBlockScalar(lineIdx+1, keyIndent, strings.HasPrefix(val, ">"))
			cfg.Instructions = body
			return next, ""
		}
		cfg.Instructions = unquoteYAMLScalar(val)
	case "reviewMode":
		v := unquoteYAMLScalar(val)
		switch v {
		case string(ReviewModeTurn):
			cfg.ReviewMode = ReviewModeTurn
		case string(ReviewModeAgentEnd):
			cfg.ReviewMode = ReviewModeAgentEnd
		default:
			return lineIdx + 1, fmt.Sprintf("%s:%d: invalid reviewMode %q — ignored", sourcePath, lineIdx+1, v)
		}
	case "reviewInterval":
		if n, err := strconv.Atoi(unquoteYAMLScalar(val)); err == nil && n >= 1 {
			cfg.ReviewInterval = n
		} else {
			return lineIdx + 1, fmt.Sprintf("%s:%d: invalid reviewInterval %q — ignored", sourcePath, lineIdx+1, val)
		}
	case "syncBacklog":
		v := unquoteYAMLScalar(val)
		switch v {
		case "off", "1", "3", "5", "strict":
			cfg.SyncBacklog = v
		default:
			return lineIdx + 1, fmt.Sprintf("%s:%d: invalid syncBacklog %q — ignored", sourcePath, lineIdx+1, v)
		}
	case "maxNotesPerUpdate":
		if n, err := strconv.Atoi(unquoteYAMLScalar(val)); err == nil && n >= 1 {
			cfg.MaxNotesPerUpdate = n
		} else {
			return lineIdx + 1, fmt.Sprintf("%s:%d: invalid maxNotesPerUpdate %q — ignored", sourcePath, lineIdx+1, val)
		}
	case "enabled":
		if b, ok := parseYAMLBool(val); ok {
			cfg.Enabled = &b
		} else {
			return lineIdx + 1, fmt.Sprintf("%s:%d: invalid enabled %q — ignored", sourcePath, lineIdx+1, val)
		}
	case "includeReasoning":
		if b, ok := parseYAMLBool(val); ok {
			cfg.IncludeReasoning = &b
		}
	case "scope":
		v := unquoteYAMLScalar(val)
		if v == "primary" || v == "all" {
			cfg.Scope = v
		}
	case "tools":
		cfg.ToolsExplicit = true
		if val == "[]" {
			cfg.Tools = []string{}
			return lineIdx + 1, ""
		}
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(val[1 : len(val)-1])
			var rawTools []string
			if inner != "" {
				for _, part := range strings.Split(inner, ",") {
					t := unquoteYAMLScalar(part)
					if t != "" {
						rawTools = append(rawTools, t)
					}
				}
			}
			cfg.Tools = normalizeAdvisorTools(rawTools)
			return lineIdx + 1, ""
		}
		// Sequence lines indented deeper than `tools:`
		var rawTools []string
		j := lineIdx + 1
		for j < len(lines) {
			raw := lines[j]
			tr := strings.TrimSpace(raw)
			if tr == "" || strings.HasPrefix(tr, "#") {
				j++
				continue
			}
			if leadingSpaces(raw) <= keyIndent || !strings.HasPrefix(tr, "-") {
				break
			}
			item := unquoteYAMLScalar(strings.TrimSpace(strings.TrimPrefix(tr, "-")))
			if item != "" {
				rawTools = append(rawTools, item)
			}
			j++
		}
		cfg.Tools = normalizeAdvisorTools(rawTools)
		return j, ""
	}
	return lineIdx + 1, ""
}

func normalizeAdvisorTools(raw []string) []string {
	if len(raw) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		norm, ok := knownAdvisorTools[strings.ToLower(strings.TrimSpace(t))]
		if !ok {
			continue
		}
		if !slices.Contains(out, norm) {
			out = append(out, norm)
		}
	}
	return out
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func splitYAMLKeyValue(line string) (key, val string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])
	val = stripYAMLInlineComment(val)
	return key, val, key != ""
}

// stripYAMLInlineComment removes a trailing `# ...` comment outside quoted
// strings (both unquoted scalars and quoted scalars like `"X" # note`).
func stripYAMLInlineComment(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}
	if val[0] == '"' || val[0] == '\'' {
		quote := val[0]
		escaped := false
		for i := 1; i < len(val); i++ {
			c := val[i]
			if quote == '"' {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					return strings.TrimSpace(val[:i+1])
				}
			} else {
				// YAML single-quoted strings escape `'` as `''`.
				if c == '\'' {
					if i+1 < len(val) && val[i+1] == '\'' {
						i++
						continue
					}
					return strings.TrimSpace(val[:i+1])
				}
			}
		}
		return val
	}
	if hash := strings.Index(val, " #"); hash >= 0 {
		return strings.TrimSpace(val[:hash])
	}
	if hash := strings.Index(val, "\t#"); hash >= 0 {
		return strings.TrimSpace(val[:hash])
	}
	return val
}

func isBlockScalarHeader(val string) bool {
	return strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">")
}

func unquoteYAMLScalar(val string) string {
	v := stripYAMLInlineComment(val)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			if v[0] == '"' {
				if u, err := strconv.Unquote(v); err == nil {
					return u
				}
			}
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
	}
	return v
}

func parseYAMLBool(val string) (bool, bool) {
	switch strings.ToLower(unquoteYAMLScalar(val)) {
	case "true", "yes", "on":
		return true, true
	case "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// ResolveEditPath returns the canonical WATCHDOG.yml (or existing WATCHDOG.yaml)
// path for the given project cwd (upstream: advisor/config.ts:290-299).
func ResolveEditPath(cwd string) string {
	yml := filepath.Join(cwd, "WATCHDOG.yml")
	yaml := filepath.Join(cwd, "WATCHDOG.yaml")
	if _, err := os.Stat(yml); err != nil {
		if _, err2 := os.Stat(yaml); err2 == nil {
			return yaml
		}
	}
	return yml
}

// WatchdogTemplate is the starter WATCHDOG.yml schema shown by `/advisor configure`.
const WatchdogTemplate = `# WATCHDOG.yml — configure background advisor(s) for /advisor
instructions: |
  Focus on correctness, edge cases, and verification gaps.
advisors:
  - name: Reviewer
    model: "@advisor"
    tools: [read, grep, glob]
    reviewMode: turn
    reviewInterval: 1
    maxNotesPerUpdate: 4
    enabled: true
`
