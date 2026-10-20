package pi_permission_system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twins of test/authority/approval-escalator.test.ts. Real timers and the real filesystem, as the original.

// waitForRequestFile waits for the request file ParentAuthorizer.Authorize writes, so the test can respond as the parent session would.
func waitForRequestFile(t *testing.T, requestsDir string) (*jsObject, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(requestsDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") && !strings.Contains(e.Name(), ".tmp") {
				raw, err := os.ReadFile(filepath.Join(requestsDir, e.Name()))
				if err == nil {
					if o, ok := fwParse(t, string(raw)).(*jsObject); ok {
						return o, filepath.Join(requestsDir, e.Name())
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a request file in %s", requestsDir)
	return nil, ""
}

type escalation struct {
	result chan escalationResult
}

type escalationResult struct {
	decision PermissionPromptDecision
	err      error
}

func (e *escalation) await(t *testing.T) PermissionPromptDecision {
	t.Helper()
	select {
	case r := <-e.result:
		if r.err != nil {
			t.Fatalf("authorize failed: %v", r.err)
		}
		return r.decision
	case <-time.After(30 * time.Second):
		t.Fatal("authorize never returned")
		return PermissionPromptDecision{}
	}
}

func authorizeAsync(a *ParentAuthorizer, details *PromptPermissionDetails) *escalation {
	e := &escalation{result: make(chan escalationResult, 1)}
	go func() {
		d, err := a.Authorize(details)
		e.result <- escalationResult{d, err}
	}()
	return e
}

type parentOpts struct {
	forwardingDir string
	registry      ParentChainRegistry
	serving       TargetServingLookup
	timeoutMs     float64
	logger        DebugReviewLogger
}

// alwaysServing is the lookup that reports every target as serving (the non-fast-fail default).
func alwaysServing() TargetServingLookup { return serving(true) }

func makeParentAuthorizer(ctx ForwarderContext, o parentOpts) *ParentAuthorizer {
	if o.serving == nil {
		o.serving = alwaysServing()
	}
	if o.timeoutMs == 0 {
		o.timeoutMs = PermissionForwardingTimeoutMs
	}
	if o.logger == nil {
		o.logger = &fwLogger{}
	}
	return NewParentAuthorizer(ctx, ParentAuthorizerDeps{ForwardingDir: o.forwardingDir, Registry: o.registry, Serving: o.serving,
		GetTimeoutMs: func() float64 { return o.timeoutMs }, Logger: o.logger})
}

// makeLivenessJudge is the real judge over an in-process registry and the heartbeat records under forwardingDir.
func makeLivenessJudge(forwardingDir string, registry ServingLookup, isProcessAlive func(int) bool) *ForwardingLivenessJudge {
	if registry == nil {
		registry = NewServingSessionRegistry()
	}
	return NewForwardingLivenessJudge(ForwardingLivenessJudgeDeps{
		Registry:   registry,
		Heartbeats: NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: forwardingDir, Logger: &fwLogger{}, IsProcessAlive: isProcessAlive}),
	})
}

// publishServingHeartbeat publishes a serving heartbeat for sessionID, as a live parent would.
func publishServingHeartbeat(forwardingDir, sessionID string, pid int) {
	NewServingHeartbeatStore(ServingHeartbeatStoreDeps{ForwardingDir: forwardingDir, Logger: &fwLogger{}, PID: pid}).MarkServing(sessionID)
}

func respond(t *testing.T, dir *fwTempDir, requestID, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir.location.ResponsesDir, requestID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func unavailableDecision(reason string) PermissionPromptDecision {
	return PermissionPromptDecision{State: DecisionDenied, ConfirmationUnavailable: true, DenialReason: &reason, DecidedBy: DecisionSource{Kind: "unavailable", Reason: &reason}}
}

func TestParentAuthorizerTwins(t *testing.T) {
	f := "authority/approval-escalator"
	childRegistry := func(parent string) ParentChainRegistry {
		return fwMakeSubagentRegistry("child-session", &SubagentSessionInfo{ParentSessionID: &parent})
	}
	childContext := func(mut ...func(*ForwarderContext)) ForwarderContext {
		ctx := fwMakeForwarderContext(false, "child-session")
		for _, m := range mut {
			m(&ctx)
		}
		return ctx
	}
	forwardedAsk := func() *PromptPermissionDetails {
		return authDetails(func(d *PromptPermissionDetails) { d.RequestID, d.AgentName, d.ToolName = "perm-child-request", sp("Explore"), sp("bash") })
	}
	// exchangeWith drives one forwarded exchange to completion: escalate, wait for the request file, answer it with response.
	exchangeWith := func(t *testing.T, response string) PermissionPromptDecision {
		t.Helper()
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		e := authorizeAsync(a, authDetails(func(d *PromptPermissionDetails) { d.RequestID = "perm-child-request" }))
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		id, _ := request.str("id")
		respond(t, dir, id, response)
		return e.await(t)
	}
	forwardedVia := func(responder string, decision *DecisionSource) DecisionSource {
		return DecisionSource{Kind: "forwarded", ResponderSessionID: &responder, Decision: decision}
	}

	tw(t, f, "nests the responder's own decider under the forwarding hop", func(t *testing.T) {
		// The reported case: a human at the parent, or the parent's policy? The child's own terminal entry has to answer that.
		got := exchangeWith(t, `{"approved":true,"state":"approved","responderSessionId":"parent-session","decidedBy":{"kind":"user","via":"dialog"}}`)
		eq(t, got.DecidedBy, forwardedVia("parent-session", &DecisionSource{Kind: "user", Via: "dialog"}), "decidedBy")
	})
	tw(t, f, "still names the responding session when an older parent sends no decider", func(t *testing.T) {
		got := exchangeWith(t, `{"approved":true,"state":"approved","responderSessionId":"parent-session"}`)
		eq(t, got.DecidedBy, forwardedVia("parent-session", nil), "decidedBy")
	})
	tw(t, f, "relays the width the responder's human chose for the session grant", func(t *testing.T) {
		// The child records the grant, so the width has to survive the hop or the parent's choice is silently narrowed back (#813).
		got := exchangeWith(t, `{"approved":true,"state":"approved_for_session","sessionGrantWidth":"family","responderSessionId":"parent-session"}`)
		eq(t, got.SessionGrantWidth, SessionGrantWidth("family"), "width")
	})
	tw(t, f, "discards a malformed decider rather than relaying a corrupt one", func(t *testing.T) {
		got := exchangeWith(t, `{"approved":true,"state":"approved","responderSessionId":"parent-session","decidedBy":{"kind":"user","via":"smoke-signal"}}`)
		eq(t, got.DecidedBy, forwardedVia("parent-session", nil), "decidedBy")
	})

	// roundTrip runs one exchange whose request the test inspects before it answers.
	roundTrip := func(t *testing.T, ctx ForwarderContext, details *PromptPermissionDetails, inspect func(request *jsObject)) PermissionPromptDecision {
		t.Helper()
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(ctx, parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		e := authorizeAsync(a, details)
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		inspect(request)
		id, _ := request.str("id")
		respond(t, dir, id, `{"approved":true,"state":"approved","responderSessionId":"parent-session"}`)
		return e.await(t)
	}
	field := func(t *testing.T, o *jsObject, key string, want any) {
		t.Helper()
		got, _ := o.get(key)
		if !fwEqual(got, fwNorm(want)) {
			t.Errorf("%s: got %s, want %s", key, marshalJSON(got, ""), marshalJSON(fwNorm(want), ""))
		}
	}

	tw(t, f, "writes a forwarded request carrying the display fields and resolves with the parent's response", func(t *testing.T) {
		got := roundTrip(t, childContext(), authDetails(func(d *PromptPermissionDetails) {
			d.RequestID, d.AgentName, d.ToolName, d.Command = "perm-child-request", sp("Explore"), sp("bash"), sp("git push")
		}), func(r *jsObject) {
			field(t, r, "targetSessionId", "parent-session")
			field(t, r, "requesterSessionId", "child-session")
			field(t, r, "source", "tool_call")
			field(t, r, "surface", "bash")
			field(t, r, "value", "git push")
		})
		eq(t, got.Approved, true, "approved")
		eq(t, got.State, DecisionApproved, "state")
	})
	tw(t, f, "persists the details' sessionApproval suggestion onto the forwarded request", func(t *testing.T) {
		roundTrip(t, childContext(), authDetails(func(d *PromptPermissionDetails) {
			d.RequestID, d.AgentName, d.ToolName, d.Command = "perm-child-request", sp("Explore"), sp("bash"), sp("git push")
			d.SessionApproval = &ForwardedSessionApproval{Grants: []ApprovalGrant{{Surface: "bash", Pattern: "git *"}}}
		}), func(r *jsObject) {
			got, _ := r.get("sessionApproval")
			if marshalJSON(got, "") != `{"grants":[{"surface":"bash","pattern":"git *"}]}` {
				t.Errorf("sessionApproval: %s", marshalJSON(got, ""))
			}
		})
	})
	tw(t, f, "stamps the child-fixed access intent with requester identity onto the forwarded request", func(t *testing.T) {
		boundary := "/worktree/issue-42/src/foo.ts"
		roundTrip(t, childContext(func(c *ForwarderContext) { c.Cwd = "/worktree/issue-42" }), authDetails(func(d *PromptPermissionDetails) {
			d.RequestID, d.AgentName, d.ToolName, d.Path = "perm-child-request", sp("Explore"), sp("read"), sp("src/foo.ts")
			d.AccessIntent = &ForwardedAccessFacts{Surface: "path", MatchValues: []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts"}, BoundaryValue: &boundary}
		}), func(r *jsObject) {
			// The display fields still ride the same request alongside the structured intent (the #292/#557 contract must not regress).
			field(t, r, "source", "tool_call")
			field(t, r, "surface", "read")
			field(t, r, "value", "src/foo.ts")
			requesterSessionID, _ := r.get("requesterSessionId")
			requesterAgentName, _ := r.get("requesterAgentName")
			intent, _ := r.get("accessIntent")
			want := fwObj("surface", "path", "matchValues", []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts"}, "boundaryValue", boundary,
				"requesterCwd", "/worktree/issue-42", "principal", fwObj("sessionId", requesterSessionID, "agentName", requesterAgentName))
			if !fwEqual(intent, want) {
				t.Errorf("accessIntent: got %s, want %s", marshalJSON(intent, ""), marshalJSON(want, ""))
			}
		})
	})
	tw(t, f, "relays the details' prompt payload onto the forwarded request", func(t *testing.T) {
		payload := authPayload(func(p *PromptPayload) {
			p.Kind = "bash"
			p.Request = PromptRequestFacts{Requester: PromptRequester{AgentName: sp("Explore")}, Surface: "bash", ToolName: sp("bash"), Value: "git push", MatchedPattern: sp("git *")}
			p.Evidence = []PromptEvidence{{Label: "command", Text: "git push"}}
		})
		roundTrip(t, childContext(), authDetails(func(d *PromptPermissionDetails) {
			d.RequestID, d.AgentName, d.ToolName, d.Command, d.Payload = "perm-child-request", sp("Explore"), sp("bash"), sp("git push"), payload
		}), func(r *jsObject) {
			got, _ := r.get("payload")
			if !fwEqual(got, payload.JS()) {
				t.Errorf("payload: %s", marshalJSON(got, ""))
			}
		})
	})
	tw(t, f, "omits accessIntent from the request when the details carry none", func(t *testing.T) {
		roundTrip(t, childContext(), authDetails(func(d *PromptPermissionDetails) { d.RequestID, d.AgentName, d.ToolName = "perm-child-request", sp("Explore"), sp("read") }), func(r *jsObject) {
			if r.has("accessIntent") {
				t.Error("accessIntent present")
			}
		})
	})
	tw(t, f, "omits sessionApproval from the request when the details carry none", func(t *testing.T) {
		roundTrip(t, childContext(), authDetails(func(d *PromptPermissionDetails) { d.RequestID, d.AgentName, d.ToolName = "perm-child-request", sp("Explore"), sp("read") }), func(r *jsObject) {
			if r.has("sessionApproval") {
				t.Error("sessionApproval present")
			}
		})
	})
	tw(t, f, "returns denied when the response marks the request denied", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		e := authorizeAsync(a, authDetails(func(d *PromptPermissionDetails) { d.RequestID, d.AgentName, d.ToolName = "perm-child-request", sp("Explore"), sp("read") }))
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		id, _ := request.str("id")
		respond(t, dir, id, `{"approved":false,"state":"denied","responderSessionId":"parent-session"}`)
		got := e.await(t)
		eq(t, got.Approved, false, "approved")
		eq(t, got.State, DecisionDenied, "state")
	})
	adoptions := func(t *testing.T, requestID string, check func(id string)) {
		t.Helper()
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		e := authorizeAsync(a, authDetails(func(d *PromptPermissionDetails) { d.RequestID = requestID }))
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		id, _ := request.str("id")
		check(id)
		respond(t, dir, id, `{"approved":true,"state":"approved"}`)
		e.await(t)
	}
	tw(t, f, "adopts the requester's request id as the forwarded request id", func(t *testing.T) {
		adoptions(t, "perm-child-request", func(id string) { eq(t, id, "perm-child-request", "id") })
	})
	tw(t, f, "mints a fresh id when the requester's is not filename-safe", func(t *testing.T) {
		adoptions(t, "../../escape", func(id string) {
			if !strings.HasPrefix(id, "perm-") {
				t.Errorf("id %q", id)
			}
		})
	})

	// Abandonment: every path where ParentAuthorizer gives up without a human having ruled must be distinguishable from a user denial.
	tw(t, f, "reports an unresolvable target as unavailable, not user-denied", func(t *testing.T) {
		fwClearHintEnv(t)
		a := makeParentAuthorizer(childContext(), parentOpts{registry: fwMakeSubagentRegistry("child-session", nil)})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision("Could not resolve a parent session to forward this permission request to"), "decision")
	})
	tw(t, f, "reports a self-naming marker as unresolvable and writes no request", func(t *testing.T) {
		// A child's own copy of a subagent extension can overwrite the spawner's marker with the child's own session id (#907).
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "child-session", true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "child-session")
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: fwMakeSubagentRegistry("child-session", nil)})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision("Could not resolve a parent session to forward this permission request to"), "decision")
		entries, _ := os.ReadDir(dir.location.RequestsDir)
		eq(t, len(entries), 0, "requests")
	})
	tw(t, f, "reports unusable forwarding directories as unavailable", func(t *testing.T) {
		fwClearHintEnv(t)
		root := t.TempDir()
		// A file where the forwarding root must be a directory: every mkdir beneath it fails with ENOTDIR.
		forwardingDir := filepath.Join(root, "forwarding")
		if err := os.WriteFile(forwardingDir, []byte("not a directory"), 0o644); err != nil {
			t.Fatal(err)
		}
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: forwardingDir, registry: childRegistry("parent-session")})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision("Permission forwarding directories could not be prepared for session 'parent-session'"), "decision")
	})
	tw(t, f, "reports an unwritable request as unavailable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("a root process writes into a 0500 directory, so the write cannot be denied")
		}
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		// Deny writes into requests/ so the atomic write's temp file fails.
		if err := os.Chmod(dir.location.RequestsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := os.Stat(dir.location.RequestsDir); err == nil {
				_ = os.Chmod(dir.location.RequestsDir, 0o700)
			}
		}()
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision("The forwarded permission request could not be written"), "decision")
		// The directories it created for an exchange that never happened are cleaned up, so the chmod'd directory is already gone.
		if _, err := os.Stat(dir.location.RequestsDir); err == nil {
			t.Error("the requests directory is still there")
		}
	})
	tw(t, f, "reports an unreadable response as unavailable, not as the parent's denial", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		e := authorizeAsync(a, forwardedAsk())
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		id, _ := request.str("id")
		respond(t, dir, id, "{ not json")
		eq(t, e.await(t), unavailableDecision("The parent session's permission response could not be read"), "decision")
	})
	tw(t, f, "reports an unanswered request as timed out, not user-denied", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session"), timeoutMs: 400})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision("Session 'parent-session' did not answer within 0.4s"), "decision")
	})
	notServing := "Session 'parent-session' is not serving forwarded permission requests"
	tw(t, f, "abandons quickly when an in-process target is not serving", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		// Nobody has marked themselves as serving.
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session"),
			serving: makeLivenessJudge(dir.forwardingDir, nil, nil), timeoutMs: 60_000})
		started := time.Now()
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision(notServing), "decision")
		if time.Since(started) >= 60*time.Second {
			t.Error("waited out the timeout")
		}
	})
	waitsWhileServing := func(t *testing.T, a *ParentAuthorizer, dir *fwTempDir) {
		t.Helper()
		e := authorizeAsync(a, forwardedAsk())
		request, _ := waitForRequestFile(t, dir.location.RequestsDir)
		// Well past the unserved grace window: a serving target must not be abandoned no matter how long the human deliberates.
		time.Sleep((PermissionForwardingServingGraceMs + 250) * time.Millisecond)
		id, _ := request.str("id")
		respond(t, dir, id, `{"approved":true,"state":"approved","responderSessionId":"parent-session"}`)
		got := e.await(t)
		eq(t, got.Approved, true, "approved")
		eq(t, got.State, DecisionApproved, "state")
	}
	tw(t, f, "keeps waiting while the in-process target is serving", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		registry := NewServingSessionRegistry()
		registry.MarkServing("parent-session")
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session"),
			serving: makeLivenessJudge(dir.forwardingDir, registry, nil), timeoutMs: 60_000})
		waitsWhileServing(t, a, dir)
	})
	tw(t, f, "abandons quickly when an out-of-process target published no heartbeat", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "parent-session")
		// No registry entry, so the target resolves from the environment: a parent in another process, reachable only through the
		// filesystem.
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: fwMakeSubagentRegistry("child-session", nil),
			serving: makeLivenessJudge(dir.forwardingDir, nil, nil), timeoutMs: 60_000})
		started := time.Now()
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision(notServing), "decision")
		if time.Since(started) >= 60*time.Second {
			t.Error("waited out the timeout")
		}
	})
	tw(t, f, "abandons quickly when an out-of-process target's process is gone", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "parent-session")
		publishServingHeartbeat(dir.forwardingDir, "parent-session", 4242)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: fwMakeSubagentRegistry("child-session", nil),
			serving: makeLivenessJudge(dir.forwardingDir, nil, func(int) bool { return false }), timeoutMs: 60_000})
		eq(t, mustDecision(t)(a.Authorize(forwardedAsk())), unavailableDecision(notServing), "decision")
	})
	tw(t, f, "keeps waiting while an out-of-process target's heartbeat is fresh", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "parent-session")
		publishServingHeartbeat(dir.forwardingDir, "parent-session", 0)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: fwMakeSubagentRegistry("child-session", nil),
			serving: makeLivenessJudge(dir.forwardingDir, nil, nil), timeoutMs: 60_000})
		waitsWhileServing(t, a, dir)
	})
	tw(t, f, "records which channel answered and what it saw when it gives up", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		t.Setenv("PI_SUBAGENT_PARENT_SESSION", "parent-session")
		publishServingHeartbeat(dir.forwardingDir, "other-parent", 0)
		logger := &fwLogger{}
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: fwMakeSubagentRegistry("child-session", nil),
			serving: makeLivenessJudge(dir.forwardingDir, nil, nil), timeoutMs: 60_000, logger: logger})
		mustDecision(t)(a.Authorize(forwardedAsk()))
		want := fwObj("requesterSessionId", "child-session", "targetSessionId", "parent-session", "servingChannel", "heartbeat", "servingState", "absent",
			"servingSessionIds", []string{"other-parent"})
		if !logger.reviewMatching("forwarded_permission.no_serving_session", want) {
			t.Errorf("not recorded: %v", reviewEvents(logger))
		}
	})
	tw(t, f, "deletes the request it abandoned so the parent cannot answer it later", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		a := makeParentAuthorizer(childContext(), parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session"), timeoutMs: 400})
		mustDecision(t)(a.Authorize(forwardedAsk()))
		if _, err := os.Stat(dir.location.RequestsDir); err == nil {
			t.Error("the requests directory is still there")
		}
	})

	// Not in the original: a session manager that throws while the requester is identified rejects the ask, writing nothing.
	t.Run("a failing session manager rejects the ask and writes no request", func(t *testing.T) {
		fwClearHintEnv(t)
		dir := fwCreateTempDir(t, "parent-session", true)
		ctx := childContext()
		ctx.SessionManager = &fwSessionManager{sessionID: fwID("child-session"), sessionDir: fwID(""),
			entries: func() ([]SessionEntryView, error) { return nil, os.ErrPermission }}
		a := makeParentAuthorizer(ctx, parentOpts{forwardingDir: dir.forwardingDir, registry: childRegistry("parent-session")})
		if _, err := a.Authorize(forwardedAsk()); err == nil {
			t.Error("no error")
		}
		entries, _ := os.ReadDir(dir.location.RequestsDir)
		eq(t, len(entries), 0, "requests")
	})
}
