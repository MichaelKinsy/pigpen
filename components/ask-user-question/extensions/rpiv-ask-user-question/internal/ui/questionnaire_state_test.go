package ui

import (
	"reflect"
	"testing"
)

func answersOf(a QuestionAnswer) map[int]QuestionAnswer { return map[int]QuestionAnswer{0: a} }

func TestQuestionnaireState(t *testing.T) {
	const f = "state/questionnaire-state"
	q := makeQuestion
	hello, bs, zz, as := "Hello", "B", "ZZ", "A"
	custom := QuestionAnswer{QuestionIndex: 0, Question: "q", Kind: "custom", Answer: &hello}

	tw(t, f, "returns undefined when the question is multiSelect", func(t *testing.T) {
		answers := answersOf(QuestionAnswer{QuestionIndex: 0, Question: "q", Kind: "option", Answer: &as})
		if SelectConfirmedIndicator([]QuestionData{q(multiQ)}, 0, answers, itemsRegular) != nil {
			t.Fatal("want nil")
		}
	})
	tw(t, f, "returns undefined when there is no prior answer for the tab", func(t *testing.T) {
		if SelectConfirmedIndicator([]QuestionData{q()}, 0, map[int]QuestionAnswer{}, itemsRegular) != nil {
			t.Fatal("want nil")
		}
	})
	tw(t, f, "returns the kind:'other' index + labelOverride when the prior answer was kind:'custom'", func(t *testing.T) {
		got := SelectConfirmedIndicator([]QuestionData{q()}, 0, answersOf(custom), itemsWithOther)
		if got == nil || got.Index != 2 || got.LabelOverride == nil || *got.LabelOverride != "Hello" {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "returns undefined when kind:'custom' but the items array has no kind:'other' row", func(t *testing.T) {
		if SelectConfirmedIndicator([]QuestionData{q()}, 0, answersOf(custom), itemsRegular) != nil {
			t.Fatal("want nil")
		}
	})
	tw(t, f, "returns the matching index for a regular label answer", func(t *testing.T) {
		answers := answersOf(QuestionAnswer{QuestionIndex: 0, Question: "q", Kind: "option", Answer: &bs})
		got := SelectConfirmedIndicator([]QuestionData{q()}, 0, answers, itemsRegular)
		if got == nil || !reflect.DeepEqual(*got, OptionListConfirmed{Index: 1}) {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "returns undefined when the prior label matches no row (defensive)", func(t *testing.T) {
		answers := answersOf(QuestionAnswer{QuestionIndex: 0, Question: "q", Kind: "option", Answer: &zz})
		if SelectConfirmedIndicator([]QuestionData{q()}, 0, answers, itemsRegular) != nil {
			t.Fatal("want nil")
		}
	})

	tw(t, f, "returns currentTab when within range", func(t *testing.T) {
		if SelectActivePreviewPaneIndex(1, 3) != 1 {
			t.Fatal()
		}
	})
	tw(t, f, "clamps to the last question index when on the Submit tab", func(t *testing.T) {
		if SelectActivePreviewPaneIndex(3, 3) != 2 {
			t.Fatal()
		}
	})
	tw(t, f, "returns 0 when totalQuestions is 0 (defensive)", func(t *testing.T) {
		if SelectActivePreviewPaneIndex(0, 0) != 0 {
			t.Fatal()
		}
	})

	tw(t, f, "returns 'notes' when notesVisible is true", func(t *testing.T) {
		if SelectActiveView(true, 0, 2) != ActiveViewNotes {
			t.Fatal()
		}
	})
	tw(t, f, "returns 'submit' when currentTab equals totalQuestions and notes hidden", func(t *testing.T) {
		if SelectActiveView(false, 2, 2) != ActiveViewSubmit {
			t.Fatal()
		}
	})
	tw(t, f, "returns 'options' as the default", func(t *testing.T) {
		if SelectActiveView(false, 0, 2) != ActiveViewOptions {
			t.Fatal()
		}
	})
	tw(t, f, "priority order: notes wins over Submit-tab (matches dispatcher cascade)", func(t *testing.T) {
		if SelectActiveView(true, 2, 2) != ActiveViewNotes {
			t.Fatal()
		}
	})

	tw(t, f, "returns the kind:'other' index + labelOverride when prior was kind:'custom'", func(t *testing.T) {
		items := []WrappingSelectItem{{Kind: "option", Label: "A"}, {Kind: "other", Label: "Type something."}}
		questions := []QuestionData{q(func(x *QuestionData) { x.Options = []OptionData{{Label: "A", Description: "a"}} })}
		got := SelectConfirmedIndicator(questions, 0, answersOf(custom), items)
		if got == nil || got.Index != 1 || got.LabelOverride == nil || *got.LabelOverride != "Hello" {
			t.Fatalf("%+v", got)
		}
	})
}
