package langsmith

// Port of utils/xxhash/xxhash.js (langsmith 0.10.5): XXH3 128-bit, seed 0, default secret, every length class
// (0-16, 17-128, 129-240, long). The vendored JavaScript (xxh3-ts) is followed, not the reference C: it differs
// in two places, both reproduced here and listed in port/lane-wire.md (findings W1, W2).

import (
	"encoding/binary"
	"encoding/hex"
	"math/bits"
)

const (
	xxPrime32_1 uint64 = 0x9E3779B1
	xxPrime32_2 uint64 = 0x85EBCA77
	xxPrime32_3 uint64 = 0xC2B2AE3D
	xxPrime64_1 uint64 = 0x9E3779B185EBCA87
	xxPrime64_2 uint64 = 0xC2B2AE3D27D4EB4F
	xxPrime64_3 uint64 = 0x165667B19E3779F9
	xxPrime64_4 uint64 = 0x85EBCA77C2B2AE63
	xxPrime64_5 uint64 = 0x27D4EB2F165667C5
	xxPrimeMX1  uint64 = 0x165667919E3779F9
	xxPrimeMX2  uint64 = 0x9FB21C651E98DF25

	xxStripeLen = 64
	xxAccNB     = 8
)

// xxSecret is `kkey` (xxhash.js:11): the default XXH3 secret, 192 bytes.
var xxSecret = func() (k [192]byte) {
	const h = "b8fe6c3923a44bbe7c01812cf721ad1cded46de9839097db7240a4a4b7b3671fcb79e64eccc0e578825ad07dccff7221b8084674f743248ee03590e6813a264c3c2852bb91c300cb88d0658b1b532ea371644897a20df94e3819ef46a9deacd8a8fa763fe39c343ff9dcbbc7c70b4f1d8a51e04bcdb45931c89f7ec9d9787364eac5ac8334d3ebc3c581a0fffa1363eb170ddd51b7f0da49d316552629d4689e2b16be587d47a1fc8ff8b8d17ad031ce45cb3a8f95160428afd7fbcabb4b407e"
	if _, err := hex.Decode(k[:], []byte(h)); err != nil {
		panic(err)
	}
	return
}()

func rd64(b []byte, off int) uint64 { return binary.LittleEndian.Uint64(b[off:]) }
func rd32(b []byte, off int) uint64 { return uint64(binary.LittleEndian.Uint32(b[off:])) }

// xxMul128Fold64 is XXH3_mul128_fold64: low ^ high of the 128-bit product.
func xxMul128Fold64(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return lo ^ hi
}

// xxAvalanche is XXH3_avalanche (xxhash.js:157).
func xxAvalanche(h uint64) uint64 {
	h ^= h >> 37
	h *= xxPrimeMX1
	h ^= h >> 32
	return h
}

// xxAvalanche64 is XXH3_avalanche64 (xxhash.js:165).
func xxAvalanche64(h uint64) uint64 {
	h ^= h >> 33
	h *= xxPrime64_2
	h ^= h >> 29
	h *= xxPrime64_3
	h ^= h >> 32
	return h
}

// xxMix16B is XXH3_mix16B with seed 0.
func xxMix16B(data, key []byte) uint64 {
	return xxMul128Fold64(rd64(data, 0)^rd64(key, 0), rd64(data, 8)^rd64(key, 8))
}

// xxMix32B is XXH3_mix32B with seed 0 (accl/acch are the low/high halves of its 128-bit accumulator).
func xxMix32B(accl, acch uint64, d1, d2, key []byte) (uint64, uint64) {
	accl += xxMix16B(d1, key)
	accl ^= rd64(d2, 0) + rd64(d2, 8)
	acch += xxMix16B(d2, key[16:])
	acch ^= rd64(d1, 0) + rd64(d1, 8)
	return accl, acch
}

// xxFinishMid is the tail shared by XXH3_len_17to128_128b and XXH3_len_129to240_128b (seed 0).
func xxFinishMid(accl, acch uint64, n int) (high, low uint64) {
	low = xxAvalanche(accl + acch)
	h := accl*xxPrime64_1 + acch*xxPrime64_4 + uint64(n)*xxPrime64_2
	high = -xxAvalanche(h) // inv64
	return
}

func xxLen1to3(data []byte) (high, low uint64) {
	n := len(data)
	// xxhash.js:171: `readUInt8(data, len>>1) << 24` is an int32 shift: a byte >= 0x80 makes `combined` a negative
	// BigInt, so its sign extends through `combined ^ blow` into the high 32 bits of the low half (finding W1).
	mid := uint32(data[n>>1])
	combined := uint32(data[n-1]) | uint32(n)<<8 | uint32(data[0])<<16 | mid<<24
	c64 := uint64(combined)
	if mid >= 0x80 {
		c64 |= 0xFFFFFFFF00000000
	}
	blow := rd32(xxSecret[:], 0) ^ rd32(xxSecret[:], 4)
	low = c64 ^ blow
	bhigh := rd32(xxSecret[:], 8) ^ rd32(xxSecret[:], 12)
	swapped := bits.ReverseBytes32(combined)
	high = uint64(bits.RotateLeft32(swapped, 13)) ^ bhigh
	return xxAvalanche64(high), xxAvalanche64(low)
}

func xxLen4to8(data []byte) (high, low uint64) {
	n := len(data)
	l64 := rd32(data, 0) | rd32(data, n-4)<<32
	bitflip := rd64(xxSecret[:], 16) ^ rd64(xxSecret[:], 24)
	keyed := l64 ^ bitflip
	hi, lo := bits.Mul64(keyed, xxPrime64_1+uint64(n)<<2)
	hi += lo << 1
	lo ^= hi >> 3
	lo ^= lo >> 35
	lo *= xxPrimeMX2
	lo ^= lo >> 28
	return xxAvalanche(hi), lo
}

func xxLen9to16(data []byte) (high, low uint64) {
	n := len(data)
	bitflipl := rd64(xxSecret[:], 32) ^ rd64(xxSecret[:], 40)
	bitfliph := rd64(xxSecret[:], 48) ^ rd64(xxSecret[:], 56)
	ll1 := rd64(data, 0)
	ll2 := rd64(data, n-8)
	mh, ml := bits.Mul64(ll1^ll2^bitflipl, xxPrime64_1)
	// xxhash.js:218-219: the (len-1)<<54 addend is not masked to 64 bits before it is OR-ed into the high half, so a
	// carry out of the low half sets bit 0 of the high half instead of being added (finding W2).
	ml, carry := bits.Add64(ml, uint64(n-1)<<54, 0)
	mh |= carry
	ll2 ^= bitfliph
	mh += ll2 + (ll2&0xFFFFFFFF)*(xxPrime32_2-1)
	ml ^= bits.ReverseBytes64(mh)
	hh, hl := bits.Mul64(ml, xxPrime64_2)
	hh += mh * xxPrime64_2
	return xxAvalanche(hh), xxAvalanche(hl)
}

func xxLen17to128(data []byte) (high, low uint64) {
	n := len(data)
	accl, acch := uint64(n)*xxPrime64_1, uint64(0)
	for i := (n - 1) / 32; i >= 0; i-- {
		accl, acch = xxMix32B(accl, acch, data[16*i:], data[n-16*(i+1):], xxSecret[32*i:])
	}
	return xxFinishMid(accl, acch, n)
}

func xxLen129to240(data []byte) (high, low uint64) {
	n := len(data)
	accl, acch := uint64(n)*xxPrime64_1, uint64(0)
	for i := 32; i < 160; i += 32 {
		accl, acch = xxMix32B(accl, acch, data[i-32:], data[i-16:], xxSecret[i-32:])
	}
	accl, acch = xxAvalanche(accl), xxAvalanche(acch)
	for i := 160; i <= n; i += 32 {
		accl, acch = xxMix32B(accl, acch, data[i-32:], data[i-16:], xxSecret[3+i-160:])
	}
	accl, acch = xxMix32B(accl, acch, data[n-16:], data[n-32:], xxSecret[136-17-16:])
	return xxFinishMid(accl, acch, n)
}

func xxAccumulate512(acc *[xxAccNB]uint64, data, key []byte) {
	for i := 0; i < xxAccNB; i++ {
		dv := rd64(data, i*8)
		dk := dv ^ rd64(key, i*8)
		acc[i^1] += dv
		acc[i] += (dk & 0xFFFFFFFF) * (dk >> 32)
	}
}

func xxScrambleAcc(acc *[xxAccNB]uint64, key []byte) {
	for i := 0; i < xxAccNB; i++ {
		a := acc[i]
		a ^= a >> 47
		a ^= rd64(key, i*8)
		acc[i] = a * xxPrime32_1
	}
}

func xxMergeAccs(acc *[xxAccNB]uint64, key []byte, start uint64) uint64 {
	r := start
	for j := 0; j < 4; j++ {
		r += xxMul128Fold64(acc[2*j]^rd64(key, 16*j), acc[2*j+1]^rd64(key, 16*j+8))
	}
	return xxAvalanche(r)
}

func xxHashLong(data []byte) (high, low uint64) {
	sec := xxSecret[:]
	acc := [xxAccNB]uint64{xxPrime32_3, xxPrime64_1, xxPrime64_2, xxPrime64_3, xxPrime64_4, xxPrime32_2, xxPrime64_5, xxPrime32_1}
	n := len(data)
	const stripesPerBlock = (192 - xxStripeLen) / 8
	const blockLen = xxStripeLen * stripesPerBlock
	nbBlocks := (n - 1) / blockLen
	for b := 0; b < nbBlocks; b++ {
		for s := 0; s < stripesPerBlock; s++ {
			xxAccumulate512(&acc, data[b*blockLen+s*xxStripeLen:], sec[s*8:])
		}
		xxScrambleAcc(&acc, sec[192-xxStripeLen:])
	}
	nbStripes := (n - 1 - blockLen*nbBlocks) / xxStripeLen
	for s := 0; s < nbStripes; s++ {
		xxAccumulate512(&acc, data[nbBlocks*blockLen+s*xxStripeLen:], sec[s*8:])
	}
	xxAccumulate512(&acc, data[n-xxStripeLen:], sec[192-xxStripeLen-7:])
	low = xxMergeAccs(&acc, sec[11:], uint64(n)*xxPrime64_1)
	high = xxMergeAccs(&acc, sec[192-xxStripeLen-11:], ^(uint64(n) * xxPrime64_2))
	return
}

// xxh3_128 returns the 128-bit hash as high and low 64-bit halves (JS: `(high << 64) | low`).
func xxh3_128(data []byte) (high, low uint64) {
	n := len(data)
	switch {
	case n == 0:
		low = xxAvalanche64(rd64(xxSecret[:], 64) ^ rd64(xxSecret[:], 72))
		high = xxAvalanche64(rd64(xxSecret[:], 80) ^ rd64(xxSecret[:], 88))
		return
	case n <= 3:
		return xxLen1to3(data)
	case n <= 8:
		return xxLen4to8(data)
	case n <= 16:
		return xxLen9to16(data)
	case n <= 128:
		return xxLen17to128(data)
	case n <= 240:
		return xxLen129to240(data)
	}
	return xxHashLong(data)
}

// xxh128ToBytes is xxhash.js xxh128ToBytes: high64 then low64, both big-endian.
func xxh128ToBytes(high, low uint64) [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[0:], high)
	binary.BigEndian.PutUint64(b[8:], low)
	return b
}
