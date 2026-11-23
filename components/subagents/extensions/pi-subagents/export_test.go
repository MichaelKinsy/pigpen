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
)

// ErrCancelled is the child runner's cancellation error: the run's control channel closed (a stop, an interrupt, or
// the deadline) and the child ended with it.
var ErrCancelled = errCancelled

// SetRunChild replaces the child runner for one test.
func SetRunChild(t *testing.T, f func(done <-chan struct{}, req ChildRequest) (ChildResult, error)) {
	old := runChild
	t.Cleanup(func() {
		// A detached child may still be starting when its test ends, and it reads the runner when it starts:
		// wait for the registry to settle first, so the swap is ordered after that read (the race detector sees
		// the otherwise unsynchronized pair, and a run that never settles must not hold the cleanup forever).
		waitForRunsToSettle()
		runChild = old
	})
	runChild = f
}

// waitForRunsToSettle waits for the runs still in the registry to end, or for a second.
func waitForRunsToSettle() {
	deadline := time.Now().Add(time.Second)
	for len(fleet.active()) > 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
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

// StartPaneFake is a scripted herdr-core whose parent terminal is "t1"; it returns its socket and a counter of the calls
// it got for a method.
func StartPaneFake(t *testing.T, answer string) (string, func(method string) int) {
	s, calls, _ := StartPaneFakeParams(t, answer)
	return s, calls
}

// StartPaneFakeParams is StartPaneFake that also returns the params of every call to a method.
func StartPaneFakeParams(t *testing.T, answer string) (string, func(method string) int, func(method string) []map[string]any) {
	r := newPaneRig(t, bothLabels, []string{"idle/0", "working/0", "blocked/1", "blocked/1", "working/1", "done/2"}, answer)
	return r.fake.socket, func(m string) int { return len(r.fake.calls(m)) }, r.fake.calls
}
