// Package surface implements Hermes's CLI flags, CLI subcommands, interactive slash commands,
// TUI skins/branding, status bar, and compact tool renderers for pig-hermes.
package surface

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
	"github.com/MichaelKinsy/pigpen/pig-hermes/agents"
)

const statusMessageType = "hermes-surface"

type state struct {
	mu       sync.RWMutex
	paths    paths.ProfilePaths
	cfg      *config.Config
	memStore *memory.Store
	skin     string
	toolset  string
	maxTurns int
	turns    int
}

func newState(p paths.ProfilePaths, cfg *config.Config, memStore *memory.Store) *state {
	if cfg == nil {
		cfg = config.Default()
	}
	if p.Home == "" {
		p = paths.ResolveProfilePaths("")
	}
	if memStore == nil {
		memStore = memory.NewStore(p.MemoriesDir, cfg.MemoryLimit, cfg.UserLimit)
	}
	skin := cfg.Skin
	if _, ok := GetSkin(skin); !ok {
		skin = "default"
	}
	toolset := cfg.Toolset
	if strings.TrimSpace(toolset) == "" {
		toolset = "hermes-cli"
	}
	return &state{
		paths:    p,
		cfg:      cfg,
		memStore: memStore,
		skin:     skin,
		toolset:  toolset,
	}
}

func (s *state) currentSkin() Skin {
	s.mu.RLock()
	name := s.skin
	s.mu.RUnlock()
	sk, ok := GetSkin(name)
	if !ok {
		sk, _ = GetSkin("default")
	}
	return sk
}

func (s *state) memPercent() int {
	_, used, limit, err := s.memStore.ReadLive(memory.TargetMemory)
	if err != nil {
		return 0
	}
	return calcPercent(used, limit)
}

func (s *state) updateStatusBar(ctx sdk.Context) {
	s.mu.RLock()
	profile := s.paths.ProfileName
	skin := s.skin
	s.mu.RUnlock()
	ctx.SetStatus("hermes", FormatStatusText(profile, skin, s.memPercent(), s.cfg.IsYolo()))
}

func (s *state) show(ctx sdk.Context, text string) {
	no := false
	if err := ctx.SendMessage(statusMessageType, text, true, sdk.SendMessageOptions{TriggerTurn: &no}); err != nil {
		ctx.Notify(text, "info")
	}
}

// Register wires Hermes CLI flags, CLI subcommands, interactive slash commands,
// TUI skin/status bar lifecycle hooks, and compact tree-style tool renderers.
func Register(ext *sdk.Extension, p paths.ProfilePaths, cfg *config.Config, memStore *memory.Store) {
	st := newState(p, cfg, memStore)

	// 1. CLI flags
	ext.Flag("profile", sdk.FlagOptions{
		Description: "Hermes profile name (isolates ~/.hermes/profiles/<name>)",
		Type:        sdk.FlagString,
		Default:     st.paths.ProfileName,
	})
	ext.Flag("toolsets", sdk.FlagOptions{
		Description: "Comma-separated Hermes toolsets to enable (default: hermes-cli)",
		Type:        sdk.FlagString,
		Default:     st.toolset,
	})
	ext.Flag("skin", sdk.FlagOptions{
		Description: "Hermes TUI display skin (default, ares, mono, slate, daylight, warm-lightmode, poseidon, sisyphus, charizard)",
		Type:        sdk.FlagString,
		Default:     st.skin,
	})
	ext.Flag("yolo", sdk.FlagOptions{
		Description: "Bypass dangerous command approvals for this session",
		Type:        sdk.FlagBoolean,
		Default:     st.cfg.IsYolo(),
	})
	ext.Flag("max-turns", sdk.FlagOptions{
		Description: "Maximum agent turns for this session (integer)",
		Type:        sdk.FlagString,
		Default:     "",
	})

	// 2. CLI subcommands & -p/--profile flag preprocessor
	registerCLI()

	// 3. Interactive slash commands (avoiding collisions with PiG built-ins)
	registerSlashCommands(ext, st)

	// 4. Lifecycle hooks & Hermes compact tool renderers
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		st.applySessionFlags(ctx)
		st.updateStatusBar(ctx)
		return nil, nil
	})
	ext.OnEvent("agent_start", func(_ sdk.Context, _ map[string]any) (any, error) {
		st.mu.Lock()
		st.turns = 0
		st.mu.Unlock()
		return nil, nil
	})
	ext.OnEvent("turn_end", func(ctx sdk.Context, _ map[string]any) (any, error) {
		st.mu.Lock()
		st.turns++
		turns := st.turns
		maxTurns := st.maxTurns
		st.mu.Unlock()
		st.updateStatusBar(ctx)
		if maxTurns > 0 && turns >= maxTurns {
			ctx.Notify(fmt.Sprintf("Hermes max-turns limit reached (%d/%d); aborting.", turns, maxTurns), "warning")
			ctx.Abort()
		}
		return nil, nil
	})

	ext.ToolRenderer(st.toolRendererResolver)
}

func (s *state) applySessionFlags(ctx sdk.Context) {
	if raw, err := ctx.GetFlag("skin"); err == nil {
		if name, ok := raw.(string); ok && strings.TrimSpace(name) != "" {
			if sk, valid := GetSkin(name); valid {
				s.mu.Lock()
				s.skin = sk.Name
				s.mu.Unlock()
			}
		}
	}
	if raw, err := ctx.GetFlag("toolsets"); err == nil {
		if ts, ok := raw.(string); ok && strings.TrimSpace(ts) != "" {
			trimmed := strings.TrimSpace(ts)
			if resolved := ResolveToolsetTools(trimmed); len(resolved) > 0 {
				s.mu.Lock()
				s.toolset = trimmed
				s.mu.Unlock()
				// Only restrict active tools when the user explicitly passed a non-default toolset,
				// or preserve any sibling extension tools (e.g. omh_*, mcp__*) registered in derived Piglets.
				if trimmed != "hermes-cli" {
					ctx.SetActiveTools(mergeSiblingExtensionTools(ctx, resolved))
				}
			}
		}
	}
	if raw, err := ctx.GetFlag("yolo"); err == nil {
		if b, ok := raw.(bool); ok && b {
			s.cfg.SetYolo(true)
		}
	}
	if raw, err := ctx.GetFlag("max-turns"); err == nil {
		switch v := raw.(type) {
		case string:
			if n, convErr := strconv.Atoi(strings.TrimSpace(v)); convErr == nil && n > 0 {
				s.mu.Lock()
				s.maxTurns = n
				s.mu.Unlock()
			}
		case float64:
			if int(v) > 0 {
				s.mu.Lock()
				s.maxTurns = int(v)
				s.mu.Unlock()
			}
		case int:
			if v > 0 {
				s.mu.Lock()
				s.maxTurns = v
				s.mu.Unlock()
			}
		}
	}
}

func (s *state) toolRendererResolver(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
	if !isHermesTool(toolName) {
		return next()
	}
	return &sdk.ToolRenderers{
		Shell: sdk.ToolRenderShellDefault,
		Call: func(_ sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
			sk := s.currentSkin()
			summary := summarizeToolArgs(args)
			line := fmt.Sprintf("%s ├─ %s %s", sk.ToolPrefix, toolName, summary)
			return []string{truncateLine(strings.TrimSpace(line), width)}, nil
		},
		Result: func(_ sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			sk := s.currentSkin()
			prefix := sk.ToolPrefix + " └─ "
			if opts.IsPartial {
				return []string{truncateLine(prefix+"running...", width)}, nil
			}
			text := firstResultLine(result)
			if render.IsError {
				line := prefix + "error: " + text
				return []string{truncateLine(line, width)}, nil
			}
			if render.DurationMs != nil && *render.DurationMs > 0 {
				dur := fmt.Sprintf(" (%.1fs)", float64(*render.DurationMs)/1000.0)
				return []string{truncateLine(prefix+text+dur, width)}, nil
			}
			return []string{truncateLine(prefix+text, width)}, nil
		},
	}
}

func summarizeToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(len(keys), 3))
	for _, k := range keys {
		if len(parts) >= 3 {
			break
		}
		v := fmt.Sprintf("%v", args[k])
		v = strings.ReplaceAll(v, "\n", " ")
		if len(v) > 40 {
			v = v[:37] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, " ")
}

func firstResultLine(result sdk.ToolRenderResult) string {
	for _, block := range result.Content {
		if t, ok := block["text"].(string); ok && strings.TrimSpace(t) != "" {
			line := strings.SplitN(strings.TrimSpace(t), "\n", 2)[0]
			return line
		}
	}
	return "done"
}

func truncateLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// ── Slash Commands ───────────────────────────────────────────────────────────

func registerSlashCommands(ext *sdk.Extension, st *state) {
	ext.RegisterCommand("memory", sdk.CommandOptions{
		Description: "Show, add, or clear bounded MEMORY.md & USER.md gauges",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			return filterCompletions(prefix, []sdk.AutocompleteItem{
				{Value: "show", Description: "Show MEMORY.md and USER.md gauges"},
				{Value: "add ", Description: "Add an entry to memory or user profile"},
				{Value: "clear memory", Description: "Clear all MEMORY.md entries"},
				{Value: "clear user", Description: "Clear all USER.md entries"},
			}), nil
		},
		Handler: st.cmdMemory,
	})

	ext.RegisterCommand("skills", sdk.CommandOptions{
		Description: "List, search, or inspect discovered Hermes skills",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			return filterCompletions(prefix, []sdk.AutocompleteItem{
				{Value: "list", Description: "List discovered skills"},
				{Value: "search ", Description: "Search skills by keyword"},
				{Value: "inspect ", Description: "View full SKILL.md"},
			}), nil
		},
		Handler: st.cmdSkills,
	})

	ext.RegisterCommand("profile", sdk.CommandOptions{
		Description: "Show active Hermes profile and list available profiles",
		Handler:     st.cmdProfile,
	})

	ext.RegisterCommand("skin", sdk.CommandOptions{
		Description: "Show or switch among Hermes's 9 built-in skins",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			items := make([]sdk.AutocompleteItem, 0, len(builtinSkinOrder))
			for _, name := range builtinSkinOrder {
				sk, _ := GetSkin(name)
				items = append(items, sdk.AutocompleteItem{Value: name, Description: sk.Description})
			}
			return filterCompletions(prefix, items), nil
		},
		Handler: st.cmdSkin,
	})

	ext.RegisterCommand("toolsets", sdk.CommandOptions{
		Description: "List active Hermes toolset and enabled tools",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			all := BuiltinToolsets()
			items := make([]sdk.AutocompleteItem, 0, len(all))
			for _, ts := range all {
				items = append(items, sdk.AutocompleteItem{Value: ts.Name, Description: ts.Description})
			}
			return filterCompletions(prefix, items), nil
		},
		Handler: st.cmdToolsets,
	})

	ext.RegisterCommand("tools", sdk.CommandOptions{
		Description: "List registered Hermes tools and counts",
		Handler:     st.cmdTools,
	})

	ext.RegisterCommand("cron", sdk.CommandOptions{
		Description: "List, pause, resume, or remove scheduled Hermes cron jobs",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			return filterCompletions(prefix, []sdk.AutocompleteItem{
				{Value: "list", Description: "List scheduled jobs"},
				{Value: "pause ", Description: "Pause a job by ID"},
				{Value: "resume ", Description: "Resume a job by ID"},
				{Value: "remove ", Description: "Remove a job by ID"},
			}), nil
		},
		Handler: st.cmdCron,
	})

	ext.RegisterCommand("usage", sdk.CommandOptions{
		Description: "Show session and child token, cost, and turn ledger",
		Handler:     st.cmdUsage,
	})

	ext.RegisterCommand("status", sdk.CommandOptions{
		Description: "Show Hermes session status (profile, skin, memory %, toolset, turns)",
		Handler:     st.cmdStatus,
	})

	ext.RegisterCommand("personality", sdk.CommandOptions{
		Description: "Inspect or set SOUL.md identity summary",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			return filterCompletions(prefix, []sdk.AutocompleteItem{
				{Value: "show", Description: "Show current SOUL.md"},
				{Value: "set ", Description: "Set SOUL.md identity text"},
				{Value: "clear", Description: "Remove SOUL.md"},
			}), nil
		},
		Handler: st.cmdPersonality,
	})

	ext.RegisterCommand("yolo", sdk.CommandOptions{
		Description: "Toggle approval bypass mode for the current session",
		Handler:     st.cmdYolo,
	})

	ext.RegisterCommand("compress", sdk.CommandOptions{
		Description: "Show context usage and PiG /compact alias guidance",
		Handler:     st.cmdCompress,
	})

	ext.RegisterCommand("todos", sdk.CommandOptions{
		Description: "Display current session todo checklist",
		Handler:     st.cmdTodos,
	})
}

func filterCompletions(prefix string, items []sdk.AutocompleteItem) []sdk.AutocompleteItem {
	p := strings.ToLower(strings.TrimSpace(prefix))
	if p == "" {
		return items
	}
	var out []sdk.AutocompleteItem
	for _, it := range items {
		if strings.HasPrefix(strings.ToLower(it.Value), p) {
			out = append(out, it)
		}
	}
	return out
}

func (s *state) cmdMemory(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	fields := strings.Fields(trimmed)
	sub := "show"
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}

	switch sub {
	case "show", "status", "":
		memEntries, memChars, memLimit, _ := s.memStore.ReadLive(memory.TargetMemory)
		userEntries, userChars, userLimit, _ := s.memStore.ReadLive(memory.TargetUser)
		var b strings.Builder
		fmt.Fprintf(&b, "Hermes Memory\n")
		fmt.Fprintf(&b, "  MEMORY.md: %d/%d chars (%d%%, %d entries)\n", memChars, memLimit, calcPercent(memChars, memLimit), len(memEntries))
		for i, e := range memEntries {
			fmt.Fprintf(&b, "    %d. %s\n", i+1, e)
		}
		fmt.Fprintf(&b, "  USER.md:   %d/%d chars (%d%%, %d entries)\n", userChars, userLimit, calcPercent(userChars, userLimit), len(userEntries))
		for i, e := range userEntries {
			fmt.Fprintf(&b, "    %d. %s\n", i+1, e)
		}
		s.show(ctx, strings.TrimRight(b.String(), "\n"))
		return nil

	case "add":
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
		if rest == "" {
			s.show(ctx, "usage: /memory add [memory|user] <entry text>")
			return nil
		}
		target := memory.TargetMemory
		restFields := strings.Fields(rest)
		if len(restFields) >= 2 && (strings.EqualFold(restFields[0], "memory") || strings.EqualFold(restFields[0], "user")) {
			if strings.EqualFold(restFields[0], "user") {
				target = memory.TargetUser
			}
			rest = strings.TrimSpace(strings.TrimPrefix(rest, restFields[0]))
		}
		res, err := s.memStore.Apply(context.Background(), target, []memory.Operation{
			{Action: "add", Content: rest},
		})
		if err != nil {
			s.show(ctx, fmt.Sprintf("memory add failed: %v", err))
			return nil
		}
		s.updateStatusBar(ctx)
		s.show(ctx, fmt.Sprintf("Added to %s (%d/%d chars, %d%%)", target, res.CharsUsed, res.CharsLimit, res.Percent))
		return nil

	case "clear":
		target := memory.TargetMemory
		if len(fields) >= 2 && strings.EqualFold(fields[1], "user") {
			target = memory.TargetUser
		}
		entries, _, _, err := s.memStore.ReadLive(target)
		if err != nil {
			s.show(ctx, fmt.Sprintf("memory clear failed: %v", err))
			return nil
		}
		if len(entries) == 0 {
			s.show(ctx, fmt.Sprintf("%s is already empty.", target))
			return nil
		}
		ops := make([]memory.Operation, 0, len(entries))
		for _, e := range entries {
			ops = append(ops, memory.Operation{Action: "remove", OldText: e})
		}
		res, err := s.memStore.Apply(context.Background(), target, ops)
		if err != nil {
			s.show(ctx, fmt.Sprintf("memory clear failed: %v", err))
			return nil
		}
		s.updateStatusBar(ctx)
		s.show(ctx, fmt.Sprintf("Cleared %s (%d/%d chars, %d%%)", target, res.CharsUsed, res.CharsLimit, res.Percent))
		return nil

	default:
		s.show(ctx, "usage: /memory [show | add [memory|user] <text> | clear [memory|user]]")
		return nil
	}
}

func (s *state) cmdSkills(ctx sdk.Context, args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	sub := "list"
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}
	skills := discoverSkills(s.paths.SkillsDir)

	switch sub {
	case "list", "ls", "":
		if len(skills) == 0 {
			s.show(ctx, fmt.Sprintf("No skills found in %s", s.paths.SkillsDir))
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Hermes Skills (%d)\n", len(skills))
		for _, sk := range skills {
			if sk.Description != "" {
				fmt.Fprintf(&b, "  - %s: %s\n", sk.Name, sk.Description)
			} else {
				fmt.Fprintf(&b, "  - %s\n", sk.Name)
			}
		}
		s.show(ctx, strings.TrimRight(b.String(), "\n"))
		return nil

	case "search":
		if len(fields) < 2 {
			s.show(ctx, "usage: /skills search <query>")
			return nil
		}
		q := strings.ToLower(strings.Join(fields[1:], " "))
		var matches []discoveredSkill
		for _, sk := range skills {
			if strings.Contains(strings.ToLower(sk.Name), q) || strings.Contains(strings.ToLower(sk.Description), q) {
				matches = append(matches, sk)
			}
		}
		if len(matches) == 0 {
			s.show(ctx, fmt.Sprintf("No skills matching %q", q))
			return nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Matching Skills (%d)\n", len(matches))
		for _, sk := range matches {
			fmt.Fprintf(&b, "  - %s: %s\n", sk.Name, sk.Description)
		}
		s.show(ctx, strings.TrimRight(b.String(), "\n"))
		return nil

	case "inspect", "view":
		if len(fields) < 2 {
			s.show(ctx, "usage: /skills inspect <name>")
			return nil
		}
		target := fields[1]
		for _, sk := range skills {
			if strings.EqualFold(sk.Name, target) {
				raw, err := os.ReadFile(sk.Path)
				if err != nil {
					s.show(ctx, fmt.Sprintf("failed to read skill %q: %v", target, err))
					return nil
				}
				s.show(ctx, string(raw))
				return nil
			}
		}
		s.show(ctx, fmt.Sprintf("Skill %q not found.", target))
		return nil

	default:
		s.show(ctx, "usage: /skills [list | search <query> | inspect <name>]")
		return nil
	}
}

func (s *state) cmdProfile(ctx sdk.Context, _ string) error {
	names := listProfileNames(s.paths)
	var b strings.Builder
	fmt.Fprintf(&b, "Active Profile: %s\n", s.paths.ProfileName)
	fmt.Fprintf(&b, "HERMES_HOME:    %s\n", s.paths.Home)
	fmt.Fprintf(&b, "Available Profiles:\n")
	for _, n := range names {
		marker := " "
		if n == s.paths.ProfileName {
			marker = "*"
		}
		fmt.Fprintf(&b, "  %s %s\n", marker, n)
	}
	s.show(ctx, strings.TrimRight(b.String(), "\n"))
	return nil
}

func (s *state) cmdSkin(ctx sdk.Context, args string) error {
	target := strings.TrimSpace(args)
	if target == "" || strings.EqualFold(target, "list") {
		cur := s.currentSkin()
		var b strings.Builder
		fmt.Fprintf(&b, "Active Skin: %s (%s)\n", cur.Name, cur.Description)
		fmt.Fprintf(&b, "Built-in Skins (%d):\n", len(builtinSkinOrder))
		for _, name := range builtinSkinOrder {
			sk, _ := GetSkin(name)
			marker := " "
			if name == cur.Name {
				marker = "*"
			}
			fmt.Fprintf(&b, "  %s %-15s %s [%s / %s]\n",
				marker, sk.Name, sk.Description, sk.Color("banner_title", ""), sk.Color("banner_accent", ""))
		}
		s.show(ctx, strings.TrimRight(b.String(), "\n"))
		return nil
	}

	sk, ok := GetSkin(target)
	if !ok {
		s.show(ctx, fmt.Sprintf("Unknown skin %q. Available: %s", target, strings.Join(BuiltinSkinNames(), ", ")))
		return nil
	}
	s.mu.Lock()
	s.skin = sk.Name
	s.mu.Unlock()

	_, _ = ctx.SetTheme(sk.Name)
	_ = config.SetKey(s.paths.ConfigFile, "display.skin", sk.Name)
	s.updateStatusBar(ctx)
	s.show(ctx, fmt.Sprintf("Switched skin to %s (%s)", sk.Name, sk.Description))
	return nil
}

func (s *state) cmdToolsets(ctx sdk.Context, args string) error {
	target := strings.TrimSpace(args)
	if target != "" && !strings.EqualFold(target, "list") {
		resolved := ResolveToolsetTools(target)
		if len(resolved) == 0 {
			s.show(ctx, fmt.Sprintf("Unknown toolset %q.", target))
			return nil
		}
		s.mu.Lock()
		s.toolset = target
		s.mu.Unlock()
		ctx.SetActiveTools(mergeSiblingExtensionTools(ctx, resolved))
		s.show(ctx, fmt.Sprintf("Active toolset set to %s (%d tools): %s", target, len(resolved), strings.Join(resolved, ", ")))
		return nil
	}

	s.mu.RLock()
	active := s.toolset
	s.mu.RUnlock()
	enabled := ResolveToolsetTools(active)

	var b strings.Builder
	fmt.Fprintf(&b, "Active Toolset: %s (%d tools)\n", active, len(enabled))
	fmt.Fprintf(&b, "Enabled Tools:  %s\n", strings.Join(enabled, ", "))
	fmt.Fprintf(&b, "Available Toolsets:\n")
	for _, ts := range BuiltinToolsets() {
		marker := " "
		if strings.EqualFold(ts.Name, active) {
			marker = "*"
		}
		fmt.Fprintf(&b, "  %s %-16s %s\n", marker, ts.Name, ts.Description)
	}
	s.show(ctx, strings.TrimRight(b.String(), "\n"))
	return nil
}

// mergeSiblingExtensionTools keeps non-Hermes tools registered by sibling extensions
// (such as oh-my-hermes's omh_* tools or MCP mcp__* tools) active when a Hermes toolset is selected.
func mergeSiblingExtensionTools(ctx sdk.Context, hermesTools []string) []string {
	allHermes := make(map[string]bool)
	for _, name := range ResolveToolsetTools("hermes-cli") {
		allHermes[name] = true
	}
	seen := make(map[string]bool, len(hermesTools))
	out := make([]string, 0, len(hermesTools))
	for _, name := range hermesTools {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if all, err := ctx.GetAllTools(); err == nil {
		for _, t := range all {
			if t.Name != "" && !allHermes[t.Name] && !seen[t.Name] {
				seen[t.Name] = true
				out = append(out, t.Name)
			}
		}
	}
	return out
}

func (s *state) cmdTools(ctx sdk.Context, _ string) error {
	allTools, err := ctx.GetAllTools()
	activeTools, _ := ctx.GetActiveTools()

	if err != nil || len(allTools) == 0 {
		s.mu.RLock()
		ts := s.toolset
		s.mu.RUnlock()
		resolved := ResolveToolsetTools(ts)
		s.show(ctx, fmt.Sprintf("Hermes Tools (%d in %s): %s", len(resolved), ts, strings.Join(resolved, ", ")))
		return nil
	}

	activeSet := make(map[string]bool, len(activeTools))
	for _, name := range activeTools {
		activeSet[name] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Registered Tools (%d active / %d total)\n", len(activeTools), len(allTools))
	for _, t := range allTools {
		mark := " "
		if activeSet[t.Name] {
			mark = "*"
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, t.Name)
	}
	s.show(ctx, strings.TrimRight(b.String(), "\n"))
	return nil
}

func (s *state) cmdCron(ctx sdk.Context, args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	sub := "list"
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}

	store := agents.NewCronStore(s.paths.CronJobsFile)
	switch sub {
	case "list", "ls", "":
		jobs, err := store.List(true)
		if err != nil {
			s.show(ctx, fmt.Sprintf("failed to load cron jobs: %v", err))
			return nil
		}
		s.show(ctx, formatCronJobs(jobs))
		return nil

	case "pause":
		if len(fields) < 2 {
			s.show(ctx, "usage: /cron pause <id>")
			return nil
		}
		id := fields[1]
		job, err := store.Pause(id)
		if err != nil {
			s.show(ctx, fmt.Sprintf("failed to pause cron job: %v", err))
			return nil
		}
		s.show(ctx, fmt.Sprintf("Paused cron job %s", job.ID))
		return nil

	case "resume":
		if len(fields) < 2 {
			s.show(ctx, "usage: /cron resume <id>")
			return nil
		}
		id := fields[1]
		job, err := store.Resume(id)
		if err != nil {
			s.show(ctx, fmt.Sprintf("failed to resume cron job: %v", err))
			return nil
		}
		s.show(ctx, fmt.Sprintf("Resumed cron job %s", job.ID))
		return nil

	case "remove", "rm", "delete":
		if len(fields) < 2 {
			s.show(ctx, "usage: /cron remove <id>")
			return nil
		}
		id := fields[1]
		job, err := store.Remove(id)
		if err != nil {
			s.show(ctx, fmt.Sprintf("failed to remove cron job: %v", err))
			return nil
		}
		s.show(ctx, fmt.Sprintf("Removed cron job %s", job.ID))
		return nil

	default:
		s.show(ctx, "usage: /cron [list | pause <id> | resume <id> | remove <id>]")
		return nil
	}
}

func (s *state) cmdUsage(ctx sdk.Context, _ string) error {
	ledger := usage.Default()
	inTok, outTok, cost, turns, toolCalls := ledger.Totals()
	records := ledger.Snapshot()
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID < records[j].SessionID })

	var b strings.Builder
	fmt.Fprintf(&b, "Hermes Usage Ledger\n")
	fmt.Fprintf(&b, "  Totals: %d in / %d out tokens · $%.4f · %d turns · %d tool calls\n",
		inTok, outTok, cost, turns, toolCalls)
	if len(records) == 0 {
		fmt.Fprintf(&b, "  (no session usage recorded yet)")
	} else {
		for _, r := range records {
			role := "session"
			if r.ParentID != "" {
				role = "child of " + r.ParentID
			}
			fmt.Fprintf(&b, "  - %s (%s, %s): %d in / %d out · $%.4f · %d turns\n",
				r.SessionID, role, r.Model, r.InputTokens, r.OutputTokens, r.Cost, r.Turns)
		}
	}
	s.show(ctx, strings.TrimRight(b.String(), "\n"))
	return nil
}

func (s *state) cmdStatus(ctx sdk.Context, _ string) error {
	s.mu.RLock()
	profile := s.paths.ProfileName
	home := s.paths.Home
	skin := s.skin
	toolset := s.toolset
	yolo := s.cfg.IsYolo()
	turns := s.turns
	maxTurns := s.maxTurns
	s.mu.RUnlock()

	_, memChars, memLimit, _ := s.memStore.ReadLive(memory.TargetMemory)
	_, userChars, userLimit, _ := s.memStore.ReadLive(memory.TargetUser)

	var b strings.Builder
	fmt.Fprintf(&b, "Hermes Session Status\n")
	fmt.Fprintf(&b, "  Profile:   %s (%s)\n", profile, home)
	fmt.Fprintf(&b, "  Skin:      %s\n", skin)
	fmt.Fprintf(&b, "  MEMORY.md: %d/%d (%d%%) | USER.md: %d/%d (%d%%)\n",
		memChars, memLimit, calcPercent(memChars, memLimit),
		userChars, userLimit, calcPercent(userChars, userLimit))
	fmt.Fprintf(&b, "  Toolset:   %s\n", toolset)
	if maxTurns > 0 {
		fmt.Fprintf(&b, "  Turns:     %d / %d\n", turns, maxTurns)
	} else {
		fmt.Fprintf(&b, "  Turns:     %d\n", turns)
	}
	fmt.Fprintf(&b, "  YOLO:      %v\n", yolo)
	if cu, err := ctx.GetContextUsage(); err == nil && cu != nil && cu.Tokens != nil && cu.Percent != nil {
		fmt.Fprintf(&b, "  Context:   %d / %d tokens (%.1f%%)\n", *cu.Tokens, cu.ContextWindow, *cu.Percent)
	}
	s.show(ctx, strings.TrimRight(b.String(), "\n"))
	return nil
}

func (s *state) cmdPersonality(ctx sdk.Context, args string) error {
	trimmed := strings.TrimSpace(args)
	fields := strings.Fields(trimmed)
	sub := "show"
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}

	switch sub {
	case "show", "":
		raw, err := os.ReadFile(s.paths.SoulFile)
		if err != nil {
			if os.IsNotExist(err) {
				s.show(ctx, fmt.Sprintf("No SOUL.md set at %s. Use /personality set <text> to define one.", s.paths.SoulFile))
				return nil
			}
			s.show(ctx, fmt.Sprintf("failed to read SOUL.md: %v", err))
			return nil
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			s.show(ctx, fmt.Sprintf("SOUL.md at %s is empty.", s.paths.SoulFile))
			return nil
		}
		s.show(ctx, fmt.Sprintf("SOUL.md (%s):\n%s", s.paths.SoulFile, text))
		return nil

	case "set":
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
		if body == "" {
			s.show(ctx, "usage: /personality set <identity text>")
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(s.paths.SoulFile), 0o755); err != nil {
			s.show(ctx, fmt.Sprintf("failed to create directory: %v", err))
			return nil
		}
		tmp := s.paths.SoulFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(body+"\n"), 0o644); err != nil {
			s.show(ctx, fmt.Sprintf("failed to write SOUL.md: %v", err))
			return nil
		}
		if err := os.Rename(tmp, s.paths.SoulFile); err != nil {
			s.show(ctx, fmt.Sprintf("failed to save SOUL.md: %v", err))
			return nil
		}
		s.show(ctx, fmt.Sprintf("Updated SOUL.md (%d chars).", len(body)))
		return nil

	case "clear":
		if err := os.Remove(s.paths.SoulFile); err != nil && !os.IsNotExist(err) {
			s.show(ctx, fmt.Sprintf("failed to remove SOUL.md: %v", err))
			return nil
		}
		s.show(ctx, "Cleared SOUL.md.")
		return nil

	default:
		s.show(ctx, "usage: /personality [show | set <text> | clear]")
		return nil
	}
}

func (s *state) cmdYolo(ctx sdk.Context, args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	enabled := s.cfg.IsYolo()
	switch arg {
	case "on", "true", "1":
		enabled = true
		s.cfg.SetYolo(true)
	case "off", "false", "0":
		enabled = false
		s.cfg.SetYolo(false)
	case "status":
		// keep current value
	default:
		enabled = !enabled
		s.cfg.SetYolo(enabled)
	}
	if enabled {
		_ = os.Setenv("HERMES_YOLO_MODE", "1")
	} else {
		_ = os.Unsetenv("HERMES_YOLO_MODE")
	}
	s.updateStatusBar(ctx)
	if enabled {
		s.show(ctx, "YOLO mode: ON (command approvals bypassed for this session)")
	} else {
		s.show(ctx, "YOLO mode: OFF (command approvals active)")
	}
	return nil
}

func (s *state) cmdCompress(ctx sdk.Context, _ string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Use PiG's built-in /compact command to compress conversation context.")
	if cu, err := ctx.GetContextUsage(); err == nil && cu != nil {
		if cu.Tokens != nil && cu.Percent != nil {
			fmt.Fprintf(&b, "\nCurrent context usage: %d / %d tokens (%.1f%%)", *cu.Tokens, cu.ContextWindow, *cu.Percent)
		} else if cu.ContextWindow > 0 {
			fmt.Fprintf(&b, "\nContext window: %d tokens (usage unknown)", cu.ContextWindow)
		}
	}
	s.show(ctx, b.String())
	return nil
}

func (s *state) cmdTodos(ctx sdk.Context, _ string) error {
	sessionID, _ := ctx.GetSessionID()
	store := todo.Default()
	items := store.Get(sessionID)
	if len(items) == 0 && sessionID != "default" {
		items = store.Get("default")
	}
	if len(items) == 0 {
		s.show(ctx, "No active todo items for this session.")
		return nil
	}
	s.show(ctx, todo.FormatSummary(items))
	return nil
}
