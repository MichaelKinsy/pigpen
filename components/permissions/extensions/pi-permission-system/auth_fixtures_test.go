package pi_permission_system

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// Fixtures for the authority lane's twins: the Go counterparts of test/helpers/prompt-details-fixtures.ts, decision-fixtures.ts,
// authorizer-log-fixtures.ts, prompt-view-fixtures.ts and authorizer-fixtures.ts.

var (
	decidedByHuman           = DecisionSource{Kind: "user", Via: "dialog"}
	decidedByAbsentAuthority = DecisionSource{Kind: "unavailable", Reason: sp("No live authority was reachable for this session")}
)

// authPayload is makePromptPayload().
func authPayload(mut ...func(*PromptPayload)) PromptPayload {
	p := fwMakePromptPayload()
	for _, m := range mut {
		m(&p)
	}
	return p
}

// authDetails is makePromptDetails(overrides).
func authDetails(mut ...func(*PromptPermissionDetails)) *PromptPermissionDetails {
	d := &PromptPermissionDetails{RequestID: "req-1", Source: "tool_call", Payload: authPayload()}
	for _, m := range mut {
		m(d)
	}
	return d
}

// terminalFake is a terminal stub returning a fixed decision and recording its calls.
type terminalFake struct {
	mu       sync.Mutex
	decision PermissionPromptDecision
	err      error
	calls    []*PromptPermissionDetails
}

func newTerminal(decision ...PermissionPromptDecision) *terminalFake {
	t := &terminalFake{decision: PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: decidedByHuman}}
	if len(decision) > 0 {
		t.decision = decision[0]
	}
	return t
}

func (f *terminalFake) Authorize(d *PromptPermissionDetails) (PermissionPromptDecision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d)
	return f.decision, f.err
}

func (f *terminalFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// linkCall is one call of a link stub.
type linkCall struct {
	details *PromptPermissionDetails
	query   PermissionQuery
	log     AuthorizerLog
}

// linkFake is a non-terminal link stub returning a fixed verdict, under a given name.
type linkFake struct {
	name    string
	verdict AuthorizerVerdict
	err     error
	calls   []linkCall
}

func newLink(verdict AuthorizerVerdict, name ...string) *linkFake {
	l := &linkFake{name: "link", verdict: verdict}
	if len(name) > 0 {
		l.name = name[0]
	}
	return l
}

func (l *linkFake) authorize(d *PromptPermissionDetails, q PermissionQuery, log AuthorizerLog) (AuthorizerVerdict, error) {
	l.calls = append(l.calls, linkCall{d, q, log})
	return l.verdict, l.err
}

func (l *linkFake) named() NamedAuthorizer {
	return NamedAuthorizer{Name: l.name, Authorize: l.authorize}
}

// queryFake is an identity-comparable PermissionQuery.
type queryFake struct{ id int }

func (*queryFake) CheckPermission(string, *string, *string) PermissionCheckResult {
	return PermissionCheckResult{}
}
func (*queryFake) GetToolPermission(string, *string) string { return "ask" }

// sameFn reports whether two link callbacks are the same function value (Go cannot compare funcs directly).
func sameFn(a, b AuthorizeFunc) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// deferLink and the others are distinct function values.
func deferLink(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
	return AuthorizerVerdict{Kind: "defer"}, nil
}
func allowLink(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
	return AuthorizerVerdict{Kind: "allow"}, nil
}
func denyLink(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
	return AuthorizerVerdict{Kind: "deny"}, nil
}

// reviewEvents is the review-log events a logger recorded, in order.
func reviewEvents(l *fwLogger) []string {
	var out []string
	for _, c := range l.reviews() {
		out = append(out, c.event)
	}
	return out
}

// mustDecision fails the test on an escalation error.
func mustDecision(t *testing.T) func(PermissionPromptDecision, error) PermissionPromptDecision {
	return func(d PermissionPromptDecision, err error) PermissionPromptDecision {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return d
	}
}

// promptUIFake is a PermissionPromptUi double: the dialogUI select/input scripts plus a scripted custom and the tool-expansion state.
type promptUIFake struct {
	dialogUI
	mu       sync.Mutex
	custom   func(factory PromptComponentFactory, options PromptCustomOptions) (UnattributedDecision, bool, error)
	expanded bool
	getCalls int
	setCalls []bool
}

func (u *promptUIFake) Custom(factory PromptComponentFactory, options PromptCustomOptions) (UnattributedDecision, bool, error) {
	if u.custom == nil {
		return UnattributedDecision{}, false, nil
	}
	return u.custom(factory, options)
}

func (u *promptUIFake) GetToolsExpanded() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.getCalls++
	return u.expanded
}

func (u *promptUIFake) SetToolsExpanded(v bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expanded = v
	u.setCalls = append(u.setCalls, v)
}

// busFake is a PermissionEventBus recording emits.
type busFake struct {
	mu     sync.Mutex
	emits  []busEmit
	failOn string
	panics bool
}

type busEmit struct {
	channel string
	data    any
}

func (b *busFake) Emit(channel string, data any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emits = append(b.emits, busEmit{channel, data})
	if b.panics {
		panic("listener exploded")
	}
	if b.failOn == channel {
		return errors.New("listener failed")
	}
	return nil
}

func (b *busFake) snapshot() []busEmit {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]busEmit(nil), b.emits...)
}

// clearHintEnv blanks every subagent env hint (neutralizeSubagentEnvHints).
func clearHintEnv(t *testing.T) { fwClearHintEnv(t) }
