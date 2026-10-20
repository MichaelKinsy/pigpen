// Regenerates extensions/warden/testdata/audit-prompt.txt from the vendored ORIGINAL (port/oracle): the prompt
// `/warden audit` sends the session model (audit.ts buildAuditPrompt), for a fixed fixture workspace. The Go test
// builds the same fixture and compares byte for byte. The fixture's directory is replaced by <cwd>. Run from port/oracle:
//   node --import tsx --import ./tests/setup-env.ts ../gen-audit-prompt.ts
// (or through the jiti that Pi ships, as gen-rules-questions.ts).
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import "./oracle/src/guard.ts";
import { buildAuditPrompt } from "./oracle/src/audit.ts";

const root = mkdtempSync(join(tmpdir(), "audit-prompt-"));
const cwd = join(root, "workspace");
const files: Record<string, string> = {
  "package.json": "{}", "index.ts": "x", "README.md": "x", "node_modules/p/skip.js": "x",
  "a/package.json": "{}", "a/one.ts": "x", "a/two.py": "x", "a/sub/three.go": "x",
  "b/Cargo.toml": "[package]", "b/main.rs": "x",
  "c/go.mod": "module c", "c/main.go": "x",
  "d/pyproject.toml": "[project]",
};
for (const [name, body] of Object.entries(files)) {
  mkdirSync(join(cwd, name, ".."), { recursive: true });
  writeFileSync(join(cwd, name), body);
}
const prompt = buildAuditPrompt(cwd, [cwd, join(cwd, "a"), join(cwd, "b"), join(cwd, "c"), join(cwd, "d")]);
writeFileSync(new URL("../extensions/warden/testdata/audit-prompt.txt", import.meta.url), prompt.replaceAll(cwd, "<cwd>"));
rmSync(root, { recursive: true, force: true });
