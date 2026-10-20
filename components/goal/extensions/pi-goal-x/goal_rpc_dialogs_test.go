package pi_goal_x

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var rpcQuestion = goalQuestionnaireQuestion{id: "scope", question: "Scope?", context: "All the details", options: []string{"A", "B"}, recommended: questionnaireInt(1)}

func withOptions(q goalQuestionnaireQuestion, opts []string) goalQuestionnaireQuestion {
	q.options = opts
	return q
}

func capableTUI() *mockDialogTUI {
	rows, cols := 40, 92
	_ = cols
	return &mockDialogTUI{shown: true, rows: &rows}
}

// mountRichDialog: custom() builds the component with a capable fake TUI and waits for done (goal-rpc-dialogs.test.ts).
type richMount struct {
	done  chan struct{}
	value any
	comp  chan goalDialogComponent
}

func mountRichDialog(h *scriptedHost, tuiArg *mockDialogTUI) *richMount {
	m := &richMount{done: make(chan struct{}), comp: make(chan goalDialogComponent, 1)}
	h.ctx.u.customFn = func(factory goalCustomFactory, _ goalCustomOptions) (any, error) {
		c := factory(tuiArg, rpcTheme{}, func(v any) {
			select {
			case <-m.done:
			default:
				m.value = v
				close(m.done)
			}
		})
		m.comp <- c
		<-m.done
		return m.value, nil
	}
	return m
}

type rpcTheme struct{}

func (rpcTheme) fg(_, text string) string { return text }
func (rpcTheme) bold(text string) string  { return text }
func (rpcTheme) bg(_, _ string) string    { return "" }

func isDone(m *richMount) bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

func asyncRun(h *scriptedHost, qs []goalQuestionnaireQuestion) chan goalQuestionnaireResult {
	out := make(chan goalQuestionnaireResult, 1)
	go func() {
		r, _ := runGoalQuestionnaire(h.ctx, qs, nil)
		out <- r
	}()
	return out
}

func TestGoalRpcDialogs(t *testing.T) {
	const f = "goal-rpc-dialogs"
	one := []goalQuestionnaireQuestion{rpcQuestion}

	tw(t, f, "rpc hosts that DO render TUI components get the rich dialog (no per-question dialogs)", func(t *testing.T) {
		h := newScriptedHost([]int{9})
		sawRichRender := false
		h.ctx.u.customFn = func(factory goalCustomFactory, _ goalCustomOptions) (any, error) {
			c := factory(capableTUI(), rpcTheme{}, func(any) {})
			sawRichRender = len(c.Render(92)) > 0
			return &goalQuestionnaireResult{
				questions:      []goalQuestionnaireQuestion{{id: rpcQuestion.id, question: rpcQuestion.question, options: rpcQuestion.options}},
				answers:        []goalQuestionnaireAnswer{{id: rpcQuestion.id, question: rpcQuestion.question, answer: "B"}},
				auditorEnabled: questionnaireBool(true),
			}, nil
		}
		result := mustRun(t, h.ctx, one, nil)
		eqDeep(t, sawRichRender, true, "the rich dialog component must render lines")
		eqDeep(t, len(h.dialogs()), 0, "no per-question dialog may be used when the rich dialog renders")
		eqDeep(t, result.answers[0].answer, "B", "answer")
	})

	tw(t, f, "an rpc host that advertises custom but throws degrades instead of failing the draft", func(t *testing.T) {
		h := newScriptedHost([]int{1})
		h.ctx.u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) {
			return nil, errors.New("host cannot render TUI dialogs")
		}
		result := mustRun(t, h.ctx, one, nil)
		eqDeep(t, result.answers[0].answer, "B", "rpc hosts keep their safety net")
		eqDeep(t, len(h.dialogs()), 1, "one dialog")
		mustMatch(t, `host cannot render TUI dialogs`, strings.Join(h.ctx.u.notices, "|"), "reason is announced")
	})

	tw(t, f, "the rich dialog commits the recommended answer on Enter (forwarded keystrokes)", func(t *testing.T) {
		h := newScriptedHost([]int{9})
		m := mountRichDialog(h, capableTUI())
		res := asyncRun(h, one)
		comp := <-m.comp
		comp.SetFocused(true)
		for i := 0; i < 4 && !isDone(m); i++ {
			comp.HandleInput("\r")
		}
		result := <-res
		eqDeep(t, result.cancelled, false, "not cancelled")
		eqDeep(t, result.answers[0].answer, "B", "Enter confirms the recommended option")
		eqDeep(t, len(h.dialogs()), 0, "no per-question dialog may be used")
	})

	tw(t, f, "Escape on the rich dialog cancels with a defined result (never falls back)", func(t *testing.T) {
		h := newScriptedHost([]int{9})
		m := mountRichDialog(h, capableTUI())
		res := asyncRun(h, one)
		comp := <-m.comp
		comp.SetFocused(true)
		comp.HandleInput("\x1b")
		select {
		case <-m.done:
		case <-time.After(2 * time.Second):
			t.Fatal("Escape must produce a defined result")
		}
		if m.value == nil {
			t.Fatal("Escape must produce a defined result, not the factory bail-out")
		}
		result := <-res
		eqDeep(t, result.cancelled, true, "cancelled")
		eqDeep(t, len(h.dialogs()), 0, "cancelling must not resurrect per-question dialogs")
	})

	tw(t, f, "a synchronously throwing custom is treated like a rejection (rpc degrades, non-rpc surfaces)", func(t *testing.T) {
		rpcHost := newScriptedHost([]int{1})
		rpcHost.ctx.u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) {
			return nil, errors.New("host cannot render TUI dialogs")
		}
		eqDeep(t, mustRun(t, rpcHost.ctx, one, nil).answers[0].answer, "B", "rpc degrades")

		synHost := newScriptedHost([]int{1})
		synHost.ctx.modeStr = ""
		synHost.ctx.u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) { return nil, errors.New("Host disconnected") }
		_, err := runGoalQuestionnaire(synHost.ctx, one, nil)
		if err == nil || !strings.Contains(err.Error(), "Host disconnected") {
			t.Errorf("non-rpc surfaces: %v", err)
		}
	})

	tw(t, f, "the proposal confirmation shares the rpc safety net", func(t *testing.T) {
		h := newScriptedHost([]int{0, 0})
		h.ctx.u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) {
			return nil, errors.New("host cannot render TUI dialogs")
		}
		result, err := showProposalDialog(h.ctx, "OBJECTIVE", "goal", questionnaireBool(true))
		if err != nil {
			t.Fatal(err)
		}
		eqDeep(t, result.decision, "confirm", "proposal confirm falls back to the per-question dialogs")
		eqDeep(t, len(h.dialogs()), 2, "auditor toggle + confirmation")
	})

	tw(t, f, "a non-rpc host that throws keeps upstream semantics (the error surfaces)", func(t *testing.T) {
		h := newScriptedHost([]int{1})
		h.ctx.modeStr = ""
		h.ctx.u.customFn = func(goalCustomFactory, goalCustomOptions) (any, error) { return nil, errors.New("Host disconnected") }
		_, err := runGoalQuestionnaire(h.ctx, one, nil)
		if err == nil || !strings.Contains(err.Error(), "Host disconnected") {
			t.Errorf("error surfaces: %v", err)
		}
		eqDeep(t, len(h.dialogs()), 0, "no dialogs")
	})

	tw(t, f, "a TUI-less host still bails out through the factory capability check", func(t *testing.T) {
		h := newScriptedHost([]int{1})
		h.ctx.u.customFn = func(factory goalCustomFactory, _ goalCustomOptions) (any, error) {
			c := factory(nil, plainTestTheme{}, func(any) {})
			eqDeep(t, c.Render(80), []string{}, "empty render")
			return nil, nil
		}
		result := mustRun(t, h.ctx, one, nil)
		eqDeep(t, result.answers[0].answer, "B", "answer")
		eqDeep(t, len(h.dialogs()), 1, "one dialog")
	})

	tw(t, f, "per-question dialogs still preserve recommendation labels when custom is absent", func(t *testing.T) {
		h := newScriptedHost([]int{1})
		h.ctx.u.customFn = nil
		result := mustRun(t, h.ctx, one, nil)
		eqDeep(t, result.answers[0].answer, "B", "answer")
		eqDeep(t, h.dialogs()[0], dialogRecord{"Scope?\n\nAll the details", []string{"1. A", "2. B (Recommended)", "3. Write your own answer..."}}, "dialog")
	})

	tw(t, f, "unknown-mode web factory arguments fall back without throwing", func(t *testing.T) {
		h := newScriptedHost([]int{0})
		h.ctx.modeStr = ""
		h.ctx.u.customFn = func(factory goalCustomFactory, _ goalCustomOptions) (any, error) {
			var got any
			c := factory(nil, plainTestTheme{}, func(v any) { got = v })
			eqDeep(t, c.Render(80), []string{}, "empty render")
			return got, nil
		}
		eqDeep(t, mustRun(t, h.ctx, one, nil).answers[0].answer, "A", "answer")
	})

	tw(t, f, "missing custom falls back and only requires primitives the questions use", func(t *testing.T) {
		h := newScriptedHost([]int{0})
		h.ctx.modeStr = ""
		h.ctx.u.customFn = nil
		h.ctx.u.inputFn = nil
		no := false
		q := rpcQuestion
		q.allowCustom = &no
		eqDeep(t, mustRun(t, h.ctx, []goalQuestionnaireQuestion{q}, nil).cancelled, false, "select-only is enough")
		eqDeep(t, mustRun(t, h.ctx, one, nil).unavailable, true, "custom answers need input")
	})

	tw(t, f, "free text is direct, trims answers, and reprompts whitespace", func(t *testing.T) {
		h := newScriptedHost(nil, strp("  "), strp("  my answer  "))
		h.ctx.u.selectFn = nil
		result := mustRun(t, h.ctx, []goalQuestionnaireQuestion{withOptions(rpcQuestion, []string{})}, nil)
		eqDeep(t, result.answers[0].answer, "my answer", "trimmed")
		eqDeep(t, result.answers[0].wasCustom, true, "custom")
		eqDeep(t, len(h.dialogs()), 2, "reprompted")
		for _, d := range h.dialogs() {
			if d.options != nil || !strings.Contains(d.title, rpcQuestion.context) {
				t.Errorf("dialog %#v", d)
			}
		}
	})

	tw(t, f, "duplicate and reserved labels cannot become a custom answer accidentally", func(t *testing.T) {
		q := withOptions(rpcQuestion, []string{"Write your own answer...", "Same", "Same"})
		selected := mustRun(t, newScriptedHost([]int{0}).ctx, []goalQuestionnaireQuestion{q}, nil)
		eqDeep(t, selected.answers[0].wasCustom, false, "not custom")
		eqDeep(t, selected.answers[0].answer, q.options[0], "answer")
		custom := newScriptedHost([]int{3}, strp("new answer"))
		eqDeep(t, mustRun(t, custom.ctx, []goalQuestionnaireQuestion{q}, nil).answers[0].wasCustom, true, "custom")
		mustMatch(t, `All the details`, custom.dialogs()[1].title, "input shows the context")
	})

	tw(t, f, "cancellation on a later question or custom input discards every answer", func(t *testing.T) {
		second := rpcQuestion
		second.id = "second"
		for _, h := range []*scriptedHost{newScriptedHost([]int{0, dialogNone}), newScriptedHost([]int{0, 2}, nil)} {
			result := mustRun(t, h.ctx, []goalQuestionnaireQuestion{rpcQuestion, second}, nil)
			eqDeep(t, result.cancelled, true, "cancelled")
			eqDeep(t, result.answers, []goalQuestionnaireAnswer{}, "answers discarded")
		}
	})

	tw(t, f, "RPC proposal changes auditor ${enabled} and handles decision ${action}", func(t *testing.T) {
		for _, enabled := range []bool{true, false} {
			for _, action := range []int{0, 1, 2, dialogNone} {
				t.Run(fmt.Sprintf("auditor %v decision %d", enabled, action), func(t *testing.T) {
					h := newScriptedHost([]int{1, action})
					result, err := showProposalDialog(h.ctx, "OBJECTIVE\nTasks: one, two\nContract: tests pass", "goal", questionnaireBool(enabled))
					if err != nil {
						t.Fatal(err)
					}
					eqDeep(t, result.auditorEnabled, !enabled, "auditor")
					want := "continue"
					if action == 0 {
						want = "confirm"
					} else if action == 2 {
						want = "cancel"
					}
					eqDeep(t, result.decision, want, "decision")
					eqDeep(t, result.unavailable, false, "unavailable")
					cur := "disabled"
					next := "enabled"
					if enabled {
						cur, next = "enabled", "disabled"
					}
					mustMatch(t, "currently "+cur, h.dialogs()[0].title, "toggle prompt")
					mustMatch(t, `(?s)OBJECTIVE.*Tasks: one, two.*Contract: tests pass`, h.dialogs()[1].title, "proposal prompt")
					mustMatch(t, "Auditor for this goal: "+next, h.dialogs()[1].title, "auditor line")
				})
			}
		}
	})

	tw(t, f, "auditor cancellation never presents or confirms the proposal", func(t *testing.T) {
		h := newScriptedHost([]int{dialogNone})
		r, _ := showProposalDialog(h.ctx, "objective", "goal", questionnaireBool(false))
		eqDeep(t, r.decision, "continue", "decision")
		eqDeep(t, len(h.dialogs()), 1, "only the auditor dialog")
	})

	tw(t, f, "unknown host selections and failed primitives cannot confirm proposals", func(t *testing.T) {
		h := newScriptedHost(nil)
		h.ctx.u.selectFn = func(string, []string) (string, bool, error) { return "Confirm a made-up choice", true, nil }
		_, err := showProposalDialog(h.ctx, "objective", "goal", questionnaireBool(true))
		if err == nil || !strings.Contains(err.Error(), "unknown auditor choice") {
			t.Errorf("unknown auditor choice: %v", err)
		}
		h.ctx.u.selectFn = func(string, []string) (string, bool, error) { return "", false, errors.New("Host disconnected") }
		_, err = runGoalQuestionnaire(h.ctx, one, nil)
		if err == nil || !strings.Contains(err.Error(), "Host disconnected") {
			t.Errorf("failed primitive: %v", err)
		}
	})
}
