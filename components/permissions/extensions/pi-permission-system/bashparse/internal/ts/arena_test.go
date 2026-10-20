package ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

// TestArenaUsage logs how much slab memory the benchmark inputs need (a sizing aid for the
// retention bounds in arena.go); it asserts that a second parse on the same parser reuses it.
func TestArenaUsage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "port", "bashparse-diff", "bench-inputs.json"))
	if err != nil {
		t.Skip(err)
	}
	var in map[string]string
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	l, err := BashLanguage()
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range in {
		p := NewParser(l)
		units := utf16.Encode([]rune(s))
		p.Parse(units)
		a := &p.arenas
		t.Logf("%s: subtrees %d (used %d chunks %d), stack nodes %d, child pointers %d (chunks %d)", name,
			a.subs.total, a.subs.used, len(a.subs.chunks), a.stackNodes.total, a.arrays.total, len(a.arrays.chunks))
		before := a.subs.total + a.stackNodes.total + a.arrays.total
		p.Parse(units)
		if after := a.subs.total + a.stackNodes.total + a.arrays.total; after != before {
			t.Errorf("%s: a second parse grew the slabs from %d to %d", name, before, after)
		}
	}
}
