package pi_subagents

import (
	"fmt"
	"sort"
	"strings"
)

// Port of src/runs/shared/acceptance.ts: the effective acceptance of a run and the prompt that states it to the child.

var acceptanceLevelRank = map[string]int{"none": 0, "attested": 1, "checked": 2, "verified": 3}

// acceptanceGate is the original's ResolvedAcceptanceGate.
type acceptanceGate struct {
	ID       string
	Must     string
	Evidence []string
	Severity string // required, recommended
}

// verifyCommand is the original's AcceptanceVerifyCommand.
type verifyCommand struct {
	ID           string
	Command      string
	TimeoutMs    int
	Cwd          string
	Env          map[string]string
	AllowFailure bool
	Output       string // "json" for a typed command
	Schema       any
}

// reviewGate is the original's AcceptanceReviewGate; Required is nil when unset.
type reviewGate struct {
	Agent, Focus string
	Required     *bool
}

// resolvedAcceptance is the original's ResolvedAcceptanceConfig.
type resolvedAcceptance struct {
	Level               string
	Explicit            bool
	InferredReason      []string
	Criteria            []acceptanceGate
	Evidence            []string
	PreserveStagedIndex bool
	Verify              []verifyCommand
	Review              *reviewGate // nil: none (false counts as none, see ReviewDisabled)
	ReviewDisabled      bool        // review: false was given
	StopRules           []string
	Reason              string
}

func unique(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, i := range items {
		if !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}
	return out
}

func requiredEvidenceForLevel(level string) []string {
	switch level {
	case "attested":
		return []string{"manual-notes", "residual-risks"}
	case "checked":
		return []string{"changed-files", "tests-added", "commands-run", "residual-risks", "no-staged-files"}
	case "verified":
		return []string{"changed-files", "tests-added", "commands-run", "validation-output", "residual-risks", "no-staged-files"}
	}
	return []string{}
}

// acceptanceRun is the call context the inference reads.
type acceptanceRun struct {
	Explicit       any // the acceptance input (nil: none)
	AgentName      string
	AcceptanceRole string
	Task           string
	Mode           string
	Async          bool
	Dynamic        bool
	DynamicGroup   bool
	AgentContract  bool // isAgentContract(contract)
}

type inferredAcceptance struct {
	Level    string
	Reasons  []string
	Criteria []string
	Evidence []string
	Review   *reviewGate
}

func inferAcceptanceLevel(in acceptanceRun) inferredAcceptance {
	yes := true
	switch {
	case in.AcceptanceRole == "writer" && (in.Async || in.Dynamic || in.DynamicGroup):
		reason := "dynamic declared writer"
		if in.Async {
			reason = "async declared writer"
		}
		reasons := []string{reason}
		if in.Dynamic || in.DynamicGroup {
			reasons = append(reasons, "dynamic fanout context")
		}
		return inferredAcceptance{"checked", reasons, []string{"Implement the requested change without widening scope", "Return evidence sufficient for an independent acceptance review"}, requiredEvidenceForLevel("checked"), &reviewGate{Agent: "reviewer", Required: &yes}}
	case in.AcceptanceRole == "writer":
		return inferredAcceptance{"checked", []string{"declared writer acceptance role"}, []string{"Implement the requested change without widening scope"}, requiredEvidenceForLevel("checked"), nil}
	case in.AcceptanceRole == "read-only":
		return inferredAcceptance{"none", []string{"declared read-only acceptance role"}, []string{"Return concrete findings with file paths and severity when applicable"}, []string{"review-findings", "residual-risks"}, nil}
	}
	return inferredAcceptance{"attested", []string{"default lightweight attestation"}, []string{"Return a concise result and residual risks when applicable"}, []string{"manual-notes", "residual-risks"}, nil}
}

// resolveEffectiveAcceptance is the original's resolveEffectiveAcceptance. A validation failure of the explicit input is an
// error (the original's executor validates before it resolves).
func resolveEffectiveAcceptance(in acceptanceRun) (*resolvedAcceptance, error) {
	explicit, err := normalizeAcceptanceInput(in.Explicit)
	if err != nil {
		return nil, err
	}
	get := func(k string) (any, bool) {
		v, ok := explicit.vals[k]
		return v, ok && !isUndefined(v)
	}
	explicitLevel := "auto"
	if v, ok := get("level"); ok {
		explicitLevel, _ = v.(string)
	}
	reason, _ := explicit.vals["reason"].(string)
	canDisable := explicitLevel == "none" && jsTrim(reason) != ""
	requestsPolicy := explicitLevel != "auto" || func() bool {
		for _, k := range explicit.order() {
			if k != "level" {
				return true
			}
		}
		return false
	}()
	evidenceOf := func() []string {
		ev, _ := get("evidence")
		return stringsOf(ev)
	}
	verify := parseVerifyCommands(explicit.vals["verify"])
	review, reviewDisabled := parseReviewGate(explicit)
	preserve := explicit.vals["preserveStagedIndex"] == true
	stop := stringsOf(explicit.vals["stopRules"])
	if in.AgentContract {
		level := explicitLevel
		if canDisable || explicitLevel == "auto" {
			level = "none"
		}
		evidence := unique(evidenceOf())
		criteria := normalizeCriteria(explicit.vals["criteria"], evidence)
		return &resolvedAcceptance{Level: level, Explicit: !isUndefined(in.Explicit), InferredReason: []string{}, Criteria: criteria, Evidence: evidence, PreserveStagedIndex: preserve, Verify: verify, Review: review, ReviewDisabled: reviewDisabled, StopRules: stop, Reason: reason}, nil
	}
	inferred := inferAcceptanceLevel(in)
	inferredLevel := inferred.Level
	if inferredLevel == "none" && requestsPolicy {
		inferredLevel = "attested"
	}
	var level string
	switch {
	case canDisable:
		level = "none"
	case explicitLevel == "auto":
		level = inferredLevel
	case acceptanceLevelRank[explicitLevel] >= acceptanceLevelRank[inferredLevel]:
		level = explicitLevel
	default:
		level = inferredLevel
	}
	base := requiredEvidenceForLevel(level)
	if level == inferredLevel {
		base = inferred.Evidence
	}
	evidence := unique(append(append([]string{}, base...), evidenceOf()...))
	var rawCriteria any
	if c, ok := asArray(explicit.vals["criteria"]); ok && len(c) > 0 {
		rawCriteria = c
	} else {
		rawCriteria = anyStrings(inferred.Criteria)
	}
	criteria := normalizeCriteria(rawCriteria, evidence)
	if _, has := get("review"); !has {
		review, reviewDisabled = inferred.Review, false
	}
	out := &resolvedAcceptance{Level: level, Explicit: !isUndefined(in.Explicit), InferredReason: inferred.Reasons, PreserveStagedIndex: preserve, Verify: verify, Review: review, ReviewDisabled: reviewDisabled, StopRules: stop, Reason: reason}
	if level == "none" {
		out.Criteria, out.Evidence = []acceptanceGate{}, []string{}
	} else {
		out.Criteria, out.Evidence = criteria, evidence
	}
	return out, nil
}

func parseReviewGate(explicit *jsObject) (g *reviewGate, disabled bool) {
	v, ok := explicit.vals["review"]
	if !ok || isUndefined(v) {
		return nil, false
	}
	if v == false {
		return nil, true
	}
	o, ok := asObject(v)
	if !ok {
		return nil, false
	}
	g = &reviewGate{}
	g.Agent, _ = o.vals["agent"].(string)
	g.Focus, _ = o.vals["focus"].(string)
	if r, ok := o.vals["required"].(bool); ok {
		g.Required = &r
	}
	return g, false
}

func parseVerifyCommands(v any) []verifyCommand {
	arr, _ := asArray(v)
	out := []verifyCommand{}
	for _, item := range arr {
		o, ok := asObject(item)
		if !ok {
			continue
		}
		c := verifyCommand{}
		c.ID, _ = o.vals["id"].(string)
		c.Command, _ = o.vals["command"].(string)
		if f, ok := asNumber(o.vals["timeoutMs"]); ok {
			c.TimeoutMs = int(f)
		}
		c.Cwd, _ = o.vals["cwd"].(string)
		if env, ok := asObject(o.vals["env"]); ok {
			c.Env = map[string]string{}
			for _, k := range env.order() {
				if s, ok := env.vals[k].(string); ok {
					c.Env[k] = s
				}
			}
		}
		c.AllowFailure = o.vals["allowFailure"] == true
		c.Output, _ = o.vals["output"].(string)
		if s, ok := o.vals["schema"]; ok && !isUndefined(s) {
			c.Schema = s
		}
		out = append(out, c)
	}
	return out
}

func normalizeCriteria(criteria any, evidence []string) []acceptanceGate {
	arr, _ := asArray(criteria)
	out := []acceptanceGate{}
	for i, c := range arr {
		var g acceptanceGate
		if s, ok := c.(string); ok {
			g = acceptanceGate{ID: fmt.Sprintf("criterion-%d", i+1), Must: s, Evidence: evidence, Severity: "required"}
		} else if o, ok := asObject(c); ok {
			id, _ := o.vals["id"].(string)
			if jsTrim(id) == "" {
				id = fmt.Sprintf("criterion-%d", i+1)
			} else {
				id = jsTrim(id)
			}
			must, _ := o.vals["must"].(string)
			g = acceptanceGate{ID: id, Must: must, Evidence: evidence, Severity: "required"}
			if ev, ok := asArray(o.vals["evidence"]); ok {
				g.Evidence = []string{}
				for _, e := range ev {
					if es, ok := e.(string); ok && validAcceptanceEvidence[es] {
						g.Evidence = append(g.Evidence, es)
					}
				}
			}
			if s, ok := o.vals["severity"].(string); ok && s != "" {
				g.Severity = s
			}
		} else {
			continue
		}
		if jsTrim(g.Must) != "" {
			out = append(out, g)
		}
	}
	return out
}

func acceptanceRequiresChildReport(a *resolvedAcceptance) bool {
	return len(a.Criteria) > 0 || len(a.Evidence) > 0
}

// formatResolvedReviewGateLabel labels a declared review gate the same way in prompts and capability summaries.
func formatResolvedReviewGateLabel(r *reviewGate) string {
	status := "required"
	if r.Required != nil && !*r.Required {
		status = "optional"
	}
	if r.Agent != "" {
		return status + " by " + r.Agent
	}
	return status
}

type acceptancePromptOptions struct{ ReportOptional, StructuredOutput bool }

// formatAcceptancePrompt is the contract section appended to a child's task ("" when nothing is required).
func formatAcceptancePrompt(a *resolvedAcceptance, opt acceptancePromptOptions) string {
	if a.Level == "none" {
		return ""
	}
	if opt.ReportOptional && !acceptanceRequiresChildReport(a) {
		return ""
	}
	lines := []string{"", "## Acceptance Contract", "Acceptance level: " + a.Level, "Completion is not accepted from prose alone. End with a structured acceptance report.", "", "Criteria:"}
	if len(a.Criteria) > 0 {
		for _, c := range a.Criteria {
			lines = append(lines, "- "+c.ID+": "+c.Must)
		}
	} else {
		lines = append(lines, "- Return the requested result.")
	}
	ev := strings.Join(a.Evidence, ", ")
	if ev == "" {
		ev = "none"
	}
	lines = append(lines, "", "Required evidence: "+ev)
	if a.PreserveStagedIndex {
		lines = append(lines, "The host will verify that the staged index is unchanged from launch; report noStagedFiles truthfully even when the preserved index is non-empty.")
	}
	if len(a.Verify) > 0 {
		lines = append(lines, "", "Runtime verification commands configured by parent:")
		for _, c := range a.Verify {
			lines = append(lines, "- "+c.ID+": "+c.Command)
		}
	}
	if a.Review != nil {
		lines = append(lines, "", "Review gate: "+formatResolvedReviewGateLabel(a.Review)+".")
		if a.Review.Focus != "" {
			lines = append(lines, "Review focus: "+a.Review.Focus)
		}
	}
	if len(a.StopRules) > 0 {
		lines = append(lines, "", "Stop rules:")
		for _, r := range a.StopRules {
			lines = append(lines, "- "+r)
		}
	}
	intro := "Finish with a fenced JSON block tagged `acceptance-report` in this shape:"
	if opt.StructuredOutput {
		intro = "Include an `acceptanceReport` object in your final `structured_output` tool call in this shape:"
	}
	lines = append(lines, "", intro,
		"Use empty arrays when no items apply; array fields contain strings unless object entries are shown.",
		"Empty-string entries (`[\"\"]`) are ignored; use `[]` when nothing applies.",
		"`criteriaSatisfied[].status` must be exactly one of: satisfied, not-satisfied, not-applicable.",
		"`commandsRun[].result` must be exactly one of: passed, failed, not-run.",
		"`manualNotes` and `notes` are optional strings; an empty string means no note and does not satisfy `manual-notes` evidence.")
	if !opt.StructuredOutput {
		lines = append(lines, "```acceptance-report")
	}
	satisfied := []any{}
	for _, c := range a.Criteria {
		if c.Severity != "recommended" {
			o := newObject()
			o.set("id", c.ID)
			o.set("status", "satisfied")
			o.set("evidence", "specific proof")
			satisfied = append(satisfied, o)
		}
	}
	cmd := newObject()
	cmd.set("command", "command")
	cmd.set("result", "passed")
	cmd.set("summary", "short result")
	example := newObject()
	example.set("criteriaSatisfied", satisfied)
	example.set("changedFiles", []any{"src/file.ts"})
	example.set("testsAddedOrUpdated", []any{"test/file.test.ts"})
	example.set("commandsRun", []any{cmd})
	example.set("validationOutput", []any{"validation output or concise summary"})
	example.set("residualRisks", []any{"none"})
	example.set("noStagedFiles", true)
	example.set("diffSummary", "short description of the diff")
	example.set("reviewFindings", []any{"blocker: file.ts:12 - issue found, or no blockers"})
	example.set("manualNotes", "anything else the parent should know")
	lines = append(lines, marshalJSON(example, "  "))
	if !opt.StructuredOutput {
		lines = append(lines, "```")
	}
	return strings.Join(lines, "\n")
}

// sortedKeys is the keys of a set, sorted (for stable text).
func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
