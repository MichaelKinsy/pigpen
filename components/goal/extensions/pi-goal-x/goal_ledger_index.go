package pi_goal_x

// ported from goal-ledger-index.ts: the bounded per-goal working set; the complete history stays in the JSONL ledger.

const (
	ledgerIndexRecentCap   = 12
	ledgerIndexActivityCap = 64
)

// goalLedgerIndex is GoalLedgerIndex. keyOrder is the property order of the JavaScript object, which a checkpoint file
// serializes: recent, activity, oracle first, then the optional keys in the order they were first assigned.
type goalLedgerIndex struct {
	recent            []*jsObject
	activity          []*jsObject
	lastActivityEvent *jsObject
	audit             *jsObject
	completion        *jsObject
	lifecycle         *jsObject
	oracle            *orderedMap[*jsObject] // fingerprint -> {failedAttempts, followupAttempted, result?, lastFailure?}
	keyOrder          []string
}

func newGoalLedgerIndex() *goalLedgerIndex {
	return &goalLedgerIndex{oracle: newOrderedMap[*jsObject](), keyOrder: []string{"recent", "activity", "oracle"}}
}

func (g *goalLedgerIndex) markKey(k string) {
	for _, x := range g.keyOrder {
		if x == k {
			return
		}
	}
	g.keyOrder = append(g.keyOrder, k)
}

func (g *goalLedgerIndex) toJSON() *jsObject {
	o := newObject()
	for _, k := range g.keyOrder {
		switch k {
		case "recent":
			o.set(k, eventsToAny(g.recent))
		case "activity":
			o.set(k, eventsToAny(g.activity))
		case "oracle":
			pairs := []any{}
			for _, fp := range g.oracle.ids {
				pairs = append(pairs, []any{fp, g.oracle.m[fp]})
			}
			o.set(k, pairs)
		case "lastActivityEvent":
			o.set(k, g.lastActivityEvent)
		case "audit":
			o.set(k, g.audit)
		case "completion":
			o.set(k, g.completion)
		case "lifecycle":
			o.set(k, g.lifecycle)
		}
	}
	return o
}

// goalLedgerIndexFromJSON validates one runtimeIndex entry of a checkpoint file; nil when it does not hold.
func goalLedgerIndexFromJSON(v any) *goalLedgerIndex {
	entry, ok := v.(*jsObject)
	if !ok {
		return nil
	}
	recent, ok1 := entry.vals["recent"].([]any)
	activity, ok2 := entry.vals["activity"].([]any)
	oracle, ok3 := entry.vals["oracle"].([]any)
	if !ok1 || !ok2 || !ok3 {
		return nil
	}
	if len(recent) > ledgerIndexRecentCap || len(activity) > ledgerIndexActivityCap {
		return nil
	}
	if len(activity) > 0 && !truthy(entry.vals["lastActivityEvent"]) {
		return nil
	}
	g := newGoalLedgerIndex()
	for _, r := range recent {
		if !isValidLedgerEvent(r) {
			return nil
		}
		g.recent = append(g.recent, r.(*jsObject))
	}
	for _, r := range activity {
		if !isValidLedgerEvent(r) {
			return nil
		}
		g.activity = append(g.activity, r.(*jsObject))
	}
	single := func(k string) (*jsObject, bool) {
		x := entry.vals[k]
		if !truthy(x) {
			return nil, true
		}
		if !isValidLedgerEvent(x) {
			return nil, false
		}
		return x.(*jsObject), true
	}
	var good bool
	if g.audit, good = single("audit"); !good {
		return nil
	}
	if g.completion, good = single("completion"); !good {
		return nil
	}
	if g.lifecycle, good = single("lifecycle"); !good {
		return nil
	}
	if g.lastActivityEvent, good = single("lastActivityEvent"); !good {
		return nil
	}
	for _, p := range oracle {
		pair, isArr := p.([]any)
		if !isArr || len(pair) != 2 {
			return nil
		}
		fp, isStr := pair[0].(string)
		state, isObj := pair[1].(*jsObject)
		if !isStr || !isObj {
			return nil
		}
		if !isSafeInt(state.vals["failedAttempts"]) || state.vals["failedAttempts"].(float64) < 0 || !isBoolV(state.vals["followupAttempted"]) {
			return nil
		}
		if r, present := state.vals["result"]; present {
			ro, isO := r.(*jsObject)
			if !isO || !isStrV(ro.vals["adviceId"]) || !isStrV(ro.vals["summary"]) || !isDisposition(ro.vals["disposition"]) {
				return nil
			}
		}
		if f, present := state.vals["lastFailure"]; present {
			fo, isO := f.(*jsObject)
			if !isO || !isStrV(fo.vals["errorCode"]) || !isStrV(fo.vals["message"]) {
				return nil
			}
		}
		g.oracle.set(fp, state)
	}
	// `{...entry}` keeps the key order of the file.
	order := []string{}
	for _, k := range entry.order() {
		switch k {
		case "recent", "activity", "oracle", "lastActivityEvent", "audit", "completion", "lifecycle":
			order = append(order, k)
		}
	}
	g.keyOrder = order
	return g
}

var ledgerLifecycleEvents = map[string]bool{
	"goal_created": true, "goal_paused": true, "goal_resumed": true, "goal_blocked": true,
	"goal_budget_limited": true, "goal_completed": true, "goal_aborted": true, "goal_archived": true,
}

func indexLedgerEvent(index *orderedMap[*goalLedgerIndex], event *jsObject) {
	if !hasGoalId(event) {
		return
	}
	goalID := evStr(event, "goalId")
	goal, ok := index.get(goalID)
	if !ok {
		goal = newGoalLedgerIndex()
		index.set(goalID, goal)
	}
	typ := evStr(event, "type")
	goal.recent = append(goal.recent, event)
	if len(goal.recent) > ledgerIndexRecentCap {
		goal.recent = goal.recent[1:]
	}
	if ledgerLifecycleEvents[typ] {
		goal.lifecycle = event
		goal.markKey("lifecycle")
	}
	if typ == "audit_result" {
		goal.audit = event
		goal.markKey("audit")
	}
	if typ == "completion_requested" {
		goal.completion = event
		goal.markKey("completion")
	}
	if isActivityEvent(event) && (goal.lastActivityEvent == nil || !sameActivityEvent(event, goal.lastActivityEvent)) {
		goal.lastActivityEvent = event
		goal.markKey("lastActivityEvent")
		at := evStr(event, "at")
		n := len(goal.activity)
		if n == 0 || at >= evStr(goal.activity[n-1], "at") {
			goal.activity = append(goal.activity, event)
		} else {
			low, high := 0, n
			for low < high {
				mid := (low + high) >> 1
				if evStr(goal.activity[mid], "at") <= at {
					low = mid + 1
				} else {
					high = mid
				}
			}
			goal.activity = append(goal.activity, nil)
			copy(goal.activity[low+1:], goal.activity[low:])
			goal.activity[low] = event
		}
		if len(goal.activity) > ledgerIndexActivityCap {
			goal.activity = goal.activity[1:]
		}
	}
	if fpv, has := event.vals["fingerprint"]; has {
		fp, _ := fpv.(string)
		state := newOracleConsultState()
		if prior, ok := goal.oracle.get(fp); ok {
			state = prior.clone()
		}
		switch typ {
		case "oracle_result":
			r := newObject()
			r.set("adviceId", event.vals["adviceId"])
			r.set("disposition", event.vals["disposition"])
			r.set("summary", event.vals["summary"])
			state.set("result", r)
		case "oracle_failed":
			n, _ := state.vals["failedAttempts"].(float64)
			state.set("failedAttempts", n+1)
			f := newObject()
			f.set("errorCode", event.vals["errorCode"])
			f.set("message", event.vals["message"])
			state.set("lastFailure", f)
		case "oracle_followup_attempted":
			state.set("followupAttempted", true)
		}
		goal.oracle.set(fp, state)
	}
}

func buildLedgerIndex(events []*jsObject) *orderedMap[*goalLedgerIndex] {
	index := newOrderedMap[*goalLedgerIndex]()
	for _, e := range events {
		indexLedgerEvent(index, e)
	}
	return index
}
