package keys

import "testing"

func TestDecodeNamesEveryKeyOrpheusBinds(t *testing.T) {
	// The default keys of Orpheus (README, "Keys") in the encodings PiG can deliver:
	// legacy, application cursor, Kitty CSI-u and xterm modifyOtherKeys.
	cases := []struct {
		name  string
		input []string
		want  string
	}{
		{"play/pause", []string{" ", "\x1b[32u", "\x1b[27;1;32~"}, "space"},
		{"next", []string{"n", "\x1b[110u"}, "n"},
		{"previous", []string{"p"}, "p"},
		{"seek back", []string{"\x1b[D", "\x1bOD", "\x1b[1;1D"}, "left"},
		{"seek forward", []string{"\x1b[C", "\x1bOC"}, "right"},
		{"up", []string{"\x1b[A", "\x1bOA"}, "up"},
		{"down", []string{"\x1b[B", "\x1bOB"}, "down"},
		{"volume up", []string{"+", "\x1b[61:43;2u"}, "+"},
		{"volume down", []string{"-"}, "-"},
		{"shuffle", []string{"s"}, "s"},
		{"repeat", []string{"l"}, "l"},
		{"switch tab", []string{"\t"}, "tab"},
		{"previous tab", []string{"\x1b[Z", "\x1b[1;2Z"}, "shift+tab"},
		{"select", []string{"\r", "\n", "\x1b[13u", "\x1b[13;1u", "\x1b[27;1;13~"}, "enter"},
		{"remove from queue", []string{"x"}, "x"},
		{"reorder up", []string{"["}, "["},
		{"reorder down", []string{"]"}, "]"},
		{"settings", []string{"o"}, "o"},
		{"help", []string{"?", "\x1b[63:63;2u"}, "?"},
		{"quit", []string{"q"}, "q"},
		{"filter", []string{"/"}, "/"},
		{"escape", []string{"\x1b", "\x1b[27u"}, "escape"},
		{"force quit", []string{"\x03", "\x1b[99;5u", "\x1b[27;5;99~"}, "ctrl+c"},
	}
	for _, c := range cases {
		for _, in := range c.input {
			got, ok := Decode(in)
			if !ok || got.Name != c.want || got.Release {
				t.Errorf("%s: Decode(%q) = %+v, %v; want name %q", c.name, in, got, ok, c.want)
			}
		}
	}
}

func TestDecodeFoldsModifiersIntoTheName(t *testing.T) {
	cases := map[string]string{
		"\x1b[1;5A":     "ctrl+up",
		"\x1b[1;3C":     "alt+right",
		"\x1b[1;6B":     "ctrl+shift+down",
		"\x1b[97;5u":    "ctrl+a",
		"\x1b[97;7u":    "ctrl+alt+a",
		"\x1b[97;2u":    "A",
		"\x1b[97:65;2u": "A",
		"\x1b[49:33;2u": "!",
		"\x1b[97;65u":   "a", // caps lock is not a modifier
		"\x1b[97;69u":   "ctrl+a",
		"\x1b[27;5;97~": "ctrl+a",
		"\x1b[3~":       "delete",
		"\x1b[3;5~":     "ctrl+delete",
		"\x1b[5~":       "pgup",
		"\x1b[6~":       "pgdn",
		"\x1b[1~":       "home",
		"\x1b[H":        "home",
		"\x1bOF":        "end",
		"\x1bOP":        "f1",
		"\x1b[15~":      "f5",
		"\x1ba":         "alt+a",
		"\x1b\x7f":      "alt+backspace",
		"\x7f":          "backspace",
		"\x00":          "ctrl+space",
		"\x01":          "ctrl+a",
		"\x1a":          "ctrl+z",
		"\x1d":          "ctrl+]",
		"\x1b[57414u":   "enter",
		"\x1b[13;5u":    "ctrl+enter",
	}
	for in, want := range cases {
		got, ok := Decode(in)
		if !ok || got.Name != want {
			t.Errorf("Decode(%q) = %+v, %v; want %q", in, got, ok, want)
		}
	}
}

func TestDecodeReportsKittyReleaseEvents(t *testing.T) {
	for _, in := range []string{"\x1b[97;1:3u", "\x1b[1;1:3A", "\x1b[3;1:3~", "\x1b[27;1:3u"} {
		got, ok := Decode(in)
		if !ok || !got.Release {
			t.Errorf("Decode(%q) = %+v, %v; want a release", in, got, ok)
		}
	}
	// Press and repeat events are presses.
	for _, in := range []string{"\x1b[97;1:1u", "\x1b[97;1:2u", "\x1b[1;1:2A"} {
		if got, ok := Decode(in); !ok || got.Release {
			t.Errorf("Decode(%q) = %+v, %v; want a press", in, got, ok)
		}
	}
}

func TestDecodeTextAndPaste(t *testing.T) {
	got, ok := Decode("é")
	if !ok || got.Name != "é" || got.Text != "é" {
		t.Errorf("é = %+v %v", got, ok)
	}
	got, ok = Decode("héllo")
	if !ok || got.Name != "text" || got.Text != "héllo" {
		t.Errorf("multi-rune = %+v %v", got, ok)
	}
	got, ok = Decode("\x1b[200~two words\x1b[201~")
	if !ok || got.Name != "paste" || got.Text != "two words" {
		t.Errorf("paste = %+v %v", got, ok)
	}
	if got, _ := Decode(" "); got.Text != " " {
		t.Errorf("space text = %q", got.Text)
	}
	if got, _ := Decode("\x1b[97;5u"); got.Text != "" {
		t.Errorf("ctrl+a produced text %q", got.Text)
	}
}

func TestDecodeIgnoresWhatItCannotName(t *testing.T) {
	for _, in := range []string{
		"", "\x1b[<0;10;5M", "\x1b[<64;1;1m", "\x1b[I", "\x1b[O", "\x1b[?1;2c", "\x1b[", "\x1b[99999~",
		"\x1b[57358u", "\x1b[57441;1u", "\x1b[xyz;u", "\xff", "\x1b[27;5;zz~", "\x1bOZ",
	} {
		if got, ok := Decode(in); ok && got.Name != "" {
			t.Errorf("Decode(%q) = %+v, want unnamed", in, got)
		}
	}
}
