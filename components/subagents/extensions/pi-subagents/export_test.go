package pi_subagents

import (
	"context"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
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

// SetGlobalNpmLookup replaces the session-start lookup of the global npm root for one test (nil restores the real one).
func SetGlobalNpmLookup(t *testing.T, f func() string) {
	old := adv.lookup
	t.Cleanup(func() { adv.lookup = old })
	adv.lookup = func(context.Context) string { return f() }
}

// RefreshAdvertisement is the refresh a management mutation triggers.
func RefreshAdvertisement() { adv.refresh() }

// ResetNpmRootCaches forgets the process-wide `npm root -g` results and the discovery cache.
func ResetNpmRootCaches() {
	agentsNpmRoot, skillsNpmRoot = npmRootLookup{}, npmRootLookup{}
	clearAgentDiscoveryCache()
}

// RegisterTestCeiling registers a capability ceiling (a JSON object) for a session and returns its dispose function.
func RegisterTestCeiling(t *testing.T, sessionID, ceilingJSON string) func() {
	v, err := parseJSON([]byte(ceilingJSON))
	if err != nil {
		t.Fatal(err)
	}
	h, err := registerSubagentCapabilityCeiling(sessionID, "test", v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.dispose)
	return h.dispose
}

// RewriteInput and RewriteExtension drive rewritePromptWithGuidance from a command of a throwaway extension.
type RewriteInput = rewriteInput

func RewriteExtension(in RewriteInput, out *string, failure *error) *sdk.Extension {
	e := sdk.New("rewrite-probe")
	e.Command("rewrite", "rewrite", func(ctx sdk.Context, _ string) error {
		*out, *failure = rewritePromptWithGuidance(ctx, in)
		return nil
	})
	return e
}
