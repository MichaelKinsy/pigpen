package ui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

func dcRender(over dialogOver, width int) []string {
	return makeDialog(makeDialogConfig(containerDefaults, over)).Render(width)
}

func dcJoin(over dialogOver, width int) string { return joinLines(dcRender(over, width)) }

func dcContains(t *testing.T, joined, sub string) {
	t.Helper()
	if !strings.Contains(joined, sub) {
		t.Fatalf("output does not contain %q:\n%s", sub, joined)
	}
}

func dcNotContains(t *testing.T, joined, sub string) {
	t.Helper()
	if strings.Contains(joined, sub) {
		t.Fatalf("output contains %q:\n%s", sub, joined)
	}
}

func dcState(mod func(*DialogState)) *DialogState {
	s := vwState()
	if mod != nil {
		mod(s)
	}
	return s
}

func dcMultiQ(header string) QuestionData {
	q := dlgQuestion("areas?", header, [2]string{"FE", "FE"}, [2]string{"BE", "BE"})
	q.MultiSelect = true
	return q
}

func dcOtherQ() QuestionData {
	return dlgQuestion("second?", "S", [2]string{"x", "x"}, [2]string{"y", "y"})
}

func TestDialogContainer(t *testing.T) {
	const f = "view/dialog-container"
	onlyQ := func(header string) []QuestionData {
		return []QuestionData{dlgQuestion("only?", header, [2]string{"yes", "y"}, [2]string{"no", "n"})}
	}

	tw(t, f, "omits the TabBar entirely", func(t *testing.T) {
		joined := dcJoin(dialogOver{
			questions: onlyQ("Only"), isMulti: boolp(false),
			tabBar: &stubComponent{[]string{"<TABBAR>", ""}},
		}, 80)
		dcNotContains(t, joined, "<TABBAR>")
		dcContains(t, joined, "<PREVIEW>")
		dcContains(t, joined, HintSingle)
	})
	tw(t, f, "renders the inner header badge in the dialog body (no tab bar to show it)", func(t *testing.T) {
		dcContains(t, dcJoin(dialogOver{questions: onlyQ("H-only"), isMulti: boolp(false)}, 80), " H-only ")
	})

	tw(t, f, "includes TabBar + PreviewPane + multi hint", func(t *testing.T) {
		joined := dcJoin(dialogOver{}, 80)
		dcContains(t, joined, "<TABBAR>")
		dcContains(t, joined, "<PREVIEW>")
		dcContains(t, joined, HintPartNotes)
	})
	tw(t, f, "does NOT render the inner header badge inside the dialog body in multi-question mode", func(t *testing.T) {
		for _, l := range dcRender(dialogOver{}, 80) {
			if strings.Contains(l, " H1 ") && !strings.Contains(l, "<TABBAR>") {
				t.Fatalf("inner header badge in %q", l)
			}
		}
	})
	tw(t, f, "appends 'Space toggle' suffix when current question is multiSelect", func(t *testing.T) {
		multiQ := dcMultiQ("Areas")
		initial := dcState(nil)
		mso := NewMultiSelectView(identityTheme{}, multiQ)
		mso.SetProps(vwMultiSelectPropsFromState(multiQ, initial, true))
		joined := dcJoin(dialogOver{
			questions: []QuestionData{multiQ, dcOtherQ()}, state: initial,
			multiSelectByTab: []TabMultiSelect{mso, nil},
			getBodyHeight:    func(int) int { return 4 },
		}, 120)
		dcContains(t, joined, HintPartToggle)
	})
	tw(t, f, "renders 'n to add notes' in the resting hint (universal — no preview required)", func(t *testing.T) {
		answer := "A"
		st := dcState(func(s *DialogState) {
			s.Answers = map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Q1?", Kind: "option", Answer: &answer}}
		})
		dcContains(t, dcJoin(dialogOver{state: st}, 80), HintPartNotes)
	})
	tw(t, f, "footer hint names the configured collapseKey instead of the default (#176)", func(t *testing.T) {
		joined := dcJoin(dialogOver{collapseKey: "alt+o"}, 160)
		dcContains(t, joined, "Alt+O to collapse")
		dcNotContains(t, joined, "Ctrl+]")
	})
	tw(t, f, "footer hint drops the collapse part when collapseKey is 'off' (#176)", func(t *testing.T) {
		joined := dcJoin(dialogOver{collapseKey: "off"}, 160)
		dcNotContains(t, joined, "to collapse")
		dcContains(t, joined, HintPartEnter)
		dcContains(t, joined, "Esc to cancel")
	})
	tw(t, f, "shows multiline controls at the right and drops notes while inputMode captures text", func(t *testing.T) {
		joined := dcJoin(dialogOver{state: dcState(func(s *DialogState) { s.InputMode = true })}, 160)
		dcContains(t, joined, HintPartEnter)
		dcContains(t, joined, HintPartNewLine)
		dcContains(t, joined, HintPartClear)
		if !(strings.Index(joined, HintPartNewLine) > strings.Index(joined, HintPartCollapse)) {
			t.Fatal("newline hint not after collapse hint")
		}
		if !(strings.Index(joined, HintPartClear) > strings.Index(joined, HintPartNewLine)) {
			t.Fatal("clear hint not after newline hint")
		}
		dcNotContains(t, joined, "Ctrl+G to edit")
		dcNotContains(t, joined, HintPartNotes)
	})
	tw(t, f, "notesVisible adds the notes editor below the preview (line count grows)", func(t *testing.T) {
		hidden := dcRender(dialogOver{}, 160)
		visible := dcRender(dialogOver{state: dcState(func(s *DialogState) { s.NotesVisible = true })}, 160)
		if len(visible) <= len(hidden) {
			t.Fatalf("visible %d hidden %d", len(visible), len(hidden))
		}
		dcContains(t, joinLines(visible), HintPartNewLine)
		dcContains(t, joinLines(visible), "<NOTES_INPUT>")
		dcNotContains(t, joinLines(hidden), "<NOTES_INPUT>")
	})
	tw(t, f, "renders multiSelect checkboxes inline ([✔] / [ ]) in place of PreviewPane", func(t *testing.T) {
		multiQ := dcMultiQ("Areas")
		st := dcState(func(s *DialogState) { s.OptionIndex = 1; s.MultiSelectChecked = map[int]bool{0: true} })
		mso := NewMultiSelectView(identityTheme{}, multiQ)
		mso.SetProps(vwMultiSelectPropsFromState(multiQ, st, true))
		joined := dcJoin(dialogOver{
			questions: []QuestionData{multiQ, dlgQuestion("q?", "Q", [2]string{"a", "a"}, [2]string{"b", "b"})},
			state:     st, multiSelectByTab: []TabMultiSelect{mso, nil},
			getBodyHeight: func(int) int { return 4 },
		}, 80)
		dcContains(t, joined, "[✔]")
		dcContains(t, joined, "[ ]")
		dcNotContains(t, joined, "<PREVIEW>")
	})

	// Submit tab.
	ansA, ansNil := "A", (*string)(nil)
	fullAnswers := func() map[int]QuestionAnswer {
		return map[int]QuestionAnswer{
			0: {QuestionIndex: 0, Question: "Q1?", Kind: "option", Answer: &ansA},
			1: {QuestionIndex: 1, Question: "Q2?", Kind: "multi", Answer: ansNil, Selected: []string{"X", "Y"}, HasSelected: true},
		}
	}
	partialAnswers := func() map[int]QuestionAnswer {
		return map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "Q1?", Kind: "option", Answer: &ansA}}
	}
	submitState := func(mod func(*DialogState)) *DialogState {
		s := vwState()
		s.CurrentTab = 2
		s.Answers = fullAnswers()
		if mod != nil {
			mod(s)
		}
		return s
	}
	makePicker2 := func(state *DialogState, focused bool) *SubmitPicker {
		p := NewSubmitPicker(identityTheme{})
		p.SetProps(vwSubmitPickerPropsFromState(state, focused))
		return p
	}
	submitOver := func(state *DialogState, focused bool) dialogOver {
		return dialogOver{state: state, submitPicker: makePicker2(state, focused), getBodyHeight: func(int) int { return 6 }}
	}
	lineWith := func(joined, sub string) string {
		for _, l := range strings.Split(joined, "\n") {
			if strings.Contains(l, sub) {
				return l
			}
		}
		return ""
	}

	tw(t, f, "renders REVIEW_HEADING always", func(t *testing.T) {
		dcContains(t, dcJoin(submitOver(submitState(nil), true), 80), ReviewHeading)
	})
	tw(t, f, "renders bullet+arrow summary for answered questions", func(t *testing.T) {
		joined := dcJoin(submitOver(submitState(nil), true), 80)
		for _, s := range []string{"● H1", "→", "A", "● H2", "X, Y"} {
			dcContains(t, joined, s)
		}
	})
	tw(t, f, "omits unanswered rows from summary (no ✖)", func(t *testing.T) {
		st := submitState(func(s *DialogState) { s.Answers = partialAnswers() })
		joined := dcJoin(submitOver(st, false), 80)
		dcNotContains(t, joined, "✖")
		dcNotContains(t, joined, "unanswered")
		dcContains(t, joined, "● H1")
		dcNotContains(t, joined, "● H2")
	})
	tw(t, f, "shows READY_PROMPT when complete", func(t *testing.T) {
		dcContains(t, dcJoin(submitOver(submitState(nil), true), 80), ReadyPrompt)
	})
	tw(t, f, "shows INCOMPLETE_WARNING_PREFIX + missing labels when incomplete", func(t *testing.T) {
		st := submitState(func(s *DialogState) { s.Answers = partialAnswers() })
		joined := dcJoin(submitOver(st, false), 80)
		dcContains(t, joined, IncompleteWarningPrefix)
		dcContains(t, joined, "H2")
		dcNotContains(t, joined, ReadyPrompt)
	})
	tw(t, f, "renders SubmitPicker rows (1. Submit answers / 2. Cancel)", func(t *testing.T) {
		joined := dcJoin(submitOver(submitState(nil), true), 80)
		dcContains(t, joined, SubmitLabel)
		dcContains(t, joined, CancelLabel)
	})
	tw(t, f, "Submit row renders normal regardless of completeness (D1 revised)", func(t *testing.T) {
		incomplete := submitState(func(s *DialogState) { s.Answers = partialAnswers() })
		joined := dcJoin(submitOver(incomplete, true), 80)
		if strings.Contains(strings.ToLower(lineWith(joined, SubmitLabel)), "<dim>") {
			t.Fatal("submit line is dim")
		}
	})
	tw(t, f, "active pointer follows state.submitChoiceIndex", func(t *testing.T) {
		row0 := submitState(func(s *DialogState) { s.SubmitChoiceIndex = 0 })
		j0 := dcJoin(submitOver(row0, true), 80)
		if !strings.Contains(lineWith(j0, SubmitLabel), "❯") || strings.Contains(lineWith(j0, CancelLabel), "❯") {
			t.Fatalf("row0:\n%s", j0)
		}
		row1 := submitState(func(s *DialogState) { s.SubmitChoiceIndex = 1 })
		j1 := dcJoin(submitOver(row1, true), 80)
		if strings.Contains(lineWith(j1, SubmitLabel), "❯") || !strings.Contains(lineWith(j1, CancelLabel), "❯") {
			t.Fatalf("row1:\n%s", j1)
		}
	})
	tw(t, f, "does NOT render the chat row or HINT_MULTI on Submit Tab (regression)", func(t *testing.T) {
		joined := dcJoin(submitOver(submitState(nil), true), 80)
		dcNotContains(t, joined, "<CHAT_ROW>")
		dcNotContains(t, joined, HintMulti)
	})
	// it.each upstream: the ledger does not list parametrized titles, so this is a plain subtest, not a tw twin.
	t.Run("submit + question tab heights stay equal across fixtures: %s", func(t *testing.T) {
		short := []QuestionData{
			dlgQuestion("Q1?", "1", [2]string{"A", "a"}, [2]string{"B", "b"}),
			dlgQuestion("Q2?", "2", [2]string{"X", "x"}, [2]string{"Y", "y"}),
		}
		mixed := []QuestionData{short[0], dlgQuestion("Q2?", "H2", [2]string{"X", "x"}, [2]string{"Y", "y"})}
		for name, qs := range map[string][]QuestionData{
			"both with headers": nil, "both with short single-char headers": short,
			"mixed: tab 0 short header, tab 1 longer header": mixed,
		} {
			submitS := submitState(nil)
			o := submitOver(submitS, true)
			o.questions = qs
			submitDlg := dcRender(o, 120)
			qs0 := submitState(func(s *DialogState) { s.CurrentTab = 0 })
			questionDlg := dcRender(dialogOver{questions: qs, state: qs0, getBodyHeight: func(int) int { return 6 }}, 120)
			if len(submitDlg) != len(questionDlg) {
				t.Fatalf("%s: submit %d question %d", name, len(submitDlg), len(questionDlg))
			}
		}
	})
	tw(t, f, "total dialog height equals a question tab's height (no collapse / no jump)", func(t *testing.T) {
		submit := dcRender(submitOver(submitState(nil), true), 120)
		qs0 := submitState(func(s *DialogState) { s.CurrentTab = 0 })
		questionTab := dcRender(dialogOver{state: qs0, getBodyHeight: func(int) int { return 6 }}, 120)
		if len(submit) != len(questionTab) {
			t.Fatalf("submit %d question %d", len(submit), len(questionTab))
		}
	})

	tw(t, f, "setProps replaces the rendered pane on subsequent render() calls", func(t *testing.T) {
		paneA := newStubPreviewPane([]string{"<PANE_A>"}, nil)
		paneB := newStubPreviewPane([]string{"<PANE_B>"}, nil)
		parts := makeDialogConfig(containerDefaults, dialogOver{previewPane: paneA})
		dlg := makeDialog(parts)
		dcContains(t, joinLines(dlg.Render(80)), "<PANE_A>")
		dlg.SetProps(DialogProps{State: parts.initialProps.State, ActivePreviewPane: paneB})
		dcContains(t, joinLines(dlg.Render(80)), "<PANE_B>")
		dcNotContains(t, joinLines(dlg.Render(80)), "<PANE_A>")
	})

	tw(t, f, "every emitted line satisfies visibleWidth(line) <= width across all modes", func(t *testing.T) {
		for _, w := range []int{60, 80, 120} {
			for _, ct := range []int{0, 1, 2} {
				st := dcState(func(s *DialogState) {
					s.CurrentTab = ct
					s.NotesVisible = ct == 0
					s.Answers = map[int]QuestionAnswer{0: {QuestionIndex: 0, Question: "q", Kind: "option", Answer: &ansA}}
				})
				for _, line := range dcRender(dialogOver{state: st}, w) {
					if widthx.VisibleWidth(line) > w {
						t.Fatalf("tab %d width %d: %q", ct, w, line)
					}
				}
			}
		}
	})

	tw(t, f, "dialog total grows by (getBodyHeight delta) when getCurrentBodyHeight stays constant", func(t *testing.T) {
		tall := func() int { return 200 }
		one := func(int) int { return 1 }
		a := dcRender(dialogOver{getTerminalRows: tall, getBodyHeight: func(int) int { return 5 }, getCurrentBodyHeight: one}, 80)
		b := dcRender(dialogOver{getTerminalRows: tall, getBodyHeight: func(int) int { return 20 }, getCurrentBodyHeight: one}, 80)
		if len(b)-len(a) != 15 {
			t.Fatalf("delta %d", len(b)-len(a))
		}
	})
	tw(t, f, "residual rows live AFTER the controls hint (very bottom of the dialog)", func(t *testing.T) {
		lines := dcRender(dialogOver{getBodyHeight: func(int) int { return 6 }, getCurrentBodyHeight: func(int) int { return 1 }}, 80)
		hintIdx := -1
		for i, l := range lines {
			if strings.Contains(l, HintPartEnter) {
				hintIdx = i
				break
			}
		}
		if hintIdx <= 0 {
			t.Fatalf("hintIdx %d", hintIdx)
		}
		tail := lines[hintIdx+1:]
		if len(tail) != 8 {
			t.Fatalf("tail %d", len(tail))
		}
		for _, l := range tail {
			if strings.TrimSpace(l) != "" {
				t.Fatalf("non-blank tail %q", l)
			}
		}
	})
	tw(t, f, "dialog total line count is identical across tab switches with mixed single/multi fixture", func(t *testing.T) {
		multiQ := dlgQuestion("areas?", "H2",
			[2]string{"FE", "FE"}, [2]string{"BE", "BE"}, [2]string{"DB", "DB"}, [2]string{"QA", "QA"}, [2]string{"Ops", "Ops"})
		multiQ.MultiSelect = true
		singleQ := dlgQuestion("Q1", "H1", [2]string{"A", "a"}, [2]string{"B", "b"})
		questions := []QuestionData{singleQ, multiQ}
		stateTab0 := dcState(nil)
		stateTab1 := dcState(func(s *DialogState) { s.CurrentTab = 1 })
		mso := NewMultiSelectView(identityTheme{}, multiQ)
		mso.SetProps(vwMultiSelectPropsFromState(multiQ, stateTab0, true))
		byTab := []TabMultiSelect{nil, mso}
		getBodyHeight := func(w int) int { return max(1, len(mso.Render(w))) }
		rows32 := func() int { return 32 }
		n0 := len(dcRender(dialogOver{questions: questions, state: stateTab0, multiSelectByTab: byTab, getBodyHeight: getBodyHeight, getTerminalRows: rows32}, 120))
		n1 := len(dcRender(dialogOver{questions: questions, state: stateTab1, multiSelectByTab: byTab, getBodyHeight: getBodyHeight, getTerminalRows: rows32}, 120))
		if n0 != n1 {
			t.Fatalf("tab0 %d tab1 %d", n0, n1)
		}
	})
}
