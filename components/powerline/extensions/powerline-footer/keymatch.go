package powerline_footer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// matchesKey and isKeyRelease: the parts of pi-tui's keys.ts (matchesKey, isKeyRelease) the shortcuts need, over raw terminal
// input. The SDK has no keybinding lookup and no way to ask whether the terminal runs the Kitty protocol, so the Kitty-only
// branches of keys.ts (`isKittyProtocolActive()`) are taken as inactive: a legacy sequence that Kitty reassigns (ESC+letter for
// alt+letter, ESC+control for ctrl+alt+letter) is matched, as it is in a terminal that has not enabled the protocol.
// upstream: @earendil-works/pi-tui keys.ts matchesKey.

const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
	lockMask = 64 + 128 // Caps Lock and Num Lock never take part in a match
)

const (
	cpArrowUp    = -1
	cpArrowDown  = -2
	cpArrowRight = -3
	cpArrowLeft  = -4
	cpDelete     = -10
	cpInsert     = -11
	cpPageUp     = -12
	cpPageDown   = -13
	cpHome       = -14
	cpEnd        = -15
	cpEscape     = 27
	cpTab        = 9
	cpEnter      = 13
	cpSpace      = 32
	cpBackspace  = 127
	cpKPEnter    = 57414
)

type namedKey struct {
	cp      int
	isArrow bool
}

var namedKeys = map[string]namedKey{
	"up": {cpArrowUp, true}, "down": {cpArrowDown, true}, "left": {cpArrowLeft, true}, "right": {cpArrowRight, true},
	"delete": {cpDelete, false}, "insert": {cpInsert, false}, "pageup": {cpPageUp, false}, "pagedown": {cpPageDown, false},
	"home": {cpHome, false}, "end": {cpEnd, false},
	"escape": {cpEscape, false}, "esc": {cpEscape, false}, "tab": {cpTab, false}, "enter": {cpEnter, false},
	"return": {cpEnter, false}, "space": {cpSpace, false}, "backspace": {cpBackspace, false},
}

var arrowFinals = map[byte]int{'A': cpArrowUp, 'B': cpArrowDown, 'C': cpArrowRight, 'D': cpArrowLeft}
var homeEndFinals = map[byte]int{'H': cpHome, 'F': cpEnd}
var funcKeyNumbers = map[int]int{2: cpInsert, 3: cpDelete, 5: cpPageUp, 6: cpPageDown, 7: cpHome, 8: cpEnd}
var arrowFinalOf = map[int]byte{cpArrowUp: 'A', cpArrowDown: 'B', cpArrowRight: 'C', cpArrowLeft: 'D'}
var funcNumberOf = map[int]int{cpDelete: 3, cpInsert: 2, cpPageUp: 5, cpPageDown: 6, cpHome: 7, cpEnd: 8}
var homeEndFinalOf = map[int]byte{cpHome: 'H', cpEnd: 'F'}

// functionKeys are the legacy sequences of f1-f12 (no CSI-u codepoint in pi-tui's key vocabulary).
var functionKeys = map[string][]string{
	"f1": {"\x1bOP", "\x1b[11~", "\x1b[[A"}, "f2": {"\x1bOQ", "\x1b[12~", "\x1b[[B"}, "f3": {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4": {"\x1bOS", "\x1b[14~", "\x1b[[D"}, "f5": {"\x1b[15~", "\x1b[[E"}, "f6": {"\x1b[17~"}, "f7": {"\x1b[18~"},
	"f8": {"\x1b[19~"}, "f9": {"\x1b[20~"}, "f10": {"\x1b[21~"}, "f11": {"\x1b[23~"}, "f12": {"\x1b[24~"},
}

var kittyCSIU = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)

type parsedKey struct {
	codepoint, modifier int
	baseLayout          int
	hasBaseLayout       bool
}

func parseCSIu(data string) *parsedKey {
	m := kittyCSIU.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	cp, err := strconv.Atoi(m[1])
	if err != nil {
		return nil
	}
	mod := 1
	if m[4] != "" {
		if mod, err = strconv.Atoi(m[4]); err != nil {
			return nil
		}
	}
	pk := &parsedKey{codepoint: cp, modifier: (mod - 1) &^ lockMask}
	if m[3] != "" {
		if v, err := strconv.Atoi(m[3]); err == nil {
			pk.baseLayout, pk.hasBaseLayout = v, true
		}
	}
	return pk
}

func normalizeShiftedLetter(cp, mod int) int {
	if mod&^lockMask&modShift != 0 && cp >= 'A' && cp <= 'Z' {
		return cp + 32
	}
	return cp
}

func isSymbolKey(ch byte) bool {
	return strings.IndexByte("`-=[]\\;',./!@#$%^&*()_+|~{}:<>?", ch) >= 0
}

// normalizeKittyFunctionalCodepoint maps the keypad codepoints Kitty reports to the keys they stand for.
func normalizeKittyFunctionalCodepoint(cp int) int {
	switch {
	case cp >= 57399 && cp <= 57408: // kp0-kp9
		return '0' + (cp - 57399)
	case cp == 57409:
		return '.'
	case cp == 57410:
		return '/'
	case cp == 57411:
		return '*'
	case cp == 57412:
		return '-'
	case cp == 57413:
		return '+'
	case cp == 57414:
		return cpEnter
	case cp == 57415:
		return '='
	}
	return cp
}

func matchesKitty(pk *parsedKey, expectedCP, expectedMod int) bool {
	if pk.modifier != expectedMod&^lockMask {
		return false
	}
	norm := normalizeShiftedLetter(normalizeKittyFunctionalCodepoint(pk.codepoint), pk.modifier)
	if norm == normalizeShiftedLetter(normalizeKittyFunctionalCodepoint(expectedCP), expectedMod) {
		return true
	}
	if pk.hasBaseLayout && pk.baseLayout == expectedCP {
		isLatin := norm >= 'a' && norm <= 'z'
		unit := norm & 0xFFFF
		isSym := unit < 128 && isSymbolKey(byte(unit))
		return !isLatin && !isSym
	}
	return false
}

func parseModifyOtherKeys(data string) *parsedKey {
	if len(data) < 9 || !strings.HasPrefix(data, "\x1b[27;") || data[len(data)-1] != '~' {
		return nil
	}
	parts := strings.SplitN(data[5:len(data)-1], ";", 2)
	if len(parts) != 2 {
		return nil
	}
	mod, e1 := strconv.Atoi(parts[0])
	cp, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return nil
	}
	return &parsedKey{codepoint: cp, modifier: (mod - 1) &^ lockMask}
}

func parseModifiedArrow(data string) *parsedKey {
	if len(data) < 6 || !strings.HasPrefix(data, "\x1b[1;") {
		return nil
	}
	final := data[len(data)-1]
	cp, ok := arrowFinals[final]
	if !ok {
		if cp, ok = homeEndFinals[final]; !ok {
			return nil
		}
	}
	mod, _, _ := strings.Cut(data[4:len(data)-1], ":")
	v, err := strconv.Atoi(mod)
	if err != nil {
		return nil
	}
	return &parsedKey{codepoint: cp, modifier: (v - 1) &^ lockMask}
}

func parseModifiedFunc(data string) *parsedKey {
	if len(data) < 4 || !strings.HasPrefix(data, "\x1b[") || data[len(data)-1] != '~' {
		return nil
	}
	params, event, hasEvent := strings.Cut(data[2:len(data)-1], ":")
	if hasEvent {
		if _, err := strconv.Atoi(event); err != nil {
			return nil
		}
	}
	numText, modText, hasMod := strings.Cut(params, ";")
	mod := 1
	if hasMod {
		v, err := strconv.Atoi(modText)
		if err != nil {
			return nil
		}
		mod = v
	}
	n, err := strconv.Atoi(numText)
	if err != nil {
		return nil
	}
	cp, ok := funcKeyNumbers[n]
	if !ok {
		return nil
	}
	return &parsedKey{codepoint: cp, modifier: (mod - 1) &^ lockMask}
}

func parseTerminalInput(data string) *parsedKey {
	if pk := parseModifyOtherKeys(data); pk != nil {
		if pk.modifier&modShift != 0 && pk.codepoint >= 'A' && pk.codepoint <= 'Z' {
			pk.codepoint += 32
		}
		return pk
	}
	if pk := parseModifiedArrow(data); pk != nil {
		return pk
	}
	return parseModifiedFunc(data)
}

func parseKeyID(keyID string) (base string, mod int, ok bool) {
	parts := strings.Split(strings.ToLower(keyID), "+")
	base = parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			mod |= modCtrl
		case "shift":
			mod |= modShift
		case "alt":
			mod |= modAlt
		case "super":
			mod |= modSuper
		default:
			return "", 0, false
		}
	}
	return base, mod, true
}

func resolveKeyCodepoint(base string) (int, bool) {
	if k, ok := namedKeys[base]; ok {
		return k.cp, true
	}
	if len(base) == 1 {
		ch := base[0]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || isSymbolKey(ch) {
			return int(ch), true
		}
	}
	return 0, false
}

func rawCtrlChar(key byte) byte {
	switch {
	case key >= 'a' && key <= 'z', key == '[', key == '\\', key == ']', key == '_':
		return key & 0x1f
	case key == '-':
		return 31
	}
	return 0
}

// matchesKey is matchesKey(data, keyId): whether raw terminal input is the key named by keyID ("ctrl+alt+h", "up", "f5").
func matchesKey(data, keyID string) bool {
	base, mod, ok := parseKeyID(keyID)
	if !ok {
		return false
	}
	cp, dynamic := resolveKeyCodepoint(base)
	if !dynamic {
		for _, seq := range functionKeys[strings.ToLower(keyID)] {
			if seq == data {
				return true
			}
		}
		return false
	}
	if pk := parseCSIu(data); pk != nil {
		if matchesKitty(pk, cp, mod) {
			return true
		}
		return cp == cpEnter && matchesKitty(pk, cpKPEnter, mod)
	}
	if pk := parseTerminalInput(data); pk != nil {
		return pk.codepoint == cp && pk.modifier == mod
	}
	return matchesLegacy(data, base, mod)
}

func matchesLegacy(data, base string, mod int) bool {
	switch base {
	case "escape", "esc":
		return mod == 0 && data == "\x1b"
	case "enter", "return":
		switch mod {
		case 0:
			return data == "\r" || data == "\n" || data == "\x1bOM"
		case modAlt:
			return data == "\x1b\r"
		}
		return false
	case "tab":
		return mod == 0 && data == "\t" || mod == modShift && data == "\x1b[Z"
	case "space":
		switch mod {
		case 0:
			return data == " "
		case modCtrl:
			return data == "\x00"
		case modAlt:
			return data == "\x1b "
		}
		return false
	case "backspace":
		switch mod {
		case 0:
			return data == "\x7f" || data == "\x08"
		case modCtrl:
			return data == "\x08"
		case modAlt:
			return data == "\x1b\x7f" || data == "\x1b\x08"
		}
		return false
	case "delete":
		if mod == 0 {
			return data == "\x1b[3~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "insert":
		if mod == 0 {
			return data == "\x1b[2~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "home":
		if mod == 0 {
			return data == "\x1b[H" || data == "\x1bOH" || data == "\x1b[1~" || data == "\x1b[7~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "end":
		if mod == 0 {
			return data == "\x1b[F" || data == "\x1bOF" || data == "\x1b[4~" || data == "\x1b[8~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "pageup":
		if mod == 0 {
			return data == "\x1b[5~" || data == "\x1b[[5~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "pagedown":
		if mod == 0 {
			return data == "\x1b[6~" || data == "\x1b[[6~"
		}
		return matchesLegacyModSeq(data, base, mod)
	case "up", "down", "left", "right":
		letter := map[string]byte{"up": 'A', "down": 'B', "right": 'C', "left": 'D'}[base]
		switch mod {
		case 0:
			return data == "\x1b["+string(letter) || data == "\x1bO"+string(letter)
		case modAlt:
			alt := map[string][]string{"up": {"\x1bp"}, "down": {"\x1bn"}, "left": {"\x1bb", "\x1bB"}, "right": {"\x1bf", "\x1bF"}}[base]
			for _, a := range alt {
				if data == a {
					return true
				}
			}
			return data == "\x1b[1;3"+string(letter)
		}
		return matchesLegacyModSeq(data, base, mod)
	}
	if len(base) == 1 {
		ch := base[0]
		switch {
		case mod == 0:
			return len(data) == 1 && data[0] == ch
		case mod == modCtrl:
			if rc := rawCtrlChar(ch); rc != 0 {
				return len(data) == 1 && data[0] == rc
			}
		case mod == modAlt:
			return len(data) == 2 && data[0] == '\x1b' && data[1] == ch
		case mod == modCtrl|modAlt:
			if rc := rawCtrlChar(ch); rc != 0 {
				return len(data) == 2 && data[0] == '\x1b' && data[1] == rc
			}
		case mod == modShift && ch >= 'a' && ch <= 'z':
			return len(data) == 1 && data[0] == ch-32
		}
	}
	return false
}

func matchesLegacyModSeq(data, base string, mod int) bool {
	if k, ok := namedKeys[base]; ok {
		m := strconv.Itoa(mod + 1)
		if k.isArrow {
			if data == fmt.Sprintf("\x1b[1;%s%c", m, arrowFinalOf[k.cp]) {
				return true
			}
		} else if f, ok := homeEndFinalOf[k.cp]; ok && data == fmt.Sprintf("\x1b[1;%s%c", m, f) {
			return true
		}
		if n, ok := funcNumberOf[k.cp]; ok && data == fmt.Sprintf("\x1b[%d;%s~", n, m) {
			return true
		}
	}
	table := map[int]map[string]string{
		modShift: {"up": "\x1b[a", "down": "\x1b[b", "right": "\x1b[c", "left": "\x1b[d", "insert": "\x1b[2$", "delete": "\x1b[3$",
			"pageup": "\x1b[5$", "pagedown": "\x1b[6$", "home": "\x1b[7$", "end": "\x1b[8$"},
		modCtrl: {"up": "\x1bOa", "down": "\x1bOb", "right": "\x1bOc", "left": "\x1bOd", "insert": "\x1b[2^", "delete": "\x1b[3^",
			"pageup": "\x1b[5^", "pagedown": "\x1b[6^", "home": "\x1b[7^", "end": "\x1b[8^"},
	}
	if seq, ok := table[mod][base]; ok {
		return data == seq
	}
	return false
}

// isKeyRelease is isKeyRelease(data): a Kitty key-release chunk (event 3). Bracketed paste is never a release.
func isKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, s := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, s) {
			return true
		}
	}
	return false
}
