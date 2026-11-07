// utils/sampling.js golden vectors (langsmith 0.10.5).
import { load, writeVectors, xorshift32, uuidOf } from "./wire-common.mjs";

const { isSampledById } = await load("utils/sampling.js");
const { XXH3_128 } = await load("utils/xxhash/xxhash.js");
const enc = new TextEncoder();

const rng = xorshift32(99);
const ids = [];
for (let i = 0; i < 400; i++) ids.push(uuidOf(rng, 7));
for (let i = 0; i < 50; i++) ids.push(uuidOf(rng, 4, true));
ids.push(
  "", "a", "A", "run", "RUN", "Straße", "STRASSE", "İstanbul", "ΑΣ", "ΑΣ ", "ΑΣΑ", "ΑΣ.", "Σ", "ΑΣ'Α", "ΑΣ\u0301", "Ά.Σ",
  "日本語", "ÉCOLE", "😀", "I\u0307", "ǅ", "ẞ", "\ud800", "a\udc00B", "x".repeat(130).toUpperCase(),
  "K\u212A", "\u03a3\u03a3", "ABΣ", "ΑΣΣ"
);
const rates = [0, 0.1, 0.5, 0.9999, 1, 0.25, 0.01, 0.000001, 0.999999, 1e-9, 2, -1, 0.3333333333333333];

const cases = ids.map((id) => {
  const bucket = Number(XXH3_128(enc.encode(id.toLowerCase())) % 1000000n);
  const bits = rates.map((r) => (isSampledById(id, r) ? "1" : "0")).join("");
  return { id, bucket, bits };
});
writeVectors("wire-sampling.json", {
  rates,
  undefinedRate: ids.slice(0, 5).map((id) => isSampledById(id, undefined)),
  nanRate: ids.slice(0, 5).map((id) => isSampledById(id, NaN)),
  cases,
});
