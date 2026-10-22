package stretchtext

import (
	"encoding/json"
	"os"
	"testing"
)

// The same inputs and operations as port/perf/bench-original.mjs, which times the original.
type benchInputs struct {
	Reply    string    `json:"reply"`
	Short    string    `json:"short"`
	Long     string    `json:"long"`
	Progress []float64 `json:"progress"`
}

func loadBenchInputs(b *testing.B) benchInputs {
	b.Helper()
	data, err := os.ReadFile("../../port/perf/inputs.json")
	if err != nil {
		b.Fatal(err)
	}
	var in benchInputs
	if err := json.Unmarshal(data, &in); err != nil {
		b.Fatal(err)
	}
	return in
}

func BenchmarkParse(b *testing.B) {
	in := loadBenchInputs(b)
	b.ReportAllocs()
	for b.Loop() {
		Parse(in.Reply)
	}
}

func BenchmarkGrow(b *testing.B) {
	in := loadBenchInputs(b)
	long := Parse(in.Long)
	draw := func(n *Stretch) string { return "<" + n.Key + ">" }
	paint := func(w string) string { return "[" + w + "]" }
	b.ReportAllocs()
	for b.Loop() {
		for _, p := range in.Progress {
			Grow(in.Short, long, p, draw, paint)
		}
	}
}
