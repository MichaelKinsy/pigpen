// Records the character classes (iswalpha, iswspace) the language's wasm external scanner applies,
// by parsing probe inputs whose tree depends on them (the scanner's libc is not in the grammar's
// sources, so the oracle itself is the ground truth). Copied into the oracle package by
// run-wctype.sh. Output: JSON {alpha: [[lo,hi],...], space: [[lo,hi],...]}.
//
//   alpha: `x<c>=1` is a variable_assignment only when the scanner's VARIABLE_NAME loop accepts
//          <c> as a letter (scanner.c scan, "iswalpha(lexer->lookahead) || '_'").
//   space: in `cat <<A<c>B` the heredoc_start token ends after `A` only when the scanner's
//          advance_word stops at <c> because iswspace(<c>) (scanner.c advance_word).
import { writeFileSync } from "node:fs";
import { it } from "vitest";
import { getGrammarParser } from "#src/access-intent/bash/parser";

it("records iswalpha and iswspace", { timeout: 0 }, async () => {
  const out = process.env.WCTYPE_OUT;
  if (!out) throw new Error("WCTYPE_OUT is required");
  const parser = await getGrammarParser();
  const ranges = (pred: (c: number) => boolean, max: number): [number, number][] => {
    const r: [number, number][] = [];
    let start = -1;
    for (let c = 0; c <= max + 1; c++) {
      const v = c <= max && pred(c);
      if (v && start < 0) start = c;
      if (!v && start >= 0) {
        r.push([start, c - 1]);
        start = -1;
      }
    }
    return r;
  };
  const firstChildren = (s: string): string[] => {
    const t = parser.parse(s);
    if (!t) return ["NULL"];
    const root = t.rootNode;
    const first = root.child(0);
    const names: string[] = [];
    if (first) {
      names.push(first.type);
      for (let i = 0; i < first.childCount; i++) names.push(first.child(i)?.type ?? "?");
    }
    t.delete();
    return names;
  };
  const alpha = (c: number) => firstChildren(`x${String.fromCodePoint(c)}=1`)[0] === "variable_assignment";
  const space = (c: number) => {
    const t = parser.parse(`cat <<A${String.fromCodePoint(c)}B\nx\nA\n`);
    if (!t) return false;
    let found = false;
    const walk = (n: import("#src/access-intent/bash/parser").TSNode) => {
      if (n.type === "heredoc_start") found = n.endIndex - n.startIndex === 1;
      for (let i = 0; i < n.childCount; i++) {
        const k = n.child(i);
        if (k) walk(k);
      }
    };
    walk(t.rootNode);
    t.delete();
    return found;
  };
  const surrogateSafe = (pred: (c: number) => boolean) => (c: number) => {
    // a lone surrogate cannot be a code point string via fromCodePoint on all engines; fromCodePoint accepts it
    return pred(c);
  };
  const result = {
    alpha: ranges(surrogateSafe(alpha), 0x30000),
    space: ranges(surrogateSafe(space), 0x3200),
  };
  writeFileSync(out, JSON.stringify(result));
});
