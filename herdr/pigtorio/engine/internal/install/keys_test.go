package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeysInsertsSectionAndKeepsTheRest(t *testing.T) {
	home := t.TempDir()
	orig := "{\n  \"global\": { \"cmd-t\": \"tab.new\" },\n  \"terminal\": { \"ctrl-1\": \"terminal\" }\n}\n"
	os.WriteFile(filepath.Join(home, "keys.json"), []byte(orig), 0o644)
	added, err := Keys(home)
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "keys.json"))
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["layout:pigtorio"]["cmd-shift-b"] != "pigtorio.bell" || m["global"]["cmd-t"] != "tab.new" || m["terminal"]["ctrl-1"] != "terminal" {
		t.Fatalf("got %s", b)
	}
	if !strings.Contains(string(b), "\"global\": { \"cmd-t\": \"tab.new\" },\n  \"terminal\"") {
		t.Fatalf("other sections were rewritten:\n%s", b)
	}
	again, err := Keys(home)
	if err != nil || again {
		t.Fatalf("second run added=%v err=%v", again, err)
	}
}

func TestKeysRefusesATakenChord(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "keys.json"), []byte(`{"global":{"cmd-shift-b":"something"}}`), 0o644)
	if added, err := Keys(home); err == nil || added {
		t.Fatalf("added=%v err=%v", added, err)
	}
}

func TestKeysOnAnEmptyObject(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "keys.json"), []byte("{}"), 0o644)
	if _, err := Keys(home); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "keys.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || len(m) != 1 {
		t.Fatalf("%v %s", err, b)
	}
}
