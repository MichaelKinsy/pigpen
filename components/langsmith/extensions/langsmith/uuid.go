package langsmith

// Port of utils/_uuid.js and utils/uuid/src/v7.js (langsmith 0.10.5). OWNER: wire lane.

// uuid7FromTime is _uuid.js uuid7FromTime: uuidv7({msecs, seq: 0}) with 16 cryptographically random bytes.
// Bytes 6-9 are therefore fixed (0x70 0x00 0x80 0x00), byte 10 is rnd[10]&3, bytes 11-15 are random.
func uuid7FromTime(epochMs int64) string { panic("unported: uuid.go uuid7FromTime") }

// nonCryptographicUuid7Deterministic is _uuid.js:131-164 (XXH3-128 of `${originalID}:${key}`).
func nonCryptographicUuid7Deterministic(originalID, key string) string {
	panic("unported: uuid.go nonCryptographicUuid7Deterministic")
}

// assertUUID is _uuid.js assertUuid: the relaxed regexp, error text `Invalid UUID: <s>`.
func assertUUID(s string) error { panic("unported: uuid.go assertUUID") }

// uuidVersion is getUuidVersion: the version nibble, or -1 when s is not a UUID.
func uuidVersion(s string) int { panic("unported: uuid.go uuidVersion") }
