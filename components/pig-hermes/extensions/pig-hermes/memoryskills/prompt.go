package memoryskills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/events"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const defaultAgentIdentity = "You are Hermes Agent, built by Nous Research. Be direct: match the length of your reply to the weight of the ask " +
	"— a one-line question gets a one-line answer, and finished work gets a short report of what changed, what's " +
	"verified, and what's left, never a replay of the process. No filler (\"Great question,\" \"I'd be happy to\"), no " +
	"restating the request back, no re-summarizing what you already said, no narrating tool calls the user can see. " +
	"Plain claims over adjectives; when unsure, say so plainly. Agree because it's right, not because the user said " +
	"it. Depth is earned — give it when the user asks for detail, teaches, or the stakes demand it, not by default."

const hermesAgentHelpGuidance = "For help with Hermes Agent itself — configuring, setting up, using, extending, or troubleshooting it — " +
	"or to understand your own features, tools, or capabilities, the documentation at " +
	"https://hermes-agent.nousresearch.com/docs is your authoritative reference. " +
	"Load the `hermes-agent` skill with skill_view(name='hermes-agent') when available."

const memoryGuidance = "You have persistent memory, carried across sessions and loaded into each new session's context; " +
	"the memory tool's schema defines what belongs there. Skills come first: when you learn something while doing a task — " +
	"a procedure, a pitfall, and the user's preferences and corrections for that kind of work — record it in the skill you " +
	"used or built for the task (skill_manage), where it loads only when relevant. Memory is the narrow exception for facts " +
	"that apply to EVERY session regardless of task (who the user is, environment facts, standing conventions with no task home); " +
	"it has a hard character budget, so when it fills, consolidate stale entries rather than skipping the save."

const skillsGuidance = "When you work out a non-trivial workflow, record it with skill_manage for future reuse."

const sessionSearchGuidance = "When the user references something from a past conversation or you suspect relevant cross-session " +
	"context exists, use session_search to recall it before asking them to repeat themselves."

const (
	defaultContextFileMaxChars = 20000
	providerPrefetchTimeout    = 8 * time.Second
)

// ContextFile represents one discovered project context file.
type ContextFile struct {
	Kind    string // "hermes_md", "agents_md", "claude_md", "cursorrules"
	Label   string
	Path    string
	Content string
}

type promptSessionState struct {
	mu          sync.Mutex
	startedAt   time.Time
	frozenSnap  *memory.Snapshot
}

func registerPromptHooks(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config, memStore *memory.Store) {
	state := &promptSessionState{
		startedAt: time.Now(),
	}

	// Freeze memory snapshot at session start so prompt prefix stays cache-warm.
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		state.mu.Lock()
		state.startedAt = time.Now()
		snap := memStore.LoadSnapshot()
		state.frozenSnap = &snap
		state.mu.Unlock()
		return nil, nil
	})

	// Reload frozen snapshot after context compaction.
	ext.OnEvent(sdk.EventSessionCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		state.mu.Lock()
		snap := memStore.LoadSnapshot()
		state.frozenSnap = &snap
		state.mu.Unlock()
		return nil, nil
	})

	// Notify external memory providers before compaction.
	ext.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		sessionID := ""
		if id, err := ctx.GetSessionID(); err == nil {
			sessionID = id
		}
		goCtx, cancel := context.WithTimeout(contextFromSDK(ctx), providerPrefetchTimeout)
		defer cancel()
		for _, prov := range memStore.Providers() {
			_ = prov.OnPreCompress(goCtx, sessionID)
		}
		return nil, nil
	})

	// Notify external memory providers on session shutdown.
	ext.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		sessionID := ""
		if id, err := ctx.GetSessionID(); err == nil {
			sessionID = id
		}
		goCtx, cancel := context.WithTimeout(contextFromSDK(ctx), providerPrefetchTimeout)
		defer cancel()
		for _, prov := range memStore.Providers() {
			_ = prov.OnSessionEnd(goCtx, sessionID)
		}
		return nil, nil
	})

	// Three-tier System Prompt Assembly in-place via sdk.SystemPromptSections.Set.
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		applyPromptSections(ctx, data, p, cfg, memStore, state)
		return nil, nil
	})
}

func applyPromptSections(
	ctx sdk.Context,
	data map[string]any,
	p paths.ProfilePaths,
	cfg *config.Config,
	memStore *memory.Store,
	state *promptSessionState,
) {
	sections := extractPromptSections(data)
	if sections == nil {
		return
	}

	cwd := safeCwd(ctx)

	state.mu.Lock()
	var snap memory.Snapshot
	if state.frozenSnap != nil {
		snap = *state.frozenSnap
	} else {
		snap = memStore.FrozenSnapshot()
		state.frozenSnap = &snap
	}
	startedAt := state.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
		state.startedAt = startedAt
	}
	state.mu.Unlock()

	// Tier 1 (Stable): Identity + Tool Guidance.
	identity := buildIdentitySection(p.SoulFile)
	toolsGuidance := buildToolsGuidanceSection(cfg)

	// Tier 2 (Context): Project context files in strict Hermes priority order.
	contextBlock := buildContextSection(cwd)

	// Tier 3 (Volatile): Skills index, Frozen Memory + External Provider Prefetch, Environment.
	skills := DiscoverSkills(cwd, p, cfg)
	skillsBlock := buildSkillsSection(skills)

	query := extractUserPrompt(data)
	sessionID := safeSessionID(ctx)
	memoryBlock := buildMemorySection(contextFromSDK(ctx), snap, memStore, sessionID, query)
	envBlock := buildEnvironmentSection(p, cwd, startedAt)

	// Populate sections in-place in three-tier order (stable -> context -> volatile)
	// WITHOUT wiping or overwriting unrelated sections (so Phase 2 oh-my-hermes-pig
	// can append omh_* sections cleanly).
	sections.Set(events.SectionIdentity, identity)
	sections.Set(events.SectionTools, toolsGuidance)
	sections.Set(events.SectionContext, contextBlock)
	sections.Set(events.SectionSkills, skillsBlock)
	sections.Set(events.SectionMemory, memoryBlock)
	sections.Set(events.SectionEnvironment, envBlock)
}

func extractPromptSections(data map[string]any) *sdk.SystemPromptSections {
	if data == nil {
		return nil
	}
	opts, ok := data["systemPromptOptions"].(map[string]any)
	if !ok || opts == nil {
		return nil
	}
	if sec, ok := opts["sections"].(*sdk.SystemPromptSections); ok && sec != nil {
		return sec
	}
	if sec, ok := opts["sections"].(sdk.SystemPromptSections); ok {
		cp := sec
		opts["sections"] = &cp
		return &cp
	}
	sec := &sdk.SystemPromptSections{}
	opts["sections"] = sec
	return sec
}

func extractUserPrompt(data map[string]any) string {
	if data == nil {
		return ""
	}
	if s, ok := data["prompt"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// buildIdentitySection loads SOUL.md from profile home if present, falling back to DEFAULT_AGENT_IDENTITY.
func buildIdentitySection(soulFile string) string {
	identity := defaultAgentIdentity
	if soulFile != "" {
		if raw, err := os.ReadFile(soulFile); err == nil {
			text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
			if text != "" {
				identity = truncateContextContent(text, "SOUL.md", soulFile, defaultContextFileMaxChars)
			}
		}
	}
	return identity + "\n\n" + hermesAgentHelpGuidance
}

func buildToolsGuidanceSection(cfg *config.Config) string {
	var parts []string
	if !isToolDisabled(cfg, "memory") {
		parts = append(parts, memoryGuidance)
	}
	if !isToolDisabled(cfg, "skill_manage") {
		parts = append(parts, skillsGuidance)
	}
	if !isToolDisabled(cfg, "session_search") {
		parts = append(parts, sessionSearchGuidance)
	}
	return strings.Join(parts, "\n\n")
}

// DiscoverProjectContextFiles discovers project context files strictly in Hermes's
// single-type priority order:
//  1. .hermes.md / HERMES.md (nearest match walking cwd -> git root)
//  2. AGENTS.override.md / AGENTS.md / agents.md (git root -> cwd directory chain)
//  3. CLAUDE.md / claude.md (cwd only)
//  4. .cursorrules + .cursor/rules/*.mdc (cwd only)
//
// Only the FIRST non-empty type wins; lower-priority types are shadowed.
func DiscoverProjectContextFiles(cwd string) []ContextFile {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}

	if files := findHermesMD(absCwd); len(files) > 0 {
		return files
	}
	if files := findAgentsMDChain(absCwd); len(files) > 0 {
		return files
	}
	if files := findClaudeMD(absCwd); len(files) > 0 {
		return files
	}
	if files := findCursorRules(absCwd); len(files) > 0 {
		return files
	}
	return nil
}

func findHermesMD(absCwd string) []ContextFile {
	gitRoot := findGitRoot(absCwd)
	dirs := []string{absCwd}
	if gitRoot != "" {
		dirs = walkUpToRoot(absCwd, gitRoot)
	}
	for _, dir := range dirs {
		for _, name := range []string{".hermes.md", "HERMES.md"} {
			candidate := filepath.Join(dir, name)
			content := readNonEmptyFile(candidate)
			if content != "" {
				content = stripYAMLFrontmatter(content)
				label := relOrBase(absCwd, candidate)
				return []ContextFile{{
					Kind:    "hermes_md",
					Label:   label,
					Path:    candidate,
					Content: content,
				}}
			}
		}
	}
	return nil
}

func findAgentsMDChain(absCwd string) []ContextFile {
	gitRoot := findGitRoot(absCwd)
	var chain []string
	if gitRoot == "" || gitRoot == absCwd {
		chain = []string{absCwd}
	} else {
		// Build git root -> cwd chain (shallowest to deepest).
		up := walkUpToRoot(absCwd, gitRoot)
		chain = make([]string, len(up))
		for i := range up {
			chain[i] = up[len(up)-1-i]
		}
	}

	var out []ContextFile
	seenContent := make(map[string]bool)
	for _, dir := range chain {
		for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "agents.md"} {
			candidate := filepath.Join(dir, name)
			content := readNonEmptyFile(candidate)
			if content == "" {
				continue
			}
			if !seenContent[content] {
				seenContent[content] = true
				out = append(out, ContextFile{
					Kind:    "agents_md",
					Label:   relOrBase(absCwd, candidate),
					Path:    candidate,
					Content: content,
				})
			}
			break // first non-empty match wins per directory
		}
	}
	return out
}

func findClaudeMD(absCwd string) []ContextFile {
	for _, name := range []string{"CLAUDE.md", "claude.md"} {
		candidate := filepath.Join(absCwd, name)
		content := readNonEmptyFile(candidate)
		if content != "" {
			return []ContextFile{{
				Kind:    "claude_md",
				Label:   name,
				Path:    candidate,
				Content: content,
			}}
		}
	}
	return nil
}

func findCursorRules(absCwd string) []ContextFile {
	var out []ContextFile
	crPath := filepath.Join(absCwd, ".cursorrules")
	if content := readNonEmptyFile(crPath); content != "" {
		out = append(out, ContextFile{
			Kind:    "cursorrules",
			Label:   ".cursorrules",
			Path:    crPath,
			Content: content,
		})
	}
	rulesDir := filepath.Join(absCwd, ".cursor", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err == nil {
		var mdcNames []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mdc") {
				mdcNames = append(mdcNames, e.Name())
			}
		}
		sort.Strings(mdcNames)
		for _, name := range mdcNames {
			p := filepath.Join(rulesDir, name)
			if content := readNonEmptyFile(p); content != "" {
				out = append(out, ContextFile{
					Kind:    "cursorrules",
					Label:   ".cursor/rules/" + name,
					Path:    p,
					Content: content,
				})
			}
		}
	}
	return out
}

func buildContextSection(cwd string) string {
	files := DiscoverProjectContextFiles(cwd)
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Project Context\n\nThe following project context files have been loaded and should be followed:\n\n")
	for i, cf := range files {
		if i > 0 {
			b.WriteString("\n\n")
		}
		body := fmt.Sprintf("## %s\n\n%s", cf.Label, cf.Content)
		b.WriteString(truncateContextContent(body, cf.Label, cf.Path, defaultContextFileMaxChars))
	}
	return b.String()
}

func buildSkillsSection(skills []SkillMeta) string {
	if len(skills) == 0 {
		return ""
	}
	byCat := make(map[string][]SkillMeta)
	for _, s := range skills {
		cat := s.Category
		if cat == "" {
			cat = "general"
		}
		byCat[cat] = append(byCat[cat], s)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	var lines []string
	for _, cat := range cats {
		entries := byCat[cat]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		indent := "  "
		if len(entries) > 1 {
			lines = append(lines, fmt.Sprintf("  %s:", cat))
			indent = "    "
		}
		for _, s := range entries {
			desc := truncateDesc(s.Description, defaultDescLimit)
			if s.Source == "project" {
				desc = strings.TrimSpace("[project] " + desc)
			}
			if desc != "" {
				lines = append(lines, fmt.Sprintf("%s- %s: %s", indent, s.Name, desc))
			} else {
				lines = append(lines, fmt.Sprintf("%s- %s", indent, s.Name))
			}
		}
	}

	var b strings.Builder
	b.WriteString("## Skills\n")
	b.WriteString("Before replying, scan the skills below. If a skill matches or is even partially relevant to your task, ")
	b.WriteString("load it with skill_view(name) and follow its instructions.\n")
	b.WriteString("If a skill has issues, fix it with skill_manage(action='patch').\n\n")
	b.WriteString("<available_skills>\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n</available_skills>\n\n")
	b.WriteString("Only proceed without loading a skill if genuinely none are relevant to the task.")
	return b.String()
}

func buildMemorySection(ctx context.Context, snap memory.Snapshot, memStore *memory.Store, sessionID, query string) string {
	var parts []string
	if formatted := memory.FormatForPrompt(snap); formatted != "" {
		parts = append(parts, formatted)
	}

	if memStore != nil {
		providers := memStore.Providers()
		for _, prov := range providers {
			pCtx, cancel := context.WithTimeout(ctx, providerPrefetchTimeout)
			recalled, err := prov.Prefetch(pCtx, sessionID, query)
			cancel()
			recalled = strings.TrimSpace(recalled)
			if err == nil && recalled != "" {
				fenced := fmt.Sprintf("<memory-context provider=%q>\n[System note: The following is recalled memory context, NOT new user input.]\n\n%s\n</memory-context>", prov.Name(), recalled)
				parts = append(parts, fenced)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func buildEnvironmentSection(p paths.ProfilePaths, cwd string, startedAt time.Time) string {
	var lines []string
	profileName := p.ProfileName
	if profileName == "" {
		profileName = "default"
	}
	lines = append(lines, fmt.Sprintf("Active Hermes profile: %s (%s)", profileName, p.Home))
	lines = append(lines, fmt.Sprintf("Conversation started: %s", startedAt.Format("Monday, January 02, 2006 (MST, -07:00)")))
	lines = append(lines, fmt.Sprintf("Host: %s/%s", runtime.GOOS, runtime.GOARCH))
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		lines = append(lines, fmt.Sprintf("User home directory: %s", home))
	}
	if cwd != "" {
		lines = append(lines, fmt.Sprintf("Current working directory: %s", cwd))
	}
	return "# Hermes runtime environment\n\n" + strings.Join(lines, "\n") + "\n\n<!-- End Hermes runtime environment -->"
}

func walkUpToRoot(start, root string) []string {
	var dirs []string
	cur := filepath.Clean(start)
	rootClean := filepath.Clean(root)
	for {
		dirs = append(dirs, cur)
		if cur == rootClean {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return dirs
}

func readNonEmptyFile(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
}

func stripYAMLFrontmatter(content string) string {
	clean := strings.TrimPrefix(content, "\ufeff")
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	if !strings.HasPrefix(clean, "---") {
		return content
	}
	idx := strings.Index(clean[3:], "\n---")
	if idx < 0 {
		return content
	}
	body := strings.TrimLeft(clean[3+idx+4:], "\n")
	if body == "" {
		return content
	}
	return body
}

func relOrBase(baseDir, target string) string {
	if rel, err := filepath.Rel(baseDir, target); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(target)
}

func truncateContextContent(content, filename, readPath string, maxChars int) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	headChars := (maxChars * 70) / 100
	tailChars := (maxChars * 20) / 100
	if headChars+tailChars >= len(content) {
		return content
	}
	marker := fmt.Sprintf(
		"\n\n[...truncated %s: kept %d+%d of %d chars. The middle is omitted. If you need the full instructions, read the complete file: %s]\n\n",
		filename, headChars, tailChars, len(content), readPath,
	)
	return content[:headChars] + marker + content[len(content)-tailChars:]
}

func truncateDesc(desc string, limit int) string {
	desc = strings.TrimSpace(desc)
	if limit <= 0 || len(desc) <= limit {
		return desc
	}
	if limit <= 3 {
		return desc[:limit]
	}
	return strings.TrimSpace(desc[:limit-3]) + "..."
}
