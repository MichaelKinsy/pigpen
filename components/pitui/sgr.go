package tui

import (
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/pigpen/components/pitui/lazyregexp"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// SGR reset sequences (copied from PiG's tui/theme.go).
const (
	SGRResetAll       = "\x1b[0m"
	SGRFgReset        = "\x1b[39m"
	SGRBgReset        = "\x1b[49m"
	SGRBoldDimReset   = "\x1b[22m"
	SGRItalicReset    = "\x1b[23m"
	SGRUnderlineReset = "\x1b[24m"
	SGRInverseReset   = "\x1b[27m"
	SGRStrikeReset    = "\x1b[29m"
)

// FgClose is the sequence that ends a foreground opened by prefix: a dim token
// sets faint too, so it closes both.
func FgClose(prefix string) string {
	if strings.HasSuffix(prefix, "\x1b[2m") {
		return "\x1b[22;39m"
	}
	return SGRFgReset
}

var kittyProtocolActive atomic.Bool

// SetKittyProtocolActive mirrors pi-tui's global Kitty keyboard state
// (setKittyProtocolActive in keys.ts).
func SetKittyProtocolActive(active bool) { kittyProtocolActive.Store(active) }

// IsKittyProtocolActive reports whether the Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool { return kittyProtocolActive.Load() }

// KeyReleaseReceiver is implemented by components that want Kitty key-release
// events delivered to HandleInput (pi-tui's optional wantsKeyRelease).
type KeyReleaseReceiver interface {
	WantsKeyRelease() bool
}

// Fake cursor markers: zero-width APC sequences around the text drawn as the
// cursor (pi-tui FAKE_CURSOR_START / FAKE_CURSOR_END). The host TUI resolves
// them with ResolveFakeCursors.
const (
	CursorMarker    = widthx.CursorMarker
	FakeCursorStart = "\x1b_pi:fc\x07"
	FakeCursorEnd   = "\x1b_pi:/fc\x07"
)

// RenderFakeCursor wraps text in fake cursor markers (pi-tui renderFakeCursor).
func RenderFakeCursor(text string) string { return FakeCursorStart + text + FakeCursorEnd }

var focusedFakeCursor = lazyregexp.New(`(?s)(\x1b_pi:c\x07)\x1b_pi:fc\x07(.*?)(?:\x1b_pi:/fc\x07|$)`)

// ResolveFakeCursors resolves fake cursor markers to reverse video, as pi-tui
// TUI.resolveFakeCursors does. When the hardware cursor is shown, a fake
// cursor directly after CursorMarker is dropped because the terminal cursor is
// drawn there.
func ResolveFakeCursors(lines []string, showHardwareCursor bool) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		if !strings.Contains(line, FakeCursorStart) {
			out[i] = line
			continue
		}
		if showHardwareCursor {
			line = focusedFakeCursor.ReplaceAllString(line, "$1$2")
		}
		out[i] = strings.ReplaceAll(strings.ReplaceAll(line, FakeCursorStart, "\x1b[7m"), FakeCursorEnd, "\x1b[27m")
	}
	return out
}
