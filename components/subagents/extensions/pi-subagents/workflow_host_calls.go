package pi_subagents

import (
	"context"

	"regexp"
)

func sortStrings(s []string) { panic("not implemented") }

func (h *wfHost) pollAbort() { panic("not implemented") }

func jsErrString(err error) string { panic("not implemented") }

func recoveryBarrierMessage(sourceKey, target string) string { panic("not implemented") }

func isAcceptanceMetadataRecovery(result *jsObject) bool { panic("not implemented") }

func (h *wfHost) assertRecoveryBarrierAllowsRun(key string, params *jsObject) error {
	panic("not implemented")
}

func workflowStringMetadata(params *jsObject) (phase, label, agent string) { panic("not implemented") }

func traceWithMetadata(e WorkflowScriptTraceEntry, params *jsObject) WorkflowScriptTraceEntry {
	panic("not implemented")
}

// respondValue answers a call with a value (the "response" message, scripted-workflow.ts:2383).
func (h *wfHost) respondFuture(callID int, f *WorkflowFuture[any], responsePath string, onBoundaryError func(error)) {
	panic("not implemented")
}

func (h *wfHost) respondValue(callID int, v any, responsePath string, onBoundaryError func(error)) {
	panic("not implemented")
}

func (h *wfHost) respondRejected(callID int, err error) { panic("not implemented") }

func (h *wfHost) postResponse(callID int, ok bool, value any, errMsg, kind string) {
	panic("not implemented")
}

// onCall handles the script's calls (scripted-workflow.ts:2380-2701).
func (h *wfHost) onCall(callID int, method string, args *jsObject) { panic("not implemented") }

func workflowValidateKey(v any, owner string) (string, error) { panic("not implemented") }

func (h *wfHost) onState(callID int, method string, args *jsObject) { panic("not implemented") }

// asAny widens a child-result future for respondFuture.
func asAny(l *workflowLoop, f *WorkflowFuture[*jsObject]) *WorkflowFuture[any] {
	panic("not implemented")
}

func (h *wfHost) onStatus(callID int, args *jsObject) { panic("not implemented") }

func (h *wfHost) onSteer(callID int, args *jsObject) { panic("not implemented") }

var workflowHostOutputUnsafe = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// normalizeWorkflowHostCommandParams is normalizeWorkflowHostCommandParams (host-command.ts:44).
func normalizeWorkflowHostCommandParams(value any, label string) (WorkflowHostCommandParams, error) {
	panic("not implemented")
}

var workflowWhitespaceRuns = regexp.MustCompile(`\s+`)

func (h *wfHost) onHost(callID int, args *jsObject) { panic("not implemented") }

type wfLaunchPlan struct{}

// onRun launches one child (scripted-workflow.ts:2523-2701).
func (h *wfHost) onRun(callID int, args *jsObject) { panic("not implemented") }

// first: admission settles; then resume resolution, the concurrency slot, the launch itself.

// the admission rejection handler of the original is the aerr branch above; what follows records the outcome.

func nonNilString(o *jsObject, k string) string { panic("not implemented") }

func dedupeStrings(in []string) []string { panic("not implemented") }

// combinedAbortContext is combinedAbortSignal: the first of a and b to be cancelled cancels the result with its cause.
func combinedAbortContext(a, b context.Context) (context.Context, func()) { panic("not implemented") }

func parseWorkflowResumeReference(v any) (*WorkflowResumeRef, string) { panic("not implemented") }

func workflowGateShapeErrorGo(gate any) string { panic("not implemented") }

func workflowGateAcceptanceConflictGo(gate, acceptance any) string { panic("not implemented") }

var workflowAutoResumeParamKeys = []string{"acceptance", "agentContract", "baseRef", "index", "intercomBridge", "label", "lane", "maxRuntimeMs", "output", "outputMode", "outputSchema", "phase", "skill", "skills", "task", "timeoutMs", "toolBudget", "worktree"}

func isZeroUsage(v any) bool { panic("not implemented") }

// setupAbortResumeParams is setupAbortResumeParams (scripted-workflow.ts:2054): a child that failed before doing anything, with
// "This operation was aborted", is resumed once.
func setupAbortResumeParams(params, result *jsObject, ctx context.Context) *jsObject {
	panic("not implemented")
}

func childUsage(child *jsObject) any { panic("not implemented") }
