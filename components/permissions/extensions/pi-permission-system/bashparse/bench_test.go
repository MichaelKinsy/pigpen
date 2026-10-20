package bashparse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Benchmarks of the Go parse on port/bashparse-diff/bench-inputs.json, the inputs the original's WASM
// parser is timed on (bench.mjs): the grammar parse alone and the corrected parse (getParser).

var processStart = time.Now()

func benchInputs(tb testing.TB) map[string]string {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "port", "bashparse-diff", "bench-inputs.json"))
	if err != nil {
		tb.Skip("bench inputs not found: ", err)
	}
	var in map[string]string
	if err := json.Unmarshal(data, &in); err != nil {
		tb.Fatal(err)
	}
	return in
}

func BenchmarkParse(b *testing.B) {
	inputs := benchInputs(b)
	p, err := GetParser()
	if err != nil {
		b.Fatal(err)
	}
	for name, s := range inputs {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if p.Parse(s) == nil {
					b.Fatal("nil tree")
				}
			}
		})
	}
}

// TestColdStart logs the time from process start to the first parse (tables load included).
func TestColdStart(t *testing.T) {
	start := time.Now()
	p, err := GetParser()
	if err != nil {
		t.Fatal(err)
	}
	loaded := time.Since(start)
	p.Parse("echo hi")
	t.Logf("COLD load %v, first parse %v, since process start %v", loaded, time.Since(start)-loaded, time.Since(processStart))
}
