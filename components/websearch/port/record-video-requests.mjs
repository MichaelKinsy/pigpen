// Records what the ORIGINAL video and YouTube extractors (oracle/extract.ts, video-extract.ts,
// youtube-extract.ts) send and return, for inputs the Go twins replay
// (extensions/websearch/video_wire_test.go). The Gemini, Perplexity and YouTube thumbnail
// endpoints are fixed, so a scenario server cannot receive them; this stands in: `fetch` is
// replaced by a recorder that answers from canned responses. yt-dlp, ffmpeg and ffprobe are
// small shell scripts on PATH that log their arguments and answer from a canned table, so the
// recording holds the commands the original started and the text it made of their results.
//
//   node port/record-video-requests.mjs > extensions/websearch/testdata/video-wire.json
//
// Run it from components/websearch with the oracle's dependencies installed (Node 24, which strips
// the TypeScript types itself). Secrets are synthetic. The scripts are part of the recording: the
// Go test writes the same scripts and runs the port against them.
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, truncateSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const oracle = join(dirname(fileURLToPath(import.meta.url)), "oracle");
const extractUrl = new URL(`file://${join(oracle, "extract.ts")}`).href;

const KEY = { GEMINI_API_KEY: "synthetic-gemini-key" };
const answer = (text) => ({ match: ":generateContent", json: { candidates: [{ content: { parts: [{ text }] } }] } });
const JPEG = "\\377\\330\\377\\340JFIF";
const stream = (duration) => ({ stdout: `${duration}\\nhttps://stream.example/video.mp4\\n` });
const ok = { stdout: JPEG };
const ytShims = (yt = stream("125.5"), ffmpeg = ok) => ({ "yt-dlp": yt, ffmpeg });
const localShims = (ffprobe = { stdout: "75.25\\n" }, ffmpeg = ok) => ({ ffprobe, ffmpeg });
const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ";
const clip = "<root>/clip.mp4";
const upload = [
	{ match: "/upload/v1beta/files?upload_id", method: "PUT", json: { file: { name: "files/synthetic", uri: "files/synthetic" } } },
	{ match: "/upload/v1beta/files", method: "POST", headers: { "x-goog-upload-url": "https://generativelanguage.googleapis.com/upload/v1beta/files?upload_id=synthetic" } },
	{ match: "/v1beta/files/synthetic", method: "GET", json: { state: "ACTIVE" } },
	{ match: "/v1beta/files/synthetic", method: "DELETE", status: 204 },
];
const thumb = { match: "img.youtube.com", bytesBase64: Buffer.from("thumb-bytes").toString("base64"), headers: { "content-type": "image/jpeg" } };
const files = { "clip.mp4": "synthetic media" };

const cases = [
	// youtube-extract.ts: the Gemini API path with the thumbnail
	{ name: "youtube-gemini-api", env: KEY, url: "https://youtu.be/dQw4w9WgXcQ?t=5", responses: [answer("# Rick\n\nThe video."), thumb] },
	{ name: "youtube-prompt-model", env: KEY, url: yt, options: { prompt: "What is said?", model: "gemini-test" }, responses: [answer("no heading here"), { match: "img.youtube.com", status: 404 }] },
	{ name: "youtube-model-from-config", env: KEY, config: { youtube: { preferredModel: " gemini-config " } }, url: yt, responses: [answer("# Configured"), thumb] },
	{ name: "youtube-image-disabled", env: KEY, config: { image: { enabled: false } }, url: yt, responses: [answer("# No thumbnail")] },
	{ name: "youtube-perplexity-default-prompt", env: { PERPLEXITY_API_KEY: "synthetic-perplexity-key" }, url: yt, responses: [{ match: "api.perplexity.ai", json: { choices: [{ message: { content: "It is a song." } }], citations: [] } }, thumb] },
	{ name: "youtube-perplexity-custom-prompt", env: { PERPLEXITY_API_KEY: "synthetic-perplexity-key" }, url: yt, options: { prompt: "List the chapters." }, responses: [{ match: "api.perplexity.ai", json: { choices: [{ message: { content: "Chapter one." } }], citations: [] } }, thumb] },
	{ name: "youtube-gemini-error-then-perplexity", env: { ...KEY, PERPLEXITY_API_KEY: "synthetic-perplexity-key" }, url: yt, responses: [{ match: ":generateContent", status: 503, json: { error: { message: "model overloaded" } } }, { match: "api.perplexity.ai", json: { choices: [{ message: { content: "Fallback answer." } }], citations: [] } }, thumb] },
	{ name: "youtube-all-paths-fail", env: { ...KEY, PERPLEXITY_API_KEY: "synthetic-perplexity-key" }, url: yt, responses: [{ match: ":generateContent", status: 503, json: { error: { message: "model overloaded" } } }, { match: "api.perplexity.ai", status: 401, text: "bad key" }] },
	{ name: "youtube-perplexity-empty-answer", env: { PERPLEXITY_API_KEY: "synthetic-perplexity-key" }, url: yt, responses: [{ match: "api.perplexity.ai", json: { choices: [{ message: { content: "" } }] } }] },
	{ name: "youtube-no-access", url: yt, responses: [] },
	{ name: "youtube-playlist-is-a-page", url: "https://www.youtube.com/playlist?list=PL1234567890abcdefghij", responses: [{ match: "youtube.com", status: 404, text: "nope", headers: { "content-type": "text/html" } }] },
	{ name: "youtube-disabled", config: { youtube: { enabled: false } }, url: yt, responses: [{ match: "youtube.com", status: 404, text: "nope", headers: { "content-type": "text/html" } }] },
	{ name: "youtube-bad-config", rawConfig: "{ not json", url: yt, responses: [] },

	// extract.ts: frames and timestamps of YouTube videos (yt-dlp then ffmpeg)
	{ name: "youtube-frames", shims: ytShims(), url: yt, options: { frames: 3 }, responses: [] },
	{ name: "youtube-frames-default-density", shims: ytShims(stream("20")), url: yt, options: { frames: 6 }, responses: [] },
	{ name: "youtube-frames-unknown-duration", shims: ytShims({ stdout: "NA\\nhttps://stream.example/video.mp4\\n" }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-frame-single", shims: ytShims(), url: yt, options: { timestamp: "1:23" }, responses: [] },
	{ name: "youtube-frame-hours", shims: ytShims(stream("9000")), url: yt, options: { timestamp: "1:02:03" }, responses: [] },
	{ name: "youtube-frame-past-end", shims: ytShims(), url: yt, options: { timestamp: "5:00" }, responses: [] },
	{ name: "youtube-frames-from-timestamp", shims: ytShims(), url: yt, options: { timestamp: "10", frames: 3 }, responses: [] },
	{ name: "youtube-frames-from-timestamp-past-end", shims: ytShims(), url: yt, options: { timestamp: "120", frames: 3 }, responses: [] },
	{ name: "youtube-range", shims: ytShims(), url: yt, options: { timestamp: "0:10-1:10" }, responses: [] },
	{ name: "youtube-range-dense", shims: ytShims(), url: yt, options: { timestamp: "0:10-0:20", frames: 5 }, responses: [] },
	{ name: "youtube-range-past-end", shims: ytShims(), url: yt, options: { timestamp: "1:00-5:00" }, responses: [] },
	{ name: "youtube-range-unknown-duration", shims: ytShims({ stdout: "NA\\nhttps://stream.example/video.mp4\\n" }), url: yt, options: { timestamp: "0:10-0:40" }, responses: [] },
	{ name: "youtube-frame-ffmpeg-partial", shims: ytShims(stream("125.5"), { stdout: JPEG, rules: [{ contains: "-ss 15 ", stdout: "", stderr: "boom\n", exit: 1 }] }), url: yt, options: { timestamp: "0:00-0:30", frames: 3 }, responses: [] },
	{ name: "youtube-frame-ffmpeg-all-fail", shims: ytShims(stream("125.5"), { stdout: "", stderr: "Server returned 403 Forbidden\\n", exit: 1 }), url: yt, options: { timestamp: "10-30" }, responses: [] },
	{ name: "youtube-frame-ffmpeg-empty", shims: ytShims(), url: yt, options: { timestamp: "10" }, responses: [], shimsOverride: { ffmpeg: { stdout: "" } } },
	{ name: "youtube-frame-ffmpeg-missing", shims: { "yt-dlp": stream("125.5") }, url: yt, options: { timestamp: "10" }, responses: [] },
	{ name: "youtube-ytdlp-missing", shims: {}, url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-private", shims: ytShims({ stderr: "ERROR: Private video. Sign in\\n", exit: 1 }), url: yt, options: { timestamp: "5" }, responses: [] },
	{ name: "youtube-ytdlp-sign-in", shims: ytShims({ stderr: "ERROR: Please sign in\\n", exit: 1 }), url: yt, options: { timestamp: "5-30" }, responses: [] },
	{ name: "youtube-ytdlp-unavailable", shims: ytShims({ stderr: "ERROR: Video not available\\n", exit: 1 }), url: yt, options: { timestamp: "5", frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-live", shims: ytShims({ stderr: "ERROR: This live event will begin\\n", exit: 1 }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-other", shims: ytShims({ stderr: "ERROR:   something\\n  odd happened\\n", exit: 1 }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-silent-failure", shims: ytShims({ exit: 3 }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-no-stream", shims: ytShims({ stdout: "125\\n" }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "youtube-ytdlp-crlf", shims: ytShims({ stdout: "30\\r\\nhttps://stream.example/crlf.mp4\\r\\n" }), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "frames-need-image-support", config: { image: { enabled: false } }, shims: ytShims(), url: yt, options: { frames: 2 }, responses: [] },
	{ name: "timestamp-invalid", shims: ytShims(), url: yt, options: { timestamp: "later" }, responses: [] },
	{ name: "timestamp-reversed-range-is-invalid", shims: ytShims(), url: yt, options: { timestamp: "1:00-0:10" }, responses: [] },
	{ name: "frames-of-a-page", url: "https://example.com/page", options: { frames: 2 }, responses: [] },
	{ name: "timestamp-of-a-page", url: "https://example.com/page", options: { timestamp: "1:00" }, responses: [] },

	// extract.ts and video-extract.ts: local videos
	{ name: "local-frames", files, shims: localShims(), url: clip, options: { frames: 4 }, responses: [] },
	{ name: "local-frames-ffprobe-missing", files, shims: { ffmpeg: ok }, url: clip, options: { frames: 4 }, responses: [] },
	{ name: "local-frames-ffprobe-garbage", files, shims: localShims({ stdout: "N/A\\n" }), url: clip, options: { frames: 4 }, responses: [] },
	{ name: "local-frames-ffprobe-failure", files, shims: localShims({ stderr: "moov atom not found\\n", exit: 1 }), url: clip, options: { frames: 4 }, responses: [] },
	{ name: "local-frames-ffprobe-silent-failure", files, shims: localShims({ exit: 1 }), url: clip, options: { frames: 4 }, responses: [] },
	{ name: "local-frame-single", files, shims: localShims(), url: clip, options: { timestamp: "12" }, responses: [] },
	{ name: "local-frame-file-url", files, shims: localShims(), url: "file://<root>/clip.mp4", options: { timestamp: "0:05" }, responses: [] },
	{ name: "local-frame-spaced-name", files: { "my clip.mp4": "synthetic media" }, shims: localShims(), url: "<root>/my\u00a0clip.mp4", options: { timestamp: "7" }, responses: [] },
	{ name: "local-frame-ffmpeg-missing", files, shims: {}, url: clip, options: { timestamp: "12" }, responses: [] },
	{ name: "local-frame-ffmpeg-failure", files, shims: localShims(undefined, { stderr: "Invalid data found when processing input\\n", exit: 1 }), url: clip, options: { timestamp: "12" }, responses: [] },
	{ name: "local-frames-from-timestamp", files, shims: localShims(), url: clip, options: { timestamp: "20", frames: 3 }, responses: [] },
	{ name: "local-range", files, shims: localShims(), url: clip, options: { timestamp: "0:10-1:00" }, responses: [] },
	{ name: "local-range-dense", files, shims: localShims(), url: clip, options: { timestamp: "0:10-0:22", frames: 4 }, responses: [] },
	{ name: "local-oversize-analysis", sparse: { "clip.mp4": 50 * 1024 * 1024 + 1 }, url: clip, options: { prompt: "summarize" }, responses: [] },
	{ name: "local-oversize-config-limit", files, config: { video: { maxSizeMB: 0.00001 } }, url: clip, responses: [] },
	{ name: "local-oversize-frames", sparse: { "clip.mp4": 50 * 1024 * 1024 + 1 }, shims: localShims(), url: clip, options: { frames: 1 }, responses: [] },
	{ name: "local-oversize-timestamp", sparse: { "clip.mp4": 50 * 1024 * 1024 + 1 }, shims: localShims(), url: clip, options: { timestamp: "1" }, responses: [] },
	{ name: "local-video-disabled", files, config: { video: { enabled: false } }, shims: localShims(), url: clip, options: { frames: 2 }, responses: [] },
	{ name: "local-video-missing-file", shims: localShims(), url: "<root>/absent.mp4", options: { frames: 2 }, responses: [] },
	{ name: "local-not-a-video", files: { "notes.txt": "x" }, shims: localShims(), url: "<root>/notes.txt", options: { frames: 2 }, responses: [] },
	{ name: "local-video-bad-config", files, rawConfig: "{ not json", shims: localShims(), url: clip, options: { frames: 2 }, responses: [] },

	// video-extract.ts: the Gemini Files API upload of a local video
	{ name: "local-gemini-api", files, env: KEY, shims: localShims(), url: clip, responses: [...upload, answer("# Local Clip\n\nContent.")] },
	{ name: "local-gemini-api-title-from-name", files, env: KEY, shims: localShims(), url: clip, options: { prompt: "what happens", model: "gemini-test" }, responses: [...upload, answer("plain text")] },
	{ name: "local-gemini-api-relay", files, env: KEY, config: { geminiBaseUrl: "https://relay.example.com" }, shims: localShims(), url: clip, responses: [
		{ match: "/upload/v1beta/files?upload_id", method: "PUT", json: { file: { name: "files/synthetic", uri: "files/synthetic" } } },
		{ match: "/upload/v1beta/files", method: "POST", headers: { "x-goog-upload-url": "https://relay.example.com/upload/v1beta/files?upload_id=synthetic" } },
		{ match: "/v1beta/files/synthetic", method: "GET", json: { state: "ACTIVE" } },
		{ match: "/v1beta/files/synthetic", method: "DELETE", status: 204 },
		answer("# Relay video"),
	] },
	{ name: "local-gemini-api-processing-then-active", files, env: KEY, shims: localShims(), url: clip, responses: [
		upload[0], upload[1],
		{ match: "/v1beta/files/synthetic", method: "GET", json: { state: "PROCESSING" }, times: 1 },
		upload[2], upload[3], answer("# Slow"),
	] },
	{ name: "local-gemini-api-file-failed", files, env: KEY, shims: localShims(), url: clip, responses: [
		upload[0], upload[1], { match: "/v1beta/files/synthetic", method: "GET", json: { state: "FAILED" } }, upload[3],
	] },
	{ name: "local-gemini-api-state-check-error", files, env: KEY, shims: localShims(), url: clip, responses: [
		upload[0], upload[1], { match: "/v1beta/files/synthetic", method: "GET", status: 500, text: "down" }, upload[3],
	] },
	{ name: "local-gemini-api-init-error", files, env: KEY, shims: localShims(), url: clip, responses: [
		{ match: "/upload/v1beta/files", method: "POST", status: 403, text: "denied for synthetic-gemini-key" },
	] },
	{ name: "local-gemini-api-no-upload-url", files, env: KEY, shims: localShims(), url: clip, responses: [
		{ match: "/upload/v1beta/files", method: "POST", status: 200 },
	] },
	{ name: "local-gemini-api-upload-error", files, env: KEY, shims: localShims(), url: clip, responses: [
		{ match: "/upload/v1beta/files?upload_id", method: "PUT", status: 500, text: "x".repeat(300) }, upload[1],
	] },
	{ name: "local-gemini-api-no-file-in-reply", files, env: KEY, shims: localShims(), url: clip, responses: [
		{ match: "/upload/v1beta/files?upload_id", method: "PUT", json: {} }, upload[1],
	] },
	{ name: "local-gemini-api-generate-error", files, env: KEY, shims: localShims(), url: clip, responses: [
		...upload, { match: ":generateContent", status: 400, text: "bad" },
	] },
	{ name: "local-gemini-api-thumbnail-fails", files, env: KEY, shims: { ffmpeg: { stderr: "no\\n", exit: 1 } }, url: clip, responses: [...upload, answer("# No thumb")] },
	{ name: "local-gemini-api-image-disabled", files, env: KEY, config: { image: { enabled: false } }, url: clip, responses: [...upload, answer("# No thumb")] },
	{ name: "local-no-gemini", files, shims: localShims(), url: clip, responses: [] },
];

const quote = (s) => `'${s.replaceAll("'", `'\\''`)}'`;
function shimScript(spec) {
	let body = `#!/bin/sh\n{ printf 'ARGS'; for a in "$@"; do printf '\\037%s' "$a"; done; printf '\\n'; } >> "$SHIM_LOG"\ncase "$*" in\n`;
	const reply = (r) => `printf ${quote(r.stdout ?? "")}; printf '%s' ${quote((r.stderr ?? "").replaceAll("\\n", "\n"))} >&2; exit ${r.exit ?? 0}`;
	for (const r of spec.rules ?? []) body += `*${quote(r.contains)}*) ${reply(r)};;\n`;
	return body + `*) ${reply(spec)};;\nesac\n`;
}

const childPrelude = `
const responses = JSON.parse(process.env.RECORDER_RESPONSES);
const used = responses.map(() => 0);
const requests = [];
globalThis.fetch = async (url, init = {}) => {
	const method = init.method ?? "GET";
	const headers = {};
	for (const [k, v] of new Headers(init.headers)) headers[k] = v;
	let body = null;
	if (typeof init.body === "string") body = init.body;
	else if (init.body instanceof URLSearchParams) body = init.body.toString();
	else if (init.body) body = Buffer.from(init.body).toString("utf8");
	requests.push({ method, url: String(url), headers, body });
	const i = responses.findIndex((r, n) => String(url).includes(r.match) && (!r.method || r.method === method) && (r.times === undefined || used[n] < r.times));
	if (i < 0) throw new Error("Unexpected fetch: " + url);
	used[i]++;
	const hit = responses[i];
	if (hit.throw) throw new Error(hit.throw);
	const payload = hit.bytesBase64 !== undefined ? Buffer.from(hit.bytesBase64, "base64") : hit.json !== undefined ? JSON.stringify(hit.json) : (hit.text ?? "");
	return new Response(hit.status === 204 ? null : payload, { status: hit.status ?? 200, headers: hit.headers ?? {} });
};
`;

const sub = (v, root) => (typeof v === "string" ? v.replaceAll("<root>", root) : v);
const unroot = (v, root) => JSON.parse(JSON.stringify(v ?? null).replaceAll(JSON.stringify(root).slice(1, -1), "<root>"));

const out = { recorder: "port/record-video-requests.mjs", cases: {} };
for (const c of cases) {
	const root = mkdtempSync(join(tmpdir(), "video-wire-"));
	const bin = join(root, "bin");
	mkdirSync(bin);
	const scripts = {};
	for (const [name, spec] of Object.entries(c.shims ?? {})) {
		scripts[name] = shimScript({ ...spec, ...(c.shimsOverride?.[name] ?? {}) });
		writeFileSync(join(bin, name), scripts[name]);
		chmodSync(join(bin, name), 0o755);
	}
	if (c.rawConfig !== undefined) writeFileSync(join(root, "web-search.json"), c.rawConfig);
	else if (c.config) writeFileSync(join(root, "web-search.json"), JSON.stringify(c.config));
	for (const [name, content] of Object.entries(c.files ?? {})) writeFileSync(join(root, name), content);
	for (const [name, size] of Object.entries(c.sparse ?? {})) { writeFileSync(join(root, name), ""); truncateSync(join(root, name), size); }
	const log = join(root, "shim.log");
	writeFileSync(log, "");
	const env = { PATH: bin, HOME: root, USERPROFILE: root, PI_CODING_AGENT_DIR: root, SHIM_LOG: log, RECORDER_RESPONSES: JSON.stringify(c.responses), ...(c.env ?? {}) };
	const url = sub(c.url, root);
	const options = c.options ?? {};
	const script = `${childPrelude}
const { extractContent } = await import(${JSON.stringify(extractUrl)});
const lookup = async () => [{ address: "93.184.216.34", family: 4 }];
const result = await extractContent(${JSON.stringify(url)}, undefined, { lookup, ...${JSON.stringify(options)} });
await new Promise((resolve) => setTimeout(resolve, 100));
process.stdout.write(JSON.stringify({ requests, result }));
`;
	const child = spawnSync(process.execPath, ["--input-type=module"], { input: script, encoding: "utf8", env, maxBuffer: 16 * 1024 * 1024, timeout: 120000 });
	if (child.status !== 0) throw new Error(`${c.name}: ${child.stderr}`);
	const rec = JSON.parse(child.stdout);
	const commands = readFileSync(log, "utf8").split("\n").filter(Boolean).map((l) => l.split("\u001f").slice(1));
	out.cases[c.name] = {
		env: c.env ?? {},
		config: c.config ?? null,
		rawConfig: c.rawConfig ?? null,
		files: c.files ?? {},
		sparse: c.sparse ?? {},
		scripts,
		url: c.url,
		options,
		responses: c.responses,
		commands: unroot(commands, root),
		requests: unroot(rec.requests, root),
		result: unroot(rec.result, root),
	};
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
