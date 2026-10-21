package pi_goal_x

import (
	"os"
	"strconv"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// goalDialogUI is the host UI the dialogs run on (ctx.ui in the original). A primitive the host lacks reports false from its has* method
// (the original tests `typeof ctx.ui.select === "function"`).
type goalDialogUI interface {
	hasCustom() bool
	hasSelect() bool
	hasInput() bool
	// custom shows a component built by factory and blocks until it calls done (or the host resolves without a result: nil).
	custom(factory goalCustomFactory, opts goalCustomOptions) (any, error)
	// select returns ok=false when the user dismissed the dialog.
	selectOption(title string, options []string) (string, bool, error)
	input(title, placeholder string) (string, bool, error)
	notify(message, level string)
	setWorkingVisible(visible bool)
	// onTerminalInput registers a terminal input listener (nil result = not consumed) and returns its unsubscribe.
	onTerminalInput(cb func(data string) *goalInputResult) (unsubscribe func())
}

// goalDialogContext is the ExtensionContext slice the dialogs use.
type goalDialogContext interface {
	hasUI() bool
	mode() string
	cwd() string
	ui() goalDialogUI
}

// goalDialogTUI is the TUI object handed to a custom factory. A nil goalDialogTUI models a host that passes a render callback instead.
type goalDialogTUI interface {
	getShowHardwareCursor() bool
	setShowHardwareCursor(enabled bool)
	requestRender()
	// terminalRows is terminal.rows (ok=false when absent); previousLinesLen is previousLines.length.
	terminalRows() (int, bool)
	previousLinesLen() (int, bool)
}

// goalTheme-like surface the dialogs need (the original's Theme).
type dialogTheme interface {
	fg(color, text string) string
	bold(text string) string
	bg(color, text string) string
}

// goalDialogComponent is a custom-dialog component: render/invalidate plus key input and focus.
type goalDialogComponent interface {
	tui.Component
	HandleInput(data string)
	SetFocused(focused bool)
	dispose()
}

type goalCustomFactory func(t goalDialogTUI, theme dialogTheme, done func(result any)) goalDialogComponent

type goalCustomOptions struct {
	overlay        bool
	anchor         string
	width          string
	minWidth       int
	maxHeight      string
	overlayOptions bool
}

// emptyDialogComponent is the bail-out component of a factory the host cannot drive.
type emptyDialogComponent struct{}

func (emptyDialogComponent) Render(int) []string { return []string{} }
func (emptyDialogComponent) Invalidate()         {}
func (emptyDialogComponent) HandleInput(string)  {}
func (emptyDialogComponent) SetFocused(bool)     {}
func (emptyDialogComponent) dispose()            {}

// ported from goal-task-confirmation.ts renderConfirmationTasks
func renderConfirmationTasks(tasks []*jsObject, indent int) []string {
	prefix := strings.Repeat("  ", indent)
	lines := []string{}
	for _, t := range tasks {
		lw := ""
		if gflag(t, "lightweightSubtasks") {
			lw = " (lightweight)"
		}
		contract := ""
		if vc := gstr(t, "verificationContract"); vc != "" {
			contract = " contract: " + vc
		}
		lines = append(lines, prefix+"[ ] "+gstr(t, "id")+": "+gstr(t, "title")+lw+contract)
		if subs := draftTaskObjects(t, "subtasks"); len(subs) > 0 {
			lines = append(lines, renderConfirmationTasks(subs, indent+1)...)
		}
	}
	return lines
}

// draftTaskObjects reads a task array field (stored as []any or []*jsObject).
func draftTaskObjects(o *jsObject, key string) []*jsObject {
	v, ok := o.get(key)
	if !ok {
		return nil
	}
	switch a := v.(type) {
	case []*jsObject:
		return a
	case []any:
		out := make([]*jsObject, 0, len(a))
		for _, e := range a {
			if x, ok := e.(*jsObject); ok {
				out = append(out, x)
			}
		}
		return out
	}
	return nil
}

type taskConfirmationResult struct {
	decision string // "confirm" | "cancel"
}

// showTaskConfirmation ported from goal-task-confirmation.ts: task-only confirmation boundary.
func showTaskConfirmation(ctx goalDialogContext, proposalText string) (taskConfirmationResult, error) {
	env := os.Getenv("PI_GOAL_AUTO_CONFIRM")
	if env == "1" {
		return taskConfirmationResult{"confirm"}, nil
	}
	if !ctx.hasUI() {
		if env == "0" {
			return taskConfirmationResult{"cancel"}, nil
		}
		return taskConfirmationResult{"confirm"}, nil
	}
	ui := ctx.ui()
	if ctx.mode() != "rpc" && ui.hasCustom() {
		result, err := showTaskListConfirmationDialog(ctx, proposalText)
		if err != nil {
			return taskConfirmationResult{}, err
		}
		if result != nil {
			return *result, nil
		}
	}
	if !ui.hasSelect() {
		return taskConfirmationResult{"cancel"}, nil
	}
	answer, ok, err := ui.selectOption("Task list confirmation\n\n"+proposalText, []string{"Confirm task list", "Keep current tasks"})
	if err != nil {
		return taskConfirmationResult{}, err
	}
	if ok && answer == "Confirm task list" {
		return taskConfirmationResult{"confirm"}, nil
	}
	return taskConfirmationResult{"cancel"}, nil
}

type taskConfirmationOption struct{ label, value, description string }

func showTaskListConfirmationDialog(ctx goalDialogContext, proposalText string) (*taskConfirmationResult, error) {
	ui := ctx.ui()
	factory := func(t goalDialogTUI, theme dialogTheme, done func(any)) goalDialogComponent {
		if t == nil {
			done(nil)
			return emptyDialogComponent{}
		}
		wasShown := t.getShowHardwareCursor()
		t.setShowHardwareCursor(false)
		ui.setWorkingVisible(false)
		return &taskConfirmationDialog{ui: ui, t: t, theme: theme, done: done, wasShown: wasShown, body: strings.Split(proposalText, "\n")}
	}
	res, err := ui.custom(factory, goalCustomOptions{overlay: true, anchor: "center", width: "70%", minWidth: 50, maxHeight: "60%"})
	if err != nil {
		return nil, err
	}
	if r, ok := res.(*taskConfirmationResult); ok && r != nil {
		return r, nil
	}
	return nil, nil
}

var taskConfirmationOptions = []taskConfirmationOption{
	{"Confirm task list", "confirm", "Replace the current task list with this structure."},
	{"Keep current tasks", "cancel", "Leave the existing task list unchanged."},
}

const taskConfirmationMaxBody = 16

type taskConfirmationDialog struct {
	ui       goalDialogUI
	t        goalDialogTUI
	theme    dialogTheme
	done     func(any)
	wasShown bool
	body     []string
	selected int
}

func (d *taskConfirmationDialog) dispose() {
	d.t.setShowHardwareCursor(d.wasShown)
	d.ui.setWorkingVisible(true)
}
func (d *taskConfirmationDialog) Invalidate()     {}
func (d *taskConfirmationDialog) SetFocused(bool) {}

func (d *taskConfirmationDialog) Render(width int) []string {
	termWidth := width
	if termWidth > 80 {
		termWidth = 80
	}
	inner := dialogInnerWidth(termWidth, 64)
	accent := func(s string) string { return d.theme.fg("accent", s) }
	dim := func(s string) string { return d.theme.fg("dim", s) }
	muted := func(s string) string { return d.theme.fg("muted", s) }
	line := func(c string) string { return borderedLine(accent, inner, c) }
	horiz := horizontalRule(inner)
	const p = "  "
	lines := []string{accent("┌" + horiz + "┐"), line(p + d.theme.bold("Task list confirmation")), accent("├" + horiz + "┤")}
	n := len(d.body)
	if n > taskConfirmationMaxBody {
		n = taskConfirmationMaxBody
	}
	for _, raw := range d.body[:n] {
		t := raw
		if t == "" {
			t = " "
		}
		lines = append(lines, line(p+widthx.TruncateToWidth(muted(t), inner-len(p), "…", false)))
	}
	if len(d.body) > taskConfirmationMaxBody {
		lines = append(lines, line(p+dim("+"+strconv.Itoa(len(d.body)-taskConfirmationMaxBody)+" more lines")))
	}
	lines = append(lines, accent("├"+horiz+"┤"))
	for i, opt := range taskConfirmationOptions {
		sel := i == d.selected
		marker := "  "
		label := opt.label
		if sel {
			marker = "▸ "
			label = d.theme.fg("accent", opt.label)
		}
		lines = append(lines, line(p+marker+widthx.TruncateToWidth(label, inner-6, "…", false)))
		if sel {
			lines = append(lines, line(p+strings.Repeat(" ", 4)+widthx.TruncateToWidth(dim(opt.description), inner-10, "…", false)))
		}
	}
	lines = append(lines, accent("├"+horiz+"┤"), line(p+dim("Enter to select  ·  ↑↓ to navigate  ·  Esc = keep current tasks")), accent("└"+horiz+"┘"))
	return lines
}

func (d *taskConfirmationDialog) HandleInput(data string) {
	n := len(taskConfirmationOptions)
	switch {
	case tui.MatchesKey(data, "up"):
		d.selected = (d.selected - 1 + n) % n
		d.t.requestRender()
	case tui.MatchesKey(data, "down"):
		d.selected = (d.selected + 1) % n
		d.t.requestRender()
	case tui.MatchesKey(data, "enter"):
		d.done(&taskConfirmationResult{taskConfirmationOptions[d.selected].value})
	case tui.MatchesKey(data, "escape"):
		d.done(&taskConfirmationResult{"cancel"})
	}
}
