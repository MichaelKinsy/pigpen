package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `stop` and `interrupt` actions (src/runs/foreground/async-stop-action.ts):
// the port stops the run's own child process and seals the run's artifacts with
// the original's texts.

// stopAction is `subagent({action:"stop", id})`.
func init() {
	registerAction("stop", []string{"id", "runId", "dir", "childId"}, stopAction)
	registerAction("interrupt", []string{"id", "runId", "dir", "childId"}, interruptAction)
}

// stopAction is `subagent({action:"stop", id})`.
func stopAction(ctx sdk.Context, params map[string]any) (any, error) {
	id, _ := params["id"].(string)
	runID, _ := params["runId"].(string)
	dir, _ := params["dir"].(string)
	requested := jsTrim(id)
	if requested == "" {
		requested = jsTrim(runID)
	}
	sessionID := sessionIDOf(ctx)

	var run *asyncRun
	if requested != "" {
		run = runs.resolveRun(requested)
	} else if dir != "" {
		for _, r := range allRuns() {
			if r.Dir == dir {
				run = r
				break
			}
		}
	}
	if run == nil {
		target := requested
		if target == "" {
			target = "current"
		}
		return nil, sdk.NewToolError(fmt.Sprintf("No running or queued async run was found for '%s'.", target))
	}
	run.mu.Lock()
	owned := run.SessionID == "" || sessionID == "" || run.SessionID == sessionID
	state := run.State
	run.mu.Unlock()
	if !owned {
		return nil, sdk.NewToolError(fmt.Sprintf("Async run '%s' was not found in the active session.", run.ID))
	}
	if state != "running" && state != "queued" {
		return nil, sdk.NewToolError(fmt.Sprintf("No running or queued async run was found for '%s'.", requested))
	}
	childID, hasChild := params["childId"].(string)
	if hasChild && jsTrim(childID) != "" {
		// The port's single runs have one child, addressed by index 0.
		if jsTrim(childID) != "0" && jsTrim(childID) != run.Agent {
			return nil, sdk.NewToolError(fmt.Sprintf("Child '%s' in async run '%s' is not a pending or running child of this run.", childID, run.ID))
		}
	}
	run.requestStop()
	text := fmt.Sprintf("Stop requested for async run %s.", run.ID)
	if hasChild && jsTrim(childID) != "" {
		text = fmt.Sprintf("Stop requested for child %s in async run %s.", childID, run.ID)
	}
	return explicitOK{Content: text, Details: map[string]any{"mode": "management", "results": []any{}}}, nil
}

// interruptAction is `subagent({action:"interrupt", id})`: the port interrupts
// the run's child the way stop does; the original additionally asks a Pi child
// session to pause, which a child process cannot receive here.
func interruptAction(ctx sdk.Context, params map[string]any) (any, error) {
	res, err := stopAction(ctx, params)
	if err != nil {
		return nil, err
	}
	if ok, isText := res.(explicitOK); isText {
		ok.Content = strings.Replace(ok.Content, "Stop requested for", "Interrupt requested for", 1)
		return ok, nil
	}
	return res, nil
}

// allRuns lists every run this process tracks.
func allRuns() []*asyncRun {
	runs.mu.Lock()
	defer runs.mu.Unlock()
	out := make([]*asyncRun, 0, len(runs.order))
	for _, id := range runs.order {
		if r := runs.runs[id]; r != nil {
			out = append(out, r)
		}
	}
	return out
}

// stopAllRuns stops every tracked run of a session, as /subagents-stop does.
func stopAllRuns(ctx sdk.Context) int {
	sessionID := sessionIDOf(ctx)
	count := 0
	for _, r := range allRuns() {
		r.mu.Lock()
		same := r.SessionID == sessionID
		active := r.State == "queued" || r.State == "running"
		r.mu.Unlock()
		if same && active {
			r.requestStop()
			count++
		}
	}
	return count
}

// restoreRunsAfterReload re-checks runs this process no longer owns after the
// host reloaded the extension: their status files are reconciled on read, and
// their children (child processes of the old extension process) are gone.
func restoreRunsAfterReload() {
	entries, err := os.ReadDir(asyncRootDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		reconcileDiskRun(filepath.Join(asyncRootDir(), e.Name()))
	}
}
