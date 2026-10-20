package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Twins of test/authority/forwarded-request-server.test.ts.

var (
	fwDecidedByHuman      = DecisionSource{Kind: "user", Via: "dialog"}
	fwDecidedByAuthorizer = DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: fwSP("reads outside the project")}
)

type fwPolicyFake struct {
	mu    sync.Mutex
	fn    func(ForwardedAccessIntent) PermissionCheckResult
	calls []ForwardedAccessIntent
}

func (p *fwPolicyFake) Resolve(intent ForwardedAccessIntent) PermissionCheckResult {
	p.mu.Lock()
	p.calls = append(p.calls, intent)
	p.mu.Unlock()
	return p.fn(intent)
}

func fwPolicyReturning(result PermissionCheckResult) *fwPolicyFake {
	return &fwPolicyFake{fn: func(ForwardedAccessIntent) PermissionCheckResult { return result }}
}

// fwCheck is makeCheckResult: a PermissionCheckResult with the fixture defaults (toolName read, origin builtin).
func fwCheck(state string, edit ...func(*PermissionCheckResult)) PermissionCheckResult {
	r := PermissionCheckResult{State: state, ToolName: "read", Origin: "builtin"}
	for _, e := range edit {
		e(&r)
	}
	return r
}

type fwEscalatorFake struct {
	mu    sync.Mutex
	fn    func(*PromptPermissionDetails) (PermissionPromptDecision, error)
	calls []*PromptPermissionDetails
}

func (e *fwEscalatorFake) Escalate(d *PromptPermissionDetails) (PermissionPromptDecision, error) {
	e.mu.Lock()
	e.calls = append(e.calls, d)
	e.mu.Unlock()
	return e.fn(d)
}

func (e *fwEscalatorFake) last(t *testing.T) *PromptPermissionDetails {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) == 0 {
		t.Fatal("no ask was escalated")
	}
	return e.calls[len(e.calls)-1]
}

func fwEscalatorAnswering(d PermissionPromptDecision) *fwEscalatorFake {
	return &fwEscalatorFake{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) { return d, nil }}
}

func fwApprove(state PermissionDecisionState, extra ...func(*PermissionPromptDecision)) PermissionPromptDecision {
	d := PermissionPromptDecision{Approved: true, State: state, DecidedBy: fwDecidedByHuman}
	for _, e := range extra {
		e(&d)
	}
	return d
}

type fwBroadcasterFake struct {
	mu     sync.Mutex
	events []PermissionDecisionEvent
}

func (b *fwBroadcasterFake) EmitDecision(e PermissionDecisionEvent) {
	b.mu.Lock()
	b.events = append(b.events, e)
	b.mu.Unlock()
}

type fwRecorderFake struct{ approvals []*SessionApproval }

func (r *fwRecorderFake) RecordSessionApproval(a *SessionApproval) {
	r.approvals = append(r.approvals, a)
}

// fwServerEnv is the server under test with its fakes. makeServerDeps' defaults: a policy deferring to escalation (ask) and an
// approving escalator.
type fwServerEnv struct {
	t           *testing.T
	temp        *fwTempDir
	logger      *fwLogger
	policy      *fwPolicyFake
	escalator   *fwEscalatorFake
	broadcaster *fwBroadcasterFake
	recorder    SessionApprovalRecorder
	registry    ParentChainRegistry
}

func fwNewServerEnv(t *testing.T, createResponsesDir bool) *fwServerEnv {
	t.Helper()
	return &fwServerEnv{
		t:           t,
		temp:        fwCreateTempDir(t, "parent-session", createResponsesDir),
		logger:      &fwLogger{},
		policy:      fwPolicyReturning(fwCheck("ask")),
		escalator:   fwEscalatorAnswering(fwApprove(DecisionApproved)),
		broadcaster: &fwBroadcasterFake{},
		recorder:    &fwRecorderFake{},
	}
}

func (e *fwServerEnv) server() *ForwardedRequestServer {
	return NewForwardedRequestServer(ForwardedRequestServerDeps{
		ForwardingDir: e.temp.forwardingDir, Logger: e.logger, Policy: e.policy, Escalator: e.escalator,
		Broadcaster: e.broadcaster, Recorder: e.recorder, Registry: e.registry,
	})
}

// process drains the inbox under the serving context every case uses.
func (e *fwServerEnv) process() {
	e.t.Helper()
	e.server().ProcessInbox(fwMakeForwarderContext(true, "parent-session"))
}

func (e *fwServerEnv) events() []PermissionDecisionEvent {
	e.broadcaster.mu.Lock()
	defer e.broadcaster.mu.Unlock()
	return append([]PermissionDecisionEvent(nil), e.broadcaster.events...)
}

func fwReq(edits ...func(*ForwardedPermissionRequest)) []func(*ForwardedPermissionRequest) {
	return edits
}

func fwWith(id string, source, surface, value string) func(*ForwardedPermissionRequest) {
	return func(r *ForwardedPermissionRequest) {
		r.ID = id
		if source != "" {
			r.Source = source
		}
		if surface != "" {
			r.Surface = DisplayStr(surface)
		}
		if value != "" {
			r.Value = DisplayStr(value)
		}
	}
}

func fwWithIntent(intent ForwardedAccessIntent) func(*ForwardedPermissionRequest) {
	return func(r *ForwardedPermissionRequest) { r.AccessIntent = &intent }
}

func fwWithGrants(grants ...ApprovalGrant) func(*ForwardedPermissionRequest) {
	return func(r *ForwardedPermissionRequest) { r.SessionApproval = &ForwardedSessionApproval{Grants: grants} }
}

func fwNoPayload(r *ForwardedPermissionRequest) { r.Payload = nil }

func fwBashIntent(values ...string) ForwardedAccessIntent {
	return fwMakeAccessIntent(func(i *ForwardedAccessIntent) { i.MatchValues = values })
}

// escalateForwardedAsk drives one forwarded ask to escalation and returns the details the server built.
func fwEscalateForwardedAsk(t *testing.T, edits ...func(*ForwardedPermissionRequest)) *PromptPermissionDetails {
	t.Helper()
	env := fwNewServerEnv(t, true)
	env.temp.writeRequest(edits...)
	env.process()
	return env.escalator.last(t)
}

func TestProcessInboxRecordedAuthority(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tw(t, f, "auto-approves and writes an approved response when the serving policy allows", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		intent := fwBashIntent("git status")
		env.temp.writeRequest(fwWith("req-allow", "tool_call", "bash", "git status"), fwWithIntent(intent))
		env.policy = fwPolicyReturning(fwCheck("allow", func(r *PermissionCheckResult) { r.MatchedPattern = fwSP("git *"); r.Origin = "global" }))
		env.process()
		if len(env.policy.calls) != 1 || !reflect.DeepEqual(env.policy.calls[0], intent) {
			t.Fatalf("policy calls %+v", env.policy.calls)
		}
		if len(env.escalator.calls) != 0 {
			t.Fatal("escalated")
		}
		// The serving node's own rule decided, and the response names it: the child's log entry would otherwise say only that the
		// parent approved.
		want := fwObj("approved", true, "state", "approved", "decidedBy", fwObj("kind", "rule", "surface", "bash", "pattern", "git *", "origin", "global"))
		if got := env.temp.readResponse("req-allow"); !fwMatch(got, want) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
		if !env.logger.reviewMatching("forwarded_permission.auto_approved", fwObj("requestId", "req-allow", "decidedBy", fwObj("kind", "rule"))) {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
	tw(t, f, "auto-denies and writes a denied response when the serving policy denies", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		intent := fwBashIntent("rm -rf /")
		env.temp.writeRequest(fwWith("req-deny", "tool_call", "bash", "rm -rf /"), fwWithIntent(intent))
		env.policy = fwPolicyReturning(fwCheck("deny", func(r *PermissionCheckResult) { r.MatchedPattern = fwSP("rm *"); r.Origin = "project" }))
		env.process()
		if len(env.policy.calls) != 1 || !reflect.DeepEqual(env.policy.calls[0], intent) || len(env.escalator.calls) != 0 {
			t.Fatal("policy or escalator calls wrong")
		}
		response := env.temp.readResponse("req-deny")
		want := fwObj("approved", false, "state", "denied", "decidedBy", fwObj("kind", "rule", "surface", "bash", "pattern", "rm *", "origin", "project"))
		if !fwMatch(response, want) {
			t.Fatalf("%s", marshalJSON(response, ""))
		}
		// A rule with no reason carries none; a subset match cannot assert a key's absence, so the state above is not enough alone.
		if response.has("denialReason") {
			t.Fatal("denialReason present")
		}
		if !env.logger.reviewMatching("forwarded_permission.auto_denied", fwObj("requestId", "req-deny", "decidedBy", fwObj("kind", "rule"))) {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
	tw(t, f, "carries the denying rule's own reason onto the response", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-deny-reason", "tool_call", "bash", "git push --force"), fwWithIntent(fwBashIntent("git push --force")))
		env.policy = fwPolicyReturning(fwCheck("deny", func(r *PermissionCheckResult) {
			r.MatchedPattern = fwSP("git push --force*")
			r.Origin = "project"
			r.Reason = fwSP("force pushes are blocked")
		}))
		env.process()
		// The requesting session relays this to its own agent, so the operator's explanation reaches the caller instead of stopping
		// at the serving node.
		want := fwObj("approved", false, "state", "denied_with_reason", "denialReason", "force pushes are blocked",
			"decidedBy", fwObj("kind", "rule", "pattern", "git push --force*"))
		if got := env.temp.readResponse("req-deny-reason"); !fwMatch(got, want) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "relays the escalated decision's own decider onto the response", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(func(r *ForwardedPermissionRequest) { r.ID = "req-human"; r.AccessIntent = nil })
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApproved))
		env.process()
		// This is the reported case: the child could not tell a human approval at the parent from the parent's policy
		// auto-approving on its behalf.
		if got := env.temp.readResponse("req-human"); !fwMatch(got, fwObj("decidedBy", fwObj("kind", "user", "via", "dialog"))) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "attributes a failed escalation to the error, not to a denial anyone made", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(func(r *ForwardedPermissionRequest) { r.ID = "req-boom" })
		env.escalator = &fwEscalatorFake{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) {
			return PermissionPromptDecision{}, errors.New("prompt exploded")
		}}
		env.process()
		want := fwObj("approved", false, "state", "denied", "decidedBy", fwObj("kind", "gate_error", "reason", "prompt exploded"))
		if got := env.temp.readResponse("req-boom"); !fwMatch(got, want) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "escalates an ask through the AskEscalator with the forwarded provenance details", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		intent := fwBashIntent("git push")
		bash := fwMakePromptPayload()
		bash.Kind = "bash"
		bash.Request.Surface, bash.Request.Value = "bash", "git push"
		bash.Request.ToolName = fwSP("bash")
		env.temp.writeRequest(fwWith("req-ask", "tool_call", "bash", "git push"), fwWithIntent(intent), func(r *ForwardedPermissionRequest) { r.Payload = &bash })
		env.policy = fwPolicyReturning(fwCheck("ask"))
		env.process()
		if len(env.policy.calls) != 1 || !reflect.DeepEqual(env.policy.calls[0], intent) {
			t.Fatalf("policy calls %+v", env.policy.calls)
		}
		want := &PromptPermissionDetails{
			RequestID: "req-ask", Source: "tool_call", AgentName: fwSP("Explore"), Surface: fwSP("bash"), Value: fwSP("git push"),
			Forwarding:   &ForwardedAskProvenance{RequesterAgentName: fwSP("Explore"), RequesterSessionID: fwSP("child-session")},
			AccessIntent: &ForwardedAccessFacts{Surface: "bash", MatchValues: []string{"git push"}},
			Payload: PromptPayload{
				Kind: "bash",
				Request: PromptRequestFacts{
					Requester: PromptRequester{AgentName: fwSP("Explore"), Forwarded: true, SessionID: fwSP("child-session")},
					Surface:   "bash", ToolName: fwSP("bash"), Value: "git push",
				},
				Evidence: []PromptEvidence{}, Annotations: []PromptAnnotation{},
			},
		}
		if got := env.escalator.last(t); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		if got := env.temp.readResponse("req-ask"); !fwMatch(got, fwObj("approved", true, "state", "approved")) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "keeps requester cwd and principal out of the escalated payload (#635)", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-ask", "", "bash", "git push"), fwWithIntent(fwMakeAccessIntent(func(i *ForwardedAccessIntent) {
			i.RequesterCwd = "/child/cwd"
			i.Principal = ForwardedPrincipal{SessionID: "child-session", AgentName: "Explore"}
		})))
		env.process()
		// The payload is a disclosure boundary too: requester identity reaches an authorizer through details.forwarding, never
		// smuggled in as evidence.
		serialized := marshalJSON(env.escalator.last(t).Payload.JS(), "")
		if strings.Contains(serialized, "/child/cwd") || strings.Contains(serialized, "principal") {
			t.Fatalf("leaked: %s", serialized)
		}
	})
	tw(t, f, "floors a request with no fields at all (fully legacy) to escalation without consulting the policy", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		// Legacy / version-skew request: no source/surface/value/accessIntent.
		env.temp.writeRequest(func(r *ForwardedPermissionRequest) { r.ID = "req-legacy" })
		env.policy = fwPolicyReturning(fwCheck("allow"))
		env.process()
		got := env.escalator.last(t)
		if len(env.policy.calls) != 0 || got.RequestID != "req-legacy" || got.Source != "tool_call" || got.Surface != nil || got.Value != nil {
			t.Fatalf("policy calls %d, details %+v", len(env.policy.calls), got)
		}
	})
	tw(t, f, "floors a version-skew request with display fields but no accessIntent to escalation without consulting the policy", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		// An older child populated the display fields but never computed the structured intent (ADR 0008 section 4: accessIntent is
		// the sole resolution path, a request missing it floors to ask, never a silent grant).
		env.temp.writeRequest(fwWith("req-skew", "tool_call", "bash", "git push"))
		env.policy = fwPolicyReturning(fwCheck("allow"))
		env.process()
		got := env.escalator.last(t)
		if len(env.policy.calls) != 0 || got.RequestID != "req-skew" || got.Surface == nil || *got.Surface != "bash" || got.Value == nil || *got.Value != "git push" {
			t.Fatalf("policy calls %d, details %+v", len(env.policy.calls), got)
		}
	})
	tw(t, f, "denies when the escalator rejects", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-throw", "", "bash", "git push"), fwWithIntent(fwBashIntent("git push")))
		env.policy = fwPolicyReturning(fwCheck("ask"))
		env.escalator = &fwEscalatorFake{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) {
			return PermissionPromptDecision{}, errors.New("ui gone")
		}}
		env.process()
		if got := env.temp.readResponse("req-throw"); !fwMatch(got, fwObj("approved", false, "state", "denied")) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
		found := false
		for _, c := range env.logger.reviews() {
			if m, _ := c.details.str("message"); c.event == "permission_forwarding.error" && strings.Contains(m, "escalate") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
}

func TestProcessInboxAccessFacts(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tw(t, f, "carries the request's access facts onto the escalated ask details", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t,
			// The display projection is the child's tool name, which is what the UI shows, never the gate surface the rule fired on.
			fwWith("req-path-facts", "tool_call", "write", "/worktree/issue-42/src/foo.ts"),
			fwWithIntent(fwMakeAccessIntent(func(i *ForwardedAccessIntent) {
				i.Surface = "path"
				i.MatchValues = []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts", "/canonical/src/foo.ts"}
				i.BoundaryValue = fwSP("/canonical/src/foo.ts")
			})))
		// Exactly the three fact fields: requesterCwd and principal stay on the wire object and never reach an authorizer.
		want := &ForwardedAccessFacts{Surface: "path", MatchValues: []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts", "/canonical/src/foo.ts"}, BoundaryValue: fwSP("/canonical/src/foo.ts")}
		if !reflect.DeepEqual(details.AccessIntent, want) {
			t.Fatalf("%+v", details.AccessIntent)
		}
	})
	tw(t, f, "omits accessIntent entirely for a version-skew request that carried none", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t, fwWith("req-skew-facts", "tool_call", "bash", "git push"))
		// Absence, not an explicit undefined: the delegation envelope's `accessIntent?.surface ?? surface` fallback reads the
		// display surface only when the key is genuinely absent.
		if details.AccessIntent != nil {
			t.Fatalf("%+v", details.AccessIntent)
		}
	})
}

func TestProcessInboxChildPayload(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tw(t, f, "escalates the child's own payload with the requester re-stamped as forwarded", func(t *testing.T) {
		childPayload := fwBashPayload()
		childPayload.Evidence = []PromptEvidence{{Label: "full command", Text: "git push --force"}}
		details := fwEscalateForwardedAsk(t,
			func(r *ForwardedPermissionRequest) {
				r.ID, r.RequesterAgentName, r.RequesterSessionID = "req-child-payload", "Explore", "child-session"
				r.Payload = &childPayload
			}, fwWith("req-child-payload", "tool_call", "bash", "git push"))
		// The child's kind and facts pass through untouched; only the requester is re-stamped: the serving node is the only party
		// that knows the ask arrived over the wire, and the request's own provenance is authoritative (#292).
		want := childPayload
		want.Request.Requester = PromptRequester{AgentName: fwSP("Explore"), Forwarded: true, SessionID: fwSP("child-session")}
		if !reflect.DeepEqual(details.Payload, want) {
			t.Fatalf("got %+v\nwant %+v", details.Payload, want)
		}
	})
	tw(t, f, "escalates a degraded forwarded payload for a request carrying none", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t, func(r *ForwardedPermissionRequest) {
			r.ID, r.RequesterAgentName, r.RequesterSessionID = "req-skew-payload", "scout", "child-session"
		}, fwWith("req-skew-payload", "tool_call", "read", "/tmp/x"), fwNoPayload)
		// kind "forwarded" now means exactly one thing: this ask arrived without a payload, so it is rendered from the display
		// fields it does carry.
		want := PromptPayload{
			Kind: "forwarded",
			Request: PromptRequestFacts{
				Requester: PromptRequester{AgentName: fwSP("scout"), Forwarded: true, SessionID: fwSP("child-session")},
				Surface:   "read", Value: "/tmp/x",
			},
			Evidence: []PromptEvidence{}, Annotations: []PromptAnnotation{},
		}
		if !reflect.DeepEqual(details.Payload, want) {
			t.Fatalf("got %+v\nwant %+v", details.Payload, want)
		}
	})
}

func TestProcessInboxBoundedDelegation(t *testing.T) {
	const f = "authority/forwarded-request-server"
	query, log := PermissionQuery(nopQuery{}), AuthorizerLog(nopAuthLog{})
	allowingLink := func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
		return AuthorizerVerdict{Kind: "allow"}, nil
	}
	tw(t, f, "caps a link's allow on a forwarded path ask to defer", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t, fwWith("req-envelope-path", "tool_call", "write", "/worktree/issue-42/.ssh/config"),
			fwWithIntent(fwMakeAccessIntent(func(i *ForwardedAccessIntent) {
				i.Surface = "path"
				i.MatchValues = []string{"/worktree/issue-42/.ssh/config"}
				i.BoundaryValue = fwSP("/worktree/issue-42/.ssh/config")
			})))
		// The gate surface, not the displayed tool name, decides exclusion, so a forwarded path ask is capped exactly like the same
		// ask made locally.
		if v, _ := EncloseInDelegationEnvelope(allowingLink)(details, query, log); v != (AuthorizerVerdict{Kind: "defer"}) {
			t.Fatalf("%+v", v)
		}
	})
	tw(t, f, "passes a link's allow on a forwarded bash ask through", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t, fwWith("req-envelope-bash", "tool_call", "bash", "npm test"), fwWithIntent(fwBashIntent("npm test")))
		if v, _ := EncloseInDelegationEnvelope(allowingLink)(details, query, log); v != (AuthorizerVerdict{Kind: "allow"}) {
			t.Fatalf("%+v", v)
		}
	})
}

func TestProcessInboxServingChain(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tskip(t, f, "a link registered on the serving node decides the forwarded ask before its terminal",
		"drives the real AuthorizerSelection, AuthorizerRegistry and PermissionPrompter (authority lane, not ported here); the server's own part, handing the ask details to its AskEscalator and relaying the answer, is covered by the escalation twins")
}

func TestProcessInboxGrantScope(t *testing.T) {
	const f = "authority/forwarded-request-server"
	gitGrant := ApprovalGrant{Surface: "bash", Pattern: "git *"}
	rulePairs := func(rs *SessionRules) [][2]string {
		out := [][2]string{}
		for _, r := range rs.GetRuleset() {
			out = append(out, [2]string{r.Surface, r.Pattern})
		}
		return out
	}
	tw(t, f, "records a whole-session grant into the serving recorder and translates the response to a plain approve", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-whole", "tool_call", "bash", "git push"), fwWithIntent(fwBashIntent("git push")), fwWithGrants(gitGrant))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForServingSess))
		recorder := &fwRecorderFake{}
		env.recorder = recorder
		env.process()
		if len(recorder.approvals) != 1 || !reflect.DeepEqual(recorder.approvals[0].Grants, []ApprovalGrant{gitGrant}) {
			t.Fatalf("%+v", recorder.approvals)
		}
		// Translated: the child receives a plain approve and records nothing. The translation rewrites the scope, never the decider.
		want := fwObj("approved", true, "state", "approved", "decidedBy", fwObj("kind", "user", "via", "dialog"))
		if got := env.temp.readResponse("req-whole"); !fwMatch(got, want) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "records every grant, each on the surface the child proved for it", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-mixed", "tool_call", "external_directory", "cat /outside/a.ts > /elsewhere/b.ts"),
			fwWithIntent(fwBashIntent("/outside/a.ts")),
			fwWithGrants(ApprovalGrant{"external_directory_read", "/outside/*"}, ApprovalGrant{"external_directory_write", "/elsewhere/*"}))
		rules := &SessionRules{}
		env.recorder = rules
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForServingSess))
		env.process()
		want := [][2]string{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/elsewhere/*"}}
		if !reflect.DeepEqual(rulePairs(rules), want) {
			t.Fatalf("%v", rulePairs(rules))
		}
	})
	tw(t, f, "records a whole-session grant at the width the human chose", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-wide", "tool_call", "external_directory", "echo hi > /outside/out.txt"),
			fwWithIntent(fwBashIntent("/outside/out.txt")), fwWithGrants(ApprovalGrant{"external_directory_write", "/outside/*"}))
		rules := &SessionRules{}
		env.recorder = rules
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForServingSess, func(d *PermissionPromptDecision) { d.SessionGrantWidth = "family" }))
		env.process()
		// The family surface sugar-expands onto both members, so the serving node's later read of the same directory resolves
		// without a prompt.
		want := [][2]string{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/outside/*"}}
		if !reflect.DeepEqual(rulePairs(rules), want) {
			t.Fatalf("%v", rulePairs(rules))
		}
	})
	tw(t, f, "carries the chosen width back to the child on the response", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-width-wire", "tool_call", "external_directory", "echo hi > /outside/out.txt"),
			fwWithIntent(fwBashIntent("/outside/out.txt")), fwWithGrants(ApprovalGrant{"external_directory_write", "/outside/*"}))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForSession, func(d *PermissionPromptDecision) { d.SessionGrantWidth = "family" }))
		env.process()
		// A subagent-scoped grant is recorded by the child, so the width has to reach it or the child records the narrow grant the
		// parent overrode.
		want := fwObj("approved", true, "state", "approved_for_session", "sessionGrantWidth", "family")
		if got := env.temp.readResponse("req-width-wire"); !fwMatch(got, want) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "offers the request's sessionApproval to the escalated dialog details", func(t *testing.T) {
		details := fwEscalateForwardedAsk(t, fwWith("req-scope-details", "tool_call", "bash", "git push"), fwWithIntent(fwBashIntent("git push")), fwWithGrants(gitGrant))
		if !reflect.DeepEqual(details.SessionApproval, &ForwardedSessionApproval{Grants: []ApprovalGrant{gitGrant}}) {
			t.Fatalf("%+v", details.SessionApproval)
		}
	})
	tw(t, f, "passes a subagent-only grant through untouched without recording on the serving node", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-subagent", "tool_call", "bash", "git push"), fwWithIntent(fwBashIntent("git push")), fwWithGrants(gitGrant))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForSession))
		recorder := &fwRecorderFake{}
		env.recorder = recorder
		env.process()
		if len(recorder.approvals) != 0 {
			t.Fatal("recorded on the serving node")
		}
		// Passed through: the child records its own pattern (today's behavior).
		if got := env.temp.readResponse("req-subagent"); !fwMatch(got, fwObj("approved", true, "state", "approved_for_session")) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
}

func TestProcessInboxOneHopCanary(t *testing.T) {
	const f = "authority/forwarded-request-server"
	oneHop := func(env *fwServerEnv) bool {
		return fwHasWarningWith(env, "one-hop")
	}
	tw(t, f, "warns when the requester is a registered subagent whose parent is not this serving session", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-hop", "", "bash", "git push"))
		env.registry = fwMakeSubagentRegistry("child-session", &SubagentSessionInfo{ParentSessionID: fwSP("some-other-session")})
		env.process()
		if !oneHop(env) {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
	tw(t, f, "stays silent for an unregistered (external file-based) requester", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-ext", "", "bash", "git push"))
		env.registry = fwMakeSubagentRegistry("child-session", nil) // no entry
		env.process()
		if env.logger.reviewMatching("permission_forwarding.warning", nil) {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
	tw(t, f, "stays silent for a registered one-hop child whose parent is this serving session", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-ok", "", "bash", "git push"))
		env.registry = fwMakeSubagentRegistry("child-session", &SubagentSessionInfo{ParentSessionID: fwSP("parent-session")})
		env.process()
		if env.logger.reviewMatching("permission_forwarding.warning", nil) {
			t.Fatalf("%+v", env.logger.reviews())
		}
	})
}

func fwHasWarningWith(env *fwServerEnv, fragment string) bool {
	for _, c := range env.logger.reviews() {
		if m, _ := c.details.str("message"); c.event == "permission_forwarding.warning" && strings.Contains(m, fragment) {
			return true
		}
	}
	return false
}

func TestProcessInboxMechanics(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tw(t, f, "recreates a missing responses/ directory and still writes the response", func(t *testing.T) {
		// Simulate the race: requests/ exists with a pending file, but responses/ was removed by a concurrent cleanup pass (#398).
		env := fwNewServerEnv(t, false)
		env.temp.writeRequest(fwWith("req-race", "", "bash", "cat x"), fwWithIntent(fwBashIntent("cat x")))
		env.policy = fwPolicyReturning(fwCheck("allow"))
		env.process()
		if env.logger.reviewMatching("permission_forwarding.error", nil) {
			t.Fatalf("%+v", env.logger.reviews())
		}
		if got := env.temp.readResponse("req-race"); !fwMatch(got, fwObj("approved", true, "state", "approved")) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "ignores and deletes a request targeting a different session", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-mismatch", "", "bash", "git push"), fwWithIntent(fwBashIntent("git push")), func(r *ForwardedPermissionRequest) { r.TargetSessionID = "other-session" })
		env.policy = fwPolicyReturning(fwCheck("allow"))
		env.process()
		if len(env.policy.calls) != 0 {
			t.Fatal("policy consulted")
		}
		// The request is deleted (the original's title says so; its body only checks the policy).
		if pathExists(filepath.Join(env.temp.location.RequestsDir, "req-mismatch.json")) {
			t.Fatal("request not deleted")
		}
	})
}

func TestProcessInboxTerminalDecisionBroadcast(t *testing.T) {
	const f = "authority/forwarded-request-server"
	forwarding := &ForwardedPromptContext{RequesterAgentName: fwSP("Explore"), RequesterSessionID: fwSP("child-session")}
	single := func(t *testing.T, env *fwServerEnv) PermissionDecisionEvent {
		t.Helper()
		events := env.events()
		if len(events) != 1 {
			t.Fatalf("%d events: %+v", len(events), events)
		}
		return events[0]
	}
	tw(t, f, "broadcasts an approval carrying the ask's id, projection, and provenance", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-ask", "tool_call", "bash", "git push"))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApproved))
		env.process()
		// Asserted whole: the bus is the narrowest renderer, so a field added here later (decidedBy above all, #726) must be a
		// deliberate contract change rather than something that leaks in with a spread.
		want := PermissionDecisionEvent{RequestID: "req-ask", Surface: "bash", Value: "git push", AgentName: fwSP("Explore"), Result: "allow",
			Resolution: "user_approved", Forwarding: forwarding}
		if got := single(t, env); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
	})
	tw(t, f, "broadcasts a denial as user_denied", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-deny", "", "bash", "rm -rf /"))
		env.escalator = fwEscalatorAnswering(PermissionPromptDecision{Approved: false, State: DecisionDenied, DecidedBy: fwDecidedByHuman})
		env.process()
		if got := single(t, env); got.RequestID != "req-deny" || got.Result != "deny" || got.Resolution != "user_denied" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "broadcasts the human's grant scope, not the scope written to the wire", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-serving-grant", "", "bash", "git push"), fwWithGrants(ApprovalGrant{"bash", "git *"}))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForServingSess))
		env.process()
		// The grant-scope translation rewrites the response to a plain approved so the child records nothing; the broadcast still
		// reports what the human actually chose.
		if got := single(t, env); got.Result != "allow" || got.Resolution != "user_approved_for_session" {
			t.Fatalf("%+v", got)
		}
		if got := env.temp.readResponse("req-serving-grant"); !fwMatch(got, fwObj("state", "approved")) {
			t.Fatalf("%s", marshalJSON(got, ""))
		}
	})
	tw(t, f, "broadcasts a subagent-scoped grant as user_approved_for_session", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-child-grant", "", "bash", "git push"))
		env.escalator = fwEscalatorAnswering(fwApprove(DecisionApprovedForSession))
		env.process()
		if got := single(t, env); got.Result != "allow" || got.Resolution != "user_approved_for_session" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "broadcasts a chain link's denial as authorizer_denied", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-link-denied", "", "bash", "git push"))
		env.escalator = fwEscalatorAnswering(PermissionPromptDecision{Approved: false, State: DecisionDeniedWithReason, DenialReason: fwSP("reads outside the project"), DecidedBy: fwDecidedByAuthorizer})
		env.process()
		if got := single(t, env); got.Result != "deny" || got.Resolution != "authorizer_denied" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "broadcasts an unreachable authority as confirmation_unavailable", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-unavailable", "", "bash", "git push"))
		env.escalator = fwEscalatorAnswering(PermissionPromptDecision{Approved: false, State: DecisionDenied, ConfirmationUnavailable: true,
			DenialReason: fwSP("no serving session"), DecidedBy: DecisionSource{Kind: "unavailable", Reason: fwSP("no serving session")}})
		env.process()
		if got := single(t, env); got.Result != "deny" || got.Resolution != "confirmation_unavailable" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "broadcasts a failed escalation as gate_error", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-broken", "", "bash", "git push"))
		env.escalator = &fwEscalatorFake{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) {
			return PermissionPromptDecision{}, errors.New("dialog exploded")
		}}
		env.process()
		// The prompt broadcast is already out by the time the dialog can fail, so this is the terminal event a consumer waiting on
		// that prompt needs.
		if got := single(t, env); got.RequestID != "req-broken" || got.Result != "deny" || got.Resolution != "gate_error" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "broadcasts nothing when recorded authority resolves the request", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(fwWith("req-silent", "", "bash", "git status"), fwWithIntent(fwBashIntent("git status")))
		env.policy = fwPolicyReturning(fwCheck("allow"))
		env.process()
		if len(env.events()) != 0 {
			t.Fatalf("%+v", env.events())
		}
	})
	tw(t, f, "falls back to the payload's facts when the request carries no projection", func(t *testing.T) {
		env := fwNewServerEnv(t, true)
		env.temp.writeRequest(func(r *ForwardedPermissionRequest) { r.ID = "req-skew" })
		env.process()
		// A version-skewed child sends no display projection; the payload's own request facts are non-nullable, so they answer
		// instead of a sentinel.
		if got := single(t, env); got.Surface != "read" || got.Value != "read" {
			t.Fatalf("%+v", got)
		}
	})
}

// The original's last group runs the real PermissionResolver over a filesystem-backed PermissionManager; this lane ports neither.
func TestServingPolicyOverRealResolver(t *testing.T) {
	const f = "authority/forwarded-request-server"
	tskip(t, f, "hard-denies a bare-surface child request the parent's bare path config denies",
		"needs the real PermissionResolver over a PermissionManager and buildResolvedIntentFromMatchValues (policy and paths lanes); asserts a bare path deny config answers a path request")
	tskip(t, f, "hard-denies when only one direction of the parent's config denies",
		"needs the real PermissionResolver (policy lane); asserts the resolver folds path_write onto a bare path request and names toolName path_write")
	tskip(t, f, "answers a child that already named a direction on that surface alone",
		"needs the real PermissionResolver (policy lane); asserts external_directory_read and _write resolve independently under one config")
	tskip(t, f, "hard-denies a directional child request the parent's bare path config denies (#807)",
		"needs the real PermissionResolver and load-time sugar expansion of the bare path family (config and policy lanes); asserts a path_read request meets the bare deny")
	tskip(t, f, "leaves an unmatched path request without a pattern, so no gate fires (#58)",
		"needs the real PermissionResolver (policy lane); asserts an unmatched path request leaves matchedPattern undefined")
}

// Go-specific: the one-hop canary reads the registry only for a registered requester whose parent is non-empty, and the server leaves
// the request on disk when its response cannot be written, so the child's next poll still finds work to do.
func TestProcessInboxKeepsRequestWhenResponseWriteFails(t *testing.T) {
	env := fwNewServerEnv(t, true)
	env.temp.writeRequest(fwWith("req-keep", "", "bash", "git push"))
	real := renameFile
	renameFile = func(string, string) error { return fwErrnoError("ENOSPC", "rename") }
	t.Cleanup(func() { renameFile = real })
	env.process()
	if !pathExists(filepath.Join(env.temp.location.RequestsDir, "req-keep.json")) {
		t.Fatal("request deleted without a response")
	}
	if _, err := os.Stat(filepath.Join(env.temp.location.ResponsesDir, "req-keep.json")); err == nil {
		t.Fatal("response written")
	}
	if !env.logger.reviewMatching("permission_forwarding.error", nil) {
		t.Fatalf("%+v", env.logger.reviews())
	}
}

// Go-specific: a session whose inbox does not exist yet is drained for free: no log entry, nothing created. The poll runs four times
// a second for the life of every UI session, so this is the common case.
func TestProcessInboxWithoutAnInboxDoesNothing(t *testing.T) {
	env := fwNewServerEnv(t, true)
	if err := os.RemoveAll(env.temp.location.SessionRootDir); err != nil {
		t.Fatal(err)
	}
	env.process()
	if len(env.logger.reviews()) != 0 || len(env.logger.debugs()) != 0 || pathExists(env.temp.location.SessionRootDir) {
		t.Fatalf("logged %+v, or created the inbox", env.logger.reviews())
	}
}

// Go-specific: a registered requester whose entry names no parent session is silent, as an unregistered one is.
func TestProcessInboxOneHopCanaryRegisteredWithoutParent(t *testing.T) {
	env := fwNewServerEnv(t, true)
	env.temp.writeRequest(fwWith("req-orphan", "", "bash", "git push"))
	env.registry = fwMakeSubagentRegistry("child-session", &SubagentSessionInfo{})
	env.process()
	if env.logger.reviewMatching("permission_forwarding.warning", nil) {
		t.Fatalf("%+v", env.logger.reviews())
	}
	// ...and one whose parent is empty text names none either.
	env2 := fwNewServerEnv(t, true)
	env2.temp.writeRequest(fwWith("req-blank", "", "bash", "git push"))
	env2.registry = fwMakeSubagentRegistry("child-session", &SubagentSessionInfo{ParentSessionID: fwSP("")})
	env2.process()
	if env2.logger.reviewMatching("permission_forwarding.warning", nil) {
		t.Fatalf("%+v", env2.logger.reviews())
	}
}
