package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateLocale(t *testing.T) string {
	t.Helper()
	SetLocale("")
	// The resolution under test is the SDK's, which applies only with rpiv-i18n installed.
	SetI18nInstalled(true)
	t.Cleanup(func() { SetLocale(""); SetI18nInstalled(false) })
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	return dir
}

func TestI18nBridgeShim(t *testing.T) {
	const f = "state/i18n-bridge.shim"
	tskip(t, f, "bridge does not statically import the rpiv-i18n SDK", "greps i18n-bridge.ts source for a static import of the rpiv-i18n SDK; the Go bridge compiles the SDK resolution in, so there is no import statement to assert on")
	tskip(t, f, "bridge uses await import() for the rpiv-i18n SDK", "greps i18n-bridge.ts source for an await import() of the SDK; Go has no dynamic import and the SDK resolution is compiled into i18n_bridge.go")
	tskip(t, f, "bridge guards the dynamic import with try/catch", "greps i18n-bridge.ts source for a try/catch around the dynamic import; Go has no dynamic import to guard (missing locale files are handled per file in loadLocaleTables)")
	tskip(t, f, "entry point does not statically import the rpiv-i18n SDK", "greps the extension entry index.ts source for a static SDK import; the Go entry point (root package) does not load the SDK module at all")
	tskip(t, f, "entry point uses await import() for the rpiv-i18n SDK", "greps the extension entry index.ts source for an await import() of the SDK loader; Go has no dynamic import and no registerStrings call (locales are embedded)")
	tskip(t, f, "entry point guards the dynamic import with try/catch", "greps the extension entry index.ts source for a try/catch around the dynamic import; Go has no dynamic import to guard")
	tskip(t, f, "await import() of a non-existent specifier rejects (catchable)", "asserts Node's dynamic import of a missing module rejects; no JavaScript module loading in Go")
	tw(t, f, "try/catch around await import() falls through to the alternative branch", func(t *testing.T) {
		// The Go equivalent of the identity-fallback scope: a key the registry does not know returns the inline
		// fallback; and an unsupported locale falls through to English, then to the fallback.
		isolateLocale(t)
		SetLocale("xx")
		if got := T("any.key", "literal fallback"); got != "literal fallback" {
			t.Fatalf("got %q", got)
		}
		if got := T("sentinel.next", "Next"); got != "Next" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestI18nResolution(t *testing.T) {
	t.Run("override wins and translates", func(t *testing.T) {
		isolateLocale(t)
		SetLocale("uk")
		if got := DisplayLabel(KindNext); got != "Далі" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("config file then LANG then LC_ALL", func(t *testing.T) {
		dir := isolateLocale(t)
		t.Setenv("LC_ALL", "de_DE.UTF-8")
		if got := DisplayLabel(KindNext); got == "Next" {
			t.Fatalf("LC_ALL de not applied: %q", got)
		}
		t.Setenv("LANG", "uk_UA.UTF-8")
		if got := DisplayLabel(KindNext); got != "Далі" {
			t.Fatalf("LANG: %q", got)
		}
		if err := os.MkdirAll(filepath.Join(dir, "rpiv-i18n"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "rpiv-i18n", "locale.json"), []byte(`{"locale":"pt-BR"}`), 0o644)
		want := readLocale(t, "pt-BR")["sentinel.next"]
		if got := DisplayLabel(KindNext); got != want {
			t.Fatalf("config: %q want %q", got, want)
		}
	})
	t.Run("C and POSIX are ignored; English default", func(t *testing.T) {
		isolateLocale(t)
		t.Setenv("LANG", "C")
		t.Setenv("LC_ALL", "POSIX")
		if got := DisplayLabel(KindNext); got != "Next" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("broken config file is skipped", func(t *testing.T) {
		dir := isolateLocale(t)
		os.MkdirAll(filepath.Join(dir, "rpiv-i18n"), 0o755)
		os.WriteFile(filepath.Join(dir, "rpiv-i18n", "locale.json"), []byte(`{nope`), 0o644)
		t.Setenv("LANG", "uk_UA.UTF-8")
		if got := DisplayLabel(KindNext); got != "Далі" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("pt_BR environment takes the language segment only, like the SDK", func(t *testing.T) {
		isolateLocale(t)
		t.Setenv("LANG", "pt_BR.UTF-8")
		if got, want := DisplayLabel(KindNext), readLocale(t, "pt")["sentinel.next"]; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("missing key falls back to English per key", func(t *testing.T) {
		isolateLocale(t)
		SetLocale("zh")
		if got := T("no.such.key", "fb"); got != "fb" {
			t.Fatal(got)
		}
	})
}

func readLocale(t *testing.T, code string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("locales", code+".json"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestLocalesKeepKeyPlaceholder is the per-file `it.each(files)` of locales.test.ts, which the ledger does not list.
func TestLocalesKeepKeyPlaceholder(t *testing.T) {
	files, _ := filepath.Glob("locales/*.json")
	if len(files) != len(SupportedLocales) {
		t.Fatalf("locale files %v vs supported %v", files, SupportedLocales)
	}
	for _, file := range files {
		m := readLocale(t, strings.TrimSuffix(filepath.Base(file), ".json"))
		for _, key := range []string{"hint.collapse", "hint.expand_line"} {
			if v, ok := m[key]; ok && !strings.Contains(v, "{key}") {
				t.Errorf("%s %s lacks {key}", file, key)
			}
		}
	}
}

func TestLocales(t *testing.T) {
	tw(t, "locales", "en.json defines both templated hint keys (English is the fallback base for every locale)", func(t *testing.T) {
		en := readLocale(t, "en")
		for _, key := range []string{"hint.collapse", "hint.expand_line"} {
			if !strings.Contains(en[key], "{key}") {
				t.Fatalf("en.json %s", key)
			}
		}
	})
}

// Without rpiv-i18n the original's bridge returns the inline English fallback at every call site: neither
// locale.json nor LANG nor LC_ALL changes a string. Only an explicit override (tests, the --locale stand-in) does.
func TestI18nIsEnglishWithoutRpivI18n(t *testing.T) {
	dir := isolateLocale(t)
	SetI18nInstalled(false)
	t.Setenv("LANG", "uk_UA.UTF-8")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	if err := os.MkdirAll(filepath.Join(dir, "rpiv-i18n"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "rpiv-i18n", "locale.json"), []byte(`{"locale":"pt-BR"}`), 0o644)
	if got := DisplayLabel(KindNext); got != "Next" {
		t.Fatalf("not installed: %q, want English", got)
	}
	SetI18nInstalled(true)
	if got := DisplayLabel(KindNext); got == "Next" {
		t.Fatalf("installed: still English (%q)", got)
	}
	SetI18nInstalled(false)
	SetLocale("uk")
	if got := DisplayLabel(KindNext); got != "Далі" {
		t.Fatalf("the override still applies: %q", got)
	}
}
