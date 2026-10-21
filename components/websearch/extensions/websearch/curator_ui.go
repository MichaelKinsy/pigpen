package websearch

// The activity widget and the shortcuts (index.ts:1783-1827, 995-1021).

// WidgetUI is the part of the UI the activity widget needs: ctx.ui.setWidget and ctx.ui.theme. A
// nil lines clears the widget.
type WidgetUI interface {
	SetWidget(key string, lines []string)
	Theme() WidgetTheme
}

func (r *Runtime) updateActivityWidget(ui WidgetUI) {
	ui.SetWidget(ActivityWidgetKey, ActivityWidgetLines(Activity, ui.Theme(), nowMs()))
}

// ToggleActivityWidget is the activity shortcut: show the widget and keep it current as calls are
// logged, or hide it.
func (r *Runtime) ToggleActivityWidget(ui WidgetUI) {
	s := &r.cur
	s.mu.Lock()
	s.widgetVisible = !s.widgetVisible
	visible := s.widgetVisible
	prev := s.widgetUnsub
	s.widgetUnsub = nil
	s.mu.Unlock()
	if visible {
		if prev != nil {
			prev()
		}
		unsub := Activity.OnUpdate(func() { r.updateActivityWidget(ui) })
		s.mu.Lock()
		s.widgetUnsub = unsub
		s.mu.Unlock()
		r.updateActivityWidget(ui)
		return
	}
	if prev != nil {
		prev()
	}
	ui.SetWidget(ActivityWidgetKey, nil)
}

// ResetActivity is the activity part of handleSessionChange: the log starts empty and a visible
// widget follows the new session's UI.
func (r *Runtime) ResetActivity(ui WidgetUI) {
	s := &r.cur
	s.mu.Lock()
	prev := s.widgetUnsub
	s.widgetUnsub = nil
	visible := s.widgetVisible
	s.mu.Unlock()
	// Unsubscribe before clearing so the old UI is not called with the cleared state.
	if prev != nil {
		prev()
	}
	Activity.Clear()
	if visible {
		unsub := Activity.OnUpdate(func() { r.updateActivityWidget(ui) })
		s.mu.Lock()
		s.widgetUnsub = unsub
		s.mu.Unlock()
		r.updateActivityWidget(ui)
	}
}

// ShutdownActivity is the activity part of session_shutdown.
func (r *Runtime) ShutdownActivity() {
	s := &r.cur
	s.mu.Lock()
	prev := s.widgetUnsub
	s.widgetUnsub = nil
	s.widgetVisible = false
	s.mu.Unlock()
	if prev != nil {
		prev()
	}
	Activity.Clear()
}

// CurateShortcut is the curate shortcut: while the latest web_search call is still searching,
// open its curator now, with the remaining searches streaming in.
func (r *Runtime) CurateShortcut(ui interface{ Notify(message, level string) }) {
	callID, pc := r.cur.lastPending()
	if pc == nil {
		return
	}
	pc.mu.Lock()
	searching := pc.phase == "searching"
	pc.mu.Unlock()
	if !searching {
		return
	}
	done := make(chan struct{})
	pc.mu.Lock()
	pc.browserDone = done
	pc.mu.Unlock()
	go func() {
		defer close(done)
		r.openCuratorBrowser(callID, pc, false)
	}()
	ui.Notify("Opening curator — remaining searches will stream in", "info")
}
