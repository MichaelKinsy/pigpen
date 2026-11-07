// utils/env.js golden vectors (langsmith 0.10.5): each scenario runs the real module in a child node process whose
// environment is exactly the scenario's (in order), then records what the module returned.
import { spawnSync } from "node:child_process";
import { DIST, load, writeVectors } from "./wire-common.mjs";
import { pathToFileURL } from "node:url";
import path from "node:path";

const envUrl = pathToFileURL(path.join(DIST, "utils/env.js")).href;
const child = `
const m = await import(${JSON.stringify(envUrl)});
const out = {
  order: Object.keys(process.env),
  metadata: (() => { try { return m.getLangSmithEnvVarsMetadata(); } catch (e) { return { __error: String(e) }; } })(),
  vars: m.getLangSmithEnvironmentVariables(),
  runtime: m.getRuntimeEnvironment(),
  shas: m.getShas(),
  lsVar: Object.fromEntries(["API_KEY", "ENDPOINT", "PROJECT", "X", "TRACING_MODE", "OTEL_ENABLED"].map((n) => [n, m.getLangSmithEnvironmentVariable(n) ?? null])),
  otel: m.getOtelEnabled(),
};
let mode; try { mode = { ok: m.resolveTracingMode(undefined) }; } catch (e) { mode = { error: e.message }; }
out.mode = mode;
process.stdout.write(JSON.stringify(out));
`;

// [name, ordered env pairs]
const scenarios = [
  ["empty", []],
  ["basic", [["LANGSMITH_TRACING", "true"], ["LANGSMITH_API_KEY", "lsv2_pt_abcdefghijkl"], ["LANGSMITH_PROJECT", "p"], ["LANGSMITH_ENDPOINT", "https://x"], ["LANGCHAIN_PROJECT", "q"]]],
  ["pi-vars", [["LANGSMITH_PI_PROJECT", "pi"], ["LANGSMITH_PI_ENDPOINT", "http://127.0.0.1:1"], ["LANGSMITH_PI_API_KEY", "secret-value"], ["LANGSMITH_PI_DEBUG", "1"]]],
  ["revision", [["LANGCHAIN_REVISION_ID", "abc123"], ["LANGSMITH_REVISION_ID", "def"], ["LANGCHAIN_TRACING_V2", "true"], ["LANGSMITH_TRACING_V2", "true"], ["LANGSMITH_CONFIG_FILE", "/x"], ["LANGSMITH_SESSION", "s"], ["LANGCHAIN_SESSION", "s"], ["LANGCHAIN_ENDPOINT", "e"], ["LANGCHAIN_API_KEY", "abcd"]]],
  ["sensitive-names", [["LANGSMITH_A_SECRET", "abcdef"], ["LANGSMITH_B_TOKEN", "tok1234"], ["LANGSMITH_PASSWORD_X", "pw123"], ["LANGSMITH_PASSWD", "pw123"], ["LANGSMITH_X_PWD", "pw123"], ["LANGSMITH_CREDENTIALS", "cred123"], ["LANGSMITH_USER_EMAIL", "a@b.c"], ["LANGSMITH_Key", "kkkkk"], ["LANGSMITH_MONKEY_X", "vvvvv"], ["LANGSMITH_OK", "fine"], ["LANGSMITH_Ok_2", "fine2"]]],
  ["short-secret-4", [["LANGSMITH_A", "1"], ["LANGSMITH_API_KEY", "abcd"], ["LANGSMITH_B", "2"]]],
  ["short-secret-3-aborts", [["LANGSMITH_A", "1"], ["LANGSMITH_API_KEY", "abc"], ["LANGSMITH_B", "2"]]],
  ["empty-secret-aborts", [["LANGSMITH_A", "1"], ["LANGCHAIN_SOME_TOKEN", ""], ["LANGSMITH_B", "2"], ["LANGSMITH_C", "3"]]],
  ["abort-first", [["LANGSMITH_API_KEY", ""], ["LANGSMITH_B", "2"]]],
  ["utf16-secret", [["LANGSMITH_A", "1"], ["LANGSMITH_X_KEY", "😀😀"], ["LANGSMITH_B", "2"]]],
  ["utf16-secret-short", [["LANGSMITH_A", "1"], ["LANGSMITH_X_KEY", "😀"], ["LANGSMITH_B", "2"]]],
  ["utf16-secret-3", [["LANGSMITH_A", "1"], ["LANGSMITH_X_KEY", "é😀"], ["LANGSMITH_B", "2"]]],
  ["unicode-values", [["LANGSMITH_NAME", "日本語 😀"], ["LANGCHAIN_NOTE", "é"], ["LANGSMITH_EMPTY", ""]]],
  ["other-prefix", [["langsmith_lower", "x"], ["LANGSMITHX", "y"], ["LANGSMITH", "z"], ["LANGSMITH_", "u"], ["XLANGSMITH_A", "w"], ["LANGCHAIN_", "v"], ["PATH_LANGSMITH_A", "p"]]],
  ["order", [["LANGSMITH_Z", "1"], ["LANGCHAIN_A", "2"], ["LANGSMITH_A", "3"], ["LANGCHAIN_REVISION_ID", "4"], ["LANGSMITH_M", "5"]]],
  ["shas-all", [["VERCEL_GIT_COMMIT_SHA", "v"], ["NEXT_PUBLIC_VERCEL_GIT_COMMIT_SHA", "nv"], ["COMMIT_REF", "cr"], ["RENDER_GIT_COMMIT", "rg"], ["CI_COMMIT_SHA", "ci"], ["CIRCLE_SHA1", "cc"], ["CF_PAGES_COMMIT_SHA", "cf"], ["REACT_APP_GIT_SHA", "ra"], ["SOURCE_VERSION", "sv"], ["GITHUB_SHA", "gh"], ["TRAVIS_COMMIT", "tr"], ["GIT_COMMIT", "gc"], ["BUILD_VCS_NUMBER", "bv"], ["bamboo_planRepository_revision", "bb"], ["Build.SourceVersion", "bs"], ["BITBUCKET_COMMIT", "bc"], ["DRONE_COMMIT_SHA", "dr"], ["SEMAPHORE_GIT_SHA", "se"], ["BUILDKITE_COMMIT", "bk"]]],
  ["shas-some", [["GITHUB_SHA", "gh"], ["GIT_COMMIT", ""], ["CIRCLE_SHA1", "cc"], ["LANGSMITH_A", "1"]]],
  ["lsvar-fallthrough", [["LANGSMITH_API_KEY", ""], ["LANGCHAIN_API_KEY", "lc-key"], ["LANGSMITH_ENDPOINT", "ls-ep"], ["LANGCHAIN_ENDPOINT", "lc-ep"], ["LANGCHAIN_PROJECT", "lcp"], ["LANGSMITH_PROJECT", ""]]],
  ["lsvar-empty-both", [["LANGSMITH_X", ""], ["LANGCHAIN_X", ""]]],
  ["lsvar-empty-first", [["LANGSMITH_X", ""]]],
  ["mode-otel", [["LANGSMITH_TRACING_MODE", "otel"]]],
  ["mode-OTEL-upper", [["LANGSMITH_TRACING_MODE", "OTEL"]]],
  ["mode-langsmith-with-legacy", [["LANGSMITH_TRACING_MODE", "LangSmith"], ["OTEL_ENABLED", "true"]]],
  ["mode-invalid", [["LANGSMITH_TRACING_MODE", "bogus\"x"]]],
  ["mode-legacy", [["LANGSMITH_OTEL_ENABLED", "true"]]],
  ["mode-legacy-lc", [["LANGCHAIN_OTEL_ENABLED", "true"]]],
  ["mode-legacy-bare", [["OTEL_ENABLED", "true"]]],
  ["mode-legacy-false", [["OTEL_ENABLED", "false"], ["LANGSMITH_OTEL_ENABLED", "TRUE"]]],
  ["mode-empty", [["LANGSMITH_TRACING_MODE", ""], ["LANGCHAIN_TRACING_MODE", "otel"]]],
  ["mode-lc", [["LANGCHAIN_TRACING_MODE", "otel"]]],
];

const base = { PATH: process.env.PATH };
const results = [];
for (const [name, pairs] of scenarios) {
  const env = {};
  for (const [k, v] of pairs) env[k] = v;
  const r = spawnSync(process.execPath, ["--input-type=module", "-e", child], { env, encoding: "utf8" });
  if (r.status !== 0) throw new Error(`scenario ${name}: ${r.stderr}`);
  const out = JSON.parse(r.stdout);
  const keys = pairs.map(([k]) => k);
  if (JSON.stringify(out.order) !== JSON.stringify([...new Set(keys)])) {
    throw new Error(`scenario ${name}: child env order ${JSON.stringify(out.order)} != ${JSON.stringify(keys)}`);
  }
  delete out.order;
  results.push({ name, env: pairs.map(([k, v]) => `${k}=${v}`), stderr: r.stderr, ...out });
}
// Key order matters for the Go side: carry it explicitly (JSON object order is preserved by encoding the pairs).
for (const r of results) {
  for (const f of ["metadata", "vars", "runtime", "shas"]) r[f] = Object.entries(r[f]);
}
writeVectors("wire-env.json", { sdk_version: (await load("index.js")).__version__, scenarios: results });
