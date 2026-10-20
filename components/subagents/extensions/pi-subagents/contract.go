package pi_subagents

// The execution projection of a single run (src/runs/shared/agent-contract.ts): the public status of a run result.

// executionProjection is the original's ExecutionProjection: status, success, exitCode and the flags that explain them.
type executionProjection struct {
	Status      string `json:"status"`
	Success     bool   `json:"success"`
	ExitCode    int    `json:"exitCode"`
	Error       string `json:"error,omitempty"`
	TimedOut    bool   `json:"timedOut,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
	Stopped     bool   `json:"stopped,omitempty"`
	Detached    bool   `json:"detached,omitempty"`
}

// runOutcome is the part of a single result the projection reads.
type runOutcome struct {
	ExitCode                                 int
	Error                                    string
	Interrupted, TimedOut, Stopped, Detached bool
}

// buildExecutionProjection never marks a detached, stopped or interrupted run successful.
func buildExecutionProjection(r runOutcome) executionProjection {
	switch {
	case r.Detached:
		return executionProjection{Status: "detached", ExitCode: r.ExitCode, Detached: true, Error: r.Error}
	case r.Stopped:
		return executionProjection{Status: "stopped", ExitCode: r.ExitCode, Stopped: true, Error: r.Error}
	case r.Interrupted:
		return executionProjection{Status: "paused", ExitCode: r.ExitCode, Interrupted: true, Error: r.Error}
	}
	success := r.ExitCode == 0 && r.Error == "" && !r.TimedOut
	status := "failed"
	if success {
		status = "completed"
	}
	return executionProjection{Status: status, Success: success, ExitCode: r.ExitCode, Error: r.Error, TimedOut: r.TimedOut}
}
