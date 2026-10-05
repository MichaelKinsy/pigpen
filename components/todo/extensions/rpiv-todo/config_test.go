package rpiv_todo

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const fConfig = "config"

// configHome points HOME at a temp dir (and clears XDG_CONFIG_HOME) and returns the config file's path
// (upstream: config.test.ts:14-24, ~/.config/rpiv-todo/config.json).
func configHome(t *testing.T) (write func(contents string), remove func()) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path := filepath.Join(home, ".config", "rpiv-todo", "config.json")
	write = func(contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remove = func() { _ = os.Remove(path) }
	return write, remove
}

func TestGetMaxWidgetLines(t *testing.T) {
	tw(t, fConfig, "returns the default when no config is present", func(t *testing.T) {
		configHome(t)
		eq(t, getMaxWidgetLines(), defaultMaxWidgetLines, "lines")
	})
	tw(t, fConfig, "returns the default when the field is absent", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{}`)
		eq(t, getMaxWidgetLines(), defaultMaxWidgetLines, "lines")
	})
	tw(t, fConfig, "returns the default for non-number values", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"maxWidgetLines":"twelve"}`)
		eq(t, getMaxWidgetLines(), defaultMaxWidgetLines, "lines")
	})
	tw(t, fConfig, "returns the default for values below the floor (2, 1, 0, -5)", func(t *testing.T) {
		w, _ := configHome(t)
		for _, v := range []string{"2", "1", "0", "-5"} {
			w(`{"maxWidgetLines":` + v + `}`)
			eq(t, getMaxWidgetLines(), defaultMaxWidgetLines, "lines for "+v)
		}
	})
	tw(t, fConfig, "returns the configured value at the floor (3) and above — no ceiling", func(t *testing.T) {
		w, _ := configHome(t)
		for _, v := range []int{3, 8, 50} {
			w(`{"maxWidgetLines":` + strconv.Itoa(v) + `}`)
			eq(t, getMaxWidgetLines(), v, "lines")
		}
	})
}

func TestLoadConfigCollapseKey(t *testing.T) {
	tw(t, fConfig, "surfaces a user-set collapseKey unchanged (validation happens in resolveCollapseKey, not at load)", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"alt+o"}`)
		eq(t, loadConfig().CollapseKey, any("alt+o"), "collapseKey")
	})
	tw(t, fConfig, "passes invalid specs through verbatim — the resolver, not the loader, decides validity", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"ctr+t"}`)
		eq(t, loadConfig().CollapseKey, any("ctr+t"), "collapseKey")
	})
}

func TestResolveCollapseKey(t *testing.T) {
	tw(t, fConfig, "returns the default (ctrl+shift+t) when no config is present", func(t *testing.T) {
		configHome(t)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "key")
	})
	tw(t, fConfig, "returns the default when the field is absent", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "key")
	})
	tw(t, fConfig, "returns the default when the field is empty or blank", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":""}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "empty")
		w(`{"collapseKey":"   "}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "blank")
	})
	tw(t, fConfig, "returns COLLAPSE_KEY_OFF when set to the sentinel", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"off"}`)
		eq(t, resolveCollapseKey(), collapseKeyOff, "key")
	})
	tw(t, fConfig, "returns the lowercased validated spec when valid", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"Alt+O"}`)
		eq(t, resolveCollapseKey(), "alt+o", "key")
	})
	tw(t, fConfig, "returns the default when the spec is invalid", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"collapseKey":"ctr+t"}`)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "key")
	})
	tw(t, fConfig, "returns the default for non-string values", func(t *testing.T) {
		w, _ := configHome(t)
		for _, v := range []string{`123`, `true`, `["alt+o"]`, `{"key":"alt+o"}`} {
			w(`{"collapseKey":` + v + `}`)
			eq(t, resolveCollapseKey(), defaultCollapseKey, "key for "+v)
		}
	})
	tw(t, fConfig, "is arg-less and reads config fresh on every call (no caching, parity with getMaxWidgetLines)", func(t *testing.T) {
		w, _ := configHome(t)
		eq(t, resolveCollapseKey(), defaultCollapseKey, "no config")
		w(`{"collapseKey":"off"}`)
		eq(t, resolveCollapseKey(), collapseKeyOff, "off")
		w(`{"collapseKey":"alt+o"}`)
		eq(t, resolveCollapseKey(), "alt+o", "alt+o")
	})
}

func TestIsValidCollapseKeySpec(t *testing.T) {
	tw(t, fConfig, "accepts valid specs", func(t *testing.T) {
		for _, s := range []string{"ctrl+shift+t", "alt+o", "escape", "f5", "ctrl+]"} {
			eq(t, isValidCollapseKeySpec(s), true, s)
		}
	})
	tw(t, fConfig, "rejects empty spec", func(t *testing.T) { eq(t, isValidCollapseKeySpec(""), false, "empty") })
	tw(t, fConfig, "rejects leading, trailing, and double '+'", func(t *testing.T) {
		for _, s := range []string{"+", "+t", "ctrl+", "ctrl++t"} {
			eq(t, isValidCollapseKeySpec(s), false, s)
		}
	})
	tw(t, fConfig, "rejects unknown modifiers", func(t *testing.T) { eq(t, isValidCollapseKeySpec("win+t"), false, "win+t") })
	tw(t, fConfig, "rejects duplicate modifiers", func(t *testing.T) { eq(t, isValidCollapseKeySpec("ctrl+ctrl+t"), false, "ctrl+ctrl+t") })
	tw(t, fConfig, "rejects typo bases (multi-char non-special)", func(t *testing.T) { eq(t, isValidCollapseKeySpec("ctr+t"), false, "ctr+t") })
}

func TestConfigRobustness(t *testing.T) {
	t.Run("XDG_CONFIG_HOME wins when absolute, a relative one falls back to ~/.config", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"maxWidgetLines":5}`)
		xdg := t.TempDir()
		if err := os.MkdirAll(filepath.Join(xdg, "rpiv-todo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xdg, "rpiv-todo", "config.json"), []byte(`{"maxWidgetLines":7}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", xdg)
		eq(t, getMaxWidgetLines(), 7, "absolute XDG_CONFIG_HOME")
		// A relative XDG_CONFIG_HOME is ignored even when the directory exists relative to the cwd.
		cwd := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cwd, "rel", "rpiv-todo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, "rel", "rpiv-todo", "config.json"), []byte(`{"maxWidgetLines":11}`), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		t.Setenv("XDG_CONFIG_HOME", "rel")
		eq(t, getMaxWidgetLines(), 5, "relative XDG_CONFIG_HOME is ignored")
	})
	t.Run("the legacy ~/.config file is the fallback when the XDG file is missing", func(t *testing.T) {
		w, _ := configHome(t)
		w(`{"maxWidgetLines":9}`)
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		eq(t, getMaxWidgetLines(), 9, "legacy fallback")
	})
	t.Run("invalid JSON and non-object JSON read as an empty config", func(t *testing.T) {
		w, _ := configHome(t)
		for _, c := range []string{`{nope`, `[1,2]`, `null`, `"x"`} {
			w(c)
			eq(t, getMaxWidgetLines(), defaultMaxWidgetLines, c)
		}
	})
}

func TestValidateGuidanceFields(t *testing.T) {
	t.Run("keeps valid fields and drops invalid ones", func(t *testing.T) {
		g := validateGuidanceFields(map[string]any{"promptSnippet": "s", "promptGuidelines": []any{"a", "b"}, "description": "d"})
		eq(t, g, guidanceFields{PromptSnippet: "s", PromptGuidelines: []string{"a", "b"}, Description: "d"}, "valid")
		eq(t, validateGuidanceFields(map[string]any{"promptSnippet": "", "promptGuidelines": []any{"a", ""}, "description": 3.0}), guidanceFields{}, "invalid")
		eq(t, validateGuidanceFields(nil), guidanceFields{}, "nil")
		eq(t, validateGuidanceFields("x"), guidanceFields{}, "non-object")
		eq(t, validateGuidanceFields(map[string]any{"promptGuidelines": []any{}}), guidanceFields{}, "empty list")
	})
}
