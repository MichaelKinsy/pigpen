// Package mapper translates pi's agent event stream into AHP chat-channel actions and pi's
// prompt shapes into and out of AHP messages. It ports pi-ahp's src/pi/event-mapper.ts,
// activity.ts, user-message.ts, message-input.ts, image-mime.ts and session-title.ts.
//
// The mapper is a pure function of the event stream: feed it recorded events and it yields the
// actions a client would have seen. It imports nothing from PiG, so it is tested offline.
//
// Events are the JSON objects Pi emits (decoded to map[string]any). Strings measured in the
// upstream with JavaScript's `.length` are measured here in UTF-16 code units so that shortened
// descriptions cut where the upstream cuts.
package mapper

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf16"
)

// IsJSSpace matches JavaScript's `\s`.
func IsJSSpace(r rune) bool { return isJSSpace(r) }

// isJSSpace matches JavaScript's `\s`.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// collapseSpace is `value.replace(/\s+/gu, " ").trim()`.
func collapseSpace(s string) string {
	var sb strings.Builder
	pendingSpace := false
	for _, r := range s {
		if isJSSpace(r) {
			pendingSpace = true
			continue
		}
		if pendingSpace && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		pendingSpace = false
		sb.WriteRune(r)
	}
	return sb.String()
}

// trimJS is String.prototype.trim.
func trimJS(s string) string { return strings.TrimFunc(s, isJSSpace) }

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// sliceUTF16 is s.slice(from, to) over UTF-16 code units. A cut inside a surrogate pair yields
// U+FFFD (Go strings cannot hold a lone surrogate).
func sliceUTF16(s string, from, to int) string {
	u := utf16.Encode([]rune(s))
	if from < 0 {
		from = 0
	}
	if to > len(u) {
		to = len(u)
	}
	if from >= to {
		return ""
	}
	return string(utf16.Decode(u[from:to]))
}

// dig walks decoded JSON objects (and arrays for int keys); missing steps yield nil.
func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k < 0 || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func digStr(v any, path ...any) (string, bool) {
	s, ok := dig(v, path...).(string)
	return s, ok
}

func digStrOr(v any, def string, path ...any) string {
	if s, ok := digStr(v, path...); ok {
		return s
	}
	return def
}

// num reads a JSON number (float64 from decoding, or a Go integer from tests).
func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func intOr(v any, def int) int {
	if f, ok := num(v); ok {
		return int(f)
	}
	return def
}

func strPtr(s string) *string { return &s }
func i64Ptr(i int64) *int64   { return &i }

// Itoa formats an int.
func Itoa(i int) string { return strconv.Itoa(i) }
