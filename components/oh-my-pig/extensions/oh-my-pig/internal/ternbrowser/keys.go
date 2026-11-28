package ternbrowser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Modifier is a modifier key as Tern's input steps name it.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:8
type Modifier string

const (
	ModShift Modifier = "shift"
	ModCtrl  Modifier = "ctrl"
	ModAlt   Modifier = "alt"
	ModMeta  Modifier = "meta"
)

// InputStep is one trusted input step of Tern's "input" op.
// Uses typed marshaling so zero-valued x, y, dx, dy, clicks, mods are NEVER
// dropped when required by the step kind (fixes A/input omitempty defect).
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:11
type InputStep struct {
	Type   string
	Action string
	X      float64
	Y      float64
	DX     float64
	DY     float64
	Button string
	Clicks *int
	Key    string
	Code   string
	Text   string
	Mods   []Modifier
}

func (s InputStep) MarshalJSON() ([]byte, error) {
	mods := s.Mods
	if mods == nil {
		mods = []Modifier{}
	}
	switch s.Type {
	case "mouse":
		type mouseWire struct {
			Type   string     `json:"type"`
			Action string     `json:"action"`
			X      float64    `json:"x"`
			Y      float64    `json:"y"`
			Button string     `json:"button,omitempty"`
			Clicks *int       `json:"clicks,omitempty"`
			Mods   []Modifier `json:"mods"`
		}
		return json.Marshal(mouseWire{
			Type:   "mouse",
			Action: s.Action,
			X:      s.X,
			Y:      s.Y,
			Button: s.Button,
			Clicks: s.Clicks,
			Mods:   mods,
		})
	case "wheel":
		type wheelWire struct {
			Type string     `json:"type"`
			X    float64    `json:"x"`
			Y    float64    `json:"y"`
			DX   float64    `json:"dx"`
			DY   float64    `json:"dy"`
			Mods []Modifier `json:"mods"`
		}
		return json.Marshal(wheelWire{
			Type: "wheel",
			X:    s.X,
			Y:    s.Y,
			DX:   s.DX,
			DY:   s.DY,
			Mods: mods,
		})
	case "key":
		type keyWire struct {
			Type   string     `json:"type"`
			Action string     `json:"action"`
			Key    string     `json:"key"`
			Code   string     `json:"code,omitempty"`
			Text   string     `json:"text,omitempty"`
			Mods   []Modifier `json:"mods"`
		}
		return json.Marshal(keyWire{
			Type:   "key",
			Action: s.Action,
			Key:    s.Key,
			Code:   s.Code,
			Text:   s.Text,
			Mods:   mods,
		})
	case "text":
		type textWire struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		return json.Marshal(textWire{
			Type: "text",
			Text: s.Text,
		})
	default:
		return nil, fmt.Errorf("unknown input step type %q", s.Type)
	}
}

// TernKey is a resolved key with its DOM key, optional DOM code, and optional modifier.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:26
type TernKey struct {
	Key      string
	Code     string
	Modifier Modifier
}

var modifiersMap = map[string]Modifier{
	"Shift":        ModShift,
	"ShiftLeft":    ModShift,
	"ShiftRight":   ModShift,
	"Control":      ModCtrl,
	"ControlLeft":  ModCtrl,
	"ControlRight": ModCtrl,
	"Alt":          ModAlt,
	"AltLeft":      ModAlt,
	"AltRight":     ModAlt,
	"Meta":         ModMeta,
	"MetaLeft":     ModMeta,
	"MetaRight":    ModMeta,
}

var namedKeys = map[string]TernKey{
	"\r":             {Key: "Enter", Code: "Enter"},
	"\n":             {Key: "Enter", Code: "Enter"},
	"\t":             {Key: "Tab", Code: "Tab"},
	" ":              {Key: " ", Code: "Space"},
	"Space":          {Key: " ", Code: "Space"},
	"NumpadEnter":    {Key: "Enter", Code: "NumpadEnter"},
	"NumpadAdd":      {Key: "+", Code: "NumpadAdd"},
	"NumpadSubtract": {Key: "-", Code: "NumpadSubtract"},
	"NumpadMultiply": {Key: "*", Code: "NumpadMultiply"},
	"NumpadDivide":   {Key: "/", Code: "NumpadDivide"},
	"NumpadDecimal":  {Key: ".", Code: "NumpadDecimal"},
}

var (
	passThroughKeysRE = regexp.MustCompile(`^(?:Enter|Tab|Backspace|Delete|Escape|ArrowUp|ArrowDown|ArrowLeft|ArrowRight|Home|End|PageUp|PageDown|Insert|CapsLock|ContextMenu|F(?:[1-9]|1\d|2[0-4]))$`)
	keyLetterRE       = regexp.MustCompile(`^Key([A-Z])$`)
	keyDigitRE        = regexp.MustCompile(`^(?:Digit|Numpad)(\d)$`)
)

// ResolveKey resolves one Puppeteer-style key name to a TernKey.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:67
func ResolveKey(name string) (TernKey, error) {
	if name == "" {
		return TernKey{}, fmt.Errorf("Key name must be a non-empty string")
	}
	if mod, ok := modifiersMap[name]; ok {
		base := strings.TrimSuffix(strings.TrimSuffix(name, "Left"), "Right")
		code := name
		if name == base {
			code = base + "Left"
		}
		return TernKey{Key: base, Code: code, Modifier: mod}, nil
	}
	if named, ok := namedKeys[name]; ok {
		return named, nil
	}
	if passThroughKeysRE.MatchString(name) {
		return TernKey{Key: name, Code: name}, nil
	}
	if m := keyLetterRE.FindStringSubmatch(name); m != nil {
		return TernKey{Key: strings.ToLower(m[1]), Code: name}, nil
	}
	if m := keyDigitRE.FindStringSubmatch(name); m != nil {
		return TernKey{Key: m[1], Code: name}, nil
	}
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		var code string
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			code = "Key" + strings.ToUpper(name)
		} else if r >= '0' && r <= '9' {
			code = "Digit" + name
		}
		return TernKey{Key: name, Code: code}, nil
	}
	return TernKey{}, fmt.Errorf("Unknown key %s for this browser backend", jsonQuote(name))
}

// SplitKeyCombo splits "Control+Shift+a" into its constituent key names.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:92
func SplitKeyCombo(combo string) []string {
	if len(combo) <= 1 || !strings.Contains(combo, "+") {
		return []string{combo}
	}
	parts := strings.Split(combo, "+")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
		if len(parts) > 0 {
			parts[len(parts)-1] = "+"
		}
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func effectiveKey(key TernKey, mods []Modifier) string {
	if slices.Contains(mods, ModShift) && len(key.Key) == 1 && key.Key[0] >= 'a' && key.Key[0] <= 'z' {
		return strings.ToUpper(key.Key)
	}
	return key.Key
}

// KeyDownStep constructs a key-down InputStep.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:108
func KeyDownStep(key TernKey, mods []Modifier) InputStep {
	mc := make([]Modifier, len(mods))
	copy(mc, mods)
	return InputStep{
		Type:   "key",
		Action: "down",
		Key:    effectiveKey(key, mods),
		Code:   key.Code,
		Mods:   mc,
	}
}

// KeyUpStep constructs a key-up InputStep.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:114
func KeyUpStep(key TernKey, mods []Modifier) InputStep {
	mc := make([]Modifier, len(mods))
	copy(mc, mods)
	return InputStep{
		Type:   "key",
		Action: "up",
		Key:    effectiveKey(key, mods),
		Code:   key.Code,
		Mods:   mc,
	}
}

// PressSteps builds the key-down and key-up steps for pressing combo with held modifiers.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:123
func PressSteps(combo string, held []Modifier) ([]InputStep, error) {
	parts := SplitKeyCombo(combo)
	keys := make([]TernKey, 0, len(parts))
	for _, p := range parts {
		k, err := ResolveKey(p)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	mods := make([]Modifier, len(held))
	copy(mods, held)
	steps := make([]InputStep, 0, len(keys)*2)
	for _, k := range keys {
		steps = append(steps, KeyDownStep(k, mods))
		if k.Modifier != "" && !slices.Contains(mods, k.Modifier) {
			mods = append(mods, k.Modifier)
		}
	}
	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		if k.Modifier != "" && !slices.Contains(held, k.Modifier) {
			if idx := slices.Index(mods, k.Modifier); idx >= 0 {
				mods = append(mods[:idx], mods[idx+1:]...)
			}
		}
		steps = append(steps, KeyUpStep(k, mods))
	}
	return steps, nil
}

// TypeSteps builds input steps for typing text: printable ASCII and Enter/Tab
// as key presses, anything else as inserted text.
// upstream: packages/coding-agent/src/tools/browser/tern/keys.ts:146
func TypeSteps(text string, held []Modifier) ([]InputStep, error) {
	var steps []InputStep
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r <= 0x7e) {
			ps, err := PressSteps(string(r), held)
			if err != nil {
				return nil, err
			}
			steps = append(steps, ps...)
		} else {
			steps = append(steps, InputStep{Type: "text", Text: string(r)})
		}
	}
	return steps, nil
}
