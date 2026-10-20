package pi_subagents

// Resuming a finished background child: the recovery descriptor a run persists, the run location (id, prefix or directory)
// and the resume target. Ports async-resume.ts for single-child runs of the Go port.

import (
	"encoding/json"

	"reflect"
	"regexp"
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

func (d RecoveryDescriptor) MarshalJSON() ([]byte, error) { panic("not ported yet") }

// descriptorFields are the fields the original's descriptor allows (async-resume.ts readAsyncRecoveryDescriptor).
var descriptorAllowedFields = map[string]bool{
	"modelResponseAliases": true, "version": true, "launchContractDigest": true, "sourceRunId": true, "agentContract": true, "agent": true, "sessionFile": true, "cwd": true, "model": true, "modelProvider": true,
	"modelOverrideFromParent": true, "modelOrigin": true, "fast": true, "thinking": true, "thinkingCeiling": true, "tools": true, "allowNestedSubagents": true, "allowedAgents": true, "extensions": true,
	"subagentOnlyExtensions": true, "mcpDirectTools": true, "excludeTools": true, "mutationTools": true, "systemPrompt": true, "systemPromptMode": true, "inheritProjectContext": true, "inheritGlobalContext": true,
	"inheritSkills": true, "skills": true, "skillPath": true, "agentFilePath": true, "memory": true, "outputPath": true, "outputMode": true, "structuredOutputSchema": true, "acceptance": true, "sessionDir": true,
	"artifactConfig": true, "artifactsDir": true, "maxOutput": true, "controlConfig": true, "context": true, "intercomBridge": true, "absoluteDeadlineAt": true, "initialTurnBudget": true, "initialToolBudget": true,
	"maxSubagentDepth": true, "share": true, "capabilityCeiling": true, "launchResolvedExtensions": true, "runFanoutBudget": true, "lane": true, "baseRef": true, "extensionBindings": true, "requiredExtensions": true,
}

var gitRefForbidden = regexp.MustCompile("[\\[\\]\\\\~^:?*\\x00-\\x20\\x7f]")
var gitHexRef = regexp.MustCompile(`(?i)^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// validGitRef is worktree.ts validGitRef.
func validGitRef(ref string) bool { panic("not ported yet") }

const invalidBaseRefText = "baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported."

func asStringList(v any) ([]string, bool) { panic("not ported yet") }

func isInteger(v any) (int, bool) { panic("not ported yet") }

// readAsyncRecoveryDescriptor reads and validates recovery-descriptor.json; nil when there is none.
func readAsyncRecoveryDescriptor(asyncDir string) (*RecoveryDescriptor, error) {
	panic("not ported yet")
}

// the removed turn budget: accepted for recovery compatibility, never restored

// resumeUnsupportedFields names the descriptor fields the port cannot honor when it revives the child: a run the port
// started never carries them, one written by the original can.
func resumeUnsupportedFields(d *RecoveryDescriptor) []string { panic("not ported yet") }

// ── run-fanout-budget.ts (validation of a persisted descriptor; the port starts no fan-out budgets itself) ──

func runFanoutRoot() string { panic("not ported yet") }

func validateRunFanoutBudgetDescriptor(value any) error { panic("not ported yet") }
