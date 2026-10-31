package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The run parameters through the host boundary: the deadline and the output binding.

func TestTimeoutMsStopsAForegroundRun(t *testing.T) {
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	started := make(chan struct{}, 1)
	blocked := make(chan struct{})
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		started <- struct{}{}
		select {
		case <-done:
			return sub.ChildResult{}, sub.ErrCancelled
		case <-blocked:
			return sub.ChildResult{Stdout: "late"}, nil
		}
	})
	defer close(blocked)
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-timeout"}, ""
			}
			return nil, ""
		},
	})
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": false, "timeoutMs": 50})
	<-started
	eq(t, failure, "Subagent timed out after 50ms.", "the original's timeout text")
}

func TestOutputBindingThroughTheTool(t *testing.T) {
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	fakeChildren(t, map[string]string{"summarize": "the findings"})
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-output"}, ""
			}
			return nil, ""
		},
	})
	// The bound file receives the answer and the result is the original's reference.
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "summarize", "async": false, "output": "out/answer.md"})
	eq(t, failure, "", "run")
	text := resultContent(t, raw)
	path := filepath.Join(cwd, "out", "answer.md")
	abs, _ := filepath.Abs(path)
	if !strings.Contains(text, "Output saved to: "+abs) || !strings.Contains(text, "Read this file if needed.") {
		t.Errorf("reference: %s", text)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, string(body), "the findings", "the artifact")
	// file-only without output keeps the original's message.
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": false, "outputMode": "file-only"})
	if !strings.Contains(failure, `sets outputMode: "file-only" but does not configure an output file.`) {
		t.Errorf("file-only: %s", failure)
	}
	// output: false keeps no inline output.
	raw, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": false, "output": false})
	eq(t, failure, "", "suppressed")
	eq(t, resultContent(t, raw), "(no output)", "no inline output")
}
