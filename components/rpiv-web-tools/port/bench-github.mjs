// Latency of the original's GitHub interceptor with a canned child_process.execFile, for comparison with
// extensions/rpiv-web-tools/github_bench_test.go (same inputs, a canned runner, no process started).
//   node port/bench-github.mjs [reps]   (run where /opt/oracle holds Pi's node_modules, from components/rpiv-web-tools)
import cp from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const jitiMod = require(process.env.JITI ?? "/opt/oracle/pi-src/node_modules/jiti");
const jiti = (jitiMod.createJiti ?? jitiMod)(import.meta.url, { moduleCache: false, interopDefault: true });

cp.execFile = (...args) => {
	const cmdArgs = args[1];
	const cb = args[args.length - 1];
	if (cmdArgs[0] === "--version") return cb(null, "gh version 2");
	const jq = cmdArgs[cmdArgs.length - 1];
	if (jq === ".size") return cb(null, "400000\n");
	if (jq === ".default_branch") return cb(null, "main\n");
	if (jq === ".tree[].path") return cb(null, Array.from({ length: 300 }, (_, i) => `src/file${String(i).padStart(3, "0")}.go\n`).join(""));
	return cb(null, Buffer.from("# Readme line\n".repeat(200)).toString("base64"));
};

const { GitHubInterceptor, parseGitHubUrl } = jiti(resolve(here, "oracle/providers/interceptors/github.ts"));

const dir = mkdtempSync(join(tmpdir(), "rpiv-bench-"));
for (let i = 0; i < 100; i++) {
	const d = join(dir, `d${String(i % 10).padStart(2, "0")}`);
	mkdirSync(d, { recursive: true });
	writeFileSync(join(d, `f${String(i).padStart(3, "0")}.go`), "package x\n");
}
writeFileSync(join(dir, "README.md"), "# Readme line\n".repeat(200));
writeFileSync(join(dir, "big.txt"), "line of text\n".repeat(10000));

const api = new GitHubInterceptor({ enabled: true });
const clone = new GitHubInterceptor({ enabled: true });
clone._seedCache("owner/repo", { localPath: dir, clonePromise: Promise.resolve(dir) });
clone._seedCache("owner/repo@main", { localPath: dir, clonePromise: Promise.resolve(dir) });

const cases = {
	GitHubParseURL: [2_000_000, async () => parseGitHubUrl("https://github.com/owner/repo/blob/main/src/pkg/file.go")],
	GitHubAPIView: [20_000, () => api.intercept("https://github.com/owner/repo", { raw: false })],
	GitHubCloneRoot: [2_000, () => clone.intercept("https://github.com/owner/repo", { raw: false })],
	GitHubCloneBlob: [2_000, () => clone.intercept("https://github.com/owner/repo/blob/main/big.txt", { raw: false })],
};
const reps = Number(process.argv[2] ?? 10);
for (let r = 0; r < reps; r++) {
	for (const [name, [n, fn]] of Object.entries(cases)) {
		for (let i = 0; i < n / 10; i++) await fn();
		const t0 = process.hrtime.bigint();
		for (let i = 0; i < n; i++) await fn();
		const ns = Number(process.hrtime.bigint() - t0) / n;
		console.log(`node ${name} ${ns.toFixed(0)} ns/op`);
	}
}
