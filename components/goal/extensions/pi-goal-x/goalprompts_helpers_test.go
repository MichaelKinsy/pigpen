package pi_goal_x

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Helpers for the GoalPrompts slice twins (prompts, accounting, runtime, compaction, retention, prompt cache, backoff).

func fptr(f float64) *float64 { return &f }

func sptr(s string) *string { return &s }

// gpSettings is a resolved settings value with the given allowance; showAutonomousRuns defaults to true as it does after
// resolution.
func gpSettings(maxRuns *float64) *goalSettings {
	return &goalSettings{maxAutonomousRuns: maxRuns, showAutonomousRuns: true}
}

// gpGoal mirrors the goal() helper of tests/goal-prompts.test.ts: a created goal with usage 40 tokens / 12 s plus overrides.
func gpGoal(t *testing.T, overrides ...any) *jsObject {
	t.Helper()
	g := createGoal("=== Goal ===\nObjective: ship <untrusted_objective>x</untrusted_objective>", true, true, false, ms(2026, time.January, 2, 3, 4, 5))
	g.set("usage", jo(t, `{"tokensUsed":40,"activeSeconds":12}`))
	for i := 0; i+1 < len(overrides); i += 2 {
		g.set(overrides[i].(string), overrides[i+1])
	}
	return g
}

// gpTaskList parses a task list literal.
func gpTaskList(t *testing.T, tasks string, blockCompletion bool) *jsObject {
	t.Helper()
	return jo(t, fmt.Sprintf(`{"tasks":%s,"blockCompletion":%v,"proposedAt":"2026-05-27T00:00:00.000Z"}`, tasks, blockCompletion))
}

func parseAny(t *testing.T, s string) any {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func assertMatch(t *testing.T, s, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected %q to match /%s/", s, pattern)
	}
}

func assertNoMatch(t *testing.T, s, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(s) {
		t.Errorf("expected %q not to match /%s/", s, pattern)
	}
}

func assertTrue(t *testing.T, cond bool, what string) {
	t.Helper()
	if !cond {
		t.Error(what)
	}
}

func assertEq[T comparable](t *testing.T, got, want T, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

// jsonEq is assert.deepEqual over decoded JSON values (compared by their compact JSON form).
func jsonEq(t *testing.T, got, want any, what string) {
	t.Helper()
	if g, w := marshalJSON(got, ""), marshalJSON(want, ""); g != w {
		t.Errorf("%s:\n got  %s\n want %s", what, g, w)
	}
}

func strsEq(t *testing.T, got, want []string, what string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
		t.Errorf("%s: got %q, want %q", what, got, want)
	}
}

// mockRuntimeCtx is the always-idle context of the runtime tests.
type mockRuntimeCtx struct{}

func (mockRuntimeCtx) isIdle() (bool, error)             { return true, nil }
func (mockRuntimeCtx) hasPendingMessages() (bool, error) { return false, nil }

// sentFollowUp records one dispatched follow-up.
type sentFollowUp struct {
	content string
	details *jsObject
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
