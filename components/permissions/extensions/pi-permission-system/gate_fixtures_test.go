package pi_permission_system

import (
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Go counterparts of test/helpers/{gate-fixtures,decision-fixtures,prompt-details-fixtures}.ts. The TypeScript mocks are vi.fn
// stubs; here each is a small recording fake with the same call records.

var (
	gfHuman      = DecisionSource{Kind: "user", Via: "dialog"}
	gfAbsent     = DecisionSource{Kind: "unavailable", Reason: sp("No live authority was reachable for this session")}
	gfAuthorizer = DecisionSource{Kind: "authorizer", Name: "model-judge", Verdict: "deny", Reason: sp("reads outside the project")}
)

// makeCheckResult is the neutral-default check-result builder of handler-fixtures.ts.
func makeCheckResult(mut ...func(*PermissionCheckResult)) PermissionCheckResult {
	c := PermissionCheckResult{State: "allow", ToolName: "read", Source: "tool", Origin: "builtin"}
	for _, m := range mut {
		m(&c)
	}
	return c
}

// ckState sets the state and, when given, the matched pattern.
func ckState(state string, pattern ...string) func(*PermissionCheckResult) {
	return func(c *PermissionCheckResult) {
		c.State = state
		if len(pattern) > 0 {
			c.MatchedPattern = sp(pattern[0])
		}
	}
}

func ckPattern(p string) func(*PermissionCheckResult) {
	return func(c *PermissionCheckResult) { c.MatchedPattern = sp(p) }
}

func makePromptPayload(mut ...func(*PromptPayload)) PromptPayload {
	p := PromptPayload{
		Kind: "tool",
		Request: PromptRequestFacts{
			Requester: PromptRequester{}, Surface: "read", ToolName: sp("read"), Value: "read",
		},
		Evidence: []PromptEvidence{}, Annotations: []PromptAnnotation{},
	}
	for _, m := range mut {
		m(&p)
	}
	return p
}

func makeGatePromptDetails(mut ...func(*PromptPermissionDetails)) PromptPermissionDetails {
	d := PromptPermissionDetails{Source: "tool_call", Payload: makePromptPayload()}
	for _, m := range mut {
		m(&d)
	}
	return d
}

// makeDescriptor is makeDescriptor of gate-fixtures.ts: runner-test defaults carrying a payload so a block path renders its denial.
func makeDescriptor(mut ...func(*GateDescriptor)) *GateDescriptor {
	log := newObject()
	log.set("source", "tool_call")
	log.set("toolCallId", "tc-1")
	log.set("toolName", "read")
	d := &GateDescriptor{
		Surface:       "read",
		Input:         newObject(),
		Payload:       makePromptPayload(func(p *PromptPayload) { p.Request.MatchedPattern = sp("*") }),
		PromptDetails: makeGatePromptDetails(func(d *PromptPermissionDetails) { d.ToolCallID, d.ToolName = sp("tc-1"), sp("read") }),
		LogContext:    log,
		Decision:      GateDecision{Surface: "read", Value: "read"},
	}
	for _, m := range mut {
		m(d)
	}
	return d
}

// ---- recording fakes ----

type gfLogWrite struct {
	event   string
	details *jsObject
}

type gfReporter struct {
	logs      []gfLogWrite
	decisions []PermissionDecisionEvent
}

func (r *gfReporter) WriteReviewLog(event string, details *jsObject) {
	r.logs = append(r.logs, gfLogWrite{event, details})
}
func (r *gfReporter) EmitDecision(e PermissionDecisionEvent) { r.decisions = append(r.decisions, e) }

// log is the first review write of the event, failing the test when there is none.
func (r *gfReporter) log(t *testing.T, event string) *jsObject {
	t.Helper()
	for _, l := range r.logs {
		if l.event == event {
			return l.details
		}
	}
	t.Fatalf("no review log %q in %v", event, r.logEvents())
	return nil
}

func (r *gfReporter) logEvents() []string {
	var out []string
	for _, l := range r.logs {
		out = append(out, l.event)
	}
	return out
}

type gfResolver struct {
	fn    func(AccessIntent) PermissionCheckResult
	calls []AccessIntent
}

func (r *gfResolver) Resolve(intent AccessIntent) PermissionCheckResult {
	r.calls = append(r.calls, intent)
	return r.fn(intent)
}

// makeResolver is makeResolver of gate-fixtures.ts: with a default check it answers it for every intent.
func makeResolver(defaultCheck ...PermissionCheckResult) *gfResolver {
	r := &gfResolver{}
	if len(defaultCheck) > 0 {
		c := defaultCheck[0]
		r.fn = func(AccessIntent) PermissionCheckResult { return c }
	} else {
		r.fn = func(AccessIntent) PermissionCheckResult { return PermissionCheckResult{} }
	}
	return r
}

// makeSurfaceDenyingResolver denies exactly one surface; every other answers the neutral allow.
func makeSurfaceDenyingResolver(surface string, deny ...func(*PermissionCheckResult)) *gfResolver {
	r := makeResolver()
	r.fn = func(intent AccessIntent) PermissionCheckResult {
		if gfIntentSurface(intent) == surface {
			return makeCheckResult(append([]func(*PermissionCheckResult){ckState("deny", "*")}, deny...)...)
		}
		return makeCheckResult()
	}
	return r
}

func gfIntentSurface(intent AccessIntent) string {
	switch it := intent.(type) {
	case ToolAccessIntent:
		return it.Surface
	case AccessPathAccessIntent:
		return it.Surface
	case BashCommandAccessIntent:
		return it.Surface
	}
	panic("unhandled intent")
}

type gfEscalator struct {
	fn    func(*PromptPermissionDetails) (PermissionPromptDecision, error)
	calls []PromptPermissionDetails
}

func (e *gfEscalator) Escalate(d *PromptPermissionDetails) (PermissionPromptDecision, error) {
	e.calls = append(e.calls, *d)
	return e.fn(d)
}

func gfAnswer(d PermissionPromptDecision) *gfEscalator {
	return &gfEscalator{fn: func(*PromptPermissionDetails) (PermissionPromptDecision, error) { return d, nil }}
}

type gfRecorder struct{ recorded []*SessionApproval }

func (r *gfRecorder) RecordSessionApproval(a *SessionApproval) { r.recorded = append(r.recorded, a) }

type gfRunnerDeps struct {
	resolver *gfResolver
	recorder *gfRecorder
	escalate *gfEscalator
	reporter *gfReporter
}

type gfRunnerOpts struct {
	resolveResult *PermissionCheckResult
	resolve       func(AccessIntent) PermissionCheckResult
	escalate      *gfEscalator
	yolo          bool
	isYoloEnabled func() bool
}

// makeGateRunner is makeGateRunner of gate-fixtures.ts: one GateRunner over four role fakes.
func makeGateRunner(opts ...gfRunnerOpts) (*GateRunner, *gfRunnerDeps) {
	var o gfRunnerOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	resolver := makeResolver()
	switch {
	case o.resolve != nil:
		resolver.fn = o.resolve
	case o.resolveResult != nil:
		c := *o.resolveResult
		resolver.fn = func(AccessIntent) PermissionCheckResult { return c }
	default:
		c := makeCheckResult(ckPattern("*"))
		resolver.fn = func(AccessIntent) PermissionCheckResult { return c }
	}
	escalate := o.escalate
	if escalate == nil {
		escalate = gfAnswer(PermissionPromptDecision{Approved: true, State: DecisionApproved, DecidedBy: gfHuman})
	}
	isYolo := o.isYoloEnabled
	if isYolo == nil {
		isYolo = func() bool { return o.yolo }
	}
	deps := &gfRunnerDeps{resolver: resolver, recorder: &gfRecorder{}, escalate: escalate, reporter: &gfReporter{}}
	return NewGateRunner(resolver, deps.recorder, escalate, deps.reporter, isYolo), deps
}

func gfCheck(c PermissionCheckResult) *PermissionCheckResult { return &c }

// makeTcc is the bash-default tool-call context factory.
func makeTcc(mut ...func(*ToolCallContext)) ToolCallContext {
	in := newObject()
	in.set("command", "cat .env")
	t := ToolCallContext{ToolName: "bash", Input: in, ToolCallID: "tc-1", Cwd: "/test/project"}
	for _, m := range mut {
		m(&t)
	}
	return t
}

func tccTool(name string, input *jsObject) func(*ToolCallContext) {
	return func(t *ToolCallContext) { t.ToolName, t.Input = name, input }
}

// obj builds an ordered object from alternating keys and values.
func obj(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// jsonOf is the JSON text of a value (an ordered object keeps its key order), the comparison the gates' payloads and logs are read by.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case *jsObject:
		return marshalJSON(x, "")
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(b)
}

// hasFields is expect.objectContaining over an ordered object: every key is present with the value's JSON.
func hasFields(t *testing.T, got *jsObject, kv ...any) {
	t.Helper()
	for i := 0; i < len(kv); i += 2 {
		k := kv[i].(string)
		v, ok := got.get(k)
		if !ok {
			t.Errorf("missing key %q in %s", k, marshalJSON(got, ""))
			continue
		}
		if jsonOf(t, v) != jsonOf(t, kv[i+1]) {
			t.Errorf("key %q = %s, want %s", k, jsonOf(t, v), jsonOf(t, kv[i+1]))
		}
	}
}

// lacksKey reports a key absent or undefined.
func lacksKey(o *jsObject, k string) bool { return !o.has(k) }

func gfIsBlock(o GateOutcome) bool { return o.Action == "block" }

// panicMessage runs fn and returns the message it panicked with ("" and false when it did not).
func panicMessage(fn func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			switch x := r.(type) {
			case string:
				msg = x
			case error:
				msg = x.Error()
			default:
				msg = "non-string panic"
			}
		}
	}()
	fn()
	return "", false
}

var _ = errors.New
var _ = reflect.DeepEqual
var _ = runtime.GOOS
var _ = strings.Contains

// makeGateCheckResult is the path-surface check result factory (toolName "path", source "special", origin "global").
func makeGateCheckResult(mut ...func(*PermissionCheckResult)) PermissionCheckResult {
	c := PermissionCheckResult{ToolName: "path", State: "allow", Source: "special", Origin: "global"}
	for _, m := range mut {
		m(&c)
	}
	return c
}

// sameAccessPath compares two AccessPaths by everything they expose (the original compares the objects with toEqual).
func sameAccessPath(t *testing.T, got, want *AccessPath, what string) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("%s: got %v want %v", what, got, want)
	}
	eq(t, got.Value(), want.Value(), what+" value")
	eq(t, got.BoundaryValue(), want.BoundaryValue(), what+" boundary")
	eq(t, got.MatchValues(), want.MatchValues(), what+" matchValues")
	ga, gok := got.ResolvedAlias()
	wa, wok := want.ResolvedAlias()
	eq(t, [2]any{ga, gok}, [2]any{wa, wok}, what+" resolvedAlias")
}

// expectAccessPathCall checks one resolver call: an access-path intent on surface for want, with the agent.
func expectAccessPathCall(t *testing.T, call AccessIntent, surface string, want *AccessPath, agent *string) {
	t.Helper()
	it, ok := call.(AccessPathAccessIntent)
	if !ok {
		t.Fatalf("intent %T, want an access-path intent", call)
	}
	eq(t, it.Surface, surface, "surface")
	eq(t, it.AgentName, agent, "agent")
	sameAccessPath(t, it.Path, want, "path")
}

// gfExtractors is the originals' extractorLookup: one tool whose extractor reads a string input key.
type gfExtractors struct{ toolName, key string }

func (e gfExtractors) Resolve(name string) *ResolvedToolAccessExtractor {
	if name != e.toolName {
		return nil
	}
	return &ResolvedToolAccessExtractor{Origin: RegistrationLocal, Extractor: NewToolAccessExtractor(func(input *jsObject) (string, bool) {
		return input.str(e.key)
	})}
}

// hostNormalizer is `new PathNormalizer(pathFlavorForPlatform(process.platform), cwd)` built under the identity realpath.
func hostNormalizer(t *testing.T, cwd string) *PathNormalizer {
	t.Helper()
	mockRealpath(t, identityRealpath)
	return NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), cwd)
}

// grantPatterns is `grants.map((grant) => grant.pattern)`.
func grantPatterns(a *SessionApproval) []string {
	out := []string{}
	for _, g := range a.Grants {
		out = append(out, g.Pattern)
	}
	return out
}

// with applies overrides to a context (the spread of `{ ...defaults, ...overrides }`).
func (t ToolCallContext) with(mut ...func(*ToolCallContext)) ToolCallContext {
	for _, m := range mut {
		m(&t)
	}
	return t
}
