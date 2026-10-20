// Records what the original extracts from each HTML fixture of extensions/websearch/testdata/html: the whole of
// oracle/extract.ts extractContent (linkedom + @mozilla/readability + turndown, then the RSC payload and Defuddle
// fallbacks) against a local page server, so the expected title, content and error are the original's own.
// The Go twin (htmlmd_fidelity_test.go) serves the same files through the port's fetch path and compares.
//
// Run in the container, from components/websearch (Node 24 strips the TypeScript types):
//   node port/record-html-extract.mjs            # rewrites every testdata/html/*.expected.json
//   node port/record-html-extract.mjs name ...   # only these fixtures
//
// A fixture <name>.html may have <name>.meta.json: {"contentType": "...", "headers": {"Link": "..."}, "status": 200}.
// The server's origin is replaced by http://fixtures.test in every recorded string. An error that continues with the
// provider checklist ("Fallback options:") is cut before it: the checklist belongs to the hosted-fetch routing, not to
// the HTML pipeline.
import { createServer } from "node:http";
import { mkdtemp, readdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const root = new URL("../extensions/websearch/testdata/html/", import.meta.url);
const cfgDir = await mkdtemp(join(tmpdir(), "record-html-"));
await writeFile(join(cfgDir, "web-search.json"), JSON.stringify({ ssrf: { allowRanges: ["127.0.0.0/8"] } }));
process.env.PI_CODING_AGENT_DIR = cfgDir;
for (const k of ["HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"]) delete process.env[k];

const { extractContent } = await import(new URL("./oracle/extract.ts", import.meta.url).href);

const names = (await readdir(root)).filter((f) => f.endsWith(".html")).map((f) => f.slice(0, -5)).sort();
const only = process.argv.slice(2);
const todo = only.length ? only : names;

const pages = new Map();
const server = createServer((req, res) => {
	const page = pages.get(req.url);
	if (!page) { res.writeHead(404, { "content-type": "text/plain" }); res.end("not found"); return; }
	res.writeHead(page.status, { "content-type": page.contentType, ...page.headers });
	res.end(page.body);
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const norm = (s) => s.split(origin).join("http://fixtures.test");

for (const name of todo) {
	const body = await readFile(new URL(`${name}.html`, root));
	let meta = {};
	try { meta = JSON.parse(await readFile(new URL(`${name}.meta.json`, root), "utf8")); } catch {}
	pages.set(`/${name}`, { body, status: meta.status ?? 200, contentType: meta.contentType ?? "text/html; charset=utf-8", headers: meta.headers ?? {} });
	const result = await extractContent(`${origin}/${name}`);
	let error = result.error;
	if (typeof error === "string") {
		const cut = error.indexOf("\n\nFallback options:");
		if (cut >= 0) error = error.slice(0, cut);
	}
	const expected = { title: norm(result.title ?? ""), content: norm(result.content ?? ""), error: error === null ? null : norm(error) };
	await writeFile(new URL(`${name}.expected.json`, root), JSON.stringify(expected, null, 1) + "\n");
	console.log(`${name}: title=${JSON.stringify(expected.title)} content=${expected.content.length} error=${JSON.stringify(expected.error)}`);
}
server.close();
await rm(cfgDir, { recursive: true, force: true });
process.exit(0);
