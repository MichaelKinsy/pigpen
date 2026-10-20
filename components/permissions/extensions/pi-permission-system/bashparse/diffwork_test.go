package bashparse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDiffWork compares the Go parse with a fresh recording of the original in a work directory
// written by port/bashparse-diff/run.sh (full-*.jsonl and hash-*.txt). It is the investigation
// tool behind the committed differential (differential_test.go): it prints each mismatch class
// with examples. Run: BP_WORK=<dir> go test -run TestDiffWork -v ./bashparse
func TestDiffWork(t *testing.T) {
	work := os.Getenv("BP_WORK")
	if work == "" {
		t.Skip("BP_WORK not set: investigation tool, see port/bashparse-diff")
	}
	limit := 6
	files, _ := filepath.Glob(filepath.Join(work, "full-*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<28)
		total := 0
		classes := map[string]int{}
		examples := map[string][]string{}
		for sc.Scan() {
			var rec struct{ S, G, P string }
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				t.Fatal(err)
			}
			total++
			g, p := dumpBoth(rec.S)
			class := ""
			switch {
			case g != rec.G:
				class = "grammar:" + classify(rec.G, g)
			case p != rec.P:
				class = "corrected:" + classify(rec.P, p)
			}
			if class == "" {
				continue
			}
			classes[class]++
			if len(examples[class]) < limit {
				examples[class] = append(examples[class], fmt.Sprintf("%q", rec.S)+"\n"+firstDiff(rec.G, g, rec.P, p))
			}
		}
		fh.Close()
		bad := 0
		for _, n := range classes {
			bad += n
		}
		t.Logf("%s: %d inputs, %d differ", filepath.Base(f), total, bad)
		var names []string
		for c := range classes {
			names = append(names, c)
		}
		sort.Strings(names)
		for _, c := range names {
			t.Logf("  class %-40s %d", c, classes[c])
			for _, ex := range examples[c] {
				t.Logf("    %s", strings.ReplaceAll(ex, "\n", "\n    "))
			}
		}
	}
	hashes, _ := filepath.Glob(filepath.Join(work, "hash-*.txt"))
	for _, h := range hashes {
		kind := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(h), "hash-"), ".txt")
		inputs := loadStrings(t, filepath.Join(work, "in-"+kind+".json"))
		want, _ := os.ReadFile(h)
		got := strings.Split(strings.TrimSpace(string(want)), "\n")
		if len(got) != len(inputs) {
			t.Fatalf("%s: %d hashes for %d inputs", kind, len(got), len(inputs))
		}
		bad := 0
		classes := map[string]int{}
		examples := map[string][]string{}
		for i, s := range inputs {
			g, p := dumpBoth(s)
			if fnvDump(g, p) == got[i] {
				continue
			}
			bad++
			class := "root"
			if rootHasError(g) {
				class = "recovery"
			}
			classes[class]++
			if len(examples[class]) < limit {
				examples[class] = append(examples[class], fmt.Sprintf("%q", s))
			}
		}
		t.Logf("%s: %d inputs, %d differ %v", kind, len(inputs), bad, classes)
		for c, ex := range examples {
			t.Logf("  %s: %s", c, strings.Join(ex, "  "))
		}
	}
}

func loadStrings(t *testing.T, path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rootHasError(dump string) bool {
	line, _, _ := strings.Cut(dump, "\n")
	f := strings.Split(line, "|")
	return len(f) > 2 && strings.Contains(f[2], "E")
}

// classify names how two dumps differ: the shape of the first differing line pair.
func classify(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	rec := ""
	if rootHasError(want) {
		rec = "recovery/"
	} else {
		rec = "clean/"
	}
	for i := 0; i < len(wl) && i < len(gl); i++ {
		if wl[i] == gl[i] {
			continue
		}
		w, g := strings.Split(wl[i], "|"), strings.Split(gl[i], "|")
		for k := 1; k < len(w) && k < len(g); k++ {
			if w[k] != g[k] {
				return rec + [...]string{"depth", "type", "flags", "start", "end", "childCount", "field", "prev"}[k]
			}
		}
		return rec + "line"
	}
	return rec + "length"
}

func firstDiff(wg, gg, wp, gp string) string {
	pick := func(w, g string) string {
		wl, gl := strings.Split(w, "\n"), strings.Split(g, "\n")
		for i := 0; i < len(wl) || i < len(gl); i++ {
			var a, b string
			if i < len(wl) {
				a = wl[i]
			}
			if i < len(gl) {
				b = gl[i]
			}
			if a != b {
				return fmt.Sprintf("line %d\n  want %s\n  got  %s", i, a, b)
			}
		}
		return ""
	}
	if wg != gg {
		return "grammar " + pick(wg, gg)
	}
	return "corrected " + pick(wp, gp)
}
