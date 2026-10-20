// Dumps what the original's bash parse gives for a list of inputs, node by node, for the Go differential
// (components/permissions/extensions/pi-permission-system/bashparse). Copied into an installed copy of the
// original for the run by run.sh and removed after; it never lives in port/oracle.
//
//   DIFF_IN=<json array of strings> DIFF_OUT=<file> DIFF_MODE=full|hash vitest run test/zz-bashparse-dump.test.ts
//
// full: one JSON line per input {"s":input,"g":grammarDump,"p":correctedDump}
// hash: one line per input, FNV-1a/32 of g + "\u0001" + p in hex (8 chars)
//
// A dump is one line per node in pre-order: depth|type|flags|start|end|childCount|field|prev
//   type, escaped: printable ASCII except "\" and "|" as is, every other UTF-16 unit as \uXXXX
//   flags: N named, E hasError, M missing, X extra ("-" for none); the corrected tree (getParser) only has N and E
//   start,end: startIndex/endIndex (UTF-16 code units, as web-tree-sitter reports them)
//   field: the parent's field name for this child ("-" for none, and always "-" in the corrected tree)
//   prev: previousSibling as type@start-end, or "-"
import { readFileSync, writeSync, openSync, closeSync } from "node:fs";
import { it } from "vitest";
import { getGrammarParser, getParser, type TSNode } from "#src/access-intent/bash/parser";

const esc = (s: string): string => {
  let o = "";
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    o += c >= 0x21 && c <= 0x7e && c !== 0x5c && c !== 0x7c ? s[i] : "\\u" + c.toString(16).padStart(4, "0");
  }
  return o;
};

// biome-ignore lint/suspicious/noExplicitAny: web-tree-sitter's Node is only reached through TSNode's members here
type Raw = any;

function dump(root: TSNode, grammar: boolean): string {
  const lines: string[] = [];
  const walk = (n: TSNode, depth: number, field: string | null): void => {
    let flags = (n.isNamed ? "N" : "") + (n.hasError ? "E" : "");
    if (grammar) flags += ((n as Raw).isMissing ? "M" : "") + ((n as Raw).isExtra ? "X" : "");
    const prev = n.previousSibling;
    lines.push(
      [
        depth,
        esc(n.type),
        flags || "-",
        n.startIndex,
        n.endIndex,
        n.childCount,
        grammar && field ? esc(field) : "-",
        prev ? `${esc(prev.type)}@${prev.startIndex}-${prev.endIndex}` : "-",
      ].join("|"),
    );
    for (let i = 0; i < n.childCount; i++) {
      const c = n.child(i);
      if (c) walk(c, depth + 1, grammar ? ((n as Raw).fieldNameForChild(i) as string | null) : null);
    }
  };
  walk(root, 0, null);
  return lines.join("\n");
}

function fnv(s: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, "0");
}

it("dumps the original's bash parse trees", { timeout: 0 }, async () => {
  const input = process.env.DIFF_IN;
  const output = process.env.DIFF_OUT;
  const mode = process.env.DIFF_MODE ?? "full";
  if (!input || !output) throw new Error("DIFF_IN and DIFF_OUT are required");
  const inputs: string[] = JSON.parse(readFileSync(input, "utf8"));
  const grammar = await getGrammarParser();
  const corrected = await getParser();
  const fd = openSync(output, "w");
  let buf: string[] = [];
  const flush = () => {
    if (buf.length) writeSync(fd, buf.join(""));
    buf = [];
  };
  for (const s of inputs) {
    let g: string;
    let p: string;
    try {
      const gt = grammar.parse(s);
      g = gt ? dump(gt.rootNode, true) : "NULL";
      gt?.delete();
      const pt = corrected.parse(s);
      p = pt ? dump(pt.rootNode, false) : "NULL";
      pt?.delete();
    } catch (e) {
      g = p = `THROW ${(e as Error).message}`;
    }
    buf.push(mode === "hash" ? `${fnv(`${g}\u0001${p}`)}\n` : `${JSON.stringify({ s, g, p })}\n`);
    if (buf.length > 500) flush();
  }
  flush();
  closeSync(fd);
});
