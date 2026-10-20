package pi_subagents

import "testing"

// Internals for the external tests (run_host_test.go, which uses the fake host of package pi_subagents_test).

type ChildRequest = childRequest
type ChildResult = childResult

var (
	Eq           = eq
	WriteFile    = writeFile
	Tmp          = tmp
	AgentMD      = agentMD
	WithTempHome = withTempHome
)

// SetRunChild replaces the child runner for one test.
func SetRunChild(t *testing.T, f func(done <-chan struct{}, req ChildRequest) (ChildResult, error)) {
	old := runChild
	t.Cleanup(func() { runChild = old })
	runChild = f
}

// ToolParamNames are the parameters of the subagent tool's schema (the original's), and OriginalCommands its commands.
func ToolParamNames() []string {
	props, _ := toolDefs()[subagentName].Parameters["properties"].(map[string]any)
	var names []string
	for k := range props {
		names = append(names, k)
	}
	return names
}

var Refusal = refusal

// SetAsyncKill replaces the process probe of the control actions for one test.
func SetAsyncKill(t *testing.T, f func(pid, signal int) error) {
	old := asyncKill
	t.Cleanup(func() { asyncKill = old })
	asyncKill = f
}

// WriteAsyncResultFile is writeAsyncResultFile for the tests that set up a paused run.
func WriteAsyncResultFile(path string, data map[string]any) error {
	_, err := writeAsyncResultFile(path, data)
	return err
}

// ResultFilePath is the result file of a run.
func ResultFilePath(dir, id string) string { return resultFilePath(dir, id) }

// AcquireCapacity takes the one active async capacity slot (limit 1) of a session for a run and, when runner is not empty, marks
// it started by that runner; the result releases the slot again (Rollback).
func AcquireCapacity(session, runID, asyncDir, runner string) (rollback func() bool, err error) {
	limit := 1
	h, err := acquireActiveAsyncCapacity(session, &limit, runID, "runner", asyncDir, CapacityOptions{})
	if err != nil {
		return nil, err
	}
	if runner != "" {
		if err := h.MarkStarted(runner); err != nil {
			return nil, err
		}
	}
	return h.Rollback, nil
}

// ErrNoSuchProcess is what a kill probe reports for a process that is gone.
var ErrNoSuchProcess = errNoSuchProcess
