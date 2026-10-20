package tintinweb_subagents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The Agent tool's description and schema, as pure functions of descriptionFlags: the original's text for every
// combination of its optional features and the three toolDescriptionModes. The texts are extracted from the original's
// source by port/gen-spec.py (agent_text.json) and proved against what Pi sent for every combination
// (description_golden_test.go, port/description-golden). upstream: index.ts (fullAgentToolDescription,
// compactAgentToolDescription, renderToolDescriptionTemplate, loadCustomToolDescription, the schedule and isolation
// parts) and invocation-config.ts (isolationParam).

//go:embed agent_text.json
var agentTextJSON []byte

type agentTexts struct {
	Full                      string         `json:"full"`
	Compact                   string         `json:"compact"`
	ScheduleGuideline         string         `json:"scheduleGuideline"`
	IsolationGuideline        string         `json:"isolationGuideline"`
	IsolationCompactGuideline string         `json:"isolationCompactGuideline"`
	ScheduleParam             map[string]any `json:"scheduleParam"`
	IsolationParam            map[string]any `json:"isolationParam"`
}

func loadAgentTexts() agentTexts {
	var t agentTexts
	if err := json.Unmarshal(agentTextJSON, &t); err != nil {
		panic(err)
	}
	return t
}

var texts = loadAgentTexts()

// descriptionFlags are what the Agent tool's text depends on. workflows adds the SubagentWorkflow tool (a tool of its
// own: the Agent text does not change); scheduling and worktreeIsolation add a parameter and a guideline each.
type descriptionFlags struct {
	scheduling        bool
	worktreeIsolation bool
	workflows         bool
	mode              string // full, compact or custom
}

// descriptionFlagsNow: a feature is offered when this port has it (features.go) and the settings do not switch it off.
func descriptionFlagsNow(s settings, mode string) descriptionFlags {
	return descriptionFlags{
		scheduling:        featureScheduling && s.schedulingEnabled(),
		worktreeIsolation: featureWorktreeIsolation && s.worktreeIsolation(),
		workflows:         featureWorkflows && s.workflowsEnabled(),
		mode:              mode,
	}
}

// registeredToolNames are the tools the extension registers, in registration order. upstream: index.ts registerTool.
func registeredToolNames(f descriptionFlags) []string {
	names := []string{"Agent"}
	if f.workflows {
		names = append(names, "SubagentWorkflow") // registered right after the Agent tool, before the two that read and steer it
	}
	return append(names, "get_subagent_result", "steer_subagent")
}

// descriptionContext is what the description reads besides the flags.
type descriptionContext struct {
	reg      *agentRegistry
	agentDir string
	cwd      string
	warn     func(string) // a custom template's problems (the original's console.warn)
}

func (f descriptionFlags) scheduleGuideline() string {
	if f.scheduling {
		return texts.ScheduleGuideline
	}
	return ""
}

func (f descriptionFlags) isolationGuideline() string {
	if f.worktreeIsolation {
		return texts.IsolationGuideline
	}
	return ""
}

func (f descriptionFlags) isolationCompactGuideline() string {
	if f.worktreeIsolation {
		return texts.IsolationCompactGuideline
	}
	return ""
}

func (d descriptionContext) typeList() string { return buildTypeListText(d.reg) }

// compactTypeList is one line per agent type, the first sentence of its description only.
func (d descriptionContext) compactTypeList() string {
	var lines []string
	for _, name := range d.reg.availableTypes() {
		c := d.reg.m[name]
		lines = append(lines, fmt.Sprintf("- %s: %s (Tools: %s)", name, firstSentence(c.Description), formatToolsSuffix(c)))
	}
	return strings.Join(lines, "\n")
}

// isJSSpace is JavaScript's `\s` (and what String.prototype.trim removes).
func isJSSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// firstSentence is the text up to the first `.`, `!` or `?` that ends the text or is followed by white space (the
// whole text when there is none), its white space collapsed to single spaces. upstream: index.ts firstSentence.
func firstSentence(text string) string {
	rs := []rune(text)
	end := len(rs)
	for i, r := range rs {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(rs) || isJSSpace(rs[i+1])) {
			end = i + 1
			break
		}
	}
	var b strings.Builder
	space := false
	for _, r := range rs[:end] {
		if isJSSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteRune(' ')
			space = false
		}
		b.WriteRune(r)
	}
	// A leading run of white space collapses to one space that trim() then removes; a trailing one is dropped above.
	return jsTrim(b.String())
}

func fullDescription(f descriptionFlags, d descriptionContext) string {
	return strings.NewReplacer(
		"{{TYPELIST}}", d.typeList(), "{{AGENTDIR}}", d.agentDir,
		"{{ISOLATIONGUIDELINE}}", f.isolationGuideline(), "{{SCHEDULEGUIDELINE}}", f.scheduleGuideline(),
	).Replace(texts.Full)
}

func compactDescription(f descriptionFlags, d descriptionContext) string {
	return strings.NewReplacer(
		"{{COMPACTTYPELIST}}", d.compactTypeList(), "{{AGENTDIR}}", d.agentDir,
		"{{ISOLATIONCOMPACTGUIDELINE}}", f.isolationCompactGuideline(),
	).Replace(texts.Compact)
}

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

// renderDescriptionTemplate substitutes the {{placeholders}} of a custom template; an unknown one is left as it is,
// with a warning. upstream: index.ts renderToolDescriptionTemplate.
func renderDescriptionTemplate(template string, f descriptionFlags, d descriptionContext) string {
	vars := map[string]func() string{
		"typeList":           d.typeList,
		"compactTypeList":    d.compactTypeList,
		"agentDir":           func() string { return d.agentDir },
		"isolationGuideline": f.isolationGuideline,
		"scheduleGuideline":  f.scheduleGuideline,
	}
	return placeholder.ReplaceAllStringFunc(template, func(raw string) string {
		name := raw[2 : len(raw)-2]
		if fn, ok := vars[name]; ok {
			return fn()
		}
		d.warn(fmt.Sprintf("[pi-subagents] agent-tool-description.md: unknown placeholder %s left as-is", raw))
		return raw
	})
}

// loadCustomDescription reads .pi/agent-tool-description.md of the project, else the one in the agent dir. An empty
// or unreadable file is skipped with a warning. upstream: index.ts loadCustomToolDescription.
func loadCustomDescription(f descriptionFlags, d descriptionContext) (string, bool) {
	for _, path := range []string{filepath.Join(d.cwd, ".pi", "agent-tool-description.md"), filepath.Join(d.agentDir, "agent-tool-description.md")} {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				d.warn(fmt.Sprintf("[pi-subagents] failed to read %s: %v", path, err))
			}
			continue
		}
		if text := jsTrim(string(data)); text != "" {
			return renderDescriptionTemplate(text, f, d), true
		}
		d.warn(fmt.Sprintf("[pi-subagents] %s is empty — ignoring", path))
	}
	return "", false
}

// agentDescription is the Agent tool's description in the mode of f. upstream: index.ts agentToolDescription.
func agentDescription(f descriptionFlags, d descriptionContext) string {
	switch f.mode {
	case "compact":
		return compactDescription(f, d)
	case "custom":
		if text, ok := loadCustomDescription(f, d); ok {
			return text
		}
		d.warn(`[pi-subagents] toolDescriptionMode is "custom" but no agent-tool-description.md found — using "full"`)
	}
	return fullDescription(f, d)
}

// agentSchemaFor is the Agent parameters: the base ones with the available types and the agent dir filled in, then
// `isolation` and `schedule` (in that order, after the others) when offered. upstream: index.ts Type.Object of the
// Agent tool, isolationParam, scheduleParam.
func agentSchemaFor(f descriptionFlags, d descriptionContext, spec toolSpec) sdk.Schema {
	raw, _ := json.Marshal(spec.Parameters)
	text := strings.NewReplacer("{{TYPES}}", strings.Join(d.reg.availableTypes(), ", "), "{{AGENTDIR}}", d.agentDir).Replace(string(raw))
	var out sdk.Schema
	_ = json.Unmarshal([]byte(text), &out)
	props, _ := out["properties"].(map[string]any)
	if f.worktreeIsolation {
		props["isolation"] = texts.IsolationParam
	}
	if f.scheduling {
		props["schedule"] = texts.ScheduleParam
	}
	return out
}

// descriptionWarn reports a custom template's problems (the original's console.warn). A test replaces it.
var descriptionWarn = func(msg string) { fmt.Fprintln(os.Stderr, msg) }
