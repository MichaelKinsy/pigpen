package pigmodeltweaks

import (
	"os"
	"path/filepath"
	"testing"
)

// store.ts migrateLegacy: earlier versions of the extensions kept one file per
// feature. When the consolidated file is absent (or unreadable) and any of them
// exists, its contents seed the consolidated file, which is written at once.

func writeLegacy(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyFilesSeedTheConsolidatedFile(t *testing.T) {
	dir := t.TempDir()
	writeLegacy(t, dir, "last-model.json", `{"provider":" acme ","modelId":"m1"}`)
	writeLegacy(t, dir, "openrouter-provider-prefs.json", `{"vendor/a":"alpha","bad":"  ","":"x"}`)
	writeLegacy(t, dir, "model-preferences.json", `{"enabled":false,"allowedModels":[{"provider":"p","model":"m"},{"provider":"q"}]}`)
	path := filepath.Join(dir, settingsFileName)

	state := readSettings(path)
	if state.RememberModel.Last == nil || state.RememberModel.Last.Provider != "acme" || state.RememberModel.Last.ModelID != "m1" {
		t.Fatalf("remember last = %+v", state.RememberModel.Last)
	}
	if got := state.OpenRouterModelProviderPref; !got.Enabled || len(got.Locks) != 1 || got.Locks["vendor/a"] != "alpha" {
		t.Fatalf("locks = %+v", got)
	}
	if got := state.ModelGuard; got.Enabled || len(got.AllowedModels) != 1 || got.AllowedModels[0] != (modelRef{"p", "m"}) {
		t.Fatalf("guard = %+v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the consolidated file was not written: %v", err)
	}
	// The next read comes from the consolidated file, not the legacy ones.
	if err := os.Remove(filepath.Join(dir, "last-model.json")); err != nil {
		t.Fatal(err)
	}
	if again := readSettings(path); again.RememberModel.Last == nil {
		t.Fatal("the migrated settings were not persisted")
	}
}

func TestNoLegacyFilesLeavesDefaultsAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	state := readSettings(filepath.Join(dir, settingsFileName))
	if !state.ModelGuard.Enabled || state.RememberModel.Last != nil {
		t.Fatalf("defaults expected, got %+v", state)
	}
	if _, err := os.Stat(filepath.Join(dir, settingsFileName)); err == nil {
		t.Fatal("a file was written with nothing to migrate")
	}
}

func TestAnExistingConsolidatedFileWinsOverLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	writeLegacy(t, dir, "last-model.json", `{"provider":"acme","modelId":"m1"}`)
	writeLegacy(t, dir, settingsFileName, `{"version":1,"rememberModel":{"enabled":true}}`)
	if state := readSettings(filepath.Join(dir, settingsFileName)); state.RememberModel.Last != nil {
		t.Fatalf("a legacy file overrode the consolidated one: %+v", state.RememberModel.Last)
	}
}

func TestAnUnreadableConsolidatedFileFallsBackToLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	writeLegacy(t, dir, "last-model.json", `{"provider":"acme","modelId":"m1"}`)
	writeLegacy(t, dir, settingsFileName, `{not json`)
	if state := readSettings(filepath.Join(dir, settingsFileName)); state.RememberModel.Last == nil {
		t.Fatal("an invalid consolidated file did not fall back to the legacy files")
	}
}
