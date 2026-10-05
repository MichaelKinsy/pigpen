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
