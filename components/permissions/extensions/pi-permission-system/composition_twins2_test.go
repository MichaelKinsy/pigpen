package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func denyLinkNamed(reason string) AuthorizeFunc {
	return func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
		r := reason
		return AuthorizerVerdict{Kind: "deny", Reason: &r}, nil
	}
}

func TestCompositionRootTwins2(t *testing.T) {
	const f = "composition-root"
	askDemo := map[string]any{"permission": map[string]any{"*": "allow", "demo": "ask"}}

	tw(t, f, "does not resolve an authorizer registered only in the parent", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask"}, "authorizerChain": []string{"parent-only-judge"}})
		parent, child := newCompNode(t, "demo"), newCompNode(t, "demo")
		pctx, _ := makeBaseCtx(t.TempDir(), "parent-session-link", compCtxOpts{hasUI: noUI()})
		parent.sessionStart(pctx)
		registerChild(t, "child-session-link", "parent-session-link")
		var notified []string
		cctx, ui := makeBaseCtx(t.TempDir(), "child-session-link", compCtxOpts{select_: approveFirst, notify: func(m string) { notified = append(notified, m) }})
		child.sessionStart(cctx)
		called := false
		_, _ = parent.service("parent-session-link").RegisterAuthorizer("parent-only-judge", func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			called = true
			return AuthorizerVerdict{Kind: "deny"}, nil
		})
		res := child.toolCall(cctx, "demo", map[string]any{})
		eq(t, called, false, "the parent's link never runs in the child")
		eq(t, res.Block, false, "the ask reaches the child's own approving terminal")
		eq(t, len(ui.selected()), 1, "one prompt")
		events := []string{}
		for _, e := range compReviewLog(agent) {
			events = append(events, e["event"].(string))
		}
		eq(t, inStrings(events, "authorizer_chain_unregistered_link"), true, "vacancy recorded")
		eq(t, notified, []string{UnregisteredLinkMessage("parent-only-judge")}, "the operator is told once")
	})
	tw(t, f, "consults a service-registered, config-named link at the ask gate", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask"}, "authorizerChain": []string{"typo-judge"}})
		n := newCompNode(t, "demo")
		ctx, ui := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		_, _ = n.service("ui-session").RegisterAuthorizer("typo-judge", denyLinkNamed("typo path"))
		eq(t, n.toolCall(ctx, "demo", map[string]any{}).Block, true, "the link denied")
		eq(t, len(ui.selected()), 0, "before the UI terminal")
	})
	tw(t, f, "ignores a registered link the operator did not name (opt-in)", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask"}})
		n := newCompNode(t, "demo")
		ctx, ui := makeBaseCtx(t.TempDir(), "ui-session", compCtxOpts{select_: approveFirst})
		n.sessionStart(ctx)
		_, _ = n.service("ui-session").RegisterAuthorizer("typo-judge", denyLinkNamed("typo path"))
		eq(t, n.toolCall(ctx, "demo", map[string]any{}).Block, false, "dormant link")
		eq(t, len(ui.selected()), 1, "the UI was asked")
	})

	tw(t, f, "publishes the service before emitting permissions:ready", func(t *testing.T) {
		compEnv(t)
		n := newCompNode(t)
		var seen []string
		_, _ = n.bus.On(PermissionsReadyChannel, func(data any) {
			id, _ := data.(map[string]any)["sessionId"].(string)
			if id != "" && GetPermissionsService(id) != nil {
				seen = append(seen, "present")
			} else {
				seen = append(seen, "missing")
			}
		})
		eq(t, len(seen), 0, "not emitted at load")
		n.sessionStart(makeChildCtx(t.TempDir(), "top-session"))
		eq(t, seen, []string{"present"}, "published first")
	})
	tw(t, f, "re-emits permissions:ready once at the first before_agent_start", func(t *testing.T) {
		compEnv(t)
		n := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "latch-session", compCtxOpts{})
		n.sessionStart(ctx)
		n.beforeAgentStart(ctx, emptyPromptPayload())
		n.beforeAgentStart(ctx, emptyPromptPayload())
		got := readyEvents(n.bus)
		eq(t, len(got), 2, "session_start and the first turn")
		for _, e := range got {
			eq(t, e["sessionId"], "latch-session", "id")
			eq(t, e["adjudicatesLocally"], true, "role")
		}
	})
	tw(t, f, "re-arms the latch for the next session generation", func(t *testing.T) {
		compEnv(t)
		n := newCompNode(t)
		ctx, _ := makeBaseCtx(t.TempDir(), "latch-reload-session", compCtxOpts{})
		n.sessionStart(ctx)
		n.beforeAgentStart(ctx, emptyPromptPayload())
		eq(t, len(readyEvents(n.bus)), 2, "first generation")
		n.sessionStart(ctx)
		n.beforeAgentStart(ctx, emptyPromptPayload())
		n.beforeAgentStart(ctx, emptyPromptPayload())
		eq(t, len(readyEvents(n.bus)), 4, "second generation")
	})

	tw(t, f, "gate session-approval is visible to the service", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, askDemo)
		n := newCompNode(t, "demo")
		ctx, _ := makeBaseCtx(t.TempDir(), "sot-session", compCtxOpts{select_: func(_ string, o []string) (string, bool) { return o[1], true }})
		n.sessionStart(ctx)
		n.toolCall(ctx, "demo", map[string]any{"foo": "bar"})
		eq(t, n.service("sot-session").CheckPermission("demo", nil, nil).State, "allow", "the service reads the gate's session rules")
	})
	tw(t, f, "resolves a path-surface query against a deny rule on the supplied path", func(t *testing.T) {
		agent := compEnv(t)
		cwd := t.TempDir()
		target := filepath.Join(cwd, "secrets.env")
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"path": map[string]any{target: "deny"}}})
		n := newCompNode(t)
		n.sessionStart(makeChildCtx(cwd, "svc-path-session"))
		eq(t, n.service("svc-path-session").CheckPermission("path", &target, nil).State, "deny", "state")
	})

	tw(t, f, "does not let an untrusted project's `bash: allow` override global `bash: deny`", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask", "bash": "deny"}})
		cwd := t.TempDir()
		compWriteProjectConfig(t, cwd, map[string]any{"permission": map[string]any{"bash": "allow"}})
		n := newCompNode(t)
		ctx, _ := makeBaseCtx(cwd, "untrusted-session", compCtxOpts{untrusted: true})
		n.sessionStart(ctx)
		eq(t, n.service("untrusted-session").CheckPermission("bash", sp("echo hi"), nil).State, "deny", "global deny survives")
	})
	tw(t, f, "lets a trusted project's `bash: allow` override global `bash: deny`", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "ask", "bash": "deny"}})
		cwd := t.TempDir()
		compWriteProjectConfig(t, cwd, map[string]any{"permission": map[string]any{"bash": "allow"}})
		n := newCompNode(t)
		ctx, _ := makeBaseCtx(cwd, "trusted-session", compCtxOpts{})
		n.sessionStart(ctx)
		eq(t, n.service("trusted-session").CheckPermission("bash", sp("echo hi"), nil).State, "allow", "the trusted project overrides")
	})

	bare := func(t *testing.T, permission map[string]any, session string, files map[string]string, links map[string]string) (*compNode, SessionContext, string) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": permission})
		cwd := t.TempDir()
		for name, content := range files {
			_ = os.WriteFile(filepath.Join(cwd, name), []byte(content), 0o644)
		}
		for name, target := range links {
			_ = os.Symlink(target, filepath.Join(cwd, name))
		}
		n := newCompNode(t, "bash")
		ctx := makeChildCtx(cwd, session)
		n.sessionStart(ctx)
		return n, ctx, cwd
	}
	bashCall := func(n *compNode, ctx SessionContext, command string) ToolCallResult {
		return n.toolCall(ctx, "bash", map[string]any{"command": command})
	}
	tw(t, f, "denies an existing bare filename matching a specific path deny rule", func(t *testing.T) {
		n, ctx, _ := bare(t, map[string]any{"*": "allow", "path": map[string]any{"id_rsa": "deny"}}, "bare-token-session-deny", map[string]string{"id_rsa": "key"}, nil)
		eq(t, bashCall(n, ctx, "cat id_rsa").Block, true, "blocked")
	})
	tw(t, f, "denies an existing bare filename matching a wildcard path deny rule", func(t *testing.T) {
		n, ctx, _ := bare(t, map[string]any{"*": "allow", "path": map[string]any{"*.pem": "deny"}}, "bare-token-session-wildcard", map[string]string{"key.pem": "key"}, nil)
		eq(t, bashCall(n, ctx, "cat key.pem").Block, true, "blocked")
	})
	tw(t, f, "denies a bare symlink whose target matches a path deny rule (#645)", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "path": map[string]any{"*.some.secret": "deny"}}})
		cwd := t.TempDir()
		_ = os.WriteFile(filepath.Join(cwd, ".some.secret"), []byte("s3cret"), 0o644)
		_ = os.Symlink(filepath.Join(cwd, ".some.secret"), filepath.Join(cwd, "a_sym"))
		n := newCompNode(t, "bash")
		ctx := makeChildCtx(cwd, "bare-symlink-session-deny")
		n.sessionStart(ctx)
		eq(t, bashCall(n, ctx, "cat a_sym").Block, true, "blocked")
	})
	tw(t, f, "leaves a bare token that does not match any path rule unaffected", func(t *testing.T) {
		n, ctx, _ := bare(t, map[string]any{"*": "allow", "path": map[string]any{"id_rsa": "deny"}}, "bare-token-session-unaffected", nil, nil)
		eq(t, bashCall(n, ctx, "git status").Block, false, "allowed")
	})
	tw(t, f, "leaves a bare token naming no file unaffected even under a path deny rule (#645)", func(t *testing.T) {
		n, ctx, _ := bare(t, map[string]any{"*": "allow", "path": map[string]any{"id_rsa": "deny"}}, "bare-token-session-absent", nil, nil)
		eq(t, bashCall(n, ctx, "cat id_rsa").Block, false, "allowed")
	})
	tw(t, f, "leaves an existing bare file unrestricted when no explicit path rule matches (#58)", func(t *testing.T) {
		n, ctx, _ := bare(t, map[string]any{"*": "allow", "path": map[string]any{"id_rsa": "deny"}}, "bare-token-session-norule", map[string]string{"README": "docs"}, nil)
		eq(t, bashCall(n, ctx, "cat README").Block, false, "allowed")
	})
	tw(t, f, "denies a bare symlink escaping the working tree under external_directory deny (#645)", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "bash": map[string]any{"cat *": "allow"}, "external_directory": "deny"}})
		cwd, outside := t.TempDir(), t.TempDir()
		secret := filepath.Join(outside, "pi-permission-test-secret")
		_ = os.WriteFile(secret, []byte("test"), 0o644)
		_ = os.Symlink(secret, filepath.Join(cwd, "outside-link"))
		n := newCompNode(t, "bash")
		ctx := makeChildCtx(cwd, "bare-escape-session-deny")
		n.sessionStart(ctx)
		eq(t, bashCall(n, ctx, "cat outside-link").Block, true, "blocked")
	})
	tw(t, f, "denies a path embedded in a long option under external_directory deny (#645)", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, map[string]any{"permission": map[string]any{"*": "allow", "bash": map[string]any{"grep *": "allow"}, "external_directory": "deny"}})
		cwd, outside := t.TempDir(), t.TempDir()
		patterns := filepath.Join(outside, "pi-permission-patterns")
		_ = os.WriteFile(patterns, []byte("secret\n"), 0o644)
		n := newCompNode(t, "bash")
		ctx := makeChildCtx(cwd, "flag-path-session-deny")
		n.sessionStart(ctx)
		eq(t, bashCall(n, ctx, "grep --file="+patterns+" target").Block, true, "blocked")
	})

	tw(t, f, "gives an in-process child its own service, so a sibling's link registration does not collide", func(t *testing.T) {
		compEnv(t)
		parent, child := newCompNode(t), newCompNode(t)
		parent.sessionStart(makeChildCtx(t.TempDir(), "parent-session-keyed"))
		registerChild(t, "child-session-keyed", "parent-session-keyed")
		child.sessionStart(makeChildCtx(t.TempDir(), "child-session-keyed"))
		ps, cs := parent.service("parent-session-keyed"), child.service("child-session-keyed")
		eq(t, ps == cs, false, "distinct services")
		_, err := ps.RegisterAuthorizer("model-judge", deferLink)
		eq(t, err, nil, "parent registers")
		_, err = cs.RegisterAuthorizer("model-judge", deferLink)
		eq(t, err, nil, "the child's own registry has no collision")
	})
	tw(t, f, "announces the node's session id and chain role on permissions:ready", func(t *testing.T) {
		compEnv(t)
		parent, child := newCompNode(t), newCompNode(t)
		pctx, _ := makeBaseCtx(t.TempDir(), "parent-session-ready", compCtxOpts{})
		parent.sessionStart(pctx)
		registerChild(t, "child-session-ready", "parent-session-ready")
		child.sessionStart(makeChildCtx(t.TempDir(), "child-session-ready"))
		pr, cr := readyEvents(parent.bus), readyEvents(child.bus)
		eq(t, len(pr), 1, "parent ready")
		eq(t, pr[0]["sessionId"], "parent-session-ready", "parent id")
		eq(t, pr[0]["adjudicatesLocally"], true, "a UI-bearing parent adjudicates locally")
		eq(t, len(cr), 1, "child ready")
		eq(t, cr[0]["sessionId"], "child-session-ready", "child id")
		eq(t, cr[0]["adjudicatesLocally"], false, "a headless registered child relays")
	})
	tw(t, f, "records a link registered on a relaying child in the review log", func(t *testing.T) {
		agent := compEnv(t)
		parent, child := newCompNode(t), newCompNode(t)
		pctx, _ := makeBaseCtx(t.TempDir(), "parent-session-vacant", compCtxOpts{})
		parent.sessionStart(pctx)
		registerChild(t, "child-session-vacant", "parent-session-vacant")
		child.sessionStart(makeChildCtx(t.TempDir(), "child-session-vacant"))
		_, _ = parent.service("parent-session-vacant").RegisterAuthorizer("model-judge", deferLink)
		_, _ = child.service("child-session-vacant").RegisterAuthorizer("model-judge", deferLink)
		eq(t, compEventCount(compReviewLog(agent), "authorizer_link_vacant"), 1, "exactly the relaying child's vacancy")
	})
	tw(t, f, "removes only its own keyed entry when a node shuts down", func(t *testing.T) {
		compEnv(t)
		parent, child := newCompNode(t), newCompNode(t)
		parent.sessionStart(makeChildCtx(t.TempDir(), "parent-session-teardown"))
		registerChild(t, "child-session-teardown", "parent-session-teardown")
		child.sessionStart(makeChildCtx(t.TempDir(), "child-session-teardown"))
		ps := parent.service("parent-session-teardown")
		child.shutdown()
		eq(t, GetPermissionsService("child-session-teardown") == nil, true, "child removed")
		eq(t, GetPermissionsService("parent-session-teardown") == ps, true, "parent kept")
	})

	tw(t, f, "starts the next same-cwd session with an empty session ruleset", func(t *testing.T) {
		agent := compEnv(t)
		compWriteGlobalConfig(t, agent, askDemo)
		cwd := t.TempDir()
		first := newCompNode(t, "demo")
		fctx, _ := makeBaseCtx(cwd, "switch-session-1", compCtxOpts{select_: func(_ string, o []string) (string, bool) { return o[1], true }})
		first.sessionStart(fctx)
		first.toolCall(fctx, "demo", map[string]any{"foo": "bar"})
		eq(t, first.service("switch-session-1").CheckPermission("demo", nil, nil).State, "allow", "approved for the session")
		first.shutdown()
		second := newCompNode(t, "demo")
		second.sessionStart(makeChildCtx(cwd, "switch-session-2"))
		eq(t, second.service("switch-session-2").CheckPermission("demo", nil, nil).State, "ask", "the grant did not leak")
	})

	custom := "You are my personal coding assistant."
	customEvent := func() (BeforeAgentStartPayload, *apSections) {
		e := makeAPEvent(custom, func(o *PromptOptions, _ *apSections) {
			o.CustomPrompt = custom
			o.ToolSnippets = map[string]string{"read": "Read file contents"}
		})
		return e.payload, e.sections
	}
	tw(t, f, "leaves a root session's custom prompt as Pi built it", func(t *testing.T) {
		compEnv(t)
		n := newCompNode(t, "read")
		ctx, _ := makeBaseCtx(t.TempDir(), "custom-root-session", compCtxOpts{})
		n.sessionStart(ctx)
		payload, sections := customEvent()
		n.beforeAgentStart(ctx, payload)
		eq(t, sections.values, map[string]string{}, "sections")
	})
	tw(t, f, "states a registered child's tools under its custom prompt", func(t *testing.T) {
		compEnv(t)
		parent, child := newCompNode(t, "read"), newCompNode(t, "read")
		pctx, _ := makeBaseCtx(t.TempDir(), "custom-parent-session", compCtxOpts{})
		parent.sessionStart(pctx)
		registerChild(t, "custom-child-session", "custom-parent-session")
		cctx := makeChildCtx(t.TempDir(), "custom-child-session")
		child.sessionStart(cctx)
		payload, sections := customEvent()
		child.beforeAgentStart(cctx, payload)
		eq(t, sections.values["tools"], "- read: Read file contents", "tools")
	})
	_ = strings.TrimSpace
}
