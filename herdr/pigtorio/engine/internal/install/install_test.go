package install

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReplacesModAndRefusesShippedNames(t *testing.T) {
	src, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "mods/pigtorio/main.js"), "new")
	write(t, filepath.Join(src, "mods/pigtorio/art/a.png"), "png")
	write(t, filepath.Join(src, "layouts/pigtorio.json"), "{}")
	exe := filepath.Join(src, "pigtorio-bin")
	write(t, exe, "binary")
	write(t, filepath.Join(home, "mods/pigtorio/stale.js"), "old")
	write(t, filepath.Join(home, "installed.json"), `{"main":{"x":"y"},"layout:main":{}}`)
	if err := Run(home, src, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "mods/pigtorio/stale.js")); err == nil {
		t.Fatal("old mod files kept")
	}
	for _, p := range []string{"mods/pigtorio/main.js", "mods/pigtorio/art/a.png", "layouts/pigtorio.json", "pigtorio/bin/pigtorio"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if st, _ := os.Stat(filepath.Join(home, "pigtorio/bin/pigtorio")); st.Mode()&0o111 == 0 {
		t.Error("binary not executable")
	}
	for _, name := range []string{"pigtorio", "layout:pigtorio"} {
		other := t.TempDir()
		write(t, filepath.Join(other, "installed.json"), `{"`+name+`":{}}`)
		if err := Run(other, src, exe); err == nil {
			t.Errorf("installed over %q", name)
		}
		if _, err := os.Stat(filepath.Join(other, "mods")); err == nil {
			t.Errorf("wrote into a refused home (%q)", name)
		}
	}
}

func TestInstallRefusesWhenSourceIsTheDestination(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "mods/pigtorio/main.js"), "installed")
	write(t, filepath.Join(home, "layouts/pigtorio.json"), "{}")
	if err := Run(home, home, ""); err == nil {
		t.Fatal("installed the installed mod over itself")
	}
	// through a symlink too
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "mods"), 0o755)
	if err := os.Symlink(filepath.Join(home, "mods/pigtorio"), filepath.Join(src, "mods/pigtorio")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "layouts/pigtorio.json"), "{}")
	if err := Run(home, src, ""); err == nil {
		t.Fatal("installed through a symlink onto itself")
	}
	if b, err := os.ReadFile(filepath.Join(home, "mods/pigtorio/main.js")); err != nil || string(b) != "installed" {
		t.Fatalf("installed mod damaged: %v %q", err, b)
	}
}

func TestFailedCopyKeepsTheInstalledMod(t *testing.T) {
	src, home := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "mods/pigtorio/main.js"), "new")
	write(t, filepath.Join(src, "mods/pigtorio/locked.js"), "x")
	os.Chmod(filepath.Join(src, "mods/pigtorio/locked.js"), 0)
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "mods/pigtorio/locked.js"), 0o644) })
	write(t, filepath.Join(src, "layouts/pigtorio.json"), "{}")
	write(t, filepath.Join(home, "mods/pigtorio/main.js"), "old")
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	if err := Run(home, src, ""); err == nil {
		t.Fatal("copy of an unreadable file succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "mods/pigtorio/main.js")); string(b) != "old" {
		t.Fatalf("installed mod lost: %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, "pigtorio/.install")); err == nil {
		t.Fatal("staging folder left behind")
	}
}

func TestFindSourceSkipsTheHome(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "mods/pigtorio/main.js"), "installed")
	exe := filepath.Join(home, "pigtorio/bin/pigtorio") // the installed binary: bin/../.. is the home
	if got, err := FindSource(exe, home); err == nil {
		t.Fatalf("found %s inside the home", got)
	}
	repo := t.TempDir()
	write(t, filepath.Join(repo, "mods/pigtorio/main.js"), "src")
	if got, err := FindSource(filepath.Join(repo, "engine/bin/pigtorio"), home); err != nil || got != filepath.Clean(repo) {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestKeysRefusesAMissingFile(t *testing.T) {
	home := t.TempDir()
	if err := CheckKeys(home); err == nil {
		t.Fatal("CheckKeys accepted a home without keys.json")
	}
	if added, err := Keys(home); err == nil || added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	if _, err := os.Stat(filepath.Join(home, "keys.json")); err == nil {
		t.Fatal("created a keys.json holding only our section")
	}
}
