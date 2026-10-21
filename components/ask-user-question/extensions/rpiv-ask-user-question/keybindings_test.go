package ask_user_question

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeKeybindings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A value is a key or a list of keys, as Pi's KeybindingsManager reads keybindings.json.
func TestKeybindingOverridesAcceptAKeyOrAList(t *testing.T) {
	got := readKeybindingOverrides(writeKeybindings(t, `{"tui.select.up": "ctrl+p", "tui.select.down": ["ctrl+n", "down"], "app.editor.external": []}`))
	want := map[string][]string{
		"tui.select.up":       {"ctrl+p"},
		"tui.select.down":     {"ctrl+n", "down"},
		"app.editor.external": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %#v, want %#v", got, want)
	}
}

// A legacy short name is migrated to the namespaced id, unless the file also names the namespaced id.
func TestKeybindingOverridesMigrateLegacyNames(t *testing.T) {
	got := readKeybindingOverrides(writeKeybindings(t, `{"selectUp": "ctrl+p", "externalEditor": ["ctrl+q"]}`))
	want := map[string][]string{"tui.select.up": {"ctrl+p"}, "app.editor.external": {"ctrl+q"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %#v, want %#v", got, want)
	}
	got = readKeybindingOverrides(writeKeybindings(t, `{"selectUp": "ctrl+p", "tui.select.up": "ctrl+k"}`))
	want = map[string][]string{"tui.select.up": {"ctrl+k"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the namespaced id must win over its legacy name: %#v, want %#v", got, want)
	}
}

func TestKeybindingOverridesIgnoreAMissingOrMalformedFile(t *testing.T) {
	if got := readKeybindingOverrides(filepath.Join(t.TempDir(), "none.json")); got != nil {
		t.Fatalf("missing file read as %#v", got)
	}
	if got := readKeybindingOverrides(writeKeybindings(t, `{not json`)); got != nil {
		t.Fatalf("malformed file read as %#v", got)
	}
}
