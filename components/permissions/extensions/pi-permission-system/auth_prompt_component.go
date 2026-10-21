package pi_permission_system

import (
	"errors"
	"sync"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// Port of src/authority/permission-prompt-component.ts: the inline ctx.ui.custom permission dialog for TUI sessions. All
// interaction logic lives in the pure ReducePrompt model; this file is the thin adapter that renders the model's state to lines,
// maps raw keystrokes to PromptEvents, and hands the committed decision to the host's done callback. The component renders inline
// (never as an overlay).
//
// Go form of the host seam. The original's factory receives (tui, theme, keybindings, done) from ctx.ui.custom and returns the
// component; a Go extension gets the same four things from PermissionPromptUi.Custom, which blocks until done is called (the
// original's promise). The render path never calls the host: every host effect (a render request, a terminal write, the tool
// expansion toggle) is a callback the host adapter supplies, invoked outside the component's lock.

// PromptTUI is the part of Pi's TUI the dialog uses: a render request and the terminal's raw write.
type PromptTUI interface {
	RequestRender()
	// Write is tui.terminal.write: raw bytes to the terminal (the prompt notification).
	Write(data string)
}

// PromptTheme is the minimal theme surface the dialog uses; the SDK's UITheme satisfies it.
type PromptTheme interface {
	Fg(color, text string) string
}

// PromptComponent is the component a PromptComponentFactory builds.
type PromptComponent interface {
	Render(width int) []string
	HandleInput(data string)
}

// PromptComponentFactory is ctx.ui.custom's factory: it runs once, as the dialog mounts, which is the moment the human is being
// asked; a re-render does not come back through here. keybindings is the host's manager: the dialog reads only the tool-expansion
// action from it (the denial-reason editor reads pi-tui's global manager, as the original's Input does).
type PromptComponentFactory func(tui PromptTUI, theme PromptTheme, keybindings tui.Keybindings, done func(UnattributedDecision)) PromptComponent

// PromptCustomOptions are ctx.ui.custom's options. The dialog always asks for Overlay false: it renders inline, never as an overlay.
type PromptCustomOptions struct{ Overlay bool }

// PermissionPromptUi is the subset of the session UI surface the inline dialog needs (select, input, custom, getToolsExpanded,
// setToolsExpanded).
type PermissionPromptUi interface {
	PermissionDecisionUi
	// Custom is ctx.ui.custom: it mounts the component and returns what it passed to done. ok false is a dialog that closed without a
	// decision.
	Custom(factory PromptComponentFactory, options PromptCustomOptions) (decision UnattributedDecision, ok bool, err error)
	GetToolsExpanded() bool
	SetToolsExpanded(expanded bool)
}

// PromptPreferences are the live prompt-behaviour preferences read at prompt time (see doublePressToConfirm).
type PromptPreferences struct {
	DoublePressToConfirm bool
	// Budget is how much room a render has; the terminal width is added per frame.
	Budget RenderBudget
	// DialogKeys is the character bound to each decision.
	DialogKeys DialogKeyBindings
	// PromptNotifications are the terminal notifications the inline dialog emits as it opens; empty for none.
	PromptNotifications []string
}

// PermissionPromptView is the resolved presentation context selected once per activation.
type PermissionPromptView struct {
	PromptPreferences
	// Mode is the session run mode: "tui", "rpc", "json" or "print".
	Mode string
	UI   PermissionPromptUi
	// Notice is what the inline dialog's terminal notification says; the fallback ignores it.
	Notice PromptNotice
}

// RequestPermissionDecisionFunc is `typeof requestPermissionDecision`, the seam LocalUserAuthorizer takes for testability.
type RequestPermissionDecisionFunc func(view PermissionPromptView, title string, payload PromptPayload, options *RequestPermissionOptions) (PermissionPromptDecision, error)

// fallbackRenderWidth is the width the select/input fallback renders against: it renders once and cannot re-render, so it
// substitutes a nominal width for the terminal size it is never told, and the host's own select wraps from there.
const fallbackRenderWidth = 80

// RequestPermissionDecision routes a permission ask to the inline keybind dialog in TUI mode, or the select/input flow otherwise
// (RPC and frontend: the #519 constraint). It is the one place that knows which surface the human answered on, so it is where the
// decision is attributed to that surface (#726).
func RequestPermissionDecision(view PermissionPromptView, title string, payload PromptPayload, options *RequestPermissionOptions) (PermissionPromptDecision, error) {
	if view.Mode == "tui" {
		decision, err := PresentInlinePermissionPrompt(view, title, payload, options)
		if err != nil {
			return PermissionPromptDecision{}, err
		}
		return decision.Attribute(DecisionSource{Kind: "user", Via: "dialog"}), nil
	}
	rendered := RenderPromptDialog(payload, DialogBudget{RenderBudget: view.Budget, Width: fallbackRenderWidth}, nil)
	message := ""
	for i, line := range rendered.Lines {
		if i > 0 {
			message += "\n"
		}
		message += line
	}
	decision, err := RequestPermissionDecisionFromUi(view.UI, title, message, options)
	if err != nil {
		return PermissionPromptDecision{}, err
	}
	return decision.Attribute(DecisionSource{Kind: "user", Via: "select"}), nil
}

const defaultSessionLabel = "Yes, for this session"

var optionLabels = map[PromptAction]string{
	ActionApprove:            "Yes",
	ActionApproveSession:     defaultSessionLabel,
	ActionApproveSessionBoth: "Yes, for this session in both directions",
	ActionDeny:               "No",
	ActionDenyWithReason:     "No, provide reason",
}

// errDialogClosed is a dialog that mounted and closed without a decision: nobody ruled, so the ask must neither be approved nor
// attributed to the human.
var errDialogClosed = errors.New("the permission dialog closed without a decision")

// PresentInlinePermissionPrompt shows the inline dialog and blocks until the human decides. A dialog the host closed without a
// decision is an error, never an approval.
func PresentInlinePermissionPrompt(view PermissionPromptView, title string, payload PromptPayload, options *RequestPermissionOptions) (UnattributedDecision, error) {
	config := PromptModelConfig{
		DoublePressToConfirm: view.DoublePressToConfirm,
		SessionLabel:         defaultSessionLabel,
		Keys:                 view.DialogKeys,
	}
	if options != nil {
		if options.SessionLabel != nil {
			config.SessionLabel = *options.SessionLabel
		}
		if options.SessionWidth != nil {
			config.WidthLabel = options.SessionWidth.Label
		}
		config.SessionScope = options.SessionScope
	}
	decision, ok, err := view.UI.Custom(func(host PromptTUI, theme PromptTheme, keybindings tui.Keybindings, done func(UnattributedDecision)) PromptComponent {
		// The factory runs once, as the dialog mounts, which is the moment the human is being asked.
		if notification := RenderPromptNotification(view.PromptNotifications, view.Notice); notification != "" {
			host.Write(notification)
		}
		return newPermissionPromptComponent(theme, config, title, payload, view.Budget, toolsExpansion{keys: keybindings, ui: view.UI}, host.RequestRender, done)
	}, PromptCustomOptions{Overlay: false})
	if err != nil {
		return UnattributedDecision{}, err
	}
	if !ok {
		return UnattributedDecision{}, errDialogClosed
	}
	return decision, nil
}

// toolsExpansion is Pi's tool-expansion action while the dialog holds keyboard focus. A focused custom component consumes every
// keystroke, so Ctrl+O would otherwise be dead for the duration of an ask, exactly when the user most needs to see the full pending
// tool invocation. isAction reports whether a keystroke was the action, so the dialog stops before mapping it to a PromptEvent:
// expansion is a display concern and must never reach the decision model. toggle does not request a render: setToolsExpanded
// re-renders the host itself. isAction is a pure key match; toggle is the host call, made outside the component's lock.
type toolsExpansion struct {
	keys tui.Keybindings
	ui   PermissionPromptUi
}

func (t toolsExpansion) isAction(data string) bool { return t.keys.Matches(data, "app.tools.expand") }

func (t toolsExpansion) toggle() { t.ui.SetToolsExpanded(!t.ui.GetToolsExpanded()) }

// permissionPromptComponent is the dialog component.
type permissionPromptComponent struct {
	theme  PromptTheme
	config PromptModelConfig
	title  string
	// payload is the ask itself: every frame renders it, bounded to budget at that frame's width.
	payload       PromptPayload
	budget        RenderBudget
	tools         toolsExpansion
	requestRender func()
	done          func(UnattributedDecision)

	mu       sync.Mutex
	state    PromptViewState
	reason   *LineInput
	expanded bool
	finished bool
	// renderRequests counts the repaints the keystroke asks for, as many as the original calls tui.requestRender: a reason-editor
	// callback that changes the step requests one, and the editor keystroke itself requests another.
	renderRequests int
	// toggleTools is set when a keystroke was the expansion action: the host call happens after the lock is released.
	toggleTools bool
}

func newPermissionPromptComponent(theme PromptTheme, config PromptModelConfig, title string, payload PromptPayload, budget RenderBudget,
	tools toolsExpansion, requestRender func(), done func(UnattributedDecision)) *permissionPromptComponent {
	c := &permissionPromptComponent{
		theme: theme, config: config, title: title, payload: payload, budget: budget,
		tools: tools, requestRender: requestRender, done: done,
		state: InitialPromptState(config),
	}
	c.reason = c.createReasonEditor()
	return c
}

// createReasonEditor makes a fresh editor per visit to the reason step: the framework editor carries an undo stack and a kill ring,
// so reusing one instance would let a reason the operator backed out of be restored into a later ask. It emits pi-tui's zero-width
// cursor marker, which positions the hardware cursor for IME composition.
func (c *permissionPromptComponent) createReasonEditor() *LineInput {
	editor := NewLineInput(nil)
	editor.Focused = true
	editor.OnSubmit = func(draft string) { c.apply(PromptEvent{Type: EventSubmitReason, Draft: draft}) }
	editor.OnEscape = func() { c.apply(PromptEvent{Type: EventCancel}) }
	return editor
}

// Render renders the current step at width. No cached rendering state exists, and no host call is made.
func (c *permissionPromptComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return FitLinesToWidth(c.renderStep(width), width)
}

func (c *permissionPromptComponent) renderStep(width int) []string {
	switch c.state.Step {
	case StepDecision:
		return c.renderDecision(width)
	case StepReason:
		return c.renderReason(width)
	case StepScope:
		return c.renderScope()
	}
	return nil
}

// renderAsk is the ask itself, bounded to the budget at this frame's width. It is rendered per frame rather than once, because the
// row budget is a function of the width the host gives us, which a resize changes.
func (c *permissionPromptComponent) renderAsk(width int) DialogView {
	budget := DialogBudget{RenderBudget: c.budget, Width: width}
	if c.expanded {
		budget = CompleteViewBudget(width)
	}
	return RenderPromptDialog(c.payload, budget, func(text string) string { return c.theme.Fg("warning", text) })
}

// hint is the key hints, naming the expansion only when it would do something: an affordance advertised when there is nothing to
// expand is noise; one left unadvertised when the render dropped something is a decision made without the evidence.
func (c *permissionPromptComponent) hint(view DialogView) string {
	keys := []string{"↑/↓ move", "enter confirm", "esc deny", "press a letter, then again to confirm"}
	if c.expanded {
		keys = append(keys, "ctrl+o collapse")
	} else if view.Elided {
		keys = append(keys, "ctrl+o full request")
	}
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += " · "
		}
		out += k
	}
	return c.theme.Fg("muted", out)
}

// HandleInput handles one keystroke chunk. After the dialog has decided, input is dropped: the host disposes the component, and a
// second decision must never be produced.
func (c *permissionPromptComponent) HandleInput(data string) {
	c.mu.Lock()
	c.renderRequests = 0
	if !c.finished {
		c.handleInputLocked(data)
	}
	renders, toggle := c.renderRequests, c.toggleTools
	c.renderRequests, c.toggleTools = 0, false
	c.mu.Unlock()
	// Host effects run after the lock is released: a host may repaint synchronously, which calls Render.
	if toggle {
		c.tools.toggle()
	}
	if c.requestRender != nil {
		for ; renders > 0; renders-- {
			c.requestRender()
		}
	}
}

func (c *permissionPromptComponent) handleInputLocked(data string) {
	if c.state.Step == StepReason {
		c.handleReasonInput(data)
		return
	}
	if c.tools.isAction(data) {
		// One "expand" for the operator: the host expands its pending tool call and the dialog expands its own render, on the same
		// keystroke.
		c.toggleTools = true
		c.expanded = !c.expanded
		c.renderRequests++
		return
	}
	if event, ok := c.toEvent(data); ok {
		c.apply(event)
	}
}

// handleReasonInput hands the keystroke to the framework line editor. Delegating is what makes the field accept a paste: a paste
// arrives as one multi-character chunk wrapped in bracketed-paste markers, which the editor understands and a per-character reader
// cannot. Submit and cancel come back through the editor's callbacks, so the decision model still owns them.
func (c *permissionPromptComponent) handleReasonInput(data string) {
	c.reason.HandleInput(CollapsePastedNewlines(data))
	// The editor mutates its own buffer silently; only the dialog can repaint.
	c.renderRequests++
}

func (c *permissionPromptComponent) toEvent(data string) (PromptEvent, bool) {
	if tui.MatchesKey(data, "up") || tui.MatchesKey(data, "k") {
		return PromptEvent{Type: EventNav, Direction: "up"}, true
	}
	if tui.MatchesKey(data, "down") || tui.MatchesKey(data, "j") {
		return PromptEvent{Type: EventNav, Direction: "down"}, true
	}
	if tui.MatchesKey(data, "enter") {
		return PromptEvent{Type: EventConfirm}, true
	}
	if tui.MatchesKey(data, "escape") {
		return PromptEvent{Type: EventCancel}, true
	}
	if c.state.Step == StepDecision {
		for _, option := range VisibleActions(c.config) {
			if tui.MatchesKey(data, c.config.Keys[option]) {
				return PromptEvent{Type: EventHotkey, Action: option}, true
			}
		}
	}
	return PromptEvent{}, false
}

func (c *permissionPromptComponent) apply(event PromptEvent) {
	outcome := ReducePrompt(c.config, c.state, event)
	if outcome.Decided {
		if !c.finished {
			c.finished = true
			c.done(outcome.Decision)
		}
		return
	}
	if outcome.State.Step == StepReason && c.state.Step != StepReason {
		c.reason = c.createReasonEditor()
	}
	c.state = outcome.State
	c.renderRequests++
}

func (c *permissionPromptComponent) renderDecision(width int) []string {
	ask := c.renderAsk(width)
	lines := append([]string{c.theme.Fg("accent", c.title)}, ask.Lines...)
	lines = append(lines, "")
	for _, action := range VisibleActions(c.config) {
		label := c.labelFor(action)
		selected := c.state.HighlightedAction == action
		marker := " "
		if selected {
			marker = "▶"
		}
		row := marker + " (" + c.config.Keys[action] + ") " + label
		if selected {
			row = c.theme.Fg("accent", row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")
	if c.state.Hint != "" {
		lines = append(lines, c.state.Hint)
	} else {
		lines = append(lines, c.hint(ask))
	}
	return lines
}

// labelFor is the row label for an action: the two session options carry ask-supplied text naming what they grant, and the rest
// are fixed.
func (c *permissionPromptComponent) labelFor(action PromptAction) string {
	if action == ActionApproveSession {
		return c.config.SessionLabel
	}
	if action == ActionApproveSessionBoth && c.config.WidthLabel != "" {
		return c.config.WidthLabel
	}
	return optionLabels[action]
}

func (c *permissionPromptComponent) renderReason(width int) []string {
	lines := []string{c.theme.Fg("accent", c.title)}
	lines = append(lines, c.renderAsk(width).Lines...)
	lines = append(lines, "", "Reason (required):")
	// Exactly one row, whatever its length: the editor scrolls horizontally.
	lines = append(lines, c.reason.Render(width)...)
	if c.state.ReasonError != "" {
		lines = append(lines, c.theme.Fg("error", c.state.ReasonError))
	}
	lines = append(lines, "", c.theme.Fg("muted", "enter submit · esc back"))
	return lines
}

func (c *permissionPromptComponent) renderScope() []string {
	subagentLabel, servingLabel := "This subagent only", "The whole session"
	if scope := c.config.SessionScope; scope != nil {
		subagentLabel, servingLabel = scope.SubagentLabel, scope.ServingSessionLabel
	}
	lines := []string{c.theme.Fg("accent", c.title), "Apply this session grant to:", ""}
	for _, row := range []struct {
		label   string
		serving bool
	}{{subagentLabel, false}, {servingLabel, true}} {
		selected := c.state.ScopeServing == row.serving
		marker := " "
		if selected {
			marker = "▶"
		}
		text := marker + " " + row.label
		if selected {
			text = c.theme.Fg("accent", text)
		}
		lines = append(lines, text)
	}
	lines = append(lines, "", c.theme.Fg("muted", "↑/↓ move · enter confirm · esc back"))
	return lines
}
