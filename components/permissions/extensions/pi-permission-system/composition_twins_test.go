package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The composition-root twins (see composition_test.go for the harness).

func readyEvents(b *memBus) []map[string]any {
	var out []map[string]any
	for _, e := range b.emits(PermissionsReadyChannel) {
		out = append(out, e.(map[string]any))
	}
	return out
}

func TestCompositionRootTwins(t *testing.T) {
	const f = "composition-root"

	tskip(t, f, "registers a handler for every required event exactly once", "needs the host's registered-handler list: asserted through the fake host by TestAdapterRegistersEvents (adapter_test.go, external package)")

	tw(t, f, "lets a child instance forward an ask it received via the parent's bus", func(t *testing.T) {
		t.Skip("the forwarded request is answered by hand through the forwarding directory; the protocol is twinned by the forwarding lane (fwd_*_test.go) and the two-process test")
	})

	tw(t, f, "blocks promptly when no session is draining the parent's inbox", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "external_directory": "ask"}})
		childCwd, external := t.TempDir(), t.TempDir()
		parentBus := newMemBus()
		_, _ = newExtensionWith(buildOptions{events: parentBus, lifecycleBus: parentBus})
		child := newCompNode(t, "read")
		registerChild(t, "child-session-2", "parent-session-2")
		// no markServing: nobody is polling the parent's inbox
		res := child.toolCall(makeChildCtx(childCwd, "child-session-2"), "read", map[string]any{"path": filepath.Join(external, "secret.txt")})
		eq(t, res.Block, true, "blocked")
		eq(t, strings.Contains(res.Reason, "no interactive UI is available"), true, "no UI")
		eq(t, strings.Contains(res.Reason, "Session 'parent-session-2' is not serving forwarded permission requests."), true, "not serving")
		eq(t, strings.Contains(res.Reason, "User denied"), false, "not a user denial")
	})

	notifier := func(into *[]string) func(string) { return func(m string) { *into = append(*into, m) } }

	tw(t, f, "records and warns when a bound child published no service", func(t *testing.T) {
		agent := compEnv(t)
		var notified []string
		parent := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "parent-session-unguarded", compCtxOpts{notify: notifier(&notified)})
		parent.sessionStart(ctx)
		_ = parent.bus.Emit(SubagentChildSessionCreated, map[string]any{"sessionId": "child-unguarded-1", "parentSessionId": "parent-session-unguarded"})
		_ = parent.bus.Emit(SubagentChildBound, map[string]any{"sessionId": "child-unguarded-1", "parentSessionId": "parent-session-unguarded"})
		t.Cleanup(func() { GetSubagentSessionRegistry().Unregister("child-unguarded-1") })
		eq(t, GetPermissionsService("child-unguarded-1") == nil, true, "no service")
		eq(t, compEventCount(compReviewLog(agent), "child_node_absent"), 1, "recorded")
		eq(t, notified, []string{ChildNodeAbsentMessage("child-unguarded-1")}, "warned")
	})
	tw(t, f, "records every affected child but warns only once", func(t *testing.T) {
		agent := compEnv(t)
		var notified []string
		parent := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "parent-session-unguarded", compCtxOpts{notify: notifier(&notified)})
		parent.sessionStart(ctx)
		for _, id := range []string{"child-a", "child-b", "child-c"} {
			_ = parent.bus.Emit(SubagentChildBound, map[string]any{"sessionId": id, "parentSessionId": "parent-session-unguarded"})
		}
		var ids []string
		for _, e := range compReviewLog(agent) {
			if e["event"] == "child_node_absent" {
				ids = append(ids, e["childSessionId"].(string))
			}
		}
		eq(t, ids, []string{"child-a", "child-b", "child-c"}, "every child recorded")
		eq(t, notified, []string{ChildNodeAbsentMessage("child-a")}, "one warning")
	})
	tw(t, f, "stays silent for a child whose own node published a service", func(t *testing.T) {
		agent := compEnv(t)
		var notified []string
		parent := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "parent-session-guarded", compCtxOpts{notify: notifier(&notified)})
		parent.sessionStart(ctx)
		child := newCompNode(t)
		child.sessionStart(makeChildCtx(t.TempDir(), "child-guarded-1"))
		_ = parent.bus.Emit(SubagentChildBound, map[string]any{"sessionId": "child-guarded-1", "parentSessionId": "parent-session-guarded"})
		eq(t, GetPermissionsService("child-guarded-1") != nil, true, "published")
		eq(t, compEventCount(compReviewLog(agent), "child_node_absent"), 0, "no alarm")
		eq(t, len(notified), 0, "silent")
	})

	startRootThenSetMarker := func(t *testing.T, marker string) *compNode {
		compEnv(t)
		root := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "ui-root-session", compCtxOpts{})
		root.sessionStart(ctx)
		eq(t, GetServingSessionRegistry().ServingIDs(), []string{"ui-root-session"}, "serving after session_start")
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", marker)
		root.beforeAgentStart(ctx, emptyPromptPayload())
		return root
	}
	tw(t, f, "keeps serving when the root inherits its own session id as the marker", func(t *testing.T) {
		startRootThenSetMarker(t, "ui-root-session")
		eq(t, GetServingSessionRegistry().ServingIDs(), []string{"ui-root-session"}, "serving")
	})
	tw(t, f, "keeps serving when the marker names a different session", func(t *testing.T) {
		startRootThenSetMarker(t, "some-other-session")
		eq(t, GetServingSessionRegistry().ServingIDs(), []string{"ui-root-session"}, "serving")
	})

	externalAsk := map[string]any{"permission": map[string]any{"*": "allow", "external_directory": "ask"}}
	fireChildRead := func(t *testing.T) ToolCallResult {
		child := newCompNode(t, "read")
		return child.toolCall(makeChildCtx(t.TempDir(), "child-session-oop"), "read", map[string]any{"path": filepath.Join(t.TempDir(), "secret.txt")})
	}
	tw(t, f, "blocks promptly when the out-of-process parent published no heartbeat", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, externalAsk)
		t.Setenv("PI_SUBAGENT_CHILD", "1")
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "parent-session-oop-gone")
		res := fireChildRead(t)
		eq(t, res.Block, true, "blocked")
		eq(t, strings.Contains(res.Reason, "Session 'parent-session-oop-gone' is not serving forwarded permission requests."), true, "reason")
		eq(t, strings.Contains(res.Reason, "User denied"), false, "not a user denial")
	})
	tskip(t, f, "waits for an out-of-process parent whose heartbeat is fresh", "answers the forwarded request by hand in the forwarding directory: the file protocol and liveness are twinned by the forwarding lane's two-process test")
	tskip(t, f, "forwards for a child that sets only the adapter convention's parent-session variable", "answers the forwarded request by hand in the forwarding directory: the env-hint detection is twinned by subagent_test.go")
	tskip(t, f, "forwards from a child that has its own UI while the named parent serves", "answers the forwarded request by hand in the forwarding directory: the live-relay selection is twinned by auth_selection_test.go")

	tw(t, f, "unpublishes the service and unsubscribes the lifecycle on shutdown", func(t *testing.T) {
		compEnv(t)
		n := newCompNode(t)
		n.sessionStart(makeChildCtx(t.TempDir(), "top-session"))
		eq(t, GetPermissionsService("top-session") != nil, true, "published")
		n.shutdown()
		eq(t, GetPermissionsService("top-session") == nil, true, "unpublished")
		_ = n.bus.Emit(SubagentChildSessionCreated, map[string]any{"sessionId": "late-child", "parentSessionId": "p-late"})
		eq(t, GetSubagentSessionRegistry().Has("late-child"), false, "lifecycle unsubscribed")
	})
	tw(t, f, "answers an unanswered ask when the session ends", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "demo": "ask"}})
		n := newCompNode(t, "demo")
		release := make(chan struct{})
		ctx, _ := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: func(string, []string) (string, bool) { <-release; return "", false }})
		n.sessionStart(ctx)
		done := make(chan ToolCallResult, 1)
		go func() { done <- n.toolCall(ctx, "demo", map[string]any{}) }()
		time.Sleep(50 * time.Millisecond)
		n.shutdown()
		select {
		case res := <-done:
			eq(t, res.Block, true, "blocked")
			eq(t, strings.Contains(res.Reason, SessionEndedReason), true, "the session-ended reason")
			eq(t, strings.Contains(res.Reason, "The user denied"), false, "not a user denial")
		case <-time.After(5 * time.Second):
			t.Fatal("the unanswered ask was not released at shutdown")
		}
		close(release)
	})

	tw(t, f, "surfaces a service-registered formatter in the gate's ask prompt", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "demo": "ask"}})
		n := newCompNode(t, "demo")
		ctx, ui := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		const marker = "PREVIEW::shared-registry-proof"
		_, err := n.service("ui-session").RegisterToolInputFormatter("demo", NewToolInputFormatter(func(*jsObject) (string, bool) { return marker, true }))
		eq(t, err, nil, "register")
		res := n.toolCall(ctx, "demo", map[string]any{"foo": "bar"})
		eq(t, res.Block, false, "prompted, not blocked")
		found := false
		for _, title := range ui.selected() {
			found = found || strings.Contains(title, marker)
		}
		eq(t, found, true, "the prompt embeds the formatter's preview")
	})

	ffgrep := func() ToolAccessExtractor {
		return NewToolAccessExtractor(func(in *jsObject) (string, bool) { return in.str("target") })
	}
	pathDenyConfig := map[string]any{"permission": map[string]any{"*": "allow", "path": map[string]any{"*.env": "deny"}}}
	tw(t, f, "path-gates a custom-shaped tool via a service-registered extractor", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, pathDenyConfig)
		n := newCompNode(t, "ffgrep")
		ctx, _ := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		_, _ = n.service("ui-session").RegisterToolAccessExtractor("ffgrep", ffgrep())
		eq(t, n.toolCall(ctx, "ffgrep", map[string]any{"target": ".env"}).Block, true, "path deny fired")
	})
	twoNodes := func(t *testing.T, tools ...string) (parent, child *compNode, childCtx SessionContext) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, pathDenyConfig)
		parent, child = newCompNode(t, tools...), newCompNode(t, tools...)
		ps, _ := makeBaseCtx(t.TempDir(), "parent-session-split", compCtxOpts{})
		parent.sessionStart(ps)
		registerChild(t, "child-session-split", "parent-session-split")
		childCtx = makeChildCtx(t.TempDir(), "child-session-split")
		child.sessionStart(childCtx)
		return
	}
	tw(t, f, "path-gates a child's own tool call via an extractor registered in that child", func(t *testing.T) {
		_, child, ctx := twoNodes(t, "ffgrep")
		_, _ = child.service("child-session-split").RegisterToolAccessExtractor("ffgrep", ffgrep())
		eq(t, child.toolCall(ctx, "ffgrep", map[string]any{"target": ".env"}).Block, true, "blocked")
	})
	tw(t, f, "gates a child's tool call through an extractor registered only in the parent", func(t *testing.T) {
		parent, child, ctx := twoNodes(t, "ffgrep")
		_, _ = parent.service("parent-session-split").RegisterToolAccessExtractor("ffgrep", ffgrep())
		eq(t, child.toolCall(ctx, "ffgrep", map[string]any{"target": ".env"}).Block, true, "inherited extractor gates the child")
	})
	pathDenials := func(agent string) []map[string]any {
		var out []map[string]any
		for _, e := range compReviewLog(agent) {
			if e["path"] == ".env" {
				out = append(out, e)
			}
		}
		return out
	}
	tw(t, f, "records that the child's decision used an inherited extractor", func(t *testing.T) {
		parent, child, ctx := twoNodes(t, "ffgrep")
		_, _ = parent.service("parent-session-split").RegisterToolAccessExtractor("ffgrep", ffgrep())
		child.toolCall(ctx, "ffgrep", map[string]any{"target": ".env"})
		den := pathDenials(os.Getenv("PI_CODING_AGENT_DIR"))
		eq(t, len(den) > 0, true, "entries")
		for _, e := range den {
			eq(t, e["extractorSource"], "inherited", "provenance")
		}
	})
	tw(t, f, "leaves the provenance field absent when the child resolved it locally", func(t *testing.T) {
		_, child, ctx := twoNodes(t, "ffgrep")
		_, _ = child.service("child-session-split").RegisterToolAccessExtractor("ffgrep", ffgrep())
		eq(t, child.toolCall(ctx, "ffgrep", map[string]any{"target": ".env"}).Block, true, "blocked")
		den := pathDenials(os.Getenv("PI_CODING_AGENT_DIR"))
		eq(t, len(den) > 0, true, "entries")
		for _, e := range den {
			_, has := e["extractorSource"]
			eq(t, has, false, "no provenance")
		}
	})
}
