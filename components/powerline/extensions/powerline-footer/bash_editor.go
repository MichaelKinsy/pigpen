package powerline_footer

import (
	"context"
	"strings"
	"sync"
	"time"
)

// BashModeEditor's input handling over the SDK's editor. upstream: bash-mode/editor.ts (handleInput 213-352, render 483-513,
// the ghost and history helpers). The logic it needs is in bash_editor_logic.go; this is the part that drives a host editor.
//
// Differences forced by the SDK, all documented in PORT.md:
//   - Keys are compared with Pi's default key bindings (piKeyDefaults); the SDK cannot read the user's bindings (E10).
//   - The fast ASCII line edits (tryFastPrintableInput, tryFastAsciiBackspace, tryFastAsciiForwardDelete,
//     tryFastAsciiHorizontalMove) rewrite the editor's private state to save time; the default editor produces the same text
//     and cursor from the same key, so the keys go to it.
//   - Prompt-history recall (up and down outside bash mode) belongs to the default editor, which already handles it; the draft
//     Pi restores when history browsing ends is the default editor's own. The paste state (isInPaste) is followed here from the
//     bracketed paste markers of the chunks.
//   - The editor boundary shortcuts cannot place the cursor, so they send the default editor's ctrl+home and ctrl+end.
//   - The ghost suggestion is resolved while the key is handled (bounded by ghostSyncTimeout, instead of a 50 ms debounce and a
//     later render), because the SDK gives an editor no way to ask for a render when an asynchronous result arrives.

const (
	// ghostSyncTimeout bounds how long a key waits for its ghost suggestion.
	ghostSyncTimeout = 100 * time.Millisecond
	// The default editor's own keys for the document start and end (pi-tui's ctrl+home and ctrl+end), and its undo (ctrl+-).
	keyDocumentStart = "\x1b[1;5H"
	keyDocumentEnd   = "\x1b[1;5F"
	keyUndo          = "\x1f"
)

// piKeyDefaults are Pi's default bindings of the keybinding ids the editor tests (TUI_KEYBINDINGS and the app bindings).
var piKeyDefaults = map[string][]string{
	"app.interrupt":                 {"escape"},
	"app.clear":                     {"ctrl+c"},
	"tui.editor.cursorUp":           {"up"},
	"tui.editor.cursorDown":         {"down"},
	"tui.editor.cursorLeft":         {"left", "ctrl+b"},
	"tui.editor.cursorRight":        {"right", "ctrl+f"},
	"tui.input.tab":                 {"tab"},
	"tui.input.submit":              {"enter"},
	"tui.input.newLine":             {"shift+enter", "ctrl+j"},
	"tui.editor.deleteCharBackward": {"backspace"},
	"tui.editor.deleteCharForward":  {"delete", "ctrl+d"},
	"tui.editor.deleteWordBackward": {"ctrl+w", "alt+backspace"},
	"tui.editor.deleteWordForward":  {"alt+d", "alt+delete"},
	"tui.editor.deleteToLineStart":  {"ctrl+u"},
	"tui.editor.deleteToLineEnd":    {"ctrl+k"},
	"app.message.followUp":          {"alt+enter"},
}

// piKeyMatches is keybindings.matches(data, id) at Pi's defaults.
func piKeyMatches(data, id string) bool {
	for _, key := range piKeyDefaults[id] {
		if matchesKey(data, key) {
			return true
		}
	}
	return false
}

// bashEditorBase is the default editor as the bash editor uses it; *sdk.Editor implements it.
type bashEditorBase interface {
	editorBase
	GetText() (string, error)
	GetExpandedText() (string, error)
	SetText(text string) error
	InsertTextAtCursor(text string) error
}

// bashEditorEnv is BashModeEditorOptions.
type bashEditorEnv struct {
	active, running            func() bool
	completions                func() bool
	boundaryStart, boundaryEnd func() *string
	notify                     func(message, level string)
	submit                     func(command string)
	exitMode, interrupt        func()
	resolveGhost               func(ctx context.Context, text string) *ghostSuggestion
	historyEntries             func(prefix string) []string
}

type bashModeEditor struct {
	base bashEditorBase
	env  bashEditorEnv

	mu      sync.Mutex
	history *shellHistoryBrowser
	ghost   *ghostSuggestion

	// inPaste is Pi's isInPaste: a bracketed paste has started and its end marker has not arrived yet. Only the editor's input
	// goroutine reads it.
	inPaste bool
}

func newBashModeEditor(base bashEditorBase, env bashEditorEnv) *bashModeEditor {
	return &bashModeEditor{base: base, env: env, history: newShellHistoryBrowser()}
}

// resetHistory is resetShellHistoryBrowse.
func (e *bashModeEditor) resetHistory() {
	e.mu.Lock()
	e.history.reset()
	e.mu.Unlock()
}

// clearGhost is clearGhostSuggestion.
func (e *bashModeEditor) clearGhost() {
	e.mu.Lock()
	e.ghost = nil
	e.mu.Unlock()
}

// dismissBashModeUi is dismissBashModeUi: the history browse and the ghost go. (The default editor's autocomplete popup is not
// reachable from here.)
func (e *bashModeEditor) dismissBashModeUi() {
	e.resetHistory()
	e.clearGhost()
}

// refreshGhostSuggestion is refreshGhostSuggestion.
func (e *bashModeEditor) refreshGhostSuggestion() {
	if e.env.completions() {
		e.scheduleGhostUpdate()
	} else {
		e.clearGhost()
	}
}

// scheduleGhostUpdate resolves the ghost for the editor's text now, within ghostSyncTimeout.
func (e *bashModeEditor) scheduleGhostUpdate() {
	if !e.env.completions() {
		e.clearGhost()
		return
	}
	text, err := e.base.GetText()
	if err != nil {
		e.clearGhost()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghostSyncTimeout)
	defer cancel()
	ghost := e.env.resolveGhost(ctx, text)
	if ctx.Err() != nil {
		ghost = nil
	}
	e.mu.Lock()
	e.ghost = ghost
	e.mu.Unlock()
}

// isOneOffBashCommandContext: the editor's first line starts with "!".
func (e *bashModeEditor) isOneOffBashCommandContext() bool {
	lines, err := e.base.GetLines()
	return err == nil && len(lines) > 0 && isOneOffBashCommandLine(lines[0])
}

// isShellCompletionContext: completions are on and the editor is in bash mode or holds a one-off command.
func (e *bashModeEditor) isShellCompletionContext() bool {
	return e.env.completions() && (e.env.active() || e.isOneOffBashCommandContext())
}

// afterEdit is the tail every edit shares: while a shell command is being typed the ghost follows the text, otherwise it goes.
func (e *bashModeEditor) afterEdit() {
	if e.isShellCompletionContext() {
		e.scheduleGhostUpdate()
	} else {
		e.clearGhost()
	}
}

// acceptGhost is acceptGhostSuggestion: the ghost becomes the text when it extends it at the end of a one-line draft.
func (e *bashModeEditor) acceptGhost() bool {
	e.mu.Lock()
	ghost := e.ghost
	e.mu.Unlock()
	if ghost == nil {
		return false
	}
	text, err := e.base.GetText()
	if err != nil {
		return false
	}
	cursor, err := e.base.GetCursor()
	if err != nil {
		return false
	}
	accepted, ok := acceptGhostSuggestion(ghost, text, cursor.Line, cursor.Col)
	if !ok {
		return false
	}
	if e.base.SetText(accepted) != nil {
		return false
	}
	e.clearGhost()
	return true
}

// navigateShellHistory is navigateShellHistory.
func (e *bashModeEditor) navigateShellHistory(direction int) error {
	expanded, err := e.base.GetExpandedText()
	if err != nil {
		return err
	}
	e.mu.Lock()
	step := e.history.navigate(direction, expanded, e.env.historyEntries)
	e.mu.Unlock()
	if step.Notify != "" {
		e.env.notify(step.Notify, "info")
		return nil
	}
	if step.SetText != nil {
		if err := e.base.SetText(*step.SetText); err != nil {
			return err
		}
	}
	if step.ClearGhost {
		e.clearGhost()
	}
	if step.ScheduleGhost {
		e.scheduleGhostUpdate()
	}
	return nil
}

// handleInput is BashModeEditor.handleInput.
func (e *bashModeEditor) handleInput(data string) error {
	if text, ok := droppedPathTextFromInput(data); ok {
		if err := e.base.InsertTextAtCursor(text); err != nil {
			return err
		}
		e.resetHistory()
		e.afterEdit()
		return nil
	}
	paste := e.inPaste || strings.Contains(data, "\x1b[200~")
	if paste {
		if err := e.base.HandleInput(data); err != nil {
			return err
		}
		switch {
		case strings.Contains(data, "\x1b[201~"):
			e.inPaste = false
		case strings.Contains(data, "\x1b[200~"):
			e.inPaste = true
		}
		if e.inPaste {
			return nil // the paste goes on in the next chunk
		}
	} else {
		handled, err := e.handleKey(data)
		if err != nil || handled {
			return err
		}
		if err := e.base.HandleInput(data); err != nil {
			return err
		}
	}
	if !e.isShellCompletionContext() {
		e.resetHistory()
		e.clearGhost()
		return nil
	}
	if paste || isPrintableInput(data) ||
		piKeyMatches(data, "tui.editor.deleteCharBackward") || piKeyMatches(data, "tui.editor.deleteCharForward") ||
		piKeyMatches(data, "tui.editor.deleteWordBackward") || piKeyMatches(data, "tui.editor.deleteWordForward") ||
		piKeyMatches(data, "tui.editor.deleteToLineStart") || piKeyMatches(data, "tui.editor.deleteToLineEnd") ||
		piKeyMatches(data, "tui.input.newLine") || piKeyMatches(data, "tui.editor.cursorLeft") || piKeyMatches(data, "tui.editor.cursorRight") {
		e.resetHistory()
		e.scheduleGhostUpdate()
	}
	return nil
}

// handleKey is the branch of handleInput that is not a paste; it reports whether the key was taken.
func (e *bashModeEditor) handleKey(data string) (bool, error) {
	active := e.env.active()
	// The default editor has no undo for a key it does not know; Cmd+Z is its ctrl+-.
	if isCommandUndoShortcut(data) {
		if err := e.base.HandleInput(keyUndo); err != nil {
			return true, err
		}
		e.resetHistory()
		e.afterEdit()
		return true, nil
	}
	if active && piKeyMatches(data, "app.interrupt") {
		e.env.exitMode()
		return true, nil
	}
	if active && piKeyMatches(data, "app.clear") && e.env.running() {
		e.env.interrupt()
		return true, nil
	}
	if active && piKeyMatches(data, "tui.editor.cursorUp") {
		return true, e.navigateShellHistory(-1)
	}
	if active && piKeyMatches(data, "tui.editor.cursorDown") {
		return true, e.navigateShellHistory(1)
	}
	if !isKeyRelease(data) {
		if matchesConfiguredShortcut(data, e.env.boundaryStart()) {
			return true, e.base.HandleInput(keyDocumentStart)
		}
		if matchesConfiguredShortcut(data, e.env.boundaryEnd()) {
			return true, e.base.HandleInput(keyDocumentEnd)
		}
	}
	if piKeyMatches(data, "tui.input.tab") && (active || e.isOneOffBashCommandContext()) {
		e.acceptGhost()
		return true, nil
	}
	if piKeyMatches(data, "tui.editor.cursorRight") && (active || e.isOneOffBashCommandContext()) && e.acceptGhost() {
		return true, nil
	}
	if active && piKeyMatches(data, "tui.input.submit") && !piKeyMatches(data, "tui.input.newLine") {
		return true, e.submitCommand()
	}
	return false, nil
}

// submitCommand runs the editor's text as a shell command.
func (e *bashModeEditor) submitCommand() error {
	if e.env.running() {
		e.env.notify("Shell command already running", "warning")
		return nil
	}
	expanded, err := e.base.GetExpandedText()
	if err != nil {
		return err
	}
	command := jsTrim(expanded)
	if command == "" {
		return nil
	}
	e.clearGhost()
	e.resetHistory()
	e.env.submit(command)
	if err := e.base.SetText(""); err != nil {
		return err
	}
	e.refreshGhostSuggestion()
	return nil
}

// renderLines is BashModeEditor.render's tail: the ghost suffix drawn on the content row of the rows the default editor drew.
func (e *bashModeEditor) renderLines(lines []string, width int) []string {
	e.mu.Lock()
	ghost := e.ghost
	e.mu.Unlock()
	if ghost == nil || len(lines) < 3 || !e.isShellCompletionContext() {
		return lines
	}
	text, err := e.base.GetText()
	if err != nil {
		return lines
	}
	cursor, err := e.base.GetCursor()
	if err != nil {
		return lines
	}
	return renderGhostLine(lines, width, text, ghost, cursor.Line, cursor.Col, true)
}

// ghostBase is the default editor whose rows carry the bash editor's ghost, what the chrome lays out.
type ghostBase struct {
	editorBase
	bash *bashModeEditor
}

func (g ghostBase) Render(width int) ([]string, error) {
	lines, err := g.editorBase.Render(width)
	if err != nil {
		return lines, err
	}
	return g.bash.renderLines(lines, width), nil
}
