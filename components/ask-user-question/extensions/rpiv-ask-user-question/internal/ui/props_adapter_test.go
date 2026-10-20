package ui

import (
	"reflect"
	"testing"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

type adapterFixture struct {
	adapter     *QuestionnairePropsAdapter
	tui         *fakeTUI
	dialog      *fakeStatefulView[DialogProps]
	tabs        []TabComponents
	optionLists []*fakeStatefulView[OptionListViewProps]
	previews    []*fakePreviewPane
	multis      []*fakeMultiSelectView
	submit      *fakeStatefulView[SubmitPickerProps]
	tabBar      *fakeStatefulView[TabBarProps]
	questions   []QuestionData
	inlineInput *tui.Editor
}

func makeAdapterFixture(overQuestions []QuestionData) adapterFixture {
	questions := overQuestions
	if questions == nil {
		questions = []QuestionData{makeQuestion(), makeQuestion()}
	}
	var itemsByTab [][]WrappingSelectItem
	fx := adapterFixture{questions: questions}
	for _, q := range questions {
		itemsByTab = append(itemsByTab, []WrappingSelectItem{{Kind: "option", Label: "A"}, {Kind: "option", Label: "B"}})
		ol := makeStatefulView[OptionListViewProps]()
		pv := makeFakePreviewPane()
		var ms *fakeMultiSelectView
		tab := makeTabComponents(func(t *TabComponents) { t.OptionList = ol; t.Preview = pv })
		if q.MultiSelect {
			ms = makeFakeMultiSelectView()
			tab.MultiSelect = ms
		}
		fx.tabs = append(fx.tabs, tab)
		fx.optionLists = append(fx.optionLists, ol)
		fx.previews = append(fx.previews, pv)
		fx.multis = append(fx.multis, ms)
	}
	fx.submit = makeStatefulView[SubmitPickerProps]()
	fx.tabBar = makeStatefulView[TabBarProps]()
	fx.dialog = makeStatefulView[DialogProps]()
	fx.tui = &fakeTUI{columns: 120, rows: 40}
	fx.inlineInput = tui.NewEditor(tui.EditorTheme{BorderColor: func(s string) string { return s }})

	globalBindings := []BoundGlobalBinding{
		GlobalBinding(ComponentBinding[DialogProps]{Component: fx.dialog, Select: SelectDialogProps}),
		GlobalBinding(ComponentBinding[SubmitPickerProps]{Component: fx.submit, Select: SelectSubmitPickerProps}),
		GlobalBinding(ComponentBinding[TabBarProps]{Component: fx.tabBar, Select: SelectTabBarProps}),
	}
	isActive := func(s QuestionnaireState, ctx PerTabBindingContext) bool {
		paneIdx := 0
		if ctx.TotalQuestions > 0 {
			paneIdx = min(s.CurrentTab, ctx.TotalQuestions-1)
		}
		return ctx.I == paneIdx
	}
	perTab := []BoundPerTabBinding{
		PerTabBindingOf(PerTabBinding[OptionListViewProps]{
			Resolve: func(tab *TabComponents) StatefulView[OptionListViewProps] { return tab.OptionList }, Predicate: isActive, Select: SelectOptionListProps,
		}),
		PerTabBindingOf(PerTabBinding[PreviewPaneProps]{
			Resolve: func(tab *TabComponents) StatefulView[PreviewPaneProps] { return tab.Preview }, Predicate: isActive, Select: SelectPreviewPaneProps,
		}),
		PerTabBindingOf(PerTabBinding[MultiSelectViewProps]{
			Resolve: func(tab *TabComponents) StatefulView[MultiSelectViewProps] {
				if tab.MultiSelect == nil {
					return nil
				}
				return tab.MultiSelect
			},
			Select: SelectMultiSelectProps,
		}),
	}
	fx.adapter = NewQuestionnairePropsAdapter(QuestionnairePropsAdapterConfig{
		TUI: fx.tui, Questions: questions, ItemsByTab: itemsByTab, TabsByIndex: fx.tabs, InlineInput: fx.inlineInput,
		GlobalBindings: globalBindings, PerTabBindings: perTab,
	})
	return fx
}

func TestPropsAdapter(t *testing.T) {
	const f = "view/props-adapter"
	eqProps := func(t *testing.T, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}

	tw(t, f, "calls dialog.setProps exactly once with state + activePreviewPane", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		state := makeQuestionnaireState()
		fx.adapter.Apply(state)
		if len(fx.dialog.calls) != 1 {
			t.Fatalf("calls %d", len(fx.dialog.calls))
		}
		got := fx.dialog.calls[0]
		eqProps(t, *got.State, state)
		if got.ActivePreviewPane != StatefulView[PreviewPaneProps](fx.previews[0]) {
			t.Fatal("pane")
		}
	})
	tw(t, f, "drives the active OptionListView via setProps and the active PreviewPane via setProps", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		b := "B"
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Pick one", Kind: "option", Answer: &b}}
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.OptionIndex = 1; s.Answers = answers }))
		zero := 0
		eqProps(t, fx.optionLists[0].last(), OptionListViewProps{
			SelectedIndex: 1, Focused: true, InputBuffer: "", InputCursorOffset: &zero, Confirmed: &OptionListConfirmed{Index: 1},
		})
		eqProps(t, fx.previews[0].last(), PreviewPaneProps{NotesVisible: false, SelectedIndex: 1, Focused: true, InputMode: false})
	})
	tw(t, f, "suppresses option focus when notes is visible", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.NotesVisible = true }))
		if fx.optionLists[0].last().Focused {
			t.Fatal("focused")
		}
	})
	tw(t, f, "focuses the submitPicker only when on the Submit tab", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 0 }))
		eqProps(t, fx.submit.last(), SubmitPickerProps{Rows: []SubmitPickerRow{{false}, {false}}})
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = len(fx.questions); s.SubmitChoiceIndex = 0 }))
		eqProps(t, fx.submit.last(), SubmitPickerProps{Rows: []SubmitPickerRow{{true}, {false}}})
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = len(fx.questions); s.SubmitChoiceIndex = 1 }))
		eqProps(t, fx.submit.last(), SubmitPickerProps{Rows: []SubmitPickerRow{{false}, {true}}})
	})
	tw(t, f, "forwards selectTabBarProps projection to tabBar.setProps", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		a := "A"
		answers := map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "q", Kind: "option", Answer: &a}}
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.Answers = answers }))
		arg := fx.tabBar.calls[0]
		if len(arg.Tabs) != 2 {
			t.Fatal("tabs")
		}
		eqProps(t, arg.Tabs[0], TabBarTab{Label: "H", Answered: true, Active: true})
		eqProps(t, arg.Tabs[1], TabBarTab{Label: "H", Answered: false, Active: false})
		eqProps(t, arg.Submit, TabBarSubmit{Active: false, AllAnswered: false})
	})
	tw(t, f, "calls tui.requestRender exactly once", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		fx.adapter.Apply(makeQuestionnaireState())
		if fx.tui.renders != 1 {
			t.Fatalf("renders %d", fx.tui.renders)
		}
	})
	tw(t, f, "multi-select panes get setProps on every apply", func(t *testing.T) {
		fx := makeAdapterFixture([]QuestionData{makeQuestion(multiQ), makeQuestion()})
		fx.adapter.Apply(makeQuestionnaireState())
		mso := fx.multis[0]
		if len(mso.calls) != 1 {
			t.Fatalf("calls %d", len(mso.calls))
		}
		arg := mso.calls[0]
		if arg.Rows == nil || arg.NextActive || arg.NextLabel != "Next" {
			t.Fatalf("%+v", arg)
		}
		if !arg.Rows[0].Active || arg.Rows[0].Checked {
			t.Fatalf("row %+v", arg.Rows[0])
		}
	})
	tw(t, f, "multi-select on the LAST question receives nextLabel='Submit'", func(t *testing.T) {
		fx := makeAdapterFixture([]QuestionData{makeQuestion(), makeQuestion(multiQ)})
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 1 }))
		if got := fx.multis[1].last().NextLabel; got != "Submit" {
			t.Fatal(got)
		}
	})
	tw(t, f, "multi-select on a non-last question receives nextLabel='Next'", func(t *testing.T) {
		fx := makeAdapterFixture([]QuestionData{makeQuestion(multiQ), makeQuestion()})
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 0 }))
		if got := fx.multis[0].last().NextLabel; got != "Next" {
			t.Fatal(got)
		}
	})
	tw(t, f, "projects multiline editor text and its public cursor position", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		fx.inlineInput.SetText("first\nsecond")
		fx.adapter.Apply(makeQuestionnaireState())
		got := fx.optionLists[0].last()
		if got.InputBuffer != "first\nsecond" || got.InputCursorOffset == nil || *got.InputCursorOffset != 12 {
			t.Fatalf("%+v", got)
		}
	})
	tw(t, f, "forwards the resolved pane to dialog.setProps via activePreviewPane", func(t *testing.T) {
		fx := makeAdapterFixture(nil)
		fx.adapter.Apply(makeQuestionnaireState(func(s *QuestionnaireState) { s.CurrentTab = 1 }))
		if fx.dialog.calls[0].ActivePreviewPane != StatefulView[PreviewPaneProps](fx.previews[1]) {
			t.Fatal("pane")
		}
	})
}
