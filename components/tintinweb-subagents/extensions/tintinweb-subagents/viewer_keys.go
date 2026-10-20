package tintinweb_subagents

import tui "github.com/MichaelKinsy/pigpen/components/pitui"

// Scroll key matchers for the conversation viewer: tui.select.* through the user's keybindings when a manager is
// given, the previous hardcoded keys otherwise; the k/j and shift+arrow aliases always work. upstream: ui/viewer-keys.ts.

// viewerKeybindings is the structural subset of pi-tui's KeybindingsManager the viewer needs (which satisfies it).
type viewerKeybindings interface {
	Matches(data string, id tui.TUIKeybinding) bool
}

type viewerKeys struct {
	scrollUp, scrollDown, pageUp, pageDown func(data string) bool
}

func createViewerKeys(kb viewerKeybindings) viewerKeys {
	matches := func(data string, id tui.TUIKeybinding, fallback string) bool {
		if kb != nil {
			return kb.Matches(data, id)
		}
		return tui.MatchesKey(data, fallback)
	}
	return viewerKeys{
		scrollUp:   func(d string) bool { return matches(d, tui.KBSelectUp, "up") || tui.MatchesKey(d, "k") },
		scrollDown: func(d string) bool { return matches(d, tui.KBSelectDown, "down") || tui.MatchesKey(d, "j") },
		pageUp:     func(d string) bool { return matches(d, tui.KBSelectPageUp, "pageUp") || tui.MatchesKey(d, "shift+up") },
		pageDown: func(d string) bool {
			return matches(d, tui.KBSelectPageDown, "pageDown") || tui.MatchesKey(d, "shift+down")
		},
	}
}
