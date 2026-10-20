package pi_subagents

import (
	"fmt"
	"strings"
)

// Port of src/extension/public-execution.ts: the public boundary of the subagent tool. Everything a model may send is checked
// and normalized here before it reaches the executor, with the original's error texts.

// publicExecution is the original's PublicSubagentExecutionNormalization: the normalized params, or the error and the
// mode ("workflow" or "management") it is reported under.
type publicExecution struct {
	OK     bool
	Params map[string]any
	Error  string
	Mode   string
}

func publicFail(msg, mode string) publicExecution { return publicExecution{Error: msg, Mode: mode} }

// normalizePublicBaseRef is the original's normalizeWorktreeBaseRef (a validation of the ref, which is not resolved here).
func normalizePublicBaseRef(v any) error {
	if s, ok := v.(string); !ok || !validGitRef(s) {
		return fmt.Errorf("baseRef must be a valid Git ref: use HEAD or a supported named ref (for example, refs/heads/main). Full 40/64-character commit IDs and revision expressions are unsupported.")
	}
	return nil
}

// validateWorkflowCapacityOverrides: globalConcurrencyLimit and maxSubagentSpawnsPerRun must be positive safe integers.
func validateWorkflowCapacityOverrides(p map[string]any) string {
	for _, name := range []string{"globalConcurrencyLimit", "maxSubagentSpawnsPerRun"} {
		v, ok := p[name]
		if !ok || isUndefined(v) {
			continue
		}
		if f, isNum := asNumber(v); !isNum || !isSafeInteger(v) || f <= 0 {
			return name + " must be a positive safe integer."
		}
	}
	return ""
}

func defined(p map[string]any, k string) bool { v, ok := p[k]; return ok && !isUndefined(v) }

func nonBlankString(v any) bool { s, ok := v.(string); return ok && jsTrim(s) != "" }

// normalizePublicSubagentExecution enforces the public execution cutover. Internal runs.run children and structured owned
// delegation bypass this boundary.
func normalizePublicSubagentExecution(in map[string]any) publicExecution {
	params := make(map[string]any, len(in))
	for k, v := range in {
		params[k] = v
	}
	modeOfAction := func() string {
		if !defined(params, "action") {
			return "workflow"
		}
		return "management"
	}
	for _, field := range []string{"resource", "resourceProvenance", "workflowResource", "workflowResourceProvenance", "workflowResourcePermit", "resourcePermit", "permit"} {
		if defined(params, field) {
			return publicFail("Public execution does not accept workflow resource provenance or permit fields.", modeOfAction())
		}
	}
	if defined(params, "baseRef") {
		if err := normalizePublicBaseRef(params["baseRef"]); err != nil {
			return publicFail(err.Error(), modeOfAction())
		}
	}
	if defined(params, "workflowScript") && defined(params, "workflowScriptPath") {
		return publicFail("workflowScript and workflowScriptPath are mutually exclusive.", "workflow")
	}
	hasNamed := defined(params, "workflow")
	if hasNamed && !nonBlankString(params["workflow"]) {
		return publicFail("workflow must be a non-empty named workflow resource.", "workflow")
	}
	if hasNamed && (defined(params, "workflowScript") || defined(params, "workflowScriptPath")) {
		return publicFail("workflow is mutually exclusive with workflowScript and workflowScriptPath.", "workflow")
	}
	hasWorkflowInput := defined(params, "workflowScript") || defined(params, "workflowScriptPath") || hasNamed
	if !hasWorkflowInput && defined(params, "args") {
		return publicFail("args requires workflow, workflowScript, or workflowScriptPath.", "workflow")
	}
	if defined(params, "globalConcurrencyLimit") || defined(params, "maxSubagentSpawnsPerRun") {
		if msg := validateWorkflowCapacityOverrides(params); msg != "" {
			return publicFail(msg, modeOfAction())
		}
		act, _ := params["action"].(string)
		validatesSpawnBudget := jsTrim(act) == "validate" && act != "" && !defined(params, "globalConcurrencyLimit") && defined(params, "maxSubagentSpawnsPerRun")
		if (defined(params, "action") && !validatesSpawnBudget) || hasNamed || (!defined(params, "workflowScript") && !defined(params, "workflowScriptPath")) {
			return publicFail("Workflow capacity overrides are only supported on top-level workflowScript or workflowScriptPath calls; validate accepts maxSubagentSpawnsPerRun for static budget checks.", modeOfAction())
		}
	}
	if defined(params, "preflight") && !hasWorkflowInput {
		return publicFail("preflight requires workflowScript or workflowScriptPath.", modeOfAction())
	}
	if defined(params, "preflight") && hasNamed && !defined(params, "workflowScript") && !defined(params, "workflowScriptPath") {
		return publicFail("preflight is not supported with named workflow resources.", "workflow")
	}
	hasValidWorkflowInput := nonBlankString(params["workflowScript"]) || nonBlankString(params["workflowScriptPath"]) || nonBlankString(params["workflow"])
	inputMode := func() string {
		if hasWorkflowInput {
			return "workflow"
		}
		return "management"
	}
	if defined(params, "isolation") {
		iso := params["isolation"]
		if iso != "none" && iso != "worktree" {
			return publicFail("isolation must be 'none' or 'worktree'.", inputMode())
		}
		isWorktree := iso == "worktree"
		if defined(params, "worktree") && params["worktree"] != isWorktree {
			return publicFail(fmt.Sprintf("isolation '%s' conflicts with worktree: %s.", iso, jsToString(params["worktree"])), inputMode())
		}
		delete(params, "isolation")
		params["worktree"] = isWorktree
	}
	if defined(params, "runFanoutBudget") || defined(params, "runFanoutAdmitted") {
		return publicFail("Public execution does not accept internal run fan-out fields.", inputMode())
	}
	for _, k := range []string{"workflowParentRunId", "workflowKey", "workflowChildAsyncId", "workflowAwaitAsync", "workflowAwaitDetached", "workflowParentDeadlineAt", "suppressRoutineResultIntercom"} {
		if defined(params, k) {
			return publicFail("Public execution does not accept internal workflow child fields.", inputMode())
		}
	}
	action, hasAction := params["action"]
	hasAction = hasAction && !isUndefined(action)
	if hasAction {
		if s, ok := action.(string); !ok || jsTrim(s) == "" {
			return publicFail("action must be a non-empty management/control action, or omit action and use workflowScript.", "management")
		}
	}
	normalizedAction := ""
	if hasAction {
		normalizedAction = jsTrim(action.(string))
	}
	if defined(params, "baseRef") && hasAction && normalizedAction != "resume" && normalizedAction != "schedule.create" {
		return publicFail("baseRef is only supported for child execution, resume, and schedule.create.", "management")
	}
	if hasAction && hasNamed {
		return publicFail("Named workflow resource execution must omit action.", "management")
	}
	if defined(params, "clarify") {
		return publicFail("Public workflowScript execution does not support clarify UI.", "workflow")
	}
	if defined(params, "chainName") {
		return publicFail("Durable chain management was removed; use workflowScript or /prompt-workflow for repeatable workflows.", "management")
	}
	if cfg, ok := asObject(params["config"]); ok && cfg.hasOwn("steps") {
		return publicFail("Durable chain definitions were removed; use workflowScript or /prompt-workflow for repeatable workflows.", "management")
	}
	if defined(params, "resume") {
		return publicFail("Top-level resume execution is not available. Put resume on a workflowScript runs.run/runs.all item.", "workflow")
	}
	if defined(params, "tasks") || defined(params, "chain") || defined(params, "parallel") || defined(params, "concurrency") || defined(params, "chainDir") {
		mode := "workflow"
		if normalizedAction != "" {
			mode = "management"
		}
		return publicFail("Legacy top-level chain and parallel inputs were removed; use workflowScript.", mode)
	}
	if hasAction {
		switch strings.ToLower(normalizedAction) {
		case "append-step":
			return publicFail("Legacy append-step control was removed from the public subagent tool; use current workflowScript orchestration.", "management")
		case "approve-checkpoint", "reject-checkpoint":
			return publicFail("Legacy checkpoint approval controls were removed from the public subagent tool; use current workflowScript orchestration.", "management")
		case "single":
			return publicFail("action='single' is not supported. Omit action and pass { agent, task } for one child.", "workflow")
		case "parallel", "tasks", "chain":
			return publicFail("Legacy top-level chain and parallel inputs were removed; use workflowScript.", "workflow")
		}
		if normalizedAction == "validate" || normalizedAction == "schedule.create" {
			if defined(params, "agent") || defined(params, "task") || defined(params, "step") {
				return publicFail(normalizedAction+" requires workflowScript or workflowScriptPath and does not accept direct agent, task, or step execution fields.", "management")
			}
			if !hasValidWorkflowInput {
				return publicFail(normalizedAction+" requires a non-empty workflowScript or workflowScriptPath.", "management")
			}
			params["action"] = normalizedAction
			return publicExecution{OK: true, Params: params}
		}
		if hasWorkflowInput {
			return publicFail("Workflow execution must omit action; only validate and schedule.create accept action with workflowScript or workflowScriptPath.", "management")
		}
		if defined(params, "task") {
			return publicFail("Structured single-child task cannot be combined with a management/control action.", "management")
		}
		params["action"] = normalizedAction
		return publicExecution{OK: true, Params: params}
	}
	if defined(params, "step") {
		return publicFail("step is not a public execution field; use workflowScript for orchestration.", "workflow")
	}
	if hasWorkflowInput && (defined(params, "agent") || defined(params, "task")) {
		return publicFail("Structured single-child execution cannot be combined with workflow, workflowScript, or workflowScriptPath.", "workflow")
	}
	if defined(params, "agent") || defined(params, "task") {
		agent, isStr := params["agent"].(string)
		if !isStr || jsTrim(agent) == "" {
			return publicFail("Structured single-child execution requires agent to be a non-empty string.", "workflow")
		}
		if defined(params, "task") {
			if _, ok := params["task"].(string); !ok {
				return publicFail("Structured single-child task must be a string when provided.", "workflow")
			}
		}
		params["agent"] = jsTrim(agent)
		if !defined(params, "output") {
			params["output"] = true
		}
		return publicExecution{OK: true, Params: params}
	}
	if !hasValidWorkflowInput {
		return publicFail("Execution requires either { agent, task? } for one child, a named workflow resource, or a non-empty workflowScript or workflowScriptPath for orchestration.", "workflow")
	}
	return publicExecution{OK: true, Params: params}
}
