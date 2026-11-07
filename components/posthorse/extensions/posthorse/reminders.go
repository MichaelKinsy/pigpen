package posthorse

// Port of src/reminders.ts: the one-per-window checkpoint reminder decision and the
// leaf-certified branch facts it reads.

import (
	"math"
	"reflect"
	"slices"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// reminderSession is the part of ctx.sessionManager that the reminder lookup reads. The SDK's
// ReadonlySessionManager implements it; the benchmark and the tests may stand in an in-memory log.
// GetLeafEntry is `getEntry(getLeafId() ?? "")` in one read (Pi session-manager.js getLeafEntry:
// `this.leafId ? this.byId.get(this.leafId) : undefined`); GetBranch serves the optional hybrid walk.
type reminderSession interface {
	GetSessionID() (string, error)
	GetSessionFile() (*string, error)
	GetLeafEntry() (map[string]any, error)
	GetEntry(id string) (map[string]any, error)
	GetBranch(fromID *string) ([]map[string]any, error)
}

// reminderState is ReminderState (reminders.ts:13-20). The TS `manager` member (object identity of
// ctx.sessionManager) has no Go counterpart: the session id and file key the cache the same way,
// because a replaced session always has a new id.
type reminderState struct {
	sessionID string
	file      *string
	leaf      rec // nil is undefined
	windowId  string
	reminders []rec
}

// reminderLookup is ReminderLookup (reminders.ts:21-24). TS handlers are synchronous, so a lookup never
// interleaves with a reset; the SDK runs every request on its own goroutine and a lookup is several host
// round trips, so the state is guarded and a reset bumps a generation: a lookup that began before a reset
// returns its result to its caller but does not store it.
type reminderLookup struct {
	mu    sync.Mutex
	state *reminderState
	gen   uint64
}

// staleReminder is StaleReminderPredicate (reminders.ts:25-29) over a message record; nil stands for
// NO_STALE_REMINDER (rollover.ts:75), which no message is stale under.
type staleReminder func(message rec) bool

// reminderMatches is reminders.ts:31-40. Every comparison is `===`: an absent fingerprint number
// matches only an absent member, never JSON null.
func reminderMatches(details any, fingerprint reminderFingerprint) bool {
	record, ok := details.(rec)
	if !ok {
		return false
	}
	window, ok := record["windowId"].(string)
	if !ok || window != fingerprint.windowId {
		return false
	}
	return strictIntMember(record, "contextWindow", fingerprint.contextWindow) &&
		strictIntMember(record, "reserveTokens", fingerprint.reserveTokens)
}

// strictIntMember is `record[key] === want` where want is a number or undefined (nil).
func strictIntMember(record rec, key string, want *int) bool {
	value, present := record[key]
	if want == nil {
		return !present
	}
	number, ok := value.(float64)
	return ok && number == float64(*want)
}

// hasReminder is reminders.ts:41-51.
func hasReminder(entries []rec, fingerprint reminderFingerprint) bool {
	return slices.ContainsFunc(entries, func(entry rec) bool {
		return entryType(entry) == "custom_message" && isReminderType(entry["customType"]) &&
			reminderMatches(entry["details"], fingerprint)
	})
}

// reminderBufferTokens is REMINDER_BUFFER_TOKENS (reminders.ts:52).
const reminderBufferTokens = 32_000

// reminderDecision is the TS ReminderDecision (reminders.ts:53-59): kind is "skip" (with reason),
// "already-present" or "send" (with fingerprint).
type reminderDecisionResult struct {
	kind        string
	reason      string
	fingerprint reminderFingerprint
}

// reminderBudget is reminders.ts:60-78: a skip reason, or the budget.
func reminderBudget(host policyContext) (skip string, available *budget, err error) {
	usage, err := host.getContextUsage()
	if err != nil {
		return "", nil, err
	}
	if usage == nil || usage.tokens == nil || usage.contextWindow <= 0 {
		return "unknown-usage", nil, nil
	}
	window := usage.contextWindow
	b, err := budgetFor(host, &window)
	if err != nil {
		return "", nil, err
	}
	if b == nil || !b.enabled || !b.supported {
		return "unavailable-budget", nil, nil
	}
	buffer := min(reminderBufferTokens, int(math.Floor(float64(b.usable)*0.1)))
	if *usage.tokens < b.rolloverAt-buffer || *usage.tokens >= b.rolloverAt {
		return "outside-band", nil, nil
	}
	return "", b, nil
}

// endsRun is reminders.ts:79-90. The pending-message round trip runs last, as the `&&` chain does.
func endsRun(event rec, ctx sdk.Context) (bool, error) {
	message, _ := recOf(event["message"])
	if role, _ := str(message, "role"); role != "assistant" {
		return false, nil
	}
	content, _ := arrOf(message["content"])
	if slices.ContainsFunc(content, func(part any) bool {
		block, _ := recOf(part)
		kind, _ := str(block, "type")
		return kind == "toolCall"
	}) {
		return false, nil
	}
	if cont, _ := event["continue"].(bool); cont {
		return false, nil
	}
	if pending := pendingMessagesOf(event); len(pending) != 0 {
		return false, nil
	}
	pending, err := ctx.HasPendingMessages()
	if err != nil {
		return false, err
	}
	return !pending, nil
}

// pendingMessagesOf is event.context.pendingMessages.
func pendingMessagesOf(event rec) []any {
	context, _ := recOf(event["context"])
	pending, _ := arrOf(context["pendingMessages"])
	return pending
}

// reminderDecision is reminders.ts:91-123.
func reminderDecision(event rec, ctx sdk.Context, host policyContext, reminders *reminderLookup) (reminderDecisionResult, error) {
	ended, err := endsRun(event, ctx)
	if err != nil {
		return reminderDecisionResult{}, err
	}
	if ended {
		return reminderDecisionResult{kind: "skip", reason: "run-ended"}, nil
	}
	skip, b, err := reminderBudget(host)
	if err != nil {
		return reminderDecisionResult{}, err
	}
	if skip != "" {
		return reminderDecisionResult{kind: "skip", reason: skip}, nil
	}
	state, err := reminders.read(ctx.SessionManager())
	if err != nil {
		return reminderDecisionResult{}, err
	}
	fingerprint := reminderFingerprint{windowId: state.windowId, contextWindow: &b.contextWindow, reserveTokens: &b.reserveTokens}
	if hasReminder(state.reminders, fingerprint) || slices.ContainsFunc(pendingMessagesOf(event), func(item any) bool {
		message, _ := recOf(item)
		role, _ := str(message, "role")
		return role == "custom" && isReminderType(message["customType"]) && reminderMatches(message["details"], fingerprint)
	}) {
		return reminderDecisionResult{kind: "already-present"}, nil
	}
	return reminderDecisionResult{kind: "send", fingerprint: fingerprint}, nil
}

// sameEntry is the `previous.leaf === entry` certification (reminders.ts:160). Pi hands the very
// same entry object back; a Go read decodes a fresh record, so equality of the whole record
// stands in for identity (the id is compared first because it is the cheap way to say no).
func sameEntry(a, b rec) bool {
	if a == nil || b == nil {
		return false
	}
	ida, _ := str(a, "id")
	idb, _ := str(b, "id")
	if ida != idb {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// idStrictlyEquals is `entry.id === want` where want is the member `key` of another record: an
// absent member is undefined, which differs from JSON null.
func idStrictlyEquals(entry rec, want any, wantPresent bool) bool {
	id, present := entry["id"]
	if !wantPresent || !present {
		return !wantPresent && !present
	}
	if want == nil || id == nil {
		return want == nil && id == nil
	}
	s, ok := id.(string)
	w, wok := want.(string)
	return ok && wok && s == w
}

// reminderBranchAfter is the number of by-id reads after which scanAncestry fetches the rest of the
// ancestry with one GetBranch (the hybrid walk of review R7). 0 keeps the by-id walk to the root, which is
// what reminders.ts does and what the upstream tests count (they forbid getBranch for reminder checks).
// The result is the same either way: windowId and reminders are a pure function of the ancestry.
const reminderBranchAfter = 0

// ancestryScan is the state of one leaf-to-root scan (reminders.ts:129-144 ScanProgress and the
// collected reminders).
type ancestryScan struct {
	found           []walkedReminder // reminder entries, leaf to root
	boundary        rec
	boundaryIndex   int
	window          rec
	kept            bool
	keptIndex       int
	keptWant        any
	keptWantPresent bool
	index           int
}

type walkedReminder struct {
	entry rec
	index int
}

// visit advances the scan by one entry (reminders.ts:157-176). done is true when the scan is complete;
// certified is true when the entry is the certified previous leaf and the cached state extends the walk.
func (a *ancestryScan) visit(entry rec, previous *reminderState) (done, certified bool) {
	// Entry identity, not a reusable id, certifies an append-only extension of this leaf (reminders.ts:160).
	if a.boundary == nil && previous != nil && sameEntry(previous.leaf, entry) {
		return true, true
	}
	if entryType(entry) == "custom_message" && isReminderType(entry["customType"]) {
		a.found = append(a.found, walkedReminder{entry, a.index})
	}
	// advanceBoundary (reminders.ts:134-144)
	if a.boundary == nil {
		if t := entryType(entry); t == "compaction" || t == "context_window" {
			a.boundary, a.boundaryIndex = entry, a.index
			if t == "compaction" {
				a.keptWant, a.keptWantPresent = entry["firstKeptEntryId"]
			}
		}
	}
	if a.window == nil && isWindow(entry) {
		a.window = entry
	}
	if a.boundary != nil {
		if entryType(a.boundary) == "context_window" {
			a.kept = true
		} else if idStrictlyEquals(entry, a.keptWant, a.keptWantPresent) {
			a.kept, a.keptIndex = true, a.index
		}
	}
	a.index++
	// Native compaction may keep a tail before the Posthorse boundary. Negative lookups reach root once.
	return a.window != nil && a.kept, false
}

// result is the windowId and the reminders of the active context (history-entries.ts contextStart over
// the walked entries, which run root to leaf in reverse: an entry at walk index w is at or after the
// start when w <= startWalk).
func (a *ancestryScan) result() (string, []rec) {
	startWalk := a.keptIndex
	if a.boundary != nil && (entryType(a.boundary) == "context_window" || a.keptIndex < 0) {
		startWalk = a.boundaryIndex - 1
	}
	windowID := "initial"
	if a.window != nil {
		if id, ok := str(a.window, "id"); ok {
			windowID = id
		}
	}
	reminders := make([]rec, 0, len(a.found))
	for i := len(a.found) - 1; i >= 0; i-- {
		if a.boundary == nil || a.found[i].index <= startWalk {
			reminders = append(reminders, a.found[i].entry)
		}
	}
	return windowID, reminders
}

// scanAncestry is reminders.ts:151-178. It walks the by-id ancestry from the leaf and keeps only the
// reminders of the active context: the boundary's tail, including a tail that native compaction kept
// before the Posthorse boundary. With branchAfter > 0 the walk fetches the remaining ancestry with one
// GetBranch after that many by-id reads (see reminderBranchAfter).
func scanAncestry(session reminderSession, leaf rec, previous *reminderState, branchAfter int) (string, []rec, error) {
	scan := ancestryScan{boundaryIndex: -1, keptIndex: -1}
	finish := func(certified bool) (string, []rec, error) {
		if !certified {
			windowID, reminders := scan.result()
			return windowID, reminders, nil
		}
		added := make([]rec, 0, len(scan.found))
		for i := len(scan.found) - 1; i >= 0; i-- {
			added = append(added, scan.found[i].entry)
		}
		if len(added) == 0 {
			return previous.windowId, previous.reminders, nil
		}
		return previous.windowId, slices.Concat(previous.reminders, added), nil
	}
	steps := 0
	for entry := leaf; entry != nil; {
		if done, certified := scan.visit(entry, previous); done {
			return finish(certified)
		}
		next := parentID(entry)
		if next == "" {
			return finish(false)
		}
		if branchAfter > 0 && steps >= branchAfter {
			// The rest of the path in one read: root to parent, walked from the parent down to the root.
			path, err := session.GetBranch(&next)
			if err != nil {
				return "", nil, err
			}
			for i := len(path) - 1; i >= 0; i-- {
				if done, certified := scan.visit(path[i], previous); done {
					return finish(certified)
				}
			}
			return finish(false)
		}
		steps++
		var err error
		if entry, err = session.GetEntry(next); err != nil {
			return "", nil, err
		}
	}
	return finish(false)
}

// parentID is the id of an entry's parent as reminders.ts:145-150 parentEntry reads it: "" for a missing, null or empty one.
func parentID(entry rec) string {
	id, _ := str(entry, "parentId")
	return id
}

// createReminderLookup is reminders.ts:179-202.
func createReminderLookup() *reminderLookup { return &reminderLookup{} }

// reset drops the cached branch facts (session start and tree navigation).
func (l *reminderLookup) reset() {
	l.mu.Lock()
	l.state = nil
	l.gen++
	l.mu.Unlock()
}

// read is reminders.ts:185-200. It costs three reads while the leaf is unchanged: the session id and
// file (the cache key) and the leaf entry.
func (l *reminderLookup) read(session reminderSession) (*reminderState, error) {
	// The generation is taken before the first host read: a reset at any later point drops this result.
	l.mu.Lock()
	gen := l.gen
	l.mu.Unlock()
	sessionID, err := session.GetSessionID()
	if err != nil {
		return nil, err
	}
	file, err := session.GetSessionFile()
	if err != nil {
		return nil, err
	}
	leaf, err := session.GetLeafEntry()
	if err != nil {
		return nil, err
	}
	if len(leaf) == 0 {
		leaf = nil
	}
	l.mu.Lock()
	var previous *reminderState
	if s := l.state; s != nil && s.sessionID == sessionID && equalFile(s.file, file) {
		previous = s
	}
	l.mu.Unlock()
	if previous != nil && (sameEntry(previous.leaf, leaf) || (previous.leaf == nil && leaf == nil)) {
		return previous, nil
	}
	windowID, reminders, err := scanAncestry(session, leaf, previous, reminderBranchAfter)
	if err != nil {
		return nil, err
	}
	state := &reminderState{sessionID: sessionID, file: file, leaf: leaf, windowId: windowID, reminders: reminders}
	l.mu.Lock()
	if l.gen == gen {
		l.state = state
	}
	l.mu.Unlock()
	return state, nil
}

func equalFile(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// retainedProjection is the part of a session projection that native compaction kept before its newest
// compaction entry. Pi's buildContextEntries (session-manager.js) lists the newest compaction first, then
// the entries from firstKeptEntryId up to it, then every entry after it; the entries after it start at the
// compaction's child, the first one whose parentId is the compaction's id. That span is exactly the entries
// reminders.ts:204-221 retainedEntryIdSet names from the branch (system messages are not projected there,
// and countRetainedFromProjected skips them anyway), so the projection alone gives the same count without
// reading the branch (review R5). An empty result is the `undefined`/empty set of the TS.
func retainedProjection(projected []sdk.ProjectedSessionEntry) []sdk.ProjectedSessionEntry {
	if len(projected) < 2 || entryType(projected[0].SourceEntry) != "compaction" {
		return nil
	}
	compactionID, ok := str(projected[0].SourceEntry, "id")
	if !ok {
		return nil
	}
	end := len(projected)
	for i := 1; i < len(projected); i++ {
		if parent, ok := str(projected[i].SourceEntry, "parentId"); ok && parent == compactionID {
			end = i
			break
		}
	}
	return projected[1:end]
}

// countRetainedFromProjected is reminders.ts:223-244 over the retained span of the projection.
func countRetainedFromProjected(retained []sdk.ProjectedSessionEntry, stale staleReminder) int {
	count := 0
	for _, item := range retained {
		for _, message := range item.Messages {
			if role, _ := str(message, "role"); role != "system" && (stale == nil || !stale(message)) {
				count++
			}
		}
	}
	return count
}

// countRetainedMessages is reminders.ts:263-288. The TS reads the branch to name the retained entries and
// then the projection; the Go port reads the projection only (retainedProjection), one wire read per
// call. The TS duck-types a session manager without getBranch/buildSessionProjection and falls back to a
// raw-entry count; the Go SDK's ReadonlySessionManager always has both, so that branch cannot run.
func countRetainedMessages(ctx sdk.Context, stale staleReminder) (int, error) {
	projection, err := ctx.SessionManager().BuildSessionProjection()
	if err != nil {
		return 0, err
	}
	return countRetainedFromProjected(retainedProjection(projection.Entries), stale), nil
}
