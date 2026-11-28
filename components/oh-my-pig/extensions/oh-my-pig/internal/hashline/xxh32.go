package hashline

import "encoding/binary"

// Pure-Go xxHash32 and xxHash64 (seed 0) matching crates/edit/src/store.rs
// (xxhash_rust::xxh32::Xxh32 and xxhash_rust::xxh64::xxh64).
const (
	prime32_1 uint32 = 0x9E3779B1
	prime32_2 uint32 = 0x85EBCA77
	prime32_3 uint32 = 0xC2B2AE3D
	prime32_4 uint32 = 0x27D4EB2F
	prime32_5 uint32 = 0x165667B1

	prime64_1 uint64 = 0x9E3779B185EBCA87
	prime64_2 uint64 = 0xC2B2AE3D27D4EB4F
	prime64_3 uint64 = 0x165667B19E3779F9
	prime64_4 uint64 = 0x85EBCA77C2B2AE63
	prime64_5 uint64 = 0x27D4EB2F165667C5
)

type xxh32 struct {
	totalLen uint32
	v1       uint32
	v2       uint32
	v3       uint32
	v4       uint32
	mem      [16]byte
	memSize  int
	large    bool
}

func newXXH32(seed uint32) xxh32 {
	return xxh32{
		v1: seed + prime32_1 + prime32_2,
		v2: seed + prime32_2,
		v3: seed,
		v4: seed - prime32_1,
	}
}

func rotl32(x uint32, r uint) uint32 {
	return (x << r) | (x >> (32 - r))
}

func xxh32Round(acc, input uint32) uint32 {
	acc += input * prime32_2
	acc = rotl32(acc, 13)
	acc *= prime32_1
	return acc
}

func (h *xxh32) update(b []byte) {
	if len(b) == 0 {
		return
	}
	h.totalLen += uint32(len(b))
	if h.memSize+len(b) < 16 {
		copy(h.mem[h.memSize:], b)
		h.memSize += len(b)
		return
	}
	h.large = true
	p := 0
	if h.memSize > 0 {
		need := 16 - h.memSize
		copy(h.mem[h.memSize:], b[:need])
		h.v1 = xxh32Round(h.v1, binary.LittleEndian.Uint32(h.mem[0:4]))
		h.v2 = xxh32Round(h.v2, binary.LittleEndian.Uint32(h.mem[4:8]))
		h.v3 = xxh32Round(h.v3, binary.LittleEndian.Uint32(h.mem[8:12]))
		h.v4 = xxh32Round(h.v4, binary.LittleEndian.Uint32(h.mem[12:16]))
		p = need
		h.memSize = 0
	}
	for p+16 <= len(b) {
		h.v1 = xxh32Round(h.v1, binary.LittleEndian.Uint32(b[p:p+4]))
		h.v2 = xxh32Round(h.v2, binary.LittleEndian.Uint32(b[p+4:p+8]))
		h.v3 = xxh32Round(h.v3, binary.LittleEndian.Uint32(b[p+8:p+12]))
		h.v4 = xxh32Round(h.v4, binary.LittleEndian.Uint32(b[p+12:p+16]))
		p += 16
	}
	if p < len(b) {
		h.memSize = copy(h.mem[:], b[p:])
	}
}

func (h *xxh32) digest() uint32 {
	var acc uint32
	if h.large {
		acc = rotl32(h.v1, 1) + rotl32(h.v2, 7) + rotl32(h.v3, 12) + rotl32(h.v4, 18)
	} else {
		acc = h.v3 + prime32_5
	}
	acc += h.totalLen

	p := 0
	for p+4 <= h.memSize {
		acc += binary.LittleEndian.Uint32(h.mem[p:p+4]) * prime32_3
		acc = rotl32(acc, 17) * prime32_4
		p += 4
	}
	for p < h.memSize {
		acc += uint32(h.mem[p]) * prime32_5
		acc = rotl32(acc, 11) * prime32_1
		p++
	}

	acc ^= acc >> 15
	acc *= prime32_2
	acc ^= acc >> 13
	acc *= prime32_3
	acc ^= acc >> 16
	return acc
}

func rotl64(x uint64, r uint) uint64 {
	return (x << r) | (x >> (64 - r))
}

func xxh64Round(acc, input uint64) uint64 {
	acc += input * prime64_2
	acc = rotl64(acc, 31)
	acc *= prime64_1
	return acc
}

func xxh64MergeRound(acc, val uint64) uint64 {
	val = xxh64Round(0, val)
	acc ^= val
	acc = acc*prime64_1 + prime64_4
	return acc
}

// xxh64Sum computes the 64-bit xxHash of b with seed.
func xxh64Sum(b []byte, seed uint64) uint64 {
	n := len(b)
	var acc uint64
	p := 0
	if n >= 32 {
		v1 := seed + prime64_1 + prime64_2
		v2 := seed + prime64_2
		v3 := seed
		v4 := seed - prime64_1
		for p+32 <= n {
			v1 = xxh64Round(v1, binary.LittleEndian.Uint64(b[p:p+8]))
			v2 = xxh64Round(v2, binary.LittleEndian.Uint64(b[p+8:p+16]))
			v3 = xxh64Round(v3, binary.LittleEndian.Uint64(b[p+16:p+24]))
			v4 = xxh64Round(v4, binary.LittleEndian.Uint64(b[p+24:p+32]))
			p += 32
		}
		acc = rotl64(v1, 1) + rotl64(v2, 7) + rotl64(v3, 12) + rotl64(v4, 18)
		acc = xxh64MergeRound(acc, v1)
		acc = xxh64MergeRound(acc, v2)
		acc = xxh64MergeRound(acc, v3)
		acc = xxh64MergeRound(acc, v4)
	} else {
		acc = seed + prime64_5
	}
	acc += uint64(n)
	for p+8 <= n {
		k1 := xxh64Round(0, binary.LittleEndian.Uint64(b[p:p+8]))
		acc ^= k1
		acc = rotl64(acc, 27)*prime64_1 + prime64_4
		p += 8
	}
	if p+4 <= n {
		acc ^= uint64(binary.LittleEndian.Uint32(b[p:p+4])) * prime64_1
		acc = rotl64(acc, 23)*prime64_2 + prime64_3
		p += 4
	}
	for p < n {
		acc ^= uint64(b[p]) * prime64_5
		acc = rotl64(acc, 11) * prime64_1
		p++
	}
	acc ^= acc >> 33
	acc *= prime64_2
	acc ^= acc >> 29
	acc *= prime64_3
	acc ^= acc >> 32
	return acc
}

// PayloadHash computes a stable 64-bit key for raw patch input.
// upstream: crates/edit/src/store.rs:106
func PayloadHash(text string) uint64 {
	return xxh64Sum([]byte(text), 0)
}
