package pi_subagents

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The part of src/runs/shared/host-step-status.ts that receipts, the checklist and the workflow graph depend on: the validated
// HostStepNode (types.ts:58) and its fail-closed parser. Everything here is workflow-prefixed so that the terminal-view port of
// the rest of host-step-status.ts can sit next to it.

const (
	workflowHostStepMaxIDChars         = 128
	workflowHostStepMaxLabelChars      = 80
	workflowHostStepMaxRoleChars       = 32
	workflowHostStepMaxProviderChars   = 64
	workflowHostStepMaxReasonChars     = 64
	workflowHostStepMaxDetailChars     = 200
	workflowHostStepMaxTargetChars     = 128
	workflowHostStepMaxRefChars        = 128
	workflowHostStepMaxReportPathChars = 240
	// WorkflowHostStepMaxCount bounds the host steps of one run (host-step-status.ts HOST_STEP_MAX_COUNT).
	WorkflowHostStepMaxCount = 32
)

func itoa(n int) string { return strconv.Itoa(n) }

// WorkflowHostStepFreshness is the expected/observed ref pair of a CI or gate monitor.
type WorkflowHostStepFreshness struct {
	ExpectedRef string
	ObservedRef string
	Stale       *bool
}

// WorkflowHostStep is the bounded status of a host-owned workflow monitor (types.ts HostStepNode). ExitCode is nil when absent;
// HasExitCode with a nil ExitCode is the JSON null a settled command without a code carries.
type WorkflowHostStep struct {
	MonitorKind string // command, ci, gate
	ID          string
	Label       string
	Role        string
	Provider    string
	State       string // pending, running, done, cancelled, error
	Verdict     string // pass, fail, inconclusive, or ""
	ReasonCode  string
	Detail      string
	Target      string
	Freshness   *WorkflowHostStepFreshness
	ReportPath  string
	HasExitCode bool
	ExitCode    *int
	UpdatedAt   int64
	DeadlineAt  *int64
}

// Object is the persisted JSON shape, in the original's field order.
func (h *WorkflowHostStep) Object() *jsObject {
	o := jo("version", float64(1), "kind", "host-step", "monitorKind", h.MonitorKind, "id", h.ID, "label", h.Label)
	str := func(k, v string) {
		if v != "" {
			o.set(k, v)
		}
	}
	str("role", h.Role)
	str("provider", h.Provider)
	o.set("state", h.State)
	str("verdict", h.Verdict)
	str("reasonCode", h.ReasonCode)
	str("detail", h.Detail)
	str("target", h.Target)
	if h.Freshness != nil {
		f := jo("expectedRef", h.Freshness.ExpectedRef)
		if h.Freshness.ObservedRef != "" {
			f.set("observedRef", h.Freshness.ObservedRef)
		}
		if h.Freshness.Stale != nil {
			f.set("stale", *h.Freshness.Stale)
		}
		o.set("freshness", f)
	}
	str("reportPath", h.ReportPath)
	if h.HasExitCode {
		if h.ExitCode == nil {
			o.set("exitCode", nil)
		} else {
			o.set("exitCode", float64(*h.ExitCode))
		}
	}
	o.set("updatedAt", float64(h.UpdatedAt))
	if h.DeadlineAt != nil {
		o.set("deadlineAt", float64(*h.DeadlineAt))
	}
	return o
}

var workflowHostStepFields = map[string]bool{"version": true, "kind": true, "monitorKind": true, "id": true, "label": true, "role": true, "provider": true, "state": true, "verdict": true, "reasonCode": true, "detail": true, "target": true, "freshness": true, "reportPath": true, "exitCode": true, "updatedAt": true, "deadlineAt": true}

func hostStepErr(source, format string, a ...any) error {
	return fmt.Errorf("Invalid host step '%s': %s", source, fmt.Sprintf(format, a...))
}

func isSafeInt(f float64) bool { return f == math.Trunc(f) && math.Abs(f) <= 9007199254740991 }

// hostStepBounded is assertBoundedString: absent is allowed unless required.
func hostStepBounded(o *jsObject, field string, maxChars int, source string, required bool) (string, error) {
	v, ok := o.vals[field]
	if _, u := v.(undef); !ok || u {
		if !required {
			return "", nil
		}
	}
	s, isStr := v.(string)
	if !isStr || jsTrim(s) == "" {
		return "", hostStepErr(source, "%s must be a non-empty string.", field)
	}
	if jsLen(s) > maxChars {
		return "", hostStepErr(source, "%s exceeds %d characters.", field, maxChars)
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", hostStepErr(source, "%s must be single-line.", field)
	}
	return s, nil
}

func hostStepTimestamp(o *jsObject, field, source string, required bool) (int64, bool, error) {
	v, ok := o.vals[field]
	if _, u := v.(undef); !ok || u {
		if !required {
			return 0, false, nil
		}
	}
	f, isNum := v.(float64)
	if !isNum || !isSafeInt(f) || f < 0 {
		return 0, false, hostStepErr(source, "%s must be a non-negative safe integer.", field)
	}
	return int64(f), true, nil
}

func workflowHostStepReportName(p string) string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ParseWorkflowHostStep is parseHostStepNode: it validates the persisted host-step contract and rejects unknown fields and
// unbounded values so status and receipt loaders fail closed.
func ParseWorkflowHostStep(value any, source string) (*WorkflowHostStep, error) {
	o, ok := value.(*jsObject)
	if !ok {
		return nil, hostStepErr(source, "expected an object.")
	}
	var unknown []string
	for _, k := range o.order() {
		if !workflowHostStepFields[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, hostStepErr(source, "unsupported fields: %s.", strings.Join(unknown, ", "))
	}
	if v, _ := o.vals["version"].(float64); v != 1 {
		return nil, hostStepErr(source, "version must be 1.")
	}
	if o.vals["kind"] != "host-step" {
		return nil, hostStepErr(source, "kind must be 'host-step'.")
	}
	h := &WorkflowHostStep{}
	mk, _ := o.vals["monitorKind"].(string)
	if mk != "command" && mk != "ci" && mk != "gate" {
		return nil, hostStepErr(source, "monitorKind must be 'command', 'ci', or 'gate'.")
	}
	h.MonitorKind = mk
	var err error
	if h.ID, err = hostStepBounded(o, "id", workflowHostStepMaxIDChars, source, true); err != nil {
		return nil, err
	}
	if h.Label, err = hostStepBounded(o, "label", workflowHostStepMaxLabelChars, source, true); err != nil {
		return nil, err
	}
	if h.Role, err = hostStepBounded(o, "role", workflowHostStepMaxRoleChars, source, false); err != nil {
		return nil, err
	}
	if h.Provider, err = hostStepBounded(o, "provider", workflowHostStepMaxProviderChars, source, false); err != nil {
		return nil, err
	}
	state, _ := o.vals["state"].(string)
	switch state {
	case "pending", "running", "done", "cancelled", "error":
	default:
		return nil, hostStepErr(source, "state is invalid.")
	}
	h.State = state
	if o.has("verdict") {
		v, _ := o.vals["verdict"].(string)
		if v != "pass" && v != "fail" && v != "inconclusive" {
			return nil, hostStepErr(source, "verdict is invalid.")
		}
		h.Verdict = v
	}
	if state != "done" && o.has("verdict") {
		return nil, hostStepErr(source, "verdict is only valid for done state.")
	}
	if h.ReasonCode, err = hostStepBounded(o, "reasonCode", workflowHostStepMaxReasonChars, source, false); err != nil {
		return nil, err
	}
	if h.Detail, err = hostStepBounded(o, "detail", workflowHostStepMaxDetailChars, source, false); err != nil {
		return nil, err
	}
	if h.Target, err = hostStepBounded(o, "target", workflowHostStepMaxTargetChars, source, false); err != nil {
		return nil, err
	}
	if o.has("freshness") {
		f, ok := o.vals["freshness"].(*jsObject)
		if !ok {
			return nil, hostStepErr(source, "freshness must be an object.")
		}
		var bad []string
		for _, k := range f.order() {
			if k != "expectedRef" && k != "observedRef" && k != "stale" {
				bad = append(bad, k)
			}
		}
		if len(bad) > 0 {
			return nil, hostStepErr(source, "freshness has unsupported fields: %s.", strings.Join(bad, ", "))
		}
		fr := &WorkflowHostStepFreshness{}
		if fr.ExpectedRef, err = hostStepBounded(f, "expectedRef", workflowHostStepMaxRefChars, source, true); err != nil {
			return nil, hostStepErrField(err, "freshness.expectedRef")
		}
		if fr.ObservedRef, err = hostStepBounded(f, "observedRef", workflowHostStepMaxRefChars, source, false); err != nil {
			return nil, hostStepErrField(err, "freshness.observedRef")
		}
		if f.has("stale") {
			b, ok := f.vals["stale"].(bool)
			if !ok {
				return nil, hostStepErr(source, "freshness.stale must be a boolean.")
			}
			fr.Stale = &b
		}
		h.Freshness = fr
		if fr.Stale != nil && *fr.Stale && (state != "done" || h.Verdict != "inconclusive") {
			return nil, hostStepErr(source, "stale freshness requires done/inconclusive state.")
		}
	}
	if h.ReportPath, err = hostStepBounded(o, "reportPath", workflowHostStepMaxReportPathChars, source, false); err != nil {
		return nil, err
	}
	if o.has("reportPath") {
		if n := workflowHostStepReportName(h.ReportPath); n == "" || n == "." || n == ".." {
			return nil, hostStepErr(source, "reportPath must name a report.")
		}
	}
	if o.has("exitCode") {
		h.HasExitCode = true
		switch c := o.vals["exitCode"].(type) {
		case nil:
		case float64:
			if !isSafeInt(c) || c < 0 {
				return nil, hostStepErr(source, "exitCode must be a non-negative safe integer or null.")
			}
			h.ExitCode = new(int(c))
		default:
			return nil, hostStepErr(source, "exitCode must be a non-negative safe integer or null.")
		}
		if mk != "command" {
			return nil, hostStepErr(source, "exitCode is only valid for command steps.")
		}
		if state == "pending" || state == "running" {
			return nil, hostStepErr(source, "exitCode is only valid after command settlement.")
		}
	}
	if h.UpdatedAt, _, err = hostStepTimestamp(o, "updatedAt", source, true); err != nil {
		return nil, err
	}
	if d, has, err := hostStepTimestamp(o, "deadlineAt", source, false); err != nil {
		return nil, err
	} else if has {
		h.DeadlineAt = &d
	}
	return h, nil
}

// hostStepErrField rewrites the field name of a bounded-string error ("expectedRef" -> "freshness.expectedRef").
func hostStepErrField(err error, field string) error {
	short := field[strings.LastIndex(field, ".")+1:]
	return fmt.Errorf("%s", strings.Replace(err.Error(), "': "+short+" ", "': "+field+" ", 1))
}

// AssertUniqueWorkflowHostStepIDs is assertUniqueHostStepIds.
func AssertUniqueWorkflowHostStepIDs(steps []*WorkflowHostStep, source string) error {
	seen := map[string]bool{}
	for _, s := range steps {
		if seen[s.ID] {
			return hostStepErr(source, "duplicate host step id '%s'.", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}
