package warden

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The rules check behind `/warden rules check` (src/rules-lint.ts): two questions about the rule set itself, not about a
// change.
//
// One asks whether the rules guard can judge a rule correctly from the changed file's content alone. The guard sends
// the judge a rule, the path of the one file being written, and the text written to it (plus a few lines of that same
// file around an edit): a rule that needs another file, the history, or the task scores poorly there.
//
// The other asks whether a standard linter, formatter, or type checker could enforce the rule exactly, where a request
// per edit buys nothing over a tool that runs for free and decides the same way every time.
//
// Advice only. Nothing here changes, disables, or skips a rule. Requests carry each rule's heading, text, and `paths:`
// scope, redacted; no file content and no task text.

const (
	// checkBodyLimit: rules are read at a length that shows a rule's shape; the guard clips harder for a per-edit request.
	checkBodyLimit = 400
	// checkStateBudget is the JSON bytes one state may use: the API caps one request at 64 KiB and the state is repeated
	// in every chunk, so the budget leaves room for the questions and the rest of the request next to it.
	checkStateBudget = 48_000
	// mechanicalCutoff is the P(a linter could enforce the rule exactly) at or above which the check calls the rule mechanical.
	mechanicalCutoff = 0.7
	// maxQuestionsPerRequest is the judge's per-request question limit (pi-typesafe DEFAULT_MAX_QUESTIONS).
	maxQuestionsPerRequest = 32

	judgeablePrefix  = "judgeable_"
	mechanicalPrefix = "mechanical_"
)

// judgeabilityReasons are the answer labels of the judgeable question; from_change_alone is the healthy one.
var judgeabilityReasons = []string{"from_change_alone", "needs_other_files", "needs_task_or_history", "too_vague"}

func isJudgeabilityReason(s string) bool {
	for _, r := range judgeabilityReasons {
		if r == s {
			return true
		}
	}
	return false
}

func fillRuleTemplate(template string, id, name string) string {
	return strings.NewReplacer("{id}", id, "{name}", name).Replace(template)
}

func judgeableQuestion(id, name string) Question {
	q := rulesQuestions().judgeable
	q.Instructions = fillRuleTemplate(q.Instructions, id, name)
	return q
}

func mechanicalQuestion(id, name string) Question {
	q := rulesQuestions().mechanical
	q.Instructions = fillRuleTemplate(q.Instructions, id, name)
	return q
}

// RuleCheckView is a rule as it travels: redacted, clipped, with its path scope.
type RuleCheckView struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
	Body  string   `json:"body"`
}

func ruleCheckView(rule Rule) RuleCheckView {
	paths := append([]string{}, rule.Paths...)
	return RuleCheckView{ID: rule.ID, Name: rule.Name, Paths: paths, Body: Redact(truncate(rule.Body, checkBodyLimit))}
}

// jsonLength is JSON.stringify(value).length: UTF-16 units of compact JSON with no HTML escaping.
func jsonLength(v any) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0
	}
	text := strings.TrimSuffix(buf.String(), "\n")
	// Go escapes the line separators JavaScript leaves as they are.
	text = strings.NewReplacer(`\u2028`, "\u2028", `\u2029`, "\u2029").Replace(text)
	return utf16Len(text)
}

// RulesCheckRequest is the judge request of a rules check.
type RulesCheckRequest struct {
	State map[string]any
	// Order is the question ids in insertion order, so chunking keeps the rule order.
	Order     []string
	Questions Questions
	// Rules are the rules the request asks about, in rule-set order.
	Rules []RuleCheckView
	// NotChecked counts the rules left out because the state had reached its byte budget.
	NotChecked int
}

// buildRulesCheckRequest: one state carries every rule and two questions each; a request past the judge's 32-question
// limit becomes chunks that repeat the state. A state past the byte budget drops the tail of the rule set, which the
// caller reports rather than sending a request the API would reject.
func buildRulesCheckRequest(rules []Rule) RulesCheckRequest {
	var views []RuleCheckView
	bytesUsed := utf16Len(rulesQuestions().checkAbout) + 64
	for _, rule := range rules {
		view := ruleCheckView(rule)
		size := jsonLength(view) + 8
		if len(views) > 0 && bytesUsed+size > checkStateBudget {
			break
		}
		views = append(views, view)
		bytesUsed += size
	}
	request := RulesCheckRequest{Questions: Questions{}, Rules: views, NotChecked: len(rules) - len(views)}
	stateRules := make([]any, len(views))
	for i, view := range views {
		paths := make([]any, len(view.Paths))
		for j, p := range view.Paths {
			paths[j] = p
		}
		stateRules[i] = map[string]any{"id": view.ID, "name": view.Name, "paths": paths, "body": view.Body}
		request.Questions[judgeablePrefix+view.ID] = judgeableQuestion(view.ID, view.Name)
		request.Questions[mechanicalPrefix+view.ID] = mechanicalQuestion(view.ID, view.Name)
		request.Order = append(request.Order, judgeablePrefix+view.ID, mechanicalPrefix+view.ID)
	}
	request.State = map[string]any{"about": rulesQuestions().checkAbout, "rules": stateRules}
	return request
}

// chunks splits a request past the per-request question limit into chunks that repeat the state, keeping the question
// order (pi-typesafe chunkEvaluationRequest).
func (r RulesCheckRequest) chunks() []Request {
	if len(r.Order) <= maxQuestionsPerRequest {
		return []Request{{State: r.State, Questions: r.Questions}}
	}
	var out []Request
	for i := 0; i < len(r.Order); i += maxQuestionsPerRequest {
		end := i + maxQuestionsPerRequest
		if end > len(r.Order) {
			end = len(r.Order)
		}
		questions := Questions{}
		for _, id := range r.Order[i:end] {
			questions[id] = r.Questions[id]
		}
		out = append(out, Request{State: r.State, Questions: questions})
	}
	return out
}

// ---------------------------------------------------------------------------
// The check

// RuleCheck is the check of one rule.
type RuleCheck struct {
	ID, Name string
	// Judgeability is the single reason the judge picked for the rule's judgeability.
	Judgeability string
	// JudgeabilityScore is P(the rule can be judged from one changed file alone).
	JudgeabilityScore float64
	// Mechanical is P(a linter, formatter, or type checker could enforce the rule exactly).
	Mechanical float64
	// NeedsAttention is true when the rule needs attention on either question.
	NeedsAttention bool
}

// RulesCheckResult is the outcome of a rules check.
type RulesCheckResult struct {
	Source  string // typesafe | error | skipped | aggregate | prose | none
	Sources []string
	// Checked counts the rules with a usable answer.
	Checked int
	// NotChecked counts the rules left out because the request would have been too large.
	NotChecked int
	// Unanswered counts the rules that went out but came back without an answer, because their request failed.
	Unanswered     int
	Results        []RuleCheck
	Attention      []RuleCheck
	Requests       int
	FailedRequests int
	Model          string
	ElapsedMs      int
	Error          string
	ErrorCode      string
	SkippedReason  string
}

// RulesCheckOptions are the inputs of a rules check.
type RulesCheckOptions struct {
	Ctx     context.Context
	Set     *RuleSet
	Judge   Judge
	Timeout time.Duration
}

// checkRules asks the judge about every rule in the active set and returns the per-rule answers. It never throws and
// never sends without a judge. Offline reasons come back as a plain source instead of a request: none (no rules file),
// prose (no rule-shaped sections), aggregate (a fallback document judged as one rule, so there are no separate rules to
// check), and skipped (no judge).
func checkRules(options RulesCheckOptions) RulesCheckResult {
	set := options.Set
	if set == nil {
		return RulesCheckResult{Source: "none"}
	}
	if set.ProseOnly {
		return RulesCheckResult{Source: "prose", Sources: set.Sources}
	}
	if set.isAggregate() {
		return RulesCheckResult{Source: "aggregate", Sources: set.Sources}
	}
	if options.Judge == nil {
		return RulesCheckResult{Source: "skipped", Sources: set.Sources, SkippedReason: "Jev judgments are off"}
	}
	ctx := options.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	request := buildRulesCheckRequest(set.Rules)
	chunks := request.chunks()
	outcomes := make([]AskAnswer, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i] = Ask(ctx, options.Judge, chunk, options.Timeout)
		}()
	}
	wg.Wait()
	answers := map[string]Answer{}
	failed := 0
	var firstError, firstCode, model string
	elapsed := 0
	for _, outcome := range outcomes {
		if !outcome.OK {
			failed++
			if firstError == "" {
				firstError = outcome.Error
			}
			if firstCode == "" {
				firstCode = outcome.ErrorCode
			}
			continue
		}
		for id, answer := range outcome.Answers {
			answers[id] = answer
		}
		if model == "" {
			model = outcome.Model
		}
		elapsed += outcome.ElapsedMs
	}
	results := []RuleCheck{}
	for _, view := range request.Rules {
		judgeable, hasJ := answers[judgeablePrefix+view.ID]
		mechanical, hasM := answers[mechanicalPrefix+view.ID]
		if !hasJ || judgeable.Choice == "" || !hasM || mechanical.Type != "noul" {
			continue
		}
		judgeability := judgeable.Choice
		if !isJudgeabilityReason(judgeability) {
			judgeability = "too_vague"
		}
		results = append(results, RuleCheck{
			ID: view.ID, Name: view.Name, Judgeability: judgeability,
			JudgeabilityScore: judgeable.Probabilities[judgeability],
			Mechanical:        mechanical.Noul,
			NeedsAttention:    judgeability != "from_change_alone" || mechanical.Noul >= mechanicalCutoff,
		})
	}
	attention := []RuleCheck{}
	for _, r := range results {
		if r.NeedsAttention {
			attention = append(attention, r)
		}
	}
	out := RulesCheckResult{
		Source: "typesafe", Sources: set.Sources, Checked: len(results), NotChecked: request.NotChecked,
		Unanswered: len(request.Rules) - len(results), Results: results, Attention: attention,
		Requests: len(chunks), FailedRequests: failed, Model: model, ElapsedMs: elapsed,
		Error: firstError, ErrorCode: firstCode,
	}
	if len(results) == 0 && firstError != "" {
		out.Source = "error"
	}
	return out
}

// ---------------------------------------------------------------------------
// The report

var checkWhy = map[string]string{
	"needs_other_files":     "needs another file to judge",
	"needs_task_or_history": "needs the task or the history to judge",
	"too_vague":             "too vague to judge twice",
}

var checkFix = map[string]string{
	"from_change_alone":     "name the concrete pattern a violation shows",
	"needs_other_files":     "split it so the changed file alone shows the violation, or leave it to review",
	"needs_task_or_history": "name the pattern a single file can show, or keep it as review-time advice",
	"too_vague":             "name the concrete pattern a violation shows",
}

// checkReasons are the reasons a rule needs attention, each with its score; empty for a rule judged healthy on both
// questions.
func checkReasons(rule RuleCheck) []string {
	var reasons []string
	if rule.Judgeability != "from_change_alone" {
		reasons = append(reasons, checkWhy[rule.Judgeability]+" ("+toFixed(rule.JudgeabilityScore, 2)+")")
	}
	if rule.Mechanical >= mechanicalCutoff {
		reasons = append(reasons, "a linter could enforce it exactly ("+toFixed(rule.Mechanical, 2)+")")
	}
	return reasons
}

// formatRuleCheck is one line per rule that needs attention: why, then one short suggestion.
func formatRuleCheck(rule RuleCheck) string {
	fix := checkFix[rule.Judgeability]
	if rule.Mechanical >= mechanicalCutoff {
		fix = "move it to your linter"
	}
	return "- " + rule.Name + " (" + rule.ID + "): " + strings.Join(checkReasons(rule), "; ") + ". Fix: " + fix + "."
}

func orNoAnswer(s string) string {
	if s == "" {
		return "no answer"
	}
	return s
}

// formatRulesCheck is the whole report: one line per rule that needs attention, then the one-line summary.
func formatRulesCheck(result RulesCheckResult) string {
	switch result.Source {
	case "none":
		return "No rules file detected. Run /warden init to create project-specific rules."
	case "prose":
		return "No rules to check: " + strings.Join(result.Sources, ", ") + " has no rule-shaped sections, so the guard judges nothing there."
	case "aggregate":
		return "No separate rules to check: " + strings.Join(result.Sources, ", ") + " has no rule headings, so the guard judges it as one document."
	case "skipped":
		return "Jev judgments are off, so the rules check sent nothing. /warden rules shows the rule set locally."
	case "error":
		return "Rules check failed after " + strconv.Itoa(result.Requests) + " " + pluralOf(result.Requests, "request", "requests") + ": " + orNoAnswer(result.Error) + ". Nothing was changed; /warden rules shows the rule set locally."
	}
	header := "Rules check: " + strings.Join(result.Sources, ", ") + " — " + strconv.Itoa(result.Checked) + " " + pluralOf(result.Checked, "rule", "rules") + " checked in " + strconv.Itoa(result.Requests) + " " + pluralOf(result.Requests, "request", "requests")
	if result.NotChecked > 0 {
		header += ", " + strconv.Itoa(result.NotChecked) + " left out (the request would be too large)"
	}
	if result.Unanswered > 0 {
		header += ", " + strconv.Itoa(result.Unanswered) + " without an answer"
	}
	if result.FailedRequests > 0 {
		header += ", " + strconv.Itoa(result.FailedRequests) + " " + pluralOf(result.FailedRequests, "request", "requests") + " failed: " + orNoAnswer(result.Error)
	}
	header += "."
	if result.Checked == 0 {
		return header + " Nothing to inspect."
	}
	lines := []string{header}
	for _, rule := range result.Attention {
		lines = append(lines, formatRuleCheck(rule))
	}
	lines = append(lines, strconv.Itoa(result.Checked-len(result.Attention))+" fine, "+strconv.Itoa(len(result.Attention))+" need attention.")
	return strings.Join(lines, "\n")
}
