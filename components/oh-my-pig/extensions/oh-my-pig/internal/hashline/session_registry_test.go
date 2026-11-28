package hashline

import (
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// Tests for per-session store registry, ForSession(ctx), and lifecycle.End isolation.
func TestSessionStoreLifecycleIsolation(t *testing.T) {
	// Register twice (simulating PiG creating fresh extension instances per session).
	RegisterLifecycle()
	RegisterLifecycle()

	s1 := StoreForSession("sess-1")
	s2 := StoreForSession("sess-2")
	if s1 == s2 {
		t.Fatal("distinct sessions must get distinct stores")
	}

	tag1 := s1.Record("/tmp/f1.go", "package a\n", nil, false)
	tag2 := s2.Record("/tmp/f2.go", "package b\n", nil, false)
	if !s1.HasHashForPath("/tmp/f1.go", tag1) || !s2.HasHashForPath("/tmp/f2.go", tag2) {
		t.Fatal("expected snapshots recorded")
	}

	// Unresolvable / zero Context gets a fresh private store, never shared and never Default().
	privA := ForSession(sdk.Context{})
	privB := ForSession(sdk.Context{})
	if privA == privB || privA == Default() || privB == Default() {
		t.Fatal("unresolvable session id must return a fresh private store")
	}
	privA.Record("/tmp/priv.go", "package p\n", nil, false)
	if _, ok := privB.Head("/tmp/priv.go"); ok {
		t.Fatal("private stores must not share state")
	}
	if _, ok := Default().Head("/tmp/priv.go"); ok {
		t.Fatal("private store must not write into Default()")
	}

	// Empty session ID also returns a fresh private store, never Default().
	emptyA := StoreForSession("")
	emptyB := StoreForSession("")
	if emptyA == emptyB || emptyA == Default() {
		t.Fatal("empty session id must return a fresh private store")
	}

	// End sess-1; sess-2 must remain intact, and calling End twice or with unknown/empty id must be safe.
	lifecycle.End("sess-1")
	lifecycle.End("sess-1")
	lifecycle.End("nonexistent-session")
	lifecycle.End("")

	// Re-fetching sess-1 creates a fresh empty store.
	s1After := StoreForSession("sess-1")
	if s1After.HasHashForPath("/tmp/f1.go", tag1) {
		t.Fatal("ended session store should have been released")
	}
	// sess-2 is completely untouched.
	if !StoreForSession("sess-2").HasHashForPath("/tmp/f2.go", tag2) {
		t.Fatal("other session store must stay intact")
	}

	// Clean up test sessions.
	lifecycle.End("sess-1")
	lifecycle.End("sess-2")
}
