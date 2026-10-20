// Records the curl invocations of the original's proxied fetch (oracle/utils.ts fetchViaCurl) for a fixed list
// of requests, with the fake curl of oracle/test/proxy-transport.test.mjs (a script on PATH that logs its
// arguments and answers from a table). The Go twins (proxy_curl_test.go, TestCurlArgumentsEqualTheOriginals) assert
// the same arguments, request-body files and results from extensions/websearch/testdata/proxy-curl.json.
//
// Run in the container, from components/websearch (Node 24 strips the TypeScript types):
//   node port/record-proxy-curl.mjs > extensions/websearch/testdata/proxy-curl.json
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const { runWithProxy } = await import(new URL("./oracle/utils.ts", import.meta.url).href);

const dir = await mkdtemp(join(tmpdir(), "record-proxy-curl-"));
const logPath = join(dir, "curl-args.jsonl");
const routes = {
	"https://93.184.216.34/page": { status: 200, statusText: "OK", body: "page body" },
	"https://93.184.216.34/post": { status: 201, statusText: "Created", body: "created" },
	"https://93.184.216.34/gone": { status: 404, statusText: "Not Found", body: "gone" },
	"https://93.184.216.34/nobody": { status: 204, statusText: "No Content", body: "ignored" },
	"https://93.184.216.34/start": { status: 302, statusText: "Found", location: "https://other.example/final" },
	"https://other.example/final": { status: 200, statusText: "OK", body: "final" },
};
await writeFile(join(dir, "curl"), `#!/usr/bin/env node
const fs = require("node:fs");
const routes = JSON.parse(process.env.RECORD_ROUTES);
const args = process.argv.slice(2);
function valueAfter(flag) { const i = args.indexOf(flag); return i === -1 ? null : args[i + 1]; }
const data = valueAfter("--data-binary");
const entry = { args, requestBodyBase64: data && data.startsWith("@") ? fs.readFileSync(data.slice(1)).toString("base64") : null };
fs.appendFileSync(process.env.RECORD_LOG, JSON.stringify(entry) + "\\n");
const url = args[args.length - 1];
const route = routes[url];
if (!route) { process.stderr.write("unexpected url " + url); process.exit(1); }
fs.writeFileSync(valueAfter("-D"), "HTTP/1.1 " + route.status + " " + route.statusText + "\\r\\n" + (route.location ? "Location: " + route.location + "\\r\\n" : "") + "\\r\\n");
fs.writeFileSync(valueAfter("--output"), route.body || "");
process.stdout.write(JSON.stringify({ url_effective: url, num_redirects: 0 }));
`);
await chmod(join(dir, "curl"), 0o755);
process.env.PATH = `${dir}:${process.env.PATH}`;
process.env.RECORD_LOG = logPath;
process.env.RECORD_ROUTES = JSON.stringify(routes);
process.env.NO_PROXY = "";
process.env.no_proxy = "";
await writeFile(logPath, "");

const cases = [
	{ name: "http proxy, GET with headers", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/page", init: { headers: { "User-Agent": "ua", Accept: "text/html,*/*", "Cache-Control": "no-cache", "X-Empty": "" } } },
	{ name: "http proxy with credentials and a path", proxy: "http://user:pw@proxy.example:3128/tunnel", url: "https://93.184.216.34/page", init: {} },
	{ name: "https proxy, default port and mixed case host", proxy: "https://Proxy.Example:443", url: "https://93.184.216.34/page", init: {} },
	{ name: "socks4 proxy", proxy: "socks4://proxy.example:9050", url: "https://93.184.216.34/page", init: {} },
	{ name: "socks4a proxy", proxy: "socks4a://proxy.example:9050", url: "https://93.184.216.34/page", init: {} },
	{ name: "socks5 proxy", proxy: "socks5://proxy.example:9050", url: "https://93.184.216.34/page", init: {} },
	{ name: "socks5h proxy", proxy: "socks5h://proxy.example:9050", url: "https://93.184.216.34/page", init: {} },
	{ name: "proxy URL with a query and a fragment", proxy: "socks5h://proxy.example:9050?x=1#frag", url: "https://93.184.216.34/page", init: {} },
	{ name: "POST with a JSON body", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/post", init: { method: "POST", body: "{\"q\":\"x\"}", headers: { "Content-Type": "application/json", Authorization: "Bearer k" } } },
	{ name: "POST with URLSearchParams", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/post", init: { method: "post", body: new URLSearchParams({ a: "1", b: "two words" }), headers: { "content-type": "application/x-www-form-urlencoded" } } },
	{ name: "PUT with bytes", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/post", init: { method: "PUT", body: new Uint8Array([1, 2, 3, 255]) } },
	{ name: "GET with a body", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/page", init: { method: "GET", body: "x" } },
	{ name: "HEAD", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/page", init: { method: "HEAD" } },
	{ name: "headers given with different case merge", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/page", init: { headers: [["Accept", "a"], ["accept", "b"], ["X-B", "2"], ["x-a", "1"]] } },
	{ name: "404 status", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/gone", init: {} },
	{ name: "204 has no body", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/nobody", init: {} },
	{ name: "manual redirect", proxy: "http://proxy.example:8080", url: "https://93.184.216.34/start", init: { redirect: "manual", headers: { Authorization: "Bearer secret" } } },
	{ name: "unknown url (curl exits 1)", proxy: "http://user:secret@proxy.example:8080", url: "https://93.184.216.34/missing", init: {}, expectError: true },
];

const out = [];
for (const c of cases) {
	await writeFile(logPath, "");
	let result;
	try {
		const response = await runWithProxy(c.proxy, () => fetch(c.url, c.init));
		const text = await response.text();
		result = { status: response.status, statusText: response.statusText, url: response.url, redirected: response.redirected, location: response.headers.get("location"), text };
	} catch (err) {
		result = { error: String(err instanceof Error ? err.message : err) };
	}
	const calls = (await readFile(logPath, "utf8")).split("\n").filter(Boolean).map((l) => JSON.parse(l));
	const norm = (s) => s.replace(/[^@\s]*pi-web-access-proxy-[A-Za-z0-9]+/g, "<dir>");
	out.push({
		name: c.name,
		proxy: c.proxy,
		url: c.url,
		method: c.init.method ?? null,
		headers: c.init.headers ?? null,
		body: c.init.body === undefined ? null : c.init.body instanceof URLSearchParams ? { urlSearchParams: c.init.body.toString() } : c.init.body instanceof Uint8Array ? { bytes: [...c.init.body] } : c.init.body,
		redirect: c.init.redirect ?? null,
		calls: calls.map((x) => ({ args: x.args.map(norm), requestBodyBase64: x.requestBodyBase64 })),
		result: { ...result, ...(result.error ? { error: norm(result.error) } : {}) },
	});
}
await rm(dir, { recursive: true, force: true });
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
