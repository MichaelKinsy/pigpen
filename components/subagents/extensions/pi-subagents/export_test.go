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

// StartPaneFake is a scripted herdr-core whose parent terminal is "t1"; it returns its socket and a counter of the calls
// it got for a method.
func StartPaneFake(t *testing.T, answer string) (string, func(method string) int) {
	r := newPaneRig(t, bothLabels, []string{"idle/0", "working/0", "blocked/1", "blocked/1", "working/1", "done/2"}, answer)
	return r.fake.socket, func(m string) int { return len(r.fake.calls(m)) }
}
