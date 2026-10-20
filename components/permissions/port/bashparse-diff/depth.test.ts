// Probe: how deep can a construct nest before the original's wasm parse (or its tree walk) traps?
// Copied into the oracle package by run-depth.sh; prints one line per construct.
import { it } from "vitest";
import { getGrammarParser, getParser, type TSNode } from "#src/access-intent/bash/parser";

const forms: Record<string, (n: number) => string> = {
  subshell: (n) => "(".repeat(n) + "ls" + ")".repeat(n),
  cmdsub: (n) => "$(".repeat(n) + "ls" + ")".repeat(n),
  quotedsub: (n) => 'echo ' + '"$('.repeat(n) + "ls" + ')"'.repeat(n),
  brace: (n) => "{ ".repeat(n) + "ls" + "; }".repeat(n),
  if: (n) => "if a; then ".repeat(n) + "ls" + "; fi".repeat(n),
  arith: (n) => "echo $((" + "(".repeat(n) + "1" + ")".repeat(n) + "))",
  pipeline: (n) => Array.from({ length: n }, () => "a").join(" | "),
  list: (n) => Array.from({ length: n }, () => "a").join(" && "),
  semis: (n) => "a;".repeat(n),
  words: (n) => "echo " + "a ".repeat(n),
  parenOnly: (n) => "(".repeat(n),
  procsub: (n) => "<(".repeat(n) + "ls" + ")".repeat(n),
};

function walkDepth(n: TSNode, d: number): number {
  let m = d;
  for (let i = 0; i < n.childCount; i++) {
    const c = n.child(i);
    if (c) m = Math.max(m, walkDepth(c, d + 1));
  }
  return m;
}

it("probes nesting limits", { timeout: 0 }, async () => {
  const g = await getGrammarParser();
  const c = await getParser();
  for (const [name, f] of Object.entries(forms)) {
    let parseFail = -1;
    let walkFail = -1;
    let correctFail = -1;
    for (const n of [1, 2, 5, 10, 20, 40, 60, 80, 100, 150, 200, 250, 300, 400, 500, 600, 700, 800, 900, 1000, 1500, 2000, 4000, 8000]) {
      const s = f(n);
      try {
        const t = g.parse(s);
        try {
          if (t) walkDepth(t.rootNode, 0);
        } catch {
          if (walkFail < 0) walkFail = n;
        }
        t?.delete();
      } catch {
        if (parseFail < 0) parseFail = n;
      }
      try {
        c.parse(s)?.delete();
      } catch {
        if (correctFail < 0) correctFail = n;
      }
    }
    console.log(`DEPTH ${name}: grammar-parse-throws-from=${parseFail} walk-throws-from=${walkFail} corrected-throws-from=${correctFail}`);
  }
});
