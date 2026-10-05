package music

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheLastSearchIsSavedPrivatelyAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run", "search.json")
	in := []Track{{ID: "aaaaaaaaaaa", Title: "One", Artists: []string{"A"}, Duration: time.Minute}, {ID: "bbbbbbbbbbb", Title: "Two"}}
	if err := SaveSearch(path, in); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%v %v", st, err)
	}
	out, err := LoadSearch(path)
	if err != nil || len(out) != 2 || out[0].Title != "One" || out[1].ID != "bbbbbbbbbbb" || out[0].Duration != time.Minute {
		t.Errorf("%+v %v", out, err)
	}
}

func TestLoadingAMissingSearchIsNotExistAndADamagedOneNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSearch(filepath.Join(dir, "none.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o600)
	if _, err := LoadSearch(bad); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%v", err)
	}
}
