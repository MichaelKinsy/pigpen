package surface

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// explicitThinkingFlag records whether the user passed --thinking or --reasoning on the CLI
// so ApplyRuntimeConfig does not override an explicit CLI thinking level.
var explicitThinkingFlag atomic.Bool

// validThinkingLevels lists the canonical PiG/Hermes reasoning effort levels.
var validThinkingLevels = map[string]bool{
	"off":     true,
	"minimal": true,
	"low":     true,
	"medium":  true,
	"high":    true,
	"xhigh":   true,
	"max":     true,
}

// normalizeThinkingLevel validates and normalizes a reasoning effort / thinking level string.
func normalizeThinkingLevel(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "none":
		return "off", true
	case "med":
		return "medium", true
	}
	if validThinkingLevels[v] {
		return v, true
	}
	return "", false
}

// safeContextModel returns the active model ID without panicking on a zero or detached Context.
func safeContextModel(ctx sdk.Context) (model string) {
	defer func() {
		if recover() != nil {
			model = ""
		}
	}()
	return strings.TrimSpace(ctx.Model())
}

// ApplyRuntimeConfig applies Hermes config.yaml runtime settings to the live PiG session
// without overriding explicit CLI flags (such as --max-turns or --model).
func ApplyRuntimeConfig(ctx sdk.Context, st *state) {
	if st == nil || st.cfg == nil {
		return
	}
	cfg := st.cfg

	// 1. agent.max_turns (only when not already set by CLI --max-turns) and
	// 2. agent.reasoning_effort -> ctx.SetThinkingLevel (once per state, and never
	//    when --thinking / --reasoning was explicitly passed on the CLI).
	st.mu.Lock()
	alreadyApplied := st.runtimeConfigApplied
	st.runtimeConfigApplied = true
	if cfg.Raw != nil && st.maxTurns == 0 {
		if raw := strings.TrimSpace(cfg.Raw["agent.max_turns"]); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				st.maxTurns = n
			}
		}
	}
	st.mu.Unlock()

	if cfg.Raw != nil && !alreadyApplied && !explicitThinkingFlag.Load() {
		if raw := strings.TrimSpace(cfg.Raw["agent.reasoning_effort"]); raw != "" {
			if level, ok := normalizeThinkingLevel(raw); ok {
				ctx.SetThinkingLevel(level)
			}
		}
	}
	// 3. DisabledTools -> filter active/all tools and apply via ctx.SetActiveTools.
	if len(cfg.DisabledTools) > 0 {
		disabled := make(map[string]bool, len(cfg.DisabledTools))
		for _, entry := range cfg.DisabledTools {
			name := strings.TrimSpace(entry)
			if name == "" {
				continue
			}
			disabled[name] = true
			// Also expand if the entry is a Hermes toolset name.
			if _, ok := GetToolset(name); ok {
				for _, t := range ResolveToolsetTools(name) {
					disabled[t] = true
				}
			}
		}
		if allTools, err := ctx.GetAllTools(); err == nil && len(allTools) > 0 {
			// Preserve current active subset when available; otherwise start from allTools.
			var base []string
			if active, actErr := ctx.GetActiveTools(); actErr == nil && len(active) > 0 {
				base = active
			} else {
				base = make([]string, 0, len(allTools))
				for _, t := range allTools {
					base = append(base, t.Name)
				}
			}
			filtered := make([]string, 0, len(base))
			for _, name := range base {
				if !disabled[name] {
					filtered = append(filtered, name)
				}
			}
			ctx.SetActiveTools(filtered)
		}
	}

	// 4. Default model from config.yaml ONLY if the session has no active model yet
	// (never override an explicit CLI --model flag).
	if modelSpec := strings.TrimSpace(cfg.Model); modelSpec != "" {
		if safeContextModel(ctx) == "" {
			if info, err := ctx.GetModelInfo(); err != nil || info == nil || strings.TrimSpace(info.ID) == "" {
				spec := modelSpec
				if !strings.Contains(spec, "/") && strings.TrimSpace(cfg.Provider) != "" {
					spec = strings.TrimSpace(cfg.Provider) + "/" + spec
				}
				_, _ = ctx.SetModelNamed(spec)
			}
		}
	}
}

// RunCompressCommand executes live conversation compaction via PiG's Compact API
// with optional custom instructions from `/compress [instructions]`.
func RunCompressCommand(ctx sdk.Context, st *state, args string) error {
	instructions := strings.TrimSpace(args)
	compactArgs := map[string]any{}
	if instructions != "" {
		compactArgs["customInstructions"] = instructions
	}

	var beforeLine string
	if cu, err := ctx.GetContextUsage(); err == nil && cu != nil {
		if cu.Tokens != nil && cu.Percent != nil {
			beforeLine = fmt.Sprintf("Context before compaction: %d / %d tokens (%.1f%%)", *cu.Tokens, cu.ContextWindow, *cu.Percent)
		} else if cu.ContextWindow > 0 {
			beforeLine = fmt.Sprintf("Context window: %d tokens", cu.ContextWindow)
		}
	}

	ctx.Compact(compactArgs)

	var b strings.Builder
	b.WriteString("Compaction triggered.")
	if instructions != "" {
		fmt.Fprintf(&b, " Instructions: %s", instructions)
	}
	if beforeLine != "" {
		fmt.Fprintf(&b, "\n%s", beforeLine)
	}
	msg := b.String()
	ctx.Notify(msg, "info")
	if st != nil {
		st.show(ctx, msg)
	}
	return nil
}

// RunToolsCommand handles `/tools`, `/tools enable <name...>`, and `/tools disable <name...>`.
func RunToolsCommand(ctx sdk.Context, st *state, args string) error {
	show := func(text string) {
		if st != nil {
			st.show(ctx, text)
			return
		}
		ctx.Notify(text, "info")
	}

	fields := strings.Fields(strings.TrimSpace(args))
	allTools, allErr := ctx.GetAllTools()
	activeTools, actErr := ctx.GetActiveTools()

	if len(fields) == 0 || strings.EqualFold(fields[0], "list") {
		if allErr != nil || len(allTools) == 0 {
			ts := "hermes-cli"
			if st != nil {
				st.mu.RLock()
				if st.toolset != "" {
					ts = st.toolset
				}
				st.mu.RUnlock()
			}
			resolved := ResolveToolsetTools(ts)
			show(fmt.Sprintf("Hermes Tools (%d in %s): %s", len(resolved), ts, strings.Join(resolved, ", ")))
			return nil
		}
		activeSet := make(map[string]bool, len(activeTools))
		for _, name := range activeTools {
			activeSet[name] = true
		}
		var enabledList, disabledList []string
		for _, t := range allTools {
			if activeSet[t.Name] {
				enabledList = append(enabledList, t.Name)
			} else {
				disabledList = append(disabledList, t.Name)
			}
		}
		sort.Strings(enabledList)
		sort.Strings(disabledList)

		var b strings.Builder
		fmt.Fprintf(&b, "Tools (%d active / %d total)\n", len(enabledList), len(allTools))
		if len(enabledList) > 0 {
			fmt.Fprintf(&b, "  Active:\n")
			for _, name := range enabledList {
				fmt.Fprintf(&b, "    * %s\n", name)
			}
		}
		if len(disabledList) > 0 {
			fmt.Fprintf(&b, "  Disabled:\n")
			for _, name := range disabledList {
				fmt.Fprintf(&b, "    - %s\n", name)
			}
		}
		show(strings.TrimRight(b.String(), "\n"))
		return nil
	}

	sub := strings.ToLower(fields[0])
	targets := fields[1:]
	if len(targets) == 0 {
		show("usage: /tools [list | enable <name...> | disable <name...>]")
		return nil
	}

	// Build known tool registry and ordered active set.
	known := make(map[string]bool, len(allTools))
	for _, t := range allTools {
		known[t.Name] = true
	}
	if actErr != nil && len(activeTools) == 0 && len(allTools) > 0 {
		for _, t := range allTools {
			activeTools = append(activeTools, t.Name)
		}
	}
	activeSet := make(map[string]bool, len(activeTools))
	ordered := make([]string, 0, len(activeTools)+len(targets))
	for _, name := range activeTools {
		if !activeSet[name] {
			activeSet[name] = true
			ordered = append(ordered, name)
		}
	}

	// Expand any Hermes toolset names in targets as well as literal tool names.
	expandedTargets := make([]string, 0, len(targets))
	for _, raw := range targets {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(known) > 0 && known[name] {
			expandedTargets = append(expandedTargets, name)
			continue
		}
		if _, ok := GetToolset(name); ok {
			expandedTargets = append(expandedTargets, ResolveToolsetTools(name)...)
			continue
		}
		expandedTargets = append(expandedTargets, name)
	}

	switch sub {
	case "enable", "on":
		var added, unknown []string
		for _, name := range expandedTargets {
			if len(known) > 0 && !known[name] {
				unknown = append(unknown, name)
				continue
			}
			if !activeSet[name] {
				activeSet[name] = true
				ordered = append(ordered, name)
				added = append(added, name)
			}
		}
		if len(added) > 0 {
			ctx.SetActiveTools(ordered)
		}
		var b strings.Builder
		if len(added) > 0 {
			fmt.Fprintf(&b, "Enabled tools: %s (%d active)", strings.Join(added, ", "), len(ordered))
		} else if len(unknown) == 0 {
			fmt.Fprintf(&b, "Requested tools are already active (%d active).", len(ordered))
		}
		if len(unknown) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "Unknown tools ignored: %s", strings.Join(unknown, ", "))
		}
		show(b.String())
		return nil

	case "disable", "off":
		removeSet := make(map[string]bool, len(expandedTargets))
		for _, name := range expandedTargets {
			removeSet[name] = true
		}
		var removed []string
		next := make([]string, 0, len(ordered))
		for _, name := range ordered {
			if removeSet[name] {
				removed = append(removed, name)
				continue
			}
			next = append(next, name)
		}
		if len(removed) > 0 {
			ctx.SetActiveTools(next)
		}
		if len(removed) > 0 {
			show(fmt.Sprintf("Disabled tools: %s (%d active)", strings.Join(removed, ", "), len(next)))
		} else {
			show(fmt.Sprintf("No matching active tools to disable (%d active).", len(next)))
		}
		return nil

	default:
		show("usage: /tools [list | enable <name...> | disable <name...>]")
		return nil
	}
}
