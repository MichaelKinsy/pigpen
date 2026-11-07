// XXH3-128 golden vectors from utils/xxhash/xxhash.js (langsmith 0.10.5).
import { load, writeVectors, bytes, hex, xorshift32 } from "./wire-common.mjs";

const { XXH3_128, xxh128ToBytes } = await load("utils/xxhash/xxhash.js");
const h128 = (u8) => hex(xxh128ToBytes(XXH3_128(u8)));

// Every length 0..700 of the xorshift32 stream seeded len+1.
const byLength = [];
for (let n = 0; n <= 700; n++) byLength.push(h128(bytes(n + 1, n)));

// Longer inputs: block boundaries of the long path (1024-byte blocks, 64-byte stripes) and others.
const longLens = [];
for (const base of [1024, 2048, 3072, 4096, 8192, 65536]) {
  for (const d of [-65, -64, -63, -1, 0, 1, 63, 64, 65]) longLens.push(base + d);
}
longLens.push(701, 767, 1000, 1500, 5000, 10000, 20000, 100000);
const long = longLens.map((n) => ({ seed: 0x1000 + n, len: n, hash: h128(bytes(0x1000 + n, n)) }));

// Explicit random inputs, short lengths with high bytes (the 1-3 and 9-16 byte paths are where the JS
// departs from the reference C), plus a few full-range ones. Stored as hex so the Go test needs no generator.
const rng = xorshift32(0xc0ffee);
const random = [];
for (let i = 0; i < 3000; i++) {
  const len = i < 1500 ? 1 + (rng() % 3) : i < 2500 ? 4 + (rng() % 13) : rng() % 300;
  const u8 = new Uint8Array(len);
  for (let j = 0; j < len; j++) u8[j] = rng() >>> 24;
  random.push({ in: hex(u8), hash: h128(u8) });
}
// All single bytes and the 0x80 boundary of the middle byte for lengths 2 and 3.
const exhaustive = [];
for (let b = 0; b < 256; b++) exhaustive.push({ in: hex(Uint8Array.of(b)), hash: h128(Uint8Array.of(b)) });
for (const mid of [0x00, 0x7f, 0x80, 0xff]) {
  for (const a of [0x00, 0x41, 0xff]) {
    for (const c of [0x00, 0x42, 0xff]) {
      for (const u8 of [Uint8Array.of(a, c), Uint8Array.of(a, mid, c)]) {
        exhaustive.push({ in: hex(u8), hash: h128(u8) });
      }
    }
  }
}

// Unicode strings (UTF-8 of the string).
const strings = [
  "", "a", "é", "日本語", "😀", "👨‍👩‍👧‍👦", "İstanbul", "ΑΣ", "Straße", "\u0000", "a\u0000b",
  "The quick brown fox jumps over the lazy dog",
  "日本語のテキスト、これは長い文字列です。".repeat(10),
  "😀".repeat(70),
  "é".repeat(100),
  "\ud800", // lone surrogate -> U+FFFD
  "a\udc00b",
  "x".repeat(129), "x".repeat(240), "x".repeat(241),
];
const enc = new TextEncoder();
const unicode = strings.map((s) => ({ s, hash: h128(enc.encode(s)) }));

writeVectors("wire-xxh3.json", {
  note: "xxhash.js XXH3_128 -> xxh128ToBytes hex; byLength[n] hashes bytes(n+1, n); bytes(seed,len) = len bytes of next()>>>24 of xorshift32(seed)",
  byLength,
  long,
  random,
  exhaustive,
  unicode,
});
