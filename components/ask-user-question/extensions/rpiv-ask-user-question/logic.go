package ask_user_question

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
)

// reservedLabels cannot be used as an option label: the canonical English sentinel labels (never the localized
// ones), plus "Other". upstream: tool/types.ts RESERVED_LABELS.
var reservedLabels = []string{"Other", ui.RowIntent(ui.KindOther).Label, ui.RowIntent(ui.KindNext).Label}

// Texts of the rejections and the envelope. upstream: tool/validate-questionnaire.ts, tool/response-envelope.ts.
const (
	errTextNoQuestions       = "Error: At least one question is required"
	errTextTooManyQuestions  = "Error: At most 4 questions are allowed per invocation"
	errTextDuplicateQuestion = "Error: Question text must be unique within an invocation"
	errTextTooFewOptions     = "Error: Each question requires at least 2 options"
	errTextReservedLabel     = "Error: Option label is reserved (Other, Type something., Next)"
	errTextDuplicateOption   = "Error: Option labels must be unique within a question"

	declineMessage = "User declined to answer questions"
	envelopePrefix = "User has answered your questions:"
	envelopeSuffix = "You can now continue with the user's answers in mind."
	noInput        = "(no input)"
)

type validation struct {
	OK      bool
	Error   string
	Message string
}

// normalizeLineTerminators maps CRLF to LF and deletes a lone CR. upstream: tool/normalize-params.ts.
func normalizeLineTerminators(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "")
}

// normalizeQuestionParams runs once, ahead of validation, so every consumer sees the same clean text (#192).
// The input is not modified.
func normalizeQuestionParams(p questionParams) questionParams {
	out := questionParams{}
	for _, q := range p.Questions {
		n := question{
			Question:    normalizeLineTerminators(q.Question),
			Header:      normalizeLineTerminators(q.Header),
			MultiSelect: q.MultiSelect,
		}
		for _, o := range q.Options {
			n.Options = append(n.Options, option{
				Label:       normalizeLineTerminators(o.Label),
				Description: normalizeLineTerminators(o.Description),
				Preview:     normalizeLineTerminators(o.Preview),
			})
		}
		out.Questions = append(out.Questions, n)
	}
	return out
}

// validateQuestionnaire is the runtime validation the schema cannot express. The checks run in the original's
// order: count, then every question's text, then each question's options.
func validateQuestionnaire(p questionParams) validation {
	if len(p.Questions) == 0 {
		return validation{Error: errNoQuestions, Message: errTextNoQuestions}
	}
	if len(p.Questions) > maxQuestions {
		return validation{Error: errTooManyQuestions, Message: errTextTooManyQuestions}
	}
	seenQuestions := map[string]bool{}
	for _, q := range p.Questions {
		if seenQuestions[q.Question] {
			return validation{Error: errDuplicateQuestion, Message: errTextDuplicateQuestion}
		}
		seenQuestions[q.Question] = true
	}
	for _, q := range p.Questions {
		if len(q.Options) < minOptions {
			return validation{Error: errEmptyOptions, Message: errTextTooFewOptions}
		}
		seen := map[string]bool{}
		for _, o := range q.Options {
			for _, reserved := range reservedLabels {
				if o.Label == reserved {
					return validation{Error: errReservedLabel, Message: errTextReservedLabel}
				}
			}
			if seen[o.Label] {
				return validation{Error: errDuplicateOption, Message: errTextDuplicateOption}
			}
			seen[o.Label] = true
		}
	}
	return validation{OK: true}
}

// buildItemsForQuestion lists a question's options (preview dropped) and the sentinel rows it gets, labelled in the
// active locale. upstream: ask-user-question.ts buildItemsForQuestion, state/row-intent.ts sentinelsToAppend.
func buildItemsForQuestion(q question) []ui.WrappingSelectItem {
	items := make([]ui.WrappingSelectItem, 0, len(q.Options)+2)
	for _, o := range q.Options {
		items = append(items, ui.WrappingSelectItem{Kind: ui.KindOption, Label: o.Label, Description: o.Description})
	}
	for _, kind := range ui.SentinelsToAppend(uiQuestion(q)) {
		items = append(items, ui.WrappingSelectItem{Kind: kind, Label: ui.DisplayLabel(kind)})
	}
	return items
}

// formatAnswerScalar is the answer as the envelope shows it. upstream: tool/format-answer.ts.
func formatAnswerScalar(a questionAnswer) string {
	switch a.Kind {
	case "multi":
		if len(a.Selected) > 0 {
			return strings.Join(a.Selected, ", ")
		}
		return noInput
	case "custom":
		if a.Answer != nil && *a.Answer != "" {
			return *a.Answer
		}
		return noInput
	default: // "option"
		if a.Answer != nil {
			return *a.Answer
		}
		return noInput
	}
}

// buildAnswerSegment is `"<question>"="<answer>"`, then the matched preview and the notes, ending in a period.
func buildAnswerSegment(a questionAnswer) string {
	parts := []string{`"` + a.Question + `"="` + formatAnswerScalar(a) + `"`}
	if a.Preview != "" {
		parts = append(parts, "selected preview: "+a.Preview)
	}
	if a.Notes != "" {
		parts = append(parts, "user notes: "+a.Notes)
	}
	return strings.Join(parts, ". ") + "."
}

func buildToolResult(text string, d questionnaireResult) toolOutput {
	return toolOutput{Text: text, Details: d}
}

// buildQuestionnaireResponse turns a result into the tool's output: a decline when the user cancelled (the
// answers so far stay in the details), otherwise the envelope the model reads. A nil result is a decline.
func buildQuestionnaireResponse(r *questionnaireResult, p questionParams) toolOutput {
	if r == nil || r.Cancelled {
		d := questionnaireResult{Cancelled: true}
		if r != nil {
			d.Answers, d.GlobalNote = r.Answers, r.GlobalNote
		}
		return buildToolResult(declineMessage, d)
	}
	var segments []string
	for i := range p.Questions {
		for _, a := range r.Answers {
			if a.QuestionIndex == i {
				segments = append(segments, buildAnswerSegment(a))
				break
			}
		}
	}
	if r.GlobalNote != "" {
		segments = append(segments, "global note: "+r.GlobalNote+".")
	}
	if len(segments) == 0 {
		return buildToolResult(declineMessage, questionnaireResult{Answers: r.Answers, Cancelled: true})
	}
	return buildToolResult(envelopePrefix+" "+strings.Join(segments, " ")+" "+envelopeSuffix, *r)
}
