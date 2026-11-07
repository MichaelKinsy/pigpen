package posthorse

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// jsSample builds strings from a pool that exercises every representation: ASCII, BMP, astral
// characters and lone surrogates (in the WTF-8 form of the SDK).
func jsSample(r *rand.Rand, max int) string {
	pool := [][]uint16{{'a'}, {'b'}, {' '}, {'\n'}, {0xE9}, {0x20AC}, {0xD83D, 0xDE00}, {0xD83D, 0xDC4D}, {0xD83D}, {0xDE00}, {0xFEFF}}
	var units []uint16
	for range r.IntN(max + 1) {
		units = append(units, pool[r.IntN(len(pool))]...)
	}
	return u16string(units)
}

func TestU16Len(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want int
	}{
		{"", 0}, {"abc", 3}, {"é€", 2}, {"😀", 2}, {"a😀b", 4},
		{u16string([]uint16{0xD83D}), 1}, {u16string([]uint16{0xDE00, 0xD83D}), 2},
		// Two cut halves written next to each other count as the astral character they form.
		{u16string([]uint16{0xD83D}) + u16string([]uint16{0xDE00}), 2},
	} {
		if got := u16len(tc.s); got != tc.want {
			t.Errorf("u16len(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestU16SliceTable(t *testing.T) {
	smile := "😀"
	high, low := u16string([]uint16{0xD83D}), u16string([]uint16{0xDE00})
	for _, tc := range []struct {
		name       string
		s          string
		start, end int
		want       string
	}{
		{"ascii", "hello", 1, 3, "el"},
		{"negative start", "hello", -3, u16end, "llo"},
		{"negative end", "hello", 0, -1, "hell"},
		{"clamped", "hello", -99, 99, "hello"},
		{"empty when start >= end", "hello", 3, 3, ""},
		{"reversed", "hello", 4, 1, ""},
		{"astral whole", "a" + smile + "b", 1, 3, smile},
		{"cut after the high half", "a" + smile + "b", 0, 2, "a" + high},
		{"cut before the low half", "a" + smile + "b", 2, u16end, low + "b"},
		{"both halves cut", smile + smile, 1, 3, low + high},
		{"inside one character", smile, 1, 2, low},
		{"non-ASCII negative", "é€x", -2, u16end, "€x"},
	} {
		if got := u16slice(tc.s, tc.start, tc.end); got != tc.want {
			t.Errorf("%s: u16slice(%q, %d, %d) = %q, want %q", tc.name, tc.s, tc.start, tc.end, got, tc.want)
		}
	}
}

// jsRefSlice is `s.slice(start, end)` over code units.
func jsRefSlice(units []uint16, start, end int) []uint16 {
	n := len(units)
	rel := func(i int) int {
		if i < 0 {
			return max(n+i, 0)
		}
		return min(i, n)
	}
	from, to := rel(start), rel(end)
	if from >= to {
		return nil
	}
	return units[from:to]
}

func TestU16SliceMatchesUnitReference(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	for range 4000 {
		s := jsSample(r, 12)
		units := u16units(s)
		start, end := r.IntN(2*len(units)+5)-len(units)-2, r.IntN(2*len(units)+5)-len(units)-2
		if r.IntN(5) == 0 {
			end = u16end
		}
		want := u16string(jsRefSlice(units, start, end))
		if got := u16slice(s, start, end); u16len(got) != len(jsRefSlice(units, start, end)) || !equalUnits(u16units(got), u16units(want)) {
			t.Fatalf("u16slice(%q, %d, %d) = %q (%v), want %q (%v)", s, start, end, got, u16units(got), want, u16units(want))
		}
	}
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestU16SubstringAndAt(t *testing.T) {
	if got := u16substring("hello", 3, 1); got != "el" {
		t.Errorf("swapped substring = %q", got)
	}
	if got := u16substring("hello", -5, 2); got != "he" {
		t.Errorf("negative start = %q", got)
	}
	if got := u16substring("a😀b", 1, 2); got != u16string([]uint16{0xD83D}) {
		t.Errorf("cut substring = %q", got)
	}
	r := rand.New(rand.NewPCG(2, 2))
	for range 2000 {
		s := jsSample(r, 10)
		units := u16units(s)
		for i := -1; i <= len(units); i++ {
			want := ""
			if i >= 0 && i < len(units) {
				want = u16string(units[i : i+1])
			}
			if got := u16at(s, i); !equalUnits(u16units(got), u16units(want)) {
				t.Fatalf("u16at(%q, %d) = %q, want %q", s, i, got, want)
			}
			unit, ok := u16code(s, i)
			if ok != (want != "") || ok && unit != units[i] {
				t.Fatalf("u16code(%q, %d) = %d,%v", s, i, unit, ok)
			}
		}
	}
}

func TestU16IndexMatchesUnitReference(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 3))
	for range 6000 {
		s := jsSample(r, 10)
		sub := jsSample(r, 3)
		if r.IntN(2) == 0 && len(s) > 0 {
			// a substring of s found at a unit boundary
			units := u16units(s)
			a := r.IntN(len(units) + 1)
			sub = u16string(units[a:min(len(units), a+r.IntN(4))])
		}
		from := r.IntN(len(u16units(s))+4) - 1
		want := unitIndex(u16units(s), u16units(sub), max(from, 0))
		if got := u16index(s, sub, from); got != want {
			t.Fatalf("u16index(%q, %q, %d) = %d, want %d", s, sub, from, got, want)
		}
	}
	if got := u16index("a😀b", "b", 0); got != 3 {
		t.Errorf("index after an astral character = %d, want 3", got)
	}
	if got := u16index("a😀b", u16string([]uint16{0xDE00}), 0); got != 2 {
		t.Errorf("a lone low surrogate matches the second half of a pair at 2, got %d", got)
	}
	if got := u16index("abc", "", 99); got != 3 {
		t.Errorf("empty needle clamps from to the length, got %d", got)
	}
}

func TestU16LastIndex(t *testing.T) {
	for _, tc := range []struct {
		s, sub string
		from   int
		want   int
	}{
		{"abcabc", "bc", u16end, 4}, {"abcabc", "bc", 3, 1}, {"abcabc", "bc", 4, 4}, {"abc", "x", u16end, -1},
		{"a😀a😀", "😀", u16end, 4}, {"a😀a😀", "😀", 2, 1}, {"abc", "", u16end, 3},
	} {
		if got := u16lastIndex(tc.s, tc.sub, tc.from); got != tc.want {
			t.Errorf("u16lastIndex(%q, %q, %d) = %d, want %d", tc.s, tc.sub, tc.from, got, tc.want)
		}
	}
}

func TestJSToInt(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int
	}{{math.NaN(), 0}, {1.9, 1}, {-1.9, -1}, {math.Inf(1), math.MaxInt}, {math.Inf(-1), math.MinInt}, {1e30, math.MaxInt}} {
		if got := jsToInt(tc.in); got != tc.want {
			t.Errorf("jsToInt(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if got := u16sliceF("hello", math.NaN(), 2.7); got != "he" {
		t.Errorf("u16sliceF NaN start = %q", got)
	}
	if got := u16sliceF("hello", 1, math.Inf(1)); got != "ello" {
		t.Errorf("u16sliceF infinite end = %q", got)
	}
}

func TestJSToLocaleInt(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 83617: "83,617", 1234567: "1,234,567", -1234: "-1,234", -999: "-999", 100000: "100,000", 9007199254740991: "9,007,199,254,740,991"} {
		if got := jsToLocaleInt(n); got != want {
			t.Errorf("jsToLocaleInt(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestJSRound(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{2.5, 3}, {-2.5, -2}, {2.4, 2}, {-2.6, -3}, {0.5, 1}, {-0.5, 0}, {0.49999999999999994, 0}, {1e300, 1e300}, {36, 36}, {35.5, 36},
	} {
		if got := jsRound(tc.in); got != tc.want {
			t.Errorf("jsRound(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if got := jsRound(-0.4); !(got == 0 && math.Signbit(got)) {
		t.Errorf("jsRound(-0.4) = %v, want -0", got)
	}
	if !math.IsNaN(jsRound(math.NaN())) || !math.IsInf(jsRound(math.Inf(1)), 1) {
		t.Error("NaN and infinities pass through")
	}
}

func TestJSTrim(t *testing.T) {
	if got := jsTrim("\uFEFF \u00A0\u3000x y\u2028\t\n"); got != "x y" {
		t.Errorf("jsTrim = %q", got)
	}
	if got := jsTrim("\u0085x\u0085"); got != "\u0085x\u0085" {
		t.Errorf("U+0085 is not JavaScript whitespace: %q", got)
	}
	if got := jsTrimStart("  x "); got != "x " {
		t.Errorf("jsTrimStart = %q", got)
	}
	if got := jsTrimEnd("  x "); got != "  x" {
		t.Errorf("jsTrimEnd = %q", got)
	}
}

func TestJSFromBytes(t *testing.T) {
	const fffd = "\uFFFD"
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"plain é€😀", "plain é€😀"},
		{"\xe2\x82", fffd},                   // truncated three-byte sequence: one replacement
		{"\xf0\x9f\x98", fffd},               // truncated four-byte sequence
		{"\xed\xa0\x80", fffd + fffd + fffd}, // an encoded surrogate is three ill-formed subparts
		{"\xff", fffd}, {"\x80\x80", fffd + fffd},
		{"a\xc3(b", "a" + fffd + "(b"},
		{"\xe2\x82a", fffd + "a"},
		{"\xf4\x90\x80\x80", fffd + fffd + fffd + fffd},
		{"\xef\xbb\xbfbom", "\uFEFFbom"}, // no BOM stripping
	} {
		if got := jsFromBytes([]byte(tc.in)); got != tc.want {
			t.Errorf("jsFromBytes(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := string(appendJSFromBytes([]byte("x"), []byte(tc.in))); got != "x"+tc.want {
			t.Errorf("appendJSFromBytes(%q) = %q", tc.in, got)
		}
	}
}

func TestJSUTF8Bytes(t *testing.T) {
	high, low := u16string([]uint16{0xD83D}), u16string([]uint16{0xDE00})
	for _, tc := range []struct{ in, want string }{
		{"abc", "abc"},
		{high, "\uFFFD"},
		{low + "x", "\uFFFDx"},
		{high + low, "😀"}, // two cut halves rejoined form the character
		{low + high, "\uFFFD\uFFFD"},
	} {
		if got := string(jsUTF8Bytes(tc.in)); got != tc.want {
			t.Errorf("jsUTF8Bytes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJSToLowerCase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ABC", "abc"}, {"İstanbul", "i\u0307stanbul"}, {"ΑΣ", "ας"}, {"ΑΣΑ", "ασα"}, {"Σ", "σ"}, {"ΑΣ.", "ας."}, {"ΑΣ Α", "ας α"},
		{"É😀", "é😀"}, {"A" + u16string([]uint16{0xD83D}), "a" + u16string([]uint16{0xD83D})},
	} {
		if got := jsToLowerCase(tc.in); got != tc.want {
			t.Errorf("jsToLowerCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJSStringifyKeepsOrderAndEscapes(t *testing.T) {
	got, err := jsStringify(rec{"b": 1.0, "a": []any{"<&>", nil, true}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"a":["<&>",null,true],"b":1}`; got != want {
		t.Errorf("jsStringify = %s, want %s", got, want)
	}
	lone := u16string([]uint16{0xD83D})
	got, _ = jsStringify(lone)
	if got != `"\ud83d"` {
		t.Errorf("a lone surrogate is escaped in lower case: %s", got)
	}
}

func TestStringsInHelpers(t *testing.T) {
	// Guard: the representation helpers never read past a string's end.
	for _, s := range []string{"", "\xed", "\xed\xa0", "\xf0\x9f", strings.Repeat("\xff", 5)} {
		_ = u16len(s)
		_ = u16slice(s, 1, 3)
		_ = u16index(s, "x", 0)
		_ = u16units(s)
	}
}

func BenchmarkU16LenASCII(b *testing.B) {
	s := strings.Repeat("abcdefgh", 8192)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for range b.N {
		_ = u16len(s)
	}
}

func BenchmarkU16LenMixed(b *testing.B) {
	s := strings.Repeat("abcdéf😀h", 8192)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for range b.N {
		_ = u16len(s)
	}
}

func BenchmarkU16SliceASCII(b *testing.B) {
	s := strings.Repeat("abcdefgh", 8192)
	b.ReportAllocs()
	for range b.N {
		_ = u16slice(s, 1000, 41000)
	}
}

func BenchmarkU16SliceMixed(b *testing.B) {
	s := strings.Repeat("abcdéf😀h", 8192)
	b.ReportAllocs()
	for range b.N {
		_ = u16slice(s, 1000, 41000)
	}
}

func BenchmarkU16IndexASCII(b *testing.B) {
	s := strings.Repeat("abcdefgh", 8192) + "needle"
	b.ReportAllocs()
	for range b.N {
		_ = u16index(s, "needle", 0)
	}
}

func BenchmarkU16IndexMixed(b *testing.B) {
	s := strings.Repeat("abcdéf😀h", 8192) + "needle"
	b.ReportAllocs()
	for range b.N {
		_ = u16index(s, "needle", 0)
	}
}

func BenchmarkJSFromBytesValid(b *testing.B) {
	p := []byte(strings.Repeat("abcdéf😀h", 8192))
	b.SetBytes(int64(len(p)))
	b.ReportAllocs()
	for range b.N {
		_ = jsFromBytes(p)
	}
}

func BenchmarkJSToLocaleInt(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		_ = jsToLocaleInt(1234567)
	}
}
