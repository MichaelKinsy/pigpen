package pi_goal_x

// ported from goal-oracle.ts: opt-in read-only Blocker Oracle (issue #26). Hard safety properties: disabled by default,
// explicit provider/model, nested session with ONLY read/grep/find/ls plus a structured submit tool, one successful
// consultation per blocker fingerprint, actionable advice keeps the goal ACTIVE and arms a follow-up marker.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

func oracleHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

// normalizeBlockerReason is reason.normalize("NFKC").toLowerCase().replace(/\s+/g, " ").trim().
func normalizeBlockerReason(reason string) string {
	s := strings.ToLower(norm.NFKC.String(reason))
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return jsTrim(b.String())
}

// taskStateFingerprint is a stable short hash over task-tree shape + contracts (not usage/timestamps).
func taskStateFingerprint(goal *jsObject) string {
	var lines []string
	var walk func(tasks []any, depth int)
	walk = func(tasks []any, depth int) {
		for _, x := range tasks {
			t, _ := x.(*jsObject)
			if t == nil {
				continue
			}
			lines = append(lines, strconv.Itoa(depth)+":"+gstr(t, "id")+":"+gstr(t, "status")+":"+gstr(t, "verificationContract"))
			if subs, ok := t.vals["subtasks"].([]any); ok {
				walk(subs, depth+1)
			}
		}
	}
	if tl := goal.obj("taskList"); tl != nil {
		tasks, _ := tl.vals["tasks"].([]any)
		walk(tasks, 0)
	}
	return oracleHash(strings.Join(lines, "|"), 16)
}

// buildBlockerFingerprint distinguishes materially different blockers without changing when usage, elapsed time or
// revision counters change. The hashed input is JSON.stringify of BlockerFingerprintInput (key order preserved;
// currentTaskId is omitted when undefined).
func buildBlockerFingerprint(goal *jsObject, reason string) string {
	in := newObject()
	in.set("goalId", gstr(goal, "id"))
	in.set("objectiveHash", oracleHash(gstr(goal, "objective"), 16))
	in.set("verificationContractHash", oracleHash(gstr(goal, "verificationContract"), 16))
	in.set("taskStateHash", taskStateFingerprint(goal))
	if cur, ok := goal.str("currentTaskId"); ok {
		in.set("currentTaskId", cur)
	}
	in.set("normalizedReason", normalizeBlockerReason(reason))
	return oracleHash(marshalJSON(in, ""), 24)
}

type oracleConsultResult struct{ adviceID, disposition, summary string }
type oracleConsultFailure struct{ errorCode, message string }

type oracleConsultState struct {
	result            *oracleConsultResult
	failedAttempts    int
	lastFailure       *oracleConsultFailure
	followupAttempted bool
}

// oracleStateForFingerprint reconstructs per-fingerprint consult state from raw ledger events.
func oracleStateForFingerprint(events []*jsObject, goalID, fingerprint string) oracleConsultState {
	var state oracleConsultState
	for _, e := range events {
		typ := gstr(e, "type")
		if typ != "oracle_started" && typ != "oracle_result" && typ != "oracle_failed" && typ != "oracle_followup_attempted" {
			continue
		}
		if gstr(e, "goalId") != goalID || gstr(e, "fingerprint") != fingerprint {
			continue
		}
		switch typ {
		case "oracle_result":
			state.result = &oracleConsultResult{adviceID: gstr(e, "adviceId"), disposition: gstr(e, "disposition"), summary: gstr(e, "summary")}
		case "oracle_failed":
			state.failedAttempts++
			state.lastFailure = &oracleConsultFailure{errorCode: gstr(e, "errorCode"), message: gstr(e, "message")}
		case "oracle_followup_attempted":
			state.followupAttempted = true
		}
	}
	return state
}

type oracleAlternative struct {
	title, rationale string
	steps            []string
	expectedEvidence []string
}

type oracleAdvice struct {
	diagnosis           string
	alternatives        []oracleAlternative
	recommendedIndex    int
	unresolvedQuestions []string
	disposition         string // actionable | needs_human | insufficient_context
}

// oracleRunResult is the OracleRunResult union: ok with advice, or an errorCode (config|provider|aborted|invalid_output) and message.
type oracleRunResult struct {
	ok        bool
	advice    *oracleAdvice
	errorCode string
	message   string
}

func buildBlockerOraclePrompt(goal *jsObject, reason string, attemptedActions []string, recentEvidence string) string {
	parts := []string{
		"You are a read-only blocker adviser.",
		"Find concrete alternative paths. Do not mutate files.",
		"Do not mark the goal complete or blocked.",
		"Return advice only through submit_goal_oracle_advice.",
		"",
		"<objective>",
		escapePromptPayload(gstr(goal, "objective")),
		"</objective>",
	}
	if c := jsTrim(gstr(goal, "verificationContract")); c != "" {
		parts = append(parts, "<verification_contract>\n"+escapePromptPayload(c)+"\n</verification_contract>")
	} else {
		parts = append(parts, "")
	}
	parts = append(parts, "<blocker_reason>\n"+escapePromptPayload(reason)+"\n</blocker_reason>")
	if len(attemptedActions) > 0 {
		lines := make([]string, len(attemptedActions))
		for i, a := range attemptedActions {
			lines[i] = "- " + a
		}
		parts = append(parts, "<attempted_actions>\n"+strings.Join(lines, "\n")+"\n</attempted_actions>")
	} else {
		parts = append(parts, "")
	}
	if jsTrim(recentEvidence) != "" {
		parts = append(parts, "<recent_evidence>\n"+escapePromptPayload(utf16Prefix(recentEvidence, 2000))+"\n</recent_evidence>")
	} else {
		parts = append(parts, "")
	}
	parts = append(parts, "", "Inspect relevant files with read-only tools before advising when needed.")
	// The original joins with "\n\n" after .filter(Boolean): empty strings drop out.
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

func oracleStringList(v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// validateAdvice validates untrusted submitted advice against the bounded schema semantics; errMsg is "" when valid.
// A non-integer recommendedIndex is rejected as out of range (the original would crash on it later).
func validateAdvice(value any) (*oracleAdvice, string) {
	raw, _ := value.(*jsObject)
	if raw == nil {
		return nil, "advice must be an object"
	}
	diagnosis, _ := raw.str("diagnosis")
	if jsTrim(diagnosis) == "" {
		return nil, "diagnosis required"
	}
	alts, ok := raw.vals["alternatives"].([]any)
	if !ok || len(alts) < 1 || len(alts) > 4 {
		return nil, "1-4 alternatives required"
	}
	advice := &oracleAdvice{diagnosis: diagnosis}
	for _, x := range alts {
		alt, _ := x.(*jsObject)
		if alt == nil {
			return nil, "alternative title/rationale required"
		}
		title, okT := alt.str("title")
		rationale, okR := alt.str("rationale")
		if !okT || !okR {
			return nil, "alternative title/rationale required"
		}
		steps, okS := oracleStringList(alt.vals["steps"])
		if !okS || len(steps) < 1 {
			return nil, "steps must be non-empty strings"
		}
		evidence, _ := oracleStringList(alt.vals["expectedEvidence"])
		advice.alternatives = append(advice.alternatives, oracleAlternative{title: title, rationale: rationale, steps: steps, expectedEvidence: evidence})
	}
	idx, ok := raw.num("recommendedIndex")
	if !ok || idx < 0 || idx >= float64(len(alts)) || idx != float64(int(idx)) {
		return nil, "recommendedIndex out of range"
	}
	advice.recommendedIndex = int(idx)
	disp, _ := raw.str("disposition")
	if disp != "actionable" && disp != "needs_human" && disp != "insufficient_context" {
		return nil, "disposition must be actionable|needs_human|insufficient_context"
	}
	advice.disposition = disp
	advice.unresolvedQuestions, _ = oracleStringList(raw.vals["unresolvedQuestions"])
	return advice, ""
}

// resolveOracleModel resolves the EXPLICIT Oracle model; provider-only or missing config is refused (message != "").
func resolveOracleModel(ctx auditorContext, settings resolvedGoalOracleSettings) (*auditorModel, string) {
	if settings.provider == "" || settings.model == "" {
		return nil, "The blocker Oracle is enabled but not fully configured: set both oracle.provider and oracle.model in settings. The executor model is NOT used silently."
	}
	var found *auditorModel
	if r := ctx.modelRegistry(); r != nil {
		found = r.find(settings.provider, settings.model)
	}
	if found == nil {
		return nil, "Configured Oracle model not found: " + settings.provider + "/" + settings.model + "."
	}
	return found, ""
}

type oracleToolContent struct{ typ, text string }
type oracleToolResult struct{ content []oracleToolContent }

// goalOracleTool is the defineTool() submit tool handed to the nested session as a custom tool.
type goalOracleTool struct {
	name, label, description, promptSnippet string
	parameters                              *jsObject // JSON schema of OracleAdviceSchema
	execute                                 func(toolCallID string, params *jsObject) oracleToolResult
}

const oracleAdviceSchemaJSON = `{"type":"object","properties":{` +
	`"diagnosis":{"type":"string","maxLength":2000},` +
	`"alternatives":{"type":"array","minItems":1,"maxItems":4,"items":{"type":"object","properties":{` +
	`"title":{"type":"string","maxLength":200},"rationale":{"type":"string","maxLength":1000},` +
	`"steps":{"type":"array","minItems":1,"maxItems":8,"items":{"type":"string","maxLength":400}},` +
	`"expectedEvidence":{"type":"array","maxItems":8,"items":{"type":"string","maxLength":400}}},` +
	`"required":["title","rationale","steps","expectedEvidence"]}},` +
	`"recommendedIndex":{"type":"integer","minimum":0,"maximum":3},` +
	`"unresolvedQuestions":{"type":"array","maxItems":6,"items":{"type":"string","maxLength":400}},` +
	`"disposition":{"anyOf":[{"const":"actionable","type":"string"},{"const":"needs_human","type":"string"},{"const":"insufficient_context","type":"string"}]}},` +
	`"required":["diagnosis","alternatives","recommendedIndex","unresolvedQuestions","disposition"]}`

type runBlockerOracleArgs struct {
	ctx              auditorContext
	goal             *jsObject
	reason           string
	attemptedActions []string
	settings         resolvedGoalOracleSettings
	recentEvidence   string
	signal           context.Context
	createSession    auditorSessionFactory
}

func (a runBlockerOracleArgs) aborted() bool { return a.signal != nil && a.signal.Err() != nil }

// runBlockerOracle runs ONE isolated read-only Oracle consultation.
func runBlockerOracle(args runBlockerOracleArgs) oracleRunResult {
	model, msg := resolveOracleModel(args.ctx, args.settings)
	if msg != "" {
		return oracleRunResult{errorCode: "config", message: msg}
	}
	abortedResult := oracleRunResult{errorCode: "aborted", message: "Oracle consultation aborted."}

	var mu sync.Mutex
	var submitted *oracleAdvice
	schema, _ := parseJSON([]byte(oracleAdviceSchemaJSON))
	schemaObj, _ := schema.(*jsObject)
	submitTool := &goalOracleTool{
		name:          "submit_goal_oracle_advice",
		label:         "Submit Oracle Advice",
		description:   "Submit your structured blocker advice. This is the ONLY way to return a result.",
		promptSnippet: "Submit structured advice via submit_goal_oracle_advice.",
		parameters:    schemaObj,
		execute: func(_ string, params *jsObject) oracleToolResult {
			advice, errMsg := validateAdvice(params)
			if errMsg != "" {
				return oracleToolResult{content: []oracleToolContent{{"text", "Invalid advice: " + errMsg}}}
			}
			mu.Lock()
			submitted = advice
			mu.Unlock()
			return oracleToolResult{content: []oracleToolContent{{"text", "Advice recorded."}}}
		},
	}
	provider := func(err error) oracleRunResult {
		if args.aborted() {
			return abortedResult
		}
		return oracleRunResult{errorCode: "provider", message: err.Error()}
	}
	if args.createSession == nil {
		return provider(errNoAuditorSessionFactory)
	}
	opts := auditorSessionOptions{
		cwd: args.ctx.cwd(), model: model, thinkingLevel: args.settings.thinkingLevel,
		inMemorySessionCwd: args.ctx.cwd(), compactionEnabled: false,
		// Read-only tool profile: NO bash/write/edit is exposed to the Oracle.
		tools:       []string{"read", "grep", "find", "ls", "submit_goal_oracle_advice"},
		customTools: []*goalOracleTool{submitTool},
	}
	mo := resolveAuditorSessionModelOptions(args.ctx)
	opts.modelRegistry, opts.modelRuntime = mo.modelRegistry, mo.modelRuntime
	if !args.settings.projectResources {
		opts.resourceLoader = makeAuditorResourceLoader(
			"You are a read-only blocker adviser. Inspect evidence and suggest concrete alternative paths. Never modify files or decide goal outcomes. Return advice through submit_goal_oracle_advice.")
	}
	session, err := args.createSession.createSession(opts)
	if err != nil {
		return provider(err)
	}
	stopWatch := watchAbortSignal(args.signal, session.abort)
	var promptErr error
	preAborted := false
	func() {
		defer func() {
			stopWatch()
			session.dispose()
		}()
		if args.aborted() {
			preAborted = true
			return
		}
		attempted := args.attemptedActions
		if len(attempted) > 8 {
			attempted = attempted[:8]
		}
		promptErr = session.prompt(buildBlockerOraclePrompt(args.goal, args.reason, attempted, args.recentEvidence))
	}()
	if preAborted {
		return abortedResult
	}
	if promptErr != nil {
		return provider(promptErr)
	}
	if args.aborted() {
		return abortedResult
	}
	mu.Lock()
	defer mu.Unlock()
	if submitted == nil {
		return oracleRunResult{errorCode: "invalid_output", message: "Oracle returned no structured advice."}
	}
	return oracleRunResult{ok: true, advice: submitted}
}

// ── armed follow-up marker (session-scoped) ────────────────────────────────

type armedAdvice struct{ goalID, fingerprint, adviceID, text string }

var armedByGoalID = map[string]armedAdvice{}

func armOracleAdvice(goalID, fingerprint string, advice oracleAdvice) string {
	title := "advice"
	if advice.recommendedIndex < len(advice.alternatives) {
		title = advice.alternatives[advice.recommendedIndex].title
	}
	id := safeIdPart(title)
	if n := len(id); n > 24 {
		id = id[:24]
	}
	adviceID := fingerprint + "-" + id
	rec := advice.alternatives[advice.recommendedIndex]
	lines := []string{"Oracle suggested an alternative path:", rec.title + " — " + rec.rationale, "Steps:"}
	for i, s := range rec.steps {
		lines = append(lines, "  "+strconv.Itoa(i+1)+". "+s)
	}
	if len(rec.expectedEvidence) > 0 {
		lines = append(lines, "Expected evidence: "+strings.Join(rec.expectedEvidence, "; "))
	}
	lines = append(lines, "Attempt this path before reporting blocked again.")
	armedByGoalID[goalID] = armedAdvice{goalID: goalID, fingerprint: fingerprint, adviceID: adviceID, text: strings.Join(lines, "\n")}
	return adviceID
}

func hasPendingOracleAdviceForFocusedGoal(goalID string) bool {
	_, ok := armedByGoalID[goalID]
	return ok
}

// consumeOracleFollowupMarker consumes the armed marker after a meaningful work attempt.
func consumeOracleFollowupMarker(goalID string) (armedAdvice, bool) {
	a, ok := armedByGoalID[goalID]
	delete(armedByGoalID, goalID)
	return a, ok
}

func renderActionableOracleAdvice(advice oracleAdvice) string {
	rec := advice.alternatives[advice.recommendedIndex]
	lines := []string{"Oracle diagnosis: " + advice.diagnosis, "Recommended alternative: " + rec.title + " — " + rec.rationale}
	for i, s := range rec.steps {
		lines = append(lines, "  "+strconv.Itoa(i+1)+". "+s)
	}
	lines = append(lines, `Try this path before calling update_goal({status:"blocked"}) again.`)
	return strings.Join(lines, "\n")
}

func renderOracleAdviceReminder(armed armedAdvice) string {
	return armed.text + "\n(The same blocker fingerprint already received Oracle advice; no new consultation will run until you attempt it.)"
}
