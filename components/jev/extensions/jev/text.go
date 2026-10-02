package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

// The original measures strings in UTF-16 code units (JavaScript .length and
// .slice). Every limit here uses the same unit so a configured 400 keeps meaning
// what the README says.

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// sliceUTF16 returns the first n UTF-16 units of s. JavaScript can cut a
// surrogate pair in two and send a lone surrogate; a Go string cannot hold one,
// so a cut through a pair yields U+FFFD in its place.
func sliceUTF16(s string, n int) string {
	if n <= 0 {
		return ""
	}
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			if units < n { // half of a pair
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// elide keeps the first max units and states how many were cut, as
// `${value.slice(0, max)}…[N chars elided]`.
func elide(s string, max int) string {
	total := utf16Len(s)
	if total <= max {
		return s
	}
	return fmt.Sprintf("%s…[%d chars elided]", sliceUTF16(s, max), total-max)
}

// truncateText is the gate's user-request cut: `…[truncated]`.
func truncateText(s string, max int) string {
	if utf16Len(s) <= max {
		return s
	}
	return sliceUTF16(s, max) + "…[truncated]"
}

// toFixed2 is JavaScript's Number.prototype.toFixed(2): the exact decimal value
// of the double, a tie rounds up (0.125 gives 0.13, where Go's %.2f gives 0.12).
func toFixed2(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	}
	neg := x < 0
	r := new(big.Rat).SetFloat64(math.Abs(x))
	r.Mul(r, big.NewRat(100, 1))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Div(r.Num(), r.Denom())
	digits := n.String()
	for len(digits) < 3 {
		digits = "0" + digits
	}
	out := digits[:len(digits)-2] + "." + digits[len(digits)-2:]
	if neg {
		out = "-" + out
	}
	return out
}

// jsonString encodes s without HTML escaping.
func jsonString(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func jsonValue(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

const maxNesting = 64

// summarize copies a decoded JSON value, eliding every string longer than max
// (at any depth: the original stopped at depth 4 and let deeper strings out
// whole, see PORT.md C7). Keys are written sorted; the SDK hands events over as
// Go maps, so the wire order of the original's argument keys is not available.
func summarize(v any, max, depth int) any {
	if depth > maxNesting {
		return "…[nested value elided]"
	}
	switch t := v.(type) {
	case string:
		return elide(t, max)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = summarize(item, max, depth+1)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = summarize(item, max, depth+1)
		}
		return out
	}
	return v
}

// stableKey is a deterministic rendering of a JSON value (sorted keys).
func stableKey(v any) string {
	return string(jsonValue(v))
}

// platform is process.platform as Node spells it.
func platform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collapse folds whitespace runs and trims, as the original's error text does.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func cut(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}
