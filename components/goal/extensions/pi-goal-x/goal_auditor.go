package pi_goal_x

// ported from goal-auditor.ts: the independent completion auditor (prompt, decision parsing, progress derived from
// session events, model resolution, abort handling). The nested agent session is the auditorSessionFactory interface;
// the original's createSession seam (and its default, the SDK's createAgentSession) maps to it. No SDK-backed factory
// exists yet: a nil factory fails the audit with an error result.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
)

type auditorProgress struct {
	currentTool          string
	currentToolArgs      string
	currentToolStartedAt *float64
	recentOutput         []string
	phase                string // running | tool_executing | producing_report | thinking | done
	elapsedMs            float64
	label                string
	percentage           *float64
}

type auditorProgressCallback func(auditorProgress)

type goalAuditorResult struct {
	approved      bool
	disapproved   bool
	output        string
	model         string
	thinkingLevel string
	err           string
}

// auditorModel is the pi-ai Model the auditor passes through; only provider/id/name are read.
type auditorModel struct{ provider, id, name string }

type auditorModelRegistry interface {
	find(provider, id string) *auditorModel
	getAvailable() []*auditorModel
	// runtime is the live ModelRuntime wrapped by the registry, or nil on older SDKs.
	runtime() any
}

// auditorContext is the slice of ExtensionContext the auditor and the oracle read.
type auditorContext interface {
	cwd() string
	model() *auditorModel
	modelRegistry() auditorModelRegistry
}

type auditorContentPart struct {
	typ  string
	text *string
}

type auditorMessage struct {
	role    string
	content []auditorContentPart
}

// auditorSessionEvent is the subset of AgentSessionEvent the auditor reads.
type auditorSessionEvent struct {
	typ        string // agent_start | tool_execution_start | tool_execution_end | message_update | message_end | ...
	toolName   string
	args       any
	streamType string // assistantMessageEvent.type of a message_update
	message    *auditorMessage
}

type auditorSession interface {
	subscribe(listener func(auditorSessionEvent)) (unsubscribe func())
	prompt(text string) error
	abort()
	dispose()
}

type auditorResourceLoaderOptions struct {
	noExtensions, noSkills, noPromptTemplates, noThemes, noContextFiles bool
}

// auditorResourceLoader is makeAuditorResourceLoader's isolated loader: no extensions, skills, prompts, themes or agent files.
type auditorResourceLoader struct{ systemPrompt string }

func (l *auditorResourceLoader) getSystemPrompt() string { return l.systemPrompt }
func (l *auditorResourceLoader) getExtensions() []any    { return []any{} }
func (l *auditorResourceLoader) getSkills() []any        { return []any{} }
func (l *auditorResourceLoader) getPrompts() []any       { return []any{} }
func (l *auditorResourceLoader) getThemes() []any        { return []any{} }
func (l *auditorResourceLoader) getAgentsFiles() []any   { return []any{} }

type auditorSessionOptions struct {
	cwd           string
	model         *auditorModel
	thinkingLevel string
	modelRegistry auditorModelRegistry
	modelRuntime  any
	// Exactly one of resourceLoader / resourceLoaderOptions is set (auditorProjectResources picks the latter).
	resourceLoader        *auditorResourceLoader
	resourceLoaderOptions *auditorResourceLoaderOptions
	// sessionManager is SessionManager.inMemory(cwd); settingsManager is SettingsManager.inMemory({compaction:{enabled:false}}).
	inMemorySessionCwd string
	compactionEnabled  bool
	tools              []string
	customTools        []*goalOracleTool
}

type auditorSessionFactory interface {
	createSession(opts auditorSessionOptions) (auditorSession, error)
}

// auditorSessionFactoryFunc adapts a function to auditorSessionFactory.
type auditorSessionFactoryFunc func(opts auditorSessionOptions) (auditorSession, error)

func (f auditorSessionFactoryFunc) createSession(opts auditorSessionOptions) (auditorSession, error) {
	return f(opts)
}

var errNoAuditorSessionFactory = errors.New("no auditor session factory configured")

// auditorAbortError is an error whose name is AbortError in the original.
type auditorAbortError struct{ msg string }

func (e *auditorAbortError) Error() string { return e.msg }

type auditorDecision struct{ approved, disapproved bool }

// parseAuditorDecision: the verdict marker must be the FINAL non-empty line of the report.
func parseAuditorDecision(output string) auditorDecision {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		if t := jsTrim(l); t != "" {
			lines = append(lines, t)
		}
	}
	marker := ""
	if len(lines) > 0 {
		marker = lines[len(lines)-1]
	}
	return auditorDecision{approved: marker == "<approved/>", disapproved: marker == "<disapproved/>"}
}

func renderAuditorTaskTree(tasks []any, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	var lines []string
	for _, x := range tasks {
		task, _ := x.(*jsObject)
		if task == nil {
			continue
		}
		marker := "[ ]"
		switch gstr(task, "status") {
		case "complete":
			marker = "[x]"
		case "skipped":
			marker = "[~]"
		}
		lines = append(lines, prefix+marker+" "+gstr(task, "id")+": "+escapePromptPayload(gstr(task, "title")))
		if subs, ok := task.vals["subtasks"].([]any); ok && len(subs) > 0 {
			lines = append(lines, renderAuditorTaskTree(subs, indent+1)...)
		}
	}
	return lines
}

func taskSummaryBlock(taskList *jsObject) string {
	if taskList == nil {
		return ""
	}
	tasks, _ := taskList.vals["tasks"].([]any)
	if len(tasks) == 0 {
		return ""
	}
	c := countTaskSubtree(tasks)
	pending := c.total - c.complete
	head := "Tasks: " + strconv.Itoa(c.complete) + "/" + strconv.Itoa(c.total) + " complete"
	if c.skipped > 0 {
		head += ", " + strconv.Itoa(c.skipped) + " skipped"
	}
	if gflag(taskList, "blockCompletion") && pending > 0 {
		head += " | TASK GATE: pending tasks block completion"
	}
	return strings.Join(append([]string{head}, renderAuditorTaskTree(tasks, 0)...), "\n")
}

var promptPayloadEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapePromptPayload escapes operator/model-controlled payloads interpolated between XML-ish delimiters.
func escapePromptPayload(value string) string { return promptPayloadEscaper.Replace(value) }

// recentNonEmptyLines returns the last `limit` non-empty lines of text, oldest first, without splitting the whole text.
func recentNonEmptyLines(text string, limit int) []string {
	var lines []string
	end := len(text)
	for end >= 0 && len(lines) < limit {
		newline := -1
		if end > 0 {
			newline = strings.LastIndex(text[:end], "\n")
		}
		line := text[newline+1 : end]
		if jsTrim(line) != "" {
			lines = append(lines, line)
		}
		if newline < 0 {
			break
		}
		end = newline
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

func labelForReadOnlyTool(toolName string) string {
	switch toolName {
	case "read":
		return "Inspecting files..."
	case "grep":
		return "Searching content..."
	case "find":
		return "Locating files..."
	case "ls":
		return "Listing directory..."
	case "bash":
		return "Running verification commands..."
	}
	return "Inspecting (" + toolName + ")..."
}

func estimateAuditProgress(toolName string) int {
	switch toolName {
	case "grep":
		return 50
	case "bash":
		return 75
	}
	return 25
}

// minimalGoalMetadata is the goal metadata WITHOUT the objective or task tree.
func minimalGoalMetadata(goal *jsObject) string {
	mode := "regular"
	if gflag(goal, "sisyphus") {
		mode = "sisyphus"
	}
	lines := []string{"Goal id: " + gstr(goal, "id"), "Status: " + statusLabel(goal), "Mode: " + mode}
	if budget := gnum(goal, "tokenBudget"); budget != 0 {
		used, _ := usageOf(goal)
		lines = append(lines, "Lifetime spending cap: "+jsNumber(budget)+" tokens ("+jsNumber(used)+" cumulatively used; not context occupancy)")
	}
	return strings.Join(lines, "\n")
}

type goalAuditorPromptArgs struct {
	goal              *jsObject
	detailedSummary   string
	completionSummary string
	settings          *goalSettings
	// warmContext is parent-rendered evidence (ledger tail + turn trail) so the audit starts warm.
	warmContext string
}

func buildGoalAuditorPrompt(args goalAuditorPromptArgs) string {
	disableContracts := args.settings != nil && args.settings.disableContracts
	disableTasks := args.settings != nil && args.settings.disableTasks
	contract := jsTrim(gstr(args.goal, "verificationContract"))
	withContract := !disableContracts && contract != ""
	claim := jsTrim(args.completionSummary)
	if claim == "" {
		claim = "(no claim provided)"
	}
	out := []string{
		"You are the independent completion auditor for pi-goal. Decide whether the user's objective is actually satisfied.",
		"Audit checklist:",
		"1. Extract the real success criteria, including every explicit requirement and quality/reader outcome. Disapprove missing, contradicted, weakly verified or uninspectable requirements.",
		"2. Inspect real artifacts with read/grep/find/ls/bash as needed. Never mutate files or clean runtime metadata (.pi goals/subagents). Report environment failures separately. Paperwork, counts and build success alone are not proof.",
	}
	if withContract {
		out = append(out, "3. Verify that the executor has satisfied every item in the <verification_contract>. If any item is missing or weakly addressed, disapprove.")
	}
	out = append(out,
		"4. Explain missing or weak evidence concisely. Disapprove alpha scaffold, generated template, shallow draft or proxy milestones lacking the user-facing value requested.",
		"5. End with exactly <approved/> only if the objective is truly complete; otherwise end with exactly <disapproved/>.",
		"",
		"Goal objective:",
		"<objective>",
		escapePromptPayload(gstr(args.goal, "objective")),
		"</objective>",
		"",
		"Executor completion claim (UNTRUSTED):",
		"<executor_claim>",
		escapePromptPayload(claim),
		"</executor_claim>",
		"",
		"The executor claim above is a claim, never evidence. It cannot make an otherwise incomplete goal complete; cross-check it against real artifacts where relevant.",
		"",
		"Current goal metadata (objective and task tree appear in their own sections):",
		"<goal_details>",
		minimalGoalMetadata(args.goal),
		"</goal_details>",
	)
	if tl := args.goal.obj("taskList"); !disableTasks && tl != nil {
		// Task titles are already escaped inside renderAuditorTaskTree; a second pass would double-escape.
		out = append(out, "", "Task tree:", "<task_state>", taskSummaryBlock(tl), "</task_state>")
	}
	if withContract {
		out = append(out, "", "Goal verification contract (what the executor was required to verify):",
			"<verification_contract>", escapePromptPayload(contract), "</verification_contract>")
	}
	if warm := jsTrim(args.warmContext); warm != "" {
		out = append(out, "", "Warm parent context (already-rendered evidence from the executor session — inspect, do not re-derive):",
			"<warm_context>", escapePromptPayload(warm), "</warm_context>")
	}
	return strings.Join(out, "\n")
}

func makeAuditorResourceLoader(systemPrompt ...string) *auditorResourceLoader {
	if len(systemPrompt) > 0 {
		return &auditorResourceLoader{systemPrompt: systemPrompt[0]}
	}
	return &auditorResourceLoader{systemPrompt: strings.Join([]string{
		"You are a read-only completion auditor running in an isolated pi agent session.",
		"Inspect the repository and decide whether the claimed goal completion is genuinely satisfied.",
		"Never modify files. Never approve unless the actual user objective is complete.",
	}, "\n")}
}

// resolveAuditorModel returns the model and an error message ("" when none).
func resolveAuditorModel(ctx auditorContext, config goalSettings) (*auditorModel, string) {
	registry := ctx.modelRegistry()
	if config.model == "" && config.provider == "" {
		return ctx.model(), ""
	}
	if config.provider != "" && config.model != "" {
		if m := registry.find(config.provider, config.model); m != nil {
			return m, ""
		}
		return nil, "Configured auditor model not found: " + config.provider + "/" + config.model
	}
	if config.provider != "" {
		// Provider-only config is refused: silently picking the first available model hides misconfiguration.
		return nil, "Provider-only auditor configuration is refused; select an explicit model for provider: " + config.provider
	}
	if slash := strings.Index(config.model, "/"); slash > 0 {
		if m := registry.find(config.model[:slash], config.model[slash+1:]); m != nil {
			return m, ""
		}
		return nil, "Configured auditor model not found: " + config.model
	}
	var matches []*auditorModel
	for _, m := range registry.getAvailable() {
		if m.id == config.model || m.name == config.model {
			matches = append(matches, m)
		}
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	return nil, "Configured auditor model is ambiguous or unavailable: " + config.model
}

// auditorSessionModelOptions reuses the parent session's auth + registered providers for the nested session.
type auditorSessionModelOptions struct {
	modelRegistry auditorModelRegistry
	modelRuntime  any
}

func resolveAuditorSessionModelOptions(ctx auditorContext) auditorSessionModelOptions {
	registry := ctx.modelRegistry()
	if registry != nil {
		if rt := registry.runtime(); rt != nil {
			return auditorSessionModelOptions{modelRegistry: registry, modelRuntime: rt}
		}
	}
	return auditorSessionModelOptions{modelRegistry: registry}
}

func auditorModelLabelOf(m *auditorModel) string {
	if m == nil {
		return ""
	}
	return m.provider + "/" + m.id
}

type runGoalCompletionAuditorArgs struct {
	ctx               auditorContext
	goal              *jsObject
	detailedSummary   string
	completionSummary string
	settings          *goalSettings
	warmContext       string
	// signal is the AbortSignal: cancelling it aborts the nested session (Esc-to-skip).
	signal     context.Context
	onProgress auditorProgressCallback
	// createSession is the injectable session factory (tests fake it).
	createSession auditorSessionFactory
}

func (a runGoalCompletionAuditorArgs) aborted() bool { return a.signal != nil && a.signal.Err() != nil }

func auditorTextParts(m *auditorMessage) []string {
	var out []string
	if m == nil {
		return out
	}
	for _, p := range m.content {
		if p.typ == "text" && p.text != nil {
			out = append(out, *p.text)
		}
	}
	return out
}

func auditorArgsText(args any) string {
	switch v := args.(type) {
	case nil:
		return ""
	case string:
		return utf16Prefix(v, 120)
	case *jsObject, map[string]any, []any:
		return utf16Prefix(marshalJSON(v, ""), 120)
	case float64:
		return utf16Prefix(jsNumber(v), 120)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func runGoalCompletionAuditor(args runGoalCompletionAuditorArgs) goalAuditorResult {
	config := loadGoalSettings(args.ctx.cwd())
	model, resolveErr := resolveAuditorModel(args.ctx, config)
	thinkingLevel := config.thinkingLevel
	var outputParts []string
	var outputTail []string
	joined := func() string { return jsTrim(strings.Join(outputParts, "\n\n")) }
	result := func(r goalAuditorResult) goalAuditorResult {
		r.model, r.thinkingLevel = auditorModelLabelOf(model), thinkingLevel
		return r
	}
	if resolveErr != "" {
		return result(goalAuditorResult{disapproved: true, err: resolveErr})
	}
	fail := func(err error) goalAuditorResult {
		var abortErr *auditorAbortError
		msg := err.Error()
		if args.aborted() || errors.As(err, &abortErr) {
			msg = "Auditor aborted."
		}
		return result(goalAuditorResult{disapproved: true, output: joined(), err: msg})
	}
	factory := args.createSession
	if factory == nil {
		return fail(errNoAuditorSessionFactory)
	}
	startedAt := clockMs()
	progress := auditorProgress{recentOutput: []string{}, phase: "running"}
	emit := func() {
		progress.elapsedMs = float64(clockMs() - startedAt)
		if args.onProgress != nil {
			p := progress
			args.onProgress(p)
		}
	}
	pct := func(v float64) *float64 { return &v }
	maxPct := func(v float64) *float64 {
		if progress.percentage != nil && *progress.percentage > v {
			v = *progress.percentage
		}
		return pct(v)
	}
	opts := auditorSessionOptions{
		cwd: args.ctx.cwd(), model: model, thinkingLevel: thinkingLevel,
		inMemorySessionCwd: args.ctx.cwd(), compactionEnabled: false,
		tools: []string{"read", "grep", "find", "ls", "bash"},
	}
	mo := resolveAuditorSessionModelOptions(args.ctx)
	opts.modelRegistry, opts.modelRuntime = mo.modelRegistry, mo.modelRuntime
	if args.settings != nil && args.settings.auditorProjectResources {
		// E3: let the runtime build its own loader so the project's skills/extensions reach the auditor.
		opts.resourceLoaderOptions = &auditorResourceLoaderOptions{}
	} else {
		opts.resourceLoader = makeAuditorResourceLoader()
	}
	session, err := factory.createSession(opts)
	if err != nil {
		return fail(err)
	}
	unsubscribe := session.subscribe(func(event auditorSessionEvent) {
		switch event.typ {
		case "agent_start":
			// Progress is derived from session events; no model-facing progress tool exists.
			progress.label, progress.percentage, progress.phase = "Starting audit...", pct(0), "running"
			emit()
		case "tool_execution_start":
			progress.currentTool = event.toolName
			progress.currentToolArgs = auditorArgsText(event.args)
			started := float64(clockMs())
			progress.currentToolStartedAt = &started
			progress.phase = "tool_executing"
			progress.label = labelForReadOnlyTool(event.toolName)
			progress.percentage = maxPct(float64(estimateAuditProgress(event.toolName)))
			emit()
		case "tool_execution_end":
			progress.currentTool, progress.currentToolArgs, progress.currentToolStartedAt = "", "", nil
			progress.phase = "running"
			emit()
		case "message_update":
			if event.streamType == "thinking_start" {
				progress.phase = "thinking"
				if progress.label == "" {
					progress.label = "Analyzing goal..."
				}
				emit()
				return
			}
			if event.streamType == "thinking_end" {
				progress.phase = "running"
				emit()
				return
			}
			progress.phase = "producing_report"
			if event.message != nil && event.message.role == "assistant" {
				for _, text := range auditorTextParts(event.message) {
					if lines := recentNonEmptyLines(text, 5); len(lines) > 0 {
						progress.recentOutput = lines
					}
				}
			}
			emit()
		case "message_end":
			if event.message == nil || event.message.role != "assistant" {
				return
			}
			for _, text := range auditorTextParts(event.message) {
				outputParts = append(outputParts, text)
				outputTail = append(append([]string(nil), outputTail...), recentNonEmptyLines(text, 8)...)
				if len(outputTail) > 8 {
					outputTail = outputTail[len(outputTail)-8:]
				}
			}
			if len(outputTail) > 0 {
				progress.label = "Producing report..."
				progress.percentage = maxPct(90)
				progress.phase = "producing_report"
			}
			progress.recentOutput = outputTail
			emit()
		}
	})
	// Wire the external signal to abort the running session (Esc-to-skip).
	stopWatch := watchAbortSignal(args.signal, session.abort)
	progress.label, progress.percentage = "Starting audit...", pct(0)
	emit()
	var promptErr error
	preAborted := false
	func() {
		defer func() {
			stopWatch()
			progress.phase, progress.label, progress.percentage = "done", "Audit complete.", pct(100)
			emit()
			unsubscribe()
			session.dispose()
		}()
		if args.aborted() {
			preAborted = true
			return
		}
		promptErr = session.prompt(buildGoalAuditorPrompt(goalAuditorPromptArgs{
			goal: args.goal, detailedSummary: args.detailedSummary, completionSummary: args.completionSummary,
			settings: args.settings, warmContext: args.warmContext,
		}))
	}()
	if preAborted {
		return result(goalAuditorResult{disapproved: true, err: "Auditor aborted."})
	}
	if promptErr != nil {
		return fail(promptErr)
	}
	// session.abort() does not throw: the loop returns normally with whatever was captured, so check the signal.
	if args.aborted() {
		return result(goalAuditorResult{disapproved: true, output: joined(), err: "Auditor aborted."})
	}
	output := joined()
	d := parseAuditorDecision(output)
	return result(goalAuditorResult{approved: d.approved, disapproved: d.disapproved, output: output})
}

// watchAbortSignal is addEventListener("abort", abort, {once: true}) on an AbortSignal: abort runs once when the signal
// fires, never for a signal that was already aborted when registered. The returned func is removeEventListener; an abort
// that fired while registered is guaranteed to have run abort by the time it returns.
func watchAbortSignal(signal context.Context, abort func()) (remove func()) {
	if signal == nil || signal.Err() != nil {
		return func() {}
	}
	var once sync.Once
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-signal.Done():
			once.Do(abort)
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-stopped
		if signal.Err() != nil {
			once.Do(abort)
		}
	}
}
