package warden

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Session-scoped pause on a failing judge (src/judge-cooldown.ts): a dead backend costs one notice instead of one
// timeout per action. Nothing is persisted; a new session starts with the judge trusted again.

// JudgeFailureKind is a failed judge request as a category safe to name (never an exception body).
type JudgeFailureKind string

// The failure categories.
const (
	FailureTimeout       JudgeFailureKind = "timeout"
	FailureNetwork       JudgeFailureKind = "network"
	FailureAuth          JudgeFailureKind = "auth"
	FailureConfiguration JudgeFailureKind = "configuration"
	FailureOther         JudgeFailureKind = "other"
)

// JudgeCooldownSettings are the judge section's two numbers (config `judge`).
type JudgeCooldownSettings struct {
	// FailuresBeforeCooldown is the consecutive transient failures that start the cooldown.
	FailuresBeforeCooldown int
	// CooldownMs is how long the judge is left alone once the cooldown starts.
	CooldownMs int
}

// containsAny is true when the lower-cased text holds one of the words (case-insensitive substring tests).
func containsAny(text string, words ...string) bool {
	text = strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// ClassifyJudgeError sorts a judge error into a safe category (spec section 6: never exception bodies).
func ClassifyJudgeError(err error) JudgeFailureKind {
	var integration *IntegrationError
	if errors.As(err, &integration) {
		switch integration.Code {
		case "timeout":
			return FailureTimeout
		case "http", "connection", "response":
			return FailureNetwork
		case "configuration", "validation":
			return FailureConfiguration
		case "budget", "aborted":
			return FailureOther
		}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	switch {
	case containsAny(msg, "timeout", "timed out"):
		return FailureTimeout
	case containsAny(msg, "auth", "key", "credential", "401", "403"):
		return FailureAuth
	case containsAny(msg, "network", "fetch", "connect", "econnrefused", "enotfound"):
		return FailureNetwork
	}
	return FailureOther
}

// CooldownFailureKind says what a failure tells about the backend, or "" when it says nothing.
//
// budget already stops judgments for the session on its own. validation is raised locally for one malformed
// request and says nothing about the backend. aborted is ambiguous: warden's own per-request deadline reaches the
// client as an abort, so a hung backend surfaces as aborted, not timeout. The request context's cause tells the two
// apart; only a user's cancel is left out. signal may be nil.
func CooldownFailureKind(err error, signal context.Context) JudgeFailureKind {
	var integration *IntegrationError
	if errors.As(err, &integration) {
		switch integration.Code {
		case "budget", "validation":
			return ""
		case "aborted":
			if signal != nil && errors.Is(context.Cause(signal), context.DeadlineExceeded) {
				return FailureTimeout
			}
			return ""
		}
		if integration.Status == 401 || integration.Status == 403 {
			return FailureAuth
		}
	}
	return ClassifyJudgeError(err)
}

// CooldownEvent is what the user is told: the window started, or the judge recovered.
type CooldownEvent struct {
	// Type is "started" or "recovered".
	Type string
	Kind JudgeFailureKind
	Ms   int
}

// JudgeCooldown is the pause state.
type JudgeCooldown struct {
	mu       sync.Mutex
	now      func() time.Time
	failures int
	until    time.Time
	// cooled: a window has started and no judgment has succeeded since, so the user was told and not yet told it recovered.
	cooled bool
}

// NewJudgeCooldown reads now on each call, not once, so a test can move the clock. nil uses time.Now.
func NewJudgeCooldown(now func() time.Time) *JudgeCooldown {
	if now == nil {
		now = time.Now
	}
	return &JudgeCooldown{now: now}
}

func (c *JudgeCooldown) activeLocked() bool { return c.now().Before(c.until) }

// Active is true while judgments are paused.
func (c *JudgeCooldown) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeLocked()
}

// Failure counts one failed judgment; it returns the event to tell the user, if any.
func (c *JudgeCooldown) Failure(kind JudgeFailureKind, s JudgeCooldownSettings) *CooldownEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A request already in flight when the window opened adds nothing the user has not been told.
	if c.activeLocked() {
		return nil
	}
	// The probe after a window failed too: the judge is still down, which the open notice already says.
	if c.cooled {
		c.open(s)
		return nil
	}
	c.failures++
	persistent := kind == FailureAuth || kind == FailureConfiguration
	if !persistent && c.failures < s.FailuresBeforeCooldown {
		return nil
	}
	c.open(s)
	return &CooldownEvent{Type: "started", Kind: kind, Ms: s.CooldownMs}
}

// Success resets the count; it announces recovery once, and only after a window.
func (c *JudgeCooldown) Success() *CooldownEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	if !c.cooled {
		return nil
	}
	c.cooled = false
	return &CooldownEvent{Type: "recovered"}
}

// Reset starts the next session with the judge trusted.
func (c *JudgeCooldown) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
	c.until = time.Time{}
	c.cooled = false
}

func (c *JudgeCooldown) open(s JudgeCooldownSettings) {
	c.failures = 0
	c.cooled = true
	c.until = c.now().Add(time.Duration(s.CooldownMs) * time.Millisecond)
}
