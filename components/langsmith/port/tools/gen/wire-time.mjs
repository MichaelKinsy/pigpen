// run_trees.js getMicrosecondPrecisionDatestring / convertToDottedOrderFormat golden vectors (langsmith 0.10.5).
// Those two are not exported; convertToDottedOrderFormat is, and returns both strings.
import { load, writeVectors, xorshift32, uuidOf } from "./wire-common.mjs";

const { convertToDottedOrderFormat } = await load("run_trees.js");
const rng = xorshift32(2026);

const epochs = [
  0, 1, 999, 1000, -1, -1000, -86400000, -62167219200000, -62167219200001, -62198755200000, 951782400000, 951868800000,
  4107542400000, 253402300799999, 253402300800000, 253402300800001, 8640000000000000, -8640000000000000,
  1700000000000, 1762387200000, 1762387200123, 1735689599999, 1709164800000, 1709251199999,
];
for (let i = 0; i < 300; i++) epochs.push(Math.floor((rng() / 2 ** 32) * 4e12) + 1e12);
for (let i = 0; i < 40; i++) epochs.push(Math.floor((rng() / 2 ** 32) * 2e13) - 1e13);
const orders = [1, 0, 2, 9, 10, 99, 100, 101, 999, 1000, 1234, 12345, 99999, -1, -12, -123, -1234, 1e6];
const runId = "019a0000-1111-7222-8333-444455556666";

const cases = [];
for (let i = 0; i < epochs.length; i++) {
  const epoch = epochs[i];
  const ords = i < 40 ? orders : [orders[i % orders.length]];
  for (const order of ords) {
    try {
      const r = convertToDottedOrderFormat(epoch, runId, order);
      cases.push({ epoch, order, ds: r.microsecondPrecisionDatestring, dotted: r.dottedOrder });
    } catch (e) {
      cases.push({ epoch, order, error: e.name + ": " + e.message });
    }
  }
}
for (const epoch of [8640000000000001, -8640000000000001, 1e16]) {
  try {
    convertToDottedOrderFormat(epoch, runId, 1);
    throw new Error("expected RangeError");
  } catch (e) {
    cases.push({ epoch, order: 1, error: e.name + ": " + e.message });
  }
}
// ISO strings of the same epochs.
const iso = epochs.filter((e) => Math.abs(e) <= 8.64e15).map((e) => ({ epoch: e, iso: new Date(e).toISOString() }));
writeVectors("wire-time.json", { runId, cases, iso });
