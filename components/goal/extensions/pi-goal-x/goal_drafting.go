package pi_goal_x

// ported from goal-drafting.ts: guided drafting (/goal, /sisyphus, /goal-tweak). A durable branch-local draft session, the drafting
// tool profile, the three draft tools and the confirmation flow.

import (
	"encoding/json"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pitui "github.com/MichaelKinsy/pigpen/components/pitui"
)

const (
	draftEntry        = "pi-goal-draft"
	draftEntryVersion = 1
)

// activeGoalDraft is ActiveGoalDraft; mode is "goal", "sisyphus" or "tweak"; targetGoalId "" is undefined.
type activeGoalDraft struct {
	mode           string
	originalTopic  string
	targetGoalId   string
	startedAt      string
	auditorEnabled bool
	// questionnaireEcho is E5: the formatted questionnaire Q&A to echo in the created-goal report.
	questionnaireEcho string
}

func (c *core) hasActiveDraft() bool { return c.draft != nil }

// draftSession is the GoalDraftSession entry data for a draft (clearedAt "" is undefined).
func draftSession(d *activeGoalDraft, clearedAt string) *jsObject {
	o := newObject()
	o.set("version", float64(draftEntryVersion))
	o.set("mode", d.mode)
	o.set("seed", d.originalTopic)
	if d.targetGoalId != "" {
		o.set("targetGoalId", d.targetGoalId)
	}
	o.set("startedAt", d.startedAt)
	o.set("auditorEnabled", d.auditorEnabled)
	if clearedAt != "" {
		o.set("clearedAt", clearedAt)
	}
	return o
}

func (c *core) draftSessionEntry(session *jsObject) { c.h.AppendEntry(draftEntry, session) }

// clearGoalDrafting drops the active draft, tombstones its durable entry (the last entry wins on rehydration) and restores the
// execution tool profile.
func (c *core) clearGoalDrafting() {
	if c.draft == nil {
		return
	}
	existing := c.draft
	c.draft = nil
	c.draftSessionEntry(draftSession(existing, nowIso()))
	c.installGoalToolProfile(c.tasksEnabled)
}

const staleTweakDraftNotice = "The goal tweak draft is stale (its target goal changed); it was discarded."

// rehydrateDraft rehydrates a durable draft on session_start / session_tree. It restores the transient drafting profile only for a
// valid, un-cleared draft whose tweak target still matches the focused goal; stale tweak drafts are tombstoned.
func (c *core) rehydrateDraft() {
	// Validate any live memory draft against the reloaded world first: a tweak draft whose target is no longer focused is stale and
	// must not survive.
	if live := c.draft; live != nil {
		if live.mode == "tweak" {
			c.reconcileFocusedGoalFromDisk(false)
			if g := c.state(); g == nil || gstr(g, "id") != live.targetGoalId {
				c.clearGoalDrafting()
				c.h.Notify(staleTweakDraftNotice, "warning")
			}
		}
		return
	}
	var session *jsObject
	entries := c.h.Branch()
	for i := len(entries) - 1; i >= 0; i-- {
		entry, _ := entries[i].(*jsObject)
		if entry == nil {
			continue
		}
		if t, _ := entry.str("type"); t != "custom" {
			continue
		}
		if ct, _ := entry.str("customType"); ct != draftEntry {
			continue
		}
		session = entry.obj("data")
		break
	}
	if session == nil || gnum(session, "version") != draftEntryVersion || gstr(session, "clearedAt") != "" {
		// No durable draft: make sure a previous drafting profile is not left installed (e.g. after a stale entry or an interrupted
		// session).
		c.installGoalToolProfile(c.tasksEnabled)
		return
	}
	mode := gstr(session, "mode")
	if mode == "tweak" {
		c.reconcileFocusedGoalFromDisk(false)
		if g := c.state(); g == nil || gstr(g, "id") != gstr(session, "targetGoalId") {
			// The tweak target is gone or no longer focused — the draft is stale.
			tomb := session.clone()
			tomb.set("clearedAt", nowIso())
			c.draftSessionEntry(tomb)
			c.h.Notify(staleTweakDraftNotice, "warning")
			c.installGoalToolProfile(c.tasksEnabled)
			return
		}
	}
	auditor, _ := session.flag("auditorEnabled")
	c.draft = &activeGoalDraft{mode: mode, originalTopic: gstr(session, "seed"), targetGoalId: gstr(session, "targetGoalId"), startedAt: gstr(session, "startedAt"), auditorEnabled: auditor}
	c.installDraftingToolProfile()
}

func (c *core) awaitDraftChoice(label string) string {
	choices := []string{"Resume the existing draft", "Replace it with a new draft", "Cancel"}
	c.enterGoalModal()
	defer c.exitGoalModal()
	var selected string
	var ok bool
	c.awaiting(func() { selected, ok = c.h.Select("A "+strings.ToLower(label)+" is already active", choices) })
	if !ok || selected == "" || selected == choices[0] {
		return "resume"
	}
	if selected == choices[2] {
		return "cancel"
	}
	return "replace"
}

// startGoalDrafting is startGoalDrafting(core, ctx, mode, topic, targetGoal); targetGoal is nil unless mode is "tweak".
func (c *core) startGoalDrafting(mode, topic string, targetGoal *jsObject) {
	trimmed := jsTrim(topic)
	label := "Goal draft"
	switch mode {
	case "sisyphus":
		label = "Sisyphus draft"
	case "tweak":
		label = "Goal tweak draft"
	}
	// A second draft must never silently discard the first.
	if c.draft != nil {
		choice := "replace" // headless: explicit new intent wins, but not silently (notified)
		if c.h.HasUI() {
			choice = c.awaitDraftChoice(label)
		}
		if choice == "resume" {
			c.h.Notify("A draft is already active; resuming it. Use /goal-cancel to discard it.", "info")
			return
		}
		if choice == "cancel" {
			c.h.Notify("Draft start cancelled; the existing draft stays active.", "info")
			return
		}
		c.h.Notify("Replacing the active draft with a new "+strings.ToLower(label)+".", "warning")
		c.clearGoalDrafting()
	}
	startedAt := nowIso()
	// §tweak-persist: a tweak confirmation defaults to the target goal's persisted per-goal auditor setting (skipAuditor); the global
	// `auditor disabled` setting is only the fallback when the goal has no per-goal value. Confirming a tweak must never silently
	// re-enable or disable the auditor the user chose when the goal was created.
	auditorEnabled := !loadGoalSettings(c.h.Cwd()).disabled
	if mode == "tweak" && targetGoal != nil {
		if skip, has := targetGoal.flag("skipAuditor"); has {
			auditorEnabled = !skip
		}
	}
	d := &activeGoalDraft{mode: mode, originalTopic: trimmed, startedAt: startedAt, auditorEnabled: auditorEnabled}
	if targetGoal != nil {
		d.targetGoalId = gstr(targetGoal, "id")
	}
	c.draft = d
	c.draftSessionEntry(draftSession(d, ""))
	c.clearContinuationState(true)
	c.clearActiveAccounting()
	c.installDraftingToolProfile()
	started := label + " started"
	if trimmed != "" {
		started += ": " + utf16Prefix(trimmed, 60)
	}
	c.h.Notify(started+". The agent will clarify, propose a goal and tasks where useful, then ask you to confirm.", "info")
	var prompt string
	if mode == "tweak" {
		objective := "(goal no longer available)"
		var budget *float64
		if targetGoal != nil {
			objective = gstr(targetGoal, "objective")
			budget = goalTokenBudget(targetGoal)
		}
		request := trimmed
		if request == "" {
			request = "(ask what they want to change)"
		}
		prompt = strings.Join([]string{
			"[GOAL TWEAK DRAFT]",
			"The user wants to revise the focused persistent goal. Discuss requirements as needed; do not edit files or start substantive work.",
			"Propose the complete revised objective with propose_goal_draft. Preserve the current goal mode. Include a complete flat task list when the revision changes a decomposable plan; omit tasks to retain the current list.",
			"For budget-only changes preserve the objective, tasks, mode and auditor. token_budget: positive integer sets the total lifetime limit; null removes it; omit to retain it. Change budgets only when requested. Do not add clarification steps when the request is clear.",
			formatGoalBudget(budget),
			"Current objective:", objective,
			"User request:", request,
		}, "\n")
	} else {
		prompt = goalDraftingPrompt(trimmed, goalDraftingFocus(mode))
	}
	deliverAs := "steer"
	if c.h.IsIdle() {
		deliverAs = "followUp"
	}
	if err := c.h.SendUserMessage(prompt, deliverAs); err != nil {
		c.clearGoalDrafting()
		c.h.Notify("Could not start "+strings.ToLower(label)+": "+err.Error(), "error")
	}
}

// goalTokenBudget is goal.tokenBudget (nil when undefined).
func goalTokenBudget(g *jsObject) *float64 {
	if g == nil {
		return nil
	}
	if v, ok := g.num("tokenBudget"); ok {
		return &v
	}
	return nil
}

// draftBudget is the proposal's token_budget: undefined (set=false), null (set, value nil) or a number.
type draftBudget struct {
	set   bool
	value *float64
}

func asTaskObjects(tasks []any) []*jsObject {
	out := make([]*jsObject, 0, len(tasks))
	for _, t := range tasks {
		if o, ok := t.(*jsObject); ok {
			out = append(out, o)
		}
	}
	return out
}

// proposedTaskList is proposedTaskList(core, ctx, tasks, blockCompletion): the proposed list (nil when omitted) or a message.
func (c *core) proposedTaskList(tasks []any, hasTasks bool, blockCompletion bool) (*jsObject, string) {
	if !hasTasks {
		return nil, ""
	}
	if !c.tasksEnabled {
		return nil, "Task lists are disabled by settings; omit tasks from this proposal."
	}
	converted, msg := convertFlatTasks(tasks, int(loadGoalSettings(c.h.Cwd()).subtaskDepth))
	if msg != "" {
		return nil, msg
	}
	list := newObject()
	list.set("tasks", converted)
	list.set("blockCompletion", blockCompletion)
	list.set("proposedAt", nowIso())
	return list, ""
}

// proposalText is proposalText(draft, objective, autoContinue, taskList, current, tokenBudget).
func proposalText(draft *activeGoalDraft, objective string, autoContinue bool, taskList *jsObject, current *jsObject, tokenBudget draftBudget) string {
	var base string
	if draft.mode == "tweak" && current != nil {
		summary := draft.originalTopic
		if summary == "" {
			summary = "Goal revised through guided drafting."
		}
		base = buildTweakConfirmationText(gstr(current, "objective"), objective, summary, gflag(current, "sisyphus"), asTaskObjects(taskListTasks(taskList)))
	} else {
		focus := goalDraftingFocus("goal")
		if draft.mode == "sisyphus" {
			focus = "sisyphus"
		}
		base = buildDraftConfirmationText(focus, draft.originalTopic, objective, autoContinue)
	}
	// The task list is always shown exactly once in a proposal: explicitly proposed tasks, or (new drafts) tasks derived from the RAW
	// objective, or (tweaks) the current goal's list when it is retained unchanged.
	tasksText := ""
	proposed := taskListTasks(taskList)
	if draft.mode == "tweak" {
		// buildTweakConfirmationText already renders explicit tasks exactly once; never append a second render. When no explicit
		// list is proposed the current list is retained on apply, so preview it for confirmation.
		if taskList == nil && current != nil {
			if retained := taskListTasks(current.obj("taskList")); len(retained) > 0 {
				tasksText = "\n\nCurrent task list (retained unchanged):\n" + strings.Join(renderConfirmationTasks(asTaskObjects(retained), 0), "\n")
			}
		}
	} else if len(proposed) > 0 {
		tasksText = "\n\nTasks proposed for confirmation:\n" + strings.Join(renderConfirmationTasks(asTaskObjects(proposed), 0), "\n")
	} else {
		// §single-task-set: the derived preview must derive from the SAME objective text the apply path persists (extracted — the
		// Verification contract line removed), so shown == persisted.
		extracted, _ := extractVerificationContract(objective)
		if derived := deriveTasksFromObjective(extracted); len(derived) > 0 {
			tasksText = "\n\nTasks derived from the objective (confirm or ask the agent to adjust):\n" + strings.Join(renderConfirmationTasks(asTaskObjects(derived), 0), "\n")
		}
	}
	var currentBudget *float64
	if current != nil {
		currentBudget = goalTokenBudget(current)
	}
	var header string
	if draft.mode == "tweak" {
		proposedBudget := currentBudget
		if tokenBudget.set {
			proposedBudget = tokenBudget.value
		}
		header = "Current " + formatGoalBudget(currentBudget) + "\nProposed " + formatGoalBudget(proposedBudget)
	} else {
		header = formatGoalBudget(tokenBudget.value)
	}
	return header + "\n\n" + base + tasksText
}

// ---- tools ----

func draftResult(c *core, text string) sdk.ToolResult {
	return sdk.ToolResult{Content: text, Details: goalDetails(c.state(), "")}
}

func draftSchemaObject(props map[string]any, required ...string) sdk.Schema {
	s := sdk.Schema{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func flatTaskSchema() map[string]any {
	return map[string]any{"type": "array", "description": "Flat parent-linked task tree to confirm with the goal.", "items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                    map[string]any{"type": "string", "description": "Short stable slug, for example task-1."},
			"title":                 map[string]any{"type": "string", "description": "Human-readable task title."},
			"parent_id":             map[string]any{"type": "string", "description": "Optional parent task id in this proposal."},
			"verification_contract": map[string]any{"type": "string", "description": "Evidence required for this task."},
			"lightweight_subtasks":  map[string]any{"type": "boolean", "description": "True only for a task with lightweight children."},
		},
		"required": []string{"id", "title"},
	}}
}

func optInt(v any) *int {
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

func optBool(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

// runDraftQuestionnaire runs the questionnaire as a modal with the dialog yielding the core.
func (c *core) runDraftQuestionnaire(questions []goalQuestionnaireQuestion) (goalQuestionnaireResult, error) {
	c.enterGoalModal()
	defer c.exitGoalModal()
	var result goalQuestionnaireResult
	var err error
	dctx := hostDialogCtx{c.h}
	c.awaiting(func() { result, err = runGoalQuestionnaire(dctx, questions, nil) })
	return result, err
}

func errText(err error) string { return err.Error() }

func (c *core) draftQuestionTool(params map[string]any) (any, error) {
	if c.draft == nil {
		return draftResult(c, "No guided goal draft is active. Ask the user to run /goal or /sisyphus."), nil
	}
	q := goalQuestionnaireQuestion{id: "question", question: toolStr(params, "question"), options: toolStrings(params["options"]), recommended: optInt(params["recommended"]), allowCustom: optBool(params["allow_custom"])}
	result, err := c.runDraftQuestionnaire([]goalQuestionnaireQuestion{q})
	if err != nil {
		return draftResult(c, "Goal drafting dialog failed: "+errText(err)+". Drafting remains active; ask in chat instead of retrying the dialog."), nil
	}
	if result.unavailable {
		return draftResult(c, dialogUnavailableHint+" Ask the question in chat instead."), nil
	}
	if result.cancelled {
		return draftResult(c, "The user cancelled the question. Continue drafting conversationally."), nil
	}
	return draftResult(c, formatQuestionnaireAnswers(result)), nil
}

func toolStr(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

func toolStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func (c *core) draftQuestionnaireTool(params map[string]any) (any, error) {
	if c.draft == nil {
		return draftResult(c, "No guided goal draft is active."), nil
	}
	var questions []goalQuestionnaireQuestion
	raw, _ := params["questions"].([]any)
	for _, item := range raw {
		m, _ := item.(map[string]any)
		q := goalQuestionnaireQuestion{id: toolStr(m, "id"), question: toolStr(m, "question"), context: toolStr(m, "context"), options: toolStrings(m["options"]), recommended: optInt(m["recommended"]), allowCustom: optBool(m["allow_custom"])}
		questions = append(questions, q)
	}
	result, err := c.runDraftQuestionnaire(questions)
	if err != nil {
		return draftResult(c, "Goal drafting dialog failed: "+errText(err)+". Drafting remains active; ask in chat instead of retrying the dialog."), nil
	}
	if !result.cancelled && c.draft != nil {
		c.draft.questionnaireEcho = formatQuestionnaireAnswers(result) // E5
	}
	if result.unavailable {
		return draftResult(c, dialogUnavailableHint+" Ask the questions in chat instead."), nil
	}
	if result.cancelled {
		return draftResult(c, "The user cancelled the questionnaire. Continue drafting conversationally."), nil
	}
	return draftResult(c, formatQuestionnaireAnswers(result)), nil
}

// proposeDraftTool is the propose_goal_draft execute.
func (c *core) proposeDraftTool(params map[string]any) (any, error) {
	draft := c.draft
	if draft == nil {
		return draftResult(c, "No guided goal draft is active. Do not create a goal without the user starting /goal or /sisyphus."), nil
	}
	rawBudget, budgetPresent := params["token_budget"]
	if budgetPresent && rawBudget != nil {
		if _, msg := validateTokenBudgetInput(rawBudget); msg != "" {
			return draftResult(c, msg), nil
		}
	}
	objective := jsTrim(toolStr(params, "objective"))
	if objective == "" {
		return draftResult(c, "The proposed objective must be at least 1 character."), nil
	}
	if max := loadGoalSettings(c.h.Cwd()).objectiveMaxChars; max > 0 && float64(utf16Len(objective)) > max {
		return draftResult(c, "The proposed objective must be at most "+jsNumber(max)+" characters ("+strconv.Itoa(utf16Len(objective))+" given)."), nil
	}
	expectedSisyphus := draft.mode == "sisyphus"
	if sis, _ := params["sisyphus"].(bool); draft.mode != "tweak" && sis != expectedSisyphus {
		return draftResult(c, "Proposal mode does not match the command that began this draft."), nil
	}
	flat, hasTasks := params["tasks"].([]any)
	block, _ := params["block_completion"].(bool)
	taskList, msg := c.proposedTaskList(flat, hasTasks, block)
	if msg != "" {
		return draftResult(c, msg), nil
	}
	flushError, err := c.goalService.flushForAudit(c.st)
	if err != nil {
		flushError = err.Error()
	}
	if flushError != "" || c.goalService.isTurnBuffered() {
		if flushError == "" {
			flushError = "goal changes are still buffered"
		}
		return draftResult(c, "Proposal not saved: "+flushError), nil
	}
	c.reconcileFocusedGoalFromDisk(false)
	var target *jsObject
	if draft.mode == "tweak" {
		target = c.state()
	}
	if draft.mode == "tweak" && (target == nil || gstr(target, "id") != draft.targetGoalId) {
		return draftResult(c, "The goal changed while drafting; review it and start /goal-tweak again."), nil
	}
	if target != nil && gstr(target, "status") == "complete" {
		return sdk.ToolResult{Content: "Completed goals cannot be tweaked.", Details: goalDetails(target, "")}, nil
	}
	var proposalToken goalFocusToken
	var proposalRevision float64
	if target != nil {
		proposalToken = c.focusedOperationToken(gstr(target, "id"))
		proposalRevision = gnum(target, "revision")
	}
	var proposedBudget *float64
	switch {
	case !budgetPresent:
		proposedBudget = goalTokenBudget(target)
	case rawBudget != nil:
		f := rawBudget.(float64)
		proposedBudget = &f
	}
	var budgetSummary string
	if target != nil {
		budgetSummary = "Current " + formatGoalBudget(goalTokenBudget(target)) + "\nProposed " + formatGoalBudget(proposedBudget)
	} else {
		budgetSummary = formatGoalBudget(proposedBudget)
	}
	if draft.mode == "sisyphus" && !sisyphusObjectiveSufficient(objective) {
		return draftResult(c, "A Sisyphus goal needs ordered steps with explicit per-step done criteria. Refine the objective with numbered steps (1) ..., 2) ...) or Step N: blocks before proposing again."), nil
	}
	autoContinue := true
	if v, ok := params["auto_continue"].(bool); ok {
		autoContinue = v
	}
	var decision string
	var auditorEnabled, unavailable bool
	if shouldAutoConfirmProposal(c.h.HasUI(), getenv("PI_GOAL_AUTO_CONFIRM")) {
		decision, auditorEnabled, unavailable = proposalDecisionConfirm, draft.auditorEnabled, false
	} else {
		var dialog proposalDialogResult
		var dialogErr error
		focus := goalDraftingFocus("goal")
		if draft.mode == "sisyphus" {
			focus = "sisyphus"
		}
		text := proposalText(draft, objective, autoContinue, taskList, target, draftBudget{set: budgetPresent, value: proposedBudgetArg(rawBudget)})
		c.enterGoalModal()
		dctx := hostDialogCtx{c.h}
		enabled := draft.auditorEnabled
		c.awaiting(func() { dialog, dialogErr = showProposalDialog(dctx, text, focus, &enabled) })
		c.exitGoalModal()
		if dialogErr != nil {
			return draftResult(c, proposalDialogFailureMessage(dialogErr)+" Do not retry the dialog until the host issue is resolved."), nil
		}
		decision, auditorEnabled, unavailable = dialog.decision, dialog.auditorEnabled, dialog.unavailable
	}
	settings := loadGoalSettings(c.h.Cwd())
	extractedObjective, extractedContract := objective, ""
	if !settings.disableContracts {
		extractedObjective, extractedContract = extractVerificationContract(objective)
	}
	// §14: the durable proposal summary is part of the transcript for EVERY outcome (confirm / continue refining / cancel), so the user
	// can review the proposal after the dialog closes.
	summary := budgetSummary + "\n\n" + buildProposalSummary(proposalSummaryArgs{
		objective: extractedObjective, taskList: taskList, verificationContract: extractedContract, autoContinue: autoContinue, auditorEnabled: auditorEnabled,
	})
	if decision == proposalDecisionCancel {
		c.clearGoalDrafting()
		return draftResult(c, summary+"\n\nDraft cancelled; no goal was created. Run /goal or /sisyphus to start a new draft."), nil
	}
	if unavailable {
		return draftResult(c, summary+"\n\n"+dialogUnavailableHint+" The goal was NOT created and drafting remains active; do not retry the dialog until the host supports it or the user explicitly restarts with PI_GOAL_AUTO_CONFIRM=1."), nil
	}
	if decision != proposalDecisionConfirm {
		// Continue refining: preserve the user's auditor choice for the next proposal (memory and the durable session entry).
		if auditorEnabled != draft.auditorEnabled {
			next := *draft
			next.auditorEnabled = auditorEnabled
			c.draft = &next
			c.draftSessionEntry(draftSession(&next, ""))
		}
		return draftResult(c, summary+"\n\nGoal draft refinement requested. The goal was not changed; ask what the user wants revised before proposing again."), nil
	}
	skipAuditor := !auditorEnabled
	// §14: capture the questionnaire answers BEFORE clearing draft state so the confirmed-goal report can include them reliably (E5).
	qaEcho := draft.questionnaireEcho
	if draft.mode != "tweak" {
		// F2: if the confirmation carried no task plan but the objective has structure, bootstrap the derived tree so the goal starts
		// trackable.
		effective := taskList
		if effective == nil {
			if derived := deriveTasksFromObjective(extractedObjective); len(derived) > 0 {
				effective = newObject()
				effective.set("tasks", derived)
				effective.set("blockCompletion", false)
				effective.set("proposedAt", nowIso())
			}
		}
		budget := -1.0
		if proposedBudget != nil {
			budget = *proposedBudget
		}
		c.replaceGoal(goalCreationConfig{objective: extractedObjective, autoContinue: autoContinue, sisyphus: expectedSisyphus, taskList: effective, skipAuditor: skipAuditor}, true, extractedContract, budget)
		c.clearGoalDrafting()
		created := c.state()
		report := goalCreatedReportArgs{objective: extractedObjective, detailedSummary: qaEcho, confirmed: true, auditorEnabled: &auditorEnabled}
		if created != nil {
			report.goalId = gstr(created, "id")
			report.filePath = gstr(created, "activePath")
			if tl := created.obj("taskList"); tl != nil {
				n := countTasks(taskListTasks(tl))
				report.taskCount = &n
			}
			report.verificationContract = gstr(created, "verificationContract")
			report.tokenBudget = goalTokenBudget(created)
		}
		return sdk.ToolResult{Content: summary + "\n\n" + buildGoalCreatedReport(report), Details: goalDetails(c.state(), ""), Terminate: true}, nil
	}
	if target == nil {
		return draftResult(c, "The goal changed while drafting; review it and start /goal-tweak again."), nil
	}
	return c.applyTweak(applyTweakArgs{
		target: target, token: proposalToken, revision: proposalRevision, taskList: taskList, budget: proposedBudget, budgetPresent: budgetPresent,
		objective: extractedObjective, contract: extractedContract, skipAuditor: skipAuditor, summary: summary,
	})
}

// proposedBudgetArg is the proposal's token_budget as a pointer (nil for undefined and null).
func proposedBudgetArg(raw any) *float64 {
	if f, ok := raw.(float64); ok {
		return &f
	}
	return nil
}

type applyTweakArgs struct {
	target        *jsObject
	token         goalFocusToken
	revision      float64
	taskList      *jsObject
	budget        *float64
	budgetPresent bool
	objective     string
	contract      string
	skipAuditor   bool
	summary       string
}

func optFloatEqual(a, b *float64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func floatOrNull(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func (c *core) applyTweak(a applyTweakArgs) (sdk.ToolResult, error) {
	finalFlushError, ferr := c.goalService.flushForAudit(c.st)
	if ferr != nil {
		finalFlushError = ferr.Error()
	}
	c.reconcileFocusedGoalFromDisk(false)
	cur := c.state()
	if finalFlushError != "" || c.goalService.isTurnBuffered() || !c.isFocusedOperationCurrent(a.token) || cur == nil || gnum(cur, "revision") != a.revision || gstr(cur, "status") == "complete" {
		return draftResult(c, "Goal tweak was not applied: the goal changed during confirmation. Review and propose again."), nil
	}
	now := nowIso()
	// §7.5 merge: a goal tweak must never change the status of steps that persist across the tweak. The proposed list merges into the
	// goal's existing tree by id (same semantics as set_goal_tasks): surviving ids keep status/evidence/completedAt/skippedAt/
	// skipReason, new ids start pending, removed ids drop. currentTaskId survives only while its task is still pending in the merged
	// tree.
	// §tweak-resume: a confirmed tweak is a deliberate revision of a stalled goal — set when the goal actually transitions out of
	// paused/blocked so the post-apply glue can resume accounting, continuation, and the ledger event.
	resumed := false
	token := a.token
	result, err := c.goalService.apply(c.st, goalMutationSpec{
		reconcile:       new(false),
		focusToken:      &token,
		refreshFromDisk: true,
		mutate: func(goal *jsObject) *jsObject {
			var mergedTaskList *jsObject
			if a.taskList != nil {
				if existing := goal.obj("taskList"); existing != nil {
					mergedTaskList = a.taskList.clone()
					mergedTaskList.set("tasks", mergeTasksWithExisting(taskListTasks(existing), taskListTasks(a.taskList)))
				} else {
					mergedTaskList = a.taskList
				}
			}
			var taskList any = undef{}
			if mergedTaskList != nil {
				taskList = mergedTaskList
			} else if existing := goal.obj("taskList"); existing != nil {
				taskList = existing
			}
			var currentTaskId any = pickOpt(goal, "currentTaskId")
			if id, has := goal.str("currentTaskId"); mergedTaskList != nil && has && id != "" && !currentTaskIdIsPending(taskListTasks(mergedTaskList), id) {
				currentTaskId = undef{}
			}
			// §tweak-resume: resume a paused/blocked goal (mirror the /goal-resume transition: status active + pause metadata
			// cleared). A budget-limited goal needs an explicit budget change that clears the resource gate; preserve scheduler
			// limits.
			budgeted := goal.clone()
			if a.budget != nil {
				budgeted.set("tokenBudget", *a.budget)
			} else {
				budgeted.set("tokenBudget", undef{})
			}
			exhausted := budgetReached(budgeted)
			status := gstr(goal, "status")
			wantsResume := status == "paused" || status == "blocked" || (status == "budget_limited" && a.budgetPresent && !exhausted)
			scheduler := goal.obj("scheduler")
			owned := scheduler == nil
			if scheduler != nil {
				sessionID := c.h.SessionID()
				if sessionID == "" {
					sessionID = "unknown-session"
				}
				phase := gstr(scheduler, "phase")
				owned = gstr(scheduler, "owner") == sessionID && phase != "interrupted" && phase != "claimed"
			}
			allowance := true
			if limit := loadGoalSettings(c.h.Cwd()).maxAutonomousRuns; limit != nil && scheduler != nil {
				allowance = gnum(scheduler, "used") < *limit
			} else if limit != nil {
				allowance = 0 < *limit
			}
			stalled := wantsResume && !exhausted && owned && allowance
			if stalled {
				resumed = true
			}
			next := goalWith(goal, "tokenBudget", optNumber(a.budget))
			if exhausted && owned && scheduler != nil {
				sch := scheduler.clone()
				sch.set("generation", newUUID())
				sch.set("phase", "idle")
				sch.set("decision", undef{})
				sch.set("dispatch", undef{})
				sch.set("wait", undef{})
				next.set("scheduler", sch)
			}
			next.set("objective", a.objective)
			if a.contract != "" {
				next.set("verificationContract", a.contract)
			}
			next.set("taskList", taskList)
			next.set("currentTaskId", currentTaskId)
			next.set("skipAuditor", a.skipAuditor)
			switch {
			case exhausted && (status == "active" || status == "budget_limited" || wantsResume):
				next.set("status", "budget_limited")
			case stalled:
				next.set("status", "active")
			case wantsResume:
				next.set("status", "paused")
			}
			if stalled {
				next.set("autoContinue", true)
				next.set("stopReason", undef{})
				next.set("pauseReason", undef{})
				next.set("pauseSuggestedAction", undef{})
			}
			next.set("updatedAt", now)
			return next
		},
		ledgerFn: func(written *jsObject) ([]*jsObject, error) {
			var events []*jsObject
			if !optFloatEqual(goalTokenBudget(a.target), goalTokenBudget(written)) {
				events = append(events, ledgerEvent("goal_budget_changed", "goalId", gstr(written, "id"), "oldBudget", floatOrNull(goalTokenBudget(a.target)), "newBudget", floatOrNull(goalTokenBudget(written)), "tokensUsed", gnum(written.obj("usage"), "tokensUsed"), "at", gstr(written, "updatedAt")))
			}
			if gstr(a.target, "status") != "budget_limited" && gstr(written, "status") == "budget_limited" {
				events = append(events, ledgerEvent("goal_budget_limited", "goalId", gstr(written, "id"), "budget", floatOrNull(goalTokenBudget(written)), "tokensUsed", gnum(written.obj("usage"), "tokensUsed"), "at", gstr(written, "updatedAt")))
			}
			events = append(events, ledgerEvent("goal_tweaked", "goalId", gstr(written, "id"), "changeSummary", "Goal revised through /goal-tweak drafting.", "at", gstr(written, "updatedAt")))
			if a.taskList != nil {
				events = append(events, ledgerEvent("task_list_set", "goalId", gstr(written, "id"), "taskCount", float64(countTasks(taskListTasks(written.obj("taskList")))), "blockCompletion", gflag(a.taskList, "blockCompletion"), "at", gstr(written, "updatedAt")))
			}
			return events, nil
		},
	})
	if err != nil {
		// The authoritative file write throws in the original, so the tool call fails with the thrown error.
		return sdk.ToolResult{}, err
	}
	if !result.ok {
		return draftResult(c, "Goal tweak was not applied: "+result.message), nil
	}
	if resumed {
		// A ledger append failure should not crash the tweak.
		c.goalService.appendEvents(c.st, []*jsObject{ledgerEvent("goal_resumed", "goalId", gstr(result.goal, "id"), "reason", "tweak", "at", nowIso())})
	}
	c.clearContinuationState(true)
	if !budgetReached(result.goal) {
		c.runtime.consumePostBudgetReminder()
	} else {
		c.runtime.armPostBudgetReminder()
		c.clearActiveAccounting()
		c.scheduler.cancelTimer()
	}
	c.updateUI()
	schedulingNote := ""
	if resumed {
		// Resume glue mirrors replaceGoal: restart accounting and queue the auto-continuation so the revived goal keeps going.
		c.beginAccounting()
		scheduling := c.scheduler.declare(goalContinuation{kind: "ready"})
		if !scheduling.terminate {
			schedulingNote = "\n" + scheduling.text
		}
		c.queueContinuation()
	}
	c.clearGoalDrafting()
	tail := ""
	switch gstr(result.goal, "status") {
	case "budget_limited":
		tail = "\nConsumed tokens already meet this budget; the goal remains budget-limited."
	case "paused":
		tail = "\nGoal remains paused. Use /goal-resume to continue when scheduling permits."
	}
	return sdk.ToolResult{Content: a.summary + "\n\nGoal tweak confirmed and applied.\n" + formatGoalBudget(goalTokenBudget(result.goal)) + schedulingNote + tail, Details: goalDetails(result.goal, ""), Terminate: true}, nil
}

func optNumber(f *float64) any {
	if f == nil {
		return undef{}
	}
	return *f
}

// ---- registration ----

// proposeDraftGuidelines are the prompt guidelines of propose_goal_draft.
var proposeDraftGuidelines = []string{
	"Use only during a /goal, /sisyphus, or /goal-tweak guided draft.",
	"Clarify ambiguity before proposing. Include tasks when the work naturally decomposes into trackable milestones; omit them for genuinely simple work.",
	"Set token_budget only when the user explicitly asked for a token budget or a spend limit. Never invent, estimate, or infer one from the objective's size; omitting it creates a goal with no budget.",
	"Confirmation creates or revises the goal atomically. Continue Chatting leaves drafting active for refinement.",
}

// registerDraftingTools is registerDraftingTools(core).
func registerDraftingTools(c *core) {
	e := c.pi.e
	e.RegisterTool(sdk.ToolDefinition{
		Name:          questionToolName,
		Label:         "Ask Drafting Question",
		Description:   "Ask one structured question during a user-started goal draft.",
		PromptSnippet: "Ask the user one focused drafting question.",
		Parameters: draftSchemaObject(map[string]any{
			"question":     map[string]any{"type": "string", "description": "The question to ask."},
			"options":      map[string]any{"type": "array", "items": map[string]any{"type": "string", "description": "A concise answer option."}},
			"recommended":  map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based recommended option."},
			"allow_custom": map[string]any{"type": "boolean", "description": "Allow a custom answer; defaults to true."},
		}, "question"),
		Execute:      c.pi.toolFunc(func(c *core, params map[string]any) (any, error) { return c.draftQuestionTool(params) }),
		RenderCall:   draftRenderCall(c, func(map[string]any, goalTheme) string { return "goal_question" }),
		RenderResult: draftRenderResult(c),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name:          questionnaireToolName,
		Label:         "Run Drafting Questionnaire",
		Description:   "Ask a short structured questionnaire during a user-started goal draft.",
		PromptSnippet: "Ask only the questions needed to make the goal and task plan concrete.",
		Parameters: draftSchemaObject(map[string]any{
			"questions": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "string", "description": "Stable question id."},
					"question":     map[string]any{"type": "string", "description": "Question for the user."},
					"context":      map[string]any{"type": "string", "description": "Optional short context."},
					"options":      map[string]any{"type": "array", "items": map[string]any{"type": "string", "description": "Answer option."}},
					"recommended":  map[string]any{"type": "integer", "minimum": 0},
					"allow_custom": map[string]any{"type": "boolean"},
				},
				"required": []string{"id", "question", "options"},
			}},
		}, "questions"),
		Execute:      c.pi.toolFunc(func(c *core, params map[string]any) (any, error) { return c.draftQuestionnaireTool(params) }),
		RenderCall:   draftRenderCall(c, func(map[string]any, goalTheme) string { return "goal_questionnaire" }),
		RenderResult: draftRenderResult(c),
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name:             proposeDraftToolName,
		Label:            "Propose Goal Draft",
		Description:      "Present the drafted objective and agent-selected task plan for explicit user confirmation.",
		PromptSnippet:    "Confirm the proposed goal and any useful task tree in one dialog.",
		PromptGuidelines: proposeDraftGuidelines,
		Parameters: draftSchemaObject(map[string]any{
			"objective":        map[string]any{"type": "string", "description": "Complete proposed objective; preserve it exactly for budget-only tweaks."},
			"token_budget":     map[string]any{"anyOf": []any{map[string]any{"type": "integer", "minimum": 1}, map[string]any{"type": "null"}}, "description": "Only on user request: positive whole-token lifetime limit; null removes it. Omit to retain the current tweak budget or create without one."},
			"auto_continue":    map[string]any{"type": "boolean", "description": "Defaults to true."},
			"sisyphus":         map[string]any{"type": "boolean", "description": "Must match the user-selected goal mode."},
			"tasks":            flatTaskSchema(),
			"block_completion": map[string]any{"type": "boolean", "description": "Require task completion before goal completion."},
		}, "objective"),
		ExecutionMode: "sequential",
		Execute:       c.pi.toolFunc(func(c *core, params map[string]any) (any, error) { return c.proposeDraftTool(params) }),
		RenderCall:    draftRenderCall(c, func(args map[string]any, theme goalTheme) string { return c.proposeCallText(args, theme) }),
		RenderResult:  draftRenderResult(c),
	})
}

// proposeCallText is the propose_goal_draft renderCall. §proposal-presentation: the tool-call display lands in the terminal buffer the
// moment the tool call starts — BEFORE the confirmation dialog opens — so it is the scrollable "complete presentation" of the goal. It
// shows the full objective contract AND every task line.
func (c *core) proposeCallText(args map[string]any, theme goalTheme) string {
	objective := ""
	if v, ok := args["objective"]; ok && v != nil {
		objective = jsDisplay(v)
	}
	text := theme.fg("toolTitle", "propose_goal_draft ") + theme.fg("muted", objective)
	flatTasks, _ := args["tasks"].([]any)
	// §single-task-set: a proposal shows exactly ONE task set — the set that will actually be persisted.
	var header string
	var lines []string
	switch {
	case len(flatTasks) > 0:
		header = "Tasks proposed for confirmation:"
		for _, t := range flatTasks {
			id, _ := toolItemString(t, "id")
			title, _ := toolItemString(t, "title")
			lines = append(lines, "[ ] "+id+": "+title)
		}
	case c.draft != nil && c.draft.mode == "tweak":
		if retained := taskListTasks(c.stateTaskList()); len(retained) > 0 {
			header = "Current task list (retained unchanged):"
			lines = renderConfirmationTasks(asTaskObjects(retained), 0)
		}
	default:
		extracted, _ := extractVerificationContract(objective)
		if derived := deriveTasksFromObjective(extracted); len(derived) > 0 {
			header = "Tasks derived from the objective (confirm or ask the agent to adjust):"
			lines = renderConfirmationTasks(asTaskObjects(derived), 0)
		}
	}
	if header != "" {
		text += "\n\n" + theme.fg("toolTitle", theme.bold(header)) + "\n" + theme.fg("toolOutput", strings.Join(lines, "\n"))
	}
	if v, ok := args["auto_continue"].(bool); ok && !v {
		text += "\n\n" + theme.fg("muted", "Automatic continuation: disabled")
	}
	if v, ok := args["block_completion"].(bool); ok && v {
		text += "\n\n" + theme.fg("muted", "Block completion on pending tasks: enabled")
	}
	return text
}

// draftRenderCall renders a draft tool's call as the text component renderCall returns.
func draftRenderCall(c *core, text func(args map[string]any, theme goalTheme) string) sdk.ToolRenderCallFunc {
	return func(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return pitui.NewText(text(args, sdkTheme{ctx.UITheme()}), 0, 0, nil).Render(width), nil
	}
}

// draftRenderResult is renderResult(result, opts, theme) → renderGoalResult.
func draftRenderResult(*core) sdk.ToolRenderResultFunc {
	return func(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
		gr := goalToolResult{}
		for _, item := range result.Content {
			typ, _ := item["type"].(string)
			text, hasText := item["text"].(string)
			gr.content = append(gr.content, goalToolContent{typ: typ, text: text, hasText: hasText})
		}
		if result.Details != nil {
			if raw, err := json.Marshal(result.Details); err == nil {
				if v, err := parseJSON(raw); err == nil {
					gr.details, _ = v.(*jsObject)
				}
			}
		}
		return renderGoalResult(gr, options.Expanded, sdkTheme{ctx.UITheme()}).Render(width), nil
	}
}

// stateTaskList is state.goal?.taskList.
func (c *core) stateTaskList() *jsObject {
	if g := c.state(); g != nil {
		return g.obj("taskList")
	}
	return nil
}
