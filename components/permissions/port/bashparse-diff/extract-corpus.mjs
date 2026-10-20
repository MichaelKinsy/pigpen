// Corpus (a) and (b) of the bash parse differential. Run from an installed copy of the oracle
// (port/oracle) as: node extract-corpus.mjs <oracle>/packages/pi-permission-system <probe cases.json> <out.json>
// Every string literal and template-literal piece in the original's test tree is a candidate: bash
// commands appear as plain literals, template literals, and pieces of concatenations, so the
// extraction is mechanical and over-inclusive on purpose (a non-bash string is just one more input).
//   a: test/access-intent/bash/** and test/handlers/** (the files the lane names) plus every command of port/probe/cases.json
//   b: every other string in test/**
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";

const [pkg, casesPath, outPath] = process.argv.slice(2);
const ts = createRequire(pkg + "/")("typescript");

function* walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) yield* walk(p);
    else if (/\.(ts|tsx|mts)$/.test(e.name)) yield p;
  }
}

function strings(file) {
  const src = fs.readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true);
  const out = [];
  const visit = (n) => {
    switch (n.kind) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
      case ts.SyntaxKind.TemplateHead:
      case ts.SyntaxKind.TemplateMiddle:
      case ts.SyntaxKind.TemplateTail:
        out.push(n.text);
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

const isA = (f) => /\/test\/(access-intent\/bash|handlers)\//.test(f);
const a = new Set();
const b = new Set();
for (const f of walk(path.join(pkg, "test"))) {
  for (const s of strings(f)) {
    if (s.length === 0 || s.length > 6000) continue;
    (isA(f) ? a : b).add(s);
  }
}
const cases = JSON.parse(fs.readFileSync(casesPath, "utf8"));
for (const c of cases.bash) a.add(c.command);
for (const s of a) b.delete(s);
fs.writeFileSync(outPath, JSON.stringify({ a: [...a].sort(), b: [...b].sort() }));
console.log(`a=${a.size} b=${b.size}`);
