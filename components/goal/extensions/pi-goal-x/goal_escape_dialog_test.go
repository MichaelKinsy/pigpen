package pi_goal_x

import (
	"strings"
	"testing"
)

// Twins of tests/goal-escape-dialog.test.ts.

type headlessCtx struct{}

func (headlessCtx) hasUI() bool      { return false }
func (headlessCtx) cwd() string      { return "/test" }
func (headlessCtx) mode() string     { return "print" }
func (headlessCtx) ui() goalDialogUI { panic("headless context has no ui") }

// escapeFixture starts the dialog (it blocks on user input) and returns the context holding the captured factory.
func escapeFixture(t *testing.T, objective string) *mockExtensionContext {
	ctx := createMockExtensionContext(t)
	ctx.startCustom(func() { showEscapeDialog(ctx, objective) })
	return ctx
}

func escapeComponent(t *testing.T, ctx *mockExtensionContext, tui goalDialogTUI, done func(any)) goalDialogComponent {
	t.Helper()
	return ctx.customCalls()[0].factory(tui, createMockTheme(), done)
}

func TestGoalEscapeDialog(t *testing.T) {
	f := "goal-escape-dialog"
	tw(t, f, "showEscapeDialog returns continue_working in headless context", func(t *testing.T) {
		eq(t, showEscapeDialog(headlessCtx{}, "Test objective"), "continue_working", "result")
	})
	tw(t, f, "showEscapeDialog returns continue_working for empty objective", func(t *testing.T) {
		eq(t, showEscapeDialog(headlessCtx{}, ""), "continue_working", "result")
	})
	tw(t, f, "showEscapeDialog returns continue_working for long objective", func(t *testing.T) {
		eq(t, showEscapeDialog(headlessCtx{}, strings.Repeat("A", 500)), "continue_working", "result")
	})
	tw(t, f, "showEscapeDialog does not crash when called with hasUI=true (regression: innerWidth ReferenceError)", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		if len(ctx.customCalls()) < 1 {
			t.Fatal("custom() was called (dialog invoked)")
		}
		tui, _ := createMockTUI()
		component := escapeComponent(t, ctx, tui, func(any) {})
		for _, w := range []int{40, 80, 64} {
			if len(component.Render(w)) <= 2 {
				t.Errorf("Renders at width %d", w)
			}
		}
	})
	tw(t, f, "escape dialog component renders correct structure and content", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, _ := createMockTUI()
		component := escapeComponent(t, ctx, tui, func(any) {})
		lines := component.Render(80)
		text := strings.Join(lines, "\n")
		if !strings.Contains(lines[0], "┌") {
			t.Error("Top border present")
		}
		if !strings.Contains(lines[len(lines)-1], "└") {
			t.Error("Bottom border present")
		}
		for _, want := range []string{"Audit interrupted by Escape", "Mark complete without audit", "Continue working", "Test objective", "Enter to select", "↑↓", "Esc"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q", want)
			}
		}
	})
	tw(t, f, "escape dialog handleInput: escape returns continue_working", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, state := createMockTUI()
		var doneValue any
		component := escapeComponent(t, ctx, tui, func(r any) { doneValue = r })
		input := component

		before := state.requestRenderCalls
		input.HandleInput("\x1b[A")
		if state.requestRenderCalls <= before {
			t.Error("Up triggers re-render")
		}
		before = state.requestRenderCalls
		input.HandleInput("\x1b[B")
		if state.requestRenderCalls <= before {
			t.Error("Down triggers re-render")
		}
		input.HandleInput("\x1b")
		eq(t, doneValue, any("continue_working"), "Escape returns continue_working")
	})
	tw(t, f, "escape dialog handleInput: enter selects focused option", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, _ := createMockTUI()
		var doneValue any
		component := escapeComponent(t, ctx, tui, func(r any) { doneValue = r })
		component.HandleInput("\r")
		eq(t, doneValue, any("continue_working"), "Default enter selects continue_working")
	})
	tw(t, f, "escape dialog handleInput: up + enter selects complete_without_audit", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, _ := createMockTUI()
		var doneValue any
		component := escapeComponent(t, ctx, tui, func(r any) { doneValue = r })
		in := component
		in.HandleInput("\x1b[A")
		in.HandleInput("\r")
		eq(t, doneValue, any("complete_without_audit"), "Up+enter selects complete_without_audit")
	})
	tw(t, f, "escape dialog handleInput: navigation wraps around", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, _ := createMockTUI()
		var doneValue any
		component := escapeComponent(t, ctx, tui, func(r any) { doneValue = r })
		in := component
		in.HandleInput("\x1b[A")
		in.HandleInput("\x1b[A")
		in.HandleInput("\r")
		eq(t, doneValue, any("continue_working"), "Navigation wraps around correctly")

		doneValue = nil
		component2 := escapeComponent(t, ctx, tui, func(r any) { doneValue = r })
		in2 := component2
		in2.HandleInput("\x1b[B")
		in2.HandleInput("\r")
		eq(t, doneValue, any("complete_without_audit"), "Down wraps around correctly")
	})
	tw(t, f, "escape dialog component dispose restores hardware cursor", func(t *testing.T) {
		ctx := escapeFixture(t, "Test objective")
		tui, state := createMockTUI()
		component := escapeComponent(t, ctx, tui, func(any) {})
		if len(state.setShowHardwareCursorCalls) < 1 {
			t.Fatal("setShowHardwareCursor was called")
		}
		eq(t, state.setShowHardwareCursorCalls[0], false, "first call")
		component.dispose()
		eq(t, state.setShowHardwareCursorCalls[len(state.setShowHardwareCursorCalls)-1], false, "Hardware cursor restored after dispose")
	})
	tw(t, f, "escape dialog renders without overflow at width ${testWidth}", func(t *testing.T) {
		for _, testWidth := range []int{50, 60, 70, 80, 90, 109} {
			ctx1 := escapeFixture(t, "Short")
			tui, _ := createMockTUI()
			comp := escapeComponent(t, ctx1, tui, func(any) {})
			for i, l := range comp.Render(testWidth) {
				if l != "" {
					if w := visibleWidth(l); w > testWidth {
						t.Errorf("Short objective at width %d, line %d: visibleWidth=%d > %d", testWidth, i, w, testWidth)
					}
				}
			}
		}
	})
	tw(t, f, "escape dialog with extreme-length objective at various widths", func(t *testing.T) {
		tui, _ := createMockTUI()
		for _, testWidth := range []int{50, 60, 70, 80, 90, 109} {
			ctx := escapeFixture(t, strings.Repeat("A very long objective that should definitely overflow at narrow terminal widths if there's a bug in the rendering code. ", 10))
			comp := escapeComponent(t, ctx, tui, func(any) {})
			for i, l := range comp.Render(testWidth) {
				if l != "" {
					if w := visibleWidth(l); w > testWidth {
						t.Errorf("Long objective at width %d, line %d: visibleWidth=%d > %d", testWidth, i, w, testWidth)
					}
				}
			}
		}
	})
	tw(t, f, "escape dialog renders various objective lengths without error", func(t *testing.T) {
		tui, _ := createMockTUI()
		ctx1 := escapeFixture(t, "Short")
		comp1 := escapeComponent(t, ctx1, tui, func(any) {})
		comp1.Render(40)
		comp1.Render(80)

		ctx2 := escapeFixture(t, strings.Repeat("A", 200))
		comp2 := escapeComponent(t, ctx2, tui, func(any) {})
		if len(comp2.Render(80)) == 0 {
			t.Error("Long objective renders")
		}
		ctx3 := escapeFixture(t, "")
		comp3 := escapeComponent(t, ctx3, tui, func(any) {})
		if len(comp3.Render(80)) == 0 {
			t.Error("Empty objective renders")
		}
	})
}
