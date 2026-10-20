package pi_subagents

import (
	"errors"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of the single-run part of the executor's run fan-out budget (src/runs/foreground/subagent-executor.ts: createRunFanoutBudget,
// claimRunFanoutBatch(["single"]) and withRunFanoutBudget): every run claims its slot of the run's budget before anything else
// starts, and its result carries the budget as a first line and as details.runFanoutBudget.

// runFanout is the claimed budget of one run.
type runFanout struct{ desc runFanoutBudgetDescriptor }

// claimRunFanout creates the budget of a top-level run (limit: config.maxSubagentSpawnsPerRun, then the environment, then 64) and
// claims the single slot. The error is the original's limit message.
func claimRunFanout(runID string) (*runFanout, error) {
	cfg := loadExtensionConfig()
	desc, err := createRunFanoutBudget(runID, resolveMaxSubagentSpawnsPerRun(cfg.MaxSubagentSpawnsPerRun, osEnv))
	if err != nil {
		return nil, err
	}
	if _, err := claimRunFanoutBatch(desc, []string{"single"}); err != nil {
		return nil, err
	}
	return &runFanout{desc: desc}, nil
}

// annotate is withRunFanoutBudget over a result or a failed result (the original's isError results, which the host turns into a
// failed tool call with the same text).
func (f *runFanout) annotate(res any, err error) (any, error) {
	snap, serr := getRunFanoutBudgetSnapshot(f.desc)
	if serr != nil {
		return res, err
	}
	line := formatRunFanoutBudget(snap)
	object := newObject()
	object.set("used", float64(snap.Used))
	object.set("limit", float64(snap.Limit))
	object.set("remaining", float64(snap.Remaining))
	if err != nil {
		if errors.Is(err, errCancelled) {
			return res, err
		}
		return nil, sdk.NewToolError(line + "\n" + err.Error())
	}
	switch r := res.(type) {
	case explicitOK:
		detailsSet(r.Details, "runFanoutBudget", object)
		r.Content = line + "\n" + r.Content
		return r, nil
	case sdk.ToolResult:
		detailsSet(r.Details, "runFanoutBudget", object)
		r.Content = line + "\n" + r.Content
		return r, nil
	}
	return res, err
}
