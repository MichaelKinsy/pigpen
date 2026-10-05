package pi_typesafe

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsonString is JSON.stringify for a string.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func number(v any) float64 {
	f, _ := v.(float64)
	return f
}

// toFixed3 is Number.prototype.toFixed(3): the exact value rounded half up in magnitude (Go's own
// formatting rounds an exact tie to even).
func toFixed3(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	neg := f < 0
	r := new(big.Rat).SetFloat64(math.Abs(f))
	r.Mul(r, big.NewRat(1000, 1))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	rem := new(big.Rat).Sub(r, new(big.Rat).SetInt(n))
	if rem.Cmp(big.NewRat(1, 2)) >= 0 {
		n.Add(n, big.NewInt(1))
	}
	digits := n.String()
	for len(digits) < 4 {
		digits = "0" + digits
	}
	out := digits[:len(digits)-3] + "." + digits[len(digits)-3:]
	if neg && strings.Trim(out, "0.") != "" {
		out = "-" + out
	}
	return out
}

// orderedIDs lists answer ids in question order when the details carry it, else sorted.
func orderedIDs(details map[string]any, answers map[string]any) []string {
	var ids []string
	seen := map[string]bool{}
	if order, ok := details["order"].([]any); ok {
		for _, o := range order {
			if id, ok := o.(string); ok {
				if _, present := answers[id]; present && !seen[id] {
					ids = append(ids, id)
					seen[id] = true
				}
			}
		}
	}
	var rest []string
	for id := range answers {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	return append(ids, rest...)
}

// probabilitiesText is JSON.stringify of a probabilities object: numeric keys ascending (a Score's levels), else sorted.
func probabilitiesText(v any) string {
	probs, _ := v.(map[string]any)
	keys := make([]string, 0, len(probs))
	for k := range probs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.Atoi(keys[i])
		b, errB := strconv.Atoi(keys[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = jsonString(k) + ":" + strconv.FormatFloat(number(probs[k]), 'f', -1, 64)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// format renders an evaluation (the tool's details) as the original's text block.
func format(details map[string]any, expanded bool) string {
	model, _ := details["model"].(string)
	lines := []string{fmt.Sprintf("TypeSafe · %s · %s ms", jsonString(model), strconv.FormatFloat(number(details["elapsedMs"]), 'f', -1, 64))}
	answers, _ := details["answers"].(map[string]any)
	for _, id := range orderedIDs(details, answers) {
		answer, _ := answers[id].(map[string]any)
		label := jsonString(id)
		switch answer["type"] {
		case "noul":
			lines = append(lines, fmt.Sprintf("%s: P(yes) = %s", label, toFixed3(number(answer["noul"]))))
		case "choice":
			choice, _ := answer["choice"].(string)
			lines = append(lines, fmt.Sprintf("%s: %s · confidence %s", label, jsonString(choice), toFixed3(number(answer["confidence"]))))
		default:
			lines = append(lines, fmt.Sprintf("%s: %s · confidence %s", label, toFixed3(number(answer["score"])), toFixed3(number(answer["confidence"]))))
		}
		if expanded && answer["type"] != "noul" {
			lines = append(lines, "  "+probabilitiesText(answer["probabilities"]))
		}
	}
	usage, _ := details["usage"].(map[string]any)
	lines = append(lines, fmt.Sprintf("%s input / %s output tokens", strconv.FormatFloat(number(usage["input_tokens"]), 'f', -1, 64), strconv.FormatFloat(number(usage["output_tokens"]), 'f', -1, 64)))
	lines = append(lines, "Confidence is distribution concentration, not proof of correctness.")
	return strings.Join(lines, "\n")
}

// cellWidth is the terminal width of a rune: 2 for East Asian wide and emoji ranges, 0 for combining marks.
func cellWidth(r rune) int {
	switch {
	case r >= 0x300 && r <= 0x36f, r == 0x200d, r >= 0xfe00 && r <= 0xfe0f:
		return 0
	case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3, r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f, r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1faff, r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func visibleWidth(s string) int {
	w := 0
	for _, r := range s {
		w += cellWidth(r)
	}
	return w
}

// wrapText breaks text into lines of at most width cells, at spaces where it can, mid-word where it must.
func wrapText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		line, lineW := "", 0
		flush := func() {
			out = append(out, strings.TrimRight(line, " "))
			line, lineW = "", 0
		}
		for _, word := range strings.SplitAfter(paragraph, " ") {
			w := visibleWidth(word)
			if lineW+w > width && line != "" {
				flush()
			}
			for w > width { // a word longer than a line
				var head strings.Builder
				hw := 0
				for len(word) > 0 {
					r, n := utf8.DecodeRuneInString(word)
					if hw+cellWidth(r) > width {
						break
					}
					head.WriteString(word[:n])
					hw += cellWidth(r)
					word = word[n:]
				}
				line, lineW = head.String(), hw
				flush()
				w = visibleWidth(word)
			}
			line += word
			lineW += w
		}
		if line != "" {
			flush()
		}
	}
	return out
}
