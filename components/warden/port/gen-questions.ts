// Regenerates extensions/warden/questions_guard.json (the large-output, slop, security and regret questions of guard.ts)
// and the violation-question fixture from the vendored ORIGINAL (port/oracle):
// node --import tsx --import ./tests/setup-env.ts ../gen-questions.ts   (run from port/oracle after `npm ci`).
import { writeFileSync } from "node:fs";
import { buildRequest, largeOutputQuestion, regretQuestions, securityQuestion, slopQuestions } from "./oracle/src/guard.ts";

const sets: Record<string, unknown> = { largeOutput: largeOutputQuestion, slop: slopQuestions, security: securityQuestion };
const two = regretQuestions([{ id: "a", tool: "bash" }, { id: "b", tool: "bash" }]);
sets.regret = { regretted: two.regretted };
writeFileSync(new URL("../extensions/warden/questions_guard.json", import.meta.url).pathname, JSON.stringify(sets, null, 2) + "\n");

// The violation question is built per instance from a template; the fixture pins its exact text for the Go test.
const violations = [
  { id: "rm-rf", severity: "risky", source: "pattern", description: "recursive removal" },
  { id: "rm-rf", severity: "destructive", source: "rules-guard", description: "no console", matchedRule: "No console.log" },
] as never[];
const request = buildRequest({ tool: "bash", command: "fixture" }, "review", { violations });
const out: Record<string, unknown> = {};
for (const [key, question] of Object.entries(request.questions)) if (key.startsWith("violation_")) out[key] = question;
const regret = regretQuestions([{ id: "a", tool: "bash", command: "rm x" }, { id: "b", tool: "write", path: "p.txt" }, { id: "c", tool: "edit" }]);
writeFileSync(new URL("../extensions/warden/testdata/violation_questions.json", import.meta.url).pathname, JSON.stringify({ violations: out, regret }, null, 2) + "\n");
