package pi_permission_system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ---- session-logger ----

type lgNotes struct{ msgs []string }

func (n *lgNotes) notify(m string) { n.msgs = append(n.msgs, m) }

func TestSessionLoggerTwins(t *testing.T) {
	const f = "logging/session-logger"
	deps := func(t *testing.T, dir string, cfg func() LogConfig) (SessionLoggerDeps, *lgNotes) {
		n := &lgNotes{}
		if cfg == nil {
			cfg = func() LogConfig { return LogConfig{PermissionReviewLog: true} }
		}
		return SessionLoggerDeps{GlobalLogsDir: dir, GetConfig: cfg, Notify: n.notify}, n
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	blocked := func(t *testing.T) string {
		d := t.TempDir()
		barrier := filepath.Join(d, "barrier")
		os.WriteFile(barrier, nil, 0o644)
		return filepath.Join(barrier, "logs")
	}
	tw(t, f, "writes a JSONL line to the debug log file when debugLog is true", func(t *testing.T) {
		dir := t.TempDir()
		d, n := deps(t, dir, func() LogConfig { return LogConfig{DebugLog: true, PermissionReviewLog: true} })
		NewPermissionSessionLogger(d).Debug("test.event", lgDetails("key", "value"))
		eq(t, exists(filepath.Join(dir, debugLogFilename)), true, "file")
		eq(t, len(n.msgs), 0, "notified")
	})
	tw(t, f, "does not write to the debug log when debugLog is false", func(t *testing.T) {
		dir := t.TempDir()
		d, n := deps(t, dir, nil)
		NewPermissionSessionLogger(d).Debug("test.event", nil)
		eq(t, exists(filepath.Join(dir, debugLogFilename)), false, "file")
		eq(t, len(n.msgs), 0, "notified")
	})
	tw(t, f, "reads getConfig at write time — a mid-session toggle change takes effect", func(t *testing.T) {
		dir := t.TempDir()
		debug := true
		d, _ := deps(t, dir, func() LogConfig { return LogConfig{DebugLog: debug} })
		l := NewPermissionSessionLogger(d)
		debug = false
		l.Debug("test.event", nil)
		eq(t, exists(filepath.Join(dir, debugLogFilename)), false, "file")
	})
	tw(t, f, "writes a JSONL line to the review log file when permissionReviewLog is true", func(t *testing.T) {
		dir := t.TempDir()
		d, n := deps(t, dir, nil)
		NewPermissionSessionLogger(d).Review("permission.granted", lgDetails("agentName", "coder"))
		eq(t, exists(filepath.Join(dir, reviewLogFilename)), true, "file")
		eq(t, len(n.msgs), 0, "notified")
	})
	tw(t, f, "does not write to the review log when permissionReviewLog is false", func(t *testing.T) {
		dir := t.TempDir()
		d, n := deps(t, dir, func() LogConfig { return LogConfig{} })
		NewPermissionSessionLogger(d).Review("permission.granted", nil)
		eq(t, exists(filepath.Join(dir, reviewLogFilename)), false, "file")
		eq(t, len(n.msgs), 0, "notified")
	})
	tw(t, f, "calls notify with the error message when the logs directory cannot be created", func(t *testing.T) {
		d, n := deps(t, blocked(t), func() LogConfig { return LogConfig{DebugLog: true} })
		NewPermissionSessionLogger(d).Debug("test.event", nil)
		eq(t, len(n.msgs), 1, "once")
		eq(t, strings.Contains(n.msgs[0], "Failed to"), true, n.msgs[0])
	})
	tw(t, f, "deduplicates the same IO-failure warning across multiple writes", func(t *testing.T) {
		d, n := deps(t, blocked(t), func() LogConfig { return LogConfig{DebugLog: true} })
		l := NewPermissionSessionLogger(d)
		l.Debug("event.one", nil)
		l.Debug("event.two", nil)
		eq(t, len(n.msgs), 1, "once")
	})
	tw(t, f, "shares the dedup set across debug and review — same message notified only once", func(t *testing.T) {
		d, n := deps(t, blocked(t), func() LogConfig { return LogConfig{DebugLog: true, PermissionReviewLog: true} })
		l := NewPermissionSessionLogger(d)
		l.Debug("event.one", nil)
		l.Review("event.two", nil)
		eq(t, len(n.msgs), 1, "once")
	})
	tw(t, f, "calls notify with the message directly", func(t *testing.T) {
		d, n := deps(t, t.TempDir(), nil)
		NewPermissionSessionLogger(d).Warn("Something went wrong")
		eq(t, n.msgs, []string{"Something went wrong"}, "msgs")
	})
	tw(t, f, "calls notify for every warn — not deduplicated", func(t *testing.T) {
		d, n := deps(t, t.TempDir(), nil)
		l := NewPermissionSessionLogger(d)
		l.Warn("same message")
		l.Warn("same message")
		eq(t, len(n.msgs), 2, "twice")
	})
	tw(t, f, "does not throw when notify is a no-op", func(t *testing.T) {
		l := NewPermissionSessionLogger(SessionLoggerDeps{GlobalLogsDir: t.TempDir(), GetConfig: func() LogConfig { return LogConfig{} }, Notify: func(string) {}})
		l.Warn("test")
	})
}

// ---- decision-audit ----

type lgAuditLogger struct {
	debugs []string
	counts []*jsObject
	warns  []string
}

func (a *lgAuditLogger) Debug(e string, d *jsObject) {
	a.debugs = append(a.debugs, e)
	a.counts = append(a.counts, d)
}
func (a *lgAuditLogger) Warn(m string) { a.warns = append(a.warns, m) }

func TestDecisionAuditTwins(t *testing.T) {
	const f = "logging/decision-audit"
	summary := func(a *DecisionAudit) *lgAuditLogger { l := &lgAuditLogger{}; a.WriteSummary(l); return l }
	tw(t, f, "counts allowed, blocked, and error decisions in the summary", func(t *testing.T) {
		a := &DecisionAudit{}
		a.RecordDecision("allow")
		a.RecordDecision("allow")
		a.RecordDecision("block")
		a.RecordError()
		l := summary(a)
		eq(t, l.debugs, []string{"permission.session_summary"}, "event")
		eq(t, lgJSON(l.counts[0]), `{"toolCalls":4,"allowed":2,"blocked":1,"errors":1}`, "counts")
	})
	tw(t, f, "emits a zeroed summary when no calls were recorded", func(t *testing.T) {
		l := summary(&DecisionAudit{})
		eq(t, lgJSON(l.counts[0]), `{"toolCalls":0,"allowed":0,"blocked":0,"errors":0}`, "counts")
	})
	tw(t, f, "does not warn when the counts are consistent", func(t *testing.T) {
		a := &DecisionAudit{}
		a.RecordDecision("allow")
		a.RecordError()
		eq(t, len(summary(a).warns), 0, "warns")
	})
	tw(t, f, "warns when the per-call invariant is violated", func(t *testing.T) {
		a := &DecisionAudit{}
		a.RecordDecision("allow")
		a.toolCalls++ // a re-opened silent path: the total moves without a sub-total
		l := summary(a)
		eq(t, len(l.warns), 1, "once")
		eq(t, strings.Contains(l.warns[0], "invariant violated"), true, l.warns[0])
		eq(t, l.warns[0], "[pi-permission-system] decision audit invariant violated: 2 tool calls != 1 allowed + 0 blocked + 0 errors. A tool call resolved without a recorded terminal decision.", "text")
	})
}

// ---- decision-reporter ----

type lgLogger struct {
	reviews       []string
	details       []*jsObject
	debugs, warns int
}

func (l *lgLogger) Debug(string, *jsObject) { l.debugs++ }
func (l *lgLogger) Warn(string)             { l.warns++ }
func (l *lgLogger) Review(e string, d *jsObject) {
	l.reviews = append(l.reviews, e)
	l.details = append(l.details, d)
}

type lgBus struct {
	channels []string
	data     []any
	fail     func() error
}

func (b *lgBus) Emit(c string, d any) error {
	b.channels = append(b.channels, c)
	b.data = append(b.data, d)
	if b.fail != nil {
		return b.fail()
	}
	return nil
}

func lgDecisionEvent() PermissionDecisionEvent {
	o := "global"
	return PermissionDecisionEvent{RequestID: "perm-00000000-0000-4000-8000-000000000000", Surface: "read", Value: "read", Result: "allow", Resolution: "policy_allow", Origin: &o}
}

func TestDecisionReporterTwins(t *testing.T) {
	const f = "logging/decision-reporter"
	tw(t, f, "satisfies the DecisionReporter interface", func(t *testing.T) {
		var r DecisionReporter = NewGateDecisionReporter(&lgLogger{}, &lgBus{})
		eq(t, r != nil, true, "defined")
	})
	tw(t, f, "delegates to logger.review with event and details", func(t *testing.T) {
		l := &lgLogger{}
		NewGateDecisionReporter(l, &lgBus{}).WriteReviewLog("permission_request.blocked", lgDetails("tool", "bash"))
		eq(t, l.reviews, []string{"permission_request.blocked"}, "event")
		eq(t, lgJSON(l.details[0]), `{"tool":"bash"}`, "details")
	})
	tw(t, f, "delegates with an empty details object", func(t *testing.T) {
		l := &lgLogger{}
		NewGateDecisionReporter(l, &lgBus{}).WriteReviewLog("permission_request.session_approved", newObject())
		eq(t, lgJSON(l.details[0]), `{}`, "details")
	})
	tw(t, f, "does not call emitDecision", func(t *testing.T) {
		b := &lgBus{}
		NewGateDecisionReporter(&lgLogger{}, b).WriteReviewLog("some.event", lgDetails("key", "val"))
		eq(t, len(b.channels), 0, "emits")
	})
	tw(t, f, "emits on the PERMISSIONS_DECISION_CHANNEL with the event", func(t *testing.T) {
		b := &lgBus{}
		ev := lgDecisionEvent()
		NewGateDecisionReporter(&lgLogger{}, b).EmitDecision(ev)
		eq(t, b.channels, []string{PermissionsDecisionChannel}, "channel")
		eq(t, b.data, []any{ev}, "event")
		eq(t, PermissionsDecisionChannel, "permissions:decision", "name")
	})
	tw(t, f, "does not call writeReviewLog", func(t *testing.T) {
		l := &lgLogger{}
		NewGateDecisionReporter(l, &lgBus{}).EmitDecision(lgDecisionEvent())
		eq(t, len(l.reviews), 0, "reviews")
	})
	tw(t, f, "does not propagate a throwing listener", func(t *testing.T) {
		NewGateDecisionReporter(&lgLogger{}, &lgBus{fail: func() error { panic(errors.New("listener boom")) }}).EmitDecision(lgDecisionEvent())
		NewGateDecisionReporter(&lgLogger{}, &lgBus{fail: func() error { return errors.New("listener boom") }}).EmitDecision(lgDecisionEvent())
	})
	t.Run("the event crosses the bus as JSON with the original's field order", func(t *testing.T) {
		got, _ := SafeJSONStringify(lgDecisionEvent())
		eq(t, got, `{"requestId":"perm-00000000-0000-4000-8000-000000000000","surface":"read","value":"read","result":"allow","resolution":"policy_allow","origin":"global","agentName":null,"matchedPattern":null}`, "json")
	})
}

// ---- tool-call-boundary ----

type lgAudit struct {
	decisions []string
	errs      int
}

func (a *lgAudit) RecordDecision(s string) { a.decisions = append(a.decisions, s) }
func (a *lgAudit) RecordError()            { a.errs++ }

type lgTracer struct {
	events  []string
	details []*jsObject
}

func (tr *lgTracer) Debug(e string, d *jsObject) {
	tr.events = append(tr.events, e)
	tr.details = append(tr.details, d)
}

type lgReporter struct {
	reviews   []string
	details   []*jsObject
	decisions []PermissionDecisionEvent
	reviewErr func()
	emitErr   func()
}

func (r *lgReporter) WriteReviewLog(e string, d *jsObject) {
	if r.reviewErr != nil {
		r.reviewErr()
	}
	r.reviews = append(r.reviews, e)
	r.details = append(r.details, d)
}
func (r *lgReporter) EmitDecision(e PermissionDecisionEvent) {
	if r.emitErr != nil {
		r.emitErr()
	}
	r.decisions = append(r.decisions, e)
}

func lgToolEvent(name string, input *jsObject) *jsObject {
	e := newObject()
	e.set("toolName", name)
	if input == nil {
		input = newObject()
	}
	e.set("input", input)
	return e
}

func lgGate(o GateOutcome, err error) GateFunc {
	return func(any, sdk.Context) (GateOutcome, error) { return o, err }
}

func TestToolCallBoundaryTwins(t *testing.T) {
	const f = "handlers/tool-call-boundary"
	var ctx sdk.Context
	tw(t, f, "translates an allow outcome to the empty SDK shape", func(t *testing.T) {
		a, r := &lgAudit{}, &lgReporter{}
		res := CreateFailClosedToolCall(lgGate(GateOutcome{Action: "allow"}, nil), r, a, &lgTracer{})(lgToolEvent("read", nil), ctx)
		eq(t, res, ToolCallResult{}, "result")
		eq(t, res.Value(), nil, "sdk value")
		eq(t, a.decisions, []string{"allow"}, "decision")
		eq(t, a.errs, 0, "errors")
		eq(t, len(r.reviews), 0, "review")
	})
	tw(t, f, "translates a block outcome to the SDK block shape with the reason", func(t *testing.T) {
		a := &lgAudit{}
		res := CreateFailClosedToolCall(lgGate(GateOutcome{Action: "block", Reason: "denied by policy"}, nil), &lgReporter{}, a, &lgTracer{})(lgToolEvent("read", nil), ctx)
		eq(t, res, ToolCallResult{Block: true, Reason: "denied by policy"}, "result")
		eq(t, res.Value(), map[string]any{"block": true, "reason": "denied by policy"}, "sdk value")
		eq(t, a.decisions, []string{"block"}, "decision")
	})
	tw(t, f, "writes a per-call decision trace with the tool name and action", func(t *testing.T) {
		tr := &lgTracer{}
		CreateFailClosedToolCall(lgGate(GateOutcome{Action: "allow"}, nil), &lgReporter{}, &lgAudit{}, tr)(lgToolEvent("bash", nil), ctx)
		eq(t, tr.events, []string{"permission.decision"}, "event")
		eq(t, lgJSON(tr.details[0]), `{"toolName":"bash","action":"allow"}`, "details")
	})
	tw(t, f, "blocks fail-closed when the gate throws, recording an error and a review-log entry", func(t *testing.T) {
		a, r := &lgAudit{}, &lgReporter{}
		in := newObject()
		in.set("command", "cd /repo && git push")
		res := CreateFailClosedToolCall(lgGate(GateOutcome{}, errors.New("parser init failed")), r, a, &lgTracer{})(lgToolEvent("bash", in), ctx)
		eq(t, res.Block, true, "block")
		eq(t, a.errs, 1, "errors")
		eq(t, len(a.decisions), 0, "decisions")
		eq(t, r.reviews, []string{"permission_request.blocked"}, "event")
		d := r.details[0]
		id, _ := d.str("requestId")
		eq(t, strings.HasPrefix(id, "perm-"), true, "id")
		eq(t, lgGet(d, "toolName"), "bash", "tool")
		eq(t, lgGet(d, "command"), "cd /repo && git push", "command")
		eq(t, lgGet(d, "resolution"), "gate_error", "resolution")
		eq(t, lgGet(d, "error"), "parser init failed", "error")
		eq(t, lgJSON(lgGet(d, "decidedBy")), `{"kind":"gate_error","reason":"parser init failed"}`, "decidedBy")
		eq(t, res.Reason, "Permission gate failed and blocked the tool call (fail-closed): parser init failed", "reason")
	})
	tw(t, f, "identifies each errored call separately", func(t *testing.T) {
		r := &lgReporter{}
		b := CreateFailClosedToolCall(lgGate(GateOutcome{}, errors.New("parser init failed")), r, &lgAudit{}, &lgTracer{})
		b(lgToolEvent("bash", nil), ctx)
		b(lgToolEvent("bash", nil), ctx)
		a, _ := r.details[0].str("requestId")
		c, _ := r.details[1].str("requestId")
		eq(t, a != c, true, "distinct")
	})
	tw(t, f, "still blocks when recording the gate error itself throws", func(t *testing.T) {
		r := &lgReporter{reviewErr: func() { panic(errors.New("review log unwritable")) }}
		res := CreateFailClosedToolCall(lgGate(GateOutcome{}, errors.New("parser init failed")), r, &lgAudit{}, &lgTracer{})(lgToolEvent("bash", nil), ctx)
		eq(t, res.Block, true, "block")
	})
	tw(t, f, "does not throw when the event is malformed and the gate throws", func(t *testing.T) {
		r := &lgReporter{}
		gate := func(any, sdk.Context) (GateOutcome, error) { panic("non-error rejection") }
		res := CreateFailClosedToolCall(gate, r, &lgAudit{}, &lgTracer{})(nil, ctx)
		eq(t, res.Block, true, "block")
		eq(t, lgGet(r.details[0], "resolution"), "gate_error", "resolution")
		eq(t, lgGet(r.details[0], "error"), "non-error rejection", "error")
		eq(t, lgGet(r.details[0], "toolName"), "<unknown>", "tool")
	})
	t.Run("terminal decision broadcast", func(t *testing.T) {
		boom := lgGate(GateOutcome{}, errors.New("parser init failed"))
		tw(t, f, "broadcasts a deny decision joined to the gate_error review entry", func(t *testing.T) {
			r := &lgReporter{}
			in := newObject()
			in.set("command", "git push")
			CreateFailClosedToolCall(boom, r, &lgAudit{}, &lgTracer{})(lgToolEvent("bash", in), ctx)
			id, _ := r.details[0].str("requestId")
			eq(t, r.decisions, []PermissionDecisionEvent{{RequestID: id, Surface: "bash", Value: "git push", Result: "deny", Resolution: "gate_error"}}, "decision")
		})
		tw(t, f, "names the tool as the value when the errored call carries no command", func(t *testing.T) {
			r := &lgReporter{}
			CreateFailClosedToolCall(boom, r, &lgAudit{}, &lgTracer{})(lgToolEvent("read", nil), ctx)
			eq(t, r.decisions[0].Surface, "read", "surface")
			eq(t, r.decisions[0].Value, "read", "value")
		})
		tw(t, f, "still blocks when the broadcast itself throws", func(t *testing.T) {
			r := &lgReporter{emitErr: func() { panic(errors.New("listener exploded")) }}
			res := CreateFailClosedToolCall(boom, r, &lgAudit{}, &lgTracer{})(lgToolEvent("bash", nil), ctx)
			eq(t, res.Block, true, "block")
		})
	})
	t.Run("a tracer or recorder that panics on the success path fails closed", func(t *testing.T) {
		r, a := &lgReporter{}, &lgAudit{}
		tr := &lgPanicTracer{}
		res := CreateFailClosedToolCall(lgGate(GateOutcome{Action: "allow"}, nil), r, a, tr)(lgToolEvent("read", nil), ctx)
		eq(t, res.Block, true, "block")
		eq(t, a.errs, 1, "error recorded")
	})
	t.Run("the tool name reads name before toolName, and an empty name is unknown", func(t *testing.T) {
		e := newObject()
		e.set("name", "a")
		e.set("toolName", "b")
		eq(t, bestEffortToolName(e), "a", "name")
		e.set("name", "")
		eq(t, bestEffortToolName(e), "<unknown>", "empty")
		e.set("name", nil)
		eq(t, bestEffortToolName(e), "b", "nullish falls through")
	})
}

type lgPanicTracer struct{}

func (lgPanicTracer) Debug(string, *jsObject) { panic("tracer") }
