package pi_permission_system

import (
	"math/big"
	"testing"
)

func lgStack(s string) *string { return &s }

func lgSafe(t *testing.T, v any) string {
	t.Helper()
	s, ok := SafeJSONStringify(v)
	if !ok {
		t.Fatalf("undefined for %#v", v)
	}
	return s
}

func TestJSONSafeStringifyTwins(t *testing.T) {
	const f = "logging/json-safe-stringify"
	tw(t, f, "serializes a plain record", func(t *testing.T) {
		o := newObject()
		o.set("toolName", "write")
		o.set("allowed", true)
		eq(t, lgSafe(t, o), `{"toolName":"write","allowed":true}`, "json")
	})
	tw(t, f, "returns undefined for a value JSON cannot represent", func(t *testing.T) {
		_, ok := SafeJSONStringify(undef{})
		eq(t, ok, false, "undefined")
		_, ok = SafeJSONStringify(func() string { return "noop" })
		eq(t, ok, false, "function")
	})
	tw(t, f, "expands an Error into name, message, and stack", func(t *testing.T) {
		o := newObject()
		o.set("error", &JSError{Name: "Error", Message: "boom", Stack: lgStack("Error: boom\n    at somewhere")})
		eq(t, lgSafe(t, o), `{"error":{"name":"Error","message":"boom","stack":"Error: boom\n    at somewhere"}}`, "json")
	})
	tw(t, f, "expands an Error at the root", func(t *testing.T) {
		eq(t, lgSafe(t, &JSError{Name: "TypeError", Message: "bad type", Stack: lgStack("trace")}),
			`{"name":"TypeError","message":"bad type","stack":"trace"}`, "json")
	})
	tw(t, f, "stringifies a bigint rather than throwing", func(t *testing.T) {
		n, _ := new(big.Int).SetString("9007199254740993", 10)
		o := newObject()
		o.set("size", n)
		eq(t, lgSafe(t, o), `{"size":"9007199254740993"}`, "json")
	})
	tw(t, f, "replaces a self-reference with the [Circular] marker", func(t *testing.T) {
		node := newObject()
		node.set("name", "root")
		node.set("self", node)
		eq(t, lgSafe(t, node), `{"name":"root","self":"[Circular]"}`, "json")
	})
	tw(t, f, "replaces a mutual reference cycle with the [Circular] marker", func(t *testing.T) {
		parent := newObject()
		parent.set("kind", "parent")
		child := newObject()
		child.set("kind", "child")
		child.set("parent", parent)
		parent.set("child", child)
		eq(t, lgSafe(t, parent), `{"kind":"parent","child":{"kind":"child","parent":"[Circular]"}}`, "json")
	})
	tw(t, f, "also marks a repeated non-cyclic reference, since seen entries are never released", func(t *testing.T) {
		shared := newObject()
		shared.set("id", 1.0)
		o := newObject()
		o.set("first", shared)
		o.set("second", shared)
		eq(t, lgSafe(t, o), `{"first":{"id":1},"second":"[Circular]"}`, "json")
	})
	t.Run("an array hole, a function in an array and a NaN", func(t *testing.T) {
		eq(t, lgSafe(t, []any{undef{}, func() {}, nanValue(), 1.5, nil}), `[null,null,null,1.5,null]`, "json")
	})
	t.Run("an astral character survives and a lone surrogate escapes", func(t *testing.T) {
		eq(t, lgSafe(t, "a\U0001F600b"), "\"a\U0001F600b\"", "astral")
		eq(t, lgSafe(t, utf16Prefix("\U0001F600", 1)), `"\ud83d"`, "lone high surrogate")
	})
}
