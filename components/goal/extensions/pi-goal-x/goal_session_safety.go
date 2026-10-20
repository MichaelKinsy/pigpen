package pi_goal_x

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// ported from goal-session-safety.ts (module functions and the audit message queue).

// jsToNumber is Number(string) for the decimal, hexadecimal, octal and binary forms and Infinity.
func jsToNumber(s string) float64 {
	t := jsTrim(s)
	if t == "" {
		return 0
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(t) > 2 && t[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[t[1]]
		if base != 0 {
			n, err := strconv.ParseUint(t[2:], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(n)
		}
	}
	for _, r := range t {
		if !(r >= '0' && r <= '9' || r == '.' || r == '+' || r == '-' || r == 'e' || r == 'E') {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil && !strings.Contains(err.Error(), "range") {
		return math.NaN()
	}
	return f
}

// isDelegatedGoalSession: pi-subagents marks fresh, forked, resumed and nested worker processes. A nil env is the process
// environment.
func isDelegatedGoalSession(env map[string]string) bool {
	get := func(k string) (string, bool) {
		if env == nil {
			return os.LookupEnv(k)
		}
		v, ok := env[k]
		return v, ok
	}
	depth := math.NaN()
	if v, ok := get("PI_SUBAGENT_DEPTH"); ok {
		depth = jsToNumber(v)
	}
	child, _ := get("PI_SUBAGENT_CHILD")
	return child == "1" || (!math.IsNaN(depth) && !math.IsInf(depth, 0) && depth > 0)
}

// sessionMessageField reads a key of a message held as *jsObject or map[string]any.
func sessionMessageField(message any, key string) (any, bool) {
	switch m := message.(type) {
	case *jsObject:
		if m == nil {
			return nil, false
		}
		v, ok := m.vals[key]
		return v, ok
	case map[string]any:
		v, ok := m[key]
		return v, ok
	}
	return nil, false
}

// filterGoalSessionContext drops display-only audits (and, for a child session, goal event messages) so they never become
// user messages between a tool and its result. A nil result is the TS null: nothing to remove.
func filterGoalSessionContext(messages []any, child bool) []any {
	var filtered []any
	for i, message := range messages {
		role, _ := sessionMessageField(message, "role")
		custom, _ := sessionMessageField(message, "customType")
		remove := false
		if role == any("custom") {
			if custom == any(goalAuditEntry) {
				remove = true
			} else if child {
				if _, ok := goalEventMessageId(message); ok {
					remove = true
				}
			}
		}
		if remove {
			if filtered == nil {
				filtered = append([]any{}, messages[:i]...)
			}
		} else if filtered != nil {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

// goalSessionContext is the part of ExtensionContext GoalAuditMessages uses.
type goalSessionContext interface {
	// sessionKey is getSessionId() ?? sessionManager: a comparable identity.
	sessionKey() any
	isIdle() bool
}

// goalMessageSender is pi.sendMessage(message, { triggerTurn: false }).
type goalMessageSender interface {
	sendMessage(message any, triggerTurn bool)
}

type goalAuditPending struct {
	session any
	message any
}

// goalAuditMessages holds transcript output until the entire agent run, including tool results, has settled.
type goalAuditMessages struct{ pending []goalAuditPending }

func (g *goalAuditMessages) enqueue(ctx goalSessionContext, message any) {
	g.pending = append(g.pending, goalAuditPending{session: ctx.sessionKey(), message: message})
}

func (g *goalAuditMessages) clear() { g.pending = nil }

func (g *goalAuditMessages) flush(ctx goalSessionContext, pi goalMessageSender) {
	session := ctx.sessionKey()
	kept := g.pending[:0:0]
	for _, item := range g.pending {
		if item.session == session {
			kept = append(kept, item)
		}
	}
	g.pending = kept
	if !ctx.isIdle() {
		return
	}
	pending := g.pending
	g.pending = nil
	for _, item := range pending {
		pi.sendMessage(item.message, false)
	}
}
