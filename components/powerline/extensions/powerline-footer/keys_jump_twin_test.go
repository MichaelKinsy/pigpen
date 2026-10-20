package powerline_footer

import (
	"reflect"
	"strings"
	"testing"
)

// Twins of tests/jump-shortcuts.test.ts.

// settingsFrom decodes a settings document the way readSettings does (ordered objects).
func settingsFrom(t *testing.T, doc string) map[string]any {
	t.Helper()
	v, err := parseJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	o, _ := v.(*jsObject)
	if o == nil {
		t.Fatalf("%s is not an object", doc)
	}
	return o.vals
}

// wantBinding asserts a binding: want "" is null.
func wantBinding(t *testing.T, got *string, want, what string) {
	t.Helper()
	switch {
	case want == "" && got != nil:
		t.Errorf("%s = %q, want null", what, *got)
	case want != "" && (got == nil || *got != want):
		g := "null"
		if got != nil {
			g = *got
		}
		t.Errorf("%s = %s, want %q", what, g, want)
	}
}

func TestJumpShortcutTwins(t *testing.T) {
	const file = "jump-shortcuts"

	tw(t, file, "surviving editor shortcuts resolve without app-owned chat scrolling", func(t *testing.T) {
		r := resolveShortcutConfig(map[string]any{})
		wantBinding(t, r.StashHistory, "ctrl+alt+h", "stashHistory")
		wantBinding(t, r.CopyEditor, "ctrl+alt+c", "copyEditor")
		wantBinding(t, r.CutEditor, "ctrl+alt+x", "cutEditor")
		wantBinding(t, r.Reply, "", "reply")
		wantBinding(t, r.EditorStart, "super+shift+up", "editorStart")
		wantBinding(t, r.EditorEnd, "super+shift+down", "editorEnd")
		rt := reflect.TypeOf(r)
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name[:1]) + rt.Field(i).Name[1:]
			if strings.HasPrefix(name, "scroll") || strings.HasPrefix(name, "jump") {
				t.Errorf("shortcut %s must not exist", name)
			}
		}
		if len(shortcutKeys) != rt.NumField() {
			t.Errorf("shortcutKeys %v and the struct disagree", shortcutKeys)
		}
	})

	tw(t, file, "super shortcut matching and conflict normalization remain supported", func(t *testing.T) {
		if !matchesConfiguredShortcut("\x1b[1;9A", new("super+up")) {
			t.Error(`"\x1b[1;9A" must match super+up`)
		}
		if matchesConfiguredShortcut("c", new("super+c")) {
			t.Error(`"c" must not match super+c`)
		}
		if !isSupportedSuperShortcut("super+up") || isSupportedSuperShortcut("super+z") {
			t.Error("super+up is supported, super+z is not")
		}
		if got := shortcutConflictKey("super+home"); got != "super+up" {
			t.Errorf("shortcutConflictKey(super+home) = %q", got)
		}
	})

	tw(t, file, "editor boundary shortcuts remain configurable", func(t *testing.T) {
		r := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"editorStart":"ctrl+shift+u","editorEnd":"ctrl+shift+d"}}`))
		wantBinding(t, r.EditorStart, "ctrl+shift+u", "editorStart")
		wantBinding(t, r.EditorEnd, "ctrl+shift+d", "editorEnd")
	})

	tw(t, file, "reply shortcut is opt-in and configurable", func(t *testing.T) {
		wantBinding(t, resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":null}}`)).Reply, "", "reply null")
		wantBinding(t, resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"ctrl+shift+r"}}`)).Reply, "ctrl+shift+r", "reply")
	})

	tw(t, file, "conflicting reply shortcut disables reply instead of borrowing another action", func(t *testing.T) {
		reserved := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"ctrl+r"}}`))
		wantBinding(t, reserved.Reply, "", "reserved reply")
		wantBinding(t, reserved.EditorStart, "super+shift+up", "editorStart")
		wantBinding(t, reserved.EditorEnd, "super+shift+down", "editorEnd")

		dup := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"ctrl+alt+h"}}`))
		wantBinding(t, dup.Reply, "", "duplicate reply")
		wantBinding(t, dup.StashHistory, "ctrl+alt+h", "stashHistory")

		start := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"super+shift+up"}}`))
		wantBinding(t, start.Reply, "", "reply on editorStart's default")
		wantBinding(t, start.EditorStart, "super+shift+up", "editorStart")
		wantBinding(t, start.EditorEnd, "super+shift+down", "editorEnd")

		end := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"super+shift+down"}}`))
		wantBinding(t, end.Reply, "", "reply on editorEnd's default")
		wantBinding(t, end.EditorStart, "super+shift+up", "editorStart")
		wantBinding(t, end.EditorEnd, "super+shift+down", "editorEnd")
	})

	tw(t, file, "bash completions are opt-in", func(t *testing.T) {
		if parseBashModeSettings(map[string]any{}, nil).Completions {
			t.Error("completions default to off")
		}
		if !parseBashModeSettings(settingsFrom(t, `{"bashMode":{"completions":true}}`), nil).Completions {
			t.Error("completions: true")
		}
		if parseBashModeSettings(settingsFrom(t, `{"bashMode":{"completions":false}}`), nil).Completions {
			t.Error("completions: false")
		}
	})

	tskip(t, file, "fast editor render keeps Powerline chrome for large drafts",
		"renderFastPowerlineEditor draws onto Pi's Editor object (its borderColor, state.lines and tui.terminal.rows); the Go SDK has no editor object to hand it (SDK: setEditorComponent)")
	tskip(t, file, "fast editor render falls back for short drafts and enabled completions",
		"the null return is the editor render hook declining a draft below its line and column thresholds; with no editor render hook in the Go SDK there is no caller that could fall back")
	tskip(t, file, "fast editor render falls back for wide characters",
		"the fallback is decided by the wide-character width of Pi's editor.state lines (1300 CJK characters); no editor state is reachable from a Go extension")
	tskip(t, file, "fast editor render updates the editor navigation width",
		"the case asserts a write to the editor's private lastWidth field; an editor's internals cannot be reached from a Go extension")
}

// Extra checks of the shortcut matcher and of the resolution rules the upstream cases do not reach.

func TestShortcutMatching(t *testing.T) {
	for _, c := range []struct {
		data, key string
		want      bool
	}{
		{"\x1b[104;7u", "ctrl+alt+h", true},
		{"\x1b\x08", "ctrl+alt+h", true},
		{"\x1bh", "ctrl+alt+h", false},
		{"\x1b[99;7u", "ctrl+alt+c", true},
		{"\x13", "ctrl+s", true},
		{"\x1bs", "alt+s", true},
		{"\x1b[115;3u", "alt+s", true},
		{"\x1b[27;7;104~", "ctrl+alt+h", true},
		{"\x1b[1;5A", "ctrl+up", true},
		{"\x1b[1;5A", "up", false},
		{"\x1b[A", "up", true},
		{"\x1b[13;2u", "shift+enter", true},
		{"\x1bOP", "f1", true},
		{"\x1b[66;6u", "ctrl+shift+b", true},
		{"\x1b[98;6u", "ctrl+shift+b", true},
		{"\x1bx", "alt+s", false},
		{"\x1b", "alt+s", false},
		{"\x1b[104;71u", "ctrl+alt+h", true}, // caps lock and num lock do not count
		{"\x1b[104;7:2u", "ctrl+alt+h", true},
		{"\x02", "ctrl+b", true},
		{"b", "ctrl+b", false},
	} {
		if got := matchesKey(c.data, c.key); got != c.want {
			t.Errorf("matchesKey(%q, %q) = %v, want %v", c.data, c.key, got, c.want)
		}
	}
	if !isKeyRelease("\x1b[104;7:3u") || isKeyRelease("\x1b[104;7u") || isKeyRelease("\x1b[200~x:3u") || isKeyRelease("\x1b[200~x:3u\x1b[201~") {
		t.Error("isKeyRelease")
	}
	if matchesConfiguredShortcut("x", nil) || matchesConfiguredShortcut("x", new("")) {
		t.Error("a missing shortcut matches nothing")
	}
}

func TestSuperShortcutHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"super+home": "super+up", "super+end": "super+down", "super+shift+home": "super+shift+up", "super+shift+end": "super+shift+down",
		"SUPER+END": "super+down", "ctrl+home": "ctrl+home", "super+up": "super+up",
	} {
		if got := shortcutConflictKey(in); got != want {
			t.Errorf("shortcutConflictKey(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"super+up": true, "ctrl+super+up": true, "Super+x": true, "ctrl+super": false, "super": false, "ctrl+up": false} {
		if got := shortcutUsesSuper(in); got != want {
			t.Errorf("shortcutUsesSuper(%q) = %v, want %v", in, got, want)
		}
	}
	for _, c := range []struct {
		data, key string
		want      bool
	}{
		{"\x1b[1;9B", "super+down", true}, {"\x1b[1;9F", "super+end", true}, {"\x1b[6;9~", "super+pagedown", true},
		{"\x1b[1;10A", "super+shift+up", true}, {"\x1b[1;9A", "super+shift+up", false}, {"\x1b[1;10A", "super+up", false},
		{"\x1b[27;9;65~", "super+up", true}, {"\x1b[57419;9u", "super+up", true}, {"\x1b[57419;9:3u", "super+up", false},
	} {
		if got := matchesConfiguredShortcut(c.data, new(c.key)); got != c.want {
			t.Errorf("matchesConfiguredShortcut(%q, %q) = %v, want %v", c.data, c.key, got, c.want)
		}
	}
}

func TestShortcutOverrideParsing(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Ctrl+Alt+H", "ctrl+alt+h"},
		{"shift+ctrl+u", "ctrl+shift+u"},
		{"cmd+shift+up", "super+shift+up"},
		{"alt+ctrl+super+shift+f5", ""},
		{"ctrl+F5", "ctrl+f5"},
		{"ctrl+;", "ctrl+;"},
		{"ctrl+ alt+h", ""},
		{"ctrl+ctrl+h", ""},
		{"ctrl+", ""},
		{"hyper+h", ""},
		{"super+z", ""},
		{"super+home", "super+home"},
		{"ctrl+f13", ""},
		{"ctrl+enter", "ctrl+enter"},
		{"ctrl+ab", ""},
	} {
		got, ok := parseShortcutOverride(c.in)
		if c.want == "" && ok {
			t.Errorf("%q parsed as %q, want invalid", c.in, got)
		}
		if c.want != "" && (!ok || got != c.want) {
			t.Errorf("%q parsed as %q (%v), want %q", c.in, got, ok, c.want)
		}
	}
	if b, defined := parseShortcutSetting("  "); b != nil || !defined {
		t.Error("a blank setting turns the shortcut off")
	}
	if b, defined := parseShortcutSetting(float64(3)); b != nil || defined {
		t.Error("a non-string setting is undefined (ignored)")
	}
}

func TestShortcutConflictsFallBackToTheOwnDefault(t *testing.T) {
	// copyEditor on Pi's ctrl+a (reserved): its own default ctrl+alt+c replaces it.
	r := resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"copyEditor":"ctrl+a"}}`))
	wantBinding(t, r.CopyEditor, "ctrl+alt+c", "copyEditor")
	// cutEditor takes copyEditor's default: it belongs to another action, so cutEditor gets its own default back.
	r = resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"cutEditor":"ctrl+alt+c"}}`))
	wantBinding(t, r.CutEditor, "ctrl+alt+x", "cutEditor")
	wantBinding(t, r.CopyEditor, "ctrl+alt+c", "copyEditor")
	// Two actions on one new shortcut: the first keeps it, the second falls back.
	r = resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"copyEditor":"ctrl+alt+y","cutEditor":"ctrl+alt+y"}}`))
	wantBinding(t, r.CopyEditor, "ctrl+alt+y", "copyEditor")
	wantBinding(t, r.CutEditor, "ctrl+alt+x", "cutEditor")
	// The bash toggle's default is reserved against the powerline shortcuts.
	r = resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"stashHistory":"ctrl+shift+b"}}`))
	wantBinding(t, r.StashHistory, "ctrl+alt+h", "stashHistory")
	// The bash toggle's shortcut takes an action's default away, so its replacement is not found and the action is off.
	r = resolveShortcutConfig(settingsFrom(t, `{"bashMode":{"toggleShortcut":"ctrl+alt+c"},"powerlineShortcuts":{"copyEditor":"ctrl+a"}}`))
	wantBinding(t, r.CopyEditor, "", "copyEditor: replacement taken")
	r = resolveShortcutConfig(settingsFrom(t, `{"bashMode":{"toggleShortcut":"ctrl+alt+c"}}`))
	wantBinding(t, r.CopyEditor, "", "copyEditor default taken by the toggle")
	wantBinding(t, r.CutEditor, "ctrl+alt+x", "cutEditor unaffected")
	// Super+home and super+up are one key.
	r = resolveShortcutConfig(settingsFrom(t, `{"powerlineShortcuts":{"reply":"super+home","editorStart":"super+shift+home"}}`))
	wantBinding(t, r.Reply, "super+home", "reply")
	wantBinding(t, r.EditorStart, "super+shift+home", "editorStart")
}

func TestBashModeSettings(t *testing.T) {
	d := parseBashModeSettings(map[string]any{}, nil)
	wantBinding(t, d.ToggleShortcut, "ctrl+shift+b", "toggle")
	if d.TranscriptMaxLines != 2000 || d.TranscriptMaxBytes != 512*1024 || d.Completions {
		t.Errorf("defaults = %+v", d)
	}
	s := parseBashModeSettings(settingsFrom(t, `{"bashMode":{"transcriptMaxLines":7.9,"transcriptMaxBytes":99999.5,"toggleShortcut":"ctrl+alt+b"}}`), nil)
	if s.TranscriptMaxLines != 100 || s.TranscriptMaxBytes != 99999 {
		t.Errorf("clamps: %+v", s)
	}
	wantBinding(t, s.ToggleShortcut, "ctrl+alt+b", "toggle")
	off := parseBashModeSettings(settingsFrom(t, `{"bashMode":{"toggleShortcut":null}}`), nil)
	wantBinding(t, off.ToggleShortcut, "", "toggle off")
	// A toggle that collides with a resolved powerline shortcut or a Pi key falls back to the default, or off when that is taken.
	rs := resolveShortcutConfig(map[string]any{})
	taken := parseBashModeSettings(settingsFrom(t, `{"bashMode":{"toggleShortcut":"ctrl+alt+c"}}`), &rs)
	wantBinding(t, taken.ToggleShortcut, "ctrl+shift+b", "collision falls back")
	rs.CopyEditor = new("ctrl+shift+b")
	rs.StashHistory = new("ctrl+alt+c")
	colliding := parseBashModeSettings(settingsFrom(t, `{"bashMode":{"toggleShortcut":"ctrl+alt+c"}}`), &rs)
	wantBinding(t, colliding.ToggleShortcut, "", "default taken too: off")
	bad := parseBashModeSettings(settingsFrom(t, `{"bashMode":{"toggleShortcut":"nonsense+x"}}`), nil)
	wantBinding(t, bad.ToggleShortcut, "ctrl+shift+b", "invalid toggle falls back")
}
