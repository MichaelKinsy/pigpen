package langsmith

import (
	"testing"
	"time"
)

type timeVectors struct {
	RunID string `json:"runId"`
	Cases []struct {
		Epoch  int64  `json:"epoch"`
		Order  int    `json:"order"`
		DS     string `json:"ds"`
		Dotted string `json:"dotted"`
		Error  string `json:"error"`
	} `json:"cases"`
	ISO []struct {
		Epoch int64  `json:"epoch"`
		ISO   string `json:"iso"`
	} `json:"iso"`
}

func mustParseISO(t testing.TB, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm.UnixMilli()
}

func panics(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg, _ = r.(string)
		}
	}()
	f()
	return ""
}

func TestDottedOrderVectors(t *testing.T) {
	var v timeVectors
	loadWireVectors(t, "wire-time.json", &v)
	if len(v.Cases) < 500 {
		t.Fatalf("vectors: %d", len(v.Cases))
	}
	errors := 0
	for _, c := range v.Cases {
		if c.Error != "" {
			errors++
			if got := panics(func() { convertToDottedOrderFormat(c.Epoch, v.RunID, c.Order) }); got != c.Error {
				t.Errorf("epoch %d: panic %q want %q", c.Epoch, got, c.Error)
			}
			continue
		}
		dotted, ds := convertToDottedOrderFormat(c.Epoch, v.RunID, c.Order)
		if ds != c.DS || dotted != c.Dotted {
			t.Fatalf("epoch %d order %d: got %q %q want %q %q", c.Epoch, c.Order, ds, dotted, c.DS, c.Dotted)
		}
		if got := getMicrosecondPrecisionDatestring(c.Epoch, c.Order); got != c.DS {
			t.Fatalf("epoch %d order %d: datestring %q want %q", c.Epoch, c.Order, got, c.DS)
		}
	}
	if errors < 3 {
		t.Fatalf("expected the RangeError cases, saw %d", errors)
	}
}

func TestISOStringVectors(t *testing.T) {
	var v timeVectors
	loadWireVectors(t, "wire-time.json", &v)
	for _, c := range v.ISO {
		if got := jsISOString(c.Epoch); got != c.ISO {
			t.Fatalf("epoch %d: got %q want %q", c.Epoch, got, c.ISO)
		}
	}
}

func TestNowMs(t *testing.T) {
	before := time.Now().UnixMilli()
	n := nowMs()
	if n < before || n > time.Now().UnixMilli() {
		t.Fatalf("nowMs %d", n)
	}
}

func TestDatestringAllocs(t *testing.T) {
	if a := testing.AllocsPerRun(50, func() { getMicrosecondPrecisionDatestring(1700000000000, 1) }); a > 1 {
		t.Errorf("%v allocs", a)
	}
}

func BenchmarkConvertToDottedOrderFormat(b *testing.B) {
	b.ReportAllocs()
	id := "019a0000-1111-7222-8333-444455556666"
	for i := 0; i < b.N; i++ {
		convertToDottedOrderFormat(1700000000000, id, 1)
	}
}
