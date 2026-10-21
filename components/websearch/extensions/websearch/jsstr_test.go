package websearch

import (
	"math/rand"
	"strings"
	"testing"
)

// jsLenRef and jsPrefixRef are the rune-at-a-time definitions the block-wise versions replace.
func jsLenRef(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func jsPrefixRef(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += width
	}
	return s
}

// jsStringCases mixes ASCII runs of every alignment with multi-byte runes, astral runes and invalid bytes.
func jsStringCases() []string {
	rng := rand.New(rand.NewSource(7))
	pieces := []string{"a", "plain ascii text of some length that spans blocks. ", "é", "日本語", "😀", "\xff", "\xc3", "\xf0\x9f", "\x00", strings.Repeat("x", 31), strings.Repeat("y", 33)}
	cases := []string{"", "a", strings.Repeat("a", 32), strings.Repeat("a", 33), strings.Repeat("a", 31) + "😀", strings.Repeat("a", 30) + "😀" + strings.Repeat("b", 40)}
	for range 400 {
		var b strings.Builder
		for range rng.Intn(30) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		cases = append(cases, b.String())
	}
	return cases
}

func TestJSLenMatchesTheRuneLoop(t *testing.T) {
	for _, s := range jsStringCases() {
		if got, want := jsLen(s), jsLenRef(s); got != want {
			t.Fatalf("jsLen(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestJSPrefixMatchesTheRuneLoop(t *testing.T) {
	for _, s := range jsStringCases() {
		for _, n := range []int{0, 1, 2, 7, 31, 32, 33, 64, 100, jsLenRef(s) - 1, jsLenRef(s), jsLenRef(s) + 1} {
			if n < 0 {
				continue
			}
			if got, want := jsPrefix(s, n), jsPrefixRef(s, n); got != want {
				t.Fatalf("jsPrefix(%q, %d) = %q, want %q", s, n, got, want)
			}
		}
	}
}

func BenchmarkJSLen2MB(b *testing.B) {
	page := strings.Repeat("The page text goes on and on. ", 70000)
	b.SetBytes(int64(len(page)))
	for i := 0; i < b.N; i++ {
		if jsLen(page) != len(page) {
			b.Fatal("length")
		}
	}
}
