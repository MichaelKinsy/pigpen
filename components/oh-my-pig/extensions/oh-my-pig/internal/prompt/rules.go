package prompt

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

//go:embed builtin-rules/*.md
var builtinRulesFS embed.FS

// BuiltinDefaultsProviderID matches omp's capability/rule.ts BUILTIN_DEFAULTS_PROVIDER_ID.
const BuiltinDefaultsProviderID = "builtin-defaults"

// RuleDiscoveryOptions controls rule discovery and bucketing.
// upstream: packages/coding-agent/src/capability/rule-buckets.ts
type RuleDiscoveryOptions struct {
	// BuiltinRules controls whether the 26 bundled default rules are included.
	// Nil defaults to true (matching omp's ttsr.builtinRules default).
	BuiltinRules *bool
	// DisabledRules lists rule names to drop before bucketing.
	DisabledRules []string
}

// DiscoveredRule is a single parsed rule from disk or the embedded builtin set.
// upstream: packages/coding-agent/src/capability/rule.ts Rule
type DiscoveredRule struct {
	Name        string
	Description string
	Path        string
	Content     string
	Globs       []string
	AlwaysApply bool
	HasTTSR     bool
	Builtin     bool
}

// DiscoveredRules holds the bucketed result of DiscoverRules.
// upstream: packages/coding-agent/src/capability/rule-buckets.ts RuleBuckets
type DiscoveredRules struct {
	// Rulebook holds domain-scoped rules rendered under <domain-rules>.
	Rulebook []Rule
	// AlwaysApply holds always-apply rules rendered under <generic-rules>.
	AlwaysApply []AlwaysApplyRule
	// All holds all non-disabled rules addressable via rule://<name>, ordered
	// as [rulebook..., alwaysApply..., ttsr...] to match omp's setActiveRules.
	All []urls.RuleEntry
}

// RuleOptions supplies rule-discovery settings (ttsr.builtinRules, ttsr.disabledRules)
// for ctx. Nil by default (defaults to BuiltinRules=true, no disabled rules).
var RuleOptions func(ctx sdk.Context) RuleDiscoveryOptions

// DiscoverRulesForContext discovers all addressable rules for a session context,
// suitable for wiring into urls.SetRulesProvider.
func DiscoverRulesForContext(ctx sdk.Context, cwd string) []urls.RuleEntry {
	agentDir := ""
	if cfgHome, err := ctx.ConfigHome(); err == nil && cfgHome != "" {
		agentDir = filepath.Join(cfgHome, "agent")
	}
	if cwd == "" {
		cwd = ctx.Cwd()
	}
	var opts RuleDiscoveryOptions
	if RuleOptions != nil {
		opts = RuleOptions(ctx)
	}
	return DiscoverRules(cwd, agentDir, "", opts).All
}

// DiscoverRules discovers rules from project (.agents/rules, .agent/rules,
// .omp/rules, .pig/rules), user (~/.agents/rules, ~/.agent/rules, <agentDir>/rules),
// and the 26 embedded bundled defaults (lowest priority, first-wins dedup by name),
// then applies disabledRules / builtinRules filtering and bucketing.
//
// When PI_NO_RULES=1 is set in the environment, all rule discovery is skipped
// (matching omp's --no-rules flag).
//
// upstream: packages/coding-agent/src/discovery/{agents,builtin,builtin-defaults}.ts
// upstream: packages/coding-agent/src/capability/rule-buckets.ts
func DiscoverRules(cwd, agentDir, home string, opts RuleDiscoveryOptions) DiscoveredRules {
	if os.Getenv("PI_NO_RULES") == "1" {
		return DiscoveredRules{}
	}

	includeBuiltin := opts.BuiltinRules == nil || *opts.BuiltinRules
	disabled := make(map[string]bool, len(opts.DisabledRules))
	for _, raw := range opts.DisabledRules {
		if name := strings.TrimSpace(raw); name != "" {
			disabled[name] = true
		}
	}

	seen := make(map[string]bool)
	var collected []DiscoveredRule
	add := func(r DiscoveredRule) {
		if r.Name == "" || seen[r.Name] {
			return
		}
		seen[r.Name] = true
		collected = append(collected, r)
	}

	if home == "" {
		home, _ = os.UserHomeDir()
	}

	// 1. Project rules (walk-up from cwd to git root / home ceiling, closest first).
	if cwd != "" {
		for _, dir := range projectRuleDirs(cwd, home) {
			for _, r := range loadRulesFromDir(dir) {
				add(r)
			}
		}
	}

	// 2. User rules (~/.agents/rules, ~/.agent/rules, <agentDir>/rules).
	for _, dir := range userRuleDirs(agentDir, home) {
		for _, r := range loadRulesFromDir(dir) {
			add(r)
		}
	}

	// 3. Bundled default rules (lowest priority).
	for _, r := range loadBuiltinRules() {
		add(r)
	}

	var rulebook []Rule
	var alwaysApply []AlwaysApplyRule
	var rulebookEntries []urls.RuleEntry
	var alwaysEntries []urls.RuleEntry
	var ttsrEntries []urls.RuleEntry

	for _, r := range collected {
		if disabled[r.Name] {
			continue
		}
		if !includeBuiltin && r.Builtin {
			continue
		}
		entry := urls.RuleEntry{
			Name:        r.Name,
			Description: r.Description,
			Path:        r.Path,
			Content:     r.Content,
		}
		if r.HasTTSR {
			ttsrEntries = append(ttsrEntries, entry)
			continue
		}
		if r.AlwaysApply {
			alwaysApply = append(alwaysApply, AlwaysApplyRule{
				Name:    r.Name,
				Content: r.Content,
				Path:    r.Path,
			})
			alwaysEntries = append(alwaysEntries, entry)
			continue
		}
		if r.Description != "" {
			rulebook = append(rulebook, Rule{
				Name:        r.Name,
				Description: r.Description,
				Path:        r.Path,
				Globs:       r.Globs,
			})
			rulebookEntries = append(rulebookEntries, entry)
		}
	}

	all := make([]urls.RuleEntry, 0, len(rulebookEntries)+len(alwaysEntries)+len(ttsrEntries))
	all = append(all, rulebookEntries...)
	all = append(all, alwaysEntries...)
	all = append(all, ttsrEntries...)

	return DiscoveredRules{
		Rulebook:    rulebook,
		AlwaysApply: alwaysApply,
		All:         all,
	}
}

func projectRuleDirs(cwd, home string) []string {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	absHome := ""
	if home != "" {
		absHome, _ = filepath.Abs(home)
	}
	stopAt := findGitRoot(absCwd)
	if stopAt == "" {
		stopAt = absHome
	}
	ceilings := projectDiscoveryCeilings(absHome)

	var dirs []string
	cur := absCwd
	for {
		if ceilings[pathKey(cur)] {
			break
		}
		if absHome == "" || !sameCleanPath(cur, absHome) {
			for _, rel := range []string{".agents/rules", ".agent/rules", ".omp/rules", ".pig/rules"} {
				cand := filepath.Join(cur, rel)
				if info, err := os.Stat(cand); err == nil && info.IsDir() {
					dirs = append(dirs, cand)
				}
			}
		}
		if stopAt != "" && sameCleanPath(cur, stopAt) {
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

func userRuleDirs(agentDir, home string) []string {
	var dirs []string
	seen := make(map[string]bool)
	addDir := func(d string) {
		if d == "" {
			return
		}
		k := pathKey(d)
		if seen[k] {
			return
		}
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			seen[k] = true
			dirs = append(dirs, d)
		}
	}
	if home != "" {
		addDir(filepath.Join(home, ".agents", "rules"))
		addDir(filepath.Join(home, ".agent", "rules"))
	}
	if agentDir != "" {
		addDir(filepath.Join(agentDir, "rules"))
	}
	return dirs
}

func loadRulesFromDir(dir string) []DiscoveredRule {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var out []DiscoveredRule
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".mdc" {
			continue
		}
		fullPath := filepath.Join(dir, name)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		ruleName := strings.TrimSuffix(name, filepath.Ext(name))
		r, ok := parseRuleMarkdown(ruleName, string(data), fullPath, false)
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func loadBuiltinRules() []DiscoveredRule {
	entries, err := builtinRulesFS.ReadDir("builtin-rules")
	if err != nil {
		return nil
	}
	var out []DiscoveredRule
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := builtinRulesFS.ReadFile("builtin-rules/" + e.Name())
		if err != nil {
			continue
		}
		ruleName := strings.TrimSuffix(e.Name(), ".md")
		virtualPath := BuiltinDefaultsProviderID + ":" + ruleName + ".md"
		r, _ := parseRuleMarkdown(ruleName, string(data), virtualPath, true)
		out = append(out, r)
	}
	return out
}

// parseRuleMarkdown parses YAML frontmatter and body from a rule markdown file.
// Returns (_, false) when frontmatter explicitly sets `enabled: false` (for non-builtin rules).
func parseRuleMarkdown(name, raw, filePath string, builtin bool) (DiscoveredRule, bool) {
	fm, body := splitRuleFrontmatter(raw)
	if !builtin && fm.disabled {
		return DiscoveredRule{}, false
	}
	return DiscoveredRule{
		Name:        name,
		Description: fm.description,
		Path:        filePath,
		Content:     body,
		Globs:       fm.globs,
		AlwaysApply: fm.alwaysApply,
		HasTTSR:     fm.hasTTSR,
		Builtin:     builtin,
	}, true
}

type parsedRuleFrontmatter struct {
	disabled    bool
	description string
	globs       []string
	alwaysApply bool
	hasTTSR     bool
}

func splitRuleFrontmatter(raw string) (parsedRuleFrontmatter, string) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return parsedRuleFrontmatter{}, strings.TrimSpace(normalized)
	}
	rest := normalized[4:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return parsedRuleFrontmatter{}, strings.TrimSpace(normalized)
	}
	fmBlock := rest[:endIdx]
	after := rest[endIdx+4:]
	if strings.HasPrefix(after, "\n") {
		after = after[1:]
	}
	body := strings.TrimLeft(after, "\n")

	var fm parsedRuleFrontmatter
	lines := strings.Split(fmBlock, "\n")
	currentListKey := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentListKey != "" {
			val := unquoteYAML(strings.TrimSpace(trimmed[2:]))
			if val != "" {
				switch currentListKey {
				case "globs":
					fm.globs = append(fm.globs, val)
				case "condition", "astCondition":
					fm.hasTTSR = true
				}
			}
			continue
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			currentListKey = ""
			continue
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := strings.TrimSpace(trimmed[colon+1:])
		currentListKey = ""

		switch key {
		case "enabled":
			if strings.EqualFold(val, "false") {
				fm.disabled = true
			}
		case "description":
			fm.description = unquoteYAML(val)
		case "alwaysApply":
			if strings.EqualFold(val, "true") {
				fm.alwaysApply = true
			}
		case "globs":
			if val == "" {
				currentListKey = "globs"
			} else if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				for _, item := range strings.Split(val[1:len(val)-1], ",") {
					if g := unquoteYAML(strings.TrimSpace(item)); g != "" {
						fm.globs = append(fm.globs, g)
					}
				}
			} else if g := unquoteYAML(val); g != "" {
				fm.globs = append(fm.globs, g)
			}
		case "condition", "astCondition":
			if val == "" {
				currentListKey = key
			} else if unquoteYAML(val) != "" && val != "[]" {
				fm.hasTTSR = true
			}
		case "question":
			if unquoteYAML(val) != "" {
				fm.hasTTSR = true
			}
		}
	}
	return fm, body
}

func unquoteYAML(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
