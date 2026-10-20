#!/bin/sh
# Records, from the original's own scripted-workflow tests, the exact workflowScript text (and args) each test hands to
# runWorkflowScript, keyed by test title, into extensions/pi-subagents/testdata/workflow-scripts.json. The Go twins read long
# scripts from it instead of retyping them. Needs a copy of port/oracle with `npm install acorn@8.18.0` done in it:
#   WFS_ORACLE=/tmp/oracle sh port/record-workflow-scripts.sh
set -e
: "${WFS_ORACLE:?set WFS_ORACLE to a prepared copy of port/oracle}"
out="$(cd "$(dirname "$0")/.." && pwd)/extensions/pi-subagents/testdata/workflow-scripts.json"
cd "$WFS_ORACLE"
python3 - <<'PY'
src = open("test/unit/scripted-workflow.test.ts").read()
src = src.replace('import { describe, it } from "node:test";', 'import { describe, it as realIt } from "node:test";\nimport { recordScript } from "./recorder.ts";\nconst it: any = (title: string, ...rest: any[]) => { const fn = rest[rest.length - 1]; rest[rest.length - 1] = async (...a: any[]) => { (globalThis as any).__title = title; return fn(...a); }; return (realIt as any)(title, ...rest); };')
src = src.replace("runWorkflowScript, validateWorkflowScript,", "runWorkflowScript as realRun, validateWorkflowScript,", 1)
src = src.replace('describe("scripted workflow runtime", () => {', 'const runWorkflowScript: typeof realRun = (opts) => { recordScript(opts.script, opts.args); return realRun(opts); };\ndescribe("scripted workflow runtime", () => {', 1)
open("test/unit/scripted-workflow.rec.test.ts", "w").write(src)
open("test/unit/recorder.ts", "w").write('''import fs from "node:fs";
const out: Record<string, Array<{ script: string; args?: unknown }>> = ((globalThis as any).__scripts ??= {});
export function recordScript(script: string, args?: unknown): void {
  const title = (globalThis as any).__title ?? "?";
  (out[title] ??= []).push({ script, ...(args !== undefined ? { args } : {}) });
  fs.writeFileSync(process.env.REC_OUT!, JSON.stringify(out, null, 1));
}
''')
PY
REC_OUT="$out" node --experimental-strip-types --import ./test/support/isolated-temp-root.mjs --test test/unit/scripted-workflow.rec.test.ts
rm -f test/unit/scripted-workflow.rec.test.ts test/unit/recorder.ts
