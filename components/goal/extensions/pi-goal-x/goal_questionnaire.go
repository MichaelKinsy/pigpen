package pi_goal_x

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

type goalQuestionnaireQuestion struct {
	id          string
	question    string
	context     string // "" = undefined
	options     []string
	recommended *int
	allowCustom *bool
}

type goalQuestionnaireAnswer struct {
	id        string
	question  string
	answer    string
	wasCustom bool
}

type goalQuestionnaireResult struct {
	questions      []goalQuestionnaireQuestion
	answers        []goalQuestionnaireAnswer
	cancelled      bool
	auditorEnabled *bool
	// unavailable: the host could not present the dialog at all, so cancelled reflects an absent UI rather than a user decision.
	unavailable bool
}

type goalAuditorToggleInit struct{ defaultEnabled bool }

const (
	proposalDecisionConfirm  = "confirm"
	proposalDecisionContinue = "continue"
	proposalDecisionCancel   = "cancel"
)

// computeDialogLineLimit bounds a custom dialog using the frame cache when available (rows/baseFrameLines 0 = absent).
func computeDialogLineLimit(terminalRows, baseFrameLines int) (int, bool) {
	rows := terminalRows
	if rows <= 0 {
		return 0, false
	}
	if baseFrameLines > 0 {
		return min(rows, max(10, rows-baseFrameLines+1)), true
	}
	return min(rows, max(4, rows-4)), true
}

// proposalPresentationSegments: absolute line indices of the tasks section and the start of the options tail.
type proposalPresentationSegments struct{ tasksStart, tasksEnd, tailStart int }

var (
	ansiSGRRe       = regexp.MustCompile("\x1b\\[[0-9;]*m")
	taskLineRe      = regexp.MustCompile(`^\s*\[[ x~]\]`)
	boxBorderRe     = regexp.MustCompile(`^[└┐┤┘─]`)
	announceRe      = regexp.MustCompile(`^● Goal (draft|tweak) ready for confirmation\.$`)
	sectionMarkerRe = regexp.MustCompile(`^───\s+(.+?)\s+───$`)
	checkboxRe      = regexp.MustCompile(`^(\[.\])(\s+)(.+)$`)
	goalSectionRe   = regexp.MustCompile(`^(=== (Goal|Sisyphus Goal) ===|Objective:|Success criteria:|Boundaries:|Constraints:|Verification contract:|If blocked:)`)
	borderStartRe   = regexp.MustCompile(`^[┌├└┐┤┘─]`)
	vagueTopicRe    = regexp.MustCompile(`(整理笔记|organize notes|notes|笔记)$`)
)

func stripAnsiCodes(value string) string { return ansiSGRRe.ReplaceAllString(value, "") }

// findProposalPresentationSegments ported from goal-questionnaire.ts: locate the tasks segment and options tail (ANSI-aware).
func findProposalPresentationSegments(lines []string, tailStart int) *proposalPresentationSegments {
	if tailStart < 0 {
		return nil
	}
	tasksStart := -1
	for i, l := range lines {
		if strings.Contains(l, "Tasks proposed for confirmation:") || strings.Contains(l, "┌─ TASKS ─") {
			tasksStart = i
			break
		}
	}
	if tasksStart < 0 || tasksStart >= tailStart {
		return nil
	}
	tasksEnd := tasksStart
	for i := tasksStart + 1; i < tailStart; i++ {
		plain := stripAnsiCodes(lines[i])
		if taskLineRe.MatchString(plain) {
			tasksEnd = i
			continue
		}
		if tasksEnd > tasksStart && boxBorderRe.MatchString(jsTrim(plain)) {
			tasksEnd = i
		}
		break
	}
	effectiveTail := tailStart
	for i := tailStart - 1; i > tasksEnd; i-- {
		if strings.Contains(lines[i], "press 'a' to toggle") {
			effectiveTail = i
			break
		}
	}
	return &proposalPresentationSegments{tasksStart, tasksEnd, effectiveTail}
}

// dialogScrollState is the §options-scroll viewport state.
type dialogScrollState struct {
	scrollTop    int
	needsFollow  bool
	optionRanges [][2]int
	followIndex  int
}

// jsSlice is Array.prototype.slice for non-negative start/end (end<0 = to the end); the result never aliases lines.
func jsSlice(lines []string, start, end int) []string {
	if end < 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		start = end
	}
	return append([]string{}, lines[start:end]...)
}

// fitDialogLines ported from goal-questionnaire.ts: never returns more than maxDialogLines.
func fitDialogLines(lines []string, maxDialogLines, protectedHead int, proposal *proposalPresentationSegments, scroll *dialogScrollState, dimStyle func(string) string) []string {
	if dimStyle == nil {
		dimStyle = func(s string) string { return s }
	}
	if maxDialogLines <= 0 || len(lines) <= maxDialogLines {
		return lines
	}
	keepHead := min(protectedHead, maxDialogLines)
	if proposal != nil {
		return fitProposalPresentation(lines, maxDialogLines, keepHead, *proposal)
	}
	if scroll != nil {
		return fitDialogViewport(lines, maxDialogLines, scroll, dimStyle)
	}
	budget := maxDialogLines - keepHead
	if budget <= 0 {
		return jsSlice(lines, 0, keepHead)
	}
	rest := jsSlice(lines, protectedHead, -1)
	out := jsSlice(lines, 0, keepHead)
	if len(rest) <= budget {
		return append(out, rest...)
	}
	return append(out, rest[len(rest)-budget:]...)
}

func fitDialogViewport(lines []string, maxDialogLines int, scroll *dialogScrollState, dimStyle func(string) string) []string {
	contentWindow := max(0, maxDialogLines-1)
	contentEnd := max(0, len(lines)-1)
	if scroll.needsFollow && scroll.followIndex >= 0 && scroll.followIndex < len(scroll.optionRanges) {
		os, oe := scroll.optionRanges[scroll.followIndex][0], scroll.optionRanges[scroll.followIndex][1]
		if os < scroll.scrollTop || oe >= scroll.scrollTop+contentWindow {
			if oe >= scroll.scrollTop+contentWindow {
				scroll.scrollTop = min(os, max(0, oe-contentWindow+1))
			} else {
				scroll.scrollTop = os
			}
		}
		scroll.needsFollow = false
	}
	scroll.scrollTop = max(0, min(scroll.scrollTop, max(0, contentEnd-contentWindow)))
	s := scroll.scrollTop
	hiddenAbove := s
	hiddenBelow := contentEnd - (s + contentWindow)
	viewport := jsSlice(lines, s, s+contentWindow)
	if hiddenAbove > 0 && len(viewport) > 0 {
		viewport[0] = dimStyle("▲ " + strconv.Itoa(hiddenAbove) + " more")
	}
	if hiddenBelow > 0 {
		viewport = append(viewport, dimStyle("… +"+strconv.Itoa(hiddenBelow)+" more · PgUp/PgDn scroll"))
	} else {
		viewport = append(viewport, lines[len(lines)-1])
	}
	return viewport
}

func nonBlank(lines []string) []string {
	out := []string{}
	for _, l := range lines {
		if jsTrim(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func fitProposalPresentation(lines []string, maxDialogLines, keepHead int, proposal proposalPresentationSegments) []string {
	tasksStart, tasksEnd, tailStart := proposal.tasksStart, proposal.tasksEnd, proposal.tailStart
	head := jsSlice(lines, 0, keepHead)
	tasks := jsSlice(lines, max(tasksStart, keepHead), min(tasksEnd, tailStart)+1)
	tail := jsSlice(lines, max(tailStart, keepHead), -1)
	candidate := append(append(append([]string{}, head...), tasks...), tail...)
	if len(candidate) <= maxDialogLines {
		return candidate
	}
	stripped := []string{}
	for i, l := range candidate {
		if i < keepHead || jsTrim(l) != "" {
			stripped = append(stripped, l)
		}
	}
	if len(stripped) <= maxDialogLines {
		return stripped
	}
	tailNoBlanks := nonBlank(tail)
	hasAuditor := len(tail) > 0 && strings.Contains(tail[0], "press 'a' to toggle")
	restTail := tailNoBlanks
	aud := 0
	if hasAuditor {
		restTail = tailNoBlanks[min(1, len(tailNoBlanks)):]
		aud = 1
	}
	keepRest := min(len(restTail), max(0, maxDialogLines-keepHead-aud))
	tailKept := []string{}
	if hasAuditor {
		tailKept = append(tailKept, tail[0])
	}
	tailKept = append(tailKept, restTail[len(restTail)-keepRest:]...)
	keepTasks := min(len(tasks), max(0, maxDialogLines-keepHead-len(tailKept)))
	out := append([]string{}, head...)
	out = append(out, tasks[:keepTasks]...)
	return append(out, tailKept...)
}

// normalizeQuestionnaireQuestions ported from goal-questionnaire.ts.
func normalizeQuestionnaireQuestions(raw []goalQuestionnaireQuestion) []goalQuestionnaireQuestion {
	seen := map[string]bool{}
	out := make([]goalQuestionnaireQuestion, 0, len(raw))
	for i, q := range raw {
		id := jsTrim(q.id)
		if id == "" {
			id = "q" + strconv.Itoa(i+1)
		}
		if seen[id] {
			id = id + "-" + strconv.Itoa(i+1)
		}
		seen[id] = true
		options := []string{}
		for _, o := range q.options {
			if utf16Len(jsTrim(o)) > 0 {
				options = append(options, o)
			}
		}
		var recommended *int
		if q.recommended != nil && *q.recommended >= 0 && *q.recommended < len(options) {
			r := *q.recommended
			recommended = &r
		}
		allow := true
		if q.allowCustom != nil {
			allow = *q.allowCustom
		}
		q.id, q.options, q.recommended, q.allowCustom = id, options, recommended, &allow
		out = append(out, q)
	}
	return out
}

func questionAllowsCustom(q goalQuestionnaireQuestion) bool {
	return q.allowCustom == nil || *q.allowCustom
}

func formatQuestionnaireAnswers(result goalQuestionnaireResult) string {
	parts := make([]string, 0, len(result.answers))
	for _, answer := range result.answers {
		var question *goalQuestionnaireQuestion
		for i := range result.questions {
			if result.questions[i].id == answer.id {
				question = &result.questions[i]
				break
			}
		}
		lines := "**Q:** " + answer.question
		if question != nil && question.context != "" {
			lines += "\n" + question.context
		}
		if question != nil && len(question.options) > 0 {
			lines += "\nOptions: " + strings.Join(question.options, " / ")
		}
		lines += "\n**A:** " + answer.answer
		parts = append(parts, lines)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func shouldAutoConfirmProposal(hasUI bool, autoConfirmEnv string) bool {
	if autoConfirmEnv == "0" {
		return false
	}
	return !hasUI || autoConfirmEnv == "1"
}

func proposalDecisionFromQuestionnaireResult(cancelled bool, answer string) string {
	if cancelled {
		return proposalDecisionContinue
	}
	if strings.HasPrefix(answer, "Confirm") {
		return proposalDecisionConfirm
	}
	if strings.HasPrefix(answer, "Cancel") {
		return proposalDecisionCancel
	}
	return proposalDecisionContinue
}

func isHeadlessQuestionSufficientForDraft(topic, questionText string) bool {
	t := strings.ToLower(topic)
	vague := utf16Len(jsTrim(t)) < 20 || vagueTopicRe.MatchString(jsTrim(t))
	return !vague
}

const customAnswerLabel = "Write your own answer..."

func questionnaireBool(b bool) *bool { return &b }

// runQuestionnaireWithBasicDialogs: sequential native dialogs for hosts that cannot render terminal components.
func runQuestionnaireWithBasicDialogs(ctx goalDialogContext, questions []goalQuestionnaireQuestion, toggle *goalAuditorToggleInit) (goalQuestionnaireResult, error) {
	ui := ctx.ui()
	unavailable := func() goalQuestionnaireResult {
		r := goalQuestionnaireResult{questions: []goalQuestionnaireQuestion{}, answers: []goalQuestionnaireAnswer{}, cancelled: true, unavailable: true}
		if toggle != nil {
			r.auditorEnabled = questionnaireBool(toggle.defaultEnabled)
		}
		return r
	}
	anyOptions, anyFree := false, false
	for _, q := range questions {
		if len(q.options) > 0 {
			anyOptions = true
		}
		if len(q.options) == 0 || questionAllowsCustom(q) {
			anyFree = true
		}
	}
	if (toggle != nil || anyOptions) && !ui.hasSelect() {
		return unavailable(), nil
	}
	if anyFree && !ui.hasInput() {
		return unavailable(), nil
	}
	var auditorEnabled *bool
	if toggle != nil {
		auditorEnabled = questionnaireBool(toggle.defaultEnabled)
	}
	cancelled := func() goalQuestionnaireResult {
		var ae *bool
		if auditorEnabled != nil {
			ae = questionnaireBool(*auditorEnabled)
		}
		return goalQuestionnaireResult{questions: questions, answers: []goalQuestionnaireAnswer{}, cancelled: true, auditorEnabled: ae}
	}
	if toggle != nil {
		enabled := "Enabled — require independent approval"
		disabled := "Disabled — skip the completion audit"
		choices := []string{disabled, enabled}
		cur := "disabled"
		if *auditorEnabled {
			choices = []string{enabled, disabled}
			cur = "enabled"
		}
		picked, ok, err := ui.selectOption("Completion auditor (currently "+cur+")", choices)
		if err != nil {
			return goalQuestionnaireResult{}, err
		}
		if !ok {
			return cancelled(), nil
		}
		if picked != enabled && picked != disabled {
			return goalQuestionnaireResult{}, errors.New("The host returned an unknown auditor choice")
		}
		auditorEnabled = questionnaireBool(picked == enabled)
	}
	answers := []goalQuestionnaireAnswer{}
	for _, question := range questions {
		prompt := question.question
		if question.context != "" {
			prompt = question.question + "\n\n" + question.context
		}
		if toggle != nil {
			if *auditorEnabled {
				prompt += "\n\nAuditor for this goal: enabled (independent approval required)."
			} else {
				prompt += "\n\nAuditor for this goal: disabled (completion skips the audit)."
			}
		}
		var answer *string
		wasCustom := len(question.options) == 0
		if !wasCustom {
			labels := make([]string, len(question.options))
			for i, option := range question.options {
				labels[i] = strconv.Itoa(i+1) + ". " + option
				if question.recommended != nil && *question.recommended == i {
					labels[i] += " (Recommended)"
				}
			}
			customLabel := strconv.Itoa(len(labels)+1) + ". " + customAnswerLabel
			options := labels
			if questionAllowsCustom(question) {
				options = append(append([]string{}, labels...), customLabel)
			}
			picked, ok, err := ui.selectOption(prompt, options)
			if err != nil {
				return goalQuestionnaireResult{}, err
			}
			if !ok {
				return cancelled(), nil
			}
			found := false
			for _, o := range options {
				if o == picked {
					found = true
				}
			}
			if !found {
				return goalQuestionnaireResult{}, errors.New("The host returned an unknown questionnaire choice")
			}
			wasCustom = picked == customLabel && questionAllowsCustom(question)
			if !wasCustom {
				for i, l := range labels {
					if l == picked {
						a := question.options[i]
						answer = &a
						break
					}
				}
			}
		}
		if wasCustom {
			for {
				v, ok, err := ui.input(prompt, "Write your answer")
				if err != nil {
					return goalQuestionnaireResult{}, err
				}
				answer = nil
				if ok {
					t := jsTrim(v)
					answer = &t
				}
				if answer == nil || *answer != "" {
					break
				}
			}
		}
		if answer == nil {
			return cancelled(), nil
		}
		answers = append(answers, goalQuestionnaireAnswer{id: question.id, question: question.question, answer: *answer, wasCustom: wasCustom})
	}
	var ae *bool
	if auditorEnabled != nil {
		ae = questionnaireBool(*auditorEnabled)
	}
	return goalQuestionnaireResult{questions: questions, answers: answers, auditorEnabled: ae}, nil
}

const dialogUnavailableHint = "This host cannot display the required goal-drafting dialogs. Use a host with select/input support or the pi TUI, or explicitly restart with PI_GOAL_AUTO_CONFIRM=1 to confirm proposals without a dialog."

func proposalDialogFailureMessage(err error) string {
	return "Goal draft confirmation failed: " + err.Error() + ". The goal was NOT created; drafting remains active."
}

// runGoalQuestionnaire ported from goal-questionnaire.ts: shared question UI for goal_questionnaire and the draft-confirm prompt.
func runGoalQuestionnaire(ctx goalDialogContext, rawQuestions []goalQuestionnaireQuestion, toggle *goalAuditorToggleInit) (goalQuestionnaireResult, error) {
	if !ctx.hasUI() {
		return goalQuestionnaireResult{questions: []goalQuestionnaireQuestion{}, answers: []goalQuestionnaireAnswer{}, cancelled: true}, nil
	}
	questions := normalizeQuestionnaireQuestions(rawQuestions)
	ui := ctx.ui()
	if !ui.hasCustom() {
		return runQuestionnaireWithBasicDialogs(ctx, questions, toggle)
	}
	showRich := func() (*goalQuestionnaireResult, error) {
		factory := func(t goalDialogTUI, theme dialogTheme, done func(any)) goalDialogComponent {
			return newQuestionnaireDialog(ctx, questions, toggle, t, theme, done)
		}
		res, err := ui.custom(factory, goalCustomOptions{})
		if err != nil {
			return nil, err
		}
		r, _ := res.(*goalQuestionnaireResult)
		return r, nil
	}
	result, err := showRich()
	if err != nil {
		if ctx.mode() != "rpc" {
			return goalQuestionnaireResult{}, err
		}
		ui.notify("Goal dialog could not be rendered by this host ("+err.Error()+"); falling back to one dialog per question.", "warning")
		result = nil
	}
	if result != nil {
		return *result, nil
	}
	return runQuestionnaireWithBasicDialogs(ctx, questions, toggle)
}

type proposalDialogResult struct {
	decision       string
	auditorEnabled bool
	unavailable    bool
}

// showProposalDialog confirms a proposed draft through the shared questionnaire UI; escape maps to "continue".
func showProposalDialog(ctx goalDialogContext, confirmationText string, focus goalDraftingFocus, defaultAuditorEnabled *bool) (proposalDialogResult, error) {
	headerTitle := "Confirm Goal Draft"
	if focus == "sisyphus" {
		headerTitle = "Confirm Sisyphus Goal Draft"
	}
	zero := 0
	no := false
	var toggle *goalAuditorToggleInit
	if defaultAuditorEnabled != nil {
		toggle = &goalAuditorToggleInit{*defaultAuditorEnabled}
	}
	result, err := runGoalQuestionnaire(ctx, []goalQuestionnaireQuestion{{
		id: "confirm", question: headerTitle, context: confirmationText,
		options:     []string{"Confirm — create this goal now", "Continue chatting — keep refining", "Cancel — discard this draft"},
		recommended: &zero, allowCustom: &no,
	}}, toggle)
	if err != nil {
		return proposalDialogResult{}, err
	}
	answer := ""
	if len(result.answers) > 0 {
		answer = result.answers[0].answer
	}
	auditor := true
	if result.auditorEnabled != nil {
		auditor = *result.auditorEnabled
	}
	return proposalDialogResult{proposalDecisionFromQuestionnaireResult(result.cancelled, answer), auditor, result.unavailable}, nil
}

// ── the rich dialog ─────────────────────────────────────────────────────────

type displayOption struct {
	label    string
	isCustom bool
}

type questionnaireDialog struct {
	ctx       goalDialogContext
	t         goalDialogTUI
	theme     dialogTheme
	done      func(any)
	questions []goalQuestionnaireQuestion
	toggle    *goalAuditorToggleInit
	isMulti   bool
	totalTabs int

	wasHardwareCursorShown bool
	maxDialogLines         int
	hasMaxDialogLines      bool
	currentTab             int
	optionIndex            int
	inputMode              bool
	inputQuestionID        string
	dialogFocused          bool
	cachedLines            []string
	hasCached              bool
	optionsStartIndex      int
	scrollTop              int
	needsFollow            bool
	optionRanges           [][2]int
	questionBlockEnd       int
	auditorEnabled         bool
	answers                map[string]goalQuestionnaireAnswer
	drafts                 map[string]string
	editor                 *tui.Editor
}

func newQuestionnaireDialog(ctx goalDialogContext, questions []goalQuestionnaireQuestion, toggle *goalAuditorToggleInit, t goalDialogTUI, theme dialogTheme, done func(any)) goalDialogComponent {
	// Some web hosts invoke the factory with a render callback in place of a TUI.
	if t == nil {
		done(nil)
		return emptyDialogComponent{}
	}
	d := &questionnaireDialog{
		ctx: ctx, t: t, theme: theme, done: done, questions: questions, toggle: toggle,
		isMulti: len(questions) > 1, totalTabs: len(questions) + 1,
		optionsStartIndex: -1, questionBlockEnd: -1, auditorEnabled: true,
		answers: map[string]goalQuestionnaireAnswer{}, drafts: map[string]string{},
	}
	if toggle != nil {
		d.auditorEnabled = toggle.defaultEnabled
	}
	d.wasHardwareCursorShown = t.getShowHardwareCursor()
	t.setShowHardwareCursor(false)
	ctx.ui().setWorkingVisible(false)
	rows, _ := t.terminalRows()
	base, _ := t.previousLinesLen()
	d.maxDialogLines, d.hasMaxDialogLines = computeDialogLineLimit(rows, base)
	d.editor = tui.NewEditor(tui.EditorTheme{
		BorderColor: func(s string) string { return theme.fg("accent", s) },
		SelectList: tui.SelectListTheme{
			SelectedPrefix: func(s string) string { return theme.fg("accent", s) },
			SelectedText:   func(s string) string { return theme.fg("accent", s) },
			Description:    func(s string) string { return theme.fg("muted", s) },
			ScrollInfo:     func(s string) string { return theme.fg("dim", s) },
			NoMatch:        func(s string) string { return theme.fg("warning", s) },
		},
	})
	d.editor.SetRowsProvider(func() int { r, _ := t.terminalRows(); return r })
	d.editor.SetRequestRender(t.requestRender)
	d.editor.OnSubmit = d.onEditorSubmit
	d.enterQuestion(d.questions[0])
	return d
}

func (d *questionnaireDialog) dispose() {}

func (d *questionnaireDialog) Invalidate() { d.hasCached = false; d.cachedLines = nil }

func (d *questionnaireDialog) SetFocused(v bool) {
	d.dialogFocused = v
	d.editor.SetFocused(v && d.inputMode)
	d.t.setShowHardwareCursor(v && d.inputMode)
}

func (d *questionnaireDialog) refresh() {
	d.hasCached = false
	d.cachedLines = nil
	d.t.requestRender()
}

func (d *questionnaireDialog) submit(cancelled bool) {
	d.t.setShowHardwareCursor(d.wasHardwareCursorShown)
	d.ctx.ui().setWorkingVisible(true)
	ordered := []goalQuestionnaireAnswer{}
	for _, q := range d.questions {
		if a, ok := d.answers[q.id]; ok {
			ordered = append(ordered, a)
		}
	}
	var ae *bool
	if d.toggle != nil {
		ae = questionnaireBool(d.auditorEnabled)
	}
	d.done(&goalQuestionnaireResult{questions: d.questions, answers: ordered, cancelled: cancelled, auditorEnabled: ae})
}

func (d *questionnaireDialog) currentQuestion() *goalQuestionnaireQuestion {
	if d.currentTab >= 0 && d.currentTab < len(d.questions) {
		return &d.questions[d.currentTab]
	}
	return nil
}

func (d *questionnaireDialog) displayOptions() []displayOption {
	q := d.currentQuestion()
	if q == nil {
		return nil
	}
	opts := make([]displayOption, 0, len(q.options)+1)
	for _, l := range q.options {
		opts = append(opts, displayOption{label: l})
	}
	if questionAllowsCustom(*q) {
		opts = append(opts, displayOption{label: "Write your own answer...", isCustom: true})
	}
	return opts
}

func (d *questionnaireDialog) allAnswered() bool {
	for _, q := range d.questions {
		if _, ok := d.answers[q.id]; !ok {
			return false
		}
	}
	return true
}

func (d *questionnaireDialog) enterQuestion(q goalQuestionnaireQuestion) {
	existing, hasExisting := d.answers[q.id]
	draft, hasDraft := d.drafts[q.id]
	d.scrollTop = 0
	d.needsFollow = false
	switch {
	case len(q.options) == 0:
		d.inputMode = true
		d.inputQuestionID = q.id
		d.editor.SetText(d.editorSeed(draft, hasDraft, existing, hasExisting))
		d.editor.SetFocused(d.dialogFocused)
		d.t.setShowHardwareCursor(d.dialogFocused)
	case hasExisting && existing.wasCustom:
		d.optionIndex = len(q.options)
	case hasExisting:
		d.optionIndex = 0
		for i, o := range q.options {
			if o == existing.answer {
				d.optionIndex = i
				break
			}
		}
	default:
		d.optionIndex = 0
		if q.recommended != nil {
			d.optionIndex = *q.recommended
		}
	}
}

func (d *questionnaireDialog) editorSeed(draft string, hasDraft bool, existing goalQuestionnaireAnswer, hasExisting bool) string {
	if hasDraft {
		return draft
	}
	if hasExisting && existing.wasCustom {
		return existing.answer
	}
	return ""
}

func (d *questionnaireDialog) enterSubmitTab() {
	d.optionIndex = 0
	d.scrollTop = 0
	d.needsFollow = false
}

func (d *questionnaireDialog) enterCurrentTab() {
	if q := d.currentQuestion(); q != nil {
		d.enterQuestion(*q)
	} else {
		d.enterSubmitTab()
	}
}

func (d *questionnaireDialog) advanceAfterAnswer() {
	if !d.isMulti {
		d.submit(false)
		return
	}
	if d.currentTab < len(d.questions)-1 {
		d.currentTab++
	} else {
		d.currentTab = len(d.questions)
	}
	d.enterCurrentTab()
	d.refresh()
}

func (d *questionnaireDialog) saveAnswer(qID, value string, wasCustom bool) {
	question := qID
	for _, q := range d.questions {
		if q.id == qID {
			question = q.question
			break
		}
	}
	d.answers[qID] = goalQuestionnaireAnswer{id: qID, question: question, answer: value, wasCustom: wasCustom}
}

// fitOptionsInFrame (§options-first fit): every option line stays in frame; nil when even the option block cannot fit.
func fitOptionsInFrame(lines []string, maxDialogLines, questionBlockEnd, optionsStart, lastOptionLine int) []string {
	if maxDialogLines <= 0 || len(lines) <= maxDialogLines {
		return lines
	}
	if optionsStart < 0 || lastOptionLine < optionsStart {
		return nil
	}
	head := jsSlice(lines, 0, max(questionBlockEnd, 1))
	middle := jsSlice(lines, max(questionBlockEnd, 1), optionsStart)
	tail := jsSlice(lines, optionsStart, -1)
	optionsBlockLen := lastOptionLine - optionsStart + 1
	if optionsBlockLen+1 > maxDialogLines {
		return nil
	}
	mid := middle
	for len(head)+len(mid)+len(tail) > maxDialogLines && len(mid) > 0 {
		blankIdx := -1
		for i := len(mid) - 1; i >= 0; i-- {
			if jsTrim(mid[i]) == "" {
				blankIdx = i
				break
			}
		}
		if blankIdx >= 0 {
			mid = append(mid[:blankIdx], mid[blankIdx+1:]...)
		} else {
			mid = mid[:len(mid)-1]
		}
	}
	tailKept := tail
	for len(head)+len(mid)+len(tailKept) > maxDialogLines && len(tailKept) > optionsBlockLen+1 {
		tailBlank := -1
		for i := len(tailKept) - 2; i >= 1; i-- {
			if jsTrim(tailKept[i]) == "" {
				tailBlank = i
				break
			}
		}
		rm := tailBlank
		if tailBlank < 1 {
			rm = len(tailKept) - 2
		}
		tailKept = append(tailKept[:rm], tailKept[rm+1:]...)
	}
	headKept := head
	for len(head)+len(mid)+len(tailKept) > maxDialogLines && len(headKept) > 1 {
		headKept = headKept[:len(headKept)-1]
	}
	if len(headKept)+len(mid)+len(tailKept) > maxDialogLines {
		return nil
	}
	out := append([]string{}, headKept...)
	out = append(out, mid...)
	return append(out, tailKept...)
}

func (d *questionnaireDialog) enterInputMode(qID string) {
	d.inputMode = true
	d.inputQuestionID = qID
	draft, hasDraft := d.drafts[qID]
	existing, hasExisting := d.answers[qID]
	d.editor.SetText(d.editorSeed(draft, hasDraft, existing, hasExisting))
	d.editor.SetFocused(d.dialogFocused)
	d.t.setShowHardwareCursor(d.dialogFocused)
}

func (d *questionnaireDialog) leaveInputMode() {
	d.inputMode = false
	d.inputQuestionID = ""
	d.editor.SetFocused(false)
	d.t.setShowHardwareCursor(false)
	d.editor.SetText("")
}

func (d *questionnaireDialog) onEditorSubmit(value string) {
	if d.inputQuestionID == "" {
		return
	}
	trimmed := jsTrim(value)
	if trimmed == "" {
		d.refresh()
		return
	}
	delete(d.drafts, d.inputQuestionID)
	d.saveAnswer(d.inputQuestionID, trimmed, true)
	d.leaveInputMode()
	d.advanceAfterAnswer()
}

func (d *questionnaireDialog) exitEditor() {
	if d.inputQuestionID != "" {
		text := d.editor.GetText()
		if jsTrim(text) != "" {
			d.drafts[d.inputQuestionID] = text
		} else {
			delete(d.drafts, d.inputQuestionID)
		}
	}
	d.leaveInputMode()
}

func (d *questionnaireDialog) pageStep() int {
	n := 10
	if d.hasMaxDialogLines {
		n = d.maxDialogLines
	}
	return max(1, n-1)
}

func (d *questionnaireDialog) HandleInput(data string) {
	if d.inputMode {
		if tui.MatchesKey(data, tui.KeyEscape) {
			q := d.currentQuestion()
			if q != nil && len(q.options) == 0 && !d.isMulti {
				d.submit(true)
			} else {
				d.exitEditor()
				d.refresh()
			}
			return
		}
		if d.isMulti && (tui.MatchesKey(data, tui.KeyTab) || tui.MatchesKey(data, tui.KeyShift(tui.KeyTab))) {
			d.exitEditor()
			if tui.MatchesKey(data, tui.KeyTab) {
				d.currentTab = (d.currentTab + 1) % d.totalTabs
			} else {
				d.currentTab = (d.currentTab - 1 + d.totalTabs) % d.totalTabs
			}
			d.enterCurrentTab()
			d.refresh()
			return
		}
		d.editor.HandleInput(data)
		d.refresh()
		return
	}

	q := d.currentQuestion()
	opts := d.displayOptions()

	if d.isMulti {
		if tui.MatchesKey(data, tui.KeyTab) || tui.MatchesKey(data, tui.KeyRight) {
			d.currentTab = (d.currentTab + 1) % d.totalTabs
			d.enterCurrentTab()
			d.refresh()
			return
		}
		if tui.MatchesKey(data, tui.KeyShift(tui.KeyTab)) || tui.MatchesKey(data, tui.KeyLeft) {
			d.currentTab = (d.currentTab - 1 + d.totalTabs) % d.totalTabs
			d.enterCurrentTab()
			d.refresh()
			return
		}
	}

	switch {
	case tui.MatchesKey(data, tui.KeyPageUp):
		d.scrollTop -= d.pageStep()
		d.needsFollow = false
		d.refresh()
		return
	case tui.MatchesKey(data, tui.KeyPageDown):
		d.scrollTop += d.pageStep()
		d.needsFollow = false
		d.refresh()
		return
	case tui.MatchesKey(data, tui.KeyCtrl(tui.KeyUp)):
		d.scrollTop--
		d.needsFollow = false
		d.refresh()
		return
	case tui.MatchesKey(data, tui.KeyCtrl(tui.KeyDown)):
		d.scrollTop++
		d.needsFollow = false
		d.refresh()
		return
	}

	if d.currentTab == len(d.questions) {
		if tui.MatchesKey(data, tui.KeyEnter) && d.allAnswered() {
			d.submit(false)
		} else if tui.MatchesKey(data, tui.KeyEscape) {
			d.submit(true)
		}
		return
	}

	if tui.MatchesKey(data, tui.KeyUp) {
		d.optionIndex = max(0, d.optionIndex-1)
		d.needsFollow = true
		d.refresh()
		return
	}
	if tui.MatchesKey(data, tui.KeyDown) {
		d.optionIndex = min(len(opts)-1, d.optionIndex+1)
		d.needsFollow = true
		d.refresh()
		return
	}
	if tui.MatchesKey(data, "a") && d.toggle != nil {
		d.auditorEnabled = !d.auditorEnabled
		d.refresh()
		return
	}
	if tui.MatchesKey(data, tui.KeyEnter) && q != nil {
		if len(q.options) == 0 || (d.optionIndex >= 0 && d.optionIndex < len(opts) && opts[d.optionIndex].isCustom) {
			d.enterInputMode(q.id)
			d.refresh()
			return
		}
		if d.optionIndex >= 0 && d.optionIndex < len(opts) {
			d.saveAnswer(q.id, opts[d.optionIndex].label, false)
			d.advanceAfterAnswer()
		}
		return
	}
	if tui.MatchesKey(data, tui.KeyEscape) {
		d.submit(true)
	}
}

const questionnairePipePrefix = "│   "

func (d *questionnaireDialog) Render(width int) []string {
	if d.hasCached {
		return d.cachedLines
	}
	theme := d.theme
	safeWidth := max(20, width)
	lines := []string{}
	q := d.currentQuestion()
	opts := d.displayOptions()

	add := func(s string) { lines = append(lines, widthx.TruncateToWidth(s, safeWidth, "…", true)) }
	addWrapped := func(s string) { lines = append(lines, widthx.WrapTextWithAnsi(s, safeWidth)...) }
	pipeWidth := widthx.VisibleWidth(questionnairePipePrefix)
	addWrappedPipe := func(styled string) {
		wrapped := widthx.WrapTextWithAnsi(styled, max(1, safeWidth-pipeWidth))
		for i, w := range wrapped {
			if i == 0 {
				lines = append(lines, w)
			} else {
				lines = append(lines, questionnairePipePrefix+w)
			}
		}
	}
	checkColor := func(bracket string) string {
		if bracket == "[x]" {
			return "success"
		}
		return "warning"
	}

	renderContextLines := func(context string) {
		for _, rawLine := range strings.Split(context, "\n") {
			trimmed := jsTrim(rawLine)
			if trimmed == "" {
				lines = append(lines, "")
				continue
			}
			if announceRe.MatchString(trimmed) {
				addWrapped(theme.fg("accent", rawLine))
				continue
			}
			if m := sectionMarkerRe.FindStringSubmatch(trimmed); m != nil {
				namePart := " " + m[1] + " "
				left, right := "┌─", "─┐"
				fill := max(0, safeWidth-2-widthx.VisibleWidth(left+namePart+right))
				add(theme.fg("accent", left+namePart+strings.Repeat("─", fill)+right))
				continue
			}
			if strings.HasPrefix(trimmed, "│") {
				pipeContent := strings.TrimPrefix(trimmed, "│")
				afterPipe := jsTrim(pipeContent)
				if m := checkboxRe.FindStringSubmatch(afterPipe); m != nil {
					innerWs := pipeContent[:len(pipeContent)-len(strings.TrimLeftFunc(pipeContent, isJSSpace))]
					linePrefix := "│" + innerWs
					addWrappedPipe(linePrefix + theme.fg(checkColor(m[1]), m[1]) + m[2] + theme.fg("muted", m[3]))
					continue
				}
				if colonIdx := strings.Index(afterPipe, ": "); colonIdx >= 0 {
					val := jsTrim(afterPipe[colonIdx+2:])
					keyPart := rawLine[:strings.Index(rawLine, afterPipe)+colonIdx+2]
					if val == "yes" || val == "no" {
						c := "warning"
						if val == "yes" {
							c = "success"
						}
						addWrappedPipe(theme.fg("muted", keyPart) + theme.fg(c, val))
						continue
					}
					addWrappedPipe(theme.fg("muted", rawLine))
					continue
				}
				addWrappedPipe(theme.fg("muted", rawLine))
				continue
			}
			if goalSectionRe.MatchString(trimmed) {
				addWrapped(theme.fg("accent", rawLine))
				continue
			}
			if borderStartRe.MatchString(trimmed) {
				addWrapped(theme.fg("dim", rawLine))
				continue
			}
			if m := checkboxRe.FindStringSubmatch(trimmed); m != nil {
				indent := rawLine[:len(rawLine)-len(trimmed)]
				addWrapped(indent + theme.fg(checkColor(m[1]), m[1]) + m[2] + theme.fg("muted", m[3]))
				continue
			}
			addWrapped(theme.fg("muted", rawLine))
		}
	}

	add(theme.fg("accent", strings.Repeat("─", safeWidth)))
	protectedCount := len(lines)
	if d.isMulti {
		tabs := []string{"← "}
		for i, qq := range d.questions {
			isActive := i == d.currentTab
			_, isAnswered := d.answers[qq.id]
			mark := "□"
			if isAnswered {
				mark = "■"
			}
			label := " " + mark + " " + qq.id + " "
			if isActive {
				tabs = append(tabs, theme.bg("selectedBg", theme.fg("text", label)))
			} else if isAnswered {
				tabs = append(tabs, theme.fg("success", label))
			} else {
				tabs = append(tabs, theme.fg("muted", label))
			}
			tabs = append(tabs, " ")
		}
		submitText := " ✓ Submit "
		switch {
		case d.currentTab == len(d.questions):
			tabs = append(tabs, theme.bg("selectedBg", theme.fg("text", submitText)))
		case d.allAnswered():
			tabs = append(tabs, theme.fg("success", submitText))
		default:
			tabs = append(tabs, theme.fg("dim", submitText))
		}
		tabs = append(tabs, " →")
		add(" " + strings.Join(tabs, ""))
		lines = append(lines, "")
		protectedCount = len(lines)
	}

	renderOptions := func() {
		d.optionsStartIndex = len(lines)
		for i, opt := range opts {
			start := len(lines)
			selected := i == d.optionIndex
			prefix := "  "
			if selected {
				prefix = theme.fg("accent", "> ")
			}
			recTag := ""
			if !opt.isCustom && q != nil && q.recommended != nil && *q.recommended == i {
				recTag = theme.fg("success", " ★")
			}
			c := "text"
			if selected {
				c = "accent"
			}
			addWrapped(prefix + theme.fg(c, strconv.Itoa(i+1)+". "+opt.label) + recTag)
			for len(d.optionRanges) <= i {
				d.optionRanges = append(d.optionRanges, [2]int{})
			}
			d.optionRanges[i] = [2]int{start, len(lines) - 1}
		}
	}

	switch {
	case d.inputMode && q != nil:
		addWrapped(theme.fg("text", " "+q.question))
		protectedCount = len(lines)
		if q.context != "" {
			renderContextLines(q.context)
		}
		lines = append(lines, "")
		if len(q.options) > 0 {
			renderOptions()
			lines = append(lines, "")
		}
		add(theme.fg("muted", " Your answer:"))
		for _, line := range d.editor.Render(safeWidth - 2) {
			add(" " + line)
		}
		lines = append(lines, "")
		add(theme.fg("dim", " Enter to submit • Esc to cancel"))
	case d.currentTab == len(d.questions):
		add(theme.fg("accent", theme.bold(" Ready to submit")))
		protectedCount = len(lines)
		lines = append(lines, "")
		for _, question := range d.questions {
			answer, ok := d.answers[question.id]
			var s string
			if ok {
				wrote := ""
				if answer.wasCustom {
					wrote = "(wrote) "
				}
				s = theme.fg("text", wrote+answer.answer)
			} else {
				s = theme.fg("warning", "(unanswered)")
			}
			add(theme.fg("muted", " "+question.id+": ") + s)
		}
		lines = append(lines, "")
		if d.allAnswered() {
			add(theme.fg("success", " Press Enter to submit"))
		} else {
			missing := []string{}
			for _, qq := range d.questions {
				if _, ok := d.answers[qq.id]; !ok {
					missing = append(missing, qq.id)
				}
			}
			add(theme.fg("warning", " Unanswered: "+strings.Join(missing, ", ")))
		}
	case q != nil:
		addWrapped(theme.fg("text", " "+q.question))
		d.questionBlockEnd = len(lines)
		protectedCount = len(lines)
		if q.context != "" {
			renderContextLines(q.context)
		}
		if d.toggle != nil {
			circle, label, color := "○", "Auditor disabled", "warning"
			if d.auditorEnabled {
				circle, label, color = "●", "Auditor enabled", "success"
			}
			add(theme.fg(color, " "+circle+" "+label) + theme.fg("dim", "  (press 'a' to toggle)"))
			lines = append(lines, "")
		}
		if existing, ok := d.answers[q.id]; ok {
			wrote := ""
			if existing.wasCustom {
				wrote = "(wrote) "
			}
			add(theme.fg("dim", " Current: "+wrote+existing.answer))
		}
		lines = append(lines, "")
		if len(opts) > 0 {
			renderOptions()
		} else {
			add(theme.fg("muted", " Press Enter to write your answer"))
		}
	}

	lines = append(lines, "")
	if !d.inputMode {
		hint := ""
		if d.toggle != nil {
			hint = " • a toggle auditor"
		}
		if d.isMulti {
			add(theme.fg("dim", " Tab/←→ navigate • ↑↓ select • Enter confirm • Esc cancel"+hint))
		} else {
			add(theme.fg("dim", " ↑↓ navigate • Enter select • Esc cancel"+hint))
		}
	}
	add(theme.fg("accent", strings.Repeat("─", safeWidth)))
	for i, line := range lines {
		if line != "" && widthx.VisibleWidth(line) > safeWidth {
			lines[i] = widthx.TruncateToWidth(line, safeWidth, "...", false)
		}
	}

	if d.hasMaxDialogLines && len(lines) > d.maxDialogLines {
		dim := func(s string) string { return theme.fg("dim", s) }
		var proposalSegments *proposalPresentationSegments
		if !d.inputMode && d.currentTab != len(d.questions) && q != nil && q.context != "" {
			proposalSegments = findProposalPresentationSegments(lines, d.optionsStartIndex)
		}
		switch {
		case proposalSegments != nil:
			lines = fitDialogLines(lines, d.maxDialogLines, protectedCount, proposalSegments, nil, nil)
		case !d.inputMode && d.currentTab != len(d.questions) && d.questionBlockEnd >= 0:
			lastOpt := -1
			if n := len(d.optionRanges); n > 0 {
				lastOpt = d.optionRanges[n-1][1]
			}
			if fitted := fitOptionsInFrame(lines, d.maxDialogLines, d.questionBlockEnd, d.optionsStartIndex, lastOpt); fitted != nil {
				lines = fitted
				d.scrollTop = 0
				d.needsFollow = false
			} else {
				st := &dialogScrollState{d.scrollTop, d.needsFollow, d.optionRanges, d.optionIndex}
				lines = fitDialogLines(lines, d.maxDialogLines, protectedCount, nil, st, dim)
				d.scrollTop, d.needsFollow = st.scrollTop, st.needsFollow
			}
		default:
			var st *dialogScrollState
			if !d.inputMode {
				st = &dialogScrollState{d.scrollTop, d.needsFollow, d.optionRanges, d.optionIndex}
			}
			lines = fitDialogLines(lines, d.maxDialogLines, protectedCount, nil, st, dim)
			if st != nil {
				d.scrollTop, d.needsFollow = st.scrollTop, st.needsFollow
			}
		}
	}
	d.cachedLines = lines
	d.hasCached = true
	return lines
}
