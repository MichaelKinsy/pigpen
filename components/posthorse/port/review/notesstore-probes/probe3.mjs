import { writeFileSync, mkdirSync, rmSync, statSync, utimesSync } from "node:fs";
const dir = "/home/kinsy/ph-work/scratch/notesstore/p3";
rmSync(dir, { recursive: true, force: true }); mkdirSync(dir, { recursive: true });
let diff = 0, n = 0, nsDiff = 0; const examples = [];
for (let i = 0; i < 3000; i++) {
  const src = `${dir}/s${i}`, dst = `${dir}/d${i}`;
  writeFileSync(src, "x"); writeFileSync(dst, "x");
  const t0 = Date.now(); while (Date.now() - t0 < 1) {}
  const before = statSync(src);
  utimesSync(dst, before.atimeMs / 1000, before.mtimeMs / 1000);
  const after = statSync(dst);
  const sNs = statSync(src, { bigint: true }).mtimeNs, dNs = statSync(dst, { bigint: true }).mtimeNs;
  n++; if (sNs !== dNs) nsDiff++;
  if (after.mtimeMs !== before.mtimeMs) { diff++; if (examples.length < 5) examples.push([String(sNs), String(dNs), before.mtimeMs, after.mtimeMs]); }
}
console.log(JSON.stringify({ n, nsDiff, diff, examples }));
rmSync(dir, { recursive: true, force: true });
