package pi_subagents

import (
	"fmt"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `models` action (the original's handleModels), the `list` capabilities
// rows, and the `guide` action. Model resolution uses the host's model
// registry through the SDK; capability-ceiling restrictions and the model
// scopes belong to their own slices and are not applied here.

func init() {
	registerAction("models", []string{"agent", "agentScope"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(modelsAction(modelsView{current: jsTrim(ctx.ModelQualified()), models: registryModels(ctx)}, ctx.Cwd(), params))
	})
	registerAction("guide", []string{"topic"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return manageOutcome(guideAction(params))
	})
}

// availableModel is one registry entry this port reads.
type availableModel struct {
	Provider string
	ID       string
	FullID   string
}

// registryModels reads the host's available models; an unavailable registry is nil.
func registryModels(ctx sdk.Context) []availableModel {
	models, err := ctx.ModelRegistry().GetAvailable()
	if err != nil {
		return nil
	}
	out := make([]availableModel, 0, len(models))
	for _, m := range models {
		provider, _ := m["provider"].(string)
		id, _ := m["id"].(string)
		full, _ := m["fullId"].(string)
		if full == "" && provider != "" && id != "" {
			full = provider + "/" + id
		}
		out = append(out, availableModel{Provider: provider, ID: id, FullID: full})
	}
	return out
}

// resolveModelCandidate is the original's resolveSubagentModelCandidate for
// the resolution this port performs: an exact provider/id, a bare id when
// unique, else as declared.
func resolveModelCandidate(model string, available []availableModel) string {
	trimmed := jsTrim(model)
	if trimmed == "" || trimmed == "inherit" || len(available) == 0 {
		return trimmed
	}
	base, suffix := splitThinkingSuffix(trimmed)
	var exact, bare []string
	for _, m := range available {
		if m.FullID == base {
			exact = append(exact, m.FullID)
		}
		if m.ID == base {
			bare = append(bare, m.FullID)
		}
	}
	switch {
	case len(exact) > 0:
		sort.Strings(exact)
		return exact[0] + suffix
	case len(bare) == 1:
		return bare[0] + suffix
	}
	return trimmed
}

// modelSource is the original's formatModelSource for the port's sources.
func modelSource(a *AgentConfig, hasCurrent bool) string {
	if a.Model != "" {
		return string(a.Source) + " agent config"
	}
	if hasCurrent {
		return "inherits current session model"
	}
	return "inherit requested, but no current session model is available"
}

// effectiveThinking is the original's resolveEffectiveThinking.
func effectiveThinking(model, thinking string) string {
	_, suffix := splitThinkingSuffix(model)
	if suffix != "" {
		return suffix[1:]
	}
	if knownThinkingLevel(thinking) {
		return strings.TrimSpace(thinking)
	}
	return ""
}

// modelsView is what the models action reads from the session: the current
// model and the registry's available models.
type modelsView struct {
	current string
	models  []availableModel
}

// modelsAction is `subagent({action:"models", agent?, agentScope})`.
func modelsAction(view modelsView, cwd string, params map[string]any) manageResult {
	requestedAgent := jsTrim(strParam(params, "agent"))
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok {
		return manageResult{"agentScope must be 'user', 'project', or 'both' for models.", true}
	}
	d := DiscoverAgentsAll(cwd)
	effective := withoutDisabled(MergeAgentsForScope(scope, d.User, d.Project, d.Builtin, d.Package))
	sort.SliceStable(effective, func(i, j int) bool { return localeLess(effective[i].Name, effective[j].Name) })
	available := view.models
	current := view.current
	hasCurrent := current != ""

	selected := effective
	if requestedAgent != "" {
		matches := findAgentsScoped(requestedAgent, "", d)
		if diag := blockingDiagnostic(requestedAgent, nil, diagnosticsForScope(d.Diagnostics, scope)); diag != nil {
			return manageResult{fmt.Sprintf("Agent '%s' has invalid configuration: %s", requestedAgent, diag.Error), true}
		}
		names := sortedNames(matches)
		if len(names) > 1 {
			return manageResult{fmt.Sprintf("Ambiguous agent alias or name '%s': %s", requestedAgent, strings.Join(names, ", ")), true}
		}
		if len(matches) == 0 {
			return manageResult{fmt.Sprintf("Agent '%s' not found. Available: %s.", requestedAgent, availableNames(cwd)), true}
		}
		selected = []AgentConfig{matches[0]}
	}

	heading := "Subagent model"
	if requestedAgent == "" {
		heading = "Subagent models"
	}
	lines := []string{heading, ""}
	if requestedAgent == "" {
		currentLine := "(unavailable)"
		if hasCurrent {
			currentLine = current
		}
		lines = append(lines, "Current session model:", "  "+currentLine, "")
	}
	for i := range selected {
		a := &selected[i]
		names := []string{a.Name}
		if requestedAgent == "" {
			names = append(names, a.Aliases...)
		}
		resolved := a.Model
		if jsTrim(resolved) == "" || resolved == "inherit" {
			resolved = current
			if !hasCurrent {
				resolved = ""
			}
		} else {
			resolved = resolveModelCandidate(resolved, available)
		}
		thinking := effectiveThinking(resolved, thinkingString(a.Thinking))
		source := modelSource(a, hasCurrent)
		if a.Disabled {
			source += "; disabled"
		}
		if requestedAgent != "" {
			lines = append(lines, "Agent: "+requestedAgent)
			lines = append(lines, "Effective model:")
			modelLine := "(unresolved)"
			if resolved != "" {
				modelLine = resolved
			}
			lines = append(lines, "  "+modelLine)
			lines = append(lines, "Source: "+source)
			lines = append(lines, "Thinking: "+orDefault(thinking, "default"))
			if a.Disabled {
				lines = append(lines, "Disabled: true")
			}
			cur := "(unavailable)"
			if hasCurrent {
				cur = current
			}
			lines = append(lines, "Current session model:", "  "+cur)
			break
		}
		for _, name := range names {
			modelLine := "(unresolved)"
			if resolved != "" {
				modelLine = resolved
			}
			lines = append(lines, name, "  model:", "    "+modelLine, "  source: "+source, "  thinking: "+orDefault(thinking, "default"), "")
		}
	}

	fullIDs := []string{}
	for _, m := range available {
		if m.FullID != "" {
			fullIDs = append(fullIDs, m.FullID)
		}
	}
	sort.Strings(fullIDs)
	if requestedAgent == "" && len(fullIDs) > 0 {
		lines = append(lines, "Available models in this session's registry (copy an exact provider/id when passing model):", "")
		shown := fullIDs
		if len(shown) > 80 {
			shown = shown[:80]
		}
		for _, id := range shown {
			lines = append(lines, "  "+id)
		}
		if len(fullIDs) > len(shown) {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(fullIDs)-len(shown)))
		}
		lines = append(lines, "", "Use an exact provider/id from this list when you pass model; bare ids resolve only when unique in the registry.")
	}
	if requestedAgent == "" {
		lines = append(lines, diagnosticLines(diagnosticsForScope(d.Diagnostics, scope))...)
	}
	return manageResult{text: strings.Join(lines, "\n")}
}

func strParam(params map[string]any, name string) string {
	s, _ := params[name].(string)
	return s
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// thinkingString renders the AgentConfig thinking value for the effective-thinking check.
func thinkingString(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case bool:
		if v {
			return ""
		}
		return "off"
	}
	return ""
}

// capabilitiesActionText is the original's capabilities heading plus its rows
// (formatAgentCapabilitiesLine) for the port's fields.
func capabilitiesText(cwd string, params map[string]any) manageResult {
	scope, ok := normalizeListScope(params["agentScope"])
	if !ok {
		return manageResult{"agentScope must be 'user', 'project', or 'both' for list.", true}
	}
	d := DiscoverAgentsAll(cwd)
	agents := withoutDisabled(MergeAgentsForScope(scope, d.User, d.Project, d.Builtin, d.Package))
	sort.SliceStable(agents, func(i, j int) bool { return localeLess(agents[i].Name, agents[j].Name) })
	lines := []string{"Executable agents (capabilities):"}
	if len(agents) == 0 {
		lines = append(lines, "- (none)")
	}
	first := true
	for _, sec := range []struct {
		src   AgentSource
		label string
	}{{SourcePackage, "Package agents"}, {SourceUser, "User agents"}, {SourceProject, "Project agents"}, {"runtime", "Runtime agents"}, {SourceBuiltin, "Builtin agents"}} {
		var section []string
		for i := range agents {
			if agents[i].Source == sec.src {
				section = append(section, capabilitiesLine(&agents[i]))
			}
		}
		if len(section) == 0 {
			continue
		}
		if !first {
			lines = append(lines, "")
		}
		first = false
		lines = append(lines, sec.label)
		lines = append(lines, section...)
	}
	lines = append(lines, diagnosticLines(diagnosticsForScope(d.Diagnostics, scope))...)
	return manageResult{text: strings.Join(lines, "\n")}
}

// capabilitiesLine is the original's formatAgentCapabilitiesLine for the port's fields.
func capabilitiesLine(a *AgentConfig) string {
	declared := append([]string{}, a.Tools...)
	for _, t := range a.McpDirectTools {
		declared = append(declared, "mcp:"+t)
	}
	tools := "none"
	if a.Tools == nil && a.McpDirectTools == nil {
		tools = "default/ambient"
	} else if len(declared) > 0 {
		tools = strings.Join(declared, ", ")
	}
	if len(a.ExcludeTools) > 0 {
		tools = tools + "; excludes: " + strings.Join(a.ExcludeTools, ", ")
	}
	model := "inherits current session"
	if a.Model != "" {
		model = a.Model
	}
	thinking := "default"
	if s, isString := a.Thinking.(string); isString && s != "" {
		thinking = s
	} else if b, isBool := a.Thinking.(bool); isBool && !b {
		thinking = "off"
	}
	machine := ""
	if a.Machine != "" {
		machine = "; Machine: " + a.Machine + " (saved Herdr placement)"
	}
	acceptance := acceptanceSummary(a)
	parts := fmt.Sprintf("- %s (%s): Description: %s; Tools: %s; Model: %s; Thinking: %s%s",
		a.Name, listMetadata(a), previewText(a.Description, 240), tools, model, thinking, machine)
	if acceptance != "" {
		parts += "; " + acceptance
	}
	return parts
}

// listMetadata is the original's agentListMetadata (without the external-CLI
// availability preflight, which belongs to the external-runner slice).
func listMetadata(a *AgentConfig) string {
	parts := []string{string(a.Source)}
	if b := runnerBadge(*a); b != "" {
		parts = append(parts, b)
	}
	if a.DefaultContext != "" {
		parts = append(parts, "context: "+a.DefaultContext)
	}
	if len(a.Aliases) > 0 {
		parts = append(parts, "aliases: "+strings.Join(a.Aliases, ", "))
	}
	return strings.Join(parts, ", ")
}

// acceptanceSummary is the original's formatAcceptanceSummary for the port's fields.
func acceptanceSummary(a *AgentConfig) string {
	var summary []string
	if a.DefaultAcceptance != "" {
		if a.DefaultAcceptance == "false" {
			summary = append(summary, "Acceptance: disabled")
		} else if !strings.HasPrefix(a.DefaultAcceptance, "{") {
			summary = append(summary, "Acceptance: "+a.DefaultAcceptance)
		} else {
			parsed, err := parseJSON([]byte(a.DefaultAcceptance))
			obj, isObj := parsed.(*jsObject)
			if err != nil || !isObj {
				summary = append(summary, "Acceptance: "+a.DefaultAcceptance)
			} else {
				summary = append(summary, "Acceptance: "+acceptanceObjectSummary(obj))
			}
		}
	}
	if a.AcceptanceRole != "" {
		summary = append(summary, "Acceptance role: "+a.AcceptanceRole)
	}
	return strings.Join(summary, "; ")
}

// acceptanceObjectSummary is the original's object-form summary line.
func acceptanceObjectSummary(policy *jsObject) string {
	level, _ := policy.str("level")
	if level == "" {
		level = "auto"
	}
	var modifiers []string
	if ev, present := policy.get("evidence"); present {
		if arr, isArr := ev.([]any); isArr {
			for _, e := range arr {
				if s, isString := e.(string); isString {
					modifiers = append(modifiers, s)
				}
			}
		}
	}
	if verify, present := policy.get("verify"); present {
		if arr, isArr := verify.([]any); isArr {
			for _, v := range arr {
				if cmd, isObj := v.(*jsObject); isObj {
					id, _ := cmd.str("id")
					modifiers = append(modifiers, "verify: "+acceptanceDisplayLabel(id))
				}
			}
		}
	}
	if criteria, present := policy.get("criteria"); present {
		if arr, isArr := criteria.([]any); isArr && len(arr) > 0 {
			modifiers = append(modifiers, fmt.Sprintf("criteria: %d", len(arr)))
		}
	}
	if stopRules, present := policy.get("stopRules"); present {
		if arr, isArr := stopRules.([]any); isArr && len(arr) > 0 {
			modifiers = append(modifiers, fmt.Sprintf("stopRules: %d", len(arr)))
		}
	}
	if review, present := policy.get("review"); present {
		if b, isBool := review.(bool); isBool && !b {
			modifiers = append(modifiers, "review: off")
		} else if reviewObj, isObj := review.(*jsObject); isObj {
			agent, _ := reviewObj.str("agent")
			required, _ := reviewObj.get("required")
			label := agent
			if b, isBool := required.(bool); isBool && b {
				label = orDefault(label, "reviewer") + " required"
			}
			modifiers = append(modifiers, "review: "+acceptanceDisplayLabel(label))
		}
	}
	if report, _ := policy.str("report"); report != "" {
		modifiers = append(modifiers, "report: "+report)
	}
	if len(modifiers) == 0 {
		return level
	}
	return level + " (" + strings.Join(modifiers, ", ") + ")"
}

// acceptanceDisplayLabel is the original's formatAcceptanceDisplayLabel (a kebab label).
func acceptanceDisplayLabel(id string) string {
	return strings.ReplaceAll(strings.TrimSpace(id), "_", "-")
}

// previewText is the original's previewDisplayText: bounded text with an ellipsis.
func previewText(value string, max int) string {
	if utf16Length(value) <= max {
		return value
	}
	out := value
	for utf16Length(out) > max {
		out = out[:len(out)-1]
	}
	return out + "…"
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Len(r)
	}
	return n
}

// modelWarning is the original's modelWarning: an agent's model that the
// session's registry does not list.
func modelWarning(model string, registry []availableModel) string {
	trimmed := jsTrim(model)
	if trimmed == "" || trimmed == "inherit" {
		return ""
	}
	for _, m := range registry {
		if m.Provider+"/"+m.ID == trimmed || m.ID == trimmed {
			return ""
		}
	}
	return fmt.Sprintf("Warning: model '%s' is not in the current model registry. Run subagent({ action: \"models\" }) to list valid provider/id selectors, then use the exact provider/id form (bare ids resolve only when unique).", model)
}

// guideAction is `subagent({action:"guide", topic})`. The port serves its own
// guides for the topics whose subject it runs; the original's packaged guides
// document features this port refuses, so it names them instead.
func guideAction(params map[string]any) manageResult {
	topic := "overview"
	if raw, present := params["topic"]; present && raw != nil {
		if s, isString := raw.(string); isString {
			topic = s
		} else {
			// The original keeps the raw value in its unknown-topic message.
			topic = fmt.Sprint(raw)
		}
	}
	if text, ok := portGuide(topic); ok {
		return manageResult{text: text}
	}
	return manageResult{fmt.Sprintf("Unknown subagents guide topic '%s'. Valid topics: %s. No files were changed.", topic, strings.Join(portGuideTopics(), ", ")), true}
}
