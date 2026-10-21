// Records the requests the ORIGINAL Gemini modules (oracle/gemini-*.ts) send, and what they return,
// for inputs the Go twins replay (extensions/websearch/gemini_wire_test.go). The Gemini endpoints
// are fixed (generativelanguage.googleapis.com, aiplatform.googleapis.com, oauth2.googleapis.com),
// so a scenario server cannot receive them; this stands in: `fetch` is replaced by a recorder
// that answers from canned responses.
//
//   node port/record-gemini-requests.mjs > extensions/websearch/testdata/gemini-wire.json
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24, which strips
// the TypeScript types itself). Secrets are synthetic.
import { spawnSync } from "node:child_process";
import { generateKeyPairSync } from "node:crypto";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const oracle = join(dirname(fileURLToPath(import.meta.url)), "oracle");
const url = (name) => new URL(`file://${join(oracle, name)}`).href;
const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048, privateKeyEncoding: { type: "pkcs8", format: "pem" }, publicKeyEncoding: { type: "spki", format: "pem" } });

const answer = (text, extra = {}) => ({ status: 200, json: { candidates: [{ content: { parts: [{ text }] }, ...extra }] } });
const filler = "Gemini extracted this page. ".repeat(5);

const cases = [
	{
		name: "api-key-search-with-grounding",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		call: `const r = await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("what is the capital of France?", { provider: "gemini" }); return r;`,
		responses: [
			{ match: "grounding-api-redirect/good", method: "HEAD", status: 302, headers: { location: "https://example.com/real" } },
			{ match: "grounding-api-redirect/bad", method: "HEAD", throw: "dns failure" },
			{ match: ":generateContent", ...answer("Paris is the capital.", { groundingMetadata: { groundingChunks: [
				{ web: { uri: "https://vertexaisearch.cloud.google.com/grounding-api-redirect/good", title: "Good" } },
				{ web: { uri: "https://vertexaisearch.cloud.google.com/grounding-api-redirect/bad", title: "Bad" } },
				{ retrievedContext: {} },
				{ web: { uri: "https://plain.example/x" } },
			] } }) },
		],
	},
	{
		name: "search-model-from-config",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		config: { searchModel: "custom-gemini-model" },
		call: `return await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("configured model", { provider: "gemini" });`,
		responses: [{ match: ":generateContent", ...answer("ok") }],
	},
	{
		name: "relay-base-url-search",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		config: { geminiBaseUrl: "https://relay.example.com/" },
		call: `return await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("relay", { provider: "gemini" });`,
		responses: [{ match: ":generateContent", ...answer("relayed") }],
	},
	{
		name: "cloudflare-gateway-search",
		env: { GOOGLE_GEMINI_BASE_URL: "https://gateway.ai.cloudflare.com/v1/account/gateway/google-ai-studio/", CLOUDFLARE_API_KEY: "synthetic-cloudflare-key" },
		call: `return await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("gateway", { provider: "gemini" });`,
		responses: [{ match: ":generateContent", ...answer("via gateway") }],
	},
	{
		name: "adc-authorized-user-search",
		config: { geminiAuth: "adc", geminiProject: "ai-eng-sandbox", geminiLocation: "global" },
		adc: { client_id: "synthetic-client-id", client_secret: "synthetic-client-secret", refresh_token: "synthetic-refresh-token", type: "authorized_user" },
		call: `return await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("adc", { provider: "gemini" });`,
		responses: [
			{ match: "oauth2.googleapis.com/token", status: 200, json: { access_token: "synthetic-adc-token", expires_in: 3599 } },
			{ match: ":generateContent", ...answer("via vertex") },
		],
	},
	{
		name: "adc-service-account-search",
		config: { geminiAuth: "adc", geminiProject: "p", geminiLocation: "l" },
		adc: { type: "service_account", client_email: "svc@example.iam", private_key: privateKey, token_uri: "https://token.example.com/oauth" },
		call: `return await (await import(${JSON.stringify(url("gemini-search.ts"))})).search("adc sa", { provider: "gemini" });`,
		responses: [
			{ match: "token.example.com/oauth", status: 200, json: { access_token: "sa-token", expires_in: 3600 } },
			{ match: ":generateContent", ...answer("via vertex sa") },
		],
	},
	{
		name: "url-context-extraction",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		call: `return await (await import(${JSON.stringify(url("gemini-url-context.ts"))})).extractWithUrlContext("https://example.com/docs/page");`,
		responses: [{ match: ":generateContent", ...answer(`# Extracted Title\n\n${filler}`) }],
	},
	{
		name: "url-context-unsafe-retrieval",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		call: `return await (await import(${JSON.stringify(url("gemini-url-context.ts"))})).extractWithUrlContext("https://example.com/docs/page");`,
		responses: [{ match: ":generateContent", ...answer(filler, { url_context_metadata: { url_metadata: [{ url_retrieval_status: "URL_RETRIEVAL_STATUS_UNSAFE" }] } }) }],
	},
	{
		name: "pdf-inline-conversion",
		env: { GEMINI_API_KEY: "synthetic-gemini-key" },
		call: `return await (await import(${JSON.stringify(url("gemini-pdf-extract.ts"))})).extractPDFViaGemini(Uint8Array.from([1, 2, 3, 4]).buffer, { pages: 5, maxPages: 2, title: "Sample Document" });`,
		responses: [{ match: ":generateContent", ...answer("```markdown\n<!-- Page 1 -->\n# Sample Document\n\nFirst page.\n\n<!-- Page 2 -->\n\nSecond page.\n```", { finishReason: "STOP" }) }],
	},
	{
		name: "video-query-gateway",
		env: { GOOGLE_GEMINI_BASE_URL: "https://gateway.ai.cloudflare.com/v1/account/gateway/google-ai-studio", CLOUDFLARE_API_KEY: "env-cf-key" },
		call: `return await (await import(${JSON.stringify(url("gemini-api.ts"))})).queryGeminiApiWithVideo("Describe", "files/test", { model: "gemini-test", mimeType: "video/webm", timeoutMs: 1000 });`,
		responses: [{ match: ":generateContent", ...answer("ok") }],
	},
	{
		name: "api-error-redacted",
		env: { GEMINI_API_KEY: "SYNTHETIC_GEMINI_SECRET" },
		call: `return await (await import(${JSON.stringify(url("gemini-api.ts"))})).queryGeminiApiWithVideo("describe", "files/test", { timeoutMs: 1000 });`,
		responses: [{ match: ":generateContent", status: 400, text: "provider echoed SYNTHETIC_GEMINI_SECRET" }],
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
	else if (init.body instanceof URLSearchParams) body = init.body.toString();
	requests.push({ method, url: String(url), headers, body });
	const hit = responses.find((r) => String(url).includes(r.match) && (!r.method || r.method === method));
	if (!hit) throw new Error("Unexpected fetch: " + url);
	if (hit.throw) throw new Error(hit.throw);
	return new Response(hit.json !== undefined ? JSON.stringify(hit.json) : (hit.text ?? ""), { status: hit.status ?? 200, headers: hit.headers ?? {} });
};
`;

const out = { recorder: "port/record-gemini-requests.mjs", cases: {} };
for (const c of cases) {
	const root = mkdtempSync(join(tmpdir(), "gemini-wire-"));
	if (c.config) writeFileSync(join(root, "web-search.json"), JSON.stringify(c.config));
	const env = { ...process.env, HOME: root, USERPROFILE: root, PI_CODING_AGENT_DIR: root, RECORDER_RESPONSES: JSON.stringify(c.responses), ...(c.env ?? {}) };
	for (const k of ["XDG_CONFIG_HOME", "GEMINI_API_KEY", "GOOGLE_GEMINI_BASE_URL", "CLOUDFLARE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "GCLOUD_PROJECT"]) {
		if (!(c.env && k in c.env)) delete env[k];
	}
	if (c.adc) {
		writeFileSync(join(root, "adc.json"), JSON.stringify(c.adc));
		env.GOOGLE_APPLICATION_CREDENTIALS = join(root, "adc.json");
	}
	const script = `${childPrelude}\nlet result = null, error = null;\ntry { result = await (async () => { ${c.call} })(); } catch (e) { error = e instanceof Error ? e.message : String(e); }\nconst { activityMonitor } = await import(${JSON.stringify(url("activity.ts"))});\nconst activity = activityMonitor.getEntries().map(({ type, query, url, status, error }) => ({ type, query, url, status, error }));\nprocess.stdout.write(JSON.stringify({ requests, result, error, activity }));\n`;
	const child = spawnSync(process.execPath, ["--input-type=module"], { input: script, encoding: "utf8", env, maxBuffer: 8 * 1024 * 1024 });
	if (child.status !== 0) throw new Error(`${c.name}: ${child.stderr}`);
	const rec = JSON.parse(child.stdout);
	// A service account JWT depends on the clock and the key: keep its decoded header and claims
	// (iat and exp as an offset) and whether a signature is present.
	for (const r of rec.requests) {
		if (typeof r.body === "string" && r.body.includes("assertion=")) {
			const form = new URLSearchParams(r.body);
			const [h, p, s] = form.get("assertion").split(".");
			const claims = JSON.parse(Buffer.from(p, "base64url").toString());
			claims.exp -= claims.iat;
			delete claims.iat;
			form.set("assertion", JSON.stringify({ header: JSON.parse(Buffer.from(h, "base64url").toString()), claims, signed: s.length > 0 }));
			r.body = form.toString();
		}
	}
	out.cases[c.name] = { env: c.env ?? {}, config: c.config ?? null, adc: c.adc && c.adc.type === "service_account" ? { ...c.adc, private_key: "<generated>" } : (c.adc ?? null), responses: c.responses, ...rec };
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
