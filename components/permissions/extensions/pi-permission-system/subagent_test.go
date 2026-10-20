package pi_permission_system

import (
	"errors"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Twins of test/authority/subagent-context.test.ts, subagent-detection.test.ts, subagent-lifecycle-events.test.ts,
// inherited-registrations.test.ts, child-node-audit.test.ts and delegation-envelope.test.ts.

// fwFlavor stands in for the paths lane's posixPathFlavor and win32PathFlavor (src/path/path-flavor.ts), which implement the real
// Node path semantics; it models only what these twins feed it: separators, `.` and `..` segments, a drive prefix, case folding.
// At integration the twins run against the paths lane's PathFlavor, which satisfies SubagentPathFlavor.
type fwFlavor struct{ win bool }

var (
	fwPosix = fwFlavor{}
	fwWin32 = fwFlavor{win: true}
)

func (f fwFlavor) sep() string {
	if f.win {
		return `\`
	}
	return "/"
}

func (f fwFlavor) Normalize(p string) string {
	if !f.win {
		return path.Clean(p)
	}
	p = strings.ReplaceAll(p, "/", `\`)
	drive := ""
	if len(p) >= 2 && p[1] == ':' {
		drive, p = p[:2], p[2:]
	}
	var segs []string
	for _, s := range strings.Split(p, `\`) {
		switch s {
		case "", ".":
		case "..":
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
		default:
			segs = append(segs, s)
		}
	}
	out := drive
	if strings.HasPrefix(p, `\`) {
		out += `\`
	}
	return out + strings.Join(segs, `\`)
}

func (f fwFlavor) Fold(v string) string {
	if f.win {
		return strings.ToLower(v)
	}
	return v
}

func (f fwFlavor) IsWithin(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, strings.TrimRight(dir, f.sep())+f.sep())
}

func fwDetectionCtx(sessionDir, sessionID string) SubagentDetectionContext {
	return SubagentDetectionContext{SessionManager: &fwSessionManager{sessionID: fwID(sessionID), sessionDir: fwID(sessionDir)}}
}

func fwIsSubagent(t *testing.T, ctx SubagentDetectionContext, root string, flavor SubagentPathFlavor, registry *SubagentSessionRegistry) bool {
	t.Helper()
	got, err := IsSubagentExecutionContext(ctx, root, flavor, registry)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestIsRegisteredSubagentChild(t *testing.T) {
	const f = "authority/subagent-context"
	const childSessionID = "child-session-abc"
	tw(t, f, "returns true when the session id is registered", func(t *testing.T) {
		fwClearHintEnv(t)
		registry := NewSubagentSessionRegistry()
		registry.Register(childSessionID, SubagentSessionInfo{})
		if !IsRegisteredSubagentChild(fwDetectionCtx("", childSessionID), registry) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when the session id is not registered", func(t *testing.T) {
		if IsRegisteredSubagentChild(fwDetectionCtx("", childSessionID), NewSubagentSessionRegistry()) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when the session id is empty", func(t *testing.T) {
		registry := NewSubagentSessionRegistry()
		registry.Register("", SubagentSessionInfo{})
		if IsRegisteredSubagentChild(fwDetectionCtx("", ""), registry) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when getSessionId throws", func(t *testing.T) {
		registry := NewSubagentSessionRegistry()
		registry.Register(childSessionID, SubagentSessionInfo{})
		ctx := SubagentDetectionContext{SessionManager: &fwSessionManager{
			sessionDir: fwID(""),
			sessionID:  func() (string, error) { return "", errors.New("session id unavailable") },
		}}
		if IsRegisteredSubagentChild(ctx, registry) {
			t.Fatal("true")
		}
	})
}

func TestNormalizeFilesystemPath(t *testing.T) {
	const f = "authority/subagent-context"
	tw(t, f, "normalizes a simple absolute path", func(t *testing.T) {
		if got := NormalizeFilesystemPath("/projects/my-app", fwPosix); got != "/projects/my-app" {
			t.Fatal(got)
		}
	})
	tw(t, f, "collapses redundant separators", func(t *testing.T) {
		if got := NormalizeFilesystemPath("/projects//my-app", fwPosix); got != "/projects/my-app" {
			t.Fatal(got)
		}
	})
	tw(t, f, "resolves . and .. segments", func(t *testing.T) {
		if got := NormalizeFilesystemPath("/projects/my-app/../other", fwPosix); got != "/projects/other" {
			t.Fatal(got)
		}
	})
	tw(t, f, "win32: lowercases and normalizes with win32 separators", func(t *testing.T) {
		if got := NormalizeFilesystemPath(`C:\Projects\My-App`, fwWin32); got != `c:\projects\my-app` {
			t.Fatal(got)
		}
	})
	tw(t, f, "posix: leaves case untouched", func(t *testing.T) {
		if got := NormalizeFilesystemPath("/Projects/My-App", fwPosix); got != "/Projects/My-App" {
			t.Fatal(got)
		}
	})
}

func TestIsSubagentExecutionContextInjectedPlatform(t *testing.T) {
	const f = "authority/subagent-context"
	tw(t, f, "win32: detects a subagent session dir case-insensitively", func(t *testing.T) {
		fwClearHintEnv(t)
		if !fwIsSubagent(t, fwDetectionCtx(`c:\sessions\subagents\child`, ""), `C:\Sessions\Subagents`, fwWin32, nil) {
			t.Fatal("false")
		}
	})
	tw(t, f, "posix: the same mixed-case dir is not a subagent context", func(t *testing.T) {
		fwClearHintEnv(t)
		if fwIsSubagent(t, fwDetectionCtx("/sessions/subagents/child", ""), "/Sessions/Subagents", fwPosix, nil) {
			t.Fatal("true")
		}
	})
}

func TestIsSubagentExecutionContextEnvHints(t *testing.T) {
	const f = "authority/subagent-context"
	hint := func(t *testing.T, key, value string) {
		t.Helper()
		fwClearHintEnv(t)
		t.Setenv(key, value)
		if !fwIsSubagent(t, fwDetectionCtx("", ""), "/sessions/subagents", fwPosix, nil) {
			t.Fatalf("%s=%q not detected", key, value)
		}
	}
	tw(t, f, "returns true when PI_IS_SUBAGENT is set", func(t *testing.T) { hint(t, "PI_IS_SUBAGENT", "true") })
	tw(t, f, "returns true when PI_SUBAGENT_SESSION_ID is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_SESSION_ID", "abc123") })
	tw(t, f, "returns true when PI_AGENT_ROUTER_SUBAGENT is set", func(t *testing.T) { hint(t, "PI_AGENT_ROUTER_SUBAGENT", "1") })
	// nicobailon/pi-subagents keys
	tw(t, f, "returns true when PI_SUBAGENT_CHILD is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_CHILD", "1") })
	tw(t, f, "returns true when PI_SUBAGENT_RUN_ID is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_RUN_ID", "run-abc") })
	tw(t, f, "returns true when PI_SUBAGENT_CHILD_AGENT is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_CHILD_AGENT", "worker") })
	tw(t, f, "returns true when PI_SUBAGENT_DEPTH is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_DEPTH", "1") })
	tw(t, f, "returns true when PI_SUBAGENT_DEPTH is zero (depth-0 is still a subagent context)", func(t *testing.T) { hint(t, "PI_SUBAGENT_DEPTH", "0") })
	// HazAT/pi-interactive-subagents keys
	tw(t, f, "returns true when PI_SUBAGENT_NAME is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_NAME", "my-agent") })
	tw(t, f, "returns true when PI_SUBAGENT_ID is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_ID", "id-xyz") })
	tw(t, f, "returns true when PI_SUBAGENT_SESSION is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_SESSION", "session-xyz") })
	tw(t, f, "returns true when PI_SUBAGENT_ACTIVITY_FILE is set", func(t *testing.T) { hint(t, "PI_SUBAGENT_ACTIVITY_FILE", "/tmp/activity.json") })
	// The adapter convention's parent-session variables (ADR 0012 decision 5): a process that names a parent session is a child by
	// definition, so naming one is sufficient on its own.
	tw(t, f, "returns true when PI_SUBAGENT_PARENT_SESSION is set alone", func(t *testing.T) { hint(t, "PI_SUBAGENT_PARENT_SESSION", "parent-session-id") })
	tw(t, f, "returns true when PI_AGENT_ROUTER_PARENT_SESSION_ID is set alone", func(t *testing.T) { hint(t, "PI_AGENT_ROUTER_PARENT_SESSION_ID", "parent-session-id") })
	tw(t, f, "covers all declared SUBAGENT_ENV_HINT_KEYS", func(t *testing.T) {
		for _, key := range []string{
			"PI_IS_SUBAGENT", "PI_SUBAGENT_SESSION_ID", "PI_AGENT_ROUTER_SUBAGENT",
			"PI_SUBAGENT_CHILD", "PI_SUBAGENT_RUN_ID", "PI_SUBAGENT_CHILD_AGENT", "PI_SUBAGENT_DEPTH",
			"PI_SUBAGENT_NAME", "PI_SUBAGENT_ID", "PI_SUBAGENT_SESSION", "PI_SUBAGENT_ACTIVITY_FILE",
			// The parent-session candidates are hints too.
			"PI_AGENT_ROUTER_PARENT_SESSION_ID", "PI_SUBAGENT_PARENT_SESSION",
		} {
			if !inStrings(SubagentEnvHintKeys, key) {
				t.Fatalf("missing %s", key)
			}
		}
		if len(SubagentEnvHintKeys) != 13 {
			t.Fatalf("%d keys", len(SubagentEnvHintKeys))
		}
	})
	tw(t, f, "returns false when env hint value is empty string", func(t *testing.T) {
		fwClearHintEnv(t)
		t.Setenv("PI_IS_SUBAGENT", "")
		if fwIsSubagent(t, fwDetectionCtx("", ""), "/sessions/subagents", fwPosix, nil) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when env hint value is whitespace only", func(t *testing.T) {
		fwClearHintEnv(t)
		t.Setenv("PI_IS_SUBAGENT", "   ")
		if fwIsSubagent(t, fwDetectionCtx("", ""), "/sessions/subagents", fwPosix, nil) {
			t.Fatal("true")
		}
	})
}

func TestIsSubagentExecutionContextSessionDir(t *testing.T) {
	const f = "authority/subagent-context"
	const root = "/home/user/.pi/agent/sessions/subagents"
	detect := func(t *testing.T, dir string) bool {
		t.Helper()
		fwClearHintEnv(t)
		return fwIsSubagent(t, fwDetectionCtx(dir, ""), root, fwPosix, nil)
	}
	tw(t, f, "returns true when session dir is within subagent root", func(t *testing.T) {
		if !detect(t, root+"/session-abc") {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true when session dir equals subagent root", func(t *testing.T) {
		if !detect(t, root) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when session dir is outside subagent root", func(t *testing.T) {
		if detect(t, "/home/user/.pi/agent/sessions/main-session") {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when session dir is a sibling with shared prefix", func(t *testing.T) {
		// "/sessions/subagents-extra" should not match root "/sessions/subagents"
		if detect(t, root+"-extra/session-abc") {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when a `..` segment escapes the subagent root", func(t *testing.T) {
		// Normalizes to /home/user/.pi/agent/sessions/evil/session-abc: outside.
		if detect(t, root+"/../evil/session-abc") {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns true when a `..` segment resolves back inside the root", func(t *testing.T) {
		// Normalizes to /home/user/.pi/agent/sessions/subagents/session-abc: inside.
		if !detect(t, root+"/nested/../session-abc") {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when session dir is under a different root", func(t *testing.T) {
		if detect(t, "/var/other/subagents/session-abc") {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when getSessionDir returns null", func(t *testing.T) {
		if detect(t, "") {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when getSessionDir returns empty string", func(t *testing.T) {
		if detect(t, "") {
			t.Fatal("true")
		}
	})
}

func TestIsSubagentExecutionContextSessionDirWin32(t *testing.T) {
	const f = "authority/subagent-context"
	const root = `C:\Users\dev\.pi\agent\sessions\subagents`
	detect := func(t *testing.T, dir string) bool {
		t.Helper()
		fwClearHintEnv(t)
		return fwIsSubagent(t, fwDetectionCtx(dir, ""), root, fwWin32, nil)
	}
	tw(t, f, "returns true when session dir is within subagent root", func(t *testing.T) {
		if !detect(t, root+`\session-abc`) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true when session dir equals subagent root (case-insensitive)", func(t *testing.T) {
		if !detect(t, strings.ToUpper(root)) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when session dir is a sibling with shared prefix", func(t *testing.T) {
		if detect(t, root+`-extra\session-abc`) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when a `..` segment escapes the subagent root", func(t *testing.T) {
		if detect(t, root+`\..\evil\session-abc`) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when session dir is on a different drive", func(t *testing.T) {
		if detect(t, `D:\Users\dev\.pi\agent\sessions\subagents\session-abc`) {
			t.Fatal("true")
		}
	})
}

func TestIsSubagentExecutionContextRegistry(t *testing.T) {
	const f = "authority/subagent-context"
	const root = "/home/user/.pi/agent/sessions/subagents"
	const outsideDir = "/home/user/projects/my-app/.pi/agent/sessions/parent/tasks"
	const childSessionID = "child-session-abc"
	tw(t, f, "returns true when session id is registered (no env vars, dir outside filesystem root)", func(t *testing.T) {
		fwClearHintEnv(t)
		registry := NewSubagentSessionRegistry()
		registry.Register(childSessionID, SubagentSessionInfo{})
		if !fwIsSubagent(t, fwDetectionCtx(outsideDir, childSessionID), root, fwPosix, registry) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true when registered session has a parentSessionId", func(t *testing.T) {
		fwClearHintEnv(t)
		registry := NewSubagentSessionRegistry()
		registry.Register(childSessionID, SubagentSessionInfo{ParentSessionID: fwSP("parent-123")})
		if !fwIsSubagent(t, fwDetectionCtx(outsideDir, childSessionID), root, fwPosix, registry) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when registry is provided but session id is not registered", func(t *testing.T) {
		fwClearHintEnv(t)
		if fwIsSubagent(t, fwDetectionCtx(outsideDir, childSessionID), root, fwPosix, NewSubagentSessionRegistry()) {
			t.Fatal("true")
		}
	})
	tw(t, f, "returns false when session id is empty and registry has no matching entry", func(t *testing.T) {
		fwClearHintEnv(t)
		if fwIsSubagent(t, fwDetectionCtx("", ""), root, fwPosix, NewSubagentSessionRegistry()) {
			t.Fatal("true")
		}
	})
	tw(t, f, "registry check takes priority over env var detection", func(t *testing.T) {
		// Registry says registered; env var not set: should still return true.
		fwClearHintEnv(t)
		registry := NewSubagentSessionRegistry()
		registry.Register(childSessionID, SubagentSessionInfo{})
		// Confirm no env var is set
		if os.Getenv("PI_IS_SUBAGENT") != "" {
			t.Fatal("env var set")
		}
		if !fwIsSubagent(t, fwDetectionCtx(outsideDir, childSessionID), root, fwPosix, registry) {
			t.Fatal("false")
		}
	})
	tw(t, f, "unregistered session falls through to env var detection", func(t *testing.T) {
		fwClearHintEnv(t)
		t.Setenv("PI_IS_SUBAGENT", "true")
		// empty registry: childSessionId not registered; env var present, so still true without a registry entry
		if !fwIsSubagent(t, fwDetectionCtx(outsideDir, childSessionID), root, fwPosix, NewSubagentSessionRegistry()) {
			t.Fatal("false")
		}
	})
	tw(t, f, "no registry passed — existing behaviour unchanged", func(t *testing.T) {
		fwClearHintEnv(t)
		// The parameter is truly optional (no registry arg)
		if fwIsSubagent(t, fwDetectionCtx("", ""), root, fwPosix, nil) {
			t.Fatal("true")
		}
	})
}

// Go-specific: a failing GetSessionDir is the original's throw, so it propagates instead of reading as "not a subagent".
func TestIsSubagentExecutionContextSessionDirError(t *testing.T) {
	fwClearHintEnv(t)
	ctx := SubagentDetectionContext{SessionManager: &fwSessionManager{
		sessionID:  fwID(""),
		sessionDir: func() (string, error) { return "", errors.New("no session dir") },
	}}
	if got, err := IsSubagentExecutionContext(ctx, "/root", fwPosix, nil); err == nil || got {
		t.Fatalf("got=%v err=%v", got, err)
	}
	// Registration is checked before the session directory is read, so a registered child never reaches the failing read.
	registry := NewSubagentSessionRegistry()
	registry.Register("c", SubagentSessionInfo{})
	ctx.SessionManager.(*fwSessionManager).sessionID = fwID("c")
	if got, err := IsSubagentExecutionContext(ctx, "/root", fwPosix, registry); err != nil || !got {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

// ── subagent-detection ─────────────────────────────────────────────────────

func TestSubagentDetection(t *testing.T) {
	const f = "authority/subagent-detection"
	const subagentSessionsDir = "/agent/subagent-sessions"
	newDetection := func(registry *SubagentSessionRegistry) *SubagentDetection {
		return NewSubagentDetection(SubagentDetectionDeps{SubagentSessionsDir: subagentSessionsDir, Flavor: fwPosix, Registry: registry})
	}
	isSubagent := func(t *testing.T, d SubagentDetector, ctx SubagentDetectionContext) bool {
		t.Helper()
		got, err := d.IsSubagent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	tw(t, f, "returns true for a registered in-process child (registry source)", func(t *testing.T) {
		fwClearHintEnv(t)
		registry := NewSubagentSessionRegistry()
		registry.Register("child-1", SubagentSessionInfo{})
		if !isSubagent(t, newDetection(registry), fwDetectionCtx("", "child-1")) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true when a subagent env hint is set (env source)", func(t *testing.T) {
		fwClearHintEnv(t)
		t.Setenv("PI_IS_SUBAGENT", "1")
		if !isSubagent(t, newDetection(NewSubagentSessionRegistry()), fwDetectionCtx("/somewhere/else", "")) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns true when the session dir is nested under subagentSessionsDir (filesystem source)", func(t *testing.T) {
		fwClearHintEnv(t)
		if !isSubagent(t, newDetection(NewSubagentSessionRegistry()), fwDetectionCtx(subagentSessionsDir+"/child-1", "")) {
			t.Fatal("false")
		}
	})
	tw(t, f, "returns false when no source matches", func(t *testing.T) {
		fwClearHintEnv(t)
		if isSubagent(t, newDetection(NewSubagentSessionRegistry()), fwDetectionCtx("/projects/my-app", "")) {
			t.Fatal("true")
		}
	})
}

// ── subagent-lifecycle-events ──────────────────────────────────────────────

// fwBus is a synchronous in-process event bus, like createEventBus(): listeners run in registration order, each finishing before
// emit returns.
type fwBus struct {
	mu    sync.Mutex
	subs  []*fwBusSub
	onLog []string
	fail  string
}

type fwBusSub struct {
	channel string
	handler func(any)
	off     bool
}

func (b *fwBus) On(channel string, handler func(any)) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onLog = append(b.onLog, channel)
	if b.fail == channel {
		return nil, errors.New("host refused " + channel)
	}
	s := &fwBusSub{channel: channel, handler: handler}
	b.subs = append(b.subs, s)
	return func() { b.mu.Lock(); s.off = true; b.mu.Unlock() }, nil
}

func (b *fwBus) emit(channel string, data any) {
	b.mu.Lock()
	var run []func(any)
	for _, s := range b.subs {
		if s.channel == channel && !s.off {
			run = append(run, s.handler)
		}
	}
	b.mu.Unlock()
	for _, h := range run {
		h(data)
	}
}

type fwAuditor struct{ children []BoundChild }

func (a *fwAuditor) AuditBoundChild(c BoundChild) { a.children = append(a.children, c) }

func TestSubscribeSubagentLifecycle(t *testing.T) {
	const f = "authority/subagent-lifecycle-events"
	setup := func(t *testing.T) (*fwBus, *SubagentSessionRegistry, *fwAuditor, func()) {
		t.Helper()
		bus, registry, audit := &fwBus{}, NewSubagentSessionRegistry(), &fwAuditor{}
		unsubscribe, err := SubscribeSubagentLifecycle(bus, registry, audit)
		if err != nil {
			t.Fatal(err)
		}
		return bus, registry, audit, unsubscribe
	}
	tw(t, f, "registers a child session on session-created", func(t *testing.T) {
		bus, registry, _, _ := setup(t)
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-session-abc", "parentSessionId": "parent-42"})
		got, ok := registry.Get("child-session-abc")
		if !ok || got.ParentSessionID == nil || *got.ParentSessionID != "parent-42" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "populates the registry synchronously — before emit() returns", func(t *testing.T) {
		// Guards the pre-bindExtensions ordering: the core emits session-created on the same synchronous call stack right before
		// bindExtensions(), so the handler must complete before emit() returns.
		bus, registry, _, _ := setup(t)
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-session-sync"})
		// No wait between emit and this assertion.
		if !registry.Has("child-session-sync") {
			t.Fatal("not registered")
		}
	})
	tw(t, f, "omits parentSessionId when the event does not carry one", func(t *testing.T) {
		bus, registry, _, _ := setup(t)
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-session-xyz"})
		if got, ok := registry.Get("child-session-xyz"); !ok || got.ParentSessionID != nil {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "unregisters a child session on disposed", func(t *testing.T) {
		bus, registry, _, _ := setup(t)
		registry.Register("child-session-abc", SubagentSessionInfo{ParentSessionID: fwSP("parent-42")})
		bus.emit(SubagentChildDisposed, map[string]any{"sessionId": "child-session-abc"})
		if registry.Has("child-session-abc") {
			t.Fatal("still registered")
		}
	})
	tw(t, f, "hands a bound child to the auditor", func(t *testing.T) {
		bus, _, audit, _ := setup(t)
		bus.emit(SubagentChildBound, map[string]any{"sessionId": "child-session-abc", "parentSessionId": "parent-42"})
		want := []BoundChild{{SessionID: "child-session-abc", ParentSessionID: fwSP("parent-42")}}
		if !reflect.DeepEqual(audit.children, want) {
			t.Fatalf("%+v", audit.children)
		}
	})
	tw(t, f, "detaches every handler when the returned unsubscribe is called", func(t *testing.T) {
		bus, registry, audit, unsubscribe := setup(t)
		unsubscribe()
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-session-abc"})
		bus.emit(SubagentChildBound, map[string]any{"sessionId": "child-session-abc"})
		bus.emit(SubagentChildDisposed, map[string]any{"sessionId": "child-session-abc"})
		if registry.Has("child-session-abc") || len(audit.children) != 0 {
			t.Fatal("handlers still attached")
		}
	})
	tw(t, f, "subscribes to a fake bus on the exact channel names", func(t *testing.T) {
		bus := &fwBus{}
		if _, err := SubscribeSubagentLifecycle(bus, NewSubagentSessionRegistry(), &fwAuditor{}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bus.onLog, []string{"subagents:child:session-created", "subagents:child:bound", "subagents:child:disposed"}) {
			t.Fatalf("%v", bus.onLog)
		}
	})
	tw(t, f, "exposes the canonical channel-name strings", func(t *testing.T) {
		if SubagentChildSessionCreated != "subagents:child:session-created" || SubagentChildBound != "subagents:child:bound" || SubagentChildDisposed != "subagents:child:disposed" {
			t.Fatal("channel names changed")
		}
	})
	// #298 regression: concurrent siblings must be independent.
	tw(t, f, "disposing one sibling does not evict the other (collision regression)", func(t *testing.T) {
		bus, registry, _, _ := setup(t)
		// Two concurrent children of the same parent register under distinct ids.
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-A", "parentSessionId": "parent-P"})
		bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-B", "parentSessionId": "parent-P"})
		// Sibling A finishes first.
		bus.emit(SubagentChildDisposed, map[string]any{"sessionId": "child-A"})
		// B must still be detected as a registered subagent.
		got, _ := registry.Get("child-B")
		if registry.Has("child-A") || !registry.Has("child-B") || got.ParentSessionID == nil || *got.ParentSessionID != "parent-P" {
			t.Fatal("sibling evicted")
		}
	})
}

// Go-specific: the host can refuse a subscription (the original's `on` cannot), and the payload can be an ordered object.
func TestSubscribeSubagentLifecycleHostErrors(t *testing.T) {
	bus := &fwBus{fail: SubagentChildDisposed}
	registry := NewSubagentSessionRegistry()
	if unsubscribe, err := SubscribeSubagentLifecycle(bus, registry, &fwAuditor{}); err == nil || unsubscribe != nil {
		t.Fatal("a refused subscription must fail the subscribe")
	}
	// The two that succeeded are detached again, so nothing half-subscribed keeps registering children.
	bus.emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child"})
	if registry.Has("child") {
		t.Fatal("a partial subscription stayed attached")
	}
	ok := &fwBus{}
	if _, err := SubscribeSubagentLifecycle(ok, registry, &fwAuditor{}); err != nil {
		t.Fatal(err)
	}
	ok.emit(SubagentChildSessionCreated, fwObj("sessionId", "obj-child", "parentSessionId", "p"))
	if got, _ := registry.Get("obj-child"); got.ParentSessionID == nil || *got.ParentSessionID != "p" {
		t.Fatal("ordered-object payload not read")
	}
	// A payload without a string session id registers nothing (the original would key an undefined entry no lookup can match).
	ok.emit(SubagentChildSessionCreated, map[string]any{"sessionId": 7.0})
	ok.emit(SubagentChildSessionCreated, nil)
}

// ── inherited-registrations ────────────────────────────────────────────────

type fwExtractor struct{ out string }

func (e *fwExtractor) Extract(*jsObject) (string, bool) { return e.out, true }

type fwFormatter struct{ out string }

func (f *fwFormatter) Format(*jsObject) (string, bool) { return f.out, true }

// fwService is makeFakePermissionsService(): every getter answers nothing unless a test sets one.
type fwService struct {
	extractor     ToolAccessExtractor
	formatter     ToolInputFormatter
	extractorArgs []string
	formatterArgs []string
}

func (s *fwService) GetToolAccessExtractor(name string) ToolAccessExtractor {
	s.extractorArgs = append(s.extractorArgs, name)
	return s.extractor
}

func (s *fwService) GetToolInputFormatter(name string) ToolInputFormatter {
	s.formatterArgs = append(s.formatterArgs, name)
	return s.formatter
}

type fwNode struct {
	id string
	ok bool
}

func (n fwNode) CurrentSessionID() (string, bool) { return n.id, n.ok }

func fwNodeIdentity(sessionID string) NodeIdentity { return fwNode{id: sessionID, ok: true} }

type fwParentChain map[string]string

func (c fwParentChain) Get(sessionID string) (SubagentSessionInfo, bool) {
	parent, ok := c[sessionID]
	if !ok {
		return SubagentSessionInfo{}, false
	}
	return SubagentSessionInfo{ParentSessionID: &parent}, true
}

func fwLocatorOf(services map[string]PermissionsService) (PermissionsServiceLocator, *[]string) {
	var calls []string
	return func(sessionID string) PermissionsService {
		calls = append(calls, sessionID)
		return services[sessionID]
	}, &calls
}

func fwHitFor(want PermissionsService) func(PermissionsService) any {
	return func(s PermissionsService) any {
		if s == want {
			return "hit"
		}
		return nil
	}
}

func TestAncestorNodesFindFirst(t *testing.T) {
	const f = "authority/inherited-registrations"
	tw(t, f, "answers from the immediate parent", func(t *testing.T) {
		parent := &fwService{}
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": parent})
		a := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate)
		if got := a.FindFirst(fwHitFor(parent)); got != "hit" {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "keeps walking to a grandparent when the parent declines", func(t *testing.T) {
		grandparent, parent := &fwService{}, &fwService{}
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": parent, "grandparent": grandparent})
		a := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent", "parent": "grandparent"}, locate)
		if got := a.FindFirst(fwHitFor(grandparent)); got != "hit" {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "walks past a hop that published no service", func(t *testing.T) {
		// A node with no service still names its own parent in the registry, so an unpublished intermediate must not end the walk.
		grandparent := &fwService{}
		locate, _ := fwLocatorOf(map[string]PermissionsService{"grandparent": grandparent})
		a := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent", "parent": "grandparent"}, locate)
		if got := a.FindFirst(fwHitFor(grandparent)); got != "hit" {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "answers undefined when this node has no session id", func(t *testing.T) {
		locate, calls := fwLocatorOf(nil)
		a := NewAncestorNodes(fwNode{}, fwParentChain{"child": "parent"}, locate)
		if got := a.FindFirst(func(PermissionsService) any { return "hit" }); got != nil || len(*calls) != 0 {
			t.Fatalf("%v %v", got, *calls)
		}
	})
	tw(t, f, "answers undefined for a root node with no registry entry", func(t *testing.T) {
		locate, calls := fwLocatorOf(nil)
		a := NewAncestorNodes(fwNodeIdentity("root"), fwParentChain{}, locate)
		if got := a.FindFirst(func(PermissionsService) any { return "hit" }); got != nil || len(*calls) != 0 {
			t.Fatalf("%v %v", got, *calls)
		}
	})
	tw(t, f, "answers undefined when no ancestor answers", func(t *testing.T) {
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": &fwService{}})
		a := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate)
		if got := a.FindFirst(func(PermissionsService) any { return nil }); got != nil {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "terminates on a cycle in the parent chain", func(t *testing.T) {
		locate, _ := fwLocatorOf(map[string]PermissionsService{"a": &fwService{}, "b": &fwService{}})
		a := NewAncestorNodes(fwNodeIdentity("a"), fwParentChain{"a": "b", "b": "a"}, locate)
		if got := a.FindFirst(func(PermissionsService) any { return nil }); got != nil {
			t.Fatalf("%v", got)
		}
	})
	tw(t, f, "resolves each hop through the locator rather than caching a service", func(t *testing.T) {
		locate, calls := fwLocatorOf(map[string]PermissionsService{"parent": &fwService{}})
		a := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate)
		a.FindFirst(func(PermissionsService) any { return "hit" })
		a.FindFirst(func(PermissionsService) any { return "hit" })
		if !reflect.DeepEqual(*calls, []string{"parent", "parent"}) {
			t.Fatalf("%v", *calls)
		}
	})
}

// fwFinder is the `{ findFirst } as AncestorNodes` fake.
type fwFinder struct{ calls int }

func (f *fwFinder) FindFirst(func(PermissionsService) any) any { f.calls++; return nil }

func TestInheritingToolAccessExtractorLookup(t *testing.T) {
	const f = "authority/inherited-registrations"
	extractor := &fwExtractor{out: "/etc/hosts"}
	tw(t, f, "answers this node's own registration as local", func(t *testing.T) {
		local := &ToolAccessExtractorRegistry{}
		if _, err := local.Register("ffgrep", extractor); err != nil {
			t.Fatal(err)
		}
		finder := &fwFinder{}
		got := NewInheritingToolAccessExtractorLookup(local, finder).Resolve("ffgrep")
		if got == nil || got.Extractor != ToolAccessExtractor(extractor) || got.Origin != "local" {
			t.Fatalf("%+v", got)
		}
		// A local hit must not reach across the node boundary at all.
		if finder.calls != 0 {
			t.Fatal("walked ancestors")
		}
	})
	tw(t, f, "answers an ancestor's registration as inherited", func(t *testing.T) {
		parent := &fwService{extractor: extractor}
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": parent})
		lookup := NewInheritingToolAccessExtractorLookup(&ToolAccessExtractorRegistry{}, NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate))
		got := lookup.Resolve("ffgrep")
		if got == nil || got.Extractor != ToolAccessExtractor(extractor) || got.Origin != "inherited" {
			t.Fatalf("%+v", got)
		}
		if !reflect.DeepEqual(parent.extractorArgs, []string{"ffgrep"}) {
			t.Fatal(parent.extractorArgs)
		}
	})
	tw(t, f, "answers undefined when neither this node nor an ancestor has one", func(t *testing.T) {
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": &fwService{}})
		lookup := NewInheritingToolAccessExtractorLookup(&ToolAccessExtractorRegistry{}, NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate))
		if got := lookup.Resolve("ffgrep"); got != nil {
			t.Fatalf("%+v", got)
		}
	})
}

func TestInheritingToolInputFormatterLookup(t *testing.T) {
	const f = "authority/inherited-registrations"
	formatter := &fwFormatter{out: "preview"}
	tw(t, f, "answers this node's own registration without consulting an ancestor", func(t *testing.T) {
		local := &ToolInputFormatterRegistry{}
		if _, err := local.Register("my-tool", formatter); err != nil {
			t.Fatal(err)
		}
		finder := &fwFinder{}
		if got := NewInheritingToolInputFormatterLookup(local, finder).Get("my-tool"); got != ToolInputFormatter(formatter) || finder.calls != 0 {
			t.Fatalf("%v calls=%d", got, finder.calls)
		}
	})
	tw(t, f, "answers an ancestor's registration", func(t *testing.T) {
		parent := &fwService{formatter: formatter}
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": parent})
		lookup := NewInheritingToolInputFormatterLookup(&ToolInputFormatterRegistry{}, NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate))
		if got := lookup.Get("my-tool"); got != ToolInputFormatter(formatter) {
			t.Fatalf("%v", got)
		}
		if !reflect.DeepEqual(parent.formatterArgs, []string{"my-tool"}) {
			t.Fatal(parent.formatterArgs)
		}
	})
	tw(t, f, "answers undefined when neither this node nor an ancestor has one", func(t *testing.T) {
		locate, _ := fwLocatorOf(map[string]PermissionsService{"parent": &fwService{}})
		lookup := NewInheritingToolInputFormatterLookup(&ToolInputFormatterRegistry{}, NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent"}, locate))
		if got := lookup.Get("my-tool"); got != nil {
			t.Fatalf("%v", got)
		}
	})
}

// ── child-node-audit ───────────────────────────────────────────────────────

func fwMakeAudit(present bool) (*ChildNodeAudit, *fwLogger) {
	logger := &fwLogger{}
	return NewChildNodeAudit(func(string) bool { return present }, logger), logger
}

func TestChildNodeAudit(t *testing.T) {
	const f = "authority/child-node-audit"
	tw(t, f, "records nothing and warns nobody", func(t *testing.T) {
		audit, logger := fwMakeAudit(true)
		audit.AuditBoundChild(BoundChild{SessionID: "child-1", ParentSessionID: fwSP("parent-1")})
		if len(logger.reviews()) != 0 || len(logger.warn) != 0 {
			t.Fatal("logged for a child with a node")
		}
	})
	tw(t, f, "asks the lookup for the child's own session id", func(t *testing.T) {
		var asked []string
		NewChildNodeAudit(func(id string) bool { asked = append(asked, id); return true }, &fwLogger{}).
			AuditBoundChild(BoundChild{SessionID: "child-1", ParentSessionID: fwSP("parent-1")})
		if !reflect.DeepEqual(asked, []string{"child-1"}) {
			t.Fatal(asked)
		}
	})
	tw(t, f, "records the absence with both session ids", func(t *testing.T) {
		audit, logger := fwMakeAudit(false)
		audit.AuditBoundChild(BoundChild{SessionID: "child-1", ParentSessionID: fwSP("parent-1")})
		r := logger.reviews()
		if len(r) != 1 || r[0].event != "child_node_absent" || !fwEqual(r[0].details, fwObj("childSessionId", "child-1", "parentSessionId", "parent-1")) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "records a null parentSessionId when the event carries none", func(t *testing.T) {
		audit, logger := fwMakeAudit(false)
		audit.AuditBoundChild(BoundChild{SessionID: "child-1"})
		r := logger.reviews()
		if len(r) != 1 || !fwEqual(r[0].details, fwObj("childSessionId", "child-1", "parentSessionId", nil)) {
			t.Fatalf("%+v", r)
		}
	})
	tw(t, f, "warns with the message naming the child and the likely cause", func(t *testing.T) {
		audit, logger := fwMakeAudit(false)
		audit.AuditBoundChild(BoundChild{SessionID: "child-1"})
		if !reflect.DeepEqual(logger.warn, []string{ChildNodeAbsentMessage("child-1")}) {
			t.Fatalf("%v", logger.warn)
		}
	})
	tw(t, f, "records every affected child", func(t *testing.T) {
		audit, logger := fwMakeAudit(false)
		for _, id := range []string{"child-1", "child-2", "child-3"} {
			audit.AuditBoundChild(BoundChild{SessionID: id})
		}
		if len(logger.reviews()) != 3 {
			t.Fatal(len(logger.reviews()))
		}
	})
	tw(t, f, "warns once, naming the first affected child", func(t *testing.T) {
		audit, logger := fwMakeAudit(false)
		for _, id := range []string{"child-1", "child-2", "child-3"} {
			audit.AuditBoundChild(BoundChild{SessionID: id})
		}
		if !reflect.DeepEqual(logger.warn, []string{ChildNodeAbsentMessage("child-1")}) {
			t.Fatalf("%v", logger.warn)
		}
	})
}

func TestChildNodeAbsentMessage(t *testing.T) {
	const f = "authority/child-node-audit"
	tw(t, f, "names the child session, the exclusion setting, and the review event", func(t *testing.T) {
		message := ChildNodeAbsentMessage("child-1")
		for _, want := range []string{"child-1", "excludedExtensionPackages", "child_node_absent"} {
			if !strings.Contains(message, want) {
				t.Fatalf("%q missing from %q", want, message)
			}
		}
	})
}

// ── delegation-envelope ────────────────────────────────────────────────────

// fwEnvelopeDetails builds details whose gate-computed surface is accessIntentSurface (nil: no access intent) and whose display
// surface is displaySurface.
func fwEnvelopeDetails(accessIntentSurface, displaySurface *string) *PromptPermissionDetails {
	details := &PromptPermissionDetails{RequestID: "req", Source: "tool_call", Payload: fwMakePromptPayload(), Surface: displaySurface}
	if accessIntentSurface != nil {
		details.AccessIntent = &ForwardedAccessFacts{Surface: *accessIntentSurface, MatchValues: []string{"/some/value"}}
	}
	return details
}

type fwLink struct {
	verdict AuthorizerVerdict
	details []*PromptPermissionDetails
	query   []PermissionQuery
	log     []AuthorizerLog
}

func (l *fwLink) authorize(d *PromptPermissionDetails, q PermissionQuery, log AuthorizerLog) (AuthorizerVerdict, error) {
	l.details, l.query, l.log = append(l.details, d), append(l.query, q), append(l.log, log)
	return l.verdict, nil
}

func fwEnclose(verdict AuthorizerVerdict) (AuthorizeFunc, *fwLink) {
	link := &fwLink{verdict: verdict}
	return EncloseInDelegationEnvelope(link.authorize), link
}

func TestEncloseInDelegationEnvelope(t *testing.T) {
	const f = "authority/delegation-envelope"
	query, log := new(int), new(int)
	verdictOf := func(t *testing.T, kind string, reason *string, details *PromptPermissionDetails) AuthorizerVerdict {
		t.Helper()
		enclosed, _ := fwEnclose(AuthorizerVerdict{Kind: kind, Reason: reason})
		v, err := enclosed(details, query, log)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	deferred := AuthorizerVerdict{Kind: "defer"}
	tw(t, f, "downgrades an allow on external_directory", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP("external_directory"), nil)); got != deferred {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "downgrades an allow on the path surface", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP("path"), nil)); got != deferred {
			t.Fatalf("%+v", got)
		}
	})
	// it.each([...]): outside the ledger (the title carries the surface).
	for _, surface := range []string{"path_read", "path_write", "external_directory_read", "external_directory_write"} {
		t.Run("downgrades an allow on "+surface+", a member of an excluded family", func(t *testing.T) {
			if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP(surface), nil)); got != deferred {
				t.Fatalf("%+v", got)
			}
		})
	}
	tw(t, f, "downgrades an allow when the surface is undetermined (fail-safe)", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(nil, nil)); got != deferred {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "keeps an allow on a non-excluded surface (bash)", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP("bash"), nil)); got != (AuthorizerVerdict{Kind: "allow"}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "keeps an allow on a per-tool surface (read)", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP("read"), nil)); got != (AuthorizerVerdict{Kind: "allow"}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "never caps a deny, even on an excluded surface", func(t *testing.T) {
		got := verdictOf(t, "deny", fwSP("wrong path"), fwEnvelopeDetails(fwSP("external_directory"), nil))
		if got.Kind != "deny" || got.Reason == nil || *got.Reason != "wrong path" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "never caps a defer", func(t *testing.T) {
		if got := verdictOf(t, "defer", nil, fwEnvelopeDetails(fwSP("path"), nil)); got != deferred {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "prefers the gate-computed accessIntent surface over the display surface", func(t *testing.T) {
		// accessIntent.surface (external_directory) is authoritative even when the display-surface override says otherwise.
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(fwSP("external_directory"), fwSP("bash"))); got != deferred {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "forwards details, the injected query, and the review-log seam to the wrapped link", func(t *testing.T) {
		enclosed, link := fwEnclose(deferred)
		details := fwEnvelopeDetails(fwSP("bash"), nil)
		if _, err := enclosed(details, query, log); err != nil {
			t.Fatal(err)
		}
		if len(link.details) != 1 || link.details[0] != details || link.query[0] != PermissionQuery(query) || link.log[0] != AuthorizerLog(log) {
			t.Fatal("arguments not forwarded as they were")
		}
	})
	// Go-specific: the display surface decides when the gate computed none, and a link's error passes through.
	t.Run("falls back to the display surface when no access intent is present", func(t *testing.T) {
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(nil, fwSP("path_write"))); got != deferred {
			t.Fatalf("%+v", got)
		}
		if got := verdictOf(t, "allow", nil, fwEnvelopeDetails(nil, fwSP("bash"))); got != (AuthorizerVerdict{Kind: "allow"}) {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("passes a link's error through without capping", func(t *testing.T) {
		boom := errors.New("link failed")
		enclosed := EncloseInDelegationEnvelope(func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			return AuthorizerVerdict{Kind: "allow"}, boom
		})
		if _, err := enclosed(fwEnvelopeDetails(fwSP("path"), nil), query, log); !errors.Is(err, boom) {
			t.Fatalf("%v", err)
		}
	})
}

// Go-specific: the walk through the inheriting lookups, which hand each service to a getter, over a hop with no service and over a
// node the registry knows with no parent.
func TestInheritingLookupsWalkPastUnpublishedHops(t *testing.T) {
	extractor := &fwExtractor{out: "/etc/hosts"}
	grandparent := &fwService{extractor: extractor, formatter: &fwFormatter{out: "p"}}
	locate, _ := fwLocatorOf(map[string]PermissionsService{"grandparent": grandparent})
	ancestors := NewAncestorNodes(fwNodeIdentity("child"), fwParentChain{"child": "parent", "parent": "grandparent"}, locate)
	got := NewInheritingToolAccessExtractorLookup(&ToolAccessExtractorRegistry{}, ancestors).Resolve("t")
	if got == nil || got.Origin != "inherited" || got.Extractor != ToolAccessExtractor(extractor) {
		t.Fatalf("%+v", got)
	}
	if NewInheritingToolInputFormatterLookup(&ToolInputFormatterRegistry{}, ancestors).Get("t") == nil {
		t.Fatal("formatter not inherited across an unpublished hop")
	}
}

type fwNoParentChain struct{}

func (fwNoParentChain) Get(string) (SubagentSessionInfo, bool) { return SubagentSessionInfo{}, true }

func TestAncestorNodesStopAtANodeWithNoParent(t *testing.T) {
	locate, calls := fwLocatorOf(nil)
	ancestors := NewAncestorNodes(fwNodeIdentity("child"), fwNoParentChain{}, locate)
	if got := ancestors.FindFirst(func(PermissionsService) any { return "hit" }); got != nil || len(*calls) != 0 {
		t.Fatalf("%v %v", got, *calls)
	}
}

// Go-specific: a session manager that answers an id together with an error did not answer (the original's call threw).
func TestIsRegisteredSubagentChildIgnoresAnIdWithAnError(t *testing.T) {
	registry := NewSubagentSessionRegistry()
	registry.Register("child", SubagentSessionInfo{})
	ctx := SubagentDetectionContext{SessionManager: &fwSessionManager{
		sessionDir: fwID(""),
		sessionID:  func() (string, error) { return "child", errors.New("session id unavailable") },
	}}
	if IsRegisteredSubagentChild(ctx, registry) {
		t.Fatal("an id that came with an error was used")
	}
}
