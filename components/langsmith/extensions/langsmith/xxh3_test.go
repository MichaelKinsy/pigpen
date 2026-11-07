package langsmith

import (
	"strconv"
	"testing"
)

type xxh3Vectors struct {
	ByLength []string `json:"byLength"`
	Long     []struct {
		Seed uint32 `json:"seed"`
		Len  int    `json:"len"`
		Hash string `json:"hash"`
	} `json:"long"`
	Random []struct {
		In   string `json:"in"`
		Hash string `json:"hash"`
	} `json:"random"`
	Exhaustive []struct {
		In   string `json:"in"`
		Hash string `json:"hash"`
	} `json:"exhaustive"`
	Unicode []struct {
		S    string `json:"s"`
		Hash string `json:"hash"`
	} `json:"unicode"`
}

func loadXXH3(t testing.TB) *xxh3Vectors {
	var v xxh3Vectors
	loadWireVectors(t, "wire-xxh3.json", &v)
	return &v
}

func TestXXH3ByLength(t *testing.T) {
	v := loadXXH3(t)
	if len(v.ByLength) != 701 {
		t.Fatalf("vectors: %d lengths", len(v.ByLength))
	}
	for n, want := range v.ByLength {
		if got := hashHex(wireBytes(uint32(n+1), n)); got != want {
			t.Fatalf("len %d: got %s want %s", n, got, want)
		}
	}
}

func TestXXH3Long(t *testing.T) {
	v := loadXXH3(t)
	if len(v.Long) < 50 {
		t.Fatalf("vectors: %d", len(v.Long))
	}
	for _, c := range v.Long {
		if got := hashHex(wireBytes(c.Seed, c.Len)); got != c.Hash {
			t.Fatalf("len %d: got %s want %s", c.Len, got, c.Hash)
		}
	}
}

func TestXXH3RandomAndExhaustive(t *testing.T) {
	v := loadXXH3(t)
	if len(v.Random) != 3000 || len(v.Exhaustive) < 256 {
		t.Fatalf("vectors: %d %d", len(v.Random), len(v.Exhaustive))
	}
	for _, set := range [][]struct {
		In   string `json:"in"`
		Hash string `json:"hash"`
	}{v.Random, v.Exhaustive} {
		for _, c := range set {
			if got := hashHex(mustHex(t, c.In)); got != c.Hash {
				t.Fatalf("in %s: got %s want %s", c.In, got, c.Hash)
			}
		}
	}
}

func TestXXH3Unicode(t *testing.T) {
	v := loadXXH3(t)
	for _, c := range v.Unicode {
		if got := hashHex([]byte(jsValidUTF8(c.S))); got != c.Hash {
			t.Fatalf("%q: got %s want %s", c.S, got, c.Hash)
		}
	}
}

func TestXXH3HalvesAndBytes(t *testing.T) {
	hi, lo := xxh3_128(nil)
	b := xxh128ToBytes(hi, lo)
	if b[0] != byte(hi>>56) || b[7] != byte(hi) || b[8] != byte(lo>>56) || b[15] != byte(lo) {
		t.Fatalf("xxh128ToBytes layout: %x", b)
	}
}

func TestXXH3NoAllocs(t *testing.T) {
	for _, n := range []int{0, 2, 7, 12, 64, 200, 5000} {
		data := wireBytes(5, n)
		if a := testing.AllocsPerRun(50, func() { xxh3_128(data) }); a != 0 {
			t.Errorf("len %d: %v allocs", n, a)
		}
	}
}

func BenchmarkXXH3(b *testing.B) {
	for _, n := range []int{16, 36, 64, 128, 240, 1024, 65536} {
		data := wireBytes(3, n)
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n))
			var sink uint64
			for i := 0; i < b.N; i++ {
				h, l := xxh3_128(data)
				sink += h ^ l
			}
			_ = sink
		})
	}
}
