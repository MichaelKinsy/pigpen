package pi_subagents

import (
	"testing"
	"time"
)

// Internals for the external tests (run_host_test.go, which uses the fake host of package pi_subagents_test).

type ChildRequest = childRequest
type ChildResult = childResult

var (
	Eq           = eq
	WriteFile    = writeFile
	Tmp          = tmp
	AgentMD      = agentMD
	WithTempHome = withTempHome
	// ErrCancelled is the cancellation error execChild returns for a stopped child.
	ErrCancelled = errCancelled
)

// SetRunChild replaces the child runner for one test. Its cleanup stops the
// tracked runs first: a background run must not read runChild while the
// restore writes it.
func SetRunChild(t *testing.T, f func(done <-chan struct{}, req ChildRequest) (ChildResult, error)) {
	old := runChild
	t.Cleanup(func() {
		stopAll()
		runChild = old
	})
	runChild = f
}

// ResetRuns stops and forgets every tracked run before and after one test, so
// the process-wide registry cannot leak between tests.
func ResetRuns(t *testing.T) {
	stopAll()
	clearRuns()
	t.Cleanup(func() {
		stopAll()
		clearRuns()
	})
}

func stopAll() {
	targets := allRuns()
	for _, r := range targets {
		r.requestStop()
	}
	// Wait for the runs to settle so their goroutines stop writing into
	// test directories that are about to be removed.
	deadline := time.After(3 * time.Second)
	for _, r := range targets {
		select {
		case <-r.doneCh:
		case <-deadline:
			return
		}
	}
}

func clearRuns() {
	runs.mu.Lock()
	runs.runs = map[string]*asyncRun{}
	runs.order = nil
	runs.mu.Unlock()
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
