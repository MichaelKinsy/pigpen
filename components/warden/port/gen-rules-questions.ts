// Regenerates extensions/warden/rules_questions.json from the vendored ORIGINAL (port/oracle): the question texts of the
// rules guard (rules.ts), the end-of-run pass (turn-rules.ts) and the rules check (rules-lint.ts), verbatim. The Go port
// embeds the file so a paraphrase cannot drift the judge's answers. Run from port/oracle after `npm ci`:
//   node --import tsx --import ./tests/setup-env.ts ../gen-rules-questions.ts
// or, without tsx, through the jiti that Pi ships: node <pi>/node_modules/jiti/lib/jiti-cli.mjs ../gen-rules-questions.ts
import { writeFileSync } from "node:fs";
// The original's modules import each other in a cycle (rules, widget, done, guard, rules-file); entering through guard
// is the order its own entry point uses, so a transpiler that loads them in sequence finds every export defined.
import "./oracle/src/guard.ts";
import { buildRulesRequest, ruleQuestion, OUTCOMES } from "./oracle/src/rules.ts";
import { turnRuleQuestion } from "./oracle/src/turn-rules.ts";
import { CHECK_ABOUT, judgeableQuestion, mechanicalQuestion } from "./oracle/src/rules-lint.ts";

const NAME = "\u0001NAME\u0001";
const BODY = "\u0001BODY\u0001";
const ID = "\u0001ID\u0001";
const TAIL = `\nRule: ${NAME}\n${BODY}`;

/** A template with the rule's id, name and body marked, so the Go side fills them in one pass. */
function template(text: string): string {
  return text.replaceAll(NAME, "{name}").replaceAll(BODY, "{body}").replaceAll(ID, "{id}");
}

/** The frame in front of the rule text of a per-rule question; fails when the question no longer ends with it. */
function frameOf(instructions: string): string {
  if (!instructions.endsWith(TAIL)) throw new Error("a rule question no longer ends with its rule text");
  return instructions.slice(0, instructions.length - TAIL.length);
}

const rule = { id: ID, name: NAME, body: BODY, paths: [] as string[] };
const perEdit = ruleQuestion(rule);
const perTurn = turnRuleQuestion(rule);
const judgeable = judgeableQuestion({ id: ID, name: NAME });
const mechanical = mechanicalQuestion({ id: ID, name: NAME });

// The aggregate and locator questions only exist inside a request.
const target = { tool: "edit" as const, path: "p", edits: [{ id: "edit_1", newText: "a" }, { id: "edit_2", newText: "b" }] };
const inRequest = buildRulesRequest(target, { sources: ["x"], rules: [], aggregate: "x", alwaysDropped: 0 }).questions;

const out = {
  outcomes: OUTCOMES,
  rule: { frame: frameOf(perEdit.instructions) },
  turn: { frame: frameOf(perTurn.instructions) },
  aggregate: inRequest.rules,
  locator: inRequest.which_edit,
  check: {
    about: CHECK_ABOUT,
    judgeable: { ...judgeable, instructions: template(judgeable.instructions) },
    mechanical: { ...mechanical, instructions: template(mechanical.instructions) },
  },
};
writeFileSync(new URL("../extensions/warden/rules_questions.json", import.meta.url), `${JSON.stringify(out, null, 1)}\n`);
