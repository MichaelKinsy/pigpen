package pigmodeltweaks

import (
	"strconv"
	"strings"
)

// key is one decoded keystroke.
type key int

const (
	keyNone key = iota
	keyRune
	keyUp
	keyDown
	keyLeft
	keyRight
	keyEnter
	keyEscape
	keySpace
	keyBackspace
)

// Kitty keyboard-protocol codepoints for the keys a picker reacts to.
const (
	codeEnter     = 13
	codeEscape    = 27
	codeSpace     = 32
	codeBackspace = 127
)

// Kitty's "report all keys as escape codes" mode encodes the cursor keys as
// CSI-u codepoints rather than as CSI with a final byte. PiG's own key matcher
// knows only the CSI forms, so an extension that mirrors its vocabulary would
// ignore arrows sent this way.
const (
	codeArrowUp    = 57399
	codeArrowDown  = 57398
	codeArrowRight = 57397
	codeArrowLeft  = 57396
)

// Kitty reports a key event as modifiers and an event type in one field:
// `\x1b[1;5:3A` is ctrl+up, released. Event 1 is a press, 3 a release.
const (
	eventPress   = 1
	eventRelease = 3
)

// Bracketed-paste markers, which surround pasted text rather than meaning a key.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// keyOf decodes one input chunk into a keystroke.
//
// A picker cannot compare raw bytes the way a line editor can, because the same
// logical key arrives in three shapes depending on the terminal and the mode the
// host negotiated: legacy CSI (`\x1b[A`), application cursor mode (`\x1bOA`), and
// the Kitty protocol, which reports arrows as CSI with modifier parameters and
// reports Enter, Escape and Space as CSI-u codepoints. Matching only the legacy
// bytes means a terminal in Kitty mode delivers arrows and Escape that look like
// nothing at all, which is what this decoder exists to prevent.
func keyOf(data string) (key, rune) {
	if data == "" || isPaste(data) {
		return keyNone, 0
	}
	// Single-byte input first: printable characters and the legacy control keys.
	if !strings.HasPrefix(data, "\x1b") {
		switch data {
		case "\r", "\n":
			return keyEnter, 0
		case "\x7f", "\b":
			return keyBackspace, 0
		case " ", "\t":
			if data == " " {
				return keySpace, 0
			}
		}
		if decoded, size := decodeRune(data); size == len(data) {
			return keyRune, decoded
		}
		return keyNone, 0
	}

	rest := data[1:]

	// CSI-u: ESC [ <codepoint> [;<modifiers>[:<event>]] [;<text>] u
	if strings.HasSuffix(rest, "u") {
		if parsed := parseCSIu(rest); parsed != nil {
			return parsed.key, parsed.rune
		}
	}

	// SS3 and CSI: ESC [ <parameters> <final> or ESC O <final>
	if len(rest) >= 2 && rest[0] == 'O' {
		return arrowOrNone(rest[1]), 0
	}
	if strings.HasPrefix(rest, "[") && len(rest) >= 2 {
		final := rest[len(rest)-1]
		if decoded, ok := arrowFromCSI(rest[1:len(rest)-1], final); ok {
			return decoded, 0
		}
		return keyNone, 0
	}

	// A bare Escape. A terminal in a mode that encodes Escape as CSI-u never
	// reaches this branch; both spellings are handled above.
	if rest == "" {
		return keyEscape, 0
	}
	return keyNone, 0
}

// parsedKey is one decoded CSI-u report.
type parsedKey struct {
	key  key
	rune rune
}

// isPaste reports whether a chunk is a bracketed paste, which is text rather than
// a keystroke.
func isPaste(data string) bool {
	return strings.HasPrefix(data, pasteStart) && strings.HasSuffix(data, pasteEnd)
}

// pastedText returns the text inside a bracketed paste.
func pastedText(data string) (string, bool) {
	if !isPaste(data) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(data, pasteStart), pasteEnd), true
}

// parseCSIu decodes `\x1b[<params>u` into the key it reports, accepting only an
// unmodified press so a Ctrl+Space does not toggle a row.
func parseCSIu(rest string) *parsedKey {
	body := strings.TrimPrefix(strings.TrimSuffix(rest, "u"), "[")
	if body == "" {
		return nil
	}
	fields := strings.Split(body, ";")
	codepoint, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil
	}
	if !unmodifiedPress(fields) {
		return nil
	}
	switch codepoint {
	case codeEnter:
		return &parsedKey{key: keyEnter}
	case codeEscape:
		return &parsedKey{key: keyEscape}
	case codeSpace:
		return &parsedKey{key: keySpace}
	case codeBackspace:
		return &parsedKey{key: keyBackspace}
	case codeArrowUp:
		return &parsedKey{key: keyUp}
	case codeArrowDown:
		return &parsedKey{key: keyDown}
	case codeArrowRight:
		return &parsedKey{key: keyRight}
	case codeArrowLeft:
		return &parsedKey{key: keyLeft}
	default:
		if decoded, size := decodeRune(string(rune(codepoint))); size == 1 {
			return &parsedKey{key: keyRune, rune: decoded}
		}
		return nil
	}
}

// arrowFromCSI decodes a CSI sequence whose final byte names a cursor key.
// parameters hold the modifier report: absent or 1 is unmodified.
func arrowFromCSI(parameters string, final byte) (key, bool) {
	arrow, ok := arrowOrNone(final), isArrowFinal(final)
	if !ok {
		return keyNone, false
	}
	if !unmodifiedPress(strings.Split(parameters, ";")) {
		return keyNone, false
	}
	return arrow, true
}

// unmodifiedPress reports whether a Kitty parameter list describes a plain key
// press: no modifiers, or modifiers of 1, and a press event rather than a release.
// A terminal that reports event types appends them to the modifier field with a
// colon, so `\x1b[1;1:1B` is an unmodified Down press and not a sequence to ignore.
func unmodifiedPress(fields []string) bool {
	modifierField := "1"
	if len(fields) > 1 && fields[1] != "" {
		modifierField = fields[1]
	}
	modifier, event := modifierField, ""
	if base, reported, found := strings.Cut(modifierField, ":"); found {
		modifier, event = base, reported
	}
	parsedModifier, err := strconv.Atoi(modifier)
	if err != nil || parsedModifier != 1 {
		return false
	}
	if event == "" {
		return true
	}
	parsedEvent, err := strconv.Atoi(event)
	if err != nil {
		return false
	}
	return parsedEvent == eventPress || parsedEvent != eventRelease
}

// arrowOrNone maps a final byte to a cursor key. The lowercase forms are rxvt's
// shifted arrows; a picker has no shift semantics, so they navigate like the
// uppercase ones.
func arrowOrNone(final byte) key {
	switch final {
	case 'A', 'a':
		return keyUp
	case 'B', 'b':
		return keyDown
	case 'C', 'c':
		return keyRight
	case 'D', 'd':
		return keyLeft
	default:
		return keyNone
	}
}

// isArrowFinal reports whether a final byte names a cursor key at all.
func isArrowFinal(final byte) bool {
	return final == 'A' || final == 'B' || final == 'C' || final == 'D' ||
		final == 'a' || final == 'b' || final == 'c' || final == 'd'
}

// decodeRune decodes the first rune of text.
func decodeRune(text string) (rune, int) {
	for index, r := range text {
		if index == 0 {
			return r, len(string(r))
		}
	}
	return 0, 0
}
