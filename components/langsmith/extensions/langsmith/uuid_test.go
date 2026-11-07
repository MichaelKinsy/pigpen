package langsmith

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

type uuidVectors struct {
	Deterministic []struct{ ID, Key, Out string } `json:"deterministic"`
	Versions      []struct {
		ID      string `json:"id"`
		Version *int   `json:"version"`
	} `json:"versions"`
	Asserts []struct {
		ID     string  `json:"id"`
		Err    *string `json:"err"`
		ErrFor *string `json:"errFor"`
	} `json:"asserts"`
	Time []struct {
		Ms  int64  `json:"ms"`
		Out string `json:"out"`
	} `json:"time"`
	TimeStr []struct {
		S   string `json:"s"`
		Out string `json:"out"`
	} `json:"timeStr"`
}

func loadUUID(t testing.TB) *uuidVectors {
	var v uuidVectors
	loadWireVectors(t, "wire-uuid.json", &v)
	return &v
}

func TestNonCryptographicUuid7Deterministic(t *testing.T) {
	v := loadUUID(t)
	if len(v.Deterministic) < 240 {
		t.Fatalf("vectors: %d", len(v.Deterministic))
	}
	for _, c := range v.Deterministic {
		if got := nonCryptographicUuid7Deterministic(c.ID, c.Key); got != c.Out {
			t.Fatalf("(%q,%q): got %s want %s", c.ID, c.Key, got, c.Out)
		}
	}
}

func TestUUIDVersion(t *testing.T) {
	v := loadUUID(t)
	for _, c := range v.Versions {
		want := -1
		if c.Version != nil {
			want = *c.Version
		}
		if got := uuidVersion(c.ID); got != want {
			t.Errorf("%q: got %d want %d", c.ID, got, want)
		}
	}
}

func TestAssertUUID(t *testing.T) {
	v := loadUUID(t)
	for _, c := range v.Asserts {
		check := func(err error, want *string) {
			if (err == nil) != (want == nil) || (err != nil && err.Error() != *want) {
				t.Errorf("%q: got %v want %v", c.ID, err, want)
			}
		}
		check(assertUUID(c.ID), c.Err)
		check(assertUUIDFor(c.ID, "run_id"), c.ErrFor)
	}
}

func TestUUID7FromTime(t *testing.T) {
	v := loadUUID(t)
	for _, c := range v.Time {
		got := uuid7FromTime(c.Ms)
		if got[:24] != c.Out[:24] {
			t.Errorf("ms %d: prefix got %s want %s", c.Ms, got, c.Out)
		}
		if !uuidShape(got) || got[24] < '0' || got[24] > '0' || got[25] < '0' || got[25] > '3' {
			t.Errorf("ms %d: byte 10 out of range: %s", c.Ms, got)
		}
	}
	for _, c := range v.TimeStr {
		// the Go signature takes the epoch; Date.parse of the ISO string is exactly that
		ms := mustParseISO(t, c.S)
		if got := uuid7FromTime(ms); got[:24] != c.Out[:24] {
			t.Errorf("%s: got %s want %s", c.S, got, c.Out)
		}
	}
}

func TestUUID7FromTimeRandomBytes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		u := uuid7FromTime(1700000000000)
		if seen[u[24:]] {
			t.Fatalf("repeated random tail %s", u)
		}
		seen[u[24:]] = true
	}
}

func TestJSValidUTF8(t *testing.T) {
	for in, want := range map[string]string{
		"abc":            "abc",
		"\xed\xa0\x80x":  "\uFFFDx",      // lone high surrogate (WTF-8)
		"a\xed\xb0\x80b": "a\uFFFDb",     // lone low surrogate
		"\xffz":          "\uFFFDz",      // stray byte
		"\xe6\x97":       "\uFFFD\uFFFD", // truncated sequence: one per byte, as Go decodes
	} {
		if got := jsValidUTF8(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestWarnIfNotUUIDv7(t *testing.T) {
	var buf bytes.Buffer
	old := wireWarnWriter
	wireWarnWriter = &buf
	uuid7WarningEmitted.Store(false)
	defer func() { wireWarnWriter = old }()
	warnIfNotUUIDv7("019a0000-0000-7000-8000-000000000000")
	warnIfNotUUIDv7("not a uuid")
	if buf.Len() != 0 {
		t.Fatalf("v7/invalid warned: %q", buf.String())
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); warnIfNotUUIDv7("019a0000-0000-4000-8000-000000000000") }()
	}
	wg.Wait()
	want := "LangSmith now uses UUID v7 for run and trace identifiers. This warning appears when passing custom IDs. Please use: import { uuidv7 } from 'langsmith'; const id = uuidv7(); Future versions will require UUID v7.\n"
	if buf.String() != want {
		t.Fatalf("got %q", buf.String())
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Fatal("warned more than once")
	}
}

func TestNonCryptographicUuid7DeterministicNoAllocs(t *testing.T) {
	id := "019a0000-0000-7000-8000-000000000000"
	if a := testing.AllocsPerRun(50, func() { nonCryptographicUuid7Deterministic(id, "project") }); a > 1 {
		t.Errorf("%v allocs, want 1 (the result string)", a)
	}
}

func BenchmarkUUID7FromTime(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		uuid7FromTime(1700000000000)
	}
}

func BenchmarkNonCryptographicUuid7Deterministic(b *testing.B) {
	b.ReportAllocs()
	id := "019a0000-0000-7000-8000-000000000000"
	for i := 0; i < b.N; i++ {
		nonCryptographicUuid7Deterministic(id, "my-project")
	}
}
