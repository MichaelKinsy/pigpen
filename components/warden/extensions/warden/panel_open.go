package warden

import (
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Opening, toggling and closing the two panels (extension.ts togglePanel / toggleConfigPanel / traceText and the
// shortcut registration). Pi's `ui.custom` is not awaited there; here the call that mounts the overlay runs on its own
// goroutine with the command's retained context, which the SDK supports (a host call a handler left running continues
// until it ends), so a command or shortcut returns at once and a second press closes the panel.

// panelRun is one open panel.
type panelRun struct {
	mu     sync.Mutex
	handle *sdk.OverlayHandle
	// closeAsked: Close was called before the host mounted the overlay; the mount closes it.
	closeAsked bool
	built      bool
	closed     bool
}

// setHandle is the overlay's onHandle: the host mounted it.
func (r *panelRun) setHandle(h *sdk.OverlayHandle) {
	r.mu.Lock()
	r.handle, r.built = h, true
	ask := r.closeAsked
	r.mu.Unlock()
	if ask {
		_ = h.Hide()
	}
}

// Close removes the panel (a second shortcut press, or /warden trace again).
func (r *panelRun) Close() {
	r.mu.Lock()
	r.closed = true
	h := r.handle
	if h == nil {
		r.closeAsked = true
	}
	r.mu.Unlock()
	if h != nil {
		_ = h.Hide()
	}
}

// unfocus gives keyboard input back to the editor and keeps the panel visible. False says there is no handle to
// unfocus (the overlay is not mounted yet, or the host gave none): the caller closes the panel instead.
func (r *panelRun) unfocus() bool {
	r.mu.Lock()
	h := r.handle
	r.mu.Unlock()
	if h == nil {
		return false
	}
	return h.Unfocus() == nil
}

func (r *panelRun) focus() {
	r.mu.Lock()
	h := r.handle
	r.mu.Unlock()
	if h != nil {
		_ = h.Focus()
	}
}

func (r *panelRun) isBuilt() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.built
}

// overlayWidth reads widget.panelWidth: a percentage string ("40%") or a column count.
func overlayWidth(v any) *sdk.OverlaySize {
	switch t := v.(type) {
	case float64:
		return sdk.OverlayCells(int(t))
	case int:
		return sdk.OverlayCells(t)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(t), "%"), 64); err == nil {
			return sdk.OverlayPercent(f)
		}
	}
	return sdk.OverlayPercent(40)
}

type panelComponent interface {
	sdk.RemoteComponent
	sdk.RemoteComponentInvalidator
	SetViewport(rows int)
}

// toggle opens the panel in slot, or closes it when it is open. It returns the run it opened, nil when it closed one.
// The overlay is mounted on its own goroutine; closed (optional) runs there when the overlay has been removed.
func (w *warden) toggle(ctx sdk.Context, cfg Config, slot **panelRun, build func(theme ThemeLike, actions panelActions) panelComponent, closed func(run *panelRun)) *panelRun {
	w.mu.Lock()
	if open := *slot; open != nil {
		w.mu.Unlock()
		open.Close()
		w.mu.Lock()
		if *slot == open {
			*slot = nil
		}
		w.mu.Unlock()
		return nil
	}
	run := &panelRun{}
	*slot = run
	w.mu.Unlock()
	component := build(ctx.UITheme(), panelActions{focus: run.focus, unfocus: run.unfocus})
	if rows := ctx.Height(); rows > 0 {
		component.SetViewport(rows - 2)
	}
	options := sdk.RemoteOverlayOptions{
		Overlay: true,
		OverlayOptions: &sdk.OverlayOptions{
			Anchor: "right-center", Width: overlayWidth(cfg.Widget.PanelWidth), MinWidth: 44, MaxHeight: sdk.OverlayPercent(100), NonCapturing: true,
		},
		OnHandle: run.setHandle,
	}
	go func() {
		_, _ = ctx.Custom(component, options)
		w.mu.Lock()
		if *slot == run {
			*slot = nil
		}
		w.mu.Unlock()
		if closed != nil {
			closed(run)
		}
	}()
	return run
}

// toggleTrace is the one toggle behind the shortcut and /warden trace: it opens or closes the trace sidebar.
func (w *warden) toggleTrace(ctx sdk.Context, cfg Config, closed func(*panelRun)) *panelRun {
	return w.toggle(ctx, cfg, &w.tracePanel, func(theme ThemeLike, actions panelActions) panelComponent {
		return NewTracePanel(w.trace, theme, actions)
	}, closed)
}

// toggleConfig is the config overlay's toggle, the same way the sidebar's is, so the hint in its header is true.
func (w *warden) toggleConfig(ctx sdk.Context, cfg Config) {
	w.toggle(ctx, cfg, &w.configPanel, func(theme ThemeLike, actions panelActions) panelComponent {
		return NewConfigPanel(cfg, theme, actions, w.savePanelEdits)
	}, nil)
}

// savePanelEdits writes the config panel's edits into the user file, creating objects along each path.
func (w *warden) savePanelEdits(edits map[string]any, order []string) error {
	w.mu.Lock()
	path := w.cfgPath
	w.mu.Unlock()
	if path == "" {
		return errNoConfigPath
	}
	_, err := Serialize("config:"+path, func() (struct{}, error) {
		raw := ReadUserConfig(path)
		for _, key := range order {
			setInPlace(raw, strings.Split(key, "."), edits[key])
		}
		return struct{}{}, WriteUserConfig(path, raw)
	})
	return err
}

func setInPlace(obj map[string]any, keys []string, value any) {
	current := obj
	for _, k := range keys[:len(keys)-1] {
		next, ok := current[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[k] = next
		}
		current = next
	}
	current[keys[len(keys)-1]] = value
}

// traceText is the last 20 trace entries as text, newest last, for a host that cannot show the sidebar.
func (w *warden) traceText(file *TraceFile) string {
	entries := w.trace.Entries()
	text := "No guarded activity yet this session."
	if len(entries) > 0 {
		from := max(0, len(entries)-20)
		var b strings.Builder
		for i, e := range entries[from:] {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(e.At.Format("15:04:05") + " " + string(e.Guard) + ": " + e.Line)
			if len(e.Details) > 0 {
				b.WriteString("\n  " + strings.Join(e.Details, "\n  "))
			}
		}
		text = b.String()
	}
	if file != nil {
		return text + "\nTrace file: " + file.Path
	}
	return text
}
