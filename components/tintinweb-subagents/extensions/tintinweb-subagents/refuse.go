package tintinweb_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Everything the original does that this port does not do yet is refused, with this one message, instead of being
// ignored or approximated. portName is package.json's name and originalSpec the pinned original
// (port/oracle/package.json); refuse_test.go checks both against those files.
const (
	portName     = "@pi-in-go/pigpen-tintinweb-subagents"
	originalSpec = "npm:@tintinweb/pi-subagents@0.19.0"
)

// refusal is the error for a feature that is not ported.
func refusal(feature string) error {
	return errors.New(refusalText(feature))
}

func refusalText(feature string) string {
	return portName + " (Go port) does not support " + feature + " yet. Install the original extension, " + originalSpec + ", which runs unchanged in PiG."
}

// unportedCallParam names the first Agent parameter of the original that is not ported, or "".
// upstream: index.ts Agent parameters (schedule, isolation, resume, inherit_context).
func unportedCallParam(p map[string]any) string {
	if v, ok := p["schedule"]; ok && v != nil {
		return "scheduled agent runs (the schedule parameter)"
	}
	if v, _ := p["isolation"].(string); v != "" && v != "off" {
		return `worktree isolation (isolation: "` + v + `")`
	}
	if v, _ := p["inherit_context"].(bool); v {
		return "inheriting the parent conversation (inherit_context)"
	}
	if v, _ := p["resume"].(string); v != "" {
		return "resuming a finished agent's session (resume)"
	}
	return ""
}

// unportedAgentFeature names the first field of an agent file that asks for something this port does not do, or "".
// upstream: src/custom-agents.ts, src/types.ts AgentConfig.
func unportedAgentFeature(cfg *agentConfig) string {
	switch {
	case cfg.InheritContext != nil && *cfg.InheritContext:
		return "inheriting the parent conversation (inherit_context in the agent file)"
	case cfg.Isolation == "worktree":
		return "worktree isolation (isolation: worktree in the agent file)"
	case cfg.Memory != "":
		return "persistent agent memory (memory: " + cfg.Memory + ")"
	case cfg.AllowedSubagents != nil:
		return "nested delegation (allowed_subagents)"
	case len(cfg.ExtSelectors) > 0:
		return "extension tool selectors (ext: in tools)"
	case len(cfg.DisallowedTools) > 0:
		return "disallowed_tools"
	case !extensionsSupported(cfg.Extensions):
		return "an extension allow-list (extensions)"
	case len(cfg.ExcludeExtensions) > 0:
		return "exclude_extensions"
	case !skillsSupported(cfg.Skills):
		return "skill selection (skills)"
	case cfg.PersistSession != nil && *cfg.PersistSession:
		return "persisted agent sessions (persist_session)"
	case cfg.OutputTranscript != nil && *cfg.OutputTranscript:
		return "output transcripts (output_transcript)"
	case cfg.SessionDir != "":
		return "a session directory (session_dir)"
	}
	return ""
}

// extensionsSupported: unset, true and false are honored (false starts the child with no extensions); a list is not.
func extensionsSupported(v any) bool {
	switch v.(type) {
	case nil, bool:
		return true
	}
	return false
}

// skillsSupported: unset and true (the child loads its own skills) are honored; false and a list are not.
func skillsSupported(v any) bool {
	b, ok := v.(bool)
	return v == nil || (ok && b)
}

// unportedSpawnOption names the first RPC spawn option the port does not honor, or "".
// Honored: description, model, maxTurns, name, thinkingLevel, isBackground (true only); workflowId is dropped, as the original drops it. upstream: agent-manager.ts SpawnOptions.
func unportedSpawnOption(opts map[string]any) string {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := opts[k]
		if v == nil {
			continue
		}
		switch k {
		case "description", "model", "maxTurns", "name", "thinkingLevel":
		case "workflowId":
			// Internal: the original deletes it from the options a bus caller sends (index.ts:703), so a forged value cannot
			// hide an agent inside someone else's workflow. Ignored, not refused; bus.go never passes it on.
		case "isBackground":
			if b, _ := v.(bool); !b {
				return "foreground agents through the spawn RPC (isBackground: false)"
			}
		default:
			return "the spawn RPC option \"" + k + "\""
		}
	}
	return ""
}

// settingRule says which values of a subagents.json setting this port already matches. Any other valid value asks for
// a feature that is not ported. upstream: src/settings.ts sanitize.
type settingRule struct {
	key    string
	kind   string // "bool", "int" or "enum"
	okBool bool
	okInts []int
	enum   []string // the valid strings of an enum setting
	okEnum []string
}

var settingRules = []settingRule{
	{key: "maxConcurrentForeground", kind: "int", okInts: []int{0}},
	{key: "defaultJoinMode", kind: "enum", enum: []string{"async", "group", "smart"}, okEnum: []string{"async"}},
	{key: "backgroundByDefault", kind: "bool", okBool: true},
	{key: "schedulingEnabled", kind: "bool", okBool: false},
	{key: "scopeModels", kind: "bool", okBool: false},
	{key: "strictAgentFiles", kind: "bool", okBool: false},
	{key: "toolDescriptionMode", kind: "enum", enum: []string{"full", "compact", "custom"}, okEnum: []string{"full"}},
	{key: "fleetView", kind: "bool", okBool: false},
	{key: "agentMentions", kind: "enum", enum: []string{"model", "direct", "off"}, okEnum: []string{"off"}},
	{key: "rememberAgents", kind: "bool", okBool: false},
	{key: "widgetMode", kind: "enum", enum: []string{"all", "background", "off"}, okEnum: []string{"off"}},
	{key: "outputTranscript", kind: "bool", okBool: false},
	{key: "worktreeIsolation", kind: "bool", okBool: false},
	{key: "maxSubagentDepth", kind: "int", okInts: []int{0, 1}},
	{key: "reportUsage", kind: "bool", okBool: false},
	{key: "showCost", kind: "bool", okBool: false},
	{key: "showModel", kind: "bool", okBool: false},
	{key: "viewerMarkdown", kind: "enum", enum: []string{"assistant", "all", "off"}},
}

func (r settingRule) unported(v any) bool {
	switch r.kind {
	case "bool":
		b, ok := v.(bool)
		return ok && b != r.okBool
	case "int":
		n, ok := integer(v)
		if !ok || n < 0 {
			return false
		}
		for _, o := range r.okInts {
			if n == o {
				return false
			}
		}
		return true
	case "enum":
		if b, ok := v.(bool); ok && r.key == "agentMentions" { // the legacy boolean: true reads as "model", false as "off"
			return b
		}
		s, ok := v.(string)
		if !ok || !inList(r.enum, s) {
			return false
		}
		return !inList(r.okEnum, s)
	}
	return false
}

func inList(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// unportedSettings names the subagents.json settings, global then project merged key by key, whose value asks for a
// feature that is not ported. A value of the wrong type is ignored, as the original ignores it.
func unportedSettings(cwd string) []string {
	merged := map[string]any{}
	for _, path := range []string{filepath.Join(agentDir(), "subagents.json"), filepath.Join(cwd, ".pi", "subagents.json")} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		for k, v := range raw {
			merged[k] = v
		}
	}
	var out []string
	for _, r := range settingRules {
		if v, ok := merged[r.key]; ok && r.unported(v) {
			out = append(out, fmt.Sprintf("%s: %s", r.key, compactJSON(v)))
		}
	}
	return out
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// settingsRefusal is the notification for unported settings, or "".
func settingsRefusal(cwd string) string {
	if list := unportedSettings(cwd); len(list) > 0 {
		return refusalText("these subagents.json settings (" + strings.Join(list, ", ") + ")")
	}
	return ""
}
