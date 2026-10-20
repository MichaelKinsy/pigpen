package pi_subagents

// Live foreground controls of a session (the original's state.foregroundControls): what a steer or a stop addresses while a
// run is in progress. A workflow's child registers here under the child's run id with the workflow it belongs to.

// ForegroundChildControl is one live child of a foreground run.
type ForegroundChildControl struct {
	Index int
	Agent string
	// Steer delivers a message to the live child (mode steer, follow_up or auto) and reports delivered, queued or failed with a
	// reason; nil when the child cannot take one. A pig child started with --print reads no input after its task, so the
	// foreground runs of this port set it nil.
	Steer foregroundSteerFunc
}

// ForegroundControl is one live foreground run.
type ForegroundControl struct {
	RunID string
	// ParentWorkflowRunID and WorkflowKey are set on a workflow child's control.
	ParentWorkflowRunID string
	WorkflowKey         string
	// NestedRootRunID is the root run of the nested route the run inherited ("" at depth 0).
	NestedRootRunID string
	// ActiveChildren are the children running now by step index.
	ActiveChildren map[int]*ForegroundChildControl
}

// LiveChildCount is len(control.activeChildren?.size ?? 0).
func (c *ForegroundControl) LiveChildCount() int {
	if c == nil {
		return 0
	}
	return len(c.ActiveChildren)
}
