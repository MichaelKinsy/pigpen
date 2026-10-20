package ui

// Port of the parts of test-fixtures.ts the state layer and props-adapter twins use. The view lanes keep their own
// fixtures under vw* names in view_fixtures_test.go.

var itemsRegular = []WrappingSelectItem{
	{Kind: "option", Label: "A"},
	{Kind: "option", Label: "B"},
}

var itemsWithOther = []WrappingSelectItem{
	{Kind: "option", Label: "A"},
	{Kind: "option", Label: "B"},
	{Kind: "other", Label: "Type something."},
}

// makeQuestion is makeQuestion of test-fixtures.ts: defaults overridden by mods.
func makeQuestion(mods ...func(*QuestionData)) QuestionData {
	q := QuestionData{
		Question: "Pick one",
		Header:   "H",
		Options:  []OptionData{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}},
	}
	for _, m := range mods {
		m(&q)
	}
	return q
}

func multiQ(q *QuestionData) { q.MultiSelect = true }

// makeQuestionnaireState is makeQuestionnaireState of test-fixtures.ts: defaults overridden by mods.
func makeQuestionnaireState(mods ...func(*QuestionnaireState)) QuestionnaireState {
	s := QuestionnaireState{
		Answers:            map[int]QuestionAnswer{},
		MultiSelectChecked: map[int]bool{},
		CustomDraftsByTab:  map[int]string{},
		NotesByTab:         map[int]string{},
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

// makeApplyContext is makeApplyContext of test-fixtures.ts.
func makeApplyContext(mods ...func(*ApplyContext)) ApplyContext {
	c := ApplyContext{}
	for _, m := range mods {
		m(&c)
	}
	if c.Questions == nil {
		c.Questions = []QuestionData{makeQuestion()}
	}
	if c.ItemsByTab == nil {
		for range c.Questions {
			c.ItemsByTab = append(c.ItemsByTab, itemsRegular)
		}
	}
	return c
}

// fakeStatefulView is makeStatefulView of test-fixtures.ts: SetProps is a spy (vi.fn()).
type fakeStatefulView[P any] struct {
	calls       []P
	invalidates int
}

func makeStatefulView[P any]() *fakeStatefulView[P] { return &fakeStatefulView[P]{} }

func (f *fakeStatefulView[P]) SetProps(p P)              { f.calls = append(f.calls, p) }
func (f *fakeStatefulView[P]) Render(width int) []string { return []string{} }
func (f *fakeStatefulView[P]) Invalidate()               { f.invalidates++ }
func (f *fakeStatefulView[P]) last() P                   { return f.calls[len(f.calls)-1] }

// fakePreviewPane is makeFakePreviewPane.
type fakePreviewPane struct {
	fakeStatefulView[PreviewPaneProps]
}

func makeFakePreviewPane() *fakePreviewPane                   { return &fakePreviewPane{} }
func (f *fakePreviewPane) SetGlobalLeftWidth(func(int) int)   {}
func (f *fakePreviewPane) NaturalHeight(int) int              { return 0 }
func (f *fakePreviewPane) MaxNaturalHeight(int) int           { return 0 }
func (f *fakePreviewPane) FocusedItemRowRange(int) (int, int) { return 0, 0 }

// fakeMultiSelectView is makeFakeMultiSelectView.
type fakeMultiSelectView struct {
	fakeStatefulView[MultiSelectViewProps]
}

func makeFakeMultiSelectView() *fakeMultiSelectView               { return &fakeMultiSelectView{} }
func (f *fakeMultiSelectView) FocusedItemRowRange(int) (int, int) { return 0, 0 }
func (f *fakeMultiSelectView) NaturalHeight(int) int              { return 0 }

// makeTabComponents is makeTabComponents of test-fixtures.ts.
func makeTabComponents(mods ...func(*TabComponents)) TabComponents {
	t := TabComponents{}
	for _, m := range mods {
		m(&t)
	}
	if t.OptionList == nil {
		t.OptionList = makeStatefulView[OptionListViewProps]()
	}
	if t.Preview == nil {
		t.Preview = makeFakePreviewPane()
	}
	if t.BodyHeights == nil {
		t.BodyHeights = func(int) TabBodyHeights { return TabBodyHeights{} }
	}
	return t
}
