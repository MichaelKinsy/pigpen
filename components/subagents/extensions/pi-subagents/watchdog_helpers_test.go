package pi_subagents

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Helpers of the watchdog twins (the original's assert.match, assert.doesNotMatch and its promise-based waits).

func wdMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("%q does not match /%s/", text, pattern)
	}
}

func wdNoMatch(t *testing.T, text, pattern string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("%q matches /%s/", text, pattern)
	}
}

func wdEq(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func wdTrue(t *testing.T, ok bool, what string) {
	t.Helper()
	if !ok {
		t.Errorf("%s: want true", what)
	}
}

// wdEventually polls until cond holds (the original's `await tick()` and its resolved-promise waits).
func wdEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wdAsync runs fn on its own goroutine; the channel closes when it returns (a promise the test awaits later).
func wdAsync(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func wdAwait(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func wdStr(parts ...string) string { return strings.Join(parts, "") }

func wdPtr[T any](v T) *T { return &v }

// wdObj decodes a JSON literal into the ordered dynamic form of jsjson.go (the original's object literals).
func wdObj(t *testing.T, raw string) any {
	t.Helper()
	v, err := parseJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseJSON(%s): %v", raw, err)
	}
	return v
}
