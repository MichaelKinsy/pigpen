package prompt

import (
	"fmt"
	"strings"
	"sync"
)

// maxReminderRoots caps concurrent conversation states tracked by one injector.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:210
const maxReminderRoots = 8

// RenderDateCwdReminder renders omp's per-turn date/cwd reminder XML block.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:23-25
func RenderDateCwdReminder(date, cwd string) string {
	text := strings.ReplaceAll(dateCwdReminderTemplate, "{{date}}", date)
	text = strings.ReplaceAll(text, "{{cwd}}", NormalizePromptPath(StandardizePromptCwd(cwd)))
	return strings.TrimSpace(formatPrompt(text))
}

type reminderRootState struct {
	reminder          string
	syntheticReminder string            // when root is a summary turn, the immutable reminder prepended before it
	injections        map[string]string // turn key -> reminder placed on that turn
	seen              map[string]bool   // turn keys already sent in previous requests
	counts            map[string]int    // base key -> count in previous request
	order             uint64            // monotonic touch counter for LRU eviction
}

// DateCwdReminderInjector attaches omp's deterministic `<system-reminder>` carrying
// the local date and cwd onto the first user-like turn (keeping the system prompt
// byte-stable for prompt caching) and onto any new unsent user turn when the date
// or cwd changes.
//
// State is tracked per conversation root (up to maxReminderRoots, LRU-evicted) so
// interleaved side/advisor requests or compactions do not corrupt or rewrite earlier
// turns. Because PiG's EventContext does not support injecting synthetic
// `role: "developer"` control turns, a mid-tool-loop date/cwd change leaves
// `reminder` un-updated until a new unsent user turn arrives (instead of dropping
// the change or retroactively rewriting an already-sent user turn).
//
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:240-685
type DateCwdReminderInjector struct {
	mu     sync.Mutex
	states map[string]*reminderRootState
	tick   uint64
}

// NewDateCwdReminderInjector creates a fresh injector for a session.
func NewDateCwdReminderInjector() *DateCwdReminderInjector {
	return &DateCwdReminderInjector{
		states: map[string]*reminderRootState{},
	}
}

// Reset clears per-session injection state (e.g. on session_start).
func (d *DateCwdReminderInjector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.states = map[string]*reminderRootState{}
	d.tick = 0
}

// Transform returns a new message slice with the date/cwd reminder attached without
// mutating incoming maps, or (messages, false) when nothing needs changing.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:251-684
func (d *DateCwdReminderInjector) Transform(messages []any, date, cwd string) ([]any, bool) {
	return d.transform(messages, date, cwd, false)
}

// TransformInPlace attaches the date/cwd reminder by mutating incoming user message
// maps in place when only existing turns are modified (preserving PiG's positional
// system message placement via unchanged list identity), or returning a new slice
// when a leading synthetic user message must be prepended (e.g. post-compaction
// lists starting with compactionSummary and no user turn).
func (d *DateCwdReminderInjector) TransformInPlace(messages []any, date, cwd string) ([]any, bool, bool) {
	out, changed := d.transform(messages, date, cwd, true)
	if !changed {
		return messages, false, false
	}
	replacedList := len(out) != len(messages)
	return out, true, replacedList
}

func (d *DateCwdReminderInjector) transform(messages []any, date, cwd string, inPlace bool) ([]any, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	reminder := RenderDateCwdReminder(date, cwd)
	keys, counts := keysOfMessages(messages)

	// Find the conversation's first user-like turn: either the first role=="user"
	// or an earlier summary turn (compactionSummary / branchSummary / custom).
	// PiG's context event delivers agent messages before convertToLlm, so a
	// compacted history starts with compactionSummary; anchoring root state on
	// that summary keeps the prepended synthetic reminder byte-identical across
	// midnight rollovers and once later real user turns arrive.
	firstIdx := -1
	var firstMsg map[string]any
	firstIsSummary := false
	for i, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role := anyString(msg["role"])
		if role == "user" {
			firstIdx = i
			firstMsg = msg
			break
		}
		if isUserLikeSummaryRole(role) {
			firstIdx = i
			firstMsg = msg
			firstIsSummary = true
			break
		}
	}
	if firstIdx < 0 {
		return messages, false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	root := keys[firstIdx]
	state := d.states[root]
	if state != nil {
		followOrdinals(state, counts, false)
		if carried := carriedReminder(state, keys); carried != "" {
			state.reminder = carried
		}
	} else {
		predecessor := d.predecessorOf(keys)
		state = newReminderRootState(keys, counts, root, firstMsg, firstIsSummary, reminder, predecessor)
	}
	d.activate(root, state)

	if state.reminder != reminder {
		// Only a user turn appended after everything already sent takes the changed
		// reminder; already-sent turns are never retroactively rewritten. If no new
		// user turn exists yet (mid-tool-loop rollover), state.reminder stays at its
		// previous value so the next new user turn receives the updated reminder.
		lastSent := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if state.seen[keys[i]] {
				lastSent = i
				break
			}
		}
		newUser := -1
		for i := len(messages) - 1; i > lastSent; i-- {
			if msg, ok := messages[i].(map[string]any); ok && msg["role"] == "user" {
				newUser = i
				break
			}
		}
		if newUser >= 0 {
			state.injections[keys[newUser]] = reminder
			state.reminder = reminder
		}
	}

	changed := false
	var out []any
	if inPlace && state.syntheticReminder == "" {
		out = messages
	} else {
		out = make([]any, 0, len(messages)+1)
	}
	for i, m := range messages {
		key := keys[i]
		state.seen[key] = true
		if i == firstIdx && state.syntheticReminder != "" {
			synthetic := map[string]any{
				"role":    "user",
				"content": state.syntheticReminder,
			}
			if ts, ok := firstMsg["timestamp"]; ok && ts != nil {
				synthetic["timestamp"] = ts
			}
			out = append(out, synthetic)
			changed = true
		}
		msg, ok := m.(map[string]any)
		if !ok || msg["role"] != "user" {
			if !inPlace || state.syntheticReminder != "" {
				out = append(out, m)
			}
			continue
		}
		rem, hasRem := state.injections[key]
		if !hasRem || messageStartsWithReminder(msg, rem) {
			if !inPlace || state.syntheticReminder != "" {
				out = append(out, m)
			}
			continue
		}
		if inPlace && state.syntheticReminder == "" {
			prependReminderInPlace(msg, rem)
		} else {
			out = append(out, prependReminderToUserMessage(msg, rem))
		}
		changed = true
	}
	if !changed {
		return messages, false
	}
	return out, true
}

func isUserLikeSummaryRole(role string) bool {
	return role == "compactionSummary" || role == "branchSummary" || role == "custom"
}

// keysOfMessages computes one deterministic key per turn from role, timestamp,
// attribution (for user/developer), and an ordinal for duplicate keys. Content is
// intentionally excluded so in-place rewrites keep the same turn key and identical
// same-millisecond or untimestamped turns get distinct ordinals.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:268-280
func keysOfMessages(messages []any) ([]string, map[string]int) {
	counts := make(map[string]int, len(messages))
	keys := make([]string, len(messages))
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		var base string
		if !ok {
			base = "unknown:"
		} else {
			role := anyString(msg["role"])
			ts := ""
			if v, exists := msg["timestamp"]; exists && v != nil {
				ts = anyString(v)
			}
			if role == "user" || role == "developer" {
				attr := ""
				if v, exists := msg["attribution"]; exists && v != nil {
					attr = anyString(v)
				}
				base = role + ":" + ts + ":" + attr
			} else {
				base = role + ":" + ts
			}
		}
		count := counts[base]
		counts[base] = count + 1
		if count == 0 {
			keys[i] = base
		} else {
			keys[i] = fmt.Sprintf("%s#%d", base, count)
		}
	}
	return keys, counts
}

// splitOrdinal splits "base#N" into ("base", N); keys without "#N" return (key, 0).
func splitOrdinal(key string) (string, int) {
	idx := strings.LastIndexByte(key, '#')
	if idx < 0 || idx == len(key)-1 {
		return key, 0
	}
	n := 0
	for i := idx + 1; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return key, 0
		}
		n = n*10 + int(c-'0')
	}
	return key[:idx], n
}

// followOrdinals mirrors DateCwdReminderInjector.#followOrdinals.
// upstream: packages/coding-agent/src/session/date-cwd-reminder.ts:292-324
func followOrdinals(state *reminderRootState, counts map[string]int, rootChanged bool) {
	shift := func(key string) (string, bool) {
		base, ordinal := splitOrdinal(key)
		now := counts[base]
		lost := state.counts[base] - now
		if lost <= 0 {
			return key, true
		}
		if !rootChanged {
			if ordinal < now {
				return key, true
			}
			return "", false
		}
		shifted := ordinal - lost
		if shifted < 0 {
			return "", false
		}
		if shifted == 0 {
			return base, true
		}
		return fmt.Sprintf("%s#%d", base, shifted), true
	}

	nextInjections := make(map[string]string, len(state.injections))
	for key, rem := range state.injections {
		if shifted, ok := shift(key); ok {
			nextInjections[shifted] = rem
		}
	}
	state.injections = nextInjections

	nextSeen := make(map[string]bool, len(state.seen))
	for key := range state.seen {
		if shifted, ok := shift(key); ok {
			nextSeen[shifted] = true
		}
	}
	state.seen = nextSeen

	nextCounts := make(map[string]int, len(counts))
	for k, v := range counts {
		nextCounts[k] = v
	}
	state.counts = nextCounts
}

func (d *DateCwdReminderInjector) predecessorOf(keys []string) *reminderRootState {
	var best *reminderRootState
	bestShared := 0
	for _, state := range d.states {
		shared := 0
		for _, key := range keys {
			if state.seen[key] || state.injections[key] != "" {
				shared++
			}
		}
		if shared > 0 && (shared > bestShared || (shared == bestShared && (best == nil || state.order > best.order))) {
			best = state
			bestShared = shared
		}
	}
	return best
}

func newReminderRootState(keys []string, counts map[string]int, root string, firstMsg map[string]any, firstIsSummary bool, reminder string, predecessor *reminderRootState) *reminderRootState {
	state := &reminderRootState{
		reminder:   reminder,
		injections: map[string]string{},
		seen:       map[string]bool{},
		counts:     map[string]int{},
	}
	if predecessor != nil {
		for k, v := range predecessor.injections {
			state.injections[k] = v
		}
		for k, v := range predecessor.seen {
			state.seen[k] = v
		}
		for k, v := range predecessor.counts {
			state.counts[k] = v
		}
	}
	followOrdinals(state, counts, true)
	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		present[k] = true
	}
	for k := range state.injections {
		if !present[k] {
			delete(state.injections, k)
		}
	}
	for k := range state.seen {
		if !present[k] {
			delete(state.seen, k)
		}
	}
	if firstIsSummary {
		state.syntheticReminder = reminder
	} else if _, already := state.injections[root]; !already && !messageStartsWithReminder(firstMsg, reminder) {
		state.injections[root] = reminder
	}
	return state
}

func carriedReminder(state *reminderRootState, keys []string) string {
	for i := len(keys) - 1; i >= 0; i-- {
		if rem, ok := state.injections[keys[i]]; ok {
			return rem
		}
	}
	return state.syntheticReminder
}

func (d *DateCwdReminderInjector) activate(root string, state *reminderRootState) {
	d.tick++
	state.order = d.tick
	d.states[root] = state
	for len(d.states) > maxReminderRoots {
		var oldestKey string
		var oldestOrder uint64
		first := true
		for k, s := range d.states {
			if first || s.order < oldestOrder {
				oldestKey = k
				oldestOrder = s.order
				first = false
			}
		}
		delete(d.states, oldestKey)
	}
}

func messageStartsWithReminder(msg map[string]any, reminder string) bool {
	switch c := msg["content"].(type) {
	case string:
		return strings.HasPrefix(c, reminder)
	case []any:
		if len(c) == 0 {
			return false
		}
		first, _ := c[0].(map[string]any)
		return first != nil && first["type"] == "text" && first["text"] == reminder
	default:
		return false
	}
}

func prependReminderInPlace(msg map[string]any, reminder string) {
	switch c := msg["content"].(type) {
	case string:
		msg["content"] = reminder + "\n\n" + c
	case []any:
		blocks := make([]any, 0, len(c)+1)
		blocks = append(blocks, map[string]any{"type": "text", "text": reminder})
		blocks = append(blocks, c...)
		msg["content"] = blocks
	default:
		msg["content"] = reminder
	}
}

func prependReminderToUserMessage(msg map[string]any, reminder string) map[string]any {
	copied := make(map[string]any, len(msg))
	for k, v := range msg {
		copied[k] = v
	}
	prependReminderInPlace(copied, reminder)
	return copied
}
