// Package audit emits structured lifecycle events with a closed field set.
//
// An event has a name, the Package, an optional tool name, an outcome, a duration and a reason code. The name,
// outcome and reason are types with no exported constructor from free text: their values come from the package's
// own variables and from the closed set of profile.Code. The Package and tool name must match the identifier rules.
// Emit replaces any value outside its set with "invalid" and counts it. A token, claim, prompt, tool argument or
// path has no field to travel in, so none is ever emitted.
//
// One event is one JSON line under 4 KiB, written with one Write call, so lines from concurrent writers do not
// interleave on a pipe. The default sink is stderr; a file in the session directory is an opt-in.
package audit

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/ident"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// MaxLineBytes bounds one audit line, newline included.
const MaxLineBytes = 4096

// UnavailableInterval is the least time between two audit_unavailable lines on stderr.
const UnavailableInterval = time.Minute

// EventName is a member of the closed set of event names. Use the package's variables; the zero value is invalid.
type EventName struct{ s string }

// The event names. Treat them as constants: assigning one of them to another is the only thing code outside this
// package can do, and the zero value is reported as "invalid".
var (
	EventLoad             = EventName{"load"}
	EventToolCall         = EventName{"tool_call"}
	EventRequest          = EventName{"request"}
	EventPolicyDecision   = EventName{"policy_decision"}
	EventCredential       = EventName{"credential"}
	EventEgress           = EventName{"egress"}
	EventToken            = EventName{"token"}
	EventJudge            = EventName{"judge"}
	EventProfile          = EventName{"profile"}
	EventAuditUnavailable = EventName{"audit_unavailable"}
	EventInvalid          = EventName{"invalid"}
)

var eventNames = map[string]bool{"load": true, "tool_call": true, "request": true, "policy_decision": true, "credential": true,
	"egress": true, "token": true, "judge": true, "profile": true, "audit_unavailable": true, "invalid": true}

// String returns the name, "" for the zero value.
func (e EventName) String() string { return e.s }

// Outcome is a member of the closed set ok, denied, error.
type Outcome struct{ s string }

// The outcomes.
var (
	OutcomeOK      = Outcome{"ok"}
	OutcomeDenied  = Outcome{"denied"}
	OutcomeError   = Outcome{"error"}
	OutcomeInvalid = Outcome{"invalid"}
)

// String returns the outcome, "" for the zero value.
func (o Outcome) String() string { return o.s }

func (o Outcome) valid() bool {
	return o.s == "ok" || o.s == "denied" || o.s == "error" || o.s == "invalid"
}

// Reason is "none", "invalid", or a profile.Code.
type Reason struct{ s string }

// The reasons that are not codes.
var (
	ReasonNone    = Reason{"none"}
	ReasonInvalid = Reason{"invalid"}
)

// ReasonOf returns the Reason for a code. A code outside the closed set is ReasonInvalid.
func ReasonOf(c profile.Code) Reason {
	if !c.Valid() {
		return ReasonInvalid
	}
	return Reason{string(c)}
}

// String returns the reason, "" for the zero value.
func (r Reason) String() string { return r.s }

func (r Reason) valid() bool { return r.s == "none" || r.s == "invalid" || profile.Code(r.s).Valid() }

// OutcomeOf classifies an error: nil is ok; a refusal by a policy, a token check, the egress rules or the absence of
// a dialog is denied; anything else is an error.
func OutcomeOf(err error) Outcome {
	if err == nil {
		return OutcomeOK
	}
	switch profile.CodeOf(err) {
	case profile.EgressDenied, profile.EgressAddressDenied, profile.EgressProxyIgnored, profile.TokenInvalid, profile.TokenAlgDenied,
		profile.PolicyForbidden, profile.PolicyDenied, profile.PolicyError, profile.PolicyUnavailable, profile.JudgeUnavailable, profile.UIUnavailable:
		return OutcomeDenied
	}
	return OutcomeError
}

// ReasonOfError returns the reason for an error: none for nil, the code of a typed error, else invalid.
func ReasonOfError(err error) Reason {
	if err == nil {
		return ReasonNone
	}
	return ReasonOf(profile.CodeOf(err))
}

// Event is one lifecycle event.
type Event struct {
	Event   EventName
	Package string
	// Tool is optional. It must match ^[A-Za-z0-9._-]{1,64}$ or it is reported as "invalid".
	Tool     string
	Outcome  Outcome
	Duration time.Duration
	Reason   Reason
}

// ProxyIgnored is the event that records a proxy named by a tool argument or another config file, which the
// egress policy ignored.
func ProxyIgnored(pkg, tool string) Event {
	return Event{Event: EventEgress, Package: pkg, Tool: tool, Outcome: OutcomeDenied, Reason: ReasonOf(profile.EgressProxyIgnored)}
}

// Emitter receives events.
type Emitter interface{ Emit(Event) }

// Recorder is an Emitter that reports a sink failure to a caller that must fail with it (audit.required).
type Recorder interface {
	Emitter
	Record(Event) error
}

// line is the wire form. Every field is a validated value; there is no other field.
type line struct {
	Time       string `json:"ts"`
	Event      string `json:"event"`
	Package    string `json:"package"`
	Tool       string `json:"tool,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason"`
}

// Logger writes events as JSON lines. A nil *Logger discards everything: it is the flag-off emitter.
type Logger struct {
	w        io.Writer
	stderr   io.Writer
	required bool
	now      func() time.Time
	closer   io.Closer
	pkg      string
	tools    map[string]struct{}

	mu          sync.Mutex
	lastUnavail time.Time
	invalid     atomic.Uint64
}

var _ Recorder = (*Logger)(nil)

// New returns a Logger that writes to w. A nil configuration returns nil (audit is off). Failures to write are
// reported on os.Stderr, at most one line per minute.
func New(cfg *profile.Audit, w io.Writer) *Logger {
	if cfg == nil {
		return nil
	}
	return &Logger{w: w, stderr: os.Stderr, required: cfg.Required, now: time.Now}
}

// WithPackage restricts the Package field to name: an event for another Package is reported as "invalid". Open sets
// it. Call it before the first event.
func (l *Logger) WithPackage(name string) *Logger {
	if l != nil {
		l.pkg = name
	}
	return l
}

// WithTools restricts the Tool field to the given names (the tools the Package registers). A value outside the set,
// even one that is shaped like an identifier, is reported as "invalid", so a token or an argument that reaches the
// Tool field by mistake is not written. Without it any identifier is accepted. Call it before the first event.
func (l *Logger) WithTools(names ...string) *Logger {
	if l != nil {
		l.tools = make(map[string]struct{}, len(names))
		for _, n := range names {
			l.tools[n] = struct{}{}
		}
	}
	return l
}

// Invalid returns how many values outside the closed sets Emit has replaced with "invalid".
func (l *Logger) Invalid() uint64 {
	if l == nil {
		return 0
	}
	return l.invalid.Load()
}

// Emit writes the event. It never fails the caller: a sink that cannot be written costs one audit_unavailable line
// on stderr per minute.
func (l *Logger) Emit(ev Event) { _ = l.Record(ev) }

// Record writes the event and returns an AuditUnavailable error when the sink cannot be written and the profile has
// audit.required. Otherwise it returns nil.
func (l *Logger) Record(ev Event) error {
	if l == nil {
		return nil
	}
	buf, pkg := l.format(ev)
	l.mu.Lock()
	n, err := l.w.Write(buf)
	failed := err != nil || n != len(buf)
	var notice []byte
	if failed {
		now := l.now()
		if l.lastUnavail.IsZero() || now.Sub(l.lastUnavail) >= UnavailableInterval {
			l.lastUnavail = now
			notice, _ = l.format(Event{Event: EventAuditUnavailable, Package: pkg, Outcome: OutcomeError, Reason: ReasonOf(profile.AuditUnavailable)})
		}
	}
	l.mu.Unlock()
	if !failed {
		return nil
	}
	if notice != nil {
		_, _ = l.stderr.Write(notice)
	}
	if l.required {
		return profile.NewError(profile.AuditUnavailable, profile.FlagAudit, pkg)
	}
	return nil
}

// Close releases the sink when it is a file opened by Open.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// format validates the event and renders it. It returns the line and the validated Package name.
func (l *Logger) format(ev Event) ([]byte, string) {
	bad := 0
	name := ev.Event.s
	if !eventNames[name] {
		name, bad = "invalid", bad+1
	}
	pkg := ev.Package
	if !ident.Package(pkg) || (l.pkg != "" && pkg != l.pkg) {
		pkg, bad = "invalid", bad+1
	}
	tool := ev.Tool
	if tool != "" {
		if _, known := l.tools[tool]; !ident.Tool(tool) || (l.tools != nil && !known) {
			tool, bad = "invalid", bad+1
		}
	}
	outcome := ev.Outcome.s
	if !ev.Outcome.valid() {
		outcome, bad = "invalid", bad+1
	}
	reason := ev.Reason.s
	if !ev.Reason.valid() {
		reason, bad = "invalid", bad+1
	}
	if bad > 0 {
		l.invalid.Add(uint64(bad))
	}
	ms := ev.Duration.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	buf, _ := marshal(line{Time: l.now().UTC().Format(time.RFC3339Nano), Event: name, Package: pkg, Tool: tool, Outcome: outcome, DurationMS: ms, Reason: reason})
	return buf, pkg
}

func marshal(v line) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b)+1 > MaxLineBytes {
		return []byte("{\"event\":\"invalid\"}\n"), err
	}
	return append(b, '\n'), nil
}
