// Differential cases for the TypeBox schemas of note-scopes.ts:9-62 and parseBinding/touchRecords.
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const S = "/home/kinsy/ph-work/scratch/notesstore";
const src = readFileSync(`${S}/oracle/src/note-scopes.ts`, "utf8").split("\n");
// lines 9-62 (1-based) hold the schema definitions verbatim
const schemaText = src.slice(8, 62).join("\n");
writeFileSync(`${S}/oracle/src/schemas-probe.ts`, `import { Type } from "typebox";\n${schemaText}\nexport { ownerSchema, scopeSchema, addressSchema, identitySchema, seedSchema, bindingSchema, touchSchema };\n`);
const schemas = await import(pathToFileURL(`${S}/oracle/src/schemas-probe.ts`).href);
const { Check } = await import(pathToFileURL(`${S}/oracle/node_modules/typebox/build/value/index.mjs`).href).catch(() => import("typebox/value"));
const scopes = await import(pathToFileURL(`${S}/oracle/src/note-scopes.ts`).href);

const ts = "2026-01-02T03:04:05.678Z";
const root = "/p/.pi/notes";
const forkScope = ".forks/2026-01-02T03-04-05-678Z_s1/notes";
const owner = () => ({ id: "s1", timestamp: ts });
const address = (scope = "", path = "a.md") => ({ root, scope, path });
const identity = () => ({ size: 3, mtimeMs: 1700000000123.5, ctimeMs: 1700000000124, ino: 42, dev: 7 });
const seed = () => ({ path: "a.md", source: address(), lineage: address(), anchors: ["e1"], digest: "abc", identity: identity() });
const project = () => ({ version: 1, kind: "project", owner: owner(), root, scope: "" });
const fork = () => ({ version: 1, kind: "fork", owner: owner(), root, scope: forkScope, source: { root, scope: "" }, seeds: [seed()], absent: ["gone.md"] });
const touch = () => ({ version: 1, owner: owner(), scope: { root, scope: forkScope }, rootToolCallId: "r", toolCallId: "c", files: [{ ...address(forkScope, "x.md"), kind: "write", seed: seed() }, { ...address("", "y.md"), kind: "read" }] });
const bases = { owner: owner(), scope: { root, scope: "" }, address: address(), identity: identity(), seed: seed(), project: project(), fork: fork(), touch: touch() };
const schemaOf = { owner: "ownerSchema", scope: "scopeSchema", address: "addressSchema", identity: "identitySchema", seed: "seedSchema", project: "bindingSchema", fork: "bindingSchema", touch: "touchSchema" };
const replacements = [null, 0, 1, 1.5, -0, 2, "", "x", "1", true, false, [], {}, ["a"], [1], { a: 1 }, 1e308, -1e308, 5e-324, 9007199254740993];
const extra = { zzz: 1, __proto__x: "p" };
function paths(v, prefix = []) {
  const out = [];
  if (v !== null && typeof v === "object") {
    for (const k of Object.keys(v)) { out.push([...prefix, k]); out.push(...paths(v[k], [...prefix, k])); }
  }
  return out;
}
const clone = (v) => JSON.parse(JSON.stringify(v));
function setAt(v, p, value, del = false) {
  const c = clone(v); let o = c;
  for (const k of p.slice(0, -1)) o = o[k];
  if (del) { if (Array.isArray(o)) o.splice(Number(p.at(-1)), 1); else delete o[p.at(-1)]; } else o[p.at(-1)] = value;
  return c;
}
const cases = [];
for (const [name, base] of Object.entries(bases)) {
  const variants = [["base", base], ["extra", { ...clone(base), ...extra }], ["array", [clone(base)]], ["null", null], ["string", "x"], ["number", 1]];
  for (const p of paths(base)) {
    variants.push([`del ${p.join(".")}`, setAt(base, p, undefined, true)]);
    for (const r of replacements) variants.push([`${p.join(".")}=${JSON.stringify(r)}`, setAt(base, p, r)]);
    const at = p.reduce((o, k) => o[k], base);
    if (at !== null && typeof at === "object" && !Array.isArray(at)) variants.push([`${p.join(".")}+extra`, setAt(base, p, { ...at, zzz: [1] })]);
  }
  for (const [label, value] of variants) {
    const json = JSON.stringify(value);
    if (json === undefined) continue;
    const parsed = JSON.parse(json);
    const c = { name: `${name}: ${label}`, schema: schemaOf[name], value: parsed, check: Check(schemas[schemaOf[name]], parsed) };
    if (schemaOf[name] === "bindingSchema") { const b = scopes.parseBinding(parsed); c.parse = b !== undefined; if (b !== undefined) c.json = JSON.stringify(b); }
    if (schemaOf[name] === "touchSchema") c.parse = scopes.touchRecords([{ type: "custom", customType: scopes.NOTES_TOUCH, data: parsed }]).length === 1;
    cases.push(c);
  }
}
// semantic cases for parseBinding beyond Check
const sem = [
  ["root relative", { ...project(), root: "p/notes" }], ["root empty", { ...project(), root: "" }], ["id empty", { ...project(), owner: { id: "", timestamp: ts } }],
  ["ts no ms", { ...project(), owner: { id: "s1", timestamp: "2026-01-02T03:04:05Z" } }], ["ts garbage", { ...project(), owner: { id: "s1", timestamp: "garbage" } }],
  ["fork wrong scope", { ...fork(), scope: ".forks/x/notes" }], ["fork id encoded", { ...fork(), owner: { id: "a b/c", timestamp: ts }, scope: ".forks/2026-01-02T03-04-05-678Z_a%20b%2Fc/notes" }],
  ["fork unicode id", { ...fork(), owner: { id: "é😀", timestamp: ts }, scope: ".forks/2026-01-02T03-04-05-678Z_%C3%A9%F0%9F%98%80/notes" }],
  ["fork source null", { ...fork(), source: null }], ["fork source private", { ...fork(), source: { root, scope: forkScope } }], ["fork source bad scope", { ...fork(), source: { root, scope: "x" } }],
  ["fork source rel root", { ...fork(), source: { root: "rel", scope: "" } }],
  ...["a/../b.md", "../a.md", ".forks/a.md", ".forks", "./a.md", "a//b.md", "a/", "/a.md", "a\\..\\b", "a\\b.md", "a\0b", "", ".", "..", " a.md", "a.md ", ".forksx/a.md", "a/.forks/b.md", "\\a.md", "a/./b.md", "é.md"].flatMap((p) => [
    [`absent ${JSON.stringify(p)}`, { ...fork(), absent: [p] }],
    [`seed path ${JSON.stringify(p)}`, { ...fork(), seeds: [{ ...seed(), path: p }] }],
    [`seed source path ${JSON.stringify(p)}`, { ...fork(), seeds: [{ ...seed(), source: address("", p) }] }],
  ]),
  ["seed lineage private", { ...fork(), seeds: [{ ...seed(), lineage: address(forkScope, "a.md") }] }],
  ["seed lineage bad scope", { ...fork(), seeds: [{ ...seed(), lineage: address(".forks/a/b/notes", "a.md") }] }],
  ["scope .forks/x\\y/notes", { ...fork(), source: { root, scope: ".forks/x\\y/notes" } }],
  ["scope .forks//notes", { ...fork(), source: { root, scope: ".forks//notes" } }],
  ["scope .forks/é/notes", { ...fork(), source: { root, scope: ".forks/é/notes" } }],
  ["scope .forks/x/notes\\n", { ...fork(), source: { root, scope: ".forks/x/notes\n" } }],
  ["project scope private", { ...project(), scope: forkScope }],
  ["leaked project in seed source", { ...fork(), seeds: [{ ...seed(), source: { ...project(), path: "a.md" } }] }],
  ["leaked fork in seed lineage", { ...fork(), seeds: [{ ...seed(), lineage: { ...fork(), path: "a.md" } }] }],
  ["leaked broken kind in seed source", { ...fork(), seeds: [{ ...seed(), source: { ...project(), kind: "read", path: "a.md" } }] }],
];
for (const [label, value] of sem) {
  const parsed = JSON.parse(JSON.stringify(value));
  const b = scopes.parseBinding(parsed);
  const c = { name: `semantic: ${label}`, schema: "bindingSchema", value: parsed, check: Check(schemas.bindingSchema, parsed), parse: b !== undefined };
  if (b !== undefined) c.json = JSON.stringify(b);
  cases.push(c);
}
writeFileSync(`${S}/tb-cases.json`, JSON.stringify(cases));
// JS-only facts (not representable in JSON)
const facts = {
  nanIdentity: Check(schemas.identitySchema, { ...identity(), size: NaN }),
  infIdentity: Check(schemas.identitySchema, { ...identity(), size: Infinity }),
  arrayAsObject: Check(schemas.ownerSchema, Object.assign([], owner())),
  undefinedSeed: Check(schemas.touchSchema, { ...touch(), files: [{ ...address(), kind: "read", seed: undefined }] }),
  dateObj: Check(schemas.ownerSchema, { id: "s", timestamp: new String("x") }),
  bigintNum: Check(schemas.identitySchema, { ...identity(), ino: 1n }),
  missingVersionUndefined: Check(schemas.bindingSchema, { ...project(), version: undefined }),
};
console.log(JSON.stringify({ cases: cases.length, accepted: cases.filter((c) => c.check).length, parsed: cases.filter((c) => c.parse).length, facts }));
