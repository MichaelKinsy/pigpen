// Error-recovery variants of corpus (a): every prefix and every single-character deletion of each
// command (characters are code points, so a variant is always a well-formed JS string).
//   node gen-variants.mjs <corpus.json> <out.json> [maxPrefixChars=400] [maxDeleteChars=200]
// Output: JSON { prefix: string[], del: string[] }, deduplicated against the corpus and each other, in a
// deterministic order.
import fs from "node:fs";

const [corpusPath, outPath, mp = "400", md = "200"] = process.argv.slice(2);
const { a } = JSON.parse(fs.readFileSync(corpusPath, "utf8"));
const seen = new Set(a);
const prefix = [];
const del = [];
for (const s of a) {
  const cps = Array.from(s);
  if (cps.length <= Number(mp)) {
    for (let i = 1; i < cps.length; i++) {
      const v = cps.slice(0, i).join("");
      if (!seen.has(v)) {
        seen.add(v);
        prefix.push(v);
      }
    }
  }
  if (cps.length <= Number(md)) {
    for (let i = 0; i < cps.length; i++) {
      const v = cps.slice(0, i).join("") + cps.slice(i + 1).join("");
      if (!seen.has(v)) {
        seen.add(v);
        del.push(v);
      }
    }
  }
}
fs.writeFileSync(outPath, JSON.stringify({ prefix, del }));
console.log(`prefix=${prefix.length} del=${del.length}`);
