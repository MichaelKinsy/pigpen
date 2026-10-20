package pi_subagents

// Resuming a finished background child: the recovery descriptor a run persists, the run location (id, prefix or directory)
// and the resume target. Ports async-resume.ts for single-child runs of the Go port.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// RecoveryDescriptor is what a run persists in recovery-descriptor.json so a finished child can be revived.
type RecoveryDescriptor struct {
	Version                 int            `json:"version"`
	LaunchContractDigest    string         `json:"launchContractDigest,omitempty"`
	SourceRunID             string         `json:"sourceRunId"`
	Agent                   string         `json:"agent"`
	SessionFile             string         `json:"sessionFile,omitempty"`
	Cwd                     string         `json:"cwd"`
	Model                   string         `json:"model,omitempty"`
	ModelProvider           string         `json:"modelProvider,omitempty"`
	ModelOverrideFromParent *bool          `json:"modelOverrideFromParent,omitempty"`
	ModelOrigin             string         `json:"modelOrigin,omitempty"`
	Fast                    *bool          `json:"fast,omitempty"`
	Thinking                string         `json:"thinking,omitempty"`
	ThinkingCeiling         string         `json:"thinkingCeiling,omitempty"`
	Tools                   []string       `json:"tools,omitempty"`
	AllowNestedSubagents    *bool          `json:"allowNestedSubagents,omitempty"`
	AllowedAgents           *[]string      `json:"allowedAgents,omitempty"`
	Extensions              []string       `json:"extensions,omitempty"`
	SubagentOnlyExtensions  []string       `json:"subagentOnlyExtensions,omitempty"`
	McpDirectTools          []string       `json:"mcpDirectTools,omitempty"`
	ExcludeTools            []string       `json:"excludeTools,omitempty"`
	MutationTools           []string       `json:"mutationTools,omitempty"`
	SystemPrompt            string         `json:"systemPrompt,omitempty"`
	SystemPromptMode        string         `json:"systemPromptMode"`
	InheritProjectContext   bool           `json:"inheritProjectContext"`
	InheritGlobalContext    bool           `json:"inheritGlobalContext"`
	InheritSkills           bool           `json:"inheritSkills"`
	Skills                  []string       `json:"skills,omitempty"`
	SkillPath               []string       `json:"skillPath,omitempty"`
	AgentFilePath           string         `json:"agentFilePath,omitempty"`
	Memory                  map[string]any `json:"memory,omitempty"`
	OutputPath              string         `json:"outputPath,omitempty"`
	OutputMode              string         `json:"outputMode"`
	StructuredOutputSchema  map[string]any `json:"structuredOutputSchema,omitempty"`
	SessionDir              string         `json:"sessionDir,omitempty"`
	ArtifactsDir            string         `json:"artifactsDir,omitempty"`
	MaxOutput               map[string]any `json:"maxOutput,omitempty"`
	Context                 string         `json:"context,omitempty"`
	AbsoluteDeadlineAt      *ms            `json:"absoluteDeadlineAt,omitempty"`
	MaxSubagentDepth        int            `json:"maxSubagentDepth"`
	Share                   bool           `json:"share"`
	BaseRef                 string         `json:"baseRef,omitempty"`
	RunFanoutBudget         map[string]any `json:"runFanoutBudget,omitempty"`
	// Carried is every other field the original's descriptor allows (acceptance, control, bridge, ceilings, bindings): the
	// port validates their shape only; a resume that would have to honor one refuses (resumeUnsupportedFields).
	Carried map[string]json.RawMessage `json:"-"`
}

type recoveryAlias RecoveryDescriptor

var recoveryKnown = jsonFieldNames(reflect.TypeOf(RecoveryDescriptor{}))

func (d RecoveryDescriptor) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(recoveryAlias(d), d.Carried)
}

// descriptorFields are the fields the original's descriptor allows (async-resume.ts readAsyncRecoveryDescriptor).
var descriptorAllowedFields = map[string]bool{
	"modelResponseAliases": true, "version": true, "launchContractDigest": true, "sourceRunId": true, "agentContract": true, "agent": true, "sessionFile": true, "cwd": true, "model": true, "modelProvider": true,
	"modelOverrideFromParent": true, "modelOrigin": true, "fast": true, "thinking": true, "thinkingCeiling": true, "tools": true, "allowNestedSubagents": true, "allowedAgents": true, "extensions": true,
	"subagentOnlyExtensions": true, "mcpDirectTools": true, "excludeTools": true, "mutationTools": true, "systemPrompt": true, "systemPromptMode": true, "inheritProjectContext": true, "inheritGlobalContext": true,
	"inheritSkills": true, "skills": true, "skillPath": true, "agentFilePath": true, "memory": true, "outputPath": true, "outputMode": true, "structuredOutputSchema": true, "acceptance": true, "sessionDir": true,
	"artifactConfig": true, "artifactsDir": true, "maxOutput": true, "controlConfig": true, "context": true, "intercomBridge": true, "absoluteDeadlineAt": true, "initialTurnBudget": true, "initialToolBudget": true,
	"maxSubagentDepth": true, "share": true, "capabilityCeiling": true, "launchResolvedExtensions": true, "runFanoutBudget": true, "lane": true, "baseRef": true, "extensionBindings": true, "requiredExtensions": true,
}

const invalidBaseRefText = "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."

func asStringList(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok || jsTrim(s) == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func isInteger(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int(f), true
}

// readAsyncRecoveryDescriptor reads and validates recovery-descriptor.json; nil when there is none.
func readAsyncRecoveryDescriptor(asyncDir string) (*RecoveryDescriptor, error) {
	if asyncDir == "" {
		return nil, nil
	}
	p := filepath.Join(asyncDir, "recovery-descriptor.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read async recovery descriptor '%s': %s", p, err.Error())
	}
	bad := func(format string, a ...any) (*RecoveryDescriptor, error) {
		return nil, fmt.Errorf("Invalid async recovery descriptor '%s': %s", p, fmt.Sprintf(format, a...))
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("Failed to parse async recovery descriptor '%s': %s", p, jsParseErrorText(data, err))
	}
	parsed, ok := value.(map[string]any)
	if !ok {
		return bad("expected an object.")
	}
	var keys []string
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !descriptorAllowedFields[k] {
			return bad("unknown field '%s'.", k)
		}
	}
	for _, f := range []string{"sourceRunId", "agent", "cwd", "systemPromptMode", "outputMode"} {
		if s, ok := parsed[f].(string); !ok || jsTrim(s) == "" {
			return bad("%s must be a non-empty string.", f)
		}
	}
	if v, _ := parsed["version"].(float64); v != 1 {
		return bad("version must be 1.")
	}
	if rf, has := parsed["runFanoutBudget"]; has {
		if _, err := validateRunFanoutBudgetDescriptor(rf); err != nil {
			return bad("%s", err.Error())
		}
	}
	if parsed["systemPromptMode"] != "append" && parsed["systemPromptMode"] != "replace" {
		return bad("systemPromptMode is invalid.")
	}
	if parsed["outputMode"] != "inline" && parsed["outputMode"] != "file-only" {
		return bad("outputMode is invalid.")
	}
	if c, has := parsed["context"]; has && c != "fresh" && c != "fork" {
		return bad("context is invalid.")
	}
	if v, has := parsed["modelOverrideFromParent"]; has {
		if _, ok := v.(bool); !ok {
			return bad("modelOverrideFromParent must be a boolean.")
		}
	}
	if v, has := parsed["fast"]; has {
		if _, ok := v.(bool); !ok {
			return bad("fast must be a boolean.")
		}
	}
	if o, has := parsed["modelOrigin"]; has && o != "explicit" && o != "inherited" && o != "configured" {
		return bad("modelOrigin must be 'explicit', 'inherited', or 'configured'.")
	}
	if _, has := parsed["modelOrigin"]; !has && parsed["model"] != nil {
		if fromParent, _ := parsed["modelOverrideFromParent"].(bool); fromParent {
			parsed["modelOrigin"] = "inherited"
		} else {
			parsed["modelOrigin"] = "configured"
		}
	}
	for _, f := range []string{"inheritProjectContext", "inheritSkills", "share"} {
		if _, ok := parsed[f].(bool); !ok {
			return bad("%s must be a boolean.", f)
		}
	}
	if g, has := parsed["inheritGlobalContext"]; !has {
		parsed["inheritGlobalContext"] = parsed["inheritProjectContext"]
	} else if _, ok := g.(bool); !ok {
		return bad("inheritGlobalContext must be a boolean.")
	}
	if v, has := parsed["allowNestedSubagents"]; has {
		if _, ok := v.(bool); !ok {
			return bad("allowNestedSubagents must be a boolean.")
		}
	}
	if v, has := parsed["allowedAgents"]; has {
		if _, ok := asStringList(v); !ok {
			if arr, isArr := v.([]any); !isArr || len(arr) != 0 {
				return bad("allowedAgents must be an array of non-empty strings.")
			}
		}
	}
	if d, ok := isInteger(parsed["maxSubagentDepth"]); !ok || d < 0 {
		return bad("maxSubagentDepth must be a non-negative integer.")
	}
	for _, f := range []string{"tools", "excludeTools", "extensions", "subagentOnlyExtensions", "mcpDirectTools", "mutationTools", "skills", "skillPath"} {
		if v, has := parsed[f]; has {
			if _, ok := asStringList(v); !ok {
				return bad("%s must contain non-empty strings.", f)
			}
		}
	}
	if v, has := parsed["systemPrompt"]; has {
		if _, ok := v.(string); !ok {
			return bad("systemPrompt must be a string.")
		}
	}
	for _, f := range []string{"launchContractDigest", "sessionFile", "model", "modelProvider", "thinking", "agentFilePath", "outputPath", "sessionDir", "artifactsDir"} {
		if v, has := parsed[f]; has {
			if s, ok := v.(string); !ok || jsTrim(s) == "" {
				return bad("%s must be a non-empty string.", f)
			}
		}
	}
	if v, has := parsed["baseRef"]; has {
		s, ok := v.(string)
		if !ok {
			return bad("baseRef must be a string.")
		}
		if !validGitRef(s) {
			return bad("%s", invalidBaseRefText)
		}
	}
	if v, has := parsed["structuredOutputSchema"]; has {
		if _, ok := v.(map[string]any); !ok {
			return bad("structuredOutputSchema must be an object.")
		}
	}
	if v, has := parsed["memory"]; has {
		m, ok := v.(map[string]any)
		if !ok {
			return bad("memory must be an object.")
		}
		scope, _ := m["scope"].(string)
		path, _ := m["path"].(string)
		if (scope != "project" && scope != "user") || jsTrim(path) == "" {
			return bad("memory is invalid.")
		}
	}
	if v, has := parsed["absoluteDeadlineAt"]; has {
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
			return bad("absoluteDeadlineAt must be a positive timestamp.")
		}
	}
	delete(parsed, "initialTurnBudget") // the removed turn budget: accepted for recovery compatibility, never restored
	if v, has := parsed["maxOutput"]; has {
		m, ok := v.(map[string]any)
		if !ok {
			return bad("maxOutput must be an object.")
		}
		for _, f := range []string{"bytes", "lines"} {
			if item, has := m[f]; has && item != nil {
				if n, ok := isInteger(item); !ok || n < 1 {
					return bad("maxOutput.%s must be a positive integer.", f)
				}
			}
		}
	}
	if v, has := parsed["artifactConfig"]; has {
		a, ok := v.(map[string]any)
		if !ok {
			return bad("artifactConfig must be an object.")
		}
		for _, f := range []string{"enabled", "includeInput", "includeOutput", "includeJsonl", "includeMetadata"} {
			if _, ok := a[f].(bool); !ok {
				return bad("artifactConfig.%s must be a boolean.", f)
			}
		}
		if t, has := a["includeTranscript"]; has {
			if _, ok := t.(bool); !ok {
				return bad("artifactConfig.includeTranscript must be a boolean.")
			}
		}
		if n, ok := isInteger(a["cleanupDays"]); !ok || n < 0 {
			return bad("artifactConfig.cleanupDays must be a non-negative integer.")
		}
	}
	if v, has := parsed["controlConfig"]; has {
		c, ok := v.(map[string]any)
		if !ok {
			return bad("controlConfig must be an object.")
		}
		if _, ok := c["enabled"].(bool); !ok {
			return bad("controlConfig.enabled must be a boolean.")
		}
		for _, f := range []string{"needsAttentionAfterMs", "activeNoticeAfterMs", "failedToolAttemptsBeforeAttention"} {
			if n, ok := isInteger(c[f]); !ok || n < 1 {
				return bad("controlConfig.%s must be a positive integer.", f)
			}
		}
		for _, f := range []string{"activeNoticeAfterTurns", "activeNoticeAfterTokens"} {
			if item, has := c[f]; has && item != nil {
				if n, ok := isInteger(item); !ok || n < 1 {
					return bad("controlConfig.%s must be a positive integer.", f)
				}
			}
		}
	}
	var d RecoveryDescriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return bad("%s", err.Error())
	}
	d.Version = 1
	if mo, ok := parsed["modelOrigin"].(string); ok {
		d.ModelOrigin = mo
	}
	d.InheritGlobalContext = parsed["inheritGlobalContext"] == true
	var all map[string]json.RawMessage
	_ = json.Unmarshal(data, &all)
	for k, v := range all {
		if !recoveryKnown[k] && k != "initialTurnBudget" {
			if d.Carried == nil {
				d.Carried = map[string]json.RawMessage{}
			}
			d.Carried[k] = v
		}
	}
	return &d, nil
}

// resumeUnsupportedFields names the descriptor fields the port cannot honor when it revives the child: a run the port
// started never carries them, one written by the original can.
func resumeUnsupportedFields(d *RecoveryDescriptor) []string {
	var out []string
	for _, k := range sortedKeys(d.Carried) {
		switch k {
		case "acceptance", "capabilityCeiling", "extensionBindings", "requiredExtensions", "lane", "intercomBridge", "controlConfig", "agentContract", "launchResolvedExtensions", "artifactConfig":
			out = append(out, k)
		}
	}
	if d.StructuredOutputSchema != nil {
		out = append(out, "structuredOutputSchema")
	}
	sort.Strings(out)
	return out
}
