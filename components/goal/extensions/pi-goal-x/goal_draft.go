package pi_goal_x

import (
	"strconv"
	"strings"
)

type goalDraftingFocus string // "goal" | "sisyphus"

type goalConfirmationIntentLike struct {
	focus         goalDraftingFocus
	originalTopic string
	startedAt     *float64
}

type draftProposalInput struct {
	intent            *goalConfirmationIntentLike
	hasUnfinishedGoal bool
	objective         string
	sisyphus          bool
	draftId           string
}

type draftProposalValidation struct {
	ok               bool
	objective        string
	expectedSisyphus bool
	message          string
	clearDrafting    bool
}

type toolGateDecision struct {
	block  bool
	reason string
}

func formatModeLabel(sisyphus bool) string {
	if sisyphus {
		return "Sisyphus (prompt/criteria style)"
	}
	return "Normal goal"
}

func formatPrefixedLines(content string) []string {
	lines := []string{}
	for _, rawLine := range strings.Split(content, "\n") {
		trimmed := jsTrim(rawLine)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "│") {
			lines = append(lines, rawLine)
		} else {
			lines = append(lines, "│   "+rawLine)
		}
	}
	return lines
}

func formatSection(title, content string) []string {
	return append([]string{"", "─── " + title + " ───", ""}, formatPrefixedLines(content)...)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func buildDraftConfirmationText(focus goalDraftingFocus, originalTopic, objective string, autoContinue bool) string {
	lines := []string{"● Goal draft ready for confirmation.", "", "─── Draft Details ───"}
	lines = append(lines, "│   Mode: "+formatModeLabel(focus == "sisyphus"))
	lines = append(lines, "│   Auto-continue: "+yesNo(autoContinue))
	lines = append(lines, formatSection("Original Topic", jsTrim(originalTopic))...)
	lines = append(lines, formatSection("Proposed Goal", objective)...)
	return strings.Join(lines, "\n")
}

func buildTweakConfirmationText(currentObjective, newObjective, changeSummary string, sisyphus bool, tasks []*jsObject) string {
	lines := []string{"● Goal tweak ready for confirmation.", "", "─── Draft Details ───"}
	lines = append(lines, "│   Mode: "+formatModeLabel(sisyphus))
	lines = append(lines, formatSection("Change", changeSummary)...)
	lines = append(lines, formatSection("Current Objective", currentObjective)...)
	lines = append(lines, formatSection("Proposed New Objective", newObjective)...)
	if len(tasks) > 0 {
		lines = append(lines, "", "┌─ TASKS ─────────────────────────────────────┐")
		lines = append(lines, renderConfirmationTasks(tasks, 0)...)
		lines = append(lines, "└──────────────────────────────────────────────┘")
	}
	return strings.Join(lines, "\n")
}

// evaluateDraftingToolGate: confirmation is prompt-guided, never runtime-enforced.
func evaluateDraftingToolGate(toolName string) toolGateDecision {
	return toolGateDecision{block: false}
}

func validateGoalDraftProposal(input draftProposalInput) draftProposalValidation {
	if input.intent == nil {
		return draftProposalValidation{message: "propose_goal_draft REJECTED: no /goal, /sisyphus, or /goal-tweak guided draft is in progress."}
	}
	expected := input.intent.focus == "sisyphus"
	if input.sisyphus != expected {
		cmd := "/goal"
		if expected {
			cmd = "/sisyphus"
		}
		return draftProposalValidation{message: "propose_goal_draft REJECTED (focus gate): the user invoked " + cmd + " but you passed sisyphus=" + boolStr(input.sisyphus) + ". Set sisyphus=" + boolStr(expected) + " to match their choice."}
	}
	objective := jsTrim(input.objective)
	if objective == "" {
		return draftProposalValidation{message: "propose_goal_draft REJECTED: objective is empty."}
	}
	return draftProposalValidation{ok: true, objective: objective, expectedSisyphus: expected}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func goalDraftingPrompt(topic string, focus goalDraftingFocus) string {
	t := jsTrim(topic)
	if t == "" {
		t = "(no topic provided — ask the user what they want to accomplish)"
	}
	safeTopic := promptSafeObjective(t)
	header := "[GOAL CONFIRMATION focus=goal]\nThe user invoked guided goal drafting (/goal). Help turn their request into a confirmed goal contract. Do NOT start substantive work yet."
	if focus == "sisyphus" {
		header = "[GOAL CONFIRMATION focus=sisyphus]\nThe user invoked Sisyphus intent discussion (/sisyphus). Help turn their request into a confirmed goal contract. Do NOT start substantive work yet."
	}
	commonProtocol := []string{
		"Confirmation protocol:",
		"- Treat this as a lightweight conversation with the user, not a separate long-running runtime phase.",
		"- If the topic is vague, ask one focused question with a recommended default. Use goal_question or goal_questionnaire when a structured answer would help, but plain conversation is acceptable.",
		"- Targeted read-only research is allowed when it helps define a better goal contract; do not start implementation before confirmation.",
		"- If the topic is already concrete, you may proceed directly to propose_goal_draft.",
		"- The goal contract should make the objective, success criteria, boundaries, constraints, and blocker rule explicit.",
		"- Keep grilling assumptions until the objective, success criteria, boundaries, constraints, and blocker rule are clear enough to confirm.",
		"- If the objective naturally decomposes into trackable milestones, you MUST include the task list in the `tasks` parameter of `propose_goal_draft` so the user can accept both goal and tasks in a single confirmation dialog. Do NOT propose the goal without tasks and then call `propose_task_list` separately.",
		"- For simple single-step goals, no task list is required. The `tasks` parameter can be omitted.",
		"- After goal creation, `propose_task_list` is still available for user-requested task additions or structural changes.",
		"- propose_goal_draft opens the user's Confirm / Continue Chatting dialog. Confirm creates and focuses the goal; Continue Chatting means keep refining through normal proposal cycles.",
		"- Call propose_goal_draft with the complete structured proposal. The tool call renderer is the scrollable, complete human presentation of the objective and task tree — do NOT duplicate the full proposal in a preceding prose message. A brief orientation sentence is fine.",
		"- create_goal is not a shortcut. Direct create_goal calls are rejected so the user keeps explicit say in goal creation.",
	}
	goalFocusItems := []string{
		"For /goal, propose a normal goal in this shape when ready:",
		"=== Goal ===",
		"Objective: <one-sentence outcome>",
		"Success criteria: <observable evidence the goal is done>",
		"Boundaries: <in scope / out of scope>",
		"Constraints: <hard rules>",
		"Verification contract: <optional — what verification evidence is required before marking complete, e.g. 'Run npm test (0 failures), grep for remaining references, re-read requirements and confirm every item is addressed'>",
		"If blocked: <default = stop and ask the user>",
		"Call propose_goal_draft with sisyphus=false and autoContinue=true unless the user asked otherwise.",
	}
	sisyphusFocusItems := []string{
		"For /sisyphus, remember that Sisyphus is a prompt/criteria style, not a separate step-counter mechanism.",
		"Propose a Sisyphus goal in this shape when ready:",
		"=== Sisyphus Goal ===",
		"Objective: <one-sentence outcome>",
		"Success criteria: <observable evidence the whole ordered goal is done>",
		"Boundaries: <in scope / out of scope>",
		"Constraints: <hard rules, files not to touch, etc.>",
		"Verification contract: <optional — what verification evidence is required before marking complete>",
		"Ordered steps: <preserve the user's requested steps and ordering; do not add preflight or reconnaissance steps they did not ask for>",
		"If blocked / unclear / failing: <default = stop and ask the user>",
		"Sisyphus reminder: Work patiently and sequentially. No rushing, no unrequested preflight steps, no improvising around blockers.",
		"Call propose_goal_draft with sisyphus=true and autoContinue=true unless the user asked otherwise.",
	}
	out := []string{header, "", "Topic the user provided:", "<goal_topic>", safeTopic, "</goal_topic>", ""}
	out = append(out, commonProtocol...)
	out = append(out, "")
	if focus == "sisyphus" {
		out = append(out, sisyphusFocusItems...)
	} else {
		out = append(out, goalFocusItems...)
	}
	return strings.Join(out, "\n")
}

type proposalSummaryArgs struct {
	objective            string
	taskList             *jsObject // GoalTaskList: {tasks: [...]}
	verificationContract string
	autoContinue         bool
	auditorEnabled       bool
}

// buildProposalSummary ported from goal-draft.ts: durable proposal summary (plan §14).
func buildProposalSummary(args proposalSummaryArgs) string {
	lines := []string{"Proposed objective:", jsTrim(args.objective)}
	if args.taskList != nil {
		if tasks := draftTaskObjects(args.taskList, "tasks"); len(tasks) > 0 {
			lines = append(lines, "", "Proposed plan:")
			lines = append(lines, renderProposalPlan(tasks)...)
		}
	}
	if vc := jsTrim(args.verificationContract); vc != "" {
		lines = append(lines, "", "Verification:", vc)
	}
	on := func(b bool) string {
		if b {
			return "enabled"
		}
		return "disabled"
	}
	lines = append(lines, "", "Automatic continuation: "+on(args.autoContinue))
	lines = append(lines, "Independent auditor: "+on(args.auditorEnabled))
	return strings.Join(lines, "\n")
}

func renderProposalPlan(tasks []*jsObject) []string {
	lines := []string{}
	for i, t := range tasks {
		lines = append(lines, strconv.Itoa(i+1)+". "+gstr(t, "title"))
		for _, child := range draftTaskObjects(t, "subtasks") {
			lines = append(lines, "   - "+gstr(child, "title"))
		}
	}
	return lines
}
