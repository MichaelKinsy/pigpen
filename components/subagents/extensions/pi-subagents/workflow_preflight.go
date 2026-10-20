package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Port of src/workflows/workflow-preflight.ts: the display-only launch plan a workflow request may declare.

const (
	WorkflowPreflightVersion         = 1
	WorkflowPreflightMaxLanes        = 64
	WorkflowPreflightMaxStringLength = 256
	WorkflowPreflightMaxClaims       = 16
	WorkflowPreflightMaxDepth        = 3
	WorkflowPreflightMaxBytes        = 16 * 1024
	WorkflowPreflightMaxWarnings     = 16
	workflowPreflightPlanLabelLength = 96
)

// WorkflowPreflightLane is one declared lane (types.ts WorkflowPreflightLane). Empty strings are absent.
type WorkflowPreflightLane struct {
	Key            string   `json:"key"`
	Mode           string   `json:"mode,omitempty"` // mutation, review, scout, gate
	Decision       string   `json:"decision,omitempty"`
	Claims         []string `json:"claims,omitempty"`
	ExpectedOutput string   `json:"expectedOutput,omitempty"`
	Independence   string   `json:"independence,omitempty"`
}

// WorkflowPreflight is the versioned, display-only plan; it never grants launch authority.
type WorkflowPreflight struct {
	Version  int                     `json:"version"`
	Coverage string                  `json:"coverage"` // complete or partial
	Lanes    []WorkflowPreflightLane `json:"lanes"`
}

var (
	workflowPreflightFields = map[string]bool{"version": true, "coverage": true, "lanes": true}
	workflowPreflightLane   = map[string]bool{"key": true, "mode": true, "decision": true, "claims": true, "expectedOutput": true, "independence": true}
	preflightControlRe      = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	preflightSpaceRe        = regexp.MustCompile(`\s+`)
)

func assertPlainObject(value any, path string) (*jsObject, error) {
	o, ok := value.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("%s must be a plain JSON object.", path)
	}
	return o, nil
}

func assertKnownFields(o *jsObject, allowed map[string]bool, path string) error {
	for _, k := range o.order() {
		if !allowed[k] {
			return fmt.Errorf("%s contains unsupported field '%s'.", path, k)
		}
	}
	return nil
}

func normalizeDisplayString(value any, path string) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string.", path)
	}
	n := jsTrim(preflightSpaceRe.ReplaceAllString(preflightControlRe.ReplaceAllString(s, " "), " "))
	if n == "" {
		return "", fmt.Errorf("%s must be a non-empty string.", path)
	}
	if jsLen(n) > WorkflowPreflightMaxStringLength {
		return "", fmt.Errorf("%s exceeds the maximum length of %d.", path, WorkflowPreflightMaxStringLength)
	}
	return n, nil
}

func preflightDefined(o *jsObject, k string) (any, bool) {
	v, ok := o.vals[k]
	if !ok {
		return nil, false
	}
	if _, u := v.(undef); u {
		return nil, false
	}
	return v, true
}

func normalizePreflightLane(value any, index int) (WorkflowPreflightLane, error) {
	var zero WorkflowPreflightLane
	path := fmt.Sprintf("preflight.lanes[%d]", index)
	o, err := assertPlainObject(value, path)
	if err != nil {
		return zero, err
	}
	if err := assertKnownFields(o, workflowPreflightLane, path); err != nil {
		return zero, err
	}
	key, err := normalizeDisplayString(o.vals["key"], path+".key")
	if err != nil {
		return zero, err
	}
	if !workflowKeyPattern.MatchString(key) {
		return zero, fmt.Errorf("%s.key must be 1-128 characters using letters, numbers, '.', '_' or '-', and start with a letter or number.", path)
	}
	lane := WorkflowPreflightLane{Key: key}
	if m, has := preflightDefined(o, "mode"); has {
		ms, _ := m.(string)
		if ms != "mutation" && ms != "review" && ms != "scout" && ms != "gate" {
			return zero, fmt.Errorf("%s.mode must be one of: mutation, review, scout, gate.", path)
		}
		lane.Mode = ms
	}
	if c, has := preflightDefined(o, "claims"); has {
		list, ok := c.([]any)
		if !ok {
			return zero, fmt.Errorf("%s.claims must be an array of strings.", path)
		}
		if len(list) > WorkflowPreflightMaxClaims {
			return zero, fmt.Errorf("%s.claims supports at most %d entries.", path, WorkflowPreflightMaxClaims)
		}
		for i, claim := range list {
			s, err := normalizeDisplayString(claim, fmt.Sprintf("%s.claims[%d]", path, i))
			if err != nil {
				return zero, err
			}
			lane.Claims = append(lane.Claims, s)
		}
		if lane.Claims == nil {
			lane.Claims = []string{}
		}
	}
	for _, f := range []struct {
		name string
		dst  *string
	}{{"decision", &lane.Decision}, {"expectedOutput", &lane.ExpectedOutput}, {"independence", &lane.Independence}} {
		if v, has := preflightDefined(o, f.name); has {
			s, err := normalizeDisplayString(v, path+"."+f.name)
			if err != nil {
				return zero, err
			}
			*f.dst = s
		}
	}
	return lane, nil
}

// NormalizeWorkflowPreflight is normalizeWorkflowPreflight: validate and canonicalize the explicit preflight input. A nil result
// with no error is an absent (undefined) input.
func NormalizeWorkflowPreflight(input any) (*WorkflowPreflight, error) {
	if _, u := input.(undef); u {
		return nil, nil
	}
	o, err := assertPlainObject(input, "preflight")
	if err != nil {
		return nil, err
	}
	if err := assertKnownFields(o, workflowPreflightFields, "preflight"); err != nil {
		return nil, err
	}
	if v, _ := goFloat(o.vals["version"]); v != WorkflowPreflightVersion {
		return nil, fmt.Errorf("preflight.version must be %d.", WorkflowPreflightVersion)
	}
	coverage := "partial"
	if c, has := preflightDefined(o, "coverage"); has {
		cs, _ := c.(string)
		if cs != "complete" && cs != "partial" {
			return nil, errors.New("preflight.coverage must be 'complete' or 'partial'.")
		}
		coverage = cs
	}
	rawLanes, ok := o.vals["lanes"].([]any)
	if !ok {
		return nil, errors.New("preflight.lanes must be an array.")
	}
	if len(rawLanes) > WorkflowPreflightMaxLanes {
		return nil, fmt.Errorf("preflight.lanes supports at most %d lanes.", WorkflowPreflightMaxLanes)
	}
	lanes := []WorkflowPreflightLane{}
	for i, raw := range rawLanes {
		lane, err := normalizePreflightLane(raw, i)
		if err != nil {
			return nil, err
		}
		lanes = append(lanes, lane)
	}
	seen := map[string]bool{}
	for _, lane := range lanes {
		if seen[lane.Key] {
			return nil, fmt.Errorf("preflight.lanes contains duplicate key '%s'.", lane.Key)
		}
		seen[lane.Key] = true
	}
	n := &WorkflowPreflight{Version: WorkflowPreflightVersion, Coverage: coverage, Lanes: lanes}
	depth := 1
	if len(lanes) > 0 {
		depth++
	}
	for _, l := range lanes {
		if l.Claims != nil {
			depth++
			break
		}
	}
	if depth > WorkflowPreflightMaxDepth {
		return nil, fmt.Errorf("preflight exceeds the maximum nesting depth of %d.", WorkflowPreflightMaxDepth)
	}
	b, _ := json.Marshal(n)
	if len(b) > WorkflowPreflightMaxBytes {
		return nil, fmt.Errorf("preflight canonical JSON is %d bytes; maximum is %d.", len(b), WorkflowPreflightMaxBytes)
	}
	return n, nil
}

// WorkflowPreflightValidation is the result of ValidateWorkflowPreflight.
type WorkflowPreflightValidation struct {
	OK        bool
	Preflight *WorkflowPreflight
	Error     string
}

// ValidateWorkflowPreflight is validateWorkflowPreflight.
func ValidateWorkflowPreflight(input any) WorkflowPreflightValidation {
	p, err := NormalizeWorkflowPreflight(input)
	if err != nil {
		return WorkflowPreflightValidation{Error: err.Error()}
	}
	return WorkflowPreflightValidation{OK: true, Preflight: p}
}

// WorkflowTraceLike is the part of a trace row preflight reads.
type WorkflowTraceLike struct {
	Operation        string
	Key              string
	Phase            string
	GeneratedLaneKey string
	Warning          string
}

// WorkflowKeyMatchesPreflightLane treats declared lane keys as plan roots: `lane` is the lane itself and `lane.stage` a stage by convention.
func WorkflowKeyMatchesPreflightLane(key, laneKey, generatedLaneKey string) bool {
	return generatedLaneKey == laneKey || key == laneKey || strings.HasPrefix(key, laneKey+".")
}

// WorkflowPreflightLaneForRuntimeKey selects the most specific advisory lane without letting declaration order override an exact runtime key.
func WorkflowPreflightLaneForRuntimeKey(p *WorkflowPreflight, key string, preferred ...string) *WorkflowPreflightLane {
	if p == nil {
		return nil
	}
	for i := range p.Lanes {
		if p.Lanes[i].Key == key {
			return &p.Lanes[i]
		}
	}
	var closest *WorkflowPreflightLane
	for i := range p.Lanes {
		l := &p.Lanes[i]
		if strings.HasPrefix(key, l.Key+".") && (closest == nil || len(l.Key) > len(closest.Key)) {
			closest = l
		}
	}
	if closest != nil {
		return closest
	}
	for _, pk := range preferred {
		if pk == "" {
			continue
		}
		for i := range p.Lanes {
			if p.Lanes[i].Key == pk {
				return &p.Lanes[i]
			}
		}
	}
	return nil
}

func keyCoveredByDeclaredLane(e WorkflowTraceLike, declared []string) bool {
	for _, k := range declared {
		if WorkflowKeyMatchesPreflightLane(e.Key, k, e.GeneratedLaneKey) {
			return true
		}
	}
	return false
}

func preflightDeclared(p *WorkflowPreflight) []string {
	keys := make([]string, len(p.Lanes))
	for i, l := range p.Lanes {
		keys[i] = l.Key
	}
	return keys
}

func launchedEntries(trace []WorkflowTraceLike) []WorkflowTraceLike {
	var out []WorkflowTraceLike
	seen := map[string]bool{}
	for _, e := range trace {
		if e.Operation != "run" || e.Phase == "auto-resume" || seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		out = append(out, e)
	}
	return out
}

func undeclaredWarning(key string) string {
	return fmt.Sprintf("Preflight advisory: workflow key '%s' launched without a declared lane.", key)
}

// WorkflowPreflightWarnings returns bounded advisory mismatch warnings; they never reject a child launch.
func WorkflowPreflightWarnings(p *WorkflowPreflight, trace []WorkflowTraceLike, settled bool) []string {
	if p == nil {
		return nil
	}
	declared := preflightDeclared(p)
	launched := launchedEntries(trace)
	var messages []string
	for _, e := range launched {
		if !keyCoveredByDeclaredLane(e, declared) {
			messages = append(messages, undeclaredWarning(e.Key))
		}
	}
	if settled {
		for _, lane := range p.Lanes {
			covered := false
			for _, e := range launched {
				covered = covered || WorkflowKeyMatchesPreflightLane(e.Key, lane.Key, e.GeneratedLaneKey)
			}
			if !covered {
				messages = append(messages, fmt.Sprintf("Preflight advisory: declared lane '%s' was not launched.", lane.Key))
			}
		}
	}
	if len(messages) <= WorkflowPreflightMaxWarnings {
		return messages
	}
	omitted := len(messages) - WorkflowPreflightMaxWarnings + 1
	return append(append([]string(nil), messages[:WorkflowPreflightMaxWarnings-1]...), fmt.Sprintf("Preflight advisory: %d additional mismatch warning(s) omitted.", omitted))
}

// AnnotateWorkflowPreflightTrace attaches the first undeclared-key warning to its first trace row for status and debug views.
func AnnotateWorkflowPreflightTrace(trace []WorkflowTraceLike, p *WorkflowPreflight) []WorkflowTraceLike {
	out := append([]WorkflowTraceLike(nil), trace...)
	if p == nil {
		return out
	}
	declared := preflightDeclared(p)
	warned := map[string]bool{}
	for i, e := range out {
		if e.Operation != "run" || e.Phase == "auto-resume" || keyCoveredByDeclaredLane(e, declared) || warned[e.Key] {
			continue
		}
		warned[e.Key] = true
		if e.Warning == "" {
			out[i].Warning = undeclaredWarning(e.Key)
		}
	}
	return out
}

func laneCell(v string) string {
	if v == "" {
		return "\u2014"
	}
	return v
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// FormatWorkflowPreflight renders the detailed bounded table reserved for tool output and expanded views.
func FormatWorkflowPreflight(p *WorkflowPreflight, indent string) string {
	if p == nil {
		return ""
	}
	n := len(p.Lanes)
	lines := []string{
		fmt.Sprintf("%sPreflight: v%d \u00b7 %s \u00b7 %d lane%s", indent, p.Version, p.Coverage, n, pluralS(n)),
		indent + "  key | mode | decision | claims | expected output | independence",
	}
	for _, l := range p.Lanes {
		claims := "\u2014"
		if l.Claims != nil {
			claims = strings.Join(l.Claims, ", ")
		}
		lines = append(lines, indent+"  "+strings.Join([]string{l.Key, laneCell(l.Mode), laneCell(l.Decision), claims, laneCell(l.ExpectedOutput), laneCell(l.Independence)}, " | "))
	}
	return strings.Join(lines, "\n")
}

// FormatWorkflowPreflightSummary is formatWorkflowPreflightSummary.
func FormatWorkflowPreflightSummary(p *WorkflowPreflight) string {
	if p == nil {
		return ""
	}
	var keys []string
	for i, l := range p.Lanes {
		if i < 4 {
			keys = append(keys, l.Key)
		}
	}
	rest := ""
	if len(p.Lanes) > 4 {
		rest = fmt.Sprintf(", +%d", len(p.Lanes)-4)
	}
	tail := ""
	if len(keys) > 0 {
		tail = ": " + strings.Join(keys, ", ") + rest
	}
	return fmt.Sprintf("preflight \u00b7 %s \u00b7 %d lane%s%s", p.Coverage, len(p.Lanes), pluralS(len(p.Lanes)), tail)
}

func planLaneLabel(l WorkflowPreflightLane) string {
	v := jsTrim(l.Decision)
	if v == "" {
		v = l.Key
	}
	if jsLen(v) <= workflowPreflightPlanLabelLength {
		return v
	}
	cut := string(utf16.Decode(utf16.Encode([]rune(v))[:workflowPreflightPlanLabelLength-1]))
	return strings.TrimRight(cut, " \t\n\r") + "\u2026"
}

// FormatWorkflowPreflightPlanSummary renders the operator-facing one-line plan of routine status views.
func FormatWorkflowPreflightPlanSummary(p *WorkflowPreflight, indent string) string {
	if p == nil {
		return ""
	}
	n := len(p.Lanes)
	var labels string
	if n == 1 {
		labels = planLaneLabel(p.Lanes[0])
	} else {
		var keys []string
		for i, l := range p.Lanes {
			if i < 4 {
				keys = append(keys, l.Key)
			}
		}
		labels = strings.Join(keys, ", ")
		if n > 4 {
			labels += fmt.Sprintf(", +%d", n-4)
		}
	}
	tail := ""
	if labels != "" {
		tail = " \u00b7 " + labels
	}
	return fmt.Sprintf("%sPlan: %d lane%s%s", indent, n, pluralS(n), tail)
}

// FormatWorkflowPreflightWarningSummary renders a bounded, non-alarming warning for routine status views. An empty hint is "details available for debug".
func FormatWorkflowPreflightWarningSummary(warnings []string, indent, hint string) string {
	if len(warnings) == 0 {
		return ""
	}
	if hint == "" {
		hint = "details available for debug"
	}
	n := len(warnings)
	es := "es"
	if n == 1 {
		es = ""
	}
	return fmt.Sprintf("%sPlan note: %d preflight mismatch%s \u00b7 %s.", indent, n, es, hint)
}

// FormatWorkflowPreflightWarnings is formatWorkflowPreflightWarnings.
func FormatWorkflowPreflightWarnings(warnings []string, indent string) string {
	if len(warnings) == 0 {
		return ""
	}
	lines := []string{indent + "Preflight warnings:"}
	for _, w := range warnings {
		lines = append(lines, indent+"  - "+jsTrim(preflightSpaceRe.ReplaceAllString(w, " ")))
	}
	return strings.Join(lines, "\n")
}
