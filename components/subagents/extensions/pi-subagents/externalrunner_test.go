package pi_subagents

import (
	"strings"
	"testing"
)

// External-runner agents: an external-cli agent runs its own binary (the
// original's runner contract, generic stdin delivery); an unavailable binary
// and an external-job provider are reported, never launched through pig.

func TestExternalCLIAgentsRunTheirBinary(t *testing.T) {
	isolateTemp(t)
	// A present binary runs: "hostname" ships on every supported platform.
	agent := &AgentConfig{Name: "hostinfo", Runner: &Runner{Type: "external-cli", Command: "hostname"}}
	out, err := runAgent(nil, t.TempDir(), agent, "the task", "")
	if err != nil {
		t.Fatalf("external-cli run: %v", err)
	}
	if jsTrim(out) == "" {
		t.Errorf("no output from the CLI: %q", out)
	}

	// An unavailable binary names the reason instead of launching pig.
	ghost := &AgentConfig{Name: "ghost", Runner: &Runner{Type: "external-cli", Command: "no-such-cli-xyz"}}
	_, err = runAgent(nil, t.TempDir(), ghost, "t", "")
	if err == nil || !strings.Contains(err.Error(), "External CLI 'no-such-cli-xyz' is not available for agent 'ghost':") {
		t.Errorf("unavailable binary: %v", err)
	}

	// An external-job provider is refused with the port's message.
	job := &AgentConfig{Name: "jobber", Runner: &Runner{Type: "external-job", Command: "some-provider"}}
	_, err = runAgent(nil, t.TempDir(), job, "t", "")
	if err == nil || !strings.Contains(err.Error(), "External-job runner 'some-provider' is not available in the Go port.") {
		t.Errorf("external-job: %v", err)
	}
}

// A regular agent still runs through pig (the runner dispatch is per-agent).
func TestRunnerDispatchLeavesPigAgentsAlone(t *testing.T) {
	isolateTemp(t)
	fakeRunChild(t, func(done <-chan struct{}, req childRequest) (childResult, error) {
		if req.Bin == "hostname" {
			t.Error("a pig agent went through the external CLI")
		}
		return childResult{Stdout: "ok"}, nil
	})
	out, err := runAgent(nil, t.TempDir(), &AgentConfig{Name: "pig-agent", SystemPromptMode: "replace"}, "t", "")
	eq(t, err, nil, "run")
	eq(t, out, "ok", "answer")
}
