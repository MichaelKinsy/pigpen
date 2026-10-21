package pi_background_tasks

import (
	"math"
	"testing"
)

func TestJSParseNumber(t *testing.T) {
	for _, row := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"", 0, true}, {"  ", 0, true}, {"12", 12, true}, {" 12 ", 12, true}, {"-5", -5, true}, {"+5", 5, true}, {".5", 0.5, true},
		{"5.", 5, true}, {"1e3", 1000, true}, {"1E-2", 0.01, true}, {"0x10", 16, true}, {"0b101", 5, true}, {"0o17", 15, true},
		{"-0x10", 0, false}, {"0x", 0, false}, {"1_000", 0, false}, {"abc", 0, false}, {"1e", 0, false}, {".", 0, false}, {"12abc", 0, false},
		{"Infinity", math.Inf(1), true}, {"-Infinity", math.Inf(-1), true}, {"infinity", 0, false}, {"1e999", math.Inf(1), true},
		{"\u00a012\ufeff", 12, true},
	} {
		got, ok := jsParseNumber(row.in)
		if ok != row.ok || (ok && got != row.want) {
			t.Errorf("jsParseNumber(%q) = %v, %v; want %v, %v", row.in, got, ok, row.want, row.ok)
		}
	}
}

func TestUTF16Slicing(t *testing.T) {
	s := "a😀b"
	eq(t, jsLen(s), 4, "an astral character counts two units")
	eq(t, jsTail(s, 1), "b", "tail 1")
	eq(t, jsTail(s, 2), "b", "a cut inside the pair drops its half")
	eq(t, jsTail(s, 3), "😀b", "tail 3")
	eq(t, jsTail(s, 9), s, "tail longer than the string")
	eq(t, jsTail(s, 0), "", "tail 0")
	eq(t, jsHead(s, 1), "a", "head 1")
	eq(t, jsHead(s, 2), "a", "a cut inside the pair")
	eq(t, jsHead(s, 3), "a😀", "head 3")
	eq(t, jsSliceFrom(s, 3), "b", "slice from the end of the pair")
	eq(t, jsSliceFrom(s, 2), "b", "slice from inside the pair")
}

func TestDecodeUTF8ReplacesEachMaximalSubpart(t *testing.T) {
	for _, row := range []struct{ in, want string }{
		{"abc", "abc"},
		{"\xff", "\ufffd"},
		{"\xff\xfe", "\ufffd\ufffd"},
		{"\xe2\x82", "\ufffd"},                 // a truncated three-byte sequence is one replacement
		{"\xe2\x82x", "\ufffdx"},               // and it stops at the next valid byte
		{"\xf0\x9f\x98", "\ufffd"},             // a truncated emoji
		{"\xc0\x80", "\ufffd\ufffd"},           // overlong forms are two
		{"\xed\xa0\x80", "\ufffd\ufffd\ufffd"}, // a surrogate encoding is three
		{"ok\xe2\x82\xac", "ok€"},
	} {
		eq(t, decodeUTF8([]byte(row.in)), row.want, "decode "+row.in)
	}
}

func TestJSSplitLines(t *testing.T) {
	eq(t, jsSplitLines(""), []string{""}, "empty")
	eq(t, jsSplitLines("a\r\nb\nc"), []string{"a", "b", "c"}, "crlf and lf")
	eq(t, jsSplitLines("a\n"), []string{"a", ""}, "trailing newline")
}
