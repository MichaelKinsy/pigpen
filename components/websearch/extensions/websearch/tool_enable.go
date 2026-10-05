package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// web_enable (tool-activation.ts): with dynamic activation the web tools start inactive and the
// model enables them on demand.

const loaderName = "web_enable"

func (r *Runtime) webEnableSpec() ToolSpec {
	tools := r.activationTools()
	caps := make([]string, len(tools))
	for i, t := range tools {
		caps[i] = capabilityLabels[t.capability]
	}
	return ToolSpec{
		Name:          loaderName,
		Label:         "Enable Web Access",
		Description:   "Enable configured pi-web-access tools for web research and content retrieval. Does not search or fetch. Enabled tools are available on the next model request; disabled capabilities remain unavailable.",
		PromptSnippet: fmt.Sprintf("If tools for %s are not already available, call web_enable first whenever current, external, or linked information could help; the tools appear on the next model request.", strings.Join(caps, ", ")),
		parameters:    append(sObject(nil, obj{}), kv{"additionalProperties", false}),
		Execute: func(ctx context.Context, _ map[string]any, _ func(ToolOutput)) (ToolOutput, error) {
			return r.webEnable()
		},
	}
}

func (r *Runtime) webEnable() (ToolOutput, error) {
	act, ok := r.host.(ToolActivator)
	if !ok {
		return ToolOutput{}, errors.New("web_enable needs a host that can change the active tool set")
	}
	tools := r.activationTools()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
	}
	registered := map[string]bool{}
	for _, n := range act.AllToolNames() {
		registered[n] = true
	}
	var unavailable []string
	for _, n := range names {
		if !registered[n] {
			unavailable = append(unavailable, n)
		}
	}
	fail := func(text string, details map[string]any) ToolOutput {
		o := textOutput(text, details)
		o.IsError = true
		return o
	}
	if len(unavailable) > 0 {
		return fail("Cannot enable unavailable tools: "+strings.Join(unavailable, ", ")+".", map[string]any{"unavailable": unavailable}), nil
	}
	merged := append([]string{}, act.ActiveToolNames()...)
	for _, n := range names {
		if !sliceHas(merged, n) {
			merged = append(merged, n)
		}
	}
	if err := act.SetActiveTools(merged); err != nil {
		return fail("Activation failed: "+err.Error()+".", map[string]any{"error": err.Error()}), nil
	}
	active := map[string]bool{}
	for _, n := range act.ActiveToolNames() {
		active[n] = true
	}
	var missing []string
	for _, n := range names {
		if !active[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fail("Tools still inactive after activation: "+strings.Join(missing, ", ")+".", map[string]any{"missing": missing}), nil
	}
	return textOutput("Enabled: "+strings.Join(names, ", ")+".", map[string]any{"enabled": names}), nil
}

// ---- lifecycle ----------------------------------------------------------------------------

// transcriptToolNames is the tool set the transcript declared with toolsAdded/toolsRemoved system
// messages; nil when it never declared any.
func transcriptToolNames(messages []map[string]any) map[string]bool {
	var tools map[string]bool
	for _, m := range messages {
		if m["role"] != "system" {
			continue
		}
		added, hasAdded := m["toolsAdded"]
		removed, hasRemoved := m["toolsRemoved"]
		if !hasAdded && !hasRemoved {
			continue
		}
		if tools == nil {
			tools = map[string]bool{}
		}
		names := func(v any) []string {
			var out []string
			list, _ := v.([]any)
			for _, e := range list {
				if o, ok := e.(map[string]any); ok {
					if n, ok := o["name"].(string); ok {
						out = append(out, n)
					}
				}
			}
			return out
		}
		for _, n := range names(removed) {
			delete(tools, n)
		}
		for _, n := range names(added) {
			tools[n] = true
		}
	}
	return tools
}

func (r *Runtime) loaderAvailable(act ToolActivator) bool {
	return sliceHas(act.AllToolNames(), loaderName)
}

// SelectFromSession is the session_start / session_tree handler of dynamic activation: the web
// tools are active exactly when the transcript recorded them (a session from before transcript
// tool declarations keeps every web tool; a fresh one starts with none).
func (r *Runtime) SelectFromSession(messages []map[string]any) {
	act, ok := r.host.(ToolActivator)
	if !ok || r.cfg.ToolActivation != "dynamic" || len(r.activationTools()) == 0 || !r.loaderAvailable(act) {
		return
	}
	tools := r.activationTools()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
	}
	declared := transcriptToolNames(messages)
	recorded := declared
	if recorded == nil {
		recorded = map[string]bool{}
		if len(messages) > 0 {
			for _, n := range names {
				recorded[n] = true
			}
		}
	}
	// A resumed transcript that declared its tools without the loader keeps that set: adding the
	// loader there would record a tool change the user never asked for.
	loaderSelected := declared == nil || declared[loaderName]
	r.mu.Lock()
	r.loaderSelected = loaderSelected
	r.mu.Unlock()
	var next []string
	for _, n := range act.ActiveToolNames() {
		if n != loaderName && !sliceHas(names, n) {
			next = append(next, n)
		}
	}
	for _, n := range names {
		if recorded[n] && !sliceHas(next, n) {
			next = append(next, n)
		}
	}
	if loaderSelected {
		next = append(next, loaderName)
	}
	// Best effort: a host that rejects the selection keeps the tools it has.
	_ = act.SetActiveTools(next)
}

// BeforeAgentStart re-adds the loader when the session selected it and it was dropped.
func (r *Runtime) BeforeAgentStart() {
	act, ok := r.host.(ToolActivator)
	if !ok || r.cfg.ToolActivation != "dynamic" || len(r.activationTools()) == 0 {
		return
	}
	r.mu.Lock()
	selected := r.loaderSelected
	r.mu.Unlock()
	if !selected || !r.loaderAvailable(act) || sliceHas(act.ActiveToolNames(), loaderName) {
		return
	}
	_ = act.SetActiveTools(append(append([]string{}, act.ActiveToolNames()...), loaderName))
}
