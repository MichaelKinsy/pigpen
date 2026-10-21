package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// A directory whose only markdown files are legacy .agents/skills files has no agent candidates: the inspection counts
// only the files that can be agents (agents.ts inspectAgentDefinitionDirectory).
func TestInspectionSkipsLegacySkillFiles(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, ".agents", "skills", "demo", "SKILL.md")
	writeFile(t, skill, "---\nname: demo\ndescription: A skill\n---\n\nSkill.\n")
	insp := inspectAgentDefinitionDirectory(root, nil)
	eq(t, insp.state, "empty", "only a skill file")
	eq(t, len(insp.files), 0, "no candidates")

	agent := filepath.Join(root, "agent.md")
	writeFile(t, agent, "---\nname: real\ndescription: An agent\n---\n\nAgent.\n")
	insp = inspectAgentDefinitionDirectory(root, nil)
	eq(t, insp.state, "candidates", "an agent beside the skill")
	eq(t, insp.files, []string{agent}, "the agent is the only candidate")
}

// statusBudget: the capacity line counts the session's slots after the ones whose runs ended with proof are released, and an
// absent limit reads "unlimited".
func TestStatusBudgetReconcilesTheSessionCapacity(t *testing.T) {
	withTempHome(t)
	tempRoot := tmp(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tempRoot)
	root := CapacityOptions{}.root()
	asyncDir := filepath.Join(tempRoot, "runs", "run-a")
	h := mustAcquire(t, "session-a", lim(2), "run-a", "runner", asyncDir, CapacityOptions{RootDir: root})
	_ = h.MarkStarted("runner-a")
	lines, _, capacity := statusBudget("session-a")
	eq(t, strings.Contains(lines, "\nActive async capacity: 1/unlimited used"), true, "a held slot: "+lines)
	eq(t, marshalJSON(capacity, ""), `{"used":1,"limit":0}`, "details with no limit")

	capWriteJSON(t, filepath.Join(asyncDir, "status.json"), map[string]any{"runId": "run-a", "sessionId": "session-a", "mode": "single", "state": "complete", "startedAt": 100,
		"processTerminal": observedProofJSON("run-a", "runner-a")})
	capWriteJSON(t, filepath.Join(asyncDir, "process-terminal.json"), observedProofJSON("run-a", "runner-a"))
	lines, _, capacity = statusBudget("session-a")
	eq(t, strings.Contains(lines, "\nActive async capacity: 0/unlimited used"), true, "the ended run's slot is released: "+lines)
	eq(t, marshalJSON(capacity, ""), `{"used":0,"limit":0}`, "details after the release")
}

// annotate is withRunFanoutBudget: the budget line and details on a result, the line on a failure, nothing on a cancelled
// run or when the budget cannot be read.
func TestRunFanoutAnnotate(t *testing.T) {
	withTempHome(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
	const line = "Run fan-out: 1/64 used, 63 remaining"
	fan, err := claimRunFanout("run-1")
	if err != nil {
		t.Fatal(err)
	}
	failed := sdk.ToolResult{Content: "boom", IsError: true, Details: map[string]any{}}
	res, err := fan.annotate(failed, nil)
	eq(t, err == nil, true, "a failed run result is a result")
	got := res.(sdk.ToolResult)
	eq(t, got.Content, line+"\nboom", "failed run result content")
	budget, ok := got.Details.(map[string]any)["runFanoutBudget"]
	eq(t, ok, true, "failed run result details")
	eq(t, marshalJSON(budget, ""), `{"used":1,"limit":64,"remaining":63}`, "failed run result budget")

	_, err = fan.annotate(nil, errors.New("launch refused"))
	eq(t, err.Error(), line+"\nlaunch refused", "a failure carries the line")
	_, err = fan.annotate(nil, errCancelled)
	eq(t, errors.Is(err, errCancelled) && err.Error() == "cancelled", true, "a cancelled run is returned as it is")

	if rerr := os.RemoveAll(fan.desc.Directory); rerr != nil {
		t.Fatal(rerr)
	}
	_, err = fan.annotate(nil, errors.New("launch refused"))
	eq(t, err.Error(), "launch refused", "an unreadable budget leaves the failure alone")
	res, err = fan.annotate("plain", nil)
	eq(t, res == "plain" && err == nil, true, "an unreadable budget leaves a result alone")
}

// The launch identity covers the tools the plan excludes.
func TestLaunchBindingCoversExcludedTools(t *testing.T) {
	src := func(exclude []string) launchBindingSource {
		return launchBindingSource{Agent: AgentConfig{Name: "helper"}, Task: "build", ToolPlan: &piLaunchToolPlan{ExcludeTools: exclude}}
	}
	none := resolveLaunchBinding(src(nil)).LaunchContractDigest
	write := resolveLaunchBinding(src([]string{"write"})).LaunchContractDigest
	edit := resolveLaunchBinding(src([]string{"edit"})).LaunchContractDigest
	eq(t, none != write && write != edit && none != edit, true, "excluded tools change the digest")
}

// extensionBindings carry at most 256 properties in total (namespaces and nested keys).
func TestExtensionBindingsPropertyBound(t *testing.T) {
	build := func(inner int) map[string]any {
		m := map[string]any{}
		for i := 0; i < inner; i++ {
			m["k"+fmt.Sprint(i)] = true
		}
		return map[string]any{"pkg/1": m}
	}
	_, err := normalizeExtensionBindings(build(maxExtensionBindingsProperties - 1))
	eq(t, err == nil, true, "256 properties")
	_, err = normalizeExtensionBindings(build(maxExtensionBindingsProperties))
	eq(t, err != nil && err.Error() == "extensionBindings exceeds 256 total properties.", true, "257 properties")
}
