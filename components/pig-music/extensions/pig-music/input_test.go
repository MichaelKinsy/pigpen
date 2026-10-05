package pig_music

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInputBecomesTheKeysTheModelBinds(t *testing.T) {
	// The raw chunks PiG delivers, in the encodings seen in a real terminal, and what the model's String() sees.
	cases := []struct{ in, want string }{
		{" ", "space"}, {"\x1b[32u", "space"},
		{"n", "n"}, {"+", "+"}, {"-", "-"}, {"=", "="}, {"[", "["}, {"]", "]"}, {"/", "/"}, {"?", "?"}, {"A", "A"},
		{"\x1b[A", "up"}, {"\x1bOB", "down"}, {"\x1b[C", "right"}, {"\x1b[D", "left"},
		{"\r", "enter"}, {"\x1b[13u", "enter"},
		{"\x1b", "esc"}, {"\x1b[27u", "esc"},
		{"\t", "tab"}, {"\x1b[Z", "shift+tab"}, {"\x1b[9;2u", "shift+tab"},
		{"\x7f", "backspace"},
		{"\x03", "ctrl+c"}, {"\x1b[99;5u", "ctrl+c"},
		{"\x1b[1;5C", "ctrl+right"},
		{"\x1b[5~", "pgup"}, {"\x1b[6~", "pgdown"}, {"\x1b[3~", "delete"}, {"\x1b[H", "home"}, {"\x1bOF", "end"},
		{"é", "é"},
	}
	for _, c := range cases {
		msg, ok := keyMsg(c.in)
		if !ok || msg.String() != c.want {
			t.Errorf("%q -> %q (ok %v), want %q", c.in, msg.String(), ok, c.want)
		}
	}
}

func TestTypedTextKeepsItsTextAndPastesArriveWhole(t *testing.T) {
	msg, _ := keyMsg("é")
	if msg.Key().Text != "é" {
		t.Errorf("text %q", msg.Key().Text)
	}
	msg, ok := keyMsg("\x1b[200~night drive\x1b[201~")
	if !ok || msg.Key().Text != "night drive" {
		t.Errorf("paste: %+v %v", msg, ok)
	}
	msg, ok = keyMsg("héllo")
	if !ok || msg.Key().Text != "héllo" {
		t.Errorf("a multi-character chunk: %+v %v", msg, ok)
	}
}

func TestInputThatIsNotAKeyIsDropped(t *testing.T) {
	for _, in := range []string{"", "\x1b[<0;10;5M", "\x1b[I", "\x1b[O", "\x1b[97;1:3u", "\x1b[1;1:3A", "\xff"} {
		if msg, ok := keyMsg(in); ok {
			t.Errorf("%q became %q", in, msg.String())
		}
	}
	var _ tea.KeyPressMsg
}

// Modifiers survive: alt+n is not n (next track), and a space keeps its text so
// that it is typed into the search box.
func TestModifiedKeysKeepTheirModifiersAndSpaceItsText(t *testing.T) {
	for in, want := range map[string]string{"\x1bn": "alt+n", "\x1b[110;3u": "alt+n", "\x1b[1;3C": "alt+right", "\x1b[110;7u": "ctrl+alt+n"} {
		if msg, ok := keyMsg(in); !ok || msg.String() != want {
			t.Errorf("%q -> %q (ok %v), want %q", in, msg.String(), ok, want)
		}
	}
	for _, in := range []string{" ", "\x1b[32u"} {
		if msg, _ := keyMsg(in); msg.Key().Text != " " {
			t.Errorf("%q: text %q, want a space", in, msg.Key().Text)
		}
	}
}
