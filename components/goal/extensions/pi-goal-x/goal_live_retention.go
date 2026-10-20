package pi_goal_x

import (
	"crypto/sha256"
	"encoding/base64"
)

// ported from goal-live-retention.ts
//
// Prefix-stable retention for request-only goal-state tails. The context hook appends live goal state to every provider
// request without persisting it; this makes each request a true prefix of the next by re-inserting previously sent tails
// verbatim at their recorded anchor, verifying the whole preceding history unchanged by digest, and appending only genuinely
// new tail content. Any history rewrite, unsafe insertion point or overflow performs one documented full reset.
//
// Messages are decoded JSON values (*jsObject records inside []any content arrays).

const liveContextType = "pi-goal-live-context"

const (
	liveTailGoalState    = "goal-state"
	liveTailGoalCounters = "goal-counters"
)

// maxRetainedLiveTails bounds retained tails per session; exceeding it performs a single full reset.
const maxRetainedLiveTails = 32

// maxTrackedSessions bounds concurrently tracked sessions (parent plus isolated runs).
const maxTrackedSessions = 16

// maxRetainedCounterBytes bounds extra historical observations, excluding the single current policy block.
const maxRetainedCounterBytes = 4096

// freshLiveTails: counters nil means absent.
type freshLiveTails struct {
	state    string
	counters *string
}

type retainedTail struct {
	anchorIndex       int
	anchorFingerprint *string
	content           string
	kind              string
}

type sessionRetention struct {
	tails     []retainedTail
	lastFresh *freshLiveTails
	// baseFingerprints is the digest of every base message at the last apply (nil entry = not serializable); any earlier
	// rewrite resets.
	baseFingerprints []*string
}

func (f freshLiveTails) blocks() []string {
	if f.counters == nil {
		return []string{f.state}
	}
	return []string{f.state, *f.counters}
}

// asRecord returns the value as an object record, or nil (goal-record.ts asRecord).
func asRecord(v any) *jsObject {
	o, _ := v.(*jsObject)
	return o
}

// fingerprintMessage is a full-message digest, or nil (never a match) when serialization fails.
func fingerprintMessage(message any) *string {
	sum := sha256.Sum256([]byte(marshalJSON(message, "")))
	s := base64.StdEncoding.EncodeToString(sum[:])
	return &s
}

// splitsToolBatch: tool results must stay adjacent to the call they answer, so a retained tail may never be inserted
// directly before one. Covers the session role (toolResult), the provider wire role (tool), and result-bearing content
// blocks in either vocabulary.
func splitsToolBatch(next any) bool {
	record := asRecord(next)
	if record == nil {
		return false
	}
	role, _ := record.str("role")
	if role == "toolResult" || role == "tool" {
		return true
	}
	if content, ok := record.vals["content"].([]any); ok {
		for _, block := range content {
			entry := asRecord(block)
			if entry == nil {
				continue
			}
			typ, _ := entry.str("type")
			if typ == "tool_result" || typ == "toolResult" {
				return true
			}
			if _, ok := entry.get("toolCallId"); ok {
				return true
			}
			if _, ok := entry.get("toolUseId"); ok {
				return true
			}
		}
	}
	return false
}

func makeLiveMessage(content string) *jsObject {
	m := newObject()
	m.set("role", "custom")
	m.set("customType", liveContextType)
	m.set("content", content)
	m.set("display", false)
	m.set("timestamp", 0.0)
	return m
}

type liveTailRetention struct {
	sessions    map[string]*sessionRetention
	sessionKeys []string
}

func newLiveTailRetention() *liveTailRetention {
	return &liveTailRetention{sessions: map[string]*sessionRetention{}}
}

// liveApplyResult is the request messages (base plus live tails) and every transient content for marker relocation.
type liveApplyResult struct {
	messages          []any
	transientContents []string
}

func samePtr(a, b *string) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// apply returns base with retained and fresh tails; fresh nil clears the session.
func (r *liveTailRetention) apply(sessionKey string, base []any, fresh *freshLiveTails) liveApplyResult {
	if fresh == nil {
		r.dropSession(sessionKey)
		return liveApplyResult{messages: append([]any{}, base...), transientContents: []string{}}
	}
	// Hash each message once per request. Extensions may reuse and mutate message objects, so object identity cannot cache a
	// digest across requests.
	fingerprints := make([]*string, len(base))
	for i, m := range base {
		fingerprints[i] = fingerprintMessage(m)
	}
	session := r.sessions[sessionKey]
	if session == nil {
		session = &sessionRetention{}
		if len(r.sessionKeys) >= maxTrackedSessions {
			r.dropSession(r.sessionKeys[0])
		}
		r.sessions[sessionKey] = session
		r.sessionKeys = append(r.sessionKeys, sessionKey)
	}
	reset := func() {
		session.tails = nil
		session.lastFresh = nil
		session.baseFingerprints = nil
	}
	// A changed state block supersedes every previously sent tail: start fresh rather than leave a stale objective or a
	// cancelled scheduling instruction lingering mid-history.
	if session.lastFresh != nil && (session.lastFresh.state != fresh.state ||
		(session.lastFresh.counters != nil && fresh.counters == nil)) {
		reset()
	}
	// Tails anchored while history was empty would linger at the head once real history arrives.
	if len(session.tails) > 0 && len(session.baseFingerprints) == 0 && len(base) > 0 {
		reset()
	}
	// Any rewrite earlier than the anchors breaks the prefix even when the trailing message is identical, so verify the
	// whole preceding history.
	if len(session.baseFingerprints) > len(base) {
		reset()
	} else {
		for i := range session.baseFingerprints {
			if fingerprints[i] == nil || !samePtr(fingerprints[i], session.baseFingerprints[i]) {
				reset()
				break
			}
		}
	}
	// An unsafe anchor invalidates the whole replay. Keeping an earlier policy while re-appending fresh blocks would
	// duplicate that policy.
	valid := 0
	for valid < len(session.tails) {
		tail := session.tails[valid]
		if tail.anchorIndex > len(base) {
			break
		}
		if tail.anchorIndex > 0 {
			current := fingerprints[tail.anchorIndex-1]
			if current == nil || !samePtr(current, tail.anchorFingerprint) {
				break
			}
		}
		if tail.anchorIndex < len(base) && splitsToolBatch(base[tail.anchorIndex]) {
			break
		}
		valid++
	}
	if valid < len(session.tails) {
		reset()
	}
	blocks := fresh.blocks()
	// Append only the suffix that differs from the last emitted tails, so unchanged content across repeats, tool loops, and
	// history advances adds zero blocks while keeping the prefix literal.
	var previous []string
	if session.lastFresh != nil {
		previous = session.lastFresh.blocks()
	}
	shared := 0
	for shared < len(blocks) && shared < len(previous) && blocks[shared] == previous[shared] {
		shared++
	}
	// Overflow is measured against what this request actually appends: an unchanged request adds nothing and must never
	// trigger a reset.
	counterBytes := 0
	for _, tail := range session.tails {
		if tail.kind == liveTailGoalCounters {
			counterBytes += len(tail.content)
		}
	}
	if fresh.counters != nil && shared != len(blocks) {
		counterBytes += len(*fresh.counters)
	}
	if len(session.tails)+(len(blocks)-shared) > maxRetainedLiveTails || counterBytes > maxRetainedCounterBytes {
		reset()
		shared = 0
	}
	messages := append([]any{}, base...)
	for i := len(session.tails) - 1; i >= 0; i-- {
		tail := session.tails[i]
		messages = append(messages, nil)
		copy(messages[tail.anchorIndex+1:], messages[tail.anchorIndex:])
		messages[tail.anchorIndex] = makeLiveMessage(tail.content)
	}
	var lastFingerprint *string
	if len(fingerprints) > 0 {
		lastFingerprint = fingerprints[len(fingerprints)-1]
	}
	for i := shared; i < len(blocks); i++ {
		content := blocks[i]
		kind := liveTailGoalCounters
		if i == 0 {
			kind = liveTailGoalState
		}
		messages = append(messages, makeLiveMessage(content))
		session.tails = append(session.tails, retainedTail{anchorIndex: len(base), anchorFingerprint: lastFingerprint, content: content, kind: kind})
	}
	session.baseFingerprints = fingerprints
	lastFresh := *fresh
	session.lastFresh = &lastFresh
	seen := map[string]bool{}
	transient := []string{}
	for _, c := range append(r.tailContents(session), blocks...) {
		if !seen[c] {
			seen[c] = true
			transient = append(transient, c)
		}
	}
	return liveApplyResult{messages: messages, transientContents: transient}
}

func (r *liveTailRetention) tailContents(s *sessionRetention) []string {
	out := make([]string, 0, len(s.tails))
	for _, t := range s.tails {
		out = append(out, t.content)
	}
	return out
}

func (r *liveTailRetention) dropSession(key string) {
	if _, ok := r.sessions[key]; !ok {
		return
	}
	delete(r.sessions, key)
	for i, k := range r.sessionKeys {
		if k == key {
			r.sessionKeys = append(r.sessionKeys[:i], r.sessionKeys[i+1:]...)
			break
		}
	}
}

// contents returns the retained tail contents of a session; ok is false for an unknown session.
func (r *liveTailRetention) contents(sessionKey string) ([]string, bool) {
	s := r.sessions[sessionKey]
	if s == nil {
		return nil, false
	}
	return r.tailContents(s), true
}

// clear drops one session, or every session when sessionKey is nil.
func (r *liveTailRetention) clear(sessionKey *string) {
	if sessionKey == nil {
		r.sessions = map[string]*sessionRetention{}
		r.sessionKeys = nil
		return
	}
	r.dropSession(*sessionKey)
}
