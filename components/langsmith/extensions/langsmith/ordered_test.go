package langsmith

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCmpUTF16MatchesUTF16Order(t *testing.T) {
	alphabet := []rune{'a', 'b', 'Z', '0', '9', 0x7f, 0x80, 0xff, 0x7ff, 0x800, 0xd7ff, 0xe000, 0xfffd, 0xffff, 0x10000, 0x1f600, 0x10ffff}
	rng := rand.New(rand.NewSource(7))
	gen := func() string {
		var b strings.Builder
		for n := rng.Intn(4); n >= 0; n-- {
			b.WriteRune(alphabet[rng.Intn(len(alphabet))])
		}
		return b.String()
	}
	for i := 0; i < 200000; i++ {
		a, b := gen(), gen()
		want := cmpUTF16Slow(a, b)
		if got := cmpUTF16(a, b); got != want {
			t.Fatalf("cmpUTF16(%q, %q) = %d, want %d", a, b, got, want)
		}
	}
}

func TestSortedKeysJSOrder(t *testing.T) {
	m := map[string]any{"b": 1, "10": 2, "2": 3, "a": 4, "\U0001F600": 5, "\uffff": 6, "01": 7, "": 8}
	got := sortedKeys(m)
	want := []string{"2", "10", "", "01", "a", "b", "\U0001F600", "\uffff"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestStringifyMapOrderAndUndefined(t *testing.T) {
	out, ok, err := stringify(map[string]any{"b": 1.0, "a": undef, "10": "x", "2": nil})
	if err != nil || !ok {
		t.Fatal(err)
	}
	if string(out) != `{"2":null,"10":"x","b":1}` {
		t.Fatalf("got %s", out)
	}
}

func BenchmarkSortedKeys(b *testing.B) {
	m := map[string]any{"role": 1, "content": 2, "api": 3, "provider": 4, "model": 5, "usage": 6, "stopReason": 7, "timestamp": 8, "toolCallId": 9}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sortedKeys(m)
	}
}
