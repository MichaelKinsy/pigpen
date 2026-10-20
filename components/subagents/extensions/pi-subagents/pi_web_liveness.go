package pi_subagents

// pi-web-session-liveness.ts. The original also registers a provider with pi-web's in-process registry
// (globalThis[Symbol.for("@agegr/pi-web/session-liveness/v1")], a Node host mechanism with no pig counterpart); the part
// that decides whether the session still has live subagent work is ported.

// retainLiveForegroundNestedRoute keeps the route of a foreground run that ended with a live nested descendant, so the session
// stays live until the tracker sees the descendant end. It reports whether the route was retained.
func retainLiveForegroundNestedRoute(state *SubagentState, route nestedRoute) (bool, error) {
	reg, err := projectNestedEvents(route)
	if err != nil {
		return false, err
	}
	if !hasLiveNestedDescendants(reg.Children) {
		return false, nil
	}
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if state.RetainedForegroundNestedRoutes == nil {
		state.RetainedForegroundNestedRoutes = map[string]*nestedRoute{}
	}
	r := route
	state.RetainedForegroundNestedRoutes[route.RootRunID] = &r
	return true, nil
}

// hasLiveSubagentWork is true while an async job is queued or running (or has live nested descendants), a foreground control
// is scheduling or has active children (or nested descendants), or a retained foreground nested route remains.
func hasLiveSubagentWork(state *SubagentState) bool {
	state.Mu.Lock()
	defer state.Mu.Unlock()
	for _, j := range state.AsyncJobs {
		if j.Status == "queued" || j.Status == "running" || hasLiveNestedDescendants(j.NestedChildren) {
			return true
		}
	}
	for _, c := range state.ForegroundControls {
		if c.SchedulingOwners > 0 || len(c.ActiveChildren) > 0 || hasLiveNestedDescendants(c.NestedChildren) {
			return true
		}
	}
	return len(state.RetainedForegroundNestedRoutes) > 0
}
