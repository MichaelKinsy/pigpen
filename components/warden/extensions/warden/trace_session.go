package warden

import (
	"regexp"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Session-level trace plumbing: the optional trace file, the judgment state it records, and the toggle shortcut.

// reShortcut is the shape check that keeps a typo in the config file from being registered (extension.ts 2453).
var reShortcut = regexp.MustCompile(`(?i)^(?:(?:ctrl|shift|alt|super)\+)+[a-z0-9]+$|^f\d{1,2}$`)

// registerShortcut registers the trace sidebar's toggle once, at load, from the user file's widget.shortcut. An empty or
// malformed value registers nothing.
func (w *warden) registerShortcut(e *sdk.Extension) {
	cfg, _ := LoadConfig(DefaultHostDirs(nil).UserConfigPath())
	shortcut := cfg.Widget.Shortcut
	if !reShortcut.MatchString(shortcut) {
		return
	}
	e.Shortcut(shortcut, "Toggle the pi-warden trace sidebar", func(ctx sdk.Context) error {
		if ctx.HasUI() {
			w.toggleTrace(ctx, w.config(ctx), nil)
		}
		return nil
	})
}

// judgmentsOffReason says why no judge is available, or "" when one is (extension.ts judgmentsOffReason, for the port's
// backends: a refused backend, no consent for the configured one, the request budget, a missing TypeSafe key).
func (w *warden) judgmentsOffReason(cfg Config) JudgmentsOffReason {
	switch {
	case cfg.BackendRefusal != "":
		return JudgmentsBadBackend
	case cfg.Backend == BackendNone || cfg.Consent != cfg.Backend:
		return JudgmentsNoConsent
	}
	w.mu.Lock()
	exhausted := w.budgetExhausted
	w.mu.Unlock()
	switch {
	case exhausted:
		return JudgmentsBudget
	case cfg.Backend == BackendTypeSafe && w.opts.Getenv("TYPESAFE_API_KEY") == "":
		return JudgmentsNoKey
	}
	return ""
}

// noteJudgments records a change of the judgment state in the trace file (a no-op without one).
func (w *warden) noteJudgments(cfg Config) {
	w.mu.Lock()
	file := w.traceFile
	w.mu.Unlock()
	if file != nil {
		file.Judgments(JudgmentsStateOf(w.judgmentsOffReason(cfg)))
	}
}

// openSessionTrace runs at session start: it removes the panels of the previous session and, when
// PIGPEN_WARDEN_TRACE_DIR is set, opens the session's trace file and subscribes it to the trace.
func (w *warden) openSessionTrace(ctx sdk.Context) {
	w.mu.Lock()
	panels := []*panelRun{w.tracePanel, w.configPanel}
	w.tracePanel, w.configPanel = nil, nil
	if w.unsubTraceFile != nil {
		w.unsubTraceFile()
	}
	w.unsubTraceFile, w.traceFile = nil, nil
	unsubWidth := w.unsubWidth
	w.unsubWidth = nil
	w.mu.Unlock()
	if unsubWidth != nil {
		unsubWidth()
	}
	// The widget's rows are laid out for one width; a resize repaints them.
	if ctx.HasUI() {
		if unsub, err := ctx.OnWidthChange(func(c sdk.Context, _ int) { w.paint(c, w.config(c)) }); err == nil {
			w.mu.Lock()
			w.unsubWidth = unsub
			w.mu.Unlock()
		}
	}
	for _, p := range panels {
		if p != nil {
			p.Close()
		}
	}
	dir := TraceDir(w.opts.Getenv)
	if dir == "" {
		return
	}
	cfg := w.config(ctx)
	sessionID, err := ctx.GetSessionID()
	if err != nil || sessionID == "" {
		sessionID = ""
	}
	file := NewTraceFile(TraceFilePath(dir, sessionID), dir, TraceFileSession{SessionID: sessionID, Cwd: ctx.Cwd(), Mode: cfg.Mode, Judgments: JudgmentsStateOf(w.judgmentsOffReason(cfg))},
		func(text string) { w.say(ctx, text, "warning") })
	unsub := w.trace.Subscribe(file.Listener)
	w.mu.Lock()
	w.traceFile, w.unsubTraceFile = file, unsub
	w.mu.Unlock()
}
