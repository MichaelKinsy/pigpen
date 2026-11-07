// utils/_uuid.js golden vectors (langsmith 0.10.5).
import { load, writeVectors, xorshift32, uuidOf } from "./wire-common.mjs";

const u = await load("utils/_uuid.js");
const rng = xorshift32(7);

const ids = [];
for (const v of [1, 2, 3, 4, 5, 6, 7, 7, 7, 8]) ids.push(uuidOf(rng, v));
ids.push(uuidOf(rng, 7, true), uuidOf(rng, 4, true));
ids.push(
  "00000000-0000-0000-0000-000000000000",
  "ffffffff-ffff-ffff-ffff-ffffffffffff",
  "01234567-89ab-7def-8123-456789abcdef",
  "019a0000-0000-7000-8000-000000000000",
  "0195b2a0-7e41-7c3d-9f00-1a2b3c4d5e6f",
  "not-a-uuid",
  "",
  "019a0000-0000-7000-8000-00000000000g",
  "019a0000-0000-7000-8000-0000000000000",
  "019a0000-0000-7000-8000-000000000000\n",
  " 019a0000-0000-7000-8000-000000000000",
  "019a00000000-7000-8000-0000-000000000000",
  "01935c07-aaaa-7bbb-8ccc-ddddeeeeffff",
  "ÀÉ",
  "日本語"
);
const keys = ["", "project", "my-project", "a:b", "日本語", "😀", "k".repeat(300), "Project\n", "x"];

const deterministic = [];
for (const id of ids) {
  for (const key of keys) {
    deterministic.push({ id, key, out: u.nonCryptographicUuid7Deterministic(id, key) });
  }
}
// Long originalId forces the 129+ byte path of the hash.
const longId = "z".repeat(150);
for (const key of keys) deterministic.push({ id: longId, key, out: u.nonCryptographicUuid7Deterministic(longId, key) });

const versions = ids.map((id) => ({ id, version: u.getUuidVersion(id) }));
const asserts = ids.map((id) => {
  const r = { id };
  try { u.assertUuid(id); r.err = null; } catch (e) { r.err = e.message; }
  try { u.assertUuid(id, "run_id"); r.errFor = null; } catch (e) { r.errFor = e.message; }
  return r;
});

// uuid7FromTime is random in bytes 10..15; keep the JS output, the Go test compares the deterministic prefix
// (24 chars: timestamp, 7000, 8000) and the shape of the rest.
const msecs = [0, 1, 255, 256, 65535, 65536, 1700000000000, 1762387200123, 281474976710655, 4294967295, 4294967296, 1099511627775, 1099511627776];
const time = msecs.map((ms) => ({ ms, out: u.uuid7FromTime(ms) }));
for (const t of time) {
  if (t.out.slice(14, 24) !== "7000-8000-") throw new Error("unexpected v7 layout " + t.out);
}
// String timestamps go through Date.parse.
const timeStr = ["2025-01-02T03:04:05.678Z", "2026-11-06T00:00:00.000Z"].map((s) => ({ s, out: u.uuid7FromTime(s) }));

writeVectors("wire-uuid.json", { deterministic, versions, asserts, time, timeStr });
