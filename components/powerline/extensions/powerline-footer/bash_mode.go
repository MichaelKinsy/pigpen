package powerline_footer

import (
	"context"
	"os"
	"strings"
	"sync"
)

// Bash mode: the sticky shell mode's state and the commands that drive it (/bash-mode, /bash-reset), the transcript widget's
// rows and the shell_mode segment's data. upstream: index.ts 1240-1243 (state), 1280-1281, 1354-1420 (setBashModeActive,
// runShellCommand), 2683-2737 (the commands), 2935-2938 (segment data), 3060-3090 (renderBashTranscriptLines).
//
// Bash mode needs the bash editor (a replacement of Pi's input editor, installed through ctx.ui.setEditorComponent). The Go
// SDK offers that since PiG v0.5.0 and the editor is the next slice; until it lands the user cannot type into the mode, so the extension keeps /bash-mode, /bash-reset and the
// bashMode setting refused (refusal.go) and does not construct a bashMode. This type is the complete logic behind them, tested
// on its own, so the editor lands on top of it: its editor hooks are the two functions of bashEditorHooks.

// bashEditorHooks are what the editor offers the mode: upstream's currentEditor?.dismissBashModeUi?.() and
// currentEditor?.refreshGhostSuggestion?.(). Either may be nil (no editor installed yet).
type bashEditorHooks struct {
	dismissBashModeUi      func()
	refreshGhostSuggestion func()
}

// bashModeEnv is what the mode needs from the extension. All members are required.
type bashModeEnv struct {
	// Cwd is the session's working directory (currentCtx?.cwd ?? process.cwd()).
	Cwd func() string
	// ShellPath is process.env.SHELL || "/bin/sh".
	ShellPath func() string
	// RequestStatusRender asks the bar to repaint; it must not block (the shell's output goroutines call it).
	RequestStatusRender func()
	// Notify is ctx.ui.notify.
	Notify func(message, level string)
}

func defaultShellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

type bashMode struct {
	env    bashModeEnv
	editor bashEditorHooks

	mu         sync.Mutex
	limits     bashTranscriptLimits
	active     bool
	transcript *bashTranscriptStore
	engine     *bashCompletionEngine
	session    *managedShellSession
}

func newBashMode(env bashModeEnv, limits bashTranscriptLimits) *bashMode {
	return &bashMode{env: env, limits: limits, transcript: newBashTranscriptStore(limits), engine: &bashCompletionEngine{}}
}

// isActive is bashModeActive.
func (b *bashMode) isActive() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active
}

// isShellRunning is shellSession?.state.running ?? false.
func (b *bashMode) isShellRunning() bool {
	b.mu.Lock()
	session := b.session
	b.mu.Unlock()
	return session != nil && session.State().Running
}

// sessionStart upstream: index.ts:1831-1867 (session_start): the old shell goes, the mode is off, the transcript and the
// completion engine are new, with the settings read again.
func (b *bashMode) sessionStart(limits bashTranscriptLimits) {
	b.mu.Lock()
	old := b.session
	b.session, b.active, b.limits = nil, false, limits
	b.transcript, b.engine = newBashTranscriptStore(limits), &bashCompletionEngine{}
	b.mu.Unlock()
	if old != nil {
		old.dispose()
	}
}

// sessionShutdown upstream: index.ts:1909-1915 (session_shutdown): the shell goes and the mode is off.
func (b *bashMode) sessionShutdown() {
	b.mu.Lock()
	old := b.session
	b.session, b.active = nil, false
	b.mu.Unlock()
	if old != nil {
		old.dispose()
	}
}

// powerlineDisabled upstream: index.ts:2593-2598 (/powerline toggled off): the shell goes, the transcript is cleared, the mode is off.
func (b *bashMode) powerlineDisabled() {
	b.mu.Lock()
	old := b.session
	b.session, b.active = nil, false
	transcript := b.transcript
	b.mu.Unlock()
	if old != nil {
		old.dispose()
	}
	transcript.clear()
}

// ensureShellSession upstream: index.ts:1364.
func (b *bashMode) ensureShellSession(ctx context.Context) (*managedShellSession, error) {
	b.mu.Lock()
	session := b.session
	if session == nil {
		cwd := b.env.Cwd()
		created, err := newManagedShellSession(b.env.ShellPath(), cwd, b.transcript, b.env.RequestStatusRender,
			func(command, shellCwd string) { appendProjectHistory(b.env.Cwd(), command, shellCwd) })
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
		b.session, session = created, created
	}
	b.mu.Unlock()
	if err := session.ensureReady(ctx); err != nil {
		return nil, err
	}
	return session, nil
}

// runShellCommand upstream: index.ts:1378. A failure is a notification, not an error.
func (b *bashMode) runShellCommand(ctx context.Context, command string) {
	session, err := b.ensureShellSession(ctx)
	if err == nil {
		err = session.runCommand(ctx, command)
	}
	if err != nil {
		b.env.Notify("Failed to run shell command: "+err.Error(), "error")
		return
	}
	b.env.RequestStatusRender()
}

// setBashModeActive upstream: index.ts:1389.
func (b *bashMode) setBashModeActive(ctx context.Context, value bool) {
	b.mu.Lock()
	current, session := b.active, b.session
	b.mu.Unlock()
	if value == current {
		return
	}
	if !value && session != nil && session.State().Running {
		b.env.Notify("Wait for the current shell command to finish before leaving bash mode", "warning")
		return
	}

	if value {
		session, err := b.ensureShellSession(ctx)
		if err != nil {
			b.mu.Lock()
			old := b.session
			b.session, b.active = nil, false
			b.mu.Unlock()
			if old != nil {
				old.dispose()
			}
			b.env.RequestStatusRender()
			b.env.Notify("Failed to start shell session: "+err.Error(), "error")
			return
		}
		b.mu.Lock()
		b.active = true
		b.mu.Unlock()
		if b.editor.dismissBashModeUi != nil {
			b.editor.dismissBashModeUi()
		}
		if b.editor.refreshGhostSuggestion != nil {
			b.editor.refreshGhostSuggestion()
		}
		b.env.RequestStatusRender()
		b.env.Notify("Bash mode enabled ("+session.State().ShellName+")", "info")
		return
	}

	b.mu.Lock()
	b.active = value
	b.mu.Unlock()
	if b.editor.dismissBashModeUi != nil {
		b.editor.dismissBashModeUi()
	}
	b.env.RequestStatusRender()
	b.env.Notify("Bash mode disabled", "info")
}

// handleBashModeCommand is the /bash-mode handler: on, off or toggle (the default). upstream: index.ts:2683.
func (b *bashMode) handleBashModeCommand(ctx context.Context, args string) {
	mode := strings.ToLower(jsTrim(args))
	if mode == "" {
		mode = "toggle"
	}
	switch mode {
	case "on":
		b.setBashModeActive(ctx, true)
	case "off":
		b.setBashModeActive(ctx, false)
	case "toggle":
		b.setBashModeActive(ctx, !b.isActive())
	default:
		b.env.Notify("Usage: /bash-mode [on|off|toggle]", "warning")
	}
}

// handleBashResetCommand is the /bash-reset handler: the shell is replaced, the transcript emptied, and in bash mode a new shell
// is started right away. upstream: index.ts:2719.
func (b *bashMode) handleBashResetCommand(ctx context.Context) {
	b.mu.Lock()
	old := b.session
	b.session = nil
	active := b.active
	transcript := b.transcript
	b.mu.Unlock()
	if old != nil {
		old.dispose()
	}
	transcript.clear()
	if active {
		if _, err := b.ensureShellSession(ctx); err != nil {
			b.mu.Lock()
			b.active = false
			b.mu.Unlock()
			b.env.Notify("Failed to restart shell session: "+err.Error(), "error")
			b.env.RequestStatusRender()
			return
		}
	}
	b.env.RequestStatusRender()
	b.env.Notify("Bash session reset", "info")
}

// shellCwd is getShellCwd: the shell's directory, else the session's.
func (b *bashMode) shellCwd() string {
	b.mu.Lock()
	session := b.session
	b.mu.Unlock()
	if session != nil {
		return session.State().Cwd
	}
	return b.env.Cwd()
}

// shellHistoryEntries upstream: index.ts:1354: the project's commands then the user's global history, each matching prefix,
// without repeats.
func (b *bashMode) shellHistoryEntries(prefix string) []string {
	var commands []string
	for _, e := range readProjectHistory(b.env.Cwd()) {
		commands = append(commands, e.Command)
	}
	project := matchHistoryEntries(commands, prefix, 50)
	global := matchHistoryEntries(readGlobalShellHistory(b.env.ShellPath()), prefix, 50)
	seen := map[string]bool{}
	out := []string{}
	for _, v := range append(project, global...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// resolveGhostSuggestion upstream: index.ts:3253: the editor's ghost source. A one-off "!cmd" line gets the ghost for the
// command with its bang prefix put back.
func (b *bashMode) resolveGhostSuggestion(ctx context.Context, text string) *ghostSuggestion {
	b.mu.Lock()
	engine := b.engine
	b.mu.Unlock()
	if oneOff := getOneOffBashCommandContext(text); oneOff != nil {
		ghost := engine.getGhostSuggestion(ctx, oneOff.Command, b.shellCwd(), b.env.ShellPath())
		if ghost == nil {
			return nil
		}
		return &ghostSuggestion{Value: oneOff.Prefix + ghost.Value, Source: ghost.Source}
	}
	return engine.getGhostSuggestion(ctx, text, b.shellCwd(), b.env.ShellPath())
}

// shellSegmentData upstream: index.ts:2935-2938: what the shell_mode and path segments read.
type shellSegmentData struct {
	Active  bool
	Running bool
	Name    string // "" when there is no shell yet
	Cwd     string // "" when there is no shell yet
}

func (b *bashMode) segmentData() shellSegmentData {
	b.mu.Lock()
	active, session := b.active, b.session
	b.mu.Unlock()
	data := shellSegmentData{Active: active}
	if session != nil {
		st := session.State()
		data.Running, data.Name, data.Cwd = st.Running, st.ShellName, st.Cwd
	}
	return data
}

// bashCollapseWhitespace is text.replace(/\s+/g, " ") followed by trim().
func bashCollapseWhitespace(text string) string {
	return strings.Join(strings.FieldsFunc(text, isJSSpace), " ")
}

// renderBashTranscriptLines is the content of the powerline-bash-transcript widget: the last four commands with their prompt,
// status and last six output lines, at most 16 rows, nothing outside bash mode. upstream: index.ts:3060.
func (b *bashMode) renderBashTranscriptLines(width int, th theme) ([]string, error) {
	b.mu.Lock()
	active, session, transcript := b.active, b.session, b.transcript
	b.mu.Unlock()
	if !active {
		return nil, nil
	}
	snapshot := transcript.getSnapshot()
	if len(snapshot.Commands) == 0 {
		return nil, nil
	}
	var lines []string
	if snapshot.TruncatedCommands > 0 {
		plural := "s"
		if snapshot.TruncatedCommands == 1 {
			plural = ""
		}
		text, err := th.Fg("dim", "… "+bashItoa(snapshot.TruncatedCommands)+" earlier command"+plural+" truncated")
		if err != nil {
			return nil, err
		}
		lines = append(lines, " "+text)
	}
	shellName := "shell"
	if session != nil {
		shellName = session.State().ShellName
	}
	promptGlyph := "$"
	if shellName == "fish" {
		promptGlyph = ">"
	}
	commands := snapshot.Commands
	if len(commands) > 4 {
		commands = commands[len(commands)-4:]
	}
	for _, command := range commands {
		var status string
		var err error
		switch {
		case command.ExitCode == nil:
			status, err = th.Fg("accent", "running")
		case *command.ExitCode == 0:
			status, err = th.Fg("success", "ok")
		default:
			status, err = th.Fg("error", "exit "+bashItoa(*command.ExitCode))
		}
		if err != nil {
			return nil, err
		}
		glyph, err := th.Fg("accent", promptGlyph)
		if err != nil {
			return nil, err
		}
		open, err := th.Fg("dim", "(")
		if err != nil {
			return nil, err
		}
		closing, err := th.Fg("dim", ")")
		if err != nil {
			return nil, err
		}
		commandLine := truncateToWidth(bashCollapseWhitespace(command.Command), max(8, width-8), "…")
		lines = append(lines, " "+glyph+" "+commandLine+" "+open+status+closing)

		output := command.Output
		if len(output) > 6 {
			output = output[len(output)-6:]
		}
		for _, outputLine := range output {
			lines = append(lines, "   "+truncateToWidth(outputLine, max(1, width-3), "…"))
		}
	}
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	return lines, nil
}
