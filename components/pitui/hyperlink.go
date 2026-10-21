package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Hyperlink wraps text in an OSC 8 hyperlink to url (pi-tui hyperlink).
func Hyperlink(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

var (
	hyperlinksMu       sync.Mutex
	hyperlinksCached   *bool
	hyperlinksOverride *bool
	tmuxHyperlinkProbe = probeTmuxHyperlinks
)

// SetHyperlinksSupported overrides terminal detection (pi-tui
// setCapabilityOverrides({hyperlinks})); nil restores detection.
func SetHyperlinksSupported(supported *bool) {
	hyperlinksMu.Lock()
	defer hyperlinksMu.Unlock()
	hyperlinksOverride = supported
	hyperlinksCached = nil
}

// HyperlinksSupported reports whether the terminal renders OSC 8 hyperlinks
// (getCapabilities().hyperlinks): the PI_HYPERLINKS override, else detection
// from the environment, cached.
func HyperlinksSupported() bool {
	hyperlinksMu.Lock()
	defer hyperlinksMu.Unlock()
	if hyperlinksCached != nil {
		return *hyperlinksCached
	}
	var v bool
	switch {
	case hyperlinksOverride != nil:
		v = *hyperlinksOverride
	case os.Getenv("PI_HYPERLINKS") == "1":
		v = true
	case os.Getenv("PI_HYPERLINKS") == "0":
		v = false
	default:
		v = detectHyperlinks(tmuxHyperlinkProbe, runtime.GOOS)
	}
	hyperlinksCached = &v
	return v
}

// probeTmuxHyperlinks: tmux re-emits OSC 8 only when the attached client's
// client_termfeatures lists hyperlinks. Any error, including the 250ms
// timeout, reports false.
func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for feature := range strings.SplitSeq(string(out), ",") {
		if strings.TrimSpace(feature) == "hyperlinks" {
			return true
		}
	}
	return false
}

// detectHyperlinks mirrors the hyperlinks field of pi-tui
// detectCapabilitiesFromEnvironment.
func detectHyperlinks(tmuxForwards func() bool, goos string) bool {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(os.Getenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(os.Getenv("TERM"))
	switch {
	case os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux"):
		return tmuxForwards()
	case strings.HasPrefix(term, "screen"):
		return false
	case termProgram == "herdr":
		return true
	case os.Getenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty":
		return true
	case termProgram == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return true
	case os.Getenv("WEZTERM_PANE") != "" || termProgram == "wezterm":
		return true
	case termProgram == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "":
		return true
	case os.Getenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app":
		return true
	case os.Getenv("WT_SESSION") != "":
		return true
	case termProgram == "alacritty" || termProgram == "vscode" || termProgram == "zed":
		return true
	case terminalEmulator == "jetbrains-jediterm":
		return false
	case goos == "windows":
		return false
	}
	return false
}
