package pi_permission_system

import (
	"errors"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Fail-closed pins for the bash gate path that the original's tests do not name (second-pass security review).

// An agent-name lookup that fails must never read as "no agent" (the global scope is the least restrictive one): the handler returns
// the error and the tool-call boundary blocks the bash call.
func TestBashGateAgentNameErrorFailsClosed(t *testing.T) {
	boom := errors.New("session manager exploded")
	rig := makeHandler(t, gfHandlerOpts{
		tools:        []string{"bash"},
		resolveAgent: func(ActiveAgentContext, string) (*string, error) { return nil, boom },
	})
	ctx, _ := makeCtx()
	event := makeToolCallEvent("bash", "input", obj("command", "rm -rf /outside/x"))

	if out, err := rig.handler.HandleToolCall(event, ctx); err == nil {
		t.Fatalf("HandleToolCall allowed a bash call after an agent-name error: %+v", out)
	}
	if out, err := rig.handler.HandleInput("/skill:review", ctx); err == nil {
		t.Fatalf("HandleInput continued after an agent-name error: %+v", out)
	}

	gate := func(event any, _ sdk.Context) (GateOutcome, error) { return rig.handler.HandleToolCall(event, ctx) }
	var sdkCtx sdk.Context
	result := CreateFailClosedToolCall(gate, &lgReporter{}, &lgAudit{}, &lgTracer{})(event, sdkCtx)
	eq(t, result.Block, true, "the boundary blocks")
}

// A bash call whose program cannot be derived is never an allow through the producers: the zero-unit fallback asks, and the gate
// functions treat a missing program as "does not apply" only for the non-shell case (the pipeline parses every shell command).
func TestBashGateNoUnitsAsksUnderPermissiveRule(t *testing.T) {
	r := makeResolver(bashResult("allow", "(", "*"))
	got := ResolveBashCommandCheck("(", nil, nil, r)
	eq(t, got.State, "ask", "state")
	eq(t, got.MatchedPattern, sp("<unparseable-bash-command>"), "pattern")
}

// A panic in a bash gate producer propagates (no recover inside the gate), so the boundary blocks.
func TestBashGateProducersDoNotSwallowPanics(t *testing.T) {
	boom := &panicResolver{}
	n := bgNormalizer(goos(), "/test/project")
	tcc := makeTcc()
	program := bgProgram(t, tcc, n)
	for name, fn := range map[string]func(){
		"path": func() { DescribeBashPathGate(tcc, program, boom, n) },
		"extdir": func() {
			DescribeBashExternalDirectoryGate(tcc, bgProgram(t, makeTcc(func(c *ToolCallContext) { c.Input = obj("command", "cat /outside/x") }), n), boom, n)
		},
		"commandck": func() { ResolveBashCommandCheck("ls", units("ls"), nil, boom) },
	} {
		if _, panicked := panicMessage(fn); !panicked {
			t.Errorf("%s gate swallowed a resolver panic", name)
		}
	}
}

type panicResolver struct{}

func (*panicResolver) Resolve(AccessIntent) PermissionCheckResult { panic("resolver exploded") }
