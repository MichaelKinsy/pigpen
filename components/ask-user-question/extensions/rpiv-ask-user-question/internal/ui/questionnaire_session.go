package ui

import (
	"strings"
	"sync"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// QuestionnaireSessionConfig configures a session. upstream: QuestionnaireSessionConfig.
type QuestionnaireSessionConfig struct {
	TUI   TUI
	Theme Theme
	// Questions is `params.questions`.
	Questions   []QuestionData
	ItemsByTab  [][]WrappingSelectItem
	Done        func(result QuestionnaireResult)
	Keybindings tui.Keybindings
	// EditInput opens Pi's configured external editor for value. It must arrange for finish to be called exactly
	// once, from any goroutine, when the edit ends: finish(edited, true) commits the replacement buffer,
	// finish("", false) is the original's resolve-undefined or rejection (the draft is retained). finish may be
	// called synchronously from inside EditInput; the session then applies the result after the current dispatch
	// has finished (the original's promise continuation is always asynchronous).
	// upstream: editInput: (value) => Promise<string | undefined>.
	EditInput func(value string, finish func(edited string, ok bool))
	// CollapseKey is the key spec for the collapse/expand shortcut, e.g. "ctrl+]" or "alt+o".
	CollapseKey string
	// CanReopenWhileHidden gates the set_overlay_hidden effect: true iff a raw terminal listener can reopen a
	// hidden overlay.
	CanReopenWhileHidden bool
}

// OverlayHandle is the part of pi-tui's OverlayHandle the session uses.
type OverlayHandle interface {
	SetHidden(hidden bool)
	IsHidden() bool
	IsFocused() bool
}

// QuestionnaireSessionComponent is the component a session exposes. upstream: QuestionnaireSessionComponent.
type QuestionnaireSessionComponent interface {
	Render(width int) []string
	Invalidate()
	HandleInput(data string)
}

func initialState() QuestionnaireState {
	return QuestionnaireState{
		Answers:            map[int]QuestionAnswer{},
		MultiSelectChecked: map[int]bool{},
		CustomDraftsByTab:  map[int]string{},
		NotesByTab:         map[int]string{},
	}
}

// QuestionnaireSession is the slim runtime: it owns the canonical state cell, the headless editor cells, the
// notes-draft mirror, and the effect runner. State transitions go through Reduce; UI fan-out goes through the
// props adapter produced by BuildQuestionnaire.
//
// Concurrency: every entry point (Component().HandleInput/Render/Invalidate, Dispatch, SetOverlayHandle,
// ToggleCollapsedExternal and the EditInput finish callback) takes one mutex, so the RPC goroutine delivering
// keystrokes and the goroutine finishing an external edit may interleave safely. The Done callback and the
// EditInput function are invoked with that mutex held: they must not call back into the session.
type QuestionnaireSession struct {
	mu    sync.Mutex
	state QuestionnaireState

	questions  []QuestionData
	isMulti    bool
	itemsByTab [][]WrappingSelectItem

	notesInput  *tui.Editor
	inlineInput *tui.Editor
	viewAdapter *QuestionnairePropsAdapter
	keybindings tui.Keybindings
	editInput   func(value string, finish func(edited string, ok bool))
	collapseKey string
	canReopen   bool

	inputEditorOpen bool
	// syncFinish collects a finish call made synchronously inside EditInput.
	inEditInput bool
	syncResult  *editResult

	overlayHandle OverlayHandle

	tui       TUI
	theme     Theme
	done      func(QuestionnaireResult)
	component QuestionnaireSessionComponent
}

type editResult struct {
	edited string
	ok     bool
}

// NewQuestionnaireSession is `new QuestionnaireSession(config)`.
func NewQuestionnaireSession(config QuestionnaireSessionConfig) *QuestionnaireSession {
	s := &QuestionnaireSession{
		state:       initialState(),
		tui:         config.TUI,
		done:        config.Done,
		questions:   config.Questions,
		isMulti:     len(config.Questions) > 1,
		itemsByTab:  config.ItemsByTab,
		keybindings: config.Keybindings,
		editInput:   config.EditInput,
		collapseKey: config.CollapseKey,
		canReopen:   config.CanReopenWhileHidden,
	}
	built := BuildQuestionnaire(QuestionnaireBuildConfig{
		TUI:           s.tui,
		Theme:         config.Theme,
		Questions:     s.questions,
		ItemsByTab:    s.itemsByTab,
		IsMulti:       s.isMulti,
		InitialState:  s.state,
		GetCurrentTab: func() int { return s.state.CurrentTab },
		CollapseKey:   s.collapseKey,
	})
	s.notesInput = built.NotesInput
	s.inlineInput = built.InlineInput
	s.viewAdapter = built.Adapter
	s.component = s.assembleComponent(built, config.Theme)
	s.viewAdapter.Apply(s.state)
	return s
}

// Component is the `component` field of the original.
func (s *QuestionnaireSession) Component() QuestionnaireSessionComponent { return s.component }

type sessionComponent struct {
	s       *QuestionnaireSession
	render  func(width int) []string
	invalid func()
}

func (c *sessionComponent) Render(width int) []string {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.state.Collapsed {
		return c.s.collapsedRender(width)
	}
	return c.render(width)
}
func (c *sessionComponent) Invalidate() {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.invalid()
}
func (c *sessionComponent) HandleInput(data string) { c.s.Dispatch(data) }

func (s *QuestionnaireSession) assembleComponent(built QuestionnaireBuilt, theme Theme) QuestionnaireSessionComponent {
	s.theme = theme
	return &sessionComponent{s: s, render: built.Render, invalid: built.Invalidate}
}

// collapsedRender is a single dim row: pi-tui sizes the overlay to the line count, so one line shrinks the
// bottom-anchored overlay. T stays inside the call (live locale). With collapseKey "off" it falls back to the
// cancel-only line rather than rendering a literal "Off to expand".
func (s *QuestionnaireSession) collapsedRender(_ int) []string {
	var hint string
	if s.collapseKey == CollapseKeyOff {
		hint = T("hint.cancel", HintPartCancel)
	} else {
		hint = strings.Replace(T("hint.expand_line", CollapsedHintTemplate), KeyPlaceholder, FormatKeySpecForDisplay(s.collapseKey), 1)
	}
	return []string{s.theme.Fg("dim", " "+hint+" ")}
}

// Dispatch handles one keystroke. upstream: dispatch.
func (s *QuestionnaireSession) Dispatch(data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchLocked(data)
}

func (s *QuestionnaireSession) dispatchLocked(data string) {
	if s.inputEditorOpen {
		return
	}
	action := RouteKey(data, s.state, s.runtime())
	if action.Kind == ActionIgnore {
		s.handleIgnoreInline(data)
		return
	}
	s.commit(action)
	s.drainSyncResult()
}

func (s *QuestionnaireSession) commit(action QuestionnaireAction) {
	result := Reduce(s.state, action, s.applyContext())
	s.state = result.State
	for _, effect := range result.Effects {
		s.runEffect(effect)
	}
	s.state = s.mirrorNotesDraft(s.state)
	s.viewAdapter.Apply(s.state)
}

func (s *QuestionnaireSession) mirrorNotesDraft(st QuestionnaireState) QuestionnaireState {
	// Drafts restore through Editor.SetText, which clears the backing paste map: read expanded.
	draft := s.notesInput.GetExpandedText()
	if st.NotesDraft == draft {
		return st
	}
	st.NotesDraft = draft
	return st
}

func (s *QuestionnaireSession) runEffect(effect Effect) {
	switch effect.Kind {
	case EffectSetInputBuffer:
		s.inlineInput.SetText(effect.Value)
	case EffectClearInputBuffer:
		s.inlineInput.SetText("")
	case EffectOpenInputEditor:
		s.openInputEditorAsync(effect.Value)
	case EffectSetNotesValue:
		s.notesInput.SetText(effect.Value)
	case EffectSetNotesFocused:
		s.notesInput.Focused = effect.Focused
	case EffectForwardNotesKeystroke:
		s.notesInput.HandleInput(effect.Data)
	case EffectSetOverlayHidden:
		// No-op until SetOverlayHandle has been called, and suppressed entirely when no raw terminal listener
		// exists: hiding would then be irreversible.
		if s.canReopen && s.overlayHandle != nil {
			s.overlayHandle.SetHidden(effect.Hidden)
		}
	case EffectDone:
		s.done(effect.Result)
	}
}

// openInputEditorAsync opens the external editor; on success the replacement buffer is committed, on reported
// failure the draft is retained.
func (s *QuestionnaireSession) openInputEditorAsync(value string) {
	if s.inputEditorOpen {
		return
	}
	s.inputEditorOpen = true
	called := false
	s.inEditInput = true
	s.syncResult = nil
	s.editInput(value, func(edited string, ok bool) {
		s.finishEdit(&called, edited, ok)
	})
	s.inEditInput = false
}

func (s *QuestionnaireSession) finishEdit(called *bool, edited string, ok bool) {
	// The caller may be on another goroutine; a synchronous call from inside EditInput already holds the lock.
	if s.inEditInput {
		if !*called {
			*called = true
			s.syncResult = &editResult{edited, ok}
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if *called {
		return
	}
	*called = true
	s.applyEditResult(edited, ok)
}

func (s *QuestionnaireSession) applyEditResult(edited string, ok bool) {
	s.inputEditorOpen = false
	if ok {
		s.commit(QuestionnaireAction{Kind: ActionInputReplace, Value: edited})
	}
}

// drainSyncResult applies a result delivered synchronously by EditInput, after the dispatch that opened it.
func (s *QuestionnaireSession) drainSyncResult() {
	if r := s.syncResult; r != nil {
		s.syncResult = nil
		s.applyEditResult(r.edited, r.ok)
	}
}

// handleIgnoreInline is the per-keystroke ignore fast path: it delegates text editing to the headless Editor
// and projects its text/cursor state without a reducer round-trip.
func (s *QuestionnaireSession) handleIgnoreInline(data string) {
	if !s.state.InputMode {
		return
	}
	s.inlineInput.HandleInput(data)
	s.viewAdapter.Apply(s.state)
}

func (s *QuestionnaireSession) runtime() QuestionnaireRuntime {
	cursorLine, _ := s.inlineInput.GetCursor()
	lastLine := len(s.inlineInput.GetLines()) - 1
	var items []WrappingSelectItem
	if s.state.CurrentTab >= 0 && s.state.CurrentTab < len(s.itemsByTab) {
		items = s.itemsByTab[s.state.CurrentTab]
	}
	return QuestionnaireRuntime{
		Keybindings:      s.keybindings,
		InputBuffer:      s.inlineInput.GetExpandedText(),
		CanMoveInputUp:   cursorLine > 0,
		CanMoveInputDown: cursorLine < lastLine,
		Questions:        s.questions,
		IsMulti:          s.isMulti,
		CurrentItem:      s.currentItem(),
		Items:            items,
		CollapseKey:      s.collapseKey,
	}
}

func (s *QuestionnaireSession) applyContext() ApplyContext {
	return ApplyContext{Questions: s.questions, ItemsByTab: s.itemsByTab}
}

func (s *QuestionnaireSession) currentItem() *WrappingSelectItem {
	var arr []WrappingSelectItem
	if s.state.CurrentTab >= 0 && s.state.CurrentTab < len(s.itemsByTab) {
		arr = s.itemsByTab[s.state.CurrentTab]
	}
	if s.state.OptionIndex >= 0 && s.state.OptionIndex < len(arr) {
		return &arr[s.state.OptionIndex]
	}
	return nil
}

// SetOverlayHandle is called once the TUI has created the overlay. Until then set_overlay_hidden effects are
// no-ops; the session still tracks state.Collapsed for the view layer.
func (s *QuestionnaireSession) SetOverlayHandle(handle OverlayHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overlayHandle = handle
}

// ToggleCollapsedExternal is the public toggle used by the raw terminal input listener.
func (s *QuestionnaireSession) ToggleCollapsedExternal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.inputEditorOpen {
		s.commit(QuestionnaireAction{Kind: ActionToggleCollapsed})
		s.drainSyncResult()
	}
}
