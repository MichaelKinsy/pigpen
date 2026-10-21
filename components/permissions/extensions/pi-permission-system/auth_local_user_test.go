package pi_permission_system

import (
	"reflect"
	"sync"
	"testing"
)

// Twins of test/authority/local-user-authorizer.test.ts.

func localDetails(mut ...func(*PromptPermissionDetails)) *PromptPermissionDetails {
	return prompterDetails(mut...)
}

func defaultPromptPreferences() PromptPreferences {
	return PromptPreferences{DoublePressToConfirm: true, Budget: DefaultRenderBudget, DialogKeys: defaultDialogKeys(), PromptNotifications: nil}
}

// decisionCall is one call of the injected requestPermissionDecision.
type decisionCall struct {
	view    PermissionPromptView
	title   string
	payload PromptPayload
	options *RequestPermissionOptions
}

// localEnv is makeDeps(): the LocalUserAuthorizer deps with a recording requestPermissionDecision.
type localEnv struct {
	deps    LocalUserAuthorizerDeps
	events  *busFake
	dialogs *AskDialogQueue
	ui      *promptUIFake
	session *NotificationSession
	mu      sync.Mutex
	calls   []decisionCall
	// next, when set, answers the n-th call instead of the default approval.
	answer func(n int) (PermissionPromptDecision, error)
	// onEmit observes an emission without replacing it.
	order []string
}

var approvedByHuman = PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}

func newLocalEnv() *localEnv {
	e := &localEnv{events: &busFake{}, dialogs: NewAskDialogQueue(), ui: &promptUIFake{}, session: &NotificationSession{Name: sp("refactor-auth"), Cwd: "/w/repo"}}
	e.deps = LocalUserAuthorizerDeps{
		UI: e.ui, Mode: "tui", Events: e.events, Dialogs: e.dialogs,
		GetPromptPreferences: defaultPromptPreferences,
		RequestPermissionDecision: func(view PermissionPromptView, title string, payload PromptPayload, options *RequestPermissionOptions) (PermissionPromptDecision, error) {
			e.mu.Lock()
			e.calls = append(e.calls, decisionCall{view, title, payload, options})
			n := len(e.calls)
			e.order = append(e.order, "dialog")
			answer := e.answer
			e.mu.Unlock()
			if answer != nil {
				return answer(n)
			}
			return approvedByHuman, nil
		},
		DescribeSession: func() NotificationSession { e.mu.Lock(); defer e.mu.Unlock(); return *e.session },
	}
	return e
}

func (e *localEnv) authorizer() *LocalUserAuthorizer { return NewLocalUserAuthorizer(e.deps) }

func (e *localEnv) callList() []decisionCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]decisionCall(nil), e.calls...)
}

// authorize runs one ask and returns the single decision call it made.
func (e *localEnv) authorize(t *testing.T, details *PromptPermissionDetails) decisionCall {
	t.Helper()
	mustDecision(t)(e.authorizer().Authorize(details))
	calls := e.callList()
	if len(calls) == 0 {
		t.Fatal("requestPermissionDecision was not called")
	}
	return calls[len(calls)-1]
}

// uiPromptEmit is the one permissions:ui_prompt emission as compact JSON.
func uiPromptEmit(t *testing.T, e *localEnv) string {
	t.Helper()
	emits := e.events.snapshot()
	if len(emits) != 1 || emits[0].channel != "permissions:ui_prompt" {
		t.Fatalf("emits: %+v", emits)
	}
	return marshalJSON(emits[0].data, "")
}

func requestJSON() string { return marshalJSON(authPayload().JS().obj("request"), "") }

func TestLocalUserAuthorizerTwins(t *testing.T) {
	f := "authority/local-user-authorizer"
	noticeOf := func(title, body string) PromptNotice { return PromptNotice{Title: title, Body: body} }
	viewFor := func(e *localEnv, notice PromptNotice) PermissionPromptView {
		return PermissionPromptView{PromptPreferences: defaultPromptPreferences(), Mode: "tui", UI: e.ui, Notice: notice}
	}
	sameView := func(t *testing.T, got, want PermissionPromptView) {
		t.Helper()
		if got.UI != want.UI || got.Mode != want.Mode || got.Notice != want.Notice || !reflect.DeepEqual(got.PromptPreferences, want.PromptPreferences) {
			t.Errorf("view: got %+v, want %+v", got, want)
		}
	}
	optionsOf := func(c decisionCall) any {
		if c.options == nil {
			return nil
		}
		return *c.options
	}

	tw(t, f, "emits a UI prompt event with normalized surface and value", func(t *testing.T) {
		e := newLocalEnv()
		e.authorize(t, localDetails(func(d *PromptPermissionDetails) {
			d.ToolName, d.Command, d.ToolInputPreview = sp("bash"), sp("git push"), sp("git push")
		}))
		eq(t, uiPromptEmit(t, e), `{"requestId":"req-123","source":"tool_call","surface":"bash","value":"git push","agentName":"test-agent","request":`+requestJSON()+`,"forwarding":null}`, "event")
	})
	tw(t, f, "normalizes skill prompt events to the skill surface", func(t *testing.T) {
		e := newLocalEnv()
		e.authorize(t, localDetails(func(d *PromptPermissionDetails) {
			d.Source, d.ToolName, d.SkillName = "skill_input", nil, sp("deploy-helper")
		}))
		eq(t, uiPromptEmit(t, e), `{"requestId":"req-123","source":"skill_input","surface":"skill","value":"deploy-helper","agentName":"test-agent","request":`+requestJSON()+`,"forwarding":null}`, "event")
	})
	tw(t, f, "calls requestPermissionDecision with the threaded view, title, and payload", func(t *testing.T) {
		e := newLocalEnv()
		details := localDetails()
		call := e.authorize(t, details)
		sameView(t, call.view, viewFor(e, noticeOf("pi \u2014 refactor-auth", "Permission Required: read")))
		eq(t, call.title, "Permission Required", "title")
		eq(t, call.payload, details.Payload, "payload")
		eq(t, optionsOf(call), nil, "options")
	})
	tw(t, f, "reads the session when each prompt opens, not when the authorizer is built", func(t *testing.T) {
		e := newLocalEnv()
		authorizer := e.authorizer()
		mustDecision(t)(authorizer.Authorize(localDetails()))
		e.mu.Lock()
		e.session = &NotificationSession{Name: sp("renamed"), Cwd: "/w/repo"}
		e.mu.Unlock()
		mustDecision(t)(authorizer.Authorize(localDetails()))
		calls := e.callList()
		sameView(t, calls[1].view, viewFor(e, noticeOf("pi \u2014 renamed", "Permission Required: read")))
	})
	tw(t, f, "passes the sessionLabel option when present", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(func(d *PromptPermissionDetails) { d.SessionLabel = sp("Yes, for 'read' tool") }))
		eq(t, optionsOf(call), RequestPermissionOptions{SessionLabel: sp("Yes, for 'read' tool")}, "options")
	})
	sessionApprovalOf := func(grants ...ApprovalGrant) func(*PromptPermissionDetails) {
		return func(d *PromptPermissionDetails) { d.SessionApproval = &ForwardedSessionApproval{Grants: grants} }
	}
	tw(t, f, "names a bare external_directory directory approval by its contents glob", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(sessionApprovalOf(ApprovalGrant{Surface: "external_directory", Pattern: "/r/a"}, ApprovalGrant{Surface: "external_directory", Pattern: "/r/a/*"})))
		eq(t, optionsOf(call), RequestPermissionOptions{SessionLabel: sp(`Yes, allow access to "/r/a/*" for this session`)}, "options")
	})
	tw(t, f, "names a bare path approval by its pattern", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(sessionApprovalOf(ApprovalGrant{Surface: "path", Pattern: "/r/*"})))
		eq(t, optionsOf(call), RequestPermissionOptions{SessionLabel: sp(`Yes, allow access to "/r/*" for this session`)}, "options")
	})
	tw(t, f, "keeps a gate-supplied label over the fallback", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(sessionApprovalOf(ApprovalGrant{Surface: "path", Pattern: "/r/*"}), func(d *PromptPermissionDetails) {
			d.SessionLabel = sp(`Yes, allow edit "/r/*" for this session`)
		}))
		eq(t, optionsOf(call), RequestPermissionOptions{SessionLabel: sp(`Yes, allow edit "/r/*" for this session`)}, "options")
	})
	tw(t, f, "adds no label for a grant on a non-path surface", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(sessionApprovalOf(ApprovalGrant{Surface: "bash", Pattern: "git *"})))
		eq(t, optionsOf(call), nil, "options")
	})
	tw(t, f, "emits the UI event before calling requestPermissionDecision", func(t *testing.T) {
		e := newLocalEnv()
		var mu sync.Mutex
		var order []string
		e.deps.Events = orderBus{e.events, func() { mu.Lock(); order = append(order, "emit"); mu.Unlock() }}
		inner := e.deps.RequestPermissionDecision
		e.deps.RequestPermissionDecision = func(v PermissionPromptView, ti string, p PromptPayload, o *RequestPermissionOptions) (PermissionPromptDecision, error) {
			mu.Lock()
			order = append(order, "dialog")
			mu.Unlock()
			return inner(v, ti, p, o)
		}
		mustDecision(t)(e.authorizer().Authorize(localDetails()))
		eq(t, order, []string{"emit", "dialog"}, "order")
	})

	forwarding := func(d *PromptPermissionDetails) {
		d.Forwarding = &ForwardedAskProvenance{RequesterAgentName: sp("Explore"), RequesterSessionID: sp("child-session")}
	}
	tw(t, f, "emits a non-degraded forwarded event with populated forwarding and the child's display projection", func(t *testing.T) {
		e := newLocalEnv()
		e.authorize(t, localDetails(func(d *PromptPermissionDetails) {
			d.Source, d.AgentName, d.Surface, d.Value = "tool_call", sp("Explore"), sp("bash"), sp("git push")
		}, forwarding))
		eq(t, uiPromptEmit(t, e), `{"requestId":"req-123","source":"tool_call","surface":"bash","value":"git push","agentName":"Explore","request":`+requestJSON()+
			`,"forwarding":{"requesterAgentName":"Explore","requesterSessionId":"child-session"}}`, "event")
	})
	tw(t, f, "uses the '(Subagent)' dialog title when the ask is forwarded", func(t *testing.T) {
		e := newLocalEnv()
		details := localDetails(forwarding)
		call := e.authorize(t, details)
		sameView(t, call.view, viewFor(e, noticeOf("pi \u2014 refactor-auth", "Permission Required (Subagent): read")))
		eq(t, call.title, "Permission Required (Subagent)", "title")
		eq(t, call.payload, details.Payload, "payload")
		eq(t, optionsOf(call), nil, "options")
	})
	tw(t, f, "offers a sessionScope when the forwarded ask carries a suggestion", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(func(d *PromptPermissionDetails) { d.ToolName, d.Command = sp("bash"), sp("git push") }, forwarding,
			sessionApprovalOf(ApprovalGrant{Surface: "bash", Pattern: "git *"})))
		eq(t, call.title, "Permission Required (Subagent)", "title")
		eq(t, optionsOf(call), RequestPermissionOptions{SessionScope: &SessionScopeOption{
			SubagentLabel: "This subagent ('Explore') only", ServingSessionLabel: `The whole session — allow bash "git *" for parent and all subagents`}}, "options")
	})
	tw(t, f, "names every path in the scope label when the ask covers several", func(t *testing.T) {
		e := newLocalEnv()
		call := e.authorize(t, localDetails(func(d *PromptPermissionDetails) {
			d.ToolName, d.Command = sp("bash"), sp("cat /outside/a.ts > /elsewhere/b.ts")
		}, forwarding,
			sessionApprovalOf(ApprovalGrant{Surface: "external_directory_read", Pattern: "/outside/*"}, ApprovalGrant{Surface: "external_directory_write", Pattern: "/elsewhere/*"})))
		if call.options == nil || call.options.SessionScope == nil ||
			call.options.SessionScope.ServingSessionLabel != "The whole session — allow external_directory 2 paths for parent and all subagents" {
			t.Errorf("options %+v", call.options)
		}
	})
	tw(t, f, "offers no sessionScope for a forwarded ask without a suggestion", func(t *testing.T) {
		e := newLocalEnv()
		eq(t, optionsOf(e.authorize(t, localDetails(forwarding))), nil, "options")
	})

	expectOptionsFor := func(t *testing.T, grants []ApprovalGrant, want any) {
		t.Helper()
		e := newLocalEnv()
		call := e.authorize(t, localDetails(func(d *PromptPermissionDetails) { d.ToolName = sp("bash") }, sessionApprovalOf(grants...)))
		eq(t, optionsOf(call), want, "options")
	}
	tw(t, f, "offers the width option when every grant proves one direction", func(t *testing.T) {
		expectOptionsFor(t, []ApprovalGrant{{Surface: "external_directory_write", Pattern: "/tmp/*"}}, RequestPermissionOptions{
			SessionLabel: sp(`Yes, allow writes to "/tmp/*" for this session`),
			SessionWidth: &SessionWidthOption{Label: `Yes, allow reads and writes to "/tmp/*" for this session`}})
	})
	tw(t, f, "counts the paths when several grants prove the same direction", func(t *testing.T) {
		expectOptionsFor(t, []ApprovalGrant{{Surface: "external_directory_read", Pattern: "/outside/a/*"}, {Surface: "external_directory_read", Pattern: "/outside/b/*"}},
			RequestPermissionOptions{SessionLabel: sp("Yes, allow reads to 2 paths for this session"),
				SessionWidth: &SessionWidthOption{Label: "Yes, allow reads and writes to 2 paths for this session"}})
	})
	tw(t, f, "offers no width option when the grants prove different directions", func(t *testing.T) {
		expectOptionsFor(t, []ApprovalGrant{{Surface: "external_directory_read", Pattern: "/outside/a/*"}, {Surface: "external_directory_write", Pattern: "/outside/b/*"}},
			RequestPermissionOptions{SessionLabel: sp("Yes, allow access to 2 paths for this session")})
	})
	tw(t, f, "offers no width option for a non-directional surface", func(t *testing.T) {
		expectOptionsFor(t, []ApprovalGrant{{Surface: "bash", Pattern: "git *"}}, nil)
	})
	tw(t, f, "offers no width option for an ask with no suggestion at all", func(t *testing.T) {
		e := newLocalEnv()
		eq(t, optionsOf(e.authorize(t, localDetails())), nil, "options")
	})
	tw(t, f, "returns the decision from requestPermissionDecision", func(t *testing.T) {
		e := newLocalEnv()
		decision := PermissionPromptDecision{State: DecisionDenied, DecidedBy: decidedByHuman}
		e.answer = func(int) (PermissionPromptDecision, error) { return decision, nil }
		eq(t, mustDecision(t)(e.authorizer().Authorize(localDetails())), decision, "decision")
	})

	// "one dialog at a time": a first dialog that stays open until the test answers it.
	overlapping := func(t *testing.T) (e *localEnv, answerFirst func(), first, second *AdmittedAsk[PermissionPromptDecision]) {
		e = newLocalEnv()
		open := make(chan struct{})
		e.answer = func(n int) (PermissionPromptDecision, error) {
			if n == 1 {
				<-open
			}
			return approvedByHuman, nil
		}
		authorizer := e.authorizer()
		admit := func() *AdmittedAsk[PermissionPromptDecision] {
			return Admit(e.dialogs, func() (PermissionPromptDecision, error) { return authorizer.present(localDetails()) }, unansweredDecision)
		}
		first, second = admit(), admit()
		return e, func() { close(open) }, first, second
	}
	wait := func(t *testing.T, a *AdmittedAsk[PermissionPromptDecision]) PermissionPromptDecision {
		t.Helper()
		d, err := a.Wait()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	tw(t, f, "does not present a second ask while the first dialog is open", func(t *testing.T) {
		e, answerFirst, first, second := overlapping(t)
		settleMicrotasks()
		eq(t, len(e.callList()), 1, "calls while open")
		answerFirst()
		eq(t, wait(t, first), approvedByHuman, "first")
		eq(t, wait(t, second), approvedByHuman, "second")
		eq(t, len(e.callList()), 2, "calls")
	})
	tw(t, f, "announces the second ask only when its dialog is presented", func(t *testing.T) {
		e, answerFirst, first, second := overlapping(t)
		settleMicrotasks()
		eq(t, len(e.events.snapshot()), 1, "emits while open")
		answerFirst()
		wait(t, first)
		settleMicrotasks()
		eq(t, len(e.events.snapshot()), 2, "emits")
		wait(t, second)
	})
	tw(t, f, "answers a released ask as an unanswered denial", func(t *testing.T) {
		e, answerFirst, first, second := overlapping(t)
		settleMicrotasks()
		e.dialogs.ReleaseAll("the session ended")
		reason := "the session ended"
		want := PermissionPromptDecision{State: DecisionDenied, ConfirmationUnavailable: true, DenialReason: &reason, DecidedBy: DecisionSource{Kind: "unavailable", Reason: &reason}}
		eq(t, wait(t, first), want, "first")
		eq(t, wait(t, second), want, "second")
		answerFirst() // lets the stale dialog goroutine finish
	})

	// Not in the original: the authorizer goes through the queue by itself, and a failing dialog is an error, never an approval.
	t.Run("a failing dialog is an error", func(t *testing.T) {
		e := newLocalEnv()
		e.answer = func(int) (PermissionPromptDecision, error) { return PermissionPromptDecision{}, errDialogClosed }
		if _, err := e.authorizer().Authorize(localDetails()); err == nil {
			t.Error("no error")
		}
	})
	t.Run("a throwing event bus does not block the dialog", func(t *testing.T) {
		e := newLocalEnv()
		e.events.panics = true
		eq(t, mustDecision(t)(e.authorizer().Authorize(localDetails())), approvedByHuman, "decision")
	})
}

// orderBus observes an emission without replacing it.
type orderBus struct {
	inner *busFake
	on    func()
}

func (b orderBus) Emit(channel string, data any) error {
	b.on()
	return b.inner.Emit(channel, data)
}
