package langsmith

// Port of utils/_uuid.js and utils/uuid/src/v7.js (langsmith 0.10.5).

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync/atomic"
)

const hexDigits = "0123456789abcdef"

// formatUUID is unsafeStringify: the lower-case 8-4-4-4-12 form of 16 bytes.
func formatUUID(b *[16]byte) string {
	var out [36]byte
	j := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hexDigits[b[i]>>4]
		out[j+1] = hexDigits[b[i]&15]
		j += 2
	}
	return string(out[:])
}

// uuid7FromTime is _uuid.js uuid7FromTime: uuidv7({msecs, seq: 0}) with 16 cryptographically random bytes
// (v7.js:8-9, 41-79). Bytes 6-9 are therefore fixed (0x70 0x00 0x80 0x00), byte 10 is rnd[10]&3, bytes 11-15 are random.
func uuid7FromTime(epochMs int64) string {
	var rnds [16]byte
	rand.Read(rnds[:]) // never fails (Go 1.24+: crashes the program instead)
	var b [16]byte
	// v7.js:56-61 `(msecs / 0x10000000000) & 0xff`: truncating division, then the low byte.
	b[0] = byte(epochMs / 0x10000000000)
	b[1] = byte(epochMs / 0x100000000)
	b[2] = byte(epochMs / 0x1000000)
	b[3] = byte(epochMs / 0x10000)
	b[4] = byte(epochMs / 0x100)
	b[5] = byte(epochMs)
	// seq 0: version nibble 7, sequence bits all zero (v7.js:63-71)
	b[6] = 0x70
	b[7] = 0
	b[8] = 0x80
	b[9] = 0
	b[10] = rnds[10] & 0x03 // ((seq << 2) & 0xff) | (rnds[10] & 0x03)
	copy(b[11:], rnds[11:])
	return formatUUID(&b)
}

// uuidShape reports whether s matches the relaxed UUID_REGEX (_uuid.js:2): 8-4-4-4-12 hex digits, any case.
func uuidShape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

func hexVal(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// assertUUID is _uuid.js assertUuid: the relaxed regexp, error text `Invalid UUID: <s>`.
func assertUUID(s string) error {
	if !uuidShape(s) {
		return errors.New("Invalid UUID: " + s)
	}
	return nil
}

// assertUUIDFor is assertUuid(str, which): error text `Invalid UUID for <which>: <s>`.
func assertUUIDFor(s, which string) error {
	if !uuidShape(s) {
		return errors.New("Invalid UUID for " + which + ": " + s)
	}
	return nil
}

// uuidVersion is getUuidVersion: the version nibble, or -1 when s is not a UUID.
func uuidVersion(s string) int {
	if !uuidShape(s) {
		return -1
	}
	return int(hexVal(s[14]))
}

var uuid7WarningEmitted atomic.Bool

// warnIfNotUUIDv7 is _uuid.js warnIfNotUuidV7: one console.warn for the first non-v7 id seen.
func warnIfNotUUIDv7(s string) {
	v := uuidVersion(s)
	if v != -1 && v != 7 && uuid7WarningEmitted.CompareAndSwap(false, true) {
		fmt.Fprintln(wireWarnWriter, "LangSmith now uses UUID v7 for run and trace identifiers. "+
			"This warning appears when passing custom IDs. "+
			"Please use: import { uuidv7 } from 'langsmith'; const id = uuidv7(); "+
			"Future versions will require UUID v7.")
	}
}

// nonCryptographicUuid7Deterministic is _uuid.js:131-164 (XXH3-128 of `${originalID}:${key}`).
func nonCryptographicUuid7Deterministic(originalID, key string) string {
	var stack [192]byte
	in := append(stack[:0], jsValidUTF8(originalID)...)
	in = append(in, ':')
	in = append(in, jsValidUTF8(key)...)
	hi, lo := xxh3_128(in)
	h := xxh128ToBytes(hi, lo)

	var b [16]byte
	if uuidVersion(originalID) == 7 {
		// uuidToBytes(originalId).slice(0, 6): the first twelve hex digits
		for i := 0; i < 6; i++ {
			p := 2 * i
			if p >= 8 {
				p++
			}
			b[i] = hexVal(originalID[p])<<4 | hexVal(originalID[p+1])
		}
	} else {
		copy(b[:6], h[10:16])
	}
	b[6] = 0x70 | (h[0] & 0x0f)
	b[7] = h[1]
	b[8] = 0x80 | (h[2] & 0x3f)
	copy(b[9:], h[3:10])
	return formatUUID(&b)
}
