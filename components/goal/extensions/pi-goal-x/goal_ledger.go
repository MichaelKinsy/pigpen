package pi_goal_x

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// ported from goal-ledger.ts: the append-only goal event ledger, its zero-op cache, the cold-start checkpoint and the
// reconstruction of goal state from events. Events are *jsObject (key order and JS number semantics are persisted).

const (
	ledgerCheckpointFile    = ".goal-ledger-checkpoint.json"
	ledgerCheckpointVersion = 3
	checkpointRecentCap     = 12
	// checkpointWriteInterval / checkpointWriteMinMs throttle the disk write of the mutation path.
	checkpointWriteInterval = 32
	checkpointWriteMinMs    = 2000
)

// orderedMap is a JavaScript Map: iteration follows insertion order; setting an existing key keeps its position.
type orderedMap[V any] struct {
	ids []string
	m   map[string]V
}

func newOrderedMap[V any]() *orderedMap[V] { return &orderedMap[V]{m: map[string]V{}} }

func (o *orderedMap[V]) set(id string, v V) {
	if _, ok := o.m[id]; !ok {
		o.ids = append(o.ids, id)
	}
	o.m[id] = v
}

func (o *orderedMap[V]) get(id string) (V, bool) { v, ok := o.m[id]; return v, ok }
func (o *orderedMap[V]) has(id string) bool      { _, ok := o.m[id]; return ok }
func (o *orderedMap[V]) size() int               { return len(o.ids) }
func (o *orderedMap[V]) keys() []string          { return append([]string(nil), o.ids...) }

func (o *orderedMap[V]) delete(id string) {
	if _, ok := o.m[id]; !ok {
		return
	}
	delete(o.m, id)
	o.ids = removeKey(o.ids, id)
}

func (o *orderedMap[V]) values() []V {
	out := make([]V, 0, len(o.ids))
	for _, id := range o.ids {
		out = append(out, o.m[id])
	}
	return out
}

// copy is `new Map(map)`: a shallow copy.
func (o *orderedMap[V]) copy() *orderedMap[V] {
	c := &orderedMap[V]{ids: append([]string(nil), o.ids...), m: make(map[string]V, len(o.m))}
	for k, v := range o.m {
		c.m[k] = v
	}
	return c
}

// ledgerStateMap holds reconstructed goal states in insertion order.
type ledgerStateMap = orderedMap[*reconstructedGoalState]

// reconstructedGoalState is ReconstructedGoalState; a nil string pointer is JavaScript undefined.
type reconstructedGoalState struct {
	goalID                     string
	latestStatus               string
	latestFocus                bool
	latestPauseReason          *string
	latestPauseSuggestedAction *string
	latestAuditorResult        *jsObject // {verdict, report, at}
	latestOracleResult         *jsObject // {fingerprint, adviceId, disposition, summary, at}
	createdAt                  *string
	completedAt                *string
	abortedAt                  *string
	tweakedAt                  *string
	resumedAt                  *string
}

type goalLedgerState struct {
	focusedGoalID *string
	goals         *ledgerStateMap
	terminalGoals *ledgerStateMap
}

// ledgerRevision is the opaque generation of a read result; its address is the identity.
type ledgerRevision struct{ _ byte }

type goalLedgerReadResult struct {
	events    []*jsObject
	malformed int
	revision  *ledgerRevision
}

// goalAppendResult is GoalLedgerAppendResult.
type goalAppendResult struct {
	ok  bool
	err error
}

type ledgerCacheEntry struct {
	size      int64
	events    []*jsObject
	malformed int
	revision  *ledgerRevision
}

type reconstructAccumulator struct {
	goals           *ledgerStateMap
	terminalGoals   *ledgerStateMap
	focusedGoalID   *string
	focusGeneration int
	focusGenByGoal  *orderedMap[int]
}

type ledgerCheckpoint struct {
	createdAt          string
	coveredBytes       int64
	coveredEvents      int
	acc                *reconstructAccumulator
	recentEventsByGoal *orderedMap[[]*jsObject]
	runtimeIndex       *orderedMap[*goalLedgerIndex]
}

type ledgerStateReadResult struct {
	state              *goalLedgerState
	recentEventsByGoal map[string][]*jsObject
	malformed          int
	coveredBytes       int64
	coveredEvents      int
	source             string // "cache" | "checkpoint" | "tail" | "full"
}

type lastCheckpointWrite struct {
	appendsSinceWrite int
	at                int64
}

var (
	ledgerCache             = map[string]*ledgerCacheEntry{}
	checkpointCache         = map[string]*ledgerCheckpoint{}
	checkpointCacheKnown    = map[string]bool{} // key present (a nil checkpoint = known absent)
	lastCheckpointDiskWrite = map[string]lastCheckpointWrite{}
	runtimeReady            = map[string]bool{}
)

func goalLedgerPath(ctx storage) string { return ctx.mustPath(ledgerFile) }

func goalIdOf(event *jsObject) (string, bool) {
	v, ok := event.vals["goalId"]
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

func hasGoalId(event *jsObject) bool { _, ok := event.vals["goalId"]; return ok }

func checkpointPathFor(filePath string) string {
	return filepath.Join(filepath.Dir(filePath), ledgerCheckpointFile)
}

// appendGoalEvent appends one ledger event; a failure is returned, never fatal (the goal file is authoritative).
func appendGoalEvent(ctx storage, event *jsObject) goalAppendResult {
	return appendLedgerLines(ctx, []*jsObject{event})
}

// appendGoalEvents appends several events as one block.
func appendGoalEvents(ctx storage, events []*jsObject) goalAppendResult {
	if len(events) == 0 {
		return goalAppendResult{ok: true}
	}
	return appendLedgerLines(ctx, events)
}

func appendLedgerLines(ctx storage, events []*jsObject) goalAppendResult {
	filePath := goalLedgerPath(ctx)
	dir := filepath.Dir(filePath)
	if !ledgerDirKnown[dir] {
		if err := ctx.ensureDir(goalsDir, false); err != nil {
			return goalAppendResult{err: err}
		}
		ledgerDirKnown[dir] = true
	}
	var b strings.Builder
	for _, e := range events {
		b.WriteString(marshalJSON(e, ""))
		b.WriteString("\n")
	}
	lines := b.String()
	if err := appendFileString(filePath, lines); err != nil {
		return goalAppendResult{err: err}
	}
	extendLedgerCache(filePath, lines, events)
	updateLedgerCheckpointAfterAppend(filePath, lines, events)
	return goalAppendResult{ok: true}
}

func appendFileString(file, content string) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// invalidateGoalLedgerCache is the session boundary: the next read goes to disk.
func invalidateGoalLedgerCache() {
	ledgerCache = map[string]*ledgerCacheEntry{}
	runtimeReady = map[string]bool{}
	checkpointCache = map[string]*ledgerCheckpoint{}
	checkpointCacheKnown = map[string]bool{}
	lastCheckpointDiskWrite = map[string]lastCheckpointWrite{}
}

func extendLedgerCache(filePath, lines string, events []*jsObject) {
	cached := ledgerCache[filePath]
	if cached == nil {
		return
	}
	evs := cached.events
	for _, e := range events {
		evs = append(evs, sanitizeEvent(e, false))
	}
	ledgerCache[filePath] = &ledgerCacheEntry{size: cached.size + int64(len(lines)), events: evs, malformed: cached.malformed, revision: &ledgerRevision{}}
}

func readGoalLedger(ctx storage) goalLedgerReadResult {
	filePath := goalLedgerPath(ctx)
	if cached := ledgerCache[filePath]; cached != nil {
		return goalLedgerReadResult{events: cached.events, malformed: cached.malformed, revision: cached.revision}
	}
	return readGoalLedgerCold(filePath)
}

func readGoalLedgerCold(filePath string) goalLedgerReadResult {
	data, err := os.ReadFile(filePath)
	if err != nil {
		empty := goalLedgerReadResult{events: []*jsObject{}, revision: &ledgerRevision{}}
		ledgerCache[filePath] = &ledgerCacheEntry{events: empty.events, revision: empty.revision}
		return empty
	}
	parsed := parseLedgerLines(string(data))
	parsed.revision = &ledgerRevision{}
	ledgerCache[filePath] = &ledgerCacheEntry{size: int64(len(data)), events: parsed.events, malformed: parsed.malformed, revision: parsed.revision}
	return parsed
}

func parseLedgerLines(content string) goalLedgerReadResult {
	events := []*jsObject{}
	goalIDs := map[string]string{}
	malformed := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := jsTrim(line)
		if trimmed == "" {
			continue
		}
		v, err := parseJSON([]byte(trimmed))
		if err != nil {
			malformed++
			continue
		}
		parsed, _ := v.(*jsObject)
		if !isValidLedgerEvent(v) {
			malformed++
			continue
		}
		if id, ok := parsed.vals["goalId"].(string); ok {
			safe, seen := goalIDs[id]
			if !seen {
				safe = safeIdPart(id)
				if len(goalIDs) < 1024 {
					goalIDs[id] = safe
				}
			}
			parsed.set("goalId", safe)
		}
		events = append(events, parsed)
	}
	return goalLedgerReadResult{events: events, malformed: malformed}
}

// ---- event validation ----

func isStrV(v any) bool  { _, ok := v.(string); return ok }
func isNumV(v any) bool  { _, ok := v.(float64); return ok }
func isBoolV(v any) bool { _, ok := v.(bool); return ok }

func isSafeInt(v any) bool {
	f, ok := v.(float64)
	return ok && f == math.Trunc(f) && math.Abs(f) <= 9007199254740991
}

// optStrV is `value === undefined || typeof value === "string"`.
func optStrV(o *jsObject, k string) bool {
	v, ok := o.vals[k]
	if !ok {
		return true
	}
	if _, u := v.(undef); u {
		return true
	}
	return isStrV(v)
}

func oneOfStr(v any, opts ...string) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, o := range opts {
		if s == o {
			return true
		}
	}
	return false
}

func isNullableBudget(o *jsObject, k string) bool {
	v, ok := o.vals[k]
	if !ok {
		return false
	}
	if v == nil {
		return true
	}
	return isSafeInt(v) && v.(float64) > 0
}

func isValidLedgerEvent(value any) bool {
	obj, ok := value.(*jsObject)
	if !ok {
		return false
	}
	typ, ok := obj.vals["type"].(string)
	if !ok || !isStrV(obj.vals["at"]) {
		return false
	}
	v := obj.vals
	gid := isStrV(v["goalId"])
	switch typ {
	case "goal_created":
		return gid && isStrV(v["objective"]) && isBoolV(v["sisyphus"]) && isBoolV(v["autoContinue"])
	case "goal_focused", "goal_resumed", "goal_stalled":
		return gid && isStrV(v["reason"])
	case "goal_unfocused":
		return isStrV(v["reason"])
	case "goal_paused":
		_, hasStatus := v["status"]
		_, statusUndef := v["status"].(undef)
		statusOK := !hasStatus || statusUndef || v["status"] == "paused"
		_, hasSource := v["source"]
		_, sourceUndef := v["source"].(undef)
		sourceOK := !hasSource || sourceUndef || oneOfStr(v["source"], "user", "agent")
		return gid && isStrV(v["reason"]) && optStrV(obj, "suggestedAction") && statusOK && sourceOK
	case "goal_tweaked":
		return gid && isStrV(v["changeSummary"])
	case "auditor_toggled":
		return gid && isBoolV(v["enabled"])
	case "completion_requested":
		return gid && optStrV(obj, "summary")
	case "audit_started":
		return gid && optStrV(obj, "provider") && optStrV(obj, "model") && optStrV(obj, "thinkingLevel")
	case "audit_result":
		return gid && oneOfStr(v["verdict"], "approved", "disapproved", "error") && isStrV(v["report"])
	case "audit_skipped":
		return gid && oneOfStr(v["reason"], "disabled", "user_aborted") && optStrV(obj, "provider") && optStrV(obj, "model") && optStrV(obj, "thinkingLevel")
	case "goal_completed":
		return gid && optStrV(obj, "archivePath")
	case "goal_archived":
		return gid && isStrV(v["archivePath"])
	case "goal_archive_failed":
		return gid && isStrV(v["message"])
	case "goal_aborted":
		return gid && isStrV(v["reason"]) && optStrV(obj, "archivePath")
	case "task_list_set":
		return gid && isNumV(v["taskCount"]) && isBoolV(v["blockCompletion"])
	case "task_complete":
		return gid && isStrV(v["taskId"]) && optStrV(obj, "evidence")
	case "task_skipped":
		return gid && isStrV(v["taskId"]) && isStrV(v["reason"])
	case "task_reopened", "task_started":
		return gid && isStrV(v["taskId"])
	case "goal_budget_changed":
		return gid && isNullableBudget(obj, "oldBudget") && isNullableBudget(obj, "newBudget") && isNumV(v["tokensUsed"])
	case "goal_budget_limited":
		return gid && isNumV(v["budget"]) && isNumV(v["tokensUsed"])
	case "goal_budget_warning":
		return gid && isNumV(v["budget"]) && isNumV(v["tokensUsed"]) && isNumV(v["pct"])
	case "goal_blocked":
		return gid && isStrV(v["reason"]) && oneOfStr(v["source"], "agent", "system")
	case "oracle_started":
		return gid && isStrV(v["fingerprint"]) && isStrV(v["provider"]) && isStrV(v["model"]) && isStrV(v["reason"]) && optStrV(obj, "thinkingLevel")
	case "oracle_result":
		return gid && isStrV(v["fingerprint"]) && isStrV(v["adviceId"]) && oneOfStr(v["disposition"], "actionable", "needs_human", "insufficient_context") && isStrV(v["summary"])
	case "oracle_failed":
		return gid && isStrV(v["fingerprint"]) && isNumV(v["attempt"]) && oneOfStr(v["errorCode"], "config", "provider", "aborted", "invalid_output") && isStrV(v["message"])
	case "oracle_followup_attempted":
		return gid && isStrV(v["fingerprint"]) && isStrV(v["adviceId"]) && isStrV(v["firstToolName"])
	}
	return false
}

// sanitizeEvent replaces a goalId by its safe form; unless owned, the event is copied first.
func sanitizeEvent(event *jsObject, owned bool) *jsObject {
	id, ok := goalIdOf(event)
	if !ok {
		return event
	}
	safe := safeIdPart(id)
	if owned {
		event.set("goalId", safe)
		return event
	}
	c := event.clone()
	c.set("goalId", safe)
	return c
}

// ---- reconstruction ----

func freshAccumulator() *reconstructAccumulator {
	return &reconstructAccumulator{goals: newOrderedMap[*reconstructedGoalState](), terminalGoals: newOrderedMap[*reconstructedGoalState](), focusGenByGoal: newOrderedMap[int]()}
}

func reconstructGoalLedger(events []*jsObject) *goalLedgerState {
	return finalizeLedgerState(applyLedgerEvents(freshAccumulator(), events))
}

func evStr(e *jsObject, k string) string { s, _ := e.vals[k].(string); return s }

func optEvStr(e *jsObject, k string) *string {
	if s, ok := e.vals[k].(string); ok {
		return &s
	}
	return nil
}

func applyLedgerEvents(acc *reconstructAccumulator, events []*jsObject) *reconstructAccumulator {
	goals, terminalGoals, focusGenByGoal := acc.goals, acc.terminalGoals, acc.focusGenByGoal
	focused := acc.focusedGoalID
	gen := acc.focusGeneration
	for _, event := range events {
		goalID := evStr(event, "goalId")
		at := evStr(event, "at")
		switch evStr(event, "type") {
		case "goal_created":
			goals.set(goalID, &reconstructedGoalState{goalID: goalID, latestStatus: "active", createdAt: new(at)})
		case "goal_focused":
			focused = new(goalID)
			gen++
			_, inGoals := goals.get(goalID)
			_, inTerminal := terminalGoals.get(goalID)
			if inGoals || inTerminal {
				focusGenByGoal.set(goalID, gen)
			}
		case "goal_unfocused":
			focused = nil
			gen++
		case "goal_paused":
			if st, ok := goals.get(goalID); ok {
				status := "paused"
				if s, ok := event.vals["status"].(string); ok {
					status = s
				}
				st.latestStatus = status
				st.latestPauseReason = optEvStr(event, "reason")
				st.latestPauseSuggestedAction = optEvStr(event, "suggestedAction")
			}
		case "goal_resumed":
			if st, ok := goals.get(goalID); ok {
				st.latestStatus = "active"
				st.resumedAt = new(at)
				st.latestPauseReason = nil
				st.latestPauseSuggestedAction = nil
			}
		case "goal_tweaked":
			if st, ok := goals.get(goalID); ok {
				st.tweakedAt = new(at)
			}
		case "audit_result":
			st, ok := goals.get(goalID)
			if !ok {
				st, ok = terminalGoals.get(goalID)
			}
			if ok {
				r := newObject()
				r.set("verdict", event.vals["verdict"])
				r.set("report", event.vals["report"])
				r.set("at", at)
				st.latestAuditorResult = r
			}
		case "goal_completed":
			st, ok := goals.get(goalID)
			if !ok {
				st = &reconstructedGoalState{goalID: goalID, latestStatus: "complete"}
			}
			st.latestStatus = "complete"
			st.completedAt = new(at)
			terminalGoals.set(goalID, st)
			goals.delete(goalID)
		case "goal_aborted":
			st, ok := goals.get(goalID)
			if !ok {
				st = &reconstructedGoalState{goalID: goalID, latestStatus: "aborted"}
			}
			st.latestStatus = "aborted"
			st.abortedAt = new(at)
			terminalGoals.set(goalID, st)
			goals.delete(goalID)
		case "oracle_result":
			st, ok := goals.get(goalID)
			if !ok {
				st, ok = terminalGoals.get(goalID)
			}
			if ok {
				r := newObject()
				for _, k := range []string{"fingerprint", "adviceId", "disposition", "summary"} {
					r.set(k, event.vals[k])
				}
				r.set("at", at)
				st.latestOracleResult = r
			}
		}
	}
	acc.focusedGoalID = focused
	acc.focusGeneration = gen
	return acc
}

func applyLedgerEvent(acc *reconstructAccumulator, event *jsObject) {
	applyLedgerEvents(acc, []*jsObject{event})
}

func finalizeLedgerState(acc *reconstructAccumulator) *goalLedgerState {
	for _, m := range []*ledgerStateMap{acc.goals, acc.terminalGoals} {
		for _, g := range m.values() {
			gen, ok := acc.focusGenByGoal.get(g.goalID)
			g.latestFocus = ok && gen == acc.focusGeneration
		}
	}
	if acc.focusedGoalID != nil && !acc.goals.has(*acc.focusedGoalID) {
		acc.focusedGoalID = nil
	}
	return &goalLedgerState{focusedGoalID: acc.focusedGoalID, goals: acc.goals, terminalGoals: acc.terminalGoals}
}

func cloneAccumulator(acc *reconstructAccumulator) *reconstructAccumulator {
	return &reconstructAccumulator{
		goals:           acc.goals.copy(),
		terminalGoals:   acc.terminalGoals.copy(),
		focusedGoalID:   acc.focusedGoalID,
		focusGeneration: acc.focusGeneration,
		focusGenByGoal:  acc.focusGenByGoal.copy(),
	}
}

// latestAuditorResultForGoal returns {verdict, report, at} of the newest audit_result, or nil.
func latestAuditorResultForGoal(events []*jsObject, goalID string) *jsObject {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if evStr(e, "type") == "audit_result" && evStr(e, "goalId") == goalID {
			r := newObject()
			r.set("verdict", e.vals["verdict"])
			r.set("report", e.vals["report"])
			r.set("at", e.vals["at"])
			return r
		}
	}
	return nil
}

func latestEventsForGoal(events []*jsObject, goalID string, limit int) []*jsObject {
	result := []*jsObject{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if hasGoalId(e) && e.vals["goalId"] == any(goalID) {
			result = append([]*jsObject{e}, result...)
			if len(result) >= limit {
				break
			}
		}
	}
	return result
}

func latestGoalLifecycleEvent(events []*jsObject, goalID string) *jsObject {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if hasGoalId(e) && e.vals["goalId"] == any(goalID) {
			return e
		}
	}
	return nil
}

// ---- checkpoint ----

func appendRecent(recent *orderedMap[[]*jsObject], goalID string, event *jsObject) {
	tail, _ := recent.get(goalID)
	tail = append(tail[:len(tail):len(tail)], event)
	if len(tail) > checkpointRecentCap {
		tail = tail[len(tail)-checkpointRecentCap:]
	}
	recent.set(goalID, tail)
}

func buildCheckpointFromEvents(events []*jsObject, coveredBytes int64) *ledgerCheckpoint {
	index := buildLedgerIndex(events)
	recent := newOrderedMap[[]*jsObject]()
	for _, id := range index.ids {
		recent.set(id, append([]*jsObject(nil), index.m[id].recent...))
	}
	return &ledgerCheckpoint{
		createdAt:          nowIso(),
		coveredBytes:       coveredBytes,
		coveredEvents:      len(events),
		acc:                applyLedgerEvents(freshAccumulator(), events),
		recentEventsByGoal: recent,
		runtimeIndex:       index,
	}
}

func goalStateToJSON(s *reconstructedGoalState) *jsObject {
	o := newObject()
	put := func(k string, p *string) {
		if p == nil {
			o.set(k, undef{})
		} else {
			o.set(k, *p)
		}
	}
	o.set("goalId", s.goalID)
	o.set("latestStatus", s.latestStatus)
	o.set("latestFocus", s.latestFocus)
	put("latestPauseReason", s.latestPauseReason)
	put("latestPauseSuggestedAction", s.latestPauseSuggestedAction)
	if s.latestAuditorResult == nil {
		o.set("latestAuditorResult", undef{})
	} else {
		o.set("latestAuditorResult", s.latestAuditorResult)
	}
	if s.latestOracleResult == nil {
		o.set("latestOracleResult", undef{})
	} else {
		o.set("latestOracleResult", s.latestOracleResult)
	}
	put("createdAt", s.createdAt)
	put("completedAt", s.completedAt)
	put("abortedAt", s.abortedAt)
	put("tweakedAt", s.tweakedAt)
	put("resumedAt", s.resumedAt)
	return o
}

func eventsToAny(evs []*jsObject) []any {
	out := make([]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, e)
	}
	return out
}

func checkpointToJSON(cp *ledgerCheckpoint) *jsObject {
	mapJSON := func(m *ledgerStateMap) []any {
		out := []any{}
		for _, s := range m.values() {
			out = append(out, goalStateToJSON(s))
		}
		return out
	}
	acc := newObject()
	acc.set("goals", mapJSON(cp.acc.goals))
	acc.set("terminalGoals", mapJSON(cp.acc.terminalGoals))
	if cp.acc.focusedGoalID == nil {
		acc.set("focusedGoalId", nil)
	} else {
		acc.set("focusedGoalId", *cp.acc.focusedGoalID)
	}
	acc.set("focusGeneration", cp.acc.focusGeneration)
	fg := []any{}
	for _, id := range cp.acc.focusGenByGoal.ids {
		fg = append(fg, []any{id, cp.acc.focusGenByGoal.m[id]})
	}
	acc.set("focusGenByGoal", fg)
	recent := []any{}
	for _, id := range cp.recentEventsByGoal.ids {
		recent = append(recent, []any{id, eventsToAny(cp.recentEventsByGoal.m[id])})
	}
	idx := []any{}
	for _, id := range cp.runtimeIndex.ids {
		idx = append(idx, []any{id, cp.runtimeIndex.m[id].toJSON()})
	}
	o := newObject()
	o.set("version", ledgerCheckpointVersion)
	o.set("format", "goal-ledger-checkpoint")
	o.set("createdAt", cp.createdAt)
	o.set("coveredBytes", int(cp.coveredBytes))
	o.set("coveredEvents", cp.coveredEvents)
	o.set("acc", acc)
	o.set("recentEventsByGoal", recent)
	o.set("runtimeIndex", idx)
	return o
}

func optStrField(o *jsObject, k string) (*string, bool) {
	v, ok := o.vals[k]
	if !ok {
		return nil, true
	}
	if s, isStr := v.(string); isStr {
		return &s, true
	}
	return nil, true
}

func isDisposition(v any) bool {
	return oneOfStr(v, "actionable", "needs_human", "insufficient_context")
}

func parseGoalState(value any) *reconstructedGoalState {
	o, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	goalID, ok := o.vals["goalId"].(string)
	if !ok {
		return nil
	}
	status, _ := o.vals["latestStatus"].(string)
	if !oneOfStr(o.vals["latestStatus"], "active", "paused", "complete", "aborted", "unknown") {
		return nil
	}
	var auditor *jsObject
	if v, present := o.vals["latestAuditorResult"]; present {
		a, isObj := v.(*jsObject)
		if !isObj {
			// `auditor.verdict` of a non-object: undefined (null throws, which the caller turns into null as well).
			return nil
		}
		if !oneOfStr(a.vals["verdict"], "approved", "disapproved", "error") || !isStrV(a.vals["report"]) || !isStrV(a.vals["at"]) {
			return nil
		}
		auditor = newObject()
		auditor.set("verdict", a.vals["verdict"])
		auditor.set("report", a.vals["report"])
		auditor.set("at", a.vals["at"])
	}
	var oracle *jsObject
	if v, present := o.vals["latestOracleResult"]; present {
		oo, isObj := v.(*jsObject)
		if !isObj || !isStrV(oo.vals["fingerprint"]) || !isStrV(oo.vals["adviceId"]) || !isStrV(oo.vals["summary"]) || !isStrV(oo.vals["at"]) || !isDisposition(oo.vals["disposition"]) {
			return nil
		}
		oracle = oo
	}
	flag, _ := o.vals["latestFocus"].(bool)
	s := &reconstructedGoalState{goalID: goalID, latestStatus: status, latestFocus: flag, latestOracleResult: oracle, latestAuditorResult: auditor}
	s.latestPauseReason = optEvStr(o, "latestPauseReason")
	s.latestPauseSuggestedAction = optEvStr(o, "latestPauseSuggestedAction")
	s.createdAt = optEvStr(o, "createdAt")
	s.completedAt = optEvStr(o, "completedAt")
	s.abortedAt = optEvStr(o, "abortedAt")
	s.tweakedAt = optEvStr(o, "tweakedAt")
	s.resumedAt = optEvStr(o, "resumedAt")
	return s
}

// destructurePair is `const [a, b] = value` inside a for-of: ok=false when the value is not iterable (the TS throws, which
// the checkpoint reader turns into a missing checkpoint); a string yields its characters.
func destructurePair(v any) (a, b any, ok bool, skip bool) {
	switch x := v.(type) {
	case []any:
		a, b = nil, nil
		if len(x) > 0 {
			a = x[0]
		} else {
			a = undef{}
		}
		if len(x) > 1 {
			b = x[1]
		} else {
			b = undef{}
		}
		return a, b, true, false
	case string:
		return nil, nil, true, true
	}
	return nil, nil, false, false
}

func checkpointFromJSON(value any) *ledgerCheckpoint {
	o, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	if o.vals["version"] != any(float64(ledgerCheckpointVersion)) || o.vals["format"] != any("goal-ledger-checkpoint") {
		return nil
	}
	cb, ok := o.vals["coveredBytes"].(float64)
	if !ok || !isSafeInt(cb) || cb < 0 {
		return nil
	}
	ce, ok := o.vals["coveredEvents"].(float64)
	if !ok || !isSafeInt(ce) || ce < 0 {
		return nil
	}
	accRaw := o.obj("acc")
	if accRaw == nil {
		return nil
	}
	fgen, isNum := accRaw.vals["focusGeneration"].(float64)
	goalsRaw, g1 := accRaw.vals["goals"].([]any)
	termRaw, g2 := accRaw.vals["terminalGoals"].([]any)
	fgRaw, g3 := accRaw.vals["focusGenByGoal"].([]any)
	recentRaw, g4 := o.vals["recentEventsByGoal"].([]any)
	if !isNum || !isSafeInt(fgen) || !g1 || !g2 || !g3 || !g4 {
		return nil
	}
	goals := newOrderedMap[*reconstructedGoalState]()
	for _, raw := range goalsRaw {
		s := parseGoalState(raw)
		if s == nil {
			return nil
		}
		goals.set(s.goalID, s)
	}
	terminal := newOrderedMap[*reconstructedGoalState]()
	for _, raw := range termRaw {
		s := parseGoalState(raw)
		if s == nil {
			return nil
		}
		terminal.set(s.goalID, s)
	}
	focusGen := newOrderedMap[int]()
	for _, pair := range fgRaw {
		gid, gen, ok, skip := destructurePair(pair)
		if !ok {
			return nil
		}
		if skip {
			continue
		}
		if id, isStr := gid.(string); isStr {
			if n, isN := gen.(float64); isN {
				focusGen.set(id, int(n))
			}
		}
	}
	recent := newOrderedMap[[]*jsObject]()
	for _, pair := range recentRaw {
		gid, evs, ok, skip := destructurePair(pair)
		if !ok {
			return nil
		}
		if skip {
			continue
		}
		id, isStr := gid.(string)
		arr, isArr := evs.([]any)
		if !isStr || !isArr {
			continue
		}
		clean := []*jsObject{}
		for _, ev := range arr {
			if isValidLedgerEvent(ev) {
				clean = append(clean, sanitizeEvent(ev.(*jsObject), false))
			}
		}
		if len(clean) > 0 {
			recent.set(id, clean)
		}
	}
	idxRaw, isArr := o.vals["runtimeIndex"].([]any)
	if !isArr {
		return nil
	}
	runtime := newOrderedMap[*goalLedgerIndex]()
	for _, pair := range idxRaw {
		p, isP := pair.([]any)
		if !isP || len(p) != 2 {
			return nil
		}
		id, isStr := p[0].(string)
		if !isStr {
			return nil
		}
		entry := goalLedgerIndexFromJSON(p[1])
		if entry == nil {
			return nil
		}
		runtime.set(id, entry)
	}
	createdAt, _ := o.vals["createdAt"].(string)
	var focused *string
	if s, isStr := accRaw.vals["focusedGoalId"].(string); isStr {
		focused = &s
	}
	return &ledgerCheckpoint{
		createdAt:          createdAt,
		coveredBytes:       int64(cb),
		coveredEvents:      int(ce),
		acc:                &reconstructAccumulator{goals: goals, terminalGoals: terminal, focusedGoalID: focused, focusGeneration: int(fgen), focusGenByGoal: focusGen},
		recentEventsByGoal: recent,
		runtimeIndex:       runtime,
	}
}

func writeLedgerCheckpointAtomic(filePath string, cp *ledgerCheckpoint) {
	cpPath := checkpointPathFor(filePath)
	tmp := fmt.Sprintf("%s.tmp-%d", cpPath, pidForTemp())
	err := os.WriteFile(tmp, []byte(marshalJSON(checkpointToJSON(cp), "")), 0o644)
	if err == nil {
		err = os.Rename(tmp, cpPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "[goal-ledger] checkpoint write failed:", err)
	}
}

func readLedgerCheckpointFile(filePath string) *ledgerCheckpoint {
	if checkpointCacheKnown[filePath] {
		return checkpointCache[filePath]
	}
	var cp *ledgerCheckpoint
	if data, err := os.ReadFile(checkpointPathFor(filePath)); err == nil {
		if v, err := parseJSON(data); err == nil {
			cp = checkpointFromJSON(v)
		}
	}
	setCheckpointCache(filePath, cp)
	return cp
}

func setCheckpointCache(filePath string, cp *ledgerCheckpoint) {
	checkpointCache[filePath] = cp
	checkpointCacheKnown[filePath] = true
}

func readLedgerTailFrom(filePath string, offset int64) goalLedgerReadResult {
	f, err := os.Open(filePath)
	if err != nil {
		return goalLedgerReadResult{events: []*jsObject{}}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return goalLedgerReadResult{events: []*jsObject{}}
	}
	n := fi.Size() - offset
	if n < 0 {
		n = 0
	}
	buf := make([]byte, n)
	if n > 0 {
		if _, err := f.ReadAt(buf, offset); err != nil {
			return goalLedgerReadResult{events: []*jsObject{}}
		}
	}
	return parseLedgerLines(string(buf))
}

func updateLedgerCheckpointAfterAppend(filePath, lines string, events []*jsObject) {
	var cp *ledgerCheckpoint
	if checkpointCacheKnown[filePath] {
		cp = checkpointCache[filePath]
	} else {
		cp = readLedgerCheckpointFile(filePath)
	}
	if cp == nil {
		cached := ledgerCache[filePath]
		if cached == nil {
			return
		}
		cp = buildCheckpointFromEvents(cached.events, cached.size)
		setCheckpointCache(filePath, cp)
		writeLedgerCheckpointAtomic(filePath, cp)
		lastCheckpointDiskWrite[filePath] = lastCheckpointWrite{appendsSinceWrite: cp.coveredEvents, at: clockMs()}
		return
	}
	for _, e := range events {
		applyLedgerEvent(cp.acc, e)
		indexLedgerEvent(cp.runtimeIndex, e)
	}
	cp.coveredBytes += int64(len(lines))
	cp.coveredEvents += len(events)
	for _, e := range events {
		if id, ok := goalIdOf(e); ok {
			appendRecent(cp.recentEventsByGoal, id, e)
		}
	}
	cp.createdAt = nowIso()
	setCheckpointCache(filePath, cp)
	last := lastCheckpointDiskWrite[filePath]
	sinceWrite := cp.coveredEvents - last.appendsSinceWrite
	if sinceWrite >= checkpointWriteInterval || clockMs()-last.at >= checkpointWriteMinMs {
		writeLedgerCheckpointAtomic(filePath, cp)
		lastCheckpointDiskWrite[filePath] = lastCheckpointWrite{appendsSinceWrite: cp.coveredEvents, at: clockMs()}
	}
}

func recentToMap(m *orderedMap[[]*jsObject]) map[string][]*jsObject {
	out := make(map[string][]*jsObject, len(m.ids))
	for _, id := range m.ids {
		out[id] = m.m[id]
	}
	return out
}

// runtimeIndex is the bounded per-goal projection set, initialized once from the checkpoint plus the ledger tail.
func runtimeIndex(ctx storage) *orderedMap[*goalLedgerIndex] {
	filePath := goalLedgerPath(ctx)
	if !runtimeReady[filePath] {
		loadLedgerState(ctx)
		if cp := checkpointCache[filePath]; cp == nil {
			full := readGoalLedger(ctx)
			var size int64
			if c := ledgerCache[filePath]; c != nil {
				size = c.size
			}
			setCheckpointCache(filePath, buildCheckpointFromEvents(full.events, size))
		}
		delete(ledgerCache, filePath)
		runtimeReady[filePath] = true
	}
	return checkpointCache[filePath].runtimeIndex
}

func goalActivityEvents(ctx storage, goalID string) []*jsObject {
	if g, ok := runtimeIndex(ctx).get(goalID); ok {
		return indexedActivityEvents(g.activity)
	}
	return indexedActivityEvents(nil)
}

func goalRuntimeEvents(ctx storage, goalID string) []*jsObject {
	entry, ok := runtimeIndex(ctx).get(goalID)
	if !ok {
		return []*jsObject{}
	}
	out := []*jsObject{}
	for _, e := range []*jsObject{entry.audit, entry.completion} {
		if e == nil {
			continue
		}
		found := false
		for _, r := range entry.recent {
			if r == e {
				found = true
				break
			}
		}
		if !found {
			out = append(out, e)
		}
	}
	return append(out, entry.recent...)
}

// goalOracleState is the consult state of one fingerprint: {failedAttempts, followupAttempted, result?, lastFailure?}.
func goalOracleState(ctx storage, goalID, fingerprint string) *jsObject {
	if g, ok := runtimeIndex(ctx).get(goalID); ok {
		if st, ok := g.oracle.get(fingerprint); ok {
			return st.clone()
		}
	}
	return newOracleConsultState()
}

func newOracleConsultState() *jsObject {
	o := newObject()
	o.set("failedAttempts", 0.0)
	o.set("followupAttempted", false)
	return o
}

func loadLedgerState(ctx storage) ledgerStateReadResult {
	filePath := goalLedgerPath(ctx)
	if cached := ledgerCache[filePath]; cached != nil {
		cp := checkpointCache[filePath]
		if cp == nil || cp.coveredBytes != cached.size || cp.coveredEvents != len(cached.events) {
			cp = buildCheckpointFromEvents(cached.events, cached.size)
			setCheckpointCache(filePath, cp)
		}
		return ledgerStateReadResult{
			state:              finalizeLedgerState(cloneAccumulator(cp.acc)),
			recentEventsByGoal: recentToMap(cp.recentEventsByGoal.copy()),
			malformed:          cached.malformed,
			coveredBytes:       cached.size,
			coveredEvents:      len(cached.events),
			source:             "cache",
		}
	}
	cp := readLedgerCheckpointFile(filePath)
	var size int64
	if fi, err := os.Stat(filePath); err == nil {
		size = fi.Size()
	}
	if cp != nil && cp.coveredBytes == size {
		return ledgerStateReadResult{
			state:              finalizeLedgerState(cloneAccumulator(cp.acc)),
			recentEventsByGoal: recentToMap(cp.recentEventsByGoal.copy()),
			coveredBytes:       cp.coveredBytes,
			coveredEvents:      cp.coveredEvents,
			source:             "checkpoint",
		}
	}
	if cp != nil && cp.coveredBytes < size {
		tail := readLedgerTailFrom(filePath, cp.coveredBytes)
		acc := cloneAccumulator(cp.acc)
		recent := cp.recentEventsByGoal.copy()
		for _, e := range tail.events {
			applyLedgerEvent(acc, e)
			indexLedgerEvent(cp.runtimeIndex, e)
			if id, ok := goalIdOf(e); ok {
				appendRecent(recent, id, e)
			}
		}
		state := finalizeLedgerState(acc)
		updated := &ledgerCheckpoint{
			createdAt:          nowIso(),
			coveredBytes:       size,
			coveredEvents:      cp.coveredEvents + len(tail.events),
			acc:                acc,
			recentEventsByGoal: recent,
			runtimeIndex:       cp.runtimeIndex,
		}
		setCheckpointCache(filePath, updated)
		writeLedgerCheckpointAtomic(filePath, updated)
		lastCheckpointDiskWrite[filePath] = lastCheckpointWrite{appendsSinceWrite: updated.coveredEvents, at: clockMs()}
		return ledgerStateReadResult{state: state, recentEventsByGoal: recentToMap(recent), malformed: tail.malformed, coveredBytes: size, coveredEvents: updated.coveredEvents, source: "tail"}
	}
	full := readGoalLedgerCold(filePath)
	fresh := buildCheckpointFromEvents(full.events, size)
	setCheckpointCache(filePath, fresh)
	writeLedgerCheckpointAtomic(filePath, fresh)
	lastCheckpointDiskWrite[filePath] = lastCheckpointWrite{appendsSinceWrite: fresh.coveredEvents, at: clockMs()}
	return ledgerStateReadResult{
		state:              finalizeLedgerState(cloneAccumulator(fresh.acc)),
		recentEventsByGoal: recentToMap(fresh.recentEventsByGoal.copy()),
		malformed:          full.malformed,
		coveredBytes:       size,
		coveredEvents:      len(full.events),
		source:             "full",
	}
}
