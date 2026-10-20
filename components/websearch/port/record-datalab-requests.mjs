// Records the requests the ORIGINAL Datalab modules (oracle/datalab-pdf-extract.ts and the Datalab
// leg of oracle/pdf-extract.ts) send, and what they return, for the inputs the Go twins replay
// (extensions/websearch/datalab_wire_test.go). The API base is configurable, so pigeq scenarios cover
// the paths a scenario server can answer; the upload URL a Datalab server returns is absolute and a
// scenario route cannot name its own server, so the full flow is proven here with `fetch` replaced
// by a recorder that answers from canned responses.
//
//   node port/record-datalab-requests.mjs > extensions/websearch/testdata/datalab-wire.json
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24, which strips
// the TypeScript types itself). Secrets are synthetic. The run takes a few seconds: two cases wait
// for the 1.5 s poll interval.
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const oracle = join(dirname(fileURLToPath(import.meta.url)), "oracle");
const url = (name) => new URL(`file://${join(oracle, name)}`).href;
const key = { DATALAB_API_KEY: "synthetic-datalab-key" };

const happy = (extra = {}) => [
	{ match: "/files/upload", method: "POST", json: { file_id: 7, upload_url: "https://storage.test/put/abc", reference: "datalab://file-7", expires_in: 3600 } },
	{ match: "/put/", method: "PUT", text: "" },
	{ match: "/files/7/confirm", json: { file_id: 7, reference: "datalab://file-7", message: "confirmed" } },
	{ match: "/files/7", method: "DELETE", text: "" },
	{ match: "/convert", method: "POST", json: { status: "processing", request_check_url: "/check/abc123" } },
	{ match: "/check/abc123", json: { status: "complete", success: true, markdown: "<!-- Page 1 -->\n# Sample Document\n\nFirst page.\n\n<!-- Page 2 -->\n\nSecond page.", page_count: 2, parse_quality_score: 4.5 } },
	...Object.entries(extra).map(([match, r]) => ({ match, ...r })),
];
const call = (expr) => `return await (async () => { const m = await import(${JSON.stringify(url("datalab-pdf-extract.ts"))}); ${expr} })();`;
const input = "Uint8Array.from([1, 2, 3, 4]).buffer";

const cases = [
	{ name: "default-flow", env: key, call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 5, title: "Sample Document" });`), responses: happy() },
	{
		name: "eu-accurate-custom-base",
		env: { ...key, DATALAB_PROCESSING_LOCATION: " EU ", DATALAB_MODE: "Accurate", DATALAB_API_BASE: "https://datalab.example.test/api/v1//" },
		call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 2, title: "  Mixed CASE & Title -- with_symbols and a very very very very long name that goes past sixty chars " });`),
		responses: [
			{ match: "/files/upload", json: { file_id: "a/b c", upload_url: "https://storage.test/put/def", reference: " datalab://ref " } },
			{ match: "/put/", method: "PUT", text: "" },
			{ match: "/confirm", json: { file_id: 12, reference: "datalab://confirmed" } },
			{ match: "/files/12", method: "DELETE", text: "" },
			{ match: "/convert", json: { status: "complete", markdown: " <!-- Page 1 -->\nDone ", page_count: 0 } },
		],
	},
	{
		name: "complete-on-convert",
		env: key,
		call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 1, title: "Doc" });`),
		responses: [...happy().filter((r) => r.match !== "/convert"), { match: "/convert", json: { status: "complete", success: true, markdown: "<!-- Page 1 -->\nFast\n\n<!-- Page 2 -->\nAlso", parse_quality_score: "x" } }],
	},
	{
		name: "key-from-config",
		config: { datalabApiKey: "synthetic-config-key" },
		call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 3, title: "Doc", mode: "fast", processingLocation: "eu" });`),
		responses: happy(),
	},
	{
		name: "http-error-redacted",
		env: key,
		call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 1, title: "Doc" });`),
		responses: [{ match: "/files/upload", status: 401, text: "  invalid key synthetic-datalab-key  " }],
	},
	{
		name: "failed-conversion",
		env: key,
		call: call(`return await m.extractPDFViaDatalab(${input}, { maxPages: 1, title: "Doc" });`),
		responses: happy({ "/check/abc123": { json: { status: "failed", error: { code: 3 } } } }),
	},
	{
		name: "chain-from-pdf-extract",
		env: key,
		config: { pdf: { provider: "datalab", datalabMode: "fast", maxPages: 7, datalabTimeoutMs: 60000 } },
		call: `return await (async () => { const m = await import(${JSON.stringify(url("pdf-extract.ts"))}); const dir = (await import("node:fs")).mkdtempSync((await import("node:path")).join((await import("node:os")).tmpdir(), "dl-")); const r = await m.extractPDFToMarkdown(${input}, "https://example.test/docs/Q3_Results.pdf", { outputDir: dir }); return { title: r.title, content: r.content, pages: r.pages, chars: r.chars, file: r.outputPath.split("/").pop() }; })();`,
		responses: happy(),
	},
];

const childPrelude = `
const responses = JSON.parse(process.env.RECORDER_RESPONSES);
const requests = [];
globalThis.fetch = async (url, init = {}) => {
	const method = init.method ?? "GET";
	const headers = {};
	for (const [k, v] of new Headers(init.headers)) headers[k] = v;
	let body = null;
	if (typeof init.body === "string") body = init.body;
	else if (init.body instanceof FormData) body = { form: [...init.body.entries()] };
	else if (init.body instanceof Blob) body = { blob: { type: init.body.type, base64: Buffer.from(await init.body.arrayBuffer()).toString("base64") } };
	requests.push({ method, url: String(url), headers, body });
	const hit = responses.find((r) => String(url).includes(r.match) && (!r.method || r.method === method));
	if (!hit) throw new Error("Unexpected fetch: " + url);
	if (hit.throw) throw new Error(hit.throw);
	return new Response(hit.json !== undefined ? JSON.stringify(hit.json) : (hit.text ?? ""), { status: hit.status ?? 200, statusText: hit.statusText ?? ({ 401: "Unauthorized" }[hit.status] ?? ""), headers: hit.headers ?? {} });
};
`;

const out = { recorder: "port/record-datalab-requests.mjs", cases: {} };
for (const c of cases) {
	const root = mkdtempSync(join(tmpdir(), "datalab-wire-"));
	if (c.config) writeFileSync(join(root, "web-search.json"), JSON.stringify(c.config));
	const env = { ...process.env, HOME: root, USERPROFILE: root, PI_CODING_AGENT_DIR: root, RECORDER_RESPONSES: JSON.stringify(c.responses), ...(c.env ?? {}) };
	for (const k of ["XDG_CONFIG_HOME", "DATALAB_API_KEY", "DATALAB_MODE", "DATALAB_PROCESSING_LOCATION", "DATALAB_API_BASE", "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL", "CLOUDFLARE_API_KEY"]) {
		if (!(c.env && k in c.env)) delete env[k];
	}
	// The cleanup request is not awaited by the original: give it a moment before reporting.
	const script = `${childPrelude}\nlet result = null, error = null;\ntry { result = await (async () => { ${c.call} })(); } catch (e) { error = e instanceof Error ? e.message : String(e); }\nawait new Promise((r) => setTimeout(r, 100));\nprocess.stdout.write(JSON.stringify({ requests, result, error }));\n`;
	const child = spawnSync(process.execPath, ["--input-type=module"], { input: script, encoding: "utf8", env, maxBuffer: 8 * 1024 * 1024 });
	if (child.status !== 0) throw new Error(`${c.name}: ${child.stderr}`);
	out.cases[c.name] = { env: c.env ?? {}, config: c.config ?? null, responses: c.responses, ...JSON.parse(child.stdout) };
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
