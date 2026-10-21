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

func TestJSSliceAndSanitizeAgreeAcrossTheASCIIFastPath(t *testing.T) {
	generalSlice := func(s string, start, end int) string {
		u := utf16.Encode([]rune(s))
		from, to := jsSliceBounds(len(u), start, end)
		if from >= to {
			return ""
		}
		return string(utf16.Decode(u[from:to]))
	}
	for _, s := range []string{"", "abc", "mcp__danger_srv__wipe", "danger-srv", "é", "a😀b"} {
		for _, start := range []int{-10, -2, -1, 0, 1, 2, 3, 40} {
			for _, end := range []int{-10, -1, 0, 1, 2, 3, 40, jsEnd} {
				if got, want := jsSlice(s, start, end), generalSlice(s, start, end); got != want {
					t.Errorf("jsSlice(%q, %d, %d) = %q, want %q", s, start, end, got, want)
				}
			}
		}
	}
	for in, want := range map[string]string{"": "", "danger-srv": "danger_srv", "a.b c": "a_b_c", "ok_09AZ": "ok_09AZ", "é": "_", "a😀b": "a__b"} {
		if got := sanitizePiMcpName(in); got != want {
			t.Errorf("sanitizePiMcpName(%q) = %q, want %q", in, got, want)
		}
	}
}
