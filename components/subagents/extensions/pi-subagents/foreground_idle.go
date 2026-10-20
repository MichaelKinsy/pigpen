package pi_subagents

import "sync"

// removeForegroundControlIfIdle (runs/foreground/subagent-executor.ts) drops a foreground control that has nothing scheduled and no
// active child. A control whose nested route still has live descendants hands the route to the retained set first (and tells the
// tracker, when there is one), so the session stays live until those descendants end. It reports whether the control was removed.
func removeForegroundControlIfIdle(state *SubagentState, runID string, track func(rootRunID string)) bool {
	state.Mu.Lock()
	control := state.ForegroundControls[runID]
	settled := control == nil || (control.SchedulingOwners == 0 && len(control.ActiveChildren) == 0)
	state.Mu.Unlock()
	if !settled {
		return false
	}
	if control != nil && control.NestedRoute != nil && track != nil {
		if ok, err := retainLiveForegroundNestedRoute(state, *control.NestedRoute); err != nil {
			nestedRouteLog("Failed to retain live nested descendants for foreground run '"+runID+"':", err)
		} else if ok {
			track(runID)
		}
	}
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if control != nil {
		retainNestedLookupRoute(state, control.NestedRoute, control.SessionID)
	}
	delete(state.ForegroundControls, runID)
	if state.LastForegroundControlID == runID {
		state.LastForegroundControlID = ""
	}
	return true
}

// retainNestedLookupRoute keeps the route of the current session's foreground run for later id lookups (state.Mu held).
func retainNestedLookupRoute(state *SubagentState, route *nestedRoute, sessionID string) {
	if route == nil || sessionID == "" || sessionID != state.CurrentSessionID {
		return
	}
	if state.RetainedNestedLookupRoutes == nil || state.RetainedNestedLookupRoutes.SessionID != sessionID {
		state.RetainedNestedLookupRoutes = &retainedNestedLookupRoutes{SessionID: sessionID, Routes: map[string]*nestedRoute{}}
	}
	r := *route
	state.RetainedNestedLookupRoutes.Routes[route.RootRunID] = &r
}

// resultDeliveryOwnership decides which sessions' results this runtime delivers: the current one, and a bounded number of
// predecessor sessions a session replacement proved to be this runtime's own (runs/background/result-delivery-ownership.ts).
type resultDeliveryOwnership struct {
	mu      sync.Mutex
	current func() (sessionID, completionOwnerID string)
	claimed []claimedSession // oldest first
}

type claimedSession struct{ session, owner string }

const maxClaimedPredecessorSessions = 8

func newResultDeliveryOwnership(current func() (sessionID, completionOwnerID string)) *resultDeliveryOwnership {
	return &resultDeliveryOwnership{current: current}
}

func (o *resultDeliveryOwnership) Owns(sessionID, completionOwnerID string) bool {
	cur, owner := o.current()
	if owner == "" || completionOwnerID != owner {
		return false
	}
	if sessionID == cur {
		return true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.claimed {
		if c.session == sessionID && c.owner == owner {
			return true
		}
	}
	return false
}

func (o *resultDeliveryOwnership) ClaimedSessionIDs() []string {
	_, owner := o.current()
	out := []string{}
	if owner == "" {
		return out
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.claimed {
		if c.owner == owner {
			out = append(out, c.session)
		}
	}
	return out
}

// ClaimPredecessor claims the previous session only when the session file and the previous runtime id agree and name the current session.
func (o *resultDeliveryOwnership) ClaimPredecessor(previousSessionFile, previousRuntimeSessionID string) bool {
	cur, owner := o.current()
	if owner == "" || previousSessionFile == "" || previousRuntimeSessionID == "" {
		return false
	}
	if previousSessionFile != previousRuntimeSessionID || cur != previousRuntimeSessionID {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, c := range o.claimed {
		if c.session == previousRuntimeSessionID {
			o.claimed = append(o.claimed[:i], o.claimed[i+1:]...)
			break
		}
	}
	o.claimed = append(o.claimed, claimedSession{previousRuntimeSessionID, owner})
	for len(o.claimed) > maxClaimedPredecessorSessions {
		o.claimed = o.claimed[1:]
	}
	return true
}

func (o *resultDeliveryOwnership) Clear() {
	o.mu.Lock()
	o.claimed = nil
	o.mu.Unlock()
}
