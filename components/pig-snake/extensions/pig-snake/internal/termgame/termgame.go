// Package termgame holds the terminal plumbing of Pig Snake: the full-terminal
// overlay, its content size, and key decoding. Adapted from PiG Standard's
// internal/termgame (PiG d86eb93, MIT, Michael Kinsy; see CREDITS.md):
// the mouse decoding was dropped because the game is keyboard only.
package termgame

import (
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// FallbackHeight is the terminal height a game assumes before the host
// reports one.
const FallbackHeight = 24

// Overlay opens a component over the whole terminal. The host frames a modal
// overlay with a one-cell box whose top edge carries the title.
func Overlay(title string) sdk.RemoteOverlayOptions {
	return sdk.RemoteOverlayOptions{Title: title, WidthFraction: 1, HeightFraction: 1, Overlay: true}
}

// Viewport returns the component's content size inside the full-terminal
// overlay for a terminal of termWidth by termHeight cells.
func Viewport(termWidth, termHeight int) (width, height int) {
	if termHeight <= 0 {
		termHeight = FallbackHeight
	}
	return max(termWidth-2, 1), max(termHeight-2, 1)
}

// Key is a decoded control key.
type Key int

const (
	KeyNone Key = iota
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyEscape
	KeyEnter
	KeyText
)

// ParseKey decodes one input chunk. It accepts legacy, application-cursor, and
// Kitty keyboard encodings, and ignores Kitty key releases. KeyText carries a
// printable rune.
func ParseKey(data string) (Key, rune) {
	switch data {
	case "":
		return KeyNone, 0
	case "\x1b":
		return KeyEscape, 0
	case "\r", "\n":
		return KeyEnter, 0
	}
	if !strings.HasPrefix(data, "\x1b") {
		runes := []rune(data)
		if len(runes) == 1 && runes[0] >= ' ' {
			return KeyText, runes[0]
		}
		return KeyNone, 0
	}
	if len(data) < 3 || data[1] != '[' && data[1] != 'O' {
		return KeyNone, 0
	}
	final := data[len(data)-1]
	params := data[2 : len(data)-1]
	if strings.Contains(params, ":3") {
		return KeyNone, 0
	}
	switch final {
	case 'A':
		return KeyUp, 0
	case 'B':
		return KeyDown, 0
	case 'C':
		return KeyRight, 0
	case 'D':
		return KeyLeft, 0
	case 'u':
		code, mods, _ := strings.Cut(params, ";")
		value, err := strconv.Atoi(code)
		if err != nil {
			return KeyNone, 0
		}
		modifier, _, _ := strings.Cut(mods, ":")
		if modifier != "" && modifier != "1" && modifier != "2" {
			return KeyNone, 0
		}
		switch value {
		case 27:
			return KeyEscape, 0
		case 13:
			return KeyEnter, 0
		}
		if value >= ' ' && value < 0x110000 {
			return KeyText, rune(value)
		}
	}
	return KeyNone, 0
}
