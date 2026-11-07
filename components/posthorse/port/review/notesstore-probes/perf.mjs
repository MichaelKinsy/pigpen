// Fork-of-fork binding: the grandchild's seeds carry the leaked parent-fork binding (resolveReference's
// `{ ...current, path }` with current = the parent's binding from sourceScope).
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const S_ = "/home/kinsy/ph-work/scratch/notesstore";
const scopes = await import(pathToFileURL(`${S_}/oracle/src/note-scopes.ts`).href);
const n = Number(process.argv[2] ?? 100);
const root = "/p/.pi/notes";
const ident = { size: 3, mtimeMs: 1700000000123.5, ctimeMs: 1700000000124, ino: 42, dev: 7 };
const project = { version: 1, kind: "project", owner: { id: "p0", timestamp: "2026-01-01T00:00:00.000Z" }, root, scope: "" };
const mk = (id, ts, source, seedSource) => {
  const owner = { id, timestamp: ts };
  const scope = scopes.forkScope(owner);
  const seeds = Array.from({ length: n }, (_, i) => ({ path: `n${i}.md`, source: { ...seedSource, path: `n${i}.md` }, lineage: { ...project, path: `n${i}.md` }, anchors: [`e${i}`], digest: "d".repeat(64), identity: ident }));
  return { version: 1, kind: "fork", owner, root, scope, source, seeds, absent: [] };
};
const child = mk("c1", "2026-01-02T00:00:00.000Z", { root, scope: "" }, project);
const grand = mk("g1", "2026-01-03T00:00:00.000Z", { root, scope: child.scope }, child);
const text = JSON.stringify(grand);
writeFileSync(`${S_}/grand-${n}.json`, text);
const value = JSON.parse(text);
let ok = 0; const iters = 200;
for (let i = 0; i < 20; i++) ok += scopes.parseBinding(value) ? 1 : 0;
const t0 = process.hrtime.bigint();
for (let i = 0; i < iters; i++) ok += scopes.parseBinding(value) ? 1 : 0;
const ns = Number(process.hrtime.bigint() - t0) / iters;
console.log(JSON.stringify({ n, bytes: text.length, accepted: ok, tsParseBindingNsPerOp: Math.round(ns) }));
