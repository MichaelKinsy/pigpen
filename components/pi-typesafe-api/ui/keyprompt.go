// Package ui holds the interactive helpers of the typed API: a hidden-input key prompt, the login that
// verifies and stores a TypeSafe key, and EnsureAPIKey. They need PiG's TUI, so call them only from extension
// command handlers.
package ui

import (
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	bold      = "\x1b[1m"
	dim       = "\x1b[2m"
	reset     = "\x1b[0m"
	reverseOn = "\x1b[7m"
	reverseOf = "\x1b[27m"
	pasteOpen = "\x1b[200~"
	pasteEnd  = "\x1b[201~"
)

// KeyPrompt is a single-line input that renders bullets instead of the typed value. It is an sdk.RemoteComponent:
// Enter submits the value, Escape cancels.
type KeyPrompt struct {
	value   []rune
	Focused bool
	title   string
	hint    string
}

// NewKeyPrompt returns the prompt for a TypeSafe key.
func NewKeyPrompt() *KeyPrompt {
	return &KeyPrompt{
		Focused: true,
		title:   "TypeSafe API key",
		hint:    "Paste the key from console.typesafe.ai › API Keys. Input is hidden. Enter saves, Esc cancels.",
	}
}

var _ sdk.RemoteComponent = (*KeyPrompt)(nil)

// Render draws the title, the hint and the masked input at width.
func (p *KeyPrompt) Render(width int) []string {
	lines := []string{" " + bold + p.title + reset}
	for _, l := range wrap(p.hint, max(1, width-2)) {
		lines = append(lines, " "+dim+l+reset)
	}
	bullets := strings.Repeat("•", min(len(p.value), max(0, width-2)))
	cursor := ""
	if p.Focused {
		cursor = reverseOn + " " + reverseOf
	}
	return append(lines, bullets+cursor)
}

// HandleInput reads one chunk of terminal input: typed or pasted text, backspace, Enter and Escape.
func (p *KeyPrompt) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	switch data {
	case "\r", "\n":
		return sdk.RemoteComponentResult{Done: true, Value: string(p.value)}, nil
	case "\x1b":
		return sdk.RemoteComponentResult{Done: true, Value: nil}, nil
	case "\x7f", "\b":
		if len(p.value) > 0 {
			p.value = p.value[:len(p.value)-1]
		}
		return sdk.RemoteComponentResult{}, nil
	}
	data = strings.TrimSuffix(strings.TrimPrefix(data, pasteOpen), pasteEnd)
	if strings.HasPrefix(data, "\x1b") {
		return sdk.RemoteComponentResult{}, nil // another escape sequence: ignore
	}
	for len(data) > 0 {
		r, n := utf8.DecodeRuneInString(data)
		data = data[n:]
		if r >= 0x20 && r != 0x7f {
			p.value = append(p.value, r)
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// PromptForAPIKey asks for a key with hidden input; where custom components are unavailable or fail to open it falls back to
// PiG's plain input dialog (visible while typing). ok is false when the user cancels.
func PromptForAPIKey(ctx sdk.Context) (key string, ok bool, err error) {
	result, err := ctx.Custom(NewKeyPrompt(), sdk.RemoteOverlayOptions{Title: "TypeSafe API key"})
	if err == nil {
		value, isString := result.(string)
		return value, isString, nil
	}
	// Any failure to open the component (the host has no custom components, as in RPC mode) falls back to the
	// plain dialog; a failure of that dialog is the error reported.
	return ctx.Input("TypeSafe API key (visible while typing)", "Paste the key, then press Enter")
}
