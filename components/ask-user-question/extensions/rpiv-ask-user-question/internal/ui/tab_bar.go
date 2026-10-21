package ui

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// TabBarTab is one author-defined question's tab.
type TabBarTab struct {
	Label    string
	Answered bool
	Active   bool
}

// TabBarSubmit is the submit-tab state; AllAnswered drives the success/dim color picker.
type TabBarSubmit struct {
	Active      bool
	AllAnswered bool
}

// TabBarProps is the per-tick projection of TabBar state. The selector hoists every render-time derivation into
// props so Render is pure styling. upstream: view/components/tab-bar.ts.
type TabBarProps struct {
	// Tabs is one per author-defined question, in order.
	Tabs   []TabBarTab
	Submit TabBarSubmit
}

type TabBar struct {
	theme Theme
	props TabBarProps
}

func NewTabBar(theme Theme) *TabBar { return &TabBar{theme: theme} }

func (b *TabBar) SetProps(props TabBarProps) { b.props = props }

func (b *TabBar) HandleInput(string) {}

func (b *TabBar) Invalidate() {}

func (b *TabBar) Render(width int) []string {
	var pieces strings.Builder
	pieces.WriteString(" ← ")

	for _, tab := range b.props.Tabs {
		box := "□"
		if tab.Answered {
			box = "■"
		}
		rawSeg := " " + box + " " + tab.Label + " "
		var styled string
		if tab.Active {
			styled = b.theme.Bg("selectedBg", b.theme.Fg("text", rawSeg))
		} else {
			color := "muted"
			if tab.Answered {
				color = "success"
			}
			styled = b.theme.Fg(color, rawSeg)
		}
		pieces.WriteString(styled)
		pieces.WriteString(" ")
	}

	const submitText = " ✓ Submit "
	if b.props.Submit.Active {
		pieces.WriteString(b.theme.Bg("selectedBg", b.theme.Fg("text", submitText)))
	} else {
		color := "dim"
		if b.props.Submit.AllAnswered {
			color = "success"
		}
		pieces.WriteString(b.theme.Fg(color, submitText))
	}
	pieces.WriteString(" →")

	return []string{widthx.TruncateToWidth(pieces.String(), width, "", false), ""}
}
