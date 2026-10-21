package pi_permission_system

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

// u16 and s16 take an ASCII fast path; both must agree with the rune-slice and utf16 round trip they replace, on every shape of input.
func TestU16S16AgreeWithTheGeneralConversion(t *testing.T) {
	inputs := []string{"", "a", "/projects/my-app/src", `C:\Projects\My-App`, "é", "a/é/b", "日本語/パス", "😀", "a😀b", "\xff", "a\xffb", "\xed\xa0\x80", "\x00", "~", "\x7f", "\x80"}
	for _, s := range inputs {
		want := utf16.Encode([]rune(s))
		if got := u16(s); !reflect.DeepEqual(got, append([]uint16{}, want...)) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("u16(%q) = %v, want %v", s, got, want)
		}
		units := utf16.Encode([]rune(s))
		if got, want := s16(units), string(utf16.Decode(units)); got != want {
			t.Errorf("s16(%v) = %q, want %q", units, got, want)
		}
	}
	for _, units := range [][]uint16{nil, {}, {0xd800}, {0xdc00, 'a'}, {'a', 0xd83d, 0xde00}, {0x7f}, {0x80}} {
		if got, want := s16(units), string(utf16.Decode(units)); got != want {
			t.Errorf("s16(%v) = %q, want %q", units, got, want)
		}
	}
}
