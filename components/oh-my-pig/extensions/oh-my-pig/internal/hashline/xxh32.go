package hashline

import "encoding/binary"

// Pure-Go xxHash32 implementation (seed 0) matching crates/pi-edit/src/store.rs (xxhash_rust::xxh32::Xxh32).
const (
	prime32_1 uint32 = 0x9E3779B1
	prime32_2 uint32 = 0x85EBCA77
	prime32_3 uint32 = 0xC2B2AE3D
	prime32_4 uint32 = 0x27D4EB2F
	prime32_5 uint32 = 0x165667B1
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
