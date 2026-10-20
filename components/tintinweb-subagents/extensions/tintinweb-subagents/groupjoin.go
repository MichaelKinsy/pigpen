package tintinweb_subagents

import (
	"sync"
	"time"
)

// Grouped completion notifications of background agents: instead of each agent nudging the model on its own, the
// agents of a group are held until all have finished (or a timeout fires), then one consolidated notification goes
// out. upstream: src/group-join.ts.

const (
	groupJoinTimeout      = 30 * time.Second // after the first completion of a group
	groupStragglerTimeout = 15 * time.Second // for the stragglers after a partial delivery
)

// afterFunc runs f after d and returns the function that cancels it (time.AfterFunc, or a test's fake clock).
type afterFunc func(d time.Duration, f func()) (stop func())

func realAfter(d time.Duration, f func()) func() {
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

// deliveryCallback gets the records to announce, and whether others of the group are still running.
type deliveryCallback func(records []*agentRecord, partial bool)

type agentGroup struct {
	id             string
	agentIDs       map[string]bool
	completed      map[string]*agentRecord
	completedOrder []string
	stop           func() // the pending timeout, if armed
	delivered      bool
	isStraggler    bool // a shorter timeout applies to the stragglers after a partial delivery
}

type groupJoinManager struct {
	mu           sync.Mutex
	groups       map[string]*agentGroup
	agentToGroup map[string]string
	deliver      deliveryCallback
	timeout      time.Duration
	after        afterFunc
}

func newGroupJoinManager(deliver deliveryCallback, timeout time.Duration, after afterFunc) *groupJoinManager {
	return &groupJoinManager{groups: map[string]*agentGroup{}, agentToGroup: map[string]string{}, deliver: deliver, timeout: timeout, after: after}
}

// registerGroup makes the agents one group to be joined.
func (g *groupJoinManager) registerGroup(groupID string, agentIDs []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	grp := &agentGroup{id: groupID, agentIDs: map[string]bool{}, completed: map[string]*agentRecord{}}
	for _, id := range agentIDs {
		grp.agentIDs[id] = true
		g.agentToGroup[id] = groupID
	}
	g.groups[groupID] = grp
}

// onAgentComplete is told that an agent finished. It returns "pass" (not grouped: the caller sends its own nudge),
// "held" (waiting for the rest of the group) or "delivered" (this completion triggered the group's notification).
func (g *groupJoinManager) onAgentComplete(r *agentRecord) string {
	g.mu.Lock()
	groupID, ok := g.agentToGroup[r.ID]
	if !ok {
		g.mu.Unlock()
		return "pass"
	}
	grp := g.groups[groupID]
	if grp == nil || grp.delivered {
		g.mu.Unlock()
		return "pass"
	}
	if _, seen := grp.completed[r.ID]; !seen {
		grp.completedOrder = append(grp.completedOrder, r.ID)
	}
	grp.completed[r.ID] = r
	if len(grp.completed) >= len(grp.agentIDs) { // all done: deliver at once
		records := g.takeLocked(grp)
		g.mu.Unlock()
		g.deliver(records, false)
		return "delivered"
	}
	if grp.stop == nil { // the first completion of this batch starts the timeout
		d := g.timeout
		if grp.isStraggler {
			d = groupStragglerTimeout
		}
		grp.stop = g.after(d, func() { g.onTimeout(grp) })
	}
	g.mu.Unlock()
	return "held"
}

// takeLocked marks the group delivered, cancels its timeout and cleans it up; it returns the records to announce.
func (g *groupJoinManager) takeLocked(grp *agentGroup) []*agentRecord {
	if grp.stop != nil {
		grp.stop()
		grp.stop = nil
	}
	grp.delivered = true
	records := make([]*agentRecord, 0, len(grp.completedOrder))
	for _, id := range grp.completedOrder {
		records = append(records, grp.completed[id])
	}
	for id := range grp.agentIDs {
		delete(g.agentToGroup, id)
	}
	delete(g.groups, grp.id)
	return records
}

// onTimeout delivers what has finished so far and keeps the rest as stragglers with a shorter timeout.
func (g *groupJoinManager) onTimeout(grp *agentGroup) {
	g.mu.Lock()
	if grp.delivered {
		g.mu.Unlock()
		return
	}
	grp.stop = nil
	remaining := map[string]bool{}
	for id := range grp.agentIDs {
		if _, done := grp.completed[id]; !done {
			remaining[id] = true
		}
	}
	// The delivered agents will not complete again.
	records := make([]*agentRecord, 0, len(grp.completedOrder))
	for _, id := range grp.completedOrder {
		records = append(records, grp.completed[id])
		delete(g.agentToGroup, id)
	}
	grp.completed, grp.completedOrder = map[string]*agentRecord{}, nil
	grp.agentIDs = remaining
	grp.isStraggler = true // its timeout starts when the next straggler completes
	g.mu.Unlock()
	g.deliver(records, true)
}

// isGrouped: whether an agent is in a group.
func (g *groupJoinManager) isGrouped(agentID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.agentToGroup[agentID]
	return ok
}

// dispose cancels every pending timeout, so no partial delivery fires afterwards.
func (g *groupJoinManager) dispose() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, grp := range g.groups {
		if grp.stop != nil {
			grp.stop()
			grp.stop = nil
		}
	}
	g.groups, g.agentToGroup = map[string]*agentGroup{}, map[string]string{}
}
