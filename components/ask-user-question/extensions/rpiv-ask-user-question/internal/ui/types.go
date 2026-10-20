package ui

// Data types of the tool the questionnaire shows and returns. upstream: tool/types.ts (OptionData, QuestionData,
// QuestionAnswer, QuestionnaireResult). The root package converts to and from its own wire types.

// Row kinds of a WrappingSelectItem. upstream: WrappingSelectItem["kind"] (view/components/wrapping-select.ts).
const (
	KindOption = "option"
	KindOther  = "other"
	KindNext   = "next"
)

// OptionData is one author-defined option. An empty Preview is `preview` absent: the original tests
// `typeof preview === "string" && preview.length > 0`.
type OptionData struct {
	Label       string
	Description string
	Preview     string
}

// QuestionData is one question. upstream: QuestionData. MultiSelect false is `multiSelect` absent or false.
type QuestionData struct {
	Question    string
	Header      string
	Options     []OptionData
	MultiSelect bool
}

// QuestionAnswer is one answered question. upstream: QuestionAnswer. Answer is nil where the original has null.
// Selected is meaningful only for Kind "multi" (HasSelected tells an empty selection from none). Notes and Preview
// are `notes?` and `preview?`: empty means absent.
type QuestionAnswer struct {
	QuestionIndex int
	Question      string
	Kind          string // "option" | "custom" | "multi"
	Answer        *string
	Selected      []string
	HasSelected   bool
	Notes         string
	Preview       string
}

// QuestionnaireResult is what the questionnaire resolves with. upstream: QuestionnaireResult. GlobalNote empty is
// absent (the original never keeps an empty one).
type QuestionnaireResult struct {
	Answers    []QuestionAnswer
	Cancelled  bool
	GlobalNote string
	Error      string
}
