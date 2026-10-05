// Package ask_user_question is a Go port of @juicesharp/rpiv-ask-user-question (rpiv-mono
// packages/rpiv-ask-user-question 2.11.0): a structured questionnaire the model can put to the user.
package ask_user_question

import "encoding/json"

// Limits of one questionnaire. upstream: tool/types.ts.
const (
	maxQuestions    = 4
	minOptions      = 2
	maxOptions      = 4
	maxHeaderLength = 16
	maxLabelLength  = 60
)

const toolName = "ask_user_question" // upstream: ASK_USER_QUESTION_TOOL_NAME

// Events other extensions can listen to. upstream: events.ts.
const (
	promptEvent  = "rpiv:ask-user:prompt"
	blockedEvent = "rpiv:ask-user:blocked"
)

// Error codes of a questionnaire result. upstream: QuestionnaireError.
const (
	errNoUI              = "no_ui"
	errNoCustomUI        = "no_custom_ui"
	errNoQuestions       = "no_questions"
	errEmptyOptions      = "empty_options"
	errTooManyQuestions  = "too_many_questions"
	errDuplicateQuestion = "duplicate_question"
	errDuplicateOption   = "duplicate_option_label"
	errReservedLabel     = "reserved_label"
)

// option is one choice. An empty Preview is the same as no preview (the original tests `preview.length > 0`).
type option struct {
	Label       string
	Description string
	Preview     string
}

type question struct {
	Question    string
	Header      string
	Options     []option
	MultiSelect bool
}

type questionParams struct {
	Questions []question
}

// questionAnswer is one answered question. Answer is nil where the original has `null` (an empty multi-select);
// Selected is set only for kind "multi", and an empty set is kept ([] on the wire).
type questionAnswer struct {
	QuestionIndex int
	Question      string
	Kind          string // "option", "custom" or "multi"
	Answer        *string
	Selected      []string
	HasSelected   bool
	Notes         string
	Preview       string
}

// MarshalJSON writes the keys the original writes: answer is always present, selected only for multi,
// notes and preview only when set.
func (a questionAnswer) MarshalJSON() ([]byte, error) {
	m := map[string]any{"questionIndex": a.QuestionIndex, "question": a.Question, "kind": a.Kind, "answer": a.Answer}
	if a.HasSelected {
		sel := a.Selected
		if sel == nil {
			sel = []string{}
		}
		m["selected"] = sel
	}
	if a.Notes != "" {
		m["notes"] = a.Notes
	}
	if a.Preview != "" {
		m["preview"] = a.Preview
	}
	return json.Marshal(m)
}

// questionnaireResult is the tool's details. Answers is always an array on the wire.
type questionnaireResult struct {
	Answers    []questionAnswer `json:"-"`
	Cancelled  bool             `json:"cancelled"`
	GlobalNote string           `json:"globalNote,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func (r questionnaireResult) MarshalJSON() ([]byte, error) {
	answers := r.Answers
	if answers == nil {
		answers = []questionAnswer{}
	}
	m := map[string]any{"answers": answers, "cancelled": r.Cancelled}
	if r.GlobalNote != "" {
		m["globalNote"] = r.GlobalNote
	}
	if r.Error != "" {
		m["error"] = r.Error
	}
	return json.Marshal(m)
}

// toolOutput is what the tool returns before it is put on the wire: the model-facing text and the details.
type toolOutput struct {
	Text    string
	Details questionnaireResult
}
