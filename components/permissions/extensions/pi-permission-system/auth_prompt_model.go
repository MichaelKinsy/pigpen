package pi_permission_system

// Port of src/authority/permission-prompt-decision.ts: the pure decision model for the inline keybind permission dialog. Which
// hotkey produces which decision, double-press arming, step transitions and reason validation live here with no SDK or TUI imports.
// The ctx.ui.custom component (auth_prompt_component.go) is a thin adapter that forwards keystrokes to ReducePrompt and renders the
// returned state.

// PromptStep is which sub-view the dialog is showing: "decision", "reason" or "scope".
type PromptStep string

// The three steps.
const (
	StepDecision PromptStep = "decision"
	StepReason   PromptStep = "reason"
	StepScope    PromptStep = "scope"
)

// optionOrder is the decisions in display order. approveSessionBoth is conditional (#813): the roster an ask actually offers comes
// from VisibleActions.
var optionOrder = []PromptAction{ActionApprove, ActionApproveSession, ActionApproveSessionBoth, ActionDeny, ActionDenyWithReason}

var narrowOptionOrder = []PromptAction{ActionApprove, ActionApproveSession, ActionDeny, ActionDenyWithReason}

// VisibleActions is the decision step's options, in display order. The width option is offered iff the ask supplied a label for it,
// so an ask that proves no single direction is rendered and navigated exactly as before.
func VisibleActions(config PromptModelConfig) []PromptAction {
	if config.WidthLabel != "" {
		return optionOrder
	}
	return narrowOptionOrder
}

var optionVerbs = map[PromptAction]string{
	ActionApprove:            "approve",
	ActionApproveSession:     "approve for this session",
	ActionApproveSessionBoth: "approve both directions for this session",
	ActionDeny:               "deny",
	ActionDenyWithReason:     "deny with a reason",
}

// PromptModelConfig is the static configuration of a single prompt presentation.
type PromptModelConfig struct {
	// DoublePressToConfirm: a hotkey arms first and commits only on a second press.
	DoublePressToConfirm bool
	// Keys is the character bound to each decision: what the dialog matches keystrokes against and what it renders.
	Keys DialogKeyBindings
	// SessionLabel is the label shown beside the approve-for-session option.
	SessionLabel string
	// WidthLabel is the label for the both-directions session option (#813); its presence is what offers the option.
	WidthLabel string
	// SessionScope (forwarded asks only): confirming "s" opens a second step choosing whether the grant applies to the requesting
	// subagent only (least-privilege default) or the whole serving session.
	SessionScope *SessionScopeOption
}

// PromptViewState is the re-render view state the component draws from.
type PromptViewState struct {
	Step              PromptStep
	HighlightedAction PromptAction
	// ArmedAction is set only while awaiting the confirming second press of a hotkey ("" is undefined).
	ArmedAction PromptAction
	// Hint is "Press y again to approve." while armed; empty otherwise.
	Hint string
	// ReasonError is set when an empty reason submit is rejected ("" is undefined).
	ReasonError string
	// ScopeServing is the scope step's choice: false is subagent-only (default), true the whole serving session.
	ScopeServing bool
	// GrantWidth is the width the session option chosen so far would grant, reset to "proven" on every return to the decision step
	// so a width the user backed out of cannot ride along with a later narrow choice.
	GrantWidth SessionGrantWidth
}

// PromptEventType is the kind of an input event the reducer understands.
type PromptEventType string

// The five event types.
const (
	EventNav          PromptEventType = "nav"
	EventHotkey       PromptEventType = "hotkey"
	EventConfirm      PromptEventType = "confirm"
	EventCancel       PromptEventType = "cancel"
	EventSubmitReason PromptEventType = "submitReason"
)

// PromptEvent is an input event: Direction ("up" or "down") for nav, Action for hotkey, Draft for submitReason.
type PromptEvent struct {
	Type      PromptEventType
	Direction string
	Action    PromptAction
	Draft     string
}

// PromptOutcome is either a re-render (Decided false, State set) or a terminal decision (Decided true, Decision set).
type PromptOutcome struct {
	Decided  bool
	State    PromptViewState
	Decision UnattributedDecision
}

// InitialPromptState is the state a prompt opens in.
func InitialPromptState(PromptModelConfig) PromptViewState {
	return PromptViewState{Step: StepDecision, HighlightedAction: ActionApprove, GrantWidth: "proven"}
}

// ReducePrompt advances the dialog by one input event, returning either the next view state to render or the committed decision.
// The model states the outcome and not the decider: which human surface this is gets attributed by the dispatcher that chose to
// render this dialog.
func ReducePrompt(config PromptModelConfig, state PromptViewState, event PromptEvent) PromptOutcome {
	switch state.Step {
	case StepDecision:
		return reduceDecisionStep(config, state, event)
	case StepReason:
		return reduceReasonStep(state, event)
	case StepScope:
		return reduceScopeStep(state, event)
	}
	// An unknown step cannot be reached through ReducePrompt's own outputs; it decides nothing.
	return renderOutcome(state)
}

func renderOutcome(state PromptViewState) PromptOutcome { return PromptOutcome{State: state} }

func decisionOutcome(d UnattributedDecision) PromptOutcome {
	return PromptOutcome{Decided: true, Decision: d}
}

func reduceDecisionStep(config PromptModelConfig, state PromptViewState, event PromptEvent) PromptOutcome {
	switch event.Type {
	case EventNav:
		next := state
		next.HighlightedAction = shiftAction(config, state.HighlightedAction, event.Direction)
		next.ArmedAction = ""
		next.Hint = ""
		return renderOutcome(next)
	case EventHotkey:
		for _, a := range VisibleActions(config) {
			if a == event.Action {
				return pressHotkey(config, state, event.Action)
			}
		}
		return renderOutcome(state)
	case EventConfirm:
		return commit(config, state, state.HighlightedAction)
	case EventCancel:
		return decisionOutcome(CreateDeniedPermissionDecision(nil))
	}
	return renderOutcome(state)
}

func pressHotkey(config PromptModelConfig, state PromptViewState, action PromptAction) PromptOutcome {
	if !config.DoublePressToConfirm || state.ArmedAction == action {
		return commit(config, state, action)
	}
	next := state
	next.HighlightedAction = action
	next.ArmedAction = action
	key, ok := config.Keys[action]
	if !ok {
		key = "undefined"
	}
	next.Hint = "Press " + key + " again to " + optionVerbs[action] + "."
	return renderOutcome(next)
}

func commit(config PromptModelConfig, state PromptViewState, action PromptAction) PromptOutcome {
	switch action {
	case ActionApprove:
		return decisionOutcome(UnattributedDecision{Approved: true, State: DecisionApproved})
	case ActionDeny:
		return decisionOutcome(CreateDeniedPermissionDecision(nil))
	case ActionDenyWithReason:
		next := state
		next.Step = StepReason
		next.HighlightedAction = ActionDenyWithReason
		next.ArmedAction = ""
		next.Hint = ""
		next.ReasonError = ""
		return renderOutcome(next)
	case ActionApproveSession, ActionApproveSessionBoth:
		// The two session options differ only in the width they grant; which scope they land on is the forwarded scope step's
		// separate question.
		width := SessionGrantWidth("proven")
		if action == ActionApproveSessionBoth {
			width = "family"
		}
		if config.SessionScope != nil {
			next := state
			next.Step = StepScope
			next.HighlightedAction = action
			next.ArmedAction = ""
			next.Hint = ""
			next.ScopeServing = false
			next.GrantWidth = width
			return renderOutcome(next)
		}
		return decisionOutcome(sessionApprovalDecision(DecisionApprovedForSession, width))
	}
	// An action outside the five (the highlight can never hold one) commits nothing.
	return renderOutcome(state)
}

func reduceReasonStep(state PromptViewState, event PromptEvent) PromptOutcome {
	if event.Type == EventCancel {
		next := state
		next.Step = StepDecision
		next.ArmedAction = ""
		next.Hint = ""
		next.ReasonError = ""
		next.GrantWidth = "proven"
		return renderOutcome(next)
	}
	if event.Type == EventSubmitReason {
		draft := event.Draft
		reason := NormalizePermissionDenialReason(&draft)
		if reason == nil {
			next := state
			next.ReasonError = "A reason is required."
			return renderOutcome(next)
		}
		return decisionOutcome(CreateDeniedPermissionDecision(reason))
	}
	return renderOutcome(state)
}

func reduceScopeStep(state PromptViewState, event PromptEvent) PromptOutcome {
	switch event.Type {
	case EventNav:
		next := state
		next.ScopeServing = event.Direction == "down"
		return renderOutcome(next)
	case EventConfirm:
		decisionState := DecisionApprovedForSession
		if state.ScopeServing {
			decisionState = DecisionApprovedForServingSess
		}
		return decisionOutcome(sessionApprovalDecision(decisionState, state.GrantWidth))
	case EventCancel:
		next := state
		next.Step = StepDecision
		next.ArmedAction = ""
		next.Hint = ""
		next.GrantWidth = "proven"
		return renderOutcome(next)
	}
	return renderOutcome(state)
}

func shiftAction(config PromptModelConfig, current PromptAction, direction string) PromptAction {
	actions := VisibleActions(config)
	index := -1
	for i, a := range actions {
		if a == current {
			index = i
			break
		}
	}
	delta := -1
	if direction == "down" {
		delta = 1
	}
	next := (index + delta + len(actions)) % len(actions)
	if next < 0 || next >= len(actions) {
		return current
	}
	return actions[next]
}
