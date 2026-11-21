package readashboard

import (
	"strconv"
	"strings"
)

type keyKind int

const (
	keyNone keyKind = iota
	keyUp
	keyDown
	keyLeft
	keyRight
	keyEnter
	keyEsc
	keyTab
	keyShiftTab
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
	keyCtrlC
	keyText
)

type key struct {
	kind keyKind
	r    rune
}

// parseKey decodes one terminal input chunk: legacy and application-cursor sequences and the Kitty keyboard
// protocol. Modifiers other than Shift on Tab are ignored, and Kitty key releases are dropped.
func parseKey(data string) key {
	switch data {
	case "":
		return key{}
	case "\x1b":
		return key{kind: keyEsc}
	case "\r", "\n":
		return key{kind: keyEnter}
	case "\t":
		return key{kind: keyTab}
	case "\x03":
		return key{kind: keyCtrlC}
	case "\x1b[Z":
		return key{kind: keyShiftTab}
	}
	if !strings.HasPrefix(data, "\x1b") {
		runes := []rune(data)
		if len(runes) == 1 && runes[0] >= ' ' && runes[0] != 0x7f {
			return key{kind: keyText, r: runes[0]}
		}
		return key{}
	}
	if len(data) < 3 || data[1] != '[' && data[1] != 'O' {
		return key{}
	}
	final := data[len(data)-1]
	params := data[2 : len(data)-1]
	if strings.Contains(params, ":3") {
		return key{}
	}
	switch final {
	case 'A':
		return key{kind: keyUp}
	case 'B':
		return key{kind: keyDown}
	case 'C':
		return key{kind: keyRight}
	case 'D':
		return key{kind: keyLeft}
	case 'H':
		return key{kind: keyHome}
	case 'F':
		return key{kind: keyEnd}
	case 'Z':
		return key{kind: keyShiftTab}
	case '~':
		code, _, _ := strings.Cut(params, ";")
		switch code {
		case "1", "7":
			return key{kind: keyHome}
		case "4", "8":
			return key{kind: keyEnd}
		case "5":
			return key{kind: keyPageUp}
		case "6":
			return key{kind: keyPageDown}
		}
	case 'u':
		code, mods, _ := strings.Cut(params, ";")
		value, err := strconv.Atoi(code)
		if err != nil {
			return key{}
		}
		modifier, _, _ := strings.Cut(mods, ":")
		shift := modifier == "2"
		ctrl := modifier == "5"
		switch {
		case value == 27 && (modifier == "" || modifier == "1"):
			return key{kind: keyEsc}
		case value == 13 && (modifier == "" || modifier == "1"):
			return key{kind: keyEnter}
		case value == 9 && shift:
			return key{kind: keyShiftTab}
		case value == 9 && (modifier == "" || modifier == "1"):
			return key{kind: keyTab}
		case value == 'c' && ctrl:
			return key{kind: keyCtrlC}
		case value >= ' ' && value < 0x110000 && (modifier == "" || modifier == "1" || shift):
			return key{kind: keyText, r: rune(value)}
		}
	}
	return key{}
}
