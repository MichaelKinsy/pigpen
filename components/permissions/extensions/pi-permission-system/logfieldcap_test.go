package pi_permission_system

import (
	"strings"
	"testing"
)

func lgJSON(v any) string { return marshalJSON(v, "") }

func lgCap(t *testing.T, json string, w float64) string {
	t.Helper()
	return lgJSON(CapLogFieldWidths(jo(t, json), w))
}

func TestLogFieldCapTwins(t *testing.T) {
	const f = "logging/log-field-cap"
	rep := strings.Repeat
	f64 := func(v float64) *float64 { return &v }
	tw(t, f, "falls back to the built-in default", func(t *testing.T) {
		eq(t, ResolveReviewLogFieldWidth(nil), float64(DefaultReviewLogFieldMaxWidth), "default")
	})
	tw(t, f, "honors the operator's configured width", func(t *testing.T) {
		eq(t, ResolveReviewLogFieldWidth(f64(40)), 40.0, "width")
	})
	tw(t, f, "defaults to the width that bounded the tool-input preview before it", func(t *testing.T) {
		eq(t, DefaultReviewLogFieldMaxWidth, 1000, "1000")
	})
	tw(t, f, "leaves a string within the width untouched", func(t *testing.T) {
		eq(t, lgCap(t, `{"command":"ls -la"}`, 20), `{"command":"ls -la"}`, "same")
	})
	tw(t, f, "shortens an oversized string and marks it with an ellipsis", func(t *testing.T) {
		eq(t, lgCap(t, `{"command":"`+rep("a", 30)+`"}`, 10), `{"command":"`+rep("a", 10)+"\u2026"+`"}`, "cut")
	})
	tw(t, f, "applies the same width to every string, whatever the key", func(t *testing.T) {
		in := `{"command":"` + rep("b", 12) + `","path":"` + rep("c", 12) + `","event":"short"}`
		eq(t, lgCap(t, in, 5), `{"command":"`+rep("b", 5)+"\u2026"+`","path":"`+rep("c", 5)+"\u2026"+`","event":"short"}`, "cut")
	})
	tw(t, f, "bounds by length alone, never by what a value looks like", func(t *testing.T) {
		o := CapLogFieldWidths(jo(t, `{"a":"sk-`+rep("x", 40)+`","b":"`+rep("y", 43)+`"}`), 10).(*jsObject)
		a, _ := o.str("a")
		b, _ := o.str("b")
		eq(t, len(a), len(b), "same length")
	})
	tw(t, f, "passes non-string values through unchanged", func(t *testing.T) {
		d := newObject()
		d.set("count", 42.0)
		d.set("enabled", true)
		d.set("missing", nil)
		d.set("nothing", undef{})
		eq(t, CapLogFieldWidths(d, 2), d, "same")
	})
	tw(t, f, "recurses into nested objects", func(t *testing.T) {
		eq(t, lgCap(t, `{"log":{"details":{"command":"`+rep("d", 9)+`"}}}`, 4), `{"log":{"details":{"command":"`+rep("d", 4)+"\u2026"+`"}}}`, "nested")
	})
	tw(t, f, "recurses into arrays", func(t *testing.T) {
		eq(t, lgCap(t, `{"paths":["`+rep("e", 7)+`","ok"]}`, 3), `{"paths":["`+rep("e", 3)+"\u2026"+`","ok"]}`, "array")
	})
	tw(t, f, "leaves the record untouched when nothing exceeds the width", func(t *testing.T) {
		eq(t, lgCap(t, `{"command":"ls","nested":{"path":"."}}`, 100), `{"command":"ls","nested":{"path":"."}}`, "same")
	})
	t.Run("width counts UTF-16 units: an astral character costs two and a cut inside one leaves its high surrogate", func(t *testing.T) {
		got := CapLogFieldWidths("\U0001F600\U0001F600", 3)
		s, _ := SafeJSONStringify(got)
		eq(t, s, `"😀\ud83d…"`, "cut")
		eq(t, CapLogFieldWidths("\U0001F600", 2), "\U0001F600", "fits")
	})
	t.Run("a negative width cuts from the end, as slice does", func(t *testing.T) {
		eq(t, CapLogFieldWidths("abcdef", -2), "abcd\u2026", "slice(0,-2)")
	})
}
