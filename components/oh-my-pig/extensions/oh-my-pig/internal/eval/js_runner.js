// OMP JavaScript eval subprocess runner for oh-my-pig.
// Runs under Bun (preferred) or Node.js 22+ using only built-in node:* modules.
//
// Wire protocol (NDJSON):
//   On POSIX, the host passes dedicated pipe FDs via OMP_JS_CTRL_FD (read) and
//   OMP_JS_FRAME_FD (write) so fd 0 (/dev/null) and fd 1/2 (captured child
//   stdout/stderr) are isolated from the control/frame protocol.
//   On Windows (where Go's exec.Cmd.ExtraFiles is unavailable), falls back to
//   process.stdin / raw stdout with process.stdout.write & process.stderr.write
//   intercepted.
//
// Host -> runner:
//   {"type": "init", "cwd": str, "sessionId": str, "preludeSource": str, "localRoots": {}, "bridge": {"url": str, "token": str, "session": str}}
//   {"type": "run", "id": str, "code": str, "filename": str, "cwd": str, "localRoots": {}, "bridge": {...}, "preludes": [...]}
//   {"type": "exit"}
//
// Runner -> host:
//   {"type": "ready"}
//   {"type": "init_failed", "error": str}
//   {"type": "stdout", "id": str, "data": str}
//   {"type": "view", "id": str, "text": str, "view": {...}}
//   {"type": "image", "id": str, "image": {"mimeType": str, "data": str, ...}}
//   {"type": "status", "id": str, "event": {...}}
//   {"type": "done", "id": str, "ok": bool, "error"?: {"name": str, "message": str, "stack"?: str}}
//
// Ported from omp (https://github.com/can1357/oh-my-pi, MIT, v19.1.1):
//   packages/coding-agent/src/eval/js/shared/runtime.ts, helpers.ts, prelude.txt,
//   worker-core.ts; cell rewriter, indirectEval, awaitMaybePromise and
//   LocalModuleLoader are loaded verbatim from the generated bundle js_rewriter.cjs.

"use strict";

const { AsyncLocalStorage } = require("node:async_hooks");
const { Console } = require("node:console");
const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const nodeModule = require("node:module");
const { createRequire, isBuiltin } = nodeModule;
const path = require("node:path");
const readline = require("node:readline");
const { Writable } = require("node:stream");
const { fileURLToPath, pathToFileURL } = require("node:url");
const util = require("node:util");
const vm = require("node:vm");
// ---------------------------------------------------------------------------
// Protocol channels (dedicated FDs on POSIX; stdio fallback on Windows)
// ---------------------------------------------------------------------------

const ctrlFdEnv = process.env.OMP_JS_CTRL_FD;
const frameFdEnv = process.env.OMP_JS_FRAME_FD;
const ctrlFd = ctrlFdEnv && /^\d+$/.test(ctrlFdEnv) ? Number(ctrlFdEnv) : null;
const frameFd = frameFdEnv && /^\d+$/.test(frameFdEnv) ? Number(frameFdEnv) : null;

// Clean internal transport env vars so user code does not observe them.
delete process.env.OMP_JS_CTRL_FD;
delete process.env.OMP_JS_FRAME_FD;

const rawStdoutWrite = process.stdout.write.bind(process.stdout);
const rawStderrWrite = process.stderr.write.bind(process.stderr);

function emitFrame(frame) {
	const line = JSON.stringify(frame) + "\n";
	if (frameFd !== null) {
		fs.writeSync(frameFd, line);
	} else {
		rawStdoutWrite(line);
	}
}

// ---------------------------------------------------------------------------
// Parent-death watchdog (POSIX): kills this runner even if main thread is
// wedged in a synchronous loop when the host dies unexpectedly (SIGKILL).
// ---------------------------------------------------------------------------

if (process.platform !== "win32" && typeof process.ppid === "number" && process.ppid > 1) {
	try {
		const { Worker } = require("node:worker_threads");
		const origPpid = process.ppid;
		const targetPid = process.pid;
		const watchdog = new Worker(
			`const origPpid = ${origPpid};
const targetPid = ${targetPid};
setInterval(() => {
  try {
    if (process.ppid !== origPpid) process.kill(targetPid, "SIGKILL");
  } catch {}
}, 2000);`,
			{ eval: true },
		);
		watchdog.unref();
	} catch {
		// Worker threads unavailable; fall back to control-pipe close exit.
	}
}

const als = new AsyncLocalStorage();
const callSiteAls = new AsyncLocalStorage();

function activeRun() {
	return als.getStore();
}

function patchStdio() {
	for (const [stream, fallback] of [
		[process.stdout, rawStdoutWrite],
		[process.stderr, rawStderrWrite],
	]) {
		stream.write = function routedWrite(chunk, encoding, callback) {
			const run = activeRun();
			if (!run) {
				return fallback(chunk, encoding, callback);
			}
			const cb = typeof encoding === "function" ? encoding : callback;
			const enc = typeof encoding === "string" ? encoding : "utf8";
			const text =
				typeof chunk === "string"
					? chunk
					: chunk instanceof Uint8Array
						? Buffer.from(chunk).toString(enc)
						: String(chunk);
			emitFrame({ type: "stdout", id: run.runId, data: text });
			if (typeof cb === "function") cb();
			return true;
		};
	}
}
patchStdio();

// ---------------------------------------------------------------------------
// Image & view formatting (from omp js/shared/runtime.ts)
// ---------------------------------------------------------------------------

const BASE64_STRICT_RE = /^[A-Za-z0-9+/]+={0,2}$/;
const DECIMAL_CSV_RE = /^\d{1,3}(?:,\d{1,3})*$/;
const JSON_VIEW_MIN_CHARS = 80;
const JSON_TEXT_VIEW_MAX_CHARS = 32 * 1024;
const JSON_TEXT_START = /^\s*[[{]/;
const NUMERIC_CELL_RE = /^[-+]?(?:(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?|\d+n|Infinity)$|^NaN$/;

function isRecord(v) {
	return v !== null && typeof v === "object" && !Array.isArray(v);
}

function isStrictBase64(s) {
	return s.length > 0 && s.length % 4 === 0 && BASE64_STRICT_RE.test(s);
}

function coerceImageBase64(data) {
	if (typeof data === "string") {
		if (isStrictBase64(data)) return data;
		if (DECIMAL_CSV_RE.test(data)) {
			const parts = data.split(",");
			const bytes = new Uint8Array(parts.length);
			for (let i = 0; i < parts.length; i++) {
				const n = Number(parts[i]);
				if (!Number.isInteger(n) || n < 0 || n > 255) return null;
				bytes[i] = n;
			}
			return Buffer.from(bytes).toString("base64");
		}
		return null;
	}
	if (data instanceof Uint8Array || data instanceof ArrayBuffer) {
		return Buffer.from(data).toString("base64");
	}
	if (ArrayBuffer.isView(data)) {
		return Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString("base64");
	}
	if (data && typeof data === "object" && data.type === "Buffer" && Array.isArray(data.data)) {
		const bytes = new Uint8Array(data.data.length);
		for (let i = 0; i < data.data.length; i++) {
			const n = data.data[i];
			if (typeof n !== "number" || !Number.isInteger(n) || n < 0 || n > 255) return null;
			bytes[i] = n;
		}
		return Buffer.from(bytes).toString("base64");
	}
	return null;
}

function imageMetadata(obj) {
	const meta = {};
	if (!isRecord(obj)) return meta;
	if (obj.detail === "auto" || obj.detail === "low" || obj.detail === "high" || obj.detail === "original") {
		meta.detail = obj.detail;
	}
	if (typeof obj.url === "string") meta.url = obj.url;
	return meta;
}

function isPlainStructure(value) {
	if (Array.isArray(value)) return true;
	const proto = Object.getPrototypeOf(value);
	return proto === Object.prototype || proto === null;
}

function toJsonValue(value) {
	try {
		const raw = JSON.stringify(value, (_k, item) => (typeof item === "bigint" ? item.toString() : item));
		return raw === undefined ? undefined : JSON.parse(raw);
	} catch {
		return undefined;
	}
}

function jsonTextView(text) {
	if (text.length <= JSON_VIEW_MIN_CHARS && !text.includes("\n")) return undefined;
	if (text.length > JSON_TEXT_VIEW_MAX_CHARS || !JSON_TEXT_START.test(text)) return undefined;
	try {
		return { type: "view", text, view: { kind: "json", value: JSON.parse(text) } };
	} catch {
		return undefined;
	}
}

function formatConsoleArg(arg) {
	if (typeof arg === "string") {
		const output = jsonTextView(arg);
		return output ? { text: arg, output } : { text: arg };
	}
	if (isRecord(arg) && arg.type === "image" && typeof arg.mimeType === "string") {
		const data = coerceImageBase64(arg.data);
		if (data !== null) {
			return {
				text: `<image ${arg.mimeType}>`,
				output: { type: "image", image: { data, mimeType: arg.mimeType, ...imageMetadata(arg) } },
			};
		}
	}
	const text = util.inspect(arg, { depth: 6, colors: false, breakLength: 120 });
	if (arg !== null && typeof arg === "object" && isPlainStructure(arg) && (text.length > JSON_VIEW_MIN_CHARS || text.includes("\n"))) {
		const value = toJsonValue(arg);
		if (value !== undefined) {
			return { text, output: { type: "view", text, view: { kind: "json", value } } };
		}
	}
	return { text };
}

function logConsoleArgs(level, args, runId) {
	const pieces = args.map(formatConsoleArg);
	const prefix = level === "error" ? "[error] " : level === "warn" ? "[warn] " : "";
	const text = `${prefix}${pieces.map(p => p.text).join(" ")}`;
	emitFrame({ type: "stdout", id: runId, data: text.endsWith("\n") ? text : `${text}\n` });
	for (const piece of pieces) {
		if (!piece.output) continue;
		if (piece.output.type === "image") {
			emitFrame({ type: "image", id: runId, image: piece.output.image });
		} else if (piece.output.type === "view") {
			emitFrame({ type: "view", id: runId, text: piece.output.text, view: piece.output.view });
		}
	}
}

function tableRowCells(line) {
	if (line.length < 2 || !line.startsWith("│") || !line.endsWith("│")) return null;
	return line.slice(1, -1).split(" │ ").map(c => c.trim());
}

function unquoteCell(cell) {
	const quote = cell[0];
	if (cell.length < 2 || (quote !== "'" && quote !== '"' && quote !== "`") || cell.at(-1) !== quote) return cell;
	return cell.slice(1, -1).replaceAll(`\\${quote}`, quote);
}

function parseConsoleTable(rendered) {
	const lines = rendered.trimEnd().split("\n");
	if (lines.length < 4 || !lines[0].startsWith("┌") || !lines[2].startsWith("├") || !lines.at(-1)?.startsWith("└")) {
		return null;
	}
	const heads = tableRowCells(lines[1]);
	if (!heads) return null;
	const rows = [];
	for (const line of lines.slice(3, -1)) {
		const cells = tableRowCells(line);
		if (!cells || cells.length !== heads.length) return null;
		rows.push(cells);
	}
	if (rows.length === 0) return null;
	const columns = heads.map((head, idx) => {
		const col = { head: head === "(index)" || head === "(iteration index)" ? "" : head };
		let saw = false;
		let numeric = true;
		for (const row of rows) {
			const cell = row[idx];
			if (cell === "") continue;
			saw = true;
			if (!NUMERIC_CELL_RE.test(cell)) {
				numeric = false;
				break;
			}
		}
		if (saw && numeric) col.numeric = true;
		return col;
	});
	return { kind: "table", columns, rows: rows.map(row => row.map(unquoteCell)) };
}

function surfaceBridgedToolImages(value, runId) {
	if (!isRecord(value)) return value;
	const { images, ...rest } = value;
	if (!Array.isArray(images) || images.length === 0) return value;
	let displayed = 0;
	for (const image of images) {
		if (!isRecord(image)) continue;
		if (typeof image.data !== "string" || typeof image.mimeType !== "string") continue;
		emitFrame({ type: "image", id: runId, image: { data: image.data, mimeType: image.mimeType, ...imageMetadata(image) } });
		displayed++;
	}
	if (displayed === 0) return value;
	return { ...rest, images: `(${displayed} image${displayed === 1 ? "" : "s"} displayed)` };
}

// ---------------------------------------------------------------------------
// Helpers (from omp js/shared/helpers.ts, Node-compatible without requiring Bun.file)
// ---------------------------------------------------------------------------

const INTERNAL_URL_RE = /^([a-z][a-z0-9+.-]*):\/\/(.*)$/i;

function resolveUnderRoot(scheme, root, rawRelative, rawPath) {
	let relative;
	try {
		relative = decodeURIComponent(rawRelative.replaceAll("\\", "/"));
	} catch {
		throw new Error(`Invalid URL encoding in ${scheme}:// path: ${rawPath}`);
	}
	const rootPath = path.resolve(root);
	if (relative === "") return rootPath;
	if (path.isAbsolute(relative)) {
		throw new Error(`Absolute paths are not allowed in ${scheme}:// URLs: ${rawPath}`);
	}
	const normalized = path.normalize(relative);
	if (normalized.startsWith("..") || normalized.includes("/../") || normalized.includes("/..")) {
		throw new Error(`Path traversal (..) is not allowed in ${scheme}:// URLs: ${rawPath}`);
	}
	const resolved = path.resolve(rootPath, normalized);
	if (resolved !== rootPath && !resolved.startsWith(`${rootPath}${path.sep}`)) {
		throw new Error(`${scheme}:// path escapes its root: ${rawPath}`);
	}
	return resolved;
}

const runtimeState = {
	cwd: process.cwd(),
	sessionId: "",
	localRoots: {},
	bridge: null,
	envOverrides: new Map(),
	installedPreludes: new Map(),
	initialized: false,
};

function currentCwd() {
	return activeRun()?.cwd || runtimeState.cwd;
}

function currentLocalRoots() {
	return activeRun()?.localRoots || runtimeState.localRoots;
}

function resolveHelperPath(rawPath, op) {
	const match = INTERNAL_URL_RE.exec(rawPath);
	if (!match) {
		return path.isAbsolute(rawPath) ? path.normalize(rawPath) : path.resolve(currentCwd(), rawPath);
	}
	const scheme = match[1].toLowerCase();
	const root = currentLocalRoots()[scheme];
	if (!root) {
		throw new Error(`Protocol paths are not supported by ${op}(): ${rawPath}`);
	}
	return resolveUnderRoot(scheme, root, match[2], rawPath);
}

const helperBundle = {
	read: async (rawPath, options = {}) => {
		const filePath = resolveHelperPath(rawPath, "read");
		const stat = await fs.promises.stat(filePath);
		if (stat.isDirectory()) return undefined;
		let text = await fs.promises.readFile(filePath, "utf8");
		const offset = typeof options.offset === "number" ? options.offset : 1;
		const limit = typeof options.limit === "number" ? options.limit : undefined;
		if (offset > 1 || limit !== undefined) {
			const lines = text.split(/\r?\n/);
			const start = Math.max(0, offset - 1);
			const end = limit !== undefined ? start + limit : lines.length;
			text = lines.slice(start, end).join("\n");
		}
		const run = activeRun();
		if (run) {
			emitFrame({ type: "status", id: run.runId, event: { op: "read", path: filePath, bytes: stat.size, chars: text.length } });
		}
		return text;
	},
	writeFile: async (rawPath, data) => {
		const filePath = resolveHelperPath(rawPath, "write");
		await fs.promises.mkdir(path.dirname(filePath), { recursive: true });
		let buf;
		if (typeof data === "string") {
			buf = Buffer.from(data, "utf8");
		} else if (data instanceof ArrayBuffer) {
			buf = Buffer.from(data);
		} else if (ArrayBuffer.isView(data)) {
			buf = Buffer.from(data.buffer, data.byteOffset, data.byteLength);
		} else if (typeof Blob !== "undefined" && data instanceof Blob) {
			buf = Buffer.from(await data.arrayBuffer());
		} else {
			throw new Error("write() expects string, Blob, ArrayBuffer, or TypedArray data");
		}
		await fs.promises.writeFile(filePath, buf);
		const run = activeRun();
		if (run) {
			emitFrame({ type: "status", id: run.runId, event: { op: "write", path: filePath, bytes: buf.byteLength } });
		}
		return filePath;
	},
	env: (key, value) => {
		const run = activeRun();
		if (!key) {
			const merged = {};
			for (const [k, v] of Object.entries(process.env)) {
				if (typeof v === "string") merged[k] = v;
			}
			for (const [k, v] of runtimeState.envOverrides) merged[k] = v;
			const sorted = Object.fromEntries(Object.entries(merged).sort(([a], [b]) => a.localeCompare(b)));
			if (run) {
				emitFrame({ type: "status", id: run.runId, event: { op: "env", count: Object.keys(sorted).length, keys: Object.keys(sorted).slice(0, 20) } });
			}
			return sorted;
		}
		if (value !== undefined) {
			runtimeState.envOverrides.set(key, String(value));
			process.env[key] = String(value);
			if (run) {
				emitFrame({ type: "status", id: run.runId, event: { op: "env", key, value: String(value), action: "set" } });
			}
			return String(value);
		}
		const res = runtimeState.envOverrides.has(key) ? runtimeState.envOverrides.get(key) : process.env[key];
		if (run) {
			emitFrame({ type: "status", id: run.runId, event: { op: "env", key, value: res, action: "get" } });
		}
		return res;
	},
	hasRoot: rawPath => {
		const match = INTERNAL_URL_RE.exec(rawPath);
		return match !== null && Object.prototype.hasOwnProperty.call(currentLocalRoots(), match[1].toLowerCase());
	},
};

// ---------------------------------------------------------------------------
// Host HTTP loopback bridge call
// ---------------------------------------------------------------------------

function bridgePost(bridge, runId, name, args) {
	return new Promise((resolve, reject) => {
		if (!bridge || !bridge.url || !bridge.token || !bridge.session) {
			reject(new Error("tool bridge is unavailable in this kernel"));
			return;
		}
		const target = new URL("/v1/tool", bridge.url);
		const payload = Buffer.from(
			JSON.stringify({
				session: bridge.session,
				run: runId,
				name,
				args,
			}),
			"utf8",
		);
		const req = http.request(
			{
				hostname: target.hostname,
				port: target.port,
				path: target.pathname,
				method: "POST",
				headers: {
					"Content-Type": "application/json",
					"Content-Length": payload.byteLength,
					Authorization: `Bearer ${bridge.token}`,
				},
			},
			res => {
				const chunks = [];
				res.on("data", chunk => chunks.push(chunk));
				res.on("end", () => {
					const body = Buffer.concat(chunks).toString("utf8");
					let parsed;
					try {
						parsed = JSON.parse(body);
					} catch {
						reject(new Error(`bridge call ${JSON.stringify(name)}: non-JSON response: ${body.slice(0, 200)}`));
						return;
					}
					if (!parsed || !parsed.ok) {
						reject(new Error((parsed && parsed.error) || `bridge call ${JSON.stringify(name)} failed`));
						return;
					}
					resolve(parsed.value);
				});
			},
		);
		req.on("error", err => reject(err));
		req.write(payload);
		req.end();
	});
}

// ---------------------------------------------------------------------------
// Node.js compatibility shims for the two Bun-only APIs used by omp's
// rewrite-imports.ts (`Bun.stringWidth` and `Bun.Transpiler`).
// Passed ONLY as a lexical `Bun` parameter to the bundled omp rewriter via
// `vm.compileFunction` under Node (never published on `globalThis`), so user
// cells under Node still observe `typeof Bun === "undefined"`.
// ---------------------------------------------------------------------------
const COMBINING_MARK_RE = /\p{Mark}|\p{Format}/u;
const EMOJI_PRES_RE = /\p{Emoji_Presentation}/u;
const EXT_PICTO_RE = /\p{Extended_Pictographic}/u;
const RI_SINGLE_RE = /^\p{Regional_Indicator}$/u;
const RI_PAIR_RE = /^\p{Regional_Indicator}{2}$/u;
const graphemeSegmenter =
	typeof Intl !== "undefined" && typeof Intl.Segmenter === "function"
		? new Intl.Segmenter(undefined, { granularity: "grapheme" })
		: null;

function isZeroWidthCodePoint(cp) {
	if (cp <= 0x1f || (cp >= 0x7f && cp <= 0x9f)) return true;
	if (cp === 0x00ad || cp === 0x034f || cp === 0x061c || cp === 0x115f || cp === 0x1160) return true;
	if (cp >= 0x200b && cp <= 0x200f) return true;
	if (cp >= 0x202a && cp <= 0x202e) return true;
	if (cp >= 0x2060 && cp <= 0x206f) return true;
	if (cp === 0xfeff) return true;
	if (cp >= 0xfe00 && cp <= 0xfe0f) return true;
	if (cp >= 0xe0100 && cp <= 0xe01ef) return true;
	if (cp >= 0x1160 && cp <= 0x11ff) return true;
	if (cp >= 0xd7b0 && cp <= 0xd7ff) return true;
	return false;
}

function isFullWidthCodePoint(cp) {
	if (cp < 0x1100) return false;
	return (
		(cp >= 0x1100 && cp <= 0x115f) ||
		cp === 0x2329 ||
		cp === 0x232a ||
		(cp >= 0x2e80 && cp <= 0x303e) ||
		(cp >= 0x3040 && cp <= 0xa4cf) ||
		(cp >= 0xa960 && cp <= 0xa97c) ||
		(cp >= 0xac00 && cp <= 0xd7a3) ||
		(cp >= 0xf900 && cp <= 0xfaff) ||
		(cp >= 0xfe10 && cp <= 0xfe19) ||
		(cp >= 0xfe30 && cp <= 0xfe6f) ||
		(cp >= 0xff00 && cp <= 0xff60) ||
		(cp >= 0xffe0 && cp <= 0xffe6) ||
		(cp >= 0x16fe0 && cp <= 0x16fe4) ||
		(cp >= 0x16ff0 && cp <= 0x16ff1) ||
		(cp >= 0x17000 && cp <= 0x187f7) ||
		(cp >= 0x18800 && cp <= 0x18cd5) ||
		(cp >= 0x18d00 && cp <= 0x18d08) ||
		(cp >= 0x1aff0 && cp <= 0x1b2fb) ||
		(cp >= 0x1f200 && cp <= 0x1f251) ||
		(cp >= 0x20000 && cp <= 0x2fffd) ||
		(cp >= 0x30000 && cp <= 0x3fffd)
	);
}

function nodeStringWidth(str) {
	if (typeof str !== "string" || str.length === 0) return 0;
	const clean = typeof util.stripVTControlCharacters === "function" ? util.stripVTControlCharacters(str) : str;
	if (clean.length === 0) return 0;
	let width = 0;
	const segments = graphemeSegmenter ? graphemeSegmenter.segment(clean) : [...clean].map(segment => ({ segment }));
	for (const { segment } of segments) {
		const cp = segment.codePointAt(0);
		if (cp === undefined) continue;
		if (RI_SINGLE_RE.test(segment)) {
			width += 1;
			continue;
		}
		if (
			EMOJI_PRES_RE.test(segment) ||
			(EXT_PICTO_RE.test(segment) && (segment.includes("\ufe0f") || segment.includes("\u200d"))) ||
			segment.includes("\u20e3") ||
			RI_PAIR_RE.test(segment)
		) {
			width += 2;
			continue;
		}
		if (isZeroWidthCodePoint(cp)) continue;
		if (segment.length === 1 && COMBINING_MARK_RE.test(segment)) continue;
		if (cp > 0xffff && segment.length === 2 && COMBINING_MARK_RE.test(segment)) continue;
		width += isFullWidthCodePoint(cp) ? 2 : 1;
	}
	return width;
}

function suppressStripTypeScriptWarning() {
	if (typeof process.emitWarning !== "function") return;
	const origEmitWarning = process.emitWarning;
	process.emitWarning = function filteredEmitWarning(warning, ...args) {
		const msg = typeof warning === "string" ? warning : warning && typeof warning.message === "string" ? warning.message : "";
		if (msg.includes("stripTypeScriptTypes")) return;
		return origEmitWarning.call(this, warning, ...args);
	};
}

class NodeTypeScriptTranspiler {
	constructor(options) {
		this.loader = (options && options.loader) || "ts";
	}

	transformSync(code) {
		if (typeof nodeModule.stripTypeScriptTypes !== "function") {
			// Older Node without module.stripTypeScriptTypes: leave source unstripped.
			return code;
		}
		try {
			return nodeModule.stripTypeScriptTypes(code, { mode: "transform" });
		} catch (directError) {
			// Node's stripTypeScriptTypes rejects top-level `return` outside a function;
			// retry wrapped in an async function body so cells with top-level `return` + TS
			// syntax still strip cleanly. If that also fails (e.g. mixed import + return or
			// genuine syntax error), rethrow so omp's stripTypeScript returns code unchanged.
			try {
				const wrapped = `async function __omp_ts_strip__() {\n${code}\n}`;
				const stripped = nodeModule.stripTypeScriptTypes(wrapped, { mode: "transform" });
				return stripped.replace(/^async function __omp_ts_strip__\(\)\s*\{\r?\n?/, "").replace(/\r?\n?\}\s*$/, "");
			} catch {
				throw directError;
			}
		}
	}
}

const hasNativeBun = typeof globalThis.Bun === "object" && globalThis.Bun !== null;
const nodeBunShim = hasNativeBun
	? null
	: {
			stringWidth: nodeStringWidth,
			Transpiler: NodeTypeScriptTranspiler,
		};
if (!hasNativeBun) {
	suppressStripTypeScriptWarning();
}

// ---------------------------------------------------------------------------
// Bundled omp cell rewriter (`js_rewriter.cjs`) loader
// ---------------------------------------------------------------------------

let rewriterBundle = null;

function loadRewriterBundle(explicitPath) {
	if (rewriterBundle) return rewriterBundle;
	const candidate = explicitPath || path.join(__dirname, "js_rewriter.cjs");
	if (hasNativeBun) {
		rewriterBundle = require(candidate);
		return rewriterBundle;
	}
	// Load the CJS bundle with `Bun` injected as a private parameter so the
	// rewriter resolves `Bun.stringWidth` and `Bun.Transpiler` without exposing
	// a partial `globalThis.Bun` to user cells.
	const bundlePath = path.resolve(candidate);
	const source = fs.readFileSync(bundlePath, "utf8");
	const mod = { exports: {} };
	const bundleRequire = createRequire(bundlePath);
	const compiled = vm.compileFunction(
		source,
		["exports", "require", "module", "__filename", "__dirname", "Bun"],
		{ filename: bundlePath },
	);
	compiled(mod.exports, bundleRequire, mod, bundlePath, path.dirname(bundlePath), nodeBunShim);
	rewriterBundle = mod.exports;
	return rewriterBundle;
}
// Compiles `source` as a global script without running it (matches indirect eval semantics).
// upstream: packages/coding-agent/src/eval/js/shared/runtime.ts:282-289
function compiles(source) {
	try {
		new vm.Script(source);
		return true;
	} catch {
		return false;
	}
}

// ---------------------------------------------------------------------------
// Local module resolution & require helpers
// Under Bun: uses omp's bundled LocalModuleLoader (supports vm.SourceTextModule,
// .ts/.tsx stripping, mtime cache invalidation, and cycle handling).
// Under Node: tracks local module dependency graphs via the bundle's
// `collectModuleSourceSpecifiers`, bumps every importer transitively (cycle-safe)
// when any dependency's mtime changes, and applies `?omp-v=N` to transitive ESM
// imports via `module.registerHooks` when available.
// ---------------------------------------------------------------------------

let localModuleLoader = null;
const nodeRequireCache = new Map();
const nodeModuleMtimes = new Map();
const nodeModuleVersions = new Map();
const nodeModuleDeps = new Map(); // canonicalPath -> Set<canonicalPath>
const nodeModuleParents = new Map(); // canonicalPath -> Set<canonicalPath>
const LOCAL_TRY_EXTENSIONS = ["", ".ts", ".mts", ".tsx", ".js", ".mjs", ".cjs", ".json"];
const LOCAL_MANAGED_EXTS = new Set([".js", ".jsx", ".mjs", ".ts", ".tsx", ".mts"]);
let nodeResolveHookInstalled = false;

function canonicalLocalPath(filePath) {
	try {
		return fs.realpathSync(filePath);
	} catch {
		return path.resolve(filePath);
	}
}

function installNodeModuleVersionHook() {
	if (hasNativeBun || nodeResolveHookInstalled) return;
	if (typeof nodeModule.registerHooks !== "function") return;
	nodeModule.registerHooks({
		resolve(specifier, context, nextResolve) {
			const res = nextResolve(specifier, context);
			if (res && typeof res.url === "string" && res.url.startsWith("file://")) {
				try {
					const u = new URL(res.url);
					if (!u.searchParams.has("omp-v")) {
						const fp = canonicalLocalPath(fileURLToPath(u));
						const version = nodeModuleVersions.get(fp);
						if (version && version > 1) {
							u.searchParams.set("omp-v", String(version));
							return { ...res, url: u.href };
						}
					}
				} catch {}
			}
			return res;
		},
	});
	nodeResolveHookInstalled = true;
}

function isLocalPathSpecifier(source) {
	return (
		source.startsWith("./") ||
		source.startsWith("../") ||
		source === "." ||
		source === ".." ||
		source.startsWith("/") ||
		source.startsWith("~/") ||
		/^[a-zA-Z]:[\\/]/.test(source)
	);
}

function filenameForUrl(moduleUrlOrPath) {
	if (!moduleUrlOrPath) return null;
	if (moduleUrlOrPath.startsWith("file://")) return fileURLToPath(moduleUrlOrPath);
	return path.isAbsolute(moduleUrlOrPath) ? moduleUrlOrPath : null;
}

function activeFilename() {
	const run = activeRun();
	const fn = run && run.filename;
	return fn && path.isAbsolute(fn) ? fn : undefined;
}

function activeRequire(moduleUrlOrPath) {
	if (localModuleLoader) {
		return localModuleLoader.requireForFile(moduleUrlOrPath ?? activeFilename(), currentCwd());
	}
	const basePath = filenameForUrl(moduleUrlOrPath ?? activeFilename()) ?? path.join(currentCwd(), "[eval]");
	let cached = nodeRequireCache.get(basePath);
	if (!cached) {
		cached = createRequire(pathToFileURL(basePath).href);
		nodeRequireCache.set(basePath, cached);
	}
	return cached;
}

function buildDynamicRequire() {
	const dynamicRequire = id => activeRequire()(id);
	const resolve = (id, options) => activeRequire().resolve(id, options);
	resolve.paths = request => activeRequire().resolve.paths(request);
	Object.defineProperties(dynamicRequire, {
		resolve: { value: resolve, configurable: true },
		cache: { get: () => activeRequire().cache, configurable: true },
		extensions: { get: () => activeRequire().extensions, configurable: true },
		main: { get: () => activeRequire().main, configurable: true },
	});
	return dynamicRequire;
}

function resolveNodeLocalPath(baseDir, source) {
	const base = path.isAbsolute(source) ? path.normalize(source) : path.resolve(baseDir, source);
	for (const ext of LOCAL_TRY_EXTENSIONS) {
		const candidate = base + ext;
		try {
			const st = fs.statSync(candidate);
			if (st.isFile()) return canonicalLocalPath(candidate);
		} catch {}
	}
	try {
		if (fs.statSync(base).isDirectory()) {
			for (const ext of [".ts", ".mts", ".js", ".mjs", ".cjs"]) {
				const idx = path.join(base, "index" + ext);
				if (fs.existsSync(idx)) return canonicalLocalPath(idx);
			}
		}
	} catch {}
	return canonicalLocalPath(base);
}

function setNodeModuleDependencies(modulePath, deps) {
	const previous = nodeModuleDeps.get(modulePath);
	if (previous) {
		for (const dep of previous) {
			const parents = nodeModuleParents.get(dep);
			if (!parents) continue;
			parents.delete(modulePath);
			if (parents.size === 0) nodeModuleParents.delete(dep);
		}
	}
	nodeModuleDeps.set(modulePath, new Set(deps));
	for (const dep of deps) {
		let parents = nodeModuleParents.get(dep);
		if (!parents) {
			parents = new Set();
			nodeModuleParents.set(dep, parents);
		}
		parents.add(modulePath);
	}
}

function bumpNodeModuleAndParents(modulePath, seen) {
	if (seen.has(modulePath)) return;
	seen.add(modulePath);
	nodeModuleVersions.set(modulePath, (nodeModuleVersions.get(modulePath) || 1) + 1);
	const parents = nodeModuleParents.get(modulePath);
	if (parents) {
		for (const parent of [...parents]) {
			bumpNodeModuleAndParents(parent, seen);
		}
	}
}

function refreshTrackedNodeModules() {
	const changed = [];
	for (const [modulePath, prevMtime] of nodeModuleMtimes.entries()) {
		let nextMtime;
		try {
			nextMtime = fs.statSync(modulePath).mtimeMs;
		} catch {
			nextMtime = undefined;
		}
		if (nextMtime === prevMtime) continue;
		if (nextMtime === undefined) nodeModuleMtimes.delete(modulePath);
		else nodeModuleMtimes.set(modulePath, nextMtime);
		changed.push(modulePath);
	}
	if (changed.length > 0) {
		const seen = new Set();
		for (const modulePath of changed) {
			bumpNodeModuleAndParents(modulePath, seen);
		}
	}
	return changed;
}

async function trackNodeModuleGraph(rootPath, visited = new Set()) {
	if (visited.has(rootPath)) return;
	visited.add(rootPath);
	if (rootPath.includes(`${path.sep}node_modules${path.sep}`)) return;
	let stat;
	try {
		stat = fs.statSync(rootPath);
	} catch {
		return;
	}
	if (!stat.isFile()) return;
	nodeModuleMtimes.set(rootPath, stat.mtimeMs);
	if (!nodeModuleVersions.has(rootPath)) nodeModuleVersions.set(rootPath, 1);

	const ext = path.extname(rootPath);
	if (!LOCAL_MANAGED_EXTS.has(ext)) {
		setNodeModuleDependencies(rootPath, new Set());
		return;
	}
	const rewriter = loadRewriterBundle();
	if (typeof rewriter.collectModuleSourceSpecifiers !== "function") return;
	let raw;
	try {
		raw = fs.readFileSync(rootPath, "utf8");
	} catch {
		return;
	}
	const specifiers = await rewriter.collectModuleSourceSpecifiers(raw);
	const moduleDir = path.dirname(rootPath);
	const localDeps = new Set();
	for (const spec of specifiers) {
		if (!isLocalPathSpecifier(spec)) continue;
		const depPath = resolveNodeLocalPath(moduleDir, spec);
		if (!depPath.includes(`${path.sep}node_modules${path.sep}`)) {
			localDeps.add(depPath);
		}
	}
	setNodeModuleDependencies(rootPath, localDeps);
	for (const depPath of localDeps) {
		await trackNodeModuleGraph(depPath, visited);
	}
}

async function resolveAndImport(baseDir, source, options) {
	if (localModuleLoader) {
		const resolved = await localModuleLoader.resolveForRun(baseDir, source);
		if (resolved.mode === "local") return resolved.value;
		return options !== undefined ? await import(resolved.target, options) : await import(resolved.target);
	}
	let target = source;
	if (typeof source === "string" && !/^[a-z][a-z0-9+.-]*:/i.test(source) && !isBuiltin(source)) {
		if (isLocalPathSpecifier(source)) {
			installNodeModuleVersionHook();
			refreshTrackedNodeModules();
			const resolvedPath = resolveNodeLocalPath(baseDir, source);
			await trackNodeModuleGraph(resolvedPath);
			const version = nodeModuleVersions.get(resolvedPath) || 1;
			const fileUrl = pathToFileURL(resolvedPath).href;
			target = version > 1 ? `${fileUrl}?omp-v=${version}` : fileUrl;
		} else {
			try {
				const req = createRequire(pathToFileURL(path.join(baseDir, "[eval]")).href);
				const resolvedPath = req.resolve(source);
				if (path.isAbsolute(resolvedPath)) {
					target = pathToFileURL(resolvedPath).href;
				}
			} catch {}
		}
	}
	return options !== undefined ? await import(target, options) : await import(target);
}
// ---------------------------------------------------------------------------
// Initialize runtime and execute omp's prelude.txt
// upstream: packages/coding-agent/src/eval/js/shared/runtime.ts:649-764
// ---------------------------------------------------------------------------

function initRuntime(msg) {
	const rewriter = loadRewriterBundle(msg.rewriterPath);

	runtimeState.cwd = msg.cwd || process.cwd();
	runtimeState.sessionId = msg.sessionId || "";
	runtimeState.localRoots = msg.localRoots || {};
	runtimeState.bridge = msg.bridge || null;
	try {
		if (runtimeState.cwd && process.cwd() !== runtimeState.cwd) {
			process.chdir(runtimeState.cwd);
		}
	} catch {}

	if (hasNativeBun && typeof vm.SourceTextModule === "function" && typeof rewriter.LocalModuleLoader === "function") {
		localModuleLoader = new rewriter.LocalModuleLoader(runtimeState.sessionId || "default");
	}

	const sessionObj = { cwd: runtimeState.cwd, sessionId: runtimeState.sessionId };
	runtimeState.sessionObj = sessionObj;

	const injected = {
		__omp_session__: sessionObj,
		__omp_helpers__: helperBundle,
		__omp_with_call_site__: (siteId, action) => {
			const run = activeRun();
			if (!run) return action();
			const occurrence = run.callOccurrences.get(siteId) ?? 0;
			run.callOccurrences.set(siteId, occurrence + 1);
			return callSiteAls.run({ siteId, occurrence }, action);
		},
		__omp_call_tool__: async (name, args) => {
			const run = activeRun();
			if (!run) throw new Error(`bridge call ${JSON.stringify(name)} ran outside an executing cell`);
			const val = await bridgePost(run.bridge || runtimeState.bridge, run.runId, name, args);
			return surfaceBridgedToolImages(val, run.runId);
		},
		__omp_prelude__: async (name, parameters) => {
			const run = activeRun();
			if (!run) throw new Error(`prelude call ${JSON.stringify(name)} ran outside an executing cell`);
			const val = await bridgePost(run.bridge || runtimeState.bridge, run.runId, "__prelude__", {
				name,
				parameters,
			});
			return surfaceBridgedToolImages(val, run.runId);
		},
		__omp_import__: async (source, options) => {
			const fn = activeFilename();
			const baseDir = fn ? path.dirname(fn) : currentCwd();
			return await resolveAndImport(baseDir, source, options);
		},
		__omp_import_from__: async (moduleUrl, source, options) => {
			if (localModuleLoader) {
				const resolved = await localModuleLoader.resolveForModule(moduleUrl, source, currentCwd());
				if (resolved.mode === "local") return resolved.value;
				return options !== undefined ? await import(resolved.target, options) : await import(resolved.target);
			}
			const modPath = filenameForUrl(moduleUrl);
			const baseDir = modPath ? path.dirname(modPath) : currentCwd();
			return await resolveAndImport(baseDir, source, options);
		},
		__omp_get_require__: moduleUrl => activeRequire(moduleUrl),
		__omp_get_filename__: moduleUrl => {
			if (localModuleLoader) {
				return localModuleLoader.filenameForUrl(moduleUrl ?? activeFilename()) ?? path.join(currentCwd(), "[eval]");
			}
			return filenameForUrl(moduleUrl ?? activeFilename()) ?? path.join(currentCwd(), "[eval]");
		},
		__omp_get_dirname__: moduleUrl => {
			if (localModuleLoader) {
				return localModuleLoader.dirnameForUrl(moduleUrl ?? activeFilename(), currentCwd());
			}
			const fn = filenameForUrl(moduleUrl ?? activeFilename());
			return fn ? path.dirname(fn) : currentCwd();
		},
		__omp_emit_status__: (op, data = {}) => {
			const run = activeRun();
			if (run) {
				emitFrame({ type: "status", id: run.runId, event: { op, ...data } });
			}
		},
		__omp_log__: (level, ...args) => {
			const run = activeRun();
			if (run) {
				logConsoleArgs(level, args, run.runId);
			}
		},
		__omp_table__: (...args) => {
			const run = activeRun();
			if (!run) return;
			let buffer = "";
			const stream = new Writable({
				write(chunk, _enc, cb) {
					buffer += typeof chunk === "string" ? chunk : chunk.toString("utf8");
					cb();
				},
			});
			const tableConsole = new Console({ stdout: stream, colorMode: false });
			tableConsole.table(...args);
			emitFrame({ type: "stdout", id: run.runId, data: buffer.endsWith("\n") ? buffer : `${buffer}\n` });
			const table = parseConsoleTable(buffer);
			if (table) {
				emitFrame({ type: "view", id: run.runId, text: buffer, view: table });
			}
		},
		__omp_set_final_expr__: value => {
			const run = activeRun();
			if (!run) return;
			run.finalExpressionSet = true;
			run.finalExpressionValue = value;
		},
		webcrypto: crypto,
		require: buildDynamicRequire(),
		createRequire,
		fs,
	};

	Object.assign(globalThis, injected);
	if (msg.preludeSource) {
		rewriter.indirectEval(msg.preludeSource, "[omp-js-prelude]");
	}
	runtimeState.initialized = true;
}

function syncPreludes(preludes) {
	const rewriter = loadRewriterBundle();
	const desired = new Map();
	for (const p of preludes || []) {
		desired.set(p.name, p);
	}
	for (const [name, installed] of runtimeState.installedPreludes) {
		const next = desired.get(name);
		if (
			next &&
			next.source === installed.source &&
			JSON.stringify(next.exports) === JSON.stringify(installed.exports)
		) {
			continue;
		}
		for (const exp of installed.exports || []) {
			delete globalThis[exp];
		}
		runtimeState.installedPreludes.delete(name);
	}
	for (const [name, p] of desired) {
		if (runtimeState.installedPreludes.has(name)) continue;
		rewriter.indirectEval(p.source, `[eval-prelude:${name}]`);
		runtimeState.installedPreludes.set(name, p);
	}
}

// Execute a single cell using omp's exact pipeline:
//   wrapCode -> indirectEval -> SyntaxError && !compiles diagnosis ->
//   finalExpression / asyncWrapped handling -> echo formatting.
// upstream: packages/coding-agent/src/eval/js/shared/runtime.ts:540-599,
//           packages/coding-agent/src/eval/js/worker-core.ts:398-406
async function runCell(msg) {
	const rewriter = loadRewriterBundle();
	const runId = msg.id;
	const cwd = msg.cwd || runtimeState.cwd;
	runtimeState.cwd = cwd;
	if (runtimeState.sessionObj) runtimeState.sessionObj.cwd = cwd;
	if (msg.localRoots) runtimeState.localRoots = msg.localRoots;
	if (msg.bridge) runtimeState.bridge = msg.bridge;
	try {
		if (cwd && process.cwd() !== cwd) process.chdir(cwd);
	} catch {}

	const filename = msg.filename || `[eval:${runId}]`;
	const runCtx = {
		runId,
		cwd,
		filename,
		localRoots: msg.localRoots || runtimeState.localRoots,
		bridge: msg.bridge || runtimeState.bridge,
		finalExpressionSet: false,
		finalExpressionValue: undefined,
		callOccurrences: new Map(),
	};

	try {
		if (Array.isArray(msg.preludes)) {
			syncPreludes(msg.preludes);
		}
		const code = msg.code || "";
		const value = await als.run(runCtx, async () => {
			const wrapped = await rewriter.wrapCode(code);
			let evalValue;
			try {
				evalValue = rewriter.indirectEval(wrapped.source, filename);
			} catch (error) {
				if (error instanceof SyntaxError && !compiles(wrapped.source)) {
					throw (await rewriter.diagnoseCellSyntaxError(code)) ?? error;
				}
				throw error;
			}
			if (wrapped.finalExpressionReturned) {
				const awaited = await rewriter.awaitMaybePromise(evalValue);
				if (runCtx.finalExpressionSet) {
					const finalValue = runCtx.finalExpressionValue;
					runCtx.finalExpressionSet = false;
					runCtx.finalExpressionValue = undefined;
					const settled = await rewriter.awaitMaybePromise(finalValue);
					return wrapped.finalExpressionSuppressed ? undefined : settled;
				}
				return awaited;
			}
			if (wrapped.finalExpressionReturned === null) {
				return await rewriter.awaitMaybePromise(evalValue);
			}
			if (wrapped.asyncWrapped) {
				return await rewriter.awaitMaybePromise(evalValue);
			}
			await rewriter.awaitMaybePromise(evalValue);
			return undefined;
		});
		if (value !== undefined) {
			logConsoleArgs("log", [value], runId);
		}
		emitFrame({ type: "done", id: runId, ok: true });
	} catch (err) {
		const errPayload =
			err instanceof Error
				? { name: err.name, message: err.message, stack: err.stack }
				: { name: "Error", message: String(err) };
		emitFrame({ type: "done", id: runId, ok: false, error: errPayload });
	}
}

process.on("unhandledRejection", reason => {
	const run = activeRun();
	const msg = reason instanceof Error ? reason.stack || reason.message : String(reason);
	if (run) {
		emitFrame({ type: "stdout", id: run.runId, data: `[unhandled rejection] ${msg}\n` });
	}
});

// ---------------------------------------------------------------------------
// NDJSON control-channel dispatch loop
// ---------------------------------------------------------------------------

const ctrlInput = ctrlFd !== null ? fs.createReadStream(null, { fd: ctrlFd }) : process.stdin;
const rl = readline.createInterface({ input: ctrlInput, crlfDelay: Infinity });

// Dispatch queued messages directly rather than chaining `chain.then(async () => await runCell(msg))`
// so the NDJSON transport loop does not add an extra async wrapper frame beneath `runCell` in error stacks.
const pendingMessages = [];
let processingQueue = false;

function dispatchNext() {
	if (processingQueue || pendingMessages.length === 0) return;
	processingQueue = true;
	const msg = pendingMessages.shift();
	if (msg.type === "init") {
		try {
			initRuntime(msg);
			emitFrame({ type: "ready" });
		} catch (err) {
			emitFrame({
				type: "init_failed",
				error: err instanceof Error ? err.message : String(err),
			});
		}
		processingQueue = false;
		dispatchNext();
		return;
	}
	if (msg.type === "run") {
		const done = () => {
			processingQueue = false;
			dispatchNext();
		};
		runCell(msg).then(done, done);
		return;
	}
	processingQueue = false;
	dispatchNext();
}

rl.on("line", rawLine => {
	const line = rawLine.trim();
	if (!line) return;
	let msg;
	try {
		msg = JSON.parse(line);
	} catch (err) {
		emitFrame({ type: "done", id: "", ok: false, error: { name: "SyntaxError", message: String(err) } });
		return;
	}
	if (msg.type === "exit") {
		rl.close();
		process.exit(0);
	}
	pendingMessages.push(msg);
	dispatchNext();
});

rl.on("close", () => {
	process.exit(0);
});
