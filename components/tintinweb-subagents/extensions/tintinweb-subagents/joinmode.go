package tintinweb_subagents

import (
	"fmt"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// How the completion of a background agent reaches the model: one notification per agent (join mode `async`), or, for
// `group` and `smart`, one for all the agents spawned in the same turn. The agents of a turn are collected with a
// debounced timer: each new agent resets the window, so parallel tool calls dispatched across several ticks land in
// the same batch. upstream: index.ts (the batch tracking, finalizeBatch, sendIndividualNudge, the GroupJoinManager
// callback), invocation-config.ts resolveJoinMode.

const batchWindow = 100 * time.Millisecond // upstream: setTimeout(finalizeBatch, 100)

type batchEntry struct {
	id       string
	joinMode string
}

// resolveJoinMode is the default join mode for a background agent; a foreground one has none.
// upstream: invocation-config.ts resolveJoinMode.
func resolveJoinMode(defaultJoinMode string, runInBackground bool) string {
	if runInBackground {
		return defaultJoinMode
	}
	return ""
}

func (a *app) inBatch(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.batch {
		if e.id == id {
			return true
		}
	}
	return false
}

// joinBatch adds a spawned background agent to the batch of its turn, for join modes other than async.
func (a *app) joinBatch(id, joinMode string) {
	if joinMode == "" || joinMode == "async" {
		return // not part of any batch
	}
	a.mu.Lock()
	a.batch = append(a.batch, batchEntry{id: id, joinMode: joinMode})
	if a.batchStop != nil {
		a.batchStop()
	}
	a.batchStop = a.after(batchWindow, a.finalizeBatch)
	a.mu.Unlock()
}

func (a *app) cancelBatch() {
	a.mu.Lock()
	if a.batchStop != nil {
		a.batchStop()
		a.batchStop = nil
	}
	a.batch = nil
	a.mu.Unlock()
}

// finalizeBatch closes the batch: two or more smart or group agents become a group, and the ones that finished
// during the window, whose notification was deferred, are fed to it now; otherwise they get their own nudges.
func (a *app) finalizeBatch() {
	a.mu.Lock()
	a.batchStop = nil
	agents := a.batch
	a.batch = nil
	a.mu.Unlock()
	var grouped []string
	for _, e := range agents {
		if e.joinMode == "smart" || e.joinMode == "group" {
			grouped = append(grouped, e.id)
		}
	}
	if len(grouped) >= 2 {
		a.mu.Lock()
		a.batchCounter++
		groupID := fmt.Sprintf("batch-%d", a.batchCounter)
		a.mu.Unlock()
		a.groups.registerGroup(groupID, grouped)
		for _, id := range grouped {
			r := a.mgr.get(id)
			if r == nil {
				continue
			}
			a.mgr.mu.Lock()
			r.GroupID = groupID
			finished := !r.CompletedAt.IsZero() && !r.Consumed
			a.mgr.mu.Unlock()
			if finished {
				a.groups.onAgentComplete(r)
			}
		}
		return
	}
	for _, e := range agents {
		r := a.mgr.get(e.id)
		if r == nil {
			continue
		}
		a.mgr.mu.Lock()
		finished := !r.CompletedAt.IsZero() && !r.Consumed
		a.mgr.mu.Unlock()
		if finished {
			a.sendIndividualNudge(r)
		}
	}
}

// sendIndividualNudge announces one finished agent, after the hold a read of the result can cancel.
func (a *app) sendIndividualNudge(r *agentRecord) {
	a.scheduleNudge(r.ID, func() { a.emitIndividualNudge(r) })
}

func (a *app) emitIndividualNudge(r *agentRecord) {
	if a.mgr.isConsumed(r) { // re-checked at send time
		return
	}
	ctx, ok := a.context()
	if !ok {
		return
	}
	a.followUp(ctx, formatTaskNotification(r, 500))
}

// deliverGroup is the group join's callback: one consolidated notification for the agents that finished.
func (a *app) deliverGroup(records []*agentRecord, partial bool) {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.ID
	}
	a.scheduleNudge("group:"+strings.Join(ids, ","), func() {
		var unconsumed []*agentRecord
		for _, r := range records { // re-checked at send time
			if !a.mgr.isConsumed(r) {
				unconsumed = append(unconsumed, r)
			}
		}
		if len(unconsumed) == 0 {
			return
		}
		ctx, ok := a.context()
		if !ok {
			return
		}
		notifications := make([]string, len(unconsumed))
		for i, r := range unconsumed {
			notifications[i] = formatTaskNotification(r, 300)
		}
		label := fmt.Sprintf("%d agent(s) finished", len(unconsumed))
		if partial {
			label = fmt.Sprintf("%d agent(s) finished (partial — others still running)", len(unconsumed))
		}
		a.followUp(ctx, "Background agent group completed: "+label+"\n\n"+strings.Join(notifications, "\n\n")+"\n\nUse get_subagent_result for full output.")
	})
}

// followUp sends the model a notification that starts a turn once the current one ends.
func (a *app) followUp(ctx sdk.Context, content string) {
	turn := true
	_ = ctx.SendMessage("subagent-notification", content, true, sdk.SendMessageOptions{DeliverAs: "followUp", TriggerTurn: &turn})
}
