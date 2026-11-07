// Shared helpers of the wire-*.mjs golden-vector generators (lane LsWire).
// The vectors are produced by the real vendored langsmith 0.10.5 JavaScript; nothing here re-implements it.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const DIST =
  process.env.LS_SDK_DIST ||
  "/home/kinsy/ls-work/oracle-run/node_modules/langsmith/dist";

export const here = path.dirname(fileURLToPath(import.meta.url));
export const testdata = path.resolve(
  here,
  "../../../extensions/langsmith/testdata"
);

export const load = (rel) => import(pathToFileURL(path.join(DIST, rel)).href);

export function writeVectors(name, obj) {
  fs.mkdirSync(testdata, { recursive: true });
  const file = path.join(testdata, name);
  fs.writeFileSync(file, JSON.stringify(obj) + "\n");
  console.log("wrote", file);
}

// xorshift32: the same generator is in the Go tests (wire_vectors_test.go, xorshift32Bytes).
export function xorshift32(seed) {
  let x = seed >>> 0 || 1;
  return () => {
    x ^= x << 13;
    x >>>= 0;
    x ^= x >>> 17;
    x ^= x << 5;
    x >>>= 0;
    return x;
  };
}

// bytes(seed, len): the len bytes `next() >>> 24` of xorshift32(seed).
export function bytes(seed, len) {
  const next = xorshift32(seed);
  const b = new Uint8Array(len);
  for (let i = 0; i < len; i++) b[i] = next() >>> 24;
  return b;
}

export const hex = (u8) => Buffer.from(u8).toString("hex");

// Deterministic uuid-shaped string of a given version nibble.
export function uuidOf(rng, version, upper = false) {
  const b = Array.from({ length: 16 }, () => rng() >>> 24);
  b[6] = (version << 4) | (b[6] & 15);
  b[8] = 0x80 | (b[8] & 0x3f);
  const h = Buffer.from(b).toString("hex");
  const s = `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
  return upper ? s.toUpperCase() : s;
}
