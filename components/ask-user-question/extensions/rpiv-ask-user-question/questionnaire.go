package ask_user_question

import (
	"fmt"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// The interactive questionnaire: the session of internal/ui in a focused component over Context.Custom.
// upstream: ask-user-question.ts (makeSessionFactory, registerCollapseKeyListener, the custom() branch of execute).

// hostTUI is the TUI the session sees: the terminal size from the host and a render request that reaches the
// overlay through the SDK's invalidation callback (tui.requestRender).
type hostTUI struct {
	h          host
	mu         sync.Mutex
	invalidate func()
}

func (t *hostTUI) RequestRender() {
	t.mu.Lock()
	f := t.invalidate
	t.mu.Unlock()
	if f != nil {
		f()
	}
}

func (t *hostTUI) setInvalidate(f func()) {
	t.mu.Lock()
	t.invalidate = f
	t.mu.Unlock()
}

func (t *hostTUI) Columns() int {
	if w := t.h.Width(); w > 0 {
		return w
	}
	return 80
}

func (t *hostTUI) Rows() int {
	if r := t.h.Height(); r > 0 {
		return r
	}
	return 24
}

// questionnaireComponent is the sdk.RemoteComponent around a session: it renders the session's lines, feeds it
// the keystrokes the host routes to the overlay, and closes with the questionnaire's result.
type questionnaireComponent struct {
	tui     *hostTUI
	session *ui.QuestionnaireSession

	mu     sync.Mutex
	result *ui.QuestionnaireResult
}

func (c *questionnaireComponent) Render(width int) []string {
	return c.session.Component().Render(width)
}

// HandleInput routes one chunk to the session. A Kitty key release is dropped, as pi-tui does for a component
// that did not ask for releases (tui.js handleInput).
func (c *questionnaireComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if tui.IsKeyRelease(data) {
		return sdk.RemoteComponentResult{}, nil
	}
	c.session.Component().HandleInput(data)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.result == nil {
		return sdk.RemoteComponentResult{}, nil
	}
	return sdk.RemoteComponentResult{Done: true, Value: wireResult(*c.result)}, nil
}

// SetInvalidate lets the session ask for a render when it changes without input (sdk.RemoteComponentInvalidator).
func (c *questionnaireComponent) SetInvalidate(f func()) { c.tui.setInvalidate(f) }

func (c *questionnaireComponent) done(r ui.QuestionnaireResult) {
	c.mu.Lock()
	c.result = &r
	c.mu.Unlock()
}

// sessionRef is `SessionRef`: the session, once the component exists.
type sessionRef struct {
	mu      sync.Mutex
	session *ui.QuestionnaireSession
}

func (r *sessionRef) set(s *ui.QuestionnaireSession) { r.mu.Lock(); r.session = s; r.mu.Unlock() }
func (r *sessionRef) get() *ui.QuestionnaireSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

// overlayHandleRef is `OverlayHandleRef`.
type overlayHandleRef struct {
	mu     sync.Mutex
	handle ui.OverlayHandle
}

func (r *overlayHandleRef) set(h ui.OverlayHandle) { r.mu.Lock(); r.handle = h; r.mu.Unlock() }
func (r *overlayHandleRef) get() ui.OverlayHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handle
}

// newQuestionnaireComponent is makeSessionFactory: it constructs the session (capturing it in ref) and exposes
// its component.
func newQuestionnaireComponent(h host, typed questionParams, itemsByTab [][]ui.WrappingSelectItem, collapseKey string, canReopenWhileHidden bool, ref *sessionRef) *questionnaireComponent {
	c := &questionnaireComponent{tui: &hostTUI{h: h}}
	c.session = ui.NewQuestionnaireSession(ui.QuestionnaireSessionConfig{
		TUI:                  c.tui,
		Theme:                h.Theme(),
		Questions:            uiQuestions(typed),
		ItemsByTab:           itemsByTab,
		Done:                 c.done,
		Keybindings:          h.Keybindings(),
		EditInput:            editInput(h),
		CollapseKey:          collapseKey,
		CanReopenWhileHidden: canReopenWhileHidden,
	})
	ref.set(c.session)
	return c
}

// editInput is the session's external-editor hook. The original runs Pi's configured editor with the TUI stopped
// (state/external-editor.ts, ported as ui.EditWithExternalEditor); a subprocess extension cannot stop the host's
// TUI (the SDK lacks tui.stop/start), so the key reports the original's own failure path, a notification, and
// keeps the draft. The notification carries the Pigpen refusal text.
func editInput(h host) func(value string, finish func(edited string, ok bool)) {
	return func(_ string, finish func(edited string, ok bool)) {
		h.Notify(fmt.Sprintf("%s: %s", ui.T("editor.failed", "External editor failed"), unsupported("the external editor for custom answers")), "error")
		finish("", false)
	}
}

// registerCollapseKeyListener registers the raw terminal listener that toggles collapse while the overlay is
// hidden. It returns the remover, or nil when the key is off or the host has no raw input hook: the caller derives
// canReopenWhileHidden from that. upstream: registerCollapseKeyListener.
func registerCollapseKeyListener(h host, collapseKey string, session *sessionRef, handle *overlayHandleRef) func() {
	if collapseKey == collapseKeyOff {
		return nil
	}
	hasAnnouncedHide := false
	remove, supported := h.OnTerminalInput(func(data string) sdk.TerminalInputResult {
		hd := handle.get()
		if hd == nil {
			return sdk.TerminalInputResult{}
		}
		// Only act while the questionnaire is hidden (its handleInput is unreachable) or actually focused. When some
		// other overlay is on top (e.g. `/btw`), leave the keystroke to that overlay instead of toggling the
		// questionnaire from underneath it.
		if !hd.IsHidden() && !hd.IsFocused() {
			return sdk.TerminalInputResult{}
		}
		if !tui.MatchesKey(data, collapseKey) {
			return sdk.TerminalInputResult{}
		}
		// Kitty-protocol terminals report press, repeat, and release separately. Toggle only on the initial press so
		// a tap does not immediately reopen the overlay and a held key does not toggle it repeatedly.
		if tui.IsKeyRelease(data) || tui.IsKeyRepeat(data) {
			return sdk.TerminalInputResult{Consume: true}
		}
		if s := session.get(); s != nil {
			s.ToggleCollapsedExternal()
		}
		if hd.IsHidden() && !hasAnnouncedHide {
			hasAnnouncedHide = true
			h.Notify(fmt.Sprintf("ask_user_question hidden — press %s to reopen", ui.FormatKeySpecForDisplay(collapseKey)), "info")
		}
		return sdk.TerminalInputResult{Consume: true}
	})
	if !supported {
		return nil
	}
	return remove
}

// uiQuestion is one question in the questionnaire's own types.
func uiQuestion(q question) ui.QuestionData {
	d := ui.QuestionData{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
	for _, o := range q.Options {
		d.Options = append(d.Options, ui.OptionData{Label: o.Label, Description: o.Description, Preview: o.Preview})
	}
	return d
}

// uiQuestions is the questions in the questionnaire's own types.
func uiQuestions(p questionParams) []ui.QuestionData {
	out := make([]ui.QuestionData, 0, len(p.Questions))
	for _, q := range p.Questions {
		out = append(out, uiQuestion(q))
	}
	return out
}

// wireResult is the questionnaire's result as the tool's details know it; the SDK sends it to the host as JSON.
func wireResult(r ui.QuestionnaireResult) questionnaireResult {
	out := questionnaireResult{Cancelled: r.Cancelled, GlobalNote: r.GlobalNote, Error: r.Error}
	for _, a := range r.Answers {
		out.Answers = append(out.Answers, questionAnswer{
			QuestionIndex: a.QuestionIndex, Question: a.Question, Kind: a.Kind, Answer: a.Answer,
			Selected: a.Selected, HasSelected: a.HasSelected, Notes: a.Notes, Preview: a.Preview,
		})
	}
	return out
}
