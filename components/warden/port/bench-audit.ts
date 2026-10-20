// Times the ORIGINAL's audit work (findProjects + buildAuditPrompt) over the same workspace shape as
// extensions/warden/audit_bench_test.go (5 projects, 60 source files each). Run from port/oracle with Pi's jiti:
//   node --import ./tests/setup-env.ts <pi>/node_modules/jiti/lib/jiti-cli.mjs ../bench-audit.ts
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import "./oracle/src/guard.ts";
import { buildAuditPrompt } from "./oracle/src/audit.ts";
import { findProjects } from "./oracle/src/discover.ts";

const root = join(mkdtempSync(join(tmpdir(), "audit-bench-")), "workspace");
const files: Record<string, string> = { "package.json": "{}" };
const projects = [root];
for (let p = 0; p < 4; p++) {
  const name = `p${p}`;
  files[`${name}/package.json`] = "{}";
  projects.push(join(root, name));
  for (let f = 0; f < 60; f++) files[`${name}/src/d${f % 5}/f${f}.ts`] = "x";
}
for (let f = 0; f < 60; f++) files[`src/f${f}.go`] = "x";
for (const [rel, body] of Object.entries(files)) {
  mkdirSync(join(root, rel, ".."), { recursive: true });
  writeFileSync(join(root, rel), body);
}
const median = (fn: () => void, n = 200): number => {
  const t: number[] = [];
  for (let i = 0; i < n + 20; i++) { const s = process.hrtime.bigint(); fn(); if (i >= 20) t.push(Number(process.hrtime.bigint() - s)); }
  return t.sort((a, b) => a - b)[Math.floor(n / 2)]!;
};
console.log(`buildAuditPrompt median ns/op ${median(() => { buildAuditPrompt(root, projects); })}`);
console.log(`findProjects median ns/op ${median(() => { findProjects(root); })}`);
rmSync(join(root, ".."), { recursive: true, force: true });
