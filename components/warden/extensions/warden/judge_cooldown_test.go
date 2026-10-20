package warden

import (
	"context"
	"errors"
	"testing"
	"time"
)

var cooldownSettings = JudgeCooldownSettings{FailuresBeforeCooldown: 3, CooldownMs: 1000}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }
func newFakeClock() *fakeClock               { return &fakeClock{now: time.Unix(0, 0)} }

// twin: tests/judge-cooldown.test.ts:13
func TestCooldownTransientFailuresStartAtThreshold(t *testing.T) {
	clock := newFakeClock()
	c := NewJudgeCooldown(clock.Now)
	if c.Failure(FailureTimeout, cooldownSettings) != nil || c.Failure(FailureNetwork, cooldownSettings) != nil {
		t.Fatal("below the threshold")
	}
	if c.Active() {
		t.Fatal("active early")
	}
	ev := c.Failure(FailureOther, cooldownSettings)
	if ev == nil || *ev != (CooldownEvent{Type: "started", Kind: FailureOther, Ms: 1000}) {
		t.Fatalf("event %+v", ev)
	}
	if !c.Active() {
		t.Fatal("not active")
	}
	clock.advance(1000 * time.Millisecond)
	if c.Active() {
		t.Fatal("the window closes after cooldownMs")
	}
}

// twin: tests/judge-cooldown.test.ts:25
func TestCooldownAuthAndConfigurationStartAtOnce(t *testing.T) {
	for _, kind := range []JudgeFailureKind{FailureAuth, FailureConfiguration} {
		c := NewJudgeCooldown(newFakeClock().Now)
		ev := c.Failure(kind, cooldownSettings)
		if ev == nil || *ev != (CooldownEvent{Type: "started", Kind: kind, Ms: 1000}) {
			t.Fatalf("%s: %+v", kind, ev)
		}
	}
}

// twin: tests/judge-cooldown.test.ts:32
func TestCooldownSuccessResetsCount(t *testing.T) {
	c := NewJudgeCooldown(newFakeClock().Now)
	c.Failure(FailureTimeout, cooldownSettings)
	c.Failure(FailureTimeout, cooldownSettings)
	if c.Success() != nil {
		t.Fatal("nothing to announce: the judge was never paused")
	}
	c.Failure(FailureTimeout, cooldownSettings)
	c.Failure(FailureTimeout, cooldownSettings)
	if c.Active() {
		t.Fatal("the count restarted")
	}
}

// twin: tests/judge-cooldown.test.ts:42
func TestCooldownFailuresInWindowAndFailedProbeAddNoNotice(t *testing.T) {
	clock := newFakeClock()
	c := NewJudgeCooldown(clock.Now)
	c.Failure(FailureAuth, cooldownSettings)
	if c.Failure(FailureTimeout, cooldownSettings) != nil {
		t.Fatal("an in-flight request finishing late")
	}
	clock.advance(1000 * time.Millisecond)
	if c.Failure(FailureTimeout, cooldownSettings) != nil {
		t.Fatal("the probe failed; the user was already told")
	}
	if !c.Active() {
		t.Fatal("and the window is open again")
	}
	clock.advance(1000 * time.Millisecond)
	if ev := c.Success(); ev == nil || ev.Type != "recovered" {
		t.Fatalf("event %+v", ev)
	}
	if c.Success() != nil {
		t.Fatal("recovery is announced once")
	}
}

// twin: tests/judge-cooldown.test.ts:55
func TestCooldownDeadlineIsTimeoutCancelBudgetValidationDoNotCount(t *testing.T) {
	aborted := &IntegrationError{Code: "aborted", Message: "cancelled"}
	deadline, cancelDeadline := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelDeadline()
	<-deadline.Done()
	cancel, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if got := CooldownFailureKind(aborted, deadline); got != FailureTimeout {
		t.Fatalf("deadline: %q", got)
	}
	if got := CooldownFailureKind(aborted, cancel); got != "" {
		t.Fatalf("cancel: %q", got)
	}
	if got := CooldownFailureKind(aborted, nil); got != "" {
		t.Fatalf("no signal: %q", got)
	}
	if got := CooldownFailureKind(&IntegrationError{Code: "budget", Message: "limit"}, nil); got != "" {
		t.Fatalf("budget: %q", got)
	}
	if got := CooldownFailureKind(&IntegrationError{Code: "validation", Message: "too many questions"}, nil); got != "" {
		t.Fatalf("validation: %q", got)
	}
}

// twin: tests/judge-cooldown.test.ts:68
func TestCooldown401And403AreAuthOtherHTTPIsNetwork(t *testing.T) {
	cases := []struct {
		err  *IntegrationError
		want JudgeFailureKind
	}{
		{&IntegrationError{Code: "http", Message: "denied", Status: 401}, FailureAuth},
		{&IntegrationError{Code: "http", Message: "denied", Status: 403}, FailureAuth},
		{&IntegrationError{Code: "http", Message: "unavailable", Status: 503}, FailureNetwork},
		{&IntegrationError{Code: "timeout", Message: "slow"}, FailureTimeout},
	}
	for _, c := range cases {
		if got := CooldownFailureKind(c.err, nil); got != c.want {
			t.Errorf("%+v: got %q want %q", c.err, got, c.want)
		}
	}
}

func TestCooldownAFailureInFlightDoesNotExtendTheWindow(t *testing.T) {
	clock := newFakeClock()
	c := NewJudgeCooldown(clock.Now)
	c.Failure(FailureAuth, cooldownSettings)
	clock.advance(600 * time.Millisecond)
	if c.Failure(FailureTimeout, cooldownSettings) != nil {
		t.Fatal("no second notice")
	}
	clock.advance(400 * time.Millisecond)
	if c.Active() {
		t.Fatal("the late failure did not reopen the window: it ended one second after it began")
	}
}

func TestClassifyJudgeErrorByCodeAndByMessage(t *testing.T) {
	cases := []struct {
		err  error
		want JudgeFailureKind
	}{
		{&IntegrationError{Code: "timeout"}, FailureTimeout},
		{&IntegrationError{Code: "http"}, FailureNetwork},
		{&IntegrationError{Code: "connection"}, FailureNetwork},
		{&IntegrationError{Code: "response"}, FailureNetwork},
		{&IntegrationError{Code: "configuration"}, FailureConfiguration},
		{&IntegrationError{Code: "validation"}, FailureConfiguration},
		{&IntegrationError{Code: "budget"}, FailureOther},
		{&IntegrationError{Code: "aborted"}, FailureOther},
		{errors.New("request TIMED OUT"), FailureTimeout},
		{errors.New("a Timeout happened"), FailureTimeout},
		{errors.New("bad credential"), FailureAuth},
		{errors.New("HTTP 403"), FailureAuth},
		{errors.New("no api key"), FailureAuth},
		{errors.New("ECONNREFUSED 127.0.0.1"), FailureNetwork},
		{errors.New("fetch failed"), FailureNetwork},
		{errors.New("could not connect"), FailureNetwork},
		{errors.New("getaddrinfo ENOTFOUND host"), FailureNetwork},
		{errors.New("network down"), FailureNetwork},
		{errors.New("something else"), FailureOther},
		{nil, FailureOther},
	}
	for _, c := range cases {
		if got := ClassifyJudgeError(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}
