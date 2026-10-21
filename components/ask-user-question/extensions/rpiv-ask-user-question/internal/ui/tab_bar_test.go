package ui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

type tabBarQ struct{ header, question string }

type tabBarPropsOver struct {
	questions       []tabBarQ
	answeredIndices []int
	activeTabIndex  int
	totalTabs       int
}

func buildTabBarProps(over tabBarPropsOver) TabBarProps {
	questions := over.questions
	if questions == nil {
		questions = []tabBarQ{{"Scope", "Which scope?"}, {"Priority", "How urgent?"}, {"Tests", "Include tests?"}}
	}
	answered := map[int]bool{}
	for _, i := range over.answeredIndices {
		answered[i] = true
	}
	totalTabs := over.totalTabs
	if totalTabs == 0 {
		totalTabs = len(questions) + 1
	}
	submitIndex := totalTabs - 1
	tabs := make([]TabBarTab, len(questions))
	for i, q := range questions {
		label := q.header
		if label == "" {
			label = "Q" + string(rune('1'+i))
		}
		tabs[i] = TabBarTab{Label: label, Answered: answered[i], Active: i == over.activeTabIndex}
	}
	return TabBarProps{Tabs: tabs, Submit: TabBarSubmit{
		Active:      over.activeTabIndex == submitIndex,
		AllAnswered: len(answered) == len(questions) && len(questions) > 0,
	}}
}

func makeTabBar(theme Theme, initial TabBarProps) *TabBar {
	bar := NewTabBar(theme)
	bar.SetProps(initial)
	return bar
}

func TestTabBar(t *testing.T) {
	const f = "view/components/tab-bar"
	tw(t, f, "emits exactly 2 lines (tab bar + blank spacer)", func(t *testing.T) {
		lines := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{})).Render(80)
		if len(lines) != 2 || lines[1] != "" {
			t.Fatalf("lines = %q", lines)
		}
	})
	tw(t, f, "renders one indicator per question + a Submit tab", func(t *testing.T) {
		line := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{})).Render(80)[0]
		if n := strings.Count(line, "□"); n != 3 {
			t.Fatalf("empties = %d", n)
		}
		for _, s := range []string{"Submit", "←", "→"} {
			if !strings.Contains(line, s) {
				t.Fatalf("missing %q in %q", s, line)
			}
		}
	})
	tw(t, f, "flips □ → ■ for answered questions", func(t *testing.T) {
		line := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{answeredIndices: []int{1}})).Render(80)[0]
		if strings.Count(line, "■") != 1 || strings.Count(line, "□") != 2 {
			t.Fatalf("got %q", line)
		}
	})
	tw(t, f, "applies selectedBg styling to the active tab via theme.bg", func(t *testing.T) {
		theme := &recordingTheme{}
		makeTabBar(theme, buildTabBarProps(tabBarPropsOver{activeTabIndex: 1})).Render(80)
		if !theme.calledBg("selectedBg", "Priority") {
			t.Fatalf("bg calls = %v", theme.bgCalls)
		}
	})
	tw(t, f, "Submit shows success color when all answered, dim otherwise", func(t *testing.T) {
		theme := &recordingTheme{}
		makeTabBar(theme, buildTabBarProps(tabBarPropsOver{answeredIndices: []int{0, 1, 2}})).Render(80)
		if !theme.calledFg("success", "Submit") {
			t.Fatalf("fg calls = %v", theme.fgCalls)
		}
		theme.fgCalls = nil
		makeTabBar(theme, buildTabBarProps(tabBarPropsOver{})).Render(80)
		if !theme.calledFg("dim", "Submit") {
			t.Fatalf("fg calls = %v", theme.fgCalls)
		}
	})
	tw(t, f, "falls back to Q{n+1} when header is absent", func(t *testing.T) {
		line := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{
			questions: []tabBarQ{{"", "first"}, {"", "second"}}, totalTabs: 3,
		})).Render(80)[0]
		if !strings.Contains(line, "Q1") || !strings.Contains(line, "Q2") {
			t.Fatalf("got %q", line)
		}
	})
	tw(t, f, "truncates rather than overflowing when 4 long headers exceed width", func(t *testing.T) {
		tb := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{
			questions: []tabBarQ{{"VeryLongHeaderOne", ""}, {"VeryLongHeaderTwo", ""}, {"VeryLongHeaderThree", ""}, {"VeryLongHeaderFour", ""}},
			totalTabs: 5,
		}))
		for _, w := range []int{40, 60, 80, 120} {
			if got := widthx.VisibleWidth(tb.Render(w)[0]); got > w {
				t.Fatalf("width %d: visible %d", w, got)
			}
		}
	})
	tw(t, f, "setProps replaces props between renders", func(t *testing.T) {
		tb := makeTabBar(identityTheme{}, buildTabBarProps(tabBarPropsOver{}))
		before := tb.Render(80)[0]
		tb.SetProps(buildTabBarProps(tabBarPropsOver{activeTabIndex: 1, answeredIndices: []int{0}}))
		after := tb.Render(80)[0]
		if before == after || strings.Count(after, "■") != 1 {
			t.Fatalf("before %q after %q", before, after)
		}
	})
}
