// Records what the original's bash analysis (command enumeration, wrapper analysis, command effects,
// token collection, arity, sed/awk proofs) answers for a list of inputs, for the Go differential
// (components/permissions/extensions/pi-permission-system/bashanalysis). Copied into an installed copy
// of the original for the run by run.sh and removed after; it never lives in port/oracle.
//
//   DIFF_IN=<json array of strings> DIFF_OUT=<file> vitest run test/zz-bashanalysis-probe.test.ts
//
// One JSON line per input. See record() for the fields; a section that throws records {"throw": message}.
import { closeSync, openSync, readFileSync, writeSync } from "node:fs";
import { it } from "vitest";
import { awkWithdrawsReadClaim } from "#src/access-intent/bash/awk-invocation";
import { prefix, stripBashCommentLines } from "#src/access-intent/bash/bash-arity";
import { proveCommandEffect } from "#src/access-intent/bash/command-effects";
import { inlineShellPayloadNode } from "#src/access-intent/bash/command-enumeration";
import { WordReader } from "#src/access-intent/bash/node-text";
import { getParser, warmBashParser, type TSNode } from "#src/access-intent/bash/parser";
import { REDIRECT_NODE_TYPES } from "#src/access-intent/bash/redirect-analysis";
import { sedWithdrawsReadClaim } from "#src/access-intent/bash/sed-invocation";
import { ShellVariables } from "#src/access-intent/bash/shell-variable-expansion";
import { parseBashCommandsSync } from "#src/access-intent/bash/sync-commands";
import {
  COMMAND_PREFIX_TYPES,
  collectPathCandidateTokens,
  extractCommandName,
  extractCommandWord,
} from "#src/access-intent/bash/token-collection";
import {
  type CommandWord,
  classifyWrapperWords,
  executedUnitOf,
  inlineShellPayloadIndex,
  isTransparentWrapper,
} from "#src/access-intent/bash/wrapper-analysis";
import type { ArgWord } from "#src/access-intent/bash/node-text";

/** wrapper-analysis.test.ts words(): whitespace-split with quoted spans, offsets into the unit. */
function standInWords(unitText: string): CommandWord[] {
  const out: CommandWord[] = [];
  const pattern = /"[^"]*"|'[^']*'|\S+/g;
  let match = pattern.exec(unitText);
  while (match !== null) {
    out.push({ ...argWordOf(match[0]), text: match[0], offset: match.index });
    match = pattern.exec(unitText);
  }
  return out;
}

function argWordOf(text: string): ArgWord {
  const quoted = /^(['"])(.*)\1$/s.exec(text);
  const value = quoted ? quoted[2] : text;
  const computed = quoted?.[1] !== "'" && value.includes("$");
  return { value, computed, mayLeadWithDash: computed || value.startsWith("-") };
}

function attempt<T>(fn: () => T): T | { throw: string } {
  try {
    return fn();
  } catch (e) {
    return { throw: (e as Error).message };
  }
}

function commandNodes(node: TSNode, out: TSNode[] = []): TSNode[] {
  if (node.type === "command") out.push(node);
  for (let i = 0; i < node.childCount; i++) {
    const c = node.child(i);
    if (c) commandNodes(c, out);
  }
  return out;
}

function wrapperFacts(words: CommandWord[], unit: string) {
  return {
    kind: classifyWrapperWords(words) ?? null,
    index: inlineShellPayloadIndex(words),
    executed: executedUnitOf(unit, words),
    transparent: isTransparentWrapper(words, { writesViaRedirect: false }),
    transparentWrites: isTransparentWrapper(words, { writesViaRedirect: true }),
  };
}

async function record(s: string) {
  const parser = await getParser();
  const tree = parser.parse(s);
  const root = tree?.rootNode;
  const rec: Record<string, unknown> = { s };

  rec.sync = attempt(() => parseBashCommandsSync(s));

  if (root) {
    const words = new WordReader(ShellVariables.scan([root]));
    rec.tokens = attempt(() =>
      collectPathCandidateTokens(root, words).map(({ token, effect, role }) => ({ token, effect, role })),
    );
    rec.cmds = attempt(() =>
      commandNodes(root).map((node) => {
        const args: ArgWord[] = [];
        for (let i = 0; i < node.childCount; i++) {
          const child = node.child(i);
          if (!child?.isNamed) continue;
          if (COMMAND_PREFIX_TYPES.has(child.type)) continue;
          if (REDIRECT_NODE_TYPES.has(child.type)) continue;
          args.push(words.argWord(child));
        }
        const head = extractCommandWord(node, words);
        const payload = inlineShellPayloadNode(node, words);
        return {
          name: extractCommandName(node, words) ?? null,
          word: head ?? null,
          payload: payload ? { text: payload.text, start: payload.startIndex } : null,
          args,
          prove: proveCommandEffect(head ?? "", args),
        };
      }),
    );
  } else {
    rec.tokens = null;
    rec.cmds = null;
  }
  tree?.delete();

  const words = standInWords(s);
  rec.words = attempt(() => wrapperFacts(words, s));
  // Every unit the enumeration found, asked the wrapper questions over its own text.
  rec.unitWords = attempt(() =>
    (parseBashCommandsSync(s) ?? []).map((unit) => wrapperFacts(standInWords(unit.text), unit.text)),
  );
  rec.arity = attempt(() => ({ prefix: prefix(s.split(/\s+/)), strip: stripBashCommentLines(s) }));
  const literal = s.split(/\s+/).map((value) => ({ value, computed: false, mayLeadWithDash: value.startsWith("-") }));
  rec.sed = attempt(() => [
    sedWithdrawsReadClaim(words.map(argWordOf2)),
    sedWithdrawsReadClaim(words.slice(1).map(argWordOf2)),
    sedWithdrawsReadClaim(literal),
    sedWithdrawsReadClaim(literal.slice(1)),
  ]);
  rec.awk = attempt(() => [
    awkWithdrawsReadClaim(words.map(argWordOf2)),
    awkWithdrawsReadClaim(words.slice(1).map(argWordOf2)),
    awkWithdrawsReadClaim(literal),
    awkWithdrawsReadClaim(literal.slice(1)),
  ]);
  return rec;
}

function argWordOf2(w: CommandWord): ArgWord {
  return { value: w.value, computed: w.computed, mayLeadWithDash: w.mayLeadWithDash };
}

it("records the original's bash analysis", { timeout: 0 }, async () => {
  const input = process.env.DIFF_IN;
  const output = process.env.DIFF_OUT;
  if (!input || !output) throw new Error("DIFF_IN and DIFF_OUT are required");
  const inputs: string[] = JSON.parse(readFileSync(input, "utf8"));
  await warmBashParser();
  const fd = openSync(output, "w");
  let buf: string[] = [];
  for (const s of inputs) {
    buf.push(`${JSON.stringify(await record(s))}\n`);
    if (buf.length > 200) {
      writeSync(fd, buf.join(""));
      buf = [];
    }
  }
  if (buf.length) writeSync(fd, buf.join(""));
  closeSync(fd);
});
