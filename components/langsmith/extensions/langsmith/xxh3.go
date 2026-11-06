package langsmith

// Port of utils/xxhash/xxhash.js (langsmith 0.10.5): XXH3 128-bit, seed 0, default secret, every length class
// (0-16, 17-128, 129-240, long). Follow the vendored JavaScript, not the reference C, where they differ.
// OWNER: wire lane.

// xxh3_128 returns the 128-bit hash as high and low 64-bit halves (JS: `(high << 64) | low`).
func xxh3_128(data []byte) (high, low uint64) { panic("unported: xxh3.go xxh3_128") }

// xxh128ToBytes is xxhash.js xxh128ToBytes: high64 then low64, both big-endian.
func xxh128ToBytes(high, low uint64) [16]byte { panic("unported: xxh3.go xxh128ToBytes") }
