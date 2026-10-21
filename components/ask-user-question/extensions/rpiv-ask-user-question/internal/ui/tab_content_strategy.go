package ui

import (
	"strconv"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

const (
	notesHeader       = "Notes:"
	globalNotesHeader = "Global note:"
	reviewGlobalHint  = "n to add a note"
	reviewNoteLabel   = "Note"

	noInputPlaceholder = "(no input)"
)

// formatAnswerScalar formats a QuestionAnswer to its scalar string form (the "summary" variant). upstream:
// tool/format-answer.ts formatAnswerScalar.
func formatAnswerScalar(a QuestionAnswer) string {
	switch a.Kind {
	case "multi":
		if len(a.Selected) > 0 {
			return strings.Join(a.Selected, ", ")
		}
		return noInputPlaceholder
	case "custom":
		if a.Answer != nil && *a.Answer != "" {
			return *a.Answer
		}
		return noInputPlaceholder
	default: // "option"
		if a.Answer != nil {
			return *a.Answer
		}
		return noInputPlaceholder
	}
}

// oneLineClippedText is a single-row, width-clipped chrome cell. The footer row count is invariant
// (QuestionTabStrategy.footerRowCount = 2): pi-tui's Text word-wraps when the styled hint exceeds width,
// inflating that row count and desyncing the bodyHeight + footerRowCount math in DialogView.Render. Clipping with
// TruncateToWidth keeps the hint on one line; the collapse affordance falls off the right edge with `…`.
type oneLineClippedText struct {
	text        string
	paddingLeft int
}

func (o *oneLineClippedText) Render(width int) []string {
	pad := strings.Repeat(" ", o.paddingLeft)
	avail := max(0, width-o.paddingLeft)
	return []string{pad + widthx.TruncateToWidth(o.text, avail, "…", false)}
}

func (o *oneLineClippedText) Invalidate() {}

func (o *oneLineClippedText) HandleInput(string) {}

// TabContentStrategy is the per-tab content provider. Pure functional: closes over construction-time config;
// per-tick state threads through method args. The chrome wrapper enforces height equality across tabs via
// bodyHeight + footerRowCount.
type TabContentStrategy interface {
	// FooterRowCount is the total RENDERED footer rows; MUST equal what FooterRows actually emits.
	FooterRowCount() int
	// HeadingRows are the variable rows above the body, after top chrome (border + tabBar + Spacer).
	HeadingRows(state *DialogState) []tui.Component
	// BodyComponent is the Component placed at the body slot.
	BodyComponent(state *DialogState) tui.Component
	// BodyHeight is the natural rendered height of BodyComponent(state) at the given width.
	BodyHeight(width int, state *DialogState) int
	// MidRows are the optional rows between the body's trailing Spacer and the bottom border.
	MidRows(state *DialogState) []tui.Component
	// FooterRows are the footer rows below the bottom border; rendered row count MUST equal FooterRowCount.
	FooterRows(state *DialogState) []tui.Component
	// FocusedItemRowRange is the row range of the focused item within the body's rendered output; ok is false
	// when there is no interactive focus.
	FocusedItemRowRange(width int, state *DialogState) (start, end int, ok bool)
}

type QuestionTabStrategyConfig struct {
	Theme          Theme
	Questions      []QuestionData
	GetPreviewPane func() StatefulView[PreviewPaneProps]
	TabsByIndex    []TabComponents
	NotesInput     tui.Component
	IsMulti        bool
	// GetCurrentBodyHeight is the body height of the currently active tab/option.
	GetCurrentBodyHeight func(width int) int
	// CollapseKey is the resolved collapse key spec ("ctrl+]", "alt+o", or "off"); drives the footer's collapse hint.
	CollapseKey string
}

type QuestionTabStrategy struct {
	config QuestionTabStrategyConfig
}

func NewQuestionTabStrategy(config QuestionTabStrategyConfig) *QuestionTabStrategy {
	return &QuestionTabStrategy{config: config}
}

// FooterRowCount is Spacer(1) + oneLineClippedText(hint, 1) = 2 rendered rows.
func (s *QuestionTabStrategy) FooterRowCount() int { return 2 }

func (s *QuestionTabStrategy) question(state *DialogState) *QuestionData {
	if state.CurrentTab >= 0 && state.CurrentTab < len(s.config.Questions) {
		return &s.config.Questions[state.CurrentTab]
	}
	return nil
}

func (s *QuestionTabStrategy) multiSelect(state *DialogState) TabMultiSelect {
	if state.CurrentTab >= 0 && state.CurrentTab < len(s.config.TabsByIndex) {
		return s.config.TabsByIndex[state.CurrentTab].MultiSelect
	}
	return nil
}

func (s *QuestionTabStrategy) HeadingRows(state *DialogState) []tui.Component {
	out := []tui.Component{}
	question := s.question(state)
	// In multi-question mode the tab bar already shows the header; suppress the inline badge.
	if !s.config.IsMulti && question != nil && question.Header != "" {
		out = append(out, tui.NewText(s.config.Theme.Bg("selectedBg", " "+question.Header+" "), 1, 0, nil))
		out = append(out, tui.NewSpacer(1))
	}
	if question != nil {
		out = append(out, tui.NewText(s.config.Theme.Bold(question.Question), 1, 0, nil))
		out = append(out, tui.NewSpacer(1))
	}
	return out
}

func (s *QuestionTabStrategy) BodyComponent(state *DialogState) tui.Component {
	question := s.question(state)
	if mso := s.multiSelect(state); question != nil && question.MultiSelect && mso != nil {
		return mso
	}
	return s.config.GetPreviewPane()
}

func (s *QuestionTabStrategy) BodyHeight(width int, _ *DialogState) int {
	return s.config.GetCurrentBodyHeight(width)
}

func (s *QuestionTabStrategy) MidRows(state *DialogState) []tui.Component {
	if !state.NotesVisible {
		return []tui.Component{}
	}
	return []tui.Component{
		tui.NewText(s.config.Theme.Fg("muted", T("notes.header", notesHeader)), 1, 0, nil),
		s.config.NotesInput,
		tui.NewSpacer(1),
	}
}

// FooterRows uses oneLineClippedText (not pi-tui Text): BuildHintText includes the collapse affordance, pushing
// the rendered string past 80 columns; Text would wrap and break the footerRowCount = 2 invariant. Clipping on
// narrow terminals drops the trailing parts (collapse hint first, then cancel) with `…`.
func (s *QuestionTabStrategy) FooterRows(state *DialogState) []tui.Component {
	question := s.question(state)
	return []tui.Component{
		tui.NewSpacer(1),
		&oneLineClippedText{text: s.config.Theme.Fg("dim", BuildHintText(question, s.config.IsMulti, state, s.config.CollapseKey)), paddingLeft: 1},
	}
}

func (s *QuestionTabStrategy) FocusedItemRowRange(width int, state *DialogState) (int, int, bool) {
	question := s.question(state)
	if mso := s.multiSelect(state); question != nil && question.MultiSelect && mso != nil {
		a, b := mso.FocusedItemRowRange(width)
		return a, b, true
	}
	a, b := s.config.GetPreviewPane().(interface{ FocusedItemRowRange(int) (int, int) }).FocusedItemRowRange(width)
	return a, b, true
}

type SubmitTabStrategyConfig struct {
	Theme     Theme
	Questions []QuestionData
	// SubmitPicker is nil where the original has undefined.
	SubmitPicker tui.Component
	// NotesInput is the shared notes Editor, mounted into MidRows while the global-note editor is open on this tab.
	NotesInput tui.Component
}

type SubmitTabStrategy struct {
	config SubmitTabStrategyConfig
}

func NewSubmitTabStrategy(config SubmitTabStrategyConfig) *SubmitTabStrategy {
	return &SubmitTabStrategy{config: config}
}

// FooterRowCount is Spacer(1) + Text(prompt, 1) + submitPicker(2) + oneLineClippedText(hint, 1) = 5 rendered
// rows. The fallback path lands at 5 via 2 Spacer(1)s in the picker slot.
func (s *SubmitTabStrategy) FooterRowCount() int { return 5 }

func (s *SubmitTabStrategy) HeadingRows(*DialogState) []tui.Component {
	return []tui.Component{
		tui.NewText(s.config.Theme.Bold(s.config.Theme.Fg("accent", T("review.heading", ReviewHeading))), 1, 0, nil),
		tui.NewSpacer(1),
	}
}

func (s *SubmitTabStrategy) BodyComponent(state *DialogState) tui.Component {
	c := tui.NewContainer()
	th := s.config.Theme
	for i, q := range s.config.Questions {
		a, ok := state.Answers[i]
		if !ok {
			continue
		}
		label := "Q" + strconv.Itoa(i+1)
		if q.Header != "" {
			label = q.Header
		}
		answerText := formatAnswerScalar(a)
		c.AddChild(tui.NewText(th.Fg("muted", " ● "+label), 1, 0, nil))
		c.AddChild(tui.NewText("   "+th.Fg("muted", "→")+" "+th.Fg("text", answerText), 1, 0, nil))
		if a.Notes != "" {
			c.AddChild(tui.NewText(th.Fg("dim", "     notes: "+a.Notes), 1, 0, nil))
		}
	}
	// Committed global note (#182) as a review entry. Same presence predicate as the reducer's doneFor lift.
	// Hidden while the editor is open: the MidRows editor (seeded with this text) is the live surface then.
	globalNote := state.NotesByTab[len(s.config.Questions)]
	if !state.NotesVisible && globalNote != "" {
		c.AddChild(tui.NewText(th.Fg("muted", " ● "+T("review.note_label", reviewNoteLabel)), 1, 0, nil))
		c.AddChild(tui.NewText("   "+th.Fg("muted", "→")+" "+th.Fg("text", globalNote), 1, 0, nil))
	}
	return c
}

func (s *SubmitTabStrategy) BodyHeight(width int, state *DialogState) int {
	return len(s.BodyComponent(state).Render(width))
}

// MidRows is the notesVisible-gated mirror of QuestionTabStrategy.MidRows: while the global-note editor is
// open, the shared notes input mounts below the answer summary under its own header.
func (s *SubmitTabStrategy) MidRows(state *DialogState) []tui.Component {
	if !state.NotesVisible {
		return []tui.Component{}
	}
	return []tui.Component{
		tui.NewText(s.config.Theme.Fg("muted", T("notes.global_header", globalNotesHeader)), 1, 0, nil),
		s.config.NotesInput,
		tui.NewSpacer(1),
	}
}

func (s *SubmitTabStrategy) FooterRows(state *DialogState) []tui.Component {
	missing := []string{}
	for i, q := range s.config.Questions {
		if _, ok := state.Answers[i]; !ok {
			if q.Header != "" {
				missing = append(missing, q.Header)
			} else {
				missing = append(missing, "Q"+strconv.Itoa(i+1))
			}
		}
	}
	var promptText string
	if len(missing) == 0 {
		promptText = s.config.Theme.Fg("muted", T("review.ready", ReadyPrompt))
	} else {
		promptText = s.config.Theme.Fg("warning", T("review.incomplete", IncompleteWarningPrefix)+" "+strings.Join(missing, ", "))
	}
	out := []tui.Component{tui.NewSpacer(1), tui.NewText(promptText, 1, 0, nil)}
	if s.config.SubmitPicker != nil {
		out = append(out, s.config.SubmitPicker)
	} else {
		// Padding when the picker isn't wired: keeps the rendered row count at footerRowCount = 5.
		out = append(out, tui.NewSpacer(1), tui.NewSpacer(1))
	}
	// Bottom key-hint row, mirroring QuestionTabStrategy's footer idiom. Always present so footerRowCount stays
	// exactly 5.
	out = append(out, &oneLineClippedText{text: s.config.Theme.Fg("dim", BuildSubmitHintText(state)), paddingLeft: 1})
	return out
}

func (s *SubmitTabStrategy) FocusedItemRowRange(int, *DialogState) (int, int, bool) {
	return 0, 0, false
}

// BuildHintText builds the controls hint line. Order:
//
//	Enter · ↑/↓ [· Space toggle] [· n notes] [· Tab switch] · Esc [· <key> collapse] [· Shift+Enter newline] [· Ctrl+U clear]
//
// NOTES is part of the resting (notes-closed) core: it drops while the notes editor or custom-answer input has
// the keyboard. The collapse part interpolates the configured collapseKey (display-cased) and is omitted entirely
// when the shortcut is "off".
func BuildHintText(question *QuestionData, isMulti bool, state *DialogState, collapseKey string) string {
	parts := []string{T("hint.enter", HintPartEnter), T("hint.navigate", HintPartNav)}
	if question != nil && question.MultiSelect {
		parts = append(parts, T("hint.toggle", HintPartToggle))
	}
	if question != nil && !state.NotesVisible && !state.InputMode {
		parts = append(parts, T("hint.notes", HintPartNotes))
	}
	if isMulti {
		parts = append(parts, T("hint.tab", HintPartTab))
	}
	parts = append(parts, T("hint.cancel", HintPartCancel))
	if collapseKey != CollapseKeyOff {
		parts = append(parts, strings.Replace(T("hint.collapse", HintPartCollapseTemplate), KeyPlaceholder, FormatKeySpecForDisplay(collapseKey), 1))
	}
	if state.NotesVisible || state.InputMode {
		parts = append(parts, T("hint.newline", HintPartNewLine))
	}
	if state.InputMode {
		parts = append(parts, T("hint.clear", HintPartClear))
	}
	return strings.Join(parts, " · ")
}

// BuildSubmitHintText is the Submit-tab counterpart of BuildHintText. Resting: Enter · ↑/↓ · `n to add a note` ·
// Esc. While the global-note editor is open the note part drops and the Shift+Enter newline hint is appended
// after cancel.
func BuildSubmitHintText(state *DialogState) string {
	parts := []string{T("hint.enter", HintPartEnter), T("hint.navigate", HintPartNav)}
	if !state.NotesVisible {
		parts = append(parts, T("review.global_hint", reviewGlobalHint))
	}
	parts = append(parts, T("hint.cancel", HintPartCancel))
	if state.NotesVisible {
		parts = append(parts, T("hint.newline", HintPartNewLine))
	}
	return strings.Join(parts, " · ")
}
