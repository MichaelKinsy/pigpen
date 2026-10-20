package pi_subagents

import (
	"errors"
	"math"
)

// Child watchdog configuration and status (watchdog/child-status.ts): the config a child's watchdog runs with, and the status events
// it reports to the run that started it.

const (
	childWatchdogWarningLimit    = 20
	childWatchdogStatusEventType = "subagent.watchdog.status"
)

var childWatchdogPhases = []string{"idle", "reviewing", "stale", "failed"}

type childWatchdogConfig struct {
	RunID                 string
	Agent                 string
	ChildIndex            *int
	WatchdogTailTimeoutMs int
	AgentEndTimeoutMs     int
	MaxWarnings           *int
	Model                 string
	Thinking              wdThinking
	Lsp                   wdLsp
	StalemateRepeats      int
	// Cadence: EveryNTools nil means boundary reviews only.
	Cadence wdCadence
}

// childWatchdogWarningSummary is ChildWatchdogWarningSummary.
type childWatchdogWarningSummary struct {
	Severity          string `json:"severity"`
	Importance        string `json:"importance"`
	Category          string `json:"category"`
	Summary           string `json:"summary"`
	Evidence          string `json:"evidence"`
	RecommendedAction string `json:"recommendedAction"`
	DisplayedAt       string `json:"displayedAt,omitempty"`
	// Addressed is true when a later assistant turn in the child followed the warning.
	Addressed bool `json:"addressed"`
	Stalemate bool `json:"stalemate"`
}

// childWatchdogProgress is ChildWatchdogProgress.
type childWatchdogProgress struct {
	Phase      string                        `json:"phase"`
	Seq        int                           `json:"seq"`
	LastUpdate float64                       `json:"lastUpdate"`
	Reason     string                        `json:"reason,omitempty"`
	TimedOut   bool                          `json:"timedOut,omitempty"`
	Warnings   []childWatchdogWarningSummary `json:"warnings,omitempty"`
}

type childWatchdogStatusEvent struct {
	Type       string
	RunID      string
	Agent      string
	ChildIndex *int
	StepIndex  *int
	Seq        int
	Phase      string
	Ts         float64
	Reason     string
	Warning    *childWatchdogWarningSummary
}

type resolveChildWatchdogInput struct {
	Config     *watchdogConfig
	Agent      string
	RunID      string
	ChildIndex *int
}

// resolveChildWatchdogConfig is the watchdog a child launches with, nil when none: the override of its agent, then the children
// settings, then the top level.
func resolveChildWatchdogConfig(in resolveChildWatchdogInput) *childWatchdogConfig {
	c := in.Config
	var override *wdChildOverride
	if in.Agent != "" {
		override = c.Children.override(in.Agent)
	}
	childEnabled := c.Children.Enabled
	if override != nil && override.Enabled != nil {
		childEnabled = *override.Enabled
	}
	if !(c.Enabled && childEnabled) {
		return nil
	}
	model := c.Children.Model
	thinking := c.Children.Thinking
	cadence := c.Cadence
	if c.Children.Cadence != nil {
		cadence = *c.Children.Cadence
	}
	if override != nil {
		if override.Model != "" {
			model = override.Model
		}
		if override.Thinking.set() {
			thinking = override.Thinking
		}
		if override.Cadence != nil {
			cadence = *override.Cadence
		}
	}
	return &childWatchdogConfig{
		RunID: in.RunID, Agent: in.Agent, ChildIndex: in.ChildIndex,
		WatchdogTailTimeoutMs: c.Children.WatchdogTailTimeoutMs, AgentEndTimeoutMs: c.AgentEndTimeoutMs, MaxWarnings: c.MaxWarnings,
		Model: model, Thinking: thinking, Lsp: c.Lsp, StalemateRepeats: c.StalemateRepeats, Cadence: wdCadence{EveryNTools: cadence.EveryNTools},
	}
}

// toObject is the config as the JSON object the child reads (the original's JSON.stringify of ChildWatchdogConfig).
func (c childWatchdogConfig) toObject() *jsObject {
	o := newObject()
	if c.RunID != "" {
		o.set("runId", c.RunID)
	}
	if c.Agent != "" {
		o.set("agent", c.Agent)
	}
	if c.ChildIndex != nil {
		o.set("childIndex", float64(*c.ChildIndex))
	}
	o.set("watchdogTailTimeoutMs", float64(c.WatchdogTailTimeoutMs))
	o.set("agentEndTimeoutMs", float64(c.AgentEndTimeoutMs))
	if c.MaxWarnings != nil {
		o.set("maxWarnings", float64(*c.MaxWarnings))
	} else {
		o.set("maxWarnings", nil)
	}
	if c.Model != "" {
		o.set("model", c.Model)
	}
	if c.Thinking.Off {
		o.set("thinking", false)
	} else if c.Thinking.Level != "" {
		o.set("thinking", c.Thinking.Level)
	}
	o.set("lsp", wdMsg("enabled", c.Lsp.Enabled, "timeoutMs", float64(c.Lsp.TimeoutMs), "maxFiles", float64(c.Lsp.MaxFiles), "maxDiagnostics", float64(c.Lsp.MaxDiagnostics)))
	o.set("stalemateRepeats", float64(c.StalemateRepeats))
	if c.Cadence.EveryNTools != nil {
		o.set("cadence", wdMsg("everyNTools", float64(*c.Cadence.EveryNTools)))
	} else {
		o.set("cadence", wdMsg("everyNTools", nil))
	}
	return o
}

// JSON is the config as the child reads it.
func (c childWatchdogConfig) JSON() string { return marshalJSON(c.toObject(), "") }

func wdChildErr(msg string) error { return errors.New("Invalid child watchdog config: " + msg) }

func wdChildObject(v any, field string) (*jsObject, error) {
	if o, ok := v.(*jsObject); ok {
		return o, nil
	}
	return nil, wdChildErr(field + " must be an object.")
}

func wdChildOptionalString(in *jsObject, field string) (string, error) {
	if !wdHasKey(in, field) {
		return "", nil
	}
	if s, ok := in.vals[field].(string); ok && jsTrim(s) != "" {
		return s, nil
	}
	return "", wdChildErr(field + " must be a non-empty string.")
}

func wdChildInt(v any, min float64) (int, bool) {
	f, ok := wdIsInteger(v)
	return int(f), ok && f >= min
}

func wdChildPositiveInt(in *jsObject, field string) (int, error) {
	if n, ok := wdChildInt(in.vals[field], 1); ok {
		return n, nil
	}
	return 0, wdChildErr(field + " must be a positive integer.")
}

func wdChildCadence(v any) (wdCadence, error) {
	in, err := wdChildObject(v, "cadence")
	if err != nil {
		return wdCadence{}, err
	}
	if in.vals["everyNTools"] == nil && wdHasKey(in, "everyNTools") {
		return wdCadence{}, nil
	}
	if n, ok := wdChildInt(in.vals["everyNTools"], 5); ok {
		return wdCadence{EveryNTools: &n}, nil
	}
	return wdCadence{}, wdChildErr("cadence.everyNTools must be null or an integer >= 5.")
}

func wdChildLsp(v any) (wdLsp, error) {
	in, err := wdChildObject(v, "lsp")
	if err != nil {
		return wdLsp{}, err
	}
	enabled, ok := in.vals["enabled"].(bool)
	if !ok {
		return wdLsp{}, wdChildErr("lsp.enabled must be a boolean.")
	}
	timeout, ok := wdChildInt(in.vals["timeoutMs"], 1)
	if !ok {
		return wdLsp{}, wdChildErr("lsp.timeoutMs must be a positive integer.")
	}
	maxFiles, ok := wdChildInt(in.vals["maxFiles"], 1)
	if !ok {
		return wdLsp{}, wdChildErr("lsp.maxFiles must be a positive integer.")
	}
	maxDiag, ok := wdChildInt(in.vals["maxDiagnostics"], 0)
	if !ok {
		return wdLsp{}, wdChildErr("lsp.maxDiagnostics must be a non-negative integer.")
	}
	return wdLsp{Enabled: enabled, TimeoutMs: timeout, MaxFiles: maxFiles, MaxDiagnostics: maxDiag}, nil
}

// decodeChildWatchdogConfig reads the config a parent passed to a child: nil for none or a disabled one, an error for a malformed one.
func decodeChildWatchdogConfig(raw string) (*childWatchdogConfig, error) {
	if raw == "" {
		return nil, nil
	}
	v, err := parseJSON([]byte(raw))
	if err != nil {
		return nil, err
	}
	parsed, err := wdChildObject(v, "root")
	if err != nil {
		return nil, err
	}
	if wdHasKey(parsed, "fallbackModels") {
		return nil, wdChildErr("fallbackModels was removed; configure one model instead.")
	}
	if parsed.vals["enabled"] == false {
		return nil, nil
	}
	if wdHasKey(parsed, "enabled") && parsed.vals["enabled"] != true {
		return nil, wdChildErr("enabled must be true or false.")
	}
	c := &childWatchdogConfig{}
	switch th := parsed.vals["thinking"].(type) {
	case nil:
		if wdHasKey(parsed, "thinking") {
			return nil, wdChildErr("thinking must be a string or false.")
		}
	case string:
		c.Thinking = wdThinking{Level: th}
	case bool:
		if th {
			return nil, wdChildErr("thinking must be a string or false.")
		}
		c.Thinking = wdThinkingOff
	default:
		return nil, wdChildErr("thinking must be a string or false.")
	}
	if c.RunID, err = wdChildOptionalString(parsed, "runId"); err != nil {
		return nil, err
	}
	if c.Agent, err = wdChildOptionalString(parsed, "agent"); err != nil {
		return nil, err
	}
	if wdHasKey(parsed, "childIndex") {
		n, ok := wdChildInt(parsed.vals["childIndex"], 0)
		if !ok {
			return nil, wdChildErr("childIndex must be a non-negative integer.")
		}
		c.ChildIndex = &n
	}
	if c.Model, err = wdChildOptionalString(parsed, "model"); err != nil {
		return nil, err
	}
	if c.WatchdogTailTimeoutMs, err = wdChildPositiveInt(parsed, "watchdogTailTimeoutMs"); err != nil {
		return nil, err
	}
	if c.AgentEndTimeoutMs, err = wdChildPositiveInt(parsed, "agentEndTimeoutMs"); err != nil {
		return nil, err
	}
	if parsed.vals["maxWarnings"] != nil {
		n, ok := wdChildInt(parsed.vals["maxWarnings"], 0)
		if !ok {
			return nil, wdChildErr("maxWarnings must be null or a non-negative integer.")
		}
		c.MaxWarnings = &n
	} else if !wdHasKey(parsed, "maxWarnings") {
		return nil, wdChildErr("maxWarnings must be null or a non-negative integer.")
	}
	if c.Lsp, err = wdChildLsp(parsed.vals["lsp"]); err != nil {
		return nil, err
	}
	if c.StalemateRepeats, err = wdChildPositiveInt(parsed, "stalemateRepeats"); err != nil {
		return nil, err
	}
	if c.Cadence, err = wdChildCadence(parsed.vals["cadence"]); err != nil {
		return nil, err
	}
	return c, nil
}

// decodeChildWatchdogStatusEvent reads a status event a child wrote; ok is false for anything that is not one.
func decodeChildWatchdogStatusEvent(v any) (childWatchdogStatusEvent, bool) {
	var e childWatchdogStatusEvent
	o, isObj := v.(*jsObject)
	if !isObj || wdPropV(o, "type") != childWatchdogStatusEventType {
		return e, false
	}
	seq, ok := wdIsInteger(wdPropV(o, "seq"))
	if !ok || seq < 0 {
		return e, false
	}
	ts, ok := wdPropV(o, "ts").(float64)
	if !ok || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return e, false
	}
	phase, ok := wdPropV(o, "phase").(string)
	if !ok || !wdContains(childWatchdogPhases, phase) {
		return e, false
	}
	e = childWatchdogStatusEvent{Type: childWatchdogStatusEventType, Seq: int(seq), Phase: phase, Ts: ts}
	e.RunID, _ = wdPropV(o, "runId").(string)
	e.Agent, _ = wdPropV(o, "agent").(string)
	e.Reason, _ = wdPropV(o, "reason").(string)
	if n, ok := wdIsInteger(wdPropV(o, "childIndex")); ok {
		i := int(n)
		e.ChildIndex = &i
	}
	if n, ok := wdIsInteger(wdPropV(o, "stepIndex")); ok {
		i := int(n)
		e.StepIndex = &i
	}
	w, present := wdProp(o, "warning")
	if present {
		wo, isObj := w.(*jsObject)
		if !isObj {
			return e, false
		}
		sev, _ := wdPropV(wo, "severity").(string)
		imp, _ := wdPropV(wo, "importance").(string)
		cat, _ := wdPropV(wo, "category").(string)
		summary, ok1 := wdPropV(wo, "summary").(string)
		evidence, ok2 := wdPropV(wo, "evidence").(string)
		action, ok3 := wdPropV(wo, "recommendedAction").(string)
		addressed, ok4 := wdPropV(wo, "addressed").(bool)
		stalemate, ok5 := wdPropV(wo, "stalemate").(bool)
		displayed, hasDisplayed := wdProp(wo, "displayedAt")
		displayedStr, displayedOK := displayed.(string)
		if (sev != "concern" && sev != "blocker") || !wdContains(watchdogWarningImportance, imp) || !wdContains(watchdogWarningCategories, cat) ||
			!ok1 || !ok2 || !ok3 || !ok4 || !ok5 || (hasDisplayed && !displayedOK) {
			return e, false
		}
		e.Warning = &childWatchdogWarningSummary{Severity: sev, Importance: imp, Category: cat, Summary: summary, Evidence: evidence, RecommendedAction: action,
			DisplayedAt: displayedStr, Addressed: addressed, Stalemate: stalemate}
	}
	return e, true
}

func isChildWatchdogStatusEvent(v any) bool {
	_, ok := decodeChildWatchdogStatusEvent(v)
	return ok
}

func childWatchdogIsActive(p *childWatchdogProgress) bool { return p != nil && p.Phase == "reviewing" }

type acceptChildWatchdogInput struct {
	Current    *childWatchdogProgress
	Event      childWatchdogStatusEvent
	RunID      string
	Agent      string
	ChildIndex *int
}

// acceptChildWatchdogEvent folds an event into the progress; nil when the event is foreign or not newer than the progress.
func acceptChildWatchdogEvent(in acceptChildWatchdogInput) *childWatchdogProgress {
	if in.RunID != "" && in.Event.RunID != in.RunID {
		return nil
	}
	if in.Agent != "" && in.Event.Agent != in.Agent {
		return nil
	}
	eventIndex := in.Event.ChildIndex
	if eventIndex == nil {
		eventIndex = in.Event.StepIndex
	}
	if in.ChildIndex != nil && (eventIndex == nil || *eventIndex != *in.ChildIndex) {
		return nil
	}
	if in.Current != nil && in.Event.Seq <= in.Current.Seq {
		return nil
	}
	var warnings []childWatchdogWarningSummary
	if in.Current != nil {
		warnings = in.Current.Warnings
	}
	if in.Event.Warning != nil {
		warnings = append(append([]childWatchdogWarningSummary{}, warnings...), *in.Event.Warning)
		if len(warnings) > childWatchdogWarningLimit {
			warnings = warnings[len(warnings)-childWatchdogWarningLimit:]
		}
	}
	out := &childWatchdogProgress{Phase: in.Event.Phase, Seq: in.Event.Seq, LastUpdate: in.Event.Ts, Reason: in.Event.Reason}
	if len(warnings) > 0 {
		out.Warnings = warnings
	}
	return out
}

// applyChildWatchdogMessage marks earlier warnings addressed on an assistant turn; nil when nothing changes.
func applyChildWatchdogMessage(current *childWatchdogProgress, message any) *childWatchdogProgress {
	if wdPropV(message, "role") != "assistant" || current == nil {
		return nil
	}
	pending := false
	for _, w := range current.Warnings {
		pending = pending || !w.Addressed
	}
	if !pending {
		return nil
	}
	next := *current
	next.Warnings = make([]childWatchdogWarningSummary, len(current.Warnings))
	for i, w := range current.Warnings {
		w.Addressed = true
		next.Warnings[i] = w
	}
	return &next
}

// childWatchdogBlockers are the blocker warnings that still stand: unaddressed, or held by a stalemate.
func childWatchdogBlockers(p *childWatchdogProgress) []childWatchdogWarningSummary {
	if p == nil {
		return nil
	}
	var out []childWatchdogWarningSummary
	for _, w := range p.Warnings {
		if w.Severity == "blocker" && (!w.Addressed || w.Stalemate) {
			out = append(out, w)
		}
	}
	return out
}

// childWatchdogProgressForModel is what the parent model sees: only the high-importance findings.
func childWatchdogProgressForModel(p *childWatchdogProgress) *childWatchdogProgress {
	if p == nil {
		return nil
	}
	var high []childWatchdogWarningSummary
	for _, w := range p.Warnings {
		if w.Importance == "high" {
			high = append(high, w)
		}
	}
	out := *p
	out.Warnings = high
	return &out
}
