package ask_user_question

import (
	"strings"
	"testing"
)

const fExecute = "ask-user-question.execute"

func TestValidateQuestionnaire(t *testing.T) {
	tw(t, fExecute, "returns ERROR_NO_QUESTIONS text when questions array is empty", func(t *testing.T) {
		eq(t, validateQuestionnaire(params()), validation{Error: errNoQuestions, Message: "Error: At least one question is required"}, "result")
	})
	tw(t, fExecute, "returns error: too_many_questions when questions exceed MAX_QUESTIONS", func(t *testing.T) {
		var qs []question
		for i := 0; i < 5; i++ {
			qs = append(qs, qn(string(rune('a'+i)), "H", two()...))
		}
		eq(t, validateQuestionnaire(params(qs...)), validation{Error: errTooManyQuestions, Message: "Error: At most 4 questions are allowed per invocation"}, "result")
	})
	tw(t, fExecute, "returns cancelled result when any question has empty options", func(t *testing.T) {
		got := validateQuestionnaire(params(qn("Q?", "H")))
		eq(t, got, validation{Error: errEmptyOptions, Message: "Error: Each question requires at least 2 options"}, "result")
	})
	tw(t, fExecute, "widens empty_options check to < MIN_OPTIONS (single-option rejected)", func(t *testing.T) {
		eq(t, validateQuestionnaire(params(qn("Q?", "H", opt("only", "d")))).Error, errEmptyOptions, "error")
	})
	tw(t, fExecute, "returns error: duplicate_question when two questions share text", func(t *testing.T) {
		got := validateQuestionnaire(params(qn("Same?", "A", two()...), qn("Same?", "B", two()...)))
		eq(t, got, validation{Error: errDuplicateQuestion, Message: "Error: Question text must be unique within an invocation"}, "result")
	})
	tw(t, fExecute, "returns error: duplicate_option_label when two options in a question share label", func(t *testing.T) {
		got := validateQuestionnaire(params(qn("Q?", "H", opt("a", "1"), opt("a", "2"))))
		eq(t, got, validation{Error: errDuplicateOption, Message: "Error: Option labels must be unique within a question"}, "result")
	})
	tw(t, fExecute, "returns error: reserved_label when an option uses 'Other' / 'Type something.'", func(t *testing.T) {
		want := validation{Error: errReservedLabel, Message: "Error: Option label is reserved (Other, Type something., Next)"}
		for _, label := range []string{"Other", "Type something.", "Next"} {
			eq(t, validateQuestionnaire(params(qn("Q?", "H", opt(label, "d"), opt("x", "d")))), want, label)
		}
	})
	tw(t, fExecute, "rejects 'Type something.' as a reserved label even on multiSelect questions (Decision 9)", func(t *testing.T) {
		got := validateQuestionnaire(params(multi(qn("Q?", "H", opt("Type something.", "d"), opt("x", "d")))))
		eq(t, got.Error, errReservedLabel, "error")
	})
}

func TestValidateAcceptsAValidQuestionnaireAndChecksInOrder(t *testing.T) {
	eq(t, validateQuestionnaire(single()), validation{OK: true}, "valid")
	// Duplicate question text is reported before a short options list (the original checks every question's text first).
	got := validateQuestionnaire(params(qn("Same?", "A", opt("only", "d")), qn("Same?", "B", opt("only", "d"))))
	eq(t, got.Error, errDuplicateQuestion, "order")
	// A reserved label is reported before a duplicate label in the same question.
	got = validateQuestionnaire(params(qn("Q?", "H", opt("Other", "1"), opt("Other", "2"))))
	eq(t, got.Error, errReservedLabel, "reserved first")
	if !strings.HasPrefix(got.Message, "Error: ") {
		t.Fatalf("message %q", got.Message)
	}
}

func TestNormalizedLabelsAreValidated(t *testing.T) {
	const f = "ask-user-question.normalize"
	tw(t, f, "rejects a reserved label that only differed by a trailing CR", func(t *testing.T) {
		got := validateQuestionnaire(normalizeQuestionParams(params(qn("Q?", "H", opt("Other\r", "d"), opt("x", "d")))))
		eq(t, got.Error, errReservedLabel, "error")
	})
	tw(t, f, "rejects duplicate labels that only differed by a CR", func(t *testing.T) {
		got := validateQuestionnaire(normalizeQuestionParams(params(qn("Q?", "H", opt("a\r", "d"), opt("a", "d")))))
		eq(t, got.Error, errDuplicateOption, "error")
	})
}
