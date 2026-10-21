package ui

import "testing"

// Benchmarks of the questionnaire on a representative input: three questions (a single-select with markdown
// previews, a multi-select, a single-select), as the model typically asks. The input path is Dispatch (key router,
// reducer, effects) and the render path is Component().Render, both on the TUI's input and render path.

var benchParams = []QuestionData{
	{Question: "Which library should we use for dates?", Header: "Library", Options: []OptionData{
		{Label: "date-fns", Description: "Modular and tree-shakeable", Preview: "## date-fns\n\n- small\n- `format(new Date(), 'yyyy')`\n\n```ts\nimport { format } from 'date-fns';\n```"},
		{Label: "dayjs", Description: "Moment-compatible, 2 kB", Preview: "## dayjs\n\nA **tiny** library with a [moment](https://momentjs.com) API."},
		{Label: "luxon", Description: "Immutable, timezone aware", Preview: "## luxon\n\nUses `Intl` for time zones."},
	}},
	{Question: "Which checks should run?", Header: "Checks", MultiSelect: true, Options: []OptionData{
		{Label: "lint", Description: "Static analysis"}, {Label: "unit", Description: "Unit tests"}, {Label: "e2e", Description: "End to end tests"},
	}},
	{Question: "Merge strategy?", Header: "Merge", Options: []OptionData{
		{Label: "squash", Description: "One commit"}, {Label: "rebase", Description: "Linear history"},
	}},
}

// benchFlow is the keys of a whole answer: pick the second library, tick two checks, pick the first merge option,
// then confirm on the submit tab.
var benchFlow = []string{sDOWN, sENTER, " ", sDOWN, " ", sTAB, sENTER, sENTER}

func BenchmarkQuestionnaireRender(b *testing.B) {
	h := makeSession(sessionOpts{params: benchParams})
	c := h.session.Component()
	c.Render(120)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Invalidate()
		c.Render(120)
	}
}

func BenchmarkQuestionnaireKeystroke(b *testing.B) {
	h := makeSession(sessionOpts{params: benchParams})
	c := h.session.Component()
	c.Render(120)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.session.Dispatch(sDOWN)
		c.Render(120)
		h.session.Dispatch(sUP)
		c.Render(120)
	}
}

func BenchmarkQuestionnaireFullFlow(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h := makeSession(sessionOpts{params: benchParams})
		c := h.session.Component()
		c.Render(120)
		for _, k := range benchFlow {
			h.session.Dispatch(k)
			c.Render(120)
		}
		if len(h.results) == 0 {
			b.Fatal("the flow did not finish")
		}
	}
}
