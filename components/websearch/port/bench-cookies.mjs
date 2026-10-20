// Times the ORIGINAL's cookie extraction and authenticated fetch with canned programs, to compare
// with BenchmarkCookieExtraction and BenchmarkAuthenticatedFetch (extensions/websearch/cookies_bench_test.go).
// `execFile` and `fetch` are replaced in-process, so only the module's own work is timed; the
// cookie database is a placeholder file and every password is synthetic.
//
//   node port/bench-cookies.mjs [iterations]
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24).
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import cp from "node:child_process";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const iterations = Number(process.argv[2] ?? 200);
const home = mkdtempSync(join(tmpdir(), "pi-bench-cookies-"));
const agent = join(home, "agent");
mkdirSync(agent, { recursive: true });
Object.assign(process.env, { HOME: home, USERPROFILE: home, PI_CODING_AGENT_DIR: agent, PI_ALLOW_BROWSER_COOKIES: "1", PI_WEB_ACCESS_DISABLE_NODE_SQLITE: "1" });
writeFileSync(join(agent, "web-search.json"), JSON.stringify({ authFetch: { work: ["example.com"] } }));
const base = process.platform === "darwin" ? join(home, "Library", "Application Support", "Google", "Chrome") : join(home, ".config", "google-chrome");
mkdirSync(join(base, "Default"), { recursive: true });
writeFileSync(join(base, "Default", "Cookies"), "placeholder");

const rows = JSON.stringify([{ name: "sid", value: "synthetic", host_key: "app.example.com", path: "/private", expires_utc: 0, encrypted_value_hex: "" }]);
cp.execFile = (command, _args, _opts, cb) => {
	queueMicrotask(() => cb(null, command === "sqlite3" ? rows : "peanuts", ""));
	return {};
};
syncBuiltinESMExports();
globalThis.fetch = async () => new Response("private body", { status: 200, headers: { "content-type": "text/plain" } });

const oracle = join(dirname(fileURLToPath(import.meta.url)), "oracle");
const { getBrowserCookiesForHosts } = await import(new URL(`file://${join(oracle, "chrome-cookies.ts")}`).href);
const { resolveAuthFetchProfile } = await import(new URL(`file://${join(oracle, "auth-fetch.ts")}`).href);
const { extractContent } = await import(new URL(`file://${join(oracle, "extract.ts")}`).href);

const median = (xs) => xs.sort((a, b) => a - b)[Math.floor(xs.length / 2)];
async function time(label, fn) {
	await fn();
	const samples = [];
	for (let i = 0; i < iterations; i++) {
		const t = process.hrtime.bigint();
		await fn();
		samples.push(Number(process.hrtime.bigint() - t) / 1e3);
	}
	console.log(`${label}: median ${median(samples).toFixed(1)} us over ${iterations} runs`);
}

const requestUrl = new URL("https://app.example.com/private/page");
await time("cookie extraction (password cached)", async () => {
	const r = await getBrowserCookiesForHosts({ hosts: ["app.example.com"], requestUrl });
	if (r?.cookieHeader !== "sid=synthetic") throw new Error("unexpected result");
});
const profile = resolveAuthFetchProfile("work");
await time("authenticated fetch", async () => {
	const r = await extractContent("https://app.example.com/private/page", undefined, { mode: "raw", authFetchProfile: profile, lookup: async () => [{ address: "93.184.216.34", family: 4 }] });
	if (r.content !== "private body") throw new Error(`unexpected result: ${r.error}`);
});
