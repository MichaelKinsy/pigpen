package langsmith

import (
	"math"
	"testing"
)

type samplingVectors struct {
	Rates         []float64 `json:"rates"`
	UndefinedRate []bool    `json:"undefinedRate"`
	NaNRate       []bool    `json:"nanRate"`
	Cases         []struct {
		ID     string `json:"id"`
		Bucket uint64 `json:"bucket"`
		Bits   string `json:"bits"`
	} `json:"cases"`
}

func TestIsSampledByID(t *testing.T) {
	var v samplingVectors
	loadWireVectors(t, "wire-sampling.json", &v)
	if len(v.Cases) < 400 || len(v.Rates) < 10 {
		t.Fatalf("vectors: %d cases, %d rates", len(v.Cases), len(v.Rates))
	}
	sampled := 0
	for _, c := range v.Cases {
		if got := samplingBucket(c.ID); got != c.Bucket {
			t.Fatalf("bucket(%q): got %d want %d", c.ID, got, c.Bucket)
		}
		for i, r := range v.Rates {
			r := r
			if got := isSampledByID(c.ID, &r); got != (c.Bits[i] == '1') {
				t.Fatalf("isSampledByID(%q, %v): got %v want %c", c.ID, r, got, c.Bits[i])
			}
			if c.Bits[i] == '1' {
				sampled++
			}
		}
	}
	if sampled == 0 {
		t.Fatal("no sampled case")
	}
	for i, want := range v.UndefinedRate {
		if got := isSampledByID(v.Cases[i].ID, nil); got != want {
			t.Errorf("nil rate: got %v want %v", got, want)
		}
	}
	nan := math.NaN()
	for i, want := range v.NaNRate {
		if got := isSampledByID(v.Cases[i].ID, &nan); got != want {
			t.Errorf("NaN rate: got %v want %v", got, want)
		}
	}
}

func TestJSLowerCase(t *testing.T) {
	for in, want := range map[string]string{
		"ABC":          "abc",
		"İ":            "i\u0307",
		"ΑΣ":           "ας",
		"ΑΣΑ":          "ασα",
		"Σ":            "σ",
		"ΑΣ.":          "ας.",
		"ΑΣ.Α":         "ασ.α",
		"Straße":       "straße",
		"\u212A":       "k",
		"\xed\xa0\x80": "\uFFFD",
	} {
		if got := string(jsUTF8Lower(nil, in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestSamplingNoAllocs(t *testing.T) {
	r := 0.5
	id := "019a0000-0000-7000-8000-000000000000"
	if a := testing.AllocsPerRun(50, func() { isSampledByID(id, &r) }); a != 0 {
		t.Errorf("%v allocs", a)
	}
}

func BenchmarkIsSampledByID(b *testing.B) {
	b.ReportAllocs()
	r := 0.5
	id := "019a0000-0000-7000-8000-000000000000"
	for i := 0; i < b.N; i++ {
		isSampledByID(id, &r)
	}
}
