package ui

import tui "github.com/MichaelKinsy/pigpen/components/pitui"

// QuestionnaireState is the canonical state of the questionnaire dialog: the single source of truth read by the
// dispatcher (RouteKey) and the view layer. The maps are treated as immutable: copy to change.
// upstream: state/state.ts QuestionnaireState.
type QuestionnaireState struct {
	CurrentTab   int
	OptionIndex  int
	InputMode    bool
	NotesVisible bool
	Answers      map[int]QuestionAnswer
	// MultiSelectChecked is ReadonlySet<number>.
	MultiSelectChecked map[int]bool
	// CustomDraftsByTab holds in-flight custom answers keyed by tab. A present empty string overrides an older answer.
	CustomDraftsByTab map[int]string
	// NotesByTab is the pre-answer notes side-band, keyed by tab index; the Submit-tab global note sits at the
	// len(questions) pseudo-index.
	NotesByTab map[int]string
	// SubmitChoiceIndex is the focused row of the Submit-tab picker (0 Submit, 1 Cancel).
	SubmitChoiceIndex int
	// NotesDraft is the canonical mirror of the in-flight notes editor.
	NotesDraft string
	// Collapsed: the questionnaire got out of the way (see the original's long comment): while true every
	// keystroke except cancel is swallowed.
	Collapsed bool
}

// QuestionnaireRuntime is the per-tick context the dispatcher needs alongside canonical state.
// upstream: state/state.ts QuestionnaireRuntime.
type QuestionnaireRuntime struct {
	Keybindings      tui.Keybindings
	InputBuffer      string
	CanMoveInputUp   bool
	CanMoveInputDown bool
	Questions        []QuestionData
	IsMulti          bool
	// CurrentItem is nil where the original has undefined.
	CurrentItem *WrappingSelectItem
	Items       []WrappingSelectItem
	// CollapseKey is the key spec of the collapse/expand shortcut ("off" disables it).
	CollapseKey string
}
