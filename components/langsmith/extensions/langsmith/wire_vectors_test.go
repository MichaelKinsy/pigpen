package langsmith

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// loadWireVectors decodes testdata/<name>, produced by port/tools/gen/wire-*.mjs from the real langsmith 0.10.5 JS.
func loadWireVectors(t testing.TB, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// wireBytes is wire-common.mjs bytes(seed, len): len bytes `next()>>>24` of xorshift32(seed).
func wireBytes(seed uint32, n int) []byte {
	x := seed
	if x == 0 {
		x = 1
	}
	b := make([]byte, n)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x >> 24)
	}
	return b
}

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hashHex(data []byte) string {
	h := xxh128ToBytes(xxh3_128(data))
	return hex.EncodeToString(h[:])
}
