// Package keys decodes the input chunks PiG hands a focused component into
// named keys.
//
// PiG forwards terminal input unparsed: one chunk per key press, as the terminal
// sent it. Depending on the terminal and on whether the Kitty keyboard protocol
// is active, the same key arrives as a legacy sequence ("\x1b[A"), an
// application-cursor sequence ("\x1bOA"), a CSI-u sequence ("\x1b[97;5u") or an
// xterm modifyOtherKeys sequence ("\x1b[27;5;97~"). Decode folds all of them to
// one name, such as "up", "ctrl+c", "shift+tab", "q" or "space".
package keys

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key is one decoded key event.
type Key struct {
	// Name identifies the key: a lowercase key name ("up", "enter", "f5"), the
	// character itself for printable keys ("q", "+", "A"), "space" for a space,
	// optionally prefixed by modifiers in the order ctrl+alt+shift+super.
	Name string
	// Text is the printable text the key produces, empty for control keys and for
	// chords with ctrl or alt.
	Text string
	// Release is true for a Kitty key-release event. Callers normally ignore it.
	Release bool
}

// Modifier bits of the CSI-u and modifyOtherKeys parameter, which carries them
// plus one.
const (
	modShift = 1 << iota
	modAlt
	modCtrl
	modSuper
)

// Decode names one input chunk. It reports false for chunks it cannot name
// (mouse reports, focus events, unknown escape sequences).
func Decode(data string) (Key, bool) {
	switch {
	case data == "":
		return Key{}, false
	case data == "\x1b":
		return Key{Name: "escape"}, true
	case strings.HasPrefix(data, "\x1b[200~"):
		return Key{Name: "paste", Text: strings.TrimSuffix(strings.TrimPrefix(data, "\x1b[200~"), "\x1b[201~")}, true
	case strings.HasPrefix(data, "\x1b["):
		return decodeCSI(data[2:])
	case strings.HasPrefix(data, "\x1bO") && len(data) == 3:
		return decodeSS3(data[2])
	case data[0] == 0x1b:
		// ESC followed by a key: alt plus that key.
		inner, ok := Decode(data[1:])
		if !ok || inner.Release || strings.HasPrefix(inner.Name, "alt+") {
			return Key{}, false
		}
		return withModifier(inner, modAlt), true
	}
	return decodePlain(data)
}

func decodePlain(data string) (Key, bool) {
	if len(data) == 1 {
		switch c := data[0]; {
		case c == '\r' || c == '\n':
			return Key{Name: "enter"}, true
		case c == '\t':
			return Key{Name: "tab"}, true
		case c == 0x7f || c == 0x08:
			return Key{Name: "backspace"}, true
		case c == ' ':
			return Key{Name: "space", Text: " "}, true
		case c == 0:
			return Key{Name: "ctrl+space"}, true
		case c >= 1 && c <= 26:
			return Key{Name: "ctrl+" + string(rune('a'+c-1))}, true
		case c >= 28 && c <= 31:
			return Key{Name: "ctrl+" + string(rune("\\]^_"[c-28]))}, true
		case c < ' ':
			return Key{}, false
		}
	}
	if !utf8.ValidString(data) {
		return Key{}, false
	}
	if utf8.RuneCountInString(data) == 1 {
		return Key{Name: data, Text: data}, true
	}
	// Several characters in one chunk: a paste or an input method commit.
	return Key{Name: "text", Text: data}, true
}

var finals = map[byte]string{
	'A': "up", 'B': "down", 'C': "right", 'D': "left",
	'H': "home", 'F': "end", 'E': "clear",
	'P': "f1", 'Q': "f2", 'R': "f3", 'S': "f4",
}

var tilde = map[int]string{
	1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pgup", 6: "pgdn", 7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5", 17: "f6", 18: "f7", 19: "f8",
	20: "f9", 21: "f10", 23: "f11", 24: "f12",
}

func decodeSS3(final byte) (Key, bool) {
	name, ok := finals[final]
	if !ok {
		return Key{}, false
	}
	return Key{Name: name}, true
}

// decodeCSI decodes what follows "ESC [".
func decodeCSI(body string) (Key, bool) {
	if body == "" {
		return Key{}, false
	}
	final := body[len(body)-1]
	params := body[:len(body)-1]
	if strings.ContainsAny(params, "<>?=") || final < 0x40 {
		// Mouse reports, device replies and incomplete sequences.
		return Key{}, false
	}
	fields := strings.Split(params, ";")
	// Focus in and out ("ESC [ I", "ESC [ O") fall to the lookup below and are unnamed.
	switch final {
	case 'Z':
		bits := modShift
		if len(fields) > 1 {
			bits |= modsOf(fields[1]).bits
		}
		return withModifier(Key{Name: "tab"}, bits), true
	case 'u':
		return decodeCSIu(fields)
	case '~':
		return decodeTilde(fields)
	}
	name, ok := finals[final]
	if !ok {
		return Key{}, false
	}
	key := Key{Name: name}
	if len(fields) > 1 {
		m := modsOf(fields[1])
		if m.release {
			key.Release = true
		}
		return withModifier(key, m.bits), true
	}
	return key, true
}

type mods struct {
	bits    int
	alt     bool
	release bool
}

// modsOf parses "mods[:event]". The modifier parameter is one plus the bit set;
// lock bits (caps 64, num 128) are dropped.
func modsOf(field string) mods {
	value, event, _ := strings.Cut(field, ":")
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		n = 1
	}
	bits := (n - 1) & (modShift | modAlt | modCtrl | modSuper)
	return mods{bits: bits, alt: bits&modAlt != 0, release: event == "3"}
}

func decodeTilde(fields []string) (Key, bool) {
	code, err := strconv.Atoi(fields[0])
	if err != nil {
		return Key{}, false
	}
	if code == 27 && len(fields) == 3 {
		// xterm modifyOtherKeys: ESC [ 27 ; mods ; code ~
		cp, err := strconv.Atoi(fields[2])
		if err != nil {
			return Key{}, false
		}
		return fromCodepoint(cp, 0, modsOf(fields[1]))
	}
	name, ok := tilde[code]
	if !ok {
		return Key{}, false
	}
	key := Key{Name: name}
	if len(fields) > 1 {
		m := modsOf(fields[1])
		key.Release = m.release
		return withModifier(key, m.bits), true
	}
	return key, true
}

// decodeCSIu decodes "code[:shifted[:base]] ; mods[:event] ; text u".
func decodeCSIu(fields []string) (Key, bool) {
	codes := strings.Split(fields[0], ":")
	cp, err := strconv.Atoi(codes[0])
	if err != nil {
		return Key{}, false
	}
	shifted := 0
	if len(codes) > 1 && codes[1] != "" {
		shifted, _ = strconv.Atoi(codes[1])
	}
	m := mods{}
	if len(fields) > 1 {
		m = modsOf(fields[1])
	}
	return fromCodepoint(cp, shifted, m)
}

// fromCodepoint names a CSI-u or modifyOtherKeys key. shifted is the Kitty
// "shifted key" alternate, zero when absent.
func fromCodepoint(cp, shifted int, m mods) (Key, bool) {
	var key Key
	switch cp {
	case 13, 57414: // enter, keypad enter
		key = Key{Name: "enter"}
	case 27:
		key = Key{Name: "escape"}
	case 9:
		key = Key{Name: "tab"}
	case 127, 8:
		key = Key{Name: "backspace"}
	case 32:
		key = Key{Name: "space", Text: " "}
	default:
		// Control codes and the Kitty private-use block (lock keys, keypad,
		// media and modifier keys) have no name here.
		if cp < 32 || cp >= 57344 && cp <= 63743 || cp > 0x10ffff {
			return Key{}, false
		}
		text := string(rune(cp))
		// A plain shifted letter or symbol is its shifted character, so that
		// bindings can say "+" or "A" instead of "shift+=".
		if m.bits&modShift != 0 && m.bits&(modCtrl|modAlt|modSuper) == 0 {
			switch {
			case shifted > 0:
				text = string(rune(shifted))
				m.bits &^= modShift
			case cp >= 'a' && cp <= 'z':
				text = strings.ToUpper(text)
				m.bits &^= modShift
			}
		}
		key = Key{Name: text, Text: text}
	}
	key.Release = m.release
	return withModifier(key, m.bits), true
}

// withModifier prefixes key's name with the modifier bits and clears Text for
// chords that do not produce text.
func withModifier(key Key, bits int) Key {
	if bits == 0 {
		return key
	}
	var prefix string
	if bits&modCtrl != 0 {
		prefix += "ctrl+"
	}
	if bits&modAlt != 0 {
		prefix += "alt+"
	}
	if bits&modShift != 0 {
		prefix += "shift+"
	}
	if bits&modSuper != 0 {
		prefix += "super+"
	}
	if bits&(modCtrl|modAlt|modSuper) != 0 {
		key.Text = ""
	}
	key.Name = prefix + key.Name
	return key
}
