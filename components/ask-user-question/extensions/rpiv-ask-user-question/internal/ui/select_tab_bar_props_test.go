package ui

import "testing"

// selectTabBarProps labels a tab with its question's header, or Q<n> (one-based) when the header is empty.
func TestSelectTabBarPropsLabelsHeaderlessTabsByPosition(t *testing.T) {
	ctx := BindingContext{Questions: []QuestionData{{Header: "Lib"}, {Header: ""}, {Header: "Merge"}, {}}}
	props := SelectTabBarProps(QuestionnaireState{CurrentTab: 1, Answers: map[int]QuestionAnswer{2: {}}}, ctx)
	want := []TabBarTab{
		{Label: "Lib"},
		{Label: "Q2", Active: true},
		{Label: "Merge", Answered: true},
		{Label: "Q4"},
	}
	if len(props.Tabs) != len(want) {
		t.Fatalf("tabs = %+v", props.Tabs)
	}
	for i, tab := range want {
		if props.Tabs[i] != tab {
			t.Fatalf("tab %d = %+v, want %+v", i, props.Tabs[i], tab)
		}
	}
}
