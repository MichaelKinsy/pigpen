package warden

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session-scoped arming rules (src/arming.ts): a preparation (editing files matching globs) arms a command pattern for a
// window; while armed, matching commands fire the rule's action.
//
// The tracker is in-memory per session. State lives for the rule's window within a session; it is cleared when the
// session starts anew (session_start) and expires per `for`. It survives across agent runs within the same session
// (edit in one turn, reconcile in the next). No cross-session persistence.
//
// The armed check is deterministic: it fires whether or not the judge is available.

// defaultArmingDuration is 10 minutes (config.ts DEFAULT_ARMING_DURATION).
const defaultArmingDuration = 600_000 * time.Millisecond

var reArmingDuration = lazyRE(`^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$`)

// armingDuration is an arming duration (config.ts parseDuration): a number is milliseconds; a string takes `ms`, `s`,
// `m`, or `h`, and a string without a unit means minutes. Zero, negative, and unreadable values give the default.
func armingDuration(raw any) time.Duration {
	switch v := raw.(type) {
	case float64:
		if !math.IsInf(v, 0) && !math.IsNaN(v) && v > 0 {
			return time.Duration(v * float64(time.Millisecond))
		}
	case int:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case int64:
		if v > 0 {
			return time.Duration(v) * time.Millisecond
		}
	case string:
		m := reArmingDuration.FindStringSubmatch(jsTrim(v))
		if m == nil {
			return defaultArmingDuration
		}
		value, _ := strconv.ParseFloat(m[1], 64)
		unit := time.Minute
		switch m[2] {
		case "ms":
			unit = time.Millisecond
		case "s":
			unit = time.Second
		case "h":
			unit = time.Hour
		}
		if ms := value * float64(unit); ms > 0 {
			return time.Duration(ms)
		}
	}
	return defaultArmingDuration
}

type armedEntry struct {
	// expiresAt is refreshed on each matching edit.
	expiresAt time.Time
	// armedByPaths are the paths that armed the rule, for the dialog/status display; deduplicated.
	armedByPaths []string
}

type compiledArmingRule struct {
	rule         ArmingRule
	commandRegex *regexp.Regexp
	duration     time.Duration
}

// compileArmingRule compiles a rule, nil when its regex is invalid (Go syntax; deviation D9).
func compileArmingRule(rule ArmingRule) *compiledArmingRule {
	pattern := rule.Arms.Command
	if !rule.Arms.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return &compiledArmingRule{rule: rule, commandRegex: re, duration: armingDuration(rule.Arms.For)}
}

func compileArmingRules(rules []ArmingRule) []*compiledArmingRule {
	var out []*compiledArmingRule
	for _, r := range rules {
		if c := compileArmingRule(r); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// unparseableArmingRules are the ids of rules whose command regex cannot be compiled; surfaced once as a notice.
func unparseableArmingRules(rules []ArmingRule) []string {
	var ids []string
	for _, rule := range rules {
		if compileArmingRule(rule) == nil {
			ids = append(ids, rule.ID)
		}
	}
	return ids
}

func editedMatches(rule ArmingRule, candidate string) bool {
	return matchPathGlobs(rule.When.Edited, rule.When.Regex, candidate)
}

// ArmedHit is one armed rule a command fires.
type ArmedHit struct {
	ID           string
	Action       string
	Message      string
	ArmedByPaths []string
}

// ArmingTracker is the in-memory arming state for one session. Arm is called on write/edit calls whose path matches a
// rule's `when.edited` globs, CheckArmed on command calls to see whether any armed rule's regex matches, Reset when the
// session starts. It is safe for concurrent use.
type ArmingTracker struct {
	mu       sync.Mutex
	armed    map[string]*armedEntry
	order    []string
	compiled []*compiledArmingRule
	now      func() time.Time
	lastRef  *ArmingRule
	lastLen  int
}

// NewArmingTracker returns a tracker for the rules; now is the clock (time.Now when nil).
func NewArmingTracker(rules []ArmingRule, now func() time.Time) *ArmingTracker {
	if now == nil {
		now = time.Now
	}
	return &ArmingTracker{armed: map[string]*armedEntry{}, compiled: compileArmingRules(rules), now: now}
}

// UpdateRules recompiles the rules from the config; it recompiles only when the slice changes (its backing array or
// length), so a per-call config lookup that returns the same slice costs nothing. Armed state is kept.
func (t *ArmingTracker) UpdateRules(rules []ArmingRule) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ref *ArmingRule
	if len(rules) > 0 {
		ref = &rules[0]
	}
	if ref == t.lastRef && len(rules) == t.lastLen && t.lastLen > 0 {
		return
	}
	t.lastRef, t.lastLen = ref, len(rules)
	t.compiled = compileArmingRules(rules)
}

func expandHome(candidate string) string {
	if candidate == "~" || strings.HasPrefix(candidate, "~/") {
		return homeDir() + candidate[1:]
	}
	return candidate
}

// Arm records a write/edit call. If the tool is in a rule's `when.tools` (default write, edit) and the path matches a
// `when.edited` glob, the rule is armed (or refreshed). Sink targets (redirect/tee) are checked regardless of tool: a bash
// redirect is a write to that path. It returns the ids of the rules armed or refreshed by this call.
func (t *ArmingTracker) Arm(tool, path, cwd string, sinkTargets []string) []string {
	if path == "" && len(sinkTargets) == 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var all []string
	if path != "" {
		all = append(all, path)
	}
	for _, s := range sinkTargets {
		if s != "" {
			all = append(all, s)
		}
	}
	var armedIDs []string
	seen := map[string]bool{}
	matches := func(rule ArmingRule, candidates []string) bool {
		for _, candidate := range candidates {
			abs := resolvePath(cwd, expandHome(candidate))
			if editedMatches(rule, candidate) || editedMatches(rule, abs) {
				return true
			}
		}
		return false
	}
	for _, c := range t.compiled {
		rule := c.rule
		tools := rule.When.Tools
		if tools == nil {
			tools = []string{"write", "edit"}
		}
		inTools := false
		for _, name := range tools {
			inTools = inTools || name == tool
		}
		// The structured path is checked only when the tool matches when.tools; when it does not (e.g. bash), only sink
		// targets are checked.
		structured := inTools && matches(rule, all)
		sinkCandidates := all
		if !inTools {
			sinkCandidates = nonEmpty(sinkTargets)
		}
		if !structured && !matches(rule, sinkCandidates) {
			continue
		}
		entry := t.armed[rule.ID]
		var paths []string
		if entry != nil {
			paths = entry.armedByPaths
		}
		display := path
		if display == "" {
			display = all[0]
		}
		found := false
		for _, p := range paths {
			found = found || p == display
		}
		if !found {
			paths = append(paths, display)
		}
		if entry == nil {
			t.order = append(t.order, rule.ID)
		}
		t.armed[rule.ID] = &armedEntry{expiresAt: now.Add(c.duration), armedByPaths: paths}
		if !seen[rule.ID] {
			seen[rule.ID] = true
			armedIDs = append(armedIDs, rule.ID)
		}
	}
	t.prune(now)
	return armedIDs
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// CheckArmed returns the armed rules whose command regex matches the command. Expired rules are pruned first, and the
// data-text parse is skipped when nothing is armed (the common case).
func (t *ArmingTracker) CheckArmed(command string) []ArmedHit {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.armed) == 0 {
		return nil
	}
	t.prune(t.now())
	if len(t.armed) == 0 {
		return nil
	}
	text := StripDataText(command).Text
	var hits []ArmedHit
	for _, c := range t.compiled {
		entry := t.armed[c.rule.ID]
		if entry == nil {
			continue
		}
		if c.commandRegex.MatchString(text) {
			hits = append(hits, ArmedHit{ID: c.rule.ID, Action: c.rule.Action, Message: c.rule.Message, ArmedByPaths: append([]string(nil), entry.armedByPaths...)})
		}
	}
	return hits
}

// ArmedRuleIDs are the names of the currently armed rules.
func (t *ArmingTracker) ArmedRuleIDs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(t.now())
	return append([]string(nil), t.order...)
}

// StatusLine is the human-readable status line for `/warden status`; "" when nothing is armed.
func (t *ArmingTracker) StatusLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.prune(now)
	if len(t.order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(t.order))
	for _, id := range t.order {
		remaining := max(0, t.armed[id].expiresAt.Sub(now))
		mins := int(math.Ceil(float64(remaining) / float64(time.Minute)))
		parts = append(parts, fmt.Sprintf("%s (%dm left)", id, mins))
	}
	return fmt.Sprintf("%d armed: %s", len(t.order), strings.Join(parts, ", "))
}

// Reset clears all arming state (session start).
func (t *ArmingTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.armed = map[string]*armedEntry{}
	t.order = nil
}

// prune removes expired entries; the caller holds the lock.
func (t *ArmingTracker) prune(now time.Time) {
	kept := t.order[:0]
	for _, id := range t.order {
		if !t.armed[id].expiresAt.After(now) {
			delete(t.armed, id)
			continue
		}
		kept = append(kept, id)
	}
	t.order = kept
}
