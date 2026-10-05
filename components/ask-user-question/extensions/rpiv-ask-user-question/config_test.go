package ask_user_question

import (
	"os"
	"path/filepath"
	"testing"
)

const fConfig = "config"

// configHome points HOME at a temp dir (and clears XDG_CONFIG_HOME) and returns a writer for
// ~/.config/rpiv-ask-user-question/config.json (upstream: config.test.ts).
func configHome(t *testing.T) (write func(contents string)) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path := filepath.Join(home, ".config", "rpiv-ask-user-question", "config.json")
	return func(contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFormatKeySpecForDisplay(t *testing.T) {
	tw(t, fConfig, "capitalizes each +-part of a resolved spec", func(t *testing.T) {
		eq(t, formatKeySpecForDisplay("ctrl+shift+t"), "Ctrl+Shift+T", "spec")
		eq(t, formatKeySpecForDisplay("alt+o"), "Alt+O", "alt")
	})
	tw(t, fConfig, "handles named special keys and bare keys", func(t *testing.T) {
		eq(t, formatKeySpecForDisplay("escape"), "Escape", "escape")
		eq(t, formatKeySpecForDisplay("f5"), "F5", "f5")
		eq(t, formatKeySpecForDisplay("]"), "]", "bare")
	})
	tw(t, fConfig, "cases compound-word named keys conventionally", func(t *testing.T) {
		eq(t, formatKeySpecForDisplay("pageup"), "PageUp", "pageup")
		eq(t, formatKeySpecForDisplay("ctrl+pagedown"), "Ctrl+PageDown", "pagedown")
	})
	tw(t, fConfig, "round-trips the default key to the historical hint casing", func(t *testing.T) {
		eq(t, formatKeySpecForDisplay(defaultCollapseKey), "Ctrl+]", "default")
	})
}

func TestResolveCollapseKey(t *testing.T) {
	tw(t, fConfig, "returns the default when config has no collapseKey", func(t *testing.T) {
		w := configHome(t)
		eq(t, resolveCollapseKey(), "ctrl+]", "no file")
		w(`{}`)
		eq(t, resolveCollapseKey(), "ctrl+]", "empty config")
	})
	tw(t, fConfig, "returns the default when collapseKey is empty or whitespace", func(t *testing.T) {
		w := configHome(t)
		w(`{"collapseKey":""}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "empty")
		w(`{"collapseKey":"   "}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "blank")
	})
	tw(t, fConfig, "normalizes the spec (trim + lowercase)", func(t *testing.T) {
		w := configHome(t)
		w(`{"collapseKey":"  Alt+O  "}`)
		eq(t, resolveCollapseKey(), "alt+o", "key")
	})
	tw(t, fConfig, "returns the off sentinel unchanged (case-insensitive)", func(t *testing.T) {
		w := configHome(t)
		for _, v := range []string{"off", "OFF", " Off "} {
			w(`{"collapseKey":"` + v + `"}`)
			eq(t, resolveCollapseKey(), collapseKeyOff, v)
		}
	})
	tw(t, fConfig, "falls back to the default for malformed specs", func(t *testing.T) {
		w := configHome(t)
		for _, v := range []string{"+", "ctrl+", "+t", "ctrl++t", "ctrl+ctrl+t", ""} {
			w(`{"collapseKey":"` + v + `"}`)
			eq(t, resolveCollapseKey(), defaultCollapseKey, "spec "+v)
		}
	})
	tw(t, fConfig, "falls back to the default for typo'd modifiers and unknown key names", func(t *testing.T) {
		w := configHome(t)
		for _, v := range []string{"ctr+]", "control+t", "ctrl+foo", "ctrl+f13", "meta+t"} {
			w(`{"collapseKey":"` + v + `"}`)
			eq(t, resolveCollapseKey(), defaultCollapseKey, "spec "+v)
		}
	})
	tw(t, fConfig, "accepts named special keys and bare base keys", func(t *testing.T) {
		w := configHome(t)
		for _, v := range []string{"escape", "ctrl+pageup", "f12", "alt+o", "ctrl+]", "shift+tab", "super+k"} {
			w(`{"collapseKey":"` + v + `"}`)
			eq(t, resolveCollapseKey(), v, "spec "+v)
		}
	})
}

func TestLoadConfig(t *testing.T) {
	tw(t, fConfig, "returns an empty config when no file is present", func(t *testing.T) {
		configHome(t)
		eq(t, loadConfig(), askConfig{}, "config")
	})
	tw(t, fConfig, "reads a valid JSON config", func(t *testing.T) {
		w := configHome(t)
		w(`{"collapseKey":"alt+o","guidance":{"promptSnippet":"s"}}`)
		got := loadConfig()
		eq(t, got.CollapseKey, "alt+o", "collapseKey")
		eq(t, got.Guidance, map[string]any{"promptSnippet": "s"}, "guidance")
	})
}

func TestConfigRobustness(t *testing.T) {
	w := configHome(t)
	for _, bad := range []string{`not json`, `[]`, `"x"`, `null`, ``} {
		w(bad)
		eq(t, loadConfig(), askConfig{}, "content "+bad)
	}
	// The default location is the legacy one; an XDG location wins when its file exists.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	w(`{"collapseKey":"alt+o"}`)
	eq(t, resolveCollapseKey(), "alt+o", "legacy location when XDG has no file")
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if err := os.MkdirAll(filepath.Join(xdg, "rpiv-ask-user-question"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "rpiv-ask-user-question", "config.json"), []byte(`{"collapseKey":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	eq(t, resolveCollapseKey(), "off", "XDG file wins")
	t.Setenv("XDG_CONFIG_HOME", "relative/dir")
	eq(t, resolveCollapseKey(), "alt+o", "a relative XDG_CONFIG_HOME is ignored")
}

func TestValidateGuidanceFields(t *testing.T) {
	eq(t, validateGuidanceFields(nil), guidanceFields{}, "nil")
	eq(t, validateGuidanceFields("x"), guidanceFields{}, "string")
	eq(t, validateGuidanceFields(map[string]any{"promptSnippet": "s", "promptGuidelines": []any{"a", "b"}, "description": "d"}),
		guidanceFields{PromptSnippet: "s", PromptGuidelines: []string{"a", "b"}, Description: "d"}, "valid")
	eq(t, validateGuidanceFields(map[string]any{"promptSnippet": "", "promptGuidelines": []any{"a", ""}, "description": 5.0}), guidanceFields{}, "invalid")
	eq(t, validateGuidanceFields(map[string]any{"promptGuidelines": []any{}}), guidanceFields{}, "empty list")
}
