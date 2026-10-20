package bashparse

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse/internal/ts"
)

// The differential proof: the Go parse against the original's own parse trees (tree-sitter-bash
// 0.25.1 through web-tree-sitter 0.26.9), recorded by port/bashparse-diff/run.sh and packed into
// testdata/ by pack-testdata.py. Every node of the grammar's tree (type, named, error, missing,
// extra, UTF-16 span, child count, field, previous sibling) and of the corrected tree (what
// getParser returns) is compared. The recorded corpora:
//
//	a        every string literal of the original's tests under test/access-intent/bash and
//	         test/handlers plus the 646 commands of port/probe/cases.json, full node dumps
//	b        every other string literal of the original's tests
//	shapes   hand-built shapes: heredocs of every kind, redirects, quoting, expansions, compound
//	         statements, CRLF, unicode, lone surrogates, very long input
//	prefix   every prefix of each corpus a string (error recovery at every cut)
//	del      every single-character deletion of each corpus a string
//	fuzz     token soup, mutations and splices of the above, and non-BMP characters split by the web
//	         binding's 5119-unit read chunk (fuzzgen_test.go)
//
// All but corpus a are compared by a 32-bit hash of both dumps, so a disagreement is found here and
// investigated with `BP_WORK=<run.sh work dir> go test -run TestDiffWork`.

// knownDifferences are the inputs on which the original itself fails, so there is no tree to
// agree with.
var knownDifferences = map[string]string{
	strings.Repeat("(", 900) + strings.Repeat(")", 900): "the original's wasm traps with `memory access out of bounds` " +
		"(its stack is exhausted) walking an 1800-deep nest of empty subshells, so its dump is an exception",
}

type corpusRecord struct{ S, G, P string }

func readGzip(t testing.TB, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadCorpusA(t testing.TB) []corpusRecord {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(readGzip(t, "corpus-a.jsonl.gz")))
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	var recs []corpusRecord
	for sc.Scan() {
		var r corpusRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, r)
	}
	return recs
}

func loadStrings(t testing.TB, name string) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal(readGzip(t, name), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadHashes(t testing.TB, name string) ([]uint32, bool) {
	t.Helper()
	if _, err := os.Stat(filepath.Join("testdata", name)); err != nil {
		return nil, false
	}
	data := readGzip(t, name)
	hs := make([]uint32, len(data)/4)
	for i := range hs {
		hs[i] = binary.LittleEndian.Uint32(data[i*4:])
	}
	return hs, true
}

// differentialSeeds are the recorded strings the fuzz inputs are built from: corpus a and shapes.
func differentialSeeds(t testing.TB) []string {
	t.Helper()
	var seeds []string
	for _, r := range loadCorpusA(t) {
		seeds = append(seeds, r.S)
	}
	return append(seeds, loadStrings(t, "shapes.json.gz")...)
}

// variantsOf is gen-variants.mjs: every prefix and every single-code-point deletion of each corpus
// a string (prefixes of strings up to 400 code points, deletions of strings up to 200), deduplicated
// against the corpus and each other, in a deterministic order.
func variantsOf(a []string) (prefix, del []string) {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range a {
		cps := []rune(s)
		if len(cps) <= 400 {
			for i := 1; i < len(cps); i++ {
				if v := string(cps[:i]); !seen[v] {
					seen[v] = true
					prefix = append(prefix, v)
				}
			}
		}
		if len(cps) <= 200 {
			for i := range cps {
				if v := string(cps[:i]) + string(cps[i+1:]); !seen[v] {
					seen[v] = true
					del = append(del, v)
				}
			}
		}
	}
	return prefix, del
}

type differentialWorker struct{ g, c Parser }

func newDifferentialWorker(t testing.TB) *differentialWorker {
	t.Helper()
	lang, err := ts.BashLanguage()
	if err != nil {
		t.Fatal(err)
	}
	g := newGrammarParser(lang)
	return &differentialWorker{g, &correctingParser{g}}
}

func (w *differentialWorker) hash(input string) uint32 {
	g, p := dumpWith(w.g, w.c, input)
	var h uint32 = 0x811c9dc5
	for _, s := range []string{g, "\x01", p} {
		for i := 0; i < len(s); i++ {
			h ^= uint32(s[i])
			h *= 0x01000193
		}
	}
	return h
}

// forEach runs fn over 0..n-1 on one parser per CPU.
func forEach(t testing.TB, n int, fn func(w *differentialWorker, i int)) {
	t.Helper()
	workers := runtime.GOMAXPROCS(0)
	var next int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		w := newDifferentialWorker(t)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n {
					return
				}
				fn(w, i)
			}
		}()
	}
	wg.Wait()
}

// compareHashes checks inputs against recorded hashes and reports every disagreement not in
// knownDifferences.
func compareHashes(t *testing.T, kind string, inputs []string, want []uint32) {
	t.Helper()
	if len(inputs) != len(want) {
		t.Fatalf("%s: %d inputs but %d recorded hashes", kind, len(inputs), len(want))
	}
	var mu sync.Mutex
	var bad []string
	known := 0
	forEach(t, len(inputs), func(w *differentialWorker, i int) {
		if w.hash(inputs[i]) == want[i] {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if _, ok := knownDifferences[inputs[i]]; ok {
			known++
			return
		}
		bad = append(bad, inputs[i])
	})
	t.Logf("%s: %d inputs, %d differ from the original (%d known)", kind, len(inputs), len(bad), known)
	if path := os.Getenv("BP_DUMP_MISMATCH"); path != "" && len(bad) > 0 {
		// The inputs to record the original's full dumps for (run-dump.sh), then TestDiffDump.
		data, _ := json.Marshal(bad)
		_ = os.WriteFile(path, data, 0o644)
	}
	for i, s := range bad {
		if i == 10 {
			t.Errorf("... and %d more", len(bad)-10)
			break
		}
		t.Errorf("%s: differs from the original: %q", kind, s)
	}
}

func TestDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("the differential against the original's recorded parse trees is long")
	}
	recs := loadCorpusA(t)
	a := make([]string, len(recs))
	for i, r := range recs {
		a[i] = r.S
	}

	t.Run("a: every node of every original-test string", func(t *testing.T) {
		var mu sync.Mutex
		var bad []string
		forEach(t, len(recs), func(w *differentialWorker, i int) {
			g, p := dumpWith(w.g, w.c, recs[i].S)
			if g == recs[i].G && p == recs[i].P {
				return
			}
			mu.Lock()
			bad = append(bad, recs[i].S+"\n"+firstDiff(recs[i].G, g, recs[i].P, p))
			mu.Unlock()
		})
		t.Logf("a: %d inputs, %d differ", len(recs), len(bad))
		for i, s := range bad {
			if i == 5 {
				break
			}
			t.Errorf("%q", s)
		}
	})

	for _, kind := range []string{"b", "shapes"} {
		t.Run(kind, func(t *testing.T) {
			name := map[string]string{"b": "corpus-b.json.gz", "shapes": "shapes.json.gz"}[kind]
			want, ok := loadHashes(t, "hash-"+kind+".bin.gz")
			if !ok {
				t.Skip("no recorded hashes")
			}
			compareHashes(t, kind, loadStrings(t, name), want)
		})
	}

	prefix, del := variantsOf(a)
	t.Run("prefix", func(t *testing.T) {
		want, ok := loadHashes(t, "hash-prefix.bin.gz")
		if !ok {
			t.Skip("no recorded hashes")
		}
		compareHashes(t, "prefix", prefix, want)
	})
	t.Run("del", func(t *testing.T) {
		want, ok := loadHashes(t, "hash-del.bin.gz")
		if !ok {
			t.Skip("no recorded hashes")
		}
		compareHashes(t, "del", del, want)
	})
	t.Run("fuzz", func(t *testing.T) {
		want, ok := loadHashes(t, "hash-fuzz.bin.gz")
		if !ok {
			t.Skip("no recorded hashes")
		}
		compareHashes(t, "fuzz", fuzzInputs(differentialSeeds(t)), want)
	})
}
