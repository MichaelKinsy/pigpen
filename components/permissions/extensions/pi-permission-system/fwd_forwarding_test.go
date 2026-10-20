package pi_permission_system

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Twins of test/authority/permission-forwarding.test.ts, serving-registry.test.ts and subagent-registry.test.ts.

func fwMakeSubagentRegistry(childSessionID string, entry *SubagentSessionInfo) *SubagentSessionRegistry {
	registry := NewSubagentSessionRegistry()
	if entry != nil {
		registry.Register(childSessionID, *entry)
	}
	return registry
}

func fwTarget(sessionID string, source PermissionForwardingTargetSource) *PermissionForwardingTarget {
	return &PermissionForwardingTarget{SessionID: sessionID, Source: source}
}

func fwResolve(o ResolveTargetOptions) *PermissionForwardingTarget {
	target, ok := ResolvePermissionForwardingTarget(o)
	if !ok {
		return nil
	}
	return &target
}

func TestSubagentParentSessionEnvCandidates(t *testing.T) {
	const f = "authority/permission-forwarding"
	tw(t, f, "is an array containing PI_AGENT_ROUTER_PARENT_SESSION_ID", func(t *testing.T) {
		if !inStrings(SubagentParentSessionEnvCandidates, "PI_AGENT_ROUTER_PARENT_SESSION_ID") {
			t.Fatal("missing")
		}
	})
	tw(t, f, "contains PI_SUBAGENT_PARENT_SESSION for CLI-based subagent extensions", func(t *testing.T) {
		if !inStrings(SubagentParentSessionEnvCandidates, "PI_SUBAGENT_PARENT_SESSION") {
			t.Fatal("missing")
		}
	})
	tw(t, f, "deprecated SUBAGENT_PARENT_SESSION_ENV_KEY equals the first candidate", func(t *testing.T) {
		if SubagentParentSessionEnvKey != SubagentParentSessionEnvCandidates[0] {
			t.Fatal(SubagentParentSessionEnvKey)
		}
	})
}

func TestResolvePermissionForwardingTarget(t *testing.T) {
	const f = "authority/permission-forwarding"
	env := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	check := func(t *testing.T, got, want *PermissionForwardingTarget) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
	tw(t, f, "isSubagent=false returns null", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: false, CurrentSessionID: "session-xyz", Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "parent-session-abc")}), nil)
	})
	tw(t, f, "isSubagent=true, no candidates set returns null", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env()}), nil)
	})
	tw(t, f, "isSubagent=true, PI_AGENT_ROUTER_PARENT_SESSION_ID set returns its value", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "parent-session-abc")}),
			fwTarget("parent-session-abc", TargetSourceEnv))
	})
	tw(t, f, "isSubagent=true, PI_SUBAGENT_PARENT_SESSION resolves when PI_AGENT_ROUTER_PARENT_SESSION_ID is absent", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env("PI_SUBAGENT_PARENT_SESSION", "parent-from-convention")}),
			fwTarget("parent-from-convention", TargetSourceEnv))
	})
	tw(t, f, "isSubagent=true, PI_AGENT_ROUTER_PARENT_SESSION_ID takes precedence over PI_SUBAGENT_PARENT_SESSION", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz",
			Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "parent-from-router", "PI_SUBAGENT_PARENT_SESSION", "parent-from-convention")}),
			fwTarget("parent-from-router", TargetSourceEnv))
	})
	tw(t, f, "isSubagent=true, candidate value is empty string returns null", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "")}), nil)
	})
	tw(t, f, "isSubagent=true, candidate value is 'unknown' returns null", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "unknown")}), nil)
	})
	// A request written into your own inbox is drained by a watcher you are not running, so it can never be answered. A spawner's
	// marker is overwritten with the child's own id when the child's own copy of that extension does not recognize it as a child (#907).
	tw(t, f, "skips a candidate naming the requesting session and takes the next", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz",
			Env: env("PI_AGENT_ROUTER_PARENT_SESSION_ID", "session-xyz", "PI_SUBAGENT_PARENT_SESSION", "parent-from-convention")}),
			fwTarget("parent-from-convention", TargetSourceEnv))
	})
	tw(t, f, "returns null when the only candidate names the requesting session", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "session-xyz", Env: env("PI_SUBAGENT_PARENT_SESSION", "session-xyz")}), nil)
	})
	tw(t, f, "skips a self-naming candidate that is whitespace-padded", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: " session-xyz ", Env: env("PI_SUBAGENT_PARENT_SESSION", "  session-xyz  ")}), nil)
	})
	tw(t, f, "env defaults to process.env when omitted", func(t *testing.T) {
		t.Setenv("PI_AGENT_ROUTER_PARENT_SESSION_ID", "env-session-abc")
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true}), fwTarget("env-session-abc", TargetSourceEnv))
	})
}

func TestResolvePermissionForwardingTargetRegistry(t *testing.T) {
	const f = "authority/permission-forwarding"
	const childSessionID = "child-session-abc"
	parentOf := func(p string) *SubagentSessionInfo { return &SubagentSessionInfo{ParentSessionID: &p} }
	envRouter := map[string]string{"PI_AGENT_ROUTER_PARENT_SESSION_ID": "parent-from-env"}
	check := func(t *testing.T, got, want *PermissionForwardingTarget) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
	tw(t, f, "returns parentSessionId from registry when env vars are absent", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Registry: fwMakeSubagentRegistry(childSessionID, parentOf("parent-from-registry")), Env: map[string]string{}}),
			fwTarget("parent-from-registry", TargetSourceRegistry))
	})
	tw(t, f, "registry takes priority over env vars", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Registry: fwMakeSubagentRegistry(childSessionID, parentOf("parent-from-registry")), Env: envRouter}),
			fwTarget("parent-from-registry", TargetSourceRegistry))
	})
	tw(t, f, "falls through to env vars when the registry entry names the requesting session", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: childSessionID, SessionID: childSessionID,
			Registry: fwMakeSubagentRegistry(childSessionID, parentOf(childSessionID)), Env: envRouter}),
			fwTarget("parent-from-env", TargetSourceEnv))
	})
	tw(t, f, "falls through to env vars when registry entry has no parentSessionId", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Registry: fwMakeSubagentRegistry(childSessionID, &SubagentSessionInfo{}), Env: envRouter}),
			fwTarget("parent-from-env", TargetSourceEnv))
	})
	tw(t, f, "falls through to env vars when sessionId is not in registry", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Registry: fwMakeSubagentRegistry(childSessionID, nil), Env: envRouter}),
			fwTarget("parent-from-env", TargetSourceEnv))
	})
	tw(t, f, "returns null when registry entry has no parentSessionId and no env vars set", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Registry: fwMakeSubagentRegistry(childSessionID, &SubagentSessionInfo{}), Env: map[string]string{}}), nil)
	})
	tw(t, f, "omitting registry preserves existing behaviour", func(t *testing.T) {
		check(t, fwResolve(ResolveTargetOptions{IsSubagent: true, SessionID: childSessionID, Env: envRouter}), fwTarget("parent-from-env", TargetSourceEnv))
	})
}

// Moved from permission-system.test.ts catch-all (#342).
func TestPermissionForwardingCatchAll(t *testing.T) {
	const f = "authority/permission-forwarding"
	tw(t, f, "Permission forwarding resolves the parent interactive session from subagent runtime env", func(t *testing.T) {
		target := fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "child-session", Env: map[string]string{"PI_AGENT_ROUTER_PARENT_SESSION_ID": "parent-session"}})
		if !reflect.DeepEqual(target, fwTarget("parent-session", TargetSourceEnv)) {
			t.Fatalf("%+v", target)
		}
	})
	tw(t, f, "Permission forwarding does not guess a target session when subagent runtime env is missing", func(t *testing.T) {
		if target := fwResolve(ResolveTargetOptions{IsSubagent: true, CurrentSessionID: "child-session", Env: map[string]string{}}); target != nil {
			t.Fatalf("%+v", target)
		}
	})
	tw(t, f, "Permission forwarding uses session-scoped directories per interactive session", func(t *testing.T) {
		forwardingRoot := filepath.Join(os.TempDir(), "pi-permission-system-forwarding-root")
		a, _ := CreatePermissionForwardingLocation(forwardingRoot, "session-a")
		b, _ := CreatePermissionForwardingLocation(forwardingRoot, "session-b")
		if a.SessionRootDir == b.SessionRootDir || a.RequestsDir == b.RequestsDir || a.ResponsesDir == b.ResponsesDir {
			t.Fatal("sessions share a directory")
		}
	})
	tw(t, f, "Permission forwarding request routing only matches the intended UI session", func(t *testing.T) {
		if !IsForwardedPermissionRequestForSession("session-a", "session-a") {
			t.Fatal("same session rejected")
		}
		if IsForwardedPermissionRequestForSession("session-a", "session-b") {
			t.Fatal("other session accepted")
		}
	})
	tw(t, f, "Permission forwarding rejects unresolved sentinel session ids", func(t *testing.T) {
		if target := fwResolve(ResolveTargetOptions{IsSubagent: false, CurrentSessionID: "unknown"}); target != nil {
			t.Fatalf("%+v", target)
		}
	})
}

// Go-specific: the location layout, session id encoding and the error for an unusable id, which the wire layout depends on.
func TestPermissionForwardingLocationLayout(t *testing.T) {
	loc, err := CreatePermissionForwardingLocation(filepath.Join("root", "fwd"), " a/b c ")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("root", "fwd", "sessions", "a%2Fb%20c")
	if loc.SessionID != "a/b c" || loc.SessionRootDir != want || loc.RequestsDir != filepath.Join(want, "requests") || loc.ResponsesDir != filepath.Join(want, "responses") || loc.Label != "primary" {
		t.Fatalf("%+v", loc)
	}
	for _, bad := range []string{"", "   ", "unknown", " UNKNOWN "} {
		if _, err := CreatePermissionForwardingLocation("root", bad); err == nil || err.Error() != "Permission forwarding session id must be a non-empty string." {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	for in, out := range map[string]string{"é": "%C3%A9", "😀": "%F0%9F%98%80", "A-_.!~*'()": "A-_.!~*'()", "a b&c=d": "a%20b%26c%3Dd"} {
		if got := EncodeSessionIDForPath(in); got != out {
			t.Fatalf("%q: %q want %q", in, got, out)
		}
	}
}

// ── serving-registry ───────────────────────────────────────────────────────

// fwClearServingRegistrySlot drops the global slot between tests (the original deletes the globalThis symbol).
func fwClearServingRegistrySlot() {
	servingRegistryMu.Lock()
	servingRegistrySlot = nil
	servingRegistryMu.Unlock()
}

func TestServingSessionRegistry(t *testing.T) {
	const f = "authority/serving-registry"
	t.Cleanup(fwClearServingRegistrySlot)
	tw(t, f, "reports an unmarked session as not serving", func(t *testing.T) {
		if NewServingSessionRegistry().IsServing("sess-1") {
			t.Fatal("serving")
		}
	})
	tw(t, f, "reports a marked session as serving", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.MarkServing("sess-1")
		if !r.IsServing("sess-1") {
			t.Fatal("not serving")
		}
	})
	tw(t, f, "does not report a sibling session as serving", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.MarkServing("sess-1")
		if r.IsServing("sess-2") {
			t.Fatal("sibling serving")
		}
	})
	tw(t, f, "is idempotent", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.MarkServing("sess-1")
		r.MarkServing("sess-1")
		if !reflect.DeepEqual(r.ServingIDs(), []string{"sess-1"}) {
			t.Fatal(r.ServingIDs())
		}
	})
	tw(t, f, "keeps concurrent sessions independent", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.MarkServing("sess-1")
		r.MarkServing("sess-2")
		r.ClearServing("sess-1")
		if !reflect.DeepEqual(r.ServingIDs(), []string{"sess-2"}) {
			t.Fatal(r.ServingIDs())
		}
	})
	tw(t, f, "stops reporting the session as serving", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.MarkServing("sess-1")
		r.ClearServing("sess-1")
		if r.IsServing("sess-1") {
			t.Fatal("serving")
		}
	})
	tw(t, f, "is a no-op for an unmarked session", func(t *testing.T) {
		r := NewServingSessionRegistry()
		r.ClearServing("sess-1")
		if len(r.ServingIDs()) != 0 {
			t.Fatal(r.ServingIDs())
		}
	})
	tw(t, f, "is empty for a fresh registry", func(t *testing.T) {
		if !reflect.DeepEqual(NewServingSessionRegistry().ServingIDs(), []string{}) {
			t.Fatal("not empty")
		}
	})
	// Go-specific: Set iteration order, which the diagnostic entry lists.
	t.Run("lists ids in insertion order and keeps a re-marked id in place", func(t *testing.T) {
		r := NewServingSessionRegistry()
		for _, id := range []string{"b", "a", "c", "a"} {
			r.MarkServing(id)
		}
		if !reflect.DeepEqual(r.ServingIDs(), []string{"b", "a", "c"}) {
			t.Fatal(r.ServingIDs())
		}
	})
}

func TestComposeServingAnnouncers(t *testing.T) {
	const f = "authority/serving-registry"
	tw(t, f, "marks on every channel a serving session publishes to", func(t *testing.T) {
		first, second := &fwAnnouncer{}, &fwAnnouncer{}
		ComposeServingAnnouncers(first, second).MarkServing("sess-1")
		if !reflect.DeepEqual(first.markList(), []string{"sess-1"}) || !reflect.DeepEqual(second.markList(), []string{"sess-1"}) {
			t.Fatal("not marked on both")
		}
	})
	tw(t, f, "clears on every channel", func(t *testing.T) {
		first, second := &fwAnnouncer{}, &fwAnnouncer{}
		ComposeServingAnnouncers(first, second).ClearServing("sess-1")
		if !reflect.DeepEqual(first.clearList(), []string{"sess-1"}) || !reflect.DeepEqual(second.clearList(), []string{"sess-1"}) {
			t.Fatal("not cleared on both")
		}
	})
	tw(t, f, "is a no-op with no channels", func(t *testing.T) {
		ComposeServingAnnouncers().MarkServing("sess-1")
	})
}

func TestGetServingSessionRegistry(t *testing.T) {
	const f = "authority/serving-registry"
	t.Cleanup(fwClearServingRegistrySlot)
	tw(t, f, "returns the same process-global instance on repeated calls", func(t *testing.T) {
		fwClearServingRegistrySlot()
		if GetServingSessionRegistry() != GetServingSessionRegistry() {
			t.Fatal("different instances")
		}
	})
	tw(t, f, "shares marks across callers, as separate jiti instances require", func(t *testing.T) {
		fwClearServingRegistrySlot()
		GetServingSessionRegistry().MarkServing("parent-session")
		if !GetServingSessionRegistry().IsServing("parent-session") {
			t.Fatal("mark lost")
		}
	})
	tw(t, f, "creates a fresh registry once the global slot is cleared", func(t *testing.T) {
		fwClearServingRegistrySlot()
		first := GetServingSessionRegistry()
		first.MarkServing("parent-session")
		fwClearServingRegistrySlot()
		if GetServingSessionRegistry() == first || GetServingSessionRegistry().IsServing("parent-session") {
			t.Fatal("slot not cleared")
		}
	})
}

// ── subagent-registry ──────────────────────────────────────────────────────

func TestSubagentSessionRegistry(t *testing.T) {
	const f = "authority/subagent-registry"
	parentInfo := func(p string) SubagentSessionInfo { return SubagentSessionInfo{ParentSessionID: &p} }
	tw(t, f, "has() returns false for an unregistered key", func(t *testing.T) {
		if NewSubagentSessionRegistry().Has("session-abc") {
			t.Fatal("has")
		}
	})
	tw(t, f, "get() returns undefined for an unregistered key", func(t *testing.T) {
		if _, ok := NewSubagentSessionRegistry().Get("session-abc"); ok {
			t.Fatal("found")
		}
	})
	tw(t, f, "has() returns true after register()", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("session-abc", SubagentSessionInfo{})
		if !r.Has("session-abc") {
			t.Fatal("not registered")
		}
	})
	tw(t, f, "get() returns the registered info after register()", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		info := parentInfo("parent-123")
		r.Register("session-abc", info)
		if got, ok := r.Get("session-abc"); !ok || !reflect.DeepEqual(got, info) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "register() stores entry without parentSessionId", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("session-abc", SubagentSessionInfo{})
		if got, ok := r.Get("session-abc"); !ok || got.ParentSessionID != nil {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "has() returns false after unregister()", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("session-abc", SubagentSessionInfo{})
		r.Unregister("session-abc")
		if r.Has("session-abc") {
			t.Fatal("still registered")
		}
	})
	tw(t, f, "get() returns undefined after unregister()", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("session-abc", SubagentSessionInfo{})
		r.Unregister("session-abc")
		if _, ok := r.Get("session-abc"); ok {
			t.Fatal("found")
		}
	})
	tw(t, f, "unregister() is a no-op for an unknown key", func(t *testing.T) {
		NewSubagentSessionRegistry().Unregister("session-nonexistent")
	})
	tw(t, f, "register() overwrites a previous entry for the same key", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("session-abc", parentInfo("parent-1"))
		r.Register("session-abc", parentInfo("parent-2"))
		if got, _ := r.Get("session-abc"); got.ParentSessionID == nil || *got.ParentSessionID != "parent-2" {
			t.Fatalf("%+v", got)
		}
	})
	// #298 regression: concurrent siblings must be independent.
	tw(t, f, "two sibling session ids are registered independently", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("child-session-A", parentInfo("parent-P"))
		r.Register("child-session-B", parentInfo("parent-P"))
		if !r.Has("child-session-A") || !r.Has("child-session-B") {
			t.Fatal("sibling missing")
		}
	})
	tw(t, f, "disposing one sibling does not evict the other (collision regression)", func(t *testing.T) {
		r := NewSubagentSessionRegistry()
		r.Register("child-session-A", parentInfo("parent-P"))
		r.Register("child-session-B", parentInfo("parent-P"))
		// Sibling A finishes: should not affect B.
		r.Unregister("child-session-A")
		got, _ := r.Get("child-session-B")
		if r.Has("child-session-A") || !r.Has("child-session-B") || got.ParentSessionID == nil || *got.ParentSessionID != "parent-P" {
			t.Fatal("sibling evicted")
		}
	})
}

func fwClearSubagentRegistrySlot() {
	subagentRegistryMu.Lock()
	subagentRegistrySlot = nil
	subagentRegistryMu.Unlock()
}

func TestGetSubagentSessionRegistry(t *testing.T) {
	const f = "authority/subagent-registry"
	t.Cleanup(fwClearSubagentRegistrySlot)
	tw(t, f, "returns a SubagentSessionRegistry instance", func(t *testing.T) {
		fwClearSubagentRegistrySlot()
		if GetSubagentSessionRegistry() == nil {
			t.Fatal("nil")
		}
	})
	tw(t, f, "returns the same instance on repeated calls", func(t *testing.T) {
		fwClearSubagentRegistrySlot()
		if GetSubagentSessionRegistry() != GetSubagentSessionRegistry() {
			t.Fatal("different")
		}
	})
	tw(t, f, "state registered through one call is visible through another call", func(t *testing.T) {
		fwClearSubagentRegistrySlot()
		GetSubagentSessionRegistry().Register("child-session-xyz", SubagentSessionInfo{ParentSessionID: fwSP("parent-abc")})
		got, ok := GetSubagentSessionRegistry().Get("child-session-xyz")
		if !ok || got.ParentSessionID == nil || *got.ParentSessionID != "parent-abc" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "starts empty on first call", func(t *testing.T) {
		fwClearSubagentRegistrySlot()
		if GetSubagentSessionRegistry().Has("any-session-id") {
			t.Fatal("not empty")
		}
	})
}

// Go-specific: the timing the original declares as constants but its tests only compare with each other.
func TestPermissionForwardingTimingConstants(t *testing.T) {
	if PermissionForwardingPollIntervalMs != 250 || PermissionForwardingTimeoutMs != 600_000 || PermissionForwardingServingGraceMs != 2_000 {
		t.Fatalf("poll %d timeout %d grace %d", PermissionForwardingPollIntervalMs, PermissionForwardingTimeoutMs, PermissionForwardingServingGraceMs)
	}
	if ServingHeartbeatRefreshMs != 1_000 || ServingHeartbeatStaleMs != 5_000 {
		t.Fatalf("refresh %d stale %d", ServingHeartbeatRefreshMs, ServingHeartbeatStaleMs)
	}
	if !reflect.DeepEqual(SubagentParentSessionEnvCandidates, []string{"PI_AGENT_ROUTER_PARENT_SESSION_ID", "PI_SUBAGENT_PARENT_SESSION"}) {
		t.Fatal(SubagentParentSessionEnvCandidates)
	}
}
